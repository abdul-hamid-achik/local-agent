import { mkdir, mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type {
  AgentEvent,
  AgentOptions,
  Answer,
  Approval,
  ApprovalRequest,
  GoalReport,
  McpConnection,
  Message,
  OllamaAdmission,
  Provider,
  Question,
  RecoveryItem,
  Session,
  SessionContext,
  Workspace,
} from "@local-agent/runtime";
import {
  ConversationFullError,
  MAX_MESSAGE_CHARS,
  MLX_WARNING,
  StepLimitError,
} from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  type Block,
  DETAILS_MAX_CHARS,
  MAX_MESSAGE_BLOCKS,
  MAX_RECEIPTS,
  type MessageView,
  parseBlocks,
  RECEIPTS_MAX_CHARS,
  runsText,
} from "#cli/tuimark/blocks";
import type { CopyResult } from "#cli/tuimark/clipboard";
import { BUSY_STATUS, type PaletteItem } from "#cli/tuimark/commands";
import {
  APPROVAL_ARM_MS,
  formatElapsed,
  TuimarkClient,
} from "#cli/tuimark/controller";
import { FakeHost } from "#cli/tuimark/fake-host";
import {
  APPROVAL_SANDBOX_SESSION_STATUS,
  APPROVAL_SECRET_STATUS,
  APPROVAL_STATUS,
  APPROVAL_TRUNCATED_SESSION_STATUS,
  APPROVAL_TRUNCATED_STATUS,
  APPROVAL_UNARMED_SESSION_STATUS,
  APPROVAL_UNARMED_STATUS,
  APPROVAL_UNCHECKED_STATUS,
  APPROVAL_WAIT_STATUS,
  NO_DETAILS_STATUS,
  PROVIDER_NO_ANSWER_STATUS,
  QUESTION_CHOOSE_STATUS,
  QUESTION_EMPTY_STATUS,
  QUESTION_NO_MATCH_STATUS,
  QUESTION_PLACEHOLDER_CHOICE,
  QUESTION_PLACEHOLDER_TEXT,
  QUESTION_STATUS,
  QUESTION_UNARMED_STATUS,
  QUESTION_WAIT_STATUS,
  type Store,
} from "#cli/tuimark/store";

// --------------------------------------------------------------------- scripting

const provider = {
  type: "ollama",
  model: "fixture",
  context_size: 8192,
} as Provider;
const workspace = {} as Workspace;

type Ctx = { options: AgentOptions; messages: Message[] };
type Script = (ctx: Ctx) => AsyncGenerator<AgentEvent>;

/** A script that only waits or throws and yields no event. */
const quiet = (fn: (ctx: Ctx) => Promise<void>): Script =>
  async function* (ctx) {
    await fn(ctx);
    yield* [] as AgentEvent[];
  };

function setup(
  script: Script,
  extra: Partial<ConstructorParameters<typeof TuimarkClient>[0]> = {},
) {
  const ui = new FakeHost();
  const aborts = new AbortController();
  const seen: { messages: string[] }[] = [];
  const client = new TuimarkClient({
    provider,
    workspace,
    intent: "build",
    signal: aborts.signal,
    flushMs: 2,
    armMs: 10,
    run: (_provider, messages, options) => {
      seen.push({ messages: messages.map((m) => `${m.role}:${m.content}`) });
      return script({ options, messages });
    },
    ...extra,
  });
  decisions.length = 0;
  ui.store = JSON.parse(JSON.stringify(client.initialStore()));
  client.attach(ui);
  return { ui, client, aborts, seen };
}

const reply = (text: string): Script =>
  async function* () {
    for (const word of text.split(" "))
      yield { type: "text", text: `${word} ` };
    yield { type: "done", inputTokens: 100, outputTokens: 20 };
    yield { type: "message", message: { role: "assistant", content: text } };
    yield { type: "turn_complete", messages: [] };
  };

const store = (ui: FakeHost) => ui.store as unknown as Store;

/** A turn runs (or waits on a question or an approval): the composer is not at rest. */
const isBusy = (ui: FakeHost) => store(ui).placeholder !== "Message the model";

/** The text of the in-flight reply: the final blocks and the tail. */
const liveText = (s: Store) =>
  [...s.live.blocks, ...s.live.tail]
    .map((block) => runsText(block.runs))
    .join("\n");

/** The text of one transcript view, block by block. */
const viewText = (view: MessageView) =>
  view.blocks.map((block) => runsText(block.runs)).join("\n");

// ------------------------------------------------------------------------ tests

test("a turn publishes the prompt, streams, keeps the reply and returns to idle", async () => {
  const { ui, client } = setup(reply("hello there"));
  await ui.fire("send", "hi");
  expect(isBusy(ui)).toBe(true);
  expect(store(ui).messages[0]).toMatchObject({ is_user: true });
  expect(store(ui).draft).toBe("");
  expect(store(ui).placeholder).toBe("Type to queue a message");
  await client.idle();
  const s = store(ui);
  expect(s.messages.map((m: MessageView) => [m.is_user, m.is_asst])).toEqual([
    [true, false],
    [false, true],
  ]);
  expect(s.live.active).toBe(false);
  expect(s.placeholder).toBe("Message the model");
  expect(s.placeholder).toBe("Message the model");
  expect(s.status).toBe("build · last <1s");
  expect(s.header.ctx_label).toBe("120/8.0k");
  expect(client.writeErrors).toEqual([]);
});

test("thinking streams into its own block, appended as it comes, and is gone once the answer starts", async () => {
  let toAnswer: () => void = () => {};
  const gate = new Promise<void>((r) => {
    toAnswer = r;
  });
  const { ui, client } = setup(async function* () {
    yield { type: "thinking", text: "one\ntwo\nthree\n" };
    await new Promise((r) => setTimeout(r, 20));
    yield { type: "thinking", text: "four\nfi" };
    await gate;
    yield { type: "text", text: "The answer " };
    await new Promise((r) => setTimeout(r, 30));
    yield {
      type: "done",
      inputTokens: 10,
      outputTokens: 40,
      evalCount: 40,
      evalDuration: 1_000_000_000,
    };
    yield {
      type: "message",
      message: { role: "assistant", content: "The answer", thinking: "one" },
    };
  });
  await ui.fire("send", "go");
  await until(() => runsText(store(ui).thinking.runs).endsWith("four\nfi"));
  const during = store(ui);
  expect(during.thinking.on).toBe(true);
  // The runs are appended as they arrive; the markup shows the end of them in a
  // three-row scroll.
  expect(runsText(during.thinking.runs)).toBe("one\ntwo\nthree\nfour\nfi");
  expect(ui.batches.flat().some((w) => w.op === "append")).toBe(true);
  expect(during.live.active).toBe(false);
  toAnswer();
  await until(() => store(ui).live.active);
  expect(store(ui).thinking.on).toBe(false);
  await client.idle();
  const s = store(ui);
  expect(s.thinking).toEqual({ on: false, runs: [] });
  // The reasoning is not in the committed history, and the speed is on the status line.
  expect(JSON.stringify(s.messages)).not.toContain("three");
  expect(s.status).toBe("build · last <1s · 40 tok/s");
  expect(client.writeErrors).toEqual([]);
});

test("a turn without timings leaves the idle status alone", async () => {
  const { ui, client } = setup(reply("hello there"));
  await ui.fire("send", "hi");
  await client.idle();
  expect(store(ui).status).toBe("build · last <1s");
});

test("the model sees the history like the Vue client sends it", async () => {
  const { ui, client, seen } = setup(reply("one"));
  await ui.fire("send", "first");
  await client.idle();
  await ui.fire("send", "second");
  await client.idle();
  expect(seen[1]?.messages).toEqual([
    "user:first",
    "assistant:one",
    "user:second",
  ]);
});

test("the live message is flushed while streaming, with one write in flight at a time", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* () {
    yield { type: "text", text: "partial reply " };
    await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: "partial reply done" },
    };
  });
  await ui.fire("send", "go");
  await until(() => liveText(store(ui)).includes("partial reply"));
  expect(store(ui).live).toMatchObject({ active: true, is_asst: true });
  expect(liveText(store(ui)).trim()).toBe("partial reply");
  release();
  await client.idle();
  expect(store(ui).live.active).toBe(false);
  expect(ui.maxInflight).toBe(1);
});

test("slash commands never reach the model and use the Vue client's words", async () => {
  const { ui, seen } = setup(reply("x"));
  await ui.fire("send", "/mode plan");
  expect(store(ui).status).toBe("Mode changed to plan");
  expect(store(ui).header.mode).toBe("plan");
  await ui.fire("send", "/help");
  expect(store(ui).help.open).toBe(true);
  // A typo stays in the composer to be fixed; a command that ran clears it.
  store(ui).draft = "/bogus";
  await ui.fire("send", "/bogus");
  expect(store(ui).status).toBe("Unknown command: /bogus. Use /help");
  expect(store(ui).draft).toBe("/bogus");
  store(ui).draft = "/theme blue";
  await ui.fire("send", "/theme blue");
  expect(store(ui).status).toContain("Unknown theme: blue");
  expect(store(ui).draft).toBe("/theme blue");
  store(ui).draft = "/mode build";
  await ui.fire("send", "/mode build");
  expect(store(ui).draft).toBe("");
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toContain("MCP servers: 0");
  expect(seen).toHaveLength(0);
  expect(store(ui).messages).toHaveLength(0);
});

test("the mode chosen by /mode is the intent of the next turn", async () => {
  const intents: (string | undefined)[] = [];
  const { ui, client } = setup(
    quiet(async ({ options }) => {
      intents.push(options.intent);
    }),
  );
  await ui.fire("send", "/mode plan");
  await ui.fire("send", "go");
  await client.idle();
  expect(intents).toEqual(["plan"]);
});

test("while busy, a slash command says Busy and a prompt is queued and runs next", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  });
  await ui.fire("send", "go");
  await ui.fire("send", "/mode plan");
  expect(store(ui).status).toBe("Busy · wait for the current turn to finish");
  await ui.fire("send", "second prompt");
  // Queued: the draft cleared, the status says so, and nothing ran yet.
  expect(store(ui).draft).toBe("");
  expect(store(ui).status).toBe("Queued: second prompt · ↑ edits it");
  expect(seen).toHaveLength(1);
  // A slash command while one is queued is still refused and keeps the queue.
  await ui.fire("send", "/mode plan");
  expect(store(ui).status).toBe("Busy · wait for the current turn to finish");
  expect(store(ui).header.mode).toBe("build");
  release();
  await client.idle();
  // The queued prompt ran on its own, once, after the first turn's reply.
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages).toEqual([
    "user:go",
    "assistant:answer 1",
    "user:second prompt",
  ]);
  expect(
    store(ui).messages.map((m: MessageView) => [m.is_user, m.is_asst]),
  ).toEqual([
    [true, false],
    [false, true],
    [true, false],
    [false, true],
  ]);
  expect(store(ui).status).toBe("build · last <1s");
});

test("a second send while one is queued is added to it, never in its place", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  });
  await ui.fire("send", "go");
  await ui.fire("send", "first queued");
  expect(store(ui).status).toBe("Queued: first queued · ↑ edits it");
  await ui.fire("send", "replacement\nsecond line");
  expect(store(ui).status).toBe(
    "Added to the queued message: replacement · ↑ edits it",
  );
  release();
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages.at(-1)).toBe(
    "user:first queued\n\nreplacement\nsecond line",
  );
});

test("up in an empty composer takes the queued send back to edit while the turn goes on", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  });
  await ui.fire("send", "go");
  await ui.fire("send", "first");
  await ui.fire("send", "second");
  await ui.fire("history_prev", "");
  expect(store(ui).draft).toBe("first\n\nsecond");
  expect(store(ui).status).toBe(
    "Queued message back to edit · the turn goes on",
  );
  // Sent again, the edit is what is queued: the old text is not kept too.
  await ui.fire("send", "first, edited");
  expect(store(ui).status).toBe("Queued: first, edited · ↑ edits it");
  expect(seen).toHaveLength(1);
  release();
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages).toEqual([
    "user:go",
    "assistant:answer 1",
    "user:first, edited",
  ]);
});

test("a queued send taken back to edit does not run when the turn ends", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client, seen } = setup(async function* () {
    await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: "answer" },
    };
  });
  await ui.fire("send", "go");
  await ui.fire("send", "later");
  expect(store(ui).status).toBe("Queued: later · ↑ edits it");
  await ui.fire("history_prev", "");
  release();
  await client.idle();
  // The turn was not cancelled: its reply is there, and nothing else ran.
  expect(seen).toHaveLength(1);
  const last = store(ui).messages.at(-1);
  expect(last && viewText(last)).toBe("answer");
  expect(store(ui).draft).toBe("later");
  expect(store(ui).status).toBe("build · last <1s");
});

test("up over a draft recalls the history and leaves the queued send queued", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  });
  await ui.fire("send", "go");
  await ui.fire("send", "queued one");
  await ui.fire("history_prev", "half-typed");
  expect(store(ui).draft).toBe("queued one");
  // A recalled message emptied by hand: up goes on through the history.
  await ui.fire("history_prev", "");
  expect(store(ui).draft).toBe("go");
  await ui.fire("history_next", "go");
  await ui.fire("history_next", "queued one");
  expect(store(ui).draft).toBe("half-typed");
  release();
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages.at(-1)).toBe("user:queued one");
});

test("a send past what a message holds stays in the composer, and so does one the queue cannot take", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client, seen } = setup(async function* () {
    await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: "done" },
    };
  });
  const huge = "x".repeat(MAX_MESSAGE_CHARS + 1);
  store(ui).draft = huge;
  await ui.fire("send", huge);
  expect(store(ui).status).toBe(
    "Not sent: 1,000,001 characters, and a message holds 1,000,000 at most. Send it in parts, or save it to a file in the workspace and ask the model to read it",
  );
  expect(store(ui).draft).toBe(huge);
  expect(seen).toHaveLength(0);
  // The queue joins sends with a blank line, up to the limit exactly.
  const most = "y".repeat(MAX_MESSAGE_CHARS - 3);
  await ui.fire("send", "go");
  await ui.fire("send", most);
  await ui.fire("send", "z");
  expect(store(ui).status).toBe("Added to the queued message: z · ↑ edits it");
  store(ui).draft = "z";
  await ui.fire("send", "z");
  expect(store(ui).status).toMatch(
    /^Not added to the queued message, together: 1,000,003 characters, /,
  );
  expect(store(ui).draft).toBe("z");
  release();
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages.at(-1)).toBe(`user:${most}\n\nz`);
});

test("a queued send given back as a turn ends is never overwritten by its prompt", async () => {
  const { ui, client, seen } = setup(async function* ({ options }) {
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
    yield* [] as AgentEvent[];
  });
  await ui.fire("send", "go");
  await ui.fire("send", "queued behind it");
  // The give-back is still in flight when the turn end reads the draft.
  ui.ackMs = 30;
  await ui.fire("quit_request");
  await client.idle();
  expect(store(ui).draft).toBe("queued behind it");
  expect(seen).toHaveLength(1);
});

test("ctrl+c while a cancelled turn winds down gives a send queued behind it back", async () => {
  let finish: () => void = () => {};
  const wound = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const { ui, client, seen } = setup(
    quiet(async ({ options }) => {
      // The turn ignores the abort for a while, as a slow tool can.
      await new Promise<void>((resolve) =>
        options.signal.addEventListener("abort", () => resolve()),
      );
      await wound;
    }),
    { session: fakeSession("abc123", []) },
  );
  await ui.fire("send", "go");
  expect(await ui.fire("quit_request")).toBeUndefined();
  // Typed and sent while the cancelled turn has not ended yet.
  await ui.fire("send", "after the cancel");
  expect(store(ui).status).toBe("Queued: after the cancel · ↑ edits it");
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(store(ui).draft).toBe("after the cancel");
  expect(store(ui).status).toBe("Queued message back in the composer");
  finish();
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(store(ui).draft).toBe("after the cancel");
});

test("esc cancels the turn and gives the queued message back to the composer", async () => {
  const { ui, client, seen } = setup(async function* ({ options }) {
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
    yield* [] as AgentEvent[];
  });
  await ui.fire("send", "slow one");
  await ui.fire("send", "queued one");
  expect(store(ui).status).toBe("Queued: queued one · ↑ edits it");
  store(ui).draft = "typed meanwhile";
  await ui.fire("typed", "typed meanwhile");
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe(
    "Cancelled · queued message back in the composer",
  );
  // After what was typed since, never in its place.
  expect(store(ui).draft).toBe("typed meanwhile\nqueued one");
  expect(seen).toHaveLength(1);
  expect(isBusy(ui)).toBe(false);
});

test("esc after a queued send was taken back to edit cancels the turn and leaves the edit alone", async () => {
  const { ui, client, seen } = setup(async function* ({ options }) {
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
    yield* [] as AgentEvent[];
  });
  await ui.fire("send", "slow one");
  await ui.fire("send", "queued one");
  await ui.fire("history_prev", "");
  expect(store(ui).draft).toBe("queued one");
  await ui.fire("cancel");
  await client.idle();
  // Nothing was queued any more: the edit is not given back a second time.
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).draft).toBe("queued one");
  expect(seen).toHaveLength(1);
  expect(isBusy(ui)).toBe(false);
});

test("a queued prompt runs after a failed turn too, without duplicating the failed one", async () => {
  let fail: () => void = () => {};
  const gate = new Promise<void>((r) => {
    fail = r;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) {
      await gate;
      throw new Error("daemon is down");
    }
    yield {
      type: "message",
      message: { role: "assistant", content: "recovered" },
    };
  });
  await ui.fire("send", "doomed prompt");
  await ui.fire("send", "queued one");
  fail();
  await client.idle();
  // Prompt, its error notice, then the queued turn and its reply.
  const views = store(ui).messages;
  expect(
    views.map((m: MessageView) => [m.is_user, m.is_error, m.is_asst]),
  ).toEqual([
    [true, false, false],
    [false, true, false],
    [true, false, false],
    [false, false, true],
  ]);
  expect(viewText(views[1] as MessageView)).toContain("daemon is down");
  // No session: the failed prompt left the model's history, so the queued
  // turn is the whole conversation — nothing duplicated, notice never sent.
  expect(seen[1]?.messages).toEqual(["user:queued one"]);
  expect(store(ui).status).toBe("build · last <1s");
});

function editProposal(diff = "-x\n+y"): ApprovalRequest {
  return {
    kind: "edit",
    path: "a.txt",
    before: "",
    after: "",
    diff,
    identity: "i",
  };
}

/** What `approve` resolved to, one entry per approval of the running test. */
const decisions: Approval[] = [];

const withApproval: Script = async function* ({ options }) {
  yield {
    type: "message",
    message: {
      role: "assistant",
      content: "",
      tool_calls: [
        { id: "c1", function: { name: "edit", arguments: { path: "a.txt" } } },
      ],
    },
  };
  yield { type: "tool_requested", id: "e1", name: "edit" };
  const allowed = await options.approve(editProposal(), options.signal);
  decisions.push(allowed);
  if (allowed) yield { type: "tool_start", id: "e1", name: "edit" };
  const status = allowed ? "succeeded" : "denied";
  const message: Message = {
    role: "tool",
    tool_name: "edit",
    tool_call_id: "c1",
    content: JSON.stringify({
      status,
      output: allowed ? "Updated a.txt" : "Edit denied by host",
    }),
  };
  yield { type: "message", message };
  yield { type: "tool_result", id: "e1", name: "edit", status, output: "o" };
};

/** Waits for `ok`, about a second at most; a condition that never holds fails the test. */
async function until(ok: () => boolean) {
  for (let i = 0; i < 200 && !ok(); i++)
    await new Promise((r) => setTimeout(r, 5));
  if (!ok()) throw new Error(`until: still false after 1 s: ${ok}`);
}

/** The armed status of a command card with the sandbox on: no `a`. */
const SANDBOX_ARMED_STATUS =
  "Waiting for approval · y allow · n deny · ! always · Esc deny";
/** The armed status of a card that offers no grant (a secret path). */
const NO_GRANT_ARMED_STATUS =
  "Waiting for approval · y allow · n deny · Esc deny";

/**
 * Until the dialog is open and has armed itself: its status offers "y
 * allow" and names the grant keys that act.
 */
async function untilApproval(ui: FakeHost, status = APPROVAL_STATUS) {
  await until(() => store(ui).status === status);
  expect(store(ui).approval.open).toBe(true);
}

/**
 * Holds the approval dialog's arm timer: it fires when the test calls `arm()`,
 * never on the clock, so no key can race it. `release()` puts the real timer back.
 */
function holdArmTimer() {
  const armMs = 80_000;
  const held: (() => void)[] = [];
  let fired = 0;
  const real = globalThis.setTimeout;
  globalThis.setTimeout = ((
    fn: () => void,
    ms?: number,
    ...rest: unknown[]
  ) => {
    if (ms !== armMs) return real(fn, ms, ...rest);
    held.push(fn);
    return 0;
  }) as unknown as typeof setTimeout;
  return {
    armMs,
    /** Arms the next dialog that has opened (waits for its timer to be set). */
    async arm() {
      await until(() => held.length > fired);
      held[fired++]?.();
    },
    release() {
      globalThis.setTimeout = real;
    },
  };
}

test("approval: the dialog opens with the tool card, and allow resolves the promise", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  expect(store(ui).approval.title).toBe("Edit a.txt?");
  expect(store(ui).live).toMatchObject({
    active: true,
    is_tool: true,
    card: { name: "edit", status: "waiting for approval", args: "a.txt" },
  });
  expect(store(ui).status).toBe(APPROVAL_STATUS);
  await ui.fire("dialog_confirm");
  expect(store(ui).approval.open).toBe(false);
  expect(decisions).toEqual([true]);
  await client.idle();
  const tool = store(ui).messages.find((m: MessageView) => m.is_tool);
  expect(tool?.card).toMatchObject({
    ok: true,
    status: "succeeded",
    args: "a.txt",
  });
  expect(store(ui).live.active).toBe(false);
});

test("a tool that ran this turn says how long it took; a refused one does not", async () => {
  let nowMs = 5_000;
  const ran: Script = async function* () {
    for (const [id, started] of [
      ["r1", true],
      ["r2", false],
    ] as const) {
      yield {
        type: "message",
        message: {
          role: "assistant",
          content: "",
          tool_calls: [
            {
              id,
              function: { name: "read", arguments: { path: `${id}.txt` } },
            },
          ],
        },
      };
      yield { type: "tool_requested", id, name: "read" };
      if (started) yield { type: "tool_start", id, name: "read" };
      nowMs += 1_500;
      yield {
        type: "message",
        message: {
          role: "tool",
          tool_name: "read",
          tool_call_id: id,
          content: JSON.stringify({
            status: started ? "succeeded" : "denied",
            output: "o",
          }),
        },
      };
    }
  };
  const { ui, client } = setup(ran, { now: () => nowMs });
  await ui.fire("send", "read twice");
  await client.idle();
  const cards = store(ui)
    .messages.filter((m: MessageView) => m.is_tool)
    .map((m: MessageView) => [m.card.args, m.card.took]);
  expect(cards).toEqual([
    ["r1.txt", "1.5s"],
    ["r2.txt", ""],
  ]);
});

test("a parallel batch times each call from its start to its own result", async () => {
  let nowMs = 5_000;
  const ids = ["p1", "p2", "p3"];
  const call = (id: string) => ({
    id,
    function: { name: "read", arguments: { path: `${id}.txt` } },
  });
  const tool = (id: string) =>
    ({
      type: "message",
      message: {
        role: "tool",
        tool_name: "read",
        tool_call_id: id,
        content: JSON.stringify({ status: "succeeded", output: "o" }),
      },
    }) as const;
  const result = (id: string) =>
    ({
      type: "tool_result",
      id,
      name: "read",
      status: "succeeded",
      output: "o",
    }) as const;
  // The runtime's parallel shape: every request and start first, results
  // as calls finish (p3, then p1, then p2), messages in call order after.
  const batch: Script = async function* () {
    yield {
      type: "message",
      message: { role: "assistant", content: "", tool_calls: ids.map(call) },
    };
    for (const id of ids) {
      yield { type: "tool_requested", id, name: "read" };
      yield { type: "tool_start", id, name: "read" };
    }
    nowMs += 300;
    yield result("p3");
    nowMs += 900;
    yield result("p1");
    nowMs += 1_800;
    yield result("p2");
    for (const id of ids) yield tool(id);
  };
  const { ui, client } = setup(batch, { now: () => nowMs });
  await ui.fire("send", "read three");
  await client.idle();
  const cards = store(ui)
    .messages.filter((m: MessageView) => m.is_tool)
    .map((m: MessageView) => [m.card.args, m.card.took]);
  expect(cards).toEqual([
    ["p1.txt", "1.2s"],
    ["p2.txt", "3.0s"],
    ["p3.txt", "0.3s"],
  ]);
});

// ------------------------------------------------------------------ ask_user

const STORE_QUESTION: Question = {
  question: "Which store?",
  kind: "single",
  options: [
    { id: "pg", label: "Postgres" },
    { id: "lite", label: "SQLite", detail: "one file" },
    { id: "mem", label: "Memory" },
  ],
};
const PORT_QUESTION: Question = {
  question: "Which port?",
  kind: "freetext",
  options: [],
};
const DEPLOY_QUESTION: Question = {
  question: "Deploy now?",
  kind: "yes_no",
  options: [],
};

/**
 * A turn that asks `question` once `gate` opens (the user may type
 * meanwhile), records the answer in `answers`, and finishes with a reply.
 */
function asking(
  question: Question,
  answers: Answer[],
  gate: Promise<void> = Promise.resolve(),
): Script {
  return async function* ({ options }) {
    await gate;
    yield {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            id: "q1",
            function: { name: "ask_user", arguments: { ...question } },
          },
        ],
      },
    };
    yield { type: "tool_requested", id: "a1", name: "ask_user" };
    if (!options.ask) throw new Error("the client offers no ask");
    const answer = await options.ask(question, options.signal);
    answers.push(answer);
    yield { type: "tool_start", id: "a1", name: "ask_user" };
    const status = answer.kind === "declined" ? "denied" : "succeeded";
    yield {
      type: "message",
      message: {
        role: "tool",
        tool_name: "ask_user",
        tool_call_id: "q1",
        content: JSON.stringify({ status, output: "o" }),
      },
    };
    yield {
      type: "tool_result",
      id: "a1",
      name: "ask_user",
      status,
      output: "o",
    };
    yield {
      type: "message",
      message: { role: "assistant", content: "after the answer" },
    };
  };
}

/** The question is open and has armed itself (the status line says so). */
async function untilQuestion(ui: FakeHost) {
  await until(() => store(ui).status === QUESTION_STATUS);
  expect(store(ui).question.open).toBe(true);
}

test("a question opens above the composer: the arrows choose, enter answers, the draft comes back", async () => {
  const answers: Answer[] = [];
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(asking(STORE_QUESTION, answers, gate));
  await ui.fire("send", "go");
  // A follow-up half typed while the turn runs is put aside, not answered with.
  await ui.fire("typed", "and then the tests");
  release();
  await until(() => store(ui).question.open);
  expect(store(ui).draft).toBe("");
  expect(store(ui).status).toBe(QUESTION_WAIT_STATUS);
  expect(store(ui).placeholder).toBe(QUESTION_PLACEHOLDER_CHOICE);
  expect(store(ui).live.card).toMatchObject({
    title: "Question",
    // The head says the turn waits; the card does not repeat it, and
    // nothing on it spins beside the head's still glyph.
    status: "asked, answer below",
    running: false,
    mark: "?",
  });
  expect(store(ui).head).toMatchObject({ still: true, spin: false });
  const q = store(ui).question;
  // A single choice starts with none chosen.
  expect(q.rows.map((row) => [row.label, row.detail, row.mark])).toEqual([
    ["1 Postgres", "", ""],
    ["2 SQLite", "· one file", ""],
    ["3 Memory", "", ""],
  ]);
  // An enter already on its way when it opened is not the answer.
  await ui.fire("send", "");
  expect(store(ui).status).toBe(QUESTION_UNARMED_STATUS);
  expect(answers).toEqual([]);
  await untilQuestion(ui);
  // Armed, enter with nothing chosen answers nothing either.
  await ui.fire("send", "");
  expect(store(ui).status).toBe(QUESTION_CHOOSE_STATUS);
  expect(answers).toEqual([]);
  // Down takes the first row; three of them end clamped on the last.
  await ui.fire("question_next");
  expect(store(ui).question.rows.map((row) => row.sel)).toEqual([
    true,
    false,
    false,
  ]);
  for (const action of ["question_next", "question_next", "question_next"])
    await ui.fire(action);
  // Clamped at the last row, then back one.
  expect(store(ui).question.rows.map((row) => row.sel)).toEqual([
    false,
    false,
    true,
  ]);
  await ui.fire("question_prev");
  await ui.fire("send", "");
  await client.idle();
  expect(answers).toEqual([{ kind: "choice", id: "lite" }]);
  expect(store(ui).question.open).toBe(false);
  expect(store(ui).draft).toBe("and then the tests");
  // The card has no duration: the wait was the user's.
  const card = store(ui).messages.find((m: MessageView) => m.is_tool)?.card;
  expect(card).toMatchObject({ title: "Question", took: "", mark: "✓" });
  expect(liveText(store(ui))).toBe("");
  expect(
    store(ui).messages.some((m: MessageView) =>
      runsText(m.blocks.flatMap((b) => b.runs)).includes("after the answer"),
    ),
  ).toBe(true);
});

test("a typed answer goes to the question: never the queue, a command or the popup", async () => {
  const answers: Answer[] = [];
  const { ui, client } = setup(asking(PORT_QUESTION, answers));
  await ui.fire("send", "go");
  await untilQuestion(ui);
  expect(store(ui).placeholder).toBe(QUESTION_PLACEHOLDER_TEXT);
  await ui.fire("typed", "/he");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("send", "  \n ");
  expect(store(ui).status).toBe(QUESTION_EMPTY_STATUS);
  await ui.fire("send", "/help");
  await client.idle();
  expect(answers).toEqual([{ kind: "text", text: "/help" }]);
  expect(store(ui).status).not.toContain("Queued");
  expect(store(ui).help.open).toBe(false);
  expect(store(ui).placeholder).not.toBe(QUESTION_PLACEHOLDER_TEXT);
});

