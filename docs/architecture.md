---
title: Architecture
description: Understand Local Agent's Go and Charm runtime, model manager, MCP policy, durable goal contracts, and local persistence.
outline: deep
---

# Architecture

Local Agent keeps the conversation surface, inference runtime, tool authority, and durable goal state separate.

```text
Charm TUI or headless output
          |
          v
Agent loop --------> prompt builder + mode policy
    |                         |
    |                         +-- AGENTS.md, skills, context, memory
    |
    +-- tool policy -> approval -> built-ins / MCP registry
    |
    +-- ModelManager -> Ollama inventory -> local or consented Cloud model

Goal Runtime -> budgets + permits + receipts -> optional Cortex advisor

Persistence -> scoped memory/ICE + SQLite sessions/ledger + logs
```

## Runtime boundaries

- `internal/agent` owns the ReAct loop, prompts, policies, and tool dispatch.
- `internal/llm` owns the Ollama client, live inventory, model admission, and per-request context policy.
- `internal/mcp` owns server connections, health checks, reconnection, and tool namespacing.
- `internal/ui` owns the Charm interface and renders state from the host runtime.
- `internal/goal` owns durable goal state, budgets, receipts, and recovery values.
- `internal/goaladvisor` bounds the optional Cortex/MCPHub semantic adapter.
- `internal/db` persists sessions, permissions, checkpoints, and execution evidence.

The model does not enforce its own mode. The host decides which tools are visible and rejects out-of-policy requests.

## Effect ordering

Built-in, memory, and MCP calls execute deterministically in model order. Unknown MCP effects are not parallelized. The append-only execution ledger records durable dispatch intent before an external effect begins, which makes uncertain outcomes visible after interruption instead of guessing that nothing happened.

## Goal supervisor boundary

The foreground Goal Runtime is implemented. The `internal/supervisor` and `internal/workunit` packages define safety-tested scheduling and specialist-admission contracts, but they are not wired as a headless queue or multi-process specialist runner.

## Current limitations

- Ollama is the only inference adapter.
- Routing uses heuristics and a 16 GB-oriented memory guard rather than live free-memory scheduling.
- MCP support is currently tool-focused.
- ICE uses a flat workspace-scoped JSON store.
- In-flight effects do not continue automatically after a crash.
- The current product has no OS-level sandbox.
