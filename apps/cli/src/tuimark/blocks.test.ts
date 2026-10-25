import type { Message } from "@local-agent/runtime";
import { expect, test, vi } from "vitest";
import {
  callSummary,
  capDetailsOutput,
  clip,
  DETAILS_MAX_CHARS,
  DETAILS_TRUNCATED_TAIL,
  fencedCodeBlocks,
  formatTook,
  IDLE_LIVE,
  imageMime,
  inlineRuns,
  type LinkRef,
  liveToolView,
  MAX_CODE_CHARS,
  MAX_CODE_LINES,
  MAX_IMAGE_B64,
  MAX_IMAGES,
  MAX_LINK_TEXT,
  MAX_LINKS,
  MAX_MESSAGE_BLOCKS,
  MAX_MESSAGE_CHARS,
  MAX_PARA_CHARS,
  type MessageView,
  messageImages,
  NO_CARD,
  onHold,
  outputRuns,
  parseBlocks,
  parseStreaming,
  type Run,
  resultSummary,
  runningCard,
  runsText,
  STILL_GLYPH,
  startsOpenFence,
  THINK_TRIM,
  tableRuns,
  tableWidth,
  thinkingTail,
  tokenizeLine,
  toolTitle,
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

test("a thematic break is a dim rule; a paragraph over === or --- is a heading", () => {
  const rule = { text: "─".repeat(24), class: "rule" };
  for (const line of ["---", "***", "___", "* * *", "- - -", "   ----  "]) {
    const blocks = parseBlocks(`before\n\n${line}\n\nafter`);
    expect(blocks.map((b) => b.runs)).toEqual([["before"], [rule], ["after"]]);
    expect(blocks[1]).toMatchObject({ para: true, item: false });
  }
  // As wide as a table may be, never past 72 columns.
  expect(parseBlocks("***", "b", undefined, 40)[0]?.runs).toEqual([
    { text: "─".repeat(40), class: "rule" },
  ]);
  expect(parseBlocks("***", "b", undefined, 200)[0]?.runs).toEqual([
    { text: "─".repeat(72), class: "rule" },
  ]);
  // Not rules: two marks, other text on the line, four spaces in.
  for (const line of ["--", "*-*", "--- x", "    ---"])
    expect(parseBlocks(line)[0]?.runs).not.toEqual([rule]);
  // CommonMark: an underline makes the paragraph above it a heading.
  for (const line of ["===", "=", "---", "--"]) {
    const blocks = parseBlocks(`A long\ntitle\n${line}\nbody`);
    expect(blocks.map((b) => [b.head, runsText(b.runs)])).toEqual([
      [true, "A long title"],
      [false, "body"],
    ]);
  }
  // One dash under a line is the list item that follows it mid-stream.
  expect(parseBlocks("Steps:\n- one").map((b) => b.item)).toEqual([
    false,
    true,
  ]);
  expect(
    parseBlocks("Steps:\n-").map((b) => [b.head, runsText(b.runs)]),
  ).toEqual([[false, "Steps:\n-"]]);
  // Under an item, --- ends the list with a rule.
  expect(parseBlocks("- one\n---").map((b) => [b.item, b.runs])).toEqual([
    [true, ["one"]],
    [false, [rule]],
  ]);
});

test("every block carries every flag, because the markup has no equality test", () => {
  for (const block of parseBlocks(
    "a\n\n# b\n\n- c\n\n```\nc\n```\n\n| a |\n|---|\n| b |",
  )) {
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
        "table",
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
  // No "table" label: the block is a code block with no language.
  expect(table).toMatchObject({ code: true, table: true, lang: "" });
  expect(parseBlocks("```ts\nx\n```")[0]?.table).toBe(false);
  expect(table?.runs[1]).toEqual({ text: "name", class: "b" });
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
  expect(runsText(tableRuns(["| a |", "| b |"]))).toBe("| a   |\n| b   |");
});

/** The painted lines of a table, and the widest of them. */
function tableLines(rows: string[], max?: number) {
  const lines = runsText(tableRuns(rows, max)).split("\n");
  return { lines, widest: Math.max(...lines.map((l) => Bun.stringWidth(l))) };
}

const WIDE = [
  "| id | name | description |",
  "|---|---|---|",
  "| 1 | alpha | A rather long description that has to wrap onto more lines |",
  "| 2 | beta | short |",
];

test("a table that fits keeps its natural width; one that does not shrinks to the max", () => {
  const natural = tableLines(WIDE);
  expect(natural.lines).toHaveLength(4);
  expect(natural.widest).toBeGreaterThan(40);
  // Exactly the natural width still fits, one column less does not.
  expect(tableLines(WIDE, natural.widest).lines).toEqual(natural.lines);
  const cut = tableLines(WIDE, natural.widest - 1);
  expect(cut.widest).toBeLessThanOrEqual(natural.widest - 1);
  expect(cut.lines.length).toBeGreaterThan(4);
});

test("a table wider than the max shrinks its widest column and wraps its cells", () => {
  const { lines, widest } = tableLines(WIDE, 40);
  expect(widest).toBeLessThanOrEqual(40);
  // The narrow columns keep their width; the description gave the width up.
  expect(lines[0]).toMatch(/^\| id {2}\| name {2}\| description\s+\|$/);
  expect(lines[1]).toMatch(/^\| -{2,} \| -{3,}.* \| -{3,}.*\|$/);
  // Every line has as many separators as the header: no stray "|" lines.
  for (const line of lines) expect(line.match(/ \| /g)?.length ?? 0).toBe(2);
  // The wrapped cell continues on lines whose other cells are blank.
  const rest = lines.slice(2).join("\n");
  expect(rest).toMatch(/^\| 1\s+\| alpha \| A rather/m);
  expect(rest).toMatch(/^\| {2}\s+\| {5}\s+\| \S.*\|$/m);
  // Nothing was lost: the description's words are all still there.
  const words = lines
    .slice(2)
    .map((l) => l.split("|")[3]?.trim())
    .join(" ")
    .replace(/\s+/g, " ");
  expect(words).toContain(
    "A rather long description that has to wrap onto more lines",
  );
  expect(words).toContain("short");
});

test("columns give up width from the widest first and never go below the minimum", () => {
  const rows = [
    "| a | bbbbbbbbbbbbbbbbbbbb | cccccccccc |",
    "|---|---|---|",
    "| x | y | z |",
  ];
  const { lines, widest } = tableLines(rows, 30);
  expect(widest).toBeLessThanOrEqual(30);
  // 30 less 10 of frame leaves 20: "a" keeps 3, the 20 wide column is cut
  // before the 10 wide one.
  const w = (lines[0] ?? "")
    .split("|")
    .slice(1, -1)
    .map((c) => c.length - 2);
  expect(w[0]).toBe(3);
  expect(w[1]).toBeGreaterThanOrEqual(w[2] ?? 0);
  expect(w.reduce((a, b) => a + b, 0)).toBe(20);
  // The width left over after the common cap goes to the widest column first.
  expect(tableLines(["| aaaaaaaaaaa | bbbbbbbbbbbb |"], 28).lines[0]).toBe(
    "| aaaaaaaaaa | bbbbbbbbbbb |",
  );
  // A column is never cut below 6 (or its own width, when smaller): at 25
  // the three columns would need 3 + 6 + 6 + 10 of frame, which fits.
  const tight = tableLines(rows, 25);
  expect(tight.widest).toBeLessThanOrEqual(25);
  expect(tight.lines[0]).toContain("|");
  // One column less and the minimums no longer fit: it is a list.
  expect(tableLines(rows, 24).lines[0]).toBe("a: x");
});

test("a table whose minimum widths do not fit becomes a Header: value list", () => {
  const rows = [
    "| Name | Role | Team | Site | Notes | Level |",
    "|---|---|---|---|---|---|",
    "| Ada Lovelace | Engineer | Analytical | London | wrote the first program for the engine | 9 |",
    "| Bob | Ops | | Paris | | 3 |",
  ];
  const { lines, widest } = tableLines(rows, 32);
  expect(widest).toBeLessThanOrEqual(32);
  expect(lines.some((l) => l.includes("|"))).toBe(false);
  expect(lines[0]).toBe("Name: Ada Lovelace");
  expect(lines).toContain("Role: Engineer");
  // A long value wraps under its label, indented.
  const at = lines.findIndex((l) => l.startsWith("Notes: wrote the first"));
  expect(at).toBeGreaterThan(0);
  expect(lines[at + 1]).toMatch(/^ {2}\S/);
  // Rows are told apart by a blank line; an empty cell is left out.
  const second = lines.slice(lines.indexOf("") + 1);
  expect(second).toEqual(["Name: Bob", "Role: Ops", "Site: Paris", "Level: 3"]);
  // The label is the header's own bold run.
  expect(tableRuns(rows, 32)[0]).toEqual({ text: "Name", class: "b" });
  // Without a header row the columns are numbered.
  const bare = runsText(tableRuns(["| a | b | c | d | e | f |"], 20));
  expect(bare.split("\n")[0]).toBe("Column 1: a");
});

test("bold, code and links in cells are styled runs, measured without their marks", () => {
  const links: LinkRef[] = [];
  const runs = tableRuns(
    [
      "| item | note |",
      "|---|---|",
      "| **bold** | `code` and [site](https://x.example) |",
    ],
    undefined,
    links,
  );
  expect(runs).toContainEqual({ text: "bold", class: "b" });
  expect(runs).toContainEqual({ text: "code", class: "code" });
  expect(runsText(runs)).not.toContain("*");
  expect(runsText(runs)).not.toContain("`");
  expect(links).toEqual([{ n: 1, text: "site", url: "https://x.example" }]);
  // The column is as wide as what it paints ("bold" is 4), not its source (8).
  const body = runsText(runs).split("\n")[2] ?? "";
  expect(body.startsWith("| bold | code and site")).toBe(true);
  // A header cell keeps its own marks and adds bold.
  const head = tableRuns(["| `k` | v |", "|---|---|", "| 1 | 2 |"]);
  expect(head[1]).toEqual({ text: "k", class: "b code" });
});

test("wide characters wrap by display width and never split", () => {
  const rows = [
    "| 名前 | 説明 |",
    "|---|---|",
    "| 日本語のテキスト | 😀😀😀😀😀😀😀😀😀😀 mixed with ascii words |",
  ];
  const { lines, widest } = tableLines(rows, 28);
  expect(widest).toBeLessThanOrEqual(28);
  const all = lines.join("\n");
  expect(all.isWellFormed()).toBe(true);
  expect(all.match(/😀/g)).toHaveLength(10);
  // The cell's text, cut between characters, reads back in order.
  const first = lines
    .slice(2)
    .map((l) => l.split("|")[1]?.trim())
    .join("");
  expect(first).toBe("日本語のテキスト");
  // Every line of the table is the same width: the columns stay aligned.
  expect(new Set(lines.map((l) => Bun.stringWidth(l))).size).toBe(1);
});

test("parseBlocks passes the width to its tables, and a view builder asks for it each time", () => {
  const md = WIDE.join("\n");
  const [wide] = parseBlocks(md);
  const [narrow] = parseBlocks(md, "b", undefined, 40);
  expect(narrow?.lang).toBe("");
  expect(runsText(wide?.runs ?? []).split("\n")).toHaveLength(4);
  const shown = runsText(narrow?.runs ?? []).split("\n");
  expect(shown.length).toBeGreaterThan(4);
  for (const line of shown)
    expect(Bun.stringWidth(line)).toBeLessThanOrEqual(40);
  let cols = 80;
  const builder = new ViewBuilder(() => cols);
  const lineCount = () =>
    runsText(
      builder.fromMessage({ role: "assistant", content: md })?.blocks[0]
        ?.runs ?? [],
    ).split("\n").length;
  const roomy = lineCount();
  cols = 40;
  expect(lineCount()).toBeGreaterThan(roomy);
});

test("a table's width is the terminal less the log and the code block", () => {
  expect(tableWidth(80, false)).toBe(74);
  expect(tableWidth(120, false)).toBe(114);
  expect(tableWidth(80, true)).toBe(78);
  // Never wider than a narrow terminal leaves, and never below one wide
  // character.
  expect(tableWidth(10, false)).toBe(4);
  expect(tableWidth(4, false)).toBe(2);
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
  // A card's number goes in the pointer: /details N opens that output.
  expect(
    outputRuns(
      Array.from({ length: 12 }, (_, i) => `l${i}`).join("\n"),
      undefined,
      [],
      3,
    ).at(-1),
  ).toEqual({
    text: "\n… 4 more lines · /details 3 for full output",
    class: "note",
  });
  expect(outputRuns("")).toEqual([]);
  expect(outputRuns("a\nb\n\n")).toEqual(["a\nb"]);
});

test("the /details affordance rides the truncation line, and only there", () => {
  const lines = (n: number) =>
    Array.from({ length: n }, (_, i) => `l${i}`).join("\n");
  // Exactly eight lines fill the card: nothing was truncated, no pointer.
  expect(runsText(outputRuns(lines(8), undefined, [], 1))).not.toContain(
    "/details",
  );
  expect(runsText(outputRuns(lines(8)))).not.toContain("more line");
  // Nine lines truncate by one: the count and the pointer share the line.
  expect(runsText(outputRuns(lines(9), undefined, [], 2))).toContain(
    "… 1 more line · /details 2 for full output",
  );
  // A head line (a command's exit) counts toward the eight, like the card.
  const headed = outputRuns(lines(8), { text: "exit 0", bad: false }, [], 4);
  expect(runsText(headed)).toContain(
    "… 1 more line · /details 4 for full output",
  );
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
    "succeeded",
  );
  expect(shown).toBe("exit 0, truncated\nok\n");
  expect(resultSummary("read", "plain", "succeeded")).toBe("plain");
  expect(resultSummary("run_command", "not json", "failed")).toBe("not json");
});

test("a call summary names the path, the command or the arguments", () => {
  const call = (args: Record<string, unknown>) => ({
    function: { name: "x", arguments: args },
  });
  expect(callSummary(call({ path: "src/a.ts" }))).toBe("src/a.ts");
  expect(callSummary(call({ argv: ["bun", "test"] }))).toBe("bun test");
  expect(callSummary(call({ q: 1 }))).toBe('{"q":1}');
  expect(callSummary(undefined)).toBe("");
  // A built-in tool's line is what the call is about, not its JSON.
  const named = (name: string, args: Record<string, unknown>) =>
    callSummary({ function: { name, arguments: args } });
  expect(named("search", { pattern: "slugify", path: "src" })).toBe(
    "slugify in src",
  );
  expect(named("search", { pattern: "slugify" })).toBe("slugify");
  expect(named("web_search", { query: "bun workspaces" })).toBe(
    "bun workspaces",
  );
  expect(named("fetch", { url: "https://bun.sh/docs" })).toBe(
    "https://bun.sh/docs",
  );
  expect(named("task", { prompt: "find the leak\nthen report" })).toBe(
    "find the leak",
  );
  expect(named("task", { prompt: "\n\n  find the leak\nthen" })).toBe(
    "find the leak",
  );
  expect(named("ask_user", { question: "Which store?\nIt matters" })).toBe(
    "Which store?",
  );
  expect(
    named("goal_status", {
      state: "done",
      summary: " tests pass\nall of them",
    }),
  ).toBe("done · tests pass");
  expect(named("goal_status", { state: "blocked", summary: "  " })).toBe(
    "blocked",
  );
  expect(named("todo_write", { todos: [{}, {}] })).toBe("2 items");
  expect(named("todo_write", { todos: [{}] })).toBe("1 item");
  // A wrong-typed argument falls back to the generic line.
  expect(named("fetch", { url: 3 })).toBe('{"url":3}');
});

test("a card names its tool as a reader says it", () => {
  expect(toolTitle("run_command")).toBe("Command");
  expect(toolTitle("ask_user")).toBe("Question");
  expect(toolTitle("goal_status")).toBe("Goal status");
  expect(toolTitle("web_search")).toBe("Web search");
  expect(toolTitle("cortex__status")).toBe("MCP · cortex · status");
  expect(toolTitle("custom")).toBe("custom");
  expect(toolTitle("bad\u001b[31mname")).toBe("badname");
  // A name the model chose starts no line of its own: a line break or a tab
  // in it is one space, on the card, its title and its receipt alike.
  expect(toolTitle("probe\nforged\tname")).toBe("probe forged name");
  const named: Message = {
    role: "tool",
    tool_name: "probe\nforged",
    content: JSON.stringify({ status: "failed", output: "no such tool" }),
  };
  expect(new ViewBuilder().fromMessage(named)?.card.name).toBe("probe forged");
  expect(new ViewBuilder().receiptOf(named)?.name).toBe("probe forged");
  // Only the table's own keys are titles, never what objects inherit.
  for (const name of ["constructor", "toString", "__proto__", "valueOf"])
    expect(toolTitle(name)).toBe(name);
});

test("a gateway's call_tool card is titled by the tool it runs, and names the gateway", () => {
  const via = (args: Record<string, unknown>) => ({
    function: { name: "mcphub__call_tool", arguments: args },
  });
  const validate = via({
    server: "tuimark",
    tool: "validate",
    arguments: { file: "ui/app.tui", strict: true },
  });
  expect(toolTitle("mcphub__call_tool", validate)).toBe(
    "MCP · tuimark · validate",
  );
  expect(callSummary(validate)).toBe(
    'via mcphub · {"file":"ui/app.tui","strict":true}',
  );
  // No server: the tool names it before its first `__`, as the runtime reads it.
  expect(toolTitle("mcphub__call_tool", via({ tool: "cortex__status" }))).toBe(
    "MCP · cortex · status",
  );
  // Arguments that do not name one tool keep the wire name and the whole JSON.
  const vague = via({ server: "a__b", tool: "x" });
  expect(toolTitle("mcphub__call_tool", vague)).toBe(
    "MCP · mcphub · call_tool",
  );
  expect(callSummary(vague)).toBe('{"server":"a__b","tool":"x"}');
  // Only call_tool reads this way, and only with its call at hand.
  expect(toolTitle("mcphub__call_tool")).toBe("MCP · mcphub · call_tool");
  expect(
    toolTitle("cortex__status", {
      function: { name: "cortex__status", arguments: { tool: "a__b" } },
    }),
  ).toBe("MCP · cortex · status");
});

test("a call's duration reads as seconds, then minutes", () => {
  expect(formatTook(0)).toBe("<0.1s");
  expect(formatTook(99)).toBe("<0.1s");
  expect(formatTook(100)).toBe("0.1s");
  expect(formatTook(420)).toBe("0.4s");
  expect(formatTook(9_949)).toBe("9.9s");
  expect(formatTook(12_400)).toBe("12s");
  expect(formatTook(125_000)).toBe("2m 05s");
  expect(formatTook(-5)).toBe("<0.1s");
  // Rounding happens before the unit is chosen.
  expect(formatTook(9_960)).toBe("10s");
  expect(formatTook(59_600)).toBe("1m 00s");
  expect(formatTook(59_400)).toBe("59s");
});

/** The card and the /details receipt of one call and its result. */
function cardOf(
  name: string,
  args: Record<string, unknown>,
  status: string,
  output: string,
) {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [{ id: "c", function: { name, arguments: args } }],
  });
  const message: Message = {
    role: "tool",
    tool_name: name,
    tool_call_id: "c",
    content: JSON.stringify({ status, output }),
  };
  return {
    card: builder.fromMessage(message)?.card,
    receipt: builder.receiptOf(message),
  };
}

