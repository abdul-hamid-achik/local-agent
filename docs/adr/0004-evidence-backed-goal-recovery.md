# ADR-0004: Evidence-backed goal recovery

## Status

Accepted and implemented — 2026-07-12

This ADR defines the authority boundary for recovery mutation in the TUI and
CLI. The atomic coordinator, TUI evidence workflow, and explicit CLI surface
implement that boundary. An outcome-unknown goal must still stay blocked unless
that shared coordinator commits exact evidence; operators cannot clear it by
editing session JSON or appending a standalone control resolution.

## Context

The execution ledger can prove that a tool request crossed its durable
dispatch boundary without being able to prove the backend outcome. The Goal
Runtime correctly blocks automatic continuation in that state, and the
exception control plane can retain an immutable question and resolution.

Before this decision, those pieces did not form a safe recovery operation:

- initial and user-directed goal turns were not all durably admitted before the
  provider command was created;
- a restored turn could contain several hazardous executions, or no execution
  event at all if the provider response was lost before any tool call;
- generic block resolution accepted host-authored evidence that was not bound to
  a durable control receipt;
- a control resolution and the owning session snapshot were written
  in separate transactions;
- raw execution recovery queries continued to report a reconciled
  `outcome_unknown` event, and the in-process Agent retained its hazard latch;
- bounded reads could contain more rows than their public limit, so truncation
  must never be interpreted as a complete recovery set.

Reconciliation is an operator assertion about inspected external state. It is
not a backend completion receipt, Goal acceptance proof, or permission to
retry an old execution identity.

## Decision Drivers

- Never create a provider command before the exact goal/turn admission is on
  disk.
- Preserve `execution_events` as the immutable forensic record.
- Bind every recovery conclusion to persisted session, workspace, goal, turn,
  item, execution, and latest-event identities.
- Commit the final control receipt and durable goal transition together.
- Make exact replay safe and conflicting replay fail closed.
- Keep a multi-execution turn blocked until every required member is resolved.
- Treat a full or corrupt bounded projection as incomplete, never safe.
- End recovery in `PAUSED` or `EXHAUSTED`; require a later explicit Resume.

## Decision Outcome

### Universal goal-turn admission

Every Goal Runtime provider turn is admitted durably before dispatch. The
admission identifies one of:

- `initial` — the first goal turn;
- `manual` — a later user-directed turn;
- `automatic` — a host-admitted continuation.

Only `automatic` admission consumes the continuation-turn budget. All kinds
carry the exact TurnID and must be settled by a matching receipt or recovered
explicitly. A failed admission save proves the provider command was not
created; recovery records that fact and never refunds an already consumed
automatic permit.

### Reconciliation groups

Unknown recovery is normalized to one group keyed by exact session, canonical
workspace, goal, and turn. The group has a turn-level parent authority item and
zero or more execution members.

Execution members correspond only to immutable lifecycles whose latest state
is `outcome_unknown`, or `started` with a non-read-only effect class. A turn
parent is still required when no execution member exists, because provider
dispatch may have occurred even though no tool lifecycle was created.

An execution lifecycle may belong to at most one reconciliation item. Its
stored turn must match the immutable execution identity. Duplicate, missing,
cross-scope, stale, or corrupt members make the group ineligible for clearance.

Completed post-snapshot effects are not unknown outcomes and must not be
dismissed through an execution-reconciliation receipt. They require an exact
projection-repair acknowledgement before the session cursor may cross them.

### Typed evidence

The user supplies only an observation, summary, evidence source, and reference.
The coordinator derives all durable identities. Supported execution
conclusions are:

- `effect_applied`;
- `effect_not_applied`;
- `effect_compensated`.

`still_unknown` is a UI choice that performs no mutation. Evidence documents
are versioned, bounded UTF-8 JSON and bind the original item payload hash,
latest execution event ID/type/hash, disposition, source, local actor label,
and observation time. Raw tool arguments, credentials, and backend secrets are
never copied into the evidence envelope.

