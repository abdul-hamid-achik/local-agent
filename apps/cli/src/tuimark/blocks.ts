// The ONE module that maps text to what the Tuimark markup reads:
//
//   runtime Message  ->  MessageView { blocks | card }  ->  store (messages[], live)
//
// Tuimark has no markdown parser, so this module turns a message into plain
// data the templates in ui/app.tui walk with `each`:
//
//   message.blocks[] -> { para | head | quote | item | code } -> runs
//
// A block's text is a list of RUNS (Tuimark 0.4d `<text runs="b.runs"/>`): a
// plain string, or `{ text, class }` that the stylesheet styles (`span.b`,
// `span.code`, ...). Inline markdown (`**bold**`, `` `code` ``, links) becomes
// runs here; a code block is ONE text node (`wrap: pre-wrap`) whose runs are
// the syntax tokens, not a node per token.
//
// Every flag is always present (true or false) because the markup has no
// equality test and `if` / `class:NAME` on a missing member report B002 / B003.

import type { Message, ToolCall } from "@local-agent/runtime";
import { terminalText } from "#cli/text";

// ---------------------------------------------------------------- text hygiene

/** Strips ANSI and control characters, drops CR and expands tabs. */
export function cleanText(text: string): string {
  return terminalText(text).replaceAll("\t", "  ");
}

/**
 * Cuts a string to `max` characters, ending in an ellipsis. It cuts on code
 * point boundaries, so an emoji is never split into a lone surrogate.
 */
export function clip(text: string, max: number): string {
  if (text.length <= max) return text;
  const chars = Array.from(text);
  return chars.length <= max ? text : `${chars.slice(0, max - 1).join("")}…`;
}

// -------------------------------------------------------------------------- runs

/** A piece of styled text: a plain string, or text with class names (Tuimark `runs`). */
export type Run = string | { text: string; class: string };

/** The text a list of runs paints, without the styling. */
export function runsText(runs: readonly Run[]): string {
  return runs.map((r) => (typeof r === "string" ? r : r.text)).join("");
}

/** Appends a piece, merging it into the previous one when the class is the same. */
function pushRun(runs: Run[], text: string, cls = ""): void {
  if (text === "") return;
  const last = runs[runs.length - 1];
  if (last === undefined) runs.push(cls ? { text, class: cls } : text);
  else if (typeof last === "string") {
    if (cls === "") runs[runs.length - 1] = last + text;
    else runs.push({ text, class: cls });
  } else if (last.class === cls) last.text += text;
  else runs.push(cls ? { text, class: cls } : text);
}

// ------------------------------------------------------------------ inline marks

const B = 1;
const I = 2;
const STRIKE = 4;
const LINK = 8;
const CODE = 16;
const URL = 32;
/** The classes of the style bits, in the order they are written. */
const CLASS_NAMES: readonly (readonly [number, string])[] = [
  [B, "b"],
  [I, "i"],
  [STRIKE, "strike"],
  [LINK, "link"],
  [CODE, "code"],
  [URL, "url"],
];
/** The bits a delimiter or a link can lay over a span of nodes. */
const SPAN_BITS = [B, I, STRIKE, LINK] as const;

/** A link destination longer than this is not one: it bounds the work per `](`. */
const MAX_URL = 2048;

