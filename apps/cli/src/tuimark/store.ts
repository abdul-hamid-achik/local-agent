// The Tuimark store: every path ui/app.tui reads, with its shape. One place, so
// the controller, the samples the goldens are made from, and the tests cannot
// drift from the markup.

import {
  type ApprovalRequest,
  COMMAND_WRAPPERS,
  namesSecretPath,
  type Provider,
  type ProviderProbe,
  placementWarning,
  providerLocality,
  type Question,
  secretPathNotes,
} from "@local-agent/runtime";
import { approvalPreview } from "#cli/preview";
import {
  type Block,
  cleanText,
  clip,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  type MessageView,
  mcpParts,
  NO_CARD,
  type ThinkingView,
  type ToolReceipt,
} from "#cli/tuimark/blocks";
import { selectionHint } from "#cli/tuimark/clipboard";

export { mcpParts };

import {
  filterPalette,
  type HelpRow,
  helpRows,
  type Intent,
  type PaletteItem,
  type SlashRow,
} from "#cli/tuimark/commands";
import type { Earlier, TranscriptSnapshot } from "#cli/tuimark/transcript";

/** MCP servers that are still connecting, or that failed to (count only). */
export interface McpStartupState {
  connecting?: boolean;
  failed?: number;
}

/** The last finished turn, as the ambient status keeps it. */
export interface LastTurn {
  seconds: number;
  /** "32 tok/s", when the provider reported its timings. */
  speed?: string;
}

/**
 * The ambient status line, the same at rest and while a turn runs: the
 * mode, the MCP state (the count, that the servers are still connecting,
 * how many failed) and, once a turn has finished, how long it took and how
 * fast it went. What a turn is doing now is the live message's head, not
 * this line. Transient messages (a command's answer, an error, the keys of
 * an open approval) take the line over and give it back at the next state
 * change.
 */
export function idleStatus(
  mcpServers: number,
  intent?: Intent,
  mcp: McpStartupState = {},
  last?: LastTurn,
): string {
  const failed = mcp.failed ?? 0;
  const parts: string[] = [];
  if (intent) parts.push(intent);
  if (mcp.connecting) parts.push("MCP connecting…");
  else if (mcpServers || failed)
    parts.push(
      `MCP ${mcpServers}${failed ? ` · ${failed} failed (/mcp)` : ""}`,
    );
  if (last) {
    parts.push(
      `last ${last.seconds < 1 ? "<1s" : formatElapsed(last.seconds)}`,
    );
    if (last.speed) parts.push(last.speed);
  }
  return parts.join(" · ");
}
/**
 * What the welcome and the help say about selecting and copying text. With
 * mouse reporting on, the terminal selects natively only with a modifier
 * (shift-drag, option-drag in iTerm2, fn-drag in Terminal.app); with it off,
 * or inline, a plain drag does.
 */
export function selectTips(
  mouse: boolean,
  termProgram?: string,
): { tip: string; help: { select_key: string; select_desc: string } } {
  const hint = selectionHint(mouse, termProgram);
  return {
    tip: `${hint} selects text`,
    help: {
      select_key: hint,
      select_desc: mouse
        ? "select text (mouse reporting is on)"
        : "select text",
    },
  };
}

/** The composer's hint at rest. */
export const PLACEHOLDER = "Message the model";
/** While a turn runs a send is queued (one message, run when the turn ends). */
export const PLACEHOLDER_BUSY = "Type to queue a message";
/** While an approval waits, its keys are the dialog's: nothing is queued. */
export const APPROVAL_PLACEHOLDER = "Answer the approval first";
/** While the dialog is not armed yet: only deny works. */
export const APPROVAL_WAIT_STATUS = "Waiting for approval · n deny · esc deny";
/**
 * Once the dialog accepts `y` (and `a`, the session grant, `!` the rule).
 * The decision comes first, so a narrow status row that cuts the grants
 * still says how to deny. armedStatus leaves out each grant a card does not
 * offer.
 */
export const APPROVAL_STATUS =
  "Waiting for approval · y allow · n deny · a session · ! always · esc deny";
/** The body says to deny an unread change, so the status leads with deny. */
export const APPROVAL_TRUNCATED_STATUS =
  "Preview truncated · n deny · y allow · esc deny";
/** A `y` swallowed by the arm delay says so instead of going silent. */
export const APPROVAL_UNARMED_STATUS = "Approval not armed yet — press y again";
/** An `a` swallowed by the arm delay, same rule as `y`. */
export const APPROVAL_UNARMED_SESSION_STATUS =
  "Approval not armed yet — press a again";
/** A `!` swallowed by the arm delay, same rule as `y` and `a`. */
export const APPROVAL_UNARMED_ALWAYS_STATUS =
  "Approval not armed yet — press ! again";
/** An `a` on a truncated preview: no grant is taken from an unread change. */
export const APPROVAL_TRUNCATED_SESSION_STATUS =
  "Preview truncated: session grant unavailable";
/** An `a` on a command card with the sandbox on: a card lets out only its own command. */
export const APPROVAL_SANDBOX_SESSION_STATUS =
  "Sandbox on: no session grant for commands; y runs this one outside the sandbox";
/** An `a` or `!` on a command that names a secret path: only its own card lets it out. */
export const APPROVAL_SECRET_STATUS =
  "Names a secret path: no session grant or always rule for this command; y runs this one";
/** The same, for a command with too many path words to check each for a link to a secret. */
export const APPROVAL_UNCHECKED_STATUS =
  "Too many paths to check for a secret: no session grant or always rule for this command; y runs this one";
/** A `!` on a truncated preview: no persistent rule comes from an unread change. */
export const APPROVAL_TRUNCATED_ALWAYS_STATUS =
  "Preview truncated: always rule unavailable";

export interface DiffLine {
  n: number;
  t: string;
  add: boolean;
  del: boolean;
  /** A row the client wrote (the "more lines" marker), not part of the preview text. */
  note: boolean;
}

