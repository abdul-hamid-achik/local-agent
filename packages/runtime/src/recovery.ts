import { createHash } from "node:crypto";
import { historyProblem } from "#runtime/agent";
import { commandLine } from "#runtime/command";
import type { Message, ToolCall } from "#runtime/index";
import type {
  Disposition,
  EventRow,
  ExecutionEvent,
  Session,
  SessionStore,
} from "#runtime/session";

/** At most this many hazards in one inspection; `total` counts them all. */
const INSPECT_LIMIT = 100;
/** A reconciliation note's length, as the table bounds it. */
export const RECONCILE_NOTE_MAX = 1000;

/** Review tokens deliberately exclude conversation, arguments and raw receipts. */
export async function inspectRecovery(
  store: SessionStore,
  publicId: string,
  workspace: string,
) {
  const initial = await store.loadSession(publicId, workspace);
  const release = await store.acquireLease(initial.id, workspace);
  try {
    const state = await store.loadSession(publicId, workspace);
    const hazards = store.listHazards(state.id, state.workspaceId);
    // The case with the most hazards needs the report most: the first ones
    // and the count, rather than an error.
    return {
      session: state.publicId,
      workspace: state.workspaceId,
      revision: state.revision,
      total: hazards.length,
      hazards: hazards.slice(0, INSPECT_LIMIT).map((event) => ({
        eventId: event.id,
        executionId: event.execution_id,
        turnId: event.turn_id,
        toolName: event.tool_name,
        eventType: event.event_type,
        effectClass: event.effect_class,
        argumentsSha256: event.arguments_sha256,
      })),
    };
  } finally {
    await release();
  }
}

/**
 * The calls of the last assistant message that have no result yet, when
 * the history before that message is whole: a history that is wrong
 * elsewhere is not an interruption to close.
 */
function unanswered(
  messages: readonly Message[],
): { calls: ToolCall[]; first: number } | undefined {
  const index = messages.findLastIndex((message) => message.role !== "tool");
  const message = messages[index];
  if (message?.role !== "assistant" || !message.tool_calls?.length)
    return undefined;
  if (historyProblem(messages.slice(0, index))) return undefined;
  const calls = message.tool_calls;
  const results = messages.slice(index + 1);
  if (
    results.some(
      (result, at) =>
        result.tool_call_id !== calls[at]?.id ||
        result.tool_name !== calls[at]?.function.name,
    )
  )
    return undefined;
  const answered = results.length;
  if (answered >= calls.length) return undefined;
  return { calls: calls.slice(answered), first: answered };
}

const NOT_RUN =
  "Not run: the session stopped before this call started. Run it again if it is still needed.";
const READ_INTERRUPTED =
  "Interrupted: the session stopped while this read-only call ran. It changed nothing; run it again if you need its result.";
const CONCLUSION: Record<Disposition, string> = {
  effect_applied: "it ran and its change is in place",
  effect_not_applied: "it made no change",
  effect_compensated: "its change was undone",
};

const toolResult = (call: ToolCall, status: string, output: string) => ({
  role: "tool" as const,
  tool_name: call.function.name,
  tool_call_id: call.id,
  content: JSON.stringify({ status, output }),
});

const terminal = (row: EventRow, detail: string): ExecutionEvent => ({
  sessionId: row.session_id,
  workspaceId: row.workspace_id,
  runId: row.run_id,
  turnId: row.turn_id,
  executionId: row.execution_id,
  idempotencyKey: row.idempotency_key,
  canonicalCallId: row.canonical_call_id,
  toolName: row.tool_name,
  effectClass: row.effect_class,
  ...(row.provider_call_id !== null
    ? { providerCallId: row.provider_call_id }
    : {}),
  ...(row.iteration !== null ? { iteration: row.iteration } : {}),
  ...(row.ordinal !== null ? { ordinal: row.ordinal } : {}),
  ...(row.kind !== null ? { kind: row.kind } : {}),
  type: "cancelled",
  argumentsSha256: row.arguments_sha256,
  detail,
});

/**
 * What closing a session's interrupted calls takes; `answered` says whether
 * the call a conclusion is about got its result.
 */
type Settlement = {
  events: ExecutionEvent[];
  results: Message[];
  answered: boolean;
};

/**
 * Plans the closing of the last assistant message's unanswered calls, in
 * order (ADR-0006 §2): one with no `started` event never ran, a read-only
 * one that started changed nothing, and one that may have had an effect
 * stops the plan unless it is the call `concluded` is about. Undefined
 * when there is nothing to close, or the ledger does not match the calls
 * exactly (then nothing is changed and the resume check reports it).
 */