const ESCAPABLE = "\\`*_~[]()#>-+.!|";
const SPECIAL = /[\\`*_~[\]]/g;
const PUNCT = /^[\p{P}\p{S}]$/u;
const SPACE = /^\s$/u;
const CJK =
  /^[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]$/u;

interface Node {
  s: string;
  /** Style bits the node has by itself (code, url). */
  m: number;
}

interface Frame {
  /** `*`, `**`, `_`, `__`, `~~` or `[`. */
  kind: string;
  /** The node holding the literal marker; it is blanked when the frame closes. */
  node: number;
}

function charBefore(text: string, i: number): string {
  if (i <= 0) return "";
  const lo = text.charCodeAt(i - 1);
  if (lo >= 0xdc00 && lo <= 0xdfff && i >= 2) return text.slice(i - 2, i);
  return text[i - 1] ?? "";
}

function charAt(text: string, i: number): string {
  const cp = i < text.length ? text.codePointAt(i) : undefined;
  return cp === undefined ? "" : String.fromCodePoint(cp);
}

/**
 * Inline markdown to runs: `**bold**`/`__bold__` (class b), `*em*`/`_em_` (i),
 * `` `code` `` (code), `~~strike~~` (strike) and `[text](url)` (the text in
 * class link, then ` (url)` in class url). A marker that does not close, or
 * that CommonMark would not let open (`2 * 3 * 4`, `snake_case_name`), stays
 * literal text, so a message that is still streaming and ends mid-marker reads
 * fine. No HTML, no images, no reference links.
 *
 * One pass with a stack of open markers: each marker is pushed and popped at
 * most once, a closer looks only for kinds that are open (O(1) when none is),
 * code spans pair backtick runs through a table built in one sweep, and a link
 * destination is read for at most MAX_URL characters. The work is linear in
 * the text (times that constant per `](`).
 */
export function inlineRuns(text: string): Run[] {
  const nodes: Node[] = [];
  const stack: Frame[] = [];
  const open: Record<string, number> = {};
  const spans: { from: number; to: number; bit: number }[] = [];

  // Backtick runs, each with the next run of the same length (a code span's closer).
  const ticks: { pos: number; len: number; next: number }[] = [];
  if (text.includes("`")) {
    for (let i = 0; i < text.length; ) {
      if (text[i] === "\\" && ESCAPABLE.includes(text[i + 1] ?? "\n")) i += 2;
      else if (text[i] === "`") {
        let j = i;
        while (text[j] === "`") j++;
        ticks.push({ pos: i, len: j - i, next: -1 });
        i = j;
      } else i++;
    }
    const last = new Map<number, number>();
    for (let k = ticks.length - 1; k >= 0; k--) {
      const t = ticks[k];
      if (!t) continue;
      t.next = last.get(t.len) ?? -1;
      last.set(t.len, k);
    }
  }
  let tp = 0;

  const add = (s: string, m = 0) => {
    if (s !== "") nodes.push({ s, m });
  };
  const push = (kind: string) => {
    stack.push({ kind, node: nodes.length });
    open[kind] = (open[kind] ?? 0) + 1;
    add(kind);
  };
  /** Pops down to the nearest frame `accept` takes; the frames above it stay literal. */
  const take = (accept: (kind: string) => boolean): Frame | undefined => {
    for (let f = stack.pop(); f; f = stack.pop()) {
      open[f.kind] = (open[f.kind] ?? 1) - 1;
      if (accept(f.kind)) return f;
    }
    return undefined;
  };
  const close = (f: Frame, bit: number) => {
    const node = nodes[f.node];
    if (node) node.s = "";
    spans.push({ from: f.node + 1, to: nodes.length, bit });
  };

  let i = 0;
  while (i < text.length) {
    const c = text[i] ?? "";
    if (c === "\\" && ESCAPABLE.includes(text[i + 1] ?? "\n")) {
      add(text[i + 1] ?? "");
      i += 2;
    } else if (c === "`") {
      while (tp < ticks.length && (ticks[tp]?.pos ?? 0) < i) tp++;
      const run = ticks[tp];
      let n = 1;
      while (text[i + n] === "`") n++;
      const closer =
        run && run.pos === i && run.next >= 0 ? ticks[run.next] : undefined;
      if (closer) {
        let code = text.slice(i + n, closer.pos).replaceAll("\n", " ");
        if (
          code.length > 2 &&
          code.startsWith(" ") &&
          code.endsWith(" ") &&
          code.trim() !== ""
        )
          code = code.slice(1, -1);
        add(code, CODE);
        i = closer.pos + n;
      } else {
        add("`".repeat(n));
        i += n;
      }
    } else if (c === "*" || c === "_" || c === "~") {
      let n = 1;
      while (text[i + n] === c) n++;
      if (c === "~" && n !== 2) {
        add(c.repeat(n));
        i += n;
        continue;
      }
      const before = charBefore(text, i);
      const after = charAt(text, i + n);
      const beforeSpace = before === "" || SPACE.test(before);
      const afterSpace = after === "" || SPACE.test(after);
      const beforePunct = PUNCT.test(before);
      const afterPunct = PUNCT.test(after);
      // CommonMark's flanking rules, with CJK text counting as a boundary next
      // to punctuation (`这是**「重要」**的` has no spaces to flank on).
      const left =
        !afterSpace &&
        (!afterPunct || beforeSpace || beforePunct || CJK.test(before));
      const right =
        !beforeSpace &&
        (!beforePunct || afterSpace || afterPunct || CJK.test(after));
      const canOpen = c === "_" ? left && (!right || beforePunct) : left;
      const canClose = c === "_" ? right && (!left || afterPunct) : right;
      let rest = n;
      if (canClose) {
        const one = c === "~" ? "" : c;
        const two = c + c;
        while (rest > 0) {
          const strong = rest >= 2 && (open[two] ?? 0) > 0;
          const em = one !== "" && (open[one] ?? 0) > 0;
          if (!strong && !em) break;
          const f = take((k) => (strong && k === two) || (em && k === one));
          if (!f) break;
          close(f, f.kind === "~~" ? STRIKE : f.kind.length === 2 ? B : I);
          rest -= f.kind.length;
        }
      }
      if (rest > 0 && canOpen) {
        if (c === "~") push("~~");
        else {
          for (let k = Math.floor(rest / 2); k > 0; k--) push(c + c);
          if (rest % 2 === 1) push(c);
        }
      } else if (rest > 0) add(c.repeat(rest));
      i += n;
    } else if (c === "[") {
      push("[");
      i++;
    } else if (c === "]") {
      i++;
      if ((open["["] ?? 0) === 0) {
        add("]");
        continue;
      }
      const f = take((k) => k === "[");
      let end = -1;
      if (f && text[i] === "(") {
        let depth = 1;
        let j = i + 1;
        for (; j < text.length && j - i <= MAX_URL; j++) {
          const u = text[j] ?? "";
          if (u === "(") depth++;
          else if (u === ")" && --depth === 0) break;
          else if (u === " " || u === "\n") break;
        }
        if (depth === 0 && text[j] === ")") end = j;
      }
      if (f && end >= 0) {
        close(f, LINK);
        const url = text.slice(i + 1, end);
        if (url !== "") add(` (${url})`, URL);
        i = end + 1;
      } else add("]");
    } else {
      SPECIAL.lastIndex = i + 1;
      const next = SPECIAL.exec(text);
      const stop = next ? next.index : text.length;
      add(text.slice(i, stop));
      i = stop;
    }
  }

  // Sweep the spans into one mask per node, then merge neighbours that share one.
  const diff = SPAN_BITS.map(() => new Int32Array(nodes.length + 1));
  for (const sp of spans) {
    const d = diff[SPAN_BITS.indexOf(sp.bit as (typeof SPAN_BITS)[number])];
    if (!d) continue;
    d[sp.from] = (d[sp.from] ?? 0) + 1;
    d[sp.to] = (d[sp.to] ?? 0) - 1;
  }
  const depth = SPAN_BITS.map(() => 0);
  const runs: Run[] = [];
  nodes.forEach((node, k) => {
    let mask = node.m;
    SPAN_BITS.forEach((bit, b) => {
      depth[b] = (depth[b] ?? 0) + (diff[b]?.[k] ?? 0);
      if ((depth[b] ?? 0) > 0) mask |= bit;
    });
    const cls = CLASS_NAMES.filter(([bit]) => mask & bit)
      .map(([, name]) => name)
      .join(" ");
    pushRun(runs, node.s, cls);
  });
  return runs;
}

