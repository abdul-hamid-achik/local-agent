# ADR-0003: UI-independent foreground goal supervisor

## Status

Accepted design; implementation partial — 2026-07-12

`internal/supervisor` currently implements and tests a non-mutating scheduling
decision contract, durable-evaluation-basis seam, and control-plane projection
adapter. `internal/workunit` implements and tests graph validation and
admission/settlement policy. Neither package drives the TUI, headless CLI, or
subprocesses yet. AUTO remains coordinated by the Bubble Tea Goal Runtime.

## Context

The first Goal Runtime integration lives inside the Bubble Tea parent model. It
already persists continuation permits before dispatch, refuses to infer
completion from prose or budget exhaustion, links Cortex revisions, and blocks
unknown effects. Those safety rules are useful, but a TUI event loop should not
be the only process capable of driving them.

Local Agent also needs headless `run --until-blocked`, inspect/resume commands,
and eventually a queue. Reimplementing the lifecycle separately in the CLI
would create two recovery policies. Moving directly to a detached worker would
be premature: an approved shell command is not yet constrained by an OS
sandbox, so a background process could outlive the terminal in which its
authority was granted.

Specialist delegation adds another constraint. Several read-only explorers can
be useful in parallel, but concurrent writers in one worktree produce ambiguous
ownership, invalid verification evidence, and unsafe recovery.

## Decision Drivers

- Keep one lifecycle and recovery policy across the TUI and headless CLI.
- Preserve the Goal Runtime as the authority for goal state and budgets.
- Make every automatic continuation deliberate, durable, and inspectable.
- Never redispatch an orphaned permit or unresolved effect automatically.
- Reuse the exact session/workspace execution lease.
- Support useful specialist decomposition without allowing writer races.
- Keep UI rendering and Bubble Tea messages outside the coordinator.
- Do not widen unattended authority before capability containment exists.

## Considered Options

1. Keep automatic goal progression entirely in the Bubble Tea model.
2. Implement a separate headless loop around the agent.
3. Add a UI-independent foreground supervisor used by both surfaces.
4. Start a detached background daemon immediately.

## Decision Outcome

Chosen option: **add one UI-independent foreground supervisor, then make the
TUI and CLI controllers/observers of it**.

The intended supervisor owns coordination, not presentation. Its bounded
interfaces are:

- a durable goal repository for snapshots and supervisor events;
- the existing session/workspace ownership lease;
- a turn driver that returns typed dispatch and settlement receipts;
- a Cortex advisor that returns correlation, progress, decision, blocker, and
  criterion-bound verification facts;
- the durable exception control plane for deferred approvals, human decisions,
  and outcome reconciliation;
- an optional constrained specialist work graph.

The Goal Runtime remains the state-machine authority. The wired supervisor must
persist a consumed continuation permit before handing a request to the turn
driver. A returned receipt is recorded against the exact turn ID. A missing or
mismatched receipt enters recovery; it is never converted into another turn.

### Foreground ownership

The initial wired supervisor will run in the foreground and hold the existing
exclusive session/workspace lease from recovery inspection through the final
persisted snapshot. TUI close, CLI cancellation, and process signals all cancel
the same context and join the active turn before releasing that lease.

Only one supervisor will drive a session. Other processes may inspect its
bounded projections but must not append execution or resolution events without
the lease. A busy lease is a visible `busy` state, not an orphan.

Detached effectful execution is deferred until Local Agent can bind explicit
filesystem, network, process, and environment capabilities to the run. A future
daemon may execute read-only work earlier, but it must use the same durable
protocol and cannot reinterpret existing approvals.

### Run-until-blocked contract

The proposed `RunUntilBlocked` is a bounded foreground operation. It will stop
on the first of:

- verified completion or explicit drop;
- pause, exhaustion, dependency block, or human decision;
- unresolved approval or outcome-unknown effect;
- unproductive turn or Cortex revision that did not advance;
- context cancellation, deadline, or persistence failure;
- configured turn/token/wall-time limit.

The stop result will include the exact goal snapshot, reason, last settled turn,
and unresolved control-plane item IDs. “Stopped” never means “completed.”

### Specialist work graph

