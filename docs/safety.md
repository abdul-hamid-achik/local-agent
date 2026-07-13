---
title: Safety and privacy boundaries
description: Learn exactly what Local Agent checks, what still requires trust, and why local-first is not the same as sandboxed.
outline: deep
---

# Safety and privacy boundaries

Local Agent makes authority visible and defaults to local endpoints. Those controls reduce risk; they do not create an operating-system sandbox.

## Default local-only policy

```yaml
privacy:
  local_only: true
```

With this setting, Local Agent treats `localhost`, loopback IPs, and unspecified bind aliases such as `0.0.0.0` or `::` as local-machine endpoints. It:

- rejects other Ollama URLs;
- rejects other SSE and Streamable HTTP MCP URLs;
- excludes Ollama Cloud from automatic routing and requires exact conversation-only consent for manual selection;
- canonicalizes built-in file paths, resolves symlinks, and rejects paths outside the startup workspace;
- applies `.agentignore` to built-in file operations;
- removes most inherited environment variables before built-in shell execution;
- starts STDIO MCP servers with a minimal environment and deterministic local executable lookup.

## Approval policy

The following requests require approval by default:

- write, edit, copy, move, remove, and directory creation;
- shell commands;
- memory save, update, and delete;
- every MCP tool call.

The approval card shows the tool and arguments. `a` persists approval by tool name, not by exact path or argument pattern.

## What local-only cannot guarantee

An approved shell command can use absolute paths, leave the workspace, start subprocesses, or contact the network. A trusted STDIO MCP server is a separate process and can act according to its own configuration.

Local Agent currently does not provide:

- an OS-level filesystem, process, or network sandbox;
- a kernel-enforced egress firewall;
- argument-scoped persistent approvals;
- automatic proof that an external side effect completed.

Do not describe the current alpha as “fully private,” “offline,” or “sandboxed.”

## PLAN and headless execution

PLAN removes mutation tools and rejects model-generated mutations in the host.

In non-interactive `-p` mode, risky and MCP calls fail closed because there is no approval UI. `--yolo` bypasses all approval prompts and should be limited to disposable or strongly versioned workspaces.

## Practical operating checklist

1. Start from a clean Git worktree.
2. Use PLAN for unfamiliar repositories.
3. Keep `privacy.local_only` enabled unless you understand the endpoint change.
4. Read every approval request, especially shell and MCP arguments.
5. Inspect `git diff` before committing.
6. Treat Cloud consent and STDIO servers as explicit trust decisions.
7. Keep valuable work backed up outside the agent process.
