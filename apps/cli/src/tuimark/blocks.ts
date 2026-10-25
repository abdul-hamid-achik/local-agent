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

import {
  changeLines,
  commandLine,
  type Message,
  type ToolCall,
} from "@local-agent/runtime";
import { terminalLine, terminalText } from "#cli/text";
import { goalNotice } from "#cli/tuimark/goal";

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

/**
 * One link of a message, as the sources row shows it: the number of its `[n]`
 * marker in the body, the link's text, and where it goes. Links are deduped by
 * url, so one target has one number however often it is cited.
 */
export interface LinkRef {
  n: number;
  text: string;
  url: string;
}

/** A message view keeps this many link targets; the rest stay plain link text. */
export const MAX_LINKS = 8;
/** A source's text keeps this many characters (the row is one line). */
export const MAX_LINK_TEXT = 60;

/**
 * One image of a message, ready for `<img>`: the mime type sniffed from the
 * bytes, the base64 the runtime attached, and the data URI the markup reads.
 */
export interface ImageRef {
  mime: string;
  b64: string;
  /** `data:<mime>;base64,<b64>`, the value `<img src>` paints. */
  src: string;
  /** What the image is, for terminals that cannot paint one. */
  alt: string;
}

/** A message view keeps this many images; the rest are only counted. */
export const MAX_IMAGES = 2;
/** An image over this many base64 characters is not kept in the view. */
export const MAX_IMAGE_B64 = 2 * 1024 * 1024;

/**
 * The mime type an image's base64 starts with, from its magic bytes (the
 * runtime validated them; the transcript still has to name the type for the
 * data URI). Undefined for bytes that are not a PNG, JPEG, GIF or WebP.
 */
export function imageMime(b64: string): string | undefined {
  const bytes = Buffer.from(b64.slice(0, 24), "base64");
  if (
    bytes[0] === 0x89 &&
    bytes[1] === 0x50 &&
    bytes[2] === 0x4e &&
    bytes[3] === 0x47 &&
    bytes[4] === 0x0d &&
    bytes[5] === 0x0a &&
    bytes[6] === 0x1a &&
    bytes[7] === 0x0a
  )
    return "image/png";
  if (bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff)
    return "image/jpeg";
  // GIF87a / GIF89a: "GIF8" is enough to name the type.
  const gif = "GIF8";
  if ([...gif].every((c, i) => bytes[i] === c.charCodeAt(0)))
    return "image/gif";
  const riff = "RIFF";
  const webp = "WEBP";
  if (
    [...riff].every((c, i) => bytes[i] === c.charCodeAt(0)) &&
    [...webp].every((c, i) => bytes[8 + i] === c.charCodeAt(0))
  )
    return "image/webp";
  return undefined;
}

/** The images a message carries, capped, plus the count that did not fit. */
export interface MessageImages {
  images: ImageRef[];
  /** Images over MAX_IMAGES or MAX_IMAGE_B64: the view says "+N more". */
  more: number;
}

/** Caps and labels the base64 images a runtime message attached. */
export function messageImages(
  b64s: readonly string[] | undefined,
): MessageImages {
  const images: ImageRef[] = [];
  let more = 0;
  for (const b64 of b64s ?? []) {
    if (images.length >= MAX_IMAGES || b64.length > MAX_IMAGE_B64) {
      more++;
      continue;
    }
    const mime = imageMime(b64);
    if (!mime) {
      more++;
      continue;
    }
    images.push({
      mime,
      b64,
      src: `data:${mime};base64,${b64}`,
      alt: `image ${images.length + 1} (${mime.slice("image/".length)})`,
    });
  }
  return { images, more };
}

/**
 * The number a link's source gets, registering it on first sight: 1-based,
 * deduped by url, at most MAX_LINKS. Undefined past the cap, where the link
 * stays link text without a marker.
 */
function addLink(
  links: LinkRef[],
  text: string,
  url: string,
): number | undefined {
  const known = links.find((link) => link.url === url);
  if (known) return known.n;
  if (links.length >= MAX_LINKS) return undefined;
  const n = links.length + 1;
  links.push({ n, text: clip(text, MAX_LINK_TEXT), url });
  return n;
}

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
 * class link, then its `[n]` source marker in class url). A marker that does
 * not close, or that CommonMark would not let open (`2 * 3 * 4`,
 * `snake_case_name`), stays literal text, so a message that is still streaming
 * and ends mid-marker reads fine. No HTML, no images, no reference links.
 *
 * When `links` is given, each link target is registered in it (see addLink) and
 * the marker is the target's number; without it (the live stream, where no
 * sources row exists yet) a link is just its text in class link.
 *
 * One pass with a stack of open markers: each marker is pushed and popped at
 * most once, a closer looks only for kinds that are open (O(1) when none is),
 * code spans pair backtick runs through a table built in one sweep, and a link
 * destination is read for at most MAX_URL characters. The work is linear in
 * the text (times that constant per `](`).
 */
