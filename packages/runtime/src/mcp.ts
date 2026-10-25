import { closeSync, constants, existsSync, openSync, readSync } from "node:fs";
import { open } from "node:fs/promises";
import { join } from "node:path";
import {
  type CallToolResult,
  Client,
  StreamableHTTPClientTransport,
  type Tool,
} from "@modelcontextprotocol/client";
import {
  DEFAULT_INHERITED_ENV_VARS,
  StdioClientTransport,
} from "@modelcontextprotocol/client/stdio";
import { z } from "zod";
import { LIMITS } from "#runtime/limits";
import { userConfigDir } from "#runtime/paths";

const MAX_TOOLS = LIMITS.mcpToolsPerServer;
const MAX_TEXT = LIMITS.mcpTextChars;
/** Connecting and listing tools: a gateway fans out to its servers first. */
const CONNECT_TIMEOUT_MS = 15_000;
/** One tool call, unless the server's entry sets `timeoutMs`. */
const CALL_TIMEOUT_MS = 60_000;
/** How much of a stdio server's stderr is kept, from its end. */
const STDERR_TAIL = 4_096;

/**
 * A call to a server whose connection has closed (the process exited, or
 * the client was closed): it was not sent, so its outcome is known.
 */
export class McpStoppedError extends Error {}

/** A stdio server that did not start, with the last line it wrote to stderr. */
export class McpStartError extends Error {
  constructor(
    readonly reason: string,
    readonly said: string,
    options?: ErrorOptions,
  ) {
    super(`${reason}; it said: ${said}`, options);
  }
}

/**
 * The last line a server wrote to stderr, for the person: one line, no
 * control characters, at most 160 characters. Never sent to the model.
 */
export function lastStderrLine(text: string): string | undefined {
  const line = Bun.stripANSI(text)
    .split(/\r?\n/)
    // biome-ignore lint/suspicious/noControlCharactersInRegex: remove control characters from a server's stderr
    .map((part) => part.replace(/[\u0000-\u001f\u007f-\u009f]/g, "").trim())
    .filter(Boolean)
    .at(-1);
  return line ? line.slice(0, 160) : undefined;
}

export type McpServerConfig = {
  name: string;
  /** Per-call timeout for this server's tools (1 s to 10 min). */
  timeoutMs?: number;
  transport:
    | {
        kind: "stdio";
        command: string;
        args?: string[];
        env?: Record<string, string>;
        cwd?: string;
      }
    | { kind: "http"; url: string; allowRemote?: boolean };
};

const McpStdioConfigSchema = z.strictObject({
  kind: z.literal("stdio"),
  command: z
    .string()
    .min(1)
    .max(4096)
    .refine((value) => !value.includes("\0")),
  args: z
    .array(
      z
        .string()
        .max(4096)
        .refine((value) => !value.includes("\0")),
    )
    .max(256)
    .optional(),
  env: z
    .record(z.string(), z.string().max(16_384))
    .refine((value) =>
      Object.entries(value).every(
        ([key, item]) => !key.includes("\0") && !item.includes("\0"),
      ),
    )
    .optional(),
  cwd: z
    .string()
    .min(1)
    .max(4096)
    .refine((value) => !value.includes("\0"))
    .optional(),
});
const McpHttpConfigSchema = z.strictObject({
  kind: z.literal("http"),
  url: z.string().url(),
  allowRemote: z.boolean().optional(),
});
export const McpServerConfigSchema = z.strictObject({
  name: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
  timeoutMs: z.number().int().min(1_000).max(600_000).optional(),
  transport: z.discriminatedUnion("kind", [
    McpStdioConfigSchema,
    McpHttpConfigSchema,
  ]),
});

export type McpTool = {
  name: string;
  description?: string;
  inputSchema: Record<string, unknown>;
  readOnly: boolean;
};

