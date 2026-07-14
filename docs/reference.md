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
| `local-agent -p "prompt"`, `local-agent --prompt "prompt"` | Run one human-readable NORMAL prompt |
| `local-agent --plan --prompt "prompt"` | Run one read-only PLAN prompt; equivalent to `--mode plan` |
| `local-agent --auto --prompt "prompt"` | Run one proactive AUTO prompt; equivalent to `--mode auto` and does not skip approvals |
| `local-agent --mode <normal\|plan\|auto> --prompt "prompt"` | Select headless authority explicitly |
| `local-agent --model <name>` | Select and pin the initial Ollama model |
| `local-agent --agent <name>` | Select the initial agent profile |
| `local-agent --resume <id\|latest>` | Open the TUI and restore an exact or newest current-workspace session |
| `local-agent --qwen-router` | Enable the experimental Qwen-specific router |
| `local-agent --skip-approvals` | Skip approval prompts while preserving explicit denies and host/tool boundaries |
| `local-agent --yolo` | Deprecated compatibility alias for `--skip-approvals` |
| `local-agent init [--force]` | Create a project `AGENTS.md` |
| `local-agent logs` | List recent structured logs |
| `local-agent logs -f` | Follow the latest log |
| `local-agent --version` | Print the build version |

`-p` and `--prompt` are exact aliases for a human-readable convenience surface,
not a stable JSON event protocol. `--auto` and `--plan` require a non-empty
prompt, are mutually exclusive, and reject a conflicting explicit `--mode`.
AUTO does not imply `--skip-approvals`. An explicitly empty or whitespace-only
prompt exits with status 2 before configuration, network, or provider startup.
In headless mode, requests that need an approval fail closed by default because
there is no approval UI.

`--resume` is interactive-only and cannot be combined with `-p` or `--prompt`. Session IDs
must be positive integers; `latest` selects the most recently updated session
whose canonical workspace matches the startup workspace. The restore runs after
TUI initialization and does not dispatch provider work by itself.

## Durable-goal inspection

These commands operate on validated goal state for the current workspace:

```bash
local-agent goal list --limit 20
local-agent goal show <session-id>
local-agent goal pending <session-id>
local-agent goal recover <session-id>
```

Add `--json` for machine-readable projections. The default `goal recover` is a dry run. Applying typed recovery evidence requires the complete explicit form printed by `--help`; there is no force flag. Successful reconciliation pauses or exhausts the goal and never schedules provider work by itself.

An ordinary session with an outcome-unknown tool receipt uses a separate exact-execution workflow:

```bash
local-agent execution recover <session-id> <execution-id>
```

Inspection is read-only. Applying evidence requires the exact revision and event ID printed by inspection plus a typed observation, source, reference, summary, and observation time. It records an immutable reconciliation receipt; it never retries the tool or rewrites its original outcome.

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
| `/skill`, `/skill list` | List discovered skills and their activation state |
| `/skill activate <name>`, `/skill deactivate <name>` | Add or remove one skill from active prompt context |
| `/load <path>`, `/unload` | Add or remove one bounded Markdown context file |
| `/scope list` | List process-local external exact-file and directory read grants |
| `/scope add-read <directory>` | Grant read-only access to one directory outside the writable workspace |
| `/scope remove-read <path>`, `/scope clear-read` | Revoke one or every external read-only grant |
| `/servers` | Show connected MCP servers and tool count |
| `/ice` | Show optional ICE status |
| `/sessions`, `/resume` | Open the saved-session picker; neither command accepts an ID argument |
| `/artifacts`, `/artifact` | List bounded file.cheap stash receipts in the current session |
| `/changes` | List files modified in the current TUI session |
| `/stats` | Show in-memory token and context counters |
| `/checkpoint [label]` | Save current agent history |
| `/checkpoints` | List checkpoints |
| `/restore <id>` | Restore a checkpoint from the active session |
| `/recover` | Review the current session's outcome-unknown execution and record typed evidence |
| `/exit` | Quit |

Slash commands use a small argument parser rather than a shell. Single or
double quotes and backslash-escaped whitespace group arguments; environment
variables and command substitutions remain literal. `/load`, `/scope`, `/import`,
and `/export` separately expand a leading `~/`. An unterminated quote is rejected
before command dispatch. Documented arity is enforced: commands with no
arguments and `/goal show`, `/goal pause`, `/goal resume`, `/goal budget`, or
`/goal drop` reject trailing fields, while `/restore` accepts exactly one
canonical positive decimal ID. `/load` accepts a regular, non-symlink Markdown
file up to 32 KB.

## Goal commands

| Command | Purpose |
|---|---|
| `/goal <duration> <prompt>` | Infer bounded criteria and start a concrete goal; ambiguous prompts ask one follow-up |
| `/goal [objective]` | Open the inline review, optionally prefilled; bare `/goal` shows the active goal when one exists |
| `/goal new [objective]` | Open an inline, editable goal review below the transcript |
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
| `esc` | Close an overlay or inline form, cancel an approval, or cancel active generation |
| `ctrl+n`, `ctrl+l` | New conversation or clear the view |
| `ctrl+c` | Quit |

The supported minimum terminal is 30 columns by 12 rows. Compact status rows
retain skipped-approval, unavailable-MCP, and Cloud/Remote boundaries instead
of truncating the rightmost state. At that minimum, file approvals preserve an
identifying target tail and show explicit paging plus exact-argument controls.
Below the minimum, input is paused except for `ctrl+c`; resizing restores the
unchanged composer, overlay, or pending authority decision.
Set `LOCAL_AGENT_REDUCED_MOTION=1` to replace active animations with static
state indicators.
