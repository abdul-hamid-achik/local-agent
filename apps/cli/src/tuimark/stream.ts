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

import {
  type Block,
  cleanText,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  MAX_MESSAGE_BLOCKS,
  MAX_MESSAGE_CHARS,
  parseBlocksAt,
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

const PREFIX = "l";

function liveWith(blocks: Block[], tail: Block[]): LiveView {
  return { ...IDLE_LIVE, active: true, blocks, tail };
}

export class LiveStream {
  /** Source of the unfinished blocks, from the first line of the first of them. */
  private pending = "";
  /** Text that arrived since the last drain, cleaned. */
  private unsent = "";
  /** The final blocks, kept so a refused write can be made good. */
  private blocks: Block[] = [];
  private tail: Block[] = [];
  private chars = 0;
  private cut = false;
  /** What the store holds now. */
  private inStore = { live: false, tail: 0, thinking: false, thinkChars: 0 };
  private lastParse = Number.NEGATIVE_INFINITY;
  /** The store's tail has text appended raw since it was last parsed. */
  private raw = false;
  private stale = false;
  // the reasoning
  private think = "";
  private thinkUnsent = "";
  private answered = false;

  constructor(private readonly now: () => number = () => performance.now()) {}

  /** The store holds the idle live message and no reasoning; nothing is pending. */
  reset(): void {
    this.pending = "";
    this.unsent = "";
    this.blocks = [];
    this.tail = [];
    this.chars = 0;
    this.cut = false;
    this.inStore = { live: false, tail: 0, thinking: false, thinkChars: 0 };
    this.lastParse = Number.NEGATIVE_INFINITY;
    this.raw = false;
    this.stale = false;
    this.think = "";
    this.thinkUnsent = "";
    this.answered = false;
  }

  /** True while there is something to write. */
  get dirty(): boolean {
    return (
      this.stale ||
      this.unsent !== "" ||
      this.thinkUnsent !== "" ||
      (this.answered && this.inStore.thinking) ||
      // Text went on the end of the tail unparsed and the stream has gone quiet.
      (this.raw && this.now() - this.lastParse >= REPARSE_MS)
    );
  }

  /** A piece of the answer. */
  pushText(text: string): void {
    if (!text) return;
    this.answered = true;
    if (this.cut) return;
    const clean = cleanText(text);
    this.unsent += clean;
    this.pending += clean;
    this.chars += clean.length;
  }

  /** A piece of the model's reasoning; it is ignored once the answer has started. */
  pushThinking(text: string): void {
    if (!text || this.answered) return;
    const clean = cleanText(text);
    this.think += clean;
    this.thinkUnsent += clean;
  }

  /** A write failed: the next drain sends everything again. */
  markStale(): void {
    this.stale = true;
  }

  /** The writes that bring the store up to date, or none. */
  drain(): Write[] {
    if (this.stale) return this.resync();
    const writes: Write[] = [];
    this.drainThinking(writes);
    this.drainAnswer(writes);
    return writes;
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
      writes.push({ path: "live", value: liveWith(this.blocks, this.tail) });
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
    const due =
      quiet ||
      !this.inStore.live ||
      this.inStore.tail === 0 ||
      delta.includes("\n") ||
      t - this.lastParse >= REPARSE_MS;
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
      writes.push({ path: "live", value: liveWith(this.blocks, this.tail) });
      return;
    }
    for (const block of this.blocks.slice(before))
      writes.push({ path: "live.blocks", value: block, op: "append" });
    writes.push({ path: "live.tail", value: this.tail });
    this.inStore.tail = this.tail.length;
  }

  /**
   * Splits `text` into final blocks (moved to `this.blocks`) and the tail. Only
   * complete lines decide what is final: a half-written line can still change
   * the block before it (a `|` that becomes a table row).
   */
  private parse(text: string): void {
    const lines = cleanText(text).split("\n");
    const complete = lines.slice(0, -1).join("\n");
    let cutLine = lines.length - 1;
    if (complete.trim() !== "") {
      const { blocks, starts } = parseBlocksAt(
        complete,
        PREFIX,
        this.blocks.length,
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
      const note = parseBlocksAt(
        `> … the rest of this reply is shown when it is finished`,
        PREFIX,
        this.blocks.length,
      ).blocks;
      this.blocks.push(...note);
      this.tail = [];
      this.pending = "";
      this.cut = true;
      return;
    }
    this.tail = this.pending.trim()
      ? parseBlocksAt(this.pending, PREFIX, this.blocks.length).blocks
      : [];
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
