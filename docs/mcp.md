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

## Contextual MCP selection

For a non-trivial ordinary turn or durable-goal phase, Local Agent can ask an
exact host-trusted local MCPHub for one bounded capability recommendation before
the provider turn. This is host assistance, not automatic execution:

```text
private user task
  → host-owned activity projection
  → mcphub_resolve_tool
  → advisory server__tool route
  → mcphub_describe_tool
  → model-authored mcphub_call_tool
  → normal scope, privacy, approval, and ledger checks
```

The resolver receives only generic activity fields such as phase, desired
outcome, available input kinds, and locally classified allowlisted intent
facets such as `symbols`, `observability`, `browser`, or `repository`. These
facets preserve useful `use_when` matching without copying arbitrary task
wording. It does not receive raw prompt text, file contents, paths, URLs,
credentials, or previous tool output. Equivalent activities are
cached only in memory. Clear recommendations expire after five minutes;
ambiguous and no-match conclusions expire after 30 seconds so a reconnecting or
changing catalog is reconsidered even before MCPHub revision events are wired
into the host. A failed exact downstream route or an explicit request to
reconsider capabilities bypasses the cached choice immediately.

The TUI labels the result as a suggested MCP route rather than a tool run.
Runtime can show the most recent route after the turn settles, but this bounded
projection remains process-local and is not saved with the session.

Local Agent accepts only MCPHub's exact contextual-resolver contract version 1:
the response must include a consistent status, a non-empty valid catalog
revision, and the bounded recommendation control fields. Missing, older, newer,
or internally inconsistent envelopes fail closed as an invalid advisory; they
are never guessed into a route.

An ambiguous recommendation is never selected or executed. The model can use
`mcphub_search_tools` to compare bounded candidate metadata. For a clear route,
the host advisory directs the model to call `mcphub_describe_tool` before
argument construction because the resolver's required-field summary cannot
express every runtime constraint, such as two individually optional inputs that
are mutually exclusive.

Search descriptions, `use_when` hints, the selected tool description, and its
sanitized JSON Schema are available only to the active model turn. They are
labeled as untrusted contract metadata, cannot expand authority, and are
replaced by a bounded semantic receipt before session persistence. Raw MCP
`StructuredContent` never leaves the parser boundary.

See MCPHub's [contextual routing guide](https://mcphubcli.dev/guide/contextual-routing)
for catalog-side `use_when`, ambiguity, and ranking configuration.

### Hitspec compatibility

Hitspec 2.17 exposes `hitspec_fetch`, `hitspec_list_requests`, and
`hitspec_validate`. `hitspec_fetch` returns content inline and does not create a
file. A durable 2.17 result therefore remains an explicit composition: review
the inline response, write the accepted content through a separately authorized
host file operation, then call `fcheap_save` separately.

Hitspec 2.18 additionally supports optional `hitspec_search_web` and
`hitspec_capture_webpage` surfaces when their server dependencies are configured.
Local Agent recognizes the compact `hitspec_capture_webpage` structured receipt
as a file.cheap artifact outcome while keeping webpage content, URLs, titles,
tags, and downstream failure prose outside durable session state. Transport,
storage, indexing, and evidence remain separate states.

See the versioned [Hitspec MCP reference](https://hitspec.dev/reference/mcp) for
the exact tool schemas and operator-owned startup requirements. MCPHub discovers
only the tools the running Hitspec server actually advertises, so a 2.17 server
does not gain the optional 2.18 surfaces through Local Agent configuration.

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
change authorization or durable effect classification. By default, MCP calls
remain effect-unknown and follow the normal approval-policy path. An exact
local-STDIO trust contract may explicitly classify named routes as `read_only`
or `workspace_effectful`: read-only routes can use the host's reduced-friction
read path, while workspace-effectful routes can receive AUTO authority only
when they provide an explicit workspace inside the active workspace. Unknown,
remote, wrapped, or uncatalogued routes remain gated. `--skip-approvals`
removes an `ask` prompt, but an explicit `deny` remains effective and the
normal audit receipt is still recorded. `--yolo` is only a deprecated
compatibility alias for that flag.

## Structured result boundary

Local Agent preserves MCP `StructuredContent` separately from display text. It prefers an exact structured contract for semantic interpretation and accepts text only when the known tool returns one complete JSON document. It never parses prose or Markdown with status heuristics.

Structured payloads are short-lived host input. After an exact tool-specific parser derives transport, domain, evidence, and routing state, the structured copy is discarded rather than copied into the saved visual tool card. The post-hook text required by model history and the execution ledger keeps its existing bounded persistence policy. This avoids duplicate `text + structured` output and keeps a second secret-bearing or very large structured copy out of the transcript projection.

Versioned verifier contracts can produce verified or contradicted evidence. Unrecognized versions, partial results, stale indexes, and stored-result receipts remain neutral or attention states; they do not become green merely because MCP returned without a protocol error.

## Current protocol boundary

The current MCP implementation is tool-focused. Prompts, roots, subscriptions, sampling, and direct multimodal rendering are not yet exposed.