// ------------------------------------------------------------------- code blocks

/** A piece of a code line and its class (`kw`, `str`, `num`, `com`, or `` for plain). */
export interface Token {
  t: string;
  cls: string;
}

export interface Block {
  id: string;
  para: boolean;
  head: boolean;
  quote: boolean;
  /** A bullet or numbered list item: `marker`, then `runs`. */
  item: boolean;
  code: boolean;
  /** A list item below the first level (indented). */
  nest: boolean;
  /** `•` or `1.` for an item, empty for every other block. */
  marker: string;
  /** The label of a code block (`ts`, `table`), empty for every other block. */
  lang: string;
  runs: Run[];
}

/** A code block keeps this many lines; the rest is one "N more lines" row. */
export const MAX_CODE_LINES = 300;
/** A paragraph keeps this many characters. */
export const MAX_PARA_CHARS = 20_000;
/** A code line keeps this many characters. */
export const MAX_CODE_COLS = 1000;
/** One message view keeps this many characters of source and this many blocks. */
export const MAX_MESSAGE_CHARS = 256 * 1024;
export const MAX_MESSAGE_BLOCKS = 400;

const KEYWORDS = new Set([
  "const",
  "let",
  "var",
  "function",
  "return",
  "if",
  "else",
  "for",
  "while",
  "await",
  "async",
  "import",
  "from",
  "export",
  "type",
  "interface",
  "class",
  "new",
  "throw",
  "try",
  "catch",
  "of",
  "in",
  "def",
  "yield",
  "true",
  "false",
  "null",
]);

