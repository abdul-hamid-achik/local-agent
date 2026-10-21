// Deterministic stores for `tuimark validate` / `dump` / `play` and the goldens.
// They are built with the same code the client runs (blocks.ts, store.ts), so a
// change to a store shape shows up in the goldens instead of in a terminal.

import type { ApprovalRequest, Message } from "@local-agent/runtime";
import { liveToolView, streamingView, ViewBuilder } from "#cli/tuimark/blocks";
import { filterPalette } from "#cli/tuimark/commands";
import {
  APPROVAL_STATUS,
  approvalState,
  contextLabel,
  contextPct,
  initialStore,
  type Store,
} from "#cli/tuimark/store";
import { Transcript } from "#cli/tuimark/transcript";

const provider = {
  type: "ollama" as const,
  model: "qwen3-coder:30b",
  context_size: 32_768,
};

const EDIT_CALL = {
  id: "call-1",
  function: {
    name: "edit",
    arguments: { path: "src/fetch.ts", old_string: "a", new_string: "b" },
  },
};

const REPLY = [
  "Sure. I will wrap the call in a bounded retry with backoff:",
  "",
  "```ts",
  "export async function fetchRetry(url, tries = 3) {",
  "  // exponential backoff",
  "  for (let attempt = 1; ; attempt++) {",
  "    try { return await fetch(url); }",
  "    catch (e) { if (attempt === tries) throw e; }",
  "  }",
  "}",
  "```",
].join("\n");

const MARKDOWN = [
  "## Notes",
  "- the delay doubles on every attempt",
  "- the last error is rethrown",
  "",
  "> Only idempotent requests should be retried.",
  "",
  "| attempt | delay |",
  "|---|---|",
  "| 1 | 200 ms |",
  "| 2 | 400 ms |",
  "",
  "Use `fetchRetry` for **every** call.",
].join("\n");

const MESSAGES: Message[] = [
  {
    role: "user",
    content: "Add a retry to the fetch helper and show me the result.",
  },
  { role: "assistant", content: REPLY },
  { role: "assistant", content: "", tool_calls: [EDIT_CALL] },
  {
    role: "tool",
    tool_name: "edit",
    tool_call_id: "call-1",
    content: JSON.stringify({
      status: "succeeded",
      output: "Updated src/fetch.ts. Verification has not been run.",
    }),
  },
  { role: "assistant", content: "Done. The helper now retries three times." },
];

const MARKDOWN_MESSAGES: Message[] = [
  { role: "user", content: "Explain the retry policy." },
  { role: "assistant", content: MARKDOWN },
];

const DIFF = [
  "--- a/src/fetch.ts",
  "+++ b/src/fetch.ts",
  "@@ -12,3 +12,9 @@",
  " import { sleep } from './util';",
  "-export const get = (url) => fetch(url);",
  "+export async function fetchRetry(url, tries = 3) {",
  "+  for (let attempt = 1; ; attempt++) {",
  "+    try { return await fetch(url); }",
  "+    catch (e) { if (attempt === tries) throw e; await sleep(200 * attempt); }",
  "+  }",
  "+}",
  "+export const get = (url) => fetchRetry(url);",
].join("\n");

const EDIT_PROPOSAL: ApprovalRequest = {
  kind: "edit",
  path: "src/fetch.ts",
  before: "",
  after: "",
  diff: DIFF,
  identity: "x",
};

function base(messages: Message[] = MESSAGES): {
  store: Store;
  builder: ViewBuilder;
} {
  const builder = new ViewBuilder();
  const transcript = new Transcript();
  for (const message of messages) {
    const view = builder.fromMessage(message);
    if (view) transcript.push(view);
  }
  const store = initialStore({
    provider,
    intent: "build",
    sessionId: "a1b2c3d",
    mcpServers: 0,
    transcript: transcript.snapshot(),
  });
  store.header.ctx_label = contextLabel(9_800, provider.context_size);
  store.header.ctx_pct = contextPct(9_800, provider.context_size);
  return { store, builder };
}

/** Three exchanges: text with code and a list, a finished tool card, a short reply. */
export function idleSample(): Store {
  return base().store;
}

/** Markdown beyond code: heading, bullets, quote, table, inline marks. */
export function markdownSample(): Store {
  return base(MARKDOWN_MESSAGES).store;
}

/** A tool call waiting for approval, with the edit dialog open. */
export function approvalSample(): Store {
  const { store, builder } = base();
  builder.fromMessage({
    role: "assistant",
    content: "",
    tool_calls: [EDIT_CALL],
  });
  const call = builder.takeCall();
  store.live = liveToolView(call, "edit", "approval");
  store.approval = approvalState(EDIT_PROPOSAL);
  store.busy = true;
  store.placeholder = "Generating…";
  store.status = APPROVAL_STATUS;
  return store;
}

/** A reply that is still streaming, its code block not closed yet. */
export function streamingSample(): Store {
  const { store } = base();
  store.live = streamingView(
    "Here is the plan:\n\n```ts\nconst retries = 3;\nfor (let i = 0; i < retries; i++) {\n  await fetch(url);",
  );
  store.earlier = { n: 250 };
  store.busy = true;
  store.placeholder = "Generating…";
  store.status = "Generating · Esc cancel";
  return store;
}

/** The command palette, filtered to "mode". */
export function paletteSample(): Store {
  const { store } = base();
  const found = filterPalette("mode");
  store.palette = { open: true, query: "mode", cur: found[0]?.id ?? "" };
  store.found = { any: found.length > 0, items: found };
  return store;
}

/** The help overlay. */
export function helpSample(): Store {
  const { store } = base();
  store.help = { open: true };
  return store;
}

export const SAMPLES = {
  sample: idleSample,
  "sample-markdown": markdownSample,
  "sample-approval": approvalSample,
  "sample-streaming": streamingSample,
  "sample-palette": paletteSample,
  "sample-help": helpSample,
} as const;
