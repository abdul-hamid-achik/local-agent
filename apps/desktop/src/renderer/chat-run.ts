/**
 * The Chat view's run: its transcript, stream, approval cards and lifecycle.
 * It lives outside the view, so navigating away never ends a run. `ChatRun`
 * reaches the sidecar only through `ChatApi`, so it is tested without
 * Electron; `useChat` wires the real one.
 */
import { reactive } from "vue";
import {
  api,
  errorMessage,
  randomRunId,
  subscribeStream,
} from "#desktop/renderer/api";
import type { AgentStreamEvent, ChatMessage } from "#desktop/shared/protocol";

/** `ended`: the run stopped waiting on the card before it was decided. */
export type ApprovalStatus = "pending" | "approved" | "denied" | "ended";

export type Approval = Extract<
  AgentStreamEvent,
  { type: "approval_requested" }
> & { status: ApprovalStatus };

export type Receipt = {
  id: string;
  name: string;
  status: string;
  output: string;
};

export type StartParams = {
  runId: string;
  messages: ChatMessage[];
  workspace: string;
  intent: "plan" | "build";
  model?: string;
  sessionDb?: string;
  resume?: string;
  mcpConfig?: string;
};

export type ChatApi = {
  start: (
    params: StartParams,
  ) => Promise<{ runId: string; sessionId?: string }>;
  decide: (
    runId: string,
    token: string,
    allowed: boolean,
  ) => Promise<{ decided: boolean }>;
  cancel: (runId: string) => Promise<{ cancelled: boolean }>;
  subscribe: (
    runId: string,
    callback: (event: AgentStreamEvent) => void,
  ) => () => void;
  newRunId: () => string;
};

const THINKING_SHOWN = 2000;
const RECEIPT_SHOWN = 4000;

export class ChatRun {
  readonly state = reactive({
    messages: [] as ChatMessage[],
    liveText: "",
    liveThinking: "",
    shownThinking: "",
    receipts: [] as Receipt[],
    approvals: [] as Approval[],
    usage: undefined as
      | { inputTokens: number; outputTokens: number }
      | undefined,
    running: false,
    notice: "",
    error: "",
    sessionId: "",
  });
  private runId = "";
  private unsubscribe: (() => void) | undefined;
  private readonly deciding = new Set<string>();

  /** Called with each stream event, after the state has taken it. */
  onEvent: () => void = () => undefined;

  constructor(private readonly api: ChatApi) {}

  /**
   * Sends `prompt` as a new turn. Resolves once the sidecar answered the
   * start, with the session it runs in, if any; the run goes on streaming.
   */
  async send(
    prompt: string,
    params: Omit<StartParams, "runId" | "messages">,
  ): Promise<string | undefined> {
    const state = this.state;
    if (state.running) return undefined;
    state.running = true;
    state.receipts = [];
    // An earlier run's cards are settled; a new run starts with none.
    state.approvals = [];
    state.usage = undefined;
    // What a stopped run streamed stays on view only until the next turn.
    state.liveText = "";
    state.liveThinking = "";
    state.shownThinking = "";
    state.messages.push({ role: "user", content: prompt });
    const runId = this.api.newRunId();
    this.runId = runId;
    this.unsubscribe = this.api.subscribe(runId, (event) => {
      this.handle(event);
      this.onEvent();
    });
    try {
      const started = await this.api.start({
        ...params,
        runId,
        messages: state.messages,
      });
      if (started.sessionId) state.sessionId = started.sessionId;
      return started.sessionId;
    } catch (error) {
      if (this.runId === runId) {
        state.error = errorMessage(error);
        this.finish();
      }
      return undefined;
    }
  }

