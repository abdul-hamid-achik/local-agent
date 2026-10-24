import { Database, constants as sqliteFlags } from "bun:sqlite";
import { createHash, randomBytes } from "node:crypto";
import { constants } from "node:fs";
import { chmod, lstat, mkdir, open, realpath } from "node:fs/promises";
import { dirname } from "node:path";
import { z } from "zod";
import { type Message, MessageSchema } from "#runtime/index";
import { tryLockFile } from "#runtime/workspace_native";

const SCHEMA_VERSION = 3;
const MAX_STATE_BYTES = 8 * 1024 * 1024;
/** How long a write waits for another process's lock before SQLITE_BUSY. */
const BUSY_TIMEOUT_MS = 5000;
const EVENT_TYPES = [
  "requested",
  "approval_requested",
  "approved",
  "denied",
  "started",
  "completed",
  "failed",
  "cancelled",
  "outcome_unknown",
] as const;
type EventType = (typeof EVENT_TYPES)[number];
type EffectClass = "read_only" | "effectful" | "unknown";
type ExecutionKind = "builtin" | "mcp";

export type SessionMessage = Message;
export type Session = Readonly<{
  id: number;
  publicId: string;
  workspaceId: string;
  messages: SessionMessage[];
  revision: number;
}>;
/** A session without its transcript, for listings. */
export type SessionSummary = Readonly<{
  id: number;
  publicId: string;
  workspaceId: string;
  revision: number;
  messageCount: number;
}>;
export type ExecutionEvent = Readonly<{
  sessionId: number;
  workspaceId: string;
  runId: string;
  turnId: string;
  executionId: string;
  idempotencyKey: string;
  canonicalCallId: string;
  toolName: string;
  effectClass: EffectClass;
  providerCallId?: string;
  iteration?: number;
  ordinal?: number;
  kind?: ExecutionKind;
  type: EventType;
  approval?: string;
  argumentsSha256: string;
  resultSha256?: string;
  resultReceipt?: string;
  detail?: string;
}>;

/**
 * The user's conclusion about an execution whose outcome is unknown, beside
 * the ledger rather than in it (ADR-0006): one immutable row per execution,
 * bound to the latest event it concluded about.
 */
const RECONCILIATIONS_DDL = `
  CREATE TABLE execution_reconciliations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL,
    execution_id TEXT NOT NULL UNIQUE,
    latest_event_id INTEGER NOT NULL,
    latest_event_type TEXT NOT NULL CHECK (latest_event_type IN ('started','outcome_unknown')),
    disposition TEXT NOT NULL CHECK (disposition IN ('effect_applied','effect_not_applied','effect_compensated')),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
    actor TEXT NOT NULL,
    evidence_json TEXT NOT NULL CHECK (length(evidence_json) <= 16384),
    evidence_sha256 TEXT NOT NULL CHECK (length(evidence_sha256) = 64),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
  );
  CREATE TRIGGER execution_reconciliations_scope_guard BEFORE INSERT ON execution_reconciliations WHEN NOT EXISTS (SELECT 1 FROM execution_events WHERE id = NEW.latest_event_id AND session_id = NEW.session_id AND workspace_id = NEW.workspace_id AND execution_id = NEW.execution_id AND event_type = NEW.latest_event_type) BEGIN SELECT RAISE(ABORT, 'reconciliation does not match its execution'); END;
  CREATE TRIGGER execution_reconciliations_no_update BEFORE UPDATE ON execution_reconciliations BEGIN SELECT RAISE(ABORT, 'reconciliations are append-only'); END;
  CREATE TRIGGER execution_reconciliations_no_delete BEFORE DELETE ON execution_reconciliations BEGIN SELECT RAISE(ABORT, 'reconciliations are append-only'); END;
`;
/** The hazards' latest events, less those a reconciliation concluded. */
const EFFECTIVE_HAZARDS =
  "SELECT e.* FROM execution_events e JOIN (SELECT execution_id, MAX(id) id FROM execution_events WHERE session_id = ? AND workspace_id = ? GROUP BY execution_id) latest ON latest.id = e.id WHERE (e.event_type = 'outcome_unknown' OR (e.event_type = 'started' AND e.effect_class != 'read_only')) AND NOT EXISTS (SELECT 1 FROM execution_reconciliations r WHERE r.execution_id = e.execution_id AND r.latest_event_id = e.id) ORDER BY e.id";