test("a choice can be typed: its number, its label, y or n; anything else says so", async () => {
  const answers: Answer[] = [];
  const { ui, client } = setup(asking(DEPLOY_QUESTION, answers));
  await ui.fire("send", "go");
  await untilQuestion(ui);
  // A yes/no starts on no, as a skipped one reads.
  expect(store(ui).question.rows.map((row) => row.mark)).toEqual(["", "›"]);
  await ui.fire("send", "maybe");
  expect(store(ui).status).toBe(QUESTION_NO_MATCH_STATUS);
  expect(store(ui).question.open).toBe(true);
  await ui.fire("send", "Y");
  await client.idle();
  expect(answers).toEqual([{ kind: "choice", id: "yes" }]);

  const picked: Answer[] = [];
  const second = setup(asking(STORE_QUESTION, picked));
  await second.ui.fire("send", "go");
  await untilQuestion(second.ui);
  await second.ui.fire("send", "3");
  await second.client.idle();
  const third: Answer[] = [];
  const byLabel = setup(asking(STORE_QUESTION, third));
  await byLabel.ui.fire("send", "go");
  await untilQuestion(byLabel.ui);
  await byLabel.ui.fire("send", " sqlite ");
  await byLabel.client.idle();
  expect([...picked, ...third]).toEqual([
    { kind: "choice", id: "mem" },
    { kind: "choice", id: "lite" },
  ]);
});

test("esc skips the question and the turn goes on; the status line is the question's", async () => {
  let nowMs = 1_000_000;
  const answers: Answer[] = [];
  const { ui, client } = setup(asking(STORE_QUESTION, answers), {
    now: () => nowMs,
  });
  await ui.fire("send", "go");
  await untilQuestion(ui);
  // The elapsed ticker does not write over the question's status.
  nowMs += 5_000;
  await client.flush();
  expect(store(ui).status).toBe(QUESTION_STATUS);
  await ui.fire("question_skip");
  await client.idle();
  expect(answers).toEqual([{ kind: "declined" }]);
  expect(store(ui).question.open).toBe(false);
  const card = store(ui).messages.find((m: MessageView) => m.is_tool)?.card;
  expect(card).toMatchObject({ mark: "✗", status: "denied" });
  expect(
    store(ui).messages.some((m: MessageView) =>
      runsText(m.blocks.flatMap((b) => b.runs)).includes("after the answer"),
    ),
  ).toBe(true);
});

test("up from no choice takes the last row", async () => {
  const answers: Answer[] = [];
  const { ui, client } = setup(asking(STORE_QUESTION, answers));
  await ui.fire("send", "go");
  await untilQuestion(ui);
  await ui.fire("question_prev");
  await ui.fire("send", "");
  await client.idle();
  expect(answers).toEqual([{ kind: "choice", id: "mem" }]);
});

test("the palette does not open over a question", async () => {
  const { ui } = setup(asking(STORE_QUESTION, []));
  await ui.fire("send", "go");
  await untilQuestion(ui);
  await ui.fire("palette_open");
  expect(store(ui).palette.open).toBe(false);
  expect(store(ui).question.open).toBe(true);
});

test("a question still open when its turn ends is closed, unanswered", async () => {
  let outcome = "pending";
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "tool_requested", id: "a1", name: "ask_user" };
    // A runtime that ends without waiting for the answer.
    options.ask?.(STORE_QUESTION, options.signal).then(
      () => {
        outcome = "answered";
      },
      () => {
        outcome = "rejected";
      },
    );
    await until(() => store(ui).question.open);
    yield { type: "message", message: { role: "assistant", content: "ok" } };
  });
  await ui.fire("send", "go");
  await client.idle();
  expect(store(ui).question.open).toBe(false);
  expect(outcome).toBe("rejected");
  // A later send is a message, not an answer.
  expect(store(ui).placeholder).not.toBe(QUESTION_PLACEHOLDER_CHOICE);
});

test("cancelling the turn closes an open question without an answer", async () => {
  const answers: Answer[] = [];
  const { ui, client } = setup(asking(STORE_QUESTION, answers));
  await ui.fire("send", "go");
  await untilQuestion(ui);
  await ui.fire("cancel");
  await client.idle();
  expect(answers).toEqual([]);
  expect(store(ui).question.open).toBe(false);
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).placeholder).not.toBe(QUESTION_PLACEHOLDER_CHOICE);
  // A later send is a message again.
  expect(isBusy(ui)).toBe(false);
});

test("approval: deny, and esc (also dialog_cancel), keep the edit out", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
  const tool = store(ui).messages.find((m: MessageView) => m.is_tool);
  expect(tool?.card).toMatchObject({ bad: true, status: "denied" });
  expect(store(ui).approval.open).toBe(false);
});

test("a cancelled turn says Cancelled, takes the prompt back and restores the draft", async () => {
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "text", text: "thinking " };
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  });
  await ui.fire("send", "slow one");
  await until(() => liveText(store(ui)).includes("thinking"));
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).messages).toHaveLength(0);
  expect(store(ui).draft).toBe("slow one");
  expect(isBusy(ui)).toBe(false);
});

test("fullscreen: a cancelled or failed reply keeps what had arrived", async () => {
  let calls = 0;
  const { ui, client, seen } = setup(async function* ({ options }) {
    if (++calls > 1) {
      yield { type: "message", message: { role: "assistant", content: "ok" } };
      return;
    }
    yield {
      type: "message",
      message: { role: "assistant", content: "First, the plan." },
    };
    yield { type: "text", text: "half an " };
    yield { type: "text", text: "answer" };
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  });
  await ui.fire("send", "slow one");
  await until(() => liveText(store(ui)).includes("half an answer"));
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).messages.map(viewText)).toEqual([
    "slow one",
    "First, the plan.",
    "half an answer",
  ]);
  expect(store(ui).messages[2]?.is_asst).toBe(true);
  // Only a view: the next turn's history has the finished message, not this.
  await ui.fire("send", "next");
  await client.idle();
  expect(seen[1]?.messages).toEqual([
    "user:slow one",
    "assistant:First, the plan.",
    "user:next",
  ]);

  const failed = setup(async function* () {
    yield { type: "text", text: "partway" };
    throw new Error("connection reset");
  });
  await failed.ui.fire("send", "go");
  await failed.client.idle();
  const views = store(failed.ui).messages;
  expect(views.map(viewText)).toEqual(["go", "partway", "connection reset"]);
  expect(views[2]?.is_error).toBe(true);
});

test("fullscreen: a stopped reply past the view's bounds does not promise the rest", async () => {
  const long = function* () {
    yield {
      type: "message",
      message: { role: "assistant", content: "First, the plan." },
    } as const;
    for (let n = 0; n < MAX_MESSAGE_BLOCKS + 20; n++)
      yield { type: "text", text: `paragraph ${n}\n\n` } as const;
  };
  const check = (reply: string) => {
    expect(reply).toContain("paragraph 0");
    expect(reply).not.toContain("shown when it is finished");
    expect(reply).toContain("the turn stopped before it finished");
  };
  // The generator resumes after `long` once the controller has taken all of it.
  let streamed = false;
  const cancelled = setup(async function* ({ options }) {
    yield* long();
    streamed = true;
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  });
  await cancelled.ui.fire("send", "long one");
  await until(() => streamed);
  await cancelled.ui.fire("cancel");
  await cancelled.client.idle();
  expect(store(cancelled.ui).status).toBe("Cancelled");
  check(viewText(store(cancelled.ui).messages.at(-1) as MessageView));

  const failed = setup(async function* () {
    yield* long();
    throw new Error("connection reset");
  });
  await failed.ui.fire("send", "long one");
  await failed.client.idle();
  const views = store(failed.ui).messages;
  check(viewText(views.at(-2) as MessageView));
  expect(viewText(views.at(-1) as MessageView)).toBe("connection reset");
});

test("esc while an approval is pending denies it", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  client.abort();
  await client.idle();
  expect(store(ui).approval.open).toBe(false);
  expect(isBusy(ui)).toBe(false);
  // Aborting is a deny: the promise resolved to false, not to a wrong true.
  expect(decisions).toEqual([false]);
});

test("esc with an approval open and a send queued denies it and gives the send back", async () => {
  // Like the runtime's loop, the turn stops on the aborted signal once the deny is in.
  const { ui, client, seen } = setup(async function* (ctx) {
    yield* withApproval(ctx);
    ctx.options.signal.throwIfAborted();
  });
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("send", "queued one");
  // The real Esc of a turn, not the dialog's own cancel.
  await ui.fire("cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
  expect(store(ui).approval.open).toBe(false);
  expect(store(ui).status).toBe(
    "Cancelled · queued message back in the composer",
  );
  // Back in the composer, not run as a second turn.
  expect(store(ui).draft).toBe("queued one");
  expect(seen).toHaveLength(1);
  expect(isBusy(ui)).toBe(false);
});

test("ctrl+c (quit) with the dialog open denies it and cancels the turn; the next press quits", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  expect(await ui.fire("quit_request")).toBeUndefined();
  await client.idle();
  expect(decisions).toEqual([false]);
  expect(ui.statusLog()).toContain("Turn cancelled · ctrl+c again to quit");
  expect(await ui.fire("quit_request")).toBe("quit");
});

test("the process signal with the dialog open resolves the approval to deny", async () => {
  const { ui, client, aborts } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  aborts.abort();
  await client.idle();
  expect(decisions).toEqual([false]);
});

test("a confirm before the dialog is armed is ignored; deny always works", async () => {
  const timer = holdArmTimer();
  try {
    const { ui, client } = setup(withApproval, { armMs: timer.armMs });
    await ui.fire("send", "edit");
    await until(() => store(ui).approval.open);
    expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
    await ui.fire("dialog_confirm");
    expect(store(ui).approval.open).toBe(true);
    expect(decisions).toEqual([]);
    // The swallowed `y` is not silent: the status says to press it again.
    expect(store(ui).status).toBe(APPROVAL_UNARMED_STATUS);
    await timer.arm();
    await until(() => store(ui).status === APPROVAL_STATUS);
    await ui.fire("dialog_confirm");
    expect(decisions).toEqual([true]);
    await client.idle();

    // The same turn again: deny works at once, before the arm delay.
    await ui.fire("send", "edit again");
    await until(() => store(ui).approval.open);
    expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
    await ui.fire("dialog_cancel");
    expect(decisions).toEqual([true, false]);
    await client.idle();
  } finally {
    timer.release();
  }
});

test("an approval is armed by default 300 ms after it opens", () => {
  expect(APPROVAL_ARM_MS).toBe(300);
});

test("resizing wraps an open approval preview again", async () => {
  let cols = 100;
  const { ui, client } = setup(
    async function* ({ options }) {
      yield { type: "tool_requested", id: "e1", name: "edit" };
      await options.approve(
        editProposal(`+${"x".repeat(150)}`),
        options.signal,
      );
    },
    { columns: () => cols },
  );
  await ui.fire("send", "edit");
  await untilApproval(ui);
  const wide = store(ui).approval.lines.length;
  cols = 40;
  await client.resize();
  expect(store(ui).approval.lines.length).toBeGreaterThan(wide);
  expect(store(ui).approval.open).toBe(true);
  client.abort();
  await client.idle();
});

test("loading earlier messages mid-turn keeps the streaming message", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const session = {
    store: {},
    state: {
      messages: Array.from({ length: 450 }, (_, i) => ({
        role: i % 2 ? "assistant" : "user",
        content: `m${i}`,
      })),
    },
  } as unknown as SessionContext;
  const { ui, client } = setup(
    async function* () {
      yield { type: "text", text: "first half " };
      await gate;
      yield {
        type: "message",
        message: { role: "assistant", content: "first half second half" },
      };
    },
    { session },
  );
  await ui.fire("send", "go");
  await until(() => store(ui).live.active);
  await ui.fire("load_earlier");
  expect(store(ui).messages.length).toBeGreaterThan(200);
  expect(store(ui).live.active).toBe(true);
  expect(liveText(store(ui)).trim()).toBe("first half");
  release();
  await client.idle();
  expect(store(ui).live.active).toBe(false);
});

test("idle() waits for a turn that is aborted while its first write is in flight", async () => {
  let started = false;
  const { ui, client, aborts } = setup(
    quiet(async () => {
      started = true;
    }),
  );
  const sent = ui.fire("send", "go");
  aborts.abort();
  await client.idle();
  await sent;
  // The turn was cancelled before it started: the runtime never ran.
  expect(started).toBe(false);
  expect(isBusy(ui)).toBe(false);
});

test("a refused write is shown on the status line, once, and remembered in a bounded list", async () => {
  const { ui, client } = setup(reply("hi"));
  const original = ui.batch.bind(ui);
  let failing = true;
  ui.batch = async (writes) => {
    if (failing && writes.some((w) => w.path === "messages"))
      return { type: "ack" as const, id: "x", ok: false, error: "too big" };
    return original(writes);
  };
  await ui.fire("send", "hi");
  await client.idle();
  expect(store(ui).status).toContain("UI update failed: too big");
  expect(client.writeErrors.length).toBeGreaterThan(0);
  for (let turn = 0; turn < 12; turn++) {
    await ui.fire("send", `again ${turn}`);
    await client.idle();
  }
  expect(client.writeErrors.length).toBe(20);
  failing = false;
});

test("a transport that throws is shown on the status line too, and the turn still ends", async () => {
  const { ui, client } = setup(reply("hi"));
  const original = ui.batch.bind(ui);
  ui.batch = async (writes) => {
    if (writes.some((w) => w.path === "messages"))
      throw new Error("pipe closed");
    return original(writes);
  };
  await ui.fire("send", "hi");
  await client.idle();
  expect(store(ui).status).toContain("UI update failed: pipe closed");
  expect(client.writeErrors).toContain("pipe closed");
  expect(isBusy(ui)).toBe(false);
});

test("inline: a commit that throws is shown on the status line, and the turn still ends", async () => {
  const { ui, client } = setup(reply("alpha beta"), inline);
  ui.commit = async () => {
    throw new Error("pipe closed");
  };
  await ui.fire("send", "hi");
  await client.idle();
  // The idle status comes after the notice, so it is in the log, not on screen.
  expect(ui.statusLog()).toContain("UI update failed: pipe closed");
  expect(client.writeErrors).toContain("pipe closed");
  expect(isBusy(ui)).toBe(false);
});

test("a multi-line error is one line on the status row and whole in its notice", async () => {
  const { ui, client } = setup(
    quiet(async () => {
      throw new Error('[\n  {\n    "code": "too_big"\n  }\n]');
    }),
  );
  await ui.fire("send", "go");
  await client.idle();
  expect(store(ui).status).toBe('[ { "code": "too_big" } ]');
  const notice = store(ui).messages.find((m: MessageView) => m.is_error);
  expect(notice).toBeDefined();
  // The notice keeps the error's own lines, not the clipped status form.
  expect(viewText(notice as MessageView)).toContain('"code": "too_big"');
});

test("the MCP count stays on the status line after a turn", async () => {
  const mcp = [{ tools: [] }] as unknown as McpConnection[];
  const { ui, client } = setup(reply("x"), { mcp });
  await ui.fire("send", "go");
  await client.idle();
  expect(store(ui).status).toContain("MCP 1");
});

test("/help from the palette works while a turn runs, other commands say Busy", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    quiet(async () => {
      await gate;
    }),
  );
  await ui.fire("send", "go");
  await ui.fire("palette_open");
  store(ui).palette.query = "help";
  store(ui).palette.cur = "help";
  await ui.fire("palette_run");
  expect(store(ui).help.open).toBe(true);
  store(ui).palette.query = "mode plan";
  store(ui).palette.cur = "mode-plan";
  await ui.fire("palette_run");
  expect(store(ui).status).toBe("Busy · wait for the current turn to finish");
  release();
  await client.idle();
});

test("a failed request shows its message and keeps a typed draft", async () => {
  let fail: () => void = () => {};
  const gate = new Promise<void>((r) => {
    fail = r;
  });
  const { ui, client } = setup(
    quiet(async () => {
      await gate;
      throw new Error("Model admission blocked");
    }),
  );
  await ui.fire("send", "go");
  store(ui).draft = "typing already";
  fail();
  await client.idle();
  expect(store(ui).status).toBe("Model admission blocked");
  expect(store(ui).draft).toBe("typing already");
  // The failed prompt stays visible, with its error notice under it.
  const views = store(ui).messages;
  expect(views.map((m: MessageView) => [m.is_user, m.is_error])).toEqual([
    [true, false],
    [false, true],
  ]);
  expect(viewText(views[0] as MessageView)).toBe("go");
  expect(viewText(views[1] as MessageView)).toContain(
    "Model admission blocked",
  );
  // And it is in the draft history: up recalls it over the typed draft.
  await ui.fire("history_prev", "typing already");
  expect(store(ui).draft).toBe("go");
});

test("a failed request with an empty composer gives the prompt back", async () => {
  const { ui, client } = setup(
    quiet(async () => {
      throw new Error("down");
    }),
  );
  await ui.fire("send", "retry me");
  await client.idle();
  expect(store(ui).draft).toBe("retry me");
  // It also stays in view (with its notice), one prompt view, not two.
  expect(store(ui).messages.filter((m: MessageView) => m.is_user)).toHaveLength(
    1,
  );
  expect(
    store(ui).messages.filter((m: MessageView) => m.is_error),
  ).toHaveLength(1);
});

test("with a session, a failed request keeps the completed messages", async () => {
  const session = {
    store: {},
    state: {
      messages: [
        { role: "user", content: "old" },
        { role: "assistant", content: "older" },
      ],
    },
  } as unknown as SessionContext;
  let calls = 0;
  const { ui, client, seen } = setup(
    async function* () {
      calls++;
      if (calls === 1) throw new Error("boom");
      yield {
        type: "message",
        message: { role: "assistant", content: "recovered" },
      };
    },
    { session },
  );
  expect(store(ui).messages).toHaveLength(2);
  await ui.fire("send", "next");
  await client.idle();
  // The saved pair, the failed prompt, and its notice.
  expect(
    store(ui).messages.map((m: MessageView) => [m.is_user, m.is_error]),
  ).toEqual([
    [true, false],
    [false, false],
    [true, false],
    [false, true],
  ]);
  expect(store(ui).status).toBe("boom");
  // With a session the runtime checkpointed the prompt before inference, so
  // it stays in the model's history — exactly once, and the notice (never a
  // model message) is not in what the next turn sends.
  await ui.fire("send", "again");
  await client.idle();
  expect(seen[1]?.messages).toEqual([
    "user:old",
    "assistant:older",
    "user:next",
    "user:again",
  ]);
});

test("elapsed reads as seconds, then minutes and seconds", () => {
  expect(formatElapsed(0)).toBe("0s");
  expect(formatElapsed(59)).toBe("59s");
  expect(formatElapsed(60)).toBe("1m 00s");
  expect(formatElapsed(65)).toBe("1m 05s");
  expect(formatElapsed(600)).toBe("10m 00s");
});

/** Every status value the turn wrote, in order. */
const statusWrites = (ui: FakeHost) =>
  ui.batches
    .flat()
    .filter((w: { path: string }) => w.path === "status")
    .map((w: { value: unknown }) => w.value);

test("the head walks waiting, thinking, streaming and done; the status bar never moves", async () => {
  let nowMs = 1_000_000;
  const gates = [0, 1, 2].map(() => {
    let open: () => void = () => {};
    const done = new Promise<void>((r) => {
      open = r;
    });
    return { open, done };
  });
  const { ui, client } = setup(
    async function* () {
      await gates[0]?.done;
      yield { type: "thinking", text: "hmm\n" };
      await gates[1]?.done;
      yield { type: "text", text: "hi " };
      await gates[2]?.done;
      yield {
        type: "done",
        inputTokens: 10,
        outputTokens: 40,
        evalCount: 40,
        evalDuration: 1_000_000_000,
      };
      yield { type: "message", message: { role: "assistant", content: "hi" } };
    },
    { now: () => nowMs },
  );
  expect(store(ui).head.on).toBe(false);
  const rest = store(ui).status;
  expect(rest).toBe("build");
  await ui.fire("send", "go");
  // From the moment the turn starts: the assistant's head, a spinner, and
  // what it waits for. No "Generating", no sparkline path, one status.
  expect(store(ui).head).toEqual({
    on: true,
    spin: true,
    still: false,
    glyph: "⠿",
    text: "waiting for the model · 0s",
  });
  expect(store(ui).status).toBe(rest);
  expect(store(ui).placeholder).toBe("Type to queue a message");
  expect("rate" in store(ui)).toBe(false);
  nowMs += 12_000;
  await client.flush();
  expect(store(ui).head.text).toBe("waiting for the model · 12s");
  // Within the same second nothing more is written.
  const batches = ui.batches.length;
  await client.flush();
  await client.flush();
  expect(ui.batches.length).toBe(batches);
  gates[0]?.open();
  await until(() => store(ui).head.text.startsWith("thinking"));
  expect(store(ui).head).toMatchObject({ on: true, spin: true, still: false });
  expect(store(ui).head.text).toBe("thinking · 12s");
  // The reasoning is in the message, under the head.
  await until(() => store(ui).thinking.on);
  gates[1]?.open();
  await until(() => store(ui).head.text === "12s");
  // Streaming: the elapsed alone, the text is the indication.
  expect(store(ui).head.spin).toBe(true);
  await until(() => store(ui).live.active);
  expect(store(ui).thinking.on).toBe(false);
  nowMs += 53_000;
  await client.flush();
  expect(store(ui).head.text).toBe("1m 05s");
  expect(store(ui).status).toBe(rest);
  gates[2]?.open();
  await client.idle();
  // Done: no head, so no spinner anywhere, and the bar keeps the last turn.
  expect(store(ui).head).toEqual({
    on: false,
    spin: false,
    still: false,
    glyph: "⠿",
    text: "",
  });
  expect(store(ui).status).toBe("build · last 1m 05s · 40 tok/s");
  expect(store(ui).placeholder).toBe("Message the model");
  // Busy or not, the bar only ever held the one ambient value until the end.
  expect(new Set(statusWrites(ui))).toEqual(
    new Set([rest, "build · last 1m 05s · 40 tok/s"]),
  );
  // The timer is gone with the turn: time passing writes nothing more.
  const after = ui.batches.length;
  nowMs += 120_000;
  await client.flush();
  await new Promise((r) => setTimeout(r, 20));
  expect(ui.batches.length).toBe(after);
  expect(client.writeErrors).toEqual([]);
});

test("a printed or history turn never carries a spinner", async () => {
  const { ui, client } = setup(reply("hello there"));
  await ui.fire("send", "hi");
  await client.idle();
  // The finished messages are plain views; the head is its own path, off.
  expect(JSON.stringify(store(ui).messages)).not.toContain("indeterminate");
  expect(store(ui).head.on).toBe(false);
  expect(store(ui).head.spin).toBe(false);
});

test("inline: the head is region state, never printed, and gone when the turn ends", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    async function* () {
      yield { type: "text", text: "First block.\n\nSecond " };
      await gate;
      yield {
        type: "message",
        message: { role: "assistant", content: "First block.\n\nSecond" },
      };
    },
    { presentation: "inline" },
  );
  await ui.fire("send", "go");
  expect(store(ui).head).toMatchObject({ on: true, spin: true });
  expect(store(ui).head.text).toBe("waiting for the model · 0s");
  await until(() => store(ui).head.text === "0s");
  release();
  await client.idle();
  expect(store(ui).head.on).toBe(false);
  // Only finished views and blocks reach the scrollback: no commit carries the head.
  for (const { commit, buf } of ui.commits) {
    expect(commit.template).not.toBe("head");
    expect(JSON.stringify(buf)).not.toContain("waiting for");
  }
  expect(printed(ui).join("\n")).toContain("First block.");
  expect(client.writeErrors).toEqual([]);
});

test("a running tool keeps its card; no second assistant spinner stands beside it", async () => {
  let nowMs = 1_000_000;
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let end: () => void = () => {};
  const last = new Promise<void>((r) => {
    end = r;
  });
  const { ui, client } = setup(
    async function* () {
      yield { type: "text", text: "Reading. " };
      yield {
        type: "message",
        message: {
          role: "assistant",
          content: "Reading.",
          tool_calls: [
            {
              id: "r1",
              function: { name: "read", arguments: { path: "a.txt" } },
            },
          ],
        },
      };
      yield { type: "tool_requested", id: "r1", name: "read" };
      yield { type: "tool_start", id: "r1", name: "read" };
      await gate;
      yield {
        type: "message",
        message: {
          role: "tool",
          tool_name: "read",
          tool_call_id: "r1",
          content: JSON.stringify({ status: "succeeded", output: "o" }),
        },
      };
      yield {
        type: "tool_result",
        id: "r1",
        name: "read",
        status: "succeeded",
        output: "o",
      };
      await last;
      yield { type: "message", message: { role: "assistant", content: "ok" } };
    },
    { now: () => nowMs },
  );
  await ui.fire("send", "go");
  await until(() => store(ui).live.is_tool && store(ui).live.card.running);
  await client.flush();
  // The card has its own spinner; the head steps aside.
  expect(store(ui).head.on).toBe(false);
  expect(store(ui).head.spin).toBe(false);
  nowMs += 3_000;
  release();
  // The tool is done: the model has the turn again.
  await until(() => store(ui).head.on);
  expect(store(ui).head.text).toBe("waiting for the model · 3s");
  expect(store(ui).live.is_tool).toBe(false);
  end();
  await client.idle();
  expect(store(ui).head.on).toBe(false);
});

test("an open approval freezes the head: a still glyph, the elapsed stops, and it resumes after", async () => {
  let nowMs = 1_000_000;
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    async function* (ctx) {
      yield* withApproval(ctx);
      await gate;
      yield { type: "message", message: { role: "assistant", content: "ok" } };
    },
    { now: () => nowMs },
  );
  await ui.fire("send", "edit");
  nowMs += 2_000;
  await client.flush();
  await untilApproval(ui);
  // A glyph where the spinner was; the card is the tool's, not a second spinner.
  expect(store(ui).head).toEqual({
    on: true,
    spin: false,
    still: true,
    glyph: "⠿",
    text: "waiting for your approval · 2s",
  });
  // A long wait does not run the clock.
  nowMs += 30_000;
  await client.flush();
  expect(store(ui).head.text).toBe("waiting for your approval · 2s");
  expect(store(ui).status).toBe(APPROVAL_STATUS);
  await ui.fire("dialog_confirm");
  // The wait is not counted: 2 s before it, 3 s after.
  nowMs += 3_000;
  await until(() => store(ui).head.on);
  await client.flush();
  expect(store(ui).head).toMatchObject({ spin: true, still: false });
  expect(store(ui).head.text).toBe("waiting for the model · 5s");
  release();
  await client.idle();
  expect(store(ui).status).toBe("build · last 5s");
});

test("an open question freezes the head the same way", async () => {
  let nowMs = 1_000_000;
  const answers: Answer[] = [];
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(asking(STORE_QUESTION, answers, gate), {
    now: () => nowMs,
  });
  await ui.fire("send", "go");
  release();
  await untilQuestion(ui);
  expect(store(ui).head).toMatchObject({
    on: true,
    spin: false,
    still: true,
    text: "waiting for your answer · 0s",
  });
  nowMs += 20_000;
  await client.flush();
  expect(store(ui).head.text).toBe("waiting for your answer · 0s");
  await ui.fire("question_skip");
  await client.idle();
  expect(store(ui).head.on).toBe(false);
});

test("servers still connecting show in the head, not in a busy status", async () => {
  let open: (failed: { name: string; error: string }[]) => void = () => {};
  const mcpReady = new Promise<{ name: string; error: string }[]>((r) => {
    open = r;
  });
  const { ui, client } = setup(reply("x"), { mcpReady });
  await ui.fire("send", "go");
  await until(() => store(ui).head.text.startsWith("connecting MCP"));
  expect(store(ui).head.text).toBe("connecting MCP servers · 0s");
  expect(store(ui).status).toBe("build · MCP connecting…");
  open([]);
  await client.idle();
  expect(store(ui).head.on).toBe(false);
});

test("an open approval owns the status line over the elapsed ticker", async () => {
  let nowMs = 1_000_000;
  const { ui, client } = setup(withApproval, { now: () => nowMs });
  await ui.fire("send", "edit");
  await untilApproval(ui);
  nowMs += 30_000;
  await client.flush();
  expect(store(ui).status).toBe(APPROVAL_STATUS);
  await ui.fire("dialog_cancel");
  await client.idle();
});

test("ctx moves on every done event: the turn's max shows, its last value rests", async () => {
  let toSecond: () => void = () => {};
  let toEnd: () => void = () => {};
  const gate1 = new Promise<void>((r) => {
    toSecond = r;
  });
  const gate2 = new Promise<void>((r) => {
    toEnd = r;
  });
  const { ui, client } = setup(async function* () {
    yield { type: "done", inputTokens: 7000, outputTokens: 800 };
    await gate1;
    yield { type: "done", inputTokens: 100, outputTokens: 0 };
    await gate2;
    yield { type: "message", message: { role: "assistant", content: "x" } };
  });
  await ui.fire("send", "go");
  await until(() => store(ui).header.ctx_pct === 95);
  // Mid-turn already: 7800 of 8192 is past the danger threshold.
  expect(store(ui).header.ctx_label).toBe("7.6k/8.0k");
  expect(store(ui).header.ctx_warn).toBe(true);
  expect(store(ui).header.ctx_danger).toBe(true);
  toSecond();
  await new Promise((r) => setTimeout(r, 15));
  // A smaller step mid-turn does not shrink the meter.
  expect(store(ui).header.ctx_pct).toBe(95);
  toEnd();
  await client.idle();
  // The turn's last observed value is the resting baseline.
  expect(store(ui).header.ctx_label).toBe("100/8.0k");
  expect(store(ui).header.ctx_pct).toBe(1);
  expect(store(ui).header.ctx_warn).toBe(false);
  expect(store(ui).header.ctx_danger).toBe(false);
});

test("ctrl+c clears a non-empty draft first, and only an empty composer quits", async () => {
  const { ui } = setup(reply("unused"));
  store(ui).draft = "half typed";
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(store(ui).draft).toBe("");
  expect(store(ui).status).toBe("Draft cleared · ctrl+c again to quit");
  expect(await ui.fire("quit_request")).toBe("quit");
});

test("quit aborts the running turn and answers quit", async () => {
  let aborted = false;
  const { ui, client } = setup(
    quiet(async ({ options }) => {
      // As runAgent does: a cancelled turn rejects.
      await new Promise((_, reject) =>
        options.signal.addEventListener("abort", () => {
          aborted = true;
          reject(new Error("aborted"));
        }),
      );
    }),
  );
  await ui.fire("send", "go");
  expect(await ui.fire("quit_request")).toBeUndefined();
  await client.idle();
  expect(aborted).toBe(true);
  expect(ui.statusLog()).toContain("Turn cancelled · ctrl+c again to quit");
  expect(isBusy(ui)).toBe(false);
  // The prompt came back to the composer (no session): it is cleared first.
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).draft).toBe("go");
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(store(ui).status).toBe("Draft cleared · ctrl+c again to quit");
  expect(await ui.fire("quit_request")).toBe("quit");
});

