import { Database } from "bun:sqlite";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent, sessionProblem } from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import type { Message } from "#runtime/index";
import {
  inspectRecovery,
  reconcileExecution,
  recoveryItems,
  settleInterrupted,
} from "#runtime/recovery";
import {
  type ExecutionEvent,
  type Session,
  SessionStore,
  sha256,
} from "#runtime/session";
import { Workspace } from "#runtime/workspace";

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "local-agent-recovery-"));
  const db = join(root, "sessions.sqlite");
  const store = await SessionStore.open(db);
  return {
    root,
    db,
    store,
    async cleanup() {
      store.close();
      await rm(root, { recursive: true, force: true });
    },
  };
}

const call = (
  id: string,
  name: string,
  args: Record<string, unknown> = {},
) => ({
  id,
  function: { name, arguments: args },
});

/** One execution's identity, as runAgent's `prepare` writes it. */
function execution(
  session: Session,
  id: string,
  callId: string,
  name: string,
  ordinal: number,
  effectClass: ExecutionEvent["effectClass"],
  runId = "run-1",
): Omit<ExecutionEvent, "type"> {
  return {
    sessionId: session.id,
    workspaceId: session.workspaceId,
    runId,
    turnId: runId,
    executionId: id,
    idempotencyKey: id,
    canonicalCallId: callId,
    providerCallId: "",
    iteration: 1,
    ordinal,
    kind: "builtin",
    toolName: name,
    effectClass,
    argumentsSha256: sha256("{}"),
  };
}

/** A session whose last assistant message asked for `calls`, unanswered. */
async function interrupted(
  store: SessionStore,
  root: string,
  calls: ReturnType<typeof call>[],
  answered: Message[] = [],
) {
  const created = await store.createSession(root);
  return store.saveStateCAS(created.id, created.workspaceId, 0, [
    { role: "user", content: "go" },
    { role: "assistant", content: "", tool_calls: calls },
    ...answered,
  ]);
}

const record = (
  store: SessionStore,
  base: Omit<ExecutionEvent, "type">,
  ...types: ExecutionEvent["type"][]
) => {
  for (const type of types) store.appendEvent({ ...base, type });
};

const output = (message: Message | undefined) =>
  (JSON.parse(message?.content ?? "{}") as { status: string; output: string })
    .output;

test("calls that never started, or only read, are closed and the session continues", async () => {
  const { root, store, cleanup } = await fixture();
  try {
    const session = await interrupted(store, root, [
      call("c1", "read"),
      call("c2", "write"),
      call("c3", "run_command"),
    ]);
    const read = execution(session, "e1", "c1", "read", 1, "read_only");
    const write = execution(session, "e2", "c2", "write", 2, "effectful");
    record(store, read, "requested", "started");
    record(store, write, "requested", "approval_requested");
    expect(sessionProblem(store, session)).toBe(
      "Session contains interrupted tool calls; recovery is required",
    );
    const { state, closed } = settleInterrupted(store, session);
    expect(closed).toBe(3);
    expect(state.revision).toBe(session.revision + 1);
    expect(sessionProblem(store, state)).toBeUndefined();
    const results = state.messages.slice(2);
    expect(results.map((message) => message.tool_call_id)).toEqual([
      "c1",
      "c2",
      "c3",
    ]);
    expect(output(results[0])).toContain(
      "read-only call ran. It changed nothing",
    );
    expect(output(results[1])).toContain("Not run");
    // No ledger record at all: never dispatched.
    expect(output(results[2])).toContain("Not run");
    const latest = (id: string) =>
      new Database(join(root, "sessions.sqlite"), { readonly: true })
        .query<{ event_type: string; detail: string }, [string]>(
          "SELECT event_type, detail FROM execution_events WHERE execution_id = ? ORDER BY id DESC LIMIT 1",
        )
        .get(id);
    expect(latest("e1")).toEqual({
      event_type: "cancelled",
      detail: "interrupted while running",
    });
    expect(latest("e2")).toEqual({
      event_type: "cancelled",
      detail: "interrupted before it started",
    });
    // Settled once: a second look finds nothing to do.
    expect(settleInterrupted(store, state)).toEqual({ state, closed: 0 });
  } finally {
    await cleanup();
  }
});