/** `/details` with no receipt yet. */
export const NO_DETAILS_STATUS =
  "No tool output yet. Run something first, then /details";

export interface DetailsState {
  open: boolean;
  /** "<tool> · <status>", as the receipt's card shows them. */
  title: string;
  /** The card's args line ("" when the call had none to summarize). */
  args: string;
  /** The whole output, hard-wrapped like an approval preview. */
  lines: DiffLine[];
}

/** One row of the `/sessions` picker: a saved session and the selection flag. */
export interface SessionRow {
  /** The session's public handle, also the row key. */
  id: string;
  /** "<handle> · N messages", plus " · current" on the live one. */
  label: string;
  sel: boolean;
  /** "›" on the chosen row, "" on the rest (selection must read as text). */
  mark: string;
}

/**
 * The read-only panel (`/status`, `/mcp`, `/sessions`): the details
 * viewer's shape (title, a header line, wrapped lines) plus, for the
 * sessions picker, display rows the parent moves a choice over.
 */
export interface PanelState {
  open: boolean;
  title: string;
  /** A one-line summary under the title (the `/mcp` count line), "" for none. */
  args: string;
  /** The panel's text, hard-wrapped like a preview. Empty while picking. */
  lines: DiffLine[];
  /** The picker's rows. Empty unless `picking`. */
  rows: SessionRow[];
  /** True for `/sessions`: enter loads the chosen row. */
  picking: boolean;
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
  /**
   * What `a` would grant for the rest of the session, spelled out (or why
   * no grant is offered, when the preview is truncated). "" when closed.
   */
  grant_hint: string;
  /**
   * `a` would take a session grant (see grantsOffered). The key hint and
   * the armed status name `a` only then; the key still reaches the client,
   * which says why it refuses.
   */
  session_ok: boolean;
  /** `!` would save an always rule: the same, for `!`. */
  always_ok: boolean;
  lines: DiffLine[];
}

/** Elapsed seconds as the head shows them: `12s`, then `1m 05s`. */
export function formatElapsed(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

/**
 * What the running turn is doing, as the head of the assistant's message
 * says it: waiting for the model (or for the MCP servers it needs), the
 * model reasoning, the reply streaming (the elapsed alone: the text is the
 * indication), or the turn waiting on the person (an approval, an answer).
 */
export type HeadPhase =
  | "waiting"
  | "mcp"
  | "thinking"
  | "streaming"
  | "approval"
  | "answer";

const HEAD_PHASE_TEXT: Record<HeadPhase, string> = {
  waiting: "waiting for the model",
  mcp: "connecting MCP servers",
  thinking: "thinking",
  streaming: "",
  approval: "waiting for your approval",
  answer: "waiting for your answer",
};

/** Stands where the spinner was while the turn waits on the person. */
export const HEAD_STILL_GLYPH = "⠿";

/**
 * The head of the assistant's live message, from the moment a turn starts.
 * It lives in its own store path, not in `live` (the stream rewrites `live`
 * whole), and is never part of a printed or history turn: those are
 * rendered from la-turn, which has no spinner. The engine animates the
 * spinner (`<progress indeterminate>`); the text changes once a second.
 */
export interface HeadView {
  /** The head shows: a turn runs, and no tool card stands in for it. */
  on: boolean;
  /** The spinner turns: the turn is working. */
  spin: boolean;
  /** The static glyph shows instead: the turn waits on the person. */
  still: boolean;
  glyph: string;
  /** "<phase> · <elapsed>", dim; the elapsed alone while the reply streams. */
  text: string;
}

export const NO_HEAD: HeadView = Object.freeze({
  on: false,
  spin: false,
  still: false,
  glyph: HEAD_STILL_GLYPH,
  text: "",
}) as HeadView;

/** The head for `phase` after `seconds` of the turn's own time. */
export function headState(phase: HeadPhase, seconds: number): HeadView {
  const waiting = phase === "approval" || phase === "answer";
  return {
    on: true,
    spin: !waiting,
    still: waiting,
    glyph: HEAD_STILL_GLYPH,
    text: [HEAD_PHASE_TEXT[phase], formatElapsed(seconds)]
      .filter(Boolean)
      .join(" · "),
  };
}

export interface Store {
  header: {
    provider: string;
    /**
     * Where the prompt goes, in the word the appbar keeps at every width
     * (the provider label goes first): "LOCAL" or "REMOTE".
     */
    locality: "LOCAL" | "REMOTE";
    /** The locality is REMOTE: it stands out. */
    remote: boolean;
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
    /** Active session grants as one compact label ("auto: files · bun test"), "" for none. */
    grants: string;
    /** Whether any session grant is active (the header shows `grants` only then). */
    grants_on: boolean;
    /** Commands no rule decides run in the sandbox, without a card (`/sandbox`). */
    sandbox_on: boolean;
    /** The running goal's turn of its most ("goal 3/8"), shown only while it runs. */
    goal: string;
    goal_on: boolean;
  };
  /** True only before the first message: the welcome shows. Never true again. */
  empty: boolean;
  /** Fullscreen mouse reporting (`<tui mouse="mouse_on">`). On by default in
   *  fullscreen — wheel scrolls the transcript under the pointer; inline has
   *  no mouse (the terminal's own scrollback is the history). */
  mouse_on: boolean;
  /**
   * The welcome's line on selecting text natively: a modifier-drag while mouse
   * reporting is on (see selectionHint), a plain drag otherwise.
   */
  select_tip: string;
  messages: MessageView[];
  earlier: Earlier;
  live: LiveView;
  /** The model's reasoning while it streams; gone once the answer starts. */
  thinking: ThinkingView;
  /**
   * The head of the assistant's message while a turn runs: the one place the
   * UI says what the turn is doing (see HeadView).
   */
  head: HeadView;
  approval: ApprovalState;
  /** An open ask_user question (see QuestionState). */
  question: QuestionState;
  /**
   * The `/details` viewer over the latest tool receipt: a modal fullscreen,
   * an in-flow block inline (region state, never committed).
   */
  details: DetailsState;
  /**
   * The read-only panel over `/status`, `/mcp` and the `/sessions`
   * picker: a modal fullscreen, an in-flow block inline, like `details`.
   */
  panel: PanelState;
  palette: { open: boolean; query: string; cur: string };
  found: { any: boolean; items: PaletteItem[] };
  /**
   * The `/` popup over the composer: display-only rows (nothing in an
   * `each` can take focus), the chosen one carrying `sel`. The controller
   * rewrites it as the draft is typed; inline it lives in the region and
   * is never committed.
   */
  slash: { open: boolean; rows: SlashRow[] };
  /**
   * The help overlay: `cmds` is the slash catalog, derived once at build;
   * `select_key` and `select_desc` are the row that says how to select text
   * natively (it follows the mouse toggle).
   */
  help: {
    open: boolean;
    cmds: HelpRow[];
    select_key: string;
    select_desc: string;
  };
  draft: string;
  placeholder: string;
  /**
   * A turn runs, from its send until it settles: esc cancels it only then,
   * so its hint shows only then (the document keys the row on it).
   */
  running: boolean;
  /** The status bar: ambient facts, or a transient message (see idleStatus). */
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
  is_error: false,
  blocks: [],
  card: NO_CARD,
  links: [],
  images: [],
  images_more: 0,
}) as MessageView;

