// A stand-in for the SDK's Host that tests and the fd 3 byte count use: it keeps
// a JSON store, applies batches the way the runtime does (`set`, and `append`
// to a string or an array, numeric path segments indexing arrays), and counts
// the bytes the SDK would write to fd 3 for each batch.

import type { Commit, Host, HostEvent } from "@thelacanians/tuimark";
import type { UiHost } from "#cli/tuimark/controller";

export interface WriteLike {
  path: string;
  value: unknown;
  op?: "set" | "append";
}

/** Applies one write to a JSON store; throws when an append finds the wrong type. */
export function applyWrite(store: Record<string, unknown>, w: WriteLike): void {
  // The runtime holds a copy parsed from JSON, never the caller's objects.
  const value = structuredClone(w.value);
  const keys = w.path.split(".");
  let node: unknown = store;
  for (const key of keys.slice(0, -1)) {
    const parent = node as Record<string, unknown>;
    parent[key] ??= {};
    node = parent[key];
  }
  const parent = node as Record<string, unknown>;
  const last = keys.at(-1) as string;
  if (w.op === "append") {
    const current = parent[last];
    if (typeof current === "string") {
      if (typeof value !== "string")
        throw new Error(`append ${w.path}: a string takes a string`);
      parent[last] = current + value;
    } else if (Array.isArray(current)) current.push(value);
    else throw new Error(`append ${w.path}: holds ${typeof current}`);
  } else parent[last] = value;
}

/** The bytes of the fd 3 line the SDK writes for a batch, newline included. */
export function batchBytes(writes: readonly WriteLike[], id: number): number {
  const line = JSON.stringify({
    type: "batch",
    writes: writes.map((w) => ({
      ...(w.op === undefined ? {} : { op: w.op }),
      path: w.path,
      value: w.value === undefined ? null : w.value,
    })),
    id,
  });
  return Buffer.byteLength(line) + 1;
}

/** The bytes of the fd 3 line the SDK writes for a commit, newline included. */
export function commitBytes(commit: Commit, id: number): number {
  const members: Record<string, unknown> = {};
  if (commit.lines !== undefined) members.lines = commit.lines;
  if (commit.template !== undefined) members.template = commit.template;
  if (commit.with !== undefined) members.with = commit.with;
  if (commit.props !== undefined) members.props = commit.props;
  return (
    Buffer.byteLength(JSON.stringify({ type: "commit", ...members, id })) + 1
  );
}

/** One recorded commit: what was asked, and what its `with` path held then. */
export interface RecordedCommit {
  commit: Commit;
  /** A clone of the `with` path's store value at the commit instant (paths are read once). */
  buf: unknown;
  /** False when the commit was refused: nothing was printed. */
  ok: boolean;
}

export class FakeHost implements UiHost {
  store: Record<string, unknown> = {};
  readonly handlers = new Map<string, (ev: HostEvent) => unknown>();
  readonly batches: WriteLike[][] = [];
  readonly commits: RecordedCommit[] = [];
  /** Bytes the batches (and commits) would take on fd 3. */
  bytes = 0;
  /** When set, the batch with this index (0-based) is refused. */
  refuse?: (writes: WriteLike[], index: number) => string | undefined;
  /** When set, the commit with this index (0-based) is refused. */
  refuseCommit?: (commit: Commit, index: number) => string | undefined;
  private inflight = 0;
  maxInflight = 0;
  private seq = 0;
  /** How long a batch takes to be acked, in ms. */
  ackMs = 1;

  on(action: string, handler: (ev: HostEvent) => unknown): Host {
    this.handlers.set(action, handler);
    return this as unknown as Host;
  }

  async batch(writes: WriteLike[]): Promise<{
    type: "ack";
    id: number | string;
    ok: boolean;
    error?: string;
  }> {
    this.maxInflight = Math.max(this.maxInflight, ++this.inflight);
    const index = this.batches.length;
    this.batches.push(writes);
    this.bytes += batchBytes(writes, index + 1);
    await new Promise((r) => setTimeout(r, this.ackMs));
    this.inflight--;
    const error = this.refuse?.(writes, index);
    if (error !== undefined)
      return { type: "ack", id: index + 1, ok: false, error };
    for (const w of writes) applyWrite(this.store, w);
    return { type: "ack", id: index + 1, ok: true };
  }

  async commit(commit: Commit): Promise<{
    type: "ack";
    id: number | string;
    ok: boolean;
    error?: string;
  }> {
    this.maxInflight = Math.max(this.maxInflight, ++this.inflight);
    const index = this.commits.length;
    // The runtime reads the `with` path once, as it stands at this instant.
    const path = commit.with?.split(/\s+as\s+/)[0]?.trim();
    const buf = path
      ? structuredClone(
          path
            .split(".")
            .reduce<unknown>(
              (o, k) => (o as Record<string, unknown> | undefined)?.[k],
              this.store,
            ),
        )
      : undefined;
    const record: RecordedCommit = {
      commit: structuredClone(commit),
      buf,
      ok: false,
    };
    this.commits.push(record);
    this.bytes += commitBytes(commit, index + 1);
    await new Promise((r) => setTimeout(r, this.ackMs));
    this.inflight--;
    const error = this.refuseCommit?.(commit, index);
    if (error !== undefined)
      return { type: "ack", id: index + 1, ok: false, error };
    record.ok = true;
    return { type: "ack", id: index + 1, ok: true };
  }

  async get(path: string) {
    const value = path
      .split(".")
      .reduce<unknown>(
        (o, k) => (o as Record<string, unknown> | undefined)?.[k],
        this.store,
      );
    return { found: value !== undefined, value } as Awaited<
      ReturnType<UiHost["get"]>
    >;
  }

  /** Fires an action as the document would, and waits for the handler like the host does. */
  async fire(action: string, value: unknown = null): Promise<unknown> {
    const handler = this.handlers.get(action);
    if (!handler) throw new Error(`no handler for ${action}`);
    return handler({ seq: ++this.seq, action, source: "", keys: {}, value });
  }

  statusLog(): unknown[] {
    return this.batches
      .flat()
      .filter((w) => w.path === "status")
      .map((w) => w.value);
  }
}
