---
title: Getting started
description: Install the Bun CLI from source, connect Ollama, and complete a first approval-gated session.
outline: deep
---

# Getting started

Local Agent is a local coding agent that runs from source in a Bun workspace.
There are no released binaries of the TypeScript client yet. The default model
runs on [Ollama](https://ollama.com/) on the same machine.

## Prerequisites

- macOS or Linux
- [Bun](https://bun.sh/) 1.4.2
- Ollama running locally with the default model `qwen3.5:4b-q4_K_M` pulled

Interactive runs pre-admit the model with a 15-second check; a missing model
is rejected up front, not mid-session.

## Install from source

```bash
git clone https://github.com/abdul-hamid-achik/local-agent.git
cd local-agent
bun install --frozen-lockfile
bun run dev
```

`bun run dev` starts the fullscreen tuimark client with the default model,
from the repository root. To put `local-agent` on your PATH, run `bun link`
in `apps/cli`.

Tags are releases: the [releases page](https://github.com/abdul-hamid-achik/local-agent/releases)
carries a source tarball with checksums for every `v*` tag (there are no
prebuilt binaries of the TypeScript client yet), and `--version` prints the
installed one.

## First session

Point a run at the repository you want the agent to inspect or change — the
workspace defaults to the current directory:

```bash
bun run dev --workspace /path/to/your/repository
```

The fullscreen client takes over the terminal and shows a welcome screen.

Type a prompt into the composer at the bottom and press `enter` to send.
`shift+enter` or `alt+enter` inserts a newline.

Start read-only:

```text
Read README.md and summarize the setup steps.
```

The session starts in **build** intent. When the model proposes a file edit,
write, or command, an approval card appears. Decide with:

| Key | Decision |
|---|---|
| `y` | Approve once |
| `a` | Approve this for the rest of the session |
| `n` | Deny |
| `esc` | Cancel the approval and the turn |

For `write`, `edit`, and `run_command` proposals the card shows a diff; the
arrow keys scroll it. Commands run exactly as written with your permissions —
there is no shell.

## Switch intent

Press `shift+tab` to toggle between **plan** and **build**. Plan offers only
read, list, and search tools; it hard-denies writes, edits, commands, and MCP
calls. Build can propose changes, each gated by an approval. See
[Modes](/modes).

## Try other surfaces

Tuimark inline mode prints finished blocks into your terminal's own
scrollback:

```bash
bun run dev --ui tuimark --mode inline
```

Named themes work with the tuimark client:

```bash
bun run dev --theme midnight
bun run dev --theme sepia
```

## Where to go next

- [Modes](/modes) — plan and build intents and how approval differs
- [Reference](/reference) — flags, slash commands, and keymap
- [Configuration](/configuration) — model, context, providers, and environment
