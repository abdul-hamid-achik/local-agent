// The live message as the writes that keep the store up to date.
//
// A streamed reply used to be parsed again as a whole and written again as a
// whole on every tick, so the bytes on fd 3 grew with the square of the reply.
// Now the store holds the reply in two parts, and this class writes each with
// the cheapest write that keeps it right:
//
//   live.blocks   the blocks that are final. A block is parsed once, when a
//                 later block has started, and `append`ed.
//   live.tail     the block or two still being written, parsed from the line
//                 the first of them starts at. Between parses the new text is
//                 `append`ed to the last block's runs as a plain run; the tail
//                 is parsed and set again when a line ends or REPARSE_MS has
//                 passed, so inline marks and fences catch up at a bounded rate.
//
// The reasoning streams the same way into thinking.runs, and the markup shows
// the end of it in a three-row scroll that sticks to the bottom.
//
// The class never writes; `drain()` returns the writes and the controller sends
// them. If a write is refused the store no longer matches what this class
// thinks it holds: `markStale()` makes the next drain send everything.

import { terminalText } from "#cli/text";
import {
  type Block,
  cleanText,
  IDLE_LIVE,
  IDLE_THINKING,
  type LinkRef,
  type LiveView,
  MAX_MESSAGE_BLOCKS,
  MAX_MESSAGE_CHARS,
  parseBlocksAt,
  startsOpenFence,
  THINK_KEEP,
  type ThinkingView,
  thinkingTail,
  thinkingView,
} from "#cli/tuimark/blocks";

export interface Write {
  path: string;
  value: unknown;
  op?: "set" | "append";
}

/** The longest a tail goes without being parsed while text arrives, in ms. */
export const REPARSE_MS = 250;
/**
 * The same for a code block that is not closed yet. A line ending is no reason
 * to parse it again (it grows by a line every few tokens, and parsing it sends
 * the whole block), so it is parsed again on this period, or when it closes.
 */
export const FENCE_REPARSE_MS = 1500;

/** How often the controller flushes the live message, in ms (about 30 Hz). */
export const FLUSH_MS = 33;

/**
 * A refused commit is offered again after this long, and after twice as long
 * for each further refusal, up to COMMIT_RETRY_MAX_MS: a host that keeps
 * refusing is not asked at the flush rate.
 */
export const COMMIT_RETRY_MS = 100;
export const COMMIT_RETRY_MAX_MS = 5000;

const PREFIX = "l";

/**
 * Where a piece of text ends in something that is not whole yet: the high half
 * of an emoji, or an escape sequence (`ESC`, `ESC [ 3`, `ESC ] 0 ; title`)
 * still waiting for its end. Cleaned alone, the escape's ESC would go and the
 * rest of it would show. Only the last OPEN_END_MAX units are looked at, so a
 * longer sequence is not waited for.
 */
const OPEN_END =
  // biome-ignore lint/suspicious/noControlCharactersInRegex: ESC and BEL are what the pattern looks for
  /(?:[\ud800-\udbff]|\u001b(?:\[[0-?]*[ -/]*|\][^\u0007\u001b]*|[ -/]*)?)$/;
const OPEN_END_MAX = 64;

/** Splits `text` into what is whole and the end that waits for the next piece. */
function splitOpenEnd(text: string): [whole: string, open: string] {
  const at = Math.max(0, text.length - OPEN_END_MAX);
  const m = OPEN_END.exec(text.slice(at));
  if (!m) return [text, ""];
  return [text.slice(0, at + m.index), text.slice(at + m.index)];
}

function liveWith(blocks: Block[], tail: Block[], links: LinkRef[]): LiveView {
  return { ...IDLE_LIVE, active: true, blocks, tail, links };
}

export class LiveStream {
  /** Source of the unfinished blocks, from the first line of the first of them. */
  private pending = "";
  /** Text that arrived since the last drain, cleaned. */
  private unsent = "";
  /** The end of the last piece that is not whole yet (see `splitOpenEnd`), per stream. */
  private openAnswer = "";
  private openThinking = "";
  /**
   * The reply's link targets so far. The live blocks carry the body's [n]
   * markers, and the committed view renders the same sources row from this
   * list (deduped by url, so a tail parsed again keeps its numbers).
   */
  private linkRefs: LinkRef[] = [];
  /** How many of `linkRefs` the store already holds. */
  private linksSent = 0;
  /** The final blocks, kept so a refused write can be made good. */
  private blocks: Block[] = [];
  private tail: Block[] = [];
  private chars = 0;
  private cut = false;
  /** Where the truncation note starts in `blocks`, and how many blocks it has. */
  private cutAt = -1;
  private cutLength = 0;
  /** What the store holds now. */
  private inStore = { live: false, tail: 0, thinking: false, thinkChars: 0 };
  private lastParse = Number.NEGATIVE_INFINITY;
  /** The store's tail has text appended raw since it was last parsed. */
  private raw = false;
  /** The tail starts with a code block that is not closed. */
  private fence = false;
  private stale = false;
  // the reasoning
  private think = "";
  private thinkUnsent = "";
  private answered = false;
  // the inline commits: final blocks are offered to the controller, which
  // prints them into the scrollback and confirms them only after the ack.
  // A committed block can never be taken back, so nothing above
  // `committedCount` is ever emitted to the store again, and nothing at or
  // below it is ever offered again.
  private committedCount = 0;
  private offeredCount = 0;
  /** Refused commits in a row, and when the next attempt may be made. */
  private refusals = 0;
  private retryAt = 0;
  private readonly inline: boolean;

