import { Database } from "bun:sqlite";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, realpath, stat, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { type ExecutionEvent, SessionStore, sha256 } from "./session";

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "local-agent-session-"));
  const db = join(root, "sessions.sqlite");
  const workspace = join(root, "workspace");
  await writeFile(join(root, "marker"), "ok");
  await (await import("node:fs/promises")).mkdir(workspace);
  return { db, workspace };
}

/** The result of `event`'s call, as a history holds it. */
const answer = {
  role: "tool" as const,
  content: "done",
  tool_name: "read",
  tool_call_id: "call-1",
};

function event(
  sessionId: number,
  workspaceId: string,
  type: ExecutionEvent["type"],
): ExecutionEvent {
  return {
    sessionId,
    workspaceId,
    runId: "run-1",
    turnId: "turn-1",
    executionId: "exec-1",
    idempotencyKey: "idem-1",
    canonicalCallId: "call-1",
    toolName: "read",
    effectClass: "effectful",
    type,
    argumentsSha256: sha256("args"),
  };
}

async function seedV1Database(
  path: string,
  workspaceId: string,
): Promise<void> {
  const db = new Database(path);
  db.exec(`
    CREATE TABLE schema_meta (version INTEGER NOT NULL);
    INSERT INTO schema_meta(version) VALUES (1);
    CREATE TABLE sessions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      public_id TEXT NOT NULL UNIQUE,
      workspace_id TEXT NOT NULL,
      state_json TEXT NOT NULL DEFAULT '[]',
      revision INTEGER NOT NULL DEFAULT 0,
      created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
      updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
    );
    CREATE TABLE execution_events (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      workspace_id TEXT NOT NULL,
      run_id TEXT NOT NULL,
      turn_id TEXT NOT NULL,
      execution_id TEXT NOT NULL,
      idempotency_key TEXT NOT NULL,
      canonical_call_id TEXT NOT NULL,
      tool_name TEXT NOT NULL,
      effect_class TEXT NOT NULL,
      event_type TEXT NOT NULL,
      approval TEXT NOT NULL DEFAULT '',
      arguments_sha256 TEXT NOT NULL,
      result_sha256 TEXT NOT NULL DEFAULT '',
      result_receipt TEXT NOT NULL DEFAULT '',
      detail TEXT NOT NULL DEFAULT '',
      created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
      UNIQUE(execution_id, event_type), UNIQUE(idempotency_key, event_type)
    );
    INSERT INTO sessions(id, public_id, workspace_id, state_json, revision)
      VALUES (1, 'legacy1', '${workspaceId.replaceAll("'", "''")}', '[{"role":"user","content":"legacy prompt"},{"role":"assistant","content":"legacy reply"}]', 2);
    INSERT INTO execution_events(session_id, workspace_id, run_id, turn_id, execution_id, idempotency_key, canonical_call_id, tool_name, effect_class, event_type, arguments_sha256, result_receipt)
      VALUES (1, '${workspaceId.replaceAll("'", "''")}', 'run-legacy', 'turn-legacy', 'exec-legacy', 'idem-legacy', 'call-legacy', 'read', 'read_only', 'requested', '${sha256("legacy")}', 'legacy-request');
    INSERT INTO execution_events(session_id, workspace_id, run_id, turn_id, execution_id, idempotency_key, canonical_call_id, tool_name, effect_class, event_type, arguments_sha256, result_receipt)
      VALUES (1, '${workspaceId.replaceAll("'", "''")}', 'run-legacy', 'turn-legacy', 'exec-legacy', 'idem-legacy', 'call-legacy', 'read', 'read_only', 'started', '${sha256("legacy")}', 'legacy-started');
  `);
  db.close();
}

test("creates and lists sessions by canonical workspace with lossless CAS state", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const created = await store.createSession(workspace, [
    { role: "user", content: "hello" },
  ]);
  expect(created.publicId).toMatch(/^[0-9a-z]{7}$/);
  expect(await store.listSessions(workspace)).toHaveLength(1);
  const next = store.saveStateCAS(created.id, created.workspaceId, 0, [
    { role: "assistant", content: "a" },
  ]);
  expect(next.revision).toBe(1);
  expect(
    (await store.loadSession(created.publicId, workspace)).messages[0]?.content,
  ).toBe("a");
  expect(() =>
    store.saveStateCAS(created.id, created.workspaceId, 0, []),
  ).toThrow("revision conflict");
  store.close();
});

