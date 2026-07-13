---
title: Configuration
description: Configure Local Agent per repository or through XDG paths, then apply environment overrides deliberately.
outline: deep
---

# Configuration

Local Agent supports both repository-local and user-wide configuration. Files are not merged: the first matching path wins, then environment overrides are applied.

## Search order

1. `./local-agent.yaml`
2. `./local-agent.yml`
3. `$XDG_CONFIG_HOME/local-agent/config.yaml`
4. `$XDG_CONFIG_HOME/local-agent/config.yml`
5. `$HOME/.config/local-agent/config.yaml`
6. `$HOME/.config/local-agent/config.yml`

`XDG_CONFIG_HOME` is used only when it is absolute. Duplicate paths are checked once.

## Minimal configuration

```yaml
ollama:
  model: qwen3.5:2b
  base_url: http://localhost:11434
  num_ctx: 16384

privacy:
  local_only: true

model:
  default_model: qwen3.5:2b
  fallback_chain:
    - qwen3.5:2b
    - phi4-mini:latest
    - qwen3.5:0.8b
    - qwen3.5:4b
  auto_select: true
  embed_model: nomic-embed-text

tools:
  timeout: 30s
  max_grep_results: 500
  max_iterations: 10

ice:
  enabled: false

servers: []
```

Start from the annotated [`config.example.yaml`](https://github.com/abdul-hamid-achik/local-agent/blob/main/config.example.yaml) when you need the complete model and MCP examples.

## Environment overrides

| Variable | Purpose |
|---|---|
| `OLLAMA_HOST` | Override `ollama.base_url` |
| `LOCAL_AGENT_MODEL` | Override the initial model |
| `LOCAL_AGENT_AGENTS_DIR` | Override the agents directory |
| `LOCAL_AGENT_TOOLS_TIMEOUT` | Override the built-in tool timeout |
| `LOCAL_AGENT_TOOLS_MAX_GREP` | Override the maximum grep results |
| `LOCAL_AGENT_TOOLS_MAX_ITER` | Override ReAct iterations |
| `LOCAL_AGENT_ICE_EMBED_MODEL` | Override the ICE embedding model |
| `LOCAL_AGENT_LOCAL_ONLY` | Toggle local-machine endpoint enforcement |
| `LOCAL_AGENT_ALLOW_LARGE_MODELS` | Bypass the 16 GB-oriented admission guard |
| `LOCAL_AGENT_REDUCED_MOTION` | Replace animated TUI activity with static glyphs |

## Repository instructions

At startup, Local Agent reads `./AGENTS.md`. If that file does not exist, it falls back to the legacy `./AGENT.md` name.

Create a starter file with:

```bash
local-agent init
```

Instructions are model context, not a security boundary. Mode policy, tool admission, workspace path checks, and approval checks remain host-owned.

## Profiles and skills

Global profiles and skills use the shared agent directory:

```text
~/.agents/
  agents.md
  mcp.json
  agents/
    reviewer/
      agent.yaml
  skills/
    go-review/
      SKILL.md
```

Flat Local Agent skills may also live under `~/.config/local-agent/skills/*.md`. Switch profiles with `/agent`, and activate skills with `/skill`.