/** A token count in k = 1024 tokens: 32768 reads "32k", 9800 reads "9.6k". */
/** The marker under a cut preview: lines not shown whole. */
function moreLines(n: number): string {
  return `${n} more line${n === 1 ? "" : "s"} not shown`;
}

export function tokens(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1024;
  // 9.96k rounds to "10.0k" at one decimal: from there it is whole k.
  return `${k >= 9.95 ? Math.round(k) : k.toFixed(1)}k`;
}

/** The transcript note for a compacted history (the runtime's estimates). */
export function compactedNotice(
  event: { elided: number; tokensBefore: number; tokensAfter: number },
  size: number,
): string {
  return `Context compacted: ${event.elided} earlier message${event.elided === 1 ? "" : "s"} shortened, about ${tokens(event.tokensBefore)} → ${tokens(event.tokensAfter)} of ${tokens(size)} tokens. The model can run those tools again.`;
}

/** The notice when requests start leaving more of the oldest messages out. */
export function windowedNotice(event: { left: number; sent: number }): string {
  return `Context window full: the model now sees the newest ${event.sent} messages. The oldest ${event.left} stay in the session but are not sent; /sessions new starts over.`;
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

  /** What the header shows right now (the turn's max, or the resting baseline). */
  get current(): number {
    return this.max;
  }

  /** A different conversation starts from zero (a session switch). */
  reset(): void {
    this.base = 0;
    this.max = 0;
    this.last = undefined;
  }
}

export function providerLabel(
  provider: Pick<Provider, "type" | "base_url">,
): string {
  return provider.type === "ollama_remote"
    ? "Chalupa / remote"
    : provider.type === "openai_local"
      ? `OpenAI-compatible / ${providerLocality(provider)}`
      : "Ollama / loopback";
}

export function noDetails(): DetailsState {
  return { open: false, title: "", args: "", lines: [] };
}

export function noPanel(): PanelState {
  return {
    open: false,
    title: "",
    args: "",
    lines: [],
    rows: [],
    picking: false,
  };
}

/** One choice of an open ask_user question. */
export interface QuestionRow {
  /** The choice's id, also the row key ("yes"/"no" for a yes/no question). */
  id: string;
  /** "1 Postgres": its number (what typing it picks), then its label. */
  label: string;
  /** The model's one-line detail, "" for none. */
  detail: string;
  sel: boolean;
  /** "›" on the chosen row, "" on the rest (selection must read as text). */
  mark: string;
}

/**
 * An open ask_user question, in flow above the composer in both clients.
 * The composer answers it: enter takes the chosen row, or sends the typed
 * answer; its arrows (at the draft's edges) move the choice.
 */
export interface QuestionState {
  open: boolean;
  title: string;
  /** The question, without control characters; it wraps. */
  text: string;
  /** The choices (yes/no included); empty for a typed answer. */
  rows: QuestionRow[];
  /** True when a row is the answer; false for a typed one. */
  choosing: boolean;
  /** The keys, spelled out under the question. */
  hint: string;
}

export function noQuestion(): QuestionState {
  return {
    open: false,
    title: "",
    text: "",
    rows: [],
    choosing: false,
    hint: "",
  };
}

/** The rows a question offers: yes/no as two, a single choice its options. */
export function questionChoices(
  question: Question,
): readonly Readonly<{ id: string; label: string; detail?: string | null }>[] {
  if (question.kind === "yes_no")
    return [
      { id: "yes", label: "Yes" },
      { id: "no", label: "No" },
    ];
  return question.kind === "single" ? question.options : [];
}

/** The ask_user card while its question is open (the question box is below the log). */
export const QUESTION_CARD_STATUS = "asked, answer below";
/** While the question is not armed yet: only esc works. */
export const QUESTION_WAIT_STATUS = "Question for you · esc skip";
/** Once enter answers it. The box under the log names its keys, esc skip among them. */
export const QUESTION_STATUS = "Waiting for your answer";
/** The composer's placeholder while it answers a question. */
export const QUESTION_PLACEHOLDER_CHOICE = "enter takes the chosen row";
export const QUESTION_PLACEHOLDER_TEXT = "Type your answer";
/** An enter swallowed by the arm delay, as an approval's `y` is. */
export const QUESTION_UNARMED_STATUS =
  "The question just opened — press enter again";
