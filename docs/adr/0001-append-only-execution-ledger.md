# ADR-0001: Append-only execution lifecycle ledger

## Status

Accepted — 2026-07-11

## Context

`session_state` is a lossless completed-turn projection, but it cannot prove
whether a tool was merely requested, approved, durably marked for dispatch, or
finished before a process stopped. Reconstructing that distinction from tool
cards is unsafe because denied and undispatched calls also produce UI receipts.

SQLite and an external tool backend cannot participate in one atomic commit.
There is always a crash window between recording dispatch intent and invoking
the backend, and another window between backend completion and its durable
receipt. Treating either window as proof that nothing happened could repeat a
risky effect.

## Decision Drivers

- Never automatically repeat an effect whose outcome may be unknown.
- Commit dispatch intent durably before invoking a backend.
- Preserve the existing version-1 session snapshot and restore behavior.
- Keep lifecycle types independent from the agent and database packages.
- Scope every execution to the exact persisted session and workspace.
- Avoid copying argument secrets into a second persistence surface.
- Remain useful on one local SQLite database without adding infrastructure.

## Considered Options

1. Continue using completed-turn snapshots and infer interrupted tools.
2. Mutate one current-state row per execution.
3. Append immutable lifecycle events and derive current state on read.
4. Use an external transactional workflow or message broker.

## Decision Outcome

Chosen option: **append immutable lifecycle events and derive current state on
read**, a small CQRS-like design inside the existing SQLite store.

`internal/execution` owns dependency-free lifecycle values, random identity
generation, canonical argument hashing, and payload bounds. The SQLite store
owns append validation and read projections. Agent and UI code consume those
interfaces rather than deriving authority from presentation messages.

The existing `session_state` row remains the completed-turn projection. No
event history is backfilled from old snapshots, and migration 004 does not
change snapshot schema or contents.

Completed snapshots carry an optional execution-event high-water mark in their
version-1 JSON payload. `LatestExecutionEventID` reads that cursor within the
same session/workspace scope; zero means that no event was present, and a
missing cursor in an older snapshot is treated as zero. Sampling and snapshot
storage are not atomic in the initial slice, so recovery may conservatively
surface an already-projected receipt, but it must never omit a newer effect.

### Durable identity

Each event repeats immutable execution identity:

- session and canonical workspace;
- random run, turn, execution, and idempotency identifiers;
- provider call ID and canonical call ID;
- iteration and ordinal within the run;
- tool name, backend kind, and effect classification.

The execution ID identifies ledger state. The separate idempotency key is the
stable key to pass to a backend when that backend supports idempotent effects.
Provider IDs are evidence, not trusted uniqueness boundaries.

### Lifecycle

The supported events are:

```text
requested
  ├─ approval_requested ─ approved ─ started ─ completed
  │                    └─ denied               ├─ failed (read-only)
  ├─ approved ──────────────┘                  ├─ cancelled (read-only)
  ├─ started (read-only) ──────────────────────└─ outcome_unknown (effectful/unknown)
  ├─ failed
  └─ cancelled
```

Policy, yolo, and explicit embedding opt-out decisions still receive a typed
`approved` event. A started effectful or unknown execution may not be recorded
as safely failed or cancelled; absent a completed receipt it is
`outcome_unknown`.

Pre-hooks may normalize arguments. The requested event hashes the provider's
original canonical arguments. The first later lifecycle event establishes the
effective canonical hash, which must remain stable through approval, start,
and termination.

### Durability boundary

The `started` commit is the dispatch-intent barrier. It must succeed before the
backend is invoked. SQLite is configured with WAL, a busy timeout, foreign
keys, and `synchronous=FULL`. Lifecycle append transactions acquire an
immediate writer lock so competing processes validate and append in one order.

`synchronous=FULL` reduces the chance that an acknowledged started commit is
lost during an operating-system or power failure. It does not make SQLite and
the external backend one transaction.

### Session ownership lease

A scalar snapshot cursor is safe only while one process owns execution for the
session. Without ownership, another process could append an effect between the
cursor read and snapshot write, or a recovery reader could mistake a live
started execution for an abandoned one.

