---
title: Command reference
description: Every CLI flag, run mode, slash command, and keybinding of the Local Agent Bun CLI.
outline: deep
---

# Command reference

The CLI is the `local-agent` command (see [Install](/getting-started#install);
from a checkout, `bun run dev` takes the same flags). It has one command
surface: startup flags. There are no subcommands. Run `local-agent --help` for
the authoritative short list.

## CLI flags

| Flag | Semantics |
|---|---|
| `-h`, `--help` | Print help and exit |
| `--version` | Print the CLI version and exit |
| `--model MODEL` | Ollama model. Default `qwen3.5:4b-q4_K_M` or `LOCAL_AGENT_MODEL`; the flag overrides the environment. The default is the smallest model that passed the agent battery reliably |
| `--think on\|off` | Force thinking on or off; `LOCAL_AGENT_THINK=1\|0` is equivalent. Unset means the model default |
| `--context N` | Context tokens for local Ollama, or `LOCAL_AGENT_CONTEXT`; `32768` and `32k` both work. Cannot exceed the model's native context. Default: planned from the model and the machine's memory, at most 32768 (see [Ollama models](/ollama-models#context)). Chalupa negotiates its own context |
| `-p`, `--prompt PROMPT` | Run one prompt; implies headless mode |
| `--mode fullscreen\|inline\|headless` | Run mode; default `fullscreen` |
| `--ui vue\|tuimark` | Client. `tuimark` is the default on macOS/Linux, `vue` on Windows; `tuimark` is rejected on Windows |
| `--theme NAME` | `dark`, `light`, `auto`, or a named theme such as `midnight`, `catppuccin-mocha`, `tokyo-night`, `nord` or `dracula` ([all themes](/configuration#themes)). Tuimark client only |
| `--json` | Emit NDJSON events on stdout in headless mode; implies headless |
| `--workspace PATH` | Workspace directory; default is the current working directory |
| `--intent plan\|build\|chat` | Starting intent; default `build`, which asks before each edit or command. `plan` refuses edits, writes, commands and MCP calls; `chat` refuses edits, writes and commands but keeps MCP calls, each behind its approval ([Modes](/modes)) |
| `--approve-edits` | Headless only: allow file edits and writes |
| `--approve-commands` | Headless only: allow command execution with your permissions |
| `--sandbox-commands` | macOS: run commands in the sandbox without asking: no network, writes only in the workspace, no reads in the home folder beyond toolchains (see [Sandboxed commands](/safety#sandboxed-commands-macos)). Headless, no command runs outside it: deny and ask rules refuse, allow rules run confined; a command that names a path the workspace treats as secret is refused. In the Tuimark client, starts with `/sandbox on` |
| `--sandbox-loopback` | With `--sandbox-commands`: confined commands may also use TCP to this machine, in and out, so a test can start its own server and reach a local service. It opens every local port (Ollama and any other local server included), and a server a command starts may be reachable from your network; connecting to another host is refused, measured against the router and the internet (see [Loopback](/safety#loopback-opt-in)). In the Tuimark client, starts with `/sandbox loopback` |
| `--approve-mcp` | Headless only: allow configured MCP tool calls |
| `--mcp-config PATH` | Trusted JSON array of MCP servers, instead of your own `~/.config/local-agent/mcp.json` (loaded by default when it exists). At most 1 MB and 16 servers; server names must be unique |
| `--no-mcp` | Start without your own `mcp.json` |
| `--session-db PATH` | Opt into experimental durable sessions in a separate database |
| `--resume HANDLE` | Resume a completed session from `--session-db` by its public id. Calls the session stopped before they ran, or that only read, are closed first (stderr: `note: closed N tool call(s) …`). A session with a call of unknown outcome is refused headless and in the Vue client until it is recorded (see `--recover`); the Tuimark client opens it |
| `--sessions` | List sessions in the workspace without contacting the model; prints `{id, revision, messages}` JSON |
| `--session-info ID` | Inspect saved state and recovery hazards; runs no tools |
| `--recovery-info ID` | Inspect recovery review tokens; shows no transcript or raw receipts. Returns the first 100 hazards and the total |
| `--recover HANDLE` | List, as JSON, the session's calls whose outcome is unknown. See [Interrupted work](/sessions#interrupted-work) |
| `--resolve EXECUTION=OUTCOME` | With `--recover`: `OUTCOME` is `applied`, `not-applied` or `undone`. Shows what would be recorded (a dry run); nothing runs again |
| `--note TEXT` | With `--resolve`: a note of at most 1000 characters kept with the record |
| `--apply` | With `--resolve`: record it beside the session's ledger, which is never rewritten |
| `--mcp-status` | Checks your own `mcp.json`, or the `--mcp-config` file. Connects and lists tools per server as JSON; exits 1 if any server is unavailable |
| `--trust-rules` | Lists the workspace's untrusted allow rules and trusts them after you type `yes` at a terminal; exits 1 without a terminal or a `yes` |

## Cross-flag rules

- At most one session operation may be given per run (`--sessions`,
  `--session-info`, `--recovery-info`, `--recover`, `--resume`).
- `--resolve`, `--note` and `--apply` go with `--recover`; `--note` and
  `--apply` need `--resolve`.
- Session operations require `--session-db`.
- `--ui tuimark` cannot be combined with headless mode.
- Headless requires a non-empty prompt.
- `--approve-edits`, `--approve-commands`, and `--approve-mcp` are headless-only.
- `--sandbox-commands` needs headless mode or the Tuimark client, and cannot
  be combined with `--approve-commands`. Headless, it stops the run before it
  starts where the sandbox is unavailable (Linux, Windows, a workspace that
  contains your home folder).
- `--sandbox-loopback` needs `--sandbox-commands`.
- `--mode fullscreen` and `--mode inline` cannot be combined with `--prompt`
  or `--json`, which run headless.
- Interactive modes require a TTY.
- Interactive runs check the Ollama model, for at most 15 s, before its first
  turn. The Tuimark client opens at once and checks while it is up: the status
  says `Checking model…`, a prompt sent meanwhile waits for the check, and a
  model that is refused closes the client with the same message. The Vue
  client checks before it opens.
- A run stopped by `ctrl+c` (SIGINT), SIGTERM, or a closed terminal (SIGHUP)
  exits 130, 143 or 129; any other failure exits 1.

## Run modes

- **fullscreen** (default): the tuimark client takes over the terminal. The vue
  client can also run fullscreen via `--ui vue`.
- **inline**: vue by default. Tuimark runs inline only with
  `--ui tuimark --mode inline`; finished blocks print into the terminal's own
  scrollback.
- **headless**: selected by `--mode headless`, `--prompt`, or `--json`.

The tuimark binary is taken from `TUIMARK_BINARY` if set; otherwise a pinned
release (0.5.4) is downloaded and checksum-verified. With no `--ui`, the run
falls back to vue with a note on stderr that says why when the binary is
missing, or when `TUIMARK_BINARY` names one older than 0.5.1 or one whose
`--version` names no version (it is asked for at most 5 seconds; the pinned
release is not asked). `--theme` is then refused. An explicit `--ui tuimark`
fails instead.

### Headless output

Text streams to stdout; tool_result statuses go to stderr, as does a note when
the history is compacted or a request leaves its oldest messages out. With
`--json`, stdout carries NDJSON events, starting with an initial
`{"type":"session","id":...}` event; a compaction is a `compacted` event
carrying the shortened history, and a windowed request is a `windowed` event
carrying counts only (`left`, `sent`). A run emits it at its first windowed
request and again whenever the window's start moves.

Approval in headless is fail-closed by default. Without `--approve-edits`,
`--approve-commands`, or `--approve-mcp`, the matching request class is denied:
there is no approval dialog to answer. A call that matches an ask rule in
`.local-agent/permissions.json` is refused whatever the flags say, and stderr
names the rule. On macOS, `--sandbox-commands` runs commands confined
instead. See [Safety](/safety#headless-approval).

## Slash commands

| Command | Purpose |
|---|---|
| `/mode plan\|build\|chat` | Switch intent. `plan` reads only; `build` can edit; `chat` is the conversational research profile |
| `/theme dark\|light\|auto` | Set the session theme |
| `/mouse on\|off` | Toggle mouse reporting; fullscreen only. OSC 8 links: ⌘/Ctrl+click may require `/mouse off` (the terminal app owns the mouse while reporting is on) |
| `/permissions` | Show session grants and the workspace file rules, each with its origin; allow rules you have not trusted are marked `(untrusted)` |
| `/permissions clear` | Clear session approval grants |
| `/permissions clear rules` | Empty the rules in `.local-agent/permissions.json` and forget their trust |
| `/permissions trust` | List the allow rules in the workspace's `.local-agent/permissions.json` that you have not trusted on this machine |
| `/permissions trust confirm` | Trust exactly the rules the last `/permissions trust` listed, if the file still holds them |
| `/sandbox on\|loopback\|off` | Tuimark client, macOS: run commands in the sandbox without a card (`loopback` also opens TCP to this machine, see [Loopback](/safety#loopback-opt-in)) (an ask rule still shows one, and its `y` runs that command outside; so does a command that names a secret path). `/sandbox` alone says whether it is on. Where it is unavailable it stays off and says why |
| `/details [N]` | Tuimark client: show tool output N in full (the number a cut card names), or the last one. Outputs are kept up to 2,000,000 characters and 500 outputs in all, each capped at 64,000 characters; one let go, or from an earlier conversation, says so |
| `/copy` | Tuimark client: put the last reply's markdown on the clipboard (also `ctrl+y`). During a turn it copies the last finished reply. With no clipboard tool, the status says how to select text by hand |
| `/copy code` | Tuimark client: copy the last code block of the last reply |
| `/status` | Show the effective config panel (model, workspace, session) |
| `/provider` | Ask the model's endpoint, read-only: answer time, Ollama's version, whether the model is loaded and how much of it is in GPU memory |
| `/sessions` | Open the session picker; needs `--session-db`. Loading a session closes the calls it stopped before they ran, or that only read, and tells the model; a call of unknown outcome loads but blocks the next prompt until `/recover` |
| `/sessions new` | Start over: a new saved session with `--session-db`, otherwise a new temporary conversation |
| `/new`, `/clear` | Tuimark client: the same as `/sessions new` |
| `/diff` | Tuimark client: list the files this session's edits and writes changed, with their diffs and `+N −M` counts. Changes made by commands are not listed |
| `/compact` | Tuimark client: shorten the conversation now, the way the automatic compaction does past 6 MiB, as far as it can go; refused while a turn runs |
| `/recover` | Tuimark client, needs `--session-db`: list the session's calls whose outcome is unknown, numbered |
| `/recover N applied\|not-applied\|undone [note]` | Tuimark client: record what you found in the workspace for the Nth call. Nothing runs again. Earlier calls are answered first. While one remains, a prompt waits and the status names `/recover` |
| `/model [NAME]` | Show the current model, or switch to another installed Ollama model mid-session |
| `/context [SIZE\|auto]` | Tuimark client, local Ollama: show the context size and how it was chosen; set one (`64k`), checked and costed as `--context` is; or plan it again (`auto`). Ollama reloads the model at the new size on the next turn and reads the conversation again. Refused while a turn or the startup model check runs, and under Chalupa or an OpenAI-compatible server |
| `/goal OBJECTIVE [--turns N]` | Tuimark client, build mode: run turns toward an objective until the model reports done or blocked, or a limit stops it (8 turns by default, 1 to 50) |
| `/goal` | Show the running goal: its objective, turn, time and tokens left, and last status |
| `/goal stop` | End the running goal; `esc` ends it too, and so does `ctrl+c` between its turns |
| `/export` | Write the transcript to a markdown file in the workspace root; it may hold tool output, so keep it out of commits |
| `/export json` | Write the raw messages array as JSON |
| `/find TEXT` | Search saved sessions for text; needs `--session-db` |
| `/search [TEXT]` | Tuimark client: search this conversation: your messages, the replies and the tool cards, not the client's own notices. Fullscreen opens a search box (also `ctrl+f`); inline prints the matches into the scrollback. Refused while a turn runs |
| `/mcp` | Show an MCP panel: server and tool counts, or a hint to use `--mcp-config` |
| `/help` | Show the command and key overlay |

An unknown command answers `Unknown command: X. Use /help`; it is never sent
to the model. In the Tuimark client names match in any case, and a line
whose first word holds a second slash, such as `/tmp/x.log is empty`, is a
message, not a command. While a turn is busy, commands report a busy status
instead of running; in the Tuimark client `/copy` and `/goal` still run.

## Keymap

In the Tuimark client the hint row names only the keys that do something
at that moment: `esc cancel` while a turn runs, and between a `/goal`'s
turns or before a queued message; `^O earlier` (fullscreen only) only when
earlier messages are hidden.

| Key | Action |
|---|---|
| `ctrl+p` | Open the command palette |
| `esc` | Cancel the current turn, and a running `/goal` with it; a queued message comes back to the composer |
| `ctrl+c` | Tuimark client: clears the draft first, then cancels a running turn, or stops a `/goal` between its turns; the next press quits. Vue client: quits at once |
| `up`, `down` | Browse input history; `up` in an empty composer takes a queued message back to edit (Tuimark client) |
| `ctrl+o` | Load earlier messages |
| `pgup`, `pgdn` | Scroll the log |
| `ctrl+l` | Jump to the latest output |
| `ctrl+y` | Tuimark client: copy the last reply, as `/copy` |
| `ctrl+f` | Tuimark client, fullscreen: search this conversation, as `/search` |
| `shift+tab` | Next intent: `build` → `plan` → `chat` → `build` |
| `enter` | Send the draft; while a turn runs, queue it for when the turn ends (Tuimark client) |
| `shift+enter`, `alt+enter` | Insert a newline without sending |

Context-sensitive keys:

| Context | Keys |
|---|---|
| Slash popup | `up`/`down` choose, `tab` completes, `esc` closes |
| Approval dialog (Tuimark) | `y` allows once; `a` grants for the session (not for commands while the sandbox is on, a command that names a secret path, or a preview too long to show whole); `!` saves an always rule (not for those last two); `n` or `esc` denies; arrows, `pgup`/`pgdn`, `home`/`end` scroll the diff |
| Approval (Vue client) | `y` allows; `n`, `enter` or `esc` denies |
| Question (Tuimark) | `up`/`down` choose, `enter` answers; or type the row's number, its label, or `y`/`n`; `esc` skips |
| Panels and details | `q` or `esc` closes; arrows scroll |
| Search box (Tuimark) | Type to search, in any case; `up`/`down` (or `ctrl+n`/`ctrl+p`) choose a match; `enter` opens it in the viewer, where `n`/`p` step through the matches and `esc` goes back to the box; `esc` closes |
| Sessions picker | `up`/`down` choose, `enter` loads |

The command palette mirrors the slash commands plus `ctrl+o` load-earlier
(fullscreen) and `ctrl+c` quit.
