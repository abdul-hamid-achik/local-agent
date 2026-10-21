// The ONE module that maps text to what the Tuimark markup reads:
//
//   runtime Message  ->  MessageView { blocks | card }  ->  store (messages[], live)
//
// Tuimark has no inline markup and no markdown parser, so this module turns a
// message into plain data the templates in ui/app.tui walk with `each`:
//
//   message.blocks[] -> { para | head | quote | code } -> code.lines[] -> line.segs[]
//
// Phase 2 (Tuimark 0.4d `runs` and `append`) replaces THIS module and nothing
// else: the templates read `blocks`, `card` and the flags below, and every flag
// is always present (true or false) because the markup has no equality test
// and `if` / `class:NAME` on a missing member report B002 / B003.

import type { Message, ToolCall } from "@local-agent/runtime";
import { terminalText } from "@local-agent/vue-tui/text";

// ---------------------------------------------------------------- text hygiene

/** Strips ANSI and control characters, drops CR and expands tabs. */
export function cleanText(text: string): string {
  return terminalText(text).replaceAll("\t", "  ");
}

/** Cuts a string to `max` columns (code units), ending in an ellipsis. */
export function clip(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
}

// ------------------------------------------------------------------- code blocks

export interface Seg {
  i: number;
  t: string;
  kw: boolean;
  str: boolean;
  num: boolean;
  com: boolean;
}

export interface CodeLine {
  n: number;
  segs: Seg[];
}

export interface Block {
  id: string;
  para: boolean;
  head: boolean;
  quote: boolean;
  code: boolean;
  t: string;
  lang: string;
  lines: CodeLine[];
}

/** A code block keeps this many lines; the rest is one "N more lines" row. */
export const MAX_CODE_LINES = 300;
/** A paragraph keeps this many characters. */
export const MAX_PARA_CHARS = 20_000;

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

type Kind = "kw" | "str" | "num" | "com" | "";

/** Splits one source line into classified runs. Whitespace stays in the runs. */
export function tokenizeLine(line: string): Omit<Seg, "i">[] {
  const out: Omit<Seg, "i">[] = [];
  const push = (t: string, kind: Kind) => {
    const last = out[out.length - 1];
    // Merge neighbours of the same plain kind so plain code is not one node per character.
    if (
      last &&
      kind === "" &&
      !last.kw &&
      !last.str &&
      !last.num &&
      !last.com
    ) {
      last.t += t;
      return;
    }
    out.push({
      t,
      kw: kind === "kw",
      str: kind === "str",
      num: kind === "num",
      com: kind === "com",
    });
  };
  const re = new RegExp(TOKEN.source, "y");
  for (let m = re.exec(line); m; m = re.exec(line)) {
    if (m[1] !== undefined) push(m[1], "com");
    else if (m[2] !== undefined) push(m[2], "str");
    else if (m[3] !== undefined) push(m[3], "num");
    else if (m[4] !== undefined) push(m[4], KEYWORDS.has(m[4]) ? "kw" : "");
    else push(m[0], "");
  }
  return out;
}

function codeBlock(id: string, lang: string, source: string[]): Block {
  const kept = source.slice(0, MAX_CODE_LINES);
  const lines: CodeLine[] = kept.map((line, n) => ({
    n,
    segs: tokenizeLine(line).map((s, i) => ({ i, ...s })),
  }));
  if (source.length > kept.length)
    lines.push({
      n: kept.length,
      segs: [
        {
          i: 0,
          t: `… ${source.length - kept.length} more lines`,
          kw: false,
          str: false,
          num: false,
          com: true,
        },
      ],
    });
  return {
    id,
    para: false,
    head: false,
    quote: false,
    code: true,
    t: "",
    lang,
    lines,
  };
}

function textBlock(
  id: string,
  kind: "para" | "head" | "quote",
  t: string,
): Block {
  return {
    id,
    para: kind === "para",
    head: kind === "head",
    quote: kind === "quote",
    code: false,
    t: clip(t, MAX_PARA_CHARS),
    lang: "",
    lines: [],
  };
}

// --------------------------------------------------------------------- markdown