/** The runtime's MCP envelope as a tool message carries it. */
function envelope(text: string, isError = false): string {
  return JSON.stringify({
    transport: "success",
    domain: "unknown",
    evidence: "unverified",
    isError,
    text,
  });
}

test("an MCP card shows the server's text under a head that keeps the doubt", () => {
  const raw = envelope(JSON.stringify({ alternatives: ["a", "b"], n: 2 }));
  const { card, receipt } = cardOf(
    "mcphub__resolve_tool",
    { goal: "x" },
    "unverified",
    raw,
  );
  const lines = runsText(card?.out ?? []).split("\n");
  // The card's state says unverified; the head adds what else is unknown.
  expect(card?.status).toBe("unverified");
  expect(card?.warn).toBe(true);
  expect(lines[0]).toBe("domain unknown");
  expect(card?.out[0]).toEqual({
    text: "domain unknown",
    class: "note",
  });
  // The text is pretty-printed and none of the envelope's keys show.
  expect(lines.slice(1)).toEqual([
    "{",
    '  "alternatives": [',
    '    "a",',
    '    "b"',
    "  ],",
    '  "n": 2',
    "}",
  ]);
  expect(runsText(card?.out ?? [])).not.toContain("transport");
  // /details (and so /export) read the same parse, whole.
  expect(receipt?.output.split("\n").slice(0, 2)).toEqual([
    "domain unknown",
    "{",
  ]);
  expect(receipt?.output).not.toContain("isError");
});