/**
 * The input schema the model is offered. MCP lets a tool that takes no
 * arguments leave out `properties` (`{"type": "object"}`), and llama.cpp's
 * chat templates, which Ollama runs for several models, refuse that: the
 * whole request fails with "properties must be an object", for every tool,
 * not just this one. An empty `properties` says the same in a form every
 * template takes.
 */
export function modelSchema(
  schema: Record<string, unknown>,
): Record<string, unknown> {
  return schema.properties === undefined
    ? { ...schema, properties: {} }
    : schema;
}

/**
 * A gateway (mcphub and the like) runs other servers' tools through one
 * `call_tool`, so its name says nothing about what runs. The identity that
 * approvals, rules and grants use is the downstream tool, qualified by the
 * gateway: `GATEWAY__SERVER__TOOL`, the name it would have if the gateway
 * listed it directly. It follows mcphub's reading of the arguments: no
 * server splits the tool at its first `__`, a server strips its own `S__`
 * prefix from the tool.
 *
 * mcphub then runs the tool by that name or, failing that, as `S_TOOL`,
 * and shows a self-prefixed `S_TOOL` as `TOOL`: `aliases` are those other
 * names of the same call. Only deny and ask rules match them; an alias can
 * name a different tool on the same server, so it never allows.
 *
 * Undefined, so the call is refused, when the arguments do not name one
 * server and one tool unambiguously: no tool, a server that is neither a
 * string nor null, a server with `__` in it, or a key that Go's JSON
 * decoder would read as `server` or `tool` (it matches keys
 * case-insensitively, with Unicode folding).
 */
export function gatewayTarget(
  gateway: string,
  argumentsJson: string,
): { target: string; inner: string; aliases: string[] } | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsJson);
  } catch {
    return undefined;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
    return undefined;
  const fields = parsed as Record<string, unknown>;
  for (const key of Object.keys(fields)) {
    const folded = key.normalize("NFKC").toLowerCase();
    if (key !== "server" && key !== "tool" && /^(server|tool)$/.test(folded))
      return undefined;
  }
  const { server, tool } = fields;
  if (typeof tool !== "string") return undefined;
  if (server !== undefined && server !== null && typeof server !== "string")
    return undefined;
  let downstream = server ? server : undefined;
  let local = tool;
  if (downstream === undefined) {
    const at = tool.indexOf("__");
    if (at <= 0) return undefined;
    downstream = tool.slice(0, at);
    local = tool.slice(at + 2);
  } else if (local.startsWith(`${downstream}__`))
    local = local.slice(downstream.length + 2);
  const valid = /^[a-zA-Z0-9_.-]{1,128}$/;
  if (
    !valid.test(downstream) ||
    downstream.includes("__") ||
    !valid.test(local)
  )
    return undefined;
  const names = [`${downstream}_${local}`];
  if (
    local.startsWith(`${downstream}_`) &&
    local.length > downstream.length + 1
  )
    names.push(local.slice(downstream.length + 1));
  return {
    target: `${gateway}__${downstream}__${local}`,
    inner: `${downstream}__${local}`,
    aliases: names.map((name) => `${gateway}__${downstream}__${name}`),
  };
}

export type McpToolResult = {
  transport: "success";
  domain: "unknown";
  evidence: "unverified";
  isError: boolean;
  text: string;
};

function boundedText(result: CallToolResult): string {
  const parts = result.content
    .filter(
      (part): part is { type: "text"; text: string } => part.type === "text",
    )
    .map((part) => part.text)
    .join("\n");
  return parts.length > MAX_TEXT ? `${parts.slice(0, MAX_TEXT)}…` : parts;
}

function validateHttp(
  config: Extract<McpServerConfig["transport"], { kind: "http" }>,
) {
  const url = new URL(config.url);
  if (url.protocol !== "http:" && url.protocol !== "https:")
    throw new Error("MCP HTTP URL must use http or https");
  if (url.username || url.password || url.hash)
    throw new Error("MCP HTTP URL cannot contain credentials or fragments");
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(
    url.hostname,
  );
  if (!loopback && !config.allowRemote)
    throw new Error("Remote MCP endpoints require allowRemote: true");
  return url;
}

