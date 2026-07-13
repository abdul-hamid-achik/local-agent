---
title: Sessions, memory, and ICE
description: Understand lossless session restore, checkpoints, workspace-scoped memory, and optional Ollama-powered ICE retrieval.
outline: deep
---

# Sessions, memory, and ICE

Local Agent separates resumable conversation state from optional cross-session retrieval.

## Sessions

`/sessions` opens SQLite-backed sessions for the current workspace. A restored session includes both visible and model-facing state:

- transcript and tool-call identities;
- mode, model pin, and profile;
- token counters and tool receipts;
- durable goal state when present.

Loading a session replaces the active conversation. It does not merge two transcripts.

## Checkpoints

Checkpoints snapshot the current agent message history inside the active session:

```text
/checkpoint before-refactor
/checkpoints
/restore 42
```

Restore validates that the checkpoint belongs to the active session.

## Structured memory

The memory store is workspace-scoped and available even when ICE is disabled. NORMAL and AUTO can request explicit save, update, and delete tools; PLAN can recall.

The store uses owner-only files, interprocess locking, coherent reloads, and fail-closed corruption handling.

## Optional ICE retrieval

ICE embeds prior conversations with Ollama and retrieves relevant history for the same canonical workspace. It is disabled by default:

```yaml
ice:
  enabled: true
  embed_model: nomic-embed-text
```

Pull the embedding model before enabling it:

```bash
ollama pull nomic-embed-text
```

When enabled, ICE can retrieve similar prior messages and run bounded background extraction after completed turns. Background extraction is single-flight, yields to foreground inference, and writes only to the current workspace's memory store.

## Storage

```text
~/.config/local-agent/conversations.json
~/.config/local-agent/memory/<workspace-hash>.json
~/.config/local-agent/local-agent.db
~/.config/local-agent/logs/
```

ICE currently uses a bounded flat JSON scan rather than an approximate-nearest-neighbor index.

## Legacy data

Historical global entries without trustworthy project provenance remain quarantined. They are not loaded into repository-scoped memory, do not appear in the command surface, and do not produce routine startup notices.