export type Disposition =
  | "effect_applied"
  | "effect_not_applied"
  | "effect_compensated";
export type Reconciliation = Readonly<{
  sessionId: number;
  workspaceId: string;
  executionId: string;
  latestEventId: number;
  latestEventType: "started" | "outcome_unknown";
  disposition: Disposition;
  note: string;
  actor: string;
  evidenceJson: string;
  evidenceSha256: string;
}>;

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz";

/**
 * A conversation that cannot grow: more messages than one request carries,
 * or saved state past 8 MiB. The message is a whole sentence; a client
 * adds how it starts a new session.
 */
export class ConversationFullError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConversationFullError";
  }
}

export class SessionStore {
  private constructor(
    private readonly db: Database,
    readonly path: string,
  ) {}

  static async open(path: string): Promise<SessionStore> {
    await mkdir(dirname(path), { recursive: true, mode: 0o700 });
    const parent = await realpath(dirname(path));
    const candidate = `${parent}/${path.split("/").at(-1)}`;
    let existingSize = 0;
    try {
      const existing = await lstat(candidate);
      if (existing.isSymbolicLink())
        throw new Error("session database symlink is not allowed");
      if (!existing.isFile())
        throw new Error("Session database must be a regular file");
      existingSize = existing.size;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      // Created private (and never through a link): SQLite would create it
      // with the umask's mode, open until the chmod below.
      await (await open(candidate, "wx", 0o600)).close();
    }
    const db = new Database(
      candidate,
      sqliteFlags.SQLITE_OPEN_READWRITE |
        sqliteFlags.SQLITE_OPEN_CREATE |
        sqliteFlags.SQLITE_OPEN_NOFOLLOW,
    );
    // Wait for another process's write lock rather than fail on the spot.
    db.exec(`PRAGMA busy_timeout = ${BUSY_TIMEOUT_MS}`);
    if (existingSize > 0) {
      try {
        const meta = db
          .query<{ version: number }, []>(
            "SELECT version FROM schema_meta LIMIT 1",
          )
          .get();
        if (!meta) throw new Error("unsupported session database format");
        if (![1, 2, SCHEMA_VERSION].includes(meta.version)) {
          throw new Error(`unsupported session schema version ${meta.version}`);
        }
      } catch (error) {
        db.close();
        throw error;
      }
    }
    try {
      // Before WAL mode creates them: SQLite gives the -wal and -shm files the
      // mode of the main file, which would otherwise follow the umask.
      await chmod(candidate, 0o600);
      db.exec(
        "PRAGMA journal_mode = WAL; PRAGMA synchronous = FULL; PRAGMA foreign_keys = ON;",
      );
      // Sidecars left behind by an older version may have looser modes.
      for (const suffix of ["-wal", "-shm"]) {
        try {
          await chmod(`${candidate}${suffix}`, 0o600);
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
        }
      }
      db.exec(
        "CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL)",
      );
      const row = db
        .query<{ version: number }, []>(
          "SELECT version FROM schema_meta LIMIT 1",
        )
        .get();
      if (row?.version === 1 || row?.version === 2) {
        db.transaction(() => {
          const locked = db
            .query<{ version: number }, []>(
              "SELECT version FROM schema_meta LIMIT 1",
            )
            .get();
          if (!locked) throw new Error("unsupported session database format");
          if (locked.version === 1)
            db.exec(`
              ALTER TABLE execution_events ADD COLUMN provider_call_id TEXT;
              ALTER TABLE execution_events ADD COLUMN iteration INTEGER;
              ALTER TABLE execution_events ADD COLUMN ordinal INTEGER;
              ALTER TABLE execution_events ADD COLUMN kind TEXT;
              CREATE UNIQUE INDEX execution_events_logical_slot ON execution_events(session_id, workspace_id, run_id, turn_id, iteration, ordinal) WHERE event_type = 'requested' AND iteration IS NOT NULL AND ordinal IS NOT NULL;
            `);
          if (locked.version === 1 || locked.version === 2) {
            db.exec(
              `${RECONCILIATIONS_DDL} UPDATE schema_meta SET version = ${SCHEMA_VERSION};`,
            );
          } else if (locked.version !== SCHEMA_VERSION) {
            throw new Error(
              `unsupported session schema version ${locked.version}`,
            );
          }
        }).immediate();
      }
      const currentVersion = db
        .query<{ version: number }, []>(
          "SELECT version FROM schema_meta LIMIT 1",
        )
        .get();
      if (currentVersion && currentVersion.version !== SCHEMA_VERSION) {
        throw new Error(
          `unsupported session schema version ${currentVersion.version}`,
        );
      }
      if (!row) {
        db.transaction(() => {
          const initialized = db
            .query<{ version: number }, []>(
              "SELECT version FROM schema_meta LIMIT 1",
            )
            .get();
          if (initialized) {
            if (initialized.version !== SCHEMA_VERSION)
              throw new Error(
                `unsupported session schema version ${initialized.version}`,
              );
            return;
          }
          db.exec(`
        CREATE TABLE sessions (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          public_id TEXT NOT NULL UNIQUE,
          workspace_id TEXT NOT NULL,
          state_json TEXT NOT NULL DEFAULT '[]',
          revision INTEGER NOT NULL DEFAULT 0,
          created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
          updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
        );
        CREATE INDEX sessions_workspace ON sessions(workspace_id, updated_at DESC);
        CREATE TABLE execution_events (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
          workspace_id TEXT NOT NULL,
          run_id TEXT NOT NULL,
          turn_id TEXT NOT NULL,
          execution_id TEXT NOT NULL,
          idempotency_key TEXT NOT NULL,
          provider_call_id TEXT,
          canonical_call_id TEXT NOT NULL,
          iteration INTEGER,
          ordinal INTEGER,
          kind TEXT,
          tool_name TEXT NOT NULL,
          effect_class TEXT NOT NULL,
          event_type TEXT NOT NULL,
          approval TEXT NOT NULL DEFAULT '',
          arguments_sha256 TEXT NOT NULL,
          result_sha256 TEXT NOT NULL DEFAULT '',
          result_receipt TEXT NOT NULL DEFAULT '',
          detail TEXT NOT NULL DEFAULT '',
          created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
          UNIQUE(execution_id, event_type),
          UNIQUE(idempotency_key, event_type)
        );
        CREATE UNIQUE INDEX execution_events_one_terminal ON execution_events(execution_id) WHERE event_type IN ('denied','completed','failed','cancelled','outcome_unknown');
        CREATE UNIQUE INDEX execution_events_logical_slot ON execution_events(session_id, workspace_id, run_id, turn_id, iteration, ordinal) WHERE event_type = 'requested' AND iteration IS NOT NULL AND ordinal IS NOT NULL;
        CREATE TRIGGER execution_events_workspace_guard BEFORE INSERT ON execution_events WHEN NOT EXISTS (SELECT 1 FROM sessions WHERE id = NEW.session_id AND workspace_id = NEW.workspace_id) BEGIN SELECT RAISE(ABORT, 'event workspace does not match session'); END;
        CREATE TRIGGER execution_events_no_update BEFORE UPDATE ON execution_events BEGIN SELECT RAISE(ABORT, 'execution events are append-only'); END;
        CREATE TRIGGER execution_events_no_delete BEFORE DELETE ON execution_events BEGIN SELECT RAISE(ABORT, 'execution events are append-only'); END;
        CREATE INDEX execution_events_session ON execution_events(session_id, workspace_id, id);
        ${RECONCILIATIONS_DDL}
        INSERT INTO schema_meta(version) VALUES (${SCHEMA_VERSION});
          `);
        }).immediate();
      }
      const initializedVersion = db
        .query<{ version: number }, []>(
          "SELECT version FROM schema_meta LIMIT 1",
        )
        .get();
      if (!initializedVersion || initializedVersion.version !== SCHEMA_VERSION)
        throw new Error("session schema initialization failed");
      return new SessionStore(db, candidate);
    } catch (error) {
      db.close();
      throw error;
    }
  }

