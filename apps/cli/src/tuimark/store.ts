// The Tuimark store: every path ui/app.tui reads, with its shape. One place, so
// the controller, the samples the goldens are made from, and the tests cannot
// drift from the markup.

import type { ApprovalRequest, Provider } from "@local-agent/runtime";
import { approvalPreview } from "#cli/preview";
import {
  type Block,
  cleanText,
  clip,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  type MessageView,
  NO_CARD,
  type ThinkingView,
} from "#cli/tuimark/blocks";
import {
  filterPalette,
  type HelpRow,
  helpRows,
  type Intent,
  type PaletteItem,
  type SlashRow,
} from "#cli/tuimark/commands";
import type { TranscriptSnapshot } from "#cli/tuimark/transcript";

export const IDLE_STATUS = "Ready · /help for commands";

/** The idle status line; it keeps the MCP count for the whole session. */
export function idleStatus(mcpServers: number): string {
  return mcpServers ? `${IDLE_STATUS} · MCP ${mcpServers}` : IDLE_STATUS;
}
export const PLACEHOLDER = "Message the model";
export const PLACEHOLDER_BUSY = "Generating…";
/** While the dialog is not armed yet: only deny works. */
export const APPROVAL_WAIT_STATUS = "Waiting for approval · n deny · Esc deny";
/** Once the dialog accepts `y`. */
export const APPROVAL_STATUS =
  "Waiting for approval · y allow · n deny · Esc deny";
export const APPROVAL_TRUNCATED_STATUS =
  "Preview truncated · y allow · n deny · Esc deny";
export const BUSY_STATUS_LINE = "Generating…";
/** Busy, but no text/thinking/tool event has arrived yet this turn. */
export const WAITING_STATUS = "Waiting for the model…";
/** A `y` swallowed by the arm delay says so instead of going silent. */
export const APPROVAL_UNARMED_STATUS = "Approval not armed yet — press y again";

export interface DiffLine {
  n: number;
  t: string;
  add: boolean;
  del: boolean;
  /** A row the client wrote (the "more lines" marker), not part of the preview text. */
  note: boolean;
}

export interface ApprovalState {
  open: boolean;
  title: string;
  body: string;
  danger: boolean;
  no: string;
  yes: string;
  /** The preview was cut at the row cap: the dialog says so and still defaults to Deny. */
  truncated: boolean;
  lines: DiffLine[];
}

export interface Store {
  header: {
    provider: string;
    model: string;
    mode: Intent;
    session: string;
    /** Basename of the workspace root, for the empty-state welcome. "" when unknown. */
    workspace: string;
    ctx_label: string;
    ctx_pct: number;
    /** Threshold styling for the ctx text/bar: warn at >=75%, danger at >=90%. */
    ctx_warn: boolean;
    ctx_danger: boolean;
  };
  /** True only before the first message: the welcome shows. Never true again. */
  empty: boolean;
  /** Fullscreen mouse reporting (`<tui mouse="mouse_on">`). Default off. */
  mouse_on: boolean;
  messages: MessageView[];
  earlier: { n: number };
  live: LiveView;
  /** The model's reasoning while it streams; gone once the answer starts. */
  thinking: ThinkingView;
  approval: ApprovalState;
  palette: { open: boolean; query: string; cur: string };
  found: { any: boolean; items: PaletteItem[] };
  /**
   * The `/` popup over the composer: display-only rows (nothing in an
   * `each` can take focus), the chosen one carrying `sel`. The controller
   * rewrites it as the draft is typed; inline it lives in the region and
   * is never committed.
   */
  slash: { open: boolean; rows: SlashRow[] };
  /** The help overlay: `cmds` is the slash catalog, derived once at build. */
  help: { open: boolean; cmds: HelpRow[] };
  draft: string;
  placeholder: string;
  busy: boolean;
  status: string;
  /**
   * Scratch paths of the inline client (ui/inline.tui reads them only at
   * commit time): the view or blocks a `commit` is about to print into the
   * scrollback. Written right before the commit, cleared after its ack.
   */
  commit_buf: MessageView | null;
  commit_blocks: Block[];
}

/** The empty view a fresh store carries at `commit_buf`, before any commit. */
export const EMPTY_COMMIT_VIEW: MessageView = Object.freeze({
  id: "commit",
  is_user: false,
  is_asst: false,
  is_tool: false,
  is_notice: false,
  blocks: [],
  card: NO_CARD,
}) as MessageView;

