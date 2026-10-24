---
title: Safety and privacy
description: What the Local Agent CLI enforces by default — loopback endpoints, plan-intent denials, per-proposal approval — and what it does not.
outline: deep
---

# Safety and privacy

The client is local-first and asks before it mutates anything. Those controls
validate endpoints and gate proposals one at a time; they are not an
operating-system sandbox.

## Local-first by default

The default provider is Ollama on the local machine, at `OLLAMA_HOST` — by
default `127.0.0.1:11434`. With that default, prompts stay on the machine.

The only remote provider is Chalupa, and it is opt-in through the
environment: the client refuses to start with Chalupa selected unless you
explicitly set `LOCAL_AGENT_LOCAL_ONLY=false`. See [Providers](/providers).

HTTP MCP endpoints follow the same posture: they must point at loopback
(`127.0.0.1`, `localhost`, `::1`) unless the server entry explicitly sets
`allowRemote: true`, and redirects are rejected. See [MCP](/mcp).

## Plan intent denies mutations

The client has two intents: plan and build, the default. In plan the model is
offered only the read-only tools — `read`, `read_image`, `list`, `search`,
`fetch`, `web_search` — and the runtime hard-denies every mutation, no matter
what an approval grant says:

- `write`;
- `edit`;
- `run_command`;
- MCP tool calls — denied even when the server annotates a tool as read-only.

In plan these proposals never reach an approval prompt; the host refuses them
first. Switch intent with `--intent`, `/mode plan|build`, or `shift+tab`. See
[Modes](/modes).

## Build asks per proposal

In build, every mutating proposal — `write`, `edit`, `run_command`, and every
MCP tool call — goes through approval one proposal at a time. There is no
setting that approves everything. Reads (`read`, `read_image`, `list`, `search`, `fetch`, `web_search`) do
not prompt.

### What reads can reach

The file tools stay inside the workspace, and they skip secrets (`.env`,
`.ssh`, keys, `.git`) as well as local-agent's own policy: the
`.local-agent/` directory and, for a workspace at your home directory,
`.config/local-agent`. A tool call cannot read or rewrite your permission
rules.

`fetch` reaches public `http` and `https` hosts only. Before every request,
redirects included, it refuses:

- any other scheme, such as `file:`;
- `localhost`;
- a host that is, or resolves to, a loopback, private, link-local,
  carrier-grade NAT or multicast address. That covers the cloud metadata
  address `169.254.169.254` and the IPv6 forms that embed those addresses.

To let `fetch` reach a local dev server or your LAN, set
`LOCAL_AGENT_FETCH_PRIVATE=1`. The scheme check still holds. The check
resolves the host itself, so a DNS server that answers differently between
that lookup and the connection (DNS rebinding) is not covered.

### What a read can send

