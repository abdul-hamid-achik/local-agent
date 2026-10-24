---
title: Safety and privacy
description: What the Local Agent CLI enforces by default — loopback endpoints, plan-intent denials, per-proposal approval — and what it does not.
outline: deep
---

# Safety and privacy

The client is local-first and asks before it mutates anything. Those controls
validate endpoints and gate proposals one at a time; they are not an
operating-system sandbox.

## Local-first by default

The default provider is Ollama on the local machine, at `OLLAMA_HOST` — by
default `127.0.0.1:11434`. With that default, prompts stay on the machine.

The only remote provider is Chalupa, and it is opt-in through the
environment: the client refuses to start with Chalupa selected unless you
explicitly set `LOCAL_AGENT_LOCAL_ONLY=false`. See [Providers](/providers).

HTTP MCP endpoints follow the same posture: they must point at loopback
(`127.0.0.1`, `localhost`, `::1`) unless the server entry explicitly sets
`allowRemote: true`, and redirects are rejected. See [MCP](/mcp).

## Plan intent denies mutations

The client has two intents: plan and build, the default. In plan the model is
offered only the read-only tools — `read`, `list`, `search` — and the runtime
hard-denies every mutation, no matter what an approval grant says:

- `write`;
- `edit`;
- `run_command`;
- MCP tool calls — denied even when the server annotates a tool as read-only.

In plan these proposals never reach an approval prompt; the host refuses them
first. Switch intent with `--intent`, `/mode plan|build`, or `shift+tab`. See
[Modes](/modes).

## Build asks per proposal

In build, every mutating proposal — `write`, `edit`, `run_command`, and every
MCP tool call — goes through approval one proposal at a time. There is no
setting that approves everything. Reads (`read`, `read_image`, `list`, `search`, `fetch`, `web_search`) do
not prompt.

### Interactive approval

The dialog shows the action: a bounded diff for edits, the exact command for
commands. Press:

- `y` — allow this call once;
- `a` — allow this call and matching ones for the rest of the session;
- `n` or `esc` — deny.

`a` grants narrowly, and the dialog spells out the scope before you press it:
file changes (later `write` and `edit`), a command prefix (program plus first
argument — `bun test …`, not every `bun` invocation), or one exact
`SERVER__TOOL` MCP name. A grant is never taken from a preview too long to
read whole.

Session grants live in memory only. They die with the process. `/permissions`
lists them beside the file rules; `/permissions clear` revokes the session
grants, and `/permissions clear rules` empties the file.

### Persistent rules

`!` on an approval card saves the rule exactly as the dialog shows it
("Always allow `bun test *`") to `.local-agent/permissions.json` in the
workspace, creating the file (and a gitignore entry for its directory) on
first use. Rules are `{effect, tool, pattern}`, evaluated before any card:

- `edit` — a gitignore-style path glob relative to the workspace (`*` one
  segment, `**` recursive);
- `run_command` — an argv prefix with a trailing `*`; `npm test *` also
  matches bare `npm test`;
- `fetch` — `domain:host`, with `*.example.com` for subdomains;
- `mcp` — `server__tool` with `*` wildcards;
- `web_search` — `*`.

Evaluation is deny → ask → allow, first match wins; deny always beats a
session grant or a saved allow. Headless runs consult the file first in both
directions: a file allow approves with the flag off, a file deny refuses with
the flag on. Rules are the user's durable intent; the flags remain the
per-run switch.

### Headless approval

Headless runs have no approval UI, so they fail closed: proposals are denied
unless you pass the matching flag.

- `--approve-edits` — allow `write` and `edit`;
- `--approve-commands` — allow `run_command`;
- `--approve-mcp` — allow MCP tool calls.

The flags are headless-only and independent; `--approve-commands` does not
authorize edits. See [Command reference](/reference).

## run_command runs exactly as written

The approval card states the contract: "Runs exactly as written: there is no
shell." The argument list you see is the argument list that executes — no
shell interprets it, so there are no pipes, redirects, or expansions to
surprise you.

Approved commands run directly, with your permissions, in the workspace, under
a timeout — 120 seconds by default, capped at 10 minutes. Removing the shell
removes interpretation surprises; it does not reduce what a command can do,
because it runs as you.

## MCP calls cross the same boundary

Every MCP tool call is an approval proposal in build, a hard deny in plan, and
`--approve-mcp`-gated in headless. Tools are invoked under namespaced
`SERVER__TOOL` names, and a session grant covers exactly that name.

Configuring `--mcp-config` is itself a trust decision. Stdio servers run as
child processes of the agent with a minimal environment plus whatever their
entry configures; HTTP servers must be loopback unless the entry says
`allowRemote: true`. See [MCP](/mcp).

## What local-first is not

Local-first is an endpoint posture: loopback by default, loopback MCP unless
explicitly marked remote, and an explicit opt-out before remote inference. It
validates endpoints. It is not an operating-system sandbox:

- there is no filesystem, process, or network isolation;
- an approved command runs with your permissions — it can read and write
  outside the workspace and contact the network, because you can;
- MCP servers are separate processes acting under their own configuration;
- approval gates cover the client's tools; they do not constrain what an
  approved command or server process does next.

Keep valuable work in version control and backed up outside the agent
process.
