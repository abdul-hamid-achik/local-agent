---
title: 'Modes: plan and build'
description: Plan reads only; build proposes changes behind a per-proposal approval gate.
outline: deep
---

# Modes: plan and build

Every session runs with one intent: **plan**, **build**, or **chat**. The
default is build. The intent decides which tools the agent may offer and how
mutations are gated.

Switch intent with `shift+tab` at any time (build → plan → chat → build),
or run `/mode plan`, `/mode build`, or `/mode chat`.

## Plan

Plan instructs the model to inspect and explain; it does not request
mutations. Only read-only tools are offered: `read`, `read_image`, `list`,
`search`, `fetch`, `web_search`, `task`, and `todo_write` (the todo list
has no external effects, so planning keeps it; `task` spawns a read-only
subagent, so planning can investigate in parallel).

Plan hard-denies the following regardless of any approver:

- `write`
- `edit`
- `run_command`
- MCP tool calls

These denials are not approval prompts you can accept; the tool call is
refused outright.

In the Tuimark client every intent also offers `ask_user`, so the model can
ask you a question and wait for the answer. An answer is text for the
model, not a permission: plan and chat still refuse every effect after a
"yes".

## Chat

Chat is a conversational research profile: no coding preamble and no todo
list. It offers the read-only research set (`read`, `read_image`, `list`,
`search`, `fetch`, `web_search`) and `task`, which spawns a read-only
subagent: several `task` calls in one reply run in parallel, so chat can
look into independent questions at once. A subagent gets the read-only set
only, with no MCP, no todo list, and no `task` of its own.

In the Tuimark client a running subagent's card names its objective, its
turn budget and the seconds it has run; several running at once share one
`Subagents` card with a row each. A finished card gives the turns it took
and the first line of its answer; `/details N` shows the whole answer.

Chat hard-denies `write`, `edit`, and `run_command` like plan. Unlike plan
it keeps your configured MCP tools, each call behind its approval card, so
a browser or search server can read a page for you. Use chat for
questions, documentation lookups, and gaming guides; switch back to build
for edits.

In every intent the model is told to say only what its tool calls did, and
to name the tools and subagents it actually called.

## Build

Build instructs the model to propose concrete changes and respect host
approval decisions. Every mutating proposal — file edits and writes, commands,
MCP calls — passes through an approve callback before it runs.

Interactively, the Tuimark client's approval card offers:

- `y` — approve this one proposal
- `a` — approve this and matching proposals for the rest of the session
- `!` — always: save a rule for exactly this proposal (see [Persistent
  rules](/safety#persistent-rules))
- `n` or `esc` — deny; the turn goes on

The Vue client's card takes `y`, and `n`, `enter` or `esc` to deny.

In the Tuimark client, `/goal OBJECTIVE` runs build turns toward an
objective until the model reports it done or blocked, or a limit stops it.
Each turn keeps the cards above; see [A goal adds no
authority](/safety#a-goal-adds-no-authority).

In headless mode there are no cards. Approval is granted up front by class:

- `--approve-edits` — allow file edits and writes
- `--approve-commands` — allow command execution with your permissions
- `--approve-mcp` — allow configured MCP tool calls

Without the relevant flag, the headless run denies that class of proposal.
See [Reference](/reference) for the full flag surface.

On macOS, commands can run in a sandbox instead of asking: `/sandbox on` in
the Tuimark client, or `--sandbox-commands` (headless or Tuimark);
`/sandbox loopback` (or `--sandbox-loopback`) also opens TCP to this machine. A
confined command has no network, writes only in the workspace (never git's
hooks or config, a `.gitattributes`, nor `.local-agent`), and reads nothing in your home folder
beyond the workspace and your toolchains. Deny rules still refuse; an ask
rule still shows its card, and headless refuses instead. See
[Sandboxed commands](/safety#sandboxed-commands-macos).