export function inlineRuns(text: string, links?: LinkRef[]): Run[] {
  const nodes: Node[] = [];
  const stack: Frame[] = [];
  const open: Record<string, number> = {};
  const spans: { from: number; to: number; bit: number }[] = [];

  // Backtick runs, each with the next run of the same length (a code span's closer).
  // Escapes do not work inside a code span, so a backslash never hides a run as a
  // closer. An escaped run (an odd number of backslashes before it) is never an
  // opener because the scan below reads its first tick as an escape; the rest of
  // the run after that tick can open one (`opener`: never a closer).
  const ticks: {
    pos: number;
    len: number;
    next: number;
    opener?: boolean;
  }[] = [];
  if (text.includes("`")) {
    for (let i = 0, slashes = 0; i < text.length; ) {
      if (text[i] === "\\") {
        slashes++;
        i++;
      } else if (text[i] === "`") {
        let j = i;
        while (text[j] === "`") j++;
        ticks.push({ pos: i, len: j - i, next: -1 });
        if (slashes % 2 === 1 && j - i > 1)
          ticks.push({ pos: i + 1, len: j - i - 1, next: -1, opener: true });
        slashes = 0;
        i = j;
      } else {
        slashes = 0;
        i++;
      }
    }
    const last = new Map<number, number>();
    for (let k = ticks.length - 1; k >= 0; k--) {
      const t = ticks[k];
      if (!t) continue;
      t.next = last.get(t.len) ?? -1;
      if (!t.opener) last.set(t.len, k);
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
        // The sources row numbers each target once: the marker [n] goes right
        // after the link text, and the same url always gets the same n. With
        // no collector (the live stream) the link is plain text in class link.
        if (url !== "" && links) {
          let label = "";
          for (let k = f.node + 1; k < nodes.length; k++)
            label += nodes[k]?.s ?? "";
          const n = addLink(links, label, url);
          if (n !== undefined) add(`[${n}]`, URL);
        }
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
  /** A pipe table (a code block with no label): it is set apart from the block above. */
  table: boolean;
  /** A list item below the first level (indented). */
  nest: boolean;
  /** `•` or `1.` for an item, empty for every other block. */
  marker: string;
  /** The label of a code block (`ts`), empty for every other block. */
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
    table: false,
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
  links?: LinkRef[],
): Block {
  return block(
    id,
    { [kind]: true },
    inlineRuns(clip(t, MAX_PARA_CHARS), links),
  );
}

// ------------------------------------------------------------------------ tables

const TABLE_ROW = /^\s*\|.*\|\s*$/;
const DIVIDER_CELL = /^:?-+:?$/;

function tableCells(row: string): string[] {
  const inner = row.trim().replace(/^\|/, "").replace(/\|$/, "");
  return inner.split(/(?<!\\)\|/).map((c) => c.trim());
}

/**
 * The columns a table's text can use in the transcript: the terminal less what
 * the log and the code block take around it. Fullscreen: the log's border and
 * padding (4) and the code block's padding (2). Inline has no log, so only the
 * code block's. Never below 2, the width of one wide character: a narrow
 * table is listed as "Header: value" lines that wrap to the width.
 */
export function tableWidth(columns: number, inline: boolean): number {
  return Math.max(2, columns - (inline ? 2 : 6));
}

/** One grapheme of a cell: what it paints, its class and its display width. */
interface Atom {
  g: string;
  cls: string;
  w: number;
}

const GRAPHEMES = new Intl.Segmenter(undefined, { granularity: "grapheme" });

function atomsOf(runs: readonly Run[], bold: boolean): Atom[] {
  const atoms: Atom[] = [];
  for (const run of runs) {
    const text = typeof run === "string" ? run : run.text;
    let cls = typeof run === "string" ? "" : run.class;
    if (bold && !cls.split(" ").includes("b")) cls = cls ? `b ${cls}` : "b";
    for (const { segment } of GRAPHEMES.segment(text))
      atoms.push({ g: segment, cls, w: Bun.stringWidth(segment) });
  }
  return atoms;
}

const textWidth = (atoms: readonly Atom[]) =>
  atoms.reduce((n, a) => n + a.w, 0);

/**
 * Breaks atoms into lines of at most `max` columns at spaces (runs of
 * whitespace count as one space), and inside a word wider than a line, between
 * graphemes. Lines after the first start with `hang` spaces. An atom wider than
 * an empty line still goes on one, so the loop always moves on.
 */
function wrapAtoms(atoms: readonly Atom[], max: number, hang = 0): Atom[][] {
  const words: Atom[][] = [];
  let word: Atom[] = [];
  for (const a of atoms) {
    if (/^\s+$/.test(a.g)) {
      if (word.length > 0) words.push(word);
      word = [];
    } else word.push(a);
  }
  if (word.length > 0) words.push(word);
  const lines: Atom[][] = [];
  let line: Atom[] = [];
  let used = 0;
  let empty = true;
  const brk = () => {
    lines.push(line);
    line = Array.from({ length: hang }, () => ({ g: " ", cls: "", w: 1 }));
    used = hang;
    empty = true;
  };
  for (const w of words) {
    if (!empty && used + 1 + textWidth(w) <= max) {
      // The space takes the words' style when both sides share it (a link's underline).
      const prev = line[line.length - 1]?.cls ?? "";
      line.push({ g: " ", cls: prev === w[0]?.cls ? prev : "", w: 1 });
      used++;
    } else if (!empty) brk();
    for (const a of w) {
      if (!empty && used + a.w > max) brk();
      line.push(a);
      used += a.w;
      empty = false;
    }
  }
  if (!empty || lines.length === 0) lines.push(line);
  return lines;
}

/** A cell's source as atoms with its inline marks parsed, whitespace collapsed. */
function cellAtoms(source: string, header: boolean, links?: LinkRef[]): Atom[] {
  const runs = inlineRuns(clip(source, MAX_CODE_COLS), links);
  return wrapAtoms(atomsOf(runs, header), Number.POSITIVE_INFINITY)[0] ?? [];
}

/** A column never shrinks below this (or its natural width, when smaller). */
const MIN_COL = 6;

/**
 * Column widths that fit `budget` columns, or undefined when even the minimums
 * do not. The widest columns give up width first: every column is capped at one
 * common width, the largest that fits, and what that leaves over goes back to
 * the capped columns, widest first.
 */
function shrinkColumns(
  natural: readonly number[],
  budget: number,
): number[] | undefined {
  const floor = natural.map((n) => Math.min(n, MIN_COL));
  const at = (cap: number) =>
    natural.map((n, k) => Math.max(floor[k] ?? 0, Math.min(n, cap)));
  const total = (cols: number[]) => cols.reduce((a, b) => a + b, 0);
  if (total(floor) > budget) return undefined;
  let lo = 0;
  let hi = Math.max(...natural);
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (total(at(mid)) <= budget) lo = mid;
    else hi = mid - 1;
  }
  const cols = at(lo);
  let spare = budget - total(cols);
  const capped = cols
    .map((_, k) => k)
    .filter((k) => (cols[k] ?? 0) < (natural[k] ?? 0))
    .sort((a, b) => (natural[b] ?? 0) - (natural[a] ?? 0) || a - b);
  for (const k of capped) {
    if (spare <= 0) break;
    cols[k] = (cols[k] ?? 0) + 1;
    spare--;
  }
  return cols;
}

/** Appends a line of atoms to runs. */
function pushAtoms(runs: Run[], atoms: readonly Atom[]): void {
  for (const a of atoms) pushRun(runs, a.g, a.cls);
}

/** Lines of runs as one run list, bounded like a code block. */
function joinLines(lines: readonly Run[][]): Run[] {
  const runs: Run[] = [];
  lines.slice(0, MAX_CODE_LINES).forEach((line, n) => {
    if (n > 0) pushRun(runs, "\n");
    for (const run of line)
      typeof run === "string"
        ? pushRun(runs, run)
        : pushRun(runs, run.text, run.class);
  });
  if (lines.length > MAX_CODE_LINES)
    pushRun(runs, `\n… ${lines.length - MAX_CODE_LINES} more lines`, "com");
  return runs;
}

/**
 * A pipe table as monospace rows: each column as wide as its widest cell (by
 * display width), a divider rebuilt to that width keeping its `:` marks, the
 * header row bold. Cells are inline markdown (bold, code, links are runs).
 *
 * `maxWidth` is the columns the whole row may take. A table wider than that
 * shrinks its widest columns, down to MIN_COL, and wraps the cells' text onto
 * continuation lines, so every line fits. When even the minimums do not fit it
 * is a list instead, one "Header: value" line per cell and a blank line between
 * rows. The width is the one at the time of writing: a block is rendered once,
 * so a resize does not reflow a table already in the transcript.
 */
export function tableRuns(
  rows: string[],
  maxWidth = Number.POSITIVE_INFINITY,
  links?: LinkRef[],
): Run[] {
  const parsed = rows.map(tableCells);
  const divider = parsed[1] ?? [];
  const header =
    divider.length > 0 && divider.every((c) => DIVIDER_CELL.test(c));
  const count = Math.max(...parsed.map((r) => r.length));
  const shown = header ? [parsed[0] ?? [], ...parsed.slice(2)] : parsed;
  const cells = shown.map((row, n) =>
    Array.from({ length: count }, (_, k) =>
      cellAtoms(row[k] ?? "", header && n === 0, links),
    ),
  );
  const mark = (k: number) => divider[k] ?? "";
  const right = (k: number) =>
    header && mark(k).endsWith(":") && !mark(k).startsWith(":");
  const natural = Array.from({ length: count }, (_, k) =>
    Math.max(3, ...cells.map((row) => textWidth(row[k] ?? []))),
  );
  // "| " and " |" on the ends, " | " between columns.
  const frame = 3 * count + 1;
  const fit =
    frame + natural.reduce((a, b) => a + b, 0) <= maxWidth
      ? natural
      : shrinkColumns(natural, maxWidth - frame);
  if (!fit) return joinLines(listLines(cells, header, maxWidth));

  const lines: Run[][] = [];
  cells.forEach((row, n) => {
    const wrapped = row.map((cell, k) => wrapAtoms(cell, fit[k] ?? 0));
    const height = Math.max(...wrapped.map((w) => w.length));
    for (let i = 0; i < height; i++) {
      const line: Run[] = [];
      pushRun(line, "| ");
      wrapped.forEach((w, k) => {
        const atoms = w[i] ?? [];
        const pad = " ".repeat(Math.max(0, (fit[k] ?? 0) - textWidth(atoms)));
        if (k > 0) pushRun(line, " | ");
        if (right(k)) pushRun(line, pad);
        pushAtoms(line, atoms);
        if (!right(k)) pushRun(line, pad);
      });
      pushRun(line, " |");
      lines.push(line);
    }
    if (header && n === 0) {
      const line: Run[] = [];
      const parts = fit.map((w, k) => {
        const m = mark(k);
        const left = m.startsWith(":") ? ":" : "-";
        const end = m.endsWith(":") ? ":" : "-";
        return `${left}${"-".repeat(w - 2)}${end}`;
      });
      pushRun(line, `| ${parts.join(" | ")} |`);
      lines.push(line);
    }
  });
  return joinLines(lines);
}

/**
 * The list a table becomes when its columns cannot fit: each row's cells as
 * "Header: value" lines (the header bold, a continuation indented under it),
 * a blank line between rows. Without a header row the columns are numbered.
 */
function listLines(
  cells: readonly (readonly Atom[][])[],
  header: boolean,
  max: number,
): Run[][] {
  const label = (k: number) => {
    const named = header ? cells[0]?.[k] : undefined;
    return named && named.length > 0
      ? named
      : atomsOf([`Column ${k + 1}`], true);
  };
  const lines = (atoms: readonly Atom[], hang = 0): Run[][] =>
    wrapAtoms(atoms, max, hang).map((line) => {
      const runs: Run[] = [];
      pushAtoms(runs, line);
      return runs;
    });
  const out: Run[][] = [];
  const hang = max > 8 ? 2 : 0;
  (header ? cells.slice(1) : cells).forEach((row, n) => {
    if (n > 0) out.push([]);
    row.forEach((cell, k) => {
      if (cell.length === 0) return;
      const sep: Atom[] = [
        { g: ":", cls: "", w: 1 },
        { g: " ", cls: "", w: 1 },
      ];
      out.push(...lines([...label(k), ...sep, ...cell], hang));
    });
  });
  // A header with no rows under it: the names are all there is to show.
  if (out.length === 0)
    out.push(
      ...lines(
        (cells[0] ?? []).flatMap((_, k) => [
          ...label(k),
          { g: " ", cls: "", w: 1 },
        ]),
      ),
    );
  return out;
}

// --------------------------------------------------------------------- markdown

/**
 * An opening fence: three or more backticks or tildes, then an info string.
 * Indented four or more, it opens one only under a list item (`fenceOpens`),
 * where it sits under the item's text and models indent it that far.
 * The info string starts at a non-space and the mark run cannot give back
 * characters, so no two parts overlap and a long whitespace line cannot
 * backtrack (it is group 3, absent when empty).
 */
const FENCE_OPEN = /^(\s*)(`{3,}(?!`)|~{3,}(?!~))\s*([^`\s][^`]*)?$/;
/** A bullet (`-`, `*`, `+`) or a numbered (`1.`, `1)`) list item. */
const LIST_ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;

