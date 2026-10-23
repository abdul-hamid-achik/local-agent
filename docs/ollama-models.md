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
from local Ollama. The default is 16384; `32768` and `32k` both parse. The
value cannot exceed the model's native context, and an oversized value is
rejected up front by the same admission check.

`--context` applies to the local Ollama provider only; Chalupa negotiates its
own. See [Providers](/providers).

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
