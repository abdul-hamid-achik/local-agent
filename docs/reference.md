---
title: Command reference
description: Find Local Agent CLI commands, slash commands, keyboard shortcuts, and automation boundaries in one place.
outline: deep
---

# Command reference

Local Agent has three operator surfaces: the interactive TUI, human-readable headless prompts, and read-only or evidence-gated durable-goal inspection commands.

## CLI

| Command | Purpose |
|---|---|
| `local-agent` | Open the TUI in the current workspace |
| `local-agent -p "prompt"` | Run one human-readable NORMAL prompt |
| `local-agent --mode plan -p "prompt"` | Run one read-only PLAN prompt |
| `local-agent --model <name>` | Select and pin the initial Ollama model |
| `local-agent --agent <name>` | Select the initial agent profile |
| `local-agent --qwen-router` | Enable the experimental Qwen-specific router |
| `local-agent --yolo -p "prompt"` | Bypass approvals for a trusted disposable workflow |
| `local-agent init [--force]` | Create a project `AGENTS.md` |
| `local-agent logs` | List recent structured logs |
| `local-agent logs -f` | Follow the latest log |
| `local-agent --version` | Print the build version |

`-p` is a human-readable convenience surface, not a stable JSON event protocol. Without `--yolo`, risky and MCP calls fail closed because headless mode has no approval UI.

## Durable-goal inspection

These commands operate on validated goal state for the current workspace:

```bash
local-agent goal list --limit 20
local-agent goal show <session-id>
local-agent goal pending <session-id>
local-agent goal recover <session-id>
```

Add `--json` for machine-readable projections. The default `goal recover` is a dry run. Applying typed recovery evidence requires the complete explicit form printed by `--help`; there is no force flag. Successful reconciliation pauses or exhausts the goal and never schedules provider work by itself.

## Conversation commands

| Command | Purpose |
|---|---|
| `/help` | Open keyboard and command help |
| `/clear`, `/new` | Start a clean conversation |
| `/model`, `/models` | Open the live Ollama inventory |
| `/model list` | List currently admitted models |
| `/model <name>` | Switch to and pin a model |
| `/model auto` | Release the pin and resume local automatic routing |
| `/agent [name\|list]` | List or switch profiles |
| `/skill [list\|activate\|deactivate]` | Manage skills |
| `/load <path>`, `/unload` | Add or remove one bounded Markdown context file |
| `/servers` | Show connected MCP servers and tool count |
| `/ice` | Show optional ICE status |
| `/sessions` | Open saved workspace sessions |
| `/changes` | List files modified in the current TUI session |
| `/stats` | Show in-memory token and context counters |
| `/checkpoint [label]` | Save current agent history |
| `/checkpoints` | List checkpoints |
| `/restore <id>` | Restore a checkpoint from the active session |
| `/exit` | Quit |

`/load` accepts a regular, non-symlink Markdown file up to 32 KB. Quoted paths are supported.

## Goal commands

| Command | Purpose |
|---|---|
| `/goal <duration> <prompt>` | Infer and open a reviewed bounded goal draft |
| `/goal new [objective]` | Open an empty or partial goal review |
| `/goal show` | Inspect objective, criteria, proof, state, and budgets |
| `/goal pause` | Stop host-initiated continuation |
| `/goal resume` | Explicitly request one user-directed continuation |
| `/goal budget` | Change limits without editing the immutable goal |
| `/goal drop` | Abandon work without claiming completion |

Use Go duration syntax such as `30m` or `1h30m`. Duration-shaped but invalid input is rejected explicitly.

## Import, export, and Git

| Command | Purpose |
|---|---|
| `/export [--force] <path>` | Atomically export an owner-private Markdown transcript envelope |
| `/import <path>` | Import a supported transcript into a new session |
| `/commit [context]` | Generate a message from staged changes and run a constrained commit |

`/commit` intentionally disables Git hooks, signing, fsmonitor helpers, pagers, and automatic maintenance for its subprocess. Run `git commit` yourself when hooks or signing are required.

## Keyboard

| Key | Action |
|---|---|
| `enter`, `shift+enter` | Send or insert a newline |
| `shift+tab` | Cycle NORMAL, PLAN, and AUTO without opening a form |
| `ctrl+p` | Open session settings |
| `ctrl+o` | Open the Ollama model picker |
| `tab` | Complete commands, files, and skills |
| `up`, `down` | Browse input history |
| `pgup`, `pgdown`, `ctrl+u`, `ctrl+d` | Scroll the conversation |
| `t`, `space` | Toggle all tool details or the latest tool |
| `ctrl+t` | Toggle model thinking display |
| `ctrl+y` | Copy the latest response |
| `ctrl+e` | Edit input with `$EDITOR` |
| `ctrl+k` | Toggle compact transcript layout |
| `esc` | Close an overlay, deny approval, or cancel active generation |
| `ctrl+n`, `ctrl+l` | New conversation or clear the view |
| `ctrl+c` | Quit |

The supported minimum terminal is 30 columns by 12 rows. Set `LOCAL_AGENT_REDUCED_MOTION=1` to replace active animations with static state indicators.