  close(): void {
    this.db.close();
  }

  async createSession(
    workspace: string,
    messages: SessionMessage[] = [],
  ): Promise<Session> {
    const workspaceId = await canonicalWorkspace(workspace);
    validateMessages(messages);
    const publicId = newPublicId();
    const result = this.db
      .query(
        "INSERT INTO sessions(public_id, workspace_id, state_json) VALUES (?, ?, ?)",
      )
      .run(publicId, workspaceId, encodeMessages(messages));
    return {
      id: Number(result.lastInsertRowid),
      publicId,
      workspaceId,
      messages: clone(messages),
      revision: 0,
    };
  }

  async listSessions(workspace: string): Promise<Session[]> {
    const workspaceId = await canonicalWorkspace(workspace);
    const rows = this.db
      .query<SessionRow, [string]>(
        "SELECT * FROM sessions WHERE workspace_id = ? ORDER BY updated_at DESC, id DESC",
      )
      .all(workspaceId);
    return rows.map(readSessionRow);
  }

  /**
   * The sessions of a workspace, newest first as `listSessions` has them, with
   * a message count in place of the transcript: SQLite counts the messages, so
   * no transcript is read into JavaScript, parsed or validated.
   */
  async listSessionSummaries(workspace: string): Promise<SessionSummary[]> {
    const workspaceId = await canonicalWorkspace(workspace);
    return this.db
      .query<
        { id: number; public_id: string; revision: number; count: number },
        [string]
      >(
        "SELECT id, public_id, revision, json_array_length(state_json) AS count FROM sessions WHERE workspace_id = ? ORDER BY updated_at DESC, id DESC",
      )
      .all(workspaceId)
      .map((row) => ({
        id: row.id,
        publicId: row.public_id,
        workspaceId,
        revision: row.revision,
        messageCount: row.count,
      }));
  }

