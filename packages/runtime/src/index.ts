import { Ollama, type Tool } from "ollama";
import { z } from "zod";
import {
  type OpenAILocalProvider,
  type Provider,
  ProviderSchema,
  providerStreamIdleTimeoutMs,
} from "#runtime/config";

export {
  admitOllamaModel,
  admitOpenAILocalModel,
  type OllamaAdmission,
  type OpenAILocalAdmission,
} from "#runtime/admission";
export {
  type AgentEvent,
  type AgentOptions,
  type ApprovalRequest,
  runAgent,
  type SessionContext,
  sessionProblem,
} from "#runtime/agent";
export {
  DEFAULT_OLLAMA_MODEL,
  type OpenAILocalProvider,
  type Provider,
  type ProviderOptions,
  ProviderSchema,
  providerStreamIdleTimeoutMs,
  resolveProvider,
} from "#runtime/config";
export {
  type ContextPlan,
  PLANNED_CONTEXT_MAX,
  planContext,
  planContextSize,
} from "#runtime/context";
export {
  connectMcp,
  connectMcpServers,
  defaultMcpConfigPath,
  gatewayTarget,
  type McpConnection,
  type McpServerConfig,
  McpServerConfigSchema,
  type McpStartup,
  readMcpConfig,
} from "#runtime/mcp";
export {
  assertFetchTarget,
  type FetchPolicy,
  fetchPolicy,
  isPublicAddress,
} from "#runtime/net";
export {
  appendRule,
  COMMAND_WRAPPERS,
  clearRules,
  evaluate,
  loadPermissionFile,
  type PermissionEffect,
  type PermissionFile,
  type PermissionRequest,
  type PermissionRule,
  type PermissionTool,
  type PermissionVerdict,
  permissionRequest,
  permissionsPath,
  proposeRule,
  readPermissionFile,
  trustRules,
  trustStorePath,
  workspaceRules,
} from "#runtime/permissions";
export {
  Workspace,
  type WorkspaceEditProposal,
  type WorkspaceWriteProposal,
} from "#runtime/workspace";

export const ToolCallSchema = z.object({
  id: z.string().min(1).max(1024).optional(),
  function: z.object({
    name: z.string().min(1).max(128),
    arguments: z.record(z.string(), z.unknown()),
  }),
});
export type ToolCall = z.infer<typeof ToolCallSchema>;
export const MessageSchema = z.object({
  role: z.enum(["system", "user", "assistant", "tool"]),
  content: z.string().max(1_000_000),
  thinking: z.string().max(1_000_000).optional(),
  /** Base64 images (no data URI prefix), Ollama's vision convention; they ride the message that produced them. */
  images: z.array(z.string().max(7_000_000)).max(4).optional(),
  tool_calls: z.array(ToolCallSchema).max(32).optional(),
  tool_name: z.string().max(128).optional(),
  tool_call_id: z.string().min(1).max(1024).optional(),
});
export type Message = z.infer<typeof MessageSchema>;
export const RunEventSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("text"), text: z.string() }),
  z.object({ type: z.literal("thinking"), text: z.string() }),
  z.object({ type: z.literal("tool_call"), call: ToolCallSchema }),
  z.object({
    type: z.literal("done"),
    inputTokens: z.number().int().nonnegative(),
    outputTokens: z.number().int().nonnegative(),
    /** Ollama's own counters, present when the server reports them. Durations are nanoseconds. */
    promptEvalCount: z.number().int().nonnegative().optional(),
    promptEvalDuration: z.number().nonnegative().optional(),
    evalCount: z.number().int().nonnegative().optional(),
    evalDuration: z.number().nonnegative().optional(),
    totalDuration: z.number().nonnegative().optional(),
  }),
]);
export type RunEvent = z.infer<typeof RunEventSchema>;

const streamIdleError = (timeoutMs: number) =>
  new Error(`Provider stream idle timeout after ${timeoutMs}ms`);

function wrapBodyWithIdleTimeout(
  response: Response,
  timeoutMs: number,
  controller: AbortController,
): Response {
  if (!response.body) return response;
  const reader = response.body.getReader();
  let timer: ReturnType<typeof setTimeout> | undefined;
  const clearTimer = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
  };
  const body = new ReadableStream<Uint8Array>({
    async pull(target) {
      clearTimer();
      timer = setTimeout(() => {
        const error = streamIdleError(timeoutMs);
        controller.abort(error);
        void reader.cancel(error).catch(() => undefined);
      }, timeoutMs);
      try {
        const chunk = await reader.read();
        clearTimer();
        if (chunk.done) {
          target.close();
          return;
        }
        target.enqueue(chunk.value);
      } catch (error) {
        clearTimer();
        target.error(controller.signal.reason ?? error);
      }
    },
    async cancel(reason) {
      clearTimer();
      await reader.cancel(reason);
    },
  });
  return new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
}

