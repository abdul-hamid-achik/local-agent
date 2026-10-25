import { z } from "zod";
import { LIMITS } from "#runtime/limits";

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
  tool_calls: z
    .array(ToolCallSchema)
    .max(LIMITS.toolCallsPerResponse)
    .optional(),
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
