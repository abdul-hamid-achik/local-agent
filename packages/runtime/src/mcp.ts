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

const MAX_TOOLS = 128;
const MAX_TEXT = 16_384;
const REQUEST_TIMEOUT_MS = 10_000;

export type McpServerConfig = {
  name: string;
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
    const timeout = AbortSignal.timeout(REQUEST_TIMEOUT_MS);
    const requestSignal = externalSignal
      ? AbortSignal.any([externalSignal, timeout])
      : timeout;
    await Promise.race([
      client.connect(transport, {
        signal: requestSignal,
        timeout: REQUEST_TIMEOUT_MS,
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
    return {
      tools,
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
        const timeout = AbortSignal.timeout(REQUEST_TIMEOUT_MS);
        const result = await client.callTool(
          { name: local, arguments: arguments_ },
          { signal: signal ? AbortSignal.any([signal, timeout]) : timeout },
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
