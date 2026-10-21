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

import {
  type AgentEvent,
  type AgentOptions,
  type ApprovalRequest,
  type McpConnection,
  type Message,
  type Provider,
  runAgent,
  type SessionContext,
  type Workspace,
} from "@local-agent/runtime";
import type { Host, HostEvent } from "@thelacanians/tuimark";
import { terminalText } from "#cli/text";
import {
  clip,
  IDLE_LIVE,
  type LiveView,
  liveToolView,
  streamingView,
  ViewBuilder,
} from "#cli/tuimark/blocks";
import {
  BUSY_STATUS,
  filterPalette,
  type Intent,
  paletteCommand,
  runSlash,
} from "#cli/tuimark/commands";
import {
  APPROVAL_STATUS,
  APPROVAL_TRUNCATED_STATUS,
  APPROVAL_WAIT_STATUS,
  approvalState,
  BUSY_STATUS_LINE,
  contextLabel,
  contextPct,
  idleStatus,
  initialStore,
  noApproval,
  PLACEHOLDER,
  PLACEHOLDER_BUSY,
  type Store,
} from "#cli/tuimark/store";
import { Transcript } from "#cli/tuimark/transcript";

/** The part of the SDK's Host the client uses (a fake implements it in tests). */
export type UiHost = Pick<Host, "on" | "batch" | "get">;

type Write = { path: string; value: unknown };
type RunAgent = (
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
) => AsyncIterable<AgentEvent>;

export interface ClientOptions {
  provider: Provider;
  workspace: Workspace;
  intent: Intent;
  session?: SessionContext;
  mcp?: readonly McpConnection[];
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

/** The refused writes the client remembers. */
const MAX_WRITE_ERRORS = 20;

export class TuimarkClient {
  private ui?: UiHost;
  private readonly run: RunAgent;
  private readonly builder = new ViewBuilder();
  private readonly transcript: Transcript;
  private readonly history: Message[];
  private intent: Intent;
  // the writer
  private tail: Promise<void> = Promise.resolve();
  /** Store writes the host refused (a path the markup does not read, a dead host). */
  readonly writeErrors: string[] = [];
  // the live message
  private liveText = "";
  private liveTool?: LiveView;
  private dirty = false;
  private liveInFlight = false;
  private timer?: ReturnType<typeof setInterval>;
  // the turn
  private busy = false;
  private turn?: AbortController;
  private turnDone: Promise<void> = Promise.resolve();
  private decide?: (allowed: boolean) => Promise<void>;
  private armed = false;
  private armTimer?: ReturnType<typeof setTimeout>;
  private proposal?: ApprovalRequest;
  private usedTokens = 0;
  private reportedWriteError = false;

  constructor(private readonly options: ClientOptions) {
    this.run = options.run ?? runAgent;
    this.intent = options.intent;
    this.transcript = new Transcript(options.windowStep, options.windowSize);
    this.history = options.session?.state.messages.slice() ?? [];
    // A resumed session shows its saved messages, through the same mapping as a live turn.
    for (const message of this.history) {
      const view = this.builder.fromMessage(message);
      if (view) this.transcript.push(view);
    }
  }

  /** The store the document starts with (the `data` of spawnHost). */
  initialStore(): Store {
    const { provider, session, mcp = [] } = this.options;
    return initialStore({
      provider,
      intent: this.intent,
      sessionId: session?.state.publicId,
      mcpServers: mcp.length,
      transcript: this.transcript.snapshot(),
    });
  }

  /** Registers the action handlers on a spawned host. */
  attach(ui: UiHost): void {
    this.ui = ui;
    ui.on("send", (ev) => this.onSend(ev));
    ui.on("cancel", () => {
      this.turn?.abort();
    });
    ui.on("quit", () => {
      this.abort();
      return "quit";
    });
    ui.on("load_earlier", () => this.loadEarlier());
    ui.on("dialog_confirm", () =>
      this.armed ? this.decide?.(true) : undefined,
    );
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
  }

  /** The terminal changed size: an open approval preview is wrapped again. */
  async resize(): Promise<void> {
    const proposal = this.proposal;
    if (!proposal) return;
    await this.write([{ path: "approval", value: this.approvalFor(proposal) }]);
  }