  async loadSession(publicId: string, workspace: string): Promise<Session> {
    const workspaceId = await canonicalWorkspace(workspace);
    const row = this.db
      .query<SessionRow, [string, string]>(
        "SELECT * FROM sessions WHERE public_id = ? AND workspace_id = ?",
      )
      .get(publicId, workspaceId);
    if (!row) throw new Error("session not found");
    return readSessionRow(row);
  }

  saveStateCAS(
    sessionId: number,
    workspaceId: string,
    expectedRevision: number,
    messages: SessionMessage[],
  ): Session {
    const encoded = encodeMessages(messages);
    return this.db
      .transaction(() =>
        this.saveStateUnsafe(sessionId, workspaceId, expectedRevision, encoded),
      )
      .immediate();
  }

  commitOutcome(
    event: ExecutionEvent,
    expectedRevision: number,
    messages: SessionMessage[],
  ): Session {
    if (!isTerminal(event.type))
      throw new Error("commitOutcome requires a terminal event");
    const encoded = encodeMessages(messages);
    return this.db
      .transaction(() => {
        this.appendEventUnsafe(event);
        return this.saveStateUnsafe(
          event.sessionId,
          event.workspaceId,
          expectedRevision,
          encoded,
        );
      })
      .immediate();
  }

  private saveStateUnsafe(
    sessionId: number,
    workspaceId: string,
    expectedRevision: number,
    encoded: string,
  ): Session {
    const result = this.db
      .query(
        "UPDATE sessions SET state_json = ?, revision = revision + 1, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND workspace_id = ? AND revision = ?",
      )
      .run(encoded, sessionId, workspaceId, expectedRevision);
    if (result.changes !== 1)
      throw new Error("session state revision conflict");
    const row = this.db
      .query<SessionRow, [number]>("SELECT * FROM sessions WHERE id = ?")
      .get(sessionId);
    if (!row) throw new Error("session not found");
    return readSessionRow(row);
  }

