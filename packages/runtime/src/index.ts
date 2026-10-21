import { Ollama, type Tool } from "ollama";
import { z } from "zod";
import {
  type Provider,
  ProviderSchema,
  providerStreamIdleTimeoutMs,
} from "#runtime/config";

export { admitOllamaModel, type OllamaAdmission } from "#runtime/admission";
export {
  type AgentEvent,
  type AgentOptions,
  type ApprovalRequest,
  runAgent,
  type SessionContext,
} from "#runtime/agent";
export {
  DEFAULT_OLLAMA_MODEL,
  type Provider,
  type ProviderOptions,
  ProviderSchema,
  providerStreamIdleTimeoutMs,
  resolveProvider,
} from "#runtime/config";
export {
  connectMcp,
  type McpConnection,
  McpServerConfigSchema,
} from "#runtime/mcp";
export { Workspace, type WorkspaceEditProposal } from "#runtime/workspace";

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
  const client = new Ollama({
    host: config.base_url,
    // Redirects cannot silently move a local request across the network boundary.
    fetch: Object.assign(
      async (
        url: Parameters<typeof fetch>[0],
        init?: Parameters<typeof fetch>[1],
      ) => {
        const controller = new AbortController();
        const requestSignal = init?.signal
          ? AbortSignal.any([signal, init.signal, controller.signal])
          : AbortSignal.any([signal, controller.signal]);
        let headersTimer: ReturnType<typeof setTimeout> | undefined;
        const timeoutMs = providerStreamIdleTimeoutMs(config);
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
    ),
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