/**
 * A small markdown subset: paragraphs, `#` headings, bullets, `>` quotes,
 * pipe tables (kept aligned as a code block) and fenced code. Inline marks
 * (`**bold**`, `` `code` ``) stay literal until Tuimark has `runs` (0.4d).
 */
export function parseBlocks(markdown: string, prefix = "b"): Block[] {
  const blocks: Block[] = [];
  const lines = cleanText(markdown).split("\n");
  let para: string[] = [];
  let quote: string[] = [];
  const next = () => `${prefix}${blocks.length}`;
  const flush = () => {
    if (para.length > 0) {
      blocks.push(textBlock(next(), "para", para.join("\n")));
      para = [];
    }
    if (quote.length > 0) {
      blocks.push(textBlock(next(), "quote", quote.join("\n")));
      quote = [];
    }
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const fence = /^\s{0,3}(?:```|~~~)\s*([\w+#.-]*)\s*$/.exec(line);
    if (fence) {
      flush();
      const body: string[] = [];
      for (
        i++;
        i < lines.length && !/^\s{0,3}(?:```|~~~)\s*$/.test(lines[i] ?? "");
        i++
      ) {
        body.push(lines[i] ?? "");
      }
      blocks.push(codeBlock(next(), fence[1] ?? "", body));
    } else if (/^\s*\|.*\|\s*$/.test(line)) {
      flush();
      const table: string[] = [line.trim()];
      while (/^\s*\|.*\|\s*$/.test(lines[i + 1] ?? "")) {
        i++;
        table.push((lines[i] ?? "").trim());
      }
      blocks.push(codeBlock(next(), "table", table));
    } else if (/^#{1,6}\s/.test(line)) {
      flush();
      blocks.push(textBlock(next(), "head", line.replace(/^#+\s*/, "")));
    } else if (/^\s*>\s?/.test(line)) {
      if (para.length > 0) flush();
      quote.push(line.replace(/^\s*>\s?/, ""));
    } else if (line.trim() === "") {
      flush();
    } else {
      if (quote.length > 0) flush();
      para.push(line.replace(/^(\s*)[-*+]\s+/, "$1• "));
    }
  }
  flush();
  return blocks;
}

/**
 * Parses an in-flight reply. An unterminated fence is closed on the fly, so a
 * half-streamed code block already shows as code.
 */
export function parseStreaming(markdown: string, prefix = "l"): Block[] {
  const fences = markdown.split("\n").filter((l) => /^\s{0,3}```/.test(l));
  return parseBlocks(
    fences.length % 2 === 1 ? `${markdown}\n\`\`\`` : markdown,
    prefix,
  );
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
  out: { n: number; t: string }[];
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

/** The first lines of a tool's output, with a final "N more lines" row. */
export function outputLines(output: string): { n: number; t: string }[] {
  const all = cleanText(output).split("\n");
  while (all.length > 0 && all[all.length - 1] === "") all.pop();
  const out = all
    .slice(0, OUT_LINES)
    .map((t, n) => ({ n, t: clip(t, OUT_COLS) }));
  if (all.length > OUT_LINES)
    out.push({ n: OUT_LINES, t: `… ${all.length - OUT_LINES} more lines` });
  return out;
}

/** What to show under a finished tool card, from the `{status, output}` the runtime stores. */
export function resultSummary(name: string, output: string): string {
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
        return [[code, ...flags].join(", "), r.output].join("\n");
      }
    } catch {
      // Not JSON: show it as it is.
    }
  }
  return output;
}

function toolCard(
  name: string,
  args: string,
  state: ToolState,
  output = "",
): Card {
  return {
    name: clip(cleanText(name), 80),
    status: STATE_LABEL[state],
    args,
    ok: state === "succeeded",
    bad: state === "denied" || state === "failed",
    warn: state === "outcome_unknown" || state === "unverified",
    running:
      state === "running" || state === "requested" || state === "approval",
    out: output ? outputLines(resultSummary(name, output)) : [],
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

export type LiveView = MessageView & { active: boolean };

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
}) as LiveView;

/** An in-flight assistant reply (streaming markdown). */
export function streamingView(text: string): LiveView {
  return { ...view("live", "asst", parseStreaming(text)), active: true };
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