test("an MCP error is class err, and text that is not JSON stays as it is", () => {
  const show = (text: string) =>
    runsText(
      cardOf("srv__tool", {}, "unverified", envelope(text)).card?.out ?? [],
    );
  const { card } = cardOf(
    "srv__tool",
    {},
    "failed",
    envelope("rate limited", true),
  );
  expect(card?.out).toEqual([
    { text: "error · unverified · domain unknown", class: "err" },
    "\nrate limited",
  ]);
  // Cut by the runtime's output cap, or a bare scalar: the bytes stay.
  expect(show('{"cut": ')).toBe('domain unknown\n{"cut": ');
  expect(show("42")).toBe("domain unknown\n42");
  // JSON that would not print back the same is shown as sent: a 20-digit id
  // would be rounded, a repeated key dropped, an escape rewritten.
  for (const sent of [
    '{"id": 12345678901234567890, "s": "x"}',
    '{"a": 1, "a": 2}',
    '{"e": "\\u00e9"}',
    '{"n": 1.0}',
  ])
    expect(show(sent)).toBe(`domain unknown\n${sent}`);
  // Spaces inside strings are kept, after an escaped quote too, and do not
  // stop the indenting.
  expect(show('{"t": "a\\" b"}')).toBe('domain unknown\n{\n  "t": "a\\" b"\n}');
  expect(show('{ "say" : "a  b",\n "t": "\\"q\\" " }')).toBe(
    'domain unknown\n{\n  "say": "a  b",\n  "t": "\\"q\\" "\n}',
  );
});

test("an MCP card clips pretty-printed JSON like any output", () => {
  const big = envelope(
    JSON.stringify({ rows: Array.from({ length: 30 }, (_, i) => i) }),
  );
  const { card, receipt } = cardOf("srv__tool", {}, "unverified", big);
  const shown = runsText(card?.out ?? []).split("\n");
  expect(shown).toHaveLength(9);
  expect(shown.at(-1)).toContain("more lines · /details 1 for full output");
  expect(receipt?.output.split("\n").length).toBeGreaterThan(30);
});