/** A token count in k = 1024 tokens: 32768 reads "32k", 9800 reads "9.6k". */
export function tokens(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1024;
  return `${k >= 10 ? Math.round(k) : k.toFixed(1)}k`;
}

export function contextLabel(used: number, size: number): string {
  return `${tokens(used)}/${tokens(size)}`;
}

export function contextPct(used: number, size: number): number {
  return Math.max(0, Math.min(100, Math.round((used / size) * 100)));
}

/** The ctx threshold flags the header styles by: warn at >=75%, danger at >=90%. */
export function ctxFlags(pct: number): { warn: boolean; danger: boolean } {
  return { warn: pct >= 75, danger: pct >= 90 };
}

/**
 * What the header's ctx meter shows, turn by turn.
 *
 * The rule: within a turn the display is the LARGEST input+output any `done`
 * event of that turn has reported (a multi-step turn's later steps resend
 * the whole history, so a smaller mid-turn value is a step artifact, not
 * freed context). The max is scoped to the turn: when the turn finishes,
 * its LAST observed value becomes the baseline the next turn starts from,
 * so a legitimately smaller conversation (after a resume, say) is not
 * shown the old turn's max forever. A turn with no `done` event leaves the
 * baseline alone.
 */
export class CtxMeter {
  private base = 0;
  private max = 0;
  private last: number | undefined;

  beginTurn(): void {
    this.max = this.base;
    this.last = undefined;
  }

  /** One usage-bearing event; returns what the header should show now. */
  observe(used: number): number {
    this.last = used;
    if (used > this.max) this.max = used;
    return this.max;
  }

  /** The turn is over; returns the baseline the header rests at. */
  finishTurn(): number {
    if (this.last !== undefined) this.base = this.last;
    this.max = this.base;
    return this.base;
  }
}

export function providerLabel(provider: Pick<Provider, "type">): string {
  return provider.type === "ollama_remote"
    ? "Chalupa / remote"
    : "Ollama / loopback";
}

export function noApproval(): ApprovalState {
  return {
    open: false,
    title: "",
    body: "",
    danger: false,
    no: "Deny",
    yes: "Allow",
    truncated: false,
    lines: [],
  };
}

export interface StoreInit {
  provider: Pick<Provider, "type" | "model" | "context_size">;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  mcpServers: number;
  transcript: TranscriptSnapshot;
  /** Workspace root (or basename); only its basename is shown. */
  workspace?: string;
  /**
   * Overrides the derived empty flag. The inline client passes its real
   * transcript length here: its snapshot always starts empty.
   */
  empty?: boolean;
  /** Inline presentation: its palette starts without the load-earlier row. */
  inline?: boolean;
}

/** The last path segment of a workspace root ("" for "/" or ""). */
export function workspaceBasename(root: string): string {
  return root.split("/").filter(Boolean).at(-1) ?? "";
}

export function initialStore(init: StoreInit): Store {
  const found = filterPalette("", init.inline);
  return {
    header: {
      provider: providerLabel(init.provider),
      model: clip(cleanText(init.provider.model), 40),
      mode: init.intent,
      session: init.sessionId ? `session ${init.sessionId}` : "temporary",
      workspace: init.workspace ? workspaceBasename(init.workspace) : "",
      ctx_label: contextLabel(0, init.provider.context_size),
      ctx_pct: 0,
      ctx_warn: false,
      ctx_danger: false,
    },
    empty:
      init.empty ??
      (init.transcript.messages.length === 0 &&
        init.transcript.earlier.n === 0),
    mouse_on: false,
    messages: init.transcript.messages,
    earlier: init.transcript.earlier,
    live: IDLE_LIVE,
    thinking: IDLE_THINKING,
    approval: noApproval(),
    palette: { open: false, query: "", cur: found[0]?.id ?? "" },
    found: { any: found.length > 0, items: found },
    slash: { open: false, rows: [] },
    help: { open: false, cmds: helpRows() },
    draft: "",
    placeholder: PLACEHOLDER,
    busy: false,
    status: idleStatus(init.mcpServers),
    commit_buf: EMPTY_COMMIT_VIEW,
    commit_blocks: [],
  };
}

// ---------------------------------------------------------------------- approval

/** Rows of the approval preview; past this the preview says it is truncated. */
export const APPROVAL_MAX_ROWS = 2000;
/** The dialog is `width: 76` (see ui/app.tui), or the whole screen when narrower. */
const DIALOG_WIDTH = 76;
/** Border (2) and padding (4) of the dialog, plus the border of the preview (2). */
const DIALOG_CHROME = 8;