export const QUESTION_EMPTY_STATUS =
  "Type an answer and press enter, or esc to skip";
export const QUESTION_NO_MATCH_STATUS =
  "Not one of the choices: type its number, or ↑/↓ and enter";
/** Enter on a single choice before any row is chosen. */
export const QUESTION_CHOOSE_STATUS =
  "Choose one first: ↑/↓, or type its number";

/** The question box for `question` with row `sel` chosen (-1: none yet). */
export function questionState(question: Question, sel: number): QuestionState {
  const rows = questionChoices(question).map((choice, index) => ({
    id: choice.id,
    label: `${index + 1} ${clip(cleanText(choice.label), 120)}`,
    detail: choice.detail?.trim()
      ? `· ${clip(cleanText(choice.detail).replace(/\s+/gu, " ").trim(), 200)}`
      : "",
    sel: index === sel,
    mark: index === sel ? "›" : "",
  }));
  const choosing = rows.length > 0;
  return {
    open: true,
    title:
      question.kind === "yes_no"
        ? "Question · yes or no"
        : choosing
          ? "Question · choose one"
          : "Question",
    text: clip(cleanText(question.question), 2048),
    rows,
    choosing,
    // esc skip before the extras: a narrow box clips the end of the line.
    hint:
      question.kind === "yes_no"
        ? "↑/↓ choose · enter answer · esc skip · or type y or n"
        : choosing
          ? "↑/↓ choose · enter answer · esc skip · or type its number"
          : "type the answer · enter send · esc skip · shift+enter new line",
  };
}

/**
 * What a draft sent under an open choice picks: its number, its id or
 * label (any case), y/yes/n/no for a yes/no question, or (empty) the
 * chosen row. Undefined when it names none of them, or is empty with no
 * row chosen.
 */
export function questionPick(
  question: Question,
  sel: number,
  draft: string,
): string | undefined {
  const choices = questionChoices(question);
  const typed = draft.trim().toLowerCase();
  if (!typed) return choices[sel]?.id;
  if (question.kind === "yes_no") {
    if (typed === "y" || typed === "yes") return "yes";
    if (typed === "n" || typed === "no") return "no";
  }
  const number = /^\d+$/u.test(typed) ? Number(typed) : 0;
  if (number >= 1 && number <= choices.length) return choices[number - 1]?.id;
  return choices.find(
    (choice) =>
      choice.id.toLowerCase() === typed ||
      choice.label.trim().toLowerCase() === typed,
  )?.id;
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
    grant_hint: "",
    session_ok: false,
    always_ok: false,
    lines: [],
  };
}

export interface StoreInit {
  provider: Pick<Provider, "type" | "base_url" | "model" | "context_size">;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  mcpServers: number;
  /** Servers are still connecting: the status says so until they settle. */
  mcpConnecting?: boolean;
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
  /** Commands start sandboxed (`--sandbox-commands`, when available). */
  sandbox?: boolean;
  /** `$TERM_PROGRAM`, for the native-selection hint (a modifier differs by terminal). */
  termProgram?: string;
}

/** The last path segment of a workspace root ("" for "/" or ""). */
export function workspaceBasename(root: string): string {
  return root.split("/").filter(Boolean).at(-1) ?? "";
}

