# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Public Website Boundary

`docs/` is the deployable public website for [local-agent.dev](https://local-agent.dev). Everything under that directory must be safe and useful to publish.

- Keep `docs/` as a standalone product and documentation website. Use it only for the landing page, public user documentation, and website assets.
- Never create ADRs under `docs/` or elsewhere in this repository. Store project ADRs in `~/notes/projects/local-agent/adrs/`; `docs/architecture.md` may summarize only the stable public architecture contract.
- Never link public pages to private Notes paths or assume those records are included in a clone of this repository.
- Do not put handoffs, scratchpads, planning notes, transcripts, generated reports, diagnostics, private filesystem paths, credentials, or internal agent artifacts in `docs/`.
- Keep internal or temporary working material outside `docs/` in an appropriate ignored location.
- Do not make unsupported claims about privacy, safety, performance, adoption, compatibility, or release readiness. The current product is alpha and is not an operating-system sandbox.
- Preserve responsive design, keyboard accessibility, reduced-motion support, canonical metadata, and a passing production build for every website change.

## Build & Development Commands

This project uses [Task](https://taskfile.dev/) as its build tool.

```bash
task build       # Compile to bin/local-agent
task run         # Build + run
task dev         # Quick run via go run ./cmd/local-agent
task test        # Run all tests: go test ./...
task lint        # Run golangci-lint run ./...
task verify      # Run the Go and website CI gates
task site        # Run the local-agent.dev VitePress site
task site:build  # Build the production website
task site:preview # Build and preview the production website
task clean       # Remove bin/ directory
```

To run a single test:
```bash
go test ./internal/agent/ -run TestFunctionName
```

## Architecture

Go 1.25+ project implementing a local-first terminal coding agent. Ollama is the only inference adapter. The TUI uses Charm v2; MCP servers extend the tool surface.

### Package Layout (`internal/`)

- **agent/** — ReAct loop, prompt construction, mode policy, ordered tool dispatch, compaction, hooks, and checkpoints. Built-in, memory, and MCP effects execute deterministically in model order.
- **llm/** — Ollama client, authoritative live inventory, local/Cloud context policy, availability-aware model routing, per-request expectations, and runtime switching.
- **mcp/** — STDIO, SSE, and Streamable HTTP connections, namespaced tools, health checks, bounded results, and reconnects.
- **ecosystem/** — Exact companion-tool receipt parsers and bounded transport/domain/evidence projections. Raw MCP structured output must not cross into persisted UI state.
- **config/** — YAML/XDG loading, environment overrides, model preferences, routing, privacy policy, agent profiles, and ignore rules.
- **ice/** — Optional workspace-scoped Ollama embeddings, bounded JSON retrieval, and single-flight background auto-memory.
- **memory/** — Workspace-scoped structured JSON memory under `~/.config/local-agent/memory/<workspace-hash>.json`, with owner-only files, locking, and coherent reloads.
- **db/** — SQLite sessions, permissions, checkpoints, usage, execution events, control-plane records, and durable goal projections.
- **goal/** — Durable goal lifecycle, immutable criteria, budgets, permits, receipts, and evidence-backed recovery values.
- **goaladvisor/** — Bounded optional Cortex/MCPHub semantic adapter; it does not own scheduling or approvals.
- **controlplane/** — Append-only exception values and validation.
- **supervisor/** and **workunit/** — Tested scheduling/admission contracts; they are not wired headless or multi-process execution engines.
- **skill/** — Skill discovery and activation from Local Agent and shared `~/.agents` directories.
- **command/** — Canonical slash-command registry and hidden compatibility aliases.
- **ui/** — Bubble Tea v2 smart parent, Bubbles child components, transient overlays, model/goal/session flows, and Glamour rendering.

### Request Flow

1. The TUI or headless controller submits user input under NORMAL, PLAN, or AUTO authority.
2. Project instructions, active skills, loaded context, workspace memory, and optional ICE retrieval form bounded prompt context.
3. `ModelManager` resolves a verified Ollama model and freezes the expected context policy for that request.
4. The model response streams through the Agent loop.
5. Tool calls pass host mode policy, workspace validation, permission checks, and durable execution recording before dispatch.
6. Tool receipts return in model order; the loop continues within iteration, token, and context limits.
7. Completed session state is persisted. Background auto-memory yields to foreground inference and joins at shutdown.

An explicit `/goal` adds a durable Goal Runtime around this flow. Ordinary AUTO prompts remain direct and approval-gated. Local Agent owns budgets, permits, cancellation, approvals, persistence, and recovery. Cortex is optional bounded semantic input.

MCP transport success, domain success, and verified evidence are independent. Parse known structured contracts inside `internal/ecosystem`; unknown versions remain attention/unknown, and raw `StructuredContent` is discarded before the UI or session persistence boundary.

### Key Interfaces

- `llm.Client` — inference adapter contract (`ChatStream`, `Ping`, `Embed`).
- `agent.Output` — streaming and tool-state callbacks consumed by the UI/controller.
- `command.Registry` — canonical slash-command dispatch and completion metadata.
- Goal stores and execution repositories — durable state/evidence boundaries independent from presentation.

### Concurrency

Preserve each package's lock ownership and ordering. Inventory commits may wait for active inference and therefore run in a Bubble Tea command goroutine, never inside `Update`. MCP connections and health checks may run concurrently, but unknown tool effects do not. Auto-memory is single-flight, cancelled before foreground inference, and joined during shutdown.

### Configuration

The first matching file wins; files are not merged:

1. `./local-agent.yaml`
2. `./local-agent.yml`
3. `$XDG_CONFIG_HOME/local-agent/config.yaml`
4. `$XDG_CONFIG_HOME/local-agent/config.yml`
5. `$HOME/.config/local-agent/config.yaml`
6. `$HOME/.config/local-agent/config.yml`

Environment overrides apply afterward. Shared profiles live under `~/.agents/agents/<name>/agent.yaml`; shared skills live under `~/.agents/skills/<name>/SKILL.md`. See `config.example.yaml` and `docs/configuration.md` before changing precedence or paths.

## TUI Development Rules

- **Always use Charm libraries** for all TUI components: [BubbleTea v2](https://charm.land/bubbletea/v2), [Bubbles v2](https://charm.land/bubbles/v2), [Lip Gloss v2](https://charm.land/lipgloss/v2), [Glamour](https://github.com/charmbracelet/glamour).
- Prefer existing Bubbles components (spinner, viewport, textarea, textinput, list, table, paginator, progress, stopwatch, timer, key) over custom implementations.
- Follow the Charm "smart parent, dumb child" pattern: the main `Model` processes all messages; child components expose methods returning `tea.Cmd`.
- Use `lipgloss.LightDark()` for adaptive theming. Never hardcode ANSI colors.
- Render cached content where possible to avoid per-frame re-rendering overhead.
