import { randomUUID } from "node:crypto";
import {
  AdmissionError,
  type AgentEvent,
  StepLimitError,
} from "#runtime/agent";
import type { Provider } from "#runtime/config";
import { ConversationFullError } from "#runtime/session";
import { TunnelLostError } from "#runtime/transport";

/**
 * The turn receipt `chalupa inference report` reads (its contract lives in
 * Chalupa's src/inference-report.ts). It carries counts and states only: no
 * prompt, reply, path or tool output.
 */
export const TURN_RECEIPT_SCHEMA = "local-agent.turn-receipt.v1";

/** Chalupa's identifier rule for `run_id`. */
const RUN_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;
/** Chalupa's rule for the model name. */
const MODEL_NAME = /^[A-Za-z0-9][A-Za-z0-9._:/-]*$/;
const MAX_MODEL_NAME = 200;
const MAX_RUN_ID = 128;
/** Chalupa takes at most this many tool calls; the rest are counted. */
const MAX_TOOL_CALLS = 10_000;

export type ReceiptStatus = "settled" | "canceled" | "failed" | "not_admitted";

/** Why the turn stopped: a closed list, so a receipt never carries an error message. */
export type StopReason =
  | "completed"
  | "interrupted"
  | "not_admitted"
  | "step_limit"
  | "conversation_full"
  | "tunnel_lost"
  | "error";

export interface TurnReceipt {
  schema: typeof TURN_RECEIPT_SCHEMA;
  run_id: string;
  model: { name: string; num_ctx: number; remote: true };
  status: ReceiptStatus;
  stop_reason: StopReason;
  truncated: boolean;
  usage: { prompt_tokens: number; eval_tokens: number };
  tool_calls: { status: "ok" | "error" }[];
  tool_calls_omitted?: number;
  timing?: {
    ttft_ms: number;
    load_ms: number;
    prompt_eval_ms: number;
    eval_ms: number;
    total_ms: number;
  };
}

/** The run ID a receipt names: the one given, checked, or a new UUID. */
export function receiptRunId(given?: string): string {
  if (given === undefined) return randomUUID();
  if (given.length > MAX_RUN_ID || !RUN_ID.test(given))
    throw new Error(
      `--run-id must be 1-${MAX_RUN_ID} letters, digits, ".", "_", ":" or "-", starting with a letter or digit`,
    );
  return given;
}

/** Why a provider cannot have a receipt, or undefined when it can. */
export function receiptProblem(provider: Provider): string | undefined {
  if (provider.type !== "ollama_remote")
    return "--receipt needs a Chalupa profile: the receipt reports inference on a remote GPU (chalupa inference report)";
  // Chalupa would refuse the whole receipt for it; say so before the run.
  if (
    provider.model.length > MAX_MODEL_NAME ||
    !MODEL_NAME.test(provider.model)
  )
    return `--receipt: Chalupa's receipt does not accept the model name ${JSON.stringify(provider.model)}`;
  return undefined;
}

/** The status and stop reason of a turn that ended with `error`. */
export function receiptOutcome(
  error: unknown,
  interrupted: boolean,
): { status: ReceiptStatus; stopReason: StopReason } {
  if (interrupted) return { status: "canceled", stopReason: "interrupted" };
  if (error instanceof AdmissionError)
    return { status: "not_admitted", stopReason: "not_admitted" };
  if (error instanceof StepLimitError)
    return { status: "failed", stopReason: "step_limit" };
  if (error instanceof ConversationFullError)
    return { status: "failed", stopReason: "conversation_full" };
  if (error instanceof TunnelLostError)
    return { status: "failed", stopReason: "tunnel_lost" };
  return { status: "failed", stopReason: "error" };
}

const ms = (nanoseconds: number) => Math.round(nanoseconds / 1e6);

/**
 * Counts one headless turn for its receipt: `observe` every event the run
 * yields, then `finish` with how it ended. Usage sums every model call,
 * a `task` subagent's included. Timing is left out unless Ollama reported
 * the load, prompt and eval durations of every call and a first token came.
 */
export class TurnReceiptRecorder {
  #start: number | undefined;
  #firstToken: number | undefined;
  #promptTokens = 0;
  #evalTokens = 0;
  #calls = 0;
  #timed = 0;
  #loadNs = 0;
  #promptEvalNs = 0;
  #evalNs = 0;
  #truncated = false;
  #tools: { status: "ok" | "error" }[] = [];
  #omitted = 0;

  constructor(
    readonly runId: string,
    readonly provider: Provider,
    private readonly now: () => number = () => performance.now(),
  ) {}

  /** The turn starts now: TTFT and the total are measured from here. */
  start(): void {
    this.#start = this.now();
  }

  observe(event: AgentEvent): void {
    if (
      this.#start !== undefined &&
      this.#firstToken === undefined &&
      (event.type === "text" ||
        event.type === "thinking" ||
        event.type === "tool_call")
    )
      this.#firstToken = this.now();
    if (event.type === "done" || event.type === "subagent_usage") {
      this.#calls++;
      this.#promptTokens += event.inputTokens;
      this.#evalTokens += event.outputTokens;
      if (event.type === "done" && event.finishReason === "length")
        this.#truncated = true;
      const { loadDuration, promptEvalDuration, evalDuration } = event;
      if (
        loadDuration !== undefined &&
        promptEvalDuration !== undefined &&
        evalDuration !== undefined
      ) {
        this.#timed++;
        this.#loadNs += loadDuration;
        this.#promptEvalNs += promptEvalDuration;
        this.#evalNs += evalDuration;
      }
    }
    if (event.type === "tool_result") {
      // Only a call that ran and succeeded is "ok": a denied, failed,
      // unknown or unverified one is not.
      const status = event.status === "succeeded" ? "ok" : "error";
      if (this.#tools.length < MAX_TOOL_CALLS) this.#tools.push({ status });
      else this.#omitted++;
    }
  }

  finish(outcome: {
    status: ReceiptStatus;
    stopReason: StopReason;
  }): TurnReceipt {
    const end = this.now();
    const timed =
      this.#start !== undefined &&
      this.#firstToken !== undefined &&
      this.#calls > 0 &&
      this.#timed === this.#calls;
    return {
      schema: TURN_RECEIPT_SCHEMA,
      run_id: this.runId,
      model: {
        name: this.provider.model,
        num_ctx: this.provider.context_size,
        remote: true,
      },
      status: outcome.status,
      stop_reason: outcome.stopReason,
      truncated: this.#truncated,
      usage: {
        prompt_tokens: this.#promptTokens,
        eval_tokens: this.#evalTokens,
      },
      tool_calls: this.#tools,
      ...(this.#omitted ? { tool_calls_omitted: this.#omitted } : {}),
      ...(timed && this.#start !== undefined && this.#firstToken !== undefined
        ? {
            timing: {
              ttft_ms: Math.round(this.#firstToken - this.#start),
              load_ms: ms(this.#loadNs),
              prompt_eval_ms: ms(this.#promptEvalNs),
              eval_ms: ms(this.#evalNs),
              total_ms: Math.round(end - this.#start),
            },
          }
        : {}),
    };
  }
}