test("anything that is not exactly the MCP envelope shows as it did", () => {
  const show = (name: string, output: string) =>
    runsText(cardOf(name, {}, "unverified", output).card?.out ?? []);
  const full = {
    transport: "success",
    domain: "unknown",
    evidence: "unverified",
    isError: false,
    text: "t",
  };
  for (const bad of [
    { ...full, text: 5 },
    { ...full, isError: "no" },
    { ...full, domain: undefined },
    { ...full, transport: 1 },
    { ...full, evidence: null },
  ]) {
    const output = JSON.stringify(bad);
    expect(show("srv__tool", output)).toBe(output);
  }
  expect(show("srv__tool", "not json")).toBe("not json");
  expect(show("srv__tool", "[1,2]")).toBe("[1,2]");
  expect(show("srv__tool", "null")).toBe("null");
  // A built-in tool's output is never read as an envelope.
  const output = JSON.stringify(full);
  expect(show("fetch", output)).toBe(output);
});

test("a finished edit's card shows the change its own strings made", () => {
  const { card, receipt } = cardOf(
    "edit",
    {
      path: "src/a.ts",
      old_string: "keep\nold line\nkeep too",
      new_string: "keep\nnew line\nkeep too",
    },
    "succeeded",
    "Updated src/a.ts. Verification has not been run.",
  );
  expect(card).toMatchObject({
    title: "Edit",
    mark: "✓",
    took: "",
    args: "src/a.ts",
    args_path: true,
    args_line: false,
  });
  expect(card?.out).toEqual([
    "Updated src/a.ts. Verification has not been run.\n  keep\n",
    { text: "- old line", class: "del" },
    "\n",
    { text: "+ new line", class: "add" },
    "\n  keep too",
  ]);
  // /details holds the same rows, uncut.
  expect(receipt?.output).toBe(
    "Updated src/a.ts. Verification has not been run.\n  keep\n- old line\n+ new line\n  keep too",
  );
});

test("an edit or a write the runtime applied says applied; other tools say succeeded", () => {
  const edit = cardOf("edit", { path: "a" }, "succeeded", "Updated a.");
  expect(edit.card?.status).toBe("applied");
  expect(edit.receipt?.status).toBe("applied");
  const write = cardOf("write", { path: "b" }, "succeeded", "Created b.");
  expect(write.card?.status).toBe("applied");
  const run = cardOf("run_command", { argv: ["true"] }, "succeeded", "ok");
  expect(run.card?.status).toBe("succeeded");
  // Only success has the narrower word.
  expect(cardOf("edit", { path: "a" }, "failed", "no").card?.status).toBe(
    "failed",
  );
});

test("an edit that did not land shows no change", () => {
  // unverified is MCP's word (a server's result) and a status-less
  // receipt's: neither says an edit was applied.
  for (const status of ["denied", "failed", "outcome_unknown", "unverified"]) {
    const { card, receipt } = cardOf(
      "edit",
      { path: "a", old_string: "x", new_string: "y" },
      status,
      "Edit denied by host",
    );
    expect(runsText(card?.out ?? [])).toBe("Edit denied by host");
    expect(receipt?.output).toBe("Edit denied by host");
  }
  const denied = cardOf("edit", { path: "a" }, "denied", "no").card;
  expect(denied).toMatchObject({ mark: "✗", bad: true });
  const unknown = cardOf("edit", { path: "a" }, "outcome_unknown", "?").card;
  expect(unknown).toMatchObject({ mark: "!", warn: true });
});

test("a created file shows its lines; a replaced one shows none", () => {
  const created = cardOf(
    "write",
    { path: "new.txt", content: "one\ntwo\n" },
    "succeeded",
    "Created new.txt. Verification has not been run.",
  );
  expect(created.card?.out).toEqual([
    "Created new.txt. Verification has not been run.\n",
    { text: "+ one\n+ two", class: "add" },
  ]);
  // Like an edit's rows: a carriage return is shown, not obeyed.
  const crlf = cardOf(
    "write",
    { path: "dos.txt", content: "one\r\ntwo" },
    "succeeded",
    "Created dos.txt. Verification has not been run.",
  );
  expect(crlf.card?.out).toEqual([
    "Created dos.txt. Verification has not been run.\n",
    { text: "+ one␍\n+ two", class: "add" },
  ]);
  const empty = cardOf(
    "write",
    { path: "empty.txt", content: "" },
    "succeeded",
    "Created empty.txt. Verification has not been run.",
  );
  expect(runsText(empty.card?.out ?? [])).toBe(
    "Created empty.txt. Verification has not been run.",
  );
  // A replacement's old text is not in the call: no rows claim a diff.
  const replaced = cardOf(
    "write",
    { path: "old.txt", content: "new body" },
    "succeeded",
    "Replaced old.txt. Verification has not been run.",
  );
  expect(runsText(replaced.card?.out ?? [])).toBe(
    "Replaced old.txt. Verification has not been run.",
  );
});

test("a long change is cut with the card, and /details keeps all of it", () => {
  const before = Array.from({ length: 30 }, (_, i) => `old ${i}`).join("\n");
  const after = Array.from({ length: 30 }, (_, i) => `new ${i}`).join("\n");
  const { card, receipt } = cardOf(
    "edit",
    { path: "a", old_string: before, new_string: after },
    "succeeded",
    "Updated a.",
  );
  const shown = runsText(card?.out ?? []);
  expect(shown).toContain("- old 0");
  expect(shown).not.toContain("+ new 29");
  expect(shown).toContain("… 53 more lines · /details 1 for full output");
  expect(receipt?.output).toContain("+ new 29");
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

test("receiptOf keeps the whole receipt the card truncates", () => {
  const builder = new ViewBuilder();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [
      { id: "c1", function: { name: "read", arguments: { path: "big.txt" } } },
    ],
  });
  const output = Array.from({ length: 20 }, (_, i) => `line ${i}`).join("\n");
  const message: Message = {
    role: "tool",
    tool_name: "read",
    tool_call_id: "c1",
    content: JSON.stringify({ status: "failed", output }),
  };
  // The card keeps eight lines; the receipt keeps all twenty, with the
  // name and status exactly as the card shows them.
  expect(runsText(builder.fromMessage(message)?.card.out ?? [])).not.toContain(
    "line 19",
  );
  expect(builder.receiptOf(message)).toEqual({
    n: 1,
    name: "read",
    status: "failed",
    args: "big.txt",
    output,
  });
  // A command receipt carries its exit head line, like its card.
  const command = builder.receiptOf({
    role: "tool",
    tool_name: "run_command",
    content: JSON.stringify({
      status: "failed",
      output: JSON.stringify({ output: "boom\n", exitCode: 2 }),
    }),
  });
  expect(command?.output).toBe("exit 2\nboom\n");
  // Only tool messages are receipts.
  expect(
    builder.receiptOf({ role: "assistant", content: "hi" }),
  ).toBeUndefined();
  expect(builder.receiptOf({ role: "user", content: "hi" })).toBeUndefined();
});

test("each tool output has its own number, its card's and its receipt's alike", () => {
  const long = Array.from({ length: 12 }, (_, i) => `l${i}`).join("\n");
  // Two outputs under one call id, as a provider that numbers per reply sends.
  const tool = (): Message => ({
    role: "tool",
    tool_name: "read",
    tool_call_id: "call_0",
    content: JSON.stringify({ status: "succeeded", output: long }),
  });
  const [first, second] = [tool(), tool()];
  const builder = new ViewBuilder();
  const pointer = (message: Message) =>
    runsText(builder.fromMessage(message)?.card.out ?? [])
      .split("\n")
      .at(-1);
  expect(pointer(first)).toBe("… 4 more lines · /details 1 for full output");
  // The receipt first, the card after: still one number.
  expect(builder.receiptOf(second)?.n).toBe(2);
  expect(pointer(second)).toBe("… 4 more lines · /details 2 for full output");
  // Built again, a card keeps its number.
  expect(pointer(first)).toBe("… 4 more lines · /details 1 for full output");
  expect(builder.receiptOf(first)?.n).toBe(1);
  // A builder that takes over goes on from there.
  expect(builder.nextReceiptNumber).toBe(3);
  expect(
    new ViewBuilder(undefined, builder.nextReceiptNumber).receiptOf(tool())?.n,
  ).toBe(3);
});

