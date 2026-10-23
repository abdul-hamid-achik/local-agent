---
title: MCP integrations
description: Connect MCP servers to the Bun CLI -- trusted JSON config, namespaced tools, status checks and approval boundaries.
outline: deep
---

# MCP integrations

Local Agent connects to Model Context Protocol (MCP) servers you configure. MCP
servers extend the agent with tools; they do not change the approval model.
Every MCP call goes through the same approval flow as built-in tools.

## Configuration

```bash
bun run dev --mcp-config mcp-servers.json
```

`--mcp-config PATH` points at a JSON file containing an array of server
configurations. The file is limited to 16 servers and 1 MB, and server names
must be unique; the CLI rejects anything else. The config is trusted input: the
host connects to the servers you list, so only list servers you control.

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
      "command": "bunx",
      "args": ["@playwright/mcp@latest"],
      "env": { "PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/Users/you" }
    }
  }
]
```

STDIO servers start with a scrubbed environment: `PATH` and `HOME` are empty
unless your config provides them, which is why the `env` block above exists.
Verify the connection before a session with `--mcp-status`.

## Tool invocation

Tools from a configured server are invoked as `SERVER__TOOL` namespaced calls.
The server name from your config forms the prefix, so unique names are what
keeps tool identities unambiguous.

## Checking status

```bash
bun run dev --mcp-config mcp-servers.json --mcp-status
```

`--mcp-status` is a standalone check: it requires `--mcp-config`, connects to
each configured server, and prints a JSON report listing the tools each server
exposes. It runs no agent turn. The command exits 1 if any server is
unavailable.

## The /mcp panel

In interactive modes, `/mcp` shows an MCP panel with server and tool counts.
Without `--mcp-config` it hints that you need the flag. Close the panel with
`q` or `esc`.

## Approval and headless runs

In interactive modes, MCP calls surface as approval cards like any other tool:
grant for the session or deny.

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

## Related

- [Reference](/reference) for `--mcp-config` and `--mcp-status` details.
- [Safety](/safety) for the approval model.