export async function connectMcp(
  config: McpServerConfig,
  externalSignal?: AbortSignal,
) {
  config = McpServerConfigSchema.parse(config);
  externalSignal?.throwIfAborted();
  const transport =
    config.transport.kind === "stdio"
      ? new StdioClientTransport({
          command: config.transport.command,
          args: config.transport.args ?? [],
          cwd: config.transport.cwd,
          env: Object.fromEntries([
            ...DEFAULT_INHERITED_ENV_VARS.map((key) => [key, ""]),
            ...Object.entries(config.transport.env ?? {}),
          ]),
          stderr: "pipe",
        })
      : new StreamableHTTPClientTransport(validateHttp(config.transport), {
          fetch: async (input, init) =>
            fetch(input, { ...init, redirect: "error" }),
        });
  // A stdio server's stderr is read as it comes (an unread pipe would
  // stall it) and only its end is kept, for the reason it did not start.
  let said = "";
  if (transport instanceof StdioClientTransport)
    transport.stderr?.on("data", (chunk: Buffer | string) => {
      said = (said + chunk.toString()).slice(-STDERR_TAIL);
    });
  const client = new Client({ name: "local-agent", version: "0.0.0" });
  // The SDK ends a stdio server's stdin and waits two seconds before it
  // sends SIGTERM. After a quit (externalSignal) the server is sent SIGTERM
  // at once: one that ignores the end of its stdin would otherwise outlive
  // a caller that waits less than that, as the desktop sidecar does.
  const close = async () => {
    const pid =
      transport instanceof StdioClientTransport ? transport.pid : null;
    if (externalSignal?.aborted && pid)
      try {
        process.kill(pid, "SIGTERM");
      } catch {}
    await client.close();
  };
  let stopped = false;
  client.onclose = () => {
    stopped = true;
  };
  try {
    const timeout = AbortSignal.timeout(CONNECT_TIMEOUT_MS);
    const requestSignal = externalSignal
      ? AbortSignal.any([externalSignal, timeout])
      : timeout;
    await Promise.race([
      client.connect(transport, {
        signal: requestSignal,
        timeout: CONNECT_TIMEOUT_MS,
      }),
      // A quit or the timeout settles connecting at once, whatever the
      // SDK does with the signal while a server starts.
      new Promise<never>((_, reject) =>
        requestSignal.addEventListener(
          "abort",
          () => reject(requestSignal.reason),
          { once: true },
        ),
      ),
    ]);
    const listed = await client.listTools(undefined, { signal: requestSignal });
    if (listed.nextCursor) throw new Error("MCP pagination is not supported");
    if (listed.tools.length > MAX_TOOLS)
      throw new Error("MCP server returned too many tools");
    const names = new Set<string>();
    const tools: McpTool[] = listed.tools.map((tool: Tool) => {
      const name = `${config.name}__${tool.name}`;
      if (!/^[a-zA-Z0-9_.-]{1,128}$/.test(name))
        throw new Error("MCP tool name is invalid or exceeds 128 characters");
      if (names.has(name)) throw new Error(`Duplicate MCP tool: ${name}`);
      names.add(name);
      const schema = tool.inputSchema as Record<string, unknown>;
      if (JSON.stringify(schema).length > 64_000)
        throw new Error(`MCP tool schema too large: ${name}`);
      if (tool.description && tool.description.length > 4_096)
        throw new Error(`MCP tool description too large: ${name}`);
      return {
        name,
        description: tool.description,
        inputSchema: modelSchema(schema),
        readOnly: tool.annotations?.readOnlyHint === true,
      };
    });
    // A gateway's call_tool takes a tool name and its arguments.
    const call = listed.tools.find((tool: Tool) => tool.name === "call_tool");
    const callProps = (
      call?.inputSchema as
        | { properties?: Record<string, { type?: unknown }> }
        | undefined
    )?.properties;
    const gateway =
      callProps?.tool?.type === "string" &&
      callProps.arguments?.type === "object"
        ? { server: config.name, call: `${config.name}__call_tool` }
        : undefined;
    const callTimeoutMs = config.timeoutMs ?? CALL_TIMEOUT_MS;
    return {
      tools,
      ...(gateway ? { gateway } : {}),
      async callTool(
        name: string,
        arguments_: Record<string, unknown>,
        signal?: AbortSignal,
      ): Promise<McpToolResult> {
        const local = name.startsWith(`${config.name}__`)
          ? name.slice(config.name.length + 2)
          : undefined;
        if (!local || !names.has(name))
          throw new Error(`Unknown MCP tool: ${name}`);
        if (stopped)
          throw new McpStoppedError(
            `MCP server ${config.name} has stopped, so the call was not sent. Do not call its tools again in this session; tell the user.`,
          );
        const timeout = AbortSignal.timeout(callTimeoutMs);
        // The SDK's own request timeout is 60 s unless set: a server's
        // longer timeoutMs needs it raised too.
        const result = await client.callTool(
          { name: local, arguments: arguments_ },
          {
            signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
            timeout: callTimeoutMs,
          },
        );
        return {
          transport: "success",
          domain: "unknown",
          evidence: "unverified",
          isError: result.isError === true,
          text: boundedText(result),
        };
      },
      close,
    };
  } catch (error) {
    await close().catch(() => undefined);
    if (config.transport.kind !== "stdio") throw error;
    const reason = notStarted(config.transport, error) ?? error;
    const line = lastStderrLine(said);
    if (!line || !(reason instanceof Error)) throw reason;
    throw new McpStartError(reason.message, line, { cause: error });
  }
}

