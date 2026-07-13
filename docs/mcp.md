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

MCPHub's lazy mode intentionally advertises only pinned tools plus its discovery and routing tools. Unpinned tools are not missing: the normal flow is `search → describe or resolve → call`. Local Agent keeps the outer MCPHub namespace for safe routing, but the transcript presents the downstream specialist and action. It does not flatten the entire downstream catalog into every model request.

Large lazy results may return a stored-result receipt. Retrieve its bounded pages explicitly with `mcphub_get_result`; Local Agent does not inject every page into the conversation automatically. Treat stored TinyVault results as sensitive because the gateway stores the exact downstream result for its configured retention period.

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

Local Agent treats MCP safety annotations as untrusted presentation hints, not
authority. They may improve the wording of an approval preview, but they never
change authorization or durable effect classification. Every MCP call remains
effect-unknown and passes through the normal approval-policy path, including
tools that declare themselves read-only. Explicit `ask`, `deny`, `allow`, and
yolo decisions retain their normal audit receipts.

## Structured result boundary

Local Agent preserves MCP `StructuredContent` separately from display text. It prefers an exact structured contract for semantic interpretation and accepts text only when the known tool returns one complete JSON document. It never parses prose or Markdown with status heuristics.

Structured payloads are short-lived host input. After an exact tool-specific parser derives transport, domain, evidence, and routing state, the structured copy is discarded rather than copied into the saved visual tool card. The post-hook text required by model history and the execution ledger keeps its existing bounded persistence policy. This avoids duplicate `text + structured` output and keeps a second secret-bearing or very large structured copy out of the transcript projection.

Versioned verifier contracts can produce verified or contradicted evidence. Unrecognized versions, partial results, stale indexes, and stored-result receipts remain neutral or attention states; they do not become green merely because MCP returned without a protocol error.

## Current protocol boundary

The current MCP implementation is tool-focused. Prompts, roots, subscriptions, sampling, and direct multimodal rendering are not yet exposed.