export function initialStore(init: StoreInit): Store {
  const found = filterPalette("", init.inline);
  const remote = providerLocality(init.provider) === "remote";
  return {
    header: {
      provider: providerLabel(init.provider),
      locality: remote ? "REMOTE" : "LOCAL",
      remote,
      model: clip(cleanText(init.provider.model), 40),
      mode: init.intent,
      session: init.sessionId ? `session ${init.sessionId}` : "temporary",
      workspace: init.workspace ? workspaceBasename(init.workspace) : "",
      ctx_label: contextLabel(0, init.provider.context_size),
      ctx_pct: 0,
      ctx_warn: false,
      ctx_danger: false,
      grants: "",
      grants_on: false,
      sandbox_on: init.sandbox ?? false,
      goal: "",
      goal_on: false,
    },
    empty:
      init.empty ??
      (init.transcript.messages.length === 0 &&
        init.transcript.earlier.n === 0),
    mouse_on: !init.inline,
    select_tip: selectTips(!init.inline, init.termProgram).tip,
    messages: init.transcript.messages,
    earlier: init.transcript.earlier,
    live: IDLE_LIVE,
    thinking: IDLE_THINKING,
    head: NO_HEAD,
    approval: noApproval(),
    question: noQuestion(),
    details: noDetails(),
    panel: noPanel(),
    palette: { open: false, query: "", cur: found[0]?.id ?? "" },
    found: { any: found.length > 0, items: found },
    slash: { open: false, rows: [] },
    help: {
      open: false,
      cmds: helpRows(),
      ...selectTips(!init.inline, init.termProgram).help,
    },
    draft: "",
    placeholder: PLACEHOLDER,
    running: false,
    status: idleStatus(init.mcpServers, init.intent, {
      connecting: init.mcpConnecting,
    }),
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
 * Wraps one line by display width. It keeps every character and every space
 * (CSS `wrap: wrap` would collapse repeated spaces, which corrupts a diff),
 * so the rows joined back, arrows dropped, are the line. The continuation
 * rows are marked with a leading arrow. A row breaks after its last space
 * when that still fills half of it, so prose keeps its words whole, and
 * otherwise at the cell it fills (a long path or token splits anyway).
 */
export function wrapLine(text: string, width: number): string[] {
  if (Bun.stringWidth(text) <= width) return [text];
  const rows: string[] = [];
  // The row being filled, as graphemes and their widths; `start` is where its
  // text begins, after the arrow of a continuation row.
  let row: string[] = [];
  let widths: number[] = [];
  let start = 0;
  let used = 0;
  for (const { segment } of graphemes.segment(text)) {
    const w = Bun.stringWidth(segment);
    // A carried word can still overflow a very narrow row: break again.
    while (used + w > width && row.length > start) {
      const space = row.lastIndexOf(" ");
      const head = widths.slice(0, space + 1).reduce((a, b) => a + b, 0);
      const cut = space >= start && head * 2 >= width ? space + 1 : row.length;
      rows.push(row.slice(0, cut).join(""));
      row = ["↪ ", ...row.slice(cut)];
      widths = [2, ...widths.slice(cut)];
      start = 1;
      used = widths.reduce((a, b) => a + b, 0);
    }
    row.push(segment);
    widths.push(w);
    used += w;
  }
  rows.push(row.join(""));
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
    // A file header is `+++ b/path` or `--- a/path`; an added `++i;` is a
    // line like any other.
    const add = line.startsWith("+") && !line.startsWith("+++ ");
    const del = line.startsWith("-") && !line.startsWith("--- ");
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
      t: `… ${moreLines(source.length - shown)}`,
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
    return `${mcpCallName(proposal)}\n${prettyJson(proposal.argumentsJson)}\n\nMCP effects are not verified by transport success.`;
  return approvalPreview(proposal, root);
}

export const TRUNCATED_BODY =
  "Preview truncated. Deny unless you have read the whole change.";

// ------------------------------------------------------------ session grants
//
// `a` on an armed approval allows the call and remembers the grant for the
// rest of the session (controller memory only; nothing is persisted). The
// matching rules are deliberately coarse and spelled out in the dialog:
// edits and writes share one "file changes" grant, a command grants its
// program plus its first argument (`bun test`, `git status` cover further
// arguments), and an MCP call grants its exact SERVER__TOOL name.
//
// A prefix grant covers new positional arguments but not new options: an
// option the approved command did not carry (`bun test --preload x.ts`,
// `make build -f other.mk`, `git grep -O…`) asks again, and `a` on that
// card adds it to the grant.
//
// Two narrower shapes keep a grant from covering code nobody saw:
// - a runner subcommand of a package manager or toolchain (`bun run`,
//   `npm exec`, `docker run`, `go run`, `bun add`, …) grants three words,
//   so `bun run test …` never covers `bun run evil.ts` (`git add` is not a
//   runner: it keeps the two-word prefix);
// - a bare program, a command whose second word is an option (`git -c …`,
//   `bash -c …`, `node -e …`), a program that runs another program (`env`,
//   `xargs`, `timeout`, `ssh`, …) and the git subcommands that run code
//   from their arguments or write config that later runs (`git config`,
//   `git rebase`, `git bisect`, …) grant only that exact argv.

/** Per program, the second words that run code (or install a package)
 * named by the third. */
const RUNNER_SUBCOMMANDS: Record<string, ReadonlySet<string>> = {
  bun: new Set(["run", "x", "exec", "add", "install", "i"]),
  npm: new Set(["run", "run-script", "exec", "x", "install", "i", "add"]),
  pnpm: new Set(["run", "exec", "dlx", "add", "install", "i"]),
  yarn: new Set(["run", "exec", "dlx", "add"]),
  deno: new Set(["run", "x", "install", "add"]),
  docker: new Set(["run", "exec"]),
  podman: new Set(["run", "exec"]),
  kubectl: new Set(["run", "exec"]),
  go: new Set(["run", "install"]),
  cargo: new Set(["run", "install"]),
  uv: new Set(["run", "add"]),
  pip: new Set(["install"]),
  pip3: new Set(["install"]),
  pipx: new Set(["run", "install"]),
  poetry: new Set(["run", "add"]),
  gem: new Set(["install"]),
  brew: new Set(["install"]),
};

/** git subcommands that execute their arguments or set config that executes. */
const GIT_EXACT = new Set([
  "config",
  "submodule",
  "bisect",
  "rebase",
  "filter-branch",
  "filter-repo",
  "difftool",
  "mergetool",
  "daemon",
]);

/** A command grant's shape (see above): its prefix, and whether it is exact. */
function commandGrant(argv: readonly string[]): {
  prefix: string[];
  exact: boolean;
} {
  const program = (argv[0] ?? "").split("/").at(-1)?.toLowerCase() ?? "";
  const second = argv[1] ?? "";
  const runner =
    RUNNER_SUBCOMMANDS[program]?.has(second.toLowerCase()) ?? false;
  const exact =
    argv.length <= 1 ||
    COMMAND_WRAPPERS.has(program) ||
    second.startsWith("-") ||
    (program === "git" && GIT_EXACT.has(second.toLowerCase())) ||
    // A runner's third word is what it runs; an option there (`npm i -g`,
    // `pip install --index-url …`) would make the grant cover any target.
    (runner && (argv.length <= 2 || (argv[2] ?? "").startsWith("-")));
  if (exact) return { prefix: [...argv], exact };
  return { prefix: argv.slice(0, runner ? 3 : 2), exact };
}

/** The call as the user reads it: `SERVER__TOOL`, or `SERVER__TOOL via GATEWAY`. */
export function mcpCallName(
  proposal: Extract<ApprovalRequest, { kind: "mcp" }>,
): string {
  return proposal.inner && proposal.gateway
    ? `${proposal.inner} via ${proposal.gateway}`
    : proposal.name;
}

/** An argv word that is an option (`-x`, `--flag=value`, `--`), not `-` (stdin). */
const isOption = (word: string): boolean =>
  word.length > 1 && word.startsWith("-");

/** The grant a proposal falls under: its identity key and its short label. */
export function grantFor(proposal: ApprovalRequest): {
  key: string;
  label: string;
  /**
   * A command grant's argv prefix (program + first argument, or three
   * words after a runner subcommand). It matches as a prefix — the
   * dialog's "`bun test …`" — and nothing wider ever matches. An exact
   * grant's prefix is the whole argv, and it matches only itself.
   */
  prefix?: readonly string[];
  /** A prefix grant's option words after the prefix, as approved. */
  options?: readonly string[];
  exact?: boolean;
} {
  switch (proposal.kind) {
    case "edit":
    case "write":
      return { key: "files", label: "files" };
    case "command": {
      // Identity is the raw argv and cwd the runtime would execute;
      // cleaned text is only ever the label the user reads.
      const { prefix, exact } = commandGrant(proposal.argv);
      const label = prefix.map(cleanText).join(" ");
      return {
        key: `${exact ? "cmd=" : "cmd:"}${JSON.stringify(prefix)}@${proposal.cwd}`,
        label,
        prefix,
        ...(exact
          ? { exact }
          : { options: proposal.argv.slice(prefix.length).filter(isOption) }),
      };
    }
    case "mcp": {
      // Through a gateway the grant is the tool it runs, never call_tool.
      return {
        key: `mcp:${proposal.target ?? proposal.name}`,
        label: cleanText(proposal.inner ?? proposal.name),
      };
    }
  }
}

/**
 * Whether a remembered grant covers a proposal: exact for files and MCP,
 * an argv-prefix match for commands whose options were all approved
 * before (see `grantFor`).
 */
export function grantCovers(
  grant: {
    key: string;
    prefix?: readonly string[];
    options?: readonly string[];
  },
  proposal: ApprovalRequest,
): boolean {
  if (proposal.kind === "command") {
    // A grant with no program word covers nothing: `every` over an empty
    // prefix is vacuously true and would auto-allow every command.
    if (!grant.prefix || grant.prefix.length === 0 || grant.prefix[0] === "")
      return false;
    // The cwd is part of the grant's key: the same prefix elsewhere is
    // a different command.
    const encoded = `${JSON.stringify(grant.prefix)}@${proposal.cwd}`;
    if (grant.key === `cmd=${encoded}`)
      return (
        proposal.argv.length === grant.prefix.length &&
        grant.prefix.every((word, i) => proposal.argv[i] === word)
      );
    if (grant.key !== `cmd:${encoded}`) return false;
    const approved = new Set(grant.options ?? []);
    return (
      // Each prefix word must be there: `ls ""` does not cover a bare `ls`.
      grant.prefix.every(
        (word, i) => i < proposal.argv.length && proposal.argv[i] === word,
      ) &&
      proposal.argv
        .slice(grant.prefix.length)
        .every((word) => !isOption(word) || approved.has(word))
    );
  }
  return grantFor(proposal).key === grant.key;
}

/** What `a` would grant, as the dialog spells it out. */
export function grantHint(
  proposal: ApprovalRequest,
  truncated: boolean,
  sandboxOn = false,
): string {
  if (truncated) return "preview truncated: session grant unavailable";
  if (namesSecretPath(proposal))
    return proposal.kind === "command" &&
      secretPathNotes(proposal.secretPaths).paths.length === 0
      ? "too many paths to check: session grant and always rule unavailable"
      : "names a secret path: session grant and always rule unavailable";
  if (sandboxOn && proposal.kind === "command")
    return "sandbox on: session grant unavailable";
  if (proposal.kind === "edit" || proposal.kind === "write")
    return "a allow file changes this session";
  const { label, exact } = grantFor(proposal);
  if (proposal.kind !== "command" || exact)
    return `a allow \`${label}\` this session`;
  return `a allow \`${label} …\` this session`;
}

/**
 * Which grants the dialog's keys would take, as the card alone decides:
 * neither from a preview nobody could read whole or from a command that
 * names a secret path, and no session grant for a command while the
 * sandbox is on. (`!` also checks the rules file when pressed: a deny rule
 * written while the card is open still keeps it from saving.)
 */
export function grantsOffered(
  proposal: ApprovalRequest,
  truncated: boolean,
  sandboxOn = false,
): { session: boolean; always: boolean } {
  const unread = truncated || namesSecretPath(proposal);
  return {
    session: !unread && !(sandboxOn && proposal.kind === "command"),
    always: !unread,
  };
}

/** The status once the dialog is armed: it names only the keys that act. */
export function armedStatus(
  approval: Pick<ApprovalState, "truncated" | "session_ok" | "always_ok">,
): string {
  if (approval.truncated) return APPROVAL_TRUNCATED_STATUS;
  const keys = [
    "y allow",
    "n deny",
    ...(approval.session_ok ? ["a session"] : []),
    ...(approval.always_ok ? ["! always"] : []),
    "esc deny",
  ];
  return `Waiting for approval · ${keys.join(" · ")}`;
}

/** The card's sentences for a command's secretPaths (see secretPathNotes). */
function secretSentences(secretPaths: readonly string[] | undefined): string {
  const { paths, unchecked } = secretPathNotes(secretPaths);
  return `${
    paths.length
      ? ` It names a path the workspace treats as secret: ${paths.map(cleanText).join(", ")}.`
      : ""
  }${
    unchecked === undefined
      ? ""
      : ` It names ${unchecked} path words, too many to check each for a link to a secret.`
  }`;
}

/** The status for an `a` or `!` that namesSecretPath refuses. */
export function secretStatus(proposal: ApprovalRequest): string {
  return proposal.kind === "command" &&
    secretPathNotes(proposal.secretPaths).paths.length === 0
    ? APPROVAL_UNCHECKED_STATUS
    : APPROVAL_SECRET_STATUS;
}

/** The header's compact grants label: "auto: files · bun test", "" for none. */
export function grantsLabel(labels: readonly string[]): string {
  return labels.length === 0 ? "" : `auto: ${labels.join(" · ")}`;
}

/**
 * What the approval dialog shows for a runtime proposal, wrapped for `cols`
 * columns. `sandboxOn`: a command's card says it runs outside the sandbox.
 */
export function approvalState(
  proposal: ApprovalRequest,
  cols = 80,
  root?: string,
  inline = false,
  sandboxOn = false,
): ApprovalState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  const preview = previewRows(approvalText(proposal, root), width);
  const offered = grantsOffered(proposal, preview.truncated, sandboxOn);
  const base = {
    ...noApproval(),
    open: true,
    lines: preview.lines,
    truncated: preview.truncated,
    grant_hint: grantHint(proposal, preview.truncated, sandboxOn),
    session_ok: offered.session,
    always_ok: offered.always,
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
    case "mcp": {
      // The dialog names the server, not only the joined SERVER__TOOL name:
      // which process would run this is the decision the user is making.
      // Through a gateway, that is the downstream server, not the gateway.
      const parts = mcpParts(proposal.inner ?? proposal.name);
      const server = parts ? cleanText(parts.server) : undefined;
      const tool = cleanText(parts ? parts.tool : proposal.name);
      const gateway = proposal.gateway
        ? cleanText(proposal.gateway)
        : undefined;
      return {
        ...base,
        title: clip(
          server ? `Call ${tool} from server ${server}?` : `Call ${tool}?`,
          70,
        ),
        body: body(
          server && gateway
            ? `MCP tool ${tool} from server ${server}, run by the gateway ${gateway}. Call it with the arguments below.`
            : server
              ? `MCP tool ${tool} from server ${server}. Call it with the arguments below.`
              : "Call this MCP tool with the arguments below.",
        ),
        danger: true,
      };
    }
    default:
      return {
        ...base,
        title: "Run command?",
        // With the sandbox on, the card is the one way out of it, and what
        // it runs may be what a sandboxed command wrote.
        body: body(
          `${
            sandboxOn
              ? "Runs outside the sandbox, as you, exactly as written (no shell). It can run files that sandboxed commands changed."
              : "Runs exactly as written: there is no shell."
          }${
            // Said here too: the preview below can be long.
            secretSentences(proposal.secretPaths)
          }`,
        ),
        danger: true,
      };
  }
}

// --------------------------------------------------------------------- details

/** Rows of the details viewer; past this it says how many lines it left out. */
export const DETAILS_MAX_ROWS = APPROVAL_MAX_ROWS;

/**
 * The viewer's rows: the output wrapped with the approval preview's
 * machinery (every character kept, `↪` continuations), capped like it.
 * No add/delete coloring: tool output is not a diff.
 */
export function detailsRows(text: string, width: number): DiffLine[] {
  const source = cleanText(text).split("\n");
  while (source.length > 1 && source[source.length - 1] === "") source.pop();
  if (source.length === 1 && source[0] === "")
    return [{ n: 0, t: "(no output)", add: false, del: false, note: true }];
  const lines: DiffLine[] = [];
  let shown = 0;
  let truncated = false;
  outer: for (const line of source) {
    for (const t of wrapLine(line, width)) {
      if (lines.length >= DETAILS_MAX_ROWS) {
        truncated = true;
        break outer;
      }
      lines.push({ n: lines.length, t, add: false, del: false, note: false });
    }
    shown++;
  }
  if (truncated)
    lines.push({
      n: lines.length,
      t: `… ${moreLines(source.length - shown)}`,
      add: false,
      del: false,
      note: true,
    });
  return lines;
}

/**
 * What the viewer shows for a receipt, wrapped for `cols` columns: at the
 * approval dialog's width fullscreen, the full-width box's inline.
 */
export function detailsState(
  receipt: ToolReceipt,
  cols = 80,
  inline = false,
): DetailsState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  return {
    open: true,
    title: clip(`${receipt.name} · ${receipt.status}`, 70),
    args: receipt.args,
    lines: detailsRows(receipt.output, width),
  };
}

