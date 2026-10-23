---
title: 'Modes: plan and build'
description: Plan reads only; build proposes changes behind a per-proposal approval gate.
outline: deep
---

# Modes: plan and build

Every session runs with one intent: **plan** or **build**. The default is
build. The intent decides which tools the agent may offer and how mutations
are gated.

Switch intent with `shift+tab` at any time, or run `/mode plan` or
`/mode build`.

## Plan

Plan instructs the model to inspect and explain; it does not request
mutations. Only three read-only tools are offered: `read`, `list`, and
`search`.

Plan hard-denies the following regardless of any approver:

- `write`
- `edit`
- `run_command`
- MCP tool calls

These denials are not approval prompts you can accept; the tool call is
refused outright.

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