function planSettlement(
  store: SessionStore,
  session: Session,
  concluded?: { executionId: string; disposition: Disposition; note: string },
): Settlement | undefined {
  const pending = unanswered(session.messages);
  if (!pending) return undefined;
  // A started call a reconciliation concluded about is settled: a pending
  // one gets its result in the same transaction (reconcileExecution), so
  // what is left of it here is history, not this interruption.
  const open = store
    .listOpenExecutions(session.id, session.workspaceId)
    .filter(
      (row) => !(row.event_type === "started" && storedConclusion(store, row)),
    );
  if (new Set(open.map((row) => row.run_id)).size > 1) return undefined;
  const used = new Set<number>();
  const plan: Settlement = { events: [], results: [], answered: false };
  for (const [offset, call] of pending.calls.entries()) {
    const ordinal = pending.first + offset + 1;
    // One run's canonical call ids are unique (the store refuses a repeat).
    const row = open.find(
      (other) =>
        other.canonical_call_id === call.id &&
        other.tool_name === call.function.name &&
        other.ordinal === ordinal,
    );
    if (!row) {
      // `started` is committed before a call is dispatched, and an
      // execution's end only with its result (commitOutcome, recover): an
      // unanswered call with no open record never ran. An ended record of
      // the same id is another call's, from a run that used the id too.
      plan.results.push(toolResult(call, "failed", NOT_RUN));
      continue;
    }
    used.add(row.id);
    if (row.event_type !== "started") {
      plan.events.push(terminal(row, "interrupted before it started"));
      plan.results.push(toolResult(call, "failed", NOT_RUN));
    } else if (row.effect_class === "read_only") {
      plan.events.push(terminal(row, "interrupted while running"));
      plan.results.push(toolResult(call, "failed", READ_INTERRUPTED));
    } else {
      // Results follow their calls in order: the rest waits for it.
      if (concluded?.executionId !== row.execution_id) break;
      plan.answered = true;
      plan.results.push(concludedResult(call, concluded));
    }
  }
  // An open execution no unanswered call accounts for is not this
  // session's interruption, unless it is the one being concluded.
  if (
    open.some(
      (row) => !used.has(row.id) && row.execution_id !== concluded?.executionId,
    )
  )
    return undefined;
  return plan;
}

/** The result of a call that has none, with what the user concluded. */
const concludedResult = (
  call: ToolCall,
  concluded: { disposition: Disposition; note: string },
) =>
  toolResult(
    call,
    concluded.disposition === "effect_applied" ? "unverified" : "failed",
    `The session stopped while this call ran, so it has no result. The user checked: ${CONCLUSION[concluded.disposition]}.${concluded.note ? ` Their note: ${concluded.note}` : ""}`,
  );

function storedConclusion(store: SessionStore, row: EventRow) {
  const found = store.reconciliationOf(row.session_id, row.execution_id);
  return found && found.latest_event_id === row.id ? found : undefined;
}

/**
 * Whether a hazard's call still awaits its result: it is one of the last
 * assistant message's unanswered calls (an earlier message's call with the
 * same id, from a run that used the id too, does not count). A history
 * broken elsewhere counts as awaiting, so nothing is recorded against it.
 */
function awaitsResult(messages: readonly Message[], row: EventRow): boolean {
  const pending = unanswered(messages);
  if (!pending) return historyProblem(messages) !== undefined;
  return pending.calls.some((call) => call.id === row.canonical_call_id);
}

/**
 * Closes the interrupted calls of a saved session that never ran or only
 * read (ADR-0006 §2), under the session lease the caller holds, and
 * returns the session as saved. A call that may have had an effect is left
 * for `reconcileExecution`; `closed` counts what was closed.
 */
export function settleInterrupted(
  store: SessionStore,
  session: Session,
): { state: Session; closed: number } {
  const plan = planSettlement(store, session);
  if (!plan?.results.length) return { state: session, closed: 0 };
  const state = store.recover({
    sessionId: session.id,
    workspaceId: session.workspaceId,
    expectedRevision: session.revision,
    events: plan.events,
    messages: [...session.messages, ...plan.results],
  });
  return { state, closed: plan.results.length };
}

/**
 * `settleInterrupted` for a saved session by its public id, under its
 * lease: what a client runs as it loads a session (ADR-0006 §2).
 */
export async function settleSession(
  store: SessionStore,
  publicId: string,
  workspace: string,
): Promise<{ state: Session; closed: number }> {
  const initial = await store.loadSession(publicId, workspace);
  const release = await store.acquireLease(initial.id, workspace);
  try {
    return settleInterrupted(
      store,
      await store.loadSession(publicId, workspace),
    );
  } finally {
    await release();
  }
}

