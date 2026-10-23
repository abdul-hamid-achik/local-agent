import { createHash, randomUUID } from "node:crypto";
import type { Tool } from "ollama";
import { z } from "zod";
import { admitOllamaModel } from "#runtime/admission";
import {
  type ApprovedCommandProposal,
  createCommandProposal,
  executeApprovedCommand,
  parseArgv,
} from "#runtime/command";
import type { Provider } from "#runtime/config";
import { postEditDiagnostics } from "#runtime/diagnostics";
import {
  chat,
  type Message,
  MessageSchema,
  type RunEvent,
  type ToolCall,
} from "#runtime/index";
import { hostInstructions, projectInstructions } from "#runtime/instructions";
import type { McpConnection } from "#runtime/mcp";
import type { ExecutionEvent, Session, SessionStore } from "#runtime/session";
import type {
  Workspace,
  WorkspaceEditProposal,
  WorkspaceWriteProposal,
} from "#runtime/workspace";

const ReadArgs = z.strictObject({ path: z.string().min(1).max(4096) });
const EditArgs = ReadArgs.extend({
  old_string: z.string().min(1).max(100_000),
  new_string: z.string().max(100_000),
});
const WriteArgs = ReadArgs.extend({ content: z.string().max(1_000_000) });
// Small models send null or "" for an argument they leave out.
const optionalText = (max: number) =>
  z
    .string()
    .max(max)
    .nullish()
    .transform((value) => value || undefined);
const flag = z
  .union([
    z.boolean(),
    z.enum(["true", "false"]).transform((value) => value === "true"),
  ])
  .nullish();
// The read-only tools ignore keys they do not know (`recursive`, ...) instead of failing the call.
const ListArgs = z.object({ path: optionalText(4096) });
const SearchArgs = z.object({
  pattern: z.string().min(1).max(500),
  path: optionalText(4096),
  regex: flag,
  ignore_case: flag,
});
const FetchArgs = z.object({ url: z.string().min(1).max(8192) });
const WebSearchArgs = z.object({ query: z.string().min(1).max(500) });
const CommandArgs = z.strictObject({
  // An array, or (from a small model) a command line that is split without a shell.
  argv: z.union([
    z.array(z.string().max(100_000)).min(1).max(256),
    z.string().min(1).max(100_000),
  ]),
  timeout_ms: z.number().int().min(1).max(600_000).default(120_000),
});
const TodoItem = z.object({
  content: z.string().min(1).max(200),
  status: z.enum(["pending", "in_progress", "done"]),
});
// The list replaces the previous one entirely; at most one item is active.
const TodoWriteArgs = z.object({
  todos: z
    .array(TodoItem)
    .max(50)
    .refine(
      (todos) =>
        todos.filter((todo) => todo.status === "in_progress").length <= 1,
      "At most one todo may be in_progress; mark the previous one done or pending first",
    ),
});

const PATH_HELP =
  "relative to the workspace root, for example src/stats.ts (never /src/stats.ts)";
const pathProperty = (extra = "") => ({
  type: "string",
  description: `Path ${PATH_HELP}${extra}`,
});

const FETCH_TIMEOUT_MS = 15_000;
const FETCH_MAX_BYTES = 512 * 1024;
// Docs URLs redirect constantly (http→https, trailing-slash moves); following
// them is friendlier than the MCP policy of refusing redirects, but a manual
// loop keeps the hop count bounded.
const FETCH_MAX_REDIRECTS = 5;
const SEARXNG_TIMEOUT_MS = 10_000;

/** GET with redirects followed, capped at FETCH_MAX_REDIRECTS hops. */
async function httpGet(
  url: string,
  timeoutMs: number,
): Promise<{ response: Response; finalUrl: string }> {
  let current = url;
  for (let hop = 0; ; hop++) {
    const response = await fetch(current, {
      redirect: "manual",
      signal: AbortSignal.timeout(timeoutMs),
    });
    const location =
      response.status >= 300 && response.status < 400
        ? response.headers.get("location")
        : null;
    if (location === null) return { response, finalUrl: current };
    if (hop >= FETCH_MAX_REDIRECTS) {
      await response.body?.cancel();
      throw new Error(`fetch: more than ${FETCH_MAX_REDIRECTS} redirects`);
    }
    current = new URL(location, current).toString();
  }
}

