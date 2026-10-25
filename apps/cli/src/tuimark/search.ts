// `/search` and ctrl+f: plain-text, case-insensitive search over the messages
// the transcript holds (what the person read: their own, the assistant's and
// the tool cards'). Pure: the controller owns the box, the choice and the
// viewer; this module finds the matching lines and turns them into rows, a
// viewer's lines and an inline block.
//
// A hit is one matching line of one message. The notices and errors the
// client writes are not searched: they are its words, not the conversation.
// A tool card is searched as it is shown (its args line and the output the
// card kept), so a hit is always something the person can see.

import {
  cleanText,
  type MessageView,
  type Run,
  runsText,
} from "#cli/tuimark/blocks";
import {
  DETAILS_MAX_ROWS,
  type DetailsState,
  type DiffLine,
  type SearchRow,
  type SearchState,
  wrapLine,
} from "#cli/tuimark/store";

/** Hits kept; past this the count says how many matched in all. */
export const SEARCH_MAX_HITS = 500;
/** Rows of the box: the window of hits around the chosen one. */
export const SEARCH_ROWS = 8;
/** A query is cut to this many characters. */
export const SEARCH_QUERY_MAX = 200;
/** Lines of a message the viewer shows before the chosen hit. */
const CONTEXT_LINES = 3;
/** Characters of a matching line a row keeps, and how many of them come before the match. */
const SNIPPET_CHARS = 200;
const SNIPPET_BEFORE = 24;
/** Hits an inline block prints. */
export const SEARCH_INLINE_HITS = 30;

export interface SearchHit {
  /** The message's number: its place in the transcript, from 1. */
  n: number;
  /** "You", "Assistant" or the tool card's title. */
  who: string;
  /** The matching line's index in `lines`. */
  line: number;
  /** Every line of the message (shared by the message's hits). */
  lines: readonly string[];
}

export interface SearchResult {
  /** The query as searched: trimmed, cut to SEARCH_QUERY_MAX. */
  query: string;
  /** The first SEARCH_MAX_HITS matching lines, in transcript order. */
  hits: SearchHit[];
  /** Matching lines in all (more than `hits` when capped). */
  total: number;
  /** Messages with a match, in all. */
  messages: number;
}

/** The box's status line, and what inline says when `/search` has nothing to search for. */
export const SEARCH_STATUS =
  "Search · up/down choose · enter opens · esc closes";
export const SEARCH_INLINE_STATUS = "/search TEXT prints the matches above";

/** The query as it is searched: the plain text between its spaces. */
export function searchQuery(raw: string): string {
  return Array.from(cleanText(raw).trim()).slice(0, SEARCH_QUERY_MAX).join("");
}

/** A plain-text matcher: the query escaped, all matches (`g`) or just whether one (`test`). */
function matcher(query: string, all: boolean): RegExp {
  const source = query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(source, all ? "giu" : "iu");
}

/** What a view says, as lines, and who says it; undefined for the client's own words. */
function messageLines(
  view: MessageView,
): { who: string; lines: string[] } | undefined {
  if (view.is_user || view.is_asst) {
    const text = view.blocks.map((block) => runsText(block.runs)).join("\n");
    return { who: view.is_user ? "You" : "Assistant", lines: text.split("\n") };
  }
  if (!view.is_tool) return undefined;
  const { card } = view;
  const text = [card.args, runsText(card.out)].filter(Boolean).join("\n");
  return { who: card.title || card.name, lines: text.split("\n") };
}

/** The lines of `views` that contain `raw`, in order. An empty query matches nothing. */
export function searchViews(
  views: readonly MessageView[],
  raw: string,
): SearchResult {
  const query = searchQuery(raw);
  const result: SearchResult = { query, hits: [], total: 0, messages: 0 };
  if (query === "") return result;
  const has = matcher(query, false);
  views.forEach((view, index) => {
    const message = messageLines(view);
    if (!message) return;
    let found = false;
    message.lines.forEach((text, line) => {
      if (!has.test(text)) return;
      found = true;
      result.total++;
      if (result.hits.length < SEARCH_MAX_HITS)
        result.hits.push({
          n: index + 1,
          who: message.who,
          line,
          lines: message.lines,
        });
    });
    if (found) result.messages++;
  });
  return result;
}

/** "1 message", "2 messages", "3 matches". */
const plural = (n: number, word: string) =>
  `${n} ${n === 1 ? word : word.endsWith("ch") ? `${word}es` : `${word}s`}`;

/** The count line over the rows: where the choice is, and how much matched. */
export function searchCount(result: SearchResult, sel: number): string {
  if (result.query === "") return "Type to search this conversation";
  if (result.hits.length === 0) return "No matches";
  const messages = plural(result.messages, "message");
  const capped = result.total > result.hits.length;
  return capped
    ? `${sel + 1} of the first ${result.hits.length} · ${plural(result.total, "match")} · ${messages}`
    : `${sel + 1} of ${plural(result.total, "match")} · ${messages}`;
}

