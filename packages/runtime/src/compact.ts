// Shortening a long history before a provider call. A turn that reads and
// runs a lot fills the window with results the model has already acted on;
// once the prompt passes COMPACT_HIGH of the window, the oldest of them are
// replaced by a short note until it is back near COMPACT_LOW. Nothing is
// removed: every message keeps its place, role and tool-call identity, so
// the history stays one a session can resume.

import { type Message, MessageSchema } from "#runtime/schemas";
import type { SessionContext } from "#runtime/session";

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

/**
 * A session's saved state is shortened (compactState) once its JSON passes
 * this many bytes... Under session.ts's 8 MiB cap, with room for the next
 * step's results (a fetched image alone runs to several MiB).
 */
export const STATE_SOFT_BYTES = 6 * 1024 * 1024;
/** ...and down to this many. */
export const STATE_TARGET_BYTES = 4 * 1024 * 1024;

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
  return messageSizes(messages).reduce((total, size) => total + size, 0);
}

/** Each message's share of promptChars, in order. */
function messageSizes(messages: readonly Message[]): number[] {
  const lastUser = messages.findLastIndex((message) => message.role === "user");
  return messages.map(
    (message, index) =>
      weighted(message.content) +
      (message.thinking && index > lastUser ? weighted(message.thinking) : 0) +
      (message.tool_calls ? JSON.stringify(message.tool_calls).length : 0) +
      (message.images?.length ?? 0) * IMAGE_CHARS,
  );
}

/**
 * Where a request's history starts so it fits: 0 when all of `history`
 * does, in `budget` characters (promptChars) and `maxMessages` messages;
 * else the first user message from which the rest does. An exchange is
 * never split, so a tool result always follows its call. The part left
 * out is rounded up to a multiple of `step` characters: the start holds
 * still while a few exchanges are added, since each move costs the server
 * its cached prompt. It never passes the last user message (the current
 * turn goes whole), so the rest may still be too long: the caller checks.
 */
export function windowStart(
  history: readonly Message[],
  budget: number,
  step: number,
  maxMessages: number,
): number {
  const sizes = messageSizes(history);
  const total = sizes.reduce((sum, size) => sum + size, 0);
  if (total <= budget && history.length <= maxMessages) return 0;
  const over = Math.max(0, total - budget);
  const drop = step > 0 ? Math.ceil(over / step) * step : over;
  const lastUser = history.findLastIndex((message) => message.role === "user");
  let dropped = 0;
  for (let index = 1; index < lastUser; index++) {
    dropped += sizes[index - 1] ?? 0;
    if (
      history[index]?.role === "user" &&
      dropped >= drop &&
      history.length - index <= maxMessages
    )
      return index;
  }
  return Math.max(0, lastUser);
}

/**
 * A tool message's `{status, output}` content with the output cut to its
 * first lines and a note of what went.
 */
function elidedResult(content: string): string {
  let receipt: Record<string, unknown> = { status: "unknown" };
  let output = content;
  try {
    const parsed = JSON.parse(content) as Record<string, unknown>;
    if (
      typeof parsed.status === "string" &&
      typeof parsed.output === "string"
    ) {
      // Every other field (an external result's `source`) stays.
      receipt = parsed;
      output = parsed.output;
    }
  } catch {
    // Not the runtime's receipt: its status is unknown.
  }
  const end = output.lastIndexOf("\n", PREVIEW);
  const preview = output.slice(0, end > PREVIEW / 2 ? end : PREVIEW);
  return JSON.stringify({
    ...receipt,
    output: `${preview}\n[${output.length - preview.length} more ${ELIDED}`,
  });
}

/**
 * The todo list the model last wrote (its latest successful todo_write
 * result), and where it is: the plan of a long run, which shortening and
 * the window must not take from the model.
 */
export function latestTodo(
  history: readonly Message[],
): { index: number; text: string } | undefined {
  for (let index = history.length - 1; index >= 0; index--) {
    const message = history[index];
    if (message?.role !== "tool" || message.tool_name !== "todo_write")
      continue;
    try {
      const { status, output } = JSON.parse(message.content) as {
        status?: unknown;
        output?: unknown;
      };
      if (status === "succeeded" && typeof output === "string")
        return { index, text: output };
    } catch {
      // Not the runtime's receipt.
    }
  }
  return undefined;
}

/**
 * The todo list a windowed request carries: the latest, when something on
 * it is left to do, in at most `limit` characters. Done items give way
 * first, then pending ones from the end, "…" in their place; the item in
 * progress and the counts line stay. Only a limit too small for those is
 * cut mid-line.
 */
