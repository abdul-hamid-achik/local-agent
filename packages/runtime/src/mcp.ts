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
import { userConfigDir } from "#runtime/paths";

const MAX_TOOLS = 128;
const MAX_TEXT = 16_384;
/** Connecting and listing tools: a gateway fans out to its servers first. */
const CONNECT_TIMEOUT_MS = 15_000;
/** One tool call, unless the server's entry sets `timeoutMs`. */
const CALL_TIMEOUT_MS = 60_000;

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
 * A gateway (mcphub and the like) runs other servers' tools through one
 * `call_tool`, so its name says nothing about what runs. The identity that
 * approvals, rules and grants use is the downstream tool, qualified by the
 * gateway: `GATEWAY__SERVER__TOOL`, the name it would have if the gateway
 * listed it directly. `aliases` are other spellings the gateway resolves
 * to the same tool (a self-prefixed `SERVER_TOOL`), for deny rules.
 * Undefined when the arguments do not name a server and a tool.
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
  if (typeof parsed !== "object" || parsed === null) return undefined;
  const { server, tool } = parsed as Record<string, unknown>;
  if (typeof tool !== "string") return undefined;
  let downstream = typeof server === "string" && server ? server : undefined;
  let local = tool;
  if (downstream === undefined) {
    const at = tool.indexOf("__");
    if (at <= 0) return undefined;
    downstream = tool.slice(0, at);
    local = tool.slice(at + 2);
  } else if (local.startsWith(`${downstream}__`))
    local = local.slice(downstream.length + 2);
  const valid = /^[a-zA-Z0-9_.-]{1,128}$/;
  if (!valid.test(downstream) || !valid.test(local)) return undefined;
  const alias = local.startsWith(`${downstream}_`)
    ? local.slice(downstream.length + 1)
    : `${downstream}_${local}`;
  return {
    target: `${gateway}__${downstream}__${local}`,
    inner: `${downstream}__${local}`,
    aliases: alias ? [`${gateway}__${downstream}__${alias}`] : [],
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
          stderr: "ignore",
        })
      : new StreamableHTTPClientTransport(validateHttp(config.transport), {
          fetch: async (input, init) =>
            fetch(input, { ...init, redirect: "error" }),
        });
  const client = new Client({ name: "local-agent", version: "0.0.0" });
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
      new Promise<never>((_, reject) =>
        timeout.addEventListener("abort", () => reject(timeout.reason), {
          once: true,
        }),
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
        inputSchema: tool.inputSchema as Record<string, unknown>,
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
        const timeout = AbortSignal.timeout(callTimeoutMs);
        // The SDK's own request timeout defaults to the connect timeout:
        // it is raised with ours, or it would cut every call at 15 s.
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
      close: () => client.close(),
    };
  } catch (error) {
    await client.close().catch(() => undefined);
    throw error;
  }
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

/**
 * An MCP config file: a JSON array of at most 16 servers with distinct
 * names, at most 1 MB. Throws on anything else.
 */
export async function readMcpConfig(path: string): Promise<McpServerConfig[]> {
  const file = Bun.file(path);
  if (file.size > 1_000_000) throw new Error("MCP config exceeds 1 MB");
  const configs = McpServerConfigSchema.array()
    .max(16)
    .parse(await file.json());
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
      const reason = result.reason;
      const text = reason instanceof Error ? reason.message : String(reason);
      startup.failed.push({
        name,
        error: text.replace(/\s+/g, " ").trim().slice(0, 200) || "failed",
      });
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