/** Reads at most maxBytes of the body as text. */
async function readCapped(
  response: Response,
  maxBytes: number,
): Promise<{ text: string; truncated: boolean }> {
  const reader = response.body?.getReader();
  if (reader === undefined) return { text: "", truncated: false };
  const chunks: Uint8Array[] = [];
  let kept = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (kept + value.length > maxBytes) {
      chunks.push(value.slice(0, maxBytes - kept));
      await reader.cancel();
      return {
        text: await new Blob(chunks as BlobPart[]).text(),
        truncated: true,
      };
    }
    chunks.push(value);
    kept += value.length;
  }
  return {
    text: await new Blob(chunks as BlobPart[]).text(),
    truncated: false,
  };
}

const HTML_BLOCK_TAGS: Record<string, true> = {
  address: true,
  article: true,
  aside: true,
  blockquote: true,
  br: true,
  dd: true,
  div: true,
  dl: true,
  dt: true,
  h1: true,
  h2: true,
  h3: true,
  h4: true,
  h5: true,
  h6: true,
  header: true,
  li: true,
  main: true,
  nav: true,
  ol: true,
  p: true,
  pre: true,
  section: true,
  table: true,
  tr: true,
  ul: true,
};

/** HTML to readable plain text: script/style dropped, blocks newline-separated. */
async function htmlToText(html: string): Promise<string> {
  const stripped = await new HTMLRewriter()
    .on("script,style,noscript,template,svg", {
      element: (el) => {
        el.remove();
      },
    })
    .transform(new Response(html))
    .text();
  const parts: string[] = [];
  await new HTMLRewriter()
    .on("*", {
      element: (el) => {
        if (HTML_BLOCK_TAGS[el.tagName]) parts.push("\n");
      },
    })
    .onDocument({
      text: (t) => {
        parts.push(t.text);
      },
    })
    .transform(new Response(stripped))
    .text();
  return parts
    .join("")
    .replace(/[ \t]+/g, " ")
    .replace(/\n\s*\n+/g, "\n\n")
    .trim();
}

function textContentType(response: Response): string {
  return (response.headers.get("content-type") ?? "")
    .split(";")[0]!
    .trim()
    .toLowerCase();
}

type SearchHit = { title: string; url: string; snippet: string };

/** Query SearXNG's JSON API; null means "unavailable, try the next provider". */
async function searxngSearch(
  base: string,
  query: string,
): Promise<SearchHit[] | null> {
  let response: Response;
  try {
    response = await fetch(
      `${base}/search?q=${encodeURIComponent(query)}&format=json`,
      { signal: AbortSignal.timeout(SEARXNG_TIMEOUT_MS) },
    );
  } catch {
    return null;
  }
  if (response.status !== 200) return null;
  try {
    const payload = (await response.json()) as {
      results?: { title?: string; url?: string; content?: string }[];
    };
    return (payload.results ?? []).map((result) => ({
      title: result.title ?? result.url ?? "(untitled)",
      url: result.url ?? "",
      snippet: result.content ?? "",
    }));
  } catch {
    return null;
  }
}

/**
 * Query DuckDuckGo Lite and parse its results table. Result links point at
 * /l/?uddg=<urlencoded target>; the snippet sits in the next result-snippet
 * cell. Null means "no usable answer" (block, captcha, zero results).
 */
async function duckduckgoSearch(query: string): Promise<SearchHit[] | null> {
  let response: Response;
  try {
    response = await fetch(
      `https://lite.duckduckgo.com/lite/?q=${encodeURIComponent(query)}`,
      {
        headers: {
          "user-agent":
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
          accept: "text/html",
        },
        signal: AbortSignal.timeout(SEARXNG_TIMEOUT_MS),
      },
    );
  } catch {
    return null;
  }
  if (response.status !== 200) return null;
  const html = await response.text();
  const hits: SearchHit[] = [];
  let pending: { title: string; url: string } | undefined;
  await new HTMLRewriter()
    .on("a", {
      element: (el) => {
        const href = el.getAttribute("href");
        if (!href?.includes("uddg=")) return;
        const target = new URL(
          href,
          "https://lite.duckduckgo.com",
        ).searchParams.get("uddg");
        if (!target) return;
        pending = { title: "", url: target };
        el.onEndTag(() => {
          if (pending) hits.push({ ...pending, snippet: "" });
          pending = undefined;
        });
      },
      text: (chunk) => {
        if (pending) pending.title += chunk.text;
      },
    })
    .on("td.result-snippet", {
      text: (chunk) => {
        const hit = hits[hits.length - 1];
        if (hit) hit.snippet += chunk.text;
      },
    })
    .transform(new Response(html))
    .text();
  return hits.length
    ? hits.map((hit) => ({
        ...hit,
        title: decodeEntities(hit.title),
        snippet: decodeEntities(hit.snippet),
      }))
    : null;
}

