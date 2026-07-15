---
title: Sessions, memory, and ICE
description: Understand lossless session restore, checkpoints, workspace-scoped memory, and optional Ollama-powered ICE retrieval.
outline: deep
---

# Sessions, memory, and ICE

Local Agent separates resumable conversation state from optional cross-session retrieval.

## Sessions

`/sessions` and its `/resume` alias open SQLite-backed sessions for the current
workspace. A restored session includes both visible and model-facing state:

- transcript and tool-call identities;
- mode, model pin, and profile;
- token counters and tool receipts;
- path-free image attachment metadata for resumable vision turns;
- bounded file.cheap artifact receipts;
- durable goal state when present.

Loading a session replaces the active conversation. It does not merge two transcripts.

The same lossless restore path is available at TUI startup:

```bash
local-agent --resume S42
local-agent --resume latest
```

Each positive database ID has a derived short handle: session ID `42` is shown
as `S42`. The Runtime view and session picker show the handle and generated
title after the first submitted turn; the TUI footer includes that identity when
the current responsive status layout has room. The title is derived from the
first meaningful task text, including the reviewed Task field in guided PLAN
mode. Commands accept either `S42` or the compatible raw `42` form, while JSON
and export filenames retain numeric IDs.

An exact session must belong to the current canonical workspace. `latest` selects
that workspace's most recently updated session. The database title is restored
with the session, and an active session lease prevents two Local Agent processes
from resuming it concurrently. Startup restore retains the normal cloud-consent
and recovery checks but does not submit a prompt or automatically resume a
durable goal. `--resume` cannot be combined with headless `-p` or `--prompt`.

List or export sessions outside the TUI with:

```bash
local-agent session list
local-agent session export S42
```

`/artifacts` (or `/artifact`) lists completed file.cheap save receipts from the
active or restored transcript. The durable projection contains a host-derived
stash URI, file and byte counts, creation time, full content SHA-256, and static
secret-scan or indexing flags. Raw manifests, source paths, findings, and
provider error prose remain outside persisted UI state.

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
~/.config/local-agent/images/
~/.config/local-agent/logs/
```

The image directory is an owner-private, content-addressed object store. Session
and checkpoint JSON keep the sanitized name, MIME type, byte size, dimensions,
and full SHA-256 reference needed to rehydrate an attachment; they do not store
the original path or raw bytes. `/image forget-history` removes references from
conversation history but deliberately does not delete cached objects or pending
attachments. Existing checkpoints remain immutable and can reintroduce their
historical references if restored.

SQLite migrations are applied transactionally and recorded with source
checksums. Startup refuses a migration whose recorded checksum no longer
matches the embedded source.

ICE currently uses a bounded flat JSON scan rather than an approximate-nearest-neighbor index.

## Legacy data

Historical global entries without trustworthy project provenance remain quarantined. They are not loaded into repository-scoped memory, do not appear in the command surface, and do not produce routine startup notices.
