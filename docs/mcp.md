---
title: MCP integrations
description: Connect MCP servers to the Bun CLI -- your own mcp.json or an explicit config, namespaced tools, gateways such as mcphub, status checks and approval boundaries.
outline: deep
---

# MCP integrations

Local Agent connects to Model Context Protocol (MCP) servers you configure. MCP
servers extend the agent with tools; they do not change the approval model.
Every MCP call goes through the same approval flow as built-in tools.

## Configuration

Put the servers you always want in your own config file:

```text
~/.config/local-agent/mcp.json      ($XDG_CONFIG_HOME/local-agent/mcp.json)
```

Every run loads it when it exists. It is per user, never per workspace: a
repository you clone cannot add a server. `--no-mcp` starts without it, and
`--mcp-config PATH` loads another file instead:

```bash
local-agent --mcp-config mcp-servers.json
```

Either file holds a JSON array of server configurations. It is limited to 16
servers and 1 MB, and server names must be unique; the CLI rejects anything
else. The config is trusted input: the host connects to the servers you list,
so only list servers you control.

A minimal file looks like:

```json
[
  {
    "name": "my-server",
    "transport": { "kind": "http", "url": "http://127.0.0.1:8080/mcp" }
  }
]
```

HTTP endpoints must point at local-machine addresses unless you pass
`"allowRemote": true` inside the transport.

Each tool call has 60 seconds by default. A server whose tools take longer
sets `"timeoutMs"` in its entry, from 1000 (one second) to 600000 (ten
minutes).

## Startup

Servers connect at the same time, so one slow server does not hold up the
others. A server that fails to connect does not stop the run:

- from your own `mcp.json`, it is a warning, and the run continues with the
  servers that did connect;
- from an explicit `--mcp-config`, a headless run (and the Vue client) stops
  before any inference: you named that file for this run;
- the Tuimark client opens without waiting. The status line says
  `MCP connecting…`, and failures appear there and in `/mcp`. A message you
  send before the servers are ready waits for them, because their tools are
  part of what the model is offered. `esc` cancels the wait.

A STDIO server that does not start is reported with the last line it wrote to
stderr, after the reason, on one line with escape sequences and control
characters removed. That line is shown to you, never to the model.

A STDIO server that stops during a session is not restarted. Its later calls
fail before anything is sent, and the model is told not to call that server
again; restart the client to start it again. An HTTP server that stops
answering is not detected this way: a call to it fails in the transport as
an uncertain outcome, which stops the run until you record what happened
(see [Interrupted work](/sessions#interrupted-work)).

## A working browser today

A real browser — navigation, clicks, forms, screenshots, page snapshots — needs
no new code: run [Playwright MCP](https://github.com/microsoft/playwright-mcp)
as a configured server. It runs locally, keeps no API keys, and appears to the
agent as `browser__*` tools:

```json
[
  {
    "name": "browser",
    "transport": {
      "kind": "stdio",
      "command": "/Users/you/.bun/bin/bunx",
      "args": ["@playwright/mcp@0.0.83"],
      "env": { "PATH": "/Users/you/.bun/bin:/usr/bin:/bin", "HOME": "/Users/you" }
    }
  }
]
```

STDIO servers start with a scrubbed environment: `PATH` and `HOME` are empty
unless your config provides them, which is why the `env` block above exists.
A command that runs in your shell may not be on that `PATH`: Homebrew
installs to `/opt/homebrew/bin` on Apple silicon and Bun to `~/.bun/bin`.
Give the command's full path (`command -v bunx` prints it) or add its
directory to `env.PATH`; a server that cannot start says which. Verify the
connection before a session with `--mcp-status`.

The version is pinned on purpose: `@latest` would run whatever was published
last, the next time the server starts. Move the pin when you have checked a
newer release.

## Tool invocation

Tools from a configured server are invoked as `SERVER__TOOL` namespaced calls.
The server name from your config forms the prefix, so unique names are what
keeps tool identities unambiguous.

## Gateways such as mcphub

A gateway fronts many servers behind one connection and runs their tools
through its own `call_tool`. [mcphub](https://github.com/abdul-hamid-achik/mcphub)
in lazy mode is one. One entry is enough:

```json
[
  {
    "name": "mcphub",
    "timeoutMs": 300000,
    "transport": {
      "kind": "stdio",
      "command": "/opt/homebrew/bin/mcphub",
      "args": ["mcp", "serve", "--agent", "local-agent"],
      "env": { "PATH": "/opt/homebrew/bin:/usr/bin:/bin", "HOME": "/Users/you" }
    }
  }
]
```

When a server's `call_tool` takes a tool name and its arguments, Local Agent
treats it as a gateway. An approval card, a rule and a session grant then name
the tool the gateway runs, not `call_tool`. The name is
`GATEWAY__SERVER__TOOL`: the card says `Call status from server cortex?` and
names the gateway, and `a` grants `cortex__status` alone. Local Agent reads
`server` and `tool` the way mcphub resolves them, so `tool: "cortex__status"`
and `server: "cortex", tool: "status"` are one tool. A call is refused before it
is sent when its arguments do not name one server and one tool plainly: no
tool, a server name with `__` in it, or a key such as `Server` that the
gateway would also read as `server`.

Rules in `.local-agent/permissions.json` use the same names:

```json
{ "effect": "allow", "tool": "mcp", "pattern": "mcphub__cortex__status" }
{ "effect": "deny", "tool": "mcp", "pattern": "mcphub__monitor__*" }
```

An allow rule matches only that downstream name. A deny or ask rule also
matches the gateway's own `mcphub__call_tool`, and the other names mcphub
may run the tool by (`cortex_status` for `status`). Gateway tools that run
nothing downstream, such as `list_servers` or `search_tools`, keep their own
names.

## Checking status

```bash
local-agent --mcp-status
local-agent --mcp-config mcp-servers.json --mcp-status
```

`--mcp-status` is a standalone check of your own `mcp.json`, or of the file
`--mcp-config` names. It connects to each configured server and prints a JSON
report listing the tools each server exposes. It runs no agent turn. The
command exits 1 if any server is unavailable.

## The /mcp panel

In interactive modes, `/mcp` shows an MCP panel with server and tool counts.
In the Tuimark client it also lists servers that are still connecting or that
failed, with the reason. With no servers configured it hints at
`--mcp-config`. Close the panel with `q` or `esc`.

## Approval and headless runs

In interactive modes, MCP calls surface as approval cards like any other tool:
approve, grant for the session, or deny.

In headless mode, `--approve-mcp` allows calls to configured MCP tools. It is
one of the headless approval classes alongside `--approve-edits` and
`--approve-commands`; approve flags only work in headless mode. See
[Safety](/safety).

Plan intent hard-denies MCP calls regardless of the approver, exactly as it
denies writes, edits and commands. See [Modes](/modes).

## Transport is not domain success

A successful MCP transport call means the server received and answered the
request. It does not mean the operation the server performed succeeded on its
own terms. Read tool results; the agent does not interpret a transport-level
success as a domain success.

The model reads an MCP result as the server's text, marked as coming from
outside, beside `transport`, `domain` and `evidence` fields that say only
that the call went through: the domain outcome is `unknown` and the evidence
`unverified`. The Tuimark card shows the same doubt in its first line.

## Related

- [Reference](/reference) for `--mcp-config`, `--no-mcp` and `--mcp-status` details.
- [Safety](/safety) for the approval model.