// ----------------------------------------------------------------------- panel

/** A text panel (`/status`, `/mcp`), wrapped like the details viewer. */
export function panelState(
  title: string,
  args: string,
  text: string,
  cols = 80,
  inline = false,
): PanelState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  return {
    open: true,
    title: clip(cleanText(title), 70),
    args: clip(cleanText(args), 160),
    lines: detailsRows(text, width),
    rows: [],
    picking: false,
  };
}

/** One saved session as the picker lists it. */
export interface SessionSummary {
  handle: string;
  messages: number;
}

/** Picker rows for `sessions`, the row at `selected` marked; `current` is the live handle. */
export function sessionRows(
  sessions: readonly SessionSummary[],
  selected: number,
  current?: string,
): SessionRow[] {
  return sessions.map((session, index) => ({
    id: session.handle,
    label: cleanText(
      `${session.handle} · ${session.messages} messages${session.handle === current ? " · current" : ""}`,
    ),
    sel: index === selected,
    mark: index === selected ? "›" : "",
  }));
}

/** The `/status` panel's key/value facts, exactly those the client holds. */
export interface StatusFacts {
  provider: Pick<
    Provider,
    | "type"
    | "model"
    | "context_size"
    | "context_plan"
    | "context_warning"
    | "base_url"
    | "think"
  >;
  /** The workspace root, shown whole. */
  workspace: string;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  /** The session database path, when the session's store can name it. */
  sessionDb?: string;
  theme: string;
  /** "on" / "off", or "n/a (inline)" where the engine has no mouse. */
  mouse: string;
  /** Session-grant labels, in grant order. */
  grants: readonly string[];
  /** "on" or "off", with why it is unavailable when it is. */
  sandbox: string;
  /** Tokens the header's meter shows used. */
  usedTokens: number;
  /** Chalupa's environment name, when Chalupa launched this process. */
  environment?: string;
  mcpServers: number;
  mcpTools: number;
}