  /**
   * Asks the sidecar to cancel the run. A sidecar that does not know it (it
   * was restarted, or the run ended as Stop was pressed) sends no run_done
   * for it, so the run is finished here.
   */
  async stop(): Promise<void> {
    const runId = this.runId;
    if (!runId) return;
    let cancelled = false;
    try {
      cancelled = (await this.api.cancel(runId)).cancelled;
    } catch {
      // The sidecar is gone, and the run went with it.
    }
    if (!cancelled && this.runId === runId) {
      this.state.notice = "The run had already ended.";
      this.finish();
    }
  }

  /** Approves or denies a card. One the run no longer waits on is settled as ended. */
  async decide(approval: Approval, allowed: boolean): Promise<void> {
    if (approval.status !== "pending" || this.deciding.has(approval.token))
      return;
    // A pending card belongs to the running run: finish() settles the rest.
    this.deciding.add(approval.token);
    try {
      const result = await this.api.decide(this.runId, approval.token, allowed);
      // Not decided: the run was not waiting on this card (it ended, or a
      // cancel resolved it as a denial). Nothing was approved.
      if (!result.decided) approval.status = "ended";
      else approval.status = allowed ? "approved" : "denied";
    } catch (error) {
      // The card stays open so the decision can be retried.
      this.state.error = errorMessage(error);
    } finally {
      this.deciding.delete(approval.token);
    }
  }

  /** The sidecar exited or was replaced: a run on it ended with it. */
  sidecarGone(message: string): void {
    if (!this.state.running) return;
    this.state.error = message;
    this.finish();
  }

  /** Empties the transcript; a running turn keeps it. */
  clear(): void {
    if (this.state.running) return;
    const state = this.state;
    state.messages = [];
    state.receipts = [];
    state.approvals = [];
    state.liveText = "";
    state.liveThinking = "";
    state.shownThinking = "";
    state.usage = undefined;
    state.notice = "";
    state.error = "";
    state.sessionId = "";
  }

  private handle(event: AgentStreamEvent): void {
    const state = this.state;
    switch (event.type) {
      case "text":
        state.liveText += event.text;
        break;
      case "thinking":
        state.liveThinking += event.text;
        state.shownThinking =
          state.liveThinking.length > THINKING_SHOWN
            ? `${state.liveThinking.slice(-THINKING_SHOWN)}…`
            : state.liveThinking;
        break;
      case "message":
        state.messages.push(event.message);
        state.liveText = "";
        state.liveThinking = "";
        state.shownThinking = "";
        break;
      case "tool_result":
        state.receipts.push({
          id: event.id,
          name: event.name,
          status: event.status,
          output: event.output.slice(0, RECEIPT_SHOWN),
        });
        break;
      case "approval_requested":
        state.approvals.push({ ...event, status: "pending" });
        break;
      case "done":
        state.usage = {
          inputTokens: event.inputTokens,
          outputTokens: event.outputTokens,
        };
        break;
      case "run_error":
        state.error = event.message;
        this.finish();
        break;
      case "run_done":
        this.finish();
        break;
      default:
        break;
    }
  }

  private finish(): void {
    this.state.running = false;
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    this.runId = "";
    // The run waits on no card now: an open one would take a click it can
    // never apply.
    for (const approval of this.state.approvals)
      if (approval.status === "pending") approval.status = "ended";
  }
}

let shared: ChatRun | undefined;

/**
 * The app's one Chat run, wired to the sidecar on first use. It outlives the
 * view, and a sidecar that exits or is replaced ends its run.
 */
export function useChat(): ChatRun {
  if (shared) return shared;
  const chat = new ChatRun({
    start: api.agentStart,
    decide: api.agentDecide,
    cancel: api.agentCancel,
    subscribe: (runId, callback) =>
      subscribeStream(runId, (event) => callback(event as AgentStreamEvent)),
    newRunId: randomRunId,
  });
  api.onSidecarExit((_code, message) =>
    chat.sidecarGone(message ?? "Sidecar exited during the run."),
  );
  api.onSidecarRestart(() =>
    chat.sidecarGone("The sidecar restarted, which ended the run."),
  );
  shared = chat;
  return chat;
}
