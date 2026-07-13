# local-agent

A local-first coding agent for the terminal, built in Go with Charm and powered by local Ollama models.

[Website](https://local-agent.dev/) · [Getting started](https://local-agent.dev/getting-started) · [Safety](https://local-agent.dev/safety) · [Ecosystem](https://local-agent.dev/ecosystem)

> **Status: alpha.** `local-agent` can inspect a repository, edit files, run commands, use MCP tools, and retain optional cross-session memory. Run it in a clean Git worktree, read every approval request, and review the resulting diff. The current safety layer is useful, but it is not an operating-system sandbox.

```text
  NORMAL -> interactive work; changes require approval
  PLAN   -> inspect and design without mutations
  AUTO   -> proactive work under the same approval policy
```

## What works today

- A responsive terminal UI built with Bubble Tea v2, Bubbles v2, Lip Gloss v2, and Glamour.
- Streaming chat through Ollama with an availability-aware local model router.
- Qwen 3.5, Phi-4 Mini, and manually selected Ornith/Gemma/Qwen exclusive profiles.
- Read, search, diff, validated patch, atomic write, file-management, and shell tools.
- NORMAL, PLAN, and AUTO authority with approval prompts for risky operations.
- STDIO, SSE, and Streamable HTTP MCP tool servers, including an MCPHub gateway.
- Project instructions from `AGENTS.md` with legacy `AGENT.md` fallback.
- Lossless SQLite session resume, native reasoning display, skills, agent profiles, optional ICE retrieval, checkpoints, logs, and terminal behavior tests.

## Quick start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [Ollama](https://ollama.com/) running on the same machine
- [Task](https://taskfile.dev/) for repository development commands (optional)
- MCPHub, Cortex, Obsidian, or other MCP servers only if you want those tools

Release binaries target macOS and Linux. Windows is not published yet because
graceful cancellation must terminate complete subprocess trees, which requires
a Job Object implementation rather than Go's direct-process fallback.

Start Ollama in one terminal:

```bash
ollama serve
```

Pull the default model. The 4B tier is optional but recommended for coding work:

```bash
ollama pull qwen3.5:2b
ollama pull qwen3.5:4b
```

Install and launch:

```bash
go install github.com/abdul-hamid-achik/local-agent/cmd/local-agent@latest
local-agent
```

Or run this checkout directly:

```bash
go run ./cmd/local-agent
```

No configuration file is required for the basic Ollama-only experience. To start from the annotated configuration:

```bash
mkdir -p ~/.config/local-agent
cp config.example.yaml ~/.config/local-agent/config.yaml
```

The example enables an `mcphub` command. Comment out that server entry if MCPHub is not installed.

## Local model setup

`local-agent` asks Ollama for its authoritative inventory at startup, including local weights and Ollama Cloud aliases. Automatic routing uses only admitted local models; Cloud remains an explicit, confirmed choice.

Approximate artifact sizes vary by Ollama build and quantization:

| Model | Approx. size | Intended role | Automatic routing |
|---|---:|---|---|
| `qwen3.5:0.8b` | 1.0 GB | Very short answers and lightweight classification; weak autonomous tool use | Eligible |
| `qwen3.5:2b` | 2.7 GB | Compact interactive answers and modest tool chains | Eligible |
| `phi4-mini:latest` | 2.5 GB | Alternative compact reasoning/tool profile | Fallback eligible |
| `qwen3.5:4b` | 3.4 GB | Preferred coding, debugging, review, and multi-step tools | Eligible |
| `qwen3.5:9b` | 6.6 GB | Deep manual profile | No; exclusive |
| `ornith:latest` | 5.6 GB | Agentic coding and independent deep verification | No; exclusive |
| `gemma4:e2b` | 7.2 GB | Alternative manual reasoning/tool profile | No; exclusive |

Pull whichever profiles you intend to use:

```bash
ollama pull qwen3.5:0.8b
ollama pull qwen3.5:2b
ollama pull qwen3.5:4b
ollama pull phi4-mini:latest

# Manual exclusive profiles on a 16 GB machine:
ollama pull qwen3.5:9b
ollama pull ornith:latest
ollama pull gemma4:e2b

# Required only when ICE is enabled:
ollama pull nomic-embed-text

ollama list
```

The shipped memory guard is tuned for a 16 GB Apple-silicon machine:

- `num_ctx: 16384` is the recommended 2B/4B default.
- Qwen 9B, Ornith 9B, and Gemma E2B are explicit profiles. Switching models asks Ollama to unload the previous active chat model first.
- Gemma E4B+ and local weights above the default 16 GB-oriented profile remain blocked unless explicitly overridden.
- Ollama Cloud models remain visible while `privacy.local_only: true`. Manual selection asks for exact, conversation-only consent; automatic routing remains local.
- `LOCAL_AGENT_ALLOW_LARGE_MODELS=1` bypasses the size guard. Use it only after measuring memory headroom; it does not add memory isolation.

### Automatic and pinned models

The interactive TUI starts with automatic routing. Choosing a model or agent profile inside the TUI pins that model; `/model auto` releases the pin. Startup `--model` and profile model selections remain pinned in the TUI too.

```text
/model                     open the live Ollama inventory
/model list                list models currently admitted from Ollama
/model qwen3.5:4b          switch and pin this model
/model auto                release the pin and resume automatic routing
```

Selecting a model in the picker also pins it. Ollama's `/api/tags` inventory is the source of truth, including custom local models and Ollama Cloud aliases. The picker groups local, cloud, remote, and policy-blocked entries; `d` opens capabilities/runtime details and `a` opens the cancellable pull form. Static model configuration is retained only as routing preference metadata. Cloud is never selected automatically across the privacy boundary.

The `--qwen-router` CLI flag enables a more detailed Qwen-specific heuristic router and remains experimental.

## Operating modes

Cycle modes with `shift+tab`.

| Mode | Model behavior | Available tools |
|---|---|---|
| NORMAL | Routes for the interactive task | Read tools, writes, validated edits, shell, memory, and MCP; mutations remain approval-gated |
| PLAN | Sends ordinary prompts directly under a read-only host policy | Workspace reads, search, listing, diff, existence checks, and memory recall only |
| AUTO | Sends ordinary prompts directly with proactive tool routing | The NORMAL tool surface under the same approval policy; no blanket approval |

The mode policy is enforced by the host, not just by a prompt. A model-generated
mutation in PLAN is returned as blocked. `shift+tab` only changes authority; it
never opens a form or creates work. Ordinary prompts are sent immediately in all
three modes. AUTO is not YOLO: risky operations still follow the configured
approval policy. A durable bounded run is created only through
`/goal <duration> <prompt>` or `/goal new`, and an active goal is controlled
through the Goal Inspector instead of accepting an ordinary prompt that could
bypass its permit and budget.
Legacy ASK sessions restore as NORMAL. Legacy BUILD sessions restore as NORMAL
unless they already carry a durable goal, in which case they restore as AUTO.

Active `/goal` runs currently use the foreground Bubble Tea Goal Runtime. The
UI-independent `internal/supervisor` and `internal/workunit` packages are
validated scheduling contracts, not wired execution engines: there is no
headless `run --until-blocked`, queue, or parallel specialist process runner.

## Safety and privacy boundaries

The default configuration sets:

```yaml
privacy:
  local_only: true
```

With that setting, `local-agent`:

- Rejects Ollama URLs outside local-machine hosts (`localhost`, loopback IPs, and unspecified bind aliases).
- Rejects SSE and Streamable HTTP MCP URLs outside those local-machine hosts.
- Shows Ollama Cloud entries for explicit selection, asks before crossing the boundary, and excludes them from automatic routing.
- Canonicalizes built-in file paths, resolves symlinks, and rejects paths outside the startup workspace.
- Applies `.agentignore` to built-in file operations.
- Removes most parent-process environment variables before running the built-in shell tool.
- Starts STDIO MCP servers with a minimal environment and deterministic local executable lookup.

### Approval policy

The following operations require approval by default:

- `write`, `edit`, `bash`, `mkdir`, `remove`, `copy`, and `move`
- `memory_save`, `memory_update`, and `memory_delete`
- Every MCP tool call

The TUI shows the tool name and arguments. Respond with:

- `y` to allow once
- `n` to deny
- `a` to always allow that tool name; the policy is persisted in SQLite
- `esc` to deny and cancel the active turn

Read/search tools stay inside the workspace but do not prompt. “Always allow” is currently stored per tool name, not per path or argument pattern.

### What local-only does not guarantee

`privacy.local_only` validates configured network endpoints; it is not an egress firewall:

- An approved `bash` command can use absolute paths, leave the workspace, start subprocesses, or access the network.
- A trusted STDIO MCP server, including MCPHub or Cortex, is a separate process and may read files or contact services according to its own configuration.
- MCP tools can have side effects outside the repository.

Do not describe the current alpha as “data can never leave the machine” unless the agent and every approved subprocess are also running inside an OS/container network sandbox.

`--yolo` bypasses all approval prompts. In non-interactive `-p` mode, risky and MCP calls fail closed because there is no approval UI; add `--yolo` only for a trusted prompt in a disposable or well-versioned workspace.

## MCPHub, Cortex, and other MCP tools

`local-agent` is a generic MCP client. The recommended intelligence-stack setup is to expose Cortex, Obsidian, and other specialist servers through one local MCPHub process:

```yaml
servers:
  - name: mcphub
    command: mcphub
    args: [mcp, serve, --agent, local-agent]
```

Configure Cortex, Obsidian, and the rest of your catalog inside MCPHub using their own installation instructions. Then:

1. Start `local-agent`.
2. Check startup status or run `/servers`.
3. Use NORMAL for interactive MCP work, AUTO for proactive work, or `/goal` for a bounded durable run.
4. Review and approve each MCP call.

`local-agent` intentionally keeps Cortex orchestration behind MCPHub instead of embedding a second intelligence stack. Cortex analysis, investigation, and delegation appear as namespaced MCP tools. MCPHub owns lazy discovery, authentication, and downstream policy; local-agent owns the final user approval and transcript.

Every exposed MCP tool is namespaced as `<server>__<tool>`, results retain structured JSON, and media/resource blocks become bounded receipts instead of silently disappearing or flooding a small model with base64.

You can also configure direct servers:

```yaml
servers:
  - name: local-tools
    command: /absolute/path/to/mcp-server
    args: [serve]

  - name: local-http-tools
    transport: streamable-http
    url: http://127.0.0.1:8812/mcp
```

Supported transports are STDIO (default), SSE, and Streamable HTTP. Servers connect concurrently at startup; failed servers do not prevent the TUI from opening, and a background health monitor attempts reconnection.

## Configuration

Configuration is loaded from the first matching file:

1. `./local-agent.yaml`
2. `./local-agent.yml`
3. `$XDG_CONFIG_HOME/local-agent/config.yaml`
4. `$XDG_CONFIG_HOME/local-agent/config.yml`
5. `$HOME/.config/local-agent/config.yaml`
6. `$HOME/.config/local-agent/config.yml`

Repository-local configuration therefore has the highest precedence. The XDG
locations are considered when `XDG_CONFIG_HOME` is an absolute path; the
`$HOME/.config` locations remain the portable fallback. If XDG already points
to `$HOME/.config`, duplicate paths are checked only once. Files are not
merged: the first matching file is loaded, then environment overrides are
applied.

A compact configuration is:

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

# Disabled by default. Pull nomic-embed-text before enabling.
ice:
  enabled: false

servers: []
```

See [`config.example.yaml`](config.example.yaml) for the configured model catalog and MCP examples.

### Environment variables

| Variable | Purpose |
|---|---|
| `OLLAMA_HOST` | Override `ollama.base_url` |
| `LOCAL_AGENT_MODEL` | Override the initial Ollama model |
| `LOCAL_AGENT_AGENTS_DIR` | Override the agents directory |
| `LOCAL_AGENT_TOOLS_TIMEOUT` | Override the built-in tool timeout |
| `LOCAL_AGENT_TOOLS_MAX_GREP` | Override maximum grep results |
| `LOCAL_AGENT_TOOLS_MAX_ITER` | Override maximum ReAct iterations |
| `LOCAL_AGENT_ICE_EMBED_MODEL` | Override the ICE embedding model |
| `LOCAL_AGENT_LOCAL_ONLY` | Enable or disable local-machine endpoint enforcement |
| `LOCAL_AGENT_ALLOW_LARGE_MODELS` | Bypass the 16 GB-oriented model/context guard |
| `LOCAL_AGENT_REDUCED_MOTION` | Replace TUI spinners and the waiting shimmer with static activity glyphs |

## Project instructions, skills, and profiles

At startup, `local-agent` loads `./AGENTS.md`; if absent, it falls back to legacy `./AGENT.md`. `local-agent init` creates `AGENTS.md`.

Flat skill files live in:

```text
~/.config/local-agent/skills/*.md
```

Each skill may contain YAML frontmatter followed by instructions:

```markdown
---
name: go-review
description: Review Go changes for correctness and concurrency
---

Check cancellation, races, error handling, and tests.
```

Manage skills with `/skill list`, `/skill activate <name>`, and `/skill deactivate <name>`.

The global agent directory uses this layout:

```text
~/.agents/
  agents.md                 # global instructions; instructions.md is also accepted
  mcp.json                  # global MCP servers when config.yaml has none
  agents/
    reviewer/
      agent.yaml
  skills/
    go-review/
      SKILL.md
```

Example `~/.agents/agents/reviewer/agent.yaml`:

```yaml
name: reviewer
description: Read-only Go reviewer
model: qwen3.5:4b
skills: [go-review]
system_prompt: |
  Focus on correctness, security, concurrency, and missing tests.
```

Switch with `/agent reviewer`. A profile model is pinned until `/model auto`. `mcp_servers` restricts the model-visible and executable MCP surface to named connected servers; an empty list keeps all configured servers.

## Optional memory and ICE

The local memory store is available even when ICE is disabled. It is keyed by canonical workspace, uses owner-only files with interprocess locking and coherent reloads, and fails closed on corrupt data. NORMAL and AUTO expose explicit memory save/update/delete tools, while PLAN exposes recall.

Pre-workspace global memories and ICE entries have no trustworthy project provenance. They remain quarantined, are never attributed to the current repository, and do not add maintenance noise to normal interactive or headless startup.

ICE is opt-in:

```yaml
ice:
  enabled: true
  embed_model: nomic-embed-text
  # Optional: resolved below managed per-workspace user storage.
  # Absolute paths and parent traversal are rejected.
  # store_path: conversations.json
```

When enabled, ICE:

- Embeds conversation messages with Ollama.
- Retrieves similar messages from prior sessions.
- Injects retrieved conversations and matching memories into the prompt.
- Runs background extraction for facts, decisions, preferences, and TODOs after completed turns.

Current storage locations:

```text
~/.config/local-agent/conversations.json  # ICE entries; every entry carries a workspace ID
~/.config/local-agent/memory/<hash>.json  # workspace-scoped structured memories
~/.config/local-agent/local-agent.db      # sessions, permissions, checkpoints, usage
~/.config/local-agent/logs/               # structured session logs
```

Leaving `ice.store_path` empty uses the managed global ICE file shown above.
An explicit relative value is confined beneath
`~/.config/local-agent/ice/<workspace-hash>`; it cannot select an arbitrary
repository directory, enter the Git worktree, or target an outside path.

ICE is still a flat JSON vector store rather than an ANN index, but its bounded writes and reads are interprocess-coherent and retrieval is restricted to the same canonical workspace. Background auto-memory is single-flight, cancelled when foreground inference starts, joined at shutdown, and writes only to that workspace's memory store. Automatic extraction itself does not present a second approval prompt.

## CLI reference

| Command | Description |
|---|---|
| `local-agent` | Open the TUI |
| `local-agent -p "prompt"` | Run one user-directed NORMAL prompt and print text to stdout |
| `local-agent --mode plan -p "prompt"` | Run one read-only PLAN prompt; mutation tools are not exposed |
| `local-agent --mode auto -p "prompt"` | Run one proactive AUTO prompt under the configured approval policy |
| `local-agent --model <name>` | Select the initial model; in headless mode this prevents auto-routing |
| `local-agent --agent <name>` | Select an initial agent profile |
| `local-agent --qwen-router` | Use the experimental Qwen-specific router |
| `local-agent --yolo -p "prompt"` | Headless execution with every tool auto-approved |
| `local-agent init [--force]` | Create a project `AGENTS.md` |
| `local-agent logs` | List recent log files |
| `local-agent logs -f` | Follow the latest log with `tail -f` |
| `local-agent goal list [--limit 20] [--json]` | List validated durable goals in the current workspace without resuming them |
| `local-agent goal show [--json] <session-id>` | Inspect one complete validated goal snapshot |
| `local-agent goal pending [--limit 20] [--json] <session-id>` | Inspect unresolved decisions, approvals, and recovery items |
| `local-agent goal recover [--json] <session-id>` | Dry-run an existing validated reconciliation group without creating or changing it |
| `local-agent goal recover --apply --item ID --observation VALUE --source VALUE --reference TEXT --summary TEXT --observed-at RFC3339 [--json] <session-id>` | Append exact typed recovery evidence through the shared atomic coordinator |
| `local-agent --version` | Print the build version |

Source builds print `dev`. Tagged release artifacts print the tag version
(for example, `0.4.0`), and MCP client handshakes advertise that same build
version.

`-p` is currently a human-readable convenience mode, not a stable JSON automation protocol.

`goal list`, `goal show`, `goal pending`, and the default `goal recover` dry run
are read-only. Recovery mutation requires the complete explicit `--apply`
form, acquires the exact session/workspace lease, and accepts only a member
conclusion (`effect_applied`, `effect_not_applied`, or `effect_compensated`) or
the turn-parent conclusion `turn_abandoned_after_inspection`. Evidence sources
are `external_receipt`, `workspace_artifact`, `verification_check`, and
`operator_observation`. The timestamp and all evidence fields participate in
exact replay identity; changed evidence conflicts. There is no force escape
hatch, and a successful recovery ends in PAUSED or EXHAUSTED without resuming
provider work. The durable `deferred_approval` record type is implemented in
the store, but foreground approval prompts do not currently enqueue that type.

## Slash commands

| Command | Description |
|---|---|
| `/help` | Open help |
| `/clear`, `/new` | Clear conversation state |
| `/model` or `/models` | Open the model picker |
| `/model list` | List admitted models from the live Ollama inventory |
| `/model <name>` | Switch and pin an available Ollama model |
| `/model auto` | Resume automatic model routing |
| `/agent [name\|list]` | List or switch profiles |
| `/load <path>`, `/unload` | Asynchronously add or remove one regular, non-symlink markdown context file (32 KB maximum); quoted paths are supported |
| `/skill [list\|activate\|deactivate]` | Manage skills |
| `/servers` | Show connected MCP servers and tool count |
| `/ice` | Show ICE status |
| `/sessions` | Open lossless SQLite-backed saved sessions |
| `/goal <duration> <prompt>` | Infer bounded criteria and start a concrete goal with that wall-time cap; ambiguity asks one follow-up |
| `/goal [new [objective]]` | Open the reviewed form for a durable, budgeted goal |
| `/goal show` | Show objective, acceptance criteria, usage, state, and Cortex linkage |
| `/goal pause`, `/goal resume` | Stop automatic continuation or explicitly resume one user-directed turn |
| `/goal budget` | Change automatic-continuation, evaluation-token, and wall-time limits without editing the goal definition |
| `/goal drop` | Abandon the goal without claiming completion |
| `/changes` | List files modified in the current TUI session |
| `/commit [context]` | Generate a message from staged changes and run `git commit` |
| `/stats` | Show in-memory token counters |
| `/export [--force] <path>`, `/import <path>` | Atomically export owner-private Markdown with a typed v2 transcript envelope, or asynchronously import that envelope into a fresh session; replacement requires `--force`, and tool state is intentionally omitted |
| `/checkpoint [label]` | Save the current agent message history to SQLite |
| `/checkpoints` | List checkpoints |
| `/restore <id>` | Replace agent history with a checkpoint |
| `/exit` | Quit |

`/commit` deliberately disables Git hooks, commit signing, configured
fsmonitor helpers, pagers, and automatic maintenance/GC for its owned Git
subprocesses. It still uses your Git identity and other non-executing
configuration. Run `git commit` yourself when repository hooks or signing are
required.

Session snapshots preserve model-facing messages, tool-call IDs, tool cards, mode, model, profile, and counters. Loading one replaces both the visible transcript and the hidden model conversation. Checkpoints are validated against the active session.

### Durable goals and bounded continuation

`/goal <duration> <prompt>` is the compact path: it deterministically infers a
bounded objective and prompt-specific acceptance criteria, applies only the
explicit wall-time cap, and starts the first AUTO turn when the prompt names a
concrete target. Obvious ambiguity asks one contextual follow-up before any
runtime exists. `/goal new` opens the manual host-owned Goal Runtime review from
an empty or partial definition. Every definition requires an objective, at
least one independently checkable acceptance criterion, and at least one finite
limit. Later automatic turns are
admitted only after the previous turn produced a successful tool receipt and
the linked Cortex case advanced semantically. Each continuation permit is
saved with the exact agent TurnID before provider dispatch. The remaining
evaluation-token allowance is sent to every Ollama request as a hard generation
cap, and the remaining wall allowance becomes the turn context deadline; both
are rechecked before any later tool dispatch. Evaluation-token and wall-time
limits apply to the whole goal; the auto-turn limit applies only to
host-initiated continuations, not a new user-directed `/goal resume`.

Budget exhaustion pauses work; it never means success. A no-tool yield, failed
turn, cancellation, unavailable Cortex status, or persistence failure also
stops automatic continuation. If a process restarts with an admitted turn but
no settled receipt, the goal becomes outcome-unknown and cannot retry that
effect automatically. An otherwise active restored goal is paused until the
user resumes it. Goal definitions are immutable after creation; `/goal budget`
changes only limits.

`/goal show` opens the responsive Goal Inspector. It reports the objective,
honest criterion proof state, last settled turn, blocker and recovery reason,
Cortex revision, persistence health, and remaining budgets. Pause, Resume,
Budget, and Drop are derived from the same state-aware action metadata used by
slash completion and Help; unavailable actions show their reason, and Drop
requires confirmation.

When Cortex is reachable directly or through MCPHub, the runtime links one
stable Cortex case and asks for semantic status between productive turns.
Cortex receives each local acceptance ID and statement through its typed,
immutable `acceptanceCriteria` field; criteria are never embedded into free-form
goal prose.
Cortex's structured next action is bounded prompt context for the model—it is
never executed directly by the host and still passes through normal tool
policy and approval. Local Agent owns scheduling, budgets, cancellation,
session persistence, and the execution ledger. A goal reaches `completed` only
when the linked Cortex case is `complete` with a current canonical `verified`
assessment and no missing, stale, or degraded verification. Every local
acceptance ID and statement must have a matching bound named-claim receipt and
verifier receipt, and those receipts must match the host's current Git HEAD and
dirty-tree digest. The accepted commit, digest, and evidence references remain
in the durable completion record. Without Cortex, each bounded turn requires an
explicit user resume and the runtime deliberately cannot declare its own
completion.

The terminal interface keeps the conversation full-width. Infrequent controls
live in transient, keyboard-first overlays: press `ctrl+p` for session settings,
or keep using direct shortcuts and slash commands. Settings open focused child
overlays and `esc` returns to the settings root; overlays opened directly close
back to the conversation. Runtime status is scrollable when its diagnostics do
not fit on screen. At narrow or short sizes, Settings keeps one-line labels and
one selected-detail row so all controls remain scannable. Slash completion
shows canonical commands with descriptions while aliases remain searchable.
Active work uses one phase-specific animation with elapsed time and a visible
cancel affordance; live ToolCards own tool animation, and approval prompts pause
background motion until answered. Completed turns briefly show a stable receipt.
The supported minimum is 30 columns by 12 rows.

## Keyboard shortcuts

| Key | Action |
|---|---|
| `enter`, `shift+enter` | Send / insert a newline |
| `shift+tab` | Cycle NORMAL, PLAN, AUTO |
| `ctrl+p` | Open session settings (model, profile, mode, sessions, layout, runtime) |
| `ctrl+o` | Open Ollama model picker |
| `tab` | Complete commands, files, and skills |
| `up`, `down` | Browse input history |
| `pgup`, `pgdown`, `ctrl+u`, `ctrl+d` | Scroll conversation |
| `t`, `space` | Toggle all tool details / last tool |
| `ctrl+t` | Toggle `<think>` tag display |
| `ctrl+y` | Copy last response |
| `ctrl+e` | Edit input with `$EDITOR` |
| `ctrl+k` | Toggle compact mode |
| `esc` | Cancel active generation or close overlay; deny an active approval |
| `ctrl+n`, `ctrl+l` | New conversation / clear view |
| `ctrl+c` | Quit |

## Architecture

```text
Charm TUI or headless output
          |
          v
Agent ReAct loop -----> prompt builder + mode policy
    |     |                    |
    |     |                    +-- AGENTS.md, skills, loaded context, memory
    |     |
    |     +-- Tool policy -> permission checker -> built-ins / MCP registry
    |
    +-- Availability-aware ModelManager -> loopback Ollama
                                      |-> chat models
                                      +-> embedding model

Goal Runtime -> durable permits/budgets/receipts -> optional Cortex advisor
     |                                                |
     +-- owns continuation and cancellation           +-- returns semantic state/actions only

Local persistence: scoped JSON memory/ICE + SQLite sessions/permissions/checkpoints + logs
```

Package layout:

```text
cmd/local-agent/    CLI entry point and startup wiring
internal/agent/     ReAct loop, prompts, policies, tools, hooks, checkpoints
internal/llm/       Ollama client and model manager
internal/mcp/       MCP connections, registry, health checks, reconnects
internal/config/    YAML loading, model catalog, routing, agents, ignore rules
internal/ice/       Embeddings, retrieval, context budget, auto-memory
internal/memory/    Persistent structured memory
internal/db/        SQLite schema and queries
internal/skill/     Skill discovery and activation
internal/command/   Slash and custom commands
internal/goal/      Durable goal lifecycle, budgets, receipts, and recovery
internal/goaladvisor/ Bounded Cortex/MCPHub semantic adapter
internal/controlplane/ Append-only exception values and validation
internal/supervisor/ UI-independent scheduling decision contract (not yet wired)
internal/workunit/  Specialist scheduling/admission contract (does not spawn work)
internal/ui/        Charm terminal interface
internal/logging/   Per-run structured logs
```

Built-in, memory, and MCP calls execute deterministically in model order. The runtime does not parallelize unknown MCP effects; a future broker can opt proven read-only calls into bounded concurrency.

## Alpha limitations and roadmap

Known boundaries are documented here so the TUI does not promise more than the runtime provides:

- Ollama is the only implemented inference adapter. llama.cpp, MLX, and generic local OpenAI-compatible endpoints are not implemented.
- Model routing is heuristic and the memory guard is tuned for 16 GB Apple silicon rather than detected free memory.
- Small models can emit malformed or repetitive tool calls. Keep important work versioned and inspect every diff.
- `privacy.local_only` validates endpoints but does not sandbox approved shell or STDIO MCP processes.
- MCP support remains tool-focused; prompts, roots, subscriptions, sampling, and direct multimodal rendering are not yet exposed.
- ICE is workspace-scoped but remains a flat JSON scan rather than a scalable lexical/vector index such as the Cortex/VecLite stack.
- SQLite snapshots and the append-only execution ledger preserve completed state and tool-effect boundaries, but there is no first-class supervisor run/event repository or automatic continuation of in-flight execution after a crash.
- Outcome-reconciliation items now have manual evidence-entry and atomic TUI/CLI resolution workflows. Local Agent still cannot verify an unknown backend outcome automatically, repair a completed-but-unprojected effect automatically, or auto-resume after reconciliation.
- The supervisor and specialist work graph are safety-tested contracts only; headless run-until-blocked, queue/watch/resume controllers, durable evaluation-basis storage, and specialist process execution are not wired.
- Native Ollama reasoning and literal `<think>` tags are displayed separately, but thinking level is not yet configurable per model/profile.
- Headless mode has no structured event stream or granular approval protocol. Without `--yolo`, risky calls fail closed.
- There is no OS-level process, filesystem, or network sandbox yet.

The intended direction is a durable turn state machine and typed event stream, MCP effect metadata with bounded read-only concurrency, a measured RAM/resource scheduler, additional local runtime adapters, and an even stronger diff-first approval UI—while retaining the Go/Charm application.

## Development

```bash
task build              # bin/local-agent
task run                # build and launch
task dev                # go run ./cmd/local-agent
task test               # go test ./...
task lint               # golangci-lint run ./...
task verify             # Go verification plus production website build
task glyphrun           # terminal behavior specs
task glyphrun-snapshots # refresh intentional TUI snapshots
task site               # local documentation development server
task site:build         # production website build
task site:preview       # build and preview the production website
task clean
```

Run focused and race tests with:

```bash
go test ./internal/agent -run TestName
go test -race ./...
```

Glyphrun specs under `specs/glyphrun/` cover CLI help/version/init/log behavior,
goal-recovery help and fail-closed read-only/apply validation, the normal-width
launch, the 30×12 minimum, canonical command discovery, the durable-goal form
and safe local fallback, full-width narrow-terminal settings/help flow, and
clean quits.

With `qwen3.5:4b` installed in Ollama, run the opt-in live small-model/tool proof separately:

```bash
glyph run specs/glyphrun/live_ollama_tool.yml --format md
```

## License

MIT. See [`LICENSE`](LICENSE).