/**
 * A matching line as runs: its start cut to a few characters before the
 * first match (a row is one line wide), the matches in the `hit` class.
 */
function snippetRuns(text: string, query: string): Run[] {
  const line = text.trimStart();
  const first = matcher(query, true).exec(line)?.index ?? 0;
  let start = Math.max(0, first - SNIPPET_BEFORE);
  if (start > 0) {
    // The cut keeps its words whole when it can: it starts after a space.
    const space = line.indexOf(" ", start);
    if (space >= 0 && space < first) start = space + 1;
    // It may land inside an emoji: its low half alone is no character.
    const low = line.charCodeAt(start);
    if (low >= 0xdc00 && low <= 0xdfff) start++;
  }
  const end = Math.min(line.length, start + SNIPPET_CHARS);
  const shown = line.slice(start, end);
  const runs: Run[] = [];
  if (start > 0) runs.push("…");
  let at = 0;
  for (const match of shown.matchAll(matcher(query, true))) {
    if (match.index > at) runs.push(shown.slice(at, match.index));
    runs.push({ text: match[0], class: "hit" });
    at = match.index + match[0].length;
  }
  if (at < shown.length) runs.push(shown.slice(at));
  if (end < line.length) runs.push("…");
  return runs;
}

/** A hit as one row's runs: "12 You · the line", the match emphasised. */
export function hitRuns(hit: SearchHit, query: string): Run[] {
  return [
    `${hit.n} ${hit.who} · `,
    ...snippetRuns(hit.lines[hit.line] ?? "", query),
  ];
}

/** The window of rows around the chosen hit: `SEARCH_ROWS` at most, the choice marked. */
export function searchRows(result: SearchResult, sel: number): SearchRow[] {
  const first = Math.max(
    0,
    Math.min(
      sel - Math.floor((SEARCH_ROWS - 1) / 2),
      result.hits.length - SEARCH_ROWS,
    ),
  );
  return result.hits.slice(first, first + SEARCH_ROWS).map((hit, offset) => ({
    id: String(first + offset),
    sel: first + offset === sel,
    mark: first + offset === sel ? "›" : "",
    runs: hitRuns(hit, result.query),
  }));
}

/** The box for `result` with the choice at `sel`; `query` is what the input holds. */
export function searchState(
  result: SearchResult,
  sel: number,
  query = result.query,
): SearchState {
  return {
    open: true,
    query,
    count: searchCount(result, sel),
    rows: searchRows(result, sel),
  };
}

/**
 * The viewer over the chosen hit's message, in the details viewer's shape:
 * the message from a few lines before the hit (the viewer cannot be scrolled
 * for us), wrapped to `width`, the chosen line marked `›` and the other
 * matching lines `·`, so they read without color.
 */
export function searchViewer(
  result: SearchResult,
  sel: number,
  width: number,
): DetailsState {
  const hit = result.hits[sel];
  if (!hit) return { open: false, title: "", args: "", lines: [] };
  const has = matcher(result.query, false);
  const from = Math.max(0, hit.line - CONTEXT_LINES);
  const lines: DiffLine[] = [];
  const push = (t: string, note = false) =>
    lines.push({ n: lines.length, t, add: false, del: false, note });
  if (from > 0) push(`… ${plural(from, "earlier line")}`, true);
  let shown = from;
  for (const text of hit.lines.slice(from)) {
    if (lines.length >= DETAILS_MAX_ROWS) break;
    const mark = shown === hit.line ? "› " : has.test(text) ? "· " : "  ";
    wrapLine(text, width - 2, DETAILS_MAX_ROWS - lines.length + 1).forEach(
      (row, index) => {
        if (lines.length < DETAILS_MAX_ROWS)
          push(`${index === 0 ? mark : "  "}${row}`);
      },
    );
    shown++;
  }
  if (shown < hit.lines.length)
    push(`… ${plural(hit.lines.length - shown, "more line")}`, true);
  return {
    open: true,
    title: `Message ${hit.n} · ${hit.who}`,
    args: `match ${sel + 1} of ${result.hits.length} · n next · p previous · esc back`,
    lines,
  };
}

/**
 * The matches as paragraphs for an inline notice, printed into the
 * scrollback (inline has no box and no viewport to jump in): a heading with
 * the counts, then the first SEARCH_INLINE_HITS lines, then what was left out.
 */
export function searchBlockLines(result: SearchResult): Run[][] {
  const shown = result.hits.slice(0, SEARCH_INLINE_HITS);
  const quoted = Array.from(result.query).slice(0, 60).join("");
  const lines: Run[][] = [
    [
      `Search "${quoted}" · ${plural(result.total, "match")} in ${plural(result.messages, "message")}`,
    ],
    ...shown.map((hit) => hitRuns(hit, result.query)),
  ];
  if (result.total > shown.length)
    lines.push([`+${result.total - shown.length} more, narrow the search`]);
  return lines;
}
