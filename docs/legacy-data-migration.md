# Legacy data migration

Workspace scoping deliberately does not make unowned historical data visible
to every project. The first claim must be explicit, produces a receipt, and is
never repeated for another workspace.

## Global memories

Startup performs only `memory.PreviewDefaultLegacyForWorkspace(workspace)`.
It reports provenance-free data as quarantined and opens the current
workspace's scoped store; it never assigns the global file to the first current
directory. Headless mode has no claim path.

In the TUI:

1. Run `/migrate-memory`.
2. Review the exact count, global source, destination workspace/path, and backup
   path.
3. Run the exact `/migrate-memory confirm <preview-count>` command printed by
   the preview.

Confirmation binds both the count and SHA-256 of the previewed source, then:

- writes `memories.json.pre-workspace.bak` with mode `0600`;
- installs the data at the deterministic workspace-scoped path;
- writes `memories.json.workspace-claim.json`; and
- removes the live global `memories.json` only after those files are durable.

The same workspace can retry safely. Another workspace receives
`memory.ErrLegacyMemoryClaimedByAnotherWorkspace` and must continue with its
own scoped store. A pre-existing scoped target is adopted only when it is valid
and contains zero memories (`null` or `[]`); its exact bytes are retained at
`<scoped-path>.pre-legacy-claim.bak`. A non-empty or invalid target is left
untouched and requires an explicit manual merge.

## ICE conversation entries

ICE starts with scoped retrieval enabled, but entries whose `project_id` is
empty remain excluded and are reported as quarantined. Startup and headless
mode never call `ClaimLegacyEntries`.

In the TUI, run `/migrate-ice`, review the exact count/store/workspace, then run
the printed `/migrate-ice confirm <preview-count>` command. Confirmation fails
if the set count changes. The project receipt is stored beside
`conversations.json`; a later project receives
`ice.ErrLegacyICEClaimedByAnotherProject` and cannot adopt those entries.

## Legacy checkpoints

Checkpoints already carrying a positive session ID can be claimed only through
`ClaimLegacyCheckpointsForActiveSession`, which verifies that the persisted
session belongs to the requested workspace.

Older checkpoints generally have `session_id=0`. Do not adopt them during
startup. Once a new persisted session is active in the intended workspace:

1. Run `/migrate-checkpoints` to preview the exact count and destination.
2. Review the workspace path and active session ID shown in the receipt.
3. Run the exact command printed by the preview:
   `/migrate-checkpoints confirm <preview-count>`.

Internally, preview calls `CountUnboundLegacyCheckpoints`; confirmation passes
that unchanged count to `ClaimUnboundLegacyCheckpointsForActiveSession`.

The transaction rebinds the set and writes a durable singleton receipt. A
stale count, a second workspace, or new unbound data appearing after completion
fails closed.

## Historical `noted` sessions

Old `noted` records have no trustworthy workspace identity and their Markdown
does not contain the hidden model/tool-call state required by the SQLite session
format. They must not be bulk-imported automatically.

Inventory at most 100 candidates with:

```sh
noted list --tag session --json -n 100
```

Inspect an individual record with:

```sh
noted show <id> --json
```

For each record, choose its workspace explicitly, retain the original note as
the immutable backup, and copy only its visible transcript into a newly created
session in that workspace. Treat tool calls and hidden reasoning as unavailable;
do not synthesize them from Markdown.
