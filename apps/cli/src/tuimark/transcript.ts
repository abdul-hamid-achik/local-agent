// The full history lives here, in Bun. Tuimark only ever gets a window of it.
//
// Phase 0 measured the cost of a frame as O(messages in the store): about 8
// to 17 microseconds per message, so a 2 000-message session eats the 33 ms
// budget of a 30 Hz stream. The host therefore sends the last `window`
// messages plus a count of the ones left out, and the markup shows an
// "N earlier" row that widens the window. Until Tuimark has a virtualized
// `each` and an `append` write (0.4d) this is the whole story.

import type { MessageView } from "#cli/tuimark/blocks";

/** Messages shown at first (the Phase 0 number). */
export const DEFAULT_WINDOW = 200;
/** How many more messages "load earlier" brings in. */
export const WINDOW_STEP = 100;

export interface TranscriptSnapshot {
  messages: MessageView[];
  earlier: Earlier;
}

/** The messages above the window: how many, and the row's words for them. */
export interface Earlier {
  n: number;
  label: string;
}

export function earlier(n: number): Earlier {
  return { n, label: `${n} earlier message${n === 1 ? "" : "s"}` };
}

export class Transcript {
  private readonly views: MessageView[] = [];
  private shown: number;
  private readonly windowSize: number;

  constructor(
    private readonly step = WINDOW_STEP,
    window = DEFAULT_WINDOW,
  ) {
    if (window < 1 || step < 1)
      throw new RangeError("window and step must be >= 1");
    this.shown = window;
    this.windowSize = window;
  }

  get length(): number {
    return this.views.length;
  }

  push(...views: MessageView[]): void {
    this.views.push(...views);
  }

  /**
   * Drops every view and starts over from `views`, window back to its
   * first size (a session switch: the old conversation is gone whole).
   */
  replace(views: MessageView[]): void {
    this.views.length = 0;
    this.views.push(...views);
    this.shown = this.windowSize;
  }

  /** Removes and returns the newest view. */
  pop(): MessageView | undefined {
    return this.views.pop();
  }

  /**
   * Removes `view` wherever it is (a cancelled turn takes its prompt back,
   * even with a note pushed after it). False when it is not here.
   */
  remove(view: MessageView): boolean {
    const index = this.views.lastIndexOf(view);
    if (index < 0) return false;
    this.views.splice(index, 1);
    return true;
  }

  /** Shows `step` more messages. Returns false when everything is already shown. */
  widen(): boolean {
    if (this.shown >= this.views.length) return false;
    this.shown += this.step;
    return true;
  }

  /** Every view, oldest first (the inline client commits them all at startup). */
  all(): MessageView[] {
    return this.views.slice();
  }

  /** What the store holds: the newest `shown` views and how many are left out. */
  snapshot(): TranscriptSnapshot {
    const messages = this.views.slice(-this.shown);
    return {
      messages,
      earlier: earlier(this.views.length - messages.length),
    };
  }
}
