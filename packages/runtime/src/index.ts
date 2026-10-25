import { Ollama, type Tool } from "ollama";
import { z } from "zod";
import {
  type OpenAILocalProvider,
  type Provider,
  ProviderSchema,
  providerStreamIdleTimeoutMs,
} from "#runtime/config";
import { tunnelLost, withRetries } from "#runtime/retry";

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
export { commandLine, secretPathNotes, shellWord } from "#runtime/command";
export {
  DEFAULT_OLLAMA_MODEL,
  type OpenAILocalProvider,
  type Provider,
  type ProviderOptions,
  ProviderSchema,
  providerLocality,
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
export { providerRetryDelayMs } from "#runtime/retry";
export {
  type ChangeLine,
  changeLines,
  Workspace,
  type WorkspaceEditProposal,
  type WorkspaceEntry,
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
/** The most characters a message's content (or its thinking) can hold. */
export const MAX_MESSAGE_CHARS = 1_000_000;

/**
 * Why a client refuses a send of `length` characters, for the person, or
 * undefined when it fits in a message. Past the limit the message fails
 * here, and kept in a client's history it would fail every later turn, so
 * it never leaves the composer.
 */
export function tooLongToSend(
  length: number,
  lead = "Not sent",
): string | undefined {
  if (length <= MAX_MESSAGE_CHARS) return undefined;
  return `${lead}: ${length.toLocaleString("en-US")} characters, and a message holds ${MAX_MESSAGE_CHARS.toLocaleString("en-US")} at most. Send it in parts, or save it to a file in the workspace and ask the model to read it`;
}
export const MessageSchema = z.object({
  role: z.enum(["system", "user", "assistant", "tool"]),
  content: z.string().max(MAX_MESSAGE_CHARS),
  thinking: z.string().max(MAX_MESSAGE_CHARS).optional(),
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

/**
 * Redirects cannot silently move a local request across the network boundary.
 * A refused connection or a 429/502/503/504 is retried (see withRetries); the
 * body of a request must be replayable, as the string bodies sent here are.
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
      // The attempt that answered owns the body's idle watchdog.
      let controller = new AbortController();
      const response = await withRetries(
        async () => {
          const attempt = new AbortController();
          controller = attempt;
          const headersTimer = setTimeout(
            () => attempt.abort(streamIdleError(timeoutMs)),
            timeoutMs,
          );
          try {
            return await fetch(url, {
              ...init,
              redirect: "error",
              signal: AbortSignal.any([caller, attempt.signal]),
            });
          } catch (error) {
            throw attempt.signal.reason ?? error;
          } finally {
            clearTimeout(headersTimer);
          }
        },
        caller,
        init?.body,
      );
      return wrapBodyWithIdleTimeout(response, timeoutMs, controller);
    },
    { preconnect: fetch.preconnect },
  );
}

/**
 * Ollama's client skips a stream line that is not JSON with a warning on
 * stderr, under a fullscreen client's screen, and the reply goes on without
 * it: a tool call or the reply's end could be lost unseen. Such a line
 * fails the stream instead, naming its start.
 */
function checkedNdjson(response: Response): Response {
  if (!response.ok || !response.body) return response;
  const decoder = new TextDecoder();
  let pending = "";
  const check = (line: string) => {
    if (!line.trim()) return;
    try {
      JSON.parse(line);
    } catch {
      const flat = Bun.stripANSI(line)
        // biome-ignore lint/suspicious/noControlCharactersInRegex: remove control characters from what the server sent
        .replace(/[\s\u0000-\u001f\u007f-\u009f]+/g, " ")
        .trim();
      // By code point: a cut never leaves half of a surrogate pair.
      const start = Array.from(flat).slice(0, 60).join("");
      throw new Error(`Ollama sent a line that is not JSON: ${start}`);
    }
  };
  const body = response.body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        pending += decoder.decode(chunk, { stream: true });
        const lines = pending.split("\n");
        pending = lines.pop() ?? "";
        for (const line of lines) check(line);
        controller.enqueue(chunk);
      },
      flush() {
        check(pending + decoder.decode());
      },
    }),
  );
  return new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
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

/** A pending SSE event past this many UTF-16 code units fails the request instead of growing in memory. */
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
  // A "\r" ends a line at once; when it ends a chunk, a "\n" opening the
  // next one is its CRLF's second half and is dropped.
  let skipLF = false;
  let finished = false;
  try {
    for (;;) {
      const chunk = await reader.read();
      let text = chunk.done
        ? decoder.decode()
        : decoder.decode(chunk.value, { stream: true });
      if (skipLF && text) {
        if (text.startsWith("\n")) text = text.slice(1);
        skipLF = false;
      }
      if (text) skipLF = text.endsWith("\r");
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
          "OpenAI-compatible stream event exceeds 8 Mi UTF-16 code units without a blank line",
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

/**
 * A tool name arriving in fragments: pieces of one name, or the whole name
 * repeated in each fragment. A piece equal to what we have is ambiguous
 * ("read"+"read" or "read" twice). A repeat keeps the name unless only the
 * doubled name is a declared tool.
 */
function joinName(have: string, piece: string, declared: Set<string>) {
  if (piece !== have) return have + piece;
  return declared.has(have + piece) && !declared.has(have)
    ? have + piece
    : have;
}

/** Whether `piece` can continue `have` as a split name: the joined name leads to a declared tool. */
function namesContinue(have: string, piece: string, declared: Set<string>) {
  const joined = have + piece;
  return (
    declared.size === 0 || [...declared].some((name) => name.startsWith(joined))
  );
}

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
  const declared = new Set<string>();
  for (const tool of tools ?? [])
    if (tool.function?.name) declared.add(tool.function.name);
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
      const piece = fragment.function?.name;
      if (index === undefined) {
        // Without an index, a new id starts a call, and so does a different
        // name once the last call's arguments began (a name only splits
        // across fragments before them); anything else continues the last.
        const last =
          lastIndex === undefined ? undefined : pendingCalls.get(lastIndex);
        const newName =
          piece &&
          last?.name &&
          piece !== last.name &&
          (last.args !== "" || !namesContinue(last.name, piece, declared));
        index =
          lastIndex !== undefined &&
          last &&
          !newName &&
          (!fragment.id || last.id === undefined || fragment.id === last.id)
            ? lastIndex
            : Math.max(-1, ...pendingCalls.keys()) + 1;
      }
      lastIndex = index;
      const entry = pendingCalls.get(index) ?? { name: "", args: "" };
      // Some servers repeat the id (or send "") and the name in every
      // fragment: the first id stands, a name is joined unless it repeats.
      if (fragment.id && entry.id === undefined) entry.id = fragment.id;
      if (piece) entry.name = joinName(entry.name, piece, declared);
      if (fragment.function?.arguments)
        entry.args += fragment.function.arguments;
      pendingCalls.set(index, entry);
    }
    // The same choices the parser's finished check looks at, not only the
    // first. One reply is asked for; a server that sends several choices has
    // its first non-empty string reason count, and anything else is skipped.
    // A later chunk's reason replaces an earlier one.
    const reason = part.choices?.find(
      (choice) =>
        typeof choice.finish_reason === "string" && choice.finish_reason !== "",
    )?.finish_reason;
    if (typeof reason === "string") finishReason = reason;
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
  const fetchGuarded = guardedFetch(
    signal,
    providerStreamIdleTimeoutMs(config),
  );
  const client = new Ollama({
    host: config.base_url,
    fetch: Object.assign(
      async (...args: Parameters<typeof fetch>) =>
        checkedNdjson(await fetchGuarded(...args)),
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
          ...(typeof part.done_reason === "string" && part.done_reason
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
