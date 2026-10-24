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
  type Answer,
  type Approval,
  type ApprovalRequest,
  type GoalReport,
  MAX_HISTORY_MESSAGES,
  type Question,
  runAgent,
  type SessionContext,
  StepLimitError,
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
  explicitContextNote,
  MLX_WARNING,
  PLANNED_CONTEXT_MAX,
  planContext,
  planContextAdmitted,
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
  type LoadedModel,
  loadedModel,
  type ProviderProbe,
  placementWarning,
  probeProvider,
} from "#runtime/probe";
export {
  type ChangeLine,
  changeLines,
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
    /**
     * Why the reply ended, as the server says it (Ollama's `done_reason`,
     * OpenAI's `finish_reason`): "stop" is a finished reply, "length" one cut
     * at the output or context limit. Absent when the server did not say.
     */
    finishReason: z.string().min(1).max(64).optional(),
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

const maxProviderRetries = 2;
const maxRetryWaitMs = 10_000;
/** Answers that say "not now" before any reply: the server did nothing with the request. */
const retryableStatuses = new Set([429, 502, 503, 504]);

/** The wait before retry `attempt` (0-based): the server's Retry-After, capped, else a short backoff. */
export function providerRetryDelayMs(
  response: Response | undefined,
  attempt: number,
): number {
  const header = response?.headers.get("retry-after")?.trim();
  if (header) {
    const ms = /^\d+$/.test(header)
      ? Number(header) * 1_000
      : Date.parse(header) - Date.now();
    if (Number.isFinite(ms)) return Math.min(Math.max(ms, 0), maxRetryWaitMs);
  }
  return 250 * 2 ** attempt;
}

/** A pause that the request's own signal can end. */
const pause = (ms: number, signal: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal.addEventListener("abort", onAbort, { once: true });
  });

/**
 * Redirects cannot silently move a local request across the network boundary.
 * A refused connection or a 429/502/503/504 is retried up to twice: both
 * come before any byte of the reply, and a model call has no side effects,
 * so a repeat cannot double anything. Once a response is handed back, its
 * body is the caller's and is never replayed.
 */
function guardedFetch(signal: AbortSignal, timeoutMs: number): typeof fetch {
  return Object.assign(
    async (
      url: Parameters<typeof fetch>[0],
      init?: Parameters<typeof fetch>[1],
    ) => {
      const caller = init?.signal
        ? AbortSignal.any([signal, init.signal])
        : signal;
      for (let attempt = 0; ; attempt++) {
        const controller = new AbortController();
        const headersTimer = setTimeout(
          () => controller.abort(streamIdleError(timeoutMs)),
          timeoutMs,
        );
        let response: Response;
        try {
          response = await fetch(url, {
            ...init,
            redirect: "error",
            signal: AbortSignal.any([caller, controller.signal]),
          });
        } catch (error) {
          clearTimeout(headersTimer);
          // Only a refused or reset connection; a timeout or a cancel is final
          // (their errors are not tunnelLost, and pause throws once cancelled).
          if (attempt < maxProviderRetries && tunnelLost(error)) {
            await pause(providerRetryDelayMs(undefined, attempt), caller);
            continue;
          }
          throw controller.signal.reason ?? error;
        }
        clearTimeout(headersTimer);
        if (
          attempt < maxProviderRetries &&
          retryableStatuses.has(response.status)
        ) {
          await response.body?.cancel().catch(() => undefined);
          await pause(providerRetryDelayMs(response, attempt), caller);
          continue;
        }
        return wrapBodyWithIdleTimeout(response, timeoutMs, controller);
      }
    },
    { preconnect: fetch.preconnect },
  );
}