  /** The width a table may take, read at each parse (a stream is written once). */
  private readonly tableCols?: () => number;

  constructor(
    private readonly now: () => number = () => performance.now(),
    options: { inline?: boolean; tableCols?: () => number } = {},
  ) {
    this.inline = options.inline ?? false;
    this.tableCols = options.tableCols;
  }

  /** The store holds the idle live message and no reasoning; nothing is pending. */
  reset(): void {
    this.pending = "";
    this.unsent = "";
    this.openAnswer = "";
    this.openThinking = "";
    this.linkRefs = [];
    this.linksSent = 0;
    this.blocks = [];
    this.tail = [];
    this.chars = 0;
    this.cut = false;
    this.cutAt = -1;
    this.cutLength = 0;
    this.inStore = { live: false, tail: 0, thinking: false, thinkChars: 0 };
    this.lastParse = Number.NEGATIVE_INFINITY;
    this.raw = false;
    this.fence = false;
    this.stale = false;
    this.think = "";
    this.thinkUnsent = "";
    this.answered = false;
    this.committedCount = 0;
    this.offeredCount = 0;
    this.refusals = 0;
    this.retryAt = 0;
  }

  /** The reply's link targets, as the body's [n] markers number them. */
  get links(): LinkRef[] {
    return this.linkRefs;
  }

  /** True while there is something to write. */
  get dirty(): boolean {
    return (
      this.stale ||
      this.unsent !== "" ||
      this.thinkUnsent !== "" ||
      (this.answered && this.inStore.thinking) ||
      // Text went on the end of the tail unparsed and the stream has gone quiet.
      (this.raw && this.now() - this.lastParse >= REPARSE_MS) ||
      // Final blocks nobody has committed yet (a refused commit retries, later).
      (this.inline &&
        this.commitDue &&
        this.blocks.length > this.committedCount + this.offeredCount)
    );
  }

  /** A piece of the answer. */
  pushText(text: string): void {
    if (!text) return;
    this.answered = true;
    if (this.cut) return;
    const [whole, open] = splitOpenEnd(this.openAnswer + text);
    this.openAnswer = open;
    const clean = cleanText(whole);
    this.unsent += clean;
    // The source keeps its tabs: a code block shows them at tab stops.
    this.pending += terminalText(whole);
    this.chars += clean.length;
  }

  /** A piece of the model's reasoning; it is ignored once the answer has started. */
  pushThinking(text: string): void {
    if (!text || this.answered) return;
    const [whole, open] = splitOpenEnd(this.openThinking + text);
    this.openThinking = open;
    const clean = cleanText(whole);
    this.think += clean;
    this.thinkUnsent += clean;
  }

  /** A write failed: the next drain sends everything again. */
  markStale(): void {
    this.stale = true;
  }

  /** Final blocks whose commit the host has acked (inline only). */
  get committed(): number {
    return this.committedCount;
  }

  /** True while this reply has text the scrollback does not have yet. */
  get hasUncommitted(): boolean {
    return (
      this.blocks.length > this.committedCount || this.pending.trim() !== ""
    );
  }

  /**
   * The final blocks not yet offered for committing. Offered blocks are not
   * offered again: the caller confirms them after the ack
   * (`confirmCommitted`) or hands them back after a refusal (`releaseFinal`).
   */
  takeFinal(): Block[] {
    const fresh = this.blocks.slice(this.committedCount + this.offeredCount);
    this.offeredCount += fresh.length;
    return fresh;
  }

  /**
   * Hands offered blocks back after a refused commit, for a later attempt: each
   * refusal in a row doubles the wait (`commitDue`).
   */
  releaseFinal(count: number): void {
    this.offeredCount = Math.max(0, this.offeredCount - count);
    this.retryAt =
      this.now() +
      Math.min(COMMIT_RETRY_MAX_MS, COMMIT_RETRY_MS * 2 ** this.refusals);
    this.refusals++;
  }