`AcquireExecutionSessionLease` therefore validates the session/workspace scope
and takes a nonblocking exclusive operating-system file lock before recovery or
execution begins. The caller holds the lease through hazard inspection, the
agent run, cursor sampling, and snapshot persistence. A busy lease fails closed
with `ErrExecutionSessionBusy`; it is never treated as an orphaned execution.

The owner-only lease directory is a sibling of the canonical database path and
contains one reusable file keyed by session ID. Lease files are never unlinked:
closing the independent file descriptor releases the lock, and process exit or
crash releases it in the kernel. Closing the SQLite Store does not release an
outstanding session lease. Darwin and Linux use nonblocking `flock`; unsupported
platforms return an explicit error rather than running without ownership.

### Idempotency and exactly-once limits

An exact replay of the same execution and event type returns the existing row.
A replay with different identity, hashes, receipt, detail, or timestamp is a
conflict. Partial unique indexes allow only one approval decision and one
terminal receipt, including when two processes race.

This design does **not** promise exactly-once execution. Exactly-once effects
require cooperation from the backend using the durable idempotency key. For a
backend without that support, the ledger can prove only what was recorded
before and after dispatch.

### Recovery and retry

The initial Store API lists bounded continuation-blocking executions but does
not mutate them. Its operationally unresolved read model includes both
non-terminal lifecycles and the immutable terminal `outcome_unknown` state.
Hazardous started and outcome-unknown executions are prioritized within the
bound so a large number of older safe requests cannot hide them. Recovery and
execution must hold the scoped session lease; future reconciliation mutations
must use that same ownership boundary.

Snapshot recovery uses a second bounded read model. It always returns the
latest `outcome_unknown` and latest started non-read-only executions regardless
of cursor, because a snapshot cannot reconcile their external outcome. It also
returns completed non-read-only executions whose terminal event ID is newer
than the snapshot cursor, so a terminal receipt committed ahead of the snapshot
is not silently lost. Unknown/started hazards are prioritized before
post-cursor completions, and filtering occurs before the 100-row bound.

There is no automatic retry in this decision:

- pre-start unresolved work can be presented as not dispatched;
- interrupted read-only work may be presented as safe for an explicit retry;
- a started effectful or unknown operation is presented as outcome unknown;
- outcome-unknown work blocks continuation and requires inspection or an
  explicit reconciliation action before another call.

### Privacy and retention

Raw argument bodies are never accepted by the ledger API or stored in the
schema. Events retain a SHA-256 of canonical arguments, an optional result
hash, a result receipt bounded to 16 KiB, and detail bounded to 4 KiB.
Result-rewriting hooks run before that durable receipt is constructed, so a
secret removed from UI and model text is also absent from the ledger copy.

Events are retained for the lifetime of their session. Direct update and
delete are blocked by triggers. Explicit session deletion remains possible and
cascades to its events. Automatic event pruning is deferred; unresolved and
outcome-unknown evidence must never be silently pruned.

## Consequences

### Good

- Dispatch and terminal evidence survive completed-turn snapshot lag.
- Duplicate appends and cross-workspace identity mistakes fail closed.
- Current state is reproducible from immutable rows.
- Session snapshots remain compact and backward compatible.
- The API makes unknown outcomes explicit instead of inviting retries.
- A second process cannot execute or recover the same session concurrently.

### Bad

- Each tool call writes several rows and `synchronous=FULL` costs latency.
- State-machine validation is more complex than updating one status column.
- The ledger cannot determine an external effect's outcome after the backend
  stops responding.
- Recovery remains observational until an explicit reconciliation workflow is
  implemented; the lease supplies ownership but does not infer outcomes.
- One process holding a session lease makes other processes fail fast for that
  session until the lease closes or the owner exits.
- Recovery reads currently derive latest state from the retained session event
  history. A measured materialized-head/retention design is deferred; the
  runtime caps each provider response at 64 tool calls in the meantime.

## Rejected Options

### Snapshot-only inference

Rejected because a snapshot may predate dispatch, and UI running/error state is
not an authority boundary.

### Mutable execution rows

Rejected because overwriting status loses boundary evidence and makes replay
conflicts harder to audit.

### External workflow infrastructure

Rejected for the current local-first scope. It would add operational burden
without removing the need for backend idempotency or unknown-outcome handling.