const TOKEN =
  /(\/\/.*$|#.*$)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`)|(\b\d[\d_.]*\b)|([A-Za-z_$][\w$]*)|(\s+)|([\s\S])/gy;

/** Splits one source line into classified tokens. Whitespace stays in the tokens. */
export function tokenizeLine(line: string): Token[] {
  const out: Token[] = [];
  const push = (t: string, cls: string) => {
    const last = out[out.length - 1];
    // Merge neighbours of the same plain kind so plain code is not one token per character.
    if (last && cls === "" && last.cls === "") {
      last.t += t;
      return;
    }
    out.push({ t, cls });
  };
  const re = new RegExp(TOKEN.source, "y");
  for (let m = re.exec(line); m; m = re.exec(line)) {
    if (m[1]?.startsWith("#") && line.slice(0, m.index).trim()) {
      // `#` is a comment only at the start of a line (shell, python, yaml); in
      // `#private`, `#fff` or a URL fragment it is just a character.
      push("#", "");
      re.lastIndex = m.index + 1;
    } else if (m[1] !== undefined) push(m[1], "com");
    else if (m[2] !== undefined) push(m[2], "str");
    else if (m[3] !== undefined) push(m[3], "num");
    else if (m[4] !== undefined) push(m[4], KEYWORDS.has(m[4]) ? "kw" : "");
    else push(m[0], "");
  }
  return out;
}

/**
 * The runs of a block of lines: at most MAX_CODE_LINES lines of at most
 * MAX_CODE_COLS characters, `\n` between them (the template paints them with
 * `wrap: pre-wrap`), and a final dim "N more lines" row when lines were dropped.
 */
function linesToRuns(
  source: string[],
  tokens: (line: string, n: number) => Token[],
): Run[] {
  const runs: Run[] = [];
  const kept = source.slice(0, MAX_CODE_LINES);
  kept.forEach((line, n) => {
    if (n > 0) pushRun(runs, "\n");
    for (const tok of tokens(clip(line, MAX_CODE_COLS), n))
      pushRun(runs, tok.t, tok.cls);
  });
  if (source.length > kept.length)
    pushRun(
      runs,
      `${kept.length > 0 ? "\n" : ""}… ${source.length - kept.length} more lines`,
      "com",
    );
  return runs;
}

function block(id: string, kind: Partial<Block>, runs: Run[]): Block {
  return {
    id,
    para: false,
    head: false,
    quote: false,
    item: false,
    code: false,
    nest: false,
    marker: "",
    lang: "",
    runs,
    ...kind,
  };
}

function codeBlock(id: string, lang: string, source: string[]): Block {
  return block(
    id,
    { code: true, lang },
    linesToRuns(source, (line) => tokenizeLine(line)),
  );
}

function textBlock(
  id: string,
  kind: "para" | "head" | "quote",
  t: string,
): Block {
  return block(id, { [kind]: true }, inlineRuns(clip(t, MAX_PARA_CHARS)));
}

// ------------------------------------------------------------------------ tables

const TABLE_ROW = /^\s*\|.*\|\s*$/;
const DIVIDER_CELL = /^:?-+:?$/;

function tableCells(row: string): string[] {
  const inner = row.trim().replace(/^\|/, "").replace(/\|$/, "");
  return inner.split(/(?<!\\)\|/).map((c) => c.trim());
}

/**
 * A pipe table as monospace rows: each column as wide as its widest cell (by
 * display width), a divider rebuilt to that width keeping its `:` marks, the
 * header row in class `b`. Cell text stays literal (no inline marks).
 */
export function tableRuns(rows: string[]): Run[] {
  const parsed = rows.map(tableCells);
  const divider = parsed[1] ?? [];
  const header =
    divider.length > 0 && divider.every((c) => DIVIDER_CELL.test(c));
  const width = Array.from(
    { length: Math.max(...parsed.map((r) => r.length)) },
    (_, k) =>
      Math.max(
        3,
        ...parsed.map((r, n) =>
          header && n === 1 ? 3 : Bun.stringWidth(r[k] ?? ""),
        ),
      ),
  );
  const mark = (k: number) => divider[k] ?? "";
  const right = (k: number) =>
    header && mark(k).endsWith(":") && !mark(k).startsWith(":");
  const lines = parsed.map((row, n) => {
    const out = width.map((w, k) => {
      if (header && n === 1) {
        const m = mark(k);
        const left = m.startsWith(":") ? ":" : "-";
        const end = m.endsWith(":") ? ":" : "-";
        return `${left}${"-".repeat(w - 2)}${end}`;
      }
      const cell = row[k] ?? "";
      const pad = " ".repeat(Math.max(0, w - Bun.stringWidth(cell)));
      return right(k) ? pad + cell : cell + pad;
    });
    return `| ${out.join(" | ")} |`;
  });
  return linesToRuns(lines, (t, n) => [
    { t, cls: header && n === 0 ? "b" : "" },
  ]);
}

// --------------------------------------------------------------------- markdown

/** An opening fence: three or more backticks or tildes, then an info string. */
const FENCE_OPEN = /^\s{0,3}(`{3,}|~{3,})\s*([^`]*)$/;
/** A bullet (`-`, `*`, `+`) or a numbered (`1.`, `1)`) list item. */
const LIST_ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;