  /** False while a refused commit is waiting out its delay. */
  get commitDue(): boolean {
    return this.now() >= this.retryAt;
  }

  /**
   * Marks `count` offered blocks as printed (the commit's ack arrived) and
   * returns the write that drops them from the store's live message, where
   * they were showing until now.
   */
  confirmCommitted(count: number): Write[] {
    this.committedCount += count;
    this.offeredCount = Math.max(0, this.offeredCount - count);
    this.refusals = 0;
    this.retryAt = 0;
    if (!this.inStore.live) return [];
    return [{ path: "live.blocks", value: this.visibleBlocks() }];
  }

  /**
   * Everything this reply still owes the scrollback, at a boundary where the
   * live message ends (the finished message, a tool call, a cancel, a quit):
   * parses what is pending, promotes the tail to final blocks, and offers
   * the uncommitted rest. The stream is left drained. The truncation note of
   * the live region is not part of it: that line is region-only. `stopped`
   * says no finished message follows (a cancel, a failure): fullscreen, the
   * note then stops promising one.
   */
  finishTail(stopped = false): Block[] {
    if (!this.cut) {
      this.parse(this.pending);
      if (!this.cut && this.tail.length > 0) {
        this.blocks.push(...this.tail);
        this.tail = [];
      }
    }
    // After the parse: the cut may have happened just now.
    if (
      stopped &&
      !this.inline &&
      this.cutAt >= this.committedCount + this.offeredCount
    ) {
      const note = parseBlocksAt(
        `> … the rest of this reply is not shown: the turn stopped before it finished`,
        PREFIX,
        this.cutAt,
      ).blocks;
      this.blocks.splice(this.cutAt, this.cutLength, ...note);
      this.cutLength = note.length;
    }
    this.pending = "";
    this.unsent = "";
    // A piece that never finished (an emoji half, an escape) is not text.
    this.openAnswer = "";
    this.openThinking = "";
    this.raw = false;
    return this.takeFinal();
  }

  /** The writes that bring the store up to date, or none. */
  drain(): Write[] {
    if (this.stale) return this.resync();
    const writes: Write[] = [];
    this.drainThinking(writes);
    this.drainAnswer(writes);
    return writes;
  }

  /** The final blocks the store's live message may still show (uncommmitted). */
  private visibleBlocks(): Block[] {
    return this.inline ? this.blocks.slice(this.committedCount) : this.blocks;
  }

  /** Both values whole, as a first write or after a refused one. */
  private resync(): Write[] {
    this.stale = false;
    this.raw = false;
    this.unsent = "";
    this.thinkUnsent = "";
    this.parse(this.pending);
    this.lastParse = this.now();
    const writes: Write[] = [];
    if (this.answered) {
      this.inStore.live = true;
      this.inStore.tail = this.tail.length;
      // Inline, a resync never re-emits a block whose commit was acked: it is
      // already in the terminal's scrollback and would show twice.
      writes.push({
        path: "live",
        value: liveWith(this.visibleBlocks(), this.tail, this.linkRefs),
      });
      this.linksSent = this.linkRefs.length;
      this.inStore.thinking = false;
      this.inStore.thinkChars = 0;
      writes.push({ path: "thinking", value: IDLE_THINKING });
    } else {
      writes.push({ path: "live", value: IDLE_LIVE });
      this.inStore.live = false;
      this.inStore.tail = 0;
      const view: ThinkingView = this.think
        ? thinkingView(this.think)
        : IDLE_THINKING;
      this.inStore.thinking = view.on;
      this.inStore.thinkChars = this.think.length;
      writes.push({ path: "thinking", value: view });
    }
    return writes;
  }

  private drainThinking(writes: Write[]): void {
    if (this.answered) {
      // The answer ends the block.
      if (this.inStore.thinking) {
        writes.push({ path: "thinking", value: IDLE_THINKING });
        this.inStore.thinking = false;
        this.inStore.thinkChars = 0;
      }
      this.think = "";
      this.thinkUnsent = "";
      return;
    }
    if (!this.thinkUnsent) return;
    const delta = this.thinkUnsent;
    this.thinkUnsent = "";
    if (!this.inStore.thinking) {
      writes.push({ path: "thinking", value: thinkingView(delta) });
      this.inStore.thinking = true;
      this.inStore.thinkChars = delta.length;
    } else if (this.inStore.thinkChars + delta.length > THINK_KEEP) {
      // Only the end shows: cut the store's copy back to the last rows.
      this.think = thinkingTail(this.think);
      writes.push({ path: "thinking.runs", value: [this.think] });
      this.inStore.thinkChars = this.think.length;
    } else {
      writes.push({ path: "thinking.runs", value: delta, op: "append" });
      this.inStore.thinkChars += delta.length;
    }
  }

