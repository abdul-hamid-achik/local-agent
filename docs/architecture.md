---
title: Architecture
description: How the Local Agent Bun monorepo separates the CLI, the inference runtime, the Tuimark client, and tool approval boundaries.
---

# Architecture

Local Agent is a Bun monorepo. The conversation surface, the inference runtime, and host authority are separate packages.

```text
apps/cli (composition) -----> packages/runtime (inference + host authority)
        |                              |
        +-- tuimark client             +-- tools: read, read_image, list, search, fetch, web_search,
        |   (declarative document)     |   task, todo_write, ask_user, goal_status,
        |                              |   edit, write, run_command
        |                              +-- MCP client + approval boundary
        |
        +-- vue client (packages/vue-tui)
```

## Repository layout

- `apps/cli` composes the product: flag parsing, mode selection, client launch, session wiring. It owns no inference logic.
- `packages/runtime` owns the agent loop, providers, tools, and approval. It is the host authority: the model never enforces its own mode, and every effect crosses the runtime approval boundary. Runtime code imports no UI.
- `packages/vue-tui` is the internal, extractable Vue terminal framework used by the `--ui vue` client. It is presentation only and never a source of evidence.
- `apps/tui-playground` exists for quick Vue SFC checks against the terminal framework.
- `docs/` is this site.

## Tuimark client

The default interactive client lives in `apps/cli/src/tuimark`. It does not render cells itself; it emits a declarative document that the `tuimark` host binary renders. The SDK is the npm package `@thelacanians/tuimark`, pinned to release 0.5.2; the binary comes from `TUIMARK_BINARY` or is downloaded checksum-verified. Tuimark is [open source](https://github.com/abdul-hamid-achik/tuimark). If no `--ui` is given and the binary is missing, or `TUIMARK_BINARY` names one older than 0.5.1 or one whose `--version` names no version, Local Agent falls back to the Vue client with a note on stderr that says why.

## Presentations

Fullscreen, inline, and headless are distinct presentations over one runtime:

- **Fullscreen** (default): the client takes over the terminal.
- **Inline**: finished blocks print into the terminal's own scrollback.
- **Headless**: no client at all. It never starts the native renderer; text streams to stdout and tool statuses to stderr, or NDJSON events with `--json`. Approval in headless is decided up front by the `--approve-*` flags, not interactively.

## Tools and approval

Tools live in `packages/runtime`, not in any client. Reads (`read`, `read_image`, `list`, `search`, `fetch`, `web_search`) run without approval in build mode. `edit`, `write`, `run_command`, and MCP calls are effects: each proposal is approved per effect through the host approve callback. Plan intent offers read-only tools only and hard-denies effects regardless of the approver. Chat hard-denies edits, writes and commands the same way and keeps MCP calls behind approval; a `task` subagent, from any intent, gets the read-only tools only.

## MCP boundary

MCP servers are configured directly via `--mcp-config` (a trusted JSON array, at most 16 servers) and invoked as `SERVER__TOOL` namespaced calls. Raw structured content stays inside the MCP parser boundary; the transcript and saved sessions carry only what the runtime projects. A green transport receipt is never a domain result.