The Goal Runtime stores a typed reconciliation receipt containing the final
item and resolution IDs, the deterministic resolution-set SHA-256, and target
count. Human-readable evidence remains presentation context, not the authority
binding. Generic goal block resolution cannot clear an outcome-unknown blocker.

### One atomic coordinator

One UI-independent coordinator owns the mutation. Under the exact live
session/workspace lease and one immediate SQLite transaction it:

1. loads the revisioned session snapshot and validates the blocked goal;
2. derives and validates the reconciliation group from durable records;
3. re-reads each execution lifecycle and rejects stale evidence;
4. appends or exactly replays the typed resolution;
5. verifies that every required group member is resolved;
6. if members remain, commits only the item resolution and leaves the goal
   blocked;
7. on the final member, applies the typed goal receipt and transitions the
   Goal Runtime only to `PAUSED` or `EXHAUSTED`;
8. updates the session snapshot, projection cursor, and revision in the same
   transaction; and
9. returns a durable receipt without scheduling a provider, Cortex evaluation,
   or Resume command.

An ambiguous commit is successful only when read-back finds both the exact
control resolution and exact revisioned goal receipt. A mixed state fails
closed and requires repair; it is never interpreted as permission to run.

Session-state revisions provide compare-and-swap semantics for future
controllers. The current Bubble Tea parent still serializes in-process writes,
but CLI recovery and the foreground supervisor must not rely on UI timing.

### Effective hazard projection

Raw execution APIs remain unchanged. Recovery APIs apply a separate overlay:

- suppress only an `outcome_unknown` or non-read-only `started` lifecycle that
  has exactly one valid scoped execution item and one typed `reconciled`
  resolution;
- never suppress a completed post-cursor lifecycle;
- treat malformed, duplicate, mismatched, or untyped receipts as corruption;
- filter reconciled rows before applying the caller's output limit; and
- paginate internally or fail when completeness cannot be proven.

The session cursor advances only after every intervening external effect is
either already represented by the snapshot or covered by the exact recovery
receipt. Agent latch release uses that committed cursor, then re-queries the
effective projection before any provider work.

### Operator surfaces

The TUI is the primary interactive flow: Goal Inspector → recovery list →
evidence form → confirmation → fresh Goal Inspector. The confirmation defaults
to Back and states that the ledger remains unchanged, acceptance is not
claimed, and AUTO will not resume.

The CLI offers a deterministic dry run and requires an explicit apply flag. It
acquires the same session lease and calls the same coordinator. There is no
`--force` escape hatch.

## Consequences

### Good

- Initial, manual, and automatic goal turns share one crash boundary.
- Evidence, goal state, and projection revision cannot partially commit on the
  normal write path.
- Multiple unknown effects can be reconciled incrementally without clearing
  the aggregate block early.
- The forensic execution ledger remains truthful and append-only.
- A future foreground supervisor can reuse the same recovery authority.

### Trade-offs

- Recovery requires more durable identity and revision metadata.
- Existing legacy outcome resolutions cannot be upgraded from prose alone.
- Completed-but-unprojected effects need a distinct projection repair path.
- Detached effectful execution remains deferred until OS capability
  containment exists.

## Verification

Release gates include:

- initial, manual, automatic, and no-tool provider crash/restart tests;
- multi-member groups where only the final receipt clears the blocker;
- stale event, forged identity/hash, wrong lease, duplicate, and corrupt-row
  rejection;
- concurrent exact and conflicting reconciliation attempts;
- transaction failure and ambiguous-commit read-back at every write boundary;
- more than one public page of hazards without cursor skipping;
- raw ledger byte equality before and after reconciliation;
- effective projection and Agent latch re-checks;
- proof that the resulting goal is paused or exhausted with no provider call;
- 30x12, reduced-motion, adaptive-theme, and confirmation-default TUI tests;
- CLI dry-run, apply, busy-lease, JSON, and exact replay tests.

## Follow-up

1. Persist supervisor evaluation bases and run events, then route TUI and
   foreground headless dispatch through `internal/supervisor`.
2. Add the separate exact projection-repair workflow for completed effects
   that are newer than the durable session transcript; recovery evidence must
   never stand in for that projection proof.