test("keeps sessions isolated by workspace", async () => {
  const { db, workspace } = await fixture();
  const other = await mkdtemp(join(tmpdir(), "local-agent-other-"));
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  await expect(store.loadSession(session.publicId, other)).rejects.toThrow(
    "session not found",
  );
  store.close();
});

test("migrates v1 execution history to v3 without inventing identity metadata", async () => {
  const { db, workspace } = await fixture();
  await seedV1Database(db, await realpath(workspace));
  const store = await SessionStore.open(db);
  const loaded = await store.loadSession("legacy1", workspace);
  expect(loaded.revision).toBe(2);
  expect(loaded.messages).toEqual([
    { role: "user", content: "legacy prompt" },
    { role: "assistant", content: "legacy reply" },
  ]);
  const raw = (store as unknown as { db: Database }).db;
  const columns = raw
    .query<{ name: string }, []>("PRAGMA table_info(execution_events)")
    .all()
    .map((column) => column.name);
  expect(columns).toEqual(
    expect.arrayContaining([
      "provider_call_id",
      "iteration",
      "ordinal",
      "kind",
    ]),
  );
  const rows = raw
    .query<
      { event_type: string; result_receipt: string; iteration: number | null },
      []
    >(
      "SELECT event_type, result_receipt, iteration FROM execution_events ORDER BY id",
    )
    .all();
  expect(rows).toEqual([
    {
      event_type: "requested",
      result_receipt: "legacy-request",
      iteration: null,
    },
    {
      event_type: "started",
      result_receipt: "legacy-started",
      iteration: null,
    },
  ]);
  expect(
    raw.query<{ version: number }, []>("SELECT version FROM schema_meta").get()
      ?.version,
  ).toBe(3);
  store.close();
});

test("migrates a v2 database to v3: its history kept, reconciliations added, reopening a no-op", async () => {
  const { db, workspace } = await fixture();
  const created = await SessionStore.open(db);
  const session = await created.createSession(workspace, [
    { role: "user", content: "kept" },
  ]);
  created.close();
  // Back to what a v2 database is: no reconciliations table.
  const downgrade = new Database(db);
  downgrade.exec(
    "DROP TABLE execution_reconciliations; UPDATE schema_meta SET version = 2;",
  );
  downgrade.close();
  for (let open = 0; open < 2; open++) {
    const store = await SessionStore.open(db);
    expect(
      (await store.loadSession(session.publicId, workspace)).messages,
    ).toEqual([{ role: "user", content: "kept" }]);
    const raw = (store as unknown as { db: Database }).db;
    expect(
      raw
        .query<{ version: number }, []>("SELECT version FROM schema_meta")
        .get()?.version,
    ).toBe(3);
    expect(
      raw
        .query<{ name: string }, []>(
          "SELECT name FROM sqlite_master WHERE name LIKE 'execution_reconciliations%' ORDER BY name",
        )
        .all()
        .map((row) => row.name),
    ).toEqual([
      "execution_reconciliations",
      "execution_reconciliations_no_delete",
      "execution_reconciliations_no_update",
      "execution_reconciliations_scope_guard",
    ]);
    store.close();
  }
});

test("enforces append-only lifecycle and projects effect hazards", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  const requested = event(session.id, session.workspaceId, "requested");
  store.appendEvent(requested);
  store.appendEvent(event(session.id, session.workspaceId, "started"));
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  store.commitOutcome(
    event(session.id, session.workspaceId, "outcome_unknown"),
    0,
    [answer],
  );
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  expect(() =>
    store.commitOutcome(
      event(session.id, session.workspaceId, "completed"),
      1,
      [answer],
    ),
  ).toThrow("illegal");
  expect(() =>
    store.appendEvent({ ...requested, argumentsSha256: sha256("different") }),
  ).toThrow("identity conflict");
  store.close();
});

