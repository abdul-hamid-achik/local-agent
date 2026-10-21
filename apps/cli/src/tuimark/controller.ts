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
import { terminalText } from "@local-agent/vue-tui/text";
import type { Host, HostEvent } from "@thelacanians/tuimark";
import {
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
  approvalState,
  BUSY_STATUS_LINE,
  contextLabel,
  contextPct,
  IDLE_STATUS,
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
}

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
  private usedTokens = 0;

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
    ui.on("dialog_confirm", () => this.decide?.(true));
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
      try {
        const ack = await ui.batch(writes);
        if (!ack.ok) this.writeErrors.push(ack.error ?? "write refused");
      } catch (error) {
        this.writeErrors.push(
          error instanceof Error ? error.message : String(error),
        );
      }
    });
    this.tail = next;
    return next;
  }

  private publishMessages(extra: Write[] = []): Promise<void> {
    // Whatever was streaming is over: a later tick must not resend it.
    this.liveText = "";
    this.liveTool = undefined;
    this.dirty = false;
    const { messages, earlier } = this.transcript.snapshot();
    return this.write([
      { path: "messages", value: messages },
      { path: "earlier", value: earlier },
      { path: "live", value: IDLE_LIVE },
      ...extra,
    ]);
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
    if (this.transcript.widen()) await this.publishMessages();
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
    if (this.busy) {
      await this.write([{ path: "status", value: BUSY_STATUS }]);
      return;
    }
    const command = paletteCommand(id);
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
        this.decide = undefined;
        resolve(allowed);
        await this.write([
          { path: "approval", value: noApproval() },
          { path: "status", value: allowed ? "Approved" : "Denied" },
        ]);
      };
      const deny = () => void finish(false);
      this.decide = finish;
      signal.addEventListener("abort", deny, { once: true });
      const writes: Write[] = [
        { path: "palette.open", value: false },
        { path: "help.open", value: false },
        { path: "approval", value: approvalState(proposal) },
        { path: "status", value: APPROVAL_STATUS },
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
      void this.write(writes);
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
    await this.publishMessages([
      { path: "draft", value: "" },
      { path: "busy", value: true },
      { path: "placeholder", value: PLACEHOLDER_BUSY },
      { path: "status", value: BUSY_STATUS_LINE },
    ]);
    this.timer = setInterval(() => this.tick(), this.options.flushMs ?? 33);
    this.turnDone = this.loop(prompt, request).catch((error) => {
      this.writeErrors.push(
        error instanceof Error ? error.message : String(error),
      );
    });
  }

  private async loop(prompt: string, request: AbortController): Promise<void> {
    const { provider, workspace, session, mcp, signal } = this.options;
    let receivedMessage = false;
    let status = IDLE_STATUS;
    let toolName = "";
    try {
      for await (const event of this.run(provider, this.history, {
        workspace,
        intent: this.intent,
        approve: this.approve,
        signal: AbortSignal.any([signal, request.signal]),
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
          const draft = (await this.ui?.get("draft"))?.value;
          if (!draft) await this.write([{ path: "draft", value: prompt }]);
        }
      }
      status = request.signal.aborted
        ? "Cancelled"
        : terminalText(
            error instanceof Error ? error.message : "Inference failed",
          );
    } finally {
      if (this.timer) clearInterval(this.timer);
      this.timer = undefined;
      this.busy = false;
      this.decide = undefined;
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
