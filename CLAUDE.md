# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

This project uses [Task](https://taskfile.dev/) as its build tool.

```bash
task build       # Compile to bin/local-agent
task run         # Build + run
task dev         # Quick run via go run ./cmd/local-agent
task test        # Run all tests: go test ./...
task lint        # Run golangci-lint run ./...
task clean       # Remove bin/ directory
```

To run a single test:
```bash
go test ./internal/agent/ -run TestFunctionName
```

## Architecture

Go 1.25+ project implementing a local AI agent with a TUI chat interface, powered by Ollama and MCP (Model Context Protocol) servers.

### Package Layout (`internal/`)

- **agent/** — ReAct loop orchestration. Streams LLM responses, collects tool calls, executes them (in parallel via MCP registry), feeds results back. Max 10 iterations per turn. Handles conversation compaction when token budget is exceeded.
- **llm/** — LLM abstraction layer. `Client` interface with `OllamaClient` implementation. `ModelManager` caches per-model clients and supports runtime model switching.
- **mcp/** — MCP server registry. Manages connections to multiple MCP servers (STDIO/SSE/Streamable HTTP), routes tool calls to the correct server, handles graceful failure.
- **config/** — YAML config loading with env var overrides (`OLLAMA_HOST`, `LOCAL_AGENT_MODEL`, `LOCAL_AGENT_AGENTS_DIR`). Includes a Router that classifies task complexity to select appropriate models.
- **ice/** — In-Context Examples. Embeds user messages via Ollama, stores conversations persistently (JSON), retrieves relevant past context via similarity search, and runs auto-memory detection.
- **memory/** — Persistent key-value memory store (JSON-backed at `~/.config/local-agent/memories.json`). Tag-weighted search scoring (tags 3x content).
- **skill/** — Discovers and loads `.md` skill files (with YAML frontmatter) from configurable directories. Skills inject instructions into the system prompt.
- **command/** — Slash command registry (`/help`, `/clear`, `/model`, `/agent`, `/skill`, `/load`, `/context`, `/info`, `/quit`).
- **tui/** — BubbleTea v2 terminal UI. State machine (Idle/Waiting/Streaming) with overlay system (None/Help/Completion). Renders markdown via Glamour.

### Request Flow

1. User input → `agent.AddUserMessage()`
2. ICE embeds message and assembles relevant past context
3. System prompt built (tools, skills, loaded context, ICE results, memory)
4. LLM streams response via `ChatStream()`
5. Tool calls routed through MCP registry (or handled internally for memory tools)
6. Loop continues (up to 10 iterations) until LLM produces final text
7. Conversation compacted if token budget exceeded; auto-memory detection runs in background

### Key Interfaces

- `llm.Client` — pluggable LLM provider (`ChatStream`, `Ping`, `Embed`)
- `agent.Output` — streaming callbacks for TUI rendering
- `command.Registry` — extensible slash command dispatch

### Concurrency

`sync.RWMutex` protects shared state in `ModelManager`, `mcp.Registry`, and `memory.Store`. Auto-memory detection and MCP connections run as background goroutines.

### Configuration

Config searched in order: `./local-agent.yaml` → `~/.config/local-agent/config.yaml`. Agent profiles live in `~/.agents/[name]/` with `AGENT.md`, `SKILL.md`, and `mcp.yaml`.

## TUI Development Rules

- **Always use Charm libraries** for all TUI components: [BubbleTea v2](https://charm.land/bubbletea/v2), [Bubbles v2](https://charm.land/bubbles/v2), [Lip Gloss v2](https://charm.land/lipgloss/v2), [Glamour](https://github.com/charmbracelet/glamour).
- Prefer existing Bubbles components (spinner, viewport, textarea, textinput, list, table, paginator, progress, stopwatch, timer, key) over custom implementations.
- Follow the Charm "smart parent, dumb child" pattern: the main `Model` processes all messages; child components expose methods returning `tea.Cmd`.
- Use `lipgloss.LightDark()` for adaptive theming. Never hardcode ANSI colors.
- Render cached content where possible to avoid per-frame re-rendering overhead.