/**
 * The context warning as a line, unless the plan already says it (a size
 * the user set that does not fit carries its warning in its reason).
 */
function contextWarning(
  provider: Pick<Provider, "context_plan" | "context_warning">,
): string[] {
  const warning = provider.context_warning;
  return warning && !provider.context_plan?.includes(warning)
    ? [`Warning: ${cleanText(warning)}`]
    : [];
}

/** The `/status` lines: `Key: value`, one fact per line, nothing invented. */
export function statusLines(facts: StatusFacts): string[] {
  const lines = [
    `Model: ${cleanText(facts.provider.model)}`,
    `Provider: ${providerLabel(facts.provider)}`,
    `Endpoint: ${cleanText(facts.provider.base_url)}`,
    ...(facts.environment
      ? [`Chalupa environment: ${cleanText(facts.environment)}`]
      : []),
    `Think: ${facts.provider.think === undefined ? "model default" : facts.provider.think ? "on" : "off"}`,
    `Context: ${contextLabel(facts.usedTokens, facts.provider.context_size)}`,
  ];
  if (facts.provider.context_plan)
    lines.push(`Context size: ${cleanText(facts.provider.context_plan)}`);
  lines.push(...contextWarning(facts.provider));
  lines.push(
    `Workspace: ${cleanText(facts.workspace)}`,
    `Mode: ${facts.intent}`,
    `Session: ${facts.sessionId ?? "temporary"}`,
  );
  if (facts.sessionDb) lines.push(`Session db: ${cleanText(facts.sessionDb)}`);
  lines.push(
    `Theme: ${facts.theme}`,
    `Mouse: ${facts.mouse}`,
    `Grants: ${facts.grants.length ? grantsLabel(facts.grants).slice("auto: ".length) : "none"}`,
    `Sandbox: ${cleanText(facts.sandbox)}`,
    `MCP: ${facts.mcpServers} servers · ${facts.mcpTools} tools`,
  );
  return lines;
}

