# local-agent

A fully local AI coding agent for the terminal -- powered by Ollama and small models, with intelligent routing, cross-session memory, and MCP tool integration.

```
  ╭──────────────────────────────────────────╮
  │  local-agent                             │
  │  100% local. Your data never leaves.     │
  │                                          │
  │  ASK  --  PLAN  --  BUILD                │
  │  0.8B    4B        9B                    │
  ╰──────────────────────────────────────────╯
```

---

## What is local-agent?

- **100% local** -- runs entirely on your machine via Ollama. No API keys, no cloud, no data leaving your device.
- **Small model optimized** -- intelligent routing across Qwen 3.5 variants (0.8B / 2B / 4B / 9B) based on task complexity.
- **Three operational modes** -- ASK for quick answers, PLAN for design and reasoning, BUILD for full execution with tools.
- **MCP native** -- first-class Model Context Protocol support (STDIO, SSE, Streamable HTTP) for extensible tool integration.
- **Beautiful TUI** -- built with Charm's BubbleTea v2, Lip Gloss v2, and Glamour for rich markdown rendering in the terminal.
- **Infinite Context Engine (ICE)** -- cross-session vector retrieval that surfaces relevant past conversations automatically.
- **Auto-Memory Detection** -- the LLM extracts facts, decisions, preferences, and TODOs from conversations and persists them.
- **Thinking/CoT extraction** -- chain-of-thought reasoning is captured and displayed in collapsible blocks.
- **Skills system** -- load `.md` skill files with YAML frontmatter to inject domain-specific instructions into the system prompt.
- **Agent profiles** -- configure per-project agents with custom system prompts, skills, and MCP servers.

---