test("a call that may have had an effect stops settling; reconciling it records the user's word beside the ledger", async () => {
  const { root, db, store, cleanup } = await fixture();
  try {
    const session = await interrupted(store, root, [
      call("c1", "run_command", { argv: ["bun", "run", "migrate"] }),
      call("c2", "read"),
    ]);
    const command = execution(
      session,
      "e1",
      "c1",
      "run_command",
      1,
      "effectful",
    );
    record(
      store,
      command,
      "requested",
      "approval_requested",
      "approved",
      "started",
    );
    expect(settleInterrupted(store, session)).toEqual({
      state: session,
      closed: 0,
    });
    expect(sessionProblem(store, session)).toBe(
      "Session has an uncertain tool outcome; inspect recovery before continuing",
    );
    expect(recoveryItems(store, session)).toEqual([
      {
        executionId: "e1",
        toolName: "run_command",
        state: "ran",
        summary: "bun run migrate",
        at: expect.any(String),
      },
    ]);
    const raw = () =>
      new Database(db, { readonly: true })
        .query("SELECT * FROM execution_events ORDER BY id")
        .all();
    const before = JSON.stringify(raw());
    const { state, closed } = await reconcileExecution(
      store,
      session.publicId,
      root,
      {
        executionId: "e1",
        disposition: "effect_not_applied",
        note: "  checked\n git status ",
      },
      () => new Date("2026-10-24T20:00:00Z"),
    );
    expect(closed).toBe(2);
    expect(output(state.messages[2])).toBe(
      "The session stopped while this call ran, so it has no result. The user checked: it made no change. Their note: checked git status",
    );
    expect(output(state.messages[3])).toContain("Not run");
    expect(store.listHazards(state.id, state.workspaceId)).toEqual([]);
    expect(sessionProblem(store, state)).toBeUndefined();
    // The ledger is not written for the conclusion.
    expect(JSON.stringify(raw())).toBe(before);
    const row = store.reconciliationOf(state.id, "e1");
    expect(row).toMatchObject({
      disposition: "effect_not_applied",
      note: "checked git status",
      actor: "local user",
      latest_event_type: "started",
    });
    expect(row?.evidence_sha256).toBe(sha256(row?.evidence_json ?? ""));
    expect(JSON.parse(row?.evidence_json ?? "{}")).toMatchObject({
      version: 1,
      executionId: "e1",
      at: "2026-10-24T20:00:00.000Z",
    });
    // Once only, and the rows are immutable.
    await expect(
      reconcileExecution(store, session.publicId, root, {
        executionId: "e1",
        disposition: "effect_applied",
      }),
    ).rejects.toThrow("No execution e1 awaits recovery in this session");
    const writer = new Database(db);
    expect(() =>
      writer.exec("UPDATE execution_reconciliations SET note = 'x'"),
    ).toThrow("reconciliations are append-only");
    expect(() => writer.exec("DELETE FROM execution_reconciliations")).toThrow(
      "reconciliations are append-only",
    );
    writer.close();
  } finally {
    await cleanup();
  }
});