/** Redirects cannot silently move a local request across the network boundary. */
function guardedFetch(signal: AbortSignal, timeoutMs: number): typeof fetch {
  return Object.assign(
    async (
      url: Parameters<typeof fetch>[0],
      init?: Parameters<typeof fetch>[1],
    ) => {
      const controller = new AbortController();
      const requestSignal = init?.signal
        ? AbortSignal.any([signal, init.signal, controller.signal])
        : AbortSignal.any([signal, controller.signal]);
      let headersTimer: ReturnType<typeof setTimeout> | undefined;
      headersTimer = setTimeout(
        () => controller.abort(streamIdleError(timeoutMs)),
        timeoutMs,
      );
      try {
        const response = await fetch(url, {
          ...init,
          redirect: "error",
          signal: requestSignal,
        });
        if (headersTimer !== undefined) clearTimeout(headersTimer);
        return wrapBodyWithIdleTimeout(response, timeoutMs, controller);
      } catch (error) {
        if (headersTimer !== undefined) clearTimeout(headersTimer);
        throw controller.signal.reason ?? error;
      }
    },
    { preconnect: fetch.preconnect },
  );
}

type OpenAIStreamPart = {
  choices?: Array<{
    delta?: {
      content?: string;
      reasoning_content?: string;
      tool_calls?: Array<{
        index?: number;
        id?: string;
        function?: { name?: string; arguments?: string };
      }>;
    };
  }>;
  usage?: { prompt_tokens?: number; completion_tokens?: number };
};

/** Parses one SSE body into JSON payloads; `data: [DONE]` ends the stream. */
export async function* parseOpenAISSE(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<OpenAIStreamPart> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      for (;;) {
        const boundary = buffer.indexOf("\n\n");
        if (boundary === -1) break;
        const rawEvent = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        for (const line of rawEvent.split("\n")) {
          if (!line.startsWith("data:")) continue;
          const data = line.slice(5).trim();
          if (!data) continue;
          if (data === "[DONE]") return;
          yield JSON.parse(data) as OpenAIStreamPart;
        }
      }
    }
    throw new Error("OpenAI-compatible stream ended before [DONE]");
  } finally {
    await reader.cancel().catch(() => undefined);
  }
}

const toOpenAIMessage = (message: Message) => ({
  role: message.role,
  content: message.content,
  ...(message.tool_calls?.length
    ? {
        tool_calls: message.tool_calls.map((call) => ({
          ...(call.id === undefined ? {} : { id: call.id }),
          type: "function" as const,
          function: {
            name: call.function.name,
            arguments: JSON.stringify(call.function.arguments),
          },
        })),
      }
    : {}),
  ...(message.role === "tool" && message.tool_call_id !== undefined
    ? { tool_call_id: message.tool_call_id }
    : {}),
});

