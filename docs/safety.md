---
title: Safety and privacy
description: What the Local Agent CLI enforces by default — loopback endpoints, plan-intent denials, per-proposal approval, the macOS command sandbox — and what it does not.
outline: deep
---

# Safety and privacy

The client is local-first and asks before it mutates anything. Those controls
validate endpoints and gate proposals one at a time. On macOS, commands can
also run in an operating-system sandbox instead of asking
([Sandboxed commands](#sandboxed-commands-macos)); nothing else is sandboxed.

## Local-first by default

The default provider is Ollama on the local machine, at `OLLAMA_HOST` — by
default `127.0.0.1:11434`. With that default, prompts stay on the machine.

Remote providers are opt-in through the environment: the client refuses to
start with Chalupa, or an OpenAI-compatible server off loopback, unless you
explicitly set `LOCAL_AGENT_LOCAL_ONLY=false`. The Tuimark client's top bar
then says `REMOTE`. See [Providers](/providers#where-the-prompt-goes).

HTTP MCP endpoints follow the same posture: they must point at loopback
(`127.0.0.1`, `localhost`, `::1`) unless the server entry explicitly sets
`allowRemote: true`, and redirects are rejected. See [MCP](/mcp).

## Plan intent denies mutations

The client has three intents: plan, chat, and build, the default. In plan the
model is offered only the read-only tools — `read`, `read_image`, `list`,
`search`, `fetch`, `web_search`, and `task`, `todo_write` and `ask_user`, which
have no external effects — and the runtime hard-denies every mutation, no
matter what an approval grant says:

- `write`;
- `edit`;
- `run_command`;
- MCP tool calls — denied even when the server annotates a tool as read-only.

In plan these proposals never reach an approval prompt; the host refuses them
first. Chat refuses `write`, `edit`, and `run_command` the same way, but keeps
MCP tool calls, each behind its approval as in build. A subagent spawned with
`task`, from any intent, is offered the read-only research tools only. Switch
intent with `--intent`, `/mode plan|build|chat`, or `shift+tab`. See
[Modes](/modes).

## Build asks per proposal

In build, every mutating proposal — `write`, `edit`, `run_command`, and every
MCP tool call — goes through approval one proposal at a time. There is no
setting that approves everything. Reads (`read`, `read_image`, `list`, `search`, `fetch`, `web_search`) do
not prompt.

### What reads can reach

The file tools stay inside the workspace, and they skip secrets as well as
local-agent's own policy: the `.local-agent/` directory and, for a workspace
at your home directory, `.config/local-agent`. A tool call cannot read or
rewrite your permission rules. The hidden names are matched without regard
to case or Unicode form, and cover:

- `.git`, `.env` and `.env.*` (templates such as `.env.example` stay
  readable), `.npmrc`, `.netrc`, `.pypirc`, `.pgpass`, `.git-credentials`,
  `.dockercfg`, `.htpasswd`, `.s3cfg`, `.my.cnf`, `.boto` and `.vault-token`;
- cloud and cluster configuration: `.ssh`, `.gnupg`, `.aws`, `.azure`,
  `.kube`, `kubeconfig`, and credential files such as `credentials`,
  `credentials.json`, `credentials.toml`, `credentials.yml`,
  `credentials.yaml`, `credentials.tfrc.json` and
  `application_default_credentials.json` (a source file like
  `credentials.ts` is ordinary);
- Terraform state: `*.tfstate`, `*.tfstate.backup` and `terraform.tfstate.d`;
- keys, certificates, keystores and keychains: `*.pem`, `*.key`, `*.p12`,
  `*.pfx`, `*.p8`, `*.ppk`, `*.keystore`, `*.jks`, `*.kdbx`, `*.keychain`,
  `*.keychain-db`, `keychains`, `id_rsa*` and `id_ed25519*`;
- files at a fixed place under a folder a project may commit for other
  reasons: `.docker/config.json`, `.config/gh/hosts.yml`, `.config/gcloud`
  and `.m2/settings.xml`.

Hiding a name from the file tools does not hide it from a command you
approve; see [Commands that name a secret path](#commands-that-name-a-secret-path).

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
- `a` — allow this call and matching ones for the rest of the session (the
  Tuimark client offers no grant for a command that
  [names a secret path](#commands-that-name-a-secret-path), for a preview
  too long to show whole, or for a command while the sandbox is on);
- `!` — always: save a rule for exactly this call (see
  [Persistent rules](#persistent-rules)); not offered for a command that
  names a secret path or for a preview too long to show whole;
- `n` or `esc` — deny; the turn goes on, and the model is told the call was
  not approved.

`a` and `!` are the Tuimark client's keys; the Vue client takes `y`, and `n`,
`enter` or `esc` to deny.

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

### Questions are not approvals

In the Tuimark client the model can ask you a question with `ask_user`. Your
answer goes back to the model as text and grants nothing: an edit, write,
command or MCP call it proposes after you answer "yes" still shows its
card, follows your rules and grants, and is still refused in plan and chat.
A subagent cannot ask you anything.

### A goal adds no authority

In the Tuimark client, `/goal OBJECTIVE` runs turns toward an objective
without a message from you between them. Each of those turns is an
ordinary build turn: every edit, write, command and MCP call still shows
its card, follows your rules and grants, and runs in the sandbox when it
is on. A call you refuse stays refused for the rest of the goal. The goal
stops on its own after 8 turns (or the `--turns` you gave, at most 50), 30
minutes, 200k output tokens when the provider reports them, two turns in a
row that change nothing, a failed turn, an uncertain outcome, a skipped
question, or a switch out of build. `esc` or `/goal stop` ends it at once;
on an approval card `esc` denies that one call, as always, and the goal
goes on.

The model ends a goal with `goal_status`: done, or blocked with what it
needs. That report is the model's word. The closing notice says
"evidence" only when the report names a command that exited 0 during the
goal, and it never says the work was verified. Nothing about a goal is
saved: it ends with the process.

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
the flag on. An ask rule refuses whatever the flags say, since it wants a
person and a headless run has none; stderr names the rule
(`refused by ask rule: git push * (a headless run has no one to ask)`).
The exception to the allow is a command that
[names a secret path](#commands-that-name-a-secret-path): an allow rule does
not approve it, so it needs `--approve-commands`, or is refused under
`--sandbox-commands`. Rules are the user's durable intent; the flags remain
the per-run switch.

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
authorize edits. The [rules file](#persistent-rules) comes first: a deny or
ask rule refuses with the flag on, and a trusted allow approves with it off.
On macOS, `--sandbox-commands` runs commands confined
instead of `--approve-commands` (see [Sandboxed commands](#sandboxed-commands-macos)).
See [Command reference](/reference).

## run_command runs exactly as written

The approval card states the contract: "Runs exactly as written: there is no
shell." The argument list you see is the argument list that executes — no
shell interprets it, so there are no pipes, redirects, or expansions to
surprise you.

Approved commands run directly, with your permissions, in the workspace, under
a timeout — 120 seconds by default, capped at 10 minutes. Removing the shell
removes interpretation surprises; it does not reduce what a command can do,
because it runs as you. Every command gets a minimal environment: `PATH`,
`HOME`, `TERM`, `COLORTERM` and the locale variables, nothing else.

Each command runs in its own process group, so stopping it reaches its
descendants. `esc` or the timeout sends SIGTERM to the group, then SIGKILL to
what outlives a short grace period. When the group is gone, the command is
recorded as failed or cancelled, and the model is told it may have made
partial changes or left a background process running. If the group cannot be
confirmed gone (a signal was refused, or it outlived SIGKILL), the outcome
stays unknown and blocks a saved session until you record what happened (see
[Interrupted work](/sessions#interrupted-work)). A descendant that leaves the
group, such as a daemon, is not seen.

If the host exits, or is stopped by SIGINT, SIGTERM or SIGHUP with no handler
of its own, the groups of commands still running are killed too. A SIGKILL of
the host cannot be caught, so it can leave them behind. Closing the terminal
window sends SIGHUP, and the CLI stops as it does for SIGTERM: the running
turn's calls are closed, MCP servers are shut down and the session store is
closed.

### Commands that name a secret path

If a `run_command` argument names a path the file tools hide (the list under
[What reads can reach](#what-reads-can-reach)), the command always shows an
approval card, and the card says so:

- the match covers the words of an argument, an option value
  (`--env-file=.env`, `-d@.env`, `file=@.env`), and, for shells,
  interpreters and wrappers such as `sh -c`, `python3 -c`, `env`, `xargs`,
  `find`, `curl` and `sed`, the text of a script or command line
  (`sh -c "cat .env"`);
- a word that is a symbolic link to such a path inside the workspace counts,
  and the card shows it as `notes.txt -> .env`. At most 64 path words are
  followed: a command with more is treated the same way, and its card says it
  has too many path words to check each;
- no allow rule, session grant or sandbox spares the card. An allow rule
  turns into an ask. With the sandbox on, the Tuimark client still asks, and
  `y` runs that one command outside it;
- the Tuimark client offers neither a session grant (`a`) nor an always rule
  (`!`) for it;
- headless, the command and the path are written to stderr. With
  `--sandbox-commands` the command is refused, since nobody is there to
  answer. With `--approve-commands` the flag still approves it, as it does any
  command.

The match is lexical, so it is a prompt to look closely, not a guarantee. It
does not see a recursive read (`grep -r TOKEN .`, `find . -exec cat {} +`), a
glob that expands to a secret, or a name built inside a script. A link is
judged when the command is proposed; one changed afterwards is not seen.

## Sandboxed commands (macOS)

On macOS, commands can run confined instead of asking. Turn it on with
`/sandbox on` in the Tuimark client, or start with `--sandbox-commands`
(headless, or the Tuimark client with the sandbox already on). The header
shows `sandbox` while it is on; `/status` says whether it is on, off, or
unavailable and why.

With the sandbox on, in build intent:

- a deny rule still refuses the command;
- every other command runs confined, without a card, except one that
  [names a secret path](#commands-that-name-a-secret-path): its card is
  always shown. An allow rule or a session grant only spares the card: run
  outside, the command could run what a sandboxed command wrote in the
  workspace;
- an ask rule still shows its card in the Tuimark client. `y` runs that one
  command outside the sandbox, as you, and the card says so; `a` grants
  nothing for commands while the sandbox is on;
- headless, an ask rule refuses the command, and so does a secret path,
  since nobody is there to answer: with `--sandbox-commands`, no command runs
  outside the sandbox.
  The flag replaces `--approve-commands`, and the two cannot be combined;
- plan and chat still refuse every command, and edits, writes and MCP calls
  are gated as before.

A confined command runs under Seatbelt (`/usr/bin/sandbox-exec`) with a
profile that denies by default. It:

- **reads** the system, but in your home folder only the workspace, the
  toolchains and their caches (`~/.bun`, `~/.cargo`, `~/.rustup`, `~/go`,
  `~/.asdf`, `~/.nvm`, `~/.volta`, `~/.pyenv`, `~/.rbenv`, `~/.deno`,
  `~/.local/bin`, and named folders under `~/.cache`, `~/.local/share` and
  `~/Library/Caches`), `~/.config/git`, `~/.gitconfig` and
  `~/.tool-versions`. Credential files in those folders (git's, Cargo's,
  uv's) stay unreadable. It reads nothing in `/private/tmp`, in your own
  folder under `/var/folders` (your temporary files and caches), or on
  other volumes;
- **writes** only in the workspace and in
  `~/Library/Caches/local-agent/sandbox`, which holds its `TMPDIR` and
  `GOCACHE`. In the workspace, at any depth and in any letter case, it
  never writes a `.git` folder's hooks, config, `commondir`, `info`,
  modules or worktrees, never creates a `.git`, and never writes a
  `.gitattributes` (attributes pick diff, filter and merge drivers from your
  own git config) or `.local-agent`;
- **has no network**: no internet, no DNS, and not this machine either —
  not Ollama, not another local server, not a server of its own;
- is allowed to look up only a few named system services (user and group
  lookups, power assertions, notifications, logging): it cannot open an app
  or script another one, and it can signal and inspect only its own
  processes.

When a confined command fails, its result tells the model it ran in the
sandbox, so it can tell you instead of retrying.

What it does not stop:

- **Files it writes in the workspace run later, outside the sandbox,** when
  something else runs them: your shell, your editor, a card you approve,
  or the same command after `/sandbox off`. That covers `package.json`
  scripts, a `Makefile`, `.envrc`, test files, and hook managers that keep
  their configuration in the workspace — husky (`.husky/`), lefthook
  (`lefthook.yml`), pre-commit (`.pre-commit-config.yaml`), lint-staged.
  Review what changed before you run the project yourself.
- **What it can read, it can show the model**: the workspace, the
  toolchains, the system. `fetch` and `web_search` never ask, so keep
  secrets out of the workspace and out of `~/.gitconfig`.
- It can tell whether a path in your home folder exists, though it cannot
  read or list it.

What does not work inside it:

- anything that needs the network: installing packages that are not
  already cached, Go modules missing from the module cache, `git push`,
  and tests that start a local server;
- `git init`, and git in a linked worktree or submodule whose git data
  lives outside the workspace.

It is available only on macOS. `sandbox-exec` is Apple's Seatbelt tool,
which Apple marks as deprecated. Elsewhere — Linux, Windows — and when the
workspace is your home folder or contains it, or when the client itself
runs inside a sandbox, it is unavailable: `--sandbox-commands` stops a
headless run before it starts, and the Tuimark client keeps asking and says
why.

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
validates endpoints. Beyond the macOS command sandbox, it is not an
operating-system sandbox:

- the agent process, MCP servers and file edits run with no filesystem,
  process, or network isolation;
- a command you approve, or that a rule or `--approve-commands` allows,
  runs with your permissions — it can read and write outside the workspace
  and contact the network, because you can;
- MCP servers are separate processes acting under their own configuration;
- approval gates cover the client's tools; they do not constrain what an
  approved command or server process does next.

Keep valuable work in version control and backed up outside the agent
process.