  private approvalFor(proposal: ApprovalRequest) {
    return approvalState(proposal, this.options.columns?.() ?? 80);
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

  /** One serialized write. Never rejects: a refused write is recorded in `writeErrors`. */
  private write(writes: Write[]): Promise<void> {
    const ui = this.ui;
    if (!ui) return Promise.resolve();
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
      this.writeErrors.push(failure);
      if (this.writeErrors.length > MAX_WRITE_ERRORS) this.writeErrors.shift();
      // Say so once on the status line, so a frozen view is not silent.
      if (!this.reportedWriteError) {
        this.reportedWriteError = true;
        const status = `UI update failed: ${terminalText(failure).replace(/\s+/g, " ").slice(0, 200)}`;
        await ui.batch([{ path: "status", value: status }]).catch(() => {});
      }
    });
    this.tail = next;
    return next;
  }

  /** Only the window of the transcript: the live message is left alone. */
  private publishWindow(extra: Write[] = []): Promise<void> {
    const { messages, earlier } = this.transcript.snapshot();
    return this.write([
      { path: "messages", value: messages },
      { path: "earlier", value: earlier },
      ...extra,
    ]);
  }

  /** The window plus the end of whatever was streaming: a later tick must not resend it. */
  private publishMessages(extra: Write[] = []): Promise<void> {
    this.liveText = "";
    this.liveTool = undefined;
    this.dirty = false;
    return this.publishWindow([{ path: "live", value: IDLE_LIVE }, ...extra]);
  }

  private currentLive(): LiveView {
    if (this.liveTool) return this.liveTool;
    return this.liveText ? streamingView(this.liveText) : IDLE_LIVE;
  }

  /** Flushes the live message if it changed and no flush is in flight. */
  private tick(): void {
    if (!this.dirty || this.liveInFlight) return;
    this.dirty = false;
    this.liveInFlight = true;
    void this.write([{ path: "live", value: this.currentLive() }]).finally(
      () => {
        this.liveInFlight = false;
      },
    );
  }

  /** Test hook: flush the live message now and wait for the writer. */
  async flush(): Promise<void> {
    if (this.dirty) {
      this.dirty = false;
      await this.write([{ path: "live", value: this.currentLive() }]);
    }
    await this.tail;
  }

  // --------------------------------------------------------------------- actions

