import { expect, test } from "vitest";
import { type MessageView, NO_CARD } from "#cli/tuimark/blocks";
import {
  DEFAULT_WINDOW,
  Transcript,
  WINDOW_STEP,
} from "#cli/tuimark/transcript";

const make = (n: number): MessageView => ({
  id: `m${n}`,
  is_user: true,
  is_asst: false,
  is_tool: false,
  is_notice: false,
  blocks: [],
  card: NO_CARD,
});
const fill = (t: Transcript, n: number) => {
  for (let i = 0; i < n; i++) t.push(make(i));
};

test("defaults are the Phase 0 numbers", () => {
  expect(DEFAULT_WINDOW).toBe(200);
  expect(WINDOW_STEP).toBe(100);
});

test("the store holds only the newest messages and counts the rest", () => {
  const t = new Transcript();
  fill(t, 450);
  const { messages, earlier } = t.snapshot();
  expect(messages).toHaveLength(200);
  expect(messages[0]?.id).toBe("m250");
  expect(messages.at(-1)?.id).toBe("m449");
  expect(earlier.n).toBe(250);
});

test("widening adds 100 messages at a time until everything is shown", () => {
  const t = new Transcript();
  fill(t, 450);
  expect(t.widen()).toBe(true);
  expect(t.snapshot().earlier.n).toBe(150);
  expect(t.widen()).toBe(true);
  expect(t.snapshot().earlier.n).toBe(50);
  expect(t.widen()).toBe(true);
  expect(t.snapshot().earlier.n).toBe(0);
  expect(t.widen()).toBe(false);
  expect(t.snapshot().messages).toHaveLength(450);
});

test("a short history is shown whole, and pop takes back the newest view", () => {
  const t = new Transcript();
  fill(t, 3);
  expect(t.snapshot().earlier.n).toBe(0);
  expect(t.pop()?.id).toBe("m2");
  expect(t.length).toBe(2);
  expect(new Transcript().pop()).toBeUndefined();
});

test("a window below 1 is refused", () => {
  expect(() => new Transcript(100, 0)).toThrow(RangeError);
  expect(() => new Transcript(0, 10)).toThrow(RangeError);
});

test("replace drops the old conversation and resets the window", () => {
  const t = new Transcript(100, 2);
  fill(t, 5);
  expect(t.widen()).toBe(true);
  t.replace([make(90), make(91), make(92)]);
  expect(t.length).toBe(3);
  expect(t.all().map((view) => view.id)).toEqual(["m90", "m91", "m92"]);
  // The window is back to its first size: only the newest two show.
  expect(t.snapshot().messages.map((view) => view.id)).toEqual(["m91", "m92"]);
  expect(t.snapshot().earlier.n).toBe(1);
  t.replace([]);
  expect(t.length).toBe(0);
  expect(t.snapshot()).toEqual({ messages: [], earlier: { n: 0 } });
});
