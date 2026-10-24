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
import { join } from "node:path";
import {
  type AgentEvent,
  type AgentOptions,
  type ApprovalRequest,
  admitOllamaModel,
  appendRule,
  clearRules,
  evaluate,
  loadPermissionFile,
  type McpConnection,
  type Message,
  type OllamaAdmission,
  type PermissionFile,
  type PermissionRule,
  type Provider,
  permissionRequest,
  permissionsPath,
  proposeRule,
  runAgent,
  type SessionContext,
  trustRules,
  trustStorePath,
  type Workspace,
} from "@local-agent/runtime";
import type { Commit, Host, HostEvent } from "@thelacanians/tuimark";
import { tokensPerSecond } from "#cli/preview";
import { terminalText } from "#cli/text";
import {
  type Block,
  cleanText,
  clip,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  liveToolView,
  type MessageView,
  NO_CARD,
  type ToolReceipt,
  ViewBuilder,
} from "#cli/tuimark/blocks";
import {
  BUSY_STATUS,
  filterPalette,
  type Intent,
  MOUSE_INLINE_STATUS,
  paletteCommand,
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
  APPROVAL_STATUS,
  APPROVAL_TRUNCATED_ALWAYS_STATUS,
  APPROVAL_TRUNCATED_SESSION_STATUS,
  APPROVAL_TRUNCATED_STATUS,
  APPROVAL_UNARMED_ALWAYS_STATUS,
  APPROVAL_UNARMED_SESSION_STATUS,
  APPROVAL_UNARMED_STATUS,
  APPROVAL_WAIT_STATUS,
  approvalState,
  BUSY_STATUS_LINE,
  CtxMeter,
  contextLabel,
  contextPct,
  ctxFlags,
  detailsState,
  grantCovers,
  grantFor,
  grantsLabel,
  idleStatus,
  initialStore,
  type McpServerFacts,
  mcpLines,
  mcpParts,
  NO_DETAILS_STATUS,
  noApproval,
  noDetails,
  noPanel,
  PLACEHOLDER,
  PLACEHOLDER_BUSY,
  panelState,
  type SessionSummary,
  type Store,
  sessionRows,
  statusLines,
  WAITING_STATUS,
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
 * when the process runs with `--session-db`. `load` refuses (throws) a
 * session the runtime would refuse at its first turn — recovery hazards
 * and interrupted tool calls — with the runtime's own words.
 */
export interface SessionsCapability {
  list(): Promise<SessionSummary[]>;
  load(handle: string): Promise<SessionContext>;
  /**
   * `/find`: the workspace's saved sessions searched for `query`, one hit
   * per session (its handle and the first matching snippet), capped by the
   * capability itself. Absent when the process has no session store.
   */
  search?(query: string): Promise<FindHit[]>;
}

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
   * Creates the export file exclusively (`wx`): rejects with EEXIST when
   * the path is taken, so an export never overwrites. Default node fs.
   */
  writeExport?: (path: string, text: string) => Promise<void>;
  /**
   * Validates a candidate provider's model against the Ollama inventory
   * (`/model NAME`). Default the runtime's admission check; tests replace.
   */
  admitModel?: (
    provider: Provider,
    signal: AbortSignal,
  ) => Promise<OllamaAdmission>;
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
  /** Clock for the busy status's elapsed time. Default Date.now. */
  now?: () => number;
}

/**
 * A new approval dialog ignores dialog_confirm for this long. A `y` typed or
 * double-tapped just as the dialog opens must not approve a change nobody has
 * read; deny and cancel always work. The status line shows "y allow" only once
 * the dialog is armed.
 */
export const APPROVAL_ARM_MS = 300;
/** A message as one status line: zod errors and stack-like text span many lines. */
function oneLine(message: string): string {
  return clip(terminalText(message).replace(/\s+/g, " ").trim(), 300);
}

function withSpeed(status: string, count: number, duration: number): string {
  const speed = tokensPerSecond(count, duration);
  return speed ? `${status} · ${speed}` : status;
}