test("the process signal aborts the turn too", async () => {
  const { ui, client, aborts } = setup(
    quiet(async ({ options }) => {
      await new Promise((resolve) =>
        options.signal.addEventListener("abort", resolve),
      );
      options.signal.throwIfAborted();
    }),
  );
  await ui.fire("send", "go");
  aborts.abort();
  await client.idle();
  expect(isBusy(ui)).toBe(false);
});

test("a long history shows a window and 'load earlier' widens it by 100", async () => {
  const session = {
    store: {},
    state: {
      messages: Array.from({ length: 450 }, (_, i) => ({
        role: i % 2 ? "assistant" : "user",
        content: `m${i}`,
      })),
    },
  } as unknown as SessionContext;
  const { ui } = setup(reply("x"), { session });
  expect(store(ui).messages).toHaveLength(200);
  expect(store(ui).earlier.n).toBe(250);
  await ui.fire("load_earlier");
  expect(store(ui).messages).toHaveLength(300);
  expect(store(ui).earlier.n).toBe(150);
});

test("the palette filters, cancels and runs a command", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("palette_open");
  expect(store(ui).palette.open).toBe(true);
  expect(store(ui).found.items.length).toBeGreaterThan(2);
  await ui.fire("palette_query", "build");
  store(ui).palette.query = "build";
  expect(store(ui).found.items.map((i: PaletteItem) => i.id)).toEqual([
    "mode-build",
  ]);
  store(ui).palette.cur = "mode-build";
  await ui.fire("palette_run");
  expect(store(ui).palette.open).toBe(false);
  expect(store(ui).status).toBe("Mode changed to build");
  await ui.fire("palette_open");
  await ui.fire("palette_close");
  expect(store(ui).palette.open).toBe(false);
});

test("tool receipts and MCP servers feed /mcp", async () => {
  const mcp = [
    { tools: [{ name: "db__query" }, { name: "db__insert" }] },
    { tools: [{ name: "fs__read" }] },
  ] as unknown as McpConnection[];
  const { ui } = setup(reply("x"), { mcp, mcpNames: ["db", "fs"] });
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toBe("MCP servers: 2 · 3 tools");
  // The panel opens over the servers: count line on top, tool names only.
  expect(store(ui).panel.open).toBe(true);
  expect(store(ui).panel.title).toBe("MCP servers");
  expect(store(ui).panel.args).toBe("MCP servers: 2 · 3 tools");
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "db · 2 tools",
    "  query, insert",
    "fs · 1 tool",
    "  read",
  ]);
});

test("servers still connecting: the status says so, a turn waits, failures reach /mcp", async () => {
  const mcp: McpConnection[] = [];
  const mcpNames: string[] = [];
  let settle: (failed: { name: string; error: string }[]) => void = () => {};
  const mcpReady = new Promise<{ name: string; error: string }[]>((resolve) => {
    settle = resolve;
  });
  const offered: number[] = [];
  const { ui, client } = setup(
    async function* (input) {
      offered.push(input.options.mcp?.length ?? 0);
      yield* reply("done")(input);
    },
    { mcp, mcpNames, mcpReady },
  );
  expect(store(ui).status).toBe("build · MCP connecting…");
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toBe("MCP servers: connecting…");
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "Connecting to MCP servers…",
  ]);
  await ui.fire("panel_close");
  expect(store(ui).status).toBe("build · MCP connecting…");
  // A send waits for the servers: the model is offered their tools.
  await ui.fire("send", "hello");
  await Bun.sleep(20);
  expect(offered).toEqual([]);
  expect(store(ui).head.text).toBe("connecting MCP servers · 0s");
  expect(store(ui).status).toBe("build · MCP connecting…");
  mcp.push({ tools: [{ name: "db__query" }] } as unknown as McpConnection);
  mcpNames.push("db");
  settle([{ name: "hub", error: "Connection closed" }]);
  await client.idle();
  expect(offered).toEqual([1]);
  expect(store(ui).status).toMatch(/^build · MCP 1 · 1 failed \(\/mcp\)/);
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toBe("MCP servers: 1 · 1 tools · 1 failed");
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "hub · unavailable",
    "  Connection closed",
    "db · 1 tool",
    "  query",
  ]);
});

test("an open /mcp panel is drawn again when the servers settle", async () => {
  const mcp: McpConnection[] = [];
  let settle: (failed: { name: string; error: string }[]) => void = () => {};
  const mcpReady = new Promise<{ name: string; error: string }[]>((resolve) => {
    settle = resolve;
  });
  const { ui } = setup(reply("x"), { mcp, mcpNames: ["db"], mcpReady });
  await ui.fire("send", "/mcp");
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "Connecting to MCP servers…",
  ]);
  mcp.push({ tools: [{ name: "db__query" }] } as unknown as McpConnection);
  settle([]);
  await Bun.sleep(10);
  expect(store(ui).panel.open).toBe(true);
  expect(store(ui).status).toBe("MCP servers: 1 · 1 tools");
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "db · 1 tool",
    "  query",
  ]);
});

test("servers that settle while idle repaint the status, unless something newer is on it", async () => {
  let settle: (failed: { name: string; error: string }[]) => void = () => {};
  const mcpReady = new Promise<{ name: string; error: string }[]>((resolve) => {
    settle = resolve;
  });
  const { ui } = setup(reply("x"), { mcpReady });
  settle([]);
  await Bun.sleep(10);
  expect(store(ui).status).toBe("build");
  let late: (failed: { name: string; error: string }[]) => void = () => {};
  const second = setup(reply("x"), {
    mcpReady: new Promise((resolve) => {
      late = resolve;
    }),
  });
  await second.ui.fire("send", "/help");
  const help = store(second.ui).status;
  late([]);
  await Bun.sleep(10);
  expect(store(second.ui).status).toBe(help);
});

test("no write is ever refused by the host", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("dialog_confirm");
  await client.idle();
  expect(client.writeErrors).toEqual([]);
});

// ------------------------------------------------------- appended streaming

test("a reply streams as appends: the live message is written whole once, then only added to", async () => {
  const pieces = [
    "A first paragraph that goes on ",
    "for a while.\n\n",
    "A second ",
    "one with `code`.\n\n",
    "```ts\nconst x = 1;\n```\n\n",
    "The end.",
  ];
  // Taken inside the turn, asserted after it: an expect in the generator
  // would only turn into a failed turn the later checks still pass.
  let shown = "";
  const { ui, client } = setup(async function* () {
    for (const text of pieces) {
      yield { type: "text", text };
      await new Promise((r) => setTimeout(r, 12));
    }
    await until(() => liveText(store(ui)).includes("The end."));
    shown = liveText(store(ui));
    yield {
      type: "message",
      message: { role: "assistant", content: pieces.join("") },
    };
  });
  await ui.fire("send", "go");
  await client.idle();
  expect(shown).toContain("The end.");
  expect(store(ui).messages.some((m: MessageView) => m.is_error)).toBe(false);
  const writes = ui.batches.flat();
  // The live message is set whole once; the other sets of `live` are the idle value.
  expect(
    writes.filter(
      (w) =>
        w.path === "live" &&
        w.op !== "append" &&
        (w.value as { active: boolean }).active,
    ),
  ).toHaveLength(1);
  expect(
    writes.some((w) => w.path === "live.blocks" && w.op === "append"),
  ).toBe(true);
  expect(client.writeErrors).toEqual([]);
  expect(store(ui).live.active).toBe(false);
});

test("a refused append is made good: the next write sends the live message again", async () => {
  const text = "one two three four five six seven eight nine ten";
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* () {
    for (const word of text.split(" ")) {
      yield { type: "text", text: `${word} ` };
      await new Promise((r) => setTimeout(r, 8));
    }
    await gate;
    yield { type: "message", message: { role: "assistant", content: text } };
  });
  let refused = 0;
  ui.refuse = (writes) => {
    if (refused < 2 && writes.some((w) => w.op === "append")) {
      refused++;
      return "no room";
    }
    return undefined;
  };
  await ui.fire("send", "go");
  // The ten words are in, and both refusals have happened, before it is checked.
  await until(() => refused === 2 && liveText(store(ui)).trim() === text);
  expect(refused).toBe(2);
  // What the screen would show is the whole reply, not a hole where the refused text went.
  expect(liveText(store(ui)).trim()).toBe(text);
  expect(store(ui).status).toContain("UI update failed: no room");
  release();
  await client.idle();
});

test("a pasted block that starts with a slash is a message, one line of it is a command", async () => {
  const { ui, client, seen } = setup(reply("ok"));
  await ui.fire("send", "/usr/local/bin/tool\nfailed with 1");
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(seen[0]?.messages).toEqual([
    "user:/usr/local/bin/tool\nfailed with 1",
  ]);
  await ui.fire("send", "/mode plan");
  expect(seen).toHaveLength(1);
  expect(store(ui).header.mode).toBe("plan");
});

test("up and down recall what was sent, and down past the end brings the draft back", async () => {
  const { ui, client } = setup(reply("ok"));
  await ui.fire("send", "first");
  await client.idle();
  await ui.fire("send", "second\nline two");
  await client.idle();
  await ui.fire("send", "/mode plan");
  const draft = () => store(ui).draft;
  await ui.fire("history_next", "");
  expect(draft()).toBe("");
  await ui.fire("history_prev", "half-typed");
  expect(draft()).toBe("/mode plan");
  await ui.fire("history_prev", "/mode plan");
  expect(draft()).toBe("second\nline two");
  await ui.fire("history_prev", "second\nline two");
  expect(draft()).toBe("first");
  // The oldest stays put.
  await ui.fire("history_prev", "first");
  expect(draft()).toBe("first");
  await ui.fire("history_next", "first");
  expect(draft()).toBe("second\nline two");
  await ui.fire("history_next", "second\nline two");
  await ui.fire("history_next", "/mode plan");
  expect(draft()).toBe("half-typed");
  // Nothing to come back to once the draft is back.
  store(ui).draft = "other";
  await ui.fire("history_next", "other");
  expect(draft()).toBe("other");
});

test("the history of a resumed session is what its user messages said", async () => {
  const session = {
    state: {
      publicId: "abc1234",
      messages: [
        { role: "user", content: "earlier question" },
        { role: "assistant", content: "answer" },
        { role: "user", content: "later question" },
      ],
    },
  } as unknown as SessionContext;
  const { ui } = setup(reply("ok"), { session });
  await ui.fire("history_prev", "");
  expect(store(ui).draft).toBe("later question");
  await ui.fire("history_prev", "later question");
  expect(store(ui).draft).toBe("earlier question");
});

// ------------------------------------------------------------ the inline mode

const inline = { presentation: "inline" } as const;

/** What the scrollback received, one string per acked commit, in print order. */
function printed(ui: FakeHost): string[] {
  return ui.commits
    .filter((c) => c.ok)
    .map((c) => {
      if (c.commit.template === "la-media") {
        const view = c.buf as MessageView;
        return `sources: ${view.links.map((l) => `[${l.n}] ${l.text}`).join(" · ")}`;
      }
      if (c.commit.template === "la-turn") {
        const view = c.buf as MessageView;
        if (view.is_tool) return `tool ${view.card.name} ${view.card.status}`;
        const label = view.is_user
          ? "You"
          : view.is_error
            ? "Error"
            : view.is_notice
              ? "Notice"
              : "Assistant";
        return `${label}: ${view.blocks.map((b) => runsText(b.runs)).join("\n")}`;
      }
      return (c.buf as Block[]).map((b) => runsText(b.runs)).join("\n");
    });
}

test("fullscreen never commits", async () => {
  const { ui, client } = setup(reply("hello there"));
  await ui.fire("send", "hi");
  await client.idle();
  expect(ui.commits).toEqual([]);
});

test("inline: /details N opens the output a printed card names", async () => {
  let turn = 0;
  const { ui, client } = setup(
    (ctx) =>
      ++turn === 1
        ? toolScript(LONG_OUTPUT, "read")(ctx)
        : toolScript(LONG_OUTPUT.replace("line 15", "line XV"), "search")(ctx),
    inline,
  );
  await ui.fire("send", "one");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  // Both cards are in the scrollback for good, each naming its own output.
  const cards = ui.commits
    .filter((c) => c.ok && c.commit.template === "la-turn")
    .map((c) => c.buf as MessageView)
    .filter((view) => view.is_tool)
    .map((view) => runsText(view.card.out));
  expect(cards).toHaveLength(2);
  expect(cards[0]).toContain("/details 1 for full output");
  expect(cards[1]).toContain("/details 2 for full output");
  await ui.fire("send", "/details 1");
  expect(store(ui).details.title).toBe("read · succeeded");
  expect(detailsText(ui)).toContain("output line 15");
  expect(detailsText(ui)).not.toContain("output line XV");
});

test("inline: the prompt is committed on send and the reply as its blocks close, with no messages writes", async () => {
  const full = "para one\n\npara two\n\npara three starts\n\npara four";
  const { ui, client } = setup(async function* () {
    yield { type: "thinking", text: "secret reasoning" };
    yield { type: "text", text: "para one\n\npara two\n\npara three st" };
    await new Promise((r) => setTimeout(r, 25));
    yield { type: "text", text: "arts\n\npara four" };
    yield { type: "done", inputTokens: 10, outputTokens: 10 };
    yield { type: "message", message: { role: "assistant", content: full } };
  }, inline);
  await ui.fire("send", "write it");
  // The prompt is already in print before the reply starts.
  expect(printed(ui)).toEqual(["You: write it"]);
  await client.idle();
  const out = printed(ui);
  expect(out[0]).toBe("You: write it");
  // The first reply commit carries the label, later ones are plain blocks.
  expect(out[1]).toMatch(/^Assistant: para one/);
  if (out.length > 2) expect(out[2]).not.toMatch(/^Assistant:/);
  // Together the reply commits are the whole reply, once, in order.
  const replyText = out
    .slice(1)
    .map((s) => s.replace(/^Assistant: /, ""))
    .join("\n");
  expect(replyText).toBe(
    parseBlocks(full)
      .map((b) => runsText(b.runs))
      .join("\n"),
  );
  // The reasoning is never printed.
  expect(out.join("\n")).not.toContain("secret reasoning");
  // The region never held the history.
  expect(ui.batches.flat().some((w) => w.path === "messages")).toBe(false);
  expect(store(ui).messages).toEqual([]);
  expect(store(ui).live.active).toBe(false);
  expect(store(ui).commit_buf).toBeNull();
  expect(client.writeErrors).toEqual([]);
  // Writes and commits shared one chain: never two in flight.
  expect(ui.maxInflight).toBe(1);
});

test("inline: a streamed reply prints its sources row once, numbered like the body", async () => {
  const text =
    "See [the docs](https://x.dev/a) and [the API](https://x.dev/b), then the [docs](https://x.dev/a) again.";
  const { ui, client } = setup(async function* () {
    yield { type: "text", text: `${text}\n\n` };
    yield { type: "message", message: { role: "assistant", content: text } };
  }, inline);
  await ui.fire("send", "find the docs");
  await client.idle();
  const out = printed(ui);
  expect(out[0]).toBe("You: find the docs");
  // The body's markers and the sources row use the same numbers.
  expect(out.slice(1).join("\n")).toContain("the docs[1]");
  expect(out.slice(1).join("\n")).toContain("the API[2]");
  const sources = out.filter((row) => row.startsWith("sources: "));
  expect(sources).toEqual(["sources: [1] the docs · [2] the API"]);
  expect(client.writeErrors).toEqual([]);
});

test("inline: a tool receipt is committed in its final state, and the approval still decides", async () => {
  const { ui, client } = setup(withApproval, inline);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("dialog_confirm");
  await client.idle();
  expect(decisions).toEqual([true]);
  const out = printed(ui);
  expect(out).toContain("You: edit");
  expect(out).toContain("tool edit succeeded");
  // The live card states were never printed.
  expect(out.join("\n")).not.toContain("requested");
  expect(out.join("\n")).not.toContain("waiting for approval");
  expect(out.join("\n")).not.toContain("running");
});

test("inline: a tool result's image reaches the committed view", async () => {
  const tiny =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  const { ui, client } = setup(async function* () {
    yield {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            id: "cimg",
            function: {
              name: "read_image",
              arguments: { path: "shots/dot.png" },
            },
          },
        ],
      },
    };
    yield { type: "tool_requested", id: "img", name: "read_image" };
    yield { type: "tool_start", id: "img", name: "read_image" };
    yield {
      type: "message",
      message: {
        role: "tool",
        tool_name: "read_image",
        tool_call_id: "cimg",
        content: JSON.stringify({
          status: "succeeded",
          output: "image 1x1 png from shots/dot.png (attached)",
        }),
        images: [tiny],
      } satisfies Message,
    };
  }, inline);
  await ui.fire("send", "show it");
  await client.idle();
  const tool = ui.commits.find(
    (c) =>
      c.ok &&
      c.commit.template === "la-turn" &&
      (c.buf as { is_tool?: boolean } | undefined)?.is_tool,
  );
  expect(tool).toBeDefined();
  const view = tool?.buf as MessageView;
  expect(view.images).toEqual([
    {
      mime: "image/png",
      b64: tiny,
      src: `data:image/png;base64,${tiny}`,
      alt: "image 1 (png)",
    },
  ]);
  expect(client.writeErrors).toEqual([]);
});

test("inline: esc commits the partial reply that had arrived", async () => {
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "text", text: "half an answer" };
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  }, inline);
  await ui.fire("send", "slow one");
  await until(() => liveText(store(ui)).includes("half an answer"));
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled");
  expect(printed(ui)).toEqual(["You: slow one", "Assistant: half an answer"]);
});

test("inline: a failed turn commits its prompt and its error notice to the scrollback", async () => {
  const { ui, client } = setup(
    quiet(async () => {
      throw new Error("daemon is down\nsecond line");
    }),
    inline,
  );
  await ui.fire("send", "go now");
  await client.idle();
  const out = printed(ui);
  expect(out).toHaveLength(2);
  expect(out[0]).toBe("You: go now");
  expect(out[1]).toMatch(/^Error: /);
  expect(out[1]).toContain("daemon is down");
  expect(out[1]).toContain("second line");
  expect(store(ui).status).toBe("daemon is down second line");
  expect(client.writeErrors).toEqual([]);
});

test("inline: a queued prompt is committed only when its turn starts", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  }, inline);
  await ui.fire("send", "first one");
  await ui.fire("send", "second one");
  expect(store(ui).status).toBe("Queued: second one · ↑ edits it");
  // Only the running turn is in print; the queued prompt is not yet.
  expect(printed(ui)).toEqual(["You: first one"]);
  release();
  await client.idle();
  const out = printed(ui);
  expect(out.filter((s) => s === "You: second one")).toHaveLength(1);
  expect(out.indexOf("You: second one")).toBeGreaterThan(
    out.indexOf("You: first one"),
  );
});

test("inline: quit commits the partial reply before answering quit", async () => {
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "text", text: "still writing" };
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  }, inline);
  await ui.fire("send", "go");
  await until(() => liveText(store(ui)).includes("still writing"));
  // The first press cancels; the second, while the turn winds down, quits.
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(await ui.fire("quit_request")).toBe("quit");
  // Already printed at the moment "quit" was answered.
  expect(printed(ui)).toEqual(["You: go", "Assistant: still writing"]);
  await client.idle();
  expect(printed(ui)).toEqual(["You: go", "Assistant: still writing"]);
});

test("inline: a refused commit keeps its blocks and prints them exactly once later", async () => {
  const { ui, client } = setup(reply("alpha beta"), inline);
  // The user commit (0) lands; the reply's first commit attempt (1) is refused.
  ui.refuseCommit = (_commit, index) =>
    index === 1 ? "commit: not in inline mode" : undefined;
  await ui.fire("send", "hi");
  await client.idle();
  expect(client.writeErrors).toContain("commit: not in inline mode");
  expect(ui.commits.filter((c) => !c.ok)).toHaveLength(1);
  const out = printed(ui);
  expect(out.filter((s) => s.includes("alpha beta"))).toHaveLength(1);
});

test("inline: a resumed session's history is committed once at attach, in order", async () => {
  const session = {
    state: {
      publicId: "abc1234",
      messages: [
        { role: "user", content: "earlier question" },
        { role: "assistant", content: "the answer" },
      ],
    },
  } as unknown as SessionContext;
  const { ui, client } = setup(reply("ok"), { session, ...inline });
  expect(store(ui).messages).toEqual([]);
  await until(() => printed(ui).length === 2);
  expect(printed(ui)).toEqual([
    "You: earlier question",
    "Assistant: the answer",
  ]);
  await ui.fire("send", "next");
  await client.idle();
  // Once: a later turn does not reprint the resumed history.
  expect(
    printed(ui).filter((s) => s.includes("earlier question")),
  ).toHaveLength(1);
});

test("/theme writes the reserved @theme path in the command's batch", async () => {
  const { ui, client } = setup(reply("unused"));
  await ui.fire("send", "/theme light");
  await client.idle();
  expect(ui.store["@theme"]).toBe("light");
  expect(store(ui).status).toBe("Theme changed to light");
  // One batch carries the status and the @theme write together.
  const batch = ui.batches.find((b) => b.some((w) => w.path === "@theme"));
  expect(batch?.map((w) => w.path)).toEqual(["status", "@theme"]);
  await ui.fire("send", "/theme");
  expect(store(ui).status).toBe(
    "Current theme: light. Use /theme dark, /theme light or /theme auto",
  );
});

test("an unknown theme is refused and @theme is never written", async () => {
  const { ui } = setup(reply("unused"));
  await ui.fire("send", "/theme blue");
  expect(store(ui).status).toBe(
    "Unknown theme: blue. Use /theme dark, /theme light or /theme auto",
  );
  expect(ui.store["@theme"]).toBeUndefined();
  expect(ui.batches.flat().some((w) => w.path === "@theme")).toBe(false);
});

test("/mouse writes mouse_on; inline refuses without writing it", async () => {
  const { ui } = setup(reply("unused"));
  // Fullscreen starts with the mouse on: the wheel scrolls the transcript.
  expect(store(ui).mouse_on).toBe(true);
  await ui.fire("send", "/mouse off");
  expect(store(ui).mouse_on).toBe(false);
  expect(store(ui).status).toBe("Mouse changed to off");
  await ui.fire("send", "/mouse on");
  expect(store(ui).mouse_on).toBe(true);

  const inline = setup(reply("unused"), { presentation: "inline" });
  await inline.ui.fire("send", "/mouse on");
  expect(store(inline.ui).status).toBe("Mouse is not available in inline mode");
  expect(store(inline.ui).mouse_on).toBe(false);
  expect(
    inline.ui.batches.flat().some((w) => w.path === "mouse_on" && w.value),
  ).toBe(false);
});

test("the welcome (empty) goes with the first message and never returns", async () => {
  const ok = setup(reply("hello"));
  expect(store(ok.ui).empty).toBe(true);
  await ok.ui.fire("send", "hi");
  await ok.client.idle();
  expect(store(ok.ui).empty).toBe(false);

  // A failed first turn keeps its prompt and its error notice in view; the
  // welcome must not reappear over them.
  const failed = setup(
    quiet(async () => {
      throw new Error("boom");
    }),
  );
  expect(store(failed.ui).empty).toBe(true);
  await failed.ui.fire("send", "hi");
  await failed.client.idle();
  expect(
    store(failed.ui).messages.map((m: MessageView) => [m.is_user, m.is_error]),
  ).toEqual([
    [true, false],
    [false, true],
  ]);
  expect(store(failed.ui).empty).toBe(false);
  expect(
    failed.ui.batches.flat().filter((w) => w.path === "empty" && w.value),
  ).toEqual([]);
});

// ------------------------------------------------------------- the `/` popup

test("typing / opens the command popup; it filters, moves and completes", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("typed", "/");
  expect(store(ui).slash.open).toBe(true);
  expect(store(ui).slash.rows.map((r) => r.cmd)).toEqual([
    "/mode",
    "/model",
    "/theme",
    "/mouse",
    "/copy",
    "/permissions",
    "/sandbox",
    "/details",
    "/status",
    "/provider",
    "/sessions",
    "/recover",
    "/find",
    "/export",
    "/mcp",
    "/goal",
    "/help",
  ]);
  expect(store(ui).slash.rows[0]).toMatchObject({ sel: true, mark: "›" });
  await ui.fire("typed", "/m");
  expect(store(ui).slash.rows.map((r) => r.cmd)).toEqual([
    "/mode",
    "/model",
    "/mouse",
    "/mcp",
  ]);
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  expect(store(ui).slash.rows[3]).toMatchObject({ cmd: "/mcp", sel: true });
  // The choice clamps at the last row, like the palette's list.
  await ui.fire("slash_next", "/m");
  expect(store(ui).slash.rows[3]?.sel).toBe(true);
  await ui.fire("slash_prev", "/m");
  expect(store(ui).slash.rows[2]?.sel).toBe(true);
  // Typing again resets the choice to the first match.
  await ui.fire("typed", "/mo");
  expect(store(ui).slash.rows.map((r) => r.cmd)).toEqual([
    "/mode",
    "/model",
    "/mouse",
  ]);
  expect(store(ui).slash.rows[0]?.sel).toBe(true);
  // Tab completes the chosen row into the draft and closes the popup.
  await ui.fire("slash_next", "/mo");
  await ui.fire("slash_complete", "/mo");
  expect(store(ui).draft).toBe("/model ");
  expect(store(ui).slash).toEqual({ open: false, rows: [] });
});

test("the popup closes for arguments, unknown commands and plain text", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("typed", "/mode");
  expect(store(ui).slash.open).toBe(true);
  await ui.fire("typed", "/mode ");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("typed", "/z");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("typed", "/m\nmore");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("typed", "hello");
  expect(store(ui).slash.open).toBe(false);
});

test("esc closes the popup only: draft untouched, turn not cancelled", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    quiet(async () => {
      await gate;
    }),
  );
  await ui.fire("send", "go");
  expect(isBusy(ui)).toBe(true);
  await ui.fire("typed", "/m");
  expect(store(ui).slash.open).toBe(true);
  const batches = ui.batches.length;
  await ui.fire("slash_close");
  expect(store(ui).slash.open).toBe(false);
  // Nothing but the popup was written: the draft is untouched.
  expect(
    ui.batches
      .slice(batches)
      .flat()
      .filter((w) => w.path === "draft"),
  ).toEqual([]);
  expect(isBusy(ui)).toBe(true);
  // The dismissed popup stays closed for this draft, opens for the next.
  await ui.fire("typed", "/mo");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("typed", "");
  await ui.fire("typed", "/m");
  expect(store(ui).slash.open).toBe(true);
  release();
  await client.idle();
  expect(isBusy(ui)).toBe(false);
});

test("enter completes over the popup only when the choice differs", async () => {
  const { ui, seen } = setup(reply("x"));
  // "/mo" is not yet the chosen "/mode ": enter completes, nothing runs.
  await ui.fire("typed", "/mo");
  await ui.fire("send", "/mo");
  expect(store(ui).draft).toBe("/mode ");
  expect(store(ui).slash.open).toBe(false);
  expect(isBusy(ui)).toBe(false);
  expect(seen).toEqual([]);
  // A moved choice completes to that row, still without running it.
  await ui.fire("typed", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("send", "/m");
  expect(store(ui).draft).toBe("/mcp");
  expect(store(ui).status).toBe("build");
  // Fully typed, the same enter runs the command.
  await ui.fire("typed", "/mcp");
  expect(store(ui).slash.open).toBe(true);
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toContain("MCP servers: 0");
  expect(store(ui).draft).toBe("");
  expect(store(ui).slash.open).toBe(false);
});

test("the popup never opens while an approval is open", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("typed", "/");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("dialog_cancel");
  await client.idle();
  await ui.fire("typed", "/");
  expect(store(ui).slash.open).toBe(true);
});

test("inline: the popup is region state and is never committed", async () => {
  const { ui, client } = setup(reply("hello"), { presentation: "inline" });
  await ui.fire("typed", "/m");
  expect(store(ui).slash.open).toBe(true);
  const rows = store(ui).slash.rows.map((row) => row.cmd);
  expect(rows).toEqual(expect.arrayContaining(["/mode", "/model", "/mouse"]));
  expect(ui.commits).toEqual([]);
  await ui.fire("slash_complete", "/m");
  expect(store(ui).draft).toBe("/mode ");
  expect(ui.commits).toEqual([]);
  await ui.fire("send", "hi");
  await client.idle();
  // The turn committed its views; none of them is popup state: no row of it
  // was printed, and the popup was written closed, not committed.
  expect(ui.commits.length).toBeGreaterThan(0);
  const committed = JSON.stringify(ui.commits);
  for (const cmd of rows) expect(committed).not.toContain(cmd);
  const slashWrites = ui.batches.flat().filter((w) => w.path === "slash");
  expect(slashWrites.at(0)?.value).toMatchObject({ open: true });
  expect(slashWrites.at(-1)?.value).toEqual({ open: false, rows: [] });
  expect(store(ui).slash.open).toBe(false);
});

// ------------------------------------------------------------ palette & keys

test("a new palette query resets the cursor to the first match", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("palette_open");
  // The list cursor sits somewhere down the full list.
  store(ui).palette.cur = "quit";
  await ui.fire("palette_query", "theme");
  expect(store(ui).found.items.map((i: PaletteItem) => i.id)).toEqual([
    "theme-dark",
    "theme-light",
    "theme-auto",
  ]);
  expect(store(ui).palette.cur).toBe("theme-dark");
  await ui.fire("palette_query", "zzz");
  expect(store(ui).found.any).toBe(false);
  expect(store(ui).palette.cur).toBe("");
});

test("shift+tab cycles build→plan→chat→build like /mode, and is refused while busy", async () => {
  const { ui } = setup(reply("x"));
  expect(store(ui).header.mode).toBe("build");
  await ui.fire("mode_toggle");
  expect(store(ui).header.mode).toBe("plan");
  expect(store(ui).status).toBe("Mode changed to plan");
  await ui.fire("mode_toggle");
  expect(store(ui).header.mode).toBe("chat");
  expect(store(ui).status).toBe("Mode changed to chat");
  await ui.fire("mode_toggle");
  expect(store(ui).header.mode).toBe("build");

  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const busy = setup(
    quiet(async () => {
      await gate;
    }),
  );
  await busy.ui.fire("send", "go");
  await busy.ui.fire("mode_toggle");
  expect(store(busy.ui).status).toBe(
    "Busy · wait for the current turn to finish",
  );
  expect(store(busy.ui).header.mode).toBe("build");
  release();
  await busy.client.idle();
});

