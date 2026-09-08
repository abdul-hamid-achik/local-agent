import { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { mkdtemp, symlink, writeFile } from "node:fs/promises";
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

test("enforces append-only lifecycle and projects effect hazards", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  const requested = event(session.id, session.workspaceId, "requested");
  store.appendEvent(requested);
  store.appendEvent(event(session.id, session.workspaceId, "started"));
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  store.appendEvent(event(session.id, session.workspaceId, "outcome_unknown"));
  expect(store.listHazards(session.id, session.workspaceId)).toHaveLength(1);
  expect(() =>
    store.appendEvent(event(session.id, session.workspaceId, "completed")),
  ).toThrow("illegal");
  expect(() =>
    store.appendEvent({ ...requested, argumentsSha256: sha256("different") }),
  ).toThrow("identity conflict");
  store.close();
});

test("commitOutcome rolls back its event when the state revision is stale", async () => {
  const { db, workspace } = await fixture();
  const store = await SessionStore.open(db);
  const session = await store.createSession(workspace);
  store.appendEvent(event(session.id, session.workspaceId, "requested"));
  store.appendEvent(event(session.id, session.workspaceId, "started"));
  const completed = event(session.id, session.workspaceId, "completed");
  expect(() => store.commitOutcome(completed, 1, [])).toThrow(
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
  const saved = store.commitOutcome(completed, 0, [
    { role: "tool", content: "done", tool_name: "read" },
  ]);
  expect(saved.revision).toBe(1);
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
