import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  callSummary,
  clip,
  IDLE_LIVE,
  inlineRuns,
  liveToolView,
  MAX_CODE_COLS,
  MAX_CODE_LINES,
  MAX_MESSAGE_BLOCKS,
  MAX_MESSAGE_CHARS,
  outputRuns,
  parseBlocks,
  parseStreaming,
  type Run,
  resultSummary,
  runsText,
  tableRuns,
  tokenizeLine,
  ViewBuilder,
} from "#cli/tuimark/blocks";
import { streamingView } from "#cli/tuimark/stream";

test("markdown becomes paragraphs, headings, items, quotes and code", () => {
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
  expect(blocks.map((b) => [b.head, b.para, b.item, b.quote, b.code])).toEqual([
    [true, false, false, false, false],
    [false, true, false, false, false],
    [false, false, true, false, false],
    [false, false, true, false, false],
    [false, false, false, true, false],
    [false, false, false, false, true],
    [false, true, false, false, false],
  ]);
  expect(runsText(blocks[0]?.runs ?? [])).toBe("Title");
  expect(runsText(blocks[1]?.runs ?? [])).toBe("first line\nsecond line");
  expect(blocks[2]).toMatchObject({ marker: "•", nest: false });
  expect(blocks[3]).toMatchObject({ marker: "•", nest: true });
  expect(runsText(blocks[3]?.runs ?? [])).toBe("nested");
  expect(runsText(blocks[4]?.runs ?? [])).toBe("quoted");
  expect(blocks[5]?.lang).toBe("ts");
  expect(new Set(blocks.map((b) => b.id)).size).toBe(blocks.length);
});

test("every block carries every flag, because the markup has no equality test", () => {
  for (const block of parseBlocks("a\n\n# b\n\n- c\n\n```\nc\n```")) {
    expect(Object.keys(block).sort()).toEqual(
      [
        "code",
        "head",
        "id",
        "item",
        "lang",
        "marker",
        "nest",
        "para",
        "quote",
        "runs",
      ].sort(),
    );
  }
});

test("bullets and numbered items are their own blocks with a marker and a nesting flag", () => {
  const blocks = parseBlocks(
    [
      "1. first **one**",
      "2. second",
      "   wrapped line",
      "   - nested bullet",
      "10) tenth",
      "* star",
      "+ plus",
      "",
      "**not a bullet** at all",
    ].join("\n"),
  );
  expect(blocks.map((b) => [b.item, b.marker, b.nest])).toEqual([
    [true, "1.", false],
    [true, "2.", false],
    [true, "•", true],
    [true, "10.", false],
    [true, "•", false],
    [true, "•", false],
    [false, "", false],
  ]);
  expect(blocks[0]?.runs).toEqual(["first ", { text: "one", class: "b" }]);
  expect(runsText(blocks[1]?.runs ?? [])).toBe("second\nwrapped line");
  expect(blocks[6]?.para).toBe(true);
  expect(blocks[6]?.runs).toEqual([
    { text: "not a bullet", class: "b" },
    " at all",
  ]);
});

test("a streamed item that ends mid-marker keeps the marker as text", () => {
  const [item] = parseBlocks("- the **bol");
  expect(item?.item).toBe(true);
  expect(item?.runs).toEqual(["the **bol"]);
});

test("paragraphs, headings and quotes carry inline runs", () => {
  const blocks = parseBlocks(
    "# A `b` title\n\nUse **bold** here\n\n> a *quote*",
  );
  expect(blocks[0]?.runs).toEqual([
    "A ",
    { text: "b", class: "code" },
    " title",
  ]);
  expect(blocks[1]?.runs).toEqual([
    "Use ",
    { text: "bold", class: "b" },
    " here",
  ]);
  expect(blocks[2]?.runs).toEqual(["a ", { text: "quote", class: "i" }]);
});

test("pipe tables are aligned monospace rows with a bold header", () => {
  const [table] = parseBlocks(
    "| name | n |\n|:--|--:|\n| apple | 1 |\n| fig | 22 |",
  );
  expect(table).toMatchObject({ code: true, lang: "table" });
  expect(table?.runs[0]).toEqual({
    text: "| name  |   n |",
    class: "b",
  });
  expect(runsText(table?.runs ?? [])).toBe(
    [
      "| name  |   n |",
      "| :---- | --: |",
      "| apple |   1 |",
      "| fig   |  22 |",
    ].join("\n"),
  );
});