test("a reconciliation must name a hazard of its own session, at its latest event", async () => {
  const { root, db, store, cleanup } = await fixture();
  try {
    const session = await interrupted(store, root, [call("c1", "write")]);
    const write = execution(session, "e1", "c1", "write", 1, "effectful");
    record(store, write, "requested", "started");
    await expect(
      reconcileExecution(store, session.publicId, root, {
        executionId: "nope",
        disposition: "effect_applied",
      }),
    ).rejects.toThrow("No execution nope awaits recovery");
    await expect(
      reconcileExecution(store, session.publicId, root, {
        executionId: "e1",
        disposition: "effect_applied",
        note: "x".repeat(1001),
      }),
    ).rejects.toThrow("A note is at most 1000 characters");
    const latest = store.listHazards(session.id, session.workspaceId)[0];
    const conclusion = {
      sessionId: session.id,
      workspaceId: session.workspaceId,
      executionId: "e1",
      latestEventId: (latest?.id ?? 0) - 1,
      latestEventType: "started" as const,
      disposition: "effect_applied" as const,
      note: "",
      actor: "local user",
      evidenceJson: "{}",
      evidenceSha256: sha256("{}"),
    };
    // A stale event id fails closed, and nothing is saved.
    expect(() =>
      store.recover({
        sessionId: session.id,
        workspaceId: session.workspaceId,
        expectedRevision: session.revision,
        events: [],
        reconciliation: conclusion,
      }),
    ).toThrow("not an open hazard at this event");
    // The table's own guard: the row must match a real event of it.
    const writer = new Database(db);
    expect(() =>
      writer
        .query(
          "INSERT INTO execution_reconciliations(session_id, workspace_id, execution_id, latest_event_id, latest_event_type, disposition, actor, evidence_json, evidence_sha256) VALUES (?, ?, 'e1', ?, 'started', 'effect_applied', 'x', '{}', ?)",
        )
        .run(session.id, session.workspaceId, 999, sha256("{}")),
    ).toThrow("reconciliation does not match its execution");
    writer.close();
    expect(store.reconciliationOf(session.id, "e1")).toBeUndefined();
  } finally {
    await cleanup();
  }
});

test("an unknown result already in the history is reconciled without touching it", async () => {
  const { root, store, cleanup } = await fixture();
  try {
    const unknown = {
      role: "tool" as const,
      tool_name: "fixture__effect",
      tool_call_id: "c1",
      content: JSON.stringify({ status: "outcome_unknown", output: "?" }),
    };
    const session = await interrupted(
      store,
      root,
      [call("c1", "fixture__effect")],
      [unknown],
    );
    const mcp = execution(session, "e1", "c1", "fixture__effect", 1, "unknown");
    record(store, mcp, "requested", "started", "outcome_unknown");
    expect(recoveryItems(store, session)[0]?.state).toBe("unknown");
    const { state, closed } = await reconcileExecution(
      store,
      session.publicId,
      root,
      { executionId: "e1", disposition: "effect_applied" },
    );
    expect(closed).toBe(0);
    expect(state.revision).toBe(session.revision);
    expect(state.messages).toEqual(session.messages);
    expect(sessionProblem(store, state)).toBeUndefined();
  } finally {
    await cleanup();
  }
});

test("settling changes nothing when the ledger does not match the calls exactly", async () => {
  const { root, store, cleanup } = await fixture();
  try {
    // Open executions of two runs: not one interruption.
    const twoRuns = await interrupted(store, root, [
      call("c1", "read"),
      call("c2", "read"),
    ]);
    record(
      store,
      execution(twoRuns, "e1", "c1", "read", 1, "read_only", "run-1"),
      "requested",
    );
    record(
      store,
      execution(twoRuns, "e2", "c2", "read", 2, "read_only", "run-2"),
      "requested",
    );
    expect(settleInterrupted(store, twoRuns).closed).toBe(0);
    // An open execution at another ordinal than its call.
    const moved = await interrupted(store, root, [call("c9", "read")]);
    record(
      store,
      execution(moved, "e9", "c9", "read", 2, "read_only"),
      "requested",
    );
    expect(settleInterrupted(store, moved)).toEqual({
      state: moved,
      closed: 0,
    });
    // A history wrong before its last message is no interruption to close.
    const created = await store.createSession(root);
    const broken = await store.saveStateCAS(
      created.id,
      created.workspaceId,
      0,
      [
        { role: "tool", tool_name: "read", content: "orphan" },
        { role: "assistant", content: "", tool_calls: [call("c1", "read")] },
      ],
    );
    expect(settleInterrupted(store, broken)).toEqual({
      state: broken,
      closed: 0,
    });
  } finally {
    await cleanup();
  }
});

