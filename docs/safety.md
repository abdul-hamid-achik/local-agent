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
- pins every SSE and Streamable HTTP MCP request to the configured local origin, bypasses environment proxies, and rejects redirects, DNS answers, or server-supplied message endpoints that leave loopback;
- excludes Ollama Cloud from automatic routing and requires exact conversation-only consent for manual selection;
- canonicalizes built-in file paths, resolves symlinks, and rejects paths outside the startup workspace unless a temporary read grant covers them;
- permits explicit, process-local external exact-file or directory read grants without widening write authority;
- applies `.agentignore` to built-in file operations;
- removes most inherited environment variables before built-in shell execution;
- refuses to start repository-supplied STDIO MCP processes until their exact executable configuration is trusted;
- starts STDIO MCP servers with a minimal environment and deterministic local executable lookup.

Repository-local STDIO trust is bound to the absolute configuration path, the
server name, command, resolved executable path and content, arguments, and
explicit environment. Local Agent prints the required
`LOCAL_AGENT_TRUST_REPO_MCP=sha256:...` value and exits before starting any MCP
process. A changed executable or moved configuration produces a different
digest. A trusted launch uses the resolved path covered by that digest and
rechecks its content immediately before startup. Protect the executable and its
parent directory from concurrent writes, as the final OS launch remains
path-based. This startup trust does not approve later MCP tool calls.

Built-in workspace reads and approved file mutations execute relative to a
workspace directory handle pinned for that operation. Local Agent rechecks the
workspace identity, rejects symlink components while opening mutation parents,
and creates atomic-write temporary files inside the pinned parent. A path
component changed after validation therefore cannot redirect built-in file I/O
outside the startup workspace. This boundary does not constrain an approved
shell command or MCP process and does not turn Local Agent into an OS sandbox.
It also inherits the operating system's mount namespace: content mounted below
the workspace, including a bind mount to another tree, is visible as content of
that workspace. Keep mounts that expose sensitive data outside approved roots,
or run Local Agent inside a sandbox with the mount layout you intend to grant.

## Approval policy

The following requests require approval by default:

- write, edit, copy, move, remove, and directory creation;
- shell commands;
- memory save, update, and delete;
- every MCP tool call.

The inline approval surface replaces the composer while leaving the transcript
visible. It shows the action, scope, target or command, and a bounded diff when
one is available. Press `y` to allow once, `n` to deny, `s` to allow the
identical canonical request again during the current Agent process, `d` to
inspect exact arguments, or `esc` to cancel the approval and active turn. The
session grant is not a broad tool-name policy and is not persisted across
process restarts.

At the supported 30×12 minimum, a long file target is projected by its
identifying tail rather than an indistinguishable path prefix. `pgdn` exposes
the remaining preview and `d` switches to the exact arguments before a
decision. Below that minimum, Local Agent replaces interactive surfaces with a
resize notice and pauses keyboard, mouse, and paste input except for `ctrl+c`.
Restoring a supported size first waits for a quiet input boundary and requires
an explicit `enter` re-arm gesture. That gesture is consumed, a second bounded
quiet guard runs, and only then does the unchanged pending decision and draft
return.

Databases created by older releases may contain broad per-tool `allow` rows.
On upgrade, Local Agent retires those rows to `ask`; they never authorize a
new request. Persisted `deny` policies remain effective.

## Reading external files and projects

NORMAL, PLAN, and AUTO keep the startup workspace as the only writable root.
In the interactive TUI, an ordinary prompt that explicitly names an existing
absolute or `~/` path outside that workspace pauses before the agent turn. The
confirmation grants only the named regular file, or the named directory when a
directory was explicitly requested. Allowing it resumes the original draft
once; denying or cancelling restores the draft unchanged.

Single-, double-, or backtick-quoted path literals and macOS drag-and-drop paths
with backslash-escaped whitespace are recognized without invoking a shell or
evaluating substitutions. The scanner has a hard limit of 32 distinct explicit
path candidates. After canonicalization, deduplication, and collapsing paths
already covered by a requested directory, one prompt may require at most four
**new** external read grants. Workspace paths and paths already covered by
active read authority do not consume that four-grant budget. Exceeding either
limit restores the draft, grants nothing, sends nothing to the model, and asks
you to split the request.

For example, this asks for one exact-file read grant rather than access to all
of Downloads:

```text
Analyze ~/Downloads/bug.mp4 with Vidtrace and explain the failure sequence.
```

The approval surface shows the kind and canonical identity of every proposed
grant. Terminal control characters are escaped for display without changing
the authority value. If two paths become visually indistinguishable in a narrow
terminal, confirmation is disabled until the terminal is wide enough to show
distinct identities.

Sibling files remain unavailable. Local Agent verifies that the object has not
changed between inspection and approval. Exact-file reads recheck the authorized
file identity, so replacing the file does not transfer authority to its
replacement. Directory grants use a pinned `os.Root` boundary so relative
operations and symlinks cannot escape the selected directory. When a task needs
a whole source tree, grant that directory explicitly for the current process:

```text
/scope add-read ~/src/another-project
```

Built-in list, read, grep, and copy-source operations can then use that root,
while write, edit, move, remove, and copy destinations remain confined to the
startup workspace. Directory grants observe the external root's `.agentignore`.
Exact-file and directory grants are not saved in the session and can be revoked
with `/scope remove-read` or `/scope clear-read`.

This grant governs Local Agent's built-in file readers. It does not silently
authorize an MCP process: passing the path to Vidtrace or another MCP tool still
produces that tool's separate approval request. Approved shell commands and
trusted STDIO servers remain independent trust boundaries.

## What local-only cannot guarantee

An approved shell command can use absolute paths, leave the workspace, start subprocesses, or contact the network. A trusted STDIO MCP server is a separate process and can act according to its own configuration.

Local Agent currently does not provide:

- an OS-level filesystem, process, or network sandbox;
- a kernel-enforced egress firewall;
- detection or isolation of mount points created below an approved filesystem root;
- argument-scoped persistent approvals;
- automatic proof that an external side effect completed.

Do not describe the current alpha as “fully private,” “offline,” or “sandboxed.”

## PLAN and headless execution

PLAN removes mutation tools and rejects model-generated mutations in the host.

In non-interactive `-p`/`--prompt` mode, requests that need an approval fail
closed by default because there is no approval UI. `--skip-approvals` skips
approval prompts, but explicit deny policies, host validation, workspace/scope
limits, privacy checks, tool preflight, and the execution ledger still apply.
Limit it to disposable or strongly versioned workspaces. `--yolo` is a
deprecated compatibility alias for `--skip-approvals`.

## Practical operating checklist

1. Start from a clean Git worktree.
2. Use PLAN for unfamiliar repositories; PLAN does not itself authorize repository-supplied MCP processes.
3. Keep `privacy.local_only` enabled unless you understand the endpoint change.
4. Read every approval request, especially shell and MCP arguments.
5. Inspect `git diff` before committing.
6. Treat Cloud consent and STDIO servers as explicit trust decisions; never reuse a repository MCP digest after its configuration changes.
7. Keep valuable work backed up outside the agent process.
