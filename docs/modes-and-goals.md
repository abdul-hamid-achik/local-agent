---
title: Modes and durable goals
description: Use NORMAL, PLAN, and AUTO authority safely, and review bounded durable goals before they run.
outline: deep
---

# Modes and durable goals

Modes describe host-enforced authority. Pressing `shift+tab` cycles the mode; it does not open a form, submit work, or create a goal.

## NORMAL

NORMAL is interactive work. The model can read the workspace and request edits, file operations, shell commands, memory changes, and MCP tools. Mutations remain subject to the approval policy.

Use NORMAL for a request where you want to stay in the loop turn by turn.

## PLAN

PLAN exposes workspace reads, search, listing, diff, existence checks, and memory recall. Mutations are rejected by the host even if the model asks for one.

Use PLAN to understand a repository, compare approaches, or produce a change plan without editing.

## AUTO

AUTO marks deliberate authority to run a bounded foreground goal. It is not YOLO and does not grant blanket approval.

After switching to AUTO, submit a prompt to open a reviewed draft. The draft must have:

- one objective;
- at least one independently checkable acceptance criterion; and
- at least one finite wall-time, evaluation-token, or automatic-turn limit.

Saving that review starts the first turn.

## Compact goal command

You can start with a duration and prompt:

```text
/goal 30m fix the flaky session restore test and prove the fix
```

Local Agent infers an editable objective and proof draft, then asks you to review it. The duration must be a valid Go-style value such as `15m`, `1h`, or `1h30m`; invalid duration-like input is rejected rather than silently becoming part of the objective.

For an empty or partial draft:

```text
/goal new
/goal new improve the model picker on narrow terminals
```

## Continuation and completion

Automatic continuation is conservative:

- the previous turn must produce a successful tool receipt;
- the goal must still have budget;
- persistence must be healthy;
- a linked Cortex case must advance semantically before another host-initiated turn.

Budget exhaustion, cancellation, a failed turn, unavailable Cortex status, or an unproductive yield pauses the run. Stopping never means success.

When Cortex is unavailable, the bounded goal remains useful but each later turn requires explicit `/goal resume`, and Local Agent does not declare its own completion.

## Goal controls

```text
/goal show     inspect criteria, proof, state, blockers, and budgets
/goal pause    stop automatic continuation
/goal resume   explicitly request one user-directed continuation
/goal budget   adjust limits without changing the immutable goal
/goal drop     abandon the goal without claiming completion
```

Goals run in the foreground. Closing the TUI cancels active work; it does not create a detached agent daemon.

## Recovery after uncertain effects

If a process stops after a side effect crossed the durable dispatch boundary but before its outcome was recorded, the goal becomes outcome-unknown. Local Agent will not retry that effect automatically.

Read-only CLI projections and an explicit evidence workflow are available for inspection and reconciliation. A successful reconciliation ends in PAUSED or EXHAUSTED and still requires a later explicit resume. See the [architecture](./architecture.md) for the durable execution contracts.
