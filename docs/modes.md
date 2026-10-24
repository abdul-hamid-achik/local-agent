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

## Chat

Chat is a conversational research profile: no coding preamble, no todo
list, no subagents. It offers the same read-only research set (`read`,
`list`, `search`, `fetch`, `web_search`, `read_image`) and hard-denies
every effect exactly like plan. Use it for questions, documentation
lookups, and gaming guides; switch back to build for edits.

## Build

Build instructs the model to propose concrete changes and respect host
approval decisions. Every mutating proposal — file edits and writes, commands,
MCP calls — passes through an approve callback before it runs.

Interactively, the approval card offers:

- `y` — approve this one proposal
- `a` — approve this for the rest of the session
- `n` — deny

In headless mode there are no cards. Approval is granted up front by class:

- `--approve-edits` — allow file edits and writes
- `--approve-commands` — allow command execution with your permissions
- `--approve-mcp` — allow configured MCP tool calls

Without the relevant flag, the headless run denies that class of proposal.
See [Reference](/reference) for the full flag surface.

On macOS, commands can run in a sandbox instead of asking: `/sandbox on` in
the Tuimark client, or `--sandbox-commands` (headless or Tuimark). A
confined command has no network, writes only in the workspace (never git's
hooks or config, a `.gitattributes`, nor `.local-agent`), and reads nothing in your home folder
beyond the workspace and your toolchains. Deny rules still refuse; an ask
rule still shows its card, and headless refuses instead. See
[Sandboxed commands](/safety#sandboxed-commands-macos).
