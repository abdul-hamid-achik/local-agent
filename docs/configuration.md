---
title: Configuration
description: Configure the Local Agent Bun CLI with flags and environment variables, plus the MCP, permissions and AGENTS.md files it reads.
outline: deep
---

<script setup>
import ThemeGallery from './.vitepress/theme/ThemeGallery.vue'
</script>

# Configuration

There is no settings file: every setting is either a flag documented in the
[Command reference](/reference) or a variable in the table below. Four files
are read when they exist, each for one purpose:
`~/.config/local-agent/mcp.json` lists your MCP servers ([MCP](/mcp)), the
workspace's `.local-agent/permissions.json` holds its approval rules
([Persistent rules](/safety#persistent-rules)), its root `.agentignore` keeps
paths away from the file tools ([Ignored paths](#ignored-paths)), and its root
`AGENTS.md` is model context ([Repository instructions](#repository-instructions)).

## Environment variables

| Variable | Purpose |
|---|---|
| `LOCAL_AGENT_MODEL` | Default model; `--model` overrides it |
| `LOCAL_AGENT_THINK` | `1` or `0`; same as `--think on\|off`. Unset means the model default |
| `LOCAL_AGENT_CONTEXT` | Default context tokens; same as `--context`. `32768` and `32k` both work |
| `LOCAL_AGENT_PROVIDER` | Provider selection: `ollama` (the default), `chalupa`, or `openai_local`; see [Providers](/providers) |
| `LOCAL_AGENT_LOCAL_ONLY` | Local-endpoint enforcement; must be `false` for `chalupa`, and for an `openai_local` server that is not on loopback |
| `LOCAL_AGENT_PROVIDER_CONFIG` | The `chalupa` or `openai_local` profile JSON |
| `TUIMARK_BINARY` | Path to the tuimark binary; otherwise a pinned release (0.5.1) is downloaded checksum-verified |
| `TUIMARK_MOTION` | `reduce` or `0`: the Tuimark client draws a still glyph where its spinners would turn; the turn's elapsed time keeps counting |
| `LOCAL_AGENT_DIAGNOSTICS` | `1` appends a Biome check of each approved edit, using the workspace's own `@biomejs/biome` install and its root `biome.json` (default off) |
| `LOCAL_AGENT_DIAGNOSTICS_TSC` | `1` also appends a `tsc --noEmit` section after `.ts` edits, using the workspace's own `typescript` and its root `tsconfig.json` (slow: full project) |
| `LOCAL_AGENT_FETCH_PRIVATE` | `1` lets the `fetch` tool reach loopback and private-network hosts; by default it reaches public hosts only |
| `XDG_CONFIG_HOME` | Where `local-agent/trusted-rules.json` (the permission-rule trust store) lives; default `~/.config`. A relative value is ignored |
| `LOCAL_AGENT_SEARXNG_URL` | Optional SearXNG instance for the `web_search` tool, asked first. When it is unreachable or answers with no results, DuckDuckGo (Lite, then HTML) is asked; an empty answer from both reads `No results`. Without it, DuckDuckGo answers |
| `OLLAMA_HOST` | Ollama endpoint; default `127.0.0.1:11434` |

## Model and context

`--model` selects the Ollama model. The default is `qwen3.5:4b-q4_K_M` — the
smallest model that passed the agent battery reliably — unless
`LOCAL_AGENT_MODEL` is set; the flag overrides the environment.

`--think on|off` forces thinking on or off. Unset means whatever the model
defaults to.

`--context N` sets context tokens for the local Ollama provider
(`LOCAL_AGENT_CONTEXT` is equivalent). The value cannot exceed the model's
native context. Without either, the size is planned from the model and this
machine's memory, at most 32768; see [Ollama models](/ollama-models#context).
Chalupa negotiates its own context; the flag does not apply.

Interactive runs reject a missing model or an oversized `--context` up front
with a 15 s admission check, before the session starts.

## Providers

Three providers exist: local Ollama (the default), Chalupa, and a local
OpenAI-compatible server (`LOCAL_AGENT_PROVIDER=openai_local`). Select Chalupa
with `LOCAL_AGENT_PROVIDER=chalupa`; `LOCAL_AGENT_LOCAL_ONLY` must then be
`false`. Chalupa profiles come from the environment supplied by
`chalupa exec` and are passed via `LOCAL_AGENT_PROVIDER_CONFIG` as profile JSON.
The profile keeps its own model and context; Chalupa negotiates context itself.
See [Providers](/providers).

## Themes

`--theme` accepts `dark`, `light`, `auto`, or a named theme. Themes apply to
the tuimark client only. `auto` picks `dark` or `light` from the terminal's
background. Here is the same screen of the released client in `dark`,
`light` and every named theme:

<ThemeGallery />

Eighteen named themes are bundled:

| Family | Themes |
| --- | --- |
| local-agent | `midnight`, `sepia` |
| Catppuccin | `catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte` |
| Rosé Pine | `rose-pine`, `rose-pine-moon`, `rose-pine-dawn` |
| Tokyo Night | `tokyo-night`, `tokyo-night-storm` |
| Gruvbox | `gruvbox-dark`, `gruvbox-light` |
| Others | `nord`, `dracula`, `one-dark`, `kanagawa`, `everforest` |

An unknown name lists every theme. In every theme, each text color reads at
WCAG AA (at least 4.5:1) on the background, panels, raised surfaces and
selected rows, and so does the label on a badge, a button or a segment. Where
a palette's original color falls short of that, the bundled theme lightens or
darkens it along its own hue, so an adapted theme is close to its source but
not always identical.

`/theme dark|light|auto` changes the theme mid-session without restarting; a
named theme is chosen at launch with `--theme`.
Themes are presentation only; they do not affect authority, tool policy, or
approvals. See [Command reference](/reference).

## Ignored paths

A `.agentignore` file in the workspace root keeps paths away from the agent's
file tools. It takes one pattern per line, and `#` starts a comment. `*`, `?`,
`[abc]` and `**` are wildcards. A pattern without a `/` matches a name at any
depth (`*.log`, `node_modules`); one with a `/` is relative to the root
(`src/generated`). Ignoring a directory ignores everything in it, and a
leading `!` brings a path back; the last matching line wins. Names match in
any case.

Ignored paths are hidden from list and search, and read, edit and write
refuse them. The file is read once, when the run starts. A `.agentignore`
that is a symlink, not a regular file, over 64 KiB or not UTF-8 stops the
run; a malformed line is named once on stderr and skipped. The file itself
is hidden from the tools, so the model cannot loosen it. It does not limit
`run_command`: commands still ask for approval, or run in the
[sandbox](/safety#sandboxed-commands-macos).

## Repository instructions

At startup, the root `AGENTS.md` of the workspace is loaded as model context,
at most 64 KiB. The model receives it framed as guidance from whoever controls
the workspace, which cannot grant permissions, change approval rules, or
override the host's tool and safety rules; a line inside the file that imitates
the frame's closing marker does not end it early. It is not a security
boundary: mode policy, tool admission, and approval checks remain host-owned
and apply whatever the file says. Full skills/profile parity with the historical Go
implementation is pending.
