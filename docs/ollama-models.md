---
title: Ollama models
description: Choose the Ollama model, context, and thinking mode at launch. The default is the smallest model that passed the agent battery.
outline: deep
---

# Ollama models

There is no model picker in the current client: no inventory screen, no
Ollama Cloud, no automatic routing, no pins. The model is chosen before launch
(`--model` or `LOCAL_AGENT_MODEL`). In the Tuimark client, `/model NAME`
switches to another installed model between turns, after the same admission
check as at launch, and `/model` alone names the current one. See
[Command reference](/reference).

## The default

The default model is `qwen3.5:4b-q4_K_M` — the smallest model that passed the
agent battery reliably. You set nothing to use it.

## Selecting a model

```bash
local-agent --model qwen3.5:9b
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
machine. In a model whose metadata marks sliding-window layers (Gemma 4, for
one), those layers hold only their window, a fixed cost, and layers that share
another layer's cache cost nothing. Of the 42 layers of
`nemotron-3.5-bolt:e4b-q4_K_M`, 18 share a cache and 20 hold a window, so only
4 grow with the context: it also gets 32768, next to its 7.9 GB of weights.
A model that does not report the figures keeps 16384, as does a run that
cannot reach Ollama. In the Tuimark client, `/status` shows how the size
was chosen, and `/model` plans again for the new model. The planner stops at
32768 because refilling a larger window takes long on small models; pass
`--context` for more.

In the Tuimark client, `/context SIZE` (`64k`, say) changes the size
mid-session. It is checked against the model and costed like `--context`,
and Ollama reloads the model at that size on the next turn, which then reads
the conversation again from its start. `/context auto` plans it again, and
`/context` alone shows the size and how it was chosen. On a 16 GB machine,
`qwen3.5:4b-q4_K_M` at 65536 tokens takes 5.5 GB, all of it in GPU memory,
and a 9,500-token conversation took about 10 s to read again after the
reload.

`--context` applies to the local Ollama provider only; Chalupa negotiates its
own. See [Providers](/providers).

### Models on Ollama's MLX runner

On Apple silicon, Ollama can serve a model on its MLX runner instead of
llama.cpp. Local Agent reads the runner from the model's metadata (`/api/show`
and `/api/ps`, `runner: mlx`); for a model that names none, safetensors
weights count as MLX and GGUF does not. On MLX, Ollama ignores the requested
context size: nothing is reserved when the model loads, and its cache grows
with the conversation. The planned or chosen size then only bounds what Local
Agent sends in one request, and the run starts with a warning that names the
way out: a GGUF tag such as `qwen3.5:4b-q4_K_M`, which runs on llama.cpp. The
default model is a GGUF tag.

Because the loaded context is a soft limit there, a smaller one is not
reported as a shortfall, and `/provider` labels the line as a soft limit with
nothing reserved. The share of the model in GPU memory is still checked.

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
the admission check, at startup or on `/model NAME`. Larger models and
larger contexts need more memory — budget beyond the model weights before
raising `--context`.
