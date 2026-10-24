// Shortening a long history before a provider call. A turn that reads and
// runs a lot fills the window with results the model has already acted on;
// once the prompt passes COMPACT_HIGH of the window, the oldest of them are
// replaced by a short note until it is back near COMPACT_LOW. Nothing is
// removed: every message keeps its place, role and tool-call identity, so
// the history stays one a session can resume.

import type { Message } from "#runtime/index";

/** Compaction starts when the prompt passes this share of the window... */
export const COMPACT_HIGH = 0.7;
/** ...and stops once it is back at or under this one. */
export const COMPACT_LOW = 0.45;
/** Characters per token for estimates; code and JSON run near three. */
export const CHARS_PER_TOKEN = 3;
/** What an image costs in the estimate, in characters (about 1,000 tokens). */
const IMAGE_CHARS = 3_000;
/** How much of an elided output stays, from its start, cut at a line end. */
const PREVIEW = 240;
/** A result shorter than this is left alone: its note would save little. */
const MIN_ELIDED = 800;

const ELIDED =
  "characters elided to keep the conversation within the context window. Run the tool again if you need them.]";

// CJK and other wide scripts run near one token a character, not three.
const WIDE = /[\u2e80-\u9fff\uac00-\ud7af\uf900-\ufaff\uff00-\uffef]/g;

/** `text`'s length, a wide character counted as a whole token. */
function weighted(text: string): number {
  const wide = text.match(WIDE)?.length ?? 0;
  return text.length + wide * (CHARS_PER_TOKEN - 1);
}

/**
 * Characters the model reads of `messages`: content, tool calls and images,
 * and `thinking` only after the last user message (templates such as
 * qwen3.5's drop earlier turns' reasoning). A wide character counts three.
 */
export function promptChars(messages: readonly Message[]): number {
  const lastUser = messages.findLastIndex((message) => message.role === "user");
  let chars = 0;
  for (const [index, message] of messages.entries()) {
    chars += weighted(message.content);
    if (message.thinking && index > lastUser)
      chars += weighted(message.thinking);
    if (message.tool_calls) chars += JSON.stringify(message.tool_calls).length;
    chars += (message.images?.length ?? 0) * IMAGE_CHARS;
  }
  return chars;
}

/**
 * A tool message's `{status, output}` content with the output cut to its
 * first lines and a note of what went.
 */
function elidedResult(content: string): string {
  let status = "unknown";
  let output = content;
  try {
    const parsed = JSON.parse(content) as {
      status?: unknown;
      output?: unknown;
    };
    if (typeof parsed.status === "string") status = parsed.status;
    if (typeof parsed.output === "string") output = parsed.output;
  } catch {
    // Not the runtime's receipt: its status is unknown.
  }
  const end = output.lastIndexOf("\n", PREVIEW);
  const preview = output.slice(0, end > PREVIEW / 2 ? end : PREVIEW);
  return JSON.stringify({
    status,
    output: `${preview}\n[${output.length - preview.length} more ${ELIDED}`,
  });
}

function isElided(content: string): boolean {
  try {
    const parsed = JSON.parse(content) as { output?: unknown };
    return typeof parsed.output === "string" && parsed.output.endsWith(ELIDED);
  } catch {
    return false;
  }
}

/**
 * `history` with at least `chars` characters taken out, oldest first, or as
 * many as can be: first images, then tool results the model has already
 * read, then the reasoning of earlier steps of the current turn. The last
 * user message, the last assistant message and every result after it are
 * the model's current work and stay whole. Messages are copied, not changed.
 */
export function compactHistory(
  history: readonly Message[],
  chars: number,
): { messages: Message[]; elided: number; saved: number } {
  const messages = history.map((message) => ({ ...message }));
  const lastUser = messages.findLastIndex((message) => message.role === "user");
  const lastAssistant = messages.findLastIndex(
    (message) => message.role === "assistant",
  );
  const kept = (index: number) => index === lastUser || index >= lastAssistant;
  const changed = new Set<number>();
  let saved = 0;
  const passes: ((message: Message, index: number) => number)[] = [
    (message) => {
      if (!message.images?.length) return 0;
      const freed = message.images.length * IMAGE_CHARS;
      delete message.images;
      return freed;
    },
    (message) => {
      if (
        message.role !== "tool" ||
        message.content.length < MIN_ELIDED ||
        isElided(message.content)
      )
        return 0;
      const before = message.content.length;
      message.content = elidedResult(message.content);
      return before - message.content.length;
    },
    (message, index) => {
      if (message.role !== "assistant" || !message.thinking || index < lastUser)
        return 0;
      const freed = message.thinking.length;
      delete message.thinking;
      return freed;
    },
  ];
  for (const pass of passes)
    for (const [index, message] of messages.entries()) {
      if (saved >= chars) break;
      if (kept(index)) continue;
      const freed = pass(message, index);
      if (freed > 0) {
        saved += freed;
        changed.add(index);
      }
    }
  return { messages, elided: changed.size, saved };
}
