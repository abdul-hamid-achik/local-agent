---
title: Local Agent ecosystem
description: Explore the focused local-first tools that connect to, verify, or complement Local Agent without blurring their ownership boundaries.
outline: deep
---

# Local Agent ecosystem

Local Agent is the conversation, model, permission, and foreground-goal host. The surrounding projects remain focused tools with explicit interfaces. They are not all bundled integrations.

## Connected runtime

### [Cortex](https://cortexai.tools/)

An evidence-guided engineering kernel for durable task state, bounded changes, and verification. Local Agent can use Cortex as the optional semantic advisor for durable goals, directly or through MCPHub.

### [MCPHub](https://mcphubcli.dev/)

A single configuration and gateway for discovering MCP servers and synchronizing them to agent harnesses. MCPHub is the recommended gateway for Local Agent; it does not replace Local Agent's final approval policy.

The [MCPHub website](https://mcphubcli.dev/) and [source repository](https://github.com/abdul-hamid-achik/mcphub) document the gateway, synchronization, and deployment workflow.

## Discovery and local data

### [Vecgrep](https://vecgrep.dev/)

Local-first semantic code search with Ollama embeddings, hybrid search, and an MCP surface. It is MCP-capable and related to Local Agent; it is not a native embedded search engine inside Local Agent.

### [TinyVault](https://www.tinyvault.dev/)

A local-first secrets manager and MCP server in a Go binary. It can be configured through MCP; it is not bundled.

### [file.cheap](https://file.cheap)

A local-first CLI and MCP file vault for storing, finding, compressing, and comparing workflow artifacts. It can be configured through MCP; it is not Local Agent's session store.

### [VecLite](https://veclite.dev)

An embeddable Go vector database with single-file storage, HNSW, BM25, and hybrid search. It is related infrastructure; Local Agent's current ICE store does not embed VecLite.

## Evidence and verification

### [Glyphrun](https://glyphrun.dev)

Black-box testing for CLI and TUI workflows over a real pseudo-terminal and deterministic emulator. Local Agent uses Glyphrun as test infrastructure for its terminal contracts.

### [Cairntrace](https://cairntrace.dev/)

Local-first behavioral browser specs for coding agents, with repairable steps and evidence-backed outcomes. It is a related verifier, not bundled into Local Agent.

### [Vidtrace](https://vidtrace.dev)

A local-first Go CLI that turns bug recordings into frames, OCR, transcripts, and timestamped evidence timelines.

## Operations and building

### [Monitor](https://monitorcli.dev)

A terminal system monitor for macOS and Linux with a TUI, JSON CLI, and MCP surface. It can be configured through MCP; it is not a built-in resource scheduler.

### [Bob](https://bobcli.dev/)

A deterministic, model-free repository factory that turns a `bob.yaml` contract into owned infrastructure and detects drift in CI. It complements Local Agent's coding workflow but is not embedded.

The [Bob website](https://bobcli.dev/) and [source repository](https://github.com/abdul-hamid-achik/bob) document its repository contracts and build workflow.

## Relationship rule

“MCP-capable” means a tool can be exposed through a configured MCP server. It does not mean Local Agent ships, authenticates, or silently trusts that tool. Every configured MCP call still crosses the Local Agent approval boundary.