test("rejects independent idempotency collisions and changed v2 ordinal identity", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  const metadata = {
    providerCallId: "provider-1",
    iteration: 1,
    ordinal: 1,
    kind: "builtin" as const,
  };
  const requested = {
    ...event(session.id, session.workspaceId, "requested"),
    ...metadata,
  };
  store.appendEvent(requested);
  expect(() =>
    store.appendEvent({
      ...requested,
      executionId: "different-execution",
      type: "requested",
    }),
  ).toThrow("identity conflict");
  expect(() =>
    store.appendEvent({
      ...requested,
      executionId: "slot-execution",
      idempotencyKey: "slot-idempotency",
      canonicalCallId: "slot-call",
      type: "requested",
    }),
  ).toThrow("logical slot conflict");
  store.appendEvent({ ...requested, type: "started" });
  expect(() =>
    store.commitOutcome({ ...requested, type: "completed", ordinal: 2 }, 0, [
      answer,
    ]),
  ).toThrow("identity conflict");
  expect(() =>
    store.commitOutcome({ ...requested, type: "failed", kind: undefined }, 0, [
      answer,
    ]),
  ).toThrow("metadata");
  expect(() =>
    store.appendEvent({
      ...requested,
      executionId: "canonical-collision",
      idempotencyKey: "canonical-collision-idem",
      ordinal: 2,
      type: "requested",
    }),
  ).toThrow("canonical call collision");
  store.close();
});

test("serializes concurrent v1 migration openers", async () => {
  const { db, workspace } = await fixture();
  await seedV1Database(db, await realpath(workspace));
  const opened = await Promise.all([
    SessionStore.open(db),
    SessionStore.open(db),
  ]);
  const raw = (opened[0] as unknown as { db: Database }).db;
  expect(
    raw.query<{ version: number }, []>("SELECT version FROM schema_meta").get()
      ?.version,
  ).toBe(3);
  for (const store of opened) store.close();
});

test("commitOutcome rolls back its event when the state revision is stale", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  store.appendEvent(event(session.id, session.workspaceId, "requested"));
  store.appendEvent(event(session.id, session.workspaceId, "started"));
  const completed = event(session.id, session.workspaceId, "completed");
  expect(() => store.commitOutcome(completed, 1, [answer])).toThrow(
    "revision conflict",
  );
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  expect(() =>
    store.commitOutcome(
      event(session.id, session.workspaceId, "started"),
      0,
      [],
    ),
  ).toThrow("terminal");
  const saved = store.commitOutcome(completed, 0, [answer]);
  expect(saved.revision).toBe(1);
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(0);
  store.close();
});

test("a terminal event is written only with a history that answers its call", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  store.appendEvent(event(session.id, session.workspaceId, "requested"));
  store.appendEvent(event(session.id, session.workspaceId, "started"));
  const completed = event(session.id, session.workspaceId, "completed");
  expect(() => store.appendEvent(completed)).toThrow("written with its result");
  for (const messages of [
    [],
    [{ role: "tool" as const, content: "done", tool_name: "read" }],
    [{ ...answer, tool_call_id: "another-call" }],
  ])
    expect(() => store.commitOutcome(completed, 0, messages)).toThrow(
      "needs its call's result",
    );
  const change = {
    sessionId: session.id,
    workspaceId: session.workspaceId,
    expectedRevision: 0,
    events: [{ ...completed, type: "cancelled" as const }],
  };
  expect(() => store.recover(change)).toThrow("needs its call's result");
  expect(() => store.recover({ ...change, messages: [] })).toThrow(
    "needs its call's result",
  );
  // Nothing was written: the call is still a hazard at revision 0.
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  expect(store.recover({ ...change, messages: [answer] }).revision).toBe(1);
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(0);
  store.close();
});

test("uses an exclusive cross-process lease", async () => {
  const { db, workspace } = await fixture();
  const first = await SessionStore.open(db);
  const second = await SessionStore.open(db);
  const session = await first.createSession(workspace);
  const release = await first.acquireLease(session.id, workspace);
  const lockStat = await (await import("node:fs/promises")).stat(
    `${db}.session-${session.id}.lock`,
  );
  expect(lockStat.mode & 0o777).toBe(0o600);
  await expect(second.acquireLease(session.id, workspace)).rejects.toThrow(
    "busy",
  );
  await release();
  const again = await second.acquireLease(session.id, workspace);
  await again();
  first.close();
  second.close();
});