  private drainAnswer(writes: Write[]): void {
    const delta = this.unsent;
    this.unsent = "";
    const t = this.now();
    const quiet = delta === "" && this.raw && t - this.lastParse >= REPARSE_MS;
    if (delta === "" && !quiet) return;
    // A line ends: a block may have ended with it. An open code block only
    // matters once it closes.
    const lineEnd =
      delta.includes("\n") && (!this.fence || !startsOpenFence(this.pending));
    const due =
      quiet ||
      !this.inStore.live ||
      this.inStore.tail === 0 ||
      lineEnd ||
      t - this.lastParse >= (this.fence ? FENCE_REPARSE_MS : REPARSE_MS);
    if (!due) {
      // Between parses: the text goes on the end of the last block.
      this.raw = true;
      writes.push({
        path: `live.tail.${this.inStore.tail - 1}.runs`,
        value: delta,
        op: "append",
      });
      return;
    }
    this.raw = false;
    this.lastParse = t;
    const before = this.blocks.length;
    this.parse(this.pending);
    if (!this.inStore.live) {
      // The first text: the live message, whole.
      this.inStore.live = true;
      this.inStore.tail = this.tail.length;
      writes.push({
        path: "live",
        value: liveWith(this.visibleBlocks(), this.tail, this.linkRefs),
      });
      this.linksSent = this.linkRefs.length;
      return;
    }
    for (const block of this.blocks.slice(before))
      writes.push({ path: "live.blocks", value: block, op: "append" });
    writes.push({ path: "live.tail", value: this.tail });
    this.inStore.tail = this.tail.length;
    if (this.linksSent !== this.linkRefs.length) {
      // The sources row lives on the live message until the reply commits.
      writes.push({ path: "live.links", value: this.linkRefs });
      this.linksSent = this.linkRefs.length;
    }
  }

  /**
   * Splits `text` into final blocks (moved to `this.blocks`) and the tail. Only
   * complete lines decide what is final: a half-written line can still change
   * the block before it (a `|` that becomes a table row).
   */
  private parse(text: string): void {
    const lines = terminalText(text).split("\n");
    const complete = lines.slice(0, -1).join("\n");
    let cutLine = lines.length - 1;
    if (complete.trim() !== "") {
      const { blocks, starts } = parseBlocksAt(
        complete,
        PREFIX,
        this.blocks.length,
        this.linkRefs,
        this.tableCols?.(),
      );
      const last = blocks.length - 1;
      if (last >= 0) {
        for (const block of blocks.slice(0, last)) this.blocks.push(block);
        cutLine = starts[last] ?? 0;
      }
    }
    this.pending = lines.slice(cutLine).join("\n");
    if (
      this.blocks.length >= MAX_MESSAGE_BLOCKS ||
      this.chars > MAX_MESSAGE_CHARS
    ) {
      // Past the view's bounds: say so once and stop following the reply. The
      // history keeps all of it (the finished message goes through parseBlocks).
      if (!this.cut) {
        const note = parseBlocksAt(
          this.inline
            ? `> … the rest of this reply was left out of the live view`
            : `> … the rest of this reply is shown when it is finished`,
          PREFIX,
          this.blocks.length,
        ).blocks;
        this.cutAt = this.blocks.length;
        this.cutLength = note.length;
        this.blocks.push(...note);
      }
      this.cut = true;
      this.pending = "";
      // Inline, the note above is a final block and leaves for the scrollback;
      // the region keeps its own line saying the following stopped.
      this.tail = this.inline
        ? parseBlocksAt(
            `> … following stopped: this reply is too long for the live view`,
            PREFIX,
            this.blocks.length,
          ).blocks
        : [];
      return;
    }
    this.tail = this.pending.trim()
      ? parseBlocksAt(
          this.pending,
          PREFIX,
          this.blocks.length,
          this.linkRefs,
          this.tableCols?.(),
        ).blocks
      : [];
    this.fence = startsOpenFence(this.pending);
  }
}

/** A reply that is still streaming, as the store holds it after `text` has arrived. */
export function streamingView(text: string): LiveView {
  const stream = new LiveStream(() => 0);
  stream.pushText(text);
  const writes = stream.drain();
  const live = writes.find((w) => w.path === "live")?.value as
    | LiveView
    | undefined;
  return live ?? IDLE_LIVE;
}
