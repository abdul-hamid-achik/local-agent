// The parent's side of the Tuimark client: a reducer from runtime events to the
// Tuimark store, and the handlers of the actions ui/app.tui fires.
//
// It follows submit() in apps/cli/src/terminal.ts step by step (history, the
// streaming live message, tool status, the approval promise, the failure rule)
// so the two clients behave the same. What differs is where state goes: Vue
// refs there, `ui.batch` writes here.
//
// Writes are serialized: each one waits for the ack of the one before it, and
// the live message is flushed at ~30 Hz with a single write in flight (the
// ack is the backpressure Phase 0 measured). Nothing here touches the terminal:
// `tuimark host` owns it.

import { stat, writeFile } from "node:fs/promises";
import { totalmem } from "node:os";
import { join } from "node:path";
import {
  type AgentEvent,
  type AgentOptions,
  type Answer,
  type Approval,
  type ApprovalRequest,
  admitOllamaModel,
  admitOpenAILocalModel,
  appendRule,
  ConversationFullError,
  clearRules,
  commandLine,
  type Disposition,
  evaluate,
  explicitContextNote,
  type GoalReport,
  type LoadedModel,
  loadPermissionFile,
  MAX_HISTORY_MESSAGES,
  type McpConnection,
  type Message,
  namesSecretPath,
  type OllamaAdmission,
  type OpenAILocalAdmission,
  type PermissionFile,
  type PermissionRule,
  type Provider,
  type ProviderProbe,
  permissionRequest,
  permissionsPath,
  placementWarning,
  planContextSize,
  probeProvider,
  proposeRule,
  type Question,
  type RecoveryItem,
  ruleVerdict,
  runAgent,
  type Session,
  type SessionContext,
  StepLimitError,
  sandboxUnavailable,
  tooLongToSend,
  trustRules,
  trustStorePath,
  type Workspace,
} from "@local-agent/runtime";
import type { Commit, Host, HostEvent } from "@thelacanians/tuimark";
import { tokensPerSecond } from "#cli/preview";
import { terminalText } from "#cli/text";
import {
  type Block,
  type Card,
  cleanText,
  clip,
  fencedCodeBlocks,
  formatTook,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  liveToolView,
  MAX_RECEIPTS,
  type MessageView,
  NO_CARD,
  onHold,
  RECEIPTS_MAX_CHARS,
  type ToolReceipt,
  tableWidth,
  ViewBuilder,
} from "#cli/tuimark/blocks";
import {
  CLIPBOARD_TOOL_NAMES,
  COPY_TIMEOUT_MS,
  type CopyResult,
  characters,
  charCount,
  clipboardText,
  selectionHint,
} from "#cli/tuimark/clipboard";
import {
  BUSY_STATUS,
  type CommandOutcome,
  type Conclusion,
  filterPalette,
  type Intent,
  MOUSE_INLINE_STATUS,
  paletteCommand,
  QUEUE_RETURNED,
  runSlash,
  slashMatches,
  slashRows,
  type Theme,
} from "#cli/tuimark/commands";
import {
  exportFileName,
  transcriptJson,
  transcriptMarkdown,
} from "#cli/tuimark/export";
import {
  commandKey,
  effectful,
  type GoalCommand,
  GoalRun,
  type GoalTurn,
  newGoalTurn,
} from "#cli/tuimark/goal";
import {
  APPROVAL_PLACEHOLDER,
  APPROVAL_SANDBOX_SESSION_STATUS,
  APPROVAL_TRUNCATED_ALWAYS_STATUS,
  APPROVAL_TRUNCATED_SESSION_STATUS,
  APPROVAL_UNARMED_ALWAYS_STATUS,
  APPROVAL_UNARMED_SESSION_STATUS,
  APPROVAL_UNARMED_STATUS,
  APPROVAL_WAIT_STATUS,
  approvalState,
  armedStatus,
  CtxMeter,
  compactedNotice,
  contextLabel,
  contextPct,
  ctxFlags,
  detailsState,
  formatElapsed,
  grantCovers,
  grantFor,
  grantsLabel,
  type HeadPhase,
  type HeadView,
  headState,
  idleStatus,
  initialStore,
  type McpServerFacts,
  mcpLines,
  mcpParts,
  NO_DETAILS_STATUS,
  NO_HEAD,
  noApproval,
  noDetails,
  noPanel,
  noQuestion,
  PLACEHOLDER,
  PLACEHOLDER_BUSY,
  PROVIDER_CHECKING_STATUS,
  PROVIDER_NO_ANSWER_STATUS,
  panelState,
  providerLines,
  QUESTION_CARD_STATUS,
  QUESTION_CHOOSE_STATUS,
  QUESTION_EMPTY_STATUS,
  QUESTION_NO_MATCH_STATUS,
  QUESTION_PLACEHOLDER_CHOICE,
  QUESTION_PLACEHOLDER_TEXT,
  QUESTION_STATUS,
  QUESTION_UNARMED_STATUS,
  QUESTION_WAIT_STATUS,
  questionChoices,
  questionPick,
  questionState,
  type SessionSummary,
  type Store,
  secretStatus,
  selectTips,
  sessionRows,
  statusLines,
  windowedNotice,
} from "#cli/tuimark/store";
import { LiveStream } from "#cli/tuimark/stream";
import { Transcript } from "#cli/tuimark/transcript";

/** The part of the SDK's Host the client uses (a fake implements it in tests). */
export type UiHost = Pick<Host, "on" | "batch" | "get" | "commit">;

/** How the document is presented: the alternate screen, or inline with scrollback. */
export type Presentation = "fullscreen" | "inline";

type Write = { path: string; value: unknown; op?: "set" | "append" };
type RunAgent = (
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
) => AsyncIterable<AgentEvent>;

/**
 * Saved-session discovery and loading, handed in by the launcher only
 * when the process runs with `--session-db`. `load` first closes the
 * interrupted calls that never ran or only read, then refuses (throws) a
 * session the runtime would refuse at its first turn, with the runtime's
 * own words; one whose only problem is a call with an unknown outcome
 * loads, for `/recover`.
 */
export interface SessionsCapability {
  list(): Promise<SessionSummary[]>;
  load(handle: string): Promise<SessionContext>;
  /** `/sessions new`: a new, empty saved session in this workspace. */
  create?(): Promise<SessionContext>;
  /**
   * `/find`: the workspace's saved sessions searched for `query`, one hit
   * per session (its handle and the first matching snippet), capped by the
   * capability itself. Absent when the process has no session store.
   */
  search?(query: string): Promise<FindHit[]>;
  /** `/recover`: the session's calls whose outcome is unknown, oldest first. */
  recovery?(session: SessionContext): RecoveryItem[];
  /**
   * `/recover N …`: records what the user found about one of them, and
   * returns the session as saved after it (`closed`: calls given a result).
   */
  reconcile?(
    session: SessionContext,
    executionId: string,
    disposition: Disposition,
    note: string,
  ): Promise<{ state: Session; closed: number }>;
}

const DISPOSITIONS: Record<Conclusion, Disposition> = {
  applied: "effect_applied",
  "not-applied": "effect_not_applied",
  undone: "effect_compensated",
};
const FOUND: Record<Conclusion, string> = {
  applied: "it ran and its change is in place",
  "not-applied": "it made no change",
  undone: "its change was undone",
};

/** One `/find` result: the session it matched and a snippet around the match. */
export interface FindHit {
  handle: string;
  snippet: string;
}

export interface ClientOptions {
  provider: Provider;
  workspace: Workspace;
  intent: Intent;
  /**
   * Inline runs ui/inline.tui: finished blocks are committed into the
   * terminal's scrollback and the region holds only what is live.
   * Default fullscreen.
   */
  presentation?: Presentation;
  /**
   * The theme the host was started with (`--theme`), so `/theme` reports it
   * instead of "auto". Default "auto" (the runtime follows the terminal).
   */
  initialTheme?: Theme;
  session?: SessionContext;
  /** `/sessions` support, present only with `--session-db` (see launch.ts). */
  sessions?: SessionsCapability;
  mcp?: readonly McpConnection[];
  /**
   * The configured name of each MCP connection, in `mcp` order. A
   * connection itself carries only its `SERVER__TOOL` names; the launcher
   * knows the config names (a server with no tools has no name otherwise).
   */
  mcpNames?: readonly string[];
  /**
   * Settles once every configured MCP server has connected (pushed onto
   * `mcp` and `mcpNames` by then) or failed, with the failures. Until then
   * the status says the servers are connecting and a turn waits for them:
   * their tools are part of what the model is offered. Never rejects.
   */
  mcpReady?: Promise<readonly { name: string; error: string }[]>;
  /**
   * Creates the export file exclusively (`wx`): rejects with EEXIST when
   * the path is taken, so an export never overwrites. Default node fs.
   */
  writeExport?: (path: string, text: string) => Promise<void>;
  /**
   * Validates a candidate provider's model (`/model NAME`) as launch does:
   * against the Ollama inventory, or an OpenAI-compatible server's model
   * list. Default the runtime's admission check; tests replace.
   */
  admitModel?: (
    provider: Provider,
    signal: AbortSignal,
  ) => Promise<OllamaAdmission | OpenAILocalAdmission>;
  /**
   * Commands start in the sandbox (`--sandbox-commands`): where it is
   * unavailable they ask, and the status says why.
   */
  sandbox?: boolean;
  /** Why the command sandbox is unavailable in a workspace. Default the runtime's check; tests replace. */
  sandboxUnavailable?: (workspaceRoot: string) => string | undefined;
  /** Process-level abort (SIGINT, SIGTERM). */
  signal: AbortSignal;
  /** Replaces the runtime's agent loop (tests). */
  run?: RunAgent;
  /** Live flush period in ms. Default 33 (~30 Hz). */
  flushMs?: number;
  /** Messages the store holds at first, and how many "load earlier" adds. */
  windowSize?: number;
  windowStep?: number;
  /** The terminal width in columns, for wrapping the approval preview. */
  columns?: () => number;
  /** How long a new approval dialog ignores `y`. Default APPROVAL_ARM_MS. */
  armMs?: number;
  /** The machine's memory, for planning a new model's context. Default os.totalmem. */
  totalMemory?: () => number;
  /** `/provider`'s read-only look at the endpoint. Default the runtime's probeProvider; tests replace. */
  probe?: (provider: Provider, signal: AbortSignal) => Promise<ProviderProbe>;
  /**
   * Reads how the server loaded the model, once its first reply is done
   * (Ollama's /api/ps). main passes the runtime's loadedModel; without it
   * nothing is checked.
   */
  placement?: (
    provider: Provider,
    signal: AbortSignal,
  ) => Promise<LoadedModel | null | undefined>;
  /** Chalupa's environment name (CHALUPA_ENVIRONMENT), when Chalupa launched this process. */
  environment?: string;
  /** Clock for the head's elapsed time. Default Date.now. */
  now?: () => number;
  /**
   * Puts text on the system clipboard (`/copy`, ctrl+y). Absent: there is no
   * clipboard, so tests never touch the real one; launch.ts passes the
   * real copyToClipboard. Never an OSC 52 write: Tuimark owns the TTY.
   */
  copy?: (text: string) => Promise<CopyResult>;
  /** `$TERM_PROGRAM`, for the native-selection hint. Default none (shift-drag). */
  termProgram?: string;
}

/**
 * A new approval dialog ignores dialog_confirm for this long. A `y` typed or
 * double-tapped just as the dialog opens must not approve a change nobody has
 * read; deny and cancel always work. The status line shows "y allow" only once
 * the dialog is armed.
 */
export const APPROVAL_ARM_MS = 300;
/** A message as one status line: zod errors and stack-like text span many lines. */
/** A status line's longest text; `oneLine` clips beyond it. */
const STATUS_MAX = 300;

/** A message as one clean line, before any clipping. */
function flatLine(message: string): string {
  return terminalText(message).replace(/\s+/g, " ").trim();
}

function oneLine(message: string): string {
  return clip(flatLine(message), STATUS_MAX);
}

/** A validated admission from Ollama's inventory, the one that says how big the model is. */
function sized(
  admission: OllamaAdmission | OpenAILocalAdmission,
): admission is Extract<OllamaAdmission, { status: "validated" }> {
  return "sizeBytes" in admission;
}

export { formatElapsed };

/** What the turn is doing between the model's messages, for the head. */
type Phase = HeadPhase | "between";

/** The refused writes the client remembers. */
const MAX_WRITE_ERRORS = 20;

/** `/goal …` as typed: one line. It stays a command while busy or asking. */
function isGoalCommand(raw: string): boolean {
  const text = raw.trim();
  return !text.includes("\n") && /^\/goal(\s|$)/i.test(text);
}

/** `/copy` as typed: one line, and harmless while a turn runs (it only reads). */
function isCopyCommand(raw: string): boolean {
  const text = raw.trim();
  return !text.includes("\n") && /^\/copy(\s|$)/i.test(text);
}

const NO_REPLY_STATUS = "No reply to copy yet";
const NO_CODE_STATUS = "No code block in the last reply";

const NO_GOAL_STATUS = "No goal is running. /goal OBJECTIVE starts one";

/**
 * A slash command's first word: one slash, then no other (`/model`, a typo
 * such as `/model:x` too, which is refused, never sent). A second slash
 * makes it a path, and the line a message.
 */
const COMMAND_WORD = /^\/[^\s/]+$/;
/**
 * How long a finished turn waits for its placement read before it clears
 * busy; a read slower than this is shown after a later turn instead.
 */
const PLACEMENT_WAIT_MS = 300;
/** Refused inline prints kept for a later attempt, oldest dropped past this. */
const MAX_SPILL = 50;

/** One refused inline print, in the order it was first owed to scrollback. */
type Spill =
  | { kind: "view"; view: MessageView }
  | { kind: "links"; view: MessageView }
  | { kind: "blocks"; blocks: Block[]; head: boolean };
/** The messages the composer's history keeps. */
const MAX_SENT = 200;

