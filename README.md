# local-agent

A local-first coding agent for the terminal, built in Go with Charm. It uses
local Ollama models by default and supports explicitly selected
OpenAI-compatible remote provider profiles.

[Website](https://local-agent.dev/) · [Getting started](https://local-agent.dev/getting-started) · [Safety](https://local-agent.dev/safety) · [Ecosystem](https://local-agent.dev/ecosystem) · [Expert teams](https://local-agent.dev/experts)

> **Status: alpha.** `local-agent` can inspect a repository, edit files, run commands, use MCP tools, and retain optional cross-session memory. Run it in a clean Git worktree, read every approval request, and review the resulting diff. The current safety layer is useful, but it is not an operating-system sandbox.

```text
  NORMAL -> interactive work; changes require approval
  PLAN   -> inspect and design without mutations
  AUTO   -> proactive work with routine workspace actions pre-authorized
```

<details>
<summary>Table of contents</summary>

- [What works today](#what-works-today)
- [Quick start](#quick-start)
- [Modes and authority](#modes-and-authority)
- [Models](#models)
- [CLI reference](#cli-reference)
- [Slash commands](#slash-commands)
- [Keyboard shortcuts](#keyboard-shortcuts)
- [Configuration](#configuration)
- [Environment variables](#environment-variables)
- [Project instructions, skills, and profiles](#project-instructions-skills-and-profiles)
- [MCP servers](#mcp-servers)
- [Expert teams](#expert-teams)
- [Sessions, memory, and ICE](#sessions-memory-and-ice)
- [Architecture](#architecture)
- [Alpha limitations and roadmap](#alpha-limitations-and-roadmap)
- [Development](#development)

</details>

## What works today

- A responsive terminal UI built with Bubble Tea v2, Bubbles v2, Lip Gloss v2, and Glamour.
- Streaming chat through Ollama with an availability-aware local model router,
  plus opt-in xAI and generic OpenAI-compatible provider profiles.
- Qwen 3.5, Phi-4 Mini, and manually selected Ornith/Gemma/Qwen exclusive profiles.
- Read, search, diff, validated patch, atomic write, file-management, and shell tools.
- NORMAL, PLAN, and AUTO authority with approval prompts for risky operations.
- STDIO, SSE, and Streamable HTTP MCP tool servers, including an MCPHub gateway.
- Contextual MCPHub routing with explicit confident, ambiguous, and no-match outcomes.
- Read-only Team, Swarm, and application-level MoE consultations whose concurrency adapts to host CPU and memory.
- Process-local read grants for an exact external file or directory, without widening write authority.
- Project instructions from `AGENTS.md` with legacy `AGENT.md` fallback.
- Lossless SQLite session resume, native reasoning display, skills, agent profiles, optional ICE retrieval, checkpoints, logs, and terminal behavior tests.

## Quick start

### Prerequisites

- [Go 1.25.12 or newer](https://go.dev/dl/) when installing from source
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

On macOS, install the release cask and launch:

```bash
brew install --cask abdul-hamid-achik/tap/local-agent
local-agent
```

Or install the latest tagged Go release and launch:

```bash
go install github.com/abdul-hamid-achik/local-agent/cmd/local-agent@latest
local-agent
```

Or run this checkout directly:

```bash
go run ./cmd/local-agent
```

Resume an exact saved session, or the newest one for the current canonical
workspace, when opening the interactive TUI:

```bash
local-agent --resume S42
local-agent --resume latest
```

`S42` is the short display handle for durable session ID `42`; either spelling
is accepted by session, goal, and recovery commands. Active sessions appear as
`S42 · title` in the Runtime view and session picker, and in the TUI footer when
the current responsive status layout has room. Titles come from the first
meaningful task text. `--resume` also accepts `latest` and cannot be combined
with headless `-p`/`--prompt`. Loading restores the saved database title and
conversation state after TUI startup; it does not submit a prompt or
automatically continue a durable goal.

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
| `phi4-mini:latest` | 2.5 GB | Alternative compact reasoning profile | No; exclusive |
| `qwen3.5:4b` | 3.4 GB | Preferred coding, debugging, review, and multi-step tools | Eligible |
| `qwen3.5:9b` | 6.6 GB | Deep manual profile | No; exclusive |
| `ornith:latest` | 5.6 GB | Agentic coding and independent deep verification | No; exclusive |
| `gemma4:e2b` | 7.2 GB | Alternative manual reasoning/tool profile | No; exclusive |

The shipped Phi profile is manual-only because its advertised Ollama metadata has not been backed by behavioral tool-use verification in this harness. Its size is not the reason for the restriction.

Pull whichever profiles you intend to use:

```bash
ollama pull qwen3.5:0.8b
ollama pull qwen3.5:2b
ollama pull qwen3.5:4b

# Manual-only pending behavioral tool verification:
ollama pull phi4-mini:latest

# Large manual exclusive profiles on a 16 GB machine:
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

The interactive TUI starts with automatic routing. Choosing a verified local model inside the TUI pins it and remembers that choice for the next process start in the owner-private `~/.config/local-agent/runtime-preferences.json`; `/model auto` releases the pin and clears the saved choice. An explicit startup `--model` or agent-profile model takes precedence. Cloud consent remains conversation-scoped and is never restored from the saved preference.

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
| PLAN | Sends ordinary prompts directly under a read-only host policy | Workspace reads, search, listing, diff, existence checks, memory recall, and advisory expert consultation only |
| AUTO | Sends ordinary prompts directly with proactive tool routing | The NORMAL tool surface; confined writes and catalogued local development commands proceed automatically, while dangerous or unknown effects remain gated |

The mode policy is enforced by the host, not just by a prompt. A model-generated
mutation in PLAN is returned as blocked. `shift+tab` only changes authority; it
never opens a form or creates work. Ordinary prompts are sent immediately in all
three modes. `/plan [task]` is the guided planning entrypoint: it switches to
PLAN, opens the inline Task/Scope/Focus review, and submits an
implementation-ready read-only planning request. It does not create a durable
goal. AUTO is autonomous for validated workspace writes, directory
creation, host-catalogued local MCP routes, and a static catalog of direct,
ordinary toolchain commands. Task runners, package `run` targets, raw `find`,
`rg`, and `grep` shell commands, and `go generate` remain approval-gated; the host-owned
read/list/grep tools provide ignore-aware repository inspection. AUTO still
asks before Git, deletion, dynamic shell expansion, file
redirection, external paths, network-facing or unknown commands, memory
mutation, human decisions, and uncatalogued MCP effects. AUTO uses a larger
bounded provider-loop budget (40 iterations by default) and does not emit the
interactive near-limit warning. A durable bounded run is created only through
`/goal <duration> <prompt>` or `/goal new`, and an active goal is controlled
through the Goal Inspector instead of accepting an ordinary prompt that could
bypass its permit and budget.

AUTO classifies the outer command and visible operands; it is not an OS
sandbox. Repository-owned build scripts, tests, generators, and hooks can run
code with the Local Agent process's filesystem and network access, so use AUTO
only with workspaces whose development commands you trust.
Raw Git remains approval-gated because repository configuration, filters, and
hooks can execute programs even during apparently read-only commands. Use
`/changes` for the host-owned change summary and `/commit` for the hardened
commit path.
Legacy ASK sessions restore as NORMAL. Legacy BUILD sessions restore as NORMAL
unless they already carry a durable goal, in which case they restore as AUTO.

When enabled, `consult_experts` is a read-only tool in every mode. It can run at
most one bounded Team, Swarm, or application-level MoE consultation per parent
turn, but child experts receive no filesystem, shell, memory, or MCP tools.
In a bounded turn, their evaluation usage shares the parent turn or Goal
budget. The parent agent retains all authority and must verify their advisory
reports.

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
- Canonicalizes built-in file paths, resolves symlinks, and requires a temporary exact-file or directory read grant outside the startup workspace.
- Applies host-owned secret exclusions plus `.agentignore` to built-in file operations and external directory grants. Repository negation cannot disable the host defaults; the host layer permits conventional template leaf names such as `.env.example`, while a repository may still exclude them explicitly.
- Removes most parent-process environment variables before running the built-in shell tool.
- Starts STDIO MCP servers with a minimal environment and deterministic local executable lookup.

### Approval policy

The following operations require approval in NORMAL by default:

- `write`, `edit`, `bash`, `mkdir`, `remove`, `copy`, and `move`
- `memory_save`, `memory_update`, and `memory_delete`
- Every MCP tool call

AUTO pre-authorizes the confined subset described under Operating modes. The
remaining risky or unknown requests still use the same inline approval surface.

The TUI replaces the composer with an inline permission surface while keeping
the transcript visible. It shows a bounded action preview, target or command,
and an inline diff for supported file changes. Respond with:

- `y` to allow once
- `n` to deny
- `s` to allow the identical request again during the current Agent process
- `d` to switch between the preview and exact arguments
- `esc` to cancel the approval and active turn

Read/search tools stay inside the workspace or an explicitly granted read scope
but do not prompt. The `s` grant is bound to the exact canonical arguments and
is not persisted across process restarts. There is no broad per-tool “always
allow” choice in the TUI.

### What local-only does not guarantee

`privacy.local_only` validates configured network endpoints; it is not an egress firewall:

- An approved `bash` command can use absolute paths, leave the workspace, start subprocesses, or access the network.
- A trusted STDIO MCP server, including MCPHub or Cortex, is a separate process and may read files or contact services according to its own configuration.
- MCP tools can have side effects outside the repository.

Do not describe the current alpha as “data can never leave the machine” unless the agent and every approved subprocess are also running inside an OS/container network sandbox.

`--skip-approvals` skips approval prompts, but explicit deny policies, host
validation, workspace/scope limits, privacy checks, tool preflight, and the
execution ledger still apply. In non-interactive `-p`/`--prompt` mode, requests
that need an approval fail closed by default because there is no approval UI.
Use the flag only for a trusted request in a disposable or well-versioned
workspace. `--yolo` remains a deprecated compatibility alias for
`--skip-approvals`.

## MCPHub, Cortex, and other MCP tools

`local-agent` is a generic MCP client. The recommended intelligence-stack setup is to expose Cortex, Obsidian, and other specialist servers through one local MCPHub process:

```yaml
servers:
  - name: mcphub
    command: mcphub
    args: [mcp, serve, --agent, local-agent]
    trust:
      local_owner: mcphub
      gateway: mcphub
      read_only:
        - mcphub_list_servers
        - mcphub_search_tools
        - mcphub_describe_tool
        - mcphub_resolve_tool
        - bob__bob_check
        - bob__bob_plan
      workspace_effectful:
        - cortex__cortex_investigate
        - cortex__cortex_plan
```

The `trust` block is host-owned, exact-route policy; see
[`config.example.yaml`](config.example.yaml) for the complete built-in Cortex,
Bob, and MCPHub compatibility catalog. It is accepted only for local STDIO when
`local_owner` exactly matches the executable basename. `read_only` routes are
auto-authorized and ledgered as reads. `workspace_effectful` routes are
auto-authorized only in AUTO when an explicit `workspace` argument resolves
inside the active workspace. Explicit
deny policy still wins, and MCP annotations never grant authority. Omit the
block to retain the exact build-owned migration profile for `mcphub`, `cortex`,
or `bob`; use `trust: {disabled: true}` to suppress that profile.

Configure Cortex, Obsidian, and the rest of your catalog inside MCPHub using their own installation instructions. Then:

1. Start `local-agent`.
2. Check startup status or run `/servers`.
3. Use NORMAL for interactive MCP work, AUTO for proactive work, or `/goal` for a bounded durable run.
4. Review each MCP call; routes outside explicit trust still use the normal
   approval path.

`local-agent` intentionally keeps Cortex orchestration behind MCPHub instead of embedding a second intelligence stack. Cortex analysis, investigation, and delegation appear as namespaced MCP tools. MCPHub owns lazy discovery, authentication, and downstream policy; local-agent owns the final user approval and transcript.

Every exposed MCP tool is namespaced as `<server>__<tool>`. Exact structured
output stays inside the agent parser boundary and is discarded after known
tool-specific interpretation rather than copied into transcript or saved-card
text. Known integrations produce bounded semantic projections; media and
resource blocks are likewise bounded instead of flooding a small model or
session state with base64.

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
    - qwen3.5:0.8b
    - qwen3.5:4b
  auto_select: true
  embed_model: nomic-embed-text

tools:
  timeout: 30s
  max_grep_results: 500
  max_iterations: 10
  auto_max_iterations: 40

continuations:
  mode: suggest # off | suggest | auto_read_only
  max_auto_steps: 2

# Read-only Team, Swarm, and application-level MoE consultation.
experts:
  enabled: true

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
| `LOCAL_AGENT_TOOLS_MAX_ITER` | Override maximum NORMAL/PLAN provider iterations |
| `LOCAL_AGENT_TOOLS_AUTO_MAX_ITER` | Override maximum AUTO provider iterations |
| `LOCAL_AGENT_CONTINUATIONS_MODE` | Set typed continuation handling to `off`, `suggest`, or `auto_read_only` |
| `LOCAL_AGENT_CONTINUATIONS_MAX_AUTO_STEPS` | Set the read-only auto-follow budget from 0 to the hard maximum of 2 |
| `LOCAL_AGENT_ICE_EMBED_MODEL` | Override the ICE embedding model |
| `LOCAL_AGENT_LOCAL_ONLY` | Enable or disable local-machine endpoint enforcement |
| `LOCAL_AGENT_ALLOW_LARGE_MODELS` | Bypass the 16 GB-oriented model/context guard |
| `LOCAL_AGENT_REDUCED_MOTION` | Replace TUI spinners and the waiting shimmer with static activity glyphs |
| `LOCAL_AGENT_GLYPHS` | Set glyph profile to `unicode` (default) or `ascii` |
| `LOCAL_AGENT_TRUST_REPO_MCP` | Trust repository-level MCP server definitions |
| `LOCAL_AGENT_PROVIDER` | Set the active provider profile name |
| `LOCAL_AGENT_PROVIDER_BASE_URL` | Override the active provider base URL |
| `LOCAL_AGENT_PROVIDER_MODEL` | Override the active provider model |
| `LOCAL_AGENT_PROVIDER_API_KEY_ENV` | Override the env var name for the provider API key |
| `LOCAL_AGENT_PROVIDER_CONTEXT_SIZE` | Override the provider context window size |

## Project instructions, skills, and profiles

At startup, `local-agent` loads `./AGENTS.md`; if absent, it falls back to legacy `./AGENT.md`. `local-agent init` creates `AGENTS.md`.

Global skills live under the selected shared agents directory, which defaults
to:

```text
~/.agents/skills/<name>/SKILL.md
```

Each `SKILL.md` may contain YAML frontmatter followed by instructions:

```markdown
---
name: go-review
description: Review Go changes for correctness and concurrency
---

Check cancellation, races, error handling, and tests.
```

Manage skills with `/skill list`, `/skill activate <name>`, and `/skill deactivate <name>`.
Skill names must be unique. Startup rejects ambiguous names, invalid YAML
frontmatter, files over 1 MiB, and symlinked skill files instead of silently
choosing or skipping one. `~/.config/local-agent` remains reserved for Local
Agent configuration and private runtime data; it is not searched for skills.
Set `agents.dir` or `LOCAL_AGENT_AGENTS_DIR` to select a different shared root.
The retired top-level `skills_dir` setting is rejected with migration guidance.

Inactive skills are exposed to the model as a bounded name-and-description
catalog. When one clearly matches the task, the agent can load that exact
skill on demand through a read-only built-in tool; the body is not added to
every prompt and automatic loading does not activate the skill globally.

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

Agent Skills in this directory use the same frontmatter format shown above.
Give each `SKILL.md` an explicit, unique `name`; Local Agent uses that declared
name for catalog lookup and profile activation.

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
~/.config/local-agent/conversations.json       # ICE entries; every entry carries a workspace ID
~/.config/local-agent/memory/<hash>.json       # workspace-scoped structured memories
~/.config/local-agent/local-agent.db           # sessions, permissions, checkpoints, usage
~/.config/local-agent/runtime-preferences.json # remembered local model pin
~/.config/local-agent/logs/                    # structured session logs
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
| `local-agent -p "prompt"`, `local-agent --prompt "prompt"` | Run one user-directed NORMAL prompt and print text to stdout |
| `local-agent --plan --prompt "prompt"` | Run one read-only PLAN prompt; equivalent to `--mode plan` |
| `local-agent --auto --prompt "prompt"` | Run one proactive AUTO prompt; equivalent to `--mode auto`, with routine confined workspace actions pre-authorized |
| `local-agent --tools read,diff --plan --prompt "prompt"` | Narrow one headless turn to the named built-in tools |
| `local-agent --model <name>` | Select the initial model; in headless mode this prevents auto-routing |
| `local-agent --agent <name>` | Select an initial agent profile |
| `local-agent --resume <S42\|42\|latest>` | Open the TUI and restore an exact or newest current-workspace session |
| `local-agent --qwen-router` | Use the experimental Qwen-specific router |
| `local-agent --skip-approvals` | Skip approval prompts while retaining explicit denies and host/tool boundaries |
| `local-agent --yolo` | Deprecated compatibility alias for `--skip-approvals` |
| `local-agent init [--force]` | Create a project `AGENTS.md` |
| `local-agent logs` | List recent log files |
| `local-agent logs -f` | Follow the latest log with `tail -f` |
| `local-agent goal open --objective TEXT [options]` | Create a bounded durable goal in the current workspace without running provider work |
| `local-agent goal run <session-id> --prompt TEXT [options]` | Run and durably settle one foreground turn for an existing headless goal |
| `local-agent goal list [--limit 20] [--json]` | List validated durable goals in the current workspace without resuming them |
| `local-agent goal show [--json] <session-id>` | Inspect one complete validated goal snapshot |
| `local-agent goal pending [--limit 20] [--json] <session-id>` | Inspect unresolved decisions, approvals, and recovery items |
| `local-agent goal recover [--json] <session-id>` | Dry-run an existing validated reconciliation group without creating or changing it |
| `local-agent goal recover --apply --item ID --observation VALUE --source VALUE --reference TEXT --summary TEXT --observed-at RFC3339 [--json] <session-id>` | Append exact typed recovery evidence through the shared atomic coordinator |
| `local-agent execution recover [--json] <session-id> <execution-id>` | Inspect one outcome-unknown execution in an ordinary session without retrying it |
| `local-agent execution recover [--json] <session-id> --all` | Inspect the complete bounded pending-reconciliation set and its exact set digest |
| `local-agent session list [--json] [--limit N]` | List current-workspace sessions with short handles and titles |
| `local-agent session export [--format jsonl\|md\|both] [--out DIR] <S42\|42>` | Export one bounded session audit projection |
| `local-agent session repair [--json] <S42\|42>` | Repair one session projection from its durable execution ledger |
| `local-agent --version` | Print the build version |

Source builds print `dev`. Tagged release artifacts print the tag version
(for example, `0.19.0`), and MCP client handshakes advertise that same build
version.

`-p` and `--prompt` are exact aliases for a human-readable convenience mode,
not a stable JSON automation protocol. `--auto` and `--plan` require a
non-empty prompt and are mutually exclusive. Passing an explicit empty or
whitespace-only prompt exits with status 2 before configuration, network, or
provider initialization.

`--tools` accepts an exact comma-separated list only with a headless prompt.
It can select only built-in tools already permitted by the chosen mode and
replaces that turn's tool surface with the requested subset. Memory tools and
MCP routes are unavailable for the narrowed turn, so this flag cannot grant
additional authority.

`goal open` creates only durable state. `goal run` restores that exact state,
records an admission before provider dispatch, runs one AUTO-authority turn, and
stores the resulting receipt and conversation before exiting. It runs in the
foreground, explicitly resumes a paused goal when requested, and does not create
a daemon or automatically schedule another turn.
Use `--skip-approvals` only when you intend to suppress interactive approval
prompts; explicit denies and host/tool boundaries still apply.

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
Ordinary sessions without a durable goal use `execution recover`; its default
inspection is read-only, while applying evidence requires the exact revision,
event ID, typed observation, source, reference, summary, and timestamp printed
by its help. `execution recover SESSION_ID --all` inspects up to 100 pending
executions together and prints a digest for that exact reviewed set. Batch apply
requires `--all --apply --set-digest HASH` plus the same complete typed evidence;
it aborts without recording anything if the set changed. Neither form retries
the original tool.

`session export` writes `session-N.jsonl` and `session-N-summary.md` by default
under `./local-agent-audit-N/`. Its bounded Open Issues projection names each
unresolved or unprojected execution and the exact recovery or repair command.
Exports can contain session content, receipt detail, and paths, so review them
before sharing. `session repair` is only for terminal ledger effects newer than
the saved transcript: close the TUI first, reconcile every outcome-unknown
execution, then run repair under the exclusive session lease. Repair never
retries a tool or rewrites the immutable ledger, and goal-owned sessions use the
separate `goal show`/`goal recover` flow.

## Slash commands

| Command | Description |
|---|---|
| `/help` | Open help |
| `/clear`, `/new` | Clear conversation state |
| `/plan [task]` | Enter PLAN and open the guided read-only Task/Scope/Focus form, optionally prefilled |
| `/model` or `/models` | Open the model picker |
| `/model list` | List admitted models from the live Ollama inventory |
| `/model <name>` | Switch and pin an available Ollama model |
| `/model auto` | Resume automatic model routing |
| `/provider`, `/provider list` | Open the provider picker or list configured provider profiles |
| `/provider <name>` | Switch provider profile and persist that preference for restart |
| `/agent [name\|list]` | List or switch profiles |
| `/load <path>`, `/unload` | Asynchronously add or remove one regular, non-symlink markdown context file (32 KB maximum); quoted paths are supported |
| `/image <path>`, `/attach <path>` | Validate a PNG, JPEG, or GIF and attach it to the pending ordinary prompt |
| `/image list`, `/image clear` | Inspect or remove images attached to the pending prompt |
| `/image forget-history` | Remove historical image references when old image context is no longer needed or its private object is unavailable; pending images and cached objects remain |
| `/scope [list\|add-read <directory>\|remove-read <path>\|clear-read]` | Manage process-local read-only access outside the writable workspace; exact files are proposed automatically when referenced in a prompt |
| `/skill`, `/skill list` | List discovered skills and their activation state |
| `/skill activate <name>`, `/skill deactivate <name>` | Add or remove one skill from active prompt context |
| `/servers` | Show connected MCP servers and tool count |
| `/ice` | Show ICE status |
| `/sessions`, `/resume` | Open the lossless SQLite-backed session picker; neither command accepts an ID argument |
| `/artifacts`, `/artifact` | List bounded file.cheap stash receipts saved in the current session |
| `/goal <duration> <prompt>` | Infer bounded criteria and start a concrete goal with that wall-time cap; ambiguity asks one follow-up |
| `/goal [objective]`, `/goal new [objective]` | Open the inline reviewed form, optionally prefilled; bare `/goal` shows an existing goal instead |
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
| `/recover` | Review the current session's outcome-unknown execution and record typed evidence |
| `/exit` | Quit |

Slash commands use a small argument parser, not a shell. Single or double
quotes and backslash-escaped whitespace can group an argument, while environment
variables and command substitutions remain literal. `/load`, `/image`, `/scope`,
`/import`, and `/export` separately expand a leading `~/`. An unterminated quote is rejected
before the command runs. Documented arity is enforced: commands with no
arguments and `/goal show`, `/goal pause`, `/goal resume`, `/goal budget`, or
`/goal drop` reject trailing fields, while `/restore` accepts exactly one
canonical positive decimal ID.

`/commit` deliberately disables Git hooks, commit signing, configured
fsmonitor helpers, pagers, and automatic maintenance/GC for its owned Git
subprocesses. It still uses your Git identity and other non-executing
configuration. Run `git commit` yourself when repository hooks or signing are
required.

Session snapshots preserve model-facing messages, tool-call IDs, tool cards, mode, model, profile, counters, and bounded artifact receipts. Loading one replaces both the visible transcript and the hidden model conversation. Checkpoints are validated against the active session. `/artifacts` shows only host-normalized stash URIs, counts, timestamps, hashes, and static warning flags; raw file.cheap manifests, paths, and provider prose do not enter session state.

After a clean interactive exit, Local Agent restores the terminal and prints an
exact `local-agent --resume S…` command for the active durable session. It does
not print a command for an unsaved conversation or after a failed TUI run.

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
Cortex's or Bob's exact structured next action is normalized into bounded,
host-owned context. The default `suggest` mode shows it to the model and UI
without executing it. Optional `auto_read_only` may follow at most two exact,
fully specified, unblocked, registry-validated read-only actions; shell,
mutation, secreted execution, and unresolved gateway proxies never qualify.
Every eligible call still passes current route trust, schema, workspace,
permission, ledger, replay, and staleness checks. Local Agent owns scheduling,
budgets, cancellation,
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
The supported minimum is 30 columns by 12 rows. Compact status rows preserve
skipped-approval, unavailable-MCP, and Cloud/Remote boundaries; minimum-width
file approvals keep an identifying target tail plus paging and exact-argument
controls.

The composer grows with visually wrapped text as you type, up to a bounded
terminal-height-aware limit, then scrolls internally while keeping the cursor
region visible. Ordinary bracketed text paste follows the same layout;
large text pastes retain their existing review step. The mouse wheel scrolls the
conversation without moving the composer. While mouse reporting is active, use
your terminal's selection override (commonly Shift-drag) for native selection
and copy; `ctrl+y` remains available for copying the last response.

Attach an image with `/image <path>` or its `/attach <path>` alias. You can also
paste or drag one file path or a complete space/newline-separated list of PNG,
JPEG, or GIF paths when the terminal inserts quoted, shell-escaped, or
`file://` text. Mixed prose remains ordinary draft text. Local Agent decodes
and validates every complete file before attaching it in order; each ordinary
prompt accepts up to four images. Use `/image list` to inspect pending attachments and `/image clear` to
remove them. The active conversation is bounded to 12 image references, 40 MiB,
and 48 million decoded pixels in aggregate so repeated vision turns cannot grow
provider-request memory without limit.

Each image can be at most 20 MiB, 16,384 pixels on either side, and 24 million
pixels in total. Validated bytes are copied into the owner-private,
content-addressed store at `~/.config/local-agent/images/`. Session and
checkpoint JSON retain only a sanitized basename, MIME type, size, dimensions,
and full SHA-256 reference—never the source path or raw image bytes. Restored
turns resolve that reference from the private store and verify its digest before
provider dispatch.

An unpinned model switches to an admitted, auto-routable vision-capable Ollama
model before the turn; a manual-only Cloud model is never selected implicitly.
A pinned model that does not advertise vision is rejected locally so no provider
request starts. On macOS, explicit `Ctrl+V` also reads a PNG image directly from
the system pasteboard into the same private store. Bracketed terminal paste and
other platforms can attach a saved image by dragging it or pasting its file path.

If an older private image object was deleted or its context is no longer useful,
run `/image forget-history`. Local Agent removes the historical references and
their visible attachment badges together and saves that repair to the active
session. Pending prompt images and private cached objects are intentionally left
unchanged. Existing checkpoints are immutable snapshots and may restore their
references later; delete the referenced private objects separately when actual
data erasure is required.

## Keyboard shortcuts

| Key | Action |
|---|---|
| `enter` | Send the prompt, or queue one follow-up while a turn is running |
| `shift+enter`, `ctrl+j`, `alt+enter` | Insert a newline (`ctrl+j` is the fallback for terminals that cannot distinguish Shift+Enter) |
| `shift+tab` | Cycle NORMAL, PLAN, AUTO |
| `ctrl+p` | Open session settings (model, profile, mode, sessions, layout, runtime) |
| `ctrl+o` | Open Ollama model picker |
| `ctrl+g` | Open the Agent Hub |
| `ctrl+f` | Search the bounded safe transcript projection |
| `f1`, `ctrl+h` | Open Help when the composer is empty (`f1` is unambiguous on legacy terminals) |
| `tab` | Complete commands, files, and skills |
| `up`, `down` | Browse input history |
| `pgup`, `pgdown` | Scroll conversation without editing the draft |
| `ctrl+u`, `ctrl+d` | Edit the draft; with an empty or unavailable composer, scroll by half a page |
| `ctrl+b`, `ctrl+r` | Toggle all tool details / the focused or latest tool |
| `alt+o`, `alt+d` | Open retained output / diff for the inspected tool when available |
| `ctrl+t` | Toggle `<think>` tag display |
| `ctrl+y` | Copy last response |
| `ctrl+e` | Edit input with `$VISUAL`, then `$EDITOR` |
| `ctrl+k` | Toggle compact mode |
| `esc` | Close an overlay or inline form, cancel an approval, or cancel active generation |
| `ctrl+n`, `ctrl+l` | New conversation / clear view |
| `ctrl+c` | Quit |
| `end` | Jump to latest output (empty input) |

Transcript search never indexes private model reasoning, raw tool
arguments/results, or raw MCP structured content. Closing it restores the
existing draft and reading position.

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
    +-- Expert selector -> resource plan -> tool-free advisory inference
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
internal/expertselector/ Deterministic local Team, Swarm, and MoE selection
internal/expertteam/ Bounded read-only expert inference runtime
internal/resource/  Host resource probe and conservative concurrency planner
internal/controlplane/ Append-only exception values and validation
internal/supervisor/ UI-independent scheduling decision contract (not yet wired)
internal/workunit/  Specialist scheduling/admission contract (does not spawn work)
internal/ui/        Charm terminal interface
internal/logging/   Per-run structured logs
```

Additional utility packages (`internal/`) provide path resolution, network policy, safe I/O, execution ledger, permission policy, reconciliation, session references, image assets, and documentation link checking.

Top-level built-in, memory, and MCP calls execute deterministically in model order. The runtime does not parallelize unknown MCP effects. `consult_experts` is the bounded exception inside one read-only tool call: its tool-free inference reports may run concurrently under the host resource plan.

## Alpha limitations and roadmap

Known boundaries are documented here so the TUI does not promise more than the runtime provides:

- Ollama is the default inference adapter. Optional OpenAI-compatible remote chat (including xAI Grok) is supported via `provider` config and env-sourced API keys (prefer TinyVault `tvault run --only …`). SuperGrok OAuth is not implemented.
- Model routing remains heuristic. Expert fan-out uses measured CPU and memory, including process-visible Linux cgroup limits, where the host exposes them; it has no portable discrete-VRAM or model-specific KV-allocation telemetry. General model admission still uses the conservative 16 GB-oriented guard.
- Small models can emit malformed or repetitive tool calls. Keep important work versioned and inspect every diff.
- `privacy.local_only` validates endpoints but does not sandbox approved shell or STDIO MCP processes.
- MCP support remains tool-focused; prompts, roots, subscriptions, sampling, and direct multimodal rendering are not yet exposed.
- ICE is workspace-scoped but remains a flat JSON scan rather than a scalable lexical/vector index such as the Cortex/VecLite stack.
- SQLite snapshots and the append-only execution ledger preserve completed state and tool-effect boundaries, but there is no first-class supervisor run/event repository or automatic continuation of in-flight execution after a crash.
- Outcome-reconciliation items now have manual evidence-entry and atomic TUI/CLI resolution workflows. Local Agent still cannot verify an unknown backend outcome automatically, repair a completed-but-unprojected effect automatically, or auto-resume after reconciliation.
- The supervisor and durable specialist work graph remain safety-tested contracts only; headless run-until-blocked, queue/watch/resume controllers, durable evaluation-basis storage, and specialist process execution are not wired. The separate expert runtime is transient, read-only inference consultation rather than durable work-unit execution.
- Native Ollama reasoning and literal `<think>` tags are displayed separately, but thinking level is not yet configurable per model/profile.
- Headless mode has no structured event stream or granular approval protocol. Without `--skip-approvals`, requests that need approval fail closed by default.
- There is no OS-level process, filesystem, or network sandbox yet.

The intended direction is a durable turn state machine and typed event stream, MCP effect metadata with bounded read-only concurrency, additional local runtime adapters, and an even stronger diff-first approval UI—while retaining the Go/Charm application.

## Development

```bash
task build              # bin/local-agent
task run                # build and launch
task dev                # go run ./cmd/local-agent
task test               # go test ./...
task lint               # golangci-lint run ./...
task verify             # Go verification plus production website build
task test:integration   # optional integration-tag suite; live dependencies self-skip
task glyphrun-contracts  # verify every committed spec contract hash
task glyphrun-cli        # fast release-critical terminal contracts
task glyphrun           # complete deterministic terminal suite
task glyphrun-snapshots # refresh intentional TUI snapshots
task eval               # repeat the opt-in live qwen3.5:2b tool smoke
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

Glyphrun specs under `specs/` carry verified contract hashes and cover CLI
help/version/init/log behavior, headless authority aliases, exact external-file
read review, goal recovery, the normal-width launch, the 30×12 minimum,
canonical command discovery, model and approval flows, saved-session receipts,
and clean quits.

With a running local Ollama and the documented default `qwen3.5:2b` already installed, run the opt-in live constrained-model/tool smoke separately:

```bash
task eval                         # three repetitions by default
EVAL_REPEATS=5 task eval          # explicit stability sample
```

This is a narrow tool-call stability smoke, not a repository task-completion
benchmark. It checks the configured `OLLAMA_HOST` for an already-installed
`qwen3.5:2b`, exits if the model is unavailable, and never runs `ollama pull` or
downloads weights. It remains outside hosted CI.

## License

MIT. See [`LICENSE`](LICENSE).
