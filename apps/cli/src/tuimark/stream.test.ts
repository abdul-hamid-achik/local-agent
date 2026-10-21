import { expect, test } from "vitest";
import {
  type Block,
  MAX_MESSAGE_BLOCKS,
  parseBlocks,
  runsText,
  THINK_KEEP,
} from "#cli/tuimark/blocks";
import { applyWrite, batchBytes } from "#cli/tuimark/fake-host";
import { LiveStream, REPARSE_MS, streamingView } from "#cli/tuimark/stream";
import { fixtureTokens } from "#cli/tuimark/stream-bytes";

interface Live {
  active: boolean;
  blocks: Block[];
  tail: Block[];
}

/** A stream with a clock the test moves, and a store the writes are applied to. */
function rig() {
  let now = 0;
  const stream = new LiveStream(() => now);
  const store: Record<string, unknown> = {
    live: { active: false, blocks: [], tail: [] },
    thinking: { on: false, runs: [] },
  };
  const log: { path: string; op?: string }[][] = [];
  let bytes = 0;
  const drain = () => {
    const writes = stream.drain();
    if (writes.length === 0) return writes;
    log.push(writes.map((w) => ({ path: w.path, op: w.op })));
    bytes += batchBytes(writes, log.length);
    for (const w of writes) applyWrite(store, w);
    return writes;
  };
  return {
    stream,
    store,
    log,
    drain,
    bytes: () => bytes,
    advance: (ms: number) => {
      now += ms;
    },
    live: () => store.live as Live,
    thinking: () => store.thinking as { on: boolean; runs: unknown[] },
  };
}

/** What a block list shows, without ids (a stream numbers its blocks `l0`, `l1`, ...). */
const shape = (blocks: Block[]) => blocks.map(({ id: _id, ...rest }) => rest);

const MARKDOWN = [
  "## Plan",
  "",
  "First a paragraph with **bold**, `code` and a [link](https://example.com)",
  "that wraps onto a second line.",
  "",
  "- one",
  "- two",
  "  continued",
  "",
  "> a quote",
  "",
  "| a | b |",
  "|:--|--:|",
  "| 1 | 2 |",
  "",
  "```ts",
  "const x = 1;",
  "",
  "const y = 2;",
  "```",
  "",
  "The end.",
].join("\n");

/** A small deterministic generator, so a failure repeats. */
function prng(seed: number) {
  let state = seed;
  return () => {
    state = (state * 1664525 + 1013904223) % 4294967296;
    return state / 4294967296;
  };
}

test("a stream split anywhere ends with the blocks parseBlocks makes of the whole text", () => {
  for (let seed = 1; seed <= 40; seed++) {
    const next = prng(seed);
    const r = rig();
    for (let i = 0; i < MARKDOWN.length; ) {
      const size = 1 + Math.floor(next() * 9);
      r.stream.pushText(MARKDOWN.slice(i, i + size));
      i += size;
      r.advance(next() < 0.3 ? REPARSE_MS : 5);
      r.drain();
    }
    // The stream goes quiet: a tail with raw text on its end is parsed again by itself.
    r.advance(REPARSE_MS);
    r.drain();
    const live = r.live();
    expect(live.active).toBe(true);
    expect(shape([...live.blocks, ...live.tail])).toEqual(
      shape(parseBlocks(MARKDOWN, "l")),
    );
    expect(r.stream.dirty).toBe(false);
  }
});

test("final blocks are only ever appended, and the first text writes the live message whole", () => {
  const r = rig();
  const text = MARKDOWN;
  for (let i = 0; i < text.length; i += 7) {
    r.stream.pushText(text.slice(i, i + 7));
    r.advance(40);
    r.drain();
  }
  const writes = r.log.flat();
  expect(writes.filter((w) => w.path === "live")).toHaveLength(1);
  for (const w of writes.filter((w) => w.path === "live.blocks"))
    expect(w.op).toBe("append");
  // Between parses the text goes on the end of the last tail block.
  expect(
    writes.some(
      (w) => /^live\.tail\.\d+\.runs$/.test(w.path) && w.op === "append",
    ),
  ).toBe(true);
});

test("a block is parsed again only when a line ends or after REPARSE_MS", () => {
  const r = rig();
  r.stream.pushText("Hello");
  r.drain();
  r.advance(5);
  r.stream.pushText(" there");
  const quick = r.drain();
  expect(quick.map((w) => [w.path, w.op])).toEqual([
    ["live.tail.0.runs", "append"],
  ]);
  r.advance(5);
  r.stream.pushText(" you\n");
  expect(r.drain().map((w) => w.path)).toContain("live.tail");
  r.advance(5);
  r.stream.pushText("again");
  r.drain();
  r.advance(REPARSE_MS);
  r.stream.pushText("!");
  expect(r.drain().map((w) => w.path)).toContain("live.tail");
});