export class TuimarkClient {
  private ui?: UiHost;
  private readonly run: RunAgent;
  /**
   * The width a table may take, read when a message is built: blocks are
   * rendered once, so a table already in the transcript keeps the width it was
   * written at when the terminal is resized.
   */
  private readonly tableCols = () =>
    tableWidth(this.options.columns?.() ?? 80, this.inline);
  // A session switch starts from a fresh builder: the old one's call-id
  // map belongs to the old conversation.
  private builder = new ViewBuilder(this.tableCols);
  /**
   * The durable session the next turn checkpoints to. Starts as the
   * launch session; a `/sessions` switch replaces it whole (never while
   * a turn runs, so a checkpoint can never land in the wrong session).
   */
  private session?: SessionContext;
  private readonly transcript: Transcript;
  private readonly history: Message[];
  /** What the user sent, oldest first: up and down at the edge of the composer recall it. */
  private readonly sent: string[] = [];
  /** Where in `sent` the composer is, or undefined when it holds a fresh draft. */
  private recall?: number;
  /** The draft that recalling started from, back when the user comes down again. */
  private recalledFrom = "";
  // the `/` popup over the composer, derived from the draft on every edit
  /** Whether the popup is showing (the store's `slash.open`). */
  private slashOpen = false;
  /** The chosen popup row, an index into the current matches. */
  private slashSel = 0;
  /**
   * Esc dismissed the popup for this draft: it stays closed while the
   * draft still starts with "/", and may open again for the next one.
   */
  private slashDismissed = false;
  /** The last popup state written, so an unchanged one is not rewritten. */
  private slashSig = "[]";
  private intent: Intent;
  /** Session theme (`/theme`); written to the host's reserved `@theme` path. */
  private theme: Theme;
  /** The provider the next turn runs on (`/model NAME` swaps it mid-session). */
  private provider: Provider;
  /** Fullscreen mouse reporting (`/mouse`): on, as the store starts. Inline never writes it. */
  private mouse = false;
  // the writer
  private tail: Promise<void> = Promise.resolve();
  /** Commits run one at a time: each rewrites the same scratch path. */
  private commitChain: Promise<void> = Promise.resolve();
  /** Store writes the host refused (a path the markup does not read, a dead host). */
  readonly writeErrors: string[] = [];
  private readonly inline: boolean;
  // the live message: the answer and the reasoning stream through `stream`,
  // a tool call is one value of its own
  private readonly stream: LiveStream;
  /** Inline: the running reply's first blocks already carry its label in print. */
  private headCommitted = false;
  /** Ordered refused inline prints, retried before any newer print. */
  private spill: Spill[] = [];
  private spillDropped = 0;
  private spillFlush?: Promise<void>;
  /** A session load is in flight: no turn may start until it settles. */
  private switching = false;
  /** Quit presses serialize here; the quit stage itself is memoized below. */
  private quitChain: Promise<unknown> = Promise.resolve();
  private quitPromise?: Promise<"quit" | undefined>;
  private liveTool?: LiveView;
  private dirty = false;
  private liveInFlight = false;
  private timer?: ReturnType<typeof setInterval>;
  // the turn
  private busy = false;
  private turn?: AbortController;
  /** The turn a ctrl+c cancelled: its end says the next press quits. */
  private quitCancelled?: AbortController;
  private turnDone: Promise<void> = Promise.resolve();
  private decide?: (
    allowed: boolean,
    session?: boolean,
    status?: string,
  ) => Promise<void>;
  private armed = false;
  private armTimer?: ReturnType<typeof setTimeout>;
  /**
   * The open ask_user question: the chosen row, whether enter is taken yet
   * (the approval's arm delay), and how an answer closes it.
   */
  private asking?: {
    question: Question;
    /** The chosen row; -1 until one is chosen (a single choice starts so). */
    sel: number;
    armed: boolean;
    answer: (answer: Answer) => Promise<void>;
    /** Closes it unanswered: the runtime's ask rejects. */
    close: () => Promise<void>;
  };
  private askArmTimer?: ReturnType<typeof setTimeout>;
  /** The composer's draft as last typed or written: a question puts it aside. */
  private draftNow = "";
  private proposal?: ApprovalRequest;
  /** The open proposal's preview was truncated: `a` must not grant from it. */
  private proposalTruncated = false;
  /** The armed status the open dialog last showed (see resize). */
  private armedLine = "";
  /** `/sandbox`: commands no rule decides run confined, without a card. */
  private sandbox = false;
  /** Why the sandbox was last found unavailable (`/status` says it). */
  private sandboxWhy?: string;
  /**
   * Session grants (`a` on an approval), grant key -> label, in grant order.
   * Memory only: they die with the process and are never persisted. They
   * sit strictly on the approval step — plan intent is refused by the
   * runtime before `approve` is ever called (and again below).
   */
  private readonly grants = new Map<
    string,
    {
      label: string;
      prefix?: readonly string[];
      options?: readonly string[];
    }
  >();
  /**
   * The workspace's permission rules, cached with a stamp of the file and
   * of the trust store: a `!` verdict, a trust from another terminal or any
   * outside edit moves the stamp and the next evaluation reloads.
   */
  private permFile: PermissionFile & { untrusted?: PermissionRule[] } = {
    version: 1,
    rules: [],
  };
  private permStamp: string | undefined;
  /**
   * The untrusted allow rules `/permissions trust` last showed: the only
   * rules `/permissions trust confirm` may trust, so a rule written into
   * the file after the review is never trusted unseen.
   */
  private trustOffer?: PermissionRule[];
  /**
   * The newest completed tool receipts, whole, for `/details` (the last)
   * and `/details N` (card N), within RECEIPTS_MAX_CHARS and MAX_RECEIPTS:
   * past either, the oldest goes. Memory only: never a model message,
   * never persisted.
   */
  private readonly receipts: ToolReceipt[] = [];
  private receiptChars = 0;
  /** The first number this conversation gave: below it, an earlier one's. */
  private firstReceipt = 1;
  /** The receipt the viewer shows, re-wrapped on a resize. */
  private shownReceipt?: ToolReceipt;
  /** Whether the details viewer is open (the store's `details.open`). */
  private detailsOpen = false;
  /** Whether the read-only panel is open (the store's `panel.open`). */
  private panelOpen = false;
  /** The open text panel's source, re-wrapped on resize like a preview. */
  private panelText?: { title: string; args: string; text: string };
  /** The picker's sessions, in list order, while it is open. */
  private panelSessions: SessionSummary[] = [];
  /** The picker's chosen row, an index into `panelSessions`. */
  private panelSel = 0;
  /** MCP servers are still connecting (see ClientOptions.mcpReady). */
  private mcpPending: boolean;
  /** The servers that failed to connect, for the status and `/mcp`. */
  private mcpFailed: readonly { name: string; error: string }[] = [];
  /** The header ctx meter (see CtxMeter for the exact accumulation rule). */
  private readonly ctx = new CtxMeter();
  /**
   * One message sent while busy, run when the turn ends. A further send is
   * added to it.
   */
  private queued?: string;
  /** The cancel gave the queue back: the final status says both. */
  private cancelReturnedQueue = false;
  /** The running goal (see goal.ts); it ends with the process. */
  private goal?: GoalRun;
  /** What the running turn of a goal has done, for the stop rules. */
  private goalTurn?: GoalTurn;
  /** The goal's last turn left it open: its next turn follows. */
  private goalContinues = false;
  /**
   * Why the goal ends at its turn's end, when the user asked: `/goal stop`
   * or esc. Its turn is cancelled when one runs; asked in a turn's tail,
   * after the cancel can reach it, it still ends the goal before another
   * turn (the end of loop).
   */
  private goalStop?: string;
  /** The host's end of goal_status: the report joins the turn's record. */
  private readonly reportGoal = (report: GoalReport): void => {
    if (this.goalTurn) this.goalTurn.report = report;
  };
  /** Quit was answered: a queued message must not start after it. */
  private quitting = false;
  /** What the head of the live message says the turn is doing. */
  private phase: Phase = "waiting";
  /** The turn runs and its head may show (cleared first thing when it ends). */
  private headOn = false;
  /** The head as last written (JSON), so an unchanged one is not rewritten. */
  private headShown = JSON.stringify(NO_HEAD);
  /** The elapsed second the head last showed, so the ticker writes once a second. */
  private headSeconds = -1;
  /** When the running turn started (the `now` clock), for the head's elapsed. */
  private turnStartedAt = 0;
  /**
   * The elapsed time stops while the turn waits on the person (an approval,
   * an answer): when the wait began, and the waits already over.
   */
  private pausedAt?: number;
  private pausedMs = 0;
  /**
   * A transient status (a verdict, a rule's note) is showing since this
   * turn began: the next tool result puts the ambient status back.
   */
  private flashed = false;
  /** The last finished turn, for the ambient status ("last 1m 06s · 32 tok/s"). */
  private lastTurn?: { seconds: number; speed?: string };
  /** Ollama's generation counters, summed over the steps of a turn (nanoseconds). */
  private evalCount = 0;
  private evalDuration = 0;
  private reportedWriteError = false;

  constructor(private readonly options: ClientOptions) {
    if (options.sandbox) {
      this.sandboxWhy = this.sandboxProblem();
      this.sandbox = this.sandboxWhy === undefined;
    }
    this.run = options.run ?? runAgent;
    this.intent = options.intent;
    this.inline = options.presentation === "inline";
    this.mouse = !this.inline;
    this.stream = new LiveStream(undefined, {
      inline: this.inline,
      tableCols: this.tableCols,
    });
    this.transcript = new Transcript(options.windowStep, options.windowSize);
    this.session = options.session;
    this.provider = options.provider;
    this.theme = options.initialTheme ?? "auto";
    this.history = options.session?.state.messages.slice() ?? [];
    this.mcpPending = options.mcpReady !== undefined;
    // A resumed session shows its saved messages, through the same mapping as a live turn.
    for (const message of this.history) {
      const view = this.builder.fromMessage(message);
      if (view) this.transcript.push(view);
      const receipt = this.builder.receiptOf(message);
      if (receipt) this.keepReceipt(receipt);
      if (message.role === "user" && message.content.trim())
        this.sent.push(message.content);
    }
  }

  /** Why commands cannot run in the sandbox here, or undefined when they can. */
  private sandboxProblem(): string | undefined {
    const root = this.options.workspace?.root;
    if (!root) return "no workspace";
    return (this.options.sandboxUnavailable ?? sandboxUnavailable)(root);
  }

  /** The store the document starts with (the `data` of spawnHost). */
  initialStore(): Store {
    const { session, mcp = [] } = this.options;
    const store = initialStore({
      provider: this.provider,
      intent: this.intent,
      sessionId: session?.state.publicId,
      mcpServers: mcp.length,
      mcpConnecting: this.mcpPending,
      workspace: this.options.workspace?.root ?? "",
      inline: this.inline,
      // The inline snapshot below always starts empty; a resumed session is not.
      empty: this.transcript.length === 0,
      // Inline, the region never holds history: attach() commits it instead.
      transcript: this.inline
        ? { messages: [], earlier: { n: 0 } }
        : this.transcript.snapshot(),
      sandbox: this.sandbox,
      termProgram: this.options.termProgram,
    });
    // `--sandbox-commands` where the sandbox is unavailable: commands ask,
    // and the first status says why.
    if (this.sandboxWhy !== undefined)
      store.status = oneLine(
        `Sandbox unavailable: ${this.sandboxWhy} · commands ask`,
      );
    // The startup warning on stderr is under the screen: the first status
    // says it too (the sandbox's, a safety matter, goes first).
    else if (this.provider.context_warning)
      store.status = oneLine(
        `Warning: ${this.provider.context_warning} · /status shows it again`,
      );
    return store;
  }

  /**
   * An action handler threw (a rules file that cannot be written, a dead
   * read): its error on the status line, where a reply of `error` would
   * have ended the session.
   */
  async handlerFailed(action: string, error: unknown): Promise<void> {
    const message = error instanceof Error ? error.message : String(error);
    await this.write([
      { path: "status", value: oneLine(`Action ${action} failed: ${message}`) },
    ]);
  }

  /** Registers the action handlers on a spawned host. */
  attach(ui: UiHost): void {
    this.ui = ui;
    ui.on("send", (ev) => this.onSend(ev));
    // The composer's every edit: the `/` popup follows the draft. Most
    // edits change nothing (the sync writes only a changed popup).
    ui.on("typed", (ev) => this.onTyped(ev));
    ui.on("slash_next", (ev) => this.slashMove(ev, 1));
    ui.on("slash_prev", (ev) => this.slashMove(ev, -1));
    ui.on("slash_complete", (ev) => this.slashComplete(ev));
    ui.on("slash_close", () => this.slashEsc());
    ui.on("mode_toggle", () => this.modeToggle());
    ui.on("history_prev", (ev) => this.recallPrev(ev));
    ui.on("history_next", (ev) => this.recallNext(ev));
    ui.on("cancel", async () => {
      // Esc cancels the turn and a running goal with it; a queued send is
      // not sent, and goes back to the composer.
      const queued = this.takeQueued();
      if (queued) this.cancelReturnedQueue = true;
      if (this.goal) this.goalStop ??= "stopped: you cancelled its turn";
      this.turn?.abort();
      if (queued) await this.write([queued]);
    });
    // The binds name this `quit_request`, not `quit`: `quit` is an engine
    // built-in that ends the session itself and never reaches the parent,
    // so a handler for it would never run (proven against the real host).
    // Answering "quit" from here is what ends the session.
    ui.on("quit_request", () => this.quitRequest());
    ui.on("load_earlier", () => this.loadEarlier());
    // ctrl+y: the last reply to the clipboard, as `/copy`. An open approval
    // keeps its keys and its status line.
    ui.on("copy_reply", () =>
      this.decide ? undefined : this.copyReply("reply"),
    );
    ui.on("dialog_confirm", async () => {
      if (this.armed) return this.decide?.(true);
      // An unarmed `y` is still swallowed, but not silently.
      if (this.decide)
        await this.write([{ path: "status", value: APPROVAL_UNARMED_STATUS }]);
      return undefined;
    });
    ui.on("dialog_session", async () => {
      if (!this.decide) return undefined;
      // `a` follows `y` exactly: same arm delay, same said-so feedback.
      if (!this.armed) {
        await this.write([
          { path: "status", value: APPROVAL_UNARMED_SESSION_STATUS },
        ]);
        return undefined;
      }
      // No grant is taken from a change nobody could read whole: the
      // dialog stays open and `y` still works as it always did.
      if (this.proposalTruncated) {
        await this.write([
          { path: "status", value: APPROVAL_TRUNCATED_SESSION_STATUS },
        ]);
        return undefined;
      }
      // A command that names a secret path is let out by its own card only.
      if (this.proposal && namesSecretPath(this.proposal)) {
        await this.write([
          { path: "status", value: secretStatus(this.proposal) },
        ]);
        return undefined;
      }
      // With the sandbox on, a card lets out only its own command: a grant
      // would run later ones outside without a card.
      if (this.sandbox && this.proposal?.kind === "command") {
        await this.write([
          { path: "status", value: APPROVAL_SANDBOX_SESSION_STATUS },
        ]);
        return undefined;
      }
      return this.decide(true, true);
    });
    ui.on("dialog_always", async () => {
      const decide = this.decide;
      if (!decide || !this.proposal) return undefined;
      // `!` follows `y` and `a`: same arm delay, same said-so feedback.
      if (!this.armed) {
        await this.write([
          { path: "status", value: APPROVAL_UNARMED_ALWAYS_STATUS },
        ]);
        return undefined;
      }
      // No persistent rule comes from a change nobody could read whole.
      if (this.proposalTruncated) {
        await this.write([
          { path: "status", value: APPROVAL_TRUNCATED_ALWAYS_STATUS },
        ]);
        return undefined;
      }
      // Nor does one become a standing rule.
      if (namesSecretPath(this.proposal)) {
        await this.write([
          { path: "status", value: secretStatus(this.proposal) },
        ]);
        return undefined;
      }
      const root = this.options.workspace?.root;
      const request = permissionRequest(this.proposal);
      const proposed = proposeRule(request);
      const label = `${proposed.tool} ${proposed.pattern}`;
      let saved = "saved to .local-agent/permissions.json";
      if (root) {
        // A deny rule that already covers this request is stronger intent
        // than the keypress: say so and keep the dialog open.
        const shadow = evaluate((await this.fileRules()).rules, request);
        // Esc or a cancel while the rules loaded closed the dialog: no
        // rule is saved for a request that is no longer asked.
        if (this.decide !== decide) return undefined;
        if (shadow.effect === "deny") {
          await this.write([
            {
              path: "status",
              value: oneLine(
                `Not saved: denied by rule: ${shadow.rule?.pattern}`,
              ),
            },
          ]);
          return undefined;
        }
        try {
          const { added } = await appendRule(root, proposed);
          if (!added)
            saved = "already in .local-agent/permissions.json, now trusted";
          // The file changed under the cache: the next evaluation reloads.
          this.permStamp = undefined;
        } catch (error) {
          await this.write([
            { path: "status", value: oneLine(`Rule not saved: ${error}`) },
          ]);
          return undefined;
        }
      }
      // The rule is saved, as `!` asked; a dialog closed while it was
      // written keeps its own verdict (a deny stays a deny).
      if (this.decide !== decide) return undefined;
      return decide(
        true,
        false,
        root
          ? `Always allowed: ${label} (${saved})`
          : `Always allowed: ${label} (no workspace: rule not saved)`,
      );
    });
    ui.on("dialog_cancel", () => this.decide?.(false));
    ui.on("question_next", () => this.questionMove(1));
    ui.on("question_prev", () => this.questionMove(-1));
    // Esc skips the question; the turn goes on without an answer.
    ui.on("question_skip", () => this.asking?.answer({ kind: "declined" }));
    ui.on("palette_open", () => this.paletteOpen());
    ui.on("palette_query", (ev) => this.paletteQuery(ev));
    ui.on("palette_run", () => this.paletteRun());
    ui.on("palette_close", () =>
      this.write([{ path: "palette.open", value: false }]),
    );
    ui.on("help_close", () =>
      this.write([{ path: "help.open", value: false }]),
    );
    ui.on("details_close", () => this.closeDetails());
    ui.on("panel_close", () => this.closePanel());
    ui.on("panel_next", () => this.panelMove(1));
    ui.on("panel_prev", () => this.panelMove(-1));
    ui.on("panel_run", () => this.panelRun());
    void this.options.mcpReady?.then((failed) => this.mcpSettled(failed));
    if (this.inline) {
      // A resumed session's history is the scrollback: print it once, in order.
      const views = this.transcript.all();
      if (views.length > 0) {
        void (async () => {
          for (const view of views) await this.commitView(view);
        })();
      }
    }
  }

  /** The ambient status line: mode, MCP state and the last turn (same idle and busy). */
  private idleText(): string {
    const status = idleStatus(
      this.options.mcp?.length ?? 0,
      this.intent,
      {
        connecting: this.mcpPending,
        failed: this.mcpFailed.length,
      },
      this.lastTurn,
    );
    // A send queued while the turn runs stays a fact of the bar after the
    // "Queued: …" message has given way to a verdict or a rule's note.
    return this.queued !== undefined ? `${status} · 1 queued` : status;
  }

  /**
   * The MCP servers settled. The status shows it only if it still says
   * they are connecting: anything written since (a turn, a command's
   * answer) is newer than this.
   */
  private async mcpSettled(
    failed: readonly { name: string; error: string }[],
  ): Promise<void> {
    const connecting = this.idleText();
    this.mcpPending = false;
    this.mcpFailed = failed;
    // An open /mcp panel still says "connecting": it is drawn again.
    if (this.panelOpen && this.panelText?.title === "MCP servers") {
      await this.runCommand("/mcp");
      return;
    }
    const shown = await this.ui
      ?.get("status")
      .then((result) => result.value)
      .catch(() => undefined);
    if (shown === connecting)
      await this.write([{ path: "status", value: this.idleText() }]);
  }

  /** The terminal changed size: open previews are wrapped again. */
  async resize(): Promise<void> {
    const writes: Write[] = [];
    const proposal = this.proposal;
    if (proposal) {
      const approval = this.approvalFor(proposal);
      // A narrower terminal wraps more rows: truncation can change with it,
      // and with it the grants an armed dialog's status names. Only then is
      // the status written: a refusal the person just read stays.
      this.proposalTruncated = approval.truncated;
      writes.push({ path: "approval", value: approval });
      const line = armedStatus(approval);
      if (this.armed && line !== this.armedLine) {
        this.armedLine = line;
        writes.push({ path: "status", value: line });
      }
    }
    if (this.detailsOpen && this.shownReceipt)
      writes.push({
        path: "details",
        value: this.detailsFor(this.shownReceipt),
      });
    if (this.panelOpen && this.panelText)
      writes.push({ path: "panel", value: this.panelFor(this.panelText) });
    if (writes.length > 0) await this.write(writes);
  }