`fetch` and `web_search` do not ask, so text the model reads can steer what
it fetches next. A page, a search result or an MCP reply that tells the
model to put a file's contents in a URL can make it send them to that host.
Results from `fetch`, `web_search`, MCP and `task` reach the model marked
`"source": "external"`, and it is told that such text is data, not
instructions. That is guidance to the model, not a guarantee. Deny rules
(see [Persistent rules](#persistent-rules)) stop the two tools that never
ask: `fetch` with `domain:*` and `web_search` with `*`. MCP tools and
commands can still reach the network once you approve them or a rule
allows them.

### Interactive approval

The dialog shows the action: a bounded diff for edits, the exact command for
commands. Press:

- `y` — allow this call once;
- `a` — allow this call and matching ones for the rest of the session;
- `n` or `esc` — deny.

`a` grants narrowly, and the dialog spells out the scope before you press it:

- file changes: later `write` and `edit`;
- a command prefix: program plus first argument, so `bun test …` but not
  every `bun` invocation;
- for a package manager or toolchain subcommand that runs or installs what
  its next word names (`bun run`, `npm exec`, `pnpm dlx`, `bun add`,
  `docker run`, `go run`, `cargo install`, …), three words: `bun run test …`
  never covers `bun run other.ts`;
- one exact `SERVER__TOOL` MCP name; through a gateway such as mcphub, the
  one downstream tool the call runs, never the gateway's `call_tool`.

Some commands are granted only as the exact command, because their prefix
would cover code you never saw:

- a bare program, such as `bash`, or a bare runner subcommand such as
  `npm install`;
- a command whose second word is an option, such as `bash -c …`, `git -c …`
  or `node -e …`;
- a program that runs another program, such as `env`, `xargs`, `timeout`,
  `sudo`, `ssh` or `find`;
- the git subcommands that run code from their arguments or write config
  that later runs: `git config`, `submodule`, `bisect`, `rebase`,
  `filter-branch`, `filter-repo`, `difftool`, `mergetool` and `daemon`.

A prefix grant covers further arguments, but not an option the approved
command did not carry: after granting `bun test a.ts`, `bun test b.ts` runs,
while `bun test --preload x.ts` or `make build -f other.mk` shows the card
again (`a` there adds that option to the grant). A runner subcommand
followed by an option (`npm i -g …`, `pip install --index-url …`) is
granted only as the exact command.

A grant is never taken from a preview too long to read whole.

Session grants live in memory only. They die with the process. `/permissions`
lists them beside the file rules; `/permissions clear` revokes the session
grants, and `/permissions clear rules` empties the file.

### Post-edit diagnostics

With `LOCAL_AGENT_DIAGNOSTICS=1`, an approved edit or write of a file Biome
understands is followed by a Biome check whose findings are appended to the
tool result. `LOCAL_AGENT_DIAGNOSTICS_TSC=1` adds `tsc --noEmit`. Both run
only the workspace's own installs, found in its `node_modules`: the native
binary of `@biomejs/biome`, and `typescript`. Nothing is downloaded, and
nothing is looked up above the workspace. Biome uses the root's
`biome.json` when there is one; `tsc` runs only with a root
`tsconfig.json`. Opting in means running the workspace's toolchain after
approved edits, as a test command would. These runs are the host's, not
the model's proposals, so permission rules do not apply to them. It is off
by default.

### Persistent rules

`!` on an approval card saves a rule for exactly what the card shows to
`.local-agent/permissions.json` in the workspace, creating the file (and a
gitignore entry for its directory) on first use. For a command that is the
exact argument list (`bun test a.test.ts`), never the whole program. Rules
are `{effect, tool, pattern}`, evaluated before any card, in every client:
the Tuimark client, the Vue client, the desktop app and headless runs.

- `edit` — a gitignore-style path glob relative to the workspace (`*` one
  segment, `**` recursive). Without a leading `/` it matches at any depth
  (`a.ts` also matches `src/a.ts`); `!` saves the path anchored
  (`/src/a.ts`). A deny or ask rule ignores case and Unicode normalization,
  as macOS's default filesystem does, so `/src/secrets/**` also refuses
  `src/Secrets/key`; an allow rule matches the path as written;
- `run_command` — the argument list word for word, with `"…"` around a word
  that holds spaces or quotes. A trailing `*` written by hand makes it a
  prefix, so `npm test *` also matches bare `npm test`. A deny or ask rule
  also matches the program by name, in any case (`rm *` refuses
  `/bin/rm -rf x` and `RM x`), and checks what a wrapper (`sudo`, `env`,
  `timeout`, `xargs`, `ssh`, …), a `sh -c` script or `find -exec` would
  run. An allow rule matches the argument list as written, nothing else;
  `git *` allows all of git, `git -c …` included, so prefer narrower
  rules;
  Deny rules are a guard rail, not a shell parser: a script built at run
  time (`bash -c "$(…)"`) or an interpreter (`python3 -c …`) can still
  delete files without naming `rm`;
- `fetch` — `domain:host`, with `*.example.com` for subdomains, checked on
  every redirect hop;
- `mcp` — `server__tool` with `*` wildcards;
- `web_search` — `*`.

Evaluation is deny → ask → allow, first match wins; deny always beats a
session grant or a saved allow. `fetch` and `web_search` never ask, so only
their deny rules matter. Headless runs consult the file first in both
directions: a trusted allow approves with the flag off, a deny refuses with
the flag on. Rules are the user's durable intent; the flags remain the
per-run switch.

### Trusting a workspace's rules

The rules file lives in the workspace, so a repository you clone can ship
one. Deny and ask rules only restrict, and they always apply. An allow rule
applies only after you have trusted it on this machine:

- `!` trusts the one rule it saves;
- `/permissions` marks allow rules you have not trusted as `(untrusted)`,
  and headless runs print a warning that names them;
- `/permissions trust` lists the untrusted allow rules, and
  `/permissions trust confirm` trusts exactly those. A rule written into
  the file after that list is untrusted until you review again;
- `local-agent --trust-rules` does the same from a shell: it lists the
  rules and trusts them only after you type `yes` at a terminal.

Trust is recorded outside every workspace, in
`$XDG_CONFIG_HOME/local-agent/trusted-rules.json` (by default
`~/.config/local-agent/`; a relative `XDG_CONFIG_HOME` is ignored), keyed
by the workspace's real path and its directory: the same file in another
checkout, or in a new directory at the same path, starts untrusted. The
file tools never read or write that directory, even when it lies inside
the workspace. `/permissions clear rules` empties the file and forgets its
trust. A damaged trust store trusts nothing and is never rewritten; repair
or remove it.

### Headless approval

Headless runs have no approval UI, so they fail closed: proposals are denied
unless you pass the matching flag.

- `--approve-edits` — allow `write` and `edit`;
- `--approve-commands` — allow `run_command`;
- `--approve-mcp` — allow MCP tool calls.

The flags are headless-only and independent; `--approve-commands` does not
authorize edits. See [Command reference](/reference).

## run_command runs exactly as written

The approval card states the contract: "Runs exactly as written: there is no
shell." The argument list you see is the argument list that executes — no
shell interprets it, so there are no pipes, redirects, or expansions to
surprise you.

Approved commands run directly, with your permissions, in the workspace, under
a timeout — 120 seconds by default, capped at 10 minutes. Removing the shell
removes interpretation surprises; it does not reduce what a command can do,
because it runs as you.

## MCP calls cross the same boundary

Every MCP tool call is an approval proposal in build, a hard deny in plan, and
`--approve-mcp`-gated in headless. Tools are invoked under namespaced
`SERVER__TOOL` names, and a session grant covers exactly that name. A call
through a gateway is approved, ruled and granted as the downstream tool it
runs, so one grant never covers every tool behind the gateway.

Configuring MCP servers is itself a trust decision, whether in your own
`~/.config/local-agent/mcp.json` or with `--mcp-config`. A workspace cannot
add a server. Stdio servers run as
child processes of the agent with a minimal environment plus whatever their
entry configures; HTTP servers must be loopback unless the entry says
`allowRemote: true`. See [MCP](/mcp).

## What local-first is not

Local-first is an endpoint posture: loopback by default, loopback MCP unless
explicitly marked remote, and an explicit opt-out before remote inference. It
validates endpoints. It is not an operating-system sandbox:

- there is no filesystem, process, or network isolation;
- an approved command runs with your permissions — it can read and write
  outside the workspace and contact the network, because you can;
- MCP servers are separate processes acting under their own configuration;
- approval gates cover the client's tools; they do not constrain what an
  approved command or server process does next.

Keep valuable work in version control and backed up outside the agent
process.