/** Loopback OpenAI-compatible transport (llama.cpp, LM Studio, vLLM): SSE mirroring the Ollama seam. */
async function* openAILocalChat(
  config: OpenAILocalProvider,
  input: Message[],
  signal: AbortSignal,
  tools?: Tool[],
): AsyncGenerator<RunEvent> {
  const doFetch = guardedFetch(signal, providerStreamIdleTimeoutMs(config));
  const response = await doFetch(`${config.base_url}/chat/completions`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(config.api_key ? { authorization: `Bearer ${config.api_key}` } : {}),
    },
    body: JSON.stringify({
      model: config.model,
      stream: true,
      stream_options: { include_usage: true },
      messages: input.map(toOpenAIMessage),
      ...(tools?.length
        ? {
            tools: tools.map((tool) => ({
              type: "function" as const,
              function: tool.function,
            })),
          }
        : {}),
    }),
  });
  if (!response.ok)
    throw new Error(
      `OpenAI-compatible chat request failed (${response.status})`,
    );
  if (!response.body) throw new Error("OpenAI-compatible response had no body");
  let outputSize = 0;
  let callCount = 0;
  const pendingCalls = new Map<
    number,
    { id?: string; name: string; args: string }
  >();
  const emitToolCalls = function* (): Generator<RunEvent> {
    for (const [index, call] of [...pendingCalls.entries()].sort(
      (a, b) => a[0] - b[0],
    )) {
      if (++callCount > 32)
        throw new Error("Model response exceeds tool call limit");
      if (`${call.name}${call.args}`.length > 100_000)
        throw new Error("Tool arguments exceed size limit");
      let arguments_: unknown;
      try {
        arguments_ = JSON.parse(call.args || "{}");
      } catch {
        throw new Error("Model tool arguments were not valid JSON");
      }
      yield RunEventSchema.parse({
        type: "tool_call",
        call: {
          ...(call.id === undefined ? {} : { id: call.id }),
          function: { name: call.name, arguments: arguments_ },
        },
      });
      pendingCalls.delete(index);
    }
  };
  let usage: OpenAIStreamPart["usage"];
  for await (const part of parseOpenAISSE(response.body)) {
    signal.throwIfAborted();
    outputSize += JSON.stringify(part).length;
    if (outputSize > 1_000_000)
      throw new Error("Model response exceeds size limit");
    const delta = part.choices?.[0]?.delta;
    if (delta?.reasoning_content)
      yield RunEventSchema.parse({
        type: "thinking",
        text: delta.reasoning_content,
      });
    if (delta?.content)
      yield RunEventSchema.parse({ type: "text", text: delta.content });
    for (const fragment of delta?.tool_calls ?? []) {
      const index = fragment.index ?? 0;
      const entry = pendingCalls.get(index) ?? { name: "", args: "" };
      if (fragment.id !== undefined) entry.id = fragment.id;
      if (fragment.function?.name) entry.name += fragment.function.name;
      if (fragment.function?.arguments)
        entry.args += fragment.function.arguments;
      pendingCalls.set(index, entry);
    }
    if (part.usage) usage = part.usage;
  }
  yield* emitToolCalls();
  yield RunEventSchema.parse({
    type: "done",
    inputTokens: usage?.prompt_tokens ?? 0,
    outputTokens: usage?.completion_tokens ?? 0,
  });
}

/** Transport only: tool calls are data, never authority to execute. */
export async function* chat(
  provider: Provider,
  messages: Message[],
  signal: AbortSignal,
  tools?: Tool[],
): AsyncGenerator<RunEvent> {
  const config = ProviderSchema.parse(provider);
  const input = z.array(MessageSchema).min(1).max(1000).parse(messages);
  signal.throwIfAborted();
  if (config.type === "openai_local") {
    yield* openAILocalChat(
      { ...config, type: "openai_local" as const },
      input,
      signal,
      tools,
    );
    return;
  }
  const client = new Ollama({
    host: config.base_url,
    fetch: guardedFetch(signal, providerStreamIdleTimeoutMs(config)),
  });
  try {
    const stream = await client.chat({
      model: config.model,
      messages: input,
      stream: true,
      tools,
      // Sent only when set: an unset `think` keeps the model's own default.
      ...(config.think === undefined ? {} : { think: config.think }),
      options: { num_ctx: config.context_size },
    });
    let outputSize = 0;
    let callCount = 0;
    for await (const part of stream) {
      signal.throwIfAborted();
      outputSize += JSON.stringify(part.message ?? {}).length;
      if (outputSize > 1_000_000)
        throw new Error("Model response exceeds size limit");
      if (part.message?.thinking)
        yield RunEventSchema.parse({
          type: "thinking",
          text: part.message.thinking,
        });
      if (part.message?.content)
        yield RunEventSchema.parse({
          type: "text",
          text: part.message.content,
        });
      for (const rawCall of part.message?.tool_calls ?? []) {
        if (++callCount > 32)
          throw new Error("Model response exceeds tool call limit");
        if (JSON.stringify(rawCall).length > 100_000)
          throw new Error("Tool arguments exceed size limit");
        yield RunEventSchema.parse({ type: "tool_call", call: rawCall });
      }
      if (part.done) {
        // Ollama's own counters, only the ones it reported.
        const reported: Record<string, number> = {};
        for (const [key, value] of [
          ["promptEvalCount", part.prompt_eval_count],
          ["promptEvalDuration", part.prompt_eval_duration],
          ["evalCount", part.eval_count],
          ["evalDuration", part.eval_duration],
          ["totalDuration", part.total_duration],
        ] as const)
          if (typeof value === "number" && Number.isFinite(value) && value >= 0)
            reported[key] = value;
        yield RunEventSchema.parse({
          type: "done",
          inputTokens: part.prompt_eval_count ?? 0,
          outputTokens: part.eval_count ?? 0,
          ...reported,
        });
        return;
      }
    }
    throw new Error("Ollama stream ended before completion");
  } finally {
    client.abort();
  }
}

export { inspectRecovery } from "#runtime/recovery";
export { type Session, SessionStore } from "#runtime/session";