test("a half-written table row does not end the table before it", () => {
  const r = rig();
  r.stream.pushText("| a | b |\n|");
  r.drain();
  expect(r.live().blocks).toHaveLength(0);
  r.advance(REPARSE_MS);
  r.stream.pushText(":--|--:|\n| 1 | 2 |\n\nafter\n");
  r.drain();
  const live = r.live();
  expect(live.blocks.map((b) => b.lang)).toEqual(["table"]);
  expect(runsText(live.tail.at(-1)?.runs ?? []).trim()).toBe("after");
});

test("a tail with raw text on its end is parsed again once the stream goes quiet", () => {
  const r = rig();
  r.stream.pushText("Say **hi");
  r.drain();
  r.advance(5);
  r.stream.pushText(" there**");
  r.drain();
  expect(r.live().tail[0]?.runs).toEqual(["Say **hi", " there**"]);
  expect(r.stream.dirty).toBe(false);
  r.advance(REPARSE_MS);
  expect(r.stream.dirty).toBe(true);
  r.drain();
  expect(r.live().tail[0]?.runs).toEqual([
    "Say ",
    { text: "hi there", class: "b" },
  ]);
  expect(r.stream.dirty).toBe(false);
});

test("2000 tokens take about a hundred bytes each on fd 3, not thousands", () => {
  const r = rig();
  const pieces = fixtureTokens(2000);
  // 100 tokens a second, a tick every 33 ms.
  let ticked = 0;
  for (const piece of pieces) {
    r.stream.pushText(piece);
    r.advance(10);
    ticked += 10;
    if (ticked >= 33) {
      ticked = 0;
      r.drain();
    }
  }
  r.advance(REPARSE_MS);
  r.drain();
  expect(r.bytes() / pieces.length).toBeLessThan(200);
  const live = r.live();
  expect(shape([...live.blocks, ...live.tail])).toEqual(
    shape(parseBlocks(pieces.join(""), "l")),
  );
});

test("the reasoning is appended, cut back to its end, and gone when the answer starts", () => {
  const r = rig();
  const line = "step of the plan that is long enough to add up\n";
  let sent = "";
  for (let n = 0; n < 200; n++) {
    r.stream.pushThinking(line);
    sent += line;
    r.advance(33);
    r.drain();
    const held = runsText(r.thinking().runs as string[]).length;
    expect(held).toBeLessThanOrEqual(THINK_KEEP + line.length);
    expect(sent.endsWith(runsText(r.thinking().runs as string[]))).toBe(true);
  }
  expect(r.thinking().on).toBe(true);
  expect(r.log.flat().filter((w) => w.op === "append").length).toBeGreaterThan(
    50,
  );
  // The answer starts: the block goes in the same write as the first text.
  r.stream.pushText("Answer");
  r.advance(33);
  const writes = r.drain();
  expect(writes.map((w) => w.path)).toEqual(["thinking", "live"]);
  expect(r.thinking()).toEqual({ on: false, runs: [] });
  // Reasoning after the answer began is ignored.
  r.stream.pushThinking("late");
  r.advance(33);
  expect(r.drain()).toEqual([]);
});

test("after markStale the next drain sends the live message and the reasoning whole", () => {
  const r = rig();
  r.stream.pushText("first paragraph\n\nsecond");
  r.advance(40);
  r.drain();
  r.stream.pushText(" half");
  r.advance(40);
  r.drain();
  // Pretend the last write was refused: wipe what the store holds of it.
  r.store.live = { active: false, blocks: [], tail: [] };
  r.stream.markStale();
  expect(r.stream.dirty).toBe(true);
  const writes = r.drain();
  expect(writes.map((w) => w.path)).toEqual(["live", "thinking"]);
  const live = r.live();
  expect(shape([...live.blocks, ...live.tail])).toEqual(
    shape(parseBlocks("first paragraph\n\nsecond half", "l")),
  );
});

test("a reply past the view's bounds says so once and stops following", () => {
  const r = rig();
  for (let n = 0; n < MAX_MESSAGE_BLOCKS + 20; n++) {
    r.stream.pushText(`paragraph ${n}\n\n`);
    r.advance(40);
    r.drain();
  }
  const live = r.live();
  expect(live.blocks.length).toBeLessThanOrEqual(MAX_MESSAGE_BLOCKS + 1);
  expect(runsText(live.blocks.at(-1)?.runs ?? [])).toContain(
    "when it is finished",
  );
  expect(live.tail).toEqual([]);
  r.stream.pushText("more");
  r.advance(40);
  expect(r.drain()).toEqual([]);
});

test("reset forgets everything and streamingView is the first write of a stream", () => {
  const r = rig();
  r.stream.pushText("text\n\nmore");
  r.advance(40);
  r.drain();
  r.stream.reset();
  expect(r.stream.dirty).toBe(false);
  expect(r.stream.drain()).toEqual([]);
  const view = streamingView("Plan:\n\n```ts\nconst x = 1;");
  expect(view.active).toBe(true);
  expect(view.blocks.map((b) => b.para)).toEqual([true]);
  expect(view.tail.map((b) => b.code)).toEqual([true]);
});
