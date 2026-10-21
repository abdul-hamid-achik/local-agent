import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  callSummary,
  IDLE_LIVE,
  liveToolView,
  MAX_CODE_LINES,
  outputLines,
  parseBlocks,
  parseStreaming,
  resultSummary,
  streamingView,
  tokenizeLine,
  ViewBuilder,
} from "#cli/tuimark/blocks";

test("markdown becomes paragraphs, headings, bullets, quotes and code", () => {
  const blocks = parseBlocks(
    [
      "# Title",
      "first line",
      "second line",
      "",
      "- one",
      "  - nested",
      "> quoted",
      "```ts",
      "const a = 1;",
      "```",
      "tail",
    ].join("\n"),
  );
  expect(blocks.map((b) => [b.head, b.para, b.quote, b.code])).toEqual([
    [true, false, false, false],
    [false, true, false, false],
    [false, true, false, false],
    [false, false, true, false],
    [false, false, false, true],
    [false, true, false, false],
  ]);
  expect(blocks[0]?.t).toBe("Title");
  expect(blocks[1]?.t).toBe("first line\nsecond line");
  expect(blocks[2]?.t).toBe("• one\n  • nested");
  expect(blocks[3]?.t).toBe("quoted");
  expect(blocks[4]?.lang).toBe("ts");
  expect(new Set(blocks.map((b) => b.id)).size).toBe(blocks.length);
});

test("every block carries every flag, because the markup has no equality test", () => {
  for (const block of parseBlocks("a\n\n# b\n\n```\nc\n```")) {
    expect(Object.keys(block).sort()).toEqual(
      ["code", "head", "id", "lang", "lines", "para", "quote", "t"].sort(),
    );
  }
});

test("pipe tables stay aligned as a code block", () => {
  const [table] = parseBlocks("| a | b |\n|---|---|\n| 1 | 2 |");
  expect(table?.code).toBe(true);
  expect(table?.lang).toBe("table");
  expect(table?.lines).toHaveLength(3);
  expect(table?.lines[0]?.segs.map((s) => s.t).join("")).toBe("| a | b |");
});

test("an unterminated fence is closed while streaming", () => {
  const blocks = parseStreaming("Plan:\n\n```ts\nconst x = 1;\nfor (");
  expect(blocks.map((b) => b.code)).toEqual([false, true]);
  expect(blocks[1]?.lines).toHaveLength(2);
  expect(blocks[0]?.id.startsWith("l")).toBe(true);
});

test("tokenizer classes keywords, strings, numbers and comments, and keeps every character", () => {
  const line = 'const s = "a b"; // note 42';
  const segs = tokenizeLine(line);
  expect(segs.map((s) => s.t).join("")).toBe(line);
  expect(segs.find((s) => s.t === "const")?.kw).toBe(true);
  expect(segs.find((s) => s.t === '"a b"')?.str).toBe(true);
  expect(segs.find((s) => s.t.startsWith("//"))?.com).toBe(true);
  expect(tokenizeLine("x = 7").find((s) => s.t === "7")?.num).toBe(true);
  expect(tokenizeLine("")).toEqual([]);
  expect(
    tokenizeLine("a b")
      .map((s) => s.t)
      .join(""),
  ).toBe("a b");
});

test("ANSI, control characters and tabs never reach the store", () => {
  const [para] = parseBlocks("\u001b[31mred\u001b[0m\tx\u0007\r");
  expect(para?.t).toBe("red  x");
});

test("a long code block keeps a bounded number of lines and says how many it dropped", () => {
  const source = Array.from(
    { length: MAX_CODE_LINES + 25 },
    (_, i) => `line ${i}`,
  );
  const [code] = parseBlocks(["```", ...source, "```"].join("\n"));
  expect(code?.lines).toHaveLength(MAX_CODE_LINES + 1);
  expect(code?.lines.at(-1)?.segs[0]?.t).toBe("… 25 more lines");
});

test("tool output keeps eight lines and counts the rest", () => {
  const out = outputLines(
    Array.from({ length: 12 }, (_, i) => `l${i}`).join("\n"),
  );
  expect(out).toHaveLength(9);
  expect(out.at(-1)?.t).toBe("… 4 more lines");
  expect(outputLines("")).toEqual([]);
});

test("a command result shows its exit code before its output", () => {
  const shown = resultSummary(
    "run_command",
    JSON.stringify({
      output: "ok\n",
      exitCode: 0,
      timedOut: false,
      truncated: true,
    }),
  );
  expect(shown).toBe("exit 0, truncated\nok\n");
  expect(resultSummary("read", "plain")).toBe("plain");
  expect(resultSummary("run_command", "not json")).toBe("not json");
});

test("a call summary names the path, the command or the arguments", () => {
  const call = (args: Record<string, unknown>) => ({
    function: { name: "x", arguments: args },
  });
  expect(callSummary(call({ path: "src/a.ts" }))).toBe("src/a.ts");
  expect(callSummary(call({ argv: ["bun", "test"] }))).toBe("bun test");
  expect(callSummary(call({ q: 1 }))).toBe('{"q":1}');
  expect(callSummary(undefined)).toBe("");
});

const messages: Message[] = [
  { role: "system", content: "hidden" },
  { role: "user", content: "hi" },
  {
    role: "assistant",
    content: "",
    tool_calls: [
      { id: "c1", function: { name: "edit", arguments: { path: "a.txt" } } },
    ],
  },
  {
    role: "tool",
    tool_name: "edit",
    tool_call_id: "c1",
    content: JSON.stringify({
      status: "denied",
      output: "Edit denied by host",
    }),
  },
  { role: "assistant", content: "done" },
];

test("messages map to views: system and empty assistant messages show nothing", () => {
  const builder = new ViewBuilder();
  const views = messages.flatMap((m) => builder.fromMessage(m) ?? []);
  expect(views.map((v) => [v.is_user, v.is_asst, v.is_tool])).toEqual([
    [true, false, false],
    [false, false, true],
    [false, true, false],
  ]);
  const tool = views[1]?.card;
  expect(tool).toMatchObject({
    name: "edit",
    status: "denied",
    args: "a.txt",
    bad: true,
    ok: false,
    running: false,
  });
  expect(tool?.out.map((o) => o.t)).toEqual(["Edit denied by host"]);
  expect(new Set(views.map((v) => v.id)).size).toBe(3);
});

test("a tool receipt that is not the runtime's JSON is shown as it is", () => {
  const view = new ViewBuilder().fromMessage({
    role: "tool",
    tool_name: "x",
    content: "raw text",
  });
  expect(view?.card.out.map((o) => o.t)).toEqual(["raw text"]);
  expect(view?.card.warn).toBe(true);
});

test("live tool calls come in call order, one per tool_requested event", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [
      { id: "a", function: { name: "read", arguments: { path: "one" } } },
      { id: "b", function: { name: "read", arguments: { path: "two" } } },
    ],
  });
  expect(callSummary(builder.takeCall())).toBe("one");
  expect(callSummary(builder.takeCall())).toBe("two");
  expect(builder.takeCall()).toBeUndefined();
  const live = liveToolView(undefined, "read", "approval");
  expect(live).toMatchObject({ active: true, is_tool: true });
  expect(live.card).toMatchObject({
    running: true,
    status: "waiting for approval",
  });
});

test("the idle and streaming live views have the same keys", () => {
  expect(Object.keys(streamingView("x")).sort()).toEqual(
    Object.keys(IDLE_LIVE).sort(),
  );
  expect(Object.keys(liveToolView(undefined, "t", "running")).sort()).toEqual(
    Object.keys(IDLE_LIVE).sort(),
  );
});