test("rejects non-regular lock files without blocking", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  const lockPath = `${db}.session-${session.id}.lock`;
  const fifo = Bun.spawn(["mkfifo", lockPath]);
  expect(await fifo.exited).toBe(0);
  await expect(store.acquireLease(session.id, workspace)).rejects.toThrow(
    "regular",
  );
  await (await import("node:fs/promises")).unlink(lockPath);
  store.close();
});

test("raw SQL cannot rewrite or delete lifecycle rows and lock symlinks are refused", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  store.appendEvent(event(session.id, session.workspaceId, "requested"));
  const raw = (
    store as unknown as {
      db: { run(sql: string, ...args: unknown[]): unknown };
    }
  ).db;
  expect(() => raw.run("UPDATE execution_events SET detail = 'x'")).toThrow();
  expect(() => raw.run("DELETE FROM execution_events")).toThrow();
  store.close();
  const outside = join(tmpdir(), `session-lock-target-${Date.now()}`);
  await writeFile(outside, "x");
  await symlink(outside, `${db}.session-${session.id}.lock`);
  const reopened = await SessionStore.open(db);
  await expect(reopened.acquireLease(session.id, workspace)).rejects.toThrow(
    "symlink",
  );
  reopened.close();
});

test("a crashed child releases the session lease", async () => {
  const { db, workspace } = await fixture();
  const parent = await SessionStore.open(db);
  const session = await parent.createSession(workspace);
  const modulePath = fileURLToPath(new URL("./session.ts", import.meta.url));
  const script = `
    const { SessionStore } = await import(${JSON.stringify(modulePath)});
    const store = await SessionStore.open(${JSON.stringify(db)});
    await store.acquireLease(${session.id}, ${JSON.stringify(workspace)});
    console.log("locked");
    await Bun.sleep(10000);
  `;
  const child = Bun.spawn([process.execPath, "-e", script], {
    stdout: "pipe",
    stderr: "pipe",
  });
  const reader = child.stdout.getReader();
  let output = "";
  for (let attempt = 0; attempt < 20 && !output.includes("locked"); attempt++) {
    const chunk = await reader.read();
    if (chunk.done) break;
    output += new TextDecoder().decode(chunk.value);
  }
  expect(output).toContain("locked");
  await expect(parent.acquireLease(session.id, workspace)).rejects.toThrow(
    "busy",
  );
  child.kill();
  await child.exited;
  const release = await parent.acquireLease(session.id, workspace);
  await release();
  parent.close();
});

test("does not mutate an unknown database file", async () => {
  const { db } = await fixture();
  const seed = new Database(db);
  seed.exec(
    "CREATE TABLE sessions (id INTEGER PRIMARY KEY, workspace_id TEXT)",
  );
  const schemaBefore = seed
    .query("SELECT type, name, sql FROM sqlite_master ORDER BY type, name")
    .all();
  const journalBefore = seed.query("PRAGMA journal_mode").get();
  seed.close();
  const before = await (await import("node:fs/promises")).readFile(db);
  const beforeHash = createHash("sha256").update(before).digest("hex");
  await expect(SessionStore.open(db)).rejects.toThrow();
  const after = await (await import("node:fs/promises")).readFile(db);
  const afterHash = createHash("sha256").update(after).digest("hex");
  expect(after).toEqual(before);
  expect(afterHash).toBe(beforeHash);
  const check = new Database(db);
  expect(
    check
      .query("SELECT type, name, sql FROM sqlite_master ORDER BY type, name")
      .all(),
  ).toEqual(schemaBefore);
  expect(check.query("PRAGMA journal_mode").get()).toEqual(journalBefore);
  check.close();
});

