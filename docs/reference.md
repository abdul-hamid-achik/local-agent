---
title: Command reference
description: Every CLI flag, run mode, slash command, and keybinding of the Local Agent Bun CLI.
outline: deep
---

# Command reference

The CLI is started with `bun run dev` from the repository root (or `local-agent`
after `bun link` in `apps/cli`). It has one command surface: startup flags. There
are no subcommands. Run `local-agent --help` for the authoritative short list.

## CLI flags

| Flag | Semantics |
|---|---|
| `-h`, `--help` | Print help and exit |
| `--model MODEL` | Ollama model. Default `qwen3.5:4b-q4_K_M` or `LOCAL_AGENT_MODEL`; the flag overrides the environment. The default is the smallest model that passed the agent battery reliably |
| `--think on\|off` | Force thinking on or off; `LOCAL_AGENT_THINK=1\|0` is equivalent. Unset means the model default |
| `--context N` | Context tokens for local Ollama. Default 16384 or `LOCAL_AGENT_CONTEXT`; `32768` and `32k` both work. Cannot exceed the model's native context. Chalupa negotiates its own context |
| `-p`, `--prompt PROMPT` | Run one prompt; implies headless mode |
| `--mode fullscreen\|inline\|headless` | Run mode; default `fullscreen` |
| `--ui vue\|tuimark` | Client. `tuimark` is the default on macOS/Linux, `vue` on Windows; `tuimark` is rejected on Windows |
| `--theme NAME` | `dark`, `light`, `auto`, or a named theme (`midnight`, `sepia`). Tuimark client only |
| `--json` | Emit NDJSON events on stdout in headless mode; implies headless |
| `--workspace PATH` | Workspace directory; default is the current working directory |
| `--intent plan\|build` | Starting intent; default `build` |
| `--approve-edits` | Headless only: allow file edits and writes |
| `--approve-commands` | Headless only: allow command execution with your permissions |
| `--approve-mcp` | Headless only: allow configured MCP tool calls |
| `--mcp-config PATH` | Trusted JSON array of MCP servers. At most 1 MB and 16 servers; server names must be unique |
| `--session-db PATH` | Opt into experimental durable sessions in a separate database |
| `--resume HANDLE` | Resume a completed session from `--session-db` by its public id |
| `--sessions` | List sessions in the workspace without contacting the model; prints `{id, revision, messages}` JSON |
| `--session-info ID` | Inspect saved state and recovery hazards; runs no tools |
| `--recovery-info ID` | Inspect recovery review tokens; shows no transcript or raw receipts |
| `--mcp-status` | Requires `--mcp-config`. Connects and lists tools per server as JSON; exits 1 if any server is unavailable |

## Cross-flag rules

- At most one session operation may be given per run (`--sessions`,
  `--session-info`, `--recovery-info`, `--resume`).
- Session operations require `--session-db`.
- `--ui tuimark` cannot be combined with headless mode.
- Headless requires a non-empty prompt.
- `--approve-edits`, `--approve-commands`, and `--approve-mcp` are headless-only.
- Interactive modes require a TTY.
- Interactive runs pre-admit the Ollama model with a 15 s check before the
  session starts.

## Run modes

- **fullscreen** (default): the tuimark client takes over the terminal. The vue
  client can also run fullscreen via `--ui vue`.
- **inline**: vue by default. Tuimark runs inline only with
  `--ui tuimark --mode inline`; finished blocks print into the terminal's own
  scrollback.
- **headless**: selected by `--mode headless`, `--prompt`, or `--json`.

The tuimark binary is taken from `TUIMARK_BINARY` if set; otherwise a pinned
release (0.4.6) is downloaded and checksum-verified. If the default-client
binary is missing, the run falls back to vue with a note on stderr.

### Headless output

Text streams to stdout; tool_result statuses go to stderr. With `--json`, stdout
carries NDJSON events, starting with an initial `{"type":"session","id":...}`
event.

Approval in headless is fail-closed by default. Without `--approve-edits`,
`--approve-commands`, or `--approve-mcp`, the matching request class is denied:
there is no approval dialog to answer. See [Safety](/safety).

## Slash commands

| Command | Purpose |
|---|---|
| `/mode plan\|build` | Switch intent. `plan` reads only; `build` can edit |
| `/theme dark\|light\|auto` | Set the session theme |
| `/mouse on\|off` | Toggle mouse reporting; fullscreen only |
| `/permissions` | Show session approval grants |
| `/permissions clear` | Clear session approval grants |
| `/details` | Show the last tool output in full |
| `/status` | Show the effective config panel (model, workspace, session) |
| `/sessions` | Open the session picker; needs `--session-db` |
| `/export` | Write the transcript to a markdown file |
| `/mcp` | Show an MCP panel: server and tool counts, or a hint to use `--mcp-config` |
| `/help` | Show the command and key overlay |

An unknown command answers `Unknown command: X. Use /help`. While a turn is
busy, commands report a busy status instead of running.

## Keymap

| Key | Action |
|---|---|
| `ctrl+p` | Open the command palette |
| `esc` | Cancel the current turn |
| `ctrl+c` | Quit; clears the draft first |
| `up`, `down` | Browse input history |
| `ctrl+o` | Load earlier messages |
| `pgup`, `pgdn` | Scroll the log |
| `ctrl+l` | Jump to the latest output |
| `shift+tab` | Toggle between `plan` and `build` |
| `enter` | Send the draft |
| `shift+enter`, `alt+enter` | Insert a newline without sending |

Context-sensitive keys:

| Context | Keys |
|---|---|
| Slash popup | `up`/`down` choose, `tab` completes, `esc` closes |
| Approval dialog | `a` grants for the session; arrows, `pgup`, `home`, `end` scroll the diff |
| Panels and details | `q` or `esc` closes; arrows scroll |
| Sessions picker | `up`/`down` choose, `enter` loads |

The command palette mirrors the slash commands plus `ctrl+o` load-earlier
(fullscreen) and `ctrl+c` quit.