`internal/workunit` is the dependency-free scheduling contract for the first
specialist slice:

- `explorer` and `verifier` units are read-only;
- only `implementer` units may request the writer effect policy;
- dependencies are local, explicit, unique, and acyclic;
- every verifier depends on an implementer and shares a named acceptance
  criterion with it;
- verifier completion requires bounded evidence;
- all eligible read-only units may be admitted together by the contract;
- at most one writer may be admitted or marked running in the shared workspace.

The graph schedules authority only. It does not spawn processes, persist
secrets, or bypass the agent permission checker. Independent git worktrees may
later permit multiple writer lanes, but that requires explicit isolation and a
deterministic integration stage.

### Product modes

The implemented interaction modes are:

- **NORMAL** — interactive work; mutations remain approval-gated;
- **PLAN** — read-only exploration and planning;
- **AUTO** — deliberate entry into the foreground Bubble Tea Goal Runtime until
  a verified stop condition; it does not yet use `internal/supervisor`.

AUTO is not YOLO and does not imply blanket approval. Saved ASK/BUILD sessions
are migrated to NORMAL because they were user-directed interaction modes;
saved PLAN sessions remain PLAN. An active durable goal restores as AUTO.

### Durable records

The supervisor will maintain first-class scoped goal/run projections and an
append-only event stream. Session snapshots remain a lossless conversation
projection during migration, but they are not used as a queue. Durable records
must include exact goal, session, workspace, run, and turn identities and must
be bounded before entering SQLite.

Control-plane resolutions are separate append-only evidence. Existing
execution events remain immutable. The supervisor derives a current view; it
never edits history to make a recovery appear clean.

## Consequences

### Good

- The decision contract gives future TUI and headless controllers one safety
  protocol; those surfaces have not converged on it yet.
- A terminal restart can inspect durable blockers without guessing what ran;
  evidence-backed execution reconciliation and headless resume are not wired.
- AUTO already has a visible foreground stop boundary, while supervisor-backed
  run-until-blocked remains follow-up work.
- Read-only specialist parallelism has a tested admission policy, not an
  executing specialist runner.
- The design leaves room for a contained daemon without making it a prerequisite
  for a useful foreground supervisor.

### Trade-offs

- Session snapshots remain the current goal projection; first-class supervisor
  goal/run/event projections are not implemented yet.
- The foreground TUI process must remain alive for current AUTO runs.
- A future shared-workspace runner will use one writer lane in exchange for
  deterministic ownership.
- Cortex verification remains necessary for automatic completion when linked.

### Risks and mitigations

- **Duplicate persistence authorities:** repository adapters must write one
  supervisor transition and its projection transactionally where possible;
  session snapshots are compatibility projections only.
- **UI drift:** action availability is derived from state-aware action metadata,
  not duplicated labels and switch statements.
- **False recovery:** lease ownership and append-only control-plane evidence are
  required before resolution.
- **Unbounded delegation:** graph/unit counts, prompts, budgets, evidence, and
  query results are capped.

## Verification

- Work-graph validation tests cover cycles, cross-graph dependencies, role and
  effect constraints, writer serialization, verifier evidence, retries, forged
  snapshots, cancelled contexts, and concurrent readers.
- Supervisor decision tests cover authority ordering, stale observations,
  durable Cortex evaluation identity, and decision/approval/outcome stops.
  Driver integration tests are still required for permit-before-dispatch,
  crash/restart, busy ownership, persistence failure, and verified completion.
- Glyphrun scenarios must cover AUTO entry, Goal Inspector, stop/resume, narrow
  terminal rendering, and restored blocked state.

## Follow-up

1. Persist the supervisor's pre-turn evaluation basis and first-class
   goal/run/event projections. `goal list`, `goal show`, `goal pending`, and the
   default `goal recover` dry run are read-only projections; only the complete
   explicit `goal recover --apply` form is an evidence controller.
2. Extract the current Bubble Tea scheduling logic behind the supervisor driver
   interfaces.
3. Add `run --until-blocked`, evidence-backed `goal resume`, and `goal watch`
   CLI controllers.
4. Bind specialist graph snapshots and receipts to supervisor runs.
5. Design and implement OS capability containment before detached effectful
   execution.