type OpenAIStreamPart = {
  choices?: Array<{
    finish_reason?: string | null;
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
  /** What a server sends instead of a completion when it fails mid-stream. */
  error?: { message?: unknown } | string;
};

/**
 * The key a tool call's arguments carry when the model's JSON did not parse
 * (or was not an object): the agent answers the call with an error the
 * model can correct, instead of failing the turn.
 */
export const INVALID_ARGUMENTS = "__invalid_arguments";

/** A pending SSE event past this many characters fails the request instead of growing in memory. */
const maxPendingSSEChars = 8 << 20;

/**
 * Parses one SSE body into JSON payloads. Lines end in LF, CRLF or CR; an
 * event's `data:` lines join with "\n". `data: [DONE]` ends the stream, and
 * so does a clean close after a `finish_reason`, for servers that skip it.
 */
export async function* parseOpenAISSE(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<OpenAIStreamPart> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  // A "\r" that ends a chunk waits: a "\n" may open the next one.
  let heldCR = false;
  let finished = false;
  try {
    for (;;) {
      const chunk = await reader.read();
      let text = chunk.done
        ? decoder.decode()
        : decoder.decode(chunk.value, { stream: true });
      if (heldCR) text = `\r${text}`;
      heldCR = !chunk.done && text.endsWith("\r");
      if (heldCR) text = text.slice(0, -1);
      buffer += text.replace(/\r\n?/g, "\n");
      // A last event without its blank line still counts.
      if (chunk.done) buffer += "\n\n";
      let start = 0;
      for (;;) {
        const boundary = buffer.indexOf("\n\n", start);
        if (boundary === -1) break;
        const data: string[] = [];
        for (const line of buffer.slice(start, boundary).split("\n"))
          if (line.startsWith("data:"))
            data.push(line.slice(line.startsWith("data: ") ? 6 : 5));
        start = boundary + 2;
        const payload = data.join("\n").trim();
        if (!payload) continue;
        if (payload === "[DONE]") return;
        const part = JSON.parse(payload) as OpenAIStreamPart;
        if (part.choices?.some((choice) => choice.finish_reason))
          finished = true;
        yield part;
      }
      buffer = buffer.slice(start);
      if (buffer.length > maxPendingSSEChars)
        throw new Error(
          "OpenAI-compatible stream event exceeds 8 MiB without a blank line",
        );
      if (chunk.done) break;
    }
    if (finished) return;
    throw new Error(
      "OpenAI-compatible stream ended before [DONE] and without a finish_reason (the server closed the connection mid-reply)",
    );
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
        arguments_ = undefined;
      }
      if (
        typeof arguments_ !== "object" ||
        arguments_ === null ||
        Array.isArray(arguments_)
      )
        arguments_ = { [INVALID_ARGUMENTS]: call.args.slice(0, 500) };
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
  let finishReason: string | undefined;
  let lastIndex: number | undefined;
  for await (const part of parseOpenAISSE(response.body)) {
    signal.throwIfAborted();
    outputSize += JSON.stringify(part).length;
    if (outputSize > 1_000_000)
      throw new Error("Model response exceeds size limit");
    if (part.error !== undefined) {
      const detail =
        typeof part.error === "string" ? part.error : part.error.message;
      throw new Error(
        `OpenAI-compatible server error: ${String(detail ?? "unknown").slice(0, 300)}`,
      );
    }
    const delta = part.choices?.[0]?.delta;
    if (delta?.reasoning_content)
      yield RunEventSchema.parse({
        type: "thinking",
        text: delta.reasoning_content,
      });
    if (delta?.content)
      yield RunEventSchema.parse({ type: "text", text: delta.content });
    for (const fragment of delta?.tool_calls ?? []) {
      let index = fragment.index;
      if (index === undefined) {
        // Without an index, a new id starts a call; anything else continues the last.
        const last =
          lastIndex === undefined ? undefined : pendingCalls.get(lastIndex);
        index =
          lastIndex !== undefined &&
          last &&
          (!fragment.id || last.id === undefined || fragment.id === last.id)
            ? lastIndex
            : Math.max(-1, ...pendingCalls.keys()) + 1;
      }
      lastIndex = index;
      const entry = pendingCalls.get(index) ?? { name: "", args: "" };
      // Some servers repeat the id (or send "") and the name in every
      // fragment: the first id stands, a name that repeats is not doubled.
      if (fragment.id && entry.id === undefined) entry.id = fragment.id;
      if (fragment.function?.name && fragment.function.name !== entry.name)
        entry.name += fragment.function.name;
      if (fragment.function?.arguments)
        entry.args += fragment.function.arguments;
      pendingCalls.set(index, entry);
    }
    const reason = part.choices?.[0]?.finish_reason;
    if (reason) finishReason = reason;
    if (part.usage) usage = part.usage;
  }
  yield* emitToolCalls();
  yield RunEventSchema.parse({
    type: "done",
    inputTokens: usage?.prompt_tokens ?? 0,
    outputTokens: usage?.completion_tokens ?? 0,
    ...(finishReason ? { finishReason: finishReason.slice(0, 64) } : {}),
  });
}

/** A connection that was refused, reset or cut before the reply ended. */
function tunnelLost(error: unknown): boolean {
  const { code, message } = (error ?? {}) as {
    code?: unknown;
    message?: unknown;
  };
  return (
    /^(ConnectionRefused|ConnectionClosed|ECONNREFUSED|ECONNRESET|EPIPE)$/.test(
      String(code ?? ""),
    ) ||
    /Unable to connect|socket connection was closed|Did not receive done|stream ended before completion/.test(
      String(message ?? ""),
    )
  );
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
          ...(part.done_reason
            ? { finishReason: part.done_reason.slice(0, 64) }
            : {}),
        });
        return;
      }
    }
    throw new Error("Ollama stream ended before completion");
  } catch (error) {
    // A Chalupa endpoint is a tunnel: when it drops, say so, and that the
    // GPU behind it may still be running (Chalupa owns it, not this run).
    if (config.type === "ollama_remote" && !signal.aborted && tunnelLost(error))
      throw new Error(
        `The Chalupa tunnel to ${config.base_url} was lost (${error instanceof Error ? error.message.split("\n")[0] : "connection closed"}). The GPU may still be running: run chalupa status.`,
        { cause: error },
      );
    throw error;
  } finally {
    client.abort();
  }
}

export {
  inspectRecovery,
  RECONCILE_NOTE_MAX,
  type RecoveryItem,
  reconcileExecution,
  recoveryItems,
  settleInterrupted,
  settleSession,
} from "#runtime/recovery";
export { sandboxUnavailable } from "#runtime/sandbox";
export {
  ConversationFullError,
  type Disposition,
  type Session,
  SessionStore,
} from "#runtime/session";