function closesFence(line: string, open: string): boolean {
  const mark = open[0] ?? "`";
  return new RegExp(`^\\s{0,3}\\${mark}{${open.length},}\\s*$`).test(line);
}

/**
 * True when `text` starts with a fence that is not closed yet: the code block
 * a stream is still writing, which grows by whole lines.
 */
export function startsOpenFence(text: string): boolean {
  const lines = cleanText(text).split("\n");
  const fence = FENCE_OPEN.exec(lines[0] ?? "");
  if (!fence) return false;
  const open = fence[1] ?? "```";
  for (let i = 1; i < lines.length; i++)
    if (closesFence(lines[i] ?? "", open)) return false;
  return true;
}

/**
 * A small markdown subset: paragraphs, `#` headings, bullet and numbered
 * items (one nesting level), `>` quotes, pipe tables (aligned, header bold)
 * and fenced code, with inline marks as runs (see `inlineRuns`). A fence that
 * never closes runs to the end, so a half-streamed code block already shows as
 * code.
 *
 * The view is bounded: past MAX_MESSAGE_CHARS of source or MAX_MESSAGE_BLOCKS
 * blocks the rest is one row that says so. The model's history is not cut.
 */
export function parseBlocks(markdown: string, prefix = "b"): Block[] {
  return parseBlocksAt(markdown, prefix).blocks;
}

/** The blocks of a text, and the line each block starts at (for the streaming split). */
export interface Parsed {
  blocks: Block[];
  /** `starts[i]` is the index of the first line of `blocks[i]` in the cleaned text. */
  starts: number[];
}

/**
 * `parseBlocks` that also says where each block starts, and numbers the ids
 * from `first`, so a stream can parse its text in pieces: the blocks before the
 * last one are final, and the text from the last one's first line on is parsed
 * again when more arrives.
 */
