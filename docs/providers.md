---
title: Providers
description: Two inference providers — local Ollama by default, remote Chalupa opt-in through the environment. No profiles, no OpenAI-compatible endpoints.
outline: deep
---

# Providers

The current client has exactly two providers: local Ollama (the default) and
Chalupa (opt-in). Selection is environment-only — there are no YAML provider
profiles and no OpenAI-compatible endpoints. See
[Configuration](/configuration).

## Local Ollama (default)

The default provider talks to Ollama on the local machine at `OLLAMA_HOST`,
default `127.0.0.1:11434`. Model, context, and thinking are set at launch with
`--model`, `--context`, and `--think` — or `LOCAL_AGENT_MODEL`,
`LOCAL_AGENT_CONTEXT`, `LOCAL_AGENT_THINK`. See
[Ollama models](/ollama-models).

## Chalupa (opt-in remote)

Chalupa places the model on remote hardware and exports a profile to the
environment; `chalupa exec` supplies it. The client reads three variables:

| Variable | Role |
|---|---|
| `LOCAL_AGENT_PROVIDER=chalupa` | Select the Chalupa provider |
| `LOCAL_AGENT_LOCAL_ONLY=false` | Leave local-only explicitly; the client refuses to start Chalupa without it |
| `LOCAL_AGENT_PROVIDER_CONFIG` | The profile JSON supplied by `chalupa exec` |

Chalupa keeps the model and context of its profile. Select them on the
Chalupa side: the client rejects a `--model` that differs from the profile,
and rejects `--context` outright — Chalupa negotiates the context itself.
`--think` still applies.

Once Chalupa is active, prompts and tool receipts go to the remote endpoint.
That is exactly what the explicit `LOCAL_AGENT_LOCAL_ONLY=false`
acknowledges. See [Safety and privacy](/safety).

## Not in the current client

- No provider configuration file. Profiles are not written in YAML; there are
  no provider settings beyond the flags and variables above.
- No OpenAI-compatible endpoints. `LOCAL_AGENT_PROVIDER` accepts `ollama` or
  `chalupa` only; anything else fails at startup.
