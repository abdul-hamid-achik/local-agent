# local-agent

A local coding harness built with Bun, TypeScript and Vue terminal components.

**Experimental migration:** the Go implementation has been removed from this
working tree at the project owner's request. This does not mean feature parity
is complete. Historical tagged releases remain separate from the Bun client.

The current client supports fullscreen panels, inline native terminal scrollback,
headless JSON events, local Ollama and Chalupa process profiles, bounded file
reads, approved exact edits and commands, MCP tools, and optional SQLite sessions.
Recovery reconciliation, durable goals/memory, the full tool catalog and standalone
distribution remain incomplete. Root `AGENTS.md` is loaded; full skills/profile
and nested-instruction parity is still pending.

The [Go implementation reference](reference/GO_IMPLEMENTATION.md) preserves the
historical feature manual, implementation details, source revision, and complete
file/symbol/test inventory. Public documentation at [local-agent.dev](https://local-agent.dev)
continues to describe historical released functionality and labels that scope.

## Development and usage

Use Bun **1.4.2**. The workspace pins TypeScript **7.0.2**, Vue **3.5.42**,
Zod **4.5.4**, Vue TermUI **0.3.0**, OpenTUI **0.5.11**, Vitest **5.0.0**,
and Biome **2.5.12**.

| Workspace | Responsibility |
| --- | --- |
| `apps/cli` | Application composition and the three presentation modes |
| `apps/tui-playground` | Model-free `.vue` component playground, development reload and template checking |
| `packages/runtime` | Provider admission, cancellable streaming, serial tool loop and workspace operations; no Vue dependency |
| `packages/vue-tui` | Internal terminal components and layout on Vue TermUI/OpenTUI, with no agent dependencies |
| `docs` | Existing public VitePress website, in its original location |

```bash
bun install --frozen-lockfile
bun run check                         # lint, TS 7, tests, CLI build
bun run dev                           # fullscreen, with the default model (see below)
bun run dev --mode inline
bun run dev --mode headless -p "Hello" --json
bun run dev --model OTHER_MODEL       # any installed Ollama model with tools
bun run build
bun run start --help
bun run site:build
bun run tui:dev                       # Vue SFC playground, no model required
bun run test:integration              # requires Glyphrun v0.20.0
```

## Local model

With the local Ollama provider and neither `--model` nor `LOCAL_AGENT_MODEL`,
local-agent uses **`qwen3.5:4b-q4_K_M`**. It is the smallest model that completed
every task of a six-task coding battery (read and explain, find with grep, fix a
bug, add and run tests, a chat question, a rename across files) in all three
repetitions on a 16 GB laptop, at about 3.7 GB of memory and 30 to 40 tokens per
second. The `-q4_K_M` tags run on llama.cpp; the default `qwen3.5:4b` tag runs on
Ollama's MLX runner, whose reported memory kept growing during a session.

```bash
ollama pull qwen3.5:4b-q4_K_M
bun run dev
```

If the model is missing, the client stops before it starts with
`Model qwen3.5:4b-q4_K_M is not installed. Run: ollama pull qwen3.5:4b-q4_K_M`.
The Chalupa provider keeps the model of its profile.

| Option | Model | Notes |
| --- | --- | --- |
| Default | `qwen3.5:4b-q4_K_M` | 3.3 GB download |
| Lighter | `qwen3.5:2b-q4_K_M` with `--think off` | 1.9 GB; much faster, but weaker on multi-step work. With thinking on it often loops, so turn it off |
| Stronger | `ornith-1.5:9b` | 6.6 GB download, about 21 tokens per second; close other applications first on a 16 GB machine |

Flags and variables (a flag wins over its variable):

| Flag | Variable | Meaning |
| --- | --- | --- |
| `--model MODEL` | `LOCAL_AGENT_MODEL` | Ollama model; Chalupa rejects a different one |
| `--think on\|off` | `LOCAL_AGENT_THINK=1\|0` | Sent to Ollama as `think`, only when set; unset keeps the model's default. Applies to Chalupa too |
| `--context N` | `LOCAL_AGENT_CONTEXT` | Context tokens for local Ollama (`32768` or `32k`; default 16384). It cannot exceed the model's native context. Chalupa negotiates its own, so `--context` is refused there and the variable ignored |

Thinking streams as its own `thinking` event, apart from the answer. The Tuimark
client shows a dim "Thinking…" block with the last three lines while it streams
and removes it when the answer starts; the Vue client shows "Thinking…" in its
status line; headless `--json` emits the events. Reasoning is never printed into
the transcript. The `done` event also carries Ollama's `promptEvalCount`,
`promptEvalDuration`, `evalCount`, `evalDuration` and `totalDuration` (durations in
nanoseconds) when reported, and the status line shows `N tok/s` after a turn.

### Tools

Paths are relative to the workspace root (`src/main.ts`). An absolute path inside
the workspace is accepted and shown in its relative form; any other absolute path
is rejected with the relative path it probably meant. `..` and symbolic links out
of the workspace stay forbidden.

| Tool | Approval | What it does |
| --- | --- | --- |
| `read` | none | Read a UTF-8 file |
| `list` | none | List a directory (directories end with `/`; `.git` and `node_modules` skipped; 500 entries) |
| `search` | none | Literal or regular-expression search over workspace text files as `path:line: text` (100 matches; binaries, `.git`, `node_modules` and secrets skipped) |
| `edit` | `--approve-edits` | Replace one exact occurrence in an existing file |
| `write` | `--approve-edits` | Create a file (creating directories) or replace one; the approval shows the content as added lines, or the diff for a replacement |
| `run_command` | `--approve-commands` | Run one program from an `argv` array, with no shell |

`run_command` takes `{"argv": ["bun", "test", "tests/a.test.ts"]}`. If a model sends
a string instead it is split with a quote-aware splitter; a string that needs a shell
(`|`, `&&`, `;`, `>`, `<`, `$(`, backticks) is refused with a message that there is
no shell. The approval shows the parsed command as one shell-quoted line. The
system prompt lists which of `bun`, `node`, `npm`, `python3`, `go`, `cargo` and
`git` are on `PATH`. MCP tools are named `SERVER__TOOL` and are handled as before.

### Ornith on Chalupa

For `ornith-1.5:9b` (or `35b`) on a rented GPU, Chalupa places the model and opens
the tunnel; local-agent reads its process profile (`LOCAL_AGENT_PROVIDER=chalupa`,
`LOCAL_AGENT_PROVIDER_CONFIG`, `LOCAL_AGENT_LOCAL_ONLY=false`) and takes the model,
endpoint and context from it. Do not pass `--model` or `--context` on this route.
**`chalupa up` costs money until `chalupa down`**; check the plan with `--dry-run`
first.

```bash
chalupa up --model ornith-1.5:9b --tier gpu-small --context 64k --expires-after 45
```

Then run local-agent in one of two ways:

```bash
# From a checkout of this repository
chalupa exec --context 64k -- bun run apps/cli/src/main.ts --ui tuimark

# Or, when a `local-agent` command is on PATH
chalupa launch local-agent -- --ui tuimark
```

This repository does not install a `local-agent` command (the CLI build is not a
standalone release), so for the second route put a small wrapper on your `PATH`
yourself:

```bash
printf '#!/bin/sh\nexec bun run %s/apps/cli/src/main.ts "$@"\n' "$PWD" > ~/.local/bin/local-agent
chmod +x ~/.local/bin/local-agent
```

When you are done, stop the billing:

```bash
chalupa down
```

Workspaces and scripts use [Bun's native workspace support](https://bun.com/docs/pm/workspaces)
with isolated dependencies and one lockfile. There is no extra task orchestrator.
Internal aliases live in each package's `imports` map: `#cli/*`, `#runtime/*`
and `#tui/*` (`#playground/*` in the SFC demo). Cross-package imports use `@local-agent/*` public exports.
This keeps runtime and typechecker resolution aligned without duplicated aliases.
Unit/component tests run through Vitest with Bun (`bun run test`), with
`bun run test:watch` for development. Do not invoke them with `bun test`.
The CLI build is for this installed workspace, not a standalone release binary.

In fullscreen, assistant messages render Markdown with separate role labels.
Inline keeps streamed plain text in native terminal scrollback. In either
interactive presentation, use `/help`, `/mode plan`, `/mode build`, and `/mcp`.
Mode changes apply to the next turn; they do not alter a running tool call.

Use `--workspace PATH` to select the file root and `--intent plan` to forbid
edits, writes and commands. Build intent asks for each edit or command in interactive modes; Enter denies and
`y` approves. Headless edits and writes are denied unless `--approve-edits` is explicit.
This flag permits file changes only; it grants no command execution. Headless commands require the separate `--approve-commands` flag.
Commands run with user permissions, without an OS sandbox or implicit shell.
A successful edit does not imply that verification passed.

Use `--session-db PATH` to opt into a separate experimental database. Do not
point it at the released Go client's database. With the same workspace and
database, `--sessions` lists handles, `--resume HANDLE` restores a completed
conversation, and `--session-info HANDLE` inspects saved messages and uncertain
outcomes without contacting a provider. Interrupted effects are never replayed
automatically; reconciliation and Go session migration are not implemented yet.
`--recovery-info HANDLE` returns the session revision and exact hazardous event
identifiers for review, omitting the transcript and raw receipts. Bun schema v1
databases migrate to v2 with existing history preserved; new calls retain their
canonical ID, provider ID when supplied, iteration and position within the response.
Legacy records retain absent metadata rather than receiving invented identities.

`--mcp-status --mcp-config PATH` checks the explicitly configured servers and
prints connection status and tool names without contacting a model. It starts
configured STDIO commands for discovery and closes the connections afterward.

`--mcp-config PATH` loads a trusted JSON array of servers, for example
`[{"name":"local","transport":{"kind":"http","url":"http://127.0.0.1:3000/mcp"}}]`.
STDIO entries use `transport.kind: "stdio"`, `command`, and optional `args`,
`cwd`, and `env`; loading the configuration starts those commands. Remote HTTP
requires `allowRemote: true`. Tool names become `SERVER__TOOL`. Interactive
calls request approval; headless calls require `--approve-mcp`, independently
of edit and command permissions. Plan mode denies MCP calls, including those
advertised as read-only. Responses project bounded text and retain unknown
domain status and unverified evidence; raw structured content is not placed
in the transcript. Paginated tool catalogs are currently rejected.

The internal component package reuses [Vue TermUI](https://github.com/vue-terminal/vue-termui) and
[OpenTUI](https://opentui.com/docs/core-concepts/renderer/); our layout and lifecycle
tests exercise resize and native scrollback before more components are added.
The component package and agent CLI use `defineComponent`/`h` in `.ts` files, checked
by TS 7. The model-free playground demonstrates `.vue` templates, slots and
`v-model` through the official Vue TermUI Vite plugin. Its `vue-tsc` 3.3.11
checker is isolated with TypeScript 6.0.3: `vue-tsc` still accesses compiler APIs
that TypeScript 7 no longer exports. `bun run typecheck` runs both checks;
the root runtime compiler remains TS 7. The playground is not an agent UI.
OpenTUI's `bun-ffi-structs` dependency still declares a TypeScript 5 peer range;
Bun reports that upstream mismatch. Our TS 7 and runtime tests pass, but this
does not establish compatibility with every upstream API.

Glyphrun contracts under `specs/bun/` exercise headless output, native inline
scrollback, fullscreen resize, edit approval/denial and clean exit against a
deterministic loopback fixture. They do not download models or contact GPUs.
CI runs these contracts on Linux and macOS. Install the tested runner with
`go install github.com/abdul-hamid-achik/glyphrun/cmd/glyph@v0.20.0`.

For an already configured Chalupa environment, run from this checkout:

```bash
chalupa exec --config /path/to/chalupa.yml -- bun run apps/cli/src/main.ts
```

The client consumes the process-only `LOCAL_AGENT_PROVIDER=chalupa`,
`LOCAL_AGENT_PROVIDER_CONFIG`, and `LOCAL_AGENT_LOCAL_ONLY=false` contract.
It preserves the selected model, endpoint, context, weight limit and stream idle
timeout. Before sending a prompt, the agent checks Ollama inventory and model
metadata, rejects upstream model aliases, and enforces the negotiated weight
and native-context limits. Chalupa remains responsible for model placement,
fit and tunnels. The client does not publish Go-compatible inference receipts.
A loopback URL alone does not prove inference stays on the laptop.
Do not treat this as full `chalupa launch local-agent` compatibility. A previously
installed `local-agent` command is not replaced by building this workspace.

## License

MIT. See [LICENSE](LICENSE).