/** The same mark, at least as long, indented at most three past the opening. */
function closesFence(line: string, open: string, indent: number): boolean {
  const mark = open[0] ?? "`";
  return new RegExp(
    `^\\s{0,${indent + 3}}\\${mark}{${open.length},}\\s*$`,
  ).test(line);
}

/**
 * Whether a fence line opens a code block: indented up to three, always;
 * further, only under a list item. A paragraph's indented "```" is text
 * (CommonMark: such a line cannot interrupt a paragraph).
 */
function fenceOpens(indent: number, underItem: boolean): boolean {
  return indent <= 3 || underItem;
}

/**
 * A list item less indented than an indented fence: the next item of the
 * list the fence sits in. It ends the code block (CommonMark ends it with
 * its item) and starts the item.
 */
function leavesFence(line: string, indent: number): boolean {
  if (indent === 0) return false;
  const item = LIST_ITEM.exec(line);
  return item !== null && (item[1]?.length ?? 0) < indent;
}

/** A code line without the fence's own indent (a lazier line loses what it has). */
function unindent(line: string, indent: number): string {
  const lead = /^ */.exec(line)?.[0].length ?? 0;
  return line.slice(Math.min(lead, indent));
}

/**
 * True when `text` starts with a fence that is not closed yet: the code block
 * a stream is still writing, which grows by whole lines.
 */
