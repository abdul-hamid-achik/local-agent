import { expect, test } from "vitest";
import {
  type Block,
  type LinkRef,
  MAX_MESSAGE_BLOCKS,
  parseBlocks,
  runsText,
  THINK_KEEP,
} from "#cli/tuimark/blocks";
import { applyWrite, batchBytes } from "#cli/tuimark/fake-host";
import {
  COMMIT_RETRY_MAX_MS,
  COMMIT_RETRY_MS,
  FENCE_REPARSE_MS,
  FLUSH_MS,
  LiveStream,
  REPARSE_MS,
  streamingView,
} from "#cli/tuimark/stream";
import { fixtureTokens } from "#cli/tuimark/stream-fixture";

interface Live {
  active: boolean;
  blocks: Block[];
  tail: Block[];
  links: LinkRef[];
}

/** A stream with a clock the test moves, and a store the writes are applied to. */
function rig(options: { inline?: boolean; tableCols?: () => number } = {}) {
  let now = 0;
  const stream = new LiveStream(() => now, options);
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
  "* * *",
  "",
  "Setext title",
  "---",
  "A ![diagram](https://example.com/d.png) inline.",
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

/**
 * Text a split can hurt: emoji (a surrogate pair), CJK, colour and title
 * escapes, CRLF line ends and a tab. Cleaned, it reads as the markdown it is.
 */
const WIDE = [
  "## Résumé 日本語 😀",
  "",
  "Emoji 😀 and \u001b[31mred\u001b[0m text,\u001b]0;a title\u0007 a [リンク](https://example.com/日本) and **太字 😀**.",
  "",
  "- 項目 one",
  "- \u001b[1;32mtwo\u001b[m 🧑‍💻",
  "",
  "```ts",
  "const s = '😀';\tconst t = \u001b[33m'日本'\u001b[0m;",
  "```",
  "",
  "The end. 👍🏽",
].join("\r\n");

/** A small deterministic generator, so a failure repeats. */
function prng(seed: number) {
  let state = seed;
  return () => {
    state = (state * 1664525 + 1013904223) % 4294967296;
    return state / 4294967296;
  };
}

test("a stream split anywhere ends with the blocks parseBlocks makes of the whole text", () => {
  // Pieces of one to nine units cut an emoji, an escape and a CRLF in two.
  for (const text of [MARKDOWN, WIDE]) {
    for (let seed = 1; seed <= 40; seed++) {
      const next = prng(seed);
      const r = rig();
      for (let i = 0; i < text.length; ) {
        const size = 1 + Math.floor(next() * 9);
        r.stream.pushText(text.slice(i, i + size));
        i += size;
        r.advance(next() < 0.3 ? REPARSE_MS : 5);
        r.drain();
      }
      // The stream goes quiet: a tail with raw text on its end is parsed again by itself.
      r.advance(REPARSE_MS);
      r.drain();
      const live = r.live();
      expect(live.active).toBe(true);
      const links: LinkRef[] = [];
      expect(shape([...live.blocks, ...live.tail]), `seed ${seed}`).toEqual(
        shape(parseBlocks(text, "l", links)),
      );
      // The body's markers and the live view's sources row are one list.
      expect(live.links).toEqual(links);
      expect(r.stream.dirty).toBe(false);
    }
  }
});

test("a piece that ends in half an emoji or half an escape waits for the rest", () => {
  const r = rig();
  const seen = () =>
    runsText([...r.live().blocks, ...r.live().tail].flatMap((b) => b.runs));
  for (const piece of ["Say \ud83d", "\ude00 and \u001b[3", "1mred\u001b[0m"]) {
    r.stream.pushText(piece);
    r.advance(FLUSH_MS);
    r.drain();
    // What the host holds is never half an emoji, and no escape's text shows.
    expect(seen().isWellFormed()).toBe(true);
    expect(seen()).not.toMatch(/\[3/);
  }
  r.advance(REPARSE_MS);
  r.drain();
  expect(seen()).toBe("Say 😀 and red");
  // The same for the reasoning.
  const think = rig();
  const thought = () => runsText(think.thinking().runs as string[]);
  for (const piece of ["plan \ud83d", "\ude00 \u001b[3", "1mnow"]) {
    think.stream.pushThinking(piece);
    think.advance(FLUSH_MS);
    think.drain();
    expect(thought().isWellFormed()).toBe(true);
    expect(thought()).not.toMatch(/\[3/);
  }
  expect(thought()).toBe("plan 😀 now");
});

test("a piece that never finishes is dropped when the reply ends, not shown", () => {
  for (const unfinished of ["\ud83d", "\u001b[3"]) {
    const r = rig();
    r.stream.pushText("end ");
    r.stream.pushText(unfinished);
    expect(r.stream.finishTail().map((b) => runsText(b.runs))).toEqual([
      "end ",
    ]);
  }
});

test("a refused commit is offered again after a delay that doubles, and a confirmed one starts over", () => {
  const r = rig({ inline: true });
  r.stream.pushText("one\n\ntwo\n\nthree\n\nfour");
  r.advance(FLUSH_MS);
  r.drain();
  expect(r.stream.dirty).toBe(true);
  let offered = r.stream.takeFinal();
  expect(offered).toHaveLength(2);
  expect(r.stream.dirty).toBe(false);
  // Each refusal waits twice as long as the last, up to the cap.
  let wait = COMMIT_RETRY_MS;
  for (let refusal = 0; refusal < 8; refusal++) {
    r.stream.releaseFinal(offered.length);
    expect(r.stream.commitDue).toBe(false);
    expect(r.stream.dirty).toBe(false);
    r.advance(wait - 1);
    expect(r.stream.commitDue).toBe(false);
    expect(r.stream.dirty).toBe(false);
    r.advance(1);
    expect(r.stream.commitDue).toBe(true);
    expect(r.stream.dirty).toBe(true);
    offered = r.stream.takeFinal();
    expect(offered).toHaveLength(2);
    wait = Math.min(COMMIT_RETRY_MAX_MS, wait * 2);
  }
  expect(wait).toBe(COMMIT_RETRY_MAX_MS);
  // The ack ends it: the next refusal is quick again.
  r.stream.confirmCommitted(2);
  r.stream.pushText("\n\nfive\n\nsix");
  r.advance(FLUSH_MS);
  r.drain();
  offered = r.stream.takeFinal();
  expect(offered.length).toBeGreaterThan(0);
  r.stream.releaseFinal(offered.length);
  r.advance(COMMIT_RETRY_MS);
  expect(r.stream.commitDue).toBe(true);
});

test("final blocks are only ever appended, and the first text writes the live message whole", () => {
  const r = rig();
  const text = MARKDOWN;
  for (let i = 0; i < text.length; i += 7) {
    r.stream.pushText(text.slice(i, i + 7));
    r.advance(FLUSH_MS);
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
  expect(live.blocks).toHaveLength(1);
  expect(live.blocks[0]?.code).toBe(true);
  expect(runsText(live.blocks[0]?.runs ?? [])).toContain("| :-- | --: |");
  expect(runsText(live.tail.at(-1)?.runs ?? []).trim()).toBe("after");
});

test("a streamed table is fitted to the width the stream is given", () => {
  const r = rig({ tableCols: () => 30 });
  r.stream.pushText(
    "| id | description |\n|---|---|\n| 1 | A rather long description that has to wrap |\n\nafter\n",
  );
  r.drain();
  const lines = runsText(r.live().blocks[0]?.runs ?? []).split("\n");
  expect(lines.length).toBeGreaterThan(3);
  for (const line of lines)
    expect(Bun.stringWidth(line)).toBeLessThanOrEqual(30);
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
  // 100 tokens a second, a tick every FLUSH_MS.
  let ticked = 0;
  for (const piece of pieces) {
    r.stream.pushText(piece);
    r.advance(10);
    ticked += 10;
    if (ticked >= FLUSH_MS) {
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
    r.advance(FLUSH_MS);
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
  r.advance(FLUSH_MS);
  const writes = r.drain();
  expect(writes.map((w) => w.path)).toEqual(["thinking", "live"]);
  expect(r.thinking()).toEqual({ on: false, runs: [] });
  // Reasoning after the answer began is ignored.
  r.stream.pushThinking("late");
  r.advance(FLUSH_MS);
  expect(r.drain()).toEqual([]);
});

test("after markStale the next drain sends the live message and the reasoning whole", () => {
  const r = rig();
  r.stream.pushText("first paragraph\n\nsecond");
  r.advance(FLUSH_MS);
  r.drain();
  r.stream.pushText(" half");
  r.advance(FLUSH_MS);
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
    r.advance(FLUSH_MS);
    r.drain();
  }
  const live = r.live();
  expect(live.blocks.length).toBeLessThanOrEqual(MAX_MESSAGE_BLOCKS + 1);
  expect(runsText(live.blocks.at(-1)?.runs ?? [])).toContain(
    "when it is finished",
  );
  expect(live.tail).toEqual([]);
  r.stream.pushText("more");
  r.advance(FLUSH_MS);
  expect(r.drain()).toEqual([]);
});

// ------------------------------------------------------- the inline commits

test("inline: final blocks are offered once, confirmed only on ack, then gone from the live message", () => {
  const r = rig({ inline: true });
  r.stream.pushText("first paragraph\n\nsecond paragraph\n\nthird half");
  r.advance(FLUSH_MS);
  r.drain();
  const offered = r.stream.takeFinal();
  expect(offered.map((b) => runsText(b.runs))).toEqual(["first paragraph"]);
  // Already offered: not offered again while the commit is in flight.
  expect(r.stream.takeFinal()).toEqual([]);
  expect(r.stream.committed).toBe(0);
  const clear = r.stream.confirmCommitted(offered.length);
  expect(r.stream.committed).toBe(1);
  for (const w of clear) applyWrite(r.store, w);
  expect(r.live().blocks).toEqual([]);
  expect(r.live().tail.map((b) => runsText(b.runs))).toEqual([
    "second paragraph",
    "third half",
  ]);
});

test("inline: a released offer comes back whole, so a refused commit loses nothing", () => {
  const r = rig({ inline: true });
  r.stream.pushText("first\n\nsecond\n\nthird");
  r.advance(FLUSH_MS);
  r.drain();
  const first = r.stream.takeFinal();
  expect(first.length).toBe(1);
  r.stream.releaseFinal(first.length);
  const again = r.stream.takeFinal();
  expect(again).toEqual(first);
  r.stream.confirmCommitted(again.length);
  expect(r.stream.takeFinal()).toEqual([]);
  expect(r.stream.committed).toBe(1);
});

test("inline: a resync never re-emits a block whose commit was acked", () => {
  const r = rig({ inline: true });
  r.stream.pushText("first paragraph\n\nsecond paragraph\n\nthird half");
  r.advance(FLUSH_MS);
  r.drain();
  const offered = r.stream.takeFinal();
  const clear = r.stream.confirmCommitted(offered.length);
  for (const w of clear) applyWrite(r.store, w);
  r.store.live = { active: false, blocks: [], tail: [] };
  r.stream.markStale();
  r.drain();
  const live = r.live();
  const shown = [...live.blocks, ...live.tail]
    .map((b) => runsText(b.runs))
    .join("\n");
  expect(shown).not.toContain("first paragraph");
  expect(shown).toContain("second paragraph");
  expect(shown).toContain("third half");
});

test("inline: finishTail offers the whole remainder, parsed as parseBlocks parses it", () => {
  for (let seed = 1; seed <= 10; seed++) {
    const next = prng(seed);
    const r = rig({ inline: true });
    const printed: Block[] = [];
    for (let i = 0; i < MARKDOWN.length; ) {
      const size = 1 + Math.floor(next() * 9);
      r.stream.pushText(MARKDOWN.slice(i, i + size));
      i += size;
      r.advance(next() < 0.3 ? REPARSE_MS : 5);
      r.drain();
      // Commit as blocks close, confirming each ack at once.
      const finals = r.stream.takeFinal();
      if (finals.length > 0) {
        printed.push(...finals);
        for (const w of r.stream.confirmCommitted(finals.length))
          applyWrite(r.store, w);
      }
    }
    printed.push(...r.stream.finishTail());
    const links: LinkRef[] = [];
    expect(shape(printed)).toEqual(shape(parseBlocks(MARKDOWN, "l", links)));
    expect(r.stream.links).toEqual(links);
    expect(r.stream.finishTail()).toEqual([]);
    expect(r.stream.dirty).toBe(false);
  }
});

test("inline: past the bounds the note is a final block and the region keeps its own line", () => {
  const r = rig({ inline: true });
  for (let n = 0; n < MAX_MESSAGE_BLOCKS + 20; n++) {
    r.stream.pushText(`paragraph ${n}\n\n`);
    r.advance(FLUSH_MS);
    r.drain();
  }
  const live = r.live();
  expect(runsText(live.blocks.at(-1)?.runs ?? [])).toContain(
    "left out of the live view",
  );
  expect(live.tail.map((b) => runsText(b.runs)).join("")).toContain(
    "following stopped",
  );
  // The note is committable; the region note is not part of the offer.
  const offered = r.stream.finishTail();
  expect(offered.map((b) => runsText(b.runs)).join("\n")).toContain(
    "left out of the live view",
  );
  expect(offered.map((b) => runsText(b.runs)).join("\n")).not.toContain(
    "following stopped",
  );
  // A resync does not add a second note.
  r.stream.markStale();
  r.drain();
  const notes = r
    .live()
    .blocks.filter((b) =>
      runsText(b.runs).includes("left out of the live view"),
    );
  expect(notes).toHaveLength(1);
});

test("fullscreen: a stopped finish does not promise the finished message", () => {
  const long = (r: ReturnType<typeof rig>) => {
    for (let n = 0; n < MAX_MESSAGE_BLOCKS + 20; n++) {
      r.stream.pushText(`paragraph ${n}\n\n`);
      r.advance(FLUSH_MS);
      r.drain();
    }
  };
  const text = (blocks: Block[]) =>
    blocks.map((b) => runsText(b.runs)).join("\n");
  const finished = rig();
  long(finished);
  expect(text(finished.live().blocks)).toContain("shown when it is finished");
  const promised = finished.stream.finishTail();
  expect(text(promised)).toContain("shown when it is finished");
  const stopped = rig();
  long(stopped);
  const kept = stopped.stream.finishTail(true);
  expect(text(kept)).not.toContain("shown when it is finished");
  expect(text(kept)).toContain("the turn stopped before it finished");
  // The note keeps its place: same blocks, same ids.
  expect(kept.map((b) => b.id)).toEqual(promised.map((b) => b.id));
});

test("reset forgets everything and streamingView is the first write of a stream", () => {
  const r = rig();
  r.stream.pushText("text\n\nmore");
  r.advance(FLUSH_MS);
  r.drain();
  r.stream.reset();
  expect(r.stream.dirty).toBe(false);
  expect(r.stream.drain()).toEqual([]);
  const view = streamingView("Plan:\n\n```ts\nconst x = 1;");
  expect(view.active).toBe(true);
  expect(view.blocks.map((b) => b.para)).toEqual([true]);
  expect(view.tail.map((b) => b.code)).toEqual([true]);
});

test("an open code block is not parsed again at every line, only when it closes or on its period", () => {
  const r = rig();
  r.stream.pushText("Here:\n\n```ts\nconst a = 1;\n");
  r.advance(FLUSH_MS);
  r.drain();
  const parsedAt = (writes: { path: string }[]) =>
    writes.some((w) => w.path === "live.tail");
  // Lines end, the block stays open: the text goes on the end of the tail.
  for (let n = 0; n < 20; n++) {
    r.stream.pushText(`const b${n} = ${n};\n`);
    r.advance(FLUSH_MS);
    expect(parsedAt(r.drain())).toBe(false);
  }
  // Its period passes.
  r.advance(FENCE_REPARSE_MS);
  r.stream.pushText("const c = 3;\n");
  expect(parsedAt(r.drain())).toBe(true);
  // The closing line parses it at once, and the code is highlighted.
  r.advance(FLUSH_MS);
  r.stream.pushText("```\n");
  expect(parsedAt(r.drain())).toBe(true);
  expect(r.live().tail.some((b) => b.code && b.runs.length > 1)).toBe(true);
});

test("a reply that is one long code block costs about as much per token as prose", () => {
  const r = rig();
  const pieces = fixtureTokens(2000, "fence");
  let ticked = 0;
  for (const piece of pieces) {
    r.stream.pushText(piece);
    r.advance(10);
    ticked += 10;
    if (ticked >= FLUSH_MS) {
      ticked = 0;
      r.drain();
    }
  }
  r.advance(REPARSE_MS);
  r.drain();
  expect(r.bytes() / pieces.length).toBeLessThan(300);
  const live = r.live();
  expect(shape([...live.blocks, ...live.tail])).toEqual(
    shape(parseBlocks(pieces.join(""), "l")),
  );
});