test("the help overlay's store carries the slash commands", () => {
  const { ui } = setup(reply("x"));
  expect(store(ui).help.cmds.map((c) => c.cmd)).toEqual([
    "/mode plan|build|chat",
    "/model [NAME]",
    "/theme dark|light|auto",
    "/mouse on|off",
    "/copy [code]",
    "/permissions [clear|trust]",
    "/sandbox on|off",
    "/details [N]",
    "/status",
    "/provider",
    "/sessions [new]",
    "/recover [N outcome]",
    "/find TEXT",
    "/export [json]",
    "/mcp",
    "/goal [OBJECTIVE|stop]",
  ]);
  // Not /help: the overlay is what it opens, and "? close help" heads it.
  expect(store(ui).help.cmds[0]?.desc).toContain("plan reads only");
});

test("the palette's Quit row is the two-stage ctrl+c", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("palette_open");
  store(ui).draft = "half written";
  store(ui).palette.query = "quit";
  store(ui).palette.cur = "quit";
  // A draft exists: the first stage clears it instead of quitting.
  expect(await ui.fire("palette_run")).toBeUndefined();
  expect(store(ui).palette.open).toBe(false);
  expect(store(ui).draft).toBe("");
  expect(store(ui).status).toBe("Draft cleared · ctrl+c again to quit");
  // An empty composer quits.
  await ui.fire("palette_open");
  store(ui).palette.query = "quit";
  store(ui).palette.cur = "quit";
  expect(await ui.fire("palette_run")).toBe("quit");
});

test("the palette loads earlier messages fullscreen and never lists it inline", async () => {
  const session = {
    store: {},
    state: {
      messages: Array.from({ length: 450 }, (_, i) => ({
        role: i % 2 ? "assistant" : "user",
        content: `m${i}`,
      })),
    },
  } as unknown as SessionContext;
  const { ui } = setup(reply("x"), { session });
  await ui.fire("palette_open");
  expect(store(ui).found.items.map((i: PaletteItem) => i.id)).toContain(
    "load-earlier",
  );
  store(ui).palette.query = "earlier";
  store(ui).palette.cur = "load-earlier";
  await ui.fire("palette_run");
  expect(store(ui).messages).toHaveLength(300);
  expect(store(ui).earlier.n).toBe(150);

  const inline = setup(reply("x"), { presentation: "inline" });
  expect(
    store(inline.ui).found.items.map((i: PaletteItem) => i.id),
  ).not.toContain("load-earlier");
  await inline.ui.fire("palette_open");
  expect(
    store(inline.ui).found.items.map((i: PaletteItem) => i.id),
  ).not.toContain("load-earlier");
  expect(store(inline.ui).found.items.map((i: PaletteItem) => i.id)).toContain(
    "quit",
  );
});

// ------------------------------------------------------------ session grants

function writeProposal(): ApprovalRequest {
  return {
    kind: "write",
    path: "b.txt",
    created: true,
    before: "",
    after: "x",
    diff: "+x",
    identity: "i",
  };
}

function commandProposal(argv: [string, ...string[]]): ApprovalRequest {
  return { kind: "command", argv, cwd: "/w", timeoutMs: 1000 };
}

function mcpProposal(name: string): ApprovalRequest {
  return { kind: "mcp", name, argumentsJson: "{}" };
}

/** Turns that ask approval for each turn's proposals in order. */
function turnsAsking(lists: ApprovalRequest[][]): Script {
  let turn = 0;
  return async function* ({ options }) {
    for (const proposal of lists[turn++] ?? [])
      decisions.push(await options.approve(proposal, options.signal));
    yield {
      type: "message",
      message: { role: "assistant", content: "done" },
    };
  };
}

test("grants: `a` on an edit lets a later write through with a status flash and a header label", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let turn = 0;
  const { ui, client } = setup(async function* ({ options }) {
    turn++;
    decisions.push(
      await options.approve(
        turn === 1 ? editProposal() : writeProposal(),
        options.signal,
      ),
    );
    if (turn === 2) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: "done" },
    };
  });
  await ui.fire("send", "one");
  await untilApproval(ui);
  expect(store(ui).approval.grant_hint).toBe(
    "a allow file changes this session",
  );
  expect("grant_ok" in store(ui).approval).toBe(false);
  await ui.fire("dialog_session");
  await client.idle();
  expect(decisions).toEqual([true]);
  expect(store(ui).header.grants).toBe("auto: files");
  expect(store(ui).header.grants_on).toBe(true);

  // The write is the same "file changes" grant: no dialog, and the busy
  // status names the grant that let it through.
  await ui.fire("send", "two");
  await until(() => decisions.length === 2);
  expect(store(ui).approval.open).toBe(false);
  await until(() => store(ui).status.includes("files: allowed for session"));
  expect(store(ui).status).toContain("files: allowed for session");
  release();
  await client.idle();
  expect(decisions).toEqual([true, true]);
});

test("grants: a command grant matches program + first argument as a prefix, nothing wider", async () => {
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test", "a.ts"])],
      [
        commandProposal(["bun", "test", "b.ts"]),
        commandProposal(["bun", "run", "x"]),
      ],
      [commandProposal(["ls"])],
      [commandProposal(["ls"]), commandProposal(["ls", "-la"])],
    ]),
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  expect(store(ui).approval.grant_hint).toBe(
    "a allow `bun test …` this session",
  );
  await ui.fire("dialog_session");
  await client.idle();
  expect(store(ui).header.grants).toBe("auto: bun test");

  // `bun test …` runs; `bun run` still asks, and its grant keeps the script.
  await ui.fire("send", "two");
  await untilApproval(ui);
  expect(decisions).toEqual([true, true]);
  expect(store(ui).approval.grant_hint).toBe(
    "a allow `bun run x …` this session",
  );
  await ui.fire("dialog_cancel");
  await client.idle();

  // A bare program grants exactly itself: `ls` again is covered, `ls -la`
  // asks (a bare `bash` grant must not cover `bash -c …`).
  await ui.fire("send", "three");
  await untilApproval(ui);
  expect(store(ui).approval.grant_hint).toBe("a allow `ls` this session");
  await ui.fire("dialog_session");
  await client.idle();
  expect(store(ui).header.grants).toBe("auto: bun test · ls");
  await ui.fire("send", "four");
  await untilApproval(ui);
  expect(decisions).toEqual([true, true, false, true, true]);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, true, false, true, true, false]);
});

test("grants: files do not cover commands; MCP grants are the exact SERVER__TOOL name", async () => {
  const { ui, client } = setup(
    turnsAsking([
      [editProposal()],
      [commandProposal(["bun", "test"])],
      [mcpProposal("db__query")],
      [mcpProposal("db__query"), mcpProposal("db__other")],
    ]),
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();

  await ui.fire("send", "two");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();

  await ui.fire("send", "three");
  await untilApproval(ui);
  // The MCP dialog names the server in its title and body.
  expect(store(ui).approval.title).toBe("Call query from server db?");
  expect(store(ui).approval.body).toContain("MCP tool query from server db");
  expect(store(ui).approval.grant_hint).toBe(
    "a allow `db__query` this session",
  );
  await ui.fire("dialog_session");
  await client.idle();
  expect(store(ui).header.grants).toBe("auto: files · db__query");

  // Same tool runs; a sibling tool on the same server still asks.
  await ui.fire("send", "four");
  await untilApproval(ui);
  expect(decisions).toEqual([true, false, true, true]);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false, true, true, false]);
});

test("grants: plan mode is unaffected (a grant never auto-allows under plan)", async () => {
  // The runtime refuses plan mutations before `approve` runs at all; this
  // script calls it directly, so the controller's own guard is what keeps
  // the dialog in place.
  const { ui, client } = setup(
    turnsAsking([[editProposal()], [editProposal()]]),
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "/mode plan");
  expect(store(ui).header.mode).toBe("plan");
  await ui.fire("send", "two");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false]);
});

test("grants: /permissions lists them and /permissions clear revokes them all", async () => {
  const { ui, client } = setup(
    turnsAsking([[editProposal()], [editProposal()]]),
  );
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe(
    "No permissions: no session grants, no file rules",
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe("Permissions: session: files");
  await ui.fire("send", "/permissions clear");
  expect(store(ui).status).toBe("Session grants cleared");
  expect(store(ui).header.grants).toBe("");
  expect(store(ui).header.grants_on).toBe(false);
  // Cleared means asked again.
  await ui.fire("send", "two");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false]);
});

test("grants: a truncated preview refuses `a` (dialog stays, `y` still works, nothing remembered)", async () => {
  const huge = editProposal(
    Array.from({ length: 2100 }, (_, i) => `+line ${i}`).join("\n"),
  );
  const { ui, client } = setup(turnsAsking([[huge], [editProposal()]]));
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_TRUNCATED_STATUS);
  expect(store(ui).approval.truncated).toBe(true);
  expect("grant_ok" in store(ui).approval).toBe(false);
  expect(store(ui).approval.grant_hint).toBe(
    "preview truncated: session grant unavailable",
  );
  await ui.fire("dialog_session");
  expect(store(ui).approval.open).toBe(true);
  expect(store(ui).status).toBe(APPROVAL_TRUNCATED_SESSION_STATUS);
  expect(decisions).toEqual([]);
  await ui.fire("dialog_confirm");
  await client.idle();
  expect(decisions).toEqual([true]);
  expect(store(ui).header.grants_on).toBe(false);
  // Nothing was granted: the next edit asks.
  await ui.fire("send", "two");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false]);
});

test("grants: a resize that truncates the preview takes `a` and `!` off the armed status, and puts them back", async () => {
  const timer = holdArmTimer();
  try {
    let cols = 100;
    // 1100 lines of 50 columns: one row each at 100 columns, two at 40,
    // which is past the row cap.
    const diff = Array.from(
      { length: 1100 },
      (_, i) => `+${String(i).padStart(4, "0")}${"x".repeat(45)}`,
    ).join("\n");
    const { ui, client } = setup(turnsAsking([[editProposal(diff)]]), {
      armMs: timer.armMs,
      columns: () => cols,
    });
    await ui.fire("send", "one");
    await until(() => store(ui).approval.open);
    // Not armed yet: a resize leaves the waiting status alone.
    cols = 40;
    await client.resize();
    expect(store(ui).approval).toMatchObject({
      truncated: true,
      session_ok: false,
      always_ok: false,
    });
    expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
    cols = 100;
    await client.resize();
    await timer.arm();
    await until(() => store(ui).status === APPROVAL_STATUS);
    cols = 40;
    await client.resize();
    expect(store(ui).status).toBe(APPROVAL_TRUNCATED_STATUS);
    await ui.fire("dialog_session");
    expect(store(ui).status).toBe(APPROVAL_TRUNCATED_SESSION_STATUS);
    // A resize that changes no grant keeps the refusal on the status line.
    cols = 39;
    await client.resize();
    expect(store(ui).status).toBe(APPROVAL_TRUNCATED_SESSION_STATUS);
    cols = 100;
    await client.resize();
    expect(store(ui).approval).toMatchObject({
      truncated: false,
      session_ok: true,
      always_ok: true,
    });
    expect(store(ui).status).toBe(APPROVAL_STATUS);
    await ui.fire("dialog_cancel");
    await client.idle();
    expect(decisions).toEqual([false]);
  } finally {
    timer.release();
  }
});

test("grants: an unarmed `a` is swallowed with feedback, like `y`", async () => {
  const timer = holdArmTimer();
  try {
    const { ui, client } = setup(turnsAsking([[editProposal()]]), {
      armMs: timer.armMs,
    });
    await ui.fire("send", "one");
    await until(() => store(ui).approval.open);
    expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
    await ui.fire("dialog_session");
    expect(store(ui).approval.open).toBe(true);
    expect(store(ui).status).toBe(APPROVAL_UNARMED_SESSION_STATUS);
    expect(decisions).toEqual([]);
    await timer.arm();
    await until(() => store(ui).status === APPROVAL_STATUS);
    await ui.fire("dialog_session");
    await client.idle();
    expect(decisions).toEqual([true]);
    expect(store(ui).header.grants).toBe("auto: files");
  } finally {
    timer.release();
  }
});

test("inline grants: a command grant skips the second dialog and commits are unchanged", async () => {
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test", "a.ts"])],
      [commandProposal(["bun", "test", "b.ts"])],
    ]),
    inline,
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  expect(store(ui).approval.grant_hint).toBe(
    "a allow `bun test …` this session",
  );
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  // No second dialog ever opened, and both turns committed as before.
  expect(decisions).toEqual([true, true]);
  expect(store(ui).approval.open).toBe(false);
  expect(store(ui).header.grants).toBe("auto: bun test");
  expect(printed(ui)).toEqual([
    "You: one",
    "Assistant: done",
    "You: two",
    "Assistant: done",
  ]);
  expect(client.writeErrors).toEqual([]);
});

// --------------------------------------------------------------------- details

/** Twenty lines of tool output: the card keeps eight, `/details` the rest. */
const LONG_OUTPUT = Array.from(
  { length: 20 },
  (_, i) => `output line ${i + 1}`,
).join("\n");

/** One whole tool turn: call, receipt, and a short reply after it. */
function toolScript(
  output: string,
  name = "read",
  status: "succeeded" | "failed" | "denied" = "succeeded",
): Script {
  return async function* () {
    yield {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          { id: "c1", function: { name, arguments: { path: "src/big.txt" } } },
        ],
      },
    };
    yield { type: "tool_requested", id: "c1", name };
    yield { type: "tool_start", id: "c1", name };
    const message: Message = {
      role: "tool",
      tool_name: name,
      tool_call_id: "c1",
      content: JSON.stringify({ status, output }),
    };
    yield { type: "message", message };
    yield { type: "tool_result", id: "c1", name, status, output };
    yield { type: "message", message: { role: "assistant", content: "done" } };
    yield { type: "turn_complete", messages: [] };
  };
}

/** The viewer rows as one text. */
const detailsText = (ui: FakeHost) =>
  store(ui)
    .details.lines.map((line) => line.t)
    .join("\n");

test("/details before any tool ran says so and opens nothing", async () => {
  const { ui } = setup(reply("x"));
  store(ui).draft = "/details";
  await ui.fire("send", "/details");
  expect(store(ui).status).toBe(NO_DETAILS_STATUS);
  expect(store(ui).details.open).toBe(false);
  // Nothing to fix or send again: the line goes, so the next message does
  // not land after it.
  expect(store(ui).draft).toBe("");
});

test("/details shows the lines the card truncated, and closing rests at idle", async () => {
  const { ui, client } = setup(toolScript(LONG_OUTPUT));
  await ui.fire("send", "read it");
  await client.idle();
  // The committed card never held line 15.
  const card = store(ui).messages.find((m) => m.is_tool)?.card;
  expect(runsText(card?.out ?? [])).toContain(
    "… 12 more lines · /details 1 for full output",
  );
  expect(runsText(card?.out ?? [])).not.toContain("output line 15");
  await ui.fire("send", "/details");
  expect(store(ui).details.open).toBe(true);
  expect(store(ui).details.title).toBe("read · succeeded");
  expect(store(ui).details.args).toBe("src/big.txt");
  expect(store(ui).status).toBe("Latest tool output: read");
  expect(detailsText(ui)).toContain("output line 15");
  expect(detailsText(ui)).toContain("output line 20");
  await ui.fire("details_close");
  expect(store(ui).details.open).toBe(false);
  expect(store(ui).status).toBe("build · last <1s");
  expect(client.writeErrors).toEqual([]);
});

test("a newer receipt replaces the one /details shows", async () => {
  let turn = 0;
  const { ui, client } = setup((ctx) =>
    ++turn === 1
      ? toolScript("first output", "read")(ctx)
      : toolScript("second output", "search")(ctx),
  );
  await ui.fire("send", "one");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  await ui.fire("send", "/details");
  expect(store(ui).details.title).toBe("search · succeeded");
  expect(detailsText(ui)).toContain("second output");
  expect(detailsText(ui)).not.toContain("first output");
});

test("/details N opens the output its card names, and a resize keeps it", async () => {
  let turn = 0;
  let cols = 100;
  const { ui, client } = setup(
    (ctx) =>
      ++turn === 1
        ? toolScript(LONG_OUTPUT, "read")(ctx)
        : toolScript("second output", "search")(ctx),
    { columns: () => cols },
  );
  await ui.fire("send", "one");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  // Both turns call it c1, as a provider that numbers calls per reply
  // does: the older card still names its own output.
  const card = store(ui).messages.find((m) => m.is_tool)?.card;
  expect(runsText(card?.out ?? [])).toContain(
    "… 12 more lines · /details 1 for full output",
  );
  store(ui).draft = "/details 1";
  await ui.fire("send", "/details 1");
  expect(store(ui).details.title).toBe("read · succeeded");
  expect(store(ui).status).toBe("Tool output 1: read");
  expect(detailsText(ui)).toContain("output line 15");
  expect(store(ui).draft).toBe("");
  // A re-render wraps the receipt on screen again, not the newest one.
  cols = 40;
  await client.resize();
  expect(store(ui).details.title).toBe("read · succeeded");
  await ui.fire("details_close");
  await ui.fire("send", "/details 2");
  expect(store(ui).details.title).toBe("search · succeeded");
  expect(detailsText(ui)).toContain("second output");
});

test("/details N that names no kept output says why and keeps the line", async () => {
  const { ui, client } = setup(toolScript(LONG_OUTPUT));
  await ui.fire("send", "read it");
  await client.idle();
  for (const [line, status] of [
    // One output so far: 2 is the next number, not yet given.
    ["/details 2", "No tool output 2 yet. /details opens the latest"],
    ["/details 9", "No tool output 9 yet. /details opens the latest"],
    ["/details x", "/details N takes a number from 1: x is not one"],
  ] as const) {
    store(ui).draft = line;
    await ui.fire("send", line);
    expect(store(ui).status).toBe(status);
    expect(store(ui).details.open).toBe(false);
    // As with a refused /model, the number can be fixed in place.
    expect(store(ui).draft).toBe(line);
  }
});

/** A resumed session of `count` read outputs, each `output N` padded to `size`. */
function readSession(count: number, size = 0): SessionContext {
  const messages = Array.from({ length: count }, (_, i) => ({
    role: "tool",
    tool_name: "read",
    tool_call_id: "call_0",
    content: JSON.stringify({
      status: "succeeded",
      output: `output ${i + 1}`.padEnd(size, "."),
    }),
  }));
  return { store: {}, state: { messages } } as unknown as SessionContext;
}

const GONE = (n: number) =>
  `Tool output ${n} is no longer kept (the oldest go first). /details opens the latest`;

test("/details N keeps far more than a screenful of outputs, within a budget", async () => {
  // Small outputs: well past 32 of them, the first still opens.
  const small = setup(reply("x"), { session: readSession(40) });
  await small.ui.fire("send", "/details 1");
  expect(detailsText(small.ui)).toBe("output 1");
  // As many as the count allows, and one more: the first goes.
  const many = setup(reply("x"), {
    session: readSession(MAX_RECEIPTS + 1),
  });
  await many.ui.fire("send", "/details 1");
  expect(store(many.ui).status).toBe(GONE(1));
  await many.ui.fire("send", "/details 2");
  expect(detailsText(many.ui)).toBe("output 2");
  // Outputs as large as the viewer keeps, one past the character budget.
  const fits = Math.floor(RECEIPTS_MAX_CHARS / DETAILS_MAX_CHARS);
  const large = setup(reply("x"), {
    session: readSession(fits + 1, DETAILS_MAX_CHARS),
  });
  await large.ui.fire("send", "/details 1");
  expect(store(large.ui).status).toBe(GONE(1));
  await large.ui.fire("send", "/details 2");
  expect(store(large.ui).status).toBe("Tool output 2: read");
});

test("a new conversation goes on counting, and an older number says whose it was", async () => {
  const { ui, client } = setup(toolScript(LONG_OUTPUT), {
    session: readSession(3),
  });
  await ui.fire("send", "/sessions new");
  await client.idle();
  await ui.fire("send", "read it");
  await client.idle();
  // A card of the conversation left behind (inline, it stays in the
  // scrollback) never names this one's output.
  const card = store(ui).messages.find((m) => m.is_tool)?.card;
  expect(runsText(card?.out ?? [])).toContain("/details 4 for full output");
  await ui.fire("send", "/details 3");
  expect(store(ui).status).toBe(
    "Tool output 3 belongs to an earlier conversation. /details opens the latest",
  );
  await ui.fire("send", "/details 4");
  expect(store(ui).status).toBe("Tool output 4: read");
});

test("a new conversation starts the budget afresh", async () => {
  // The conversation left behind filled nearly all of it.
  const fits = Math.floor(RECEIPTS_MAX_CHARS / DETAILS_MAX_CHARS);
  const { ui, client } = setup(toolScript("y".repeat(20_000)), {
    session: readSession(fits, DETAILS_MAX_CHARS),
  });
  await ui.fire("send", "/sessions new");
  await client.idle();
  for (const prompt of ["one", "two"]) {
    await ui.fire("send", prompt);
    await client.idle();
  }
  await ui.fire("send", `/details ${fits + 1}`);
  expect(store(ui).status).toBe(`Tool output ${fits + 1}: read`);
});

test("failed and denied receipts are kept for /details too", async () => {
  const failed = setup(toolScript("boom: no such file", "read", "failed"));
  await failed.ui.fire("send", "read it");
  await failed.client.idle();
  await failed.ui.fire("send", "/details");
  expect(store(failed.ui).details.title).toBe("read · failed");
  expect(detailsText(failed.ui)).toContain("boom: no such file");

  const denied = setup(withApproval);
  await denied.ui.fire("send", "edit");
  await untilApproval(denied.ui);
  await denied.ui.fire("dialog_cancel");
  await denied.client.idle();
  await denied.ui.fire("send", "/details");
  expect(store(denied.ui).details.title).toBe("edit · denied");
  expect(detailsText(denied.ui)).toContain("Edit denied by host");
});

test("the retained output is capped for the viewer, and the cut is spelled out", async () => {
  const { ui, client } = setup(toolScript(`${"x".repeat(70_000)}TAIL-MARKER`));
  await ui.fire("send", "read it");
  await client.idle();
  await ui.fire("send", "/details");
  expect(detailsText(ui)).toContain(
    "… output truncated for the viewer (first 64000 characters)",
  );
  expect(detailsText(ui)).not.toContain("TAIL-MARKER");
});

test("/details is refused while a turn runs and never covers an approval", async () => {
  // Busy: a receipt exists, but a second turn is still running.
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let turn = 0;
  const busy = setup((ctx) =>
    ++turn === 1
      ? toolScript(LONG_OUTPUT)(ctx)
      : quiet(async () => {
          await gate;
        })(ctx),
  );
  await busy.ui.fire("send", "read it");
  await busy.client.idle();
  await busy.ui.fire("send", "wait");
  await until(() => isBusy(busy.ui));
  await busy.ui.fire("send", "/details");
  expect(store(busy.ui).status).toBe(
    "Busy · wait for the current turn to finish",
  );
  expect(store(busy.ui).details.open).toBe(false);
  release();
  await busy.client.idle();

  // Approval open: the dialog stays, the viewer stays shut.
  const approval = setup(withApproval);
  await approval.ui.fire("send", "edit");
  await untilApproval(approval.ui);
  await approval.ui.fire("send", "/details");
  expect(store(approval.ui).approval.open).toBe(true);
  expect(store(approval.ui).details.open).toBe(false);
  await approval.ui.fire("dialog_cancel");
  await approval.client.idle();
});

test("the palette's Details row opens the viewer", async () => {
  const { ui, client } = setup(toolScript(LONG_OUTPUT));
  await ui.fire("send", "read it");
  await client.idle();
  await ui.fire("palette_open");
  await ui.fire("palette_query", "details");
  store(ui).palette.query = "details";
  expect(store(ui).found.items.map((i: PaletteItem) => i.id)).toEqual([
    "details",
  ]);
  store(ui).palette.cur = "details";
  await ui.fire("palette_run");
  expect(store(ui).palette.open).toBe(false);
  expect(store(ui).details.open).toBe(true);
  expect(detailsText(ui)).toContain("output line 15");
});

test("resizing wraps an open viewer again", async () => {
  let cols = 120;
  const { ui, client } = setup(toolScript("y".repeat(100)), {
    columns: () => cols,
  });
  await ui.fire("send", "read it");
  await client.idle();
  await ui.fire("send", "/details");
  const wide = store(ui).details.lines.length;
  cols = 50;
  await client.resize();
  expect(store(ui).details.lines.length).toBeGreaterThan(wide);
  expect(detailsText(ui)).toContain("↪");
});

test("a finished reply's table is fitted to the terminal width", async () => {
  const table = [
    "| id | name | description |",
    "|---|---|---|",
    "| 1 | alpha | A rather long description that has to wrap onto more lines |",
  ].join("\n");
  // 40 columns: the log and the code block leave 34 for the table's text.
  const { ui, client } = setup(reply(`${table}\n\ndone`), {
    columns: () => 40,
  });
  await ui.fire("send", "table");
  await client.idle();
  const shown = store(ui).messages.at(-1)?.blocks[0];
  expect(shown?.lang).toBe("");
  const lines = runsText(shown?.runs ?? []).split("\n");
  expect(lines.length).toBeGreaterThan(3);
  for (const line of lines)
    expect(Bun.stringWidth(line)).toBeLessThanOrEqual(34);
  expect(client.writeErrors).toEqual([]);
});

test("a table still streaming is fitted to the terminal width too", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    async function* () {
      yield {
        type: "text",
        text: "| id | description |\n|---|---|\n| 1 | A rather long description that has to wrap |\n\nafter\n",
      };
      await gate;
      yield { type: "message", message: { role: "assistant", content: "x" } };
    },
    { columns: () => 40 },
  );
  await ui.fire("send", "table");
  await until(() => store(ui).live.blocks.length > 0);
  const lines = runsText(store(ui).live.blocks[0]?.runs ?? []).split("\n");
  expect(lines.length).toBeGreaterThan(3);
  for (const line of lines)
    expect(Bun.stringWidth(line)).toBeLessThanOrEqual(34);
  release();
  await client.idle();
});

