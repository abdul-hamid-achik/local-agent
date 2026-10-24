---
title: Ollama models
description: Choose the Ollama model, context, and thinking mode at launch. The default is the smallest model that passed the agent battery.
outline: deep
---

# Ollama models

There is no model picker in the current client: no inventory screen, no
Ollama Cloud, no automatic routing, no pins. The model is chosen before launch
and holds for the run; no slash command switches it mid-session.

## The default

The default model is `qwen3.5:4b-q4_K_M` — the smallest model that passed the
agent battery reliably. You set nothing to use it.

## Selecting a model

```bash
bun run dev --model qwen3.5:9b
```

Or set `LOCAL_AGENT_MODEL`; the flag overrides the environment. A missing
model is not a silent fallback: interactive runs pre-admit the model with a
15-second check and reject it up front, before the session starts. See
[Command reference](/reference).

## Context

`--context N` — or `LOCAL_AGENT_CONTEXT` — sets the context tokens requested
from local Ollama; `32768` and `32k` both parse. The value cannot exceed the
model's native context, and an oversized value is rejected up front by the
same admission check.

Without either, the size is planned at startup. Local Agent reads the model's
metadata from Ollama (`/api/show`), works out what its key/value cache costs
per token, and picks the largest of 32768, 24576, 16384, 12288 and 8192
tokens that fits in about 11/16 of the machine's memory once the weights and
2.5 GiB of headroom are set aside, never above the model's native context.
`qwen3.5:4b`, whose cache lives in one layer of four, gets 32768 on a 16 GB
machine. A model that does not report the figures keeps 16384, as does a run
that cannot reach Ollama. In the Tuimark client, `/status` shows how the size
was chosen, and `/model` plans again for the new model. The planner stops at 32768 because
refilling a larger window takes long on small models; pass `--context` for
more.

`--context` applies to the local Ollama provider only; Chalupa negotiates its
own. See [Providers](/providers).

### Long turns

A tool result holds at most about a quarter of the window, and the results of
one model reply share about 40% of it. A file too long for that comes back in
part, ending with the `offset` to read next; the model can also ask `read`
for a line range (`offset`, `limit`).

In the Tuimark client and headless runs, a turn whose prompt would pass 70%
of the window is compacted first: the oldest material the model has already
acted on is shortened until the prompt is near 45%. Images go first, then
tool results (each keeps its first lines and a note of what went), then the
reasoning of earlier steps of the turn. The user's latest message, the
latest tool calls and their results are never shortened, and the model can
run a tool again to get a result back. The session keeps the shortened
history, and the transcript shows a "Context compacted" note. The Vue client
and the desktop app do not compact.

When the history still does not fit, every client sends only its newest whole
exchanges: a request starts at a user message and fits under 70% of the
window with the system prompt and the tools. The current turn always goes
whole, so a turn whose own results pass the window is still sent as it is.
Nothing a request leaves out is shortened. The model is told how many of
the oldest messages it cannot see, and the session keeps all of them. The
Tuimark transcript says "Context window full" when more are left out.

In the Tuimark client, `/provider` shows how the server loaded the model:
its size, the share in GPU memory and the context it was loaded with. Once a
model has answered, the client checks once in the background, and warns
after the turn when part of the model runs on the CPU or it was loaded with
less context than asked.

## Thinking

`--think on|off` — or `LOCAL_AGENT_THINK=1|0` — forces the model's thinking
mode on or off. Unset means whatever the model defaults to.

## General Ollama guidance

Only the default model is verified in this harness. What follows is general
Ollama guidance about model sizes, not a tested claim about this client:

| Hardware | Class of model | Example |
|---|---|---|
| 8 GB RAM | Small chat and classification models | `qwen3.5:0.8b`, `qwen3.5:2b` |
| 16 GB RAM | Compact tool-use models | `qwen3.5:4b` (the default's family) |
| 32 GB+ RAM | Larger reasoning models | `qwen3.5:9b` |

Pull a model first (`ollama pull NAME`); a model that is not installed fails
the admission check at startup rather than mid-session. Larger models and
larger contexts need more memory — budget beyond the model weights before
raising `--context`.