/** While `/provider` waits for the endpoint (5 s at most). */
export const PROVIDER_CHECKING_STATUS = "Checking the provider…";
/** `/provider`'s status line when the endpoint did not answer (the panel says why). */
export const PROVIDER_NO_ANSWER_STATUS = "Provider: no answer";

/** The `/provider` panel's facts: the profile (never a key) and one look at its endpoint. */
export interface ProviderFacts {
  provider: Pick<
    Provider,
    | "type"
    | "model"
    | "base_url"
    | "context_size"
    | "context_plan"
    | "context_warning"
  >;
  /** Chalupa's environment name (CHALUPA_ENVIRONMENT), when Chalupa launched this process. */
  environment?: string;
  probe: ProviderProbe;
}

const gib = (bytes: number) => `${(bytes / 1024 ** 3).toFixed(1)} GiB`;

/**
 * The `/provider` lines: what the endpoint answered just now, nothing
 * inferred. An Ollama endpoint also says whether the model is loaded, how
 * much of it is in GPU memory and with what context.
 */
export function providerLines(facts: ProviderFacts): string[] {
  const { provider, probe } = facts;
  const lines = [
    `Provider: ${providerLabel(provider)}`,
    `Endpoint: ${cleanText(provider.base_url)}`,
    `Model: ${cleanText(provider.model)}`,
  ];
  if (facts.environment)
    lines.push(`Chalupa environment: ${cleanText(facts.environment)}`);
  lines.push(
    `Context asked: ${provider.context_size} tokens${provider.context_plan ? ` (${cleanText(provider.context_plan)})` : ""}`,
    ...contextWarning(provider),
  );
  if (probe.error) {
    lines.push(
      `Answer: none (${clip(cleanText(probe.error).replace(/\s+/g, " ").trim(), 120)})`,
    );
    return lines;
  }
  lines.push(`Answer: ${probe.latencyMs ?? "?"} ms`);
  if (probe.version) lines.push(`Ollama: ${cleanText(probe.version)}`);
  if (provider.type === "openai_local") return lines;
  const { loaded } = probe;
  if (loaded === undefined) lines.push("Loaded: unknown (no /api/ps answer)");
  else if (loaded === null) lines.push("Loaded: no (the next turn loads it)");
  else {
    const share = Math.floor((loaded.vramBytes / loaded.sizeBytes) * 100);
    lines.push(`Loaded: ${gib(loaded.sizeBytes)} · ${share}% in GPU memory`);
    if (loaded.contextLength !== undefined)
      lines.push(
        loaded.mlx
          ? `Loaded context: ${loaded.contextLength} tokens (MLX runner: a soft limit, nothing reserved)`
          : `Loaded context: ${loaded.contextLength} tokens`,
      );
    const warning = placementWarning(provider, loaded);
    if (warning) lines.push(`Warning: ${cleanText(warning)}`);
  }
  return lines;
}

/** One connected MCP server as the panel lists it: its name and tool names only. */
export interface McpServerFacts {
  name: string;
  /** Tool names as shown under the server (local names when the prefix is known). */
  tools: readonly string[];
}

/**
 * The `/mcp` panel's lines: one heading line per server, then its tool
 * names. Names only — never a result or structured content (those stay
 * inside the runtime's parser boundary).
 */
export function mcpLines(
  servers: readonly McpServerFacts[],
  startup: {
    connecting?: boolean;
    failed?: readonly { name: string; error: string }[];
  } = {},
): string[] {
  const failed = startup.failed ?? [];
  if (servers.length === 0 && failed.length === 0 && !startup.connecting)
    return [
      "No MCP servers configured",
      "Use --mcp-config to connect trusted servers",
    ];
  const lines: string[] = startup.connecting
    ? ["Connecting to MCP servers…"]
    : [];
  for (const server of failed)
    lines.push(
      cleanText(`${server.name} · unavailable`),
      `  ${cleanText(server.error)}`,
    );
  for (const server of servers) {
    lines.push(
      cleanText(
        `${server.name} · ${server.tools.length} tool${server.tools.length === 1 ? "" : "s"}`,
      ),
    );
    lines.push(
      server.tools.length
        ? `  ${server.tools.map((tool) => cleanText(tool)).join(", ")}`
        : "  (no tools)",
    );
  }
  return lines;
}
