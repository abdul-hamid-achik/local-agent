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

## OpenAI-compatible local servers (opt-in)

`LOCAL_AGENT_PROVIDER=openai_local` targets a local OpenAI-compatible server
(llama.cpp, LM Studio, vLLM) over `/v1/chat/completions` with SSE streaming.
The profile arrives via `LOCAL_AGENT_PROVIDER_CONFIG`:

```json
{
  "type": "openai_local",
  "baseURL": "http://127.0.0.1:8080/v1",
  "model": "qwen3-4b",
  "context_size": 32768,
  "api_key": "optional-dummy-token"
}
```

`baseURL` must be loopback (`127.0.0.1`, `localhost`, `::1`) unless
`LOCAL_AGENT_LOCAL_ONLY=false` is set explicitly, the same boundary MCP
endpoints use. `api_key` is optional: some local servers require a dummy
bearer token. The model is selected in the profile; a differing `--model` is
refused, while `--context` behaves as with local Ollama (without it the size
stays 16384; only local Ollama plans one). On startup the
client checks the model against `GET {baseURL}/models`; servers that do not
implement the endpoint (404) skip the check with a status note.

## Not in the current client

- No provider configuration file. Profiles are not written in YAML; there are
  no provider settings beyond the flags and variables above.
- No cloud OpenAI endpoints. `LOCAL_AGENT_PROVIDER` accepts `ollama`,
  `chalupa`, and `openai_local` (loopback by default); anything else fails at
  startup.