export function carriedTodo(
  history: readonly Message[],
  limit: number,
): { index: number; text: string } | undefined {
  const todo = latestTodo(history);
  if (!todo || !/^- \[[ ~]\] /m.test(todo.text)) return undefined;
  if (todo.text.length <= limit) return todo;
  const open = todo.text
    .split("\n")
    .filter((line) => !line.startsWith("- [x] "));
  if (open.join("\n").length <= limit)
    return { index: todo.index, text: open.join("\n") };
  const pending = (line: string) => line.startsWith("- [ ] ");
  // Each line takes its length and a newline, except the last: one more.
  let room =
    limit +
    1 -
    "\n…".length -
    open.reduce(
      (size, line) => (pending(line) ? size : size + line.length + 1),
      0,
    );
  const kept: string[] = [];
  let cut = false;
  for (const line of open) {
    if (pending(line) && (cut || line.length + 1 > room)) {
      if (!cut) kept.push("…");
      cut = true;
      continue;
    }
    if (pending(line)) room -= line.length + 1;
    kept.push(line);
  }
  return { index: todo.index, text: kept.join("\n").slice(0, limit) };
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
 * the model's current work and stay whole, as does its latest todo list
 * (at most 50 short items), and so does everything before
 * `from` (the part a windowed request leaves out: shortening it saves the
 * request nothing). With `state`, what is freed is measured as saved state
 * rather than as window: an image counts as its own length (not
 * IMAGE_CHARS), and the reasoning of earlier turns goes too (a request
 * leaves it out already, so only the state holds it). Messages are copied,
 * not changed.
 */
export function compactHistory(
  history: readonly Message[],
  chars: number,
  from = 0,
  { state = false }: { state?: boolean } = {},
): { messages: Message[]; elided: number; saved: number } {
  const messages = history.map((message) => ({ ...message }));
  const lastUser = messages.findLastIndex((message) => message.role === "user");
  const lastAssistant = messages.findLastIndex(
    (message) => message.role === "assistant",
  );
  const plan = latestTodo(messages)?.index;
  const kept = (index: number) =>
    index < from ||
    index === lastUser ||
    index === plan ||
    index >= lastAssistant;
  const changed = new Set<number>();
  let saved = 0;
  const passes: ((message: Message, index: number) => number)[] = [
    (message) => {
      if (!message.images?.length) return 0;
      const freed = state
        ? message.images.reduce((total, image) => total + image.length, 0)
        : message.images.length * IMAGE_CHARS;
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
      if (
        message.role !== "assistant" ||
        !message.thinking ||
        (index < lastUser && !state)
      )
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

/** The UTF-8 bytes `history` takes as saved state (its JSON). */
export function stateBytes(history: readonly Message[]): number {
  return Buffer.byteLength(JSON.stringify(history), "utf8");
}

/**
 * `history` shortened to `target` bytes of saved state when it passes
 * `soft` (else untouched): all of it, from the start, since the state keeps
 * what a request's window leaves out. Images go first, then results the
 * model has read, then old reasoning (compactHistory); the current work
 * stays whole, so the result may still be over `target`. Goes on only as
 * long as something more can be freed.
 */
export function compactState(
  history: readonly Message[],
  soft = STATE_SOFT_BYTES,
  target = STATE_TARGET_BYTES,
): { messages: Message[]; elided: number; before: number; after: number } {
  const before = stateBytes(history);
  if (before <= soft)
    return { messages: history.slice(), elided: 0, before, after: before };
  let messages = history.slice();
  let after = before;
  while (after > target) {
    // Characters stand for bytes only roughly (wide text, JSON escapes):
    // ask for what is missing, then look again.
    const next = compactHistory(messages, after - target, 0, { state: true });
    if (next.saved <= 0) break;
    messages = next.messages;
    after = stateBytes(messages);
  }
  // compactHistory copies every message: what changed is what differs.
  const elided = messages.filter(
    (message, index) =>
      message.content !== history[index]?.content ||
      message.thinking !== history[index]?.thinking ||
      message.images !== history[index]?.images,
  ).length;
  return { messages, elided, before, after };
}

/** What `compactNow` did to a conversation. */
export type CompactedNow = {
  /** The whole new history; a copy of `history` when nothing could be shortened. */
  messages: Message[];
  /** Messages whose content, images or reasoning were shortened. */
  elided: number;
  /** The history's saved size (its JSON, in UTF-8 bytes), before and after. */
  bytesBefore: number;
  bytesAfter: number;
  /** The prompt's size, estimated as `compacted` events estimate it. */
  tokensBefore: number;
  tokensAfter: number;
};

/**
 * Shortens a conversation now, on the caller's word, rather than when a
 * request nears the window: everything compaction frees (images, results
 * the model has read, old reasoning), from the start, keeping the current
 * work whole (compactHistory). With `session`, the shortened history is
 * saved to it at once (compare-and-swap, as a turn's checkpoint is) and
 * `session.state` becomes the saved state, so the next turn finds the
 * client's copy and the durable one the same. Nothing is saved when
 * nothing can be shortened. Call it only between turns: a running turn
 * owns the session.
 *
 * Throws, saving nothing, when `history` does not extend the session's
 * saved messages (the caller's copy is not the session's) or the save is
 * refused (another writer moved the session on).
 */
export function compactNow(
  given: readonly Message[],
  session?: SessionContext,
): CompactedNow {
  // As a turn reads it (runAgent), so the same copy compares equal to the
  // saved state whatever order its fields were built in.
  const history = MessageSchema.array().parse(given);
  const { messages, elided, before, after } = compactState(history, 0, 0);
  const estimate = (list: readonly Message[]) =>
    Math.round(promptChars(list) / CHARS_PER_TOKEN);
  if (elided === 0)
    return {
      messages: history.slice(),
      elided: 0,
      bytesBefore: before,
      bytesAfter: before,
      tokensBefore: estimate(history),
      tokensAfter: estimate(history),
    };
  if (session) {
    const saved = MessageSchema.array().parse(session.state.messages);
    if (
      JSON.stringify(history.slice(0, saved.length)) !== JSON.stringify(saved)
    )
      throw new Error("Session history differs from its durable state");
    session.state = session.store.saveStateCAS(
      session.state.id,
      session.state.workspaceId,
      session.state.revision,
      messages,
    );
  }
  return {
    messages,
    elided,
    bytesBefore: before,
    bytesAfter: after,
    tokensBefore: estimate(history),
    tokensAfter: estimate(messages),
  };
}