/** Decode the handful of HTML entities DuckDuckGo titles/snippets contain. */
function decodeEntities(text: string): string {
  return text
    .replace(/&#(\d+);/g, (_, code) => String.fromCodePoint(Number(code)))
    .replace(/&#x([0-9a-f]+);/gi, (_, code) =>
      String.fromCodePoint(parseInt(code, 16)),
    )
    .replace(
      /&(amp|lt|gt|quot|apos|nbsp);/g,
      (_, name) =>
        ({
          amp: "&",
          lt: "<",
          gt: ">",
          quot: '"',
          apos: "'",
          nbsp: " ",
        })[name as "amp"]!,
    );
}

const IMAGE_MAX_BYTES = 5 * 1024 * 1024;
const PNG_SIGNATURE = Buffer.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
]);

type ImageKind = {
  format: string;
  /** Content is checked by magic bytes; a renamed text file never passes. */
  matches: (bytes: Buffer) => boolean;
  dimensions: (bytes: Buffer) => { width: number; height: number };
};

const IMAGE_KINDS: readonly ImageKind[] = [
  {
    format: "PNG",
    matches: (bytes) =>
      bytes.length >= 24 &&
      bytes.subarray(0, 8).equals(PNG_SIGNATURE) &&
      bytes.subarray(12, 16).toString("latin1") === "IHDR",
    dimensions: (bytes) => ({
      width: bytes.readUInt32BE(16),
      height: bytes.readUInt32BE(20),
    }),
  },
  {
    format: "JPEG",
    matches: (bytes) =>
      bytes.length >= 4 &&
      bytes[0] === 0xff &&
      bytes[1] === 0xd8 &&
      bytes[2] === 0xff,
    dimensions: jpegDimensions,
  },
  {
    format: "GIF",
    matches: (bytes) => {
      const header = bytes.subarray(0, 6).toString("latin1");
      return bytes.length >= 10 && (header === "GIF87a" || header === "GIF89a");
    },
    dimensions: (bytes) => ({
      width: bytes.readUInt16LE(6),
      height: bytes.readUInt16LE(8),
    }),
  },
  {
    format: "WebP",
    matches: (bytes) =>
      bytes.length >= 30 &&
      bytes.subarray(0, 4).toString("latin1") === "RIFF" &&
      bytes.subarray(8, 12).toString("latin1") === "WEBP",
    dimensions: webpDimensions,
  },
];

/** Walks the JPEG markers to the first frame header (SOFn), before the scan data. */
function jpegDimensions(bytes: Buffer): { width: number; height: number } {
  let offset = 2;
  while (offset + 9 < bytes.length) {
    if (bytes[offset] !== 0xff)
      throw new Error("read_image: malformed JPEG marker structure");
    const marker = bytes[offset + 1] ?? 0;
    // Standalone markers carry no length field.
    if (
      marker === 0xd8 ||
      marker === 0x01 ||
      (marker >= 0xd0 && marker <= 0xd7)
    ) {
      offset += 2;
      continue;
    }
    if (marker === 0xd9 || marker === 0xda) break; // end of image or start of scan: no frame header was found
    const length = bytes.readUInt16BE(offset + 2);
    const frame =
      marker >= 0xc0 &&
      marker <= 0xcf &&
      marker !== 0xc4 &&
      marker !== 0xc8 &&
      marker !== 0xcc;
    if (frame)
      return {
        height: bytes.readUInt16BE(offset + 5),
        width: bytes.readUInt16BE(offset + 7),
      };
    offset += 2 + length;
  }
  throw new Error("read_image: JPEG has no frame header before its image data");
}

/** Reads the canvas size from whichever WebP chunk carries it. */
function webpDimensions(bytes: Buffer): { width: number; height: number } {
  const chunk = bytes.subarray(12, 16).toString("latin1");
  if (chunk === "VP8X")
    return {
      width:
        1 +
        ((bytes[24] ?? 0) | ((bytes[25] ?? 0) << 8) | ((bytes[26] ?? 0) << 16)),
      height:
        1 +
        ((bytes[27] ?? 0) | ((bytes[28] ?? 0) << 8) | ((bytes[29] ?? 0) << 16)),
    };
  if (chunk === "VP8L") {
    const bits = bytes.readUInt32LE(21);
    return {
      width: (bits & 0x3fff) + 1,
      height: ((bits >> 14) & 0x3fff) + 1,
    };
  }
  if (chunk === "VP8 ") {
    return {
      width: bytes.readUInt16LE(26) & 0x3fff,
      height: bytes.readUInt16LE(28) & 0x3fff,
    };
  }
  throw new Error(`read_image: unsupported WebP chunk ${chunk || "(none)"}`);
}