  appendEvent(event: ExecutionEvent): void {
    this.db.transaction(() => this.appendEventUnsafe(event)).immediate();
  }

  private appendEventUnsafe(event: ExecutionEvent): void {
    validateEvent(event);
    const session = this.db
      .query<{ workspace_id: string }, [number]>(
        "SELECT workspace_id FROM sessions WHERE id = ?",
      )
      .get(event.sessionId);
    if (!session) throw new Error("session not found");
    if (session.workspace_id !== event.workspaceId)
      throw new Error("event workspace does not match session");
    const existing = this.db
      .query<EventRow, [string, string]>(
        "SELECT * FROM execution_events WHERE execution_id = ? OR idempotency_key = ? ORDER BY id",
      )
      .all(event.executionId, event.idempotencyKey);
    if (existing.some((row) => !identityMatches(row, event)))
      throw new Error("execution identity conflict");
    if (existing.some((row) => row.event_type === event.type)) {
      const row = existing.find(
        (candidate) => candidate.event_type === event.type,
      );
      if (row && sameEvent(row, event)) return;
      throw new Error("conflicting immutable execution event");
    }
    if (
      event.type === "requested" &&
      event.iteration !== undefined &&
      event.ordinal !== undefined
    ) {
      const canonical = this.db
        .query<
          { execution_id: string; ordinal: number | null },
          [number, string, string, string]
        >(
          "SELECT execution_id, ordinal FROM execution_events WHERE session_id = ? AND run_id = ? AND canonical_call_id = ? AND event_type = 'requested' AND workspace_id = ? LIMIT 1",
        )
        .get(
          event.sessionId,
          event.runId,
          event.canonicalCallId,
          event.workspaceId,
        );
      if (canonical && canonical.execution_id !== event.executionId)
        throw new Error("execution canonical call collision");
      const slot = this.db
        .query<
          { execution_id: string; canonical_call_id: string },
          [number, string, string, string, number, number]
        >(
          "SELECT execution_id, canonical_call_id FROM execution_events WHERE session_id = ? AND workspace_id = ? AND run_id = ? AND turn_id = ? AND iteration = ? AND ordinal = ? AND event_type = 'requested' LIMIT 1",
        )
        .get(
          event.sessionId,
          event.workspaceId,
          event.runId,
          event.turnId,
          event.iteration,
          event.ordinal,
        );
      if (
        slot &&
        (slot.execution_id !== event.executionId ||
          slot.canonical_call_id !== event.canonicalCallId)
      )
        throw new Error("execution logical slot conflict");
    }
    const previous = existing.at(-1)?.event_type as EventType | undefined;
    if (!legalTransition(previous, event.type))
      throw new Error("illegal execution event transition");
    this.db
      .query(
        "INSERT INTO execution_events(session_id, workspace_id, run_id, turn_id, execution_id, idempotency_key, provider_call_id, canonical_call_id, iteration, ordinal, kind, tool_name, effect_class, event_type, approval, arguments_sha256, result_sha256, result_receipt, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
      )
      .run(
        event.sessionId,
        event.workspaceId,
        event.runId,
        event.turnId,
        event.executionId,
        event.idempotencyKey,
        event.providerCallId ?? null,
        event.canonicalCallId,
        event.iteration ?? null,
        event.ordinal ?? null,
        event.kind ?? null,
        event.toolName,
        event.effectClass,
        event.type,
        event.approval ?? "",
        event.argumentsSha256,
        event.resultSha256 ?? "",
        event.resultReceipt ?? "",
        event.detail ?? "",
      );
  }