test("table columns align by display width, not by code units", () => {
  const rows = runsText(
    tableRuns(["| a | b |", "|---|---|", "| 日本語 | 😀 |"]),
  ).split("\n");
  expect(rows.map((r) => Bun.stringWidth(r))).toEqual([
    Bun.stringWidth(rows[2] ?? ""),
    Bun.stringWidth(rows[2] ?? ""),
    Bun.stringWidth(rows[2] ?? ""),
  ]);
  // A table with no divider row has no header.
  expect(tableRuns(["| a |", "| b |"])).toEqual(["| a   |\n| b   |"]);
});

test("an unterminated fence is closed while streaming", () => {
  const blocks = parseStreaming("Plan:\n\n```ts\nconst x = 1;\nfor (");
  expect(blocks.map((b) => b.code)).toEqual([false, true]);
  expect(runsText(blocks[1]?.runs ?? [])).toBe("const x = 1;\nfor (");
  expect(blocks[0]?.id.startsWith("l")).toBe(true);
});

test("a code block is one run list: tokens classed, lines joined by a newline", () => {
  const [code] = parseBlocks(
    ["```ts", 'const s = "a"; // note', "  return 7;", "```"].join("\n"),
  );
  expect(code?.runs).toEqual([
    { text: "const", class: "kw" },
    " s = ",
    { text: '"a"', class: "str" },
    "; ",
    { text: "// note", class: "com" },
    "\n  ",
    { text: "return", class: "kw" },
    " ",
    { text: "7", class: "num" },
    ";",
  ]);
  const [empty] = parseBlocks("```\n```");
  expect(empty).toMatchObject({ code: true, runs: [] });
});

test("tokenizer classes keywords, strings, numbers and comments, and keeps every character", () => {
  const line = 'const s = "a b"; // note 42';
  const toks = tokenizeLine(line);
  expect(toks.map((s) => s.t).join("")).toBe(line);
  expect(toks.find((s) => s.t === "const")?.cls).toBe("kw");
  expect(toks.find((s) => s.t === '"a b"')?.cls).toBe("str");
  expect(toks.find((s) => s.t.startsWith("//"))?.cls).toBe("com");
  expect(tokenizeLine("x = 7").find((s) => s.t === "7")?.cls).toBe("num");
  expect(tokenizeLine("")).toEqual([]);
  expect(
    tokenizeLine("a b")
      .map((s) => s.t)
      .join(""),
  ).toBe("a b");
});

test("ANSI, control characters and tabs never reach the store", () => {
  const [para] = parseBlocks("\u001b[31mred\u001b[0m\tx\u0007\r");
  expect(runsText(para?.runs ?? [])).toBe("red  x");
});

test("a long code block keeps a bounded number of lines and says how many it dropped", () => {
  const source = Array.from(
    { length: MAX_CODE_LINES + 25 },
    (_, i) => `line ${i}`,
  );
  const [code] = parseBlocks(["```", ...source, "```"].join("\n"));
  const shown = runsText(code?.runs ?? []).split("\n");
  expect(shown).toHaveLength(MAX_CODE_LINES + 1);
  expect(shown.at(-1)).toBe("… 25 more lines");
  expect(code?.runs.at(-1)).toEqual({
    text: "\n… 25 more lines",
    class: "com",
  });
});

test("tool output is one run list: eight lines, then the count of the rest", () => {
  const out = outputRuns(
    Array.from({ length: 12 }, (_, i) => `l${i}`).join("\n"),
  );
  expect(runsText(out).split("\n")).toHaveLength(9);
  expect(out.at(-1)).toEqual({ text: "\n… 4 more lines", class: "note" });
  expect(outputRuns("")).toEqual([]);
  expect(outputRuns("a\nb\n\n")).toEqual(["a\nb"]);
});

test("a failing command's exit line is class err, a clean one's is note", () => {
  const view = (exitCode: number | null, timedOut = false) =>
    new ViewBuilder().fromMessage({
      role: "tool",
      tool_name: "run_command",
      content: JSON.stringify({
        status: "failed",
        output: JSON.stringify({ output: "boom\n", exitCode, timedOut }),
      }),
    })?.card.out;
  expect(view(2)).toEqual([{ text: "exit 2", class: "err" }, "\nboom"]);
  expect(view(0)).toEqual([{ text: "exit 0", class: "note" }, "\nboom"]);
  expect(view(null)?.[0]).toEqual({ text: "no exit code", class: "err" });
  expect(view(0, true)?.[0]).toMatchObject({ class: "err" });
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
  expect(tool?.out).toEqual(["Edit denied by host"]);
  expect(new Set(views.map((v) => v.id)).size).toBe(3);
});