  private async onSend(ev: HostEvent): Promise<void> {
    const raw = typeof ev.value === "string" ? ev.value : "";
    if (this.busy) {
      if (raw.trim().startsWith("/"))
        await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    if (!raw.trim()) return;
    const command = raw.trim();
    if (command.startsWith("/")) {
      await this.write([{ path: "draft", value: "" }]);
      await this.runCommand(command);
      return;
    }
    await this.beginTurn(raw);
  }

  private async runCommand(command: string): Promise<void> {
    const { mcp = [] } = this.options;
    const outcome = runSlash(command, {
      intent: this.intent,
      mcp: {
        servers: mcp.length,
        tools: mcp.reduce((total, server) => total + server.tools.length, 0),
      },
    });
    const writes: Write[] = [{ path: "status", value: outcome.status }];
    if (outcome.intent) {
      this.intent = outcome.intent;
      writes.push({ path: "header.mode", value: outcome.intent });
    }
    if (outcome.help) writes.push({ path: "help.open", value: true });
    await this.write(writes);
  }

  private async loadEarlier(): Promise<void> {
    if (this.transcript.widen()) await this.publishWindow();
  }

  // ------------------------------------------------------------------ the palette

  private async paletteOpen(): Promise<void> {
    const found = filterPalette("");
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
    const found = filterPalette(query);
    await this.write([
      { path: "found", value: { any: found.length > 0, items: found } },
    ]);
  }

  private async paletteRun(): Promise<void> {
    const ui = this.ui;
    if (!ui) return;
    const cur = (await ui.get("palette.cur")).value;
    const query = (await ui.get("palette.query")).value;
    const rows = filterPalette(typeof query === "string" ? query : "");
    const id = rows.find((row) => row.id === cur)?.id ?? rows[0]?.id;
    if (id === undefined) return;
    await this.write([{ path: "palette.open", value: false }]);
    const command = paletteCommand(id);
    // The help overlay is harmless while a turn runs; everything else waits.
    if (this.busy && command !== "/help") {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    if (command) await this.runCommand(command);
  }

  // -------------------------------------------------------------------- one turn

  private approve = (
    proposal: ApprovalRequest,
    signal: AbortSignal,
  ): Promise<boolean> => {
    signal.throwIfAborted();
    return new Promise<boolean>((resolve) => {
      const finish = async (allowed: boolean): Promise<void> => {
        signal.removeEventListener("abort", deny);
        clearTimeout(this.armTimer);
        this.decide = undefined;
        this.armed = false;
        this.proposal = undefined;
        resolve(allowed);
        await this.write([
          { path: "approval", value: noApproval() },
          { path: "status", value: allowed ? "Approved" : "Denied" },
        ]);
      };
      // Anything but an armed, explicit "allow" is a deny, the abort included.
      const deny = () => void finish(false);
      this.decide = finish;
      this.armed = false;
      this.proposal = proposal;
      signal.addEventListener("abort", deny, { once: true });
      const approval = this.approvalFor(proposal);
      const writes: Write[] = [
        { path: "palette.open", value: false },
        { path: "help.open", value: false },
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
    this.busy = true;
    const request = new AbortController();
    this.turn = request;
    this.liveText = "";
    this.liveTool = undefined;
    const message: Message = { role: "user", content: prompt };
    this.history.push(message);
    const view = this.builder.fromMessage(message);
    if (view) this.transcript.push(view);
    // turnDone is set before any await, so idle() can never miss this turn.
    let published: () => void = () => {};
    const promptShown = new Promise<void>((resolve) => {
      published = resolve;
    });
    this.turnDone = this.runTurn(prompt, request, published).catch((error) => {
      this.writeErrors.push(
        error instanceof Error ? error.message : String(error),
      );
    });
    await promptShown;
  }

  private async runTurn(
    prompt: string,
    request: AbortController,
    published: () => void,
  ): Promise<void> {
    try {
      await this.publishMessages([
        { path: "draft", value: "" },
        { path: "busy", value: true },
        { path: "placeholder", value: PLACEHOLDER_BUSY },
        { path: "status", value: BUSY_STATUS_LINE },
      ]);
    } finally {
      published();
    }
    this.timer = setInterval(() => this.tick(), this.options.flushMs ?? 33);
    await this.loop(prompt, request);
  }

  private async loop(prompt: string, request: AbortController): Promise<void> {
    const { provider, workspace, session, mcp, signal } = this.options;
    let receivedMessage = false;
    let status = idleStatus(mcp?.length ?? 0);
    let toolName = "";
    try {
      const turnSignal = AbortSignal.any([signal, request.signal]);
      // A turn aborted before it started (a signal while its first write was
      // in flight) never calls the runtime, so it never takes the session lease.
      turnSignal.throwIfAborted();
      for await (const event of this.run(provider, this.history, {
        workspace,
        intent: this.intent,
        approve: this.approve,
        signal: turnSignal,
        session,
        mcp,
      })) {
        switch (event.type) {
          case "text":
            this.liveText += event.text;
            this.dirty = true;
            break;
          case "message": {
            receivedMessage = true;
            this.history.push(event.message);
            const view = this.builder.fromMessage(event.message);
            if (view) this.transcript.push(view);
            await this.publishMessages();
            break;
          }
          case "tool_requested":
            toolName = event.name;
            this.liveText = "";
            this.liveTool = liveToolView(
              this.builder.takeCall(),
              event.name,
              "requested",
            );
            this.dirty = true;
            break;
          case "tool_start":
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
            await this.write([
              {
                path: "status",
                value: terminalText(
                  `${event.name || toolName}: ${event.status}`,
                ),
              },
            ]);
            break;
          case "done":
            this.usedTokens = event.inputTokens + event.outputTokens;
            break;
          default:
            break;
        }
      }
    } catch (error) {
      // Keep completed messages and tool receipts even when a later request fails.
      if (!session) {
        if (!receivedMessage) {
          this.history.pop();
          this.transcript.pop();
          // The host may be gone already (ctrl+c mid-turn): keep going to the cleanup.
          const draft = await this.ui
            ?.get("draft")
            .then((result) => result.value)
            .catch(() => "gone");
          if (!draft) await this.write([{ path: "draft", value: prompt }]);
        }
      }
      status = request.signal.aborted
        ? "Cancelled"
        : oneLine(error instanceof Error ? error.message : "Inference failed");
    } finally {
      if (this.timer) clearInterval(this.timer);
      this.timer = undefined;
      this.busy = false;
      clearTimeout(this.armTimer);
      this.decide = undefined;
      this.armed = false;
      this.proposal = undefined;
      if (this.turn === request) this.turn = undefined;
      const size = this.options.provider.context_size;
      await this.publishMessages([
        { path: "busy", value: false },
        { path: "placeholder", value: PLACEHOLDER },
        { path: "status", value: status },
        { path: "approval", value: noApproval() },
        {
          path: "header.ctx_label",
          value: contextLabel(this.usedTokens, size),
        },
        { path: "header.ctx_pct", value: contextPct(this.usedTokens, size) },
      ]);
    }
  }
}
