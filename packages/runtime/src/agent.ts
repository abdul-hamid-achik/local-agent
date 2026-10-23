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
const SEARXNG_URL = "http://127.0.0.1:8888";
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
        "Search the web through the local SearXNG instance and return the top results (title, url, snippet). Use it before fetch when you do not know which page has the answer.",
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
];
/** Tools that only inspect; plan intent offers these and nothing else. */
const READ_ONLY_TOOLS = new Set([
  "read",
  "list",
  "search",
  "fetch",
  "web_search",
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
          const base = (
            process.env.LOCAL_AGENT_SEARXNG_URL ?? SEARXNG_URL
          ).replace(/\/+$/, "");
          const url = `${base}/search?q=${encodeURIComponent(args.query)}&format=json`;
          let searchResponse: Response;
          try {
            searchResponse = await fetch(url, {
              signal: AbortSignal.timeout(SEARXNG_TIMEOUT_MS),
            });
          } catch (error) {
            throw new Error(
              `web_search: SearXNG at ${base} is unreachable (${error instanceof Error ? error.message : "error"}). Run a local instance, e.g. docker run -d -p 8888:8080 searxng/searxng, and enable the JSON format (search.formats in settings.yml). Set LOCAL_AGENT_SEARXNG_URL to override the address.`,
            );
          }
          if (searchResponse.status !== 200)
            throw new Error(
              `web_search: SearXNG returned HTTP ${searchResponse.status} (the JSON format may not be enabled: search.formats in settings.yml)`,
            );
          const payload = (await searchResponse.json()) as {
            results?: { title?: string; url?: string; content?: string }[];
          };
          const results = (payload.results ?? []).slice(0, 8);
          output = results.length
            ? results
                .map((result, index) =>
                  `${index + 1}. ${result.title ?? result.url ?? "(untitled)"}\n   ${result.url ?? ""}\n   ${result.content ?? ""}`.trimEnd(),
                )
                .join("\n\n")
            : "No results";
          if (output.length > 100_000)
            throw new Error("web_search output exceeds tool output limit");
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
