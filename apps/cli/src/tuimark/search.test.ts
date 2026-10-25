import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import { type MessageView, runsText, ViewBuilder } from "#cli/tuimark/blocks";
import {
  hitRuns,
  SEARCH_MAX_HITS,
  SEARCH_ROWS,
  searchBlockLines,
  searchCount,
  searchQuery,
  searchRows,
  searchState,
  searchViewer,
  searchViews,
} from "#cli/tuimark/search";

/** The views of a conversation, as the transcript holds them. */
function views(messages: Message[]): MessageView[] {
  const builder = new ViewBuilder();
  return messages.flatMap((message) => builder.fromMessage(message) ?? []);
}

const user = (content: string): Message => ({ role: "user", content });
const assistant = (content: string): Message => ({
  role: "assistant",
  content,
});

test("a search is plain text and case-insensitive, and finds a line per hit", () => {
  const found = searchViews(
    views([
      user("Fix the RETRY loop"),
      assistant("Done.\n\nThe retry loop now backs off.\nNo other change."),
    ]),
    "retry",
  );
  expect(found.total).toBe(2);
  expect(found.messages).toBe(2);
  expect(found.hits.map((hit) => [hit.n, hit.who, hit.line])).toEqual([
    [1, "You", 0],
    [2, "Assistant", 1],
  ]);
});

test("regex characters in the query are only characters", () => {
  const conversation = views([user("a.c abc a+c (x) [y] \\d")]);
  expect(searchViews(conversation, "a.c").total).toBe(1);
  expect(searchViews(conversation, "a+c").total).toBe(1);
  expect(searchViews(conversation, "(x)").total).toBe(1);
  expect(searchViews(conversation, "[y]").total).toBe(1);
  expect(searchViews(conversation, "\\d").total).toBe(1);
  // `a.c` as a pattern would match "abc" twice over: here it is one line, once.
  expect(
    hitRuns(searchViews(conversation, "a.c").hits[0] as never, "a.c").filter(
      (run) => typeof run !== "string" && run.class === "hit",
    ),
  ).toEqual([{ text: "a.c", class: "hit" }]);
});

test("an empty or blank query finds nothing and says what to do", () => {
  const conversation = views([user("hello")]);
  for (const query of ["", "   "]) {
    const found = searchViews(conversation, query);
    expect(found.hits).toEqual([]);
    expect(searchCount(found, 0)).toBe("Type to search this conversation");
  }
  expect(searchCount(searchViews(conversation, "zzz"), 0)).toBe("No matches");
});

test("the query is trimmed, cleaned and cut", () => {
  expect(searchQuery("  retry \n")).toBe("retry");
  expect(searchQuery("a\u001b[31mb")).not.toContain("\u001b");
  expect(Array.from(searchQuery("x".repeat(500)))).toHaveLength(200);
});

test("tool cards are searched by their args and the output they show, notices and errors are not", () => {
  const builder = new ViewBuilder();
  const call = {
    id: "c1",
    function: { name: "read", arguments: { path: "src/needle.ts" } },
  };
  builder.fromMessage({ role: "assistant", content: "", tool_calls: [call] });
  const all = [
    builder.fromMessage(user("look")) as MessageView,
    builder.fromMessage({
      role: "tool",
      tool_name: "read",
      tool_call_id: "c1",
      content: JSON.stringify({ status: "succeeded", output: "one\nneedle" }),
    }) as MessageView,
    builder.notice("a needle in the notice"),
    builder.error("a needle in the error"),
  ];
  const found = searchViews(all, "needle");
  // The args line (the path) and the output line; message 2 is the card.
  expect(found.hits.map((hit) => [hit.n, hit.who, hit.line])).toEqual([
    [2, "Read", 0],
    [2, "Read", 2],
  ]);
  expect(found.messages).toBe(1);
});

test("a hit row is its number, who said it and the line, the match emphasised as typed", () => {
  const found = searchViews(
    views([assistant("Use the Retry helper")]),
    "retry",
  );
  const runs = hitRuns(found.hits[0] as never, found.query);
  expect(runsText(runs)).toBe("1 Assistant · Use the Retry helper");
  expect(runs).toContainEqual({ text: "Retry", class: "hit" });
});

test("a long line is cut around its first match, at a word, with ellipses", () => {
  const line = `${"lorem ipsum ".repeat(30)}the needle is here ${"dolor sit ".repeat(40)}`;
  const found = searchViews(views([user(line)]), "needle");
  const text = runsText(hitRuns(found.hits[0] as never, found.query));
  expect(text.startsWith("1 You · …")).toBe(true);
  expect(text.endsWith("…")).toBe(true);
  expect(text).toContain("needle is here");
  // It starts on a whole word, not in the middle of one.
  expect(text.slice("1 You · …".length)).toMatch(/^(lorem|ipsum|the)/);
  expect(Array.from(text).length).toBeLessThan(240);
});

