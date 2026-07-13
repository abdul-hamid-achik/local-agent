---
title: MCP tools and MCPHub
description: Connect Local Agent to MCP servers directly or through MCPHub while preserving approval and privacy boundaries.
outline: deep
---

# MCP tools and MCPHub

Local Agent is an MCP client for tools. It supports STDIO, SSE, and Streamable HTTP transports. Every exposed tool is namespaced as `<server>__<tool>` and remains subject to the Local Agent approval policy.

## Recommended gateway

[MCPHub](https://mcphubcli.dev/) provides one local gateway for discovery, authentication, and downstream tool policy:

```yaml
servers:
  - name: mcphub
    command: mcphub
    args: [mcp, serve, --agent, local-agent]
```

Read the [MCPHub documentation](https://mcphubcli.dev/) or inspect its [source repository](https://github.com/abdul-hamid-achik/mcphub) for gateway setup and operational details.

With a gateway:

- MCPHub owns lazy discovery and synchronization of downstream servers.
- Local Agent owns the final user approval and transcript.
- Cortex and other tools appear as namespaced MCP calls.

## Direct servers

You can also connect a server directly:

```yaml
servers:
  - name: local-tools
    command: /absolute/path/to/mcp-server
    args: [serve]

  - name: local-http-tools
    transport: streamable-http
    url: http://127.0.0.1:8812/mcp
```

Servers connect concurrently at startup. One failed server does not prevent the TUI from opening, and a background health monitor attempts reconnection.

## Cortex and durable goals

[Cortex](https://cortexai.tools/) is an optional evidence-guided kernel. When reachable directly or through MCPHub, the Goal Runtime can link one stable Cortex case and read its semantic status between productive turns.

Cortex does not own Local Agent's budgets, approval prompts, scheduling, or cancellation. Its structured next action is bounded prompt context; it is never executed directly by the host.

## Security boundary

`privacy.local_only: true` accepts only local-machine HTTP MCP endpoints (`localhost`, loopback IPs, and unspecified bind aliases). It cannot constrain what an approved STDIO server does after it starts.

Treat each MCP server as a separate trusted process. It may read files, contact services, or create effects according to its own configuration. Review each approval request and keep server catalogs narrow.

## Current protocol boundary

The current MCP implementation is tool-focused. Prompts, roots, subscriptions, sampling, and direct multimodal rendering are not yet exposed.