test("an MCP tool's card and /details show the server's text, not the envelope", async () => {
  const envelope = JSON.stringify({
    transport: "success",
    domain: "unknown",
    evidence: "unverified",
    isError: false,
    text: JSON.stringify({ alternatives: ["a"] }),
  });
  const { ui, client } = setup(toolScript(envelope, "mcphub__resolve_tool"));
  await ui.fire("send", "route");
  await client.idle();
  const card = store(ui).messages.find((m: MessageView) => m.is_tool)?.card;
  expect(runsText(card?.out ?? [])).toMatch(
    /^unverified · domain unknown\n\{\n {2}"alternatives"/,
  );
  await ui.fire("send", "/details");
  // This script reports the call succeeded, so nothing else says the
  // evidence is unverified: the head keeps it (the card's own state drops
  // it only when it already says so).
  expect(detailsText(ui)).toContain("unverified · domain unknown");
  expect(detailsText(ui)).not.toContain("transport");
});

test("a new turn closes an open viewer", async () => {
  let turn = 0;
  const { ui, client } = setup((ctx) =>
    ++turn === 1 ? toolScript(LONG_OUTPUT)(ctx) : reply("again")(ctx),
  );
  await ui.fire("send", "read it");
  await client.idle();
  await ui.fire("send", "/details");
  expect(store(ui).details.open).toBe(true);
  await ui.fire("send", "next");
  await client.idle();
  expect(store(ui).details.open).toBe(false);
});

test("inline: the viewer is region state — never committed, never in the model history", async () => {
  const { ui, client, seen } = setup(
    toolScript(`${LONG_OUTPUT}\n${"z".repeat(70_000)}`),
    { presentation: "inline" },
  );
  await ui.fire("send", "read it");
  await client.idle();
  const commits = ui.commits.length;
  expect(commits).toBeGreaterThan(0);
  // The committed card is the truncated one: line 15 never reached print.
  expect(JSON.stringify(ui.commits)).not.toContain("output line 15");
  await ui.fire("send", "/details");
  expect(store(ui).details.open).toBe(true);
  expect(detailsText(ui)).toContain("output line 15");
  expect(ui.commits.length).toBe(commits);
  await ui.fire("details_close");
  expect(ui.commits.length).toBe(commits);
  // The viewer's own truncation note exists only in the viewer: the next
  // turn's model history (which does carry the tool receipt itself)
  // never contains it.
  await ui.fire("send", "again");
  await client.idle();
  expect(JSON.stringify(seen)).not.toContain("truncated for the viewer");
  expect(client.writeErrors).toEqual([]);
});

// ------------------------------------------------------- panel, sessions, export

const fakeSession = (handle: string, messages: Message[]): SessionContext =>
  ({
    store: { path: "/w/sessions.db" },
    state: { publicId: handle, messages },
  }) as unknown as SessionContext;

function capabilityFor(contexts: Record<string, SessionContext>) {
  return {
    list: async () =>
      Object.entries(contexts).map(([handle, context]) => ({
        handle,
        messages: context.state.messages.length,
      })),
    load: async (handle: string) => {
      const context = contexts[handle];
      if (!context) throw new Error("session not found");
      return context;
    },
  };
}

/** A session with `items` awaiting recovery; `reconcile` clears the one it records. */
function recoverable(
  session: SessionContext,
  items: RecoveryItem[],
  saved: (state: Session) => { state: Session; closed: number } = (state) => ({
    state,
    closed: 0,
  }),
) {
  const recorded: unknown[] = [];
  return {
    recorded,
    sessions: {
      ...capabilityFor({ [session.state.publicId]: session }),
      recovery: () => items,
      reconcile: async (
        _session: SessionContext,
        executionId: string,
        disposition: string,
        note: string,
      ) => {
        recorded.push({ executionId, disposition, note });
        items.splice(
          items.findIndex((item) => item.executionId === executionId),
          1,
        );
        return saved({ ...session.state, revision: 2 } as Session);
      },
    },
  };
}

const unknownCall = (executionId: string, summary: string): RecoveryItem => ({
  executionId,
  toolName: "run_command",
  state: "ran",
  summary,
  at: "2026-10-24T00:00:00.000Z",
});

test("/recover lists the calls whose outcome is unknown, and a turn waits until each is recorded", async () => {
  const session = fakeSession("aaa111", [{ role: "user", content: "go" }]);
  const items = [
    unknownCall("e1", "make deploy"),
    { ...unknownCall("e2", ""), toolName: "mcp_write", state: "unknown" },
  ] satisfies RecoveryItem[];
  const { sessions, recorded } = recoverable(session, items);
  const { ui, client, seen } = setup(reply("done"), { session, sessions });
  await ui.fire("send", "hello");
  expect(seen).toHaveLength(0);
  expect(store(ui).status).toBe(
    "2 calls have an unknown outcome · /recover to review before continuing",
  );
  await ui.fire("send", "/recover");
  expect(store(ui).panel.title).toBe("Recover");
  const lines = store(ui).panel.lines.map((line) => line.t);
  expect(lines).toContain(
    "1. run_command make deploy · it started and has no result",
  );
  expect(lines).toContain("2. mcp_write · its result is unknown");
  expect(store(ui).status).toBe(
    "2 calls with an unknown outcome · /recover N applied|not-applied|undone",
  );
  await ui.fire("send", "/recover 3 applied");
  expect(store(ui).status).toBe("No call 3 to recover: /recover lists 2");
  expect(recorded).toEqual([]);
  await ui.fire("send", "/recover 1 not-applied nothing changed");
  await client.idle();
  expect(recorded).toEqual([
    {
      executionId: "e1",
      disposition: "effect_not_applied",
      note: "nothing changed",
    },
  ]);
  expect(store(ui).status).toBe(
    "Recorded: run_command · it made no change · 1 left: /recover",
  );
  expect(store(ui).panel.open).toBe(false);
  expect(session.state.revision).toBe(2);
  await ui.fire("send", "hello");
  expect(seen).toHaveLength(0);
  expect(store(ui).status).toBe(
    "1 call has an unknown outcome · /recover to review before continuing",
  );
  await ui.fire("send", "/recover 1 applied");
  await client.idle();
  expect(store(ui).status).toBe(
    "Recorded: mcp_write · it ran and its change is in place",
  );
  await ui.fire("send", "/recover");
  expect(store(ui).status).toBe(
    "Nothing to recover: no call in this session has an unknown outcome",
  );
  await ui.fire("send", "hello");
  await client.idle();
  expect(seen).toHaveLength(1);
});

test("/recover reloads the conversation when a recorded call gets its result, and says why it cannot record", async () => {
  const call: Message = {
    role: "assistant",
    content: "",
    tool_calls: [
      { id: "c1", function: { name: "run_command", arguments: {} } },
    ],
  };
  const session = fakeSession("aaa111", [
    { role: "user", content: "deploy it" },
    call,
  ]);
  const { sessions } = recoverable(
    session,
    [unknownCall("e1", "make deploy")],
    (state) => ({
      state: {
        ...state,
        messages: [
          ...state.messages,
          {
            role: "tool",
            tool_name: "run_command",
            tool_call_id: "c1",
            content: JSON.stringify({ status: "failed", output: "checked" }),
          },
        ],
      },
      closed: 1,
    }),
  );
  const { ui, client } = setup(reply("done"), { session, sessions });
  await ui.fire("send", "/recover 1 undone");
  await client.idle();
  expect(store(ui).status).toBe(
    "Recorded: run_command · its change was undone",
  );
  // The call now has its result, as the store saved it.
  expect(store(ui).messages[1]?.card).toMatchObject({
    name: "run_command",
    status: "failed",
    out: ["checked"],
  });
  // A refusal from the store is the status; nothing else changes.
  const failing = {
    ...recoverable(session, [unknownCall("e2", "rm -rf build")]).sessions,
    reconcile: async () => {
      throw new Error("No execution e2 awaits recovery in this session");
    },
  };
  const other = setup(reply("done"), { session, sessions: failing });
  await other.ui.fire("send", "/recover 1 applied");
  await other.client.idle();
  expect(store(other.ui).status).toBe(
    "No execution e2 awaits recovery in this session",
  );
  // Without a session store there is nothing to recover from.
  const bare = setup(reply("done"));
  await bare.ui.fire("send", "/recover");
  expect(store(bare.ui).status).toBe(
    "Recovery needs --session-db PATH at startup",
  );
});

test("/recover waits while a turn runs, and a failed turn names it when a call is left unknown", async () => {
  const session = fakeSession("aaa111", []);
  const items: RecoveryItem[] = [];
  const { sessions } = recoverable(session, items);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, client } = setup(
    quiet(async () => {
      await gate;
      items.push(unknownCall("e1", "make deploy"));
      throw new Error("The MCP server failed after the call was sent");
    }),
    { session, sessions },
  );
  await ui.fire("send", "deploy it");
  await ui.fire("send", "/recover");
  expect(store(ui).status).toBe(BUSY_STATUS);
  release();
  await client.idle();
  expect(store(ui).status).toBe(
    "The MCP server failed after the call was sent. /recover reviews it.",
  );
});

test("a send queued behind a turn that leaves a call unknown goes back to the composer", async () => {
  const session = fakeSession("aaa111", []);
  const items: RecoveryItem[] = [];
  const { sessions } = recoverable(session, items);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, client, seen } = setup(
    quiet(async () => {
      await gate;
      items.push(unknownCall("e1", "make deploy"));
    }),
    { session, sessions },
  );
  await ui.fire("send", "deploy it");
  store(ui).draft = "and then report";
  await ui.fire("send", "and then report");
  expect(store(ui).status).toBe("Queued: and then report · ↑ edits it");
  release();
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(store(ui).draft).toBe("and then report");
  expect(store(ui).status).toBe(
    "1 call has an unknown outcome · /recover to review before continuing · queued message back in the composer",
  );
});

test("a queued send refused past an unknown outcome joins what was typed since", async () => {
  const session = fakeSession("aaa111", []);
  const items: RecoveryItem[] = [];
  const { sessions } = recoverable(session, items);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, client, seen } = setup(
    quiet(async () => {
      await gate;
      items.push(unknownCall("e1", "make deploy"));
    }),
    { session, sessions },
  );
  await ui.fire("send", "deploy it");
  await ui.fire("send", "and then report");
  store(ui).draft = "one more thing";
  await ui.fire("typed", "one more thing");
  release();
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(store(ui).draft).toBe("one more thing\nand then report");
});

test("/status opens the panel with the config the client actually holds", async () => {
  const session = fakeSession("abc123", []);
  const { ui } = setup(reply("x"), { session });
  await ui.fire("send", "/status");
  const panel = store(ui).panel;
  expect(panel.open).toBe(true);
  expect(panel.title).toBe("Status");
  const lines = panel.lines.map((line) => line.t);
  expect(lines).toContain("Model: fixture");
  expect(lines).toContain("Provider: Ollama / loopback");
  expect(lines).toContain("Mode: build");
  expect(lines).toContain("Session: abc123");
  expect(lines).toContain("Session db: /w/sessions.db");
  expect(lines).toContain("Theme: auto");
  expect(lines).toContain("Mouse: on");
  expect(lines).toContain("Context: 0/8.0k");
  await ui.fire("panel_close");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe("build");
});

test("/provider asks the endpoint once, read-only, and opens the panel over its answer", async () => {
  const asked: string[] = [];
  const { ui } = setup(reply("x"), {
    environment: "gpu-a10",
    probe: async (asking) => {
      asked.push(asking.model);
      return {
        latencyMs: 14,
        version: "0.12.6",
        loaded: {
          sizeBytes: 4 * 1024 ** 3,
          vramBytes: 3 * 1024 ** 3,
          contextLength: 8192,
        },
      };
    },
  });
  await ui.fire("send", "/provider");
  await until(() => store(ui).panel.open);
  expect(asked).toEqual(["fixture"]);
  expect(store(ui).panel.title).toBe("Provider");
  expect(store(ui).status).toBe("Provider");
  const text = store(ui)
    .panel.lines.map((line) => line.t)
    .join("\n");
  for (const line of [
    "Provider: Ollama / loopback",
    "Model: fixture",
    "Chalupa environment: gpu-a10",
    "Context asked: 8192 tokens",
    "Answer: 14 ms",
    "Ollama: 0.12.6",
    "Loaded: 4.0 GiB · 75% in GPU memory",
    "Loaded context: 8192 tokens",
  ])
    expect(text).toContain(line);
  expect(text.replace(/\s+/g, " ")).toContain(
    "Warning: fixture: only 75% of it is in GPU memory",
  );
});

test("/provider says when the endpoint does not answer, and claims nothing loaded", async () => {
  const { ui } = setup(reply("x"), {
    probe: async () => ({ error: "Unable to connect" }),
  });
  await ui.fire("send", "/provider");
  await until(() => store(ui).panel.open);
  expect(store(ui).status).toBe(PROVIDER_NO_ANSWER_STATUS);
  const lines = store(ui).panel.lines.map((line) => line.t);
  expect(lines).toContain("Answer: none (Unable to connect)");
  expect(lines.some((line) => line.startsWith("Loaded"))).toBe(false);
});

test("after a model's first finished turn, a placement short of what was asked leaves one warning", async () => {
  let reads = 0;
  const { ui, client } = setup(reply("hello there"), {
    placement: async () => {
      reads++;
      return { sizeBytes: 100, vramBytes: 50, contextLength: 4096 };
    },
  });
  await ui.fire("send", "one");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  // Once per endpoint, model and size: the second turn does not ask again.
  expect(reads).toBe(1);
  const views = store(ui).messages;
  const notices = views.filter((m: MessageView) => m.is_notice).map(viewText);
  expect(notices).toEqual([
    "Warning: fixture: only 50% of it is in GPU memory; the rest runs on the CPU, slower; and it is loaded with a context of 4096 tokens, below the 8192 asked. /provider shows it again.",
  ]);
  // It follows the first turn's reply, ahead of the second prompt.
  const at = views.findIndex((m: MessageView) => m.is_notice);
  expect(views[at - 1]?.is_asst).toBe(true);
  expect(views[at + 1]?.is_user).toBe(true);
});

test("a model loaded as asked, an unknown placement, or a failed turn leave no warning", async () => {
  for (const loaded of [
    { sizeBytes: 100, vramBytes: 100, contextLength: 8192 },
    null,
    undefined,
  ]) {
    const { ui, client } = setup(reply("hello"), {
      placement: async () => loaded,
    });
    await ui.fire("send", "one");
    await client.idle();
    expect(store(ui).messages.some((m: MessageView) => m.is_notice)).toBe(
      false,
    );
  }
  let reads = 0;
  const failing = setup(
    async function* () {
      yield { type: "text", text: "partial " };
      throw new Error("model fell over");
    },
    {
      placement: async () => {
        reads++;
        return { sizeBytes: 100, vramBytes: 0 };
      },
    },
  );
  await failing.ui.fire("send", "one");
  await failing.client.idle();
  expect(reads).toBe(0);
});

test("a slow placement read never holds the composer: its warning follows a later turn", async () => {
  let answer: (loaded: { sizeBytes: number; vramBytes: number }) => void =
    () => {};
  const { ui, client } = setup(reply("hello there"), {
    placement: () =>
      new Promise((resolve) => {
        answer = resolve;
      }),
  });
  await ui.fire("send", "one");
  await client.idle();
  // The turn is over and the composer free, the read still out.
  expect(isBusy(ui)).toBe(false);
  expect(store(ui).messages.some((m: MessageView) => m.is_notice)).toBe(false);
  answer({ sizeBytes: 100, vramBytes: 50 });
  await ui.fire("send", "two");
  await client.idle();
  const views = store(ui).messages;
  const at = views.findIndex((m: MessageView) => m.is_notice);
  expect(viewText(views[at] as MessageView)).toContain("only 50%");
  // After the second reply, the turn that showed it.
  expect(views[at - 1]?.is_asst).toBe(true);
  expect(views.slice(0, at).filter((m: MessageView) => m.is_user)).toHaveLength(
    2,
  );
});

test("inline: the placement warning prints after the reply it follows", async () => {
  const { ui, client } = setup(reply("hello there"), {
    presentation: "inline",
    placement: async () => ({ sizeBytes: 100, vramBytes: 50 }),
  });
  await ui.fire("send", "hi");
  await client.idle();
  const out = printed(ui);
  const answer = out.findIndex((line) => line.includes("hello there"));
  const warning = out.findIndex((line) => line.includes("only 50%"));
  expect(answer).toBeGreaterThanOrEqual(0);
  expect(warning).toBeGreaterThan(answer);
});

test("/mcp with no servers says so in the panel, count line on top", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("send", "/mcp");
  expect(store(ui).panel.open).toBe(true);
  expect(store(ui).panel.args).toBe(
    "MCP servers: 0 · use --mcp-config to connect trusted servers",
  );
  expect(store(ui).panel.lines.map((line) => line.t)).toEqual([
    "No MCP servers configured",
    "Use --mcp-config to connect trusted servers",
  ]);
});

test("/sessions without a session capability says it needs --session-db", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("send", "/sessions");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe("Sessions need --session-db PATH at startup");
});

test("/sessions new starts over in a new saved session, and the next turn checkpoints only it", async () => {
  const first = fakeSession("aaa111", [
    { role: "user", content: "old prompt" },
    { role: "assistant", content: "old reply" },
  ]);
  const fresh = fakeSession("new999", []);
  let created = 0;
  const sessions = {
    ...capabilityFor({ aaa111: first }),
    create: async () => {
      created++;
      return fresh;
    },
  };
  const turns: { session?: SessionContext; messages: string[] }[] = [];
  const { ui, client } = setup(
    async function* ({ options, messages }) {
      turns.push({
        session: options.session,
        messages: messages.map((m) => `${m.role}:${m.content}`),
      });
      yield {
        type: "message",
        message: { role: "assistant", content: "hello again" },
      };
    },
    { session: first, sessions },
  );
  await ui.fire("send", "/sessions new");
  await client.idle();
  expect(created).toBe(1);
  expect(store(ui).status).toBe("New session new999");
  expect(store(ui).header.session).toBe("session new999");
  expect(store(ui).messages).toEqual([]);
  await ui.fire("send", "fresh start");
  await client.idle();
  expect(turns[0]?.session?.state.publicId).toBe("new999");
  // Nothing of the old conversation reaches the model.
  expect(turns[0]?.messages).toEqual(["user:fresh start"]);
});

test("/sessions new without a store starts a temporary conversation; while busy it waits", async () => {
  const seenMessages: string[][] = [];
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let calls = 0;
  const { ui, client } = setup(async function* ({ messages }) {
    seenMessages.push(messages.map((m) => `${m.role}:${m.content}`));
    if (++calls === 1) await gate;
    yield { type: "message", message: { role: "assistant", content: "ok" } };
  });
  await ui.fire("send", "first");
  // Busy: refused, the turn untouched.
  await ui.fire("send", "/sessions new");
  expect(store(ui).status).toBe(BUSY_STATUS);
  release();
  await client.idle();
  await ui.fire("send", "/sessions new");
  await client.idle();
  expect(store(ui).status).toBe("New conversation (temporary: not saved)");
  expect(store(ui).header.session).toBe("temporary");
  expect(store(ui).messages).toEqual([]);
  await ui.fire("send", "second");
  await client.idle();
  expect(seenMessages.at(-1)).toEqual(["user:second"]);
});

test("a windowed request leaves a notice when more is left out, not on every turn", async () => {
  const lefts = [40, 40, 80];
  let turn = 0;
  const { ui, client } = setup(async function* () {
    const left = lefts[turn++] ?? 0;
    yield { type: "windowed", left, sent: 60 };
    yield { type: "message", message: { role: "assistant", content: "ok" } };
  });
  for (const prompt of ["one", "two", "three"]) {
    await ui.fire("send", prompt);
    await client.idle();
  }
  const notices = store(ui)
    .messages.filter((m: MessageView) => m.is_notice)
    .map(viewText);
  expect(notices).toEqual([
    "Context window full: the model now sees the newest 60 messages. The oldest 40 stay in the session but are not sent; /sessions new starts over.",
    "Context window full: the model now sees the newest 60 messages. The oldest 80 stay in the session but are not sent; /sessions new starts over.",
  ]);
});

test("a full conversation says so, and how to start a new one", async () => {
  const { ui, client } = setup(
    quiet(async () => {
      throw new ConversationFullError(
        "This conversation is full: it has 1000 messages, and a request carries at most 999 besides the system prompt.",
      );
    }),
  );
  await ui.fire("send", "one more");
  await client.idle();
  const notice = store(ui).messages.find((m: MessageView) => m.is_error);
  expect(viewText(notice as MessageView)).toBe(
    "This conversation is full: it has 1000 messages, and a request carries at most 999 besides the system prompt. /sessions new starts a new one.",
  );
});

test("the picker loads a session whole: history, header and the next turn's checkpoint target", async () => {
  const first = fakeSession("aaa111", [
    { role: "user", content: "first prompt" },
    { role: "assistant", content: "first reply" },
  ]);
  const second = fakeSession("bbb222", [
    { role: "user", content: "saved prompt" },
    { role: "assistant", content: "saved reply" },
  ]);
  const sessions = capabilityFor({ aaa111: first, bbb222: second });
  const turns: { session?: SessionContext; messages: string[] }[] = [];
  const { ui, client } = setup(
    async function* ({ options, messages }) {
      turns.push({
        session: options.session,
        messages: messages.map((m) => `${m.role}:${m.content}`),
      });
      yield {
        type: "message",
        message: { role: "assistant", content: "continued" },
      };
    },
    { session: first, sessions },
  );
  expect(store(ui).header.session).toBe("session aaa111");
  await ui.fire("send", "/sessions");
  // A draft in progress (the palette opens the picker without touching
  // it) survives the switch.
  store(ui).draft = "keep me";
  expect(store(ui).panel.picking).toBe(true);
  expect(store(ui).panel.rows.map((row) => [row.id, row.sel])).toEqual([
    ["aaa111", true],
    ["bbb222", false],
  ]);
  await ui.fire("panel_next");
  expect(store(ui).panel.rows[1]?.sel).toBe(true);
  await ui.fire("panel_run");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe("Resumed session bbb222 · 2 messages");
  expect(store(ui).header.session).toBe("session bbb222");
  expect(store(ui).draft).toBe("keep me");
  expect(store(ui).messages.map(viewText)).toEqual([
    "saved prompt",
    "saved reply",
  ]);
  // The next turn continues — and checkpoints — only the loaded session.
  await ui.fire("send", "continue please");
  await client.idle();
  expect(turns).toHaveLength(1);
  expect(turns[0]?.session?.state.publicId).toBe("bbb222");
  expect(turns[0]?.messages).toEqual([
    "user:saved prompt",
    "assistant:saved reply",
    "user:continue please",
  ]);
  expect(JSON.stringify(turns)).not.toContain("first prompt");
  expect(client.writeErrors).toEqual([]);
});

test("picking the current session just says so; a refused load keeps the conversation", async () => {
  const first = fakeSession("aaa111", [{ role: "user", content: "here" }]);
  const sessions = {
    list: async () => [
      { handle: "aaa111", messages: 1 },
      { handle: "bad999", messages: 4 },
    ],
    load: async () => {
      throw new Error(
        "Session has an uncertain tool outcome; inspect recovery before continuing",
      );
    },
  };
  const { ui } = setup(reply("x"), { session: first, sessions });
  await ui.fire("send", "/sessions");
  await ui.fire("panel_run");
  expect(store(ui).status).toBe("Already in session aaa111");
  expect(store(ui).panel.open).toBe(false);
  await ui.fire("send", "/sessions");
  await ui.fire("panel_next");
  await ui.fire("panel_run");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe(
    "Session has an uncertain tool outcome; inspect recovery before continuing",
  );
  // Nothing was swapped: the live conversation is still the first session's.
  expect(store(ui).header.session).toBe("session aaa111");
  expect(store(ui).messages).toHaveLength(1);
});

test("a session switch is refused while a turn runs, and grants survive it", async () => {
  const first = fakeSession("aaa111", []);
  const second = fakeSession("bbb222", [{ role: "user", content: "old" }]);
  const sessions = capabilityFor({ aaa111: first, bbb222: second });
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  let asked = false;
  const { ui, client } = setup(
    async function* ({ options }) {
      if (!asked) {
        asked = true;
        decisions.push(await options.approve(writeProposal(), options.signal));
      }
      await gate;
      yield {
        type: "message",
        message: { role: "assistant", content: "done" },
      };
    },
    { session: first, sessions },
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  // The turn is still running behind the gate: /sessions is refused busy.
  await ui.fire("send", "/sessions");
  expect(store(ui).status).toBe("Busy · wait for the current turn to finish");
  expect(store(ui).panel.open).toBe(false);
  release();
  await client.idle();
  expect(store(ui).header.grants).toBe("auto: files");
  await ui.fire("send", "/sessions");
  await ui.fire("panel_next");
  await ui.fire("panel_run");
  expect(store(ui).status).toBe(
    "Resumed session bbb222 · 1 messages · grants kept: files",
  );
  // The grant is the process's, not the saved session's: still listed.
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe("Permissions: session: files");
});

test("inline: a switch commits the loaded history and enter loads instead of sending", async () => {
  const first = fakeSession("aaa111", []);
  const second = fakeSession("bbb222", [
    { role: "user", content: "saved inline prompt" },
  ]);
  const sessions = capabilityFor({ aaa111: first, bbb222: second });
  const { ui, client, seen } = setup(reply("x"), {
    session: first,
    sessions,
    presentation: "inline",
  });
  await ui.fire("send", "/sessions");
  expect(store(ui).panel.picking).toBe(true);
  await ui.fire("panel_next");
  const commits = ui.commits.length;
  // Enter inline is the composer's send; under the picker it loads.
  await ui.fire("send", "");
  expect(store(ui).status).toBe("Resumed session bbb222 · 1 messages");
  expect(seen).toHaveLength(0);
  expect(ui.commits.length).toBe(commits + 1);
  expect(JSON.stringify(ui.commits.at(-1)?.buf)).toContain(
    "saved inline prompt",
  );
  await client.idle();
  expect(client.writeErrors).toEqual([]);
});

test("/export writes the visible conversation once, never overwriting", async () => {
  const files = new Map<string, string>();
  const writeExport = async (path: string, text: string) => {
    if (files.has(path))
      throw Object.assign(new Error("EEXIST"), { code: "EEXIST" });
    files.set(path, text);
  };
  const at = new Date(2026, 9, 22, 9, 5, 3).getTime();
  const { ui, client } = setup(reply("exported reply"), {
    writeExport,
    now: () => at,
  });
  await ui.fire("send", "/export");
  expect(store(ui).status).toBe("Nothing to export yet");
  await ui.fire("send", "export this");
  await client.idle();
  await ui.fire("send", "/export");
  expect(store(ui).status).toBe(
    "Exported transcript to local-agent-transcript-20261022-090503.md",
  );
  const only = [...files.values()][0] ?? "";
  expect(only).toContain("## You\n\nexport this");
  expect(only).toContain("## Assistant\n\nexported reply");
  // The same second again: a suffixed name, the first file untouched.
  await ui.fire("send", "/export");
  expect(store(ui).status).toBe(
    "Exported transcript to local-agent-transcript-20261022-090503-2.md",
  );
  expect(files.size).toBe(2);
});

test("a failed export lands as an error notice, not a model message", async () => {
  const { ui, client, seen } = setup(reply("x"), {
    writeExport: async () => {
      throw new Error("disk is full");
    },
  });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/export");
  expect(store(ui).status).toBe("Export failed: disk is full");
  expect(store(ui).messages.at(-1)?.is_error).toBe(true);
  await ui.fire("send", "next");
  await client.idle();
  expect(JSON.stringify(seen)).not.toContain("Export failed");
});

const validated = (model: string) =>
  ({
    status: "validated",
    model,
    sizeBytes: 1,
    nativeContext: undefined,
    capabilities: ["completion"],
  }) as OllamaAdmission;

test("/model reports the current model, then switches the provider the next turn runs on", async () => {
  const providers: Provider[] = [];
  const { ui, client } = setup(reply("ok"), {
    admitModel: async (candidate) => validated(candidate.model),
    run: (provider, messages, options) => {
      providers.push(provider);
      return reply("ok")({ options, messages });
    },
  });
  await ui.fire("send", "/model");
  expect(store(ui).status).toBe(
    "Current model: fixture. Use /model NAME to switch",
  );
  store(ui).draft = "/model llama3.2";
  await ui.fire("send", "/model llama3.2");
  expect(store(ui).status).toBe("Model changed to llama3.2");
  expect(store(ui).draft).toBe("");
  expect(store(ui).header.model).toBe("llama3.2");
  await ui.fire("send", "hi");
  await client.idle();
  expect(providers.at(-1)?.model).toBe("llama3.2");
});

test("/model on an OpenAI-compatible server checks its model list, as launch does", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) =>
      new URL(request.url).pathname === "/v1/models"
        ? Response.json({ data: [{ id: "qwen3-4b" }, { id: "llama-3.2" }] })
        : new Response("not found", { status: 404 }),
  });
  try {
    const providers: Provider[] = [];
    const { ui, client } = setup(reply("ok"), {
      provider: {
        type: "openai_local",
        base_url: `http://127.0.0.1:${server.port}/v1`,
        model: "qwen3-4b",
        context_size: 8192,
      } as Provider,
      run: (provider, messages, options) => {
        providers.push(provider);
        return reply("ok")({ options, messages });
      },
    });
    await ui.fire("send", "/model mistral");
    expect(store(ui).status).toBe(
      `Model mistral is not served by http://127.0.0.1:${server.port}/v1. Available: qwen3-4b, llama-3.2.`,
    );
    expect(store(ui).header.model).toBe("qwen3-4b");
    await ui.fire("send", "/model llama-3.2");
    expect(store(ui).status).toBe("Model changed to llama-3.2");
    await ui.fire("send", "hi");
    await client.idle();
    expect(providers.at(-1)).toMatchObject({
      type: "openai_local",
      model: "llama-3.2",
      context_size: 8192,
    });
  } finally {
    await server.stop(true);
  }
});

test("/model on a server without a model list switches, and says the name was not checked", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: () => new Response("not found", { status: 404 }),
  });
  try {
    const { ui } = setup(reply("ok"), {
      provider: {
        type: "openai_local",
        base_url: `http://127.0.0.1:${server.port}/v1`,
        model: "qwen3-4b",
        context_size: 8192,
      } as Provider,
    });
    await ui.fire("send", "/model mistral");
    expect(store(ui).status).toBe(
      "Model changed to mistral · Server does not expose GET /v1/models; skipped model validation",
    );
    expect(store(ui).header.model).toBe("mistral");
  } finally {
    await server.stop(true);
  }
});

test("/model on an OpenAI-compatible server that cannot be reached keeps the model", async () => {
  // A port nothing listens on any more.
  const gone = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: () => new Response(),
  });
  const port = gone.port;
  await gone.stop(true);
  const { ui } = setup(reply("ok"), {
    provider: {
      type: "openai_local",
      base_url: `http://127.0.0.1:${port}/v1`,
      model: "qwen3-4b",
      context_size: 8192,
    } as Provider,
  });
  await ui.fire("send", "/model qwen3-4bb");
  expect(store(ui).status).toBe("OpenAI-compatible endpoint is unreachable");
  expect(store(ui).header.model).toBe("qwen3-4b");
});

/** A loopback Ollama whose inventory holds qwen3.5:4b, and nothing else. */
function ollamaInventory() {
  return Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) => {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({
          models: [{ name: "qwen3.5:4b", size: 3_000_000_000 }],
        });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: {
            "general.architecture": "qwen3",
            "qwen3.context_length": 32_768,
          },
        });
      return new Response("not found", { status: 404 });
    },
  });
}

test("/model on Ollama asks Ollama's inventory, as launch does", async () => {
  const server = ollamaInventory();
  try {
    const { ui } = setup(reply("ok"), {
      provider: {
        ...provider,
        base_url: `http://127.0.0.1:${server.port}`,
      } as Provider,
    });
    await ui.fire("send", "/model qwen3.5:4b");
    expect(store(ui).status).toMatch(/^Model changed to qwen3\.5:4b/);
    expect(store(ui).header.model).toBe("qwen3.5:4b");
  } finally {
    await server.stop(true);
  }
});

test("/model on Chalupa asks the Ollama inventory behind it, as launch does", async () => {
  const server = ollamaInventory();
  try {
    const { ui } = setup(reply("ok"), {
      provider: {
        ...provider,
        type: "ollama_remote",
        base_url: `http://127.0.0.1:${server.port}`,
        max_model_bytes: 10_000_000_000,
        stream_idle_timeout: "30s",
      } as Provider,
    });
    await ui.fire("send", "/model qwen3.5:4b");
    expect(store(ui).status).toBe("Model changed to qwen3.5:4b");
    expect(store(ui).header.model).toBe("qwen3.5:4b");
  } finally {
    await server.stop(true);
  }
});

test("/model switches nothing when the process quits during the check", async () => {
  const quit = new AbortController();
  const { ui } = setup(reply("ok"), {
    signal: quit.signal,
    admitModel: async (candidate) => {
      quit.abort();
      return validated(candidate.model);
    },
  });
  await ui.fire("send", "/model llama3.2");
  expect(store(ui).header.model).toBe("fixture");
  expect(store(ui).status).not.toBe("Model changed to llama3.2");
});

test("/model on Ollama does not take an unreachable inventory for a yes", async () => {
  const { ui } = setup(reply("ok"), {
    admitModel: async () => ({
      status: "unverified",
      kind: "transport",
      message: "Ollama is not reachable at http://127.0.0.1:11434",
    }),
  });
  await ui.fire("send", "/model llama3.2");
  expect(store(ui).status).toBe(
    "Ollama is not reachable at http://127.0.0.1:11434",
  );
  expect(store(ui).header.model).toBe("fixture");
});