  private detailsFor(receipt: ToolReceipt) {
    return detailsState(receipt, this.options.columns?.() ?? 80, this.inline);
  }

  private keepReceipt(receipt: ToolReceipt): void {
    this.receipts.push(receipt);
    this.receiptChars += receipt.output.length;
    while (
      this.receipts.length > 1 &&
      (this.receipts.length > MAX_RECEIPTS ||
        this.receiptChars > RECEIPTS_MAX_CHARS)
    )
      this.receiptChars -= this.receipts.shift()?.output.length ?? 0;
  }

  /** The writes that close the viewer, or none when it is already closed. */
  private closeDetailsWrite(): Write[] {
    if (!this.detailsOpen) return [];
    this.detailsOpen = false;
    this.shownReceipt = undefined;
    return [{ path: "details", value: noDetails() }];
  }

  /** Esc/q in the viewer: it closes and the status rests at idle again. */
  private async closeDetails(): Promise<void> {
    const writes = this.closeDetailsWrite();
    if (writes.length === 0) return;
    writes.push({ path: "status", value: this.idleText() });
    await this.write(writes);
  }

  /**
   * `/details`: the latest receipt whole, in the viewer; `/details N`: the
   * receipt of the card whose pointer names N. Refused while a turn runs or
   * an approval is open — an armed dialog is never covered. Says whether
   * the viewer opened.
   */
  private async openDetails(n?: number): Promise<boolean> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return false;
    }
    const receipt =
      n === undefined
        ? this.receipts.at(-1)
        : this.receipts.find((kept) => kept.n === n);
    if (!receipt) {
      await this.write([
        {
          path: "status",
          value:
            n === undefined
              ? NO_DETAILS_STATUS
              : n >= this.builder.nextReceiptNumber
                ? `No tool output ${n} yet. /details opens the latest`
                : n < this.firstReceipt
                  ? `Tool output ${n} belongs to an earlier conversation. /details opens the latest`
                  : `Tool output ${n} is no longer kept (the oldest go first). /details opens the latest`,
        },
      ]);
      return false;
    }
    this.detailsOpen = true;
    this.shownReceipt = receipt;
    await this.write([
      ...this.closePanelWrite(),
      { path: "details", value: this.detailsFor(receipt) },
      {
        path: "status",
        value:
          n === undefined
            ? `Latest tool output: ${receipt.name}`
            : `Tool output ${n}: ${receipt.name}`,
      },
    ]);
    return true;
  }

  // ------------------------------------------------------------------ the panel

  private panelFor(text: { title: string; args: string; text: string }) {
    return panelState(
      text.title,
      text.args,
      text.text,
      this.options.columns?.() ?? 80,
      this.inline,
    );
  }

  /** The writes that close the panel, or none when it is already closed. */
  private closePanelWrite(): Write[] {
    if (!this.panelOpen) return [];
    this.panelOpen = false;
    this.panelText = undefined;
    this.panelSessions = [];
    this.panelSel = 0;
    return [{ path: "panel", value: noPanel() }];
  }

  /** Esc/q in the panel: it closes and the status rests at idle again. */
  private async closePanel(): Promise<void> {
    const writes = this.closePanelWrite();
    if (writes.length === 0) return;
    writes.push({ path: "status", value: this.idleText() });
    await this.write(writes);
  }

  /**
   * Opens a text panel (`/status`, `/mcp`). Refused while a turn runs or
   * an approval is open, like the viewer; the two never cover each other.
   */
  private async openPanel(
    title: string,
    args: string,
    text: string,
    status: string,
  ): Promise<void> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    this.panelOpen = true;
    this.panelText = { title, args, text };
    this.panelSessions = [];
    await this.write([
      ...this.closeDetailsWrite(),
      { path: "panel", value: this.panelFor(this.panelText) },
      { path: "status", value: status },
    ]);
  }

  /** Whether the sessions picker is the open panel. */
  private get picking(): boolean {
    return this.panelOpen && this.panelSessions.length > 0;
  }

  /** The picker's store value for the current choice. */
  private pickerState() {
    return {
      open: true,
      title: "Sessions",
      args: "",
      lines: [],
      rows: sessionRows(
        this.panelSessions,
        this.panelSel,
        this.session?.state.publicId,
      ),
      picking: true,
    };
  }

  /** Up and down in the picker: the choice moves, clamped like the popup's. */
  private async panelMove(by: number): Promise<void> {
    if (!this.picking) return;
    this.panelSel = Math.max(
      0,
      Math.min(this.panelSel + by, this.panelSessions.length - 1),
    );
    await this.write([{ path: "panel", value: this.pickerState() }]);
  }

  /** Enter in the picker: load the chosen session. */
  private async panelRun(): Promise<void> {
    if (!this.picking) return;
    const chosen = this.panelSessions[this.panelSel];
    if (chosen) await this.switchSession(chosen.handle);
  }

  /** `/status`: the effective config, exactly what this client holds. */
  private async openStatusPanel(): Promise<void> {
    const { mcp = [] } = this.options;
    const db = this.session?.store.path;
    const text = statusLines({
      provider: this.provider,
      workspace: this.options.workspace?.root ?? "",
      intent: this.intent,
      sessionId: this.session?.state.publicId,
      sessionDb: typeof db === "string" && db ? db : undefined,
      theme: this.theme,
      mouse: this.inline ? "n/a (inline)" : this.mouse ? "on" : "off",
      grants: this.grantLabels(),
      sandbox: this.sandbox
        ? "on"
        : this.sandboxWhy !== undefined
          ? oneLine(`off (unavailable: ${this.sandboxWhy})`)
          : "off",
      usedTokens: this.ctx.current,
      environment: this.options.environment,
      mcpServers: mcp.length,
      mcpTools: mcp.reduce((total, server) => total + server.tools.length, 0),
    }).join("\n");
    await this.openPanel("Status", "", text, "Status");
  }

  /** `/provider`: one read-only look at the endpoint, then the panel over its answer. */
  private async openProviderPanel(): Promise<void> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    await this.write([{ path: "status", value: PROVIDER_CHECKING_STATUS }]);
    const probe = await (this.options.probe ?? probeProvider)(
      this.provider,
      this.options.signal,
    );
    await this.openPanel(
      "Provider",
      "",
      providerLines({
        provider: this.provider,
        environment: this.options.environment,
        probe,
      }).join("\n"),
      probe.error ? PROVIDER_NO_ANSWER_STATUS : "Provider",
    );
  }

  /** The most messages a request has left out, as last noticed (see "windowed"). */
  private windowNoticed = 0;

  /** Endpoint, model and size whose placement this process has read. */
  private readonly placementRead = new Set<string>();
  /** The warning of a placement read not shown yet (see startPlacement). */
  private placement?: Promise<string | undefined>;

  /**
   * Once a model's first reply is done (it is loaded then), one read-only
   * look at how its server loaded it, in the background: partly outside GPU
   * memory, or with less context than asked, earns a warning. Once per
   * endpoint, model and size; `/provider` asks again at any time.
   */
  private startPlacement(): void {
    const read = this.options.placement;
    if (!read || this.provider.type === "openai_local") return;
    // A switch to another model, endpoint or size is a new placement.
    const key = JSON.stringify([
      this.provider.base_url,
      this.provider.model,
      this.provider.context_size,
    ]);
    if (this.placementRead.has(key)) return;
    this.placementRead.add(key);
    const provider = this.provider;
    this.placement = read(provider, this.options.signal)
      .then((loaded) => placementWarning(provider, loaded))
      .catch(() => undefined);
  }

  /**
   * After a finished turn's reply is printed, its placement warning, if the
   * read is back within PLACEMENT_WAIT_MS; else a later turn shows it.
   */
  private async showPlacement(): Promise<void> {
    const pending = this.placement;
    if (!pending) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const later = new Promise<"later">((resolve) => {
      timer = setTimeout(() => resolve("later"), PLACEMENT_WAIT_MS);
    });
    const warning = await Promise.race([pending, later]);
    clearTimeout(timer);
    if (warning === "later") return;
    this.placement = undefined;
    if (!warning) return;
    const view = this.builder.notice(
      `Warning: ${warning}. /provider shows it again.`,
    );
    this.transcript.push(view);
    if (this.inline) await this.commitView(view);
  }

  /** One connection as panel facts: its config name when known, else its tools' prefix. */
  private mcpFacts(): McpServerFacts[] {
    const { mcp = [], mcpNames = [] } = this.options;
    return mcp.map((server, index) => {
      const names = server.tools.map((tool) => tool.name);
      const known = mcpNames[index];
      const derived = names[0] ? mcpParts(names[0])?.server : undefined;
      const name = known ?? derived ?? `server ${index + 1}`;
      return {
        name,
        // Tool names only — the local name under its server, never a
        // result (those stay inside the runtime's parser boundary).
        tools: names.map((tool) =>
          tool.startsWith(`${name}__`) ? tool.slice(name.length + 2) : tool,
        ),
      };
    });
  }

  /** `/mcp`: each connected server with its tool names, count line on top. */
  private async openMcpPanel(countLine: string): Promise<void> {
    await this.openPanel(
      "MCP servers",
      countLine,
      mcpLines(this.mcpFacts(), {
        connecting: this.mcpPending,
        failed: this.mcpFailed,
      }).join("\n"),
      countLine,
    );
  }

  /** `/sessions`: the picker, or why this process has no saved sessions. */
  private async openSessions(): Promise<void> {
    if (this.busy || this.proposal || this.queued !== undefined) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const capability = this.options.sessions;
    if (!capability) {
      await this.write([
        {
          path: "status",
          value: "Sessions need --session-db PATH at startup",
        },
      ]);
      return;
    }
    let sessions: SessionSummary[];
    try {
      sessions = await capability.list();
    } catch (error) {
      await this.write([
        {
          path: "status",
          value: oneLine(
            error instanceof Error ? error.message : "Unable to list sessions",
          ),
        },
      ]);
      return;
    }
    if (sessions.length === 0) {
      await this.write([
        { path: "status", value: "No sessions in this workspace" },
      ]);
      return;
    }
    this.panelOpen = true;
    this.panelText = undefined;
    this.panelSessions = sessions;
    const current = sessions.findIndex(
      (session) => session.handle === this.session?.state.publicId,
    );
    this.panelSel = current >= 0 ? current : 0;
    await this.write([
      ...this.closeDetailsWrite(),
      { path: "panel", value: this.pickerState() },
      { path: "status", value: "Sessions · enter loads · esc closes" },
    ]);
  }

  /**
   * Loads `handle` as the live conversation. Refused while busy, queued
   * or approving; the draft is kept, the queue is already empty (refused
   * above), and session grants survive — they are this process's trust,
   * not the saved session's — which the confirming status says.
   */
  private async switchSession(handle: string): Promise<void> {
    const capability = this.options.sessions;
    if (!capability) return;
    if (
      this.switching ||
      this.busy ||
      this.proposal ||
      this.queued !== undefined
    ) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    if (handle === this.session?.state.publicId) {
      await this.write([
        ...this.closePanelWrite(),
        { path: "status", value: `Already in session ${handle}` },
      ]);
      return;
    }
    // Latched synchronously before the load: sends are refused while it
    // is in flight, so no turn can start under the swap.
    this.switching = true;
    try {
      await this.switchSessionLoaded(capability, handle);
    } finally {
      this.switching = false;
    }
  }

  private async switchSessionLoaded(
    capability: SessionsCapability,
    handle: string,
  ): Promise<void> {
    let context: SessionContext;
    try {
      context = await capability.load(handle);
    } catch (error) {
      await this.write([
        ...this.closePanelWrite(),
        {
          path: "status",
          value: oneLine(
            error instanceof Error ? error.message : "Unable to load session",
          ),
        },
      ]);
      return;
    }
    const grants = this.grantLabels();
    await this.adoptConversation(
      context,
      `Resumed session ${context.state.publicId} · ${context.state.messages.length} messages` +
        (grants.length ? ` · grants kept: ${grants.join(" · ")}` : ""),
    );
  }

  /**
   * `/sessions new`: the conversation starts over, as a new saved session
   * when the process has a store, else as a new temporary one. Refused
   * while busy, queued or approving, like a switch; grants survive it
   * the same way (they are this process's trust), and the status says so.
   */
  private async newSession(): Promise<void> {
    if (
      this.switching ||
      this.busy ||
      this.proposal ||
      this.queued !== undefined
    ) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    this.switching = true;
    try {
      const create = this.options.sessions?.create;
      let context: SessionContext | undefined;
      if (create)
        try {
          context = await create();
        } catch (error) {
          await this.write([
            ...this.closePanelWrite(),
            {
              path: "status",
              value: oneLine(
                error instanceof Error
                  ? error.message
                  : "Unable to start a session",
              ),
            },
          ]);
          return;
        }
      const grants = this.grantLabels();
      await this.adoptConversation(
        context,
        (context
          ? `New session ${context.state.publicId}`
          : "New conversation (temporary: not saved)") +
          (grants.length ? ` · grants kept: ${grants.join(" · ")}` : ""),
      );
    } finally {
      this.switching = false;
    }
  }

  /**
   * `/goal …`: shows the running goal, stops it (cancelling its turn), or
   * starts one in build mode, its first turn sent at once.
   */
  private async runGoal(command: GoalCommand): Promise<void> {
    const goal = this.goal;
    if (command.kind === "error") {
      await this.write([{ path: "status", value: command.status }]);
      return;
    }
    if (command.kind === "show") {
      if (!goal) {
        await this.write([{ path: "status", value: NO_GOAL_STATUS }]);
        return;
      }
      // A notice, not the status line: a running turn's ticker would
      // paint over it at once.
      const view = this.builder.notice(goal.describe(this.now()));
      this.transcript.push(view);
      if (this.inline) await this.commitView(view);
      await this.publishMessages([{ path: "status", value: "Goal" }]);
      return;
    }
    if (command.kind === "stop") {
      if (!goal) {
        await this.write([{ path: "status", value: NO_GOAL_STATUS }]);
        return;
      }
      // The goal stops when its turn has ended (see settleGoal).
      this.goalStop = "stopped: you ended it (/goal stop)";
      this.turn?.abort();
      await this.write([{ path: "status", value: "Stopping the goal…" }]);
      return;
    }
    if (goal) {
      await this.write([
        {
          path: "status",
          value: "A goal is running: /goal shows it, /goal stop ends it",
        },
      ]);
      return;
    }
    if (
      this.switching ||
      this.busy ||
      this.proposal ||
      this.queued !== undefined
    ) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    if (this.intent !== "build") {
      await this.write([
        {
          path: "status",
          value: `A goal runs in build mode, not ${this.intent}: /mode build first`,
        },
      ]);
      return;
    }
    this.goal = new GoalRun(command.objective, command.turns, this.now());
    this.goalStop = undefined;
    await this.beginTurn(this.goal.nextMessage(), true);
  }

  /**
   * After each turn while a goal runs: the stop rules (goal.ts) on what
   * the turn did. A goal that stops leaves a notice after the turn and
   * returns the status line's words; one that goes on sets goalContinues.
   */
  private async settleGoal(): Promise<string | undefined> {
    const goal = this.goal;
    const turn = this.goalTurn;
    this.goalTurn = undefined;
    this.goalContinues = false;
    if (!goal || !turn) return undefined;
    // The turn's record counts (its report is the last status) even when
    // the user's stop is the reason.
    const ruled = goal.afterTurn(turn, {
      now: this.now(),
      historyLeft: MAX_HISTORY_MESSAGES - 1 - this.history.length,
      build: this.intent === "build",
    });
    const reason = this.goalStop ?? ruled?.reason;
    if (reason === undefined) {
      this.goalContinues = true;
      return undefined;
    }
    return this.endGoal(goal, reason);
  }

  /** Ends `goal` for `reason`: a notice after its turn, and the status line's words. */
  private async endGoal(goal: GoalRun, reason: string): Promise<string> {
    this.goal = undefined;
    this.goalStop = undefined;
    this.goalContinues = false;
    const line = `Goal ${reason}`;
    const last = goal.last
      ? `\nLast status (${goal.last.state}): ${oneLine(goal.last.summary)}`
      : "";
    const view = this.builder.notice(
      `${line} · turn ${goal.turnsUsed} of ${goal.turnsMax}${last}`,
    );
    this.transcript.push(view);
    if (this.inline) await this.commitView(view);
    return oneLine(line);
  }

  /**
   * Makes `context` (undefined: a temporary conversation) the live one,
   * with `status`: history, transcript, builder and the checkpoint target
   * change together, so the next turn can only continue that session.
   */
  private async adoptConversation(
    context: SessionContext | undefined,
    status: string,
  ): Promise<void> {
    // Recheck after the await: if a turn somehow started anyway, the
    // swap aborts whole instead of mutating history under it.
    if (this.busy || this.proposal || this.queued !== undefined) {
      await this.write([
        ...this.closePanelWrite(),
        { path: "status", value: BUSY_STATUS },
      ]);
      return;
    }
    // The old session's uncommitted reply is flushed (or spilled, ordered
    // before the loaded history's commits) before the swap, never after.
    if (this.inline) await this.commitStreamTail();
    if (this.inline && this.stream.hasUncommitted) this.spillStream();
    this.stream.reset();
    this.headCommitted = false;
    // The swap is whole and synchronous from here: history, transcript,
    // builder and the checkpoint target change together, so the next
    // turn can only continue (and checkpoint) the loaded session.
    this.session = context;
    this.windowNoticed = 0;
    this.history.length = 0;
    this.history.push(...(context?.state.messages ?? []));
    // Numbering goes on: a card of the session left behind (inline, it is
    // in the scrollback) never names a receipt of this one.
    this.firstReceipt = this.builder.nextReceiptNumber;
    this.builder = new ViewBuilder(this.tableCols, this.firstReceipt);
    const views: MessageView[] = [];
    this.receipts.length = 0;
    this.receiptChars = 0;
    this.sent.length = 0;
    this.recall = undefined;
    this.recalledFrom = "";
    for (const message of this.history) {
      const view = this.builder.fromMessage(message);
      if (view) views.push(view);
      const receipt = this.builder.receiptOf(message);
      if (receipt) this.keepReceipt(receipt);
      if (message.role === "user" && message.content.trim())
        this.sent.push(message.content);
    }
    this.transcript.replace(views);
    this.ctx.reset();
    this.lastTurn = undefined;
    const closed = this.closePanelWrite();
    // Inline, the loaded history joins the scrollback like a resumed
    // session's does at attach; the region itself holds no history.
    if (this.inline) for (const view of views) await this.commitView(view);
    await this.publishMessages([
      ...closed,
      ...this.closeDetailsWrite(),
      { path: "empty", value: views.length === 0 },
      {
        path: "header.session",
        value: context ? `session ${context.state.publicId}` : "temporary",
      },
      ...this.ctxWrites(0),
      { path: "status", value: status },
    ]);
  }

  /**
   * `/export [json]`: the conversation to a timestamped file — the
   * rendered views as markdown (default), or the raw messages as JSON.
   */
  private async exportTranscript(format: "markdown" | "json"): Promise<void> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const views = this.transcript.all();
    if (views.length === 0) {
      await this.write([{ path: "status", value: "Nothing to export yet" }]);
      return;
    }
    const root = this.options.workspace?.root ?? "";
    const write =
      this.options.writeExport ??
      ((path: string, text: string) =>
        writeFile(path, text, { flag: "wx", mode: 0o600 }).then(() => {}));
    const ext = format === "json" ? ".json" : ".md";
    const text =
      format === "json"
        ? transcriptJson(this.history)
        : transcriptMarkdown(views);
    const base = exportFileName(new Date(this.now()), ext);
    const stem = base.slice(0, -ext.length);
    try {
      // Never overwrite: the timestamped name first, then -2, -3, …
      for (let suffix = 1; suffix <= 100; suffix++) {
        const name = suffix === 1 ? base : `${stem}-${suffix}${ext}`;
        try {
          await write(join(root, name), text);
          await this.write([
            { path: "status", value: `Exported transcript to ${name}` },
          ]);
          return;
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
        }
      }
      throw new Error("export file name taken after 100 attempts");
    } catch (error) {
      const message = error instanceof Error ? error.message : "Export failed";
      const notice = this.builder.error(`Export failed: ${message}`);
      this.transcript.push(notice);
      if (this.inline) await this.commitView(notice);
      await this.publishWindow([
        { path: "status", value: oneLine(`Export failed: ${message}`) },
      ]);
    }
  }

  /**
   * `/model NAME`: the runtime's admission check proves the model exists
   * at the endpoint (and carries no remote redirect) before the swap; only
   * then does the provider the next turn runs on change, with the header.
   */
  private async switchModel(name: string): Promise<boolean> {
    if (this.busy || this.proposal || this.queued !== undefined) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return false;
    }
    await this.write([
      { path: "status", value: `Switching model to ${name}…` },
    ]);
    const admit =
      this.options.admitModel ??
      (this.provider.type === "openai_local"
        ? admitOpenAILocalModel
        : admitOllamaModel);
    // A size the user did not set is planned again for the new model
    // (a different load anyway), so admission is asked only for its facts.
    const planned =
      this.provider.type === "ollama" && !this.provider.context_explicit;
    let admission: OllamaAdmission | OpenAILocalAdmission;
    try {
      admission = await admit(
        {
          ...this.provider,
          model: name,
          ...(planned ? { context_size: 512 } : {}),
        },
        this.options.signal,
      );
    } catch (error) {
      await this.write([
        {
          path: "status",
          value: oneLine(
            error instanceof Error ? error.message : "Model check failed",
          ),
        },
      ]);
      return false;
    }
    // The process is quitting: nothing is switched.
    if (this.options.signal.aborted) return false;
    // An OpenAI-compatible server that answers without a model list is
    // taken at its word, as at launch: a chat request fails loudly on a
    // model it does not serve, and the status says the name was not
    // checked. One that cannot be reached is no answer: a misspelled name
    // would wait for the next turn to fail.
    const unchecked =
      admission.status === "unverified" && admission.kind === "unlisted"
        ? admission.message
        : undefined;
    if (admission.status !== "validated" && unchecked === undefined) {
      await this.write([{ path: "status", value: oneLine(admission.message) }]);
      return false;
    }
    const model = admission.status === "validated" ? admission.model : name;
    // Only Ollama's inventory says how big a model is.
    const facts = sized(admission)
      ? {
          nativeContext: admission.nativeContext,
          weightsBytes: admission.sizeBytes,
          kvBytesPerToken: admission.kvBytesPerToken,
          totalMemory: this.options.totalMemory?.() ?? totalmem(),
          mlx: admission.mlx,
        }
      : undefined;
    // A size the user set stays: its note (and warning) is for this model.
    const plan = planned && facts ? planContextSize(facts) : undefined;
    const note =
      !planned && this.provider.type === "ollama" && facts
        ? explicitContextNote(facts, this.provider.context_size)
        : undefined;
    // A planned size warns only on Ollama's MLX runner.
    const warning = plan?.warning ?? note?.warning;
    // A local model's warning is for its own size; Chalupa's size, and so
    // its warning, stays with the profile.
    const { context_warning: _, ...rest } = this.provider;
    const previous = this.provider.type === "ollama" ? rest : this.provider;
    // A warning still on its way is the previous model's.
    this.placement = undefined;
    this.provider = {
      ...previous,
      model,
      ...(plan
        ? { context_size: plan.context, context_plan: plan.reason }
        : {}),
      ...(note ? { context_plan: note.reason } : {}),
      ...(warning ? { context_warning: warning } : {}),
    };
    const said = [warning, unchecked].filter(Boolean).join(" · ");
    await this.write([
      {
        path: "status",
        value: said
          ? oneLine(`Model changed to ${model} · ${said}`)
          : `Model changed to ${model}`,
      },
      { path: "header.model", value: clip(cleanText(model), 40) },
      ...this.ctxWrites(this.ctx.current),
    ]);
    return true;
  }

  /** `/find TEXT`: the workspace's saved sessions searched, hits in a panel. */
  private async findSessions(query: string): Promise<void> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const search = this.options.sessions?.search;
    if (!search) {
      await this.write([
        { path: "status", value: "Find needs --session-db PATH at startup" },
      ]);
      return;
    }
    let hits: FindHit[];
    try {
      hits = await search(query);
    } catch (error) {
      await this.write([
        {
          path: "status",
          value: oneLine(
            error instanceof Error
              ? error.message
              : "Unable to search sessions",
          ),
        },
      ]);
      return;
    }
    if (hits.length === 0) {
      await this.write([
        { path: "status", value: `No matches for "${cleanText(query)}"` },
      ]);
      return;
    }
    const text = hits.map((hit) => `${hit.handle} · ${hit.snippet}`).join("\n");
    await this.openPanel(
      "Find",
      query,
      text,
      `${hits.length} session${hits.length === 1 ? "" : "s"} match "${cleanText(query)}"`,
    );
  }

  private approvalFor(proposal: ApprovalRequest) {
    return approvalState(
      proposal,
      this.options.columns?.() ?? 80,
      this.options.workspace.root,
      this.inline,
      this.sandbox,
    );
  }

  /** Aborts the running turn, if any. */
  abort(): void {
    this.turn?.abort();
  }

  /** Resolves when no turn is running. Await it before closing the session store. */
  async idle(): Promise<void> {
    await this.turnDone;
    await this.tail;
  }

  // ------------------------------------------------------------------ the writer

  /** Records a refused write or commit, saying so once on the status line. */
  private async noteFailure(failure: string): Promise<void> {
    const ui = this.ui;
    if (!ui) return;
    this.writeErrors.push(failure);
    if (this.writeErrors.length > MAX_WRITE_ERRORS) this.writeErrors.shift();
    // Say so once on the status line, so a frozen view is not silent.
    if (!this.reportedWriteError) {
      this.reportedWriteError = true;
      const status = `UI update failed: ${terminalText(failure).replace(/\s+/g, " ").slice(0, 200)}`;
      await ui.batch([{ path: "status", value: status }]).catch(() => {});
    }
  }

  /**
   * One serialized write. Never rejects: a refused write is recorded in
   * `writeErrors` and resolves false.
   */
  private write(writes: Write[]): Promise<boolean> {
    for (const write of writes)
      if (write.path === "draft" && typeof write.value === "string")
        this.draftNow = write.value;
    const ui = this.ui;
    if (!ui) return Promise.resolve(true);
    let ok = true;
    const next = this.tail.then(async () => {
      let failure: string | undefined;
      try {
        const ack = await ui.batch(writes);
        if (!ack.ok) failure = ack.error ?? "write refused";
      } catch (error) {
        failure = error instanceof Error ? error.message : String(error);
      }
      if (failure === undefined) {
        this.reportedWriteError = false;
        return;
      }
      ok = false;
      // A refused head is not what the store shows: the next sync sends it
      // again, whichever path wrote it (a tick, a phase, a publish).
      if (writes.some((write) => write.path === "head")) {
        this.headShown = "";
        this.headSeconds = -1;
      }
      await this.noteFailure(failure);
    });
    this.tail = next;
    return next.then(() => ok);
  }

  /** One commit on the same chain as the writes, so print order is write order. */
  private enqueueCommit(commit: Commit): Promise<boolean> {
    const ui = this.ui;
    if (!ui) return Promise.resolve(true);
    let ok = true;
    const next = this.tail.then(async () => {
      let failure: string | undefined;
      try {
        const ack = await ui.commit(commit);
        if (!ack.ok) failure = ack.error ?? "commit refused";
      } catch (error) {
        failure = error instanceof Error ? error.message : String(error);
      }
      if (failure === undefined) {
        this.reportedWriteError = false;
        return;
      }
      ok = false;
      await this.noteFailure(failure);
    });
    this.tail = next;
    return next.then(() => ok);
  }

  /**
   * Runs one commit job at a time. Every commit rewrites the same scratch
   * path (`commit_buf` or `commit_blocks`) right before printing from it, so
   * two jobs must never interleave their set and their commit.
   */
  private serializeCommit<T>(task: () => Promise<T>): Promise<T> {
    const run = this.commitChain.then(task, task);
    this.commitChain = run.then(
      () => {},
      () => {},
    );
    return run;
  }

  private pushSpill(entry: Spill): void {
    this.spill.push(entry);
    if (this.spill.length > MAX_SPILL) {
      this.spill.shift();
      this.spillDropped++;
      this.writeErrors.push(
        `spill queue overflow: dropped oldest refused print (${this.spillDropped} dropped)`,
      );
      if (this.writeErrors.length > MAX_WRITE_ERRORS) this.writeErrors.shift();
    }
  }

  /**
   * Retries refused prints in their original order, stopping at the first
   * refusal so nothing newer can overtake them. Concurrent calls share
   * the in-flight flush.
   */
  private flushSpill(): Promise<void> {
    if (this.spillFlush) return this.spillFlush;
    if (this.spill.length === 0) return Promise.resolve();
    const run = (async () => {
      while (this.spill.length > 0) {
        const entry = this.spill[0] as Spill;
        const ok = await this.serializeCommit(() =>
          entry.kind === "view"
            ? this.printView(entry.view)
            : entry.kind === "links"
              ? this.printLinks(entry.view)
              : entry.head
                ? this.printView(this.assistantView(entry.blocks))
                : this.printBlocks(entry.blocks),
        );
        if (!ok) return;
        this.spill.shift();
      }
    })();
    this.spillFlush = run.then(
      () => {
        this.spillFlush = undefined;
      },
      () => {
        this.spillFlush = undefined;
      },
    );
    return this.spillFlush;
  }

  /** Snapshots the stream's uncommitted blocks into the spill queue. */
  private spillStream(): boolean {
    const blocks = this.stream.finishTail();
    if (blocks.length === 0) return false;
    this.pushSpill({ kind: "blocks", blocks, head: !this.headCommitted });
    this.stream.reset();
    this.headCommitted = false;
    return true;
  }

  /** Prints a streamed reply's sources row once its message has closed. */
  private async commitLinks(view: MessageView): Promise<boolean> {
    await this.flushSpill();
    if (this.spill.length > 0) {
      this.pushSpill({ kind: "links", view });
      return false;
    }
    const ok = await this.serializeCommit(() => this.printLinks(view));
    if (!ok) this.pushSpill({ kind: "links", view });
    return ok;
  }

  /** Prints one finished view (a user turn, a tool receipt, history). */
  private async commitView(view: MessageView): Promise<boolean> {
    await this.flushSpill();
    if (this.spill.length > 0) {
      this.pushSpill({ kind: "view", view });
      return false;
    }
    const ok = await this.serializeCommit(() => this.printView(view));
    if (!ok) this.pushSpill({ kind: "view", view });
    return ok;
  }

  private async printView(view: MessageView): Promise<boolean> {
    if (!(await this.write([{ path: "commit_buf", value: view }])))
      return false;
    const ok = await this.enqueueCommit({
      template: "la-turn",
      with: "commit_buf as msg",
    });
    if (ok) await this.write([{ path: "commit_buf", value: null }]);
    return ok;
  }

  /**
   * Prints only a view's sources row: inline, a reply's text is already in the
   * scrollback block by block, so its links are printed once when the message
   * closes (the body's [n] markers came with the streamed blocks).
   */
  private async printLinks(view: MessageView): Promise<boolean> {
    if (!(await this.write([{ path: "commit_buf", value: view }])))
      return false;
    const ok = await this.enqueueCommit({
      template: "la-media",
      with: "commit_buf as lm",
    });
    if (ok) await this.write([{ path: "commit_buf", value: null }]);
    return ok;
  }

  private async printBlocks(blocks: Block[]): Promise<boolean> {
    if (!(await this.write([{ path: "commit_blocks", value: blocks }])))
      return false;
    const ok = await this.enqueueCommit({
      template: "la-blocks",
      with: "commit_blocks as blocks",
    });
    if (ok) await this.write([{ path: "commit_blocks", value: [] }]);
    return ok;
  }

  /** The first printed blocks of a reply, as the turn that carries its label. */
  private assistantView(blocks: Block[]): MessageView {
    return {
      id: "live",
      is_user: false,
      is_asst: true,
      is_tool: false,
      is_notice: false,
      is_error: false,
      blocks,
      card: NO_CARD,
      links: [],
      images: [],
      images_more: 0,
    };
  }

  /**
   * Prints blocks the stream offered, confirming them only after the ack.
   * The first blocks of a reply go as its labeled turn, later ones as plain
   * blocks under it. A refused commit hands the blocks back: they stay in
   * the live region and are offered again, never printed twice.
   */
  private commitOffered(blocks: Block[]): Promise<boolean> {
    return this.serializeCommit(async () => {
      const ok = this.headCommitted
        ? await this.printBlocks(blocks)
        : await this.printView(this.assistantView(blocks));
      if (!ok) {
        this.stream.releaseFinal(blocks.length);
        return false;
      }
      this.headCommitted = true;
      const clear = this.stream.confirmCommitted(blocks.length);
      if (clear.length > 0 && !(await this.write(clear)))
        this.stream.markStale();
      return true;
    });
  }

  /** Prints the reply's newly final blocks, as they close. */
  private async commitFinals(): Promise<void> {
    await this.flushSpill();
    if (this.spill.length > 0) return;
    const blocks = this.stream.takeFinal();
    if (blocks.length > 0) await this.commitOffered(blocks);
  }

  /**
   * Prints everything the running reply still owes the scrollback, tail
   * included. True when the reply had anything at all (printed or refused),
   * so a caller knows the finished view must not be printed over it. A
   * refused tail is spilled (its blocks leave the stream with their order
   * and label kept) instead of being dropped by a later reset. Always
   * joins `commitChain`, so a caller never races an in-flight commit.
   */
  private async commitStreamTail(): Promise<boolean> {
    await this.flushSpill();
    if (this.spill.length > 0) {
      const hadHead = this.headCommitted;
      const spilled = this.spillStream();
      await this.serializeCommit(async () => {});
      return spilled || hadHead;
    }
    const blocks = this.stream.finishTail();
    if (blocks.length === 0) {
      await this.serializeCommit(async () => {});
      return this.headCommitted;
    }
    const ok = await this.commitOffered(blocks);
    if (!ok) {
      // commitOffered handed the blocks back to the stream; snapshot them.
      this.spillStream();
      return true;
    }
    return true;
  }

  /** Only the window of the transcript: the live message is left alone. */
  private async publishWindow(extra: Write[] = []): Promise<void> {
    if (this.inline) {
      // The region never holds history: finished views are already printed.
      if (extra.length > 0) await this.write(extra);
      return;
    }
    const { messages, earlier } = this.transcript.snapshot();
    await this.write([
      { path: "messages", value: messages },
      { path: "earlier", value: earlier },
      ...extra,
    ]);
  }

  /** The window plus the end of whatever was streaming: a later tick must not resend it. */
  private async publishMessages(extra: Write[] = []): Promise<void> {
    // Inline, blocks whose commit was refused are snapshotted into the
    // spill queue (retried before the next print) instead of being reset
    // away; the live region then clears like any finished reply.
    if (this.inline && this.stream.hasUncommitted) this.spillStream();
    else {
      this.stream.reset();
      this.headCommitted = false;
    }
    this.liveTool = undefined;
    this.dirty = false;
    await this.publishWindow([
      { path: "live", value: IDLE_LIVE },
      { path: "thinking", value: IDLE_THINKING },
      // The tool card is gone (or the turn is over): the head follows.
      ...this.headWrites(),
      ...extra,
    ]);
  }

  /**
   * The writes that bring the live message and the reasoning up to date: a tool
   * call is one value; a reply is the stream's appends (see stream.ts).
   */
  private liveWrites(): Write[] {
    if (this.liveTool) {
      if (!this.dirty) return [];
      this.dirty = false;
      return [
        { path: "live", value: this.liveTool },
        { path: "thinking", value: IDLE_THINKING },
        // A tool card stands in for the head (unless the turn waits on the person).
        ...this.headWrites(),
      ];
    }
    return this.stream.drain();
  }

  /** Writes what changed in the live message, and makes a refused write good. */
  private async sendLive(): Promise<void> {
    const writes = this.liveWrites();
    if (writes.length > 0) {
      const ok = await this.write(writes);
      if (!ok) {
        // The store is not what the stream thinks it is: send it all again.
        this.stream.markStale();
        if (this.liveTool) this.dirty = true;
      }
    }
    // Inline, blocks that just closed leave the region for the scrollback.
    if (this.inline && !this.liveTool) await this.commitFinals();
  }

  private now(): number {
    return this.options.now?.() ?? Date.now();
  }

  /** The turn's own seconds: the wait on the person (an approval, an answer) is not counted. */
  private turnSeconds(): number {
    const end = this.pausedAt ?? this.now();
    return Math.floor((end - this.turnStartedAt - this.pausedMs) / 1000);
  }

  /** The turn waits on the person: its elapsed time stops. */
  private pauseClock(): void {
    this.pausedAt ??= this.now();
  }

  /** The person answered: the elapsed time goes on from where it stopped. */
  private resumeClock(): void {
    if (this.pausedAt === undefined) return;
    this.pausedMs += this.now() - this.pausedAt;
    this.pausedAt = undefined;
  }

  /**
   * The head of the live assistant message, from the turn's own state: it
   * waits on the person (static glyph) while an approval or a question is
   * open, hides while a tool card stands in for it, and otherwise says
   * what the model is doing. The phase and the elapsed time are the only
   * text; neither is written per frame (see maybeElapsed).
   */
  private headView(): HeadView {
    if (!this.headOn) return NO_HEAD;
    const seconds = this.turnSeconds();
    if (this.decide) return headState("approval", seconds);
    if (this.asking) return headState("answer", seconds);
    if (this.liveTool || this.phase === "between") return NO_HEAD;
    return headState(this.phase, seconds);
  }

  /** The write that brings the head up to date, or none when it already is. */
  private headWrites(): Write[] {
    const view = this.headView();
    const shown = JSON.stringify(view);
    this.headSeconds = this.headOn ? this.turnSeconds() : -1;
    if (shown === this.headShown) return [];
    this.headShown = shown;
    return [{ path: "head", value: view }];
  }

  /** Writes the head now if it changed. Not awaited: the writer's tail orders it. */
  private syncHead(): void {
    const writes = this.headWrites();
    if (writes.length === 0) return;
    void this.write(writes);
  }

  /** What the head says the turn is doing; a change is written at once. */
  private setPhase(phase: Phase): void {
    if (this.phase === phase) return;
    this.phase = phase;
    this.syncHead();
  }

  /**
   * A transient status over the ambient one: a verdict, or a rule's note.
   * It stays until the next state change (a tool result, the turn's end).
   * An open approval owns the status line, so it wins; an open question
   * likewise. Not awaited by the event loop: the write serializes on the
   * writer's tail like every other, and event processing must not pause
   * for a status paint (an abort that lands in such a pause can pass a
   * generator still suspended at its yield).
   */
  private flash(text: string): void {
    this.flashed = true;
    if (!this.decide && !this.asking)
      void this.write([{ path: "status", value: text }]);
  }

  /** A tool finished: a transient status of this turn gives way to the ambient one. */
  private restoreStatus(): void {
    if (!this.flashed) return;
    this.flashed = false;
    if (!this.decide && !this.asking)
      void this.write([{ path: "status", value: this.idleText() }]);
  }

  /**
   * The write that takes a tool card off hold once its approval or question
   * is answered: running if the call goes on (its status is the running
   * one, not "waiting for approval" until the next event), otherwise as it
   * was. None when the card is gone (the turn ended).
   */
  private releaseCard(before: Card | undefined, running: boolean): Write[] {
    if (!before || !this.liveTool) return [];
    this.liveTool = {
      ...this.liveTool,
      card: running ? { ...before, status: "running", running: true } : before,
    };
    this.dirty = false;
    return [{ path: "live", value: this.liveTool }];
  }

  /** What the ambient status keeps of the turn that just ended: how long, how fast. */
  private recordTurn(): void {
    this.lastTurn = {
      seconds: this.turnSeconds(),
      speed: tokensPerSecond(this.evalCount, this.evalDuration),
    };
  }

  /** The writes that paint the header ctx for `used` tokens. */
  private ctxWrites(used: number): Write[] {
    const size = this.provider.context_size;
    const pct = contextPct(used, size);
    const flags = ctxFlags(pct);
    return [
      { path: "header.ctx_label", value: contextLabel(used, size) },
      { path: "header.ctx_pct", value: pct },
      { path: "header.ctx_warn", value: flags.warn },
      { path: "header.ctx_danger", value: flags.danger },
    ];
  }

  /**
   * Rewrites the head when its elapsed second changes (about once a
   * second, from the flush timer). Idle writes nothing; the head's other
   * changes (a phase, an approval) write themselves when they happen.
   */
  private async maybeElapsed(): Promise<void> {
    if (!this.busy || !this.headOn) return;
    if (this.turnSeconds() === this.headSeconds) return;
    const writes = this.headWrites();
    if (writes.length > 0) await this.write(writes);
  }

  /** Flushes the live message if it changed and no flush is in flight. */
  private tick(): void {
    void this.maybeElapsed();
    if (this.liveInFlight) return;
    if (!this.dirty && !this.stream.dirty) return;
    this.liveInFlight = true;
    void this.sendLive().finally(() => {
      this.liveInFlight = false;
    });
  }

  /** Test hook: flush the live message now and wait for the writer. */
  async flush(): Promise<void> {
    await this.maybeElapsed();
    await this.sendLive();
    await this.tail;
  }

  /**
   * The staged quit, shared by ctrl+c and the palette's Quit row: a
   * non-empty draft is cleared first; then a running turn is cancelled,
   * as esc does, so one press never ends the session mid-turn; only the
   * next press quits. Answering "quit" ends the session.
   */
  private quitRequest(): Promise<"quit" | undefined> {
    // Concurrent presses serialize: each decides its stage from the
    // draft as the previous press left it, so a second press during the
    // first's draft-clear still counts as the second stage. The quit
    // stage itself is memoized — concurrent callers share one outcome,
    // resolved only after its tail commit lands.
    if (this.quitPromise) return this.quitPromise;
    const run = this.quitChain.then(() => this.quitOnce());
    this.quitChain = run.then(
      () => {},
      () => {},
    );
    return run;
  }

  private async quitOnce(): Promise<"quit" | undefined> {
    if (this.quitPromise) return this.quitPromise;
    const ui = this.ui;
    if (!ui) return undefined;
    const draft = await ui
      .get("draft")
      .then((result) => result.value)
      .catch(() => "");
    if (typeof draft === "string" && draft !== "") {
      await this.write([
        { path: "draft", value: "" },
        ...this.closeSlash(),
        { path: "status", value: "Draft cleared · ctrl+c again to quit" },
      ]);
      return undefined;
    }
    // A running turn is cancelled as esc does, once: a press while it
    // winds down (cancelled by esc or by ctrl+c) quits.
    if (this.turn && !this.turn.signal.aborted) {
      this.quitCancelled = this.turn;
      const queued = this.takeQueued();
      if (queued) this.cancelReturnedQueue = true;
      if (this.goal) this.goalStop ??= "stopped: you cancelled its turn";
      this.turn.abort();
      await this.write([
        ...(queued ? [queued] : []),
        {
          path: "status",
          value: queued
            ? `Turn cancelled · ${QUEUE_RETURNED}`
            : "Turn cancelled · ctrl+c again to quit",
        },
      ]);
      return undefined;
    }
    // Between a goal's turns none runs, but the goal does: the press stops
    // it before its next turn, and only the next press quits. A send queued
    // for after the turn is not sent either.
    if (this.goal && this.goalStop === undefined) {
      this.goalStop = "stopped: you cancelled it";
      const queued = this.takeQueued();
      await this.write([
        ...(queued ? [queued] : []),
        {
          path: "status",
          value: queued
            ? `Goal stopping · ${QUEUE_RETURNED}`
            : "Goal stopping · ctrl+c again to quit",
        },
      ]);
      return undefined;
    }
    // A turn winding down (cancelled already) can have a send queued
    // behind it: it goes back to the composer before a press can quit.
    if (this.queued !== undefined) {
      const queued = this.takeQueued();
      await this.write([
        ...(queued ? [queued] : []),
        { path: "status", value: "Queued message back in the composer" },
      ]);
      return undefined;
    }
    const quit = (async (): Promise<"quit"> => {
      this.quitting = true;
      this.queued = undefined;
      // Inline, the partial reply is printed before the session ends: once
      // "quit" is returned the host is gone and nothing can be committed.
      if (this.inline) {
        await this.commitStreamTail();
        await this.flushSpill();
      }
      this.abort();
      return "quit";
    })();
    this.quitPromise = quit;
    return quit;
  }

  // --------------------------------------------------------------- the `/` popup
  //
  // The popup is derived state: the composer's `on:change` carries the new
  // draft on every edit, and the popup is open exactly while that draft is
  // a half-typed command (see slashMatches) with at least one match — and
  // esc has not dismissed it for this draft, and no approval is open (the
  // composer is disabled then inline, and approval force-closes it
  // fullscreen; the guard here keeps a stray edit from reopening it).
  // Completion: tab always completes the chosen row into the draft.
  // Enter completes only while the popup is open AND the chosen row's
  // completion differs from what was typed ("/mo" -> "/mode "); when the
  // typed text already is the chosen command ("/mcp"), enter sends it.

  /** The writes that close the popup, or none when it is already closed. */
  private closeSlash(): Write[] {
    this.slashOpen = false;
    this.slashSel = 0;
    const sig = "[]";
    if (this.slashSig === sig) return [];
    this.slashSig = sig;
    return [{ path: "slash", value: { open: false, rows: [] } }];
  }

  /**
   * Recomputes the popup for `draft` and returns the write it needs (none
   * when nothing changed: most keystrokes). Typing resets the choice to
   * the first match unless `keepSel` (the arrow keys moving it).
   */
  private syncSlash(draft: string, keepSel = false): Write[] {
    if (!draft.startsWith("/")) this.slashDismissed = false;
    const matches =
      this.proposal || this.slashDismissed ? undefined : slashMatches(draft);
    if (!matches || matches.length === 0) return this.closeSlash();
    this.slashOpen = true;
    this.slashSel = keepSel
      ? Math.max(0, Math.min(this.slashSel, matches.length - 1))
      : 0;
    const rows = slashRows(matches, this.slashSel);
    const sig = JSON.stringify(rows);
    if (this.slashSig === sig) return [];
    this.slashSig = sig;
    return [{ path: "slash", value: { open: true, rows } }];
  }

  /** The chosen command for a draft, when the popup is open over it. */
  private slashChoice(draft: string) {
    if (!this.slashOpen) return undefined;
    const matches = slashMatches(draft);
    if (!matches || matches.length === 0) return undefined;
    return matches[Math.min(this.slashSel, matches.length - 1)];
  }

  private async onTyped(ev: HostEvent): Promise<void> {
    const draft = typeof ev.value === "string" ? ev.value : "";
    this.draftNow = draft;
    // An answer is not a command: no popup while a question is open.
    if (this.asking) return;
    const writes = this.syncSlash(draft);
    if (writes.length > 0) await this.write(writes);
  }

  /** Up and down while the popup is open: the choice moves, clamped. */
  private async slashMove(ev: HostEvent, by: number): Promise<void> {
    const draft = typeof ev.value === "string" ? ev.value : "";
    const matches = slashMatches(draft);
    if (!matches || matches.length === 0) return;
    this.slashSel = Math.max(
      0,
      Math.min(this.slashSel + by, matches.length - 1),
    );
    const writes = this.syncSlash(draft, true);
    if (writes.length > 0) await this.write(writes);
  }

  /** Tab: the chosen command replaces the half-typed draft; popup closes. */
  private async slashComplete(ev: HostEvent): Promise<void> {
    const draft = typeof ev.value === "string" ? ev.value : "";
    const choice = this.slashChoice(draft);
    if (!choice) return;
    await this.write([
      { path: "draft", value: choice.insert },
      ...this.closeSlash(),
    ]);
  }

  /** Esc while the popup is open: it closes, the draft is untouched. */
  private async slashEsc(): Promise<void> {
    this.slashDismissed = true;
    const writes = this.closeSlash();
    if (writes.length > 0) await this.write(writes);
  }

  /**
   * Shift+tab: build -> plan -> chat -> build, through the same `/mode`
   * path, so the header and the status change exactly as the command
   * changes them. Refused while a turn runs, as `/mode` is; a queued
   * message therefore still starts under the mode in force when its turn
   * begins.
   */
  private async modeToggle(): Promise<void> {
    if (this.proposal) return;
    if (this.busy || this.switching) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const next =
      this.intent === "build"
        ? "plan"
        : this.intent === "plan"
          ? "chat"
          : "build";
    await this.runCommand(`/mode ${next}`);
  }

  // --------------------------------------------------------------------- actions

  private async onSend(ev: HostEvent): Promise<void> {
    if (this.switching) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const raw = typeof ev.value === "string" ? ev.value : "";
    // Past what a message holds, a send (an answer, a goal) is refused here
    // with the draft kept: in the history it would fail every later turn.
    // Checked without an await, so a send that fits starts in this tick.
    const tooLong = this.tooLong(raw.length);
    if (tooLong) {
      await this.write([tooLong]);
      return;
    }
    // An open question takes the send: it is the answer, never a message,
    // a command or the queue. `/goal` and `/goal stop` alone stay commands.
    if (this.asking) {
      if (isGoalCommand(raw)) await this.runCommand(raw.trim(), true);
      else await this.answerSend(raw);
      return;
    }
    // Enter over an open popup completes instead of sending when the
    // chosen row's completion differs from the typed text; a fully
    // typed command ("/mcp") sends. See the popup rules above.
    const choice = this.slashChoice(raw);
    if (choice && choice.insert !== raw) {
      await this.write([
        { path: "draft", value: choice.insert },
        ...this.closeSlash(),
      ]);
      return;
    }
    // Inline, the composer owns enter even under the picker (fullscreen
    // binds it on the modal instead): an enter over an open picker loads
    // the chosen session and leaves the draft alone.
    if (this.inline && this.picking) {
      await this.panelRun();
      return;
    }
    // Any real send leaves the popup behind with the draft. The draft is
    // cleared by write below (no typed "" follows), so a dismissal for
    // the sent draft must not latch onto the next one.
    this.slashDismissed = false;
    const closed = this.closeSlash();
    if (closed.length > 0) await this.write(closed);
    // A command is one line whose first word is a name (`/model x`): a pasted
    // block, or a line that starts with a path (`/tmp/a.log is empty`), is a
    // message.
    const command = raw.trim();
    const isCommand =
      !command.includes("\n") &&
      (command === "/" || COMMAND_WORD.test(command.split(/\s/, 1)[0] ?? ""));
    if (this.busy) {
      // A goal is seen and stopped while its turns run.
      if (isGoalCommand(raw)) {
        await this.runCommand(command, true);
        return;
      }
      // So is /copy: it reads the history and changes nothing.
      if (isCopyCommand(raw)) {
        await this.runCommand(command, true);
        return;
      }
      if (isCommand) {
        await this.write([{ path: "status", value: BUSY_STATUS }]);
        return;
      }
      if (!raw.trim()) return;
      // A queue of one message: this send runs when the turn ends (after an
      // error too — it is the user's latest intent), and a further send is
      // added to it, never in its place (up in an empty composer gives it
      // all back to edit, and esc with the turn cancelled). It
      // joins the draft history now; its view is only shown (inline:
      // committed) when it actually starts.
      const added = this.queued !== undefined;
      const next = added ? `${this.queued}\n\n${raw}` : raw;
      const overflow = this.tooLong(next.length, added);
      if (overflow) {
        await this.write([overflow]);
        return;
      }
      this.queued = next;
      this.remember(raw);
      const firstLine = clip(
        terminalText(raw).split("\n")[0]?.trim() ?? "",
        60,
      );
      // A transient of this turn, like a verdict: the next tool result puts
      // the ambient status back, which keeps "1 queued".
      this.flashed = true;
      await this.write([
        { path: "draft", value: "" },
        {
          path: "status",
          value: added
            ? `Added to the queued message: ${firstLine} · ↑ edits it`
            : `Queued: ${firstLine} · ↑ edits it`,
        },
      ]);
      return;
    }
    if (!raw.trim()) return;
    if (isCommand) {
      this.remember(raw);
      await this.runCommand(command, true);
      return;
    }
    // Checked before any await: the turn starts in this same tick.
    const unknown = this.unknownOutcomes();
    if (unknown) {
      await this.refuseForRecovery(unknown);
      return;
    }
    this.remember(raw);
    await this.beginTurn(raw);
  }

  /**
   * The status that says why a send of `length` characters is refused (the
   * draft is left as it is), or undefined when it fits. During a turn it
   * gives way like a verdict.
   */
  private tooLong(length: number, queued = false): Write | undefined {
    const refusal = tooLongToSend(
      length,
      queued ? "Not added to the queued message, together" : undefined,
    );
    if (!refusal) return undefined;
    if (this.busy) this.flashed = true;
    return { path: "status", value: refusal };
  }

  /**
   * The runtime refuses a turn past an unknown outcome: says so instead of
   * sending a prompt into that refusal, the draft kept and a queued send
   * given back.
   */
  private async refuseForRecovery(
    unknown: number,
    queued?: string,
  ): Promise<void> {
    await this.write([
      ...(queued !== undefined ? [this.returned(queued)] : []),
      {
        path: "status",
        value: `${unknown} call${unknown === 1 ? " has" : "s have"} an unknown outcome · /recover to review before continuing${queued !== undefined ? ` · ${QUEUE_RETURNED}` : ""}`,
      },
    ]);
  }

  /** The queued send, taken off the queue and given back to the composer. */
  private takeQueued(): Write | undefined {
    const queued = this.queued;
    this.queued = undefined;
    return queued === undefined ? undefined : this.returned(queued);
  }

  /** A send that will not run, back in the composer after what is there. */
  private returned(text: string): Write {
    return {
      path: "draft",
      value: this.draftNow ? `${this.draftNow}\n${text}` : text,
    };
  }

  /** How many calls of the saved session have an unknown outcome. */
  private unknownOutcomes(): number {
    const recovery = this.options.sessions?.recovery;
    return recovery && this.session ? recovery(this.session).length : 0;
  }

  /**
   * `/recover`: the session's calls whose outcome is unknown, in a panel;
   * `/recover N …` records what the user found about the Nth (ADR-0006).
   * Nothing runs again; refused while busy, like a session switch.
   */
  private async recover(
    request: NonNullable<CommandOutcome["recover"]>,
  ): Promise<void> {
    if (this.switching || this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const capability = this.options.sessions;
    const session = this.session;
    if (!capability?.recovery || !capability.reconcile || !session) {
      await this.write([
        {
          path: "status",
          value: "Recovery needs --session-db PATH at startup",
        },
      ]);
      return;
    }
    const items = capability.recovery(session);
    if (request === "list") {
      if (!items.length) {
        await this.write([
          {
            path: "status",
            value:
              "Nothing to recover: no call in this session has an unknown outcome",
          },
        ]);
        return;
      }
      const rows = items.map(
        (item, index) =>
          `${index + 1}. ${item.toolName}${item.summary ? ` ${item.summary}` : ""} · ${item.state === "ran" ? "it started and has no result" : "its result is unknown"}`,
      );
      await this.openPanel(
        "Recover",
        "",
        [
          ...rows,
          "",
          "Check the workspace, then record what you found:",
          "/recover N applied: it ran and its change is in place",
          "/recover N not-applied: it made no change",
          "/recover N undone: you undid its change",
          "A note may follow. Nothing runs again.",
        ].join("\n"),
        `${items.length} call${items.length === 1 ? "" : "s"} with an unknown outcome · /recover N applied|not-applied|undone`,
      );
      return;
    }
    const item = items[request.index - 1];
    if (!item) {
      await this.write([
        {
          path: "status",
          value: `No call ${request.index} to recover: /recover lists ${items.length}`,
        },
      ]);
      return;
    }
    this.switching = true;
    try {
      let saved: { state: Session; closed: number };
      try {
        saved = await capability.reconcile(
          session,
          item.executionId,
          DISPOSITIONS[request.conclusion],
          request.note,
        );
      } catch (error) {
        await this.write([
          {
            path: "status",
            value: oneLine(
              error instanceof Error ? error.message : "Unable to record it",
            ),
          },
        ]);
        return;
      }
      const left = capability.recovery({
        ...session,
        state: saved.state,
      }).length;
      const status = `Recorded: ${item.toolName} · ${FOUND[request.conclusion]}${left ? ` · ${left} left: /recover` : ""}`;
      // A result given to a call that had none changes the history: the
      // conversation is reloaded as saved, as a switch loads it.
      if (saved.closed)
        await this.adoptConversation(
          { ...session, state: saved.state },
          status,
        );
      else {
        session.state = saved.state;
        await this.write([
          ...this.closePanelWrite(),
          { path: "status", value: status },
        ]);
      }
    } finally {
      this.switching = false;
    }
  }

  // ------------------------------------------------------------------ the history

  /** Adds a sent message to the history that up recalls; a repeat of the last is not added. */
  private remember(text: string): void {
    this.recall = undefined;
    this.recalledFrom = "";
    if (this.sent[this.sent.length - 1] !== text) this.sent.push(text);
    if (this.sent.length > MAX_SENT) this.sent.shift();
  }

  /**
   * `up` on the first line of the composer: the message sent before the one
   * shown. In an empty composer with a send queued, the queued send instead,
   * taken off the queue to edit while the turn goes on.
   */
  private async recallPrev(ev: HostEvent): Promise<void> {
    const draft = typeof ev.value === "string" ? ev.value : "";
    if (this.queued !== undefined && this.recall === undefined && !draft) {
      const queued = this.queued;
      this.queued = undefined;
      if (this.busy) this.flashed = true;
      await this.write([
        { path: "draft", value: queued },
        {
          path: "status",
          value: "Queued message back to edit · the turn goes on",
        },
      ]);
      return;
    }
    if (this.sent.length === 0) return;
    if (this.recall === undefined) {
      this.recalledFrom = typeof ev.value === "string" ? ev.value : "";
      this.recall = this.sent.length;
    }
    if (this.recall === 0) return;
    this.recall--;
    await this.write([{ path: "draft", value: this.sent[this.recall] ?? "" }]);
  }

  /** `down` on the last line: the next message, and at the end the draft that was being written. */
  private async recallNext(_ev: HostEvent): Promise<void> {
    if (this.recall === undefined) return;
    this.recall++;
    if (this.recall >= this.sent.length) {
      this.recall = undefined;
      await this.write([{ path: "draft", value: this.recalledFrom }]);
      this.recalledFrom = "";
      return;
    }
    await this.write([{ path: "draft", value: this.sent[this.recall] ?? "" }]);
  }

  /** The writes that paint the header's grants label from the live grants. */
  private grantLabels(): string[] {
    return [...this.grants.values()].map((grant) => grant.label);
  }

  private grantsWrites(): Write[] {
    const labels = this.grantLabels();
    return [
      { path: "header.grants", value: grantsLabel(labels) },
      { path: "header.grants_on", value: labels.length > 0 },
    ];
  }

  /**
   * The workspace's permission rules, reloaded only when the file or the
   * trust store changed (two cheap stats per approval). A missing file
   * reads as empty; a broken one degrades to "ask" with the load's warning
   * on the status.
   */
  private async fileRules(): Promise<
    PermissionFile & { untrusted?: PermissionRule[] }
  > {
    const root = this.options.workspace?.root;
    if (!root) return this.permFile;
    const stamp = async (path: string) =>
      stat(path).then(
        (info) => `${info.ino}:${info.size}:${info.mtimeMs}`,
        () => "-",
      );
    const file = await stamp(permissionsPath(root));
    if (file === "-") {
      if (this.permStamp !== "-") {
        this.permFile = { version: 1, rules: [] };
        this.permStamp = "-";
      }
      return this.permFile;
    }
    const current = `${file}|${await stamp(trustStorePath())}`;
    if (current === this.permStamp) return this.permFile;
    const loaded = await loadPermissionFile(root);
    if (loaded.warning) this.flash(oneLine(loaded.warning));
    this.permFile = loaded;
    this.permStamp = current;
    return this.permFile;
  }

  /**
   * `/permissions trust` shows the untrusted allow rules and remembers
   * them; `/permissions trust confirm` trusts exactly those, if the file
   * still holds them.
   */
  private async trustStatus(
    root: string,
    step: "review" | "confirm",
  ): Promise<string> {
    const named = (rules: readonly PermissionRule[]) =>
      rules.map((rule) => `${rule.tool} ${rule.pattern}`).join(" · ");
    const plural = (n: number) => `${n} rule${n === 1 ? "" : "s"}`;
    if (step === "review") {
      this.permStamp = undefined;
      const { untrusted = [] } = await this.fileRules();
      if (!untrusted.length) {
        this.trustOffer = undefined;
        return "No untrusted allow rules in .local-agent/permissions.json";
      }
      // The offer is exactly what the line shows: rules that would not fit
      // wait for the next review instead of being trusted unseen.
      const head = (shown: readonly PermissionRule[]) => {
        const more = untrusted.length - shown.length;
        return `Untrusted allow ${plural(shown.length)} (check .local-agent/permissions.json, then /permissions trust confirm${more ? `; ${more} more after these` : ""}): `;
      };
      const shown: PermissionRule[] = [];
      for (const rule of untrusted) {
        const next = [...shown, rule];
        if (flatLine(head(next) + named(next)).length > STATUS_MAX) break;
        shown.push(rule);
      }
      if (!shown.length) {
        this.trustOffer = undefined;
        return "An untrusted allow rule is too long to show here: read .local-agent/permissions.json and run local-agent --trust-rules";
      }
      this.trustOffer = shown;
      return oneLine(head(shown) + named(shown));
    }
    const offer = this.trustOffer;
    if (!offer) return "Nothing reviewed: run /permissions trust first";
    this.trustOffer = undefined;
    try {
      const { trusted } = await trustRules(root, offer);
      this.permStamp = undefined;
      if (!trusted.length)
        return "Nothing trusted: the reviewed rules left the file or are already trusted";
      const of = trusted.length < offer.length ? ` of ${offer.length}` : "";
      return oneLine(
        `Trusted ${trusted.length}${of} ${trusted.length === 1 && !of ? "rule" : "rules"}: ${named(trusted)}`,
      );
    } catch (error) {
      return oneLine(`Rules not trusted: ${error}`);
    }
  }

  /** `/permissions` labels for the file's rules (enforced first, then the
   * allow rules this user has not trusted, marked so). A cloned file's
   * patterns are its author's text: cleaned before they reach the status. */
  private ruleLabels(): Promise<string[]> {
    return this.fileRules().then((file) => [
      ...file.rules.map((rule) =>
        cleanText(`${rule.effect} ${rule.tool}:${rule.pattern}`),
      ),
      ...(file.untrusted ?? []).map((rule) =>
        cleanText(`${rule.effect} ${rule.tool}:${rule.pattern} (untrusted)`),
      ),
    ]);
  }

  /**
   * Runs a slash command. One sent from the composer clears it, unless the
   * command (or its setting) is unknown: a typo stays to be fixed.
   */
  private async runCommand(command: string, sent = false): Promise<void> {
    const { mcp = [] } = this.options;
    // `/permissions` lists file rules too: they are loaded (mtime-checked)
    // before the pure command runs, so the listing is never stale.
    const rules = /^\/permissions(\s|$)/i.test(command)
      ? await this.ruleLabels()
      : undefined;
    const outcome = runSlash(command, {
      intent: this.intent,
      mcp: {
        servers: mcp.length,
        tools: mcp.reduce((total, server) => total + server.tools.length, 0),
        connecting: this.mcpPending,
        failed: this.mcpFailed.length,
      },
      model: this.provider.model,
      theme: this.theme,
      mouse: this.mouse,
      grants: this.grantLabels(),
      sandbox: this.sandbox,
      ...(rules !== undefined ? { rules } : {}),
    });
    // `/model` runs on controller state and can still be refused there (a
    // model admission refuses): its line is cleared only once accepted, so
    // a refused one can be fixed. `/details N` likewise keeps a number no
    // receipt answers to. Bare `/details` leaves nothing to fix, even with
    // no tool output yet: its line goes at once.
    const numbered =
      typeof outcome.details === "number" ? outcome.details : undefined;
    const clearDraft = () => this.write([{ path: "draft", value: "" }]);
    if (
      sent &&
      !outcome.unknown &&
      outcome.model === undefined &&
      numbered === undefined
    )
      await clearDraft();
    // `/details` opens over controller state (the retained receipts), so
    // its whole outcome — refusal included — is written there.
    if (outcome.details !== undefined) {
      const opened = await this.openDetails(numbered);
      if (sent && opened && numbered !== undefined) await clearDraft();
      return;
    }
    // The panel and export commands likewise run on controller state.
    if (outcome.statusPanel) {
      await this.openStatusPanel();
      return;
    }
    if (outcome.providerPanel) {
      await this.openProviderPanel();
      return;
    }
    if (outcome.mcpPanel) {
      await this.openMcpPanel(outcome.status);
      return;
    }
    if (outcome.sessions) {
      await this.openSessions();
      return;
    }
    if (outcome.newSession) {
      await this.newSession();
      return;
    }
    if (outcome.exportTranscript) {
      await this.exportTranscript(outcome.exportFormat ?? "markdown");
      return;
    }
    if (outcome.model !== undefined) {
      if ((await this.switchModel(outcome.model)) && sent) await clearDraft();
      return;
    }
    if (outcome.copy) {
      await this.copyReply(outcome.copy);
      return;
    }
    if (outcome.find !== undefined) {
      await this.findSessions(outcome.find);
      return;
    }
    if (outcome.goal) {
      await this.runGoal(outcome.goal);
      return;
    }
    if (outcome.recover) {
      await this.recover(outcome.recover);
      return;
    }
    // Inline has no mouse, and its document never reads `mouse_on`: refuse
    // here rather than write a path the host would reject.
    if (outcome.mouse !== undefined && this.inline) {
      await this.write([{ path: "status", value: MOUSE_INLINE_STATUS }]);
      return;
    }
    const writes: Write[] = [{ path: "status", value: outcome.status }];
    if (outcome.intent) {
      this.intent = outcome.intent;
      writes.push({ path: "header.mode", value: outcome.intent });
    }
    if (outcome.theme) {
      this.theme = outcome.theme;
      writes.push({ path: "@theme", value: outcome.theme });
    }
    if (outcome.mouse !== undefined) {
      this.mouse = outcome.mouse;
      writes.push({ path: "mouse_on", value: outcome.mouse });
      // How to select natively changes with it: a modifier-drag while the
      // terminal's mouse reporting is on, a plain drag when it is off.
      const tips = selectTips(outcome.mouse, this.options.termProgram);
      writes.push(
        { path: "select_tip", value: tips.tip },
        { path: "help.select_key", value: tips.help.select_key },
        { path: "help.select_desc", value: tips.help.select_desc },
      );
    }
    if (outcome.clearGrants) {
      this.grants.clear();
      writes.push(...this.grantsWrites());
    }
    if (outcome.sandbox !== undefined) {
      const why = outcome.sandbox ? this.sandboxProblem() : undefined;
      if (why !== undefined) this.sandboxWhy = why;
      this.sandbox = outcome.sandbox && why === undefined;
      writes[0] = {
        path: "status",
        value:
          why !== undefined
            ? oneLine(`Sandbox unavailable: ${why} · commands ask`)
            : this.sandbox
              ? "Sandbox on: commands run confined, without a card (no network, writes only in the workspace)"
              : "Sandbox off: commands ask",
      };
      writes.push({ path: "header.sandbox_on", value: this.sandbox });
    }
    if (outcome.clearRules) {
      const root = this.options.workspace?.root;
      if (root) {
        await clearRules(root);
        this.permStamp = undefined;
        this.trustOffer = undefined;
      } else {
        writes[0] = {
          path: "status",
          value: "No workspace: file rules unavailable",
        };
      }
    }
    if (outcome.trustRules) {
      const root = this.options.workspace?.root;
      writes[0] = {
        path: "status",
        value: root
          ? await this.trustStatus(root, outcome.trustRules)
          : "No workspace: file rules unavailable",
      };
    }
    if (outcome.help) writes.push({ path: "help.open", value: true });
    await this.write(writes);
  }

  /**
   * `/copy` and ctrl+y: the last assistant reply's markdown (`reply`), or
   * its last fenced code block (`code`), on the system clipboard through
   * the injected tool. Reads the model's history, so what is copied is
   * what the model wrote (a code block with its tabs, a table as a table),
   * not the view. The status says what happened and with which tool.
   */
  private async copyReply(what: "reply" | "code"): Promise<void> {
    const status = (text: string) =>
      this.write([{ path: "status", value: text }]);
    // What is copied is the model's text without the control bytes the view
    // strips too (an ESC sequence in a fence must not reach the clipboard).
    const reply = this.history.findLast(
      (message) =>
        message.role === "assistant" &&
        clipboardText(message.content).trim() !== "",
    );
    if (!reply) {
      await status(NO_REPLY_STATUS);
      return;
    }
    // The last block with something in it: an empty fence is no code to copy.
    const text =
      what === "reply"
        ? clipboardText(reply.content)
        : fencedCodeBlocks(reply.content)
            .map((block) => clipboardText(block.text))
            .findLast((code) => code.trim() !== "");
    if (text === undefined) {
      await status(NO_CODE_STATUS);
      return;
    }
    // While a turn runs the reply being written is not in the history yet:
    // what is copied is the last one that finished, and the status says so.
    // Taken with the text: the turn may end while the tool runs.
    const running = this.busy;
    const copied: CopyResult = (await this.options.copy?.(text)) ?? {
      ok: false,
      reason: "none",
    };
    const label =
      what === "reply"
        ? running
          ? "the last finished reply"
          : "the last reply"
        : running
          ? "the last code block of the last finished reply"
          : "the last code block";
    if (copied.ok) {
      await status(
        `Copied ${label} (${charCount(characters(text))} chars) with ${copied.tool}${running ? "; the one being written is not done yet" : ""}`,
      );
      return;
    }
    if (copied.reason === "failed") {
      await status(oneLine(`Copy failed: ${copied.tool} ${copied.detail}`));
      return;
    }
    if (copied.reason === "timeout") {
      await status(
        `${copied.tool} did not finish in ${COPY_TIMEOUT_MS / 1000} s; the clipboard may have it`,
      );
      return;
    }
    await status(
      `No clipboard tool found (${CLIPBOARD_TOOL_NAMES}): select text with ${selectionHint(this.mouse, this.options.termProgram)}`,
    );
  }

  private async loadEarlier(): Promise<void> {
    if (this.transcript.widen()) await this.publishWindow();
  }

  // ------------------------------------------------------------------ the palette

  private async paletteOpen(): Promise<void> {
    // An open approval owns the keys (fullscreen's modal traps ctrl+p; the
    // in-flow approval does not, so the palette must not open over it). An
    // open question too: the palette's modal would take its esc and enter.
    if (this.proposal || this.asking) return;
    const found = filterPalette("", this.inline);
    await this.write([
      { path: "found", value: { any: found.length > 0, items: found } },
      {
        path: "palette",
        value: { open: true, query: "", cur: found[0]?.id ?? "" },
      },
    ]);
  }

  private async paletteQuery(ev: HostEvent): Promise<void> {
    const query = typeof ev.value === "string" ? ev.value : "";
    const found = filterPalette(query, this.inline);
    // A new query is a new list: the cursor goes back to the first match
    // instead of resting on a row the filter may have removed.
    await this.write([
      { path: "found", value: { any: found.length > 0, items: found } },
      { path: "palette.cur", value: found[0]?.id ?? "" },
    ]);
  }

  private async paletteRun(): Promise<"quit" | undefined> {
    const ui = this.ui;
    if (!ui) return undefined;
    const cur = (await ui.get("palette.cur")).value;
    const query = (await ui.get("palette.query")).value;
    const rows = filterPalette(
      typeof query === "string" ? query : "",
      this.inline,
    );
    const id = rows.find((row) => row.id === cur)?.id ?? rows[0]?.id;
    if (id === undefined) return undefined;
    await this.write([{ path: "palette.open", value: false }]);
    // The key rows run their key's handler, busy or not, exactly as the
    // key would: Quit is the two-stage ctrl+c (a draft is cleared first),
    // and load-earlier widens the window like ctrl+o (fullscreen only:
    // the inline filter never lists it).
    if (id === "quit") return this.quitRequest();
    if (id === "load-earlier") {
      await this.loadEarlier();
      return undefined;
    }
    const command = paletteCommand(id);
    // The help overlay and a copy are harmless while a turn runs; everything
    // else waits.
    if (
      (this.busy || this.switching) &&
      command !== "/help" &&
      command !== "/copy" &&
      command !== "/copy code"
    ) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    if (command) await this.runCommand(command);
  }

  // -------------------------------------------------------------------- one turn

  /** A file deny rule for a read that never asks (fetch by host, web_search). */
  private deniedRead: NonNullable<AgentOptions["deniedRead"]> = async (
    request,
  ) => {
    const verdict = evaluate((await this.fileRules()).rules, request);
    if (verdict.effect !== "deny") return undefined;
    this.flash(oneLine(`denied by rule: ${verdict.rule?.pattern}`));
    return verdict.rule?.pattern ?? "";
  };

  private approve = async (
    proposal: ApprovalRequest,
    signal: AbortSignal,
  ): Promise<Approval> => {
    signal.throwIfAborted();
    // The workspace's permission file outranks everything interactive: a
    // deny rule refuses (even a session grant), an allow rule approves
    // without a card. It is ruleVerdict, the step every client takes first,
    // and it applies in every intent:
    // the runtime refuses plan and chat mutations before this runs (no rule
    // widens that), but chat still offers MCP calls.
    const verdict = ruleVerdict((await this.fileRules()).rules, proposal);
    if (verdict.effect === "deny") {
      this.flash(oneLine(`denied by rule: ${verdict.rule?.pattern}`));
      return false;
    }
    // `/sandbox on`: a command runs confined unless a person approves it on
    // a card. An allow rule or a session grant only spares the card: run
    // outside, it could run what a sandboxed command wrote in the
    // workspace. An ask rule still shows the card.
    // A command that names a path the file tools refuse always gets its
    // card, which says so: no allow rule, grant or sandbox spares it.
    const secret = namesSecretPath(proposal);
    let sandboxLost = false;
    if (
      this.sandbox &&
      proposal.kind === "command" &&
      this.intent === "build" &&
      !secret &&
      !(verdict.effect === "ask" && verdict.rule)
    ) {
      const why = this.sandboxProblem();
      if (why === undefined) {
        this.flash(oneLine(`sandboxed: ${commandLine(proposal.argv)}`));
        return "sandbox";
      }
      // It stopped being available: off, and this command asks, whatever
      // rule or grant would have spared the card.
      this.sandbox = false;
      this.sandboxWhy = why;
      sandboxLost = true;
      await this.write([
        { path: "header.sandbox_on", value: false },
        {
          path: "status",
          value: oneLine(`Sandbox unavailable: ${why} · commands ask`),
        },
      ]);
    }
    // ruleVerdict already makes a secret path's allow an ask; `secret`
    // still stops a session grant below.
    if (verdict.effect === "allow" && !sandboxLost) {
      this.flash(oneLine(`allowed by rule: ${verdict.rule?.pattern}`));
      return true;
    }
    // Grants never bypass a non-build intent: the runtime refuses plan and
    // chat mutations before this callback runs, and this keeps it true here.
    const grant = grantFor(proposal);
    // With the sandbox on, a command runs outside only from its own card.
    const grantedLabel =
      this.intent === "build" &&
      !sandboxLost &&
      !secret &&
      !(this.sandbox && proposal.kind === "command")
        ? [...this.grants.entries()].find(([key, remembered]) =>
            grantCovers({ key, ...remembered }, proposal),
          )?.[1].label
        : undefined;
    if (grantedLabel !== undefined) {
      // The least invasive visible trace: the status flash a tool result
      // gets, naming the grant that let the call through.
      this.flash(`${grantedLabel}: allowed for session`);
      return true;
    }
    return new Promise<boolean>((resolve) => {
      // Cancelled while the rules loaded: `abort` fired before the listener
      // below, so it would never deny, and a card would open for nothing.
      if (signal.aborted) {
        resolve(false);
        return;
      }
      /** The card as it was before the dialog put it on hold. */
      let held: Card | undefined;
      const finish = async (
        allowed: boolean,
        session = false,
        status?: string,
      ): Promise<void> => {
        signal.removeEventListener("abort", deny);
        clearTimeout(this.armTimer);
        this.decide = undefined;
        this.armed = false;
        this.proposal = undefined;
        this.proposalTruncated = false;
        // `a` remembers the grant before the verdict lands, so the header
        // and the status name it in the same write that closes the dialog.
        let granted: string | undefined;
        if (allowed && session) {
          // `a` again on a command with a new option widens the same grant
          // by that option, never by any other.
          const known = this.grants.get(grant.key)?.options ?? [];
          this.grants.set(grant.key, {
            label: grant.label,
            prefix: grant.prefix,
            ...(grant.options
              ? { options: [...new Set([...known, ...grant.options])] }
              : {}),
          });
          granted = grant.label;
        }
        const verdict =
          status ??
          (!allowed
            ? "Denied"
            : granted !== undefined
              ? `Approved · ${granted} allowed for session`
              : "Approved");
        // The verdict stays on the status line until the next state change
        // (a tool result, the turn's end); the turn's clock goes on.
        this.flashed = true;
        this.resumeClock();
        resolve(allowed);
        await this.write([
          { path: "approval", value: noApproval() },
          { path: "status", value: verdict },
          // The turn goes on: a send is queued again.
          { path: "placeholder", value: PLACEHOLDER_BUSY },
          // The card is no longer on hold: it runs (allowed) or is as it was.
          ...this.releaseCard(held, allowed),
          ...this.headWrites(),
          ...(granted !== undefined ? this.grantsWrites() : []),
        ]);
      };
      // Anything but an armed, explicit "allow" is a deny, the abort included.
      const deny = () => void finish(false);
      this.decide = finish;
      this.pauseClock();
      this.armed = false;
      this.proposal = proposal;
      signal.addEventListener("abort", deny, { once: true });
      const approval = this.approvalFor(proposal);
      this.proposalTruncated = approval.truncated;
      const writes: Write[] = [
        { path: "palette.open", value: false },
        { path: "help.open", value: false },
        ...this.closeSlash(),
        // The viewer never covers an armed dialog.
        ...this.closeDetailsWrite(),
        ...this.closePanelWrite(),
        { path: "approval", value: approval },
        { path: "status", value: APPROVAL_WAIT_STATUS },
        { path: "placeholder", value: APPROVAL_PLACEHOLDER },
        // The head waits on the person: a still glyph, the clock stopped.
        ...this.headWrites(),
      ];
      if (this.liveTool) {
        // The card goes in the same write as the dialog, so the two never disagree.
        held = this.liveTool.card;
        this.liveTool = {
          ...this.liveTool,
          card: onHold(this.liveTool.card, "waiting for approval"),
        };
        this.dirty = false;
        writes.unshift({ path: "live", value: this.liveTool });
      }
      // The dialog accepts `y` only a moment after the write that opens it landed.
      void this.write(writes).then(() => {
        if (this.decide !== finish) return;
        this.armTimer = setTimeout(() => {
          if (this.decide !== finish) return;
          this.armed = true;
          this.armedLine = armedStatus(approval);
          void this.write([{ path: "status", value: this.armedLine }]);
        }, this.options.armMs ?? APPROVAL_ARM_MS);
      });
    });
  };

  /**
   * The runtime's ask_user: the question opens in flow above the composer,
   * which answers it (see onSend), with the draft put aside until it
   * closes. An abort closes it and rejects; esc answers `declined`.
   */
  private readonly ask = (
    question: Question,
    signal: AbortSignal,
  ): Promise<Answer> =>
    new Promise<Answer>((resolve, reject) => {
      if (signal.aborted) {
        reject(signal.reason);
        return;
      }
      const stash = this.draftNow;
      /** The card as it was before the question put it on hold. */
      let held: Card | undefined;
      const finish = async (answer?: Answer): Promise<void> => {
        if (this.asking !== state) return;
        signal.removeEventListener("abort", abort);
        clearTimeout(this.askArmTimer);
        this.asking = undefined;
        const verdict =
          answer === undefined
            ? undefined
            : answer.kind === "declined"
              ? "Question skipped"
              : "Answered";
        // Like an approval's verdict, it stays until the next state change.
        if (verdict !== undefined) this.flashed = true;
        this.resumeClock();
        if (answer) resolve(answer);
        else reject(signal.reason);
        await this.write([
          { path: "question", value: noQuestion() },
          { path: "placeholder", value: PLACEHOLDER_BUSY },
          { path: "draft", value: stash },
          ...(verdict !== undefined
            ? [{ path: "status", value: verdict }]
            : []),
          ...this.releaseCard(held, answer !== undefined),
          ...this.headWrites(),
        ]);
      };
      const state = {
        question,
        // A yes/no defaults to no, as a skipped one reads; a single choice
        // has none until the user picks, so a stray enter answers nothing.
        sel: question.kind === "yes_no" ? 1 : -1,
        armed: false,
        answer: finish,
        close: () => finish(),
      };
      const abort = () => void finish();
      this.asking = state;
      this.pauseClock();
      signal.addEventListener("abort", abort, { once: true });
      const writes: Write[] = [
        { path: "palette.open", value: false },
        { path: "help.open", value: false },
        ...this.closeSlash(),
        ...this.closeDetailsWrite(),
        ...this.closePanelWrite(),
        { path: "question", value: questionState(question, state.sel) },
        {
          path: "placeholder",
          value:
            question.kind === "freetext"
              ? QUESTION_PLACEHOLDER_TEXT
              : QUESTION_PLACEHOLDER_CHOICE,
        },
        { path: "draft", value: "" },
        { path: "status", value: QUESTION_WAIT_STATUS },
        // The head waits on the person: a still glyph, the clock stopped.
        ...this.headWrites(),
      ];
      if (this.liveTool) {
        held = this.liveTool.card;
        this.liveTool = {
          ...this.liveTool,
          card: onHold(this.liveTool.card, QUESTION_CARD_STATUS),
        };
        this.dirty = false;
        writes.unshift({ path: "live", value: this.liveTool });
      }
      // Enter answers only a moment after the write that opens it landed:
      // a send already on its way is not taken for the answer.
      void this.write(writes).then(() => {
        if (this.asking !== state) return;
        this.askArmTimer = setTimeout(() => {
          if (this.asking !== state) return;
          state.armed = true;
          void this.write([{ path: "status", value: QUESTION_STATUS }]);
        }, this.options.armMs ?? APPROVAL_ARM_MS);
      });
    });

  /** A send while a question is open: the typed answer, or the choice it names. */
  private async answerSend(raw: string): Promise<void> {
    const asking = this.asking;
    if (!asking) return;
    if (!asking.armed) {
      await this.write([{ path: "status", value: QUESTION_UNARMED_STATUS }]);
      return;
    }
    if (asking.question.kind === "freetext") {
      if (!raw.trim()) {
        await this.write([{ path: "status", value: QUESTION_EMPTY_STATUS }]);
        return;
      }
      await asking.answer({ kind: "text", text: raw });
      return;
    }
    const id = questionPick(asking.question, asking.sel, raw);
    if (id === undefined) {
      await this.write([
        {
          path: "status",
          value: raw.trim() ? QUESTION_NO_MATCH_STATUS : QUESTION_CHOOSE_STATUS,
        },
      ]);
      return;
    }
    await asking.answer({ kind: "choice", id });
  }

  /** Up and down under an open choice: the chosen row moves, clamped. */
  private async questionMove(by: number): Promise<void> {
    const asking = this.asking;
    if (!asking) return;
    const count = questionChoices(asking.question).length;
    if (count === 0) return;
    // From no choice, down takes the first row and up the last.
    asking.sel =
      asking.sel < 0
        ? by > 0
          ? 0
          : count - 1
        : Math.max(0, Math.min(asking.sel + by, count - 1));
    await this.write([
      { path: "question", value: questionState(asking.question, asking.sel) },
    ]);
  }

  /** `hostWritten`: a goal's own message, never the user's words. */
  private async beginTurn(prompt: string, hostWritten = false): Promise<void> {
    if (this.switching) return;
    this.busy = true;
    const request = new AbortController();
    this.turn = request;
    // A previous reply whose commits were refused gets one more attempt
    // before its stream is dropped for the new turn.
    if (this.inline && this.stream.hasUncommitted)
      await this.commitStreamTail();
    this.stream.reset();
    this.headCommitted = false;
    this.liveTool = undefined;
    this.recall = undefined;
    this.evalCount = 0;
    this.evalDuration = 0;
    this.ctx.beginTurn();
    this.phase = "waiting";
    this.headOn = true;
    this.turnStartedAt = this.now();
    this.pausedAt = undefined;
    this.pausedMs = 0;
    this.headSeconds = -1;
    this.flashed = false;
    this.cancelReturnedQueue = false;
    // Every turn while a goal runs is one of its turns for the stop rules,
    // a typed one too.
    this.goalTurn = this.goal
      ? { ...newGoalTurn(), own: hostWritten }
      : undefined;
    const message: Message = { role: "user", content: prompt };
    this.history.push(message);
    // What the user typed is theirs, whatever it starts with.
    const view = this.builder.fromMessage(message, !hostWritten);
    if (view) this.transcript.push(view);
    // turnDone is set before any await, so idle() can never miss this turn.
    let published: () => void = () => {};
    const promptShown = new Promise<void>((resolve) => {
      published = resolve;
    });
    this.turnDone = this.runTurn(
      prompt,
      hostWritten,
      request,
      published,
      view,
    ).catch((error) => {
      this.writeErrors.push(
        error instanceof Error ? error.message : String(error),
      );
    });
    await promptShown;
  }

  private async runTurn(
    prompt: string,
    hostWritten: boolean,
    request: AbortController,
    published: () => void,
    userView?: MessageView,
  ): Promise<void> {
    try {
      // Inline, the prompt is printed as it is sent; the region keeps no copy.
      if (this.inline && userView) await this.commitView(userView);
      await this.publishMessages([
        // A new turn takes the screen: an open viewer closes with it.
        ...this.closeDetailsWrite(),
        ...this.closePanelWrite(),
        // The welcome goes with the first message and never returns.
        { path: "empty", value: false },
        { path: "draft", value: "" },
        { path: "placeholder", value: PLACEHOLDER_BUSY },
        // The same ambient status as at rest: a transient one of the last
        // turn (an error, a command's answer) goes with the new turn.
        { path: "status", value: this.idleText() },
        ...(this.goal
          ? [
              { path: "header.goal", value: this.goal.label },
              { path: "header.goal_on", value: true },
            ]
          : []),
      ]);
    } finally {
      published();
    }
    this.timer = setInterval(() => this.tick(), this.options.flushMs ?? 33);
    await this.loop(prompt, hostWritten, request, userView);
    // A send queued while busy runs now, after success and after an error
    // alike (it is the user's latest intent); esc and quit already dropped
    // it, and a process-level abort makes a new turn impossible.
    const next = this.queued;
    this.queued = undefined;
    const live = !this.quitting && !this.options.signal.aborted;
    const goal = this.goal && this.goalContinues ? this.goal : undefined;
    if (!live || (next === undefined && !goal)) return;
    // Neither a queued send nor a goal's next turn goes into the runtime's
    // refusal past an unknown outcome: the send goes back to the composer.
    const unknown = this.unknownOutcomes();
    if (unknown) {
      if (goal) this.goalStop ??= "stopped: a call's outcome is unknown";
      await this.endStoppedGoal();
      await this.refuseForRecovery(unknown, next);
      return;
    }
    // A queued send runs first; the goal continues after it.
    if (next !== undefined) {
      await this.beginTurn(next);
      await this.turnDone;
      return;
    }
    // A stop that landed after the turn's own check (ctrl+c between
    // turns) ends the goal before another turn starts.
    if (!goal || (await this.endStoppedGoal())) return;
    // A goal left open goes on with its next turn: host-written, marked,
    // shown as a notice.
    await this.beginTurn(goal.nextMessage(), true);
    await this.turnDone;
  }

  /** Ends a goal a stop was asked of; true when there was one. */
  private async endStoppedGoal(): Promise<boolean> {
    if (!this.goal || this.goalStop === undefined) return false;
    const line = await this.endGoal(this.goal, this.goalStop);
    await this.publishMessages([
      { path: "status", value: line },
      { path: "header.goal_on", value: false },
    ]);
    return true;
  }

  private async loop(
    prompt: string,
    hostWritten: boolean,
    request: AbortController,
    userView?: MessageView,
  ): Promise<void> {
    const { workspace, mcp, signal } = this.options;
    // The session this turn checkpoints to (a switch is refused while
    // busy, so it cannot change under the turn).
    const session = this.session;
    let receivedMessage = false;
    let status = "";
    // A card's duration, per call (the `now` clock). The runtime starts a
    // parallel batch's calls together and sends their messages afterwards,
    // in call order, so a call is matched to its message by that order,
    // and its end is its tool_result when that comes first.
    const callOrder: string[] = [];
    const startedAt = new Map<string, number>();
    const tookMs = new Map<string, number>();
    /** A failed turn's notice view, committed (inline) in the finally below. */
    let notice: MessageView | undefined;
    let finished = false;
    /** The turn ends on the ambient status (no error, no cancel, no goal note). */
    let ambient = false;
    // A goal's commands: each run_command call's argv, and the calls that
    // succeeded (matched up when the turn ends; see settleGoal).
    const commandArgs = new Map<string, unknown>();
    const commandsOk: string[] = [];
    const mcpNames = new Set(
      (mcp ?? []).flatMap((server) => server.tools.map((tool) => tool.name)),
    );
    try {
      const turnSignal = AbortSignal.any([signal, request.signal]);
      // A turn aborted before it started (a signal while its first write was
      // in flight) never calls the runtime, so it never takes the session lease.
      turnSignal.throwIfAborted();
      // Servers still connecting: their tools are part of what the model
      // is offered, so the turn waits for them (Esc still cancels).
      const ready = this.options.mcpReady;
      if (this.mcpPending && ready) {
        this.setPhase("mcp");
        await new Promise<void>((resolve, reject) => {
          const abort = () => reject(turnSignal.reason);
          turnSignal.addEventListener("abort", abort, { once: true });
          void ready.then(() => {
            turnSignal.removeEventListener("abort", abort);
            resolve();
          });
        });
        // mcpSettled runs before this continuation: its then was first.
        this.setPhase("waiting");
      }
      status = this.idleText();
      for await (const event of this.run(this.provider, this.history, {
        workspace,
        intent: this.intent,
        approve: this.approve,
        ask: this.ask,
        deniedRead: this.deniedRead,
        signal: turnSignal,
        session,
        mcp,
        // This client applies `compacted` below.
        compact: true,
        // While a goal runs: its report, and its refusals across turns.
        ...(this.goal
          ? { goal: this.reportGoal, refused: this.goal.refused }
          : {}),
      })) {
        switch (event.type) {
          case "thinking":
            // Reasoning after the reply began (a late piece) is not a phase.
            if (this.phase !== "streaming") this.setPhase("thinking");
            if (this.goalTurn) this.goalTurn.streamed = true;
            if (!this.liveTool) this.stream.pushThinking(event.text);
            break;
          case "text":
            this.setPhase("streaming");
            if (this.goalTurn) this.goalTurn.streamed = true;
            if (!this.liveTool) this.stream.pushText(event.text);
            break;
          case "message": {
            receivedMessage = true;
            // A tool message hands the turn back to the model; a reply
            // (or the text before a tool call) is complete. publishMessages
            // below writes the head the new phase gives.
            this.phase = event.message.role === "tool" ? "waiting" : "between";
            this.history.push(event.message);
            for (const call of event.message.tool_calls ?? [])
              if (call.id && call.function.name === "run_command")
                commandArgs.set(call.id, call.function.arguments.argv);
            let view = this.builder.fromMessage(event.message);
            // A tool that ran this turn says how long it took (a resumed
            // receipt cannot: the history keeps no timing).
            const callId =
              event.message.role === "tool" ? callOrder.shift() : undefined;
            const started =
              callId === undefined ? undefined : startedAt.get(callId);
            // A question's wait is the user's, not the tool's: no duration.
            if (
              view?.is_tool &&
              view.card.name !== "ask_user" &&
              callId !== undefined &&
              started !== undefined
            ) {
              const ms = tookMs.get(callId) ?? this.now() - started;
              view = { ...view, card: { ...view.card, took: formatTook(ms) } };
            }
            if (callId !== undefined) {
              startedAt.delete(callId);
              tookMs.delete(callId);
            }
            if (view) this.transcript.push(view);
            // The fullest output the client receives is this message's
            // receipt, before the card view truncates it: keep it whole.
            const receipt = this.builder.receiptOf(event.message);
            if (receipt) this.keepReceipt(receipt);
            if (this.inline) {
              // Only what is still live is printed: the whole re-parsed view
              // would duplicate the blocks already in the scrollback.
              const streamed = await this.commitStreamTail();
              if (view?.is_tool) await this.commitView(view);
              else if (view && !streamed) await this.commitView(view);
              // A reply streamed block by block already shows its [n] markers;
              // its sources row prints once, when the message closes.
              else if (view && view.links.length > 0)
                await this.commitLinks(view);
            }
            await this.publishMessages();
            break;
          }
          case "windowed": {
            // Said when more is left out than before, not on every turn.
            if (event.left <= this.windowNoticed) break;
            this.windowNoticed = event.left;
            const view = this.builder.notice(windowedNotice(event));
            this.transcript.push(view);
            if (this.inline) await this.commitView(view);
            await this.publishMessages();
            break;
          }
          case "compacted": {
            // The runtime shortened the history and checkpointed it: this
            // copy follows at once, so the next turn's history is the
            // session's even if this one fails.
            this.history.splice(0, this.history.length, ...event.messages);
            const view = this.builder.notice(
              compactedNotice(event, this.provider.context_size),
            );
            this.transcript.push(view);
            if (this.inline) await this.commitView(view);
            await this.publishMessages();
            break;
          }
          case "tool_requested":
            callOrder.push(event.id);
            // Inline, the text before the tool call is printed before the
            // stream is dropped for the tool card (and kept if refused).
            if (this.inline) await this.commitStreamTail();
            if (!this.inline || !this.stream.hasUncommitted) {
              this.stream.reset();
              this.headCommitted = false;
            }
            this.liveTool = liveToolView(
              this.builder.takeCall(),
              event.name,
              "requested",
            );
            this.dirty = true;
            break;
          case "tool_start":
            startedAt.set(event.id, this.now());
            if (this.liveTool) {
              this.liveTool = {
                ...this.liveTool,
                card: {
                  ...this.liveTool.card,
                  status: "running",
                  running: true,
                },
              };
              this.dirty = true;
            }
            break;
          case "tool_result": {
            // A parallel call ends here, before its message is sent.
            const started = startedAt.get(event.id);
            if (started !== undefined && !tookMs.has(event.id))
              tookMs.set(event.id, this.now() - started);
            // The card shows the result; a verdict or a rule's note that was
            // on the status line gives way to the ambient status.
            this.restoreStatus();
            const goal = this.goalTurn;
            if (goal) {
              if (event.status === "outcome_unknown") goal.uncertain = true;
              // A skipped question, or one answered with nothing.
              if (event.name === "ask_user" && event.status === "denied")
                goal.declined = true;
              if (event.status === "succeeded") {
                if (effectful(event.name, mcpNames)) goal.effects++;
                if (event.name === "run_command") commandsOk.push(event.id);
              }
            }
            break;
          }
          case "done": {
            this.startPlacement();
            if (this.goalTurn) this.goalTurn.outputTokens += event.outputTokens;
            // The header ctx moves during the turn, on every usage-bearing
            // event (see CtxMeter for the max-within-the-turn rule).
            const shown = this.ctx.observe(
              event.inputTokens + event.outputTokens,
            );
            await this.write(this.ctxWrites(shown));
            this.evalCount += event.evalCount ?? 0;
            this.evalDuration += event.evalDuration ?? 0;
            break;
          }
          default:
            break;
        }
      }
      this.recordTurn();
      status = this.idleText();
      ambient = true;
      finished = true;
    } catch (error) {
      const aborted = request.signal.aborted;
      if (this.goalTurn) {
        this.goalTurn.end = aborted
          ? "aborted"
          : error instanceof StepLimitError
            ? "step_limit"
            : "failed";
        this.goalTurn.error =
          error instanceof Error ? error.message : String(error);
      }
      // Keep completed messages and tool receipts even when a later request fails.
      if (!session && !receivedMessage) {
        // Model history only: with no session nothing is durable, so the
        // prompt leaves the model's history and a re-send cannot duplicate
        // it. (With a session the runtime checkpointed it before inference,
        // so it stays, exactly as the durable state has it.) The transcript
        // is a different matter: a cancelled turn takes its prompt back (it
        // returns to the draft below), but a FAILED turn's prompt stays
        // visible next to its error notice — and inline it is already in
        // the scrollback either way.
        this.history.pop();
        // The prompt's own view: a compaction note may have followed it.
        // A goal's own message is never the user's to take back: its
        // notice stays, as the reply under it does.
        if (aborted && userView && !hostWritten)
          this.transcript.remove(userView);
        // The host may be gone already (ctrl+c mid-turn): keep going to the
        // cleanup. A draft typed during the turn is never overwritten, and
        // a queued send (which cleared the draft for its own turn) is the
        // user's newer intent: the prompt is still in the draft history.
        const draft = await this.ui
          ?.get("draft")
          .then((result) => result.value)
          .catch(() => "gone");
        // A goal's own message was never the user's draft. draftNow sees a
        // queued send given back even before the host has it.
        if (
          !draft &&
          !this.draftNow &&
          this.queued === undefined &&
          !hostWritten
        )
          await this.write([{ path: "draft", value: prompt }]);
      }
      // Fullscreen, what arrived of the reply stays where it was read, under
      // a prompt that stays (inline prints it below). A cancelled turn that
      // took its prompt back takes its reply too.
      const withdrawn = aborted && !session && !receivedMessage && !hostWritten;
      if (!this.inline && !withdrawn && !this.liveTool) {
        const blocks = this.stream.finishTail(true);
        if (blocks.length > 0)
          this.transcript.push(this.builder.partial(blocks, this.stream.links));
      }
      if (aborted) {
        status = this.cancelReturnedQueue
          ? `Cancelled · ${QUEUE_RETURNED}`
          : "Cancelled";
        // Cancelled by ctrl+c: the next press quits, as its status said,
        // unless it has a draft to clear first (the prompt given back, or
        // what was typed while the turn wound down).
        if (this.quitCancelled === request && this.draftNow === "")
          status += " · ctrl+c again to quit";
      } else {
        const message =
          error instanceof ConversationFullError
            ? `${error.message} /sessions new starts a new one.`
            : error instanceof Error
              ? this.unknownOutcomes()
                ? `${error.message.replace(/[.\s]*$/, ".")} /recover reviews it.`
                : error.message
              : "Inference failed";
        status = oneLine(message);
        // The full error joins the transcript as a UI-local error notice:
        // shown (and inline, printed below), but never a model message. In
        // a goal the step limit is a turn boundary, not a failure: a notice.
        notice =
          this.goalTurn && error instanceof StepLimitError
            ? this.builder.notice(message)
            : this.builder.error(message);
        this.transcript.push(notice);
      }
    } finally {
      // The head goes with the turn: the publish below writes it off, and
      // nothing that runs before it may write it on again.
      this.headOn = false;
      this.resumeClock();
      // A cancelled or failed turn ended too: the bar's "last" is the turn
      // that ended last, not an older one.
      if (!finished) this.recordTurn();
      if (this.timer) clearInterval(this.timer);
      this.timer = undefined;
      clearTimeout(this.armTimer);
      this.decide = undefined;
      this.armed = false;
      this.proposal = undefined;
      this.proposalTruncated = false;
      clearTimeout(this.askArmTimer);
      // Never open past its turn: closed (unanswered) if it somehow is.
      if (this.asking) await this.asking.close();
      this.asking = undefined;
      if (this.turn === request) this.turn = undefined;
      // Inline, a cancelled or failed reply still prints what arrived, and
      // a failed turn's notice is committed after it like any finished view.
      if (this.inline) {
        await this.commitStreamTail();
        if (notice) await this.commitView(notice);
      }
      // After the reply is printed, so its warning never comes first.
      if (finished) await this.showPlacement();
      if (this.goalTurn)
        this.goalTurn.commands = commandsOk.flatMap(
          (id) => commandKey(commandArgs.get(id)) ?? [],
        );
      // Before busy clears: a goal that stops says why after its turn,
      // and one that goes on starts its next turn with no gap a send
      // could take (runTurn).
      const goalEnd = await this.settleGoal();
      if (goalEnd) {
        status = goalEnd;
        ambient = false;
      }
      await this.publishMessages([
        { path: "placeholder", value: PLACEHOLDER },
        // The ambient status as of now, not as of the turn's last event: a
        // send queued while the placement or the goal settled is a fact in it.
        { path: "status", value: ambient ? this.idleText() : status },
        ...(this.goal ? [] : [{ path: "header.goal_on", value: false }]),
        { path: "approval", value: noApproval() },
        { path: "question", value: noQuestion() },
        // The turn's last observed usage is the resting baseline (CtxMeter).
        ...this.ctxWrites(this.ctx.finishTurn()),
      ]);
      // Esc or /goal stop that landed after settleGoal, where no cancel
      // could reach a turn: the goal ends here, before another starts.
      // Still busy, so a send meanwhile is queued and runs next (runTurn).
      await this.endStoppedGoal();
      // Busy clears only after the final commits/publish above, so no
      // send or switch can enter while this turn still owes prints.
      this.busy = false;
    }
  }
}
