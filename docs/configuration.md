---
title: Configuration
description: Configure the Local Agent Bun CLI with flags and environment variables only.
outline: deep
---

# Configuration

There is no configuration file. Configuration is command-line flags plus
environment variables — nothing else. Every setting is either a flag documented
in the [Command reference](/reference) or a variable in the table below.

## Environment variables

| Variable | Purpose |
|---|---|
| `LOCAL_AGENT_MODEL` | Default model; `--model` overrides it |
| `LOCAL_AGENT_THINK` | `1` or `0`; same as `--think on\|off`. Unset means the model default |
| `LOCAL_AGENT_CONTEXT` | Default context tokens; same as `--context`. `32768` and `32k` both work |
| `LOCAL_AGENT_PROVIDER` | Provider selection: `ollama` or `chalupa` only |
| `LOCAL_AGENT_LOCAL_ONLY` | Local-endpoint enforcement; must be `false` for `chalupa` |
| `LOCAL_AGENT_PROVIDER_CONFIG` | Chalupa profile JSON |
| `TUIMARK_BINARY` | Path to the tuimark binary; otherwise a pinned release (0.4.6) is downloaded checksum-verified |
| `LOCAL_AGENT_SEARXNG_URL` | SearXNG instance for the `web_search` tool; default `http://127.0.0.1:8888` |
| `OLLAMA_HOST` | Ollama endpoint; default `127.0.0.1:11434` |

## Model and context

`--model` selects the Ollama model. The default is `qwen3.5:4b-q4_K_M` — the
smallest model that passed the agent battery reliably — unless
`LOCAL_AGENT_MODEL` is set; the flag overrides the environment.

`--think on|off` forces thinking on or off. Unset means whatever the model
defaults to.

`--context N` sets context tokens for the local Ollama provider. The default is
16384 (`LOCAL_AGENT_CONTEXT` is equivalent). The value cannot exceed the model's
native context. Chalupa negotiates its own context; the flag does not apply.

Interactive runs reject a missing model or an oversized `--context` up front
with a 15 s admission check, before the session starts.

## Providers

Two providers exist: local Ollama (the default) and Chalupa. Select Chalupa
with `LOCAL_AGENT_PROVIDER=chalupa`; `LOCAL_AGENT_LOCAL_ONLY` must then be
`false`. Chalupa profiles come from the environment supplied by
`chalupa exec` and are passed via `LOCAL_AGENT_PROVIDER_CONFIG` as profile JSON.
The profile keeps its own model and context; Chalupa negotiates context itself.
See [Providers](/providers).

## Themes

`--theme` accepts `dark`, `light`, `auto`, or a named theme. Two named themes
are bundled: `midnight` and `sepia`. Themes apply to the tuimark client only.

`/theme dark|light|auto` changes the theme mid-session without restarting.
Themes are presentation only; they do not affect authority, tool policy, or
approvals. See [Command reference](/reference).

## Repository instructions

At startup, the root `AGENTS.md` of the workspace is loaded as model context.
It is not a security boundary: mode policy, tool admission, and approval checks
remain host-owned. Full skills/profile parity with the historical Go
implementation is pending.