## Quick Start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [Ollama](https://ollama.ai/) running locally
- [Task](https://taskfile.dev/) (optional, for build commands)

### Install

Pull the required model, then install:

```bash
ollama pull qwen3.5:2b

go install github.com/abdul-hamid-achik/local-agent/cmd/local-agent@latest
```

For the full model routing suite (optional):

```bash
ollama pull qwen3.5:0.8b
ollama pull qwen3.5:4b
ollama pull qwen3.5:9b
ollama pull nomic-embed-text   # for ICE vector embeddings
```

### Configure

Create a config file (optional -- defaults work out of the box):

```bash
mkdir -p ~/.config/local-agent
cp config.example.yaml ~/.config/local-agent/config.yaml
```

### Run

```bash
local-agent
```

Or from source:

```bash
task dev
```

---

## Features

### Model Routing

local-agent automatically selects the right model size for the task at hand. Simple questions go to the fast 2B model; complex multi-step reasoning escalates to the 9B model. The router analyzes query complexity using keyword heuristics and word count.

| Complexity | Model         | Speed  | Use Cases                                    |
|------------|---------------|--------|----------------------------------------------|
| Simple     | qwen3.5:2b    | 2.5x   | Quick answers, simple tool use, single edits |
| Medium     | qwen3.5:4b    | 1.5x   | Code completion, refactoring, explanations   |
| Complex    | qwen3.5:9b    | 1.0x   | Multi-step reasoning, debugging, code review |

The fallback chain ensures graceful degradation if a model is not available: `2b -> 4b -> 9b`.

### Three Modes: ASK / PLAN / BUILD

Cycle between modes with `shift+tab`. Each mode configures a different system prompt and preferred model tier.

- **ASK** -- Direct, concise answers. Routes to the fastest available model. Tools available for file reads and searches.
- **PLAN** -- Design and planning. Breaks tasks into steps. Reads and explores with tools but does not modify files.
- **BUILD** -- Full execution mode. Uses the most capable model. All tools enabled including writes and modifications.

### MCP Tool Integration

Connect any MCP-compatible tool server. Supports all three transport protocols:

- **STDIO** -- Launch tools as subprocesses (default).
- **SSE** -- Connect to Server-Sent Events endpoints.
- **Streamable HTTP** -- Connect to HTTP-based MCP servers.

Tool calls execute in parallel when possible. The registry handles graceful failure if a server becomes unavailable.

### Infinite Context Engine (ICE)

ICE embeds each conversation turn using `nomic-embed-text` and stores them persistently. On every new message, it retrieves the most relevant past conversations via cosine similarity and injects them into the system prompt -- giving the agent memory that spans across sessions.

### Auto-Memory Detection

After each conversation turn, a background process analyzes the exchange and extracts structured memories:

- **FACT** -- objective information the user shared
- **DECISION** -- choices made during the conversation
- **PREFERENCE** -- user preferences and working styles
- **TODO** -- action items and follow-ups

Memories are stored in `~/.config/local-agent/memories.json` with tag-weighted search scoring (tags weighted 3x over content).

### Thinking/CoT Display

When the model produces chain-of-thought reasoning, local-agent captures it and renders it in collapsible blocks. Toggle the display with `ctrl+t`.

### Skills System

Drop `.md` files with YAML frontmatter into the skills directory to inject domain-specific instructions:

```
~/.config/local-agent/skills/
```

Manage active skills with `/skill list`, `/skill activate <name>`, and `/skill deactivate <name>`.

### Agent Profiles

Create per-project or per-domain agent profiles:

```
~/.agents/<name>/
  AGENT.md       # System prompt additions
  SKILL.md       # Agent-specific skills
  mcp.yaml       # Agent-specific MCP servers
```

Switch profiles with `/agent <name>` or `/agent list`.

---

## Configuration

### File Locations

Config is searched in order (first match wins):

1. `./local-agent.yaml` (project-local)
2. `~/.config/local-agent/config.yaml` (user-global)

### Annotated Example

```yaml
ollama:
  model: "qwen3.5:2b"               # Default model
  base_url: "http://localhost:11434"  # Ollama API endpoint
  num_ctx: 262144                     # Context window size

# Skills directory (default: ~/.config/local-agent/skills/)
# skills_dir: "/path/to/custom/skills"

# MCP tool servers
servers:
  # STDIO transport (default)
  - name: noted
    command: noted
    args: [mcp]

  # SSE transport
  # - name: remote-server
  #   transport: sse
  #   url: "http://localhost:8811"

  # Streamable HTTP transport
  # - name: streamable-server
  #   transport: streamable-http
  #   url: "http://localhost:8812/mcp"

# ICE configuration
# ice:
#   enabled: true
#   embed_model: "nomic-embed-text"
#   store_path: "~/.config/local-agent/conversations.json"
```

### Environment Variables

| Variable                 | Description                  | Overrides            |
|--------------------------|------------------------------|----------------------|
| `OLLAMA_HOST`            | Ollama API base URL          | `ollama.base_url`    |
| `LOCAL_AGENT_MODEL`      | Default model name           | `ollama.model`       |
| `LOCAL_AGENT_AGENTS_DIR` | Path to agents directory     | `agents.dir`         |

---

## Keyboard Shortcuts

### Input

| Key             | Action                        |
|-----------------|-------------------------------|
| `enter`         | Send message                  |
| `shift+enter`   | Insert new line               |
| `up` / `down`   | Browse input history          |
| `shift+tab`     | Cycle mode (ASK/PLAN/BUILD)   |
| `ctrl+m`        | Quick model switch            |

### Navigation

| Key              | Action                       |
|------------------|------------------------------|
| `pgup` / `pgdown`| Scroll conversation          |
| `ctrl+u`         | Half-page scroll up          |
| `ctrl+d`         | Half-page scroll down        |

### Display

| Key             | Action                        |
|-----------------|-------------------------------|
| `?`             | Toggle help overlay           |
| `t`             | Expand/collapse tool calls    |
| `space`         | Toggle last tool details      |
| `ctrl+t`        | Toggle thinking/CoT display   |
| `ctrl+y`        | Copy last response            |

### Control

| Key             | Action                        |
|-----------------|-------------------------------|
| `esc`           | Cancel streaming / close overlay |
| `ctrl+c`        | Quit                          |
| `ctrl+l`        | Clear screen                  |
| `ctrl+n`        | New conversation              |

---

## Slash Commands

| Command                              | Description                       |
|--------------------------------------|-----------------------------------|
| `/help`                              | Show help overlay                 |
| `/clear`                             | Clear conversation history        |
| `/new`                               | Start a fresh conversation        |
| `/model [name\|list\|fast\|smart]`   | Show or switch models             |
| `/models`                            | Open model picker                 |
| `/agent [name\|list]`                | Show or switch agent profile      |
| `/load <path>`                       | Load markdown file as context     |
| `/unload`                            | Remove loaded context             |
| `/skill [list\|activate\|deactivate] [name]` | Manage skills            |
| `/servers`                           | List connected MCP servers        |
| `/ice`                               | Show ICE engine status            |
| `/sessions`                          | Browse saved sessions             |
| `/exit`                              | Quit                              |

---

## Architecture

```
cmd/local-agent/          Entry point
internal/
  agent/                  ReAct loop orchestration
  llm/                    LLM abstraction (OllamaClient, ModelManager)
  mcp/                    MCP server registry
  config/                 YAML config, env overrides, Router
  ice/                    Infinite Context Engine
  memory/                 Persistent key-value store
  skill/                  Skill file loader
  command/                Slash command registry
  tui/                    BubbleTea v2 terminal UI
  logging/                Structured logging
```

### Request Flow

```
User Input
    |
    v
agent.AddUserMessage()
    |
    v
ICE embeds message, retrieves relevant past context
    |
    v
System prompt assembled (tools + skills + context + ICE + memory)
    |
    v
Router selects model based on task complexity
    |
    v
LLM streams response via ChatStream()
    |
    v
Tool calls routed through MCP registry (parallel execution)
    |
    v
ReAct loop continues (up to 10 iterations) until final text
    |
    v
Conversation compacted if token budget exceeded
Auto-memory detection runs in background
```

### Key Interfaces

- `llm.Client` -- pluggable LLM provider (`ChatStream`, `Ping`, `Embed`)
- `agent.Output` -- streaming callbacks for TUI rendering
- `command.Registry` -- extensible slash command dispatch

### Concurrency

`sync.RWMutex` protects shared state in `ModelManager`, `mcp.Registry`, and `memory.Store`. Auto-memory detection and MCP connections run as background goroutines. Tool calls execute in parallel when independent.

---

## Comparison

| Feature                          | local-agent | opencode | crush |
|----------------------------------|:-----------:|:--------:|:-----:|
| 100% local (no API keys)         | Yes         | No       | Yes   |
| Model routing by task complexity | Yes         | No       | No    |
| Operational modes (ASK/PLAN/BUILD)| Yes        | No       | No    |
| Cross-session memory (ICE)       | Yes         | No       | No    |
| Auto-memory detection            | Yes         | No       | No    |
| Thinking/CoT extraction          | Yes         | Yes      | No    |
| MCP tool support                 | Yes         | Yes      | Yes   |
| Skills system                    | Yes         | No       | No    |
| Plan form overlay                | Yes         | No       | No    |
| Small model optimized            | Yes         | No       | No    |
| TUI chat interface               | Yes         | Yes      | Yes   |
| Language                         | Go          | TypeScript| Go   |

---

## Building

This project uses [Task](https://taskfile.dev/) as its build tool.

```bash
task build       # Compile to bin/local-agent
task run         # Build and run
task dev         # Quick run via go run ./cmd/local-agent
task test        # Run all tests: go test ./...
task lint        # Run golangci-lint run ./...
task clean       # Remove bin/ directory
```

Run a single test:

```bash
go test ./internal/agent/ -run TestFunctionName
```

---

## License

MIT