export function parseBlocksAt(
  markdown: string,
  prefix = "b",
  first = 0,
): Parsed {
  const blocks: Block[] = [];
  const starts: number[] = [];
  const cut = markdown.length > MAX_MESSAGE_CHARS;
  const lines = cleanText(
    cut ? markdown.slice(0, MAX_MESSAGE_CHARS) : markdown,
  ).split("\n");
  let para: string[] = [];
  let paraAt = 0;
  let quote: string[] = [];
  let quoteAt = 0;
  let item:
    | { marker: string; nest: boolean; lines: string[]; at: number }
    | undefined;
  const next = () => `${prefix}${first + blocks.length}`;
  const flush = () => {
    if (para.length > 0) {
      starts.push(paraAt);
      blocks.push(textBlock(next(), "para", para.join("\n")));
      para = [];
    }
    if (quote.length > 0) {
      starts.push(quoteAt);
      blocks.push(textBlock(next(), "quote", quote.join("\n")));
      quote = [];
    }
    if (item) {
      starts.push(item.at);
      blocks.push(
        block(
          next(),
          { item: true, nest: item.nest, marker: item.marker },
          inlineRuns(clip(item.lines.join("\n"), MAX_PARA_CHARS)),
        ),
      );
      item = undefined;
    }
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const fence = FENCE_OPEN.exec(line);
    const listItem = LIST_ITEM.exec(line);
    if (fence) {
      flush();
      const at = i;
      const open = fence[1] ?? "```";
      const body: string[] = [];
      for (i++; i < lines.length && !closesFence(lines[i] ?? "", open); i++) {
        body.push(lines[i] ?? "");
      }
      const lang = clip((fence[2] ?? "").trim().split(/\s+/)[0] ?? "", 24);
      starts.push(at);
      blocks.push(codeBlock(next(), lang, body));
    } else if (TABLE_ROW.test(line)) {
      flush();
      const at = i;
      const table: string[] = [line.trim()];
      while (TABLE_ROW.test(lines[i + 1] ?? "")) {
        i++;
        table.push((lines[i] ?? "").trim());
      }
      starts.push(at);
      blocks.push(
        block(next(), { code: true, lang: "table" }, tableRuns(table)),
      );
    } else if (/^#{1,6}\s/.test(line)) {
      flush();
      starts.push(i);
      blocks.push(
        textBlock(
          next(),
          "head",
          line.replace(/^#+\s*/, "").replace(/\s+#+\s*$/, ""),
        ),
      );
    } else if (/^\s*>\s?/.test(line)) {
      if (para.length > 0 || item) flush();
      if (quote.length === 0) quoteAt = i;
      quote.push(line.replace(/^\s*>\s?/, ""));
    } else if (listItem) {
      flush();
      const mark = listItem[2] ?? "-";
      item = {
        marker: /^\d/.test(mark) ? `${mark.slice(0, -1)}.` : "•",
        nest: (listItem[1] ?? "").length >= 2,
        lines: [listItem[3] ?? ""],
        at: i,
      };
    } else if (line.trim() === "") {
      flush();
    } else if (item) {
      // A wrapped line of the item above (indented or lazy).
      item.lines.push(line.trim());
    } else {
      if (quote.length > 0) flush();
      if (para.length === 0) paraAt = i;
      para.push(line);
    }
  }
  flush();
  const dropped = blocks.length - MAX_MESSAGE_BLOCKS;
  if (dropped > 0) {
    blocks.length = MAX_MESSAGE_BLOCKS;
    starts.length = MAX_MESSAGE_BLOCKS;
  }
  if (cut || dropped > 0) {
    starts.push(lines.length);
    blocks.push(
      textBlock(
        `${prefix}${first + blocks.length}`,
        "quote",
        cut
          ? `… message truncated in this view (${markdown.length - MAX_MESSAGE_CHARS} more characters)`
          : `… ${dropped} more blocks not shown in this view`,
      ),
    );
  }
  return { blocks, starts };
}

/** Parses an in-flight reply as one piece (the same mapping, with a prefix of its own). */
export function parseStreaming(markdown: string, prefix = "l"): Block[] {
  return parseBlocks(markdown, prefix);
}

// ------------------------------------------------------------------- tool cards

export interface Card {
  name: string;
  status: string;
  args: string;
  ok: boolean;
  bad: boolean;
  warn: boolean;
  running: boolean;
  /** The output as runs for one `wrap: pre-wrap` text. */
  out: Run[];
}

export const NO_CARD: Card = Object.freeze({
  name: "",
  status: "",
  args: "",
  ok: false,
  bad: false,
  warn: false,
  running: false,
  out: [],
}) as Card;

export type ToolState =
  | "requested"
  | "approval"
  | "running"
  | "succeeded"
  | "denied"
  | "failed"
  | "outcome_unknown"
  | "unverified";

const STATE_LABEL: Record<ToolState, string> = {
  requested: "requested",
  approval: "waiting for approval",
  running: "running",
  succeeded: "succeeded",
  denied: "denied",
  failed: "failed",
  outcome_unknown: "outcome unknown, inspect the workspace",
  unverified: "unverified",
};

const OUT_LINES = 8;
const OUT_COLS = 200;

/** One line that says what a tool call is about: a path, a command, or the arguments. */
export function callSummary(call: ToolCall | undefined): string {
  if (!call) return "";
  const args = call.function.arguments;
  if (typeof args.path === "string") return clip(cleanText(args.path), 160);
  if (Array.isArray(args.argv))
    return clip(cleanText(args.argv.map(String).join(" ")), 160);
  return clip(cleanText(JSON.stringify(args)), 160);
}

/**
 * The first lines of a tool's output as runs, with a final "N more lines" row.
 * The optional head line (exit code) is the first line, in class `err` or `note`.
 */
export function outputRuns(
  output: string,
  head?: { text: string; bad: boolean },
): Run[] {
  const all = cleanText(output).split("\n");
  while (all.length > 0 && all[all.length - 1] === "") all.pop();
  if (head) all.unshift(head.text);
  const runs: Run[] = [];
  all.slice(0, OUT_LINES).forEach((line, n) => {
    if (n > 0) pushRun(runs, "\n");
    const cls = head && n === 0 ? (head.bad ? "err" : "note") : "";
    pushRun(runs, clip(line, OUT_COLS), cls);
  });
  if (all.length > OUT_LINES)
    pushRun(runs, `\n… ${all.length - OUT_LINES} more lines`, "note");
  return runs;
}

/** What to show under a finished tool card: an optional head line, then the output. */
function resultParts(
  name: string,
  output: string,
): { head?: { text: string; bad: boolean }; body: string } {
  if (name === "run_command") {
    try {
      const r = JSON.parse(output) as {
        output?: unknown;
        exitCode?: unknown;
        timedOut?: unknown;
        truncated?: unknown;
      };
      if (typeof r.output === "string") {
        const code =
          r.exitCode === null ? "no exit code" : `exit ${r.exitCode}`;
        const flags = [
          r.timedOut === true ? "timed out" : "",
          r.truncated === true ? "truncated" : "",
        ].filter(Boolean);
        return {
          head: {
            text: [code, ...flags].join(", "),
            bad: r.exitCode !== 0 || r.timedOut === true,
          },
          body: r.output,
        };
      }
    } catch {
      // Not JSON: show it as it is.
    }
  }
  return { body: output };
}

/** What to show under a finished tool card, from the `{status, output}` the runtime stores. */
export function resultSummary(name: string, output: string): string {
  const { head, body } = resultParts(name, output);
  return head ? [head.text, body].join("\n") : body;
}

function toolCard(
  name: string,
  args: string,
  state: ToolState,
  output = "",
): Card {
  const parts = resultParts(name, output);
  return {
    name: clip(cleanText(name), 80),
    status: STATE_LABEL[state],
    args,
    ok: state === "succeeded",
    bad: state === "denied" || state === "failed",
    warn: state === "outcome_unknown" || state === "unverified",
    running:
      state === "running" || state === "requested" || state === "approval",
    out: output ? outputRuns(parts.body, parts.head) : [],
  };
}

// ------------------------------------------------------------------------ views

export interface MessageView {
  id: string;
  is_user: boolean;
  is_asst: boolean;
  is_tool: boolean;
  blocks: Block[];
  card: Card;
}

/**
 * The in-flight message. `blocks` are the blocks that are final (a stream only
 * ever appends to them), `tail` the one or two still being written, which the
 * stream replaces. A tool call has neither: its `card` is the live view.
 */
export type LiveView = MessageView & { active: boolean; tail: Block[] };

function view(
  id: string,
  role: "user" | "asst" | "tool",
  blocks: Block[],
  card: Card = NO_CARD,
): MessageView {
  return {
    id,
    is_user: role === "user",
    is_asst: role === "asst",
    is_tool: role === "tool",
    blocks,
    card,
  };
}

/** The empty in-flight slot: the `live` store value when nothing is streaming. */
export const IDLE_LIVE: LiveView = Object.freeze({
  ...view("live", "asst", []),
  active: false,
  tail: [],
}) as LiveView;

/**
 * The "Thinking…" block under the log while the model reasons before it answers.
 * `runs` are plain strings the stream appends to; the markup shows the last
 * rows of them in a small scroll that sticks to the bottom.
 */
export interface ThinkingView {
  on: boolean;
  runs: Run[];
}

export const IDLE_THINKING: ThinkingView = Object.freeze({
  on: false,
  runs: [],
}) as ThinkingView;

/** The rows of the reasoning the block shows (the scroll's max-height). */
export const THINK_LINES = 3;
/** The store keeps about this many characters of reasoning; the rest is cut at a line. */
export const THINK_KEEP = 1600;
/** What a cut keeps. */
export const THINK_TRIM = 480;

/**
 * The block for a piece of reasoning: its last characters as one run, cut at a
 * line start. It lives only in the live view; the reasoning is never part of a
 * message the transcript commits.
 */
export function thinkingView(text: string): ThinkingView {
  const clean = cleanText(text);
  return { on: true, runs: clean ? [thinkingTail(clean)] : [] };
}

/** The last THINK_TRIM characters of `text`, starting at a line when there is one. */
export function thinkingTail(text: string): string {
  if (text.length <= THINK_TRIM) return text;
  const tail = text.slice(-THINK_TRIM);
  const nl = tail.indexOf("\n");
  return nl >= 0 && nl < tail.length - 1 ? tail.slice(nl + 1) : tail;
}

/** An in-flight tool call: requested, waiting for approval, or running. */
export function liveToolView(
  call: ToolCall | undefined,
  name: string,
  state: "requested" | "approval" | "running",
): LiveView {
  return {
    ...view("live", "tool", [], toolCard(name, callSummary(call), state)),
    active: true,
    tail: [],
  };
}

/**
 * Turns runtime messages into views, one message at a time. The same builder
 * seeds a resumed session and follows a live turn, so a resumed transcript
 * and a streamed one cannot differ. It remembers the tool calls of the last
 * assistant message: a tool message names its call by id, and the live
 * `tool_requested` events arrive in call order.
 */
export class ViewBuilder {
  private nextId = 0;
  private readonly calls = new Map<string, ToolCall>();
  private pending: ToolCall[] = [];

  /** The call a `tool_requested` event belongs to (they arrive in call order). */
  takeCall(): ToolCall | undefined {
    return this.pending.shift();
  }

  /** The view of a message, or undefined when it shows nothing (system, empty assistant). */
  fromMessage(message: Message): MessageView | undefined {
    if (message.role === "system") return undefined;
    if (message.role === "assistant") {
      this.pending = (message.tool_calls ?? []).slice();
      for (const call of this.pending)
        if (call.id) this.calls.set(call.id, call);
      if (!message.content.trim()) return undefined;
      return view(this.id(), "asst", parseBlocks(message.content));
    }
    if (message.role === "user")
      return view(this.id(), "user", parseBlocks(message.content));
    const name = message.tool_name ?? "result";
    const call = message.tool_call_id
      ? this.calls.get(message.tool_call_id)
      : undefined;
    let status = "unknown";
    let output = message.content;
    try {
      const result = JSON.parse(message.content) as {
        status?: unknown;
        output?: unknown;
      };
      if (typeof result.output === "string") {
        output = result.output;
        if (typeof result.status === "string") status = result.status;
      }
    } catch {
      // Not the runtime's {status, output} receipt: show the content.
    }
    return view(
      this.id(),
      "tool",
      [],
      toolCard(name, callSummary(call), asToolState(status), output),
    );
  }

  /** A view for text the client itself wrote (never sent to the model). */
  user(text: string): MessageView {
    return view(this.id(), "user", parseBlocks(text));
  }

  private id(): string {
    return `m${this.nextId++}`;
  }
}

function asToolState(status: string): ToolState {
  switch (status) {
    case "succeeded":
    case "denied":
    case "failed":
    case "outcome_unknown":
    case "unverified":
      return status;
    default:
      return "unverified";
  }
}