test("rejects a future schema before WAL or chmod mutation", async () => {
  const { db } = await fixture();
  const seed = new Database(db);
  seed.exec(
    "CREATE TABLE schema_meta (version INTEGER NOT NULL); INSERT INTO schema_meta VALUES (999)",
  );
  const beforeSchema = seed
    .query("SELECT type, name, sql FROM sqlite_master ORDER BY type, name")
    .all();
  const beforeJournal = seed.query("PRAGMA journal_mode").get();
  seed.close();
  const before = await (await import("node:fs/promises")).readFile(db);
  const beforeHash = createHash("sha256").update(before).digest("hex");
  await expect(SessionStore.open(db)).rejects.toThrow("999");
  const after = await (await import("node:fs/promises")).readFile(db);
  expect(createHash("sha256").update(after).digest("hex")).toBe(beforeHash);
  const check = new Database(db);
  expect(
    check
      .query("SELECT type, name, sql FROM sqlite_master ORDER BY type, name")
      .all(),
  ).toEqual(beforeSchema);
  expect(check.query("PRAGMA journal_mode").get()).toEqual(beforeJournal);
  check.close();
});

test("the -wal and -shm files are private too, whatever the umask", async () => {
  const { db, workspace } = await fixture();
  const previous = process.umask(0o022);
  try {
    const store = await SessionStore.open(db);
    // Written but not closed: the sidecars exist while the store is open.
    await store.createSession(workspace, [{ role: "user", content: "hi" }]);
    for (const suffix of ["", "-wal", "-shm"])
      expect((await stat(`${db}${suffix}`)).mode & 0o777, suffix).toBe(0o600);
    store.close();
  } finally {
    process.umask(previous);
  }
});

test("a write waits for another process's lock instead of failing at once", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const created = await store.createSession(workspace);
  // The lock is held by another process: a timer in this one could not run
  // while the synchronous write blocks.
  const holder = spawn(
    process.execPath,
    [
      "-e",
      `const { Database } = require("bun:sqlite");
const db = new Database(process.env.DB);
db.exec("BEGIN IMMEDIATE");
console.log("locked");
setTimeout(() => { db.exec("COMMIT"); db.close(); }, 600);`,
    ],
    { env: { ...process.env, DB: db }, stdio: ["ignore", "pipe", "inherit"] },
  );
  const done = new Promise<void>((resolve) =>
    holder.once("exit", () => resolve()),
  );
  await new Promise<void>((resolve, reject) => {
    holder.stdout.once("data", () => resolve());
    holder.once("error", reject);
  });
  const started = performance.now();
  const saved = store.saveStateCAS(created.id, created.workspaceId, 0, [
    { role: "user", content: "after the lock" },
  ]);
  expect(saved.revision).toBe(1);
  expect(performance.now() - started).toBeGreaterThan(200);
  await done;
  store.close();
});

test("session summaries match the listing in order and count, without reading transcripts", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const first = await store.createSession(workspace, [
    { role: "user", content: "one" },
    { role: "assistant", content: "two" },
  ]);
  const second = await store.createSession(workspace);
  // Saved later, so it lists first despite being created first.
  await new Promise((resolve) => setTimeout(resolve, 10));
  store.saveStateCAS(first.id, first.workspaceId, 0, [
    { role: "user", content: "one" },
    { role: "assistant", content: "two" },
    { role: "user", content: "three" },
  ]);
  const listed = await store.listSessions(workspace);
  const summaries = await store.listSessionSummaries(workspace);
  expect(summaries.map((s) => s.publicId)).toEqual(
    listed.map((s) => s.publicId),
  );
  expect(summaries.map((s) => s.publicId)).toEqual([
    first.publicId,
    second.publicId,
  ]);
  expect(summaries).toEqual(
    listed.map((s) => ({
      id: s.id,
      publicId: s.publicId,
      workspaceId: s.workspaceId,
      revision: s.revision,
      messageCount: s.messages.length,
    })),
  );
  // A transcript that no longer validates still lists: nothing decodes it.
  const raw = new Database(db);
  raw.exec(
    `UPDATE sessions SET state_json = '[{"role":"bogus"}]' WHERE id = ${second.id}`,
  );
  raw.close();
  await expect(store.listSessions(workspace)).rejects.toThrow();
  expect(
    Object.fromEntries(
      (await store.listSessionSummaries(workspace)).map((s) => [
        s.publicId,
        s.messageCount,
      ]),
    ),
  ).toEqual({ [first.publicId]: 3, [second.publicId]: 1 });
  store.close();
});