test("recovery refuses an event or a conclusion of another session", async () => {
  const { root, store, cleanup } = await fixture();
  try {
    const mine = await interrupted(store, root, [call("c1", "read")]);
    const other = await interrupted(store, root, [call("c2", "read")]);
    const theirs = execution(other, "e2", "c2", "read", 1, "read_only");
    record(store, theirs, "requested");
    expect(() =>
      store.recover({
        sessionId: mine.id,
        workspaceId: mine.workspaceId,
        expectedRevision: mine.revision,
        events: [{ ...theirs, type: "cancelled" }],
      }),
    ).toThrow("event belongs to another session");
    expect(() =>
      store.recover({
        sessionId: mine.id,
        workspaceId: mine.workspaceId,
        expectedRevision: mine.revision,
        events: [{ ...theirs, sessionId: mine.id, type: "started" }],
      }),
    ).toThrow("recovery appends only terminal events");
  } finally {
    await cleanup();
  }
});

test("a stale revision leaves no event behind", async () => {
  const { root, db, store, cleanup } = await fixture();
  try {
    const session = await interrupted(store, root, [call("c1", "read")]);
    record(
      store,
      execution(session, "e1", "c1", "read", 1, "read_only"),
      "requested",
      "started",
    );
    store.saveStateCAS(session.id, session.workspaceId, session.revision, [
      ...session.messages,
    ]);
    expect(() => settleInterrupted(store, session)).toThrow(
      "session state revision conflict",
    );
    const types = new Database(db, { readonly: true })
      .query<{ event_type: string }, []>(
        "SELECT event_type FROM execution_events ORDER BY id",
      )
      .all()
      .map((row) => row.event_type);
    expect(types).toEqual(["requested", "started"]);
  } finally {
    await cleanup();
  }
});

test("inspection lists the first 100 hazards and counts them all", async () => {
  const { root, store, cleanup } = await fixture();
  try {
    const session = await store.createSession(root);
    for (let index = 1; index <= 150; index++)
      record(
        store,
        execution(
          session,
          `e${index}`,
          `c${index}`,
          "write",
          index,
          "effectful",
        ),
        "requested",
        "started",
      );
    const review = await inspectRecovery(store, session.publicId, root);
    expect(review.total).toBe(150);
    expect(review.hazards).toHaveLength(100);
    expect(review.hazards[0]?.executionId).toBe("e1");
  } finally {
    await cleanup();
  }
});

test("an MCP call whose outcome is unknown can be reconciled, and the next turn runs without replaying it", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-reconcile-"));
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  let executions = 0;
  let chats = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      chats++;
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: chats === 1 ? "" : "Done", ...(chats === 1 ? { tool_calls: [{ function: { name: "fixture__effect", arguments: {} } }] } : {}) }, done: true })}\n`,
      );
    },
  });
  try {
    const session = { store, state: await store.createSession(root) };
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
    );
    const options = {
      workspace,
      session,
      signal: AbortSignal.timeout(5000),
      approve: async () => true,
      mcp: [
        {
          tools: [
            {
              name: "fixture__effect",
              inputSchema: { type: "object" },
              readOnly: false,
            },
          ],
          async callTool(): Promise<never> {
            executions++;
            throw new Error("transport dropped");
          },
          async close() {},
        },
      ],
    };
    const drain = async (messages: Message[]) => {
      const events: AgentEvent[] = [];
      for await (const event of runAgent(provider, messages, options))
        events.push(event);
      return events;
    };
    await expect(
      drain([{ role: "user", content: "Run tool" }]),
    ).rejects.toThrow("Tool outcome is uncertain");
    const blocked = await store.loadSession(session.state.publicId, root);
    const [item] = recoveryItems(store, blocked);
    expect(item?.state).toBe("unknown");
    const { state } = await reconcileExecution(store, blocked.publicId, root, {
      executionId: item?.executionId ?? "",
      disposition: "effect_not_applied",
    });
    session.state = state;
    const events = await drain([
      ...state.messages,
      { role: "user", content: "Carry on" },
    ]);
    expect(events.at(-1)?.type).toBe("turn_complete");
    expect(executions).toBe(1);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});