/** One execution a session cannot continue past, as a client lists it. */
export type RecoveryItem = Readonly<{
  executionId: string;
  toolName: string;
  /** `ran`: it started and has no result; `unknown`: its result was unclear. */
  state: "ran" | "unknown";
  /** The call's arguments in brief, from the session's own history. */
  summary: string;
  at: string;
}>;

/** The session's hazards, oldest first, with what the history says of each. */
export function recoveryItems(
  store: SessionStore,
  session: Session,
): RecoveryItem[] {
  // By id and name, the latest holding them: a provider may repeat ids
  // across turns.
  const calls = new Map<string, ToolCall>();
  for (const message of session.messages)
    for (const call of message.tool_calls ?? [])
      if (call.id) calls.set(`${call.id}\0${call.function.name}`, call);
  return store.listHazards(session.id, session.workspaceId).map((row) => {
    const call = calls.get(`${row.canonical_call_id}\0${row.tool_name}`);
    const argv = call?.function.arguments.argv;
    const summary = Array.isArray(argv)
      ? commandLine(argv.map(String))
      : call
        ? JSON.stringify(call.function.arguments)
        : "";
    return {
      executionId: row.execution_id,
      toolName: row.tool_name,
      state: row.event_type === "started" ? "ran" : "unknown",
      summary: summary.length > 200 ? `${summary.slice(0, 199)}…` : summary,
      at: row.created_at,
    };
  });
}

/**
 * Records the user's conclusion about one execution whose outcome is
 * unknown (ADR-0006 §3), under the session lease: the execution must still
 * be a hazard at the event the conclusion is about. When it was the
 * unanswered call that stopped settling, its result says what the user
 * concluded and the calls after it are settled too. The ledger is not
 * written; nothing is run again.
 */
export async function reconcileExecution(
  store: SessionStore,
  publicId: string,
  workspace: string,
  conclusion: { executionId: string; disposition: Disposition; note?: string },
  now: () => Date = () => new Date(),
): Promise<{ state: Session; closed: number }> {
  const note = (conclusion.note ?? "").replace(/\s+/g, " ").trim();
  if (note.length > RECONCILE_NOTE_MAX)
    throw new Error(`A note is at most ${RECONCILE_NOTE_MAX} characters`);
  const initial = await store.loadSession(publicId, workspace);
  const release = await store.acquireLease(initial.id, workspace);
  try {
    const session = await store.loadSession(publicId, workspace);
    const hazard = store
      .listHazards(session.id, session.workspaceId)
      .find((row) => row.execution_id === conclusion.executionId);
    if (!hazard)
      throw new Error(
        `No execution ${conclusion.executionId} awaits recovery in this session`,
      );
    const latestEventType =
      hazard.event_type === "started" ? "started" : "outcome_unknown";
    const evidenceJson = JSON.stringify({
      version: 1,
      session: session.publicId,
      workspace: session.workspaceId,
      executionId: hazard.execution_id,
      latestEventId: hazard.id,
      latestEventType,
      argumentsSha256: hazard.arguments_sha256,
      disposition: conclusion.disposition,
      note,
      actor: "local user",
      at: now().toISOString(),
    });
    const plan = planSettlement(store, session, {
      executionId: hazard.execution_id,
      disposition: conclusion.disposition,
      note,
    });
    // A call with no result gets one with the conclusion, or nothing is
    // recorded: a hidden hazard whose call stays unanswered is a session
    // that can neither continue nor be recovered.
    if (awaitsResult(session.messages, hazard) && !plan?.answered)
      throw new Error(
        plan
          ? "Record what happened to the earlier call first: a session answers its calls in order"
          : "This session's history does not match its ledger here, so nothing was recorded",
      );
    const state = store.recover({
      sessionId: session.id,
      workspaceId: session.workspaceId,
      expectedRevision: session.revision,
      events: plan?.events ?? [],
      ...(plan?.results.length
        ? { messages: [...session.messages, ...plan.results] }
        : {}),
      reconciliation: {
        sessionId: session.id,
        workspaceId: session.workspaceId,
        executionId: hazard.execution_id,
        latestEventId: hazard.id,
        latestEventType,
        disposition: conclusion.disposition,
        note,
        actor: "local user",
        evidenceJson,
        evidenceSha256: createHash("sha256").update(evidenceJson).digest("hex"),
      },
    });
    return { state, closed: plan?.results.length ?? 0 };
  } finally {
    await release();
  }
}