/**
 * A short, fixable reason for a stdio command that did not start. Servers
 * start with an empty PATH unless their `env` sets one, so a bare `bun`
 * that runs in the shell is not found here.
 */
function notStarted(
  transport: { command: string; env?: Record<string, string>; cwd?: string },
  error: unknown,
): Error | undefined {
  if ((error as { code?: unknown } | undefined)?.code !== "ENOENT") return;
  const { command, cwd } = transport;
  if (cwd && !existsSync(cwd))
    return new Error(`its cwd "${cwd}" does not exist`, { cause: error });
  const path = transport.env?.PATH
    ? "the PATH its env sets"
    : "its PATH (empty: its env sets none)";
  // A file that is there and still ENOENT: its #! interpreter is missing.
  if (command.includes("/"))
    return existsSync(command)
      ? interpreterMissing(command, command, error)
      : new Error(`"${command}" does not exist`, { cause: error });
  const onServerPath = Bun.which(command, {
    PATH: transport.env?.PATH ?? "",
    ...(cwd ? { cwd } : {}),
  });
  if (onServerPath) return interpreterMissing(command, onServerPath, error);
  const found = Bun.which(command);
  return new Error(
    found
      ? `"${command}" is not on ${path}; use "command": "${found}" or add its directory to env.PATH`
      : `"${command}" is not on ${path}; give its full path`,
    { cause: error },
  );
}

/** Why a file that exists did not start: the interpreter its `#!` names. */
function interpreterMissing(
  command: string,
  file: string,
  error: unknown,
): Error {
  let line = "";
  try {
    const fd = openSync(file, "r");
    try {
      const head = Buffer.alloc(128);
      line =
        head
          .subarray(0, readSync(fd, head, 0, head.length, 0))
          .toString("utf8")
          .split("\n")[0] ?? "";
    } finally {
      closeSync(fd);
    }
  } catch {}
  const at = file === command ? `"${command}"` : `"${command}" (${file})`;
  return new Error(
    line.startsWith("#!")
      ? `${at} did not start: the interpreter in "${line.slice(0, 40)}" was not found`
      : `${at} exists but did not start`,
    { cause: error },
  );
}

