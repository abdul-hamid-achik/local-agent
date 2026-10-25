---
title: Sessions (experimental)
description: Opt-in durable sessions in the Bun CLI -- session database, resume, inspection commands and recovery hazards.
outline: deep
currentImplementation: true
---

# Sessions (experimental)

Durable sessions are opt-in and experimental. Without the flags below, every
run starts a fresh session and nothing is persisted. Sessions live in a
separate SQLite database you choose; the agent does not create one by default.

There is no memory store, no ICE and no checkpointing in the current client. A
session records the transcript state you need to resume; it does not carry
facts across unrelated sessions.

## Enabling the session database

```bash
local-agent --session-db ~/.local-agent/sessions.db
```

`--session-db PATH` opts into durable sessions stored in a separate SQLite
database at `PATH`. Each run with the flag creates a new session unless
`--resume` is given. At most one session operation may be passed per run, and
every session operation requires `--session-db`.

There is no migration path from the retired Go implementation's session
storage.

A database from an earlier Bun release (schema v1 or v2) is migrated to
schema v3 the first time this version opens it, with its history kept. An
older local-agent refuses a v3 database, so keep a copy if you may go back.

## Resuming a session

```bash
local-agent --session-db ~/.local-agent/sessions.db --resume HANDLE
```

`--resume HANDLE` loads a completed session from the session database by its
public id and continues it. Before anything else it closes the calls the
session stopped before they ran, or that only read: each gets a result that
says it did not finish, so the model is told. The CLI prints
`note: closed N tool call(s) …` on stderr when it did. A session that still
has a call with an unknown outcome (see [Interrupted work](#interrupted-work))
is refused by headless runs and by the Vue client, with an error that names
`--recover`. The Tuimark client opens it, and a prompt waits until you have
recorded what happened.

## Inspection commands

These commands only read the database. They contact no model and run no tools.

- `--sessions` — list sessions in the workspace as JSON (`{id, revision,
  messages}` per session).
- `--session-info ID` — inspect saved state and recovery hazards.
- `--recovery-info ID` — inspect recovery review tokens. It does not print the
  transcript or raw receipts. It returns the session revision and the first
  100 hazards, with the total.

Example:

```bash
local-agent --session-db ~/.local-agent/sessions.db --sessions
```

## Switching interactively

In interactive modes, `/sessions` opens a session picker. It requires
`--session-db`; without it, the command tells you so. Navigate with up/down and
press enter to load a session. See [Modes](/modes) for the interactive clients.

In the Tuimark client, `/sessions new` starts over: a new saved session with
`--session-db`, otherwise a new temporary conversation. It is refused while a
turn runs. Session grants are this process's trust, so they survive it, and
the status line says so.

## Long sessions

A session keeps every message. When the conversation no longer fits the
context window, even with old tool results shortened, each request carries
only its newest whole exchanges, and the model is told how many of the oldest
it cannot see. The messages a request leaves out are saved as they were.
The todo list survives that: in modes that offer `todo_write`, while an item
on it is pending or in progress, a request that leaves the list out carries
it in the system prompt, in at most a quarter of the window (done items give
way first, then pending ones, marked `…`). A
conversation holds up to 9999 messages and a session's saved state up to
8 MiB; past either, the turn is refused with a sentence that says so. Start
over with `/sessions new`.

The Tuimark client and headless `--prompt` runs keep the saved state well
under that cap: past 6 MiB, before the next request, they shorten it from
the start, images first, then tool results the model has already read, then
the reasoning of earlier turns. The current turn stays whole. In the
Tuimark client, `/compact` does the same at once, whatever the state weighs.

A single tool result too large for the state to keep is saved as a short note
in its place, with the call's status and its size, and the turn goes on: the
model reads the note and can ask for less, for example a narrower read. The
call's record in the session ledger keeps the first 16 KiB of what the tool
returned and a SHA-256 of all of it. When not
even the note fits, nothing is saved for the call, the turn is refused with
the sentence above, and the call is handled as
[interrupted work](#interrupted-work).

## Interrupted work

Interrupted tool outcomes require inspection; they are never automatically
replayed. What a resumed session does with them depends on what the call was
doing when the run stopped.

- **A command stopped by `esc` or by its timeout** is recorded as failed or
  cancelled once its process group is gone. The model is told the command was
  stopped and may have made partial changes or left a background process
  running, so it should check before running it again. A descendant that left
  the group (a daemon, a `setsid` child) is not seen.
- **A call that never started, or that only reads,** is closed when you
  resume, as above.
- **A command whose cleanup is incomplete** (a pipe held open, a signal
  refused, a group that outlived SIGKILL) stays an unknown outcome, like a
  call the process died in the middle of.

A call with an unknown outcome blocks the session until you record what
happened. Look at the workspace first, then say what you found. Nothing is run
again; the record is kept beside the session's ledger, which is never
rewritten. Calls are answered in order: an earlier one first.

From the shell:

```bash
# List the calls awaiting a decision (JSON)
local-agent --session-db ~/.local-agent/sessions.db --recover HANDLE

# Show what would be recorded (a dry run)
local-agent --session-db ~/.local-agent/sessions.db --recover HANDLE \
  --resolve EXECUTION=applied --note "the file was written"

# Record it
local-agent --session-db ~/.local-agent/sessions.db --recover HANDLE \
  --resolve EXECUTION=applied --note "the file was written" --apply
```

- `applied`: the call ran and its change is in place.
- `not-applied`: it made no change.
- `undone`: you undid its change.

`EXECUTION` is the `executionId` the listing shows. `--note` is at most 1000
characters. `--resolve`, `--note` and `--apply` go with `--recover`, and
`--note` and `--apply` need `--resolve`.

In the Tuimark client, `/recover` lists the calls in a panel, numbered, and
`/recover N applied|not-applied|undone [note]` records the Nth. While one
remains, sending a prompt only shows a status that names `/recover`, and a
running `/goal` stops there. `--session-info` and `--recovery-info` exist for
reviewing the same hazards without changing anything.

## Related

- [Reference](/reference) for the full flag list and cross-flag rules.
- [Safety](/safety) for approval and denial semantics.
