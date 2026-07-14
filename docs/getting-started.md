---
title: Getting started
description: Install local-agent, connect Ollama, and complete a first approval-gated coding session.
outline: deep
---

# Getting started

Install `local-agent`, connect it to Ollama, and open a repository. The basic local model workflow does not require a configuration file.

::: warning Alpha software
Run Local Agent in a clean Git worktree, read approval requests, and review the resulting diff. The current safety layer is useful, but it is not an operating-system sandbox.
:::

## Requirements

- macOS or Linux
- [Go 1.25 or newer](https://go.dev/dl/)
- [Ollama](https://ollama.com/) running on the same machine
- A Git worktree for work you want the agent to inspect or change

Windows release binaries are not published yet.

## Install

Install the latest tagged Go release:

```bash
go install github.com/abdul-hamid-achik/local-agent/cmd/local-agent@latest
```

Or run a checkout directly:

```bash
git clone https://github.com/abdul-hamid-achik/local-agent.git
cd local-agent
go run ./cmd/local-agent
```

## Prepare Ollama

Start Ollama and pull a compact model:

```bash
ollama serve
ollama pull qwen3.5:2b
```

The 4B tier is optional and is the preferred installed tier for coding, debugging, review, and multi-step tool use:

```bash
ollama pull qwen3.5:4b
ollama list
```

Local Agent reads Ollama's live inventory. You do not need to duplicate every installed model in configuration.

## Start a first session

Open the repository you want to work in, then launch the TUI:

```bash
cd /path/to/your/repository
local-agent
```

Try a read-only request first:

```text
Explain the request flow in this repository and identify the tests that cover it.
```

Local Agent begins in **NORMAL** mode. Reads can proceed inside the workspace. Mutating tools such as edits, writes, shell commands, and MCP calls require approval by default.

To reopen saved work directly, pass a positive session ID or select the newest
session in the current canonical workspace:

```bash
local-agent --resume 42
local-agent --resume latest
```

Startup resume is available only in the interactive TUI, so it cannot be
combined with `-p`. It restores state without sending a prompt or automatically
continuing a durable goal.

## Essential controls

| Key | Action |
|---|---|
| `enter` | Send the prompt |
| `shift+enter` | Insert a newline |
| `shift+tab` | Cycle NORMAL, PLAN, and AUTO |
| `ctrl+o` | Open the live Ollama model picker |
| `ctrl+p` | Open session settings |
| `tab` | Complete commands, paths, and skills |
| `esc` | Close an overlay or inline form, cancel an approval, or cancel active work |
| `ctrl+c` | Quit |

Inside the inline approval surface, use `y` to allow once, `n` to deny, `s` to
allow the identical canonical request again during the current Agent process,
or `d` to inspect exact arguments. Press `esc` to cancel the approval and the
active turn. No broad allow-by-tool-name policy is persisted by this flow.

## Optional configuration

Repository-local configuration takes precedence over XDG and home configuration:

```bash
cp config.example.yaml local-agent.yaml
```

For a user-wide configuration:

```bash
mkdir -p ~/.config/local-agent
cp config.example.yaml ~/.config/local-agent/config.yaml
```

Continue with [Ollama models](./ollama-models.md), [authority modes and goals](./modes-and-goals.md), or the complete [configuration reference](./configuration.md).
