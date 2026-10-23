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
bun run dev --session-db ~/.local-agent/sessions.db
```

`--session-db PATH` opts into durable sessions stored in a separate SQLite
database at `PATH`. Each run with the flag creates a new session unless
`--resume` is given. At most one session operation may be passed per run, and
every session operation requires `--session-db`.

There is no migration path from the retired Go implementation's session
storage.

## Resuming a session

```bash
bun run dev --session-db ~/.local-agent/sessions.db --resume HANDLE
```

`--resume HANDLE` loads a completed session from the session database by its
public id and continues it.

## Inspection commands

These commands only read the database. They contact no model and run no tools.

- `--sessions` — list sessions in the workspace as JSON (`{id, revision,
  messages}` per session).
- `--session-info ID` — inspect saved state and recovery hazards.
- `--recovery-info ID` — inspect recovery review tokens. It does not print the
  transcript or raw receipts.

Example:

```bash
bun run dev --session-db ~/.local-agent/sessions.db --sessions
```

## Switching interactively

In interactive modes, `/sessions` opens a session picker. It requires
`--session-db`; without it, the command tells you so. Navigate with up/down and
press enter to load a session. See [Modes](/modes) for the interactive clients.

## Interrupted work

Interrupted tool outcomes require inspection; they are never automatically
replayed. When you resume a session whose last run was interrupted, review what
the tool actually did before asking the agent to continue. `--session-info` and
`--recovery-info` exist for this review.

## Related

- [Reference](/reference) for the full flag list and cross-flag rules.
- [Safety](/safety) for approval and denial semantics.