export function startsOpenFence(text: string): boolean {
  const lines = cleanText(text).split("\n");
  const fence = FENCE_OPEN.exec(lines[0] ?? "");
  if (!fence) return false;
  const open = fence[2] ?? "```";
  const indent = fence[1]?.length ?? 0;
  for (let i = 1; i < lines.length; i++) {
    const line = lines[i] ?? "";
    if (closesFence(line, open, indent) || leavesFence(line, indent))
      return false;
  }
  return true;
}

/**
 * The fenced code blocks of a markdown source, in order, each with its info
 * word and its text as the model wrote it (what `/copy code` puts on the
 * clipboard): a tab stays a tab and a long block or reply is not cut. It is
 * the view's own parse that finds them (`parseBlocksAt` with a capture), so
 * what is a code block here is what the transcript shows as one: lazy lines
 * after a list item, an indented fence under an item, a table or a quote
 * ending an item, an unclosed fence running to the end.
 */
export function fencedCodeBlocks(
  markdown: string,
): { lang: string; text: string }[] {
  const codes: { lang: string; text: string }[] = [];
  parseBlocksAt(markdown, "c", 0, undefined, undefined, codes);
  return codes;
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
 *
 * `tableCols` is the width a table's rows may take (`tableWidth`); without it
 * a table keeps its natural width. It is read when the block is parsed, so a
 * later resize does not reflow a table already written.
 */
export function parseBlocks(
  markdown: string,
  prefix = "b",
  links?: LinkRef[],
  tableCols?: number,
): Block[] {
  return parseBlocksAt(markdown, prefix, 0, links, tableCols).blocks;
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
  links?: LinkRef[],
  tableCols?: number,
  /**
   * Collects each fenced code block's text as written (see
   * fencedCodeBlocks). With it the whole source is parsed, not the view's
   * bounded prefix, and the classification of every line is the same.
   */
  codes?: { lang: string; text: string }[],
): Parsed {
  const blocks: Block[] = [];
  const starts: number[] = [];
  const cut = codes === undefined && markdown.length > MAX_MESSAGE_CHARS;
  const source = cut ? markdown.slice(0, MAX_MESSAGE_CHARS) : markdown;
  const lines = cleanText(source).split("\n");
  // The source's own lines, for the text a capture keeps. Cleaning can only
  // disagree on the count when an escape sequence spans lines; then the
  // cleaned lines stand in, so the two never misalign.
  const rawSplit = codes ? source.replace(/\r\n/g, "\n").split("\n") : lines;
  const rawLines = rawSplit.length === lines.length ? rawSplit : lines;
  let para: string[] = [];
  let paraAt = 0;
  let quote: string[] = [];
  let quoteAt = 0;
  let item:
    | { marker: string; nest: boolean; lines: string[]; at: number }
    | undefined;
  // Under a list item: from an item to the next unindented paragraph line,
  // heading, quote or table. Blank lines and an item's code keep it.
  let underItem = false;
  const next = () => `${prefix}${first + blocks.length}`;
  const flush = () => {
    if (para.length > 0) {
      starts.push(paraAt);
      blocks.push(textBlock(next(), "para", para.join("\n"), links));
      para = [];
    }
    if (quote.length > 0) {
      starts.push(quoteAt);
      blocks.push(textBlock(next(), "quote", quote.join("\n"), links));
      quote = [];
    }
    if (item) {
      starts.push(item.at);
      blocks.push(
        block(
          next(),
          { item: true, nest: item.nest, marker: item.marker },
          inlineRuns(clip(item.lines.join("\n"), MAX_PARA_CHARS), links),
        ),
      );
      item = undefined;
    }
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const fence = FENCE_OPEN.exec(line);
    const listItem = LIST_ITEM.exec(line);
    if (fence && fenceOpens(fence[1]?.length ?? 0, underItem)) {
      flush();
      const at = i;
      const open = fence[2] ?? "```";
      const indent = fence[1]?.length ?? 0;
      const body: string[] = [];
      const rawBody: string[] = [];
      for (
        i++;
        i < lines.length && !closesFence(lines[i] ?? "", open, indent);
        i++
      ) {
        if (leavesFence(lines[i] ?? "", indent)) {
          // The next item: the outer loop reads it.
          i--;
          break;
        }
        body.push(unindent(lines[i] ?? "", indent));
        rawBody.push(unindent(rawLines[i] ?? "", indent));
      }
      const lang = clip((fence[3] ?? "").trim().split(/\s+/)[0] ?? "", 24);
      codes?.push({ lang, text: rawBody.join("\n") });
      starts.push(at);
      blocks.push(codeBlock(next(), lang, body));
    } else if (TABLE_ROW.test(line)) {
      flush();
      underItem = false;
      const at = i;
      const table: string[] = [line.trim()];
      while (TABLE_ROW.test(lines[i + 1] ?? "")) {
        i++;
        table.push((lines[i] ?? "").trim());
      }
      starts.push(at);
      blocks.push(
        block(
          next(),
          { code: true, table: true },
          tableRuns(table, tableCols, links),
        ),
      );
    } else if (/^#{1,6}\s/.test(line)) {
      flush();
      underItem = false;
      starts.push(i);
      blocks.push(
        textBlock(
          next(),
          "head",
          line.replace(/^#+\s*/, "").replace(/\s+#+\s*$/, ""),
          links,
        ),
      );
    } else if (/^\s*>\s?/.test(line)) {
      if (para.length > 0 || item) flush();
      underItem = false;
      if (quote.length === 0) quoteAt = i;
      quote.push(line.replace(/^\s*>\s?/, ""));
    } else if (listItem) {
      flush();
      underItem = true;
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
      // An indented paragraph is the item's own; an unindented one ends it.
      if (!/^\s{2,}/.test(line)) underItem = false;
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
  /** What the card's border names: the tool as a reader says it ("Edit", "Command"). */
  title: string;
  status: string;
  /** The finished state at a glance: ✓ succeeded, ✗ denied or failed, ! otherwise; "" while running. */
  mark: string;
  /** How long the call ran ("1.2s"); "" when unknown (a resumed session, a call that never ran). */
  took: string;
  args: string;
  /** There is an args line, a path or a URL: it is cut in the middle (both ends name it). */
  args_path: boolean;
  /** There is an args line of any other kind: it is cut at its end. */
  args_line: boolean;
  ok: boolean;
  bad: boolean;
  warn: boolean;
  running: boolean;
  /** The output as runs for one `wrap: pre-wrap` text. */
  out: Run[];
}

export const NO_CARD: Card = Object.freeze({
  name: "",
  title: "",
  status: "",
  mark: "",
  took: "",
  args: "",
  args_path: false,
  args_line: false,
  ok: false,
  bad: false,
  warn: false,
  running: false,
  out: [],
}) as Card;

/** The server and tool of an MCP `SERVER__TOOL` name, or undefined without the separator. */
export function mcpParts(
  name: string,
): { server: string; tool: string } | undefined {
  const at = name.indexOf("__");
  if (at <= 0 || at + 2 >= name.length) return undefined;
  return { server: name.slice(0, at), tool: name.slice(at + 2) };
}

const TOOL_TITLES: Record<string, string> = {
  read: "Read",
  read_image: "Image",
  list: "List",
  search: "Search",
  fetch: "Fetch",
  web_search: "Web search",
  task: "Subagent",
  todo_write: "Todo",
  ask_user: "Question",
  goal_status: "Goal status",
  edit: "Edit",
  write: "Write",
  run_command: "Command",
};

/** A card's title: the built-in tool's word, `MCP · server · tool`, or the name as it came. */
export function toolTitle(name: string): string {
  const clean = clip(terminalLine(name), 80);
  // Own keys only: a model can name a tool `constructor` or `__proto__`.
  if (Object.hasOwn(TOOL_TITLES, name)) return TOOL_TITLES[name] as string;
  const parts = mcpParts(clean);
  return parts ? `MCP · ${parts.server} · ${parts.tool}` : clean;
}

/** A call's duration as a card shows it: <0.1s, 0.4s, 12s, 2m 05s. */
export function formatTook(ms: number): string {
  if (ms < 100) return "<0.1s";
  // Each branch rounds first, so 9.96 s reads 10s and 59.6 s reads 1m 00s.
  const tenths = Math.round(ms / 100);
  if (tenths < 100) return `${(tenths / 10).toFixed(1)}s`;
  const whole = Math.round(ms / 1000);
  if (whole < 60) return `${whole}s`;
  return `${Math.floor(whole / 60)}m ${String(whole % 60).padStart(2, "0")}s`;
}

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
  return callLine(call).text;
}

/** callSummary's line, and whether it is a path or a URL (cut in the middle on a card). */
function callLine(call: ToolCall | undefined): { text: string; path: boolean } {
  if (!call) return { text: "", path: false };
  const args = call.function.arguments;
  const str = (value: unknown) =>
    typeof value === "string" ? value : undefined;
  const line = (text: string, path = false) => ({
    text: clip(cleanText(text), 160),
    path,
  });
  switch (call.function.name) {
    case "search": {
      const pattern = str(args.pattern);
      const where = str(args.path);
      if (pattern !== undefined)
        return line(where ? `${pattern} in ${where}` : pattern);
      break;
    }
    case "web_search": {
      const query = str(args.query);
      if (query !== undefined) return line(query);
      break;
    }
    case "fetch": {
      const url = str(args.url);
      if (url !== undefined) return line(url, true);
      break;
    }
    case "task": {
      const prompt = str(args.prompt);
      if (prompt !== undefined) return line(prompt.trim().split("\n")[0] ?? "");
      break;
    }
    case "ask_user": {
      const question = str(args.question);
      if (question !== undefined)
        return line(question.trim().split("\n")[0] ?? "");
      break;
    }
    case "goal_status": {
      const state = str(args.state);
      const summary = str(args.summary)?.trim().split("\n")[0];
      if (state !== undefined)
        return line(summary ? `${state} · ${summary}` : state);
      break;
    }
    case "todo_write":
      if (Array.isArray(args.todos))
        return line(
          `${args.todos.length} item${args.todos.length === 1 ? "" : "s"}`,
        );
      break;
  }
  if (typeof args.path === "string") return line(args.path, true);
  if (Array.isArray(args.argv)) return line(commandLine(args.argv.map(String)));
  return line(JSON.stringify(args));
}

/** A row of a card's output: its text and the span class that styles it. */
type OutRow = { text: string; cls: string };

/**
 * What a finished edit or a created file changed, as rows after the receipt:
 * the edit's own old and new strings, or the new file's lines. Only for a
 * call that succeeded (the runtime's word for a change it applied; a
 * receipt with no status reads as unverified and shows none), and never
 * for a replaced file, whose old text is not in the call.
 */
function changeRows(
  name: string,
  call: ToolCall | undefined,
  state: ToolState,
  output: string,
): OutRow[] {
  if (!call || state !== "succeeded") return [];
  const args = call.function.arguments;
  const created =
    name === "write" &&
    typeof args.content === "string" &&
    output.startsWith("Created ");
  const edited =
    name === "edit" &&
    typeof args.old_string === "string" &&
    typeof args.new_string === "string";
  if (!created && !edited) return [];
  const [before, after] = edited
    ? [String(args.old_string), String(args.new_string)]
    : ["", String(args.content)];
  return changeLines(before, after).map((row) => {
    const text = cleanText(row.text);
    switch (row.kind) {
      case "del":
        return { text: `- ${text}`, cls: "del" };
      case "add":
        return { text: `+ ${text}`, cls: "add" };
      case "gap":
        return { text: "…", cls: "note" };
      default:
        return { text: `  ${text}`, cls: "" };
    }
  });
}

/**
 * The first lines of a tool's output as runs, with a final "N more lines" row.
 * The optional head line (exit code) is the first line, in class `err` or `note`.
 */
export function outputRuns(
  output: string,
  head?: { text: string; bad: boolean },
  change: readonly OutRow[] = [],
  details?: number,
): Run[] {
  const lines = cleanText(output).split("\n");
  while (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  const all: OutRow[] = [
    ...(head ? [{ text: head.text, cls: head.bad ? "err" : "note" }] : []),
    ...lines.map((text) => ({ text, cls: "" })),
    ...change,
  ];
  const runs: Run[] = [];
  all.slice(0, OUT_LINES).forEach((row, n) => {
    // A line break between two rows of one class stays in their run.
    if (n > 0) pushRun(runs, "\n", all[n - 1]?.cls === row.cls ? row.cls : "");
    pushRun(runs, clip(row.text, OUT_COLS), row.cls);
  });
  const more = all.length - OUT_LINES;
  // The pointer names this card's own output: on an older card, /details
  // alone would open a newer one.
  if (more > 0)
    pushRun(
      runs,
      `\n… ${more} more ${more === 1 ? "line" : "lines"}${details === undefined ? "" : ` · /details ${details} for full output`}`,
      "note",
    );
  return runs;
}

/**
 * The runtime's MCP envelope (`McpToolResult`) read at the client boundary:
 * a head that keeps the uncertainty in view, and the server's own text as the
 * body (pretty-printed when it is a JSON object or array). Undefined for
 * anything that is not exactly that shape, which the caller shows as it is.
 * Display only: the model and the session keep the envelope.
 */
function mcpEnvelope(
  output: string,
): { head: { text: string; bad: boolean }; body: string } | undefined {
  let r: Record<string, unknown>;
  try {
    const parsed: unknown = JSON.parse(output);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
      return undefined;
    r = parsed as Record<string, unknown>;
  } catch {
    return undefined;
  }
  const { transport, domain, evidence, isError, text } = r;
  if (
    typeof transport !== "string" ||
    typeof domain !== "string" ||
    typeof evidence !== "string" ||
    typeof isError !== "boolean" ||
    typeof text !== "string"
  )
    return undefined;
  // Transport success says the call went through, nothing about the result:
  // the head names what is and is not known about it.
  const head = [
    isError ? "error" : "",
    transport === "success" ? "" : `transport ${transport}`,
    evidence,
    `domain ${domain}`,
  ].filter(Boolean);
  return {
    head: { text: head.join(" · "), bad: isError },
    body: prettyJson(text),
  };
}

/**
 * `text` indented when it is a JSON object or array that prints back the
 * same; otherwise as it is. A number past double precision would be
 * rounded, a repeated key dropped and an escape rewritten: the card shows
 * the server's text, so such JSON is shown as sent.
 */
function prettyJson(text: string): string {
  if (!/^\s*[[{]/.test(text)) return text;
  try {
    const value: unknown = JSON.parse(text);
    if (JSON.stringify(value) !== withoutSpaces(text)) return text;
    return JSON.stringify(value, null, 2);
  } catch {
    // Not JSON, or cut mid-way by the runtime's output cap: show it as it is.
    return text;
  }
}

/** JSON text without the whitespace between its tokens; strings are kept. */
function withoutSpaces(json: string): string {
  let out = "";
  let inString = false;
  let escaped = false;
  for (const char of json) {
    if (inString) {
      out += char;
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') inString = false;
    } else if (char === '"') {
      inString = true;
      out += char;
    } else if (!" \t\n\r".includes(char)) out += char;
  }
  return out;
}

/** What to show under a finished tool card: an optional head line, then the output. */
function resultParts(
  name: string,
  output: string,
): { head?: { text: string; bad: boolean }; body: string } {
  if (mcpParts(name)) {
    const mcp = mcpEnvelope(output);
    if (mcp) return mcp;
  }
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

/** The `{status, output}` of a tool message's content, or the content as it is. */
function parseReceipt(content: string): { status: string; output: string } {
  let status = "unknown";
  let output = content;
  try {
    const result = JSON.parse(content) as {
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
  return { status, output };
}

/**
 * A finished tool receipt kept whole for the `/details` viewer: the name and
 * status as its card shows them, the card's args line, and the full output
 * text (a command's exit head line included, as the card shows it). It is
 * UI memory only — never a model message, never persisted.
 */
export interface ToolReceipt {
  /** The number its card's pointer gives: `/details N` opens it. */
  n: number;
  name: string;
  status: string;
  args: string;
  output: string;
}

/** `/details N` reaches the newest this many receipts; older ones are let go. */
export const MAX_RECEIPTS = 32;

/** The output the viewer keeps; past it the tail below says what was cut. */
export const DETAILS_MAX_CHARS = 64_000;
export const DETAILS_TRUNCATED_TAIL = `… output truncated for the viewer (first ${DETAILS_MAX_CHARS} characters)`;

/** Cuts viewer output to DETAILS_MAX_CHARS code points, saying so when it does. */
export function capDetailsOutput(text: string): string {
  if (text.length <= DETAILS_MAX_CHARS) return text;
  const chars = Array.from(text);
  if (chars.length <= DETAILS_MAX_CHARS) return text;
  return `${chars.slice(0, DETAILS_MAX_CHARS).join("")}\n${DETAILS_TRUNCATED_TAIL}`;
}

function toolCard(
  name: string,
  call: ToolCall | undefined,
  state: ToolState,
  output = "",
  details?: number,
): Card {
  const parts = resultParts(name, output);
  const line = callLine(call);
  const bad = state === "denied" || state === "failed";
  const running =
    state === "running" || state === "requested" || state === "approval";
  return {
    name: clip(terminalLine(name), 80),
    title: toolTitle(name),
    status: STATE_LABEL[state],
    mark: running ? "" : state === "succeeded" ? "✓" : bad ? "✗" : "!",
    took: "",
    args: line.text,
    args_path: line.text !== "" && line.path,
    args_line: line.text !== "" && !line.path,
    ok: state === "succeeded",
    bad,
    warn: state === "outcome_unknown" || state === "unverified",
    running,
    out: output
      ? outputRuns(
          parts.body,
          parts.head,
          changeRows(name, call, state, output),
          details,
        )
      : [],
  };
}

/**
 * A tool card whose call waits on the person (an approval, a question): no
 * spinner, since the live message's head already says the turn waits and
 * nothing may keep animating beside its still glyph; an amber "?" and
 * border instead. The status is the card's own word, not the head's.
 */
export function onHold(card: Card, status: string): Card {
  return { ...card, status, running: false, mark: "?", warn: true };
}

// ------------------------------------------------------------------------ views

export interface MessageView {
  id: string;
  is_user: boolean;
  is_asst: boolean;
  is_tool: boolean;
  /**
   * A UI-local notice (a goal's turn, a window or compaction note, a
   * warning): the client's words, shown and never sent to the model.
   */
  is_notice: boolean;
  /** A UI-local error (a failed turn's full error): a notice labelled Error. */
  is_error: boolean;
  blocks: Block[];
  card: Card;
  /** The message's link targets, numbered as the body's `[n]` markers ([] for none). */
  links: LinkRef[];
  /** Images the message attached, capped by messageImages ([] for none). */
  images: ImageRef[];
  /** Images past the view's caps: the message says "+N more". */
  images_more: number;
}

/**
 * The in-flight message. `blocks` are the blocks that are final (a stream only
 * ever appends to them), `tail` the one or two still being written, which the
 * stream replaces. A tool call has neither: its `card` is the live view.
 */
export type LiveView = MessageView & { active: boolean; tail: Block[] };

function view(
  id: string,
  role: "user" | "asst" | "tool" | "notice" | "error",
  blocks: Block[],
  card: Card = NO_CARD,
  links: LinkRef[] = [],
  images: MessageImages = { images: [], more: 0 },
): MessageView {
  return {
    id,
    is_user: role === "user",
    is_asst: role === "asst",
    is_tool: role === "tool",
    is_notice: role === "notice",
    is_error: role === "error",
    blocks,
    card,
    links,
    images: images.images,
    images_more: images.more,
  };
}

/** The empty in-flight slot: the `live` store value when nothing is streaming. */
export const IDLE_LIVE: LiveView = Object.freeze({
  ...view("live", "asst", []),
  active: false,
  tail: [],
}) as LiveView;

/**
 * The reasoning box under the assistant message head while the model reasons before it answers.
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
    ...view("live", "tool", [], toolCard(name, call, state)),
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
  // By the message itself, not its call id: a provider may number its calls
  // per reply, so two turns can both have a `call_0`.
  private readonly numbers = new WeakMap<Message, number>();

  /**
   * `tableCols`: the table width when a message is built (see `tableWidth`).
   * `upcoming`: the number the next tool output gets. A builder that
   * replaces another starts where it stopped, so no number a card in the
   * scrollback shows ever names a different output.
   */
  constructor(
    private readonly tableCols?: () => number,
    private upcoming = 1,
  ) {}

  /** The number the next tool output will get. */
  get nextReceiptNumber(): number {
    return this.upcoming;
  }

  /**
   * A tool message's number, given on first sight: its card's pointer and
   * its receipt read the same one, whichever is built first.
   */
  private numberOf(message: Message): number {
    let n = this.numbers.get(message);
    if (n === undefined) {
      n = this.upcoming++;
      this.numbers.set(message, n);
    }
    return n;
  }

  /** The call a `tool_requested` event belongs to (they arrive in call order). */
  takeCall(): ToolCall | undefined {
    return this.pending.shift();
  }

  /**
   * The view of a message, or undefined when it shows nothing (system,
   * empty assistant). `typed`: a user message the user typed in this
   * process, theirs whatever it starts with; otherwise (a saved one) its
   * text alone says whether a goal wrote it.
   */
  fromMessage(message: Message, typed = false): MessageView | undefined {
    if (message.role === "system") return undefined;
    if (message.role === "assistant") {
      this.pending = (message.tool_calls ?? []).slice();
      for (const call of this.pending)
        if (call.id) this.calls.set(call.id, call);
      if (!message.content.trim()) return undefined;
      return this.text("asst", message.content);
    }
    if (message.role === "user") {
      // A goal's host-written turn is the client's words, not the user's.
      const goal = typed ? undefined : goalNotice(message.content);
      return goal ? this.notice(goal) : this.text("user", message.content);
    }
    const name = message.tool_name ?? "result";
    const call = message.tool_call_id
      ? this.calls.get(message.tool_call_id)
      : undefined;
    const { status, output } = parseReceipt(message.content);
    return view(
      this.id(),
      "tool",
      [],
      toolCard(name, call, asToolState(status), output, this.numberOf(message)),
      [],
      messageImages(message.images),
    );
  }

  /**
   * A text message's view, its links collected across every block so the body's
   * [n] markers and the sources row number the same targets.
   */
  private text(role: "user" | "asst", markdown: string): MessageView {
    const links: LinkRef[] = [];
    return view(
      this.id(),
      role,
      parseBlocks(markdown, "b", links, this.tableCols?.()),
      NO_CARD,
      links,
    );
  }

  /**
   * The whole receipt of a tool message, for the `/details` viewer: the
   * same parse the card truncates, kept in full (capped for the viewer).
   * Undefined for every other message.
   */
  receiptOf(message: Message): ToolReceipt | undefined {
    if (message.role !== "tool") return undefined;
    const name = message.tool_name ?? "result";
    const call = message.tool_call_id
      ? this.calls.get(message.tool_call_id)
      : undefined;
    const { status, output } = parseReceipt(message.content);
    const state = asToolState(status);
    // The change the card starts to show, whole: the card's "/details for
    // full output" promises every row it cut.
    const change = changeRows(name, call, state, output)
      .map((row) => row.text)
      .join("\n");
    return {
      n: this.numberOf(message),
      name: clip(terminalLine(name), 80),
      status: STATE_LABEL[state],
      args: callSummary(call),
      output: capDetailsOutput(
        change
          ? `${resultSummary(name, output)}\n${change}`
          : resultSummary(name, output),
      ),
    };
  }

  /**
   * What arrived of a reply that never finished (cancelled or failed), as
   * the live region parsed it. A transcript view only: the model's history
   * and the session never had this message.
   */
  partial(blocks: Block[], links: LinkRef[]): MessageView {
    return view(this.id(), "asst", blocks, NO_CARD, links.slice());
  }

  /** A view for text the client itself wrote (never sent to the model). */
  user(text: string): MessageView {
    return this.text("user", text);
  }

  /**
   * A UI-local notice, as a quote so it reads as the client's words, not
   * the model's. Bounded like any message by parseBlocks. It is only ever
   * a transcript view: no Message is made from it, so it is never sent to
   * the model or persisted as one.
   */
  notice(text: string): MessageView {
    return this.quoted("notice", text);
  }

  /** A UI-local error: a failed turn's full error, as a notice labelled Error. */
  error(text: string): MessageView {
    return this.quoted("error", text);
  }

  private quoted(role: "notice" | "error", text: string): MessageView {
    const quoted = cleanText(text)
      .split("\n")
      .map((line) => `> ${line}`)
      .join("\n");
    return view(this.id(), role, parseBlocks(quoted));
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