  /**
   * Executions that block continuation: their latest event is
   * `outcome_unknown`, or `started` and not read-only, and no
   * reconciliation concluded about that event.
   */
  listHazards(sessionId: number, workspaceId: string): EventRow[] {
    return this.db
      .query<EventRow, [number, string]>(EFFECTIVE_HAZARDS)
      .all(sessionId, workspaceId);
  }

  /** The latest event of each execution that has no terminal event yet. */
  listOpenExecutions(sessionId: number, workspaceId: string): EventRow[] {
    return this.db
      .query<EventRow, [number, string]>(
        "SELECT e.* FROM execution_events e JOIN (SELECT execution_id, MAX(id) id FROM execution_events WHERE session_id = ? AND workspace_id = ? GROUP BY execution_id) latest ON latest.id = e.id WHERE e.event_type IN ('requested','approval_requested','approved','started') ORDER BY e.id",
      )
      .all(sessionId, workspaceId);
  }

  /** The reconciliation of an execution in this session, if any. */
  reconciliationOf(
    sessionId: number,
    executionId: string,
  ): ReconciliationRow | undefined {
    return (
      this.db
        .query<ReconciliationRow, [number, string]>(
          "SELECT * FROM execution_reconciliations WHERE session_id = ? AND execution_id = ?",
        )
        .get(sessionId, executionId) ?? undefined
    );
  }