test("/model plans the new model's context unless the user chose a size", async () => {
  const GIB = 1024 ** 3;
  const asked: number[] = [];
  const providers: Provider[] = [];
  const admitModel = async (candidate: Provider) => {
    asked.push(candidate.context_size);
    return {
      ...validated(candidate.model),
      sizeBytes: 3.3 * GIB,
      nativeContext: 262_144,
      kvBytesPerToken: 32_768,
    } as OllamaAdmission;
  };
  const run: ConstructorParameters<typeof TuimarkClient>[0]["run"] = (
    provider,
    messages,
    options,
  ) => {
    providers.push(provider);
    return reply("ok")({ options, messages });
  };
  const planned = setup(reply("ok"), {
    admitModel,
    run,
    totalMemory: () => 16 * GIB,
  });
  await planned.ui.fire("send", "/model qwen3.5:4b");
  expect(asked).toEqual([512]);
  expect(store(planned.ui).header.ctx_label).toBe("0/32k");
  await planned.ui.fire("send", "hi");
  await planned.client.idle();
  expect(providers.at(-1)).toMatchObject({
    model: "qwen3.5:4b",
    context_size: 32_768,
    context_plan: expect.stringMatching(/^planned: /),
  });
  await planned.ui.fire("send", "/status");
  expect(JSON.stringify(store(planned.ui))).toContain(
    "Context size: planned: ",
  );

  const chosen = setup(reply("ok"), {
    admitModel,
    run,
    provider: { ...provider, context_explicit: true },
    totalMemory: () => 16 * GIB,
  });
  await chosen.ui.fire("send", "/model qwen3.5:4b");
  expect(asked).toEqual([512, 8192]);
  await chosen.ui.fire("send", "hi");
  await chosen.client.idle();
  expect(providers.at(-1)).toMatchObject({
    context_size: 8192,
    context_plan:
      "set by --context or LOCAL_AGENT_CONTEXT; its KV cache takes 0.3 GiB of the 5.2 GiB left after the weights and headroom",
  });
  expect(providers.at(-1)?.context_warning).toBeUndefined();

  // A chosen size too large for this machine stays, with a warning.
  const large = setup(reply("ok"), {
    admitModel,
    run,
    provider: { ...provider, context_size: 262_144, context_explicit: true },
    totalMemory: () => 16 * GIB,
  });
  await large.ui.fire("send", "/model qwen3.5:4b");
  expect(store(large.ui).status).toBe(
    "Model changed to qwen3.5:4b · context 262144 needs a 8.0 GiB KV cache, more than the 5.2 GiB left after the weights and headroom: Ollama may run part of the model on the CPU, or fail to load it",
  );

  // Chalupa's size does not change with the model, and nor does its warning.
  const remote = setup(reply("ok"), {
    admitModel,
    run,
    provider: {
      ...provider,
      type: "ollama_remote",
      context_warning: "Chalupa set a context of 8192 tokens",
    } as Provider,
  });
  await remote.ui.fire("send", "/model qwen3.5:4b");
  await remote.ui.fire("send", "hi");
  await remote.client.idle();
  expect(providers.at(-1)).toMatchObject({
    type: "ollama_remote",
    context_size: 8192,
    context_warning: "Chalupa set a context of 8192 tokens",
  });
});

test("/model to a model on Ollama's MLX runner keeps the planned size and says it is local-agent's own", async () => {
  const GIB = 1024 ** 3;
  const providers: Provider[] = [];
  const admitModel = async (candidate: Provider) =>
    ({
      ...validated(candidate.model),
      sizeBytes: 3.3 * GIB,
      nativeContext: 262_144,
      kvBytesPerToken: 32_768,
      ...(candidate.model === "qwen3.5:4b" ? { mlx: true } : {}),
    }) as OllamaAdmission;
  const run: ConstructorParameters<typeof TuimarkClient>[0]["run"] = (
    provider,
    messages,
    options,
  ) => {
    providers.push(provider);
    return reply("ok")({ options, messages });
  };
  const { ui, client } = setup(reply("ok"), {
    admitModel,
    run,
    totalMemory: () => 16 * GIB,
  });
  await ui.fire("send", "/model qwen3.5:4b");
  expect(store(ui).status).toBe(`Model changed to qwen3.5:4b · ${MLX_WARNING}`);
  expect(store(ui).header.ctx_label).toBe("0/32k");
  await ui.fire("send", "hi");
  await client.idle();
  expect(providers.at(-1)).toMatchObject({
    context_size: 32_768,
    context_plan: expect.stringContaining(
      "Ollama's MLX runner ignores num_ctx",
    ),
    context_warning: MLX_WARNING,
  });
  // Back to a GGUF tag: the warning was the other model's.
  await ui.fire("send", "/model qwen3.5:4b-q4_K_M");
  expect(store(ui).status).toBe("Model changed to qwen3.5:4b-q4_K_M");
  await ui.fire("send", "hi");
  await client.idle();
  expect(providers.at(-1)?.context_warning).toBeUndefined();
  expect(providers.at(-1)?.context_plan).toMatch(/^planned: its KV cache/);
});

test("a model the Ollama inventory rejects is refused: no switch, no header write", async () => {
  const providers: Provider[] = [];
  const { ui, client } = setup(reply("ok"), {
    admitModel: async () => ({
      status: "rejected",
      kind: "metadata",
      message: "Model nope is not installed. Run: ollama pull nope",
      code: "not_installed",
    }),
    run: (provider, messages, options) => {
      providers.push(provider);
      return reply("ok")({ options, messages });
    },
  });
  store(ui).draft = "/model nope";
  await ui.fire("send", "/model nope");
  expect(store(ui).status).toBe(
    "Model nope is not installed. Run: ollama pull nope",
  );
  // The typo stays in the composer to fix.
  expect(store(ui).draft).toBe("/model nope");
  expect(store(ui).header.model).toBe("fixture");
  await ui.fire("send", "hi");
  await client.idle();
  expect(providers.at(-1)?.model).toBe("fixture");
});

test("/export json writes the raw messages array to a .json file", async () => {
  const files = new Map<string, string>();
  const at = new Date(2026, 9, 22, 9, 5, 3).getTime();
  const { ui, client } = setup(reply("json reply"), {
    writeExport: async (path, text) => {
      files.set(path, text);
    },
    now: () => at,
  });
  await ui.fire("send", "export this");
  await client.idle();
  await ui.fire("send", "/export json");
  expect(store(ui).status).toBe(
    "Exported transcript to local-agent-transcript-20261022-090503.json",
  );
  const parsed = JSON.parse([...files.values()][0] ?? "") as Message[];
  expect(parsed.map((m) => [m.role, m.content])).toEqual([
    ["user", "export this"],
    ["assistant", "json reply"],
  ]);
});

test("/find without a session capability says it needs --session-db", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("send", "/find needle");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe("Find needs --session-db PATH at startup");
});

test("/find lists the sessions that match, one snippet each, in a panel", async () => {
  const { ui } = setup(reply("x"), {
    sessions: {
      ...capabilityFor({}),
      search: async (query: string) =>
        query === "needle"
          ? [
              { handle: "aaa111", snippet: "…the needle in session one…" },
              { handle: "bbb222", snippet: "…another needle…" },
            ]
          : [],
    },
  });
  await ui.fire("send", "/find missing");
  expect(store(ui).status).toBe('No matches for "missing"');
  await ui.fire("send", "/find needle");
  expect(store(ui).panel.open).toBe(true);
  expect(
    store(ui)
      .panel.lines.map((l) => l.t)
      .join("\n"),
  ).toContain("aaa111");
  expect(
    store(ui)
      .panel.lines.map((l) => l.t)
      .join("\n"),
  ).toContain("the needle");
  expect(store(ui).status).toBe('2 sessions match "needle"');
});

// ------------------------------------------- adversarial-review regressions

test("F1: a send during a gated session load never starts a turn (fullscreen)", async () => {
  const first = fakeSession("aaa111", [
    { role: "user", content: "AAA prompt" },
  ]);
  const second = fakeSession("bbb222", [
    { role: "user", content: "BBB saved prompt" },
  ]);
  let loading = false;
  let releaseLoad: () => void = () => {};
  const loadGate = new Promise<void>((r) => {
    releaseLoad = r;
  });
  const sessions = {
    list: async () => [
      { handle: "aaa111", messages: 1 },
      { handle: "bbb222", messages: 1 },
    ],
    load: async () => {
      loading = true;
      await loadGate;
      return second;
    },
  };
  const { ui, client, seen } = setup(reply("turn reply"), {
    session: first,
    sessions,
  });
  await ui.fire("send", "/sessions");
  await ui.fire("panel_next");
  const switching = ui.fire("panel_run");
  await until(() => loading);
  await ui.fire("send", "hello during load");
  expect(seen).toHaveLength(0);
  releaseLoad();
  await switching;
  expect(store(ui).header.session).toBe("session bbb222");
  await ui.fire("send", "after switch");
  await client.idle();
  expect(seen[0]?.messages).toEqual([
    "user:BBB saved prompt",
    "user:after switch",
  ]);
});

test("F1: a send during a gated session load never starts a turn (inline composer)", async () => {
  const first = fakeSession("aaa111", []);
  const second = fakeSession("bbb222", [
    { role: "user", content: "BBB saved prompt" },
  ]);
  let loading = false;
  let releaseLoad: () => void = () => {};
  const loadGate = new Promise<void>((r) => {
    releaseLoad = r;
  });
  const sessions = {
    list: async () => [
      { handle: "aaa111", messages: 0 },
      { handle: "bbb222", messages: 1 },
    ],
    load: async () => {
      loading = true;
      await loadGate;
      return second;
    },
  };
  const { ui, client, seen } = setup(reply("turn reply"), {
    session: first,
    sessions,
    presentation: "inline",
  });
  await ui.fire("send", "/sessions");
  await ui.fire("panel_next");
  const switching = ui.fire("panel_run");
  await until(() => loading);
  await ui.fire("send", "hello during load");
  expect(seen).toHaveLength(0);
  releaseLoad();
  await switching;
  expect(store(ui).header.session).toBe("session bbb222");
  await ui.fire("send", "after switch");
  await client.idle();
  // The held send is gone: not in the model's history, not in the scrollback.
  expect(seen).toHaveLength(1);
  expect(seen[0]?.messages).toEqual([
    "user:BBB saved prompt",
    "user:after switch",
  ]);
  expect(JSON.stringify(ui.commits)).not.toContain("hello during load");
  expect(printed(ui)).toContain("You: after switch");
});

test("F7: a dismissed slash popup reopens for a fresh draft after a send", async () => {
  const { ui } = setup(reply("x"));
  await ui.fire("typed", "/mo");
  expect(store(ui).slash.open).toBe(true);
  await ui.fire("slash_close");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("send", "/mo");
  // No typed "" is synthesized for the programmatic draft clear.
  await ui.fire("typed", "/m");
  expect(store(ui).slash.open).toBe(true);
});

test("F2: a refused user view is spilled and printed before the reply after recovery", async () => {
  const { ui, client } = setup(reply("hello world"), inline);
  ui.refuseCommit = (_commit, index) =>
    index === 0 ? "refused-user" : undefined;
  await ui.fire("send", "prompt-one");
  await client.idle();
  ui.refuseCommit = undefined;
  await ui.fire("send", "prompt-two");
  await client.idle();
  const out = printed(ui);
  expect(out.filter((s) => s === "You: prompt-one")).toHaveLength(1);
  expect(out.indexOf("You: prompt-one")).toBeLessThan(
    out.findIndex((s) => s.includes("hello world")),
  );
  expect(out.indexOf("You: prompt-one")).toBeLessThan(
    out.indexOf("You: prompt-two"),
  );
});

test("F3: persistently refused reply blocks survive into the next healthy turn", async () => {
  let refusing = true;
  let releaseSecond: () => void = () => {};
  const gate = new Promise<void>((r) => {
    releaseSecond = r;
  });
  let calls = 0;
  const { ui, client } = setup(async function* () {
    calls++;
    if (calls === 1) {
      yield { type: "text", text: "alpha beta gamma" };
      yield {
        type: "message",
        message: { role: "assistant", content: "alpha beta gamma" },
      };
    } else {
      yield { type: "text", text: "second reply" };
      await gate;
      yield {
        type: "message",
        message: { role: "assistant", content: "second reply" },
      };
    }
  }, inline);
  let n = 0;
  ui.refuseCommit = () => (refusing && n++ > 0 ? "refused" : undefined);
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "second");
  await new Promise((r) => setTimeout(r, 20));
  refusing = false;
  releaseSecond();
  await client.idle();
  const out = printed(ui);
  expect(out.filter((s) => s.includes("alpha beta gamma"))).toHaveLength(1);
  expect(out.findIndex((s) => s.includes("alpha beta gamma"))).toBeLessThan(
    out.findIndex((s) => s.includes("second reply")),
  );
});

test("F8: refused pre-tool text prints before the tool card", async () => {
  let refusingPre = true;
  let releaseTool: () => void = () => {};
  const gate = new Promise<void>((r) => {
    releaseTool = r;
  });
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "text", text: "pre-tool text\n\n" };
    yield {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            id: "c1",
            function: { name: "edit", arguments: { path: "a.txt" } },
          },
        ],
      },
    };
    yield { type: "tool_requested", id: "e1", name: "edit" };
    const allowed = await options.approve(editProposal(), options.signal);
    await gate;
    yield {
      type: "message",
      message: {
        role: "tool",
        tool_name: "edit",
        tool_call_id: "c1",
        content: JSON.stringify({
          status: allowed ? "succeeded" : "denied",
          output: "Updated a.txt",
        }),
      } as Message,
    };
  }, inline);
  ui.refuseCommit = () => {
    if (!refusingPre) return undefined;
    const buf = JSON.stringify(ui.commits.at(-1)?.buf ?? "");
    return buf.includes("pre-tool text") ? "refused-pre" : undefined;
  };
  await ui.fire("send", "edit it");
  await untilApproval(ui);
  await ui.fire("dialog_confirm");
  refusingPre = false;
  releaseTool();
  await client.idle();
  const out = printed(ui);
  const pre = out.findIndex((s) => s.includes("pre-tool text"));
  const card = out.findIndex((s) => s === "tool edit succeeded");
  expect(pre).toBeGreaterThanOrEqual(0);
  expect(card).toBeGreaterThan(pre);
});

test("F4: a switch prints the old session's refused reply before the loaded history", async () => {
  const first = fakeSession("aaa111", []);
  const second = fakeSession("bbb222", [
    { role: "user", content: "saved prompt" },
  ]);
  const sessions = capabilityFor({ aaa111: first, bbb222: second });
  let refuse = true;
  let calls = 0;
  const { ui, client } = setup(
    async function* () {
      calls++;
      const text = calls === 1 ? "OLD-SESSION-REPLY" : "fresh reply";
      yield { type: "text", text };
      yield { type: "message", message: { role: "assistant", content: text } };
    },
    {
      presentation: "inline",
      session: first,
      sessions,
    },
  );
  let n = 0;
  ui.refuseCommit = () => (refuse && n++ > 0 ? "refused" : undefined);
  await ui.fire("send", "old prompt");
  await client.idle();
  expect(printed(ui)).toEqual(["You: old prompt"]);
  refuse = false;
  await ui.fire("send", "/sessions");
  await ui.fire("panel_next");
  await ui.fire("panel_run");
  const out = printed(ui);
  const oldReply = out.findIndex((s) => s.includes("OLD-SESSION-REPLY"));
  const saved = out.indexOf("You: saved prompt");
  expect(oldReply).toBeGreaterThanOrEqual(0);
  expect(saved).toBeGreaterThan(oldReply);
  await ui.fire("send", "new prompt");
  await client.idle();
  expect(
    printed(ui).filter((s) => s.includes("OLD-SESSION-REPLY")),
  ).toHaveLength(1);
});

test("F5: concurrent quit requests resolve only after the partial reply prints once", async () => {
  const { ui, client } = setup(async function* ({ options }) {
    yield { type: "text", text: "partial words here" };
    await new Promise((_, reject) =>
      options.signal.addEventListener("abort", () =>
        reject(new Error("aborted")),
      ),
    );
  }, inline);
  ui.ackMs = 30;
  await ui.fire("send", "go");
  await until(() => liveText(store(ui)).includes("partial words here"));
  // The first press only cancels the turn.
  expect(await ui.fire("quit_request")).toBeUndefined();
  const atResolve: string[][] = [];
  const q1 = ui.fire("quit_request").then((v) => {
    atResolve.push(printed(ui));
    return v;
  });
  const q2 = ui.fire("quit_request").then((v) => {
    atResolve.push(printed(ui));
    return v;
  });
  expect(await Promise.all([q1, q2])).toEqual(["quit", "quit"]);
  for (const snapshot of atResolve)
    expect(snapshot).toContain("Assistant: partial words here");
  await client.idle();
  expect(
    printed(ui).filter((s) => s.includes("partial words here")),
  ).toHaveLength(1);
});

test("F5: a second concurrent ctrl+c counts as the quit stage after the draft clear", async () => {
  const { ui } = setup(reply("x"));
  ui.ackMs = 20;
  store(ui).draft = "work in progress";
  const first = ui.fire("quit_request");
  const second = ui.fire("quit_request");
  expect(await first).toBeUndefined();
  expect(await second).toBe("quit");
});

test("F9: the export suffix loop is bounded at 100 attempts", async () => {
  let attempts = 0;
  const { ui, client } = setup(reply("x"), {
    writeExport: async () => {
      attempts++;
      throw Object.assign(new Error("EEXIST"), { code: "EEXIST" });
    },
  });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/export");
  expect(attempts).toBe(100);
  expect(store(ui).status).toContain("Export failed");
  expect(store(ui).messages.at(-1)?.is_error).toBe(true);
});

test("F9: a real export file is created with mode 0o600", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-export-"));
  const at = new Date(2026, 9, 22, 9, 5, 3).getTime();
  const { ui, client } = setup(reply("mode reply"), {
    workspace: { root } as Workspace,
    now: () => at,
  });
  await ui.fire("send", "mode prompt");
  await client.idle();
  await ui.fire("send", "/export");
  const path = join(root, "local-agent-transcript-20261022-090503.md");
  expect((await stat(path)).mode & 0o777).toBe(0o600);
  expect(await readFile(path, "utf8")).toContain("mode prompt");
});

// ------------------------------------------------------------ permission rules

import {
  loadPermissionFile,
  type PermissionRule,
  trustRules,
} from "@local-agent/runtime";

/** Writes the workspace's permissions file, creating the directory. */
async function writeRules(
  root: string,
  rules: PermissionRule[],
): Promise<void> {
  await mkdir(join(root, ".local-agent"), { recursive: true });
  await writeFile(
    join(root, ".local-agent", "permissions.json"),
    JSON.stringify({ version: 1, rules }),
  );
}

test("rules: allow auto-approves and deny refuses, each named in the status, no card", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-rules-"));
  await writeRules(root, [
    { effect: "allow", tool: "edit", pattern: "a.txt" },
    { effect: "deny", tool: "run_command", pattern: "rm *" },
  ]);
  // A hand-written allow rule applies once this user trusts it.
  await trustRules(root);
  const { ui, client } = setup(
    quiet(async ({ options }) => {
      // The deny decision must land even though a rule allows the edit;
      // both go through evaluate() before any card exists.
      const allowed = await options.approve(editProposal(), options.signal);
      const refused = await options.approve(
        commandProposal(["rm", "-rf", "/"]),
        options.signal,
      );
      decisions.push(allowed, refused);
    }),
    { workspace: { root } as Workspace },
  );
  // The deny decision must land even though a rule allows the edit; both
  // go through evaluate() before any card exists. The idle write replaces
  // each flash, so both are read from the writes that carried them.
  await ui.fire("send", "rules");
  await client.idle();
  expect(store(ui).approval.open).toBe(false);
  const statuses = ui.batches
    .flat()
    .filter((w) => w.path === "status")
    .map((w) => String(w.value));
  // The flash carries the elapsed suffix while the turn runs.
  expect(statuses.some((v) => v.startsWith("allowed by rule: a.txt"))).toBe(
    true,
  );
  expect(statuses.some((v) => v.startsWith("denied by rule: rm *"))).toBe(true);
  expect(decisions).toEqual([true, false]);
});

test("rules: `!` saves the proposed rule, grants the call, and later calls auto-allow", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-always-"));
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test", "a.test.ts"])],
      [commandProposal(["bun", "test", "b.test.ts"])],
    ]),
    { workspace: { root } as Workspace },
  );
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_STATUS);
  await ui.fire("dialog_always");
  // The verdict write is the one that closes the dialog; read it before
  // the turn's idle write replaces it.
  expect(store(ui).status).toBe(
    "Always allowed: run_command bun test a.test.ts (saved to .local-agent/permissions.json)",
  );
  await client.idle();
  expect(decisions).toEqual([true]);
  const saved = await loadPermissionFile(root);
  expect(saved.rules).toEqual([
    { effect: "allow", tool: "run_command", pattern: "bun test a.test.ts" },
  ]);
  expect(saved.untrusted).toEqual([]);
  // The saved rule is the exact argv: the sibling call still gets a card.
  await ui.fire("send", "two");
  await until(() => store(ui).status === APPROVAL_STATUS);
  expect(store(ui).approval.open).toBe(true);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false]);
});

test("rules: a cloned repository's allow rule never skips the card until trusted", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-planted-"));
  await writeRules(root, [
    { effect: "allow", tool: "run_command", pattern: "*" },
  ]);
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["curl", "https://evil.example"])],
      [commandProposal(["curl", "https://evil.example"])],
    ]),
    { workspace: { root } as Workspace },
  );
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_STATUS);
  expect(store(ui).approval.open).toBe(true);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe(
    "Permissions: file: allow run_command:* (untrusted)",
  );
  // Confirming before a review trusts nothing.
  await ui.fire("send", "/permissions trust confirm");
  expect(store(ui).status).toBe(
    "Nothing reviewed: run /permissions trust first",
  );
  await ui.fire("send", "/permissions trust");
  expect(store(ui).status).toBe(
    "Untrusted allow 1 rule (check .local-agent/permissions.json, then /permissions trust confirm): run_command *",
  );
  // A rule written into the file after the review is not trusted with it.
  await writeRules(root, [
    { effect: "allow", tool: "run_command", pattern: "*" },
    { effect: "allow", tool: "edit", pattern: "*" },
  ]);
  await ui.fire("send", "/permissions trust confirm");
  expect(store(ui).status).toBe("Trusted 1 rule: run_command *");
  expect((await loadPermissionFile(root)).untrusted).toEqual([
    { effect: "allow", tool: "edit", pattern: "*" },
  ]);
  await ui.fire("send", "two");
  await client.idle();
  expect(decisions).toEqual([false, true]);
});

test("rules: the runtime's read hook sees the file's deny rules", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-denied-read-"));
  await writeRules(root, [
    { effect: "deny", tool: "fetch", pattern: "domain:tracker.example" },
    { effect: "deny", tool: "web_search", pattern: "*" },
  ]);
  const verdicts: (string | undefined)[] = [];
  const { ui, client } = setup(
    async function* ({ options }) {
      for (const request of [
        { tool: "fetch", target: "tracker.example" },
        { tool: "fetch", target: "docs.example" },
        { tool: "web_search", target: "anything" },
      ] as const)
        verdicts.push(await options.deniedRead?.(request));
      yield {
        type: "message",
        message: { role: "assistant", content: "done" },
      };
    },
    { workspace: { root } as Workspace },
  );
  await ui.fire("send", "one");
  await client.idle();
  expect(verdicts).toEqual(["domain:tracker.example", undefined, "*"]);
});

test("rules: a deny rule refuses an MCP call in chat intent too", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-chat-deny-"));
  await writeRules(root, [{ effect: "deny", tool: "mcp", pattern: "db__*" }]);
  const { ui, client } = setup(
    turnsAsking([[{ kind: "mcp", name: "db__query", argumentsJson: "{}" }]]),
    { workspace: { root } as Workspace, intent: "chat" },
  );
  await ui.fire("send", "one");
  await client.idle();
  expect(decisions).toEqual([false]);
  expect(store(ui).approval.open).toBe(false);
});

test("rules: confirm trusts only the rules the review line could show", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-trust-many-"));
  const many = Array.from({ length: 12 }, (_, i) => ({
    effect: "allow" as const,
    tool: "run_command" as const,
    pattern: `tool-${i} --with-a-fairly-long-flag-${i}`,
  }));
  await writeRules(root, many);
  const { ui } = setup(reply("hi"), { workspace: { root } as Workspace });
  await ui.fire("send", "/permissions trust");
  const review = store(ui).status;
  expect(review.length).toBeLessThanOrEqual(300);
  expect(review).not.toContain("…");
  const shown = many.filter((rule) => review.includes(rule.pattern));
  expect(shown.length).toBeGreaterThan(0);
  expect(shown.length).toBeLessThan(many.length);
  expect(review).toContain(`${many.length - shown.length} more after these`);
  await ui.fire("send", "/permissions trust confirm");
  expect(store(ui).status).toContain(`Trusted ${shown.length} rules`);
  expect((await loadPermissionFile(root)).untrusted).toEqual(
    many.slice(shown.length),
  );
});

test("rules: trust given from another process reaches the cached rules", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-trust-elsewhere-"));
  await writeRules(root, [
    { effect: "allow", tool: "run_command", pattern: "*" },
  ]);
  const { ui, client } = setup(
    turnsAsking([[commandProposal(["make"])], [commandProposal(["make"])]]),
    { workspace: { root } as Workspace },
  );
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_STATUS);
  await ui.fire("dialog_cancel");
  await client.idle();
  // `local-agent --trust-rules` in another terminal: the file is untouched,
  // only the trust store moves.
  await trustRules(root);
  await ui.fire("send", "two");
  await client.idle();
  expect(decisions).toEqual([false, true]);
});

test("rules: /permissions lists both origins and clear rules empties the file", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-list-"));
  const { ui, client } = setup(turnsAsking([[editProposal()]]), {
    workspace: { root } as Workspace,
  });
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe(
    "No permissions: no session grants, no file rules",
  );
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_STATUS);
  await ui.fire("dialog_always");
  await client.idle();
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe("Permissions: file: allow edit:/a.txt");
  await ui.fire("send", "/permissions clear rules");
  expect(store(ui).status).toBe("File rules cleared");
  expect(await loadPermissionFile(root)).toEqual({
    version: 1,
    rules: [],
    untrusted: [],
  });
});

/**
 * A run that compacts the history it is handed (the last assistant reply
 * becomes a note), then fails; every other turn replies "ok".
 */
function compactingRun(sent: Message[][]) {
  let turn = 0;
  return (
    _provider: Provider,
    messages: Message[],
    options: AgentOptions,
  ): AsyncGenerator<AgentEvent> => {
    sent.push(messages.slice());
    turn++;
    if (turn !== 2) return reply("ok")({ options, messages });
    return (async function* (): AsyncGenerator<AgentEvent> {
      const compacted = messages.map((message) =>
        message.role === "assistant"
          ? { ...message, content: "[note]" }
          : message,
      );
      yield {
        type: "compacted",
        messages: compacted,
        elided: 1,
        tokensBefore: 6_000,
        tokensAfter: 3_000,
      };
      throw new Error("provider failed");
    })();
  };
}

test("a compacted history is what the next turn sends, after a failed turn", async () => {
  const sent: Message[][] = [];
  const { ui, client } = setup(reply("ok"), {
    run: compactingRun(sent),
    session: fakeSession("abc123", []),
  });
  await ui.fire("send", "hello");
  await client.idle();
  await ui.fire("send", "again");
  await client.idle();
  expect(JSON.stringify(store(ui).messages)).toContain(
    "Context compacted: 1 earlier message shortened, about 5.9k → 2.9k of 8.0k tokens.",
  );
  await ui.fire("send", "third");
  await client.idle();
  // With a session the failed prompt stays (the runtime checkpointed it),
  // and the reply is the compacted one, as the session now holds it.
  expect(sent[2]?.map((m) => `${m.role}:${m.content}`)).toEqual([
    "user:hello",
    "assistant:[note]",
    "user:again",
    "user:third",
  ]);
});

test("without a session a failed compacted turn takes back only its prompt", async () => {
  const sent: Message[][] = [];
  const { ui, client } = setup(reply("ok"), { run: compactingRun(sent) });
  await ui.fire("send", "hello");
  await client.idle();
  await ui.fire("send", "again");
  await client.idle();
  await ui.fire("send", "third");
  await client.idle();
  expect(sent[2]?.map((m) => `${m.role}:${m.content}`)).toEqual([
    "user:hello",
    "assistant:[note]",
    "user:third",
  ]);
});

test("esc after a compaction takes back the prompt, not the compaction note", async () => {
  let turn = 0;
  const { ui, client } = setup(reply("ok"), {
    run: (_provider, messages, options) => {
      turn++;
      if (turn === 1) return reply("ok")({ options, messages });
      return (async function* (): AsyncGenerator<AgentEvent> {
        yield {
          type: "compacted",
          messages: messages.slice(),
          elided: 1,
          tokensBefore: 6_000,
          tokensAfter: 3_000,
        };
        await new Promise((_, reject) =>
          options.signal.addEventListener("abort", () =>
            reject(new Error("aborted")),
          ),
        );
      })();
    },
  });
  await ui.fire("send", "hello");
  await client.idle();
  await ui.fire("send", "slow one");
  await until(() =>
    JSON.stringify(store(ui).messages).includes("Context compacted"),
  );
  await ui.fire("cancel");
  await client.idle();
  const text = JSON.stringify(store(ui).messages);
  expect(text).toContain("Context compacted");
  expect(text).not.toContain("slow one");
  expect(store(ui).draft).toBe("slow one");
});

test("sandbox: on, every command runs confined without a card unless a deny or ask rule decides", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-sandbox-"));
  await writeRules(root, [
    { effect: "ask", tool: "run_command", pattern: "git *" },
    { effect: "deny", tool: "run_command", pattern: "rm *" },
    { effect: "allow", tool: "run_command", pattern: "npm test *" },
  ]);
  await trustRules(root);
  const { ui, client } = setup(
    turnsAsking([
      [
        commandProposal(["bun", "test"]),
        commandProposal(["rm", "-rf", "x"]),
        // An allow rule only spares the card: outside, the command could run
        // what a sandboxed one wrote.
        commandProposal(["npm", "test"]),
      ],
      [commandProposal(["git", "push", "origin"])],
      [editProposal()],
    ]),
    {
      workspace: { root } as Workspace,
      sandbox: true,
      sandboxUnavailable: () => undefined,
    },
  );
  expect(store(ui).header.sandbox_on).toBe(true);
  await ui.fire("send", "one");
  await client.idle();
  expect(decisions).toEqual(["sandbox", false, "sandbox"]);
  expect(
    ui.batches
      .flat()
      .some(
        (w) =>
          w.path === "status" &&
          String(w.value).startsWith("sandboxed: bun test"),
      ),
  ).toBe(true);
  // An ask rule wants a person, sandbox or not; so does an edit. The card
  // says it runs outside, and lets out only its own command: no grant.
  await ui.fire("send", "two");
  await untilApproval(ui, SANDBOX_ARMED_STATUS);
  expect(store(ui).approval.body).toMatch(/^Runs outside the sandbox, as you/);
  expect(store(ui).approval.grant_hint).toBe(
    "sandbox on: session grant unavailable",
  );
  await ui.fire("dialog_session");
  expect(store(ui).status).toBe(APPROVAL_SANDBOX_SESSION_STATUS);
  expect(store(ui).approval.open).toBe(true);
  await ui.fire("dialog_cancel");
  await client.idle();
  await ui.fire("send", "three");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual(["sandbox", false, "sandbox", false, false]);
});

