---
title: Local Agent ecosystem
description: Explore the focused local-first tools that connect to, verify, or complement Local Agent without blurring their ownership boundaries.
outline: deep
---

# Local Agent ecosystem

Local Agent is the conversation, model, and permission host. The surrounding projects remain focused tools with explicit interfaces. They are not all bundled integrations.

Each tool owns what its results mean:

| Tool | Product role | What its results can mean |
| --- | --- | --- |
| MCPHub | MCP gateway | Routing only |
| Vecgrep | Semantic discovery | Candidate |
| Codemap | Code structure and impact | Supported, or stale when its index reports drift |
| Glyphrun | CLI and TUI verification | Verified or contradicted from a versioned run |
| Cairntrace | Browser verification | Verified or contradicted from a versioned run |
| Vidtrace | Video evidence | Artifact-backed support |
| file.cheap | Artifact storage | Preserves evidence; does not create truth by itself |
| TinyVault | Secret boundary | Security state, never evidence content |
| Monitor | Ambient and requested observability | Operational support |
| Bob | Repository contract and convergence | No evidence upgrade; clean, drift, conflict, blocked, or failed domain state |
| Tuimark | Terminal interface engine | Presentation only |

Local Agent does not read these meanings yet. A tool it reaches through a configured MCP server is a generic MCP call. In the Tuimark client's transcript, a result the server returns without an error is marked unverified (`!`) on its card, never verified, and an error result is marked failed (`✗`). A successful transport is never taken for a domain result or a verification outcome. See [MCP](/mcp#transport-is-not-domain-success).

## Terminal interface

### [Tuimark](https://tuimark.dev)

A terminal UI engine with a declarative document, diffed cell rendering, and a
host protocol for driving it from any language runtime. Local Agent's
next-generation interactive client is built on Tuimark; Tuimark owns
presentation only and is never a source of evidence.


## Connected runtime

### [MCPHub](https://mcphubcli.dev/)

A gateway some users front their MCP servers with. Local Agent does not require it: the client connects directly to servers via the `--mcp-config` JSON file, and every configured call still crosses the Local Agent approval boundary.

The [MCPHub website](https://mcphubcli.dev/) and [source repository](https://github.com/abdul-hamid-achik/mcphub) document the gateway, synchronization, and deployment workflow.

## Discovery and local data

### [Codemap](https://github.com/abdul-hamid-achik/codemap)

Structural code maps, references, impact, and index freshness. Local Agent treats fresh structural results as support, while missing or stale indexes remain visible instead of being interpreted as negative evidence.

### [Vecgrep](https://vecgrep.dev/)

Local-first semantic code search with Ollama embeddings, hybrid search, and an MCP surface. It is MCP-capable and related to Local Agent; it is not a native embedded search engine inside Local Agent.

### [TinyVault](https://www.tinyvault.dev/)

A local-first secrets manager and MCP server in a Go binary. It can be configured through MCP; it is not bundled.

### [file.cheap](https://file.cheap)

A local-first CLI and MCP file vault for storing, finding, compressing, and comparing workflow artifacts. It can be configured through MCP; it is not Local Agent's session store.

### [VecLite](https://veclite.dev)

An embeddable Go vector database with single-file storage, HNSW, BM25, and hybrid search. It is related local-first infrastructure from the same author; Local Agent does not embed it.

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

Bob is built to be driven by agents. Run `bob learn --json` once at the start of a session for a machine-readable onboarding brief: every command, the exit-code contract (0 success, 2 conflicts, 3 drift without conflicts, 4 invalid input; `plan` always exits 0), the stable `data.error.code` values, and the recipe catalog. Every `--json` failure envelope carries copy-pasteable corrective commands in `next_actions`, and every plan action carries a stable `code` field for branching without parsing prose.

Bob's `files` recipe lets an agent declare an arbitrary file tree in `bob.yaml` — paths, modes, contents, and literal `${vars.key}` substitution — and scaffold or reconcile any repository through Bob's plan/apply/lock conflict safety; `bob recipe show files --json` returns the schema with an example.

Every Bob result carries `EvidenceNone`: no evidence assessment. Bob can prove
whether repository files converge on its contract, but not whether application
behavior is correct. A Bob `clean` state is therefore visually and semantically
distinct from verified evidence.

The [Bob website](https://bobcli.dev/) and [source repository](https://github.com/abdul-hamid-achik/bob) document its repository contracts and build workflow; the [agent guide](https://bobcli.dev/agents) is the onboarding reference for coding agents.

## Relationship rule

“MCP-capable” means a tool can be exposed through a configured MCP server. It does not mean Local Agent ships, authenticates, or silently trusts that tool. Every configured MCP call still crosses the Local Agent approval boundary.
