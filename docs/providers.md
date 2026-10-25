---
title: Providers
description: Three inference providers — local Ollama by default, remote Chalupa and local OpenAI-compatible servers opt-in through the environment. No YAML profiles.
outline: deep
---

# Providers

The current client has three providers: local Ollama (the default), Chalupa
(opt-in) and a local OpenAI-compatible server (opt-in, below). Selection is
environment-only — there are no YAML provider profiles. See
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

Without `--session-db` the conversation lives only in the process and ends
with the launch. Arguments after `--` reach the client, so
`chalupa launch local-agent -- --session-db ~/.local-agent/sessions.db`
keeps it in a database on your machine that `--resume` continues later (see
[Sessions](/sessions)).

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
bearer token. It travels as a bearer header on every request, so a profile
with an `api_key` and a plain `http` `baseURL` to a host that is not loopback
is refused at startup: use an `https` URL or drop the key. Plain `http`
without a key (a server on your LAN) and `http` to loopback stay valid. The model is selected in the profile; a differing `--model` is
refused, while `--context` behaves as with local Ollama (without it the size
stays 16384; only local Ollama plans one). On startup the
client checks the model against `GET {baseURL}/models`; servers that do not
implement the endpoint (404) skip the check with a status note.

## Where the prompt goes

The Tuimark client's top bar says whether prompts leave this machine. Wide
terminals show the provider: `Ollama / loopback`, `Chalupa / remote`, or
`OpenAI-compatible / local` (or `/ remote`). Below 111 columns, `LOCAL` or
`REMOTE` takes its place, `REMOTE` in the accent color, and below 61 columns
the model name gives way to it. Chalupa is remote even though its URL is on
loopback: that URL is a tunnel. A server on loopback is local, and a server
on any other host, a LAN machine included, is remote.

## Retries

A request to the model, and the metadata reads at startup (Ollama's
`/api/tags` and `/api/show`, an OpenAI-compatible server's `/models`), is
sent again when the connection is refused or reset, or the server answers 429,
502, 503 or 504. It is tried at most twice more, after 250 ms and then 500 ms,
or after the server's `Retry-After` when it gives one. A `Retry-After` longer
than 10 seconds is not waited for: that answer stands. A timeout or a cancel
(`esc`) is final, and a reply that has started streaming is never replayed. The
`/provider` check and the background `/api/ps` look make one attempt.

Streamed replies from OpenAI-compatible servers are read whether their lines
end in LF, CRLF or CR.

## Not in the current client

- No provider configuration file. Profiles are not written in YAML; there are
  no provider settings beyond the flags and variables above.
- No cloud OpenAI endpoints. `LOCAL_AGENT_PROVIDER` accepts `ollama`,
  `chalupa`, and `openai_local` (loopback by default); anything else fails at
  startup.