test("sandbox: /sandbox turns it on only where it is available, and /status says so", async () => {
  let why: string | undefined = "the command sandbox needs macOS (Seatbelt)";
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test"])],
      [commandProposal(["bun", "test"])],
    ]),
    { sandboxUnavailable: () => why, workspace: { root: "/w" } as Workspace },
  );
  await ui.fire("send", "/sandbox");
  expect(store(ui).status).toBe(
    "Sandbox is off: commands ask. Use /sandbox on to run them confined, without a card",
  );
  await ui.fire("send", "/sandbox on");
  expect(store(ui).status).toBe(
    "Sandbox unavailable: the command sandbox needs macOS (Seatbelt) · commands ask",
  );
  expect(store(ui).header.sandbox_on).toBe(false);
  await ui.fire("send", "/status");
  // (The panel wraps the line at its width.)
  expect(
    store(ui).panel.lines.some((line) =>
      line.t.startsWith("Sandbox: off (unavailable: the command sandbox"),
    ),
  ).toBe(true);
  await ui.fire("panel_close");
  // Unavailable, a command still asks.
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  why = undefined;
  await ui.fire("send", "/sandbox on");
  expect(store(ui).header.sandbox_on).toBe(true);
  expect(store(ui).status).toMatch(/^Sandbox on: /);
  await ui.fire("send", "two");
  await client.idle();
  expect(decisions).toEqual([false, "sandbox"]);
  await ui.fire("send", "/sandbox off");
  expect(store(ui).header.sandbox_on).toBe(false);
  expect(store(ui).status).toBe("Sandbox off: commands ask");
  await ui.fire("send", "/sandbox maybe");
  expect(store(ui).status).toBe(
    "Unknown sandbox setting: maybe. Use /sandbox on or /sandbox off",
  );
  expect(store(ui).header.sandbox_on).toBe(false);
});

test("sandbox: --sandbox-commands where it is unavailable starts off and says why", async () => {
  const { ui, client } = setup(turnsAsking([[commandProposal(["ls"])]]), {
    sandbox: true,
    sandboxUnavailable: () => "the workspace contains your home folder",
    workspace: { root: "/w" } as Workspace,
  });
  expect(store(ui).status).toBe(
    "Sandbox unavailable: the workspace contains your home folder · commands ask",
  );
  expect(store(ui).header.sandbox_on).toBe(false);
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
});

test("sandbox: a session grant made before runs its command confined once the sandbox is on", async () => {
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test"])],
      [commandProposal(["bun", "test", "a.ts"])],
    ]),
    {
      sandboxUnavailable: () => undefined,
      workspace: { root: "/w" } as Workspace,
    },
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "/sandbox on");
  await ui.fire("send", "two");
  await client.idle();
  expect(decisions).toEqual([true, "sandbox"]);
});

test("sandbox: a sandbox that stops being available makes the next command ask, an allow rule's too", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-sandbox-"));
  await writeRules(root, [
    { effect: "allow", tool: "run_command", pattern: "bun test *" },
  ]);
  await trustRules(root);
  let why: string | undefined;
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["bun", "test"])],
      [commandProposal(["bun", "test"])],
    ]),
    {
      sandbox: true,
      sandboxUnavailable: () => why,
      workspace: { root } as Workspace,
    },
  );
  await ui.fire("send", "one");
  await client.idle();
  expect(decisions).toEqual(["sandbox"]);
  why = "the workspace contains your home folder";
  await ui.fire("send", "two");
  await untilApproval(ui);
  expect(store(ui).header.sandbox_on).toBe(false);
  // (The card's own status follows it.)
  expect(ui.batches.flat()).toContainEqual({
    path: "status",
    value:
      "Sandbox unavailable: the workspace contains your home folder · commands ask",
  });
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual(["sandbox", false]);
});

test("a command that names a secret path always gets its card: no allow rule, grant or sandbox spares it, and the card says why", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-secret-"));
  await writeRules(root, [
    { effect: "allow", tool: "run_command", pattern: "cat *" },
  ]);
  await trustRules(root);
  const secret = {
    ...commandProposal(["cat", ".env"]),
    secretPaths: [".env"],
  } as ApprovalRequest;
  const { ui, client } = setup(
    turnsAsking([
      [commandProposal(["cat", "README.md"]), secret],
      [commandProposal(["cat", "README.md"]), secret],
    ]),
    {
      workspace: { root } as Workspace,
      sandboxUnavailable: () => undefined,
    },
  );
  // No sandbox: the allow rule runs the plain one; the secret one asks.
  await ui.fire("send", "one");
  await untilApproval(ui, NO_GRANT_ARMED_STATUS);
  expect(store(ui).approval.body).toContain(
    "names a path the workspace treats as secret: .env",
  );
  expect(
    store(ui)
      .approval.lines.map((line) => line.t)
      .join("\n"),
  ).toContain("names a path the workspace treats as secret: .env");
  await ui.fire("dialog_confirm");
  await client.idle();
  expect(decisions).toEqual([true, true]);
  // With the sandbox on, the sandbox does not spare the next card either.
  await ui.fire("send", "/sandbox on");
  await ui.fire("send", "two");
  await untilApproval(ui, NO_GRANT_ARMED_STATUS);
  expect(store(ui).approval.body).toContain("treats as secret");
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, true, "sandbox", false]);
});

test("a card that names a secret path offers no session grant and no always rule", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-secret-"));
  const secret = {
    ...commandProposal(["cat", ".env"]),
    secretPaths: [".env"],
  } as ApprovalRequest;
  const { ui, client } = setup(turnsAsking([[secret]]), {
    workspace: { root } as Workspace,
  });
  await ui.fire("send", "one");
  await untilApproval(ui, NO_GRANT_ARMED_STATUS);
  expect(store(ui).approval.grant_hint).toBe(
    "names a secret path: session grant and always rule unavailable",
  );
  await ui.fire("dialog_session");
  expect(store(ui).status).toBe(APPROVAL_SECRET_STATUS);
  expect(store(ui).approval.open).toBe(true);
  await ui.fire("dialog_always");
  expect(store(ui).status).toBe(APPROVAL_SECRET_STATUS);
  expect(store(ui).approval.open).toBe(true);
  // No rule was written for it.
  expect(
    await Bun.file(join(root, ".local-agent", "permissions.json")).exists(),
  ).toBe(false);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
});

test("a card with too many path words to check says that, and offers no grant either", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-unchecked-"));
  const unchecked = {
    ...commandProposal(["cat", "a.txt"]),
    secretPaths: ["65 path words, only 64 checked"],
  } as ApprovalRequest;
  const { ui, client } = setup(turnsAsking([[unchecked]]), {
    workspace: { root } as Workspace,
  });
  await ui.fire("send", "one");
  await untilApproval(ui, NO_GRANT_ARMED_STATUS);
  expect(store(ui).approval.body).toContain(
    "It names 65 path words, too many to check each for a link to a secret.",
  );
  expect(store(ui).approval.body).not.toContain("treats as secret");
  expect(store(ui).approval.grant_hint).toBe(
    "too many paths to check: session grant and always rule unavailable",
  );
  await ui.fire("dialog_session");
  expect(store(ui).status).toBe(APPROVAL_UNCHECKED_STATUS);
  await ui.fire("dialog_always");
  expect(store(ui).status).toBe(APPROVAL_UNCHECKED_STATUS);
  expect(store(ui).approval.open).toBe(true);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([false]);
});

test("a session grant for a command never covers a later one that names a secret path", async () => {
  const secret = {
    ...commandProposal(["bun", "test", ".env"]),
    secretPaths: [".env"],
  } as ApprovalRequest;
  // A grant made for a plain `bun test` would cover this one by its words.
  const { ui, client } = setup(
    turnsAsking([[commandProposal(["bun", "test"])], [secret]]),
    { workspace: { root: "/w" } as Workspace },
  );
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "two");
  await untilApproval(ui, NO_GRANT_ARMED_STATUS);
  await ui.fire("dialog_cancel");
  await client.idle();
  expect(decisions).toEqual([true, false]);
});

// ------------------------------------------------------------------ the goal loop

/** One script per turn, in order; past the last, a plain reply. */
const turnsOf = (...scripts: Script[]): Script => {
  let n = 0;
  return (ctx) => (scripts[n++] ?? reply("x"))(ctx);
};

/** A call of `name` with `args` that ends `status`, as the runtime reports it. */
function call(
  id: string,
  name: string,
  args: Record<string, unknown>,
  status: "succeeded" | "denied" | "failed" | "outcome_unknown" = "succeeded",
): AgentEvent[] {
  return [
    {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [{ id, function: { name, arguments: args } }],
      },
    },
    { type: "tool_requested", id, name },
    { type: "tool_start", id, name },
    { type: "tool_result", id, name, status, output: "o" },
    {
      type: "message",
      message: {
        role: "tool",
        tool_name: name,
        tool_call_id: id,
        content: JSON.stringify({ status, output: "o" }),
      },
    },
  ];
}
const wrote = (id: string) =>
  call(id, "write", { path: `${id}.ts`, content: "x" });
const ran = (id: string, argv: string[], status?: "succeeded" | "failed") =>
  call(id, "run_command", { argv }, status);

/** A goal turn: `events`, then the model's report through the host's goal_status end. */
const reporting = (
  report: GoalReport | undefined,
  events: AgentEvent[] = [],
): Script =>
  async function* (ctx) {
    for (const event of events) yield event;
    if (report) ctx.options.goal?.(report);
    yield { type: "done", inputTokens: 100, outputTokens: 20 };
    yield {
      type: "message",
      message: { role: "assistant", content: report?.summary ?? "worked" },
    };
    yield { type: "turn_complete", messages: [] };
  };
const goes = (summary: string): GoalReport => ({
  state: "continue",
  summary,
  evidence: [],
});

const notices = (ui: FakeHost) =>
  store(ui)
    .messages.filter((m: MessageView) => m.is_notice)
    .map(viewText);

test("ctrl+c between a goal's turns stops the goal before its next turn, and the next press quits", async () => {
  const { ui, client, seen } = setup(
    turnsOf(
      reporting(goes("wrote the parser"), wrote("w1")),
      reporting(goes("tests run"), ran("c1", ["bun", "test"])),
    ),
  );
  // The press lands as the first turn publishes its end: no turn runs.
  const presses: Promise<unknown>[] = [];
  ui.refuse = (writes) => {
    if (
      !presses.length &&
      writes.some(
        (write) =>
          write.path === "placeholder" && write.value === "Message the model",
      )
    )
      presses.push(ui.fire("quit_request"));
    return undefined;
  };
  await ui.fire("send", "/goal make the tests pass");
  await client.idle();
  expect(await presses[0]).toBeUndefined();
  expect(seen).toHaveLength(1);
  expect(ui.statusLog()).toContain("Goal stopping · ctrl+c again to quit");
  expect(store(ui).status).toMatch(/^Goal stopped: you cancelled it/);
  expect(store(ui).header.goal_on).toBe(false);
  expect(await ui.fire("quit_request")).toBe("quit");
});

test("esc stays live between a goal's turns, and stops the goal there", async () => {
  const { ui, client, seen } = setup(
    turnsOf(
      reporting(goes("wrote the parser"), wrote("w1")),
      reporting(goes("tests run"), ran("c1", ["bun", "test"])),
    ),
  );
  // The first turn's end keeps `running` (its esc row) for the next turn,
  // and the press lands right there.
  const kept: unknown[] = [];
  const presses: Promise<unknown>[] = [];
  ui.refuse = (writes) => {
    if (
      !presses.length &&
      writes.some(
        (write) =>
          write.path === "placeholder" && write.value === "Message the model",
      )
    ) {
      kept.push(writes.find((write) => write.path === "running")?.value);
      presses.push(ui.fire("cancel"));
    }
    return undefined;
  };
  await ui.fire("send", "/goal make the tests pass");
  await client.idle();
  await presses[0];
  expect(kept).toEqual([true]);
  expect(seen).toHaveLength(1);
  expect(store(ui).status).toMatch(/^Goal stopped: you cancelled its turn/);
  expect(store(ui).header.goal_on).toBe(false);
  // No turn followed: esc rests again.
  expect(store(ui).running).toBe(false);
});

test("esc between a turn and its queued send gives the send back", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let calls = 0;
  const { ui, client, seen } = setup(async function* () {
    calls++;
    if (calls === 1) await gate;
    yield {
      type: "message",
      message: { role: "assistant", content: `answer ${calls}` },
    };
  });
  const kept: unknown[] = [];
  const presses: Promise<unknown>[] = [];
  ui.refuse = (writes) => {
    if (
      !presses.length &&
      writes.some(
        (write) =>
          write.path === "placeholder" && write.value === "Message the model",
      )
    ) {
      kept.push(writes.find((write) => write.path === "running")?.value);
      presses.push(ui.fire("cancel"));
    }
    return undefined;
  };
  await ui.fire("send", "go");
  await ui.fire("send", "also run lint");
  release();
  await client.idle();
  await presses[0];
  expect(kept).toEqual([true]);
  expect(seen).toHaveLength(1);
  expect(store(ui).draft).toBe("also run lint");
  expect(store(ui).running).toBe(false);
});

test("ctrl+c between a goal's turns gives a queued send back instead of running it", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, client, seen } = setup(
    turnsOf(
      async function* (ctx) {
        await gate;
        yield* reporting(goes("wrote the parser"), wrote("w1"))(ctx);
      },
      reporting(goes("tests run"), ran("c1", ["bun", "test"])),
    ),
  );
  const presses: Promise<unknown>[] = [];
  ui.refuse = (writes) => {
    if (
      !presses.length &&
      writes.some(
        (write) =>
          write.path === "placeholder" && write.value === "Message the model",
      )
    )
      presses.push(ui.fire("quit_request"));
    return undefined;
  };
  await ui.fire("send", "/goal make the tests pass");
  await ui.fire("send", "also run lint");
  expect(store(ui).status).toBe("Queued: also run lint · ↑ edits it");
  release();
  await client.idle();
  expect(await presses[0]).toBeUndefined();
  expect(seen).toHaveLength(1);
  expect(store(ui).draft).toBe("also run lint");
  expect(ui.statusLog()).toContain(
    "Goal stopping · queued message back in the composer",
  );
  expect(store(ui).header.goal_on).toBe(false);
});

test("a goal stops before a turn the runtime would refuse past an unknown outcome", async () => {
  const session = fakeSession("aaa111", []);
  const items: RecoveryItem[] = [];
  const { sessions } = recoverable(session, items);
  const { ui, client, seen } = setup(
    turnsOf(
      (ctx) => {
        items.push(unknownCall("e1", "make deploy"));
        return reporting(goes("deployed, maybe"), wrote("w1"))(ctx);
      },
      reporting(goes("never asked"), wrote("w2")),
    ),
    { session, sessions },
  );
  await ui.fire("send", "/goal ship it");
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(notices(ui).at(-1)).toMatch(
    /^Goal stopped: a call's outcome is unknown/,
  );
  expect(store(ui).header.goal_on).toBe(false);
  expect(store(ui).status).toBe(
    "1 call has an unknown outcome · /recover to review before continuing",
  );
});

test("a goal runs its turns until the model reports done, and says what the evidence was", async () => {
  const options: AgentOptions[] = [];
  const { ui, client, seen } = setup(
    turnsOf(
      (ctx) => {
        options.push(ctx.options);
        return reporting(goes("wrote the parser"), wrote("w1"))(ctx);
      },
      (ctx) => {
        options.push(ctx.options);
        return reporting(goes("tests run"), ran("c1", ["bun", "test"]))(ctx);
      },
      (ctx) => {
        options.push(ctx.options);
        return reporting({
          state: "done",
          summary: "the tests pass",
          evidence: [{ kind: "command", ref: "bun test" }],
        })(ctx);
      },
    ),
  );
  await ui.fire("send", "/goal make the tests pass");
  expect(store(ui).header.goal_on).toBe(true);
  expect(store(ui).header.goal).toBe("goal 1/8");
  await client.idle();
  // Three turns, each host-written and marked; the second and third carry
  // the last status.
  expect(
    seen.map((turn) =>
      turn.messages.filter((m) => m.startsWith("user:")).at(-1),
    ),
  ).toEqual([
    "user:[goal 1/8] Goal: make the tests pass",
    "user:[goal 2/8] Continue toward the goal: make the tests pass. Last status: wrote the parser.",
    "user:[goal 3/8] Continue toward the goal: make the tests pass. Last status: tests run.",
  ]);
  // The goal's messages are the client's notices, never the user's words.
  expect(store(ui).messages.some((m: MessageView) => m.is_user)).toBe(false);
  expect(notices(ui)).toEqual([
    "[goal 1/8] Goal: make the tests pass",
    "[goal 2/8] Continue toward the goal: make the tests pass. Last status: wrote the parser.",
    "[goal 3/8] Continue toward the goal: make the tests pass. Last status: tests run.",
    "Goal reported done — evidence: bun test exited 0 · turn 3 of 8\nLast status (done): the tests pass",
  ]);
  expect(store(ui).status).toBe(
    "Goal reported done — evidence: `bun test` exited 0",
  );
  expect(store(ui).header.goal_on).toBe(false);
  expect(isBusy(ui)).toBe(false);
  // Every turn reports to the client, and refusals share one set.
  expect(options.every((o) => typeof o.goal === "function")).toBe(true);
  expect(options[0]?.refused).toBeInstanceOf(Set);
  expect(options[1]?.refused).toBe(options[0]?.refused);
  expect(options[2]?.refused).toBe(options[0]?.refused);
  // A turn after the goal reports to nothing and refuses afresh.
  const after: AgentOptions[] = [];
  const plain = setup((ctx) => {
    after.push(ctx.options);
    return reply("x")(ctx);
  });
  await plain.ui.fire("send", "hi");
  await plain.client.idle();
  expect(after[0]?.goal).toBeUndefined();
  expect(after[0]?.refused).toBeUndefined();
});

test("done names no evidence unless a command the goal ran succeeded", async () => {
  const { ui, client } = setup(
    turnsOf(
      reporting(goes("ran it"), ran("c1", ["bun", "test"], "failed")),
      reporting({
        state: "done",
        summary: "green",
        evidence: [{ kind: "command", ref: "bun test" }],
      }),
    ),
  );
  await ui.fire("send", "/goal go");
  await client.idle();
  expect(store(ui).status).toBe("Goal reported done without evidence");
});

test("blocked ends the goal with what the model needs", async () => {
  const { ui, client, seen } = setup(
    turnsOf(
      reporting(
        { state: "blocked", summary: "need the API key", evidence: [] },
        wrote("w1"),
      ),
    ),
  );
  await ui.fire("send", "/goal deploy");
  await client.idle();
  expect(seen).toHaveLength(1);
  expect(store(ui).status).toBe("Goal blocked: need the API key");
});

test("two turns that change nothing stop the goal; so does its turn budget", async () => {
  const idle = setup(
    turnsOf(
      reporting(goes("reading"), call("r1", "read", { path: "a" })),
      reporting(goes("still reading"), call("r2", "read", { path: "b" })),
    ),
  );
  await idle.ui.fire("send", "/goal understand it");
  await idle.client.idle();
  expect(idle.seen).toHaveLength(2);
  expect(store(idle.ui).status).toBe(
    "Goal stopped: two turns in a row changed nothing (no edit, write, command or MCP call succeeded)",
  );
  const budget = setup(
    turnsOf(
      reporting(goes("a"), wrote("w1")),
      reporting(goes("b"), wrote("w2")),
      reporting(goes("c"), wrote("w3")),
    ),
  );
  await budget.ui.fire("send", "/goal ship --turns 2");
  await budget.client.idle();
  expect(budget.seen).toHaveLength(2);
  expect(store(budget.ui).status).toBe("Goal stopped: its 2 turns are used");
});

test("the step limit is a turn boundary; a failed turn or an uncertain outcome stops the goal", async () => {
  const limited = setup(
    turnsOf(
      async function* () {
        for (const event of wrote("w1")) yield event;
        throw new StepLimitError("Agent reached the 20-step limit");
      },
      async function* () {
        for (const event of wrote("w2")) yield event;
        throw new Error("provider down");
      },
    ),
  );
  await limited.ui.fire("send", "/goal big refactor");
  await limited.client.idle();
  expect(limited.seen).toHaveLength(2);
  expect(store(limited.ui).status).toBe(
    "Goal stopped: its turn failed (provider down)",
  );
  // In a goal the step limit reads as a notice; only the failure is an error.
  const errors = store(limited.ui)
    .messages.filter((m: MessageView) => m.is_error)
    .map(viewText);
  expect(errors).toEqual(["provider down"]);
  expect(notices(limited.ui)).toContain("Agent reached the 20-step limit");
  const uncertain = setup(
    turnsOf(async function* () {
      for (const event of call(
        "c1",
        "run_command",
        { argv: ["make"] },
        "outcome_unknown",
      ))
        yield event;
      throw new Error(
        "Tool outcome is uncertain; review the workspace before continuing",
      );
    }),
  );
  await uncertain.ui.fire("send", "/goal build it");
  await uncertain.client.idle();
  expect(uncertain.seen).toHaveLength(1);
  expect(store(uncertain.ui).status).toBe(
    "Goal stopped: an action's outcome is uncertain; review the workspace before going on",
  );
});

/** A turn that waits for its abort, as a long generation would. */
const waiting: Script = (ctx) =>
  quiet(
    () =>
      new Promise<void>((_, reject) =>
        ctx.options.signal.addEventListener("abort", () =>
          reject(ctx.options.signal.reason),
        ),
      ),
  )(ctx);

test("esc cancels the goal's turn and the goal; /goal stop does too, and is never queued", async () => {
  const esc = setup(
    turnsOf(async function* (ctx) {
      yield { type: "text", text: "half way there" };
      yield* waiting(ctx);
    }),
  );
  await esc.ui.fire("send", "/goal long job");
  await until(() => esc.seen.length === 1);
  await esc.ui.fire("cancel");
  await esc.client.idle();
  expect(esc.seen).toHaveLength(1);
  expect(store(esc.ui).status).toBe("Goal stopped: you cancelled its turn");
  // The goal's message never lands in the draft, so its notice stays, and
  // the reply under it: what the goal was and how far it got.
  expect(store(esc.ui).draft).toBe("");
  expect(store(esc.ui).messages.map(viewText)).toEqual([
    "[goal 1/8] Goal: long job",
    "half way there",
    "Goal stopped: you cancelled its turn · turn 1 of 8",
  ]);
  const stop = setup(turnsOf(waiting));
  await stop.ui.fire("send", "/goal long job");
  await until(() => stop.seen.length === 1);
  await stop.ui.fire("send", "/goal");
  expect(notices(stop.ui).at(-1)).toContain("Turn 1 of 8");
  await stop.ui.fire("send", "/goal stop");
  await stop.client.idle();
  expect(stop.seen).toHaveLength(1);
  expect(store(stop.ui).status).toBe("Goal stopped: you ended it (/goal stop)");
  expect(store(stop.ui).header.goal_on).toBe(false);
  await stop.ui.fire("send", "/goal stop");
  expect(store(stop.ui).status).toBe(
    "No goal is running. /goal OBJECTIVE starts one",
  );
});

test("esc in a goal turn's tail, where no cancel reaches a turn, still ends the goal before its next turn", async () => {
  const { ui, client, seen } = setup(
    turnsOf(
      reporting(goes("first part"), wrote("w1")),
      reporting(goes("second part"), wrote("w2")),
    ),
  );
  // Esc lands while the first turn's final publish (busy false) is in
  // flight: its turn is over, the goal has said it goes on.
  let pressed = false;
  ui.refuse = (writes) => {
    if (
      !pressed &&
      writes.some(
        (w) => w.path === "placeholder" && w.value === "Message the model",
      )
    ) {
      pressed = true;
      void ui.fire("cancel");
    }
    return undefined;
  };
  await ui.fire("send", "/goal two parts");
  await client.idle();
  expect(pressed).toBe(true);
  expect(seen).toHaveLength(1);
  expect(store(ui).status).toBe("Goal stopped: you cancelled its turn");
  expect(store(ui).header.goal_on).toBe(false);
  expect(notices(ui).at(-1)).toBe(
    "Goal stopped: you cancelled its turn · turn 1 of 8\nLast status (continue): first part",
  );
  // A send right after it: the goal still ends at its first turn, and the
  // send runs after its end, which never overwrites the send's turn.
  const late = setup(
    turnsOf(reporting(goes("first part"), wrote("w1")), reply("sure")),
  );
  let again = false;
  late.ui.refuse = (writes) => {
    if (
      !again &&
      writes.some(
        (w) => w.path === "placeholder" && w.value === "Message the model",
      )
    ) {
      again = true;
      void late.ui.fire("cancel");
      void late.ui.fire("send", "and then this");
    }
    return undefined;
  };
  await late.ui.fire("send", "/goal two parts");
  await until(() => late.seen.length === 2);
  await late.client.idle();
  expect(late.seen[1]?.messages.at(-1)).toBe("user:and then this");
  expect(notices(late.ui)).toEqual([
    "[goal 1/8] Goal: two parts",
    "Goal stopped: you cancelled its turn · turn 1 of 8\nLast status (continue): first part",
  ]);
  expect(store(late.ui).status).not.toContain("Goal");
  expect(store(late.ui).header.goal_on).toBe(false);
  expect(isBusy(late.ui)).toBe(false);
});

test("a typed message that starts like a goal's is still the user's", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, client, seen } = setup(
    turnsOf(
      async function* (ctx) {
        await gate;
        yield* reporting(goes("first"), wrote("w1"))(ctx);
      },
      // The typed turn: no report, nothing changed.
      reporting(undefined),
      reporting({ state: "done", summary: "all of it", evidence: [] }),
    ),
  );
  await ui.fire("send", "/goal long job");
  await until(() => seen.length === 1);
  const typed = "[goal 2/8] Continue toward the goal: mine\nsecond line";
  await ui.fire("send", typed);
  release();
  await client.idle();
  expect(seen).toHaveLength(3);
  // Shown as the user's, whole.
  const mine = store(ui).messages.filter((m: MessageView) => m.is_user);
  expect(mine.map(viewText).join("\n")).toContain("second line");
  // Not one of the goal's turns: its missing report asks for nothing, and
  // the goal's next turn is turn 2.
  expect(seen[2]?.messages.at(-1)).toBe(
    "user:[goal 2/8] Continue toward the goal: long job. Last status: first.",
  );
});

test("the goal stops when the conversation could not take another whole turn", async () => {
  const session = {
    store: {},
    state: {
      messages: Array.from({ length: 9_334 }, (_, i) => ({
        role: i % 2 ? "assistant" : "user",
        content: `m${i}`,
      })),
    },
  } as unknown as SessionContext;
  const { ui, client, seen } = setup(
    turnsOf(
      reporting(goes("a"), wrote("w1")),
      reporting(goes("b"), wrote("w2")),
      reporting(goes("c"), wrote("w3")),
    ),
    { session },
  );
  await ui.fire("send", "/goal fill it");
  await client.idle();
  // Four messages a turn: 661 left after the first, 657 after the second.
  expect(seen).toHaveLength(2);
  expect(store(ui).status).toBe(
    "Goal stopped: the conversation is nearly full; /sessions new starts over",
  );
});

test("a provider that streams but reports no usage leaves the goal's tokens unmeasured", async () => {
  const { ui, client, seen } = setup(
    turnsOf(async function* (ctx) {
      yield { type: "text", text: "working" };
      for (const event of wrote("w1")) yield event;
      ctx.options.goal?.(goes("half"));
      yield { type: "done", inputTokens: 0, outputTokens: 0 };
      yield { type: "turn_complete", messages: [] };
    }, waiting),
  );
  await ui.fire("send", "/goal measure it");
  await until(() => seen.length === 2);
  await ui.fire("send", "/goal");
  expect(notices(ui).at(-1)).toContain("output tokens unmeasured");
  await ui.fire("cancel");
  await client.idle();
});

test("with a question open, /goal stop is never its answer, and esc on it stops the goal", async () => {
  const answers: Answer[] = [];
  const open = setup(
    turnsOf(asking(STORE_QUESTION, answers, Promise.resolve())),
  );
  await open.ui.fire("send", "/goal choose a store");
  await untilQuestion(open.ui);
  await open.ui.fire("send", "/goal stop");
  await open.client.idle();
  expect(answers).toEqual([]);
  expect(store(open.ui).status).toBe("Goal stopped: you ended it (/goal stop)");
  const skipped = setup(
    turnsOf(asking(STORE_QUESTION, answers, Promise.resolve())),
  );
  await skipped.ui.fire("send", "/goal choose a store");
  await untilQuestion(skipped.ui);
  await skipped.ui.fire("question_skip");
  await skipped.client.idle();
  expect(answers).toEqual([{ kind: "declined" }]);
  expect(skipped.seen).toHaveLength(1);
  expect(store(skipped.ui).status).toBe(
    "Goal stopped: you skipped its question",
  );
});

test("a message typed during a goal runs next, and the goal goes on after it", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client, seen } = setup(
    turnsOf(
      async function* (ctx) {
        await gate;
        yield* reporting(goes("first part"), wrote("w1"))(ctx);
      },
      reporting(undefined, wrote("w2")),
      reporting({ state: "done", summary: "all of it", evidence: [] }),
    ),
  );
  await ui.fire("send", "/goal two parts");
  await ui.fire("send", "and update the docs");
  expect(store(ui).status).toContain("Queued");
  release();
  await client.idle();
  const last = (n: number) =>
    seen[n]?.messages.filter((m) => m.startsWith("user:")).at(-1);
  expect(last(1)).toBe("user:and update the docs");
  // The typed turn is not one of the goal's: the next is its second.
  expect(last(2)).toBe(
    "user:[goal 2/8] Continue toward the goal: two parts. Last status: first part.",
  );
  expect(store(ui).status).toBe("Goal reported done without evidence");
});