test("a tool receipt that is not the runtime's JSON is shown as it is", () => {
  const view = new ViewBuilder().fromMessage({
    role: "tool",
    tool_name: "x",
    content: "raw text",
  });
  expect(view?.card.out).toEqual(["raw text"]);
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

test("a notice is a quoted UI-local view: no other role flag, every line kept", () => {
  const builder = new ViewBuilder();
  const notice = builder.notice("first line\nsecond line");
  expect([
    notice.is_user,
    notice.is_asst,
    notice.is_tool,
    notice.is_notice,
  ]).toEqual([false, false, false, true]);
  // Quoted, so it reads as the client's words; the text itself is whole.
  expect(notice.blocks.length).toBeGreaterThan(0);
  expect(notice.blocks.every((b) => b.quote)).toBe(true);
  const text = notice.blocks.map((b) => runsText(b.runs)).join("\n");
  expect(text).toContain("first line");
  expect(text).toContain("second line");
  // Every other view the builder makes carries is_notice false.
  const user = builder.fromMessage({ role: "user", content: "hi" });
  expect(user?.is_notice).toBe(false);
  expect(IDLE_LIVE.is_notice).toBe(false);
});

test("the idle and streaming live views have the same keys", () => {
  expect(Object.keys(streamingView("x")).sort()).toEqual(
    Object.keys(IDLE_LIVE).sort(),
  );
  expect(Object.keys(liveToolView(undefined, "t", "running")).sort()).toEqual(
    Object.keys(IDLE_LIVE).sort(),
  );
});

test("clip cuts on code points, never inside a surrogate pair", () => {
  expect(clip("😀😀😀😀", 3)).toBe("😀😀…");
  expect(clip("short", 10)).toBe("short");
  expect(clip("exactly", 7)).toBe("exactly");
});

test("a # is a comment only at the start of a line", () => {
  const plain = tokenizeLine("this.#private = color.replace('#fff')");
  expect(plain.some((s) => s.cls === "com")).toBe(false);
  expect(plain.map((s) => s.t).join("")).toBe(
    "this.#private = color.replace('#fff')",
  );
  const comment = tokenizeLine("  # a comment");
  expect(comment.find((s) => s.cls === "com")?.t).toBe("# a comment");
  expect(tokenizeLine("x = 1  # trailing").some((s) => s.cls === "com")).toBe(
    false,
  );
});

test("fences: an info string with spaces opens one, and only the same mark closes it", () => {
  const blocks = parseBlocks(
    [
      '```python title="x.py"',
      "a = 1",
      "~~~",
      "still code",
      "```",
      "after",
    ].join("\n"),
  );
  expect(blocks.map((b) => b.code)).toEqual([true, false]);
  expect(blocks[0]?.lang).toBe("python");
  expect(runsText(blocks[0]?.runs ?? [])).toBe("a = 1\n~~~\nstill code");
  // A streamed prefix is the same mapping: the open fence is code already.
  expect(parseStreaming('```python title="x.py"\na = 1')[0]?.code).toBe(true);
});

test("a message view is bounded: characters, blocks and code line width", () => {
  const huge = parseBlocks("a".repeat(MAX_MESSAGE_CHARS + 50));
  expect(huge.at(-1)?.quote).toBe(true);
  expect(runsText(huge.at(-1)?.runs ?? [])).toBe(
    "… message truncated in this view (50 more characters)",
  );
  const many = parseBlocks(
    Array.from({ length: MAX_MESSAGE_BLOCKS + 7 }, (_, i) => `# h${i}`).join(
      "\n",
    ),
  );
  expect(many).toHaveLength(MAX_MESSAGE_BLOCKS + 1);
  expect(runsText(many.at(-1)?.runs ?? [])).toBe(
    "… 7 more blocks not shown in this view",
  );
  const [code] = parseBlocks(
    ["```", "z".repeat(MAX_CODE_COLS + 500), "```"].join("\n"),
  );
  expect(runsText(code?.runs ?? [])).toHaveLength(MAX_CODE_COLS);
});

// ----------------------------------------------------------- the inline scanner

const b = (text: string): Run => ({ text, class: "b" });
const i = (text: string): Run => ({ text, class: "i" });
const code = (text: string): Run => ({ text, class: "code" });

test("each inline marker becomes a class", () => {
  expect(inlineRuns("a **b** c")).toEqual(["a ", b("b"), " c"]);
  expect(inlineRuns("a __b__ c")).toEqual(["a ", b("b"), " c"]);
  expect(inlineRuns("a *b* c")).toEqual(["a ", i("b"), " c"]);
  expect(inlineRuns("a _b_ c")).toEqual(["a ", i("b"), " c"]);
  expect(inlineRuns("a `b` c")).toEqual(["a ", code("b"), " c"]);
  expect(inlineRuns("a ~~b~~ c")).toEqual([
    "a ",
    { text: "b", class: "strike" },
    " c",
  ]);
  expect(inlineRuns("plain")).toEqual(["plain"]);
  expect(inlineRuns("")).toEqual([]);
});

test("markers nest, and a class stays on every piece it covers", () => {
  expect(inlineRuns("**a *b* c**")).toEqual([
    b("a "),
    { text: "b", class: "b i" },
    b(" c"),
  ]);
  expect(inlineRuns("***both***")).toEqual([{ text: "both", class: "b i" }]);
  expect(inlineRuns("**bold `code`**")).toEqual([
    b("bold "),
    { text: "code", class: "b code" },
  ]);
  expect(inlineRuns("~~gone **bold**~~")).toEqual([
    { text: "gone ", class: "strike" },
    { text: "bold", class: "b strike" },
  ]);
  expect(inlineRuns("*a **b** c*")).toEqual([
    i("a "),
    { text: "b", class: "b i" },
    i(" c"),
  ]);
});

test("a marker that does not close stays literal, however a stream ends", () => {
  expect(inlineRuns("**open")).toEqual(["**open"]);
  expect(inlineRuns("a *b")).toEqual(["a *b"]);
  expect(inlineRuns("a `b")).toEqual(["a `b"]);
  expect(inlineRuns("a ~~b")).toEqual(["a ~~b"]);
  expect(inlineRuns("a [b](http://x")).toEqual(["a [b](http://x"]);
  expect(inlineRuns("a [b]")).toEqual(["a [b]"]);
  expect(runsText(inlineRuns("**a *b**"))).toBe("**a b*");
  expect(inlineRuns("**a**b**")).toEqual([b("a"), "b**"]);
  // Every prefix of a message reads as text: nothing is dropped or doubled.
  const message = "Use **bold**, `code`, *em* and [a link](https://x.dev/a_b).";
  const whole = runsText(inlineRuns(message));
  expect(whole).toBe("Use bold, code, em and a link (https://x.dev/a_b).");
  for (let n = 0; n <= message.length; n++) {
    const head = message.slice(0, n);
    const text = runsText(inlineRuns(head));
    expect(text.length).toBeGreaterThanOrEqual(Math.min(head.length, 1) - 1);
    expect(text).not.toContain("undefined");
  }
});

test("markers that cannot flank stay text: arithmetic, snake_case, spaces", () => {
  expect(inlineRuns("2 * 3 * 4")).toEqual(["2 * 3 * 4"]);
  expect(inlineRuns("snake_case_name and my_var_2")).toEqual([
    "snake_case_name and my_var_2",
  ]);
  expect(inlineRuns("a ** b ** c")).toEqual(["a ** b ** c"]);
  expect(inlineRuns("foo*bar*baz")).toEqual(["foo", i("bar"), "baz"]);
  expect(inlineRuns("x ~ y ~~~ z ~")).toEqual(["x ~ y ~~~ z ~"]);
  expect(inlineRuns("**")).toEqual(["**"]);
});

test("a code span keeps its markers, backticks and spaces", () => {
  expect(inlineRuns("`a *b* **c**`")).toEqual([code("a *b* **c**")]);
  expect(inlineRuns("`` a ` b ``")).toEqual([code("a ` b")]);
  expect(inlineRuns("`[x](y)`")).toEqual([code("[x](y)")]);
  expect(inlineRuns("`  `")).toEqual([code("  ")]);
  expect(inlineRuns("**a `*` b**")).toEqual([
    b("a "),
    { text: "*", class: "b code" },
    b(" b"),
  ]);
  expect(inlineRuns("``x`")).toEqual(["``x`"]);
});

test("a backslash escapes a marker", () => {
  expect(inlineRuns("\\*not em\\*")).toEqual(["*not em*"]);
  expect(inlineRuns("a \\`b\\` c")).toEqual(["a `b` c"]);
  expect(inlineRuns("C:\\dir")).toEqual(["C:\\dir"]);
});

test("a link is its text in class link, then the address dim", () => {
  expect(inlineRuns("see [the docs](https://x.dev/a) now")).toEqual([
    "see ",
    { text: "the docs", class: "link" },
    { text: " (https://x.dev/a)", class: "url" },
    " now",
  ]);
  expect(inlineRuns("[**bold** link](u)")).toEqual([
    { text: "bold", class: "b link" },
    { text: " link", class: "link" },
    { text: " (u)", class: "url" },
  ]);
});

test("a link address may hold parentheses, and bad links stay text", () => {
  expect(inlineRuns("[w](https://en.wikipedia.org/wiki/A_(b)) end")).toEqual([
    { text: "w", class: "link" },
    { text: " (https://en.wikipedia.org/wiki/A_(b))", class: "url" },
    " end",
  ]);
  expect(inlineRuns("(see [x](y))")).toEqual([
    "(see ",
    { text: "x", class: "link" },
    { text: " (y)", class: "url" },
    ")",
  ]);
  expect(inlineRuns("[a](b c)")).toEqual(["[a](b c)"]);
  expect(inlineRuns("[a] (b)")).toEqual(["[a] (b)"]);
  expect(inlineRuns("[a](b")).toEqual(["[a](b"]);
  expect(inlineRuns("[[nested]](u)")).toEqual([
    { text: "[nested]", class: "link" },
    { text: " (u)", class: "url" },
  ]);
  expect(inlineRuns("[a]()")).toEqual([{ text: "a", class: "link" }]);
  expect(inlineRuns("[x](y)".repeat(2))).toHaveLength(4);
});

test("CJK and emoji text is never split, and markers flank around it", () => {
  expect(inlineRuns("这是**重要**的")).toEqual(["这是", b("重要"), "的"]);
  expect(inlineRuns("这是**「重要」**的")).toEqual([
    "这是",
    b("「重要」"),
    "的",
  ]);
  expect(inlineRuns("**日本語**です")).toEqual([b("日本語"), "です"]);
  expect(inlineRuns("*한국어*")).toEqual([i("한국어")]);
  expect(inlineRuns("Great **🎉 done** 👍🏽")).toEqual([
    "Great ",
    b("🎉 done"),
    " 👍🏽",
  ]);
  expect(inlineRuns("`😀` and [👨‍👩‍👧](u)")).toEqual([
    code("😀"),
    " and ",
    { text: "👨‍👩‍👧", class: "link" },
    { text: " (u)", class: "url" },
  ]);
  for (const text of ["**😀😀**", "*👨‍👩‍👧*x", "[🇲🇽](u)", "a`🎉`b", "_日本_"]) {
    const out = runsText(inlineRuns(text));
    expect(out.isWellFormed()).toBe(true);
    for (const ch of Array.from(out)) expect(ch.length).toBeGreaterThan(0);
  }
});

test("the visible text is the source minus its markers, whatever the input", () => {
  const source = "a **b** `c` [d](e) ~~f~~ _g_ 日本 😀 \\* *h";
  expect(runsText(inlineRuns(source))).toBe("a b c d (e) f g 日本 😀 * *h");
});

test("pathological input stays linear", () => {
  const size = 20_000;
  const cases = [
    "*a ".repeat(size),
    "**a _b ~~c ".repeat(size / 4),
    "[a](".repeat(size / 2),
    "[a".repeat(size),
    "](".repeat(size),
    "`a``b```c".repeat(size / 4),
    "*a**b***c****".repeat(size / 8),
    `${"[a](x".repeat(size / 8)}${"(".repeat(size)}`,
    "*a ".repeat(size / 2) + "a* ".repeat(size / 2),
  ];
  const start = performance.now();
  for (const text of cases) {
    const runs = inlineRuns(text);
    expect(runsText(runs).length).toBeGreaterThan(0);
  }
  expect(performance.now() - start).toBeLessThan(2000);
});