/**
 * Reads one workspace image through the same pinned path authority as `read`,
 * checks its content (magic bytes, not the extension) and returns Ollama's
 * vision encoding: base64 with no data URI prefix.
 */
async function readWorkspaceImage(
  workspace: Workspace,
  path: string,
): Promise<{ base64: string; width: number; height: number; format: string }> {
  const bytes = await workspace.readBytes(path, IMAGE_MAX_BYTES);
  const kind = IMAGE_KINDS.find((entry) => entry.matches(bytes));
  if (!kind)
    throw new Error(
      `read_image: ${path} is not a PNG, JPEG, WebP or GIF image (the file content is checked, not its extension)`,
    );
  const { width, height } = kind.dimensions(bytes);
  if (
    !Number.isSafeInteger(width) ||
    !Number.isSafeInteger(height) ||
    !width ||
    !height
  )
    throw new Error(`read_image: ${path} has no usable dimensions`);
  return {
    base64: bytes.toString("base64"),
    width,
    height,
    format: kind.format,
  };
}

const tools: Tool[] = [
  {
    type: "function",
    function: {
      name: "read",
      description: `Read an existing UTF-8 text file. The path is ${PATH_HELP}.`,
      parameters: {
        type: "object",
        properties: { path: pathProperty() },
        required: ["path"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "read_image",
      description: `Read one image file so you can see it; the image is attached to this tool's result. PNG, JPEG, WebP and GIF, at most 5 MiB, checked by content not extension. Requires a vision model. The path is ${PATH_HELP}.`,
      parameters: {
        type: "object",
        properties: { path: pathProperty() },
        required: ["path"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "list",
      description:
        "List the entries of a directory (directories end with /). .git and node_modules are skipped. Omit path for the workspace root.",
      parameters: {
        type: "object",
        properties: {
          path: {
            type: "string",
            description:
              "Directory relative to the workspace root, for example src",
          },
        },
      },
    },
  },
  {
    type: "function",
    function: {
      name: "search",
      description:
        "Search the text files under a path for a literal string (or a regular expression with regex: true). Returns path:line: text for each match; .git, node_modules and binary files are skipped.",
      parameters: {
        type: "object",
        properties: {
          pattern: {
            type: "string",
            description: "Text to find, for example slugify",
          },
          path: {
            type: "string",
            description:
              "File or directory relative to the workspace root, for example src; omit to search everything",
          },
          regex: {
            type: "boolean",
            description: "Treat pattern as a regular expression",
          },
          ignore_case: { type: "boolean", description: "Ignore letter case" },
        },
        required: ["pattern"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "fetch",
      description:
        "Fetch one http(s) URL with GET and return readable text: HTML is converted to plain text, other text formats (plain text, JSON, XML) are returned as-is. Redirects are followed; the body is capped at 512 KiB. Use it for documentation pages and raw files, not for APIs that need other methods or auth.",
      parameters: {
        type: "object",
        properties: {
          url: { type: "string", description: "Absolute http(s) URL" },
        },
        required: ["url"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "web_search",
      description:
        "Search the web and return the top results (title, url, snippet). Uses SearXNG when LOCAL_AGENT_SEARXNG_URL is configured, otherwise DuckDuckGo Lite; no API keys required. Use it before fetch when you do not know which page has the answer.",
      parameters: {
        type: "object",
        properties: {
          query: { type: "string", description: "Search query" },
        },
        required: ["query"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "edit",
      description: `Replace one exact occurrence in an existing workspace file. The path is ${PATH_HELP}. The host requests approval before applying it.`,
      parameters: {
        type: "object",
        properties: {
          path: pathProperty(),
          old_string: {
            type: "string",
            description: "Exact text to replace; it must occur once",
          },
          new_string: { type: "string" },
        },
        required: ["path", "old_string", "new_string"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "write",
      description: `Create a new file, or replace a whole existing one, with the given content. The path is ${PATH_HELP}; missing directories are created. The host requests approval before applying it. Prefer edit for a small change to an existing file.`,
      parameters: {
        type: "object",
        properties: {
          path: pathProperty(),
          content: {
            type: "string",
            description: "The complete new content of the file",
          },
        },
        required: ["path", "content"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "run_command",
      description:
        'Run one program in the workspace after host approval, for example {"argv": ["bun", "test", "tests/stats.test.ts"]} or {"argv": ["grep", "-rn", "slugify", "src"]}. argv is an array: the program, then each argument as its own element. There is no shell: no pipes, &&, ;, redirects or $(...), and no globbing; run one command per call. Processes have user permissions; this is not an OS sandbox. Report exit code and actual output, not assumed verification success.',
      parameters: {
        type: "object",
        properties: {
          argv: {
            type: "array",
            items: { type: "string" },
            description: 'For example ["bun", "test"]',
          },
          timeout_ms: { type: "integer" },
        },
        required: ["argv"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "todo_write",
      description:
        "Replace the current todo list (send the full list every call; it overwrites the previous one). Each item is a short task with status pending, in_progress or done; keep at most one item in_progress and update statuses as work proceeds.",
      parameters: {
        type: "object",
        properties: {
          todos: {
            type: "array",
            items: {
              type: "object",
              properties: {
                content: {
                  type: "string",
                  description: "Short task, 1-200 chars",
                },
                status: {
                  type: "string",
                  enum: ["pending", "in_progress", "done"],
                },
              },
              required: ["content", "status"],
            },
          },
        },
        required: ["todos"],
      },
    },
  },
];
/** Tools with no external effects; plan intent offers these and nothing else. */
const READ_ONLY_TOOLS = new Set([
  "read",
  "read_image",
  "list",
  "search",
  "fetch",
  "web_search",
  "todo_write",
]);

export type ApprovalRequest =
  | WorkspaceEditProposal
  | WorkspaceWriteProposal
  | ApprovedCommandProposal
  | Readonly<{
      kind: "mcp";
      name: string;
      argumentsJson: string;
    }>;

export type AgentEvent =
  | RunEvent
  | { type: "message"; message: Message }
  | { type: "tool_requested"; id: string; name: string }
  | { type: "tool_start"; id: string; name: string }
  | {
      type: "tool_result";
      id: string;
      name: string;
      status:
        | "succeeded"
        | "denied"
        | "failed"
        | "outcome_unknown"
        | "unverified";
      output: string;
    }
  | { type: "turn_complete"; messages: Message[] };

export type SessionContext = { store: SessionStore; state: Session };

export type AgentOptions = {
  workspace: Workspace;
  /** Plan denies writes even if the approval callback would allow them. */
  intent?: "plan" | "build";
  approve: (proposal: ApprovalRequest, signal: AbortSignal) => Promise<boolean>;
  signal: AbortSignal;
  session?: SessionContext;
  mcp?: readonly McpConnection[];
};

/** Serial tool loop. A complete provider response is required before any host action. */
export async function* runAgent(
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
): AsyncGenerator<AgentEvent> {
  const saved = options.session;
  if (saved && saved.state.workspaceId !== options.workspace.root)
    throw new Error("Session belongs to another workspace");
  const release = saved
    ? await saved.store.acquireLease(saved.state.id, options.workspace.root)
    : undefined;
  try {
    if (saved) {
      const current = await saved.store.loadSession(
        saved.state.publicId,
        options.workspace.root,
      );
      if (current.revision !== saved.state.revision)
        throw new Error("Session changed in another client");
      saved.state = current;
      if (saved.store.listHazards(current.id, current.workspaceId).length)
        throw new Error(
          "Session has an uncertain tool outcome; inspect recovery before continuing",
        );
      let pending: ToolCall[] = [];
      for (const message of current.messages) {
        if (message.role !== "assistant" && message.tool_calls?.length)
          throw new Error(
            "Session contains invalid tool calls; recovery is required",
          );
        if (message.role === "tool") {
          const expected = pending.shift();
          if (
            !expected ||
            message.tool_name !== expected.function.name ||
            message.tool_call_id !== expected.id
          )
            throw new Error(
              "Session contains unmatched tool results; recovery is required",
            );
        } else {
          if (pending.length)
            throw new Error(
              "Session contains interrupted tool calls; recovery is required",
            );
          if (message.tool_calls?.length) {
            pending = message.tool_calls.slice();
          }
        }
      }
      if (pending.length)
        throw new Error(
          "Session contains interrupted tool calls; recovery is required",
        );
    }
    yield* agentTurn(provider, messages, options);
  } finally {
    await release?.();
  }
}

async function* agentTurn(
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
): AsyncGenerator<AgentEvent> {
  const { signal, workspace } = options;
  const mcpTools = new Map<string, McpConnection>();
  const availableTools = [...tools];
  for (const connection of options.mcp ?? []) {
    for (const tool of connection.tools) {
      if (availableTools.some((entry) => entry.function.name === tool.name))
        throw new Error(`Duplicate tool name: ${tool.name}`);
      mcpTools.set(tool.name, connection);
      availableTools.push({
        type: "function",
        function: {
          name: tool.name,
          description: tool.description ?? "MCP tool; host approval required",
          parameters: tool.inputSchema as Tool["function"]["parameters"],
        },
      });
    }
  }
  if (availableTools.length > 256)
    throw new Error("Tool catalog exceeds 256 tools");
  const saved = options.session;
  const runId = randomUUID();
  const canonicalCallIds = new Set<string>();
  const history = z.array(MessageSchema).min(1).max(1000).parse(messages);
  if (
    saved &&
    JSON.stringify(history.slice(0, saved.state.messages.length)) !==
      JSON.stringify(saved.state.messages)
  )
    throw new Error("Session history differs from its durable state");
  const checkpoint = () => {
    if (saved)
      saved.state = saved.store.saveStateCAS(
        saved.state.id,
        saved.state.workspaceId,
        saved.state.revision,
        history,
      );
  };
  checkpoint();
  const instructions = [
    hostInstructions(options.intent ?? "build"),
    await projectInstructions(workspace),
  ]
    .filter(Boolean)
    .join("\n\n");
  const admission = await admitOllamaModel(provider, signal);
  signal.throwIfAborted();
  if (admission.status !== "validated")
    throw new Error(
      admission.status === "rejected" && admission.code === "not_installed"
        ? admission.message
        : `Model admission ${admission.status}: ${admission.message}`,
    );
  if (!admission.capabilities.includes("tools"))
    throw new Error("Agent requires a model with tools capability");
  for (let step = 0; step < 20; step++) {
    signal.throwIfAborted();
    const assistant: Message = { role: "assistant", content: "" };
    const calls: ToolCall[] = [];
    for await (const event of chat(
      provider,
      [{ role: "system", content: instructions }, ...history],
      signal,
      options.intent === "plan"
        ? tools.filter((tool) => READ_ONLY_TOOLS.has(tool.function.name ?? ""))
        : availableTools,
    )) {
      if (event.type === "text") assistant.content += event.text;
      if (event.type === "thinking")
        assistant.thinking = (assistant.thinking ?? "") + event.text;
      if (event.type === "tool_call") calls.push(event.call);
      yield event;
    }
    const providerCallIds = calls.map((call) => call.id ?? "");
    for (const call of calls) {
      call.id ??= randomUUID();
      if (canonicalCallIds.has(call.id))
        throw new Error("Provider repeated a tool call identity");
      canonicalCallIds.add(call.id);
    }
    if (calls.length) assistant.tool_calls = calls;
    history.push(assistant);
    checkpoint();
    yield { type: "message", message: assistant };
    if (!calls.length) {
      yield { type: "turn_complete", messages: history };
      return;
    }
    // read_image results ride the tool message itself (Ollama's Message type
    // is role-agnostic), so they reach the next request with their tool call.
    const pendingImages: string[] = [];
    for (const [callIndex, call] of calls.entries()) {
      const name = call.function.name;
      const mcpConnection = mcpTools.get(name);
      const id = randomUUID();
      const execution: Omit<ExecutionEvent, "type"> | undefined = saved
        ? {
            sessionId: saved.state.id,
            workspaceId: saved.state.workspaceId,
            runId,
            turnId: runId,
            executionId: id,
            idempotencyKey: id,
            canonicalCallId: call.id ?? id,
            providerCallId: providerCallIds[callIndex] ?? "",
            iteration: step + 1,
            ordinal: callIndex + 1,
            kind: mcpConnection ? "mcp" : "builtin",
            toolName: name,
            effectClass:
              READ_ONLY_TOOLS.has(name) && !mcpConnection
                ? "read_only"
                : mcpConnection
                  ? "unknown"
                  : "effectful",
            argumentsSha256: createHash("sha256")
              .update(JSON.stringify(call.function.arguments))
              .digest("hex"),
          }
        : undefined;
      const record = (type: ExecutionEvent["type"]) => {
        if (saved && execution) saved.store.appendEvent({ ...execution, type });
      };
      record("requested");
      yield { type: "tool_requested", id, name };
      let status:
        | "succeeded"
        | "denied"
        | "failed"
        | "outcome_unknown"
        | "unverified" = "succeeded";
      let effectAttempted = false;
      let uncertain = false;
      let output: string;
      try {
        signal.throwIfAborted();
        if (name === "read") {
          const args = ReadArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          output = await workspace.read(args.path);
          if (output.length > 100_000)
            throw new Error("File exceeds tool output limit");
        } else if (name === "read_image") {
          const args = ReadArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          // Admission exposes the model's capabilities; a text-only model
          // cannot use an image, so fail fast instead of shipping base64
          // it would ignore.
          if (!admission.capabilities.includes("vision"))
            throw new Error(
              `read_image: model ${admission.model} has no vision capability; switch to a multimodal model such as llava, llama3.2-vision, qwen2.5vl or minicpm-v`,
            );
          const image = await readWorkspaceImage(workspace, args.path);
          pendingImages.push(image.base64);
          output = `image ${image.width}x${image.height} ${image.format} from ${args.path} (attached)`;
        } else if (name === "list") {
          const args = ListArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          output = await workspace.list(args.path);
        } else if (name === "search") {
          const args = SearchArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          output = await workspace.search(args.pattern, {
            path: args.path,
            regex: args.regex ?? false,
            ignoreCase: args.ignore_case ?? false,
          });
          if (output.length > 100_000)
            throw new Error("Search output exceeds tool output limit");
        } else if (name === "fetch") {
          const args = FetchArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          const target = new URL(args.url);
          if (target.protocol !== "http:" && target.protocol !== "https:")
            throw new Error(`fetch: unsupported protocol ${target.protocol}`);
          const { response, finalUrl } = await httpGet(
            target.toString(),
            FETCH_TIMEOUT_MS,
          );
          if (response.status >= 400)
            throw new Error(`fetch: HTTP ${response.status} for ${finalUrl}`);
          const contentType = textContentType(response);
          const textual =
            contentType.startsWith("text/") ||
            contentType === "application/json" ||
            contentType === "application/xml" ||
            contentType.endsWith("+json") ||
            contentType.endsWith("+xml");
          if (!textual)
            throw new Error(
              `fetch: unsupported content type ${contentType || "(none)"}`,
            );
          const { text, truncated } = await readCapped(
            response,
            FETCH_MAX_BYTES,
          );
          const body =
            contentType === "text/html" || contentType.endsWith("+html")
              ? await htmlToText(text)
              : text;
          // Keep the final tool output inside the shared 100k-char limit;
          // the header then reports the bytes actually returned.
          const truncationNote = "…(output truncated at 100k chars)";
          const headerOf = (shown: string) =>
            `${Buffer.byteLength(shown, "utf8")} bytes from ${finalUrl}${truncated ? " (truncated at 512 KiB)" : ""}\n\n`;
          const room = 100_000 - headerOf(body).length - truncationNote.length;
          output =
            body.length > room
              ? (() => {
                  const shown = body.slice(0, Math.max(0, room));
                  return headerOf(shown) + shown + truncationNote;
                })()
              : headerOf(body) + body;
        } else if (name === "web_search") {
          const args = WebSearchArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          const searxngBase = process.env.LOCAL_AGENT_SEARXNG_URL?.replace(
            /\/+$/,
            "",
          );
          let hits: SearchHit[] | null = null;
          let provider: "searxng" | "duckduckgo" = "duckduckgo";
          if (searxngBase) {
            hits = await searxngSearch(searxngBase, args.query);
            if (hits) provider = "searxng";
          }
          if (!hits) hits = await duckduckgoSearch(args.query);
          if (!hits)
            throw new Error(
              `web_search: no search provider answered${
                searxngBase ? ` (SearXNG at ${searxngBase} failed too)` : ""
              }. For a stable backend, run a SearXNG instance and set LOCAL_AGENT_SEARXNG_URL to its address.`,
            );
          const top = hits.slice(0, 8);
          const list = top.length
            ? top
                .map((hit, index) =>
                  `${index + 1}. ${hit.title}\n   ${hit.url}\n   ${hit.snippet}`.trimEnd(),
                )
                .join("\n\n")
            : "No results";
          output = `${list}\n\nvia ${provider}`;
          if (output.length > 100_000)
            throw new Error("web_search output exceeds tool output limit");
        } else if (name === "todo_write") {
          const args = TodoWriteArgs.parse(call.function.arguments);
          record("started");
          yield { type: "tool_start", id, name };
          signal.throwIfAborted();
          const markers = { pending: "[ ]", in_progress: "[~]", done: "[x]" };
          const counts = { pending: 0, in_progress: 0, done: 0 };
          const lines = args.todos.map(
            (todo) => `- ${markers[todo.status]} ${todo.content}`,
          );
          for (const todo of args.todos) counts[todo.status]++;
          output =
            [
              ...lines,
              `${args.todos.length} todos: ${counts.done} done, ${counts.in_progress} in progress, ${counts.pending} pending`,
            ].join("\n") || "0 todos: 0 done, 0 in progress, 0 pending";
        } else if (name === "write") {
          if (options.intent === "plan") {
            status = "denied";
            output = "Plan mode does not permit writes";
          } else {
            const args = WriteArgs.parse(call.function.arguments);
            const proposal = await workspace.prepareWrite(
              args.path,
              args.content,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output = "Write denied by host";
            } else {
              record("approved");
              record("started");
              yield { type: "tool_start", id, name };
              signal.throwIfAborted();
              effectAttempted = true;
              await workspace.applyWrite(proposal);
              output = `${proposal.created ? "Created" : "Replaced"} ${proposal.path}. Verification has not been run.`;
              output += await postEditDiagnostics(
                [proposal.path],
                workspace.root,
                signal,
              );
            }
          }
        } else if (name === "edit") {
          if (options.intent === "plan") {
            status = "denied";
            output = "Plan mode does not permit edits";
          } else {
            const args = EditArgs.parse(call.function.arguments);
            const proposal = await workspace.prepareEdit(
              args.path,
              args.old_string,
              args.new_string,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output = "Edit denied by host";
            } else {
              record("approved");
              record("started");
              yield { type: "tool_start", id, name };
              signal.throwIfAborted();
              effectAttempted = true;
              await workspace.applyEdit(proposal);
              output = `Updated ${proposal.path}. Verification has not been run.`;
              output += await postEditDiagnostics(
                [proposal.path],
                workspace.root,
                signal,
              );
            }
          }
        } else if (name === "run_command") {
          if (options.intent === "plan") {
            status = "denied";
            output = "Plan mode does not permit command execution";
          } else {
            const args = CommandArgs.parse(call.function.arguments);
            const proposal = await createCommandProposal(
              parseArgv(args.argv),
              workspace.root,
              args.timeout_ms,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output = "Command denied by host";
            } else {
              record("approved");
              record("started");
              yield { type: "tool_start", id, name };
              signal.throwIfAborted();
              effectAttempted = true;
              const result = await executeApprovedCommand(proposal, signal);
              uncertain =
                result.timedOut || result.cancelled || result.cleanupIncomplete;
              status =
                result.exitCode === 0 && !result.timedOut && !result.cancelled
                  ? "succeeded"
                  : "failed";
              output = JSON.stringify({
                ...result,
                output: result.output.slice(0, 100_000),
                truncated: result.truncated || result.output.length > 100_000,
              });
            }
          }
        } else if (mcpConnection) {
          if (options.intent === "plan") {
            status = "denied";
            output =
              "Plan mode does not authorize MCP calls from server annotations";
          } else {
            const argumentsJson = JSON.stringify(call.function.arguments);
            if (Buffer.byteLength(argumentsJson) > 100_000)
              throw new Error("MCP arguments exceed 100000 bytes");
            const proposal = Object.freeze({
              kind: "mcp" as const,
              name,
              argumentsJson,
            });
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output = "MCP call denied by host";
            } else {
              record("approved");
              record("started");
              yield { type: "tool_start", id, name };
              signal.throwIfAborted();
              effectAttempted = true;
              const result = await mcpConnection.callTool(
                name,
                JSON.parse(argumentsJson),
                signal,
              );
              status = result.isError ? "failed" : "unverified";
              output = JSON.stringify(result);
            }
          }
        } else {
          throw new Error(`Unknown tool: ${name}`);
        }
      } catch (error) {
        uncertain = effectAttempted;
        status = "failed";
        output = signal.aborted
          ? "Cancelled; tool did not report successful completion"
          : mcpConnection && effectAttempted
            ? "MCP call did not produce a usable result; its effects are unknown"
            : error instanceof Error
              ? error.message
              : "Tool failed";
      }
      if (uncertain) status = "outcome_unknown";
      const result = { status, output };
      const message: Message = {
        role: "tool",
        tool_name: name,
        tool_call_id: call.id,
        content: JSON.stringify(result),
      };
      if (pendingImages.length) message.images = pendingImages.splice(0);
      history.push(message);
      if (saved && execution) {
        saved.state = saved.store.commitOutcome(
          {
            ...execution,
            type: uncertain
              ? "outcome_unknown"
              : status === "succeeded" || status === "unverified"
                ? "completed"
                : status === "denied"
                  ? "denied"
                  : signal.aborted
                    ? "cancelled"
                    : "failed",
            resultSha256: createHash("sha256")
              .update(message.content)
              .digest("hex"),
            resultReceipt: message.content,
          },
          saved.state.revision,
          history,
        );
      }
      yield { type: "message", message };
      yield { type: "tool_result", id, name, ...result };
      if (uncertain)
        throw new Error(
          "Tool outcome is uncertain; review the workspace before continuing",
        );
    }
    signal.throwIfAborted();
  }
  throw new Error("Agent reached the 20-step limit");
}