test("/goal starts only in build, idle and alone, and says how", async () => {
  const plan = setup(reply("x"), { intent: "plan" });
  await plan.ui.fire("send", "/goal fix it");
  expect(plan.seen).toEqual([]);
  expect(store(plan.ui).status).toBe(
    "A goal runs in build mode, not plan: /mode build first",
  );
  const bad = setup(reply("x"));
  store(bad.ui).draft = "/goal fix it --turns 99";
  await bad.ui.fire("send", "/goal fix it --turns 99");
  expect(bad.seen).toEqual([]);
  expect(store(bad.ui).status).toBe(
    "--turns takes one whole number from 1 to 50",
  );
  // The line stays to fix.
  expect(store(bad.ui).draft).toBe("/goal fix it --turns 99");
  await bad.ui.fire("send", "/goal");
  expect(store(bad.ui).status).toBe(
    "No goal is running. /goal OBJECTIVE starts one",
  );
  const twice = setup(turnsOf(waiting));
  await twice.ui.fire("send", "/goal one");
  await until(() => twice.seen.length === 1);
  await twice.ui.fire("send", "/goal two");
  expect(store(twice.ui).status).toBe(
    "A goal is running: /goal shows it, /goal stop ends it",
  );
  await twice.ui.fire("cancel");
  await twice.client.idle();
  expect(twice.seen).toHaveLength(1);
});

test("the goal adds up the turns' output tokens and stops at its budget", async () => {
  const spending = (tokens: number, id: string): Script =>
    async function* (ctx) {
      for (const event of wrote(id)) yield event;
      ctx.options.goal?.(goes(`spent ${tokens}`));
      yield { type: "text", text: "working" };
      yield { type: "done", inputTokens: 100, outputTokens: tokens };
      yield { type: "message", message: { role: "assistant", content: "ok" } };
    };
  const { ui, client, seen } = setup(
    turnsOf(spending(150_000, "w1"), spending(60_000, "w2"), spending(1, "w3")),
  );
  await ui.fire("send", "/goal write the book");
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(store(ui).status).toBe(
    "Goal stopped: its 200k output tokens are spent",
  );
});

// ------------------------------------------------- 0.46: client fixes

test("a context warning is the first status, and /status says it again", async () => {
  const { ui } = setup(reply("x"), {
    provider: { ...provider, context_warning: MLX_WARNING },
  });
  expect(store(ui).status).toBe(
    `Warning: ${MLX_WARNING} · /status shows it again`,
  );
  await ui.fire("send", "/status");
  // The panel wraps a long line, its continuations marked "↪ ".
  expect(
    store(ui)
      .panel.lines.map((line) => line.t.replace(/^↪ /, ""))
      .join(""),
  ).toContain(`Warning: ${MLX_WARNING}`);
  // Without one the first status is the idle line.
  expect(store(setup(reply("x")).ui).status).not.toMatch(/^Warning/);
});

test("fullscreen mouse starts on, as the store does; inline has none", async () => {
  const { ui } = setup(reply("x"));
  expect(store(ui).mouse_on).toBe(true);
  await ui.fire("send", "/mouse");
  expect(store(ui).status).toBe("Mouse is on. Use /mouse on or /mouse off");
  const flat = setup(reply("x"), inline);
  expect(store(flat.ui).mouse_on).toBe(false);
  await flat.ui.fire("send", "/status");
  expect(store(flat.ui).panel.lines.map((line) => line.t)).toContain(
    "Mouse: n/a (inline)",
  );
});

test("a line that starts with a path is a message; a command name is a command in any case", async () => {
  const { ui, client, seen } = setup(reply("ok"));
  await ui.fire("send", "/tmp/x.log is empty, why?");
  await client.idle();
  expect(seen.at(-1)?.messages.at(-1)).toBe("user:/tmp/x.log is empty, why?");
  const runs = seen.length;
  await ui.fire("send", "/HELP");
  expect(store(ui).help.open).toBe(true);
  await ui.fire("help_close");
  await ui.fire("send", "/Status");
  expect(store(ui).panel.title).toBe("Status");
  await ui.fire("panel_close");
  // A lone slash, and an unknown name, are still commands: never sent.
  await ui.fire("send", "/");
  await ui.fire("send", "/nope now");
  expect(store(ui).status).toBe("Unknown command: /nope now. Use /help");
  // A typo is refused, never sent; so is one slash then a word.
  await ui.fire("send", "/model:foo");
  expect(store(ui).status).toBe("Unknown command: /model:foo. Use /help");
  await ui.fire("send", "/etc is wrong");
  expect(store(ui).status).toBe("Unknown command: /etc is wrong. Use /help");
  expect(seen.length).toBe(runs);
});

test("ctrl+c mid-turn cancels it and gives the queue back; the next presses clear it, then quit", async () => {
  let aborted = 0;
  const { ui, client, seen } = setup(
    quiet(async ({ options }) => {
      await new Promise((_, reject) =>
        options.signal.addEventListener("abort", () => {
          aborted++;
          reject(new Error("aborted"));
        }),
      );
    }),
    // A saved session keeps the prompt: nothing comes back to the draft.
    { session: fakeSession("abc123", []) },
  );
  await ui.fire("send", "go");
  await ui.fire("send", "queued");
  expect(await ui.fire("quit_request")).toBeUndefined();
  await client.idle();
  expect(aborted).toBe(1);
  // The queued message was never sent: it is back in the composer.
  expect(seen).toHaveLength(1);
  expect(ui.statusLog()).toContain(
    "Turn cancelled · queued message back in the composer",
  );
  expect(store(ui).status).toBe(
    "Cancelled · queued message back in the composer",
  );
  expect(store(ui).draft).toBe("queued");
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(store(ui).draft).toBe("");
  expect(await ui.fire("quit_request")).toBe("quit");
});

test("a dialog closed while `!` loads the rules saves no rule and keeps its deny", async () => {
  const root = await mkdtemp(join(tmpdir(), "la-always-race-"));
  const { ui, client } = setup(
    turnsAsking([[commandProposal(["bun", "test", "a.test.ts"])]]),
    { workspace: { root } as Workspace },
  );
  await ui.fire("send", "one");
  await until(() => store(ui).status === APPROVAL_STATUS);
  const always = ui.fire("dialog_always");
  await ui.fire("dialog_cancel");
  // Without the guard this rejected (the dialog's decide was gone), which
  // the host turns into the end of the session.
  await expect(always).resolves.toBeUndefined();
  await client.idle();
  expect(decisions).toEqual([false]);
  expect((await loadPermissionFile(root)).rules).toEqual([]);
});

test("an approval cancelled while the rules load is denied without a card", async () => {
  const { ui, client } = setup(async function* ({ options }) {
    const cancelled = new AbortController();
    // approve awaits the rules first; the abort lands in that wait, before
    // the dialog's listener exists.
    const asked = options.approve(editProposal(), cancelled.signal);
    cancelled.abort();
    decisions.push(await asked);
    yield* [] as AgentEvent[];
  });
  await ui.fire("send", "edit");
  await client.idle();
  expect(decisions).toEqual([false]);
  expect(store(ui).approval.open).toBe(false);
});

test("a handler failure is a status line, not the end of the session", async () => {
  const { ui, client } = setup(reply("x"));
  await client.handlerFailed(
    "dialog_always",
    new Error("EACCES: permission denied"),
  );
  expect(store(ui).status).toBe(
    "Action dialog_always failed: EACCES: permission denied",
  );
  await client.handlerFailed("send", "plain\nstring");
  expect(store(ui).status).toBe("Action send failed: plain string");
});

test("/model with an explicit size to an MLX model keeps the size and warns; Chalupa's never does", async () => {
  const GIB = 1024 ** 3;
  const admitModel = async (candidate: Provider) =>
    ({
      ...validated(candidate.model),
      sizeBytes: 3.3 * GIB,
      nativeContext: 262_144,
      kvBytesPerToken: 32_768,
      mlx: true,
    }) as OllamaAdmission;
  const providers: Provider[] = [];
  const run: ConstructorParameters<typeof TuimarkClient>[0]["run"] = (
    provider,
    messages,
    options,
  ) => {
    providers.push(provider);
    return reply("ok")({ options, messages });
  };
  const local = setup(reply("ok"), {
    admitModel,
    run,
    provider: { ...provider, context_size: 16_384, context_explicit: true },
    totalMemory: () => 16 * GIB,
  });
  await local.ui.fire("send", "/model qwen3.5:4b");
  expect(store(local.ui).status).toBe(
    `Model changed to qwen3.5:4b · ${MLX_WARNING}`,
  );
  await local.ui.fire("send", "hi");
  await local.client.idle();
  expect(providers.at(-1)).toMatchObject({
    context_size: 16_384,
    context_plan: expect.stringMatching(
      /^set by --context or LOCAL_AGENT_CONTEXT; .*MLX runner ignores num_ctx/,
    ),
    context_warning: MLX_WARNING,
  });

  const remote = setup(reply("ok"), {
    admitModel,
    run,
    provider: {
      ...provider,
      type: "ollama_remote",
      context_warning: "Chalupa set a context of 8192 tokens",
    } as Provider,
  });
  await remote.ui.fire("send", "/model qwen3.5:4b");
  expect(store(remote.ui).status).toBe("Model changed to qwen3.5:4b");
  await remote.ui.fire("send", "hi");
  await remote.client.idle();
  expect(providers.at(-1)?.context_warning).toBe(
    "Chalupa set a context of 8192 tokens",
  );
});

test("a ctrl+c while the turn winds down quits, after esc too, and even if the turn ignores the abort", async () => {
  // The turn never ends: it ignores its signal.
  const { ui } = setup(quiet(() => new Promise(() => {})));
  await ui.fire("send", "go");
  await ui.fire("cancel");
  expect(await ui.fire("quit_request")).toBe("quit");

  const second = setup(quiet(() => new Promise(() => {})));
  await second.ui.fire("send", "go");
  expect(await second.ui.fire("quit_request")).toBeUndefined();
  expect(await second.ui.fire("quit_request")).toBe("quit");
});

test("a draft typed while a ctrl+c cancel winds down: no quit hint, the next press clears it", async () => {
  const { ui, client } = setup(
    quiet(
      ({ options }) =>
        new Promise((_, reject) =>
          options.signal.addEventListener("abort", () =>
            setTimeout(() => reject(new Error("aborted")), 20),
          ),
        ),
    ),
    { session: fakeSession("abc123", []) },
  );
  await ui.fire("send", "go");
  expect(await ui.fire("quit_request")).toBeUndefined();
  store(ui).draft = "x";
  await ui.fire("typed", "x");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled");
  expect(await ui.fire("quit_request")).toBeUndefined();
  expect(store(ui).status).toBe("Draft cleared · ctrl+c again to quit");
  expect(await ui.fire("quit_request")).toBe("quit");
});

// ------------------------------------------------------------------------ copy

/** A clipboard that records what it was given, and answers `result`. */
function fakeClipboard(result: CopyResult = { ok: true, tool: "fakeclip" }): {
  copy: (text: string) => Promise<CopyResult>;
  copied: string[];
} {
  const copied: string[] = [];
  return {
    copied,
    copy: async (text) => {
      copied.push(text);
      return result;
    },
  };
}

const FENCED_REPLY = [
  "Here are two snippets.",
  "",
  "```ts",
  "const first = 1;",
  "```",
  "",
  "And the second:",
  "",
  "```sh",
  "\tbun test",
  "```",
  "",
  "Done.",
].join("\n");

test("/copy puts the last reply's markdown on the clipboard and says what it used", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(reply(FENCED_REPLY), {
    copy: clip.copy,
  });
  await ui.fire("send", "snippets please");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(clip.copied).toEqual([FENCED_REPLY]);
  expect(store(ui).status).toBe(
    `Copied the last reply (${FENCED_REPLY.length} chars) with fakeclip`,
  );
  expect(store(ui).draft).toBe("");
  expect(client.writeErrors).toEqual([]);
});

test("/copy writes the count with a thousands separator", async () => {
  const clip = fakeClipboard();
  const long = "x".repeat(1234);
  const { ui, client } = setup(reply(long), { copy: clip.copy });
  await ui.fire("send", "long");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe(
    "Copied the last reply (1,234 chars) with fakeclip",
  );
});

test("/copy code copies the last fenced block of the last reply, tabs and all", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(reply(FENCED_REPLY), { copy: clip.copy });
  await ui.fire("send", "snippets please");
  await client.idle();
  await ui.fire("send", "/copy code");
  expect(clip.copied).toEqual(["\tbun test"]);
  expect(store(ui).status).toBe(
    "Copied the last code block (9 chars) with fakeclip",
  );
});

test("/copy says so when there is no reply yet, or no code block in it", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(reply("Just words, no code."), {
    copy: clip.copy,
  });
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe("No reply to copy yet");
  await ui.fire("send", "/copy code");
  expect(store(ui).status).toBe("No reply to copy yet");
  await ui.fire("send", "talk");
  await client.idle();
  await ui.fire("send", "/copy code");
  expect(store(ui).status).toBe("No code block in the last reply");
  // Nothing reached the clipboard in any of them.
  expect(clip.copied).toEqual([]);
});

test("/copy with no clipboard tool says how to select by hand, with the terminal's modifier", async () => {
  const none = fakeClipboard({ ok: false, reason: "none" });
  const plain = setup(reply("hello"), { copy: none.copy });
  await plain.ui.fire("send", "hi");
  await plain.client.idle();
  await plain.ui.fire("send", "/copy");
  expect(store(plain.ui).status).toBe(
    "No clipboard tool found (pbcopy, wl-copy, xclip, xsel): select text with shift-drag",
  );
  // iTerm2 selects with option, Terminal.app with fn.
  const iterm = setup(reply("hello"), {
    copy: none.copy,
    termProgram: "iTerm.app",
  });
  await iterm.ui.fire("send", "hi");
  await iterm.client.idle();
  await iterm.ui.fire("send", "/copy");
  expect(store(iterm.ui).status).toContain("select text with option-drag");
  // With mouse reporting off a plain drag selects.
  await iterm.ui.fire("send", "/mouse off");
  await iterm.ui.fire("send", "/copy");
  expect(store(iterm.ui).status).toContain("select text with drag");
  // No clipboard at all (the option absent) is the same answer.
  const bare = setup(reply("hello"));
  await bare.ui.fire("send", "hi");
  await bare.client.idle();
  await bare.ui.fire("send", "/copy");
  expect(store(bare.ui).status).toContain("No clipboard tool found");
});

test("a clipboard tool that failed is named, not hidden behind 'no tool'", async () => {
  const failing = fakeClipboard({
    ok: false,
    reason: "failed",
    tool: "xclip",
    detail: "exited with code 1",
  });
  const { ui, client } = setup(reply("hello"), { copy: failing.copy });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe("Copy failed: xclip exited with code 1");
});

/** A script whose "seed" turn answers `answer`; any other turn runs `rest`. */
const seededThen = (answer: string, rest: Script): Script =>
  async function* (ctx) {
    if (ctx.messages.at(-1)?.content === "seed") {
      yield {
        type: "message",
        message: { role: "assistant", content: answer },
      };
      return;
    }
    yield* rest(ctx);
  };

test("ctrl+y copies the last reply; it is ignored while an approval is open", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(seededThen("the earlier answer", withApproval), {
    copy: clip.copy,
  });
  await ui.fire("send", "seed");
  await client.idle();
  await ui.fire("copy_reply");
  expect(clip.copied).toEqual(["the earlier answer"]);
  expect(store(ui).status).toBe(
    "Copied the last reply (18 chars) with fakeclip",
  );
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("copy_reply");
  // The approval keeps its status line and its keys: nothing was copied.
  expect(clip.copied).toEqual(["the earlier answer"]);
  expect(store(ui).status).toBe(APPROVAL_STATUS);
  await ui.fire("dialog_cancel");
  await client.idle();
});

test("/copy is not refused while a turn runs: it copies the last finished reply", async () => {
  const clip = fakeClipboard();
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(
    seededThen(
      "```sh\nls\n```",
      quiet(async () => {
        await gate;
      }),
    ),
    { copy: clip.copy },
  );
  await ui.fire("send", "seed");
  await client.idle();
  await ui.fire("send", "go");
  await ui.fire("send", "/copy code");
  // The reply being written is not in the history yet: the status says which one it copied.
  expect(store(ui).status).toBe(
    "Copied the last code block of the last finished reply (2 chars) with fakeclip; the one being written is not done yet",
  );
  expect(clip.copied).toEqual(["ls"]);
  // Another command is still refused while busy.
  await ui.fire("send", "/status");
  expect(store(ui).status).toBe(BUSY_STATUS);
  release();
  await client.idle();
});

test("a turn that ends while the tool runs does not make the copy the newest reply", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const copied: string[] = [];
  const { ui, client } = setup(
    seededThen(
      "the earlier answer",
      quiet(async () => {
        await gate;
      }),
    ),
    {
      // The turn ends while the copy is on its way.
      copy: async (text) => {
        copied.push(text);
        release();
        for (let wait = 0; wait < 100 && isBusy(ui); wait++) await Bun.sleep(5);
        return { ok: true, tool: "slowclip" };
      },
    },
  );
  await ui.fire("send", "seed");
  await client.idle();
  await ui.fire("send", "go");
  await ui.fire("copy_reply");
  await client.idle();
  expect(isBusy(ui)).toBe(false);
  expect(copied).toEqual(["the earlier answer"]);
  expect(store(ui).status).toBe(
    "Copied the last finished reply (18 chars) with slowclip; the one being written is not done yet",
  );
});

// -------------------------------------------------------- the selection hint

test("the welcome and the help name the modifier of the terminal, from TERM_PROGRAM", () => {
  const cases: [string | undefined, string][] = [
    [undefined, "shift-drag"],
    ["ghostty", "shift-drag"],
    ["WezTerm", "shift-drag"],
    ["iTerm.app", "option-drag"],
    ["Apple_Terminal", "fn-drag"],
  ];
  for (const [termProgram, hint] of cases) {
    const { ui } = setup(reply("x"), { termProgram });
    expect(store(ui).select_tip).toBe(`${hint} selects text`);
    expect(store(ui).help.select_key).toBe(hint);
    expect(store(ui).help.select_desc).toBe(
      "select text (mouse reporting is on)",
    );
  }
});

test("/mouse off turns the hint into a plain drag, and on brings the modifier back", async () => {
  const { ui } = setup(reply("x"), { termProgram: "iTerm.app" });
  await ui.fire("send", "/mouse off");
  expect(store(ui).mouse_on).toBe(false);
  expect(store(ui).select_tip).toBe("drag selects text");
  expect(store(ui).help.select_key).toBe("drag");
  expect(store(ui).help.select_desc).toBe("select text");
  await ui.fire("send", "/mouse on");
  expect(store(ui).select_tip).toBe("option-drag selects text");
  expect(store(ui).help.select_key).toBe("option-drag");
});

test("inline has no mouse reporting: a plain drag selects", () => {
  const { ui } = setup(reply("x"), {
    presentation: "inline",
    termProgram: "iTerm.app",
  });
  expect(store(ui).select_tip).toBe("drag selects text");
  expect(store(ui).help.select_key).toBe("drag");
});

test("mouse reporting stays on by default in fullscreen, off inline", () => {
  expect(store(setup(reply("x")).ui).mouse_on).toBe(true);
  expect(store(setup(reply("x"), { presentation: "inline" }).ui).mouse_on).toBe(
    false,
  );
});

test("a tool request takes the head's place even when no message came first", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* () {
    yield { type: "text", text: "Looking. " };
    yield { type: "tool_requested", id: "r1", name: "read" };
    yield { type: "tool_start", id: "r1", name: "read" };
    await gate;
  });
  await ui.fire("send", "go");
  await until(() => store(ui).live.is_tool);
  await client.flush();
  expect(store(ui).head.on).toBe(false);
  release();
  await client.idle();
});

test("a cancelled or failed turn leaves no head behind", async () => {
  const waiting = setup(
    quiet(async ({ options }) => {
      await new Promise((_, reject) =>
        options.signal.addEventListener("abort", () =>
          reject(new Error("aborted")),
        ),
      );
    }),
  );
  await waiting.ui.fire("send", "go");
  expect(store(waiting.ui).head.on).toBe(true);
  await waiting.ui.fire("cancel");
  await waiting.client.idle();
  expect(store(waiting.ui).head.on).toBe(false);
  expect(store(waiting.ui).head.spin).toBe(false);
  expect(store(waiting.ui).status).toBe("Cancelled");

  const failing = setup(
    quiet(async () => {
      throw new Error("boom");
    }),
  );
  await failing.ui.fire("send", "go");
  await failing.client.idle();
  expect(store(failing.ui).head.on).toBe(false);
  expect(store(failing.ui).status).toBe("boom");
  // Nothing late writes it back on.
  await new Promise((r) => setTimeout(r, 30));
  expect(store(failing.ui).head.on).toBe(false);
});

test("a verdict stays on the status line until a tool result gives the ambient status back", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* (ctx) {
    yield* withApproval(ctx);
    await gate;
  });
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("dialog_confirm");
  // The tool ran and its result arrived: the bar is the ambient one again.
  await until(() => store(ui).status === "build");
  expect(statusWrites(ui)).toContain("Approved");
  release();
  await client.idle();
});

test("/copy takes the newest reply, skipping a step that only called a tool", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(
    async function* (ctx) {
      const last = ctx.messages.at(-1)?.content;
      yield {
        type: "message",
        message: {
          role: "assistant",
          content: last === "one" ? "first answer" : "second answer",
        },
      };
      // A step that only calls a tool has no content to copy.
      yield {
        type: "message",
        message: {
          role: "assistant",
          content: "",
          tool_calls: [
            { id: "c9", function: { name: "read", arguments: { path: "a" } } },
          ],
        },
      };
    },
    { copy: clip.copy },
  );
  await ui.fire("send", "one");
  await client.idle();
  await ui.fire("send", "two");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(clip.copied).toEqual(["second answer"]);
});

test("the approval card stands still while the dialog is open: the head's glyph is the one indication", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  expect(store(ui).live.card).toMatchObject({
    status: "waiting for approval",
    running: false,
    mark: "?",
  });
  expect(store(ui).head).toMatchObject({ still: true, spin: false });
  await ui.fire("dialog_cancel");
  await client.idle();
});

test("a send queued while the turn runs stays a fact of the status bar", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* (ctx) {
    // Only the first turn asks; the queued one that follows just ends.
    if (ctx.messages.at(-1)?.content !== "edit") return;
    yield* withApproval(ctx);
    await gate;
  });
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("send", "and then the tests");
  // The approval owns the bar; once it is answered the verdict and then the
  // ambient status come back, and the ambient one still says a send waits.
  await ui.fire("dialog_confirm");
  await until(() => store(ui).status === "build · 1 queued");
  release();
  await client.idle();
});

test("the copy count is characters, not UTF-16 units", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(reply("😀😀😀"), { copy: clip.copy });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe(
    "Copied the last reply (3 chars) with fakeclip",
  );
});

test("/copy code: an empty fence is no code, the last block with text is copied", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(
    seededThen("```sh\nls -la\n```\n\n```\n\n```", reply("unused")),
    { copy: clip.copy },
  );
  await ui.fire("send", "seed");
  await client.idle();
  await ui.fire("send", "/copy code");
  expect(clip.copied).toEqual(["ls -la"]);
  const empty = fakeClipboard();
  const only = setup(seededThen("Words.\n\n```\n```\n", reply("x")), {
    copy: empty.copy,
  });
  await only.ui.fire("send", "seed");
  await only.client.idle();
  await only.ui.fire("send", "/copy code");
  expect(store(only.ui).status).toBe("No code block in the last reply");
  expect(empty.copied).toEqual([]);
});

test("what reaches the clipboard has no control bytes: an ESC sequence in a fence is gone, a tab stays", async () => {
  const clip = fakeClipboard();
  const reply1 =
    "Run:\n\n```sh\n\x1b[31mred\x1b[0m\tbell\x07\n```\n\x1b]0;title\x07done";
  const { ui, client } = setup(seededThen(reply1, reply("x")), {
    copy: clip.copy,
  });
  await ui.fire("send", "seed");
  await client.idle();
  await ui.fire("send", "/copy code");
  await ui.fire("send", "/copy");
  expect(clip.copied).toEqual([
    "red\tbell",
    "Run:\n\n```sh\nred\tbell\n```\ndone",
  ]);
});

test("the count is characters a reader sees: one family emoji is one", async () => {
  const clip = fakeClipboard();
  const { ui, client } = setup(reply("a👨‍👩‍👧e\u0301"), { copy: clip.copy });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe(
    "Copied the last reply (3 chars) with fakeclip",
  );
});

test("a tool that timed out is named, and says the clipboard may have it", async () => {
  const slow = fakeClipboard({ ok: false, reason: "timeout", tool: "xclip" });
  const { ui, client } = setup(reply("hello"), { copy: slow.copy });
  await ui.fire("send", "hi");
  await client.idle();
  await ui.fire("send", "/copy");
  expect(store(ui).status).toBe(
    "xclip did not finish in 3 s; the clipboard may have it",
  );
});

test("a send queued while the turn runs shows as 'Queued', then as '1 queued' once a tool result gives the ambient bar back", async () => {
  let toResult: () => void = () => {};
  const gate1 = new Promise<void>((r) => {
    toResult = r;
  });
  let end: () => void = () => {};
  const gate2 = new Promise<void>((r) => {
    end = r;
  });
  const { ui, client } = setup(async function* (ctx) {
    if (ctx.messages.at(-1)?.content !== "go") return;
    yield { type: "tool_requested", id: "r1", name: "read" };
    yield { type: "tool_start", id: "r1", name: "read" };
    await gate1;
    yield {
      type: "tool_result",
      id: "r1",
      name: "read",
      status: "succeeded",
      output: "o",
    };
    await gate2;
  });
  await ui.fire("send", "go");
  await ui.fire("send", "later");
  expect(store(ui).status).toBe("Queued: later · ↑ edits it");
  toResult();
  await until(() => store(ui).status === "build · 1 queued");
  end();
  await client.idle();
});

test("a queued send taken back to edit gives way to the ambient bar, which no longer counts it", async () => {
  const gates = [0, 1, 2].map(() => {
    let open: () => void = () => {};
    const gate = new Promise<void>((r) => {
      open = r;
    });
    return { gate, open };
  });
  const { ui, client } = setup(async function* (ctx) {
    if (ctx.messages.at(-1)?.content !== "go") return;
    for (const [i, id] of ["r1", "r2"].entries()) {
      yield { type: "tool_requested", id, name: "read" };
      yield { type: "tool_start", id, name: "read" };
      await gates[i]?.gate;
      yield {
        type: "tool_result",
        id,
        name: "read",
        status: "succeeded",
        output: "o",
      };
    }
    await gates[2]?.gate;
  });
  await ui.fire("send", "go");
  await ui.fire("send", "later");
  // A tool result has given the ambient bar back before the take-back, so
  // the take-back's own status is what the next result must replace.
  gates[0]?.open();
  await until(() => store(ui).status === "build · 1 queued");
  await ui.fire("history_prev", "");
  expect(store(ui).status).toBe(
    "Queued message back to edit · the turn goes on",
  );
  gates[1]?.open();
  await until(() => store(ui).status === "build");
  gates[2]?.open();
  await client.idle();
});

test("a cancelled or failed turn is the last turn too: the bar does not keep an older one", async () => {
  let nowMs = 1_000_000;
  const { ui, client } = setup(
    async function* (ctx) {
      const prompt = ctx.messages.at(-1)?.content;
      nowMs += prompt === "first" ? 5_000 : 2_000;
      if (prompt === "first")
        yield {
          type: "message",
          message: { role: "assistant", content: "ok" },
        };
      else if (prompt === "fail") throw new Error("boom");
      else
        await new Promise((_, reject) =>
          ctx.options.signal.addEventListener("abort", () =>
            reject(new Error("aborted")),
          ),
        );
    },
    { now: () => nowMs },
  );
  const idleBar = async () => {
    // Any idle write: a panel opened and closed rests the bar at the ambient status.
    await ui.fire("send", "/status");
    await ui.fire("panel_close");
    return store(ui).status;
  };
  await ui.fire("send", "first");
  await client.idle();
  expect(await idleBar()).toBe("build · last 5s");
  await ui.fire("send", "slow");
  await ui.fire("cancel");
  await client.idle();
  expect(await idleBar()).toBe("build · last 2s");
  await ui.fire("send", "fail");
  await client.idle();
  expect(await idleBar()).toBe("build · last 2s");
});

test("a send queued while the placement settles is a fact of the status the turn ends on", async () => {
  const { ui, client } = setup(
    async function* (ctx) {
      if (ctx.messages.at(-1)?.content !== "go") return;
      yield { type: "done", inputTokens: 1, outputTokens: 1 };
      yield { type: "message", message: { role: "assistant", content: "ok" } };
    },
    {
      // The placement read is slow: the turn waits for it (up to 300 ms) before it ends.
      placement: async () => {
        await new Promise((r) => setTimeout(r, 150));
        return null;
      },
    },
  );
  await ui.fire("send", "go");
  await until(() => store(ui).head.text === "" || !store(ui).head.on);
  await ui.fire("send", "later");
  await client.idle();
  const ending = ui.batches.find((batch) =>
    batch.some(
      (w) => w.path === "placeholder" && w.value === "Message the model",
    ),
  );
  const shown = ending?.find((w) => w.path === "status")?.value;
  expect(shown).toBe("build · last <1s · 1 queued");
});

test("once an approval is answered the card is no longer on hold: it runs", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client } = setup(async function* ({ options }) {
    yield {
      type: "message",
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            id: "c1",
            function: { name: "edit", arguments: { path: "a.txt" } },
          },
        ],
      },
    };
    yield { type: "tool_requested", id: "e1", name: "edit" };
    await options.approve(editProposal(), options.signal);
    await gate;
  });
  await ui.fire("send", "edit");
  await untilApproval(ui);
  expect(store(ui).live.card).toMatchObject({ running: false, mark: "?" });
  await ui.fire("dialog_confirm");
  expect(store(ui).live.card).toMatchObject({
    status: "running",
    running: true,
    mark: "",
    warn: false,
  });
  release();
  await client.idle();
});

test("a refused head write is sent again, whichever path wrote it", async () => {
  let nowMs = 1_000_000;
  const { ui, client } = setup(
    quiet(async ({ options }) => {
      await new Promise((r) => options.signal.addEventListener("abort", r));
    }),
    { now: () => nowMs },
  );
  await ui.fire("send", "go");
  expect(store(ui).head.text).toBe("waiting for the model · 0s");
  let refused = 0;
  ui.refuse = (writes) => {
    if (refused === 0 && writes.some((w) => w.path === "head")) {
      refused++;
      return "refused";
    }
    return undefined;
  };
  nowMs += 1_000;
  await client.flush();
  expect(refused).toBe(1);
  // The same second again: the head still differs from the store's, so it goes out.
  await until(() => store(ui).head.text === "waiting for the model · 1s");
  await ui.fire("cancel");
  await client.idle();
});