/** Columns a preview row may use inside the dialog on a terminal `cols` wide. */
export function previewWidth(cols: number): number {
  return Math.max(20, Math.min(DIALOG_WIDTH, cols) - DIALOG_CHROME);
}

/** Border (2) and padding (2) of the in-flow approval box, plus the preview's border (2). */
const INLINE_CHROME = 6;

/** Columns a preview row may use in the inline approval on a terminal `cols` wide. */
export function previewWidthInline(cols: number): number {
  return Math.max(20, cols - INLINE_CHROME);
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

/**
 * Hard-wraps one line by display width. It keeps every character and every
 * space (CSS `wrap: wrap` would collapse repeated spaces, which corrupts a
 * diff). The continuation rows are marked with a leading arrow.
 */
export function wrapLine(text: string, width: number): string[] {
  if (Bun.stringWidth(text) <= width) return [text];
  const rows: string[] = [];
  let row = "";
  let used = 0;
  let limit = width;
  for (const { segment } of graphemes.segment(text)) {
    const w = Bun.stringWidth(segment);
    if (used + w > limit && row !== "") {
      rows.push(row);
      row = "↪ ";
      used = 2;
      limit = width;
    }
    row += segment;
    used += w;
  }
  rows.push(row);
  return rows;
}

export interface Preview {
  lines: DiffLine[];
  /** Logical lines of the text, and how many of them are fully shown. */
  total: number;
  shown: number;
  truncated: boolean;
}

/** The rows of a preview text: wrapped to `width`, capped at `maxRows` plus a marker row. */
export function previewRows(
  text: string,
  width: number,
  maxRows = APPROVAL_MAX_ROWS,
): Preview {
  const source = cleanText(text).split("\n");
  // A diff ends with a newline: that is not an empty row to scroll to.
  while (source.length > 1 && source[source.length - 1] === "") source.pop();
  const lines: DiffLine[] = [];
  let shown = 0;
  let truncated = false;
  outer: for (const line of source) {
    const add = line.startsWith("+") && !line.startsWith("+++");
    const del = line.startsWith("-") && !line.startsWith("---");
    for (const t of wrapLine(line, width)) {
      if (lines.length >= maxRows) {
        truncated = true;
        break outer;
      }
      lines.push({ n: lines.length, t, add, del, note: false });
    }
    shown++;
  }
  if (truncated)
    lines.push({
      n: lines.length,
      t: `… ${source.length - shown} more lines not shown`,
      add: false,
      del: false,
      note: true,
    });
  return { lines, total: source.length, shown, truncated };
}

function prettyJson(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/** The full text the dialog previews: nothing the user approves is left out of it. */
export function approvalText(proposal: ApprovalRequest, root?: string): string {
  if (proposal.kind === "mcp")
    return `${proposal.name}\n${prettyJson(proposal.argumentsJson)}\n\nMCP effects are not verified by transport success.`;
  return approvalPreview(proposal, root);
}

export const TRUNCATED_BODY =
  "Preview truncated. Deny unless you have read the whole change.";

/** What the approval dialog shows for a runtime proposal, wrapped for `cols` columns. */
export function approvalState(
  proposal: ApprovalRequest,
  cols = 80,
  root?: string,
  inline = false,
): ApprovalState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  const preview = previewRows(approvalText(proposal, root), width);
  const base = {
    ...noApproval(),
    open: true,
    lines: preview.lines,
    truncated: preview.truncated,
  };
  const body = (text: string) => (preview.truncated ? TRUNCATED_BODY : text);
  switch (proposal.kind) {
    case "edit":
      return {
        ...base,
        title: clip(`Edit ${cleanText(proposal.path)}?`, 70),
        body: body("Apply this change to the workspace."),
        danger: preview.truncated,
      };
    case "write":
      return {
        ...base,
        title: clip(
          `${proposal.created ? "Create" : "Replace"} ${cleanText(proposal.path)}?`,
          70,
        ),
        body: body(
          proposal.created
            ? "Create this file in the workspace."
            : "Replace this file in the workspace.",
        ),
        danger: preview.truncated,
      };
    case "mcp":
      return {
        ...base,
        title: clip(`Call ${cleanText(proposal.name)}?`, 70),
        body: body("Call this MCP tool with the arguments below."),
        danger: true,
      };
    default:
      return {
        ...base,
        title: "Run command?",
        body: body("Runs exactly as written: there is no shell."),
        danger: true,
      };
  }
}