  /**
   * Closes interrupted calls (ADR-0006): appends their terminal events,
   * records a reconciliation when given, and saves the messages, in one
   * transaction under the revision check. A reconciliation must still
   * conclude about the execution's latest event, and that event must still
   * be a hazard.
   */
  recover(change: {
    sessionId: number;
    workspaceId: string;
    expectedRevision: number;
    events: ExecutionEvent[];
    messages?: SessionMessage[];
    reconciliation?: Reconciliation;
  }): Session {
    for (const event of change.events)
      if (!isTerminal(event.type))
        throw new Error("recovery appends only terminal events");
    const encoded =
      change.messages === undefined
        ? undefined
        : encodeMessages(change.messages);
    return this.db
      .transaction(() => {
        const settle = change.reconciliation;
        if (settle) {
          if (
            settle.sessionId !== change.sessionId ||
            settle.workspaceId !== change.workspaceId
          )
            throw new Error("reconciliation belongs to another session");
          const hazard = this.db
            .query<EventRow, [number, string]>(EFFECTIVE_HAZARDS)
            .all(change.sessionId, change.workspaceId)
            .find((row) => row.execution_id === settle.executionId);
          if (
            !hazard ||
            hazard.id !== settle.latestEventId ||
            hazard.event_type !== settle.latestEventType
          )
            throw new Error(
              "that execution is not an open hazard at this event any more",
            );
          this.db
            .query(
              "INSERT INTO execution_reconciliations(session_id, workspace_id, execution_id, latest_event_id, latest_event_type, disposition, note, actor, evidence_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            )
            .run(
              settle.sessionId,
              settle.workspaceId,
              settle.executionId,
              settle.latestEventId,
              settle.latestEventType,
              settle.disposition,
              settle.note,
              settle.actor,
              settle.evidenceJson,
              settle.evidenceSha256,
            );
        }
        for (const event of change.events) {
          if (
            event.sessionId !== change.sessionId ||
            event.workspaceId !== change.workspaceId
          )
            throw new Error("event belongs to another session");
          this.appendEventUnsafe(event);
        }
        if (encoded !== undefined)
          return this.saveStateUnsafe(
            change.sessionId,
            change.workspaceId,
            change.expectedRevision,
            encoded,
          );
        const row = this.db
          .query<SessionRow, [number, string, number]>(
            "SELECT * FROM sessions WHERE id = ? AND workspace_id = ? AND revision = ?",
          )
          .get(change.sessionId, change.workspaceId, change.expectedRevision);
        if (!row) throw new Error("session state revision conflict");
        return readSessionRow(row);
      })
      .immediate();
  }

  async acquireLease(
    sessionId: number,
    workspace: string,
  ): Promise<() => Promise<void>> {
    const workspaceId = await canonicalWorkspace(workspace);
    const row = this.db
      .query<{ id: number }, [number, string]>(
        "SELECT id FROM sessions WHERE id = ? AND workspace_id = ?",
      )
      .get(sessionId, workspaceId);
    if (!row) throw new Error("session not found");
    const lockPath = `${this.path}.session-${sessionId}.lock`;
    try {
      if ((await lstat(lockPath)).isSymbolicLink())
        throw new Error("session lock symlink is not allowed");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    const file = await open(
      lockPath,
      constants.O_CREAT |
        constants.O_RDWR |
        constants.O_NOFOLLOW |
        constants.O_NONBLOCK,
      0o600,
    );
    try {
      const stat = await file.stat();
      if (!stat.isFile()) throw new Error("session lock must be regular");
      await file.chmod(0o600);
      if (!tryLockFile(file.fd)) throw new Error("session is busy");
    } catch (error) {
      await file.close();
      throw error;
    }
    let closed = false;
    return async () => {
      if (closed) return;
      closed = true;
      await file.close();
    };
  }
}

type SessionRow = {
  id: number;
  public_id: string;
  workspace_id: string;
  state_json: string;
  revision: number;
};
export type EventRow = {
  id: number;
  session_id: number;
  workspace_id: string;
  execution_id: string;
  event_type: EventType;
  effect_class: EffectClass;
  arguments_sha256: string;
  run_id: string;
  turn_id: string;
  idempotency_key: string;
  provider_call_id: string | null;
  canonical_call_id: string;
  iteration: number | null;
  ordinal: number | null;
  kind: ExecutionKind | null;
  tool_name: string;
  approval: string;
  result_sha256: string;
  result_receipt: string;
  detail: string;
  created_at: string;
};

export type ReconciliationRow = {
  id: number;
  session_id: number;
  workspace_id: string;
  execution_id: string;
  latest_event_id: number;
  latest_event_type: "started" | "outcome_unknown";
  disposition: Disposition;
  note: string;
  actor: string;
  evidence_json: string;
  evidence_sha256: string;
  created_at: string;
};

function readSessionRow(row: SessionRow): Session {
  const messages = decodeMessages(row.state_json);
  return {
    id: row.id,
    publicId: row.public_id,
    workspaceId: row.workspace_id,
    messages: clone(messages),
    revision: row.revision,
  };
}
function clone(messages: SessionMessage[]): SessionMessage[] {
  return decodeMessages(encodeMessages(messages));
}
async function canonicalWorkspace(workspace: string): Promise<string> {
  return realpath(workspace);
}
function validateMessages(messages: SessionMessage[]): void {
  encodeMessages(messages);
}
function encodeMessages(messages: SessionMessage[]): string {
  const parsed = MessageSchema.array().parse(messages);
  const encoded = JSON.stringify(parsed);
  if (Buffer.byteLength(encoded, "utf8") > MAX_STATE_BYTES)
    throw new ConversationFullError(
      "This session is full: its saved state would pass 8 MiB.",
    );
  return encoded;
}
function decodeMessages(encoded: string): SessionMessage[] {
  if (Buffer.byteLength(encoded, "utf8") > MAX_STATE_BYTES)
    throw new Error("session state exceeds 8 MiB");
  return MessageSchema.array().parse(JSON.parse(encoded));
}
function newPublicId(): string {
  const bytes = randomBytes(7);
  return [...bytes].map((byte) => alphabet[byte % alphabet.length]).join("");
}
const EventSchema = z.strictObject({
  sessionId: z.number().int().positive(),
  workspaceId: z.string().min(1).max(4096),
  runId: z.string().min(1).max(128),
  turnId: z.string().min(1).max(128),
  executionId: z.string().min(1).max(128),
  idempotencyKey: z.string().min(1).max(128),
  providerCallId: z.string().max(1024).optional(),
  canonicalCallId: z.string().min(1).max(1024),
  toolName: z.string().min(1).max(512),
  effectClass: z.enum(["read_only", "effectful", "unknown"]),
  iteration: z.number().int().positive().optional(),
  ordinal: z.number().int().positive().optional(),
  kind: z.enum(["builtin", "mcp"]).optional(),
  type: z.enum(EVENT_TYPES),
  approval: z.string().max(64).optional(),
  argumentsSha256: z.string().regex(/^[0-9a-f]{64}$/),
  resultSha256: z
    .string()
    .regex(/^[0-9a-f]{64}$/)
    .optional(),
  resultReceipt: z.string().max(1_000_000).optional(),
  detail: z.string().max(4096).optional(),
});
function validateEvent(event: ExecutionEvent): void {
  EventSchema.parse(event);
  const metadata = [event.iteration, event.ordinal, event.kind].some(
    (value) => value !== undefined,
  );
  if (
    metadata &&
    (event.iteration === undefined ||
      event.ordinal === undefined ||
      event.kind === undefined)
  )
    throw new Error(
      "execution metadata must include iteration, ordinal, and kind",
    );
}

function sameEvent(row: EventRow, event: ExecutionEvent): boolean {
  return (
    row.execution_id === event.executionId &&
    row.workspace_id === event.workspaceId &&
    row.run_id === event.runId &&
    row.turn_id === event.turnId &&
    row.idempotency_key === event.idempotencyKey &&
    (row.provider_call_id ?? "") === (event.providerCallId ?? "") &&
    row.canonical_call_id === event.canonicalCallId &&
    row.iteration === (event.iteration ?? null) &&
    row.ordinal === (event.ordinal ?? null) &&
    row.kind === (event.kind ?? null) &&
    row.tool_name === event.toolName &&
    row.effect_class === event.effectClass &&
    row.event_type === event.type &&
    row.approval === (event.approval ?? "") &&
    row.arguments_sha256 === event.argumentsSha256 &&
    row.result_sha256 === (event.resultSha256 ?? "") &&
    row.result_receipt === (event.resultReceipt ?? "") &&
    row.detail === (event.detail ?? "")
  );
}
function identityMatches(row: EventRow, event: ExecutionEvent): boolean {
  return (
    row.session_id === event.sessionId &&
    row.execution_id === event.executionId &&
    row.workspace_id === event.workspaceId &&
    row.run_id === event.runId &&
    row.turn_id === event.turnId &&
    row.idempotency_key === event.idempotencyKey &&
    (row.provider_call_id ?? "") === (event.providerCallId ?? "") &&
    row.canonical_call_id === event.canonicalCallId &&
    row.iteration === (event.iteration ?? null) &&
    row.ordinal === (event.ordinal ?? null) &&
    row.kind === (event.kind ?? null) &&
    row.tool_name === event.toolName &&
    row.effect_class === event.effectClass &&
    row.arguments_sha256 === event.argumentsSha256
  );
}
function legalTransition(
  previous: EventType | undefined,
  next: EventType,
): boolean {
  if (!previous) return next === "requested";
  if (previous === "requested")
    return [
      "approval_requested",
      "started",
      "denied",
      "failed",
      "cancelled",
    ].includes(next);
  if (previous === "approval_requested")
    return ["approved", "denied", "failed", "cancelled"].includes(next);
  if (previous === "approved")
    return ["started", "failed", "cancelled"].includes(next);
  if (previous === "started")
    return ["completed", "failed", "cancelled", "outcome_unknown"].includes(
      next,
    );
  return false;
}

function isTerminal(type: EventType): boolean {
  return [
    "denied",
    "completed",
    "failed",
    "cancelled",
    "outcome_unknown",
  ].includes(type);
}

export function sha256(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}
