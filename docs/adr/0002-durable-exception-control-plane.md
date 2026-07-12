# ADR-0002: Durable append-only exception control plane

## Status

Accepted — 2026-07-12

Implementation is partial at the product layer. The domain, migration, leased
SQLite store, Cortex-decision producer, unknown-execution producer, and bounded
read-only CLI projection are implemented. Foreground approvals do not yet
produce `deferred_approval` items, and Local Agent has no user-facing command or
modal that appends execution-reconciliation evidence and clears the matching
Goal Runtime blocker.

## Context

The execution ledger records what crossed a tool-effect boundary, and the goal
runtime records durable continuation state. Neither is the right authority for
questions that must survive process exit and wait for a person or supervising
system:

- a Cortex decision that cannot safely be reduced to a boolean;
- an approval intentionally deferred instead of answered in the foreground;
- evidence that reconciles an execution whose external outcome is unknown.

Keeping these questions only in TUI state loses them on crash. Updating a
mutable `pending` row when a user answers would lose the original request and
make conflicting retries difficult to distinguish from exact replay. Appending
a synthetic `completed` execution event would also falsify the execution
ledger: reconciliation evidence is not a backend completion receipt.

## Decision Drivers

- Preserve the exact request and the evidence that later resolved it.
- Scope every item and resolution to one persisted session and workspace.
- Allow optional goal, execution, and turn correlation without inventing
  foreign ownership across those domains.
- Reject a different replay of any durable identity.
- Prevent recovery from racing the process that currently owns the session.
- Keep reads bounded and useful to both a TUI and a future headless supervisor.
- Never rewrite `execution_events` during outcome reconciliation.
- Avoid storing raw secrets by accepting only caller-redacted JSON envelopes.

## Considered Options

1. Keep pending decisions in transient UI state.
2. Add mutable pending/status columns to goal or execution records.
3. Append questions and append one immutable evidence-backed resolution.
4. Adopt an external workflow engine or message broker.

## Decision Outcome

Chosen option: **append an immutable control item and, at most once, append an
immutable evidence-backed resolution**.

`internal/controlplane` owns dependency-free values and validation.
`internal/db/controlplane.go` owns SQLite transactions and projections.
Migration `005_control_plane.sql` adds two tables without changing the schema
or meaning of `execution_events`:

```text
control_items 1 ───── 0..1 control_resolutions
```

The first slice supports three item kinds:

- `cortex_decision` → `answered` or `dismissed`;
- `deferred_approval` → `approved`, `denied`, or `dismissed`;
- `execution_reconciliation` → `reconciled` only.

An execution-reconciliation item must identify an existing execution whose
latest durable state is `outcome_unknown`, or `started` with an effectful or
unknown effect class. A completed, read-only, missing, or otherwise safe
execution cannot acquire a reconciliation item. Resolution does not append,
update, or delete any execution event. It is designed to be an independent
authority receipt for a consumer that explicitly clears its recovery latch;
that consumer workflow is not wired in the current TUI or CLI.

### Identity and scope

Every item contains globally unique item and idempotency identifiers, plus the
exact persisted session ID and canonical workspace ID. Constructors provide
random IDs, while current producers use deterministic hash-derived IDs for
replay stability. Goal, execution, and turn IDs are correlation fields and may
be empty; execution ID is necessarily present for the
execution-reconciliation kind. An optional external ID can retain a provider or
Cortex reference.

Every resolution repeats its item ID, session ID, and workspace ID. SQLite
triggers verify that copied scope against both the parent item and the session.
Store reads always require exact session/workspace scope and never fall back to
an unscoped identifier lookup.

### Ownership boundary

Appending either an item or a resolution requires the live
`ExecutionSessionLease` for the same database, session, and workspace. The
Store holds the lease mutex through validation, transaction, and commit, so
`Close` cannot release the kernel lock partway through a mutation. A missing,
closed, cross-session, cross-workspace, or foreign-database lease fails closed.

Reads are observational and do not require ownership. This permits an inspector
to display pending work while another process owns execution without granting
the inspector write authority.

### Append-only and replay semantics

Items are never updated to `resolved`. The current state is a projection of one
item and its optional resolution. SQL triggers reject direct update and direct
delete; explicit parent-session deletion still cascades, matching execution
ledger retention.

Item ID and item idempotency key are independently unique. Resolution ID,
resolution idempotency key, and parent item ID are independently unique. The
Store looks up every candidate collision in one immediate transaction:

- all immutable request fields match: return the existing row with
  `inserted=false`;
- any field differs, or identifiers resolve to different rows: return a typed
  conflict and append nothing.

Caller-omitted occurrence times are store-assigned and act as replay wildcards;
an explicitly supplied time is immutable and must match exactly. Database row
IDs and record times are store metadata, not caller identity.

### Evidence and privacy

Payload and evidence are UTF-8 JSON objects bounded to 16 KiB. The caller binds
each exact document to a lowercase SHA-256 digest before storage. A resolution
also requires an actor and a valid kind-specific outcome, so it cannot be a
bare mutable status flip. SQL validates document shape, digest format, bounds,
scope, and outcome compatibility; the Store additionally verifies the digest
against the exact bytes.

These JSON objects are presentation/evidence envelopes. Callers must redact
arguments and secrets before constructing them. The control plane deliberately
does not copy raw execution arguments from the ledger or provider.

### Bounded projections

`GetControlState` returns one exactly scoped item. `ListControlStates` returns
newest-first state projections with a mandatory limit from 1 through 100 and
optional kind, goal, execution, turn, and pending-only filters. There is no
unbounded history API.

## Consequences

### Good

- Control items and resolutions written through the Store survive
  crash/restart without relying on TUI state; deferred-approval production is
  not wired yet.
- Store consumers can audit the original question and exact resolving evidence;
  the current CLI intentionally shows only pending least-privilege summaries.
- Exact retry is safe while altered replay fails closed.
- Reconciliation cannot silently convert unknown external work into a claimed
  backend completion.
- A TUI and future supervisor can share the same durable projection.
- Existing session snapshots and execution events remain unchanged.

### Bad

- Callers must retain idempotency identities and explicitly hold the session
  lease for mutations.
- The first slice permits only one terminal resolution; corrections require a
  new item that references the prior evidence rather than editing history.
- SQLite cannot recompute SHA-256 in a portable constraint, so direct SQL can
  validate only digest format; trusted writes must use the Store API.
- Session deletion removes the retained audit trail by explicit cascade, as it
  does for execution events.

## Rejected Options

### Transient UI decisions

Rejected because a crash, terminal close, or headless handoff loses the
authority question and can invite unsafe redispatch.

### Mutable status rows

Rejected because overwriting pending state discards evidence and makes an exact
retry indistinguishable from a conflicting answer.

### Reconciliation events in `execution_events`

Rejected because human or Cortex evidence is not proof that the original tool
backend completed. The execution ledger remains a record of its own lifecycle
facts.

### External workflow infrastructure

Rejected for the current local-first slice. A future coordinator can consume
this contract without making the local CLI depend on additional services.