test("capDetailsOutput keeps 64000 characters and says what it cut", () => {
  expect(DETAILS_MAX_CHARS).toBe(64_000);
  expect(DETAILS_TRUNCATED_TAIL).toBe(
    "… output truncated for the viewer (first 64000 characters)",
  );
  expect(capDetailsOutput("short")).toBe("short");
  const capped = capDetailsOutput(`${"x".repeat(70_000)}TAIL-MARKER`);
  expect(capped).toBe(
    `${"x".repeat(DETAILS_MAX_CHARS)}\n${DETAILS_TRUNCATED_TAIL}`,
  );
  expect(capped).not.toContain("TAIL-MARKER");
  // The cut lands on a code point, never inside a surrogate pair.
  const emoji = capDetailsOutput("😀".repeat(70_000));
  expect(emoji.startsWith("😀".repeat(DETAILS_MAX_CHARS))).toBe(true);
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

test("a running card spins; with motion reduced the still glyph stands where the spinner turns", () => {
  for (const state of ["requested", "approval", "running"] as const) {
    expect(liveToolView(undefined, "read", state).card, state).toMatchObject({
      running: true,
      spin: true,
      still: false,
      mark: "",
    });
    expect(
      liveToolView(undefined, "read", state, true).card,
      state,
    ).toMatchObject({ running: true, spin: false, still: true, mark: "⠿" });
  }
  // A call that goes on after it was held runs, either way.
  const held = onHold(liveToolView(undefined, "edit", "approval").card, "hold");
  expect(held).toMatchObject({ running: false, spin: false, still: false });
  expect(runningCard(held)).toMatchObject({
    status: "running",
    running: true,
    spin: true,
    still: false,
    mark: "",
  });
  expect(runningCard(held, true)).toMatchObject({
    status: "running",
    running: true,
    spin: false,
    still: true,
    mark: STILL_GLYPH,
  });
  // A finished card never shows either.
  const builder = new ViewBuilder();
  const done = builder.fromMessage({
    role: "tool",
    tool_name: "read",
    content: JSON.stringify({ status: "succeeded", output: "ok" }),
  });
  expect(done?.card).toMatchObject({ spin: false, still: false, mark: "✓" });
  expect(NO_CARD).toMatchObject({ spin: false, still: false });
});

test("a notice and an error are quoted UI-local views: one role flag each, every line kept", () => {
  const builder = new ViewBuilder();
  const flags = (view: MessageView) => [
    view.is_user,
    view.is_asst,
    view.is_tool,
    view.is_notice,
    view.is_error,
  ];
  expect(flags(builder.error("x"))).toEqual([false, false, false, false, true]);
  const notice = builder.notice("first line\nsecond line");
  expect(flags(notice)).toEqual([false, false, false, true, false]);
  // Quoted, so it reads as the client's words; the text itself is whole.
  expect(notice.blocks.length).toBeGreaterThan(0);
  expect(notice.blocks.every((b) => b.quote)).toBe(true);
  const text = notice.blocks.map((b) => runsText(b.runs)).join("\n");
  expect(text).toContain("first line");
  expect(text).toContain("second line");
  // Every other view the builder makes carries both false.
  const user = builder.fromMessage({ role: "user", content: "hi" });
  expect(user && flags(user).slice(3)).toEqual([false, false]);
  expect(flags(IDLE_LIVE).slice(3)).toEqual([false, false]);
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

test("fences: one under a list item is code, without the item's indent", () => {
  const blocks = parseBlocks(
    [
      "1. Install:",
      "",
      "    ```sh",
      "    bun install --frozen-lockfile",
      "      rm -rf *.tmp",
      "    ```",
      "2. Run it",
    ].join("\n"),
  );
  expect(blocks.map((b) => [b.item, b.code])).toEqual([
    [true, false],
    [false, true],
    [true, false],
  ]);
  expect(blocks[1]?.lang).toBe("sh");
  expect(runsText(blocks[1]?.runs ?? [])).toBe(
    "bun install --frozen-lockfile\n  rm -rf *.tmp",
  );
  // A mark indented further than three past the opening is code, not the end.
  const open = parseBlocks("```md\n    ```\nstill code\n```\nafter");
  expect(runsText(open[0]?.runs ?? [])).toBe("    ```\nstill code");
  expect(open[1]?.para).toBe(true);
  expect(startsOpenFence("    ```sh\n    bun i")).toBe(true);
  expect(startsOpenFence("    ```sh\n    bun i\n    ```")).toBe(false);
  // The item's own indented paragraph keeps the fence under it.
  const nested = parseBlocks(
    [
      "1. Install:",
      "",
      "   Run this:",
      "",
      "    ```sh",
      "    ls",
      "    ```",
    ].join("\n"),
  );
  expect(nested.map((b) => Boolean(b.code))).toEqual([false, false, true]);
});

test("fences: an indented one in a paragraph is text; the next item ends one left open", () => {
  // Four spaces in a paragraph do not open code: the prose after stays prose.
  const prose = parseBlocks(
    ["Use this marker:", "    ```", "After that, run the tests."].join("\n"),
  );
  expect(prose.map((b) => Boolean(b.code))).toEqual([false]);
  expect(runsText(prose[0]?.runs ?? [])).toContain(
    "After that, run the tests.",
  );
  // Nor after a paragraph that ended the list.
  const after = parseBlocks(
    ["1. One", "", "Then:", "    ```", "text"].join("\n"),
  );
  expect(after.some((b) => b.code)).toBe(false);
  // An item's fence left open ends at the next item, which stays an item.
  const open = parseBlocks(
    ["1. Run:", "    ```", "    ls", "2. Next"].join("\n"),
  );
  expect(open.map((b) => [Boolean(b.item), Boolean(b.code)])).toEqual([
    [true, false],
    [false, true],
    [true, false],
  ]);
  expect(runsText(open[1]?.runs ?? [])).toBe("ls");
  // The stream sees the same end.
  expect(startsOpenFence("    ```\n    ls\n2. Next")).toBe(false);
  // A fence at column 0 is never ended by an item inside it.
  const top = parseBlocks(["```md", "1. inside", "```"].join("\n"));
  expect(top.map((b) => Boolean(b.code))).toEqual([true]);
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
    ["```", "z".repeat(MAX_CODE_CHARS + 500), "```"].join("\n"),
  );
  expect(runsText(code?.runs ?? [])).toHaveLength(MAX_CODE_CHARS);
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
  expect(whole).toBe("Use bold, code, em and a link.");
  // What a prefix shows is its letters, spaces and punctuation, in order: only
  // markers (`*`, `` ` ``, brackets) and a finished link's url may be missing.
  const kept = (s: string) => s.replace(/[^A-Za-z ,.]/g, "");
  for (let n = 0; n <= message.length; n++) {
    const head = message.slice(0, n);
    const visible = head.includes("(https://x.dev/a_b)")
      ? head.replace("https://x.dev/a_b", "")
      : head;
    expect(kept(runsText(inlineRuns(head))), head).toBe(kept(visible));
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

test("an opening fence is classified in linear time, whatever its whitespace", () => {
  // "```" + spaces + "`" is no fence (a backtick in the info string); a
  // backtracking match took seconds on it.
  for (const tail of ["`", "x`"]) {
    const line = `\`\`\`${" ".repeat(200_000)}${tail}`;
    const start = performance.now();
    expect(startsOpenFence(line)).toBe(false);
    expect(parseBlocks(line)[0]?.code).toBe(false);
    expect(performance.now() - start).toBeLessThan(100);
  }
  const tildes = `${"~".repeat(200_000)}\``;
  const start = performance.now();
  expect(startsOpenFence(tildes)).toBe(false);
  expect(performance.now() - start).toBeLessThan(100);
  expect(startsOpenFence("```\nx")).toBe(true);
  expect(startsOpenFence("```   ts\nx")).toBe(true);
  expect(startsOpenFence("```a`b\nx")).toBe(false);
  expect(parseBlocks("```   ts  x\ny\n```")[0]?.lang).toBe("ts");
});

test("a backslash is literal inside a code span", () => {
  expect(inlineRuns("path `C:\\` ok")).toEqual(["path ", code("C:\\"), " ok"]);
  expect(inlineRuns("use `\\` to")).toEqual(["use ", code("\\"), " to"]);
  // Outside a span the escaped backtick still cannot open one.
  expect(inlineRuns("\\`not code\\`")).toEqual(["`not code`"]);
  // An escaped tick leaves the rest of its run free to open a span.
  expect(inlineRuns("\\``x`")).toEqual(["`", code("x")]);
  // An escaped backslash does not escape the tick after it.
  expect(inlineRuns("\\\\`x`")).toEqual(["\\", code("x")]);
});

test("a link is its text in class link, then its [n] source marker", () => {
  const links: LinkRef[] = [];
  expect(inlineRuns("see [the docs](https://x.dev/a) now", links)).toEqual([
    "see ",
    { text: "the docs", class: "link" },
    { text: "[1]", class: "url" },
    " now",
  ]);
  expect(links).toEqual([{ n: 1, text: "the docs", url: "https://x.dev/a" }]);
  const bold: LinkRef[] = [];
  expect(inlineRuns("[**bold** link](u)", bold)).toEqual([
    { text: "bold", class: "b link" },
    { text: " link", class: "link" },
    { text: "[1]", class: "url" },
  ]);
  expect(bold).toEqual([{ n: 1, text: "bold link", url: "u" }]);
  // Without a collector (the live stream) the link is just its text: no
  // source number can exist before the sources row is rendered.
  expect(inlineRuns("see [the docs](https://x.dev/a) now")).toEqual([
    "see ",
    { text: "the docs", class: "link" },
    " now",
  ]);
});

test("an image is a link that says so; an escaped ! stays a !", () => {
  const links: LinkRef[] = [];
  expect(
    inlineRuns("see ![a diagram](https://x.dev/d.png) here", links),
  ).toEqual([
    "see image: ",
    { text: "a diagram", class: "link" },
    { text: "[1]", class: "url" },
    " here",
  ]);
  expect(links).toEqual([
    { n: 1, text: "a diagram", url: "https://x.dev/d.png" },
  ]);
  expect(inlineRuns("![](https://x.dev/e.png)", [])).toEqual([
    "image",
    { text: "[1]", class: "url" },
  ]);
  expect(inlineRuns("wow\\![x](u)")).toEqual([
    "wow!",
    { text: "x", class: "link" },
  ]);
  expect(inlineRuns("wow![x]")).toEqual(["wow![x]"]);
});

test("a link label the model wrapped is one line in the sources row", () => {
  const links: LinkRef[] = [];
  expect(
    runsText(inlineRuns("read [the\nguide](https://x.dev/g)", links)),
  ).toBe("read the\nguide[1]");
  expect(links).toEqual([{ n: 1, text: "the guide", url: "https://x.dev/g" }]);
});

test("one url gets one number, whatever the message cites it", () => {
  const links: LinkRef[] = [];
  const runs = inlineRuns(
    "[a](https://x.dev/1) then [b](https://x.dev/2) then [a again](https://x.dev/1)",
    links,
  );
  expect(runs).toEqual([
    { text: "a", class: "link" },
    { text: "[1]", class: "url" },
    " then ",
    { text: "b", class: "link" },
    { text: "[2]", class: "url" },
    " then ",
    { text: "a again", class: "link" },
    { text: "[1]", class: "url" },
  ]);
  expect(links).toEqual([
    { n: 1, text: "a", url: "https://x.dev/1" },
    { n: 2, text: "b", url: "https://x.dev/2" },
  ]);
});

test("sources stop at MAX_LINKS, and a long link text is clipped", () => {
  const links: LinkRef[] = [];
  const source = Array.from(
    { length: MAX_LINKS + 2 },
    (_, i) => `[link ${i}](https://x.dev/${i})`,
  ).join(" ");
  const runs = inlineRuns(source, links);
  expect(links).toHaveLength(MAX_LINKS);
  expect(links.at(-1)?.n).toBe(MAX_LINKS);
  expect(runsText(runs)).not.toContain(`[${MAX_LINKS + 1}]`);
  const long: LinkRef[] = [];
  inlineRuns(`[${"word ".repeat(30)}](https://x.dev/long)`, long);
  expect(long[0]?.text).toHaveLength(MAX_LINK_TEXT);
  expect(long[0]?.text.endsWith("…")).toBe(true);
});

test("a link address may hold parentheses, and bad links stay text", () => {
  const links: LinkRef[] = [];
  expect(
    inlineRuns("[w](https://en.wikipedia.org/wiki/A_(b)) end", links),
  ).toEqual([
    { text: "w", class: "link" },
    { text: "[1]", class: "url" },
    " end",
  ]);
  expect(links[0]?.url).toBe("https://en.wikipedia.org/wiki/A_(b)");
  expect(inlineRuns("(see [x](y))")).toEqual([
    "(see ",
    { text: "x", class: "link" },
    ")",
  ]);
  expect(inlineRuns("[a](b c)")).toEqual(["[a](b c)"]);
  expect(inlineRuns("[a] (b)")).toEqual(["[a] (b)"]);
  expect(inlineRuns("[a](b")).toEqual(["[a](b"]);
  expect(inlineRuns("[[nested]](u)")).toEqual([
    { text: "[nested]", class: "link" },
  ]);
  // An empty destination is no source: it stays link text with no marker.
  expect(inlineRuns("[a]()")).toEqual([{ text: "a", class: "link" }]);
  const twice: LinkRef[] = [];
  expect(inlineRuns("[x](y)".repeat(2), twice)).toHaveLength(4);
  expect(twice).toHaveLength(1);
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
  ]);
  for (const text of ["**😀😀**", "*👨‍👩‍👧*x", "[🇲🇽](u)", "a`🎉`b", "_日本_"]) {
    const out = runsText(inlineRuns(text));
    expect(out.isWellFormed()).toBe(true);
    for (const ch of Array.from(out)) expect(ch.length).toBeGreaterThan(0);
  }
});

test("the visible text is the source minus its markers, whatever the input", () => {
  const source = "a **b** `c` [d](e) ~~f~~ _g_ 日本 😀 \\* *h";
  expect(runsText(inlineRuns(source))).toBe("a b c d f g 日本 😀 * *h");
});

/** The least time of a few runs of `run`, in ms (a busy machine only adds). */
function bestOf(run: () => void): number {
  let best = Number.POSITIVE_INFINITY;
  for (let i = 0; i < 5; i++) {
    const start = performance.now();
    run();
    best = Math.min(best, performance.now() - start);
  }
  return best;
}

test("pathological input scales linearly: four times the text, about four times the time", () => {
  /** An inline case; `literal` says none of its markers can close, so it is shown as written. */
  const inline = (
    name: string,
    build: (n: number) => string,
    literal = false,
  ) => ({
    name,
    build,
    literal,
    parse: (text: string) => runsText(inlineRuns(text)),
  });
  const blocks = (name: string, build: (n: number) => string) => ({
    name,
    build,
    literal: false,
    parse: (text: string) => String(parseBlocks(text).length),
  });
  const cases = [
    inline("open stars", (n) => "*a ".repeat(n), true),
    inline("mixed openers", (n) => "**a _b ~~c ".repeat(n / 4), true),
    inline("link openers", (n) => "[a](".repeat(n / 2), true),
    inline("brackets", (n) => "[a".repeat(n), true),
    inline("closers", (n) => "](".repeat(n), true),
    inline("backtick runs", (n) => "`a``b```c".repeat(n / 4)),
    inline("star runs", (n) => "*a**b***c****".repeat(n / 8)),
    inline(
      "links and parens",
      (n) => `${"[a](x".repeat(n / 8)}${"(".repeat(n)}`,
      true,
    ),
    inline(
      "emphasis that closes late",
      (n) => "*a ".repeat(n / 2) + "a* ".repeat(n / 2),
    ),
    blocks("a fence line of spaces", (n) => `\`\`\`${" ".repeat(n)}\``),
    blocks("a long table", (n) =>
      [
        "| a | b | c |",
        "|---|---|---|",
        ...Array.from({ length: n / 4 }, (_, i) => `| ${i} | x | yy |`),
      ].join("\n"),
    ),
    blocks(
      "a long code block",
      (n) => `\`\`\`\n${"let x = 1;\n".repeat(n / 4)}\`\`\``,
    ),
  ];
  const size = 10_000;
  for (const { name, build, literal, parse } of cases) {
    const small = build(size);
    const large = build(size * 4);
    const shown = parse(large);
    expect(shown.length, name).toBeGreaterThan(0);
    if (literal) expect(shown, name).toBe(large);
    // Linear is about 4x; a quadratic scan is 16x (the limit is 8x). The floor keeps a case
    // that takes a millisecond from failing on timer noise. A shared runner can still stall
    // one measurement (a 40k case took 29 ms against a 3 ms floor on macOS CI), so a case
    // is measured up to three times: a quadratic one is over the limit every time.
    let before = 0;
    let after = 0;
    for (let attempt = 0; attempt < 3; attempt++) {
      before = Math.max(
        bestOf(() => parse(small)),
        3,
      );
      after = bestOf(() => parse(large));
      if (after < before * 8) break;
    }
    expect(
      after,
      `${name}: ${before.toFixed(1)} ms to ${after.toFixed(1)} ms`,
    ).toBeLessThan(before * 8);
  }
});

// ---------------------------------------------------------- links and images

const TINY_PNG =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

test("a message's bodies share one numbered source list", () => {
  const links: LinkRef[] = [];
  const blocks = parseBlocks(
    [
      "See [the docs](https://x.dev/a).",
      "",
      "- and [the mirror](https://x.dev/b)",
      "",
      "The [docs](https://x.dev/a) again, plus [one more](https://x.dev/c).",
    ].join("\n"),
    "b",
    links,
  );
  expect(links).toEqual([
    { n: 1, text: "the docs", url: "https://x.dev/a" },
    { n: 2, text: "the mirror", url: "https://x.dev/b" },
    { n: 3, text: "one more", url: "https://x.dev/c" },
  ]);
  const all = runsText(blocks.flatMap((block) => block.runs));
  expect(all.match(/\[1\]/g)).toHaveLength(2);
  expect(all).toContain("[2]");
  expect(all).toContain("[3]");
  expect(all).not.toContain("https://x.dev");
});

test("images are named from their magic bytes and capped", () => {
  expect(imageMime(TINY_PNG)).toBe("image/png");
  expect(
    imageMime(Buffer.from([0xff, 0xd8, 0xff, 0xe0]).toString("base64")),
  ).toBe("image/jpeg");
  expect(imageMime(Buffer.from("GIF89a……").toString("base64"))).toBe(
    "image/gif",
  );
  expect(imageMime(Buffer.from("RIFF0000WEBPVP8 ").toString("base64"))).toBe(
    "image/webp",
  );
  expect(
    imageMime(Buffer.from("not an image").toString("base64")),
  ).toBeUndefined();

  const two = messageImages([TINY_PNG, TINY_PNG]);
  expect(two.images).toEqual([
    {
      mime: "image/png",
      b64: TINY_PNG,
      src: `data:image/png;base64,${TINY_PNG}`,
      alt: "image 1 (png)",
    },
    {
      mime: "image/png",
      b64: TINY_PNG,
      src: `data:image/png;base64,${TINY_PNG}`,
      alt: "image 2 (png)",
    },
  ]);
  expect(two.more).toBe(0);

  const capped = messageImages([TINY_PNG, TINY_PNG, TINY_PNG]);
  expect(capped.images).toHaveLength(MAX_IMAGES);
  expect(capped.more).toBe(1);

  const huge = messageImages(["A".repeat(MAX_IMAGE_B64 + 1), TINY_PNG]);
  expect(huge.images).toHaveLength(1);
  expect(huge.more).toBe(1);

  expect(messageImages(undefined)).toEqual({ images: [], more: 0 });
});

test("a tool message's images reach its view, and every view carries the flags", () => {
  const builder = new ViewBuilder();
  const view = builder.fromMessage({
    role: "tool",
    tool_name: "read_image",
    content: JSON.stringify({ status: "succeeded", output: "attached" }),
    images: [TINY_PNG, TINY_PNG, TINY_PNG],
  });
  expect(view?.images).toHaveLength(MAX_IMAGES);
  expect(view?.images_more).toBe(1);
  expect(view?.images[0]?.src.startsWith("data:image/png;base64,")).toBe(true);
  expect(view?.links).toEqual([]);

  const assistant = builder.fromMessage({
    role: "assistant",
    content: "See [the docs](https://x.dev/a).",
  });
  expect(assistant?.links).toEqual([
    { n: 1, text: "the docs", url: "https://x.dev/a" },
  ]);
  expect(assistant?.images).toEqual([]);
  expect(assistant?.images_more).toBe(0);
});

test("a goal's host-written message is the client's notice, live and on resume", () => {
  const builder = new ViewBuilder();
  const goal = builder.fromMessage({
    role: "user",
    content:
      "[goal 2/8] Continue toward the goal: ship it. Last status: half way.\nYou ended the last turn without goal_status: call it before you end this one.",
  });
  expect(goal).toMatchObject({ is_notice: true, is_user: false });
  // Its first line only: the note to the model is not the user's to read.
  expect(goal?.blocks.map((block) => runsText(block.runs))).toEqual([
    "[goal 2/8] Continue toward the goal: ship it. Last status: half way.",
  ]);
  // The user's own words stay theirs, a marker inside them too.
  expect(
    builder.fromMessage({
      role: "user",
      content: "what does [goal 1/8] mean?",
    }),
  ).toMatchObject({ is_user: true, is_notice: false });
});

test("fencedCodeBlocks returns each fenced block's text exactly as written, in order", () => {
  const source = [
    "Intro.",
    "",
    "```ts",
    "const a = 1;",
    "\tconst b = 2; // a tab stays a tab",
    "```",
    "",
    "| not | code |",
    "|:--|--:|",
    "| 1 | 2 |",
    "",
    "~~~sh",
    "echo ``` inside",
    "~~~",
    "",
    "````md",
    "```",
    "nested fence",
    "```",
    "````",
  ].join("\n");
  expect(fencedCodeBlocks(source)).toEqual([
    { lang: "ts", text: "const a = 1;\n\tconst b = 2; // a tab stays a tab" },
    { lang: "sh", text: "echo ``` inside" },
    { lang: "md", text: "```\nnested fence\n```" },
  ]);
  expect(fencedCodeBlocks("no code here")).toEqual([]);
  expect(fencedCodeBlocks("")).toEqual([]);
});

test("fencedCodeBlocks: an unclosed fence runs to the end, a fence under a list item counts, CRLF is a line end", () => {
  expect(fencedCodeBlocks("Before\n\n```py\nprint(1)\nprint(2)")).toEqual([
    { lang: "py", text: "print(1)\nprint(2)" },
  ]);
  expect(
    fencedCodeBlocks("1. Install:\n\n    ```sh\n    bun add x\n    ```\n"),
  ).toEqual([{ lang: "sh", text: "bun add x" }]);
  // Indented four with no list above, it is an indented paragraph, not a fence.
  expect(
    fencedCodeBlocks("Text\n\n    ```sh\n    bun add x\n    ```\n"),
  ).toEqual([]);
  expect(fencedCodeBlocks("```\r\na\r\nb\r\n```\r\n")).toEqual([
    { lang: "", text: "a\nb" },
  ]);
  // The block copied is the model's text, not the view's: a reply past the
  // view's bounds still copies whole.
  const long = `\`\`\`\n${"x".repeat(20000)}\n\`\`\``;
  expect(fencedCodeBlocks(long)[0]?.text.length).toBe(20000);
});

test("fencedCodeBlocks and the view agree on what is a code block", () => {
  const sources = [
    // A lazy line after a list item, then an indented fence under it.
    "- step\nlazy line\n    ```js\n    second\n    ```",
    // A table row and a quote under an item end the item's hold on a deep fence.
    "- item\n\n    | a | b |\n    |--|--|\n\n    ```js\n    x\n    ```",
    "- item\n\n    > quoted\n\n    ```js\n    after quote\n    ```",
    // Indented four with no list above: not a fence, in the view or here.
    "Text\n\n    ```sh\n    bun add x\n    ```\n",
    // A numbered item with its fence, a second item, and a later fence.
    "1. one\n\n   ```sh\n   a\n   ```\n2. two\n\n```sh\nb\n```\n",
    "Plain\n\n```\nno lang\n```\n\n~~~\ntilde\n~~~\n",
    "```py\nunclosed\nstill code",
    // A table is a code block of the view's, but not a fenced one.
    "| a | b |\n|:--|--:|\n| 1 | 2 |\n\n```ts\nreal\n```\n",
  ];
  for (const source of sources) {
    const view = parseBlocks(source).filter(
      (block) => block.code && !block.table,
    );
    expect(fencedCodeBlocks(source), source).toHaveLength(view.length);
  }
  // The cases that used to disagree, with the text they keep.
  expect(fencedCodeBlocks(sources[0] ?? "")).toEqual([
    { lang: "js", text: "second" },
  ]);
  // After an indented table row the deep fence is no fence, here as in the view.
  expect(fencedCodeBlocks(sources[1] ?? "")).toEqual([]);
  expect(fencedCodeBlocks(sources[2] ?? "")).toEqual([]);
});

test("fencedCodeBlocks keeps an empty fence as an empty block and the whole of a long reply", () => {
  expect(fencedCodeBlocks("```\n```")).toEqual([{ lang: "", text: "" }]);
  expect(fencedCodeBlocks("```\n\n```")).toEqual([{ lang: "", text: "" }]);
  // Past the view's character bound the blocks still copy whole.
  const huge = `${"filler\n\n".repeat(MAX_MESSAGE_CHARS / 4)}\`\`\`\nlast one\n\`\`\``;
  expect(huge.length).toBeGreaterThan(MAX_MESSAGE_CHARS);
  expect(fencedCodeBlocks(huge).at(-1)?.text).toBe("last one");
});

// ------------------------------------------------- cuts that land inside text

test("a message cut at the character bound never ends in half an emoji", () => {
  // The bound falls between the two halves of the first emoji.
  let text = "";
  while (text.length + 1002 < MAX_MESSAGE_CHARS - 1)
    text += `${"y".repeat(1000)}\n\n`;
  text += "z".repeat(MAX_MESSAGE_CHARS - 1 - text.length);
  text += "😀😀 tail";
  expect(text.charCodeAt(MAX_MESSAGE_CHARS - 1)).toBe(0xd83d);
  const blocks = parseBlocks(text);
  for (const block of blocks)
    expect(runsText(block.runs).isWellFormed()).toBe(true);
  // The note counts what is left out in characters: two emoji and " tail".
  expect(runsText(blocks.at(-1)?.runs ?? [])).toBe(
    "… message truncated in this view (7 more characters)",
  );
});

test("the reasoning's tail never starts with half an emoji", () => {
  // 802 units long, so the last THINK_TRIM start on the low half of an emoji.
  const text = `a${"😀".repeat(400)}b`;
  expect(text.charCodeAt(text.length - THINK_TRIM)).toBe(0xde00);
  const tail = thinkingTail(text);
  expect(tail.isWellFormed()).toBe(true);
  expect(tail).toBe(text.slice(text.length - THINK_TRIM + 1));
  // Cut at a line start when there is one, whole characters either way.
  expect(thinkingTail(`${"x".repeat(600)}\nlast line`)).toBe("last line");
  expect(thinkingTail("short")).toBe("short");
});

test("a paragraph, quote or item past the bound says how much it dropped", () => {
  const over = (text: string) => runsText(parseBlocks(text)[0]?.runs ?? []);
  const long = "x".repeat(MAX_PARA_CHARS + 37);
  for (const source of [long, `> ${long}`, `- ${long}`])
    expect(over(source)).toBe(
      `${"x".repeat(MAX_PARA_CHARS)}\n… 37 more characters`,
    );
  // Characters, not UTF-16 units, and no emoji is split.
  const emoji = over("😀".repeat(MAX_PARA_CHARS + 5));
  expect(emoji.isWellFormed()).toBe(true);
  expect(emoji.endsWith("\n… 5 more characters")).toBe(true);
  // At the bound exactly, nothing is dropped and nothing is said.
  expect(over("x".repeat(MAX_PARA_CHARS))).toBe("x".repeat(MAX_PARA_CHARS));
});

test("a tab in code reaches the next tab stop; in prose it is two spaces", () => {
  const shown = (source: string) =>
    runsText(parseBlocks(source)[0]?.runs ?? []);
  expect(shown("```make\nall:\n\tgo build\n\ta\tb\n```")).toBe(
    "all:\n    go build\n    a   b",
  );
  expect(shown("```\nid\tname\nlonger\tx\n```")).toBe("id  name\nlonger  x");
  // Under a list item the fence's own indent comes off first: a tab reaching
  // past it leaves what is left of the stop.
  const code = parseBlocks("- item\n  ```go\n\tx := 1\n  ```")[1];
  expect(runsText(code?.runs ?? [])).toBe("  x := 1");
  expect(shown("a\tb")).toBe("a  b");
  // The copy keeps the tab as written.
  expect(fencedCodeBlocks("```\n\tx\n```")).toEqual([
    { lang: "", text: "\tx" },
  ]);
});

test("a fence closes on the same mark, as long, indented at most three past it", () => {
  const codes = (source: string) => parseBlocks(source).map((b) => b.code);
  expect(codes("```\nx\n```\nafter")).toEqual([true, false]);
  expect(codes("```\nx\n`````  \nafter")).toEqual([true, false]);
  expect(codes("~~~\nx\n~~~~\nafter")).toEqual([true, false]);
  expect(codes("  ```\nx\n     ```\nafter")).toEqual([true, false]);
  // Not closing: another mark, a shorter run, too much indent, text after it.
  for (const closer of ["~~~", "``", "     ```", "``` x", "```` `"])
    expect(codes(`\`\`\`\nx\n${closer}\nmore`), closer).toEqual([true]);
  expect(codes("````\nx\n```\nmore")).toEqual([true]);
});

test("a long code block builds no pattern per line", () => {
  const made = vi.spyOn(globalThis, "RegExp");
  try {
    const open = `\`\`\`\n${"let x = 1;\n".repeat(3000)}`;
    expect(parseBlocks(`${open}\`\`\``)).toHaveLength(1);
    expect(startsOpenFence(open)).toBe(true);
    // Not one per line (6000 calls). The tokenizer builds one per kept line.
    expect(made.mock.calls.length).toBeLessThanOrEqual(MAX_CODE_LINES + 10);
  } finally {
    made.mockRestore();
  }
});
