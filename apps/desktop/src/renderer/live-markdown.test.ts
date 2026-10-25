import { describe, expect, test } from "vitest";
import { reactive } from "vue";
import { type Frames, liveMarkdown } from "#desktop/renderer/live-markdown";

function fakeFrames() {
  const callbacks = new Map<number, () => void>();
  let next = 0;
  const frames: Frames = {
    request(callback) {
      callbacks.set(++next, callback);
      return next;
    },
    cancel(id) {
      callbacks.delete(id);
    },
  };
  const flush = () => {
    const due = [...callbacks.values()];
    callbacks.clear();
    for (const callback of due) callback();
  };
  return { frames, flush, waiting: () => callbacks.size };
}

describe("liveMarkdown", () => {
  test("many deltas in one frame render once, with the text as it is then", () => {
    const state = reactive({ liveText: "" });
    const { frames, flush, waiting } = fakeFrames();
    const live = liveMarkdown(() => state.liveText, frames);
    for (const piece of ["Hel", "lo ", "**wor", "ld**"])
      state.liveText += piece;
    expect(waiting()).toBe(1);
    expect(live.html.value).toBe("");
    flush();
    expect(live.html.value).toContain("<strong>world</strong>");
    state.liveText += "!";
    expect(waiting()).toBe(1);
    flush();
    expect(live.html.value).toContain("world</strong>!");
    live.stop();
  });

  test("an emptied reply clears at once and drops the frame it was waiting for", () => {
    const state = reactive({ liveText: "first reply" });
    const { frames, flush, waiting } = fakeFrames();
    const live = liveMarkdown(() => state.liveText, frames);
    flush();
    expect(live.html.value).toContain("first reply");
    state.liveText += " goes on";
    state.liveText = "";
    expect(live.html.value).toBe("");
    expect(waiting()).toBe(0);
    state.liveText = "second";
    flush();
    expect(live.html.value).toContain("second");
    expect(live.html.value).not.toContain("first");
    live.stop();
  });

  test("stop leaves no frame behind and ignores later text", () => {
    const state = reactive({ liveText: "" });
    const { frames, waiting } = fakeFrames();
    const live = liveMarkdown(() => state.liveText, frames);
    state.liveText = "text";
    live.stop();
    expect(waiting()).toBe(0);
    state.liveText = "more";
    expect(waiting()).toBe(0);
  });
});