/** Elapsed seconds as the busy status shows them: `12s`, then `1m 05s`. */
export function formatElapsed(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

/** The refused writes the client remembers. */
const MAX_WRITE_ERRORS = 20;
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
  // A session switch starts from a fresh builder: the old one's call-id
  // map belongs to the old conversation.
  private builder = new ViewBuilder();
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
  /** Fullscreen mouse reporting (`/mouse`); default off. Inline never writes it. */
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
  private turnDone: Promise<void> = Promise.resolve();
  private decide?: (
    allowed: boolean,
    session?: boolean,
    status?: string,
  ) => Promise<void>;
  private armed = false;
  private armTimer?: ReturnType<typeof setTimeout>;
  private proposal?: ApprovalRequest;
  /** The open proposal's preview was truncated: `a` must not grant from it. */
  private proposalTruncated = false;
  /**
   * Session grants (`a` on an approval), grant key -> label, in grant order.
   * Memory only: they die with the process and are never persisted. They
   * sit strictly on the approval step — plan intent is refused by the
   * runtime before `approve` is ever called (and again below).
   */
  private readonly grants = new Map<
    string,
    { label: string; prefix?: readonly string[] }
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
   * The latest completed tool receipt, whole, for `/details`. Memory only:
   * never a model message, never persisted; a newer receipt replaces it.
   */
  private lastReceipt?: ToolReceipt;
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
  /** The header ctx meter (see CtxMeter for the exact accumulation rule). */
  private readonly ctx = new CtxMeter();
  /** One message sent while busy, run when the turn ends. A new send replaces it. */
  private queued?: string;
  /** Esc dropped the queue with the cancel: the final status says both. */
  private cancelDroppedQueue = false;
  /** Quit was answered: a queued message must not start after it. */
  private quitting = false;
  /** What the busy status names: waiting, generating, or the last tool result. */
  private activity = WAITING_STATUS;
  /** When the running turn started (the `now` clock), for the elapsed suffix. */
  private turnStartedAt = 0;
  /** The elapsed second last written, so the ticker writes about once a second. */
  private elapsedShown = -1;
  /** Ollama's generation counters, summed over the steps of a turn (nanoseconds). */
  private evalCount = 0;
  private evalDuration = 0;
  private reportedWriteError = false;

  constructor(private readonly options: ClientOptions) {
    this.run = options.run ?? runAgent;
    this.intent = options.intent;
    this.inline = options.presentation === "inline";
    this.stream = new LiveStream(undefined, { inline: this.inline });
    this.transcript = new Transcript(options.windowStep, options.windowSize);
    this.session = options.session;
    this.provider = options.provider;
    this.theme = options.initialTheme ?? "auto";
    this.history = options.session?.state.messages.slice() ?? [];
    // A resumed session shows its saved messages, through the same mapping as a live turn.
    for (const message of this.history) {
      const view = this.builder.fromMessage(message);
      if (view) this.transcript.push(view);
      const receipt = this.builder.receiptOf(message);
      if (receipt) this.lastReceipt = receipt;
      if (message.role === "user" && message.content.trim())
        this.sent.push(message.content);
    }
  }

  /** The store the document starts with (the `data` of spawnHost). */
  initialStore(): Store {
    const { session, mcp = [] } = this.options;
    return initialStore({
      provider: this.provider,
      intent: this.intent,
      sessionId: session?.state.publicId,
      mcpServers: mcp.length,
      workspace: this.options.workspace?.root ?? "",
      inline: this.inline,
      // The inline snapshot below always starts empty; a resumed session is not.
      empty: this.transcript.length === 0,
      // Inline, the region never holds history: attach() commits it instead.
      transcript: this.inline
        ? { messages: [], earlier: { n: 0 } }
        : this.transcript.snapshot(),
    });
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
    ui.on("cancel", () => {
      // Esc cancels the turn and drops the queue with it.
      if (this.queued !== undefined) {
        this.queued = undefined;
        this.cancelDroppedQueue = true;
      }
      this.turn?.abort();
    });
    // The binds name this `quit_request`, not `quit`: `quit` is an engine
    // built-in that ends the session itself and never reaches the parent,
    // so a handler for it would never run (proven against the real host).
    // Answering "quit" from here is what ends the session.
    ui.on("quit_request", () => this.quitRequest());
    ui.on("load_earlier", () => this.loadEarlier());
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
      return this.decide(true, true);
    });
    ui.on("dialog_always", async () => {
      if (!this.decide || !this.proposal) return undefined;
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
      const root = this.options.workspace?.root;
      const request = permissionRequest(this.proposal);
      const proposed = proposeRule(request);
      const label = `${proposed.tool} ${proposed.pattern}`;
      let saved = "saved to .local-agent/permissions.json";
      if (root) {
        // A deny rule that already covers this request is stronger intent
        // than the keypress: say so and keep the dialog open.
        const shadow = evaluate((await this.fileRules()).rules, request);
        if (shadow.effect === "deny") {
          await this.write([
            {
              path: "status",
              value: `Not saved: denied by rule: ${shadow.rule?.pattern}`,
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
      return this.decide(
        true,
        false,
        root
          ? `Always allowed: ${label} (${saved})`
          : `Always allowed: ${label} (no workspace: rule not saved)`,
      );
    });
    ui.on("dialog_cancel", () => this.decide?.(false));
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

  /** The terminal changed size: open previews are wrapped again. */
  async resize(): Promise<void> {
    const writes: Write[] = [];
    const proposal = this.proposal;
    if (proposal) {
      const approval = this.approvalFor(proposal);
      // A narrower terminal wraps more rows: truncation can change with it.
      this.proposalTruncated = approval.truncated;
      writes.push({ path: "approval", value: approval });
    }
    if (this.detailsOpen && this.lastReceipt)
      writes.push({
        path: "details",
        value: this.detailsFor(this.lastReceipt),
      });
    if (this.panelOpen && this.panelText)
      writes.push({ path: "panel", value: this.panelFor(this.panelText) });
    if (writes.length > 0) await this.write(writes);
  }

  private detailsFor(receipt: ToolReceipt) {
    return detailsState(receipt, this.options.columns?.() ?? 80, this.inline);
  }

  /** The writes that close the viewer, or none when it is already closed. */
  private closeDetailsWrite(): Write[] {
    if (!this.detailsOpen) return [];
    this.detailsOpen = false;
    return [{ path: "details", value: noDetails() }];
  }

  /** Esc/q in the viewer: it closes and the status rests at idle again. */
  private async closeDetails(): Promise<void> {
    const writes = this.closeDetailsWrite();
    if (writes.length === 0) return;
    writes.push({
      path: "status",
      value: idleStatus(this.options.mcp?.length ?? 0, this.intent),
    });
    await this.write(writes);
  }

  /**
   * `/details`: the latest receipt whole, in the viewer. Refused while a
   * turn runs or an approval is open — an armed dialog is never covered.
   */
  private async openDetails(): Promise<void> {
    if (this.busy || this.proposal) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const receipt = this.lastReceipt;
    if (!receipt) {
      await this.write([{ path: "status", value: NO_DETAILS_STATUS }]);
      return;
    }
    this.detailsOpen = true;
    await this.write([
      ...this.closePanelWrite(),
      { path: "details", value: this.detailsFor(receipt) },
      { path: "status", value: `Latest tool output: ${receipt.name}` },
    ]);
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
    writes.push({
      path: "status",
      value: idleStatus(this.options.mcp?.length ?? 0, this.intent),
    });
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
      usedTokens: this.ctx.current,
      mcpServers: mcp.length,
      mcpTools: mcp.reduce((total, server) => total + server.tools.length, 0),
    }).join("\n");
    await this.openPanel("Status", "", text, "Status");
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
      mcpLines(this.mcpFacts()).join("\n"),
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
    this.history.length = 0;
    this.history.push(...context.state.messages);
    this.builder = new ViewBuilder();
    const views: MessageView[] = [];
    this.lastReceipt = undefined;
    this.sent.length = 0;
    this.recall = undefined;
    this.recalledFrom = "";
    for (const message of this.history) {
      const view = this.builder.fromMessage(message);
      if (view) views.push(view);
      const receipt = this.builder.receiptOf(message);
      if (receipt) this.lastReceipt = receipt;
      if (message.role === "user" && message.content.trim())
        this.sent.push(message.content);
    }
    this.transcript.replace(views);
    this.ctx.reset();
    const grants = this.grantLabels();
    const closed = this.closePanelWrite();
    // Inline, the loaded history joins the scrollback like a resumed
    // session's does at attach; the region itself holds no history.
    if (this.inline) for (const view of views) await this.commitView(view);
    await this.publishMessages([
      ...closed,
      ...this.closeDetailsWrite(),
      { path: "empty", value: views.length === 0 },
      { path: "header.session", value: `session ${context.state.publicId}` },
      ...this.ctxWrites(0),
      {
        path: "status",
        value:
          `Resumed session ${context.state.publicId} · ${context.state.messages.length} messages` +
          (grants.length ? ` · grants kept: ${grants.join(" · ")}` : ""),
      },
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
      const notice = this.builder.notice(`Export failed: ${message}`);
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
  private async switchModel(name: string): Promise<void> {
    if (this.busy || this.proposal || this.queued !== undefined) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    await this.write([
      { path: "status", value: `Switching model to ${name}…` },
    ]);
    const admit = this.options.admitModel ?? admitOllamaModel;
    let admission: OllamaAdmission;
    try {
      admission = await admit(
        { ...this.provider, model: name },
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
      return;
    }
    if (admission.status !== "validated") {
      await this.write([{ path: "status", value: oneLine(admission.message) }]);
      return;
    }
    this.provider = { ...this.provider, model: admission.model };
    await this.write([
      { path: "status", value: `Model changed to ${admission.model}` },
      { path: "header.model", value: clip(cleanText(admission.model), 40) },
    ]);
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

  /** The busy status: the activity, its elapsed time, and a queued send. */
  private busyStatus(): string {
    const seconds = Math.floor((this.now() - this.turnStartedAt) / 1000);
    this.elapsedShown = seconds;
    const queued = this.queued !== undefined ? " · 1 queued" : "";
    return `${this.activity} · ${formatElapsed(seconds)}${queued}`;
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
   * Names what the busy status shows: the first text/thinking/tool event of
   * the turn turns waiting into generating, and a tool result names itself
   * until the next event. An open approval owns the status line, so it wins:
   * the activity is remembered but not written over it. Not awaited by the
   * event loop: the write serializes on the writer's tail like every other,
   * and event processing must not pause for a status paint (an abort that
   * lands in such a pause can pass a generator still suspended at its yield).
   */
  private noteActivity(activity = BUSY_STATUS_LINE): void {
    if (this.activity === activity) return;
    this.activity = activity;
    if (!this.decide)
      void this.write([{ path: "status", value: this.busyStatus() }]);
  }

  /**
   * Rewrites the busy status when its elapsed second changes (about once a
   * second, from the flush timer). Idle and approval-pending write nothing.
   */
  private async maybeElapsed(): Promise<void> {
    if (!this.busy || this.decide) return;
    const seconds = Math.floor((this.now() - this.turnStartedAt) / 1000);
    if (seconds === this.elapsedShown) return;
    await this.write([{ path: "status", value: this.busyStatus() }]);
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
   * The two-stage quit, shared by ctrl+c and the palette's Quit row: a
   * non-empty draft is cleared first; only an empty composer quits
   * (mid-turn included, as before). Answering "quit" ends the session.
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
    // A command is one line: a pasted block that starts with a slash (a path, a
    // regex) is a message.
    const command = raw.trim();
    const isCommand = command.startsWith("/") && !command.includes("\n");
    if (this.busy) {
      if (isCommand) {
        await this.write([{ path: "status", value: BUSY_STATUS }]);
        return;
      }
      if (!raw.trim()) return;
      // A queue of one: this send runs when the turn ends (after an error
      // too — it is the user's latest intent), and a further send replaces
      // it. It joins the draft history now; its view is only shown (inline:
      // committed) when it actually starts.
      const replaced = this.queued !== undefined;
      this.queued = raw;
      this.remember(raw);
      const firstLine = clip(
        terminalText(raw).split("\n")[0]?.trim() ?? "",
        60,
      );
      await this.write([
        { path: "draft", value: "" },
        {
          path: "status",
          value: replaced
            ? `Queued message replaced: ${firstLine}`
            : `Queued: ${firstLine}`,
        },
      ]);
      return;
    }
    if (!raw.trim()) return;
    this.remember(raw);
    if (isCommand) {
      await this.write([{ path: "draft", value: "" }]);
      await this.runCommand(command);
      return;
    }
    await this.beginTurn(raw);
  }

  // ------------------------------------------------------------------ the history

  /** Adds a sent message to the history that up recalls; a repeat of the last is not added. */
  private remember(text: string): void {
    this.recall = undefined;
    this.recalledFrom = "";
    if (this.sent[this.sent.length - 1] !== text) this.sent.push(text);
    if (this.sent.length > MAX_SENT) this.sent.shift();
  }

  /** `up` on the first line of the composer: the message sent before the one shown. */
  private async recallPrev(ev: HostEvent): Promise<void> {
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
    if (loaded.warning) this.noteActivity(oneLine(loaded.warning));
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
      this.trustOffer = untrusted.length ? [...untrusted] : undefined;
      return untrusted.length
        ? oneLine(
            `Untrusted allow ${plural(untrusted.length)}: ${named(untrusted)} — check .local-agent/permissions.json, then /permissions trust confirm`,
          )
        : "No untrusted allow rules in .local-agent/permissions.json";
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

  private async runCommand(command: string): Promise<void> {
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
      },
      model: this.provider.model,
      theme: this.theme,
      mouse: this.mouse,
      grants: this.grantLabels(),
      ...(rules !== undefined ? { rules } : {}),
    });
    // `/details` opens over controller state (the retained receipt), so
    // its whole outcome — refusal included — is written there.
    if (outcome.details) {
      await this.openDetails();
      return;
    }
    // The panel and export commands likewise run on controller state.
    if (outcome.statusPanel) {
      await this.openStatusPanel();
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
    if (outcome.exportTranscript) {
      await this.exportTranscript(outcome.exportFormat ?? "markdown");
      return;
    }
    if (outcome.model !== undefined) {
      await this.switchModel(outcome.model);
      return;
    }
    if (outcome.find !== undefined) {
      await this.findSessions(outcome.find);
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
    }
    if (outcome.clearGrants) {
      this.grants.clear();
      writes.push(...this.grantsWrites());
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

  private async loadEarlier(): Promise<void> {
    if (this.transcript.widen()) await this.publishWindow();
  }

  // ------------------------------------------------------------------ the palette

  private async paletteOpen(): Promise<void> {
    // An open approval owns the keys (fullscreen's modal traps ctrl+p; the
    // in-flow approval does not, so the palette must not open over it).
    if (this.proposal) return;
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
    // The help overlay is harmless while a turn runs; everything else waits.
    if ((this.busy || this.switching) && command !== "/help") {
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
    this.noteActivity(`denied by rule: ${verdict.rule?.pattern}`);
    return verdict.rule?.pattern ?? "";
  };

  private approve = async (
    proposal: ApprovalRequest,
    signal: AbortSignal,
  ): Promise<boolean> => {
    signal.throwIfAborted();
    // The workspace's permission file outranks everything interactive: a
    // deny rule refuses (even a session grant), an allow rule approves
    // without a card. In plan intent the runtime refuses mutations before
    // this runs, and no rule widens that, so rules are only consulted in
    // build (same guard as the session grants below).
    if (this.intent === "build") {
      const verdict = evaluate(
        (await this.fileRules()).rules,
        permissionRequest(proposal),
      );
      if (verdict.effect === "deny") {
        this.noteActivity(`denied by rule: ${verdict.rule?.pattern}`);
        return false;
      }
      if (verdict.effect === "allow") {
        this.noteActivity(`allowed by rule: ${verdict.rule?.pattern}`);
        return true;
      }
    }
    // Grants never bypass a non-build intent: the runtime refuses plan and
    // chat mutations before this callback runs, and this keeps it true here.
    const grant = grantFor(proposal);
    const grantedLabel =
      this.intent === "build"
        ? [...this.grants.entries()].find(([key, remembered]) =>
            grantCovers({ key, prefix: remembered.prefix }, proposal),
          )?.[1].label
        : undefined;
    if (grantedLabel !== undefined) {
      // The least invasive visible trace: the status flash a tool result
      // gets, naming the grant that let the call through.
      this.noteActivity(`${grantedLabel}: allowed for session`);
      return true;
    }
    return new Promise<boolean>((resolve) => {
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
          this.grants.set(grant.key, {
            label: grant.label,
            prefix: grant.prefix,
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
        // The verdict is the activity until the next event, so the elapsed
        // ticker keeps it (with its suffix) instead of overwriting it.
        this.activity = verdict;
        resolve(allowed);
        await this.write([
          { path: "approval", value: noApproval() },
          { path: "status", value: verdict },
          ...(granted !== undefined ? this.grantsWrites() : []),
        ]);
      };
      // Anything but an armed, explicit "allow" is a deny, the abort included.
      const deny = () => void finish(false);
      this.decide = finish;
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
      ];
      if (this.liveTool) {
        // The card goes in the same write as the dialog, so the two never disagree.
        this.liveTool = {
          ...this.liveTool,
          card: {
            ...this.liveTool.card,
            status: "waiting for approval",
            running: true,
          },
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
          void this.write([
            {
              path: "status",
              value: approval.truncated
                ? APPROVAL_TRUNCATED_STATUS
                : APPROVAL_STATUS,
            },
          ]);
        }, this.options.armMs ?? APPROVAL_ARM_MS);
      });
    });
  };

  private async beginTurn(prompt: string): Promise<void> {
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
    this.activity = WAITING_STATUS;
    this.turnStartedAt = this.now();
    this.elapsedShown = -1;
    this.cancelDroppedQueue = false;
    const message: Message = { role: "user", content: prompt };
    this.history.push(message);
    const view = this.builder.fromMessage(message);
    if (view) this.transcript.push(view);
    // turnDone is set before any await, so idle() can never miss this turn.
    let published: () => void = () => {};
    const promptShown = new Promise<void>((resolve) => {
      published = resolve;
    });
    this.turnDone = this.runTurn(prompt, request, published, view).catch(
      (error) => {
        this.writeErrors.push(
          error instanceof Error ? error.message : String(error),
        );
      },
    );
    await promptShown;
  }

  private async runTurn(
    prompt: string,
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
        { path: "busy", value: true },
        { path: "placeholder", value: PLACEHOLDER_BUSY },
        { path: "status", value: this.busyStatus() },
      ]);
    } finally {
      published();
    }
    this.timer = setInterval(() => this.tick(), this.options.flushMs ?? 33);
    await this.loop(prompt, request);
    // A send queued while busy runs now, after success and after an error
    // alike (it is the user's latest intent); esc and quit already dropped
    // it, and a process-level abort makes a new turn impossible.
    const next = this.queued;
    this.queued = undefined;
    if (next !== undefined && !this.quitting && !this.options.signal.aborted) {
      await this.beginTurn(next);
      await this.turnDone;
    }
  }

  private async loop(prompt: string, request: AbortController): Promise<void> {
    const { workspace, mcp, signal } = this.options;
    // The session this turn checkpoints to (a switch is refused while
    // busy, so it cannot change under the turn).
    const session = this.session;
    let receivedMessage = false;
    let status = idleStatus(mcp?.length ?? 0, this.intent);
    let toolName = "";
    /** A failed turn's notice view, committed (inline) in the finally below. */
    let notice: MessageView | undefined;
    try {
      const turnSignal = AbortSignal.any([signal, request.signal]);
      // A turn aborted before it started (a signal while its first write was
      // in flight) never calls the runtime, so it never takes the session lease.
      turnSignal.throwIfAborted();
      for await (const event of this.run(this.provider, this.history, {
        workspace,
        intent: this.intent,
        approve: this.approve,
        deniedRead: this.deniedRead,
        signal: turnSignal,
        session,
        mcp,
      })) {
        switch (event.type) {
          case "thinking":
            this.noteActivity();
            if (!this.liveTool) this.stream.pushThinking(event.text);
            break;
          case "text":
            this.noteActivity();
            if (!this.liveTool) this.stream.pushText(event.text);
            break;
          case "message": {
            receivedMessage = true;
            this.history.push(event.message);
            const view = this.builder.fromMessage(event.message);
            if (view) this.transcript.push(view);
            // The fullest output the client receives is this message's
            // receipt, before the card view truncates it: keep it whole.
            const receipt = this.builder.receiptOf(event.message);
            if (receipt) this.lastReceipt = receipt;
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
          case "tool_requested":
            this.noteActivity();
            toolName = event.name;
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
            this.noteActivity();
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
          case "tool_result":
            // The result names the busy status (with its elapsed suffix)
            // until the next event, as it did before the ticker existed.
            this.noteActivity(
              terminalText(`${event.name || toolName}: ${event.status}`),
            );
            break;
          case "done": {
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
      status = withSpeed(status, this.evalCount, this.evalDuration);
    } catch (error) {
      const aborted = request.signal.aborted;
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
        if (aborted) this.transcript.pop();
        // The host may be gone already (ctrl+c mid-turn): keep going to the
        // cleanup. A draft typed during the turn is never overwritten, and
        // a queued send (which cleared the draft for its own turn) is the
        // user's newer intent: the prompt is still in the draft history.
        const draft = await this.ui
          ?.get("draft")
          .then((result) => result.value)
          .catch(() => "gone");
        if (!draft && this.queued === undefined)
          await this.write([{ path: "draft", value: prompt }]);
      }
      if (aborted) {
        status = this.cancelDroppedQueue
          ? "Cancelled · queued message dropped"
          : "Cancelled";
      } else {
        const message =
          error instanceof Error ? error.message : "Inference failed";
        status = oneLine(message);
        // The full error joins the transcript as a UI-local notice: shown
        // (and inline, printed below), but never a model message.
        notice = this.builder.notice(message);
        this.transcript.push(notice);
      }
    } finally {
      if (this.timer) clearInterval(this.timer);
      this.timer = undefined;
      clearTimeout(this.armTimer);
      this.decide = undefined;
      this.armed = false;
      this.proposal = undefined;
      this.proposalTruncated = false;
      if (this.turn === request) this.turn = undefined;
      // Inline, a cancelled or failed reply still prints what arrived, and
      // a failed turn's notice is committed after it like any finished view.
      if (this.inline) {
        await this.commitStreamTail();
        if (notice) await this.commitView(notice);
      }
      await this.publishMessages([
        { path: "busy", value: false },
        { path: "placeholder", value: PLACEHOLDER },
        { path: "status", value: status },
        { path: "approval", value: noApproval() },
        // The turn's last observed usage is the resting baseline (CtxMeter).
        ...this.ctxWrites(this.ctx.finishTurn()),
      ]);
      // Busy clears only after the final commits/publish above, so no
      // send or switch can enter while this turn still owes prints.
      this.busy = false;
    }
  }
}