test("the hits are capped, and the count says how many matched in all", () => {
  const many = views(
    Array.from({ length: SEARCH_MAX_HITS + 40 }, (_, i) =>
      user(`row ${i} hit`),
    ),
  );
  const found = searchViews(many, "hit");
  expect(found.hits).toHaveLength(SEARCH_MAX_HITS);
  expect(found.total).toBe(SEARCH_MAX_HITS + 40);
  expect(found.messages).toBe(SEARCH_MAX_HITS + 40);
  expect(searchCount(found, 0)).toBe(
    `1 of the first ${SEARCH_MAX_HITS} · ${SEARCH_MAX_HITS + 40} matches · ${SEARCH_MAX_HITS + 40} messages`,
  );
});

test("the count line says where the choice is and counts singulars right", () => {
  const found = searchViews(views([user("one hit")]), "hit");
  expect(searchCount(found, 0)).toBe("1 of 1 match · 1 message");
});

test("the rows are a window around the choice, which is marked", () => {
  const found = searchViews(
    views(Array.from({ length: 30 }, (_, i) => user(`line ${i} hit`))),
    "hit",
  );
  expect(searchRows(found, 0).map((row) => row.sel)).toEqual(
    Array.from({ length: SEARCH_ROWS }, (_, i) => i === 0),
  );
  const middle = searchRows(found, 15);
  expect(middle).toHaveLength(SEARCH_ROWS);
  expect(middle.filter((row) => row.sel).map((row) => row.mark)).toEqual(["›"]);
  expect(middle.find((row) => row.sel)?.id).toBe("15");
  // The window stays full at the end.
  const last = searchRows(found, 29);
  expect(last).toHaveLength(SEARCH_ROWS);
  expect(last.at(-1)).toMatchObject({ id: "29", sel: true, mark: "›" });
});

test("the box state carries the query the input holds", () => {
  const found = searchViews(views([user("hit")]), "hit");
  expect(searchState(found, 0)).toMatchObject({ open: true, query: "hit" });
  expect(searchState(found, 0, "hit ")).toMatchObject({ query: "hit " });
});

test("the viewer starts a few lines before the match and marks the chosen line and the others", () => {
  const body = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`);
  body[7] = "line 8 target";
  body[9] = "line 10 target too";
  const found = searchViews(views([assistant(body.join("\n\n"))]), "target");
  // Blank lines separate blocks; the viewer lists one line per block.
  const second = found.hits.length === 2 ? 1 : 0;
  const viewer = searchViewer(found, second, 76);
  expect(viewer.open).toBe(true);
  expect(viewer.title).toBe("Message 1 · Assistant");
  expect(viewer.args).toContain(`match ${second + 1} of 2`);
  const rows = viewer.lines.map((line) => line.t);
  expect(viewer.lines[0]).toMatchObject({ note: true });
  expect(rows[0]).toMatch(/^… \d+ earlier lines$/);
  expect(rows.filter((row) => row.startsWith("› "))).toEqual([
    "› line 10 target too",
  ]);
  expect(rows.filter((row) => row.startsWith("· "))).toEqual([
    "· line 8 target",
  ]);
  // The lines before the first context line are not there.
  expect(rows.join("\n")).not.toContain("line 1\n");
});

test("the viewer wraps a long line and keeps the mark column", () => {
  const found = searchViews(
    views([user(`needle ${"word ".repeat(40)}`)]),
    "needle",
  );
  const rows = searchViewer(found, 0, 40).lines.map((line) => line.t);
  expect(rows.length).toBeGreaterThan(2);
  expect(rows[0]?.startsWith("› needle")).toBe(true);
  for (const row of rows.slice(1)) expect(row.startsWith("  ")).toBe(true);
  for (const row of rows) expect(Bun.stringWidth(row)).toBeLessThanOrEqual(40);
});

test("the inline block is a heading, the first matches, and what was left out", () => {
  const found = searchViews(
    views(Array.from({ length: 35 }, (_, i) => user(`row ${i} hit`))),
    "hit",
  );
  const lines = searchBlockLines(found);
  expect(runsText(lines[0] ?? [])).toBe(
    'Search "hit" · 35 matches in 35 messages',
  );
  expect(lines).toHaveLength(1 + 30 + 1);
  expect(runsText(lines[1] ?? [])).toBe("1 You · row 0 hit");
  expect(runsText(lines.at(-1) ?? [])).toBe("+5 more, narrow the search");
});
