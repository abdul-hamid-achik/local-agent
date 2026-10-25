---
title: Getting started
description: Install local-agent on macOS or Linux, connect Ollama, and complete a first approval-gated session.
outline: deep
---

# Getting started

Local Agent is a local coding agent for the terminal. The default model runs
on [Ollama](https://ollama.com/) on the same machine.

## Prerequisites

- macOS or Linux, on arm64 or x86-64
- Ollama running locally with the default model `qwen3.5:4b-q4_K_M` pulled
  (`ollama pull qwen3.5:4b-q4_K_M`)

Interactive runs pre-admit the model with a 15-second check; a missing model
is rejected up front, not mid-session.

## Install

The installer downloads the release for your platform, checks it against the
release's `SHA256SUMS.txt`, unpacks it under `~/.local/share/local-agent/<tag>` and
links `~/.local/bin/local-agent`. It needs no root and changes nothing else;
if `~/.local/bin/local-agent` is already something it did not install, it
stops before downloading:

```bash
curl -fsSL https://local-agent.dev/install.sh | sh
```

Read [the script](https://local-agent.dev/install.sh) first if you prefer.
`LOCAL_AGENT_VERSION=v0.52.0` pins a release; the checksum catches a corrupt
or truncated download, and is not a signature. Or with Homebrew:

```bash
brew install --cask abdul-hamid-achik/tap/local-agent
```

Then check it:

```bash
local-agent --version
```

The release is one executable next to a `ui/` folder, with the LICENSE and
README. The Tuimark host it drives is downloaded on first run (stderr says
`Downloading Tuimark 0.5.2…` meanwhile) and checked against a pinned
checksum; if that fails and `--ui` was not given, it starts the Vue client
instead and says so.
Every [release](https://github.com/abdul-hamid-achik/local-agent/releases)
carries the four archives, a source tarball and their checksums.

### From source

Building from a checkout needs [Bun](https://bun.sh/) 1.4.2:

```bash
git clone https://github.com/abdul-hamid-achik/local-agent.git
cd local-agent
bun install --frozen-lockfile
bun run dev
```

`bun run dev` takes the same flags as `local-agent`, run from the repository
root. `bun link` in `apps/cli` puts a `local-agent` command on your PATH that
runs the checkout.

## First session

Start it in the repository you want the agent to inspect or change — the
workspace is the current directory unless you pass `--workspace`:

```bash
cd /path/to/your/repository
local-agent
```

The fullscreen client takes over the terminal and shows a welcome screen.

Type a prompt into the composer at the bottom and press `enter` to send.
`shift+enter` or `alt+enter` inserts a newline. A message holds up to
1,000,000 characters; a longer one stays in the composer, and the status line
says `Not sent` with its length. Send a large text in parts, or save it to a
file in the workspace and ask the model to read it.

Start read-only:

```text
Read README.md and summarize the setup steps.
```

The session starts in **build** intent. When the model proposes a file edit,
write, or command, an approval card appears. In the Tuimark client, the
default, decide with:

| Key | Decision |
|---|---|
| `y` | Approve once |
| `a` | Approve this and matching calls for the rest of the session |
| `!` | Always: save a rule for exactly this call to `.local-agent/permissions.json` |
| `n` or `esc` | Deny; the turn goes on, and the model is told the call was not approved |

The status line names only the keys the card takes: `a` and `!` are left
out where they would not act (see
[Interactive approval](/safety#interactive-approval)). The Vue client takes
`y`, and `n`, `enter` or `esc` to deny.
For `write` and `edit` the card shows a diff, for `run_command` the exact
command; the arrow keys, `pgup`/`pgdn` and `home`/`end` scroll it. Commands
run exactly as written with your permissions — there is no shell.

Every tool call leaves a card in the transcript. The card names the tool
(Read, Edit, Command, or `MCP · server · tool` for an MCP call), marks how
it ended (`✓` succeeded, or applied for an edit or a write, `✗` denied or
failed, `!` when the outcome is unknown or unverified, as for an MCP call's
result), and says how long it ran. A successful edit shows the lines it
changed, a newly created file shows its lines. A card that cuts its output
names it (`… 12 more lines · /details 3 for full output`): `/details 3`
opens that output in full, and `/details` alone the last one.

While a turn runs, the reply being written has a head that says what the
turn is doing and for how long: `Assistant ⠋ thinking · 12s`. The head reads
"waiting for the model" (or "connecting MCP servers") or "thinking", and
only the elapsed time once the reply streams. While an approval or a
question waits on you, the head stands still, and that wait is not counted.
The status bar keeps the same facts at rest and during a turn: the mode,
the MCP servers, and after a turn how long it took and how fast it went
(`last 1m 06s · 32 tok/s`).

In the Tuimark client you can keep typing while a turn runs: `enter` queues
the message, and it is sent when the turn ends. One message waits at a time (a further send is
added to it), and the status bar says `1 queued`. `up` in an empty composer
takes it back to edit while the turn goes on; `esc` cancels the turn and
gives it back to the composer, after anything typed since. `/help` lists the
commands and the keys, grouped by where they work.

`/copy`, or `ctrl+y`, puts the last reply on the clipboard; `/copy code`
copies its last code block. It uses `pbcopy` on macOS, or `wl-copy`,
`xclip` or `xsel` on Linux. With none of them installed, the status says how
to select text in your terminal: with mouse reporting on, that is
shift-drag (option-drag in iTerm2, fn-drag in Terminal.app).

The model can also stop and ask you a question. It opens above the
composer: a list of choices, yes/no, or a free-text answer. Choose with the
arrow keys and `enter`, or type the answer; `esc` skips it. What you were
typing comes back when the question closes, and an answer never approves
an edit or a command: those still show their card.

## Switch intent

Press `shift+tab` to move through **build**, **plan**, and **chat**. Plan
offers only read-only tools (read files and images, list, search, fetch, web
search, read-only subagents, and the todo list); it hard-denies writes, edits,
commands, and MCP calls. Chat keeps the research tools and read-only
subagents, without the todo list or the coding preamble; it refuses edits and
commands, and asks before each MCP call. Build can propose changes, each gated
by an approval. See [Modes](/modes).

## Try other surfaces

Tuimark inline mode prints finished blocks into your terminal's own
scrollback:

```bash
local-agent --ui tuimark --mode inline
```

Named themes work with the tuimark client:

```bash
local-agent --theme midnight
local-agent --theme catppuccin-mocha
local-agent --theme rose-pine-dawn
```

[Configuration](/configuration#themes) lists all eighteen.

## Where to go next

- [Modes](/modes) — build, plan, and chat intents and how approval differs
- [Reference](/reference) — flags, slash commands, and keymap
- [Configuration](/configuration) — model, context, providers, and environment