export type McpConnection = Awaited<ReturnType<typeof connectMcp>>;

/**
 * The user's own MCP servers, loaded when no --mcp-config is given. It is
 * per user, never per workspace: a cloned repository cannot add a server.
 */
export function defaultMcpConfigPath(
  env: Record<string, string | undefined> = process.env,
): string {
  return join(userConfigDir(env), "mcp.json");
}

const MAX_CONFIG_BYTES = LIMITS.mcpConfigBytes;

/**
 * An MCP config file: a JSON array of at most 16 servers with distinct
 * names, at most 1 MB. Throws on anything else.
 */
export async function readMcpConfig(path: string): Promise<McpServerConfig[]> {
  // Opened without blocking and checked to be a regular file before the
  // read: a FIFO or a device named mcp.json would wait forever.
  const handle = await open(path, constants.O_RDONLY | constants.O_NONBLOCK);
  let text: string;
  try {
    const stat = await handle.stat();
    if (!stat.isFile()) throw new Error("MCP config is not a regular file");
    if (stat.size > MAX_CONFIG_BYTES)
      throw new Error("MCP config exceeds 1 MB");
    text = await handle.readFile("utf8");
  } finally {
    await handle.close();
  }
  // A file that grew after the check is held to the same bound.
  if (Buffer.byteLength(text) > MAX_CONFIG_BYTES)
    throw new Error("MCP config exceeds 1 MB");
  // A BOM is not part of the JSON (Bun.file().json() skipped it too).
  const json = text.startsWith("\ufeff") ? text.slice(1) : text;
  const configs = McpServerConfigSchema.array()
    .max(LIMITS.mcpServers)
    .parse(JSON.parse(json));
  if (new Set(configs.map((config) => config.name)).size !== configs.length)
    throw new Error("Duplicate MCP server names");
  return configs;
}

export type McpStartup = {
  /** The servers that connected, in config order. */
  connected: { name: string; connection: McpConnection }[];
  /** The servers that did not, with why (one line, bounded). */
  failed: { name: string; error: string }[];
};

const STARTUP_ERROR_CHARS = 200;

/**
 * Why a server did not start, on one line of at most 200 characters, with
 * no escape sequences or control characters (a server's error can carry
 * them). What the server said is kept whole; the reason ahead of it gives
 * way.
 */
export function startupError(reason: unknown): string {
  const flat = (text: string) =>
    Bun.stripANSI(text)
      // biome-ignore lint/suspicious/noControlCharactersInRegex: remove control characters from a server's error
      .replace(/[\s\u0000-\u001f\u007f-\u009f]+/g, " ")
      .trim();
  if (reason instanceof McpStartError) {
    const tail = `; it said: ${reason.said}`;
    const room = STARTUP_ERROR_CHARS - tail.length;
    const head = flat(reason.reason);
    return `${head.length > room ? `${head.slice(0, room - 1)}…` : head}${tail}`;
  }
  const text = reason instanceof Error ? reason.message : String(reason);
  return flat(text).slice(0, STARTUP_ERROR_CHARS) || "failed";
}

/**
 * Connects every server at once: one slow or broken server neither delays
 * nor sinks the others. An abort closes whatever did connect and throws.
 */
export async function connectMcpServers(
  configs: readonly McpServerConfig[],
  signal?: AbortSignal,
): Promise<McpStartup> {
  const settled = await Promise.allSettled(
    configs.map((config) => connectMcp(config, signal)),
  );
  const startup: McpStartup = { connected: [], failed: [] };
  settled.forEach((result, index) => {
    const name = configs[index]?.name ?? `server ${index + 1}`;
    if (result.status === "fulfilled")
      startup.connected.push({ name, connection: result.value });
    else {
      startup.failed.push({ name, error: startupError(result.reason) });
    }
  });
  if (signal?.aborted) {
    await Promise.allSettled(
      startup.connected.map(({ connection }) => connection.close()),
    );
    throw signal.reason;
  }
  return startup;
}
