import type {
  AgentEvent,
  AgentOptions,
  ApprovalRequest,
  McpConnection,
  Message,
  Provider,
  SessionContext,
  Workspace,
} from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  type Block,
  type MessageView,
  parseBlocks,
  runsText,
} from "#cli/tuimark/blocks";
import type { PaletteItem } from "#cli/tuimark/commands";
import {
  APPROVAL_ARM_MS,
  formatElapsed,
  TuimarkClient,
} from "#cli/tuimark/controller";
import { FakeHost } from "#cli/tuimark/fake-host";
import {
  APPROVAL_STATUS,
  APPROVAL_TRUNCATED_SESSION_STATUS,
  APPROVAL_TRUNCATED_STATUS,
  APPROVAL_UNARMED_SESSION_STATUS,
  APPROVAL_UNARMED_STATUS,
  APPROVAL_WAIT_STATUS,
  IDLE_STATUS,
  NO_DETAILS_STATUS,
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
  expect(store(ui).busy).toBe(true);
  expect(store(ui).messages[0]).toMatchObject({ is_user: true });
  expect(store(ui).draft).toBe("");
  expect(store(ui).placeholder).toBe("Generating…");
  await client.idle();
  const s = store(ui);
  expect(s.messages.map((m: MessageView) => [m.is_user, m.is_asst])).toEqual([
    [true, false],
    [false, true],
  ]);
  expect(s.live.active).toBe(false);
  expect(s.busy).toBe(false);
  expect(s.placeholder).toBe("Message the model");
  expect(s.status).toBe("Ready · /help for commands");
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
  await new Promise((r) => setTimeout(r, 40));
  const during = store(ui);
  expect(during.thinking.on).toBe(true);
  // The runs are appended as they arrive; the markup shows the end of them in a
  // three-row scroll.
  expect(runsText(during.thinking.runs)).toBe("one\ntwo\nthree\nfour\nfi");
  expect(ui.batches.flat().some((w) => w.op === "append")).toBe(true);
  expect(during.live.active).toBe(false);
  toAnswer();
  await new Promise((r) => setTimeout(r, 20));
  expect(store(ui).thinking.on).toBe(false);
  expect(store(ui).live.active).toBe(true);
  await client.idle();
  const s = store(ui);
  expect(s.thinking).toEqual({ on: false, runs: [] });
  // The reasoning is not in the committed history, and the speed is on the status line.
  expect(JSON.stringify(s.messages)).not.toContain("three");
  expect(s.status).toBe("Ready · /help for commands · 40 tok/s");
  expect(client.writeErrors).toEqual([]);
});

test("a turn without timings leaves the idle status alone", async () => {
  const { ui, client } = setup(reply("hello there"));
  await ui.fire("send", "hi");
  await client.idle();
  expect(store(ui).status).toBe("Ready · /help for commands");
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
  await new Promise((r) => setTimeout(r, 40));
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
  await ui.fire("send", "/bogus");
  expect(store(ui).status).toBe("Unknown command: /bogus. Use /help");
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
  expect(store(ui).status).toBe("Queued: second prompt");
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
  expect(store(ui).status).toBe("Ready · /help for commands");
});

test("a second send while one is queued replaces it and says so", async () => {
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
  expect(store(ui).status).toBe("Queued: first queued");
  await ui.fire("send", "replacement\nsecond line");
  expect(store(ui).status).toBe("Queued message replaced: replacement");
  release();
  await client.idle();
  expect(seen).toHaveLength(2);
  expect(seen[1]?.messages.at(-1)).toBe("user:replacement\nsecond line");
  // The replaced text never became a turn or a view.
  expect(JSON.stringify(store(ui).messages)).not.toContain("first queued");
});

test("esc cancels the turn and drops the queued message, and the status says both", async () => {
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
  expect(store(ui).status).toBe("Queued: queued one");
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled · queued message dropped");
  expect(seen).toHaveLength(1);
  expect(store(ui).busy).toBe(false);
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
    views.map((m: MessageView) => [m.is_user, m.is_notice, m.is_asst]),
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
  expect(store(ui).status).toBe("Ready · /help for commands");
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
const decisions: boolean[] = [];

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

async function until(ok: () => boolean) {
  for (let i = 0; i < 200 && !ok(); i++)
    await new Promise((r) => setTimeout(r, 5));
}

/** The dialog is open and has armed itself (the status line offers "y allow"). */
async function untilApproval(ui: FakeHost) {
  await until(() => store(ui).status === APPROVAL_STATUS);
  expect(store(ui).approval.open).toBe(true);
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
  await new Promise((r) => setTimeout(r, 10));
  await ui.fire("cancel");
  await client.idle();
  expect(store(ui).status).toBe("Cancelled");
  expect(store(ui).messages).toHaveLength(0);
  expect(store(ui).draft).toBe("slow one");
  expect(store(ui).busy).toBe(false);
});

test("esc while an approval is pending denies it", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  client.abort();
  await client.idle();
  expect(store(ui).approval.open).toBe(false);
  expect(store(ui).busy).toBe(false);
  // Aborting is a deny: the promise resolved to false, not to a wrong true.
  expect(decisions).toEqual([false]);
});

test("ctrl+c (quit) with the dialog open resolves the approval to deny", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  expect(await ui.fire("quit_request")).toBe("quit");
  await client.idle();
  expect(decisions).toEqual([false]);
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
  const { ui, client } = setup(withApproval, { armMs: 80 });
  await ui.fire("send", "edit");
  await until(() => store(ui).approval.open);
  expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
  await ui.fire("dialog_confirm");
  expect(store(ui).approval.open).toBe(true);
  expect(decisions).toEqual([]);
  // The swallowed `y` is not silent: the status says to press it again.
  expect(store(ui).status).toBe(APPROVAL_UNARMED_STATUS);
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
  expect(store(ui).busy).toBe(false);
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

test("a multi-line error is one line on the status row and whole in its notice", async () => {
  const { ui, client } = setup(
    quiet(async () => {
      throw new Error('[\n  {\n    "code": "too_big"\n  }\n]');
    }),
  );
  await ui.fire("send", "go");
  await client.idle();
  expect(store(ui).status).toBe('[ { "code": "too_big" } ]');
  const notice = store(ui).messages.find((m: MessageView) => m.is_notice);
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
  expect(views.map((m: MessageView) => [m.is_user, m.is_notice])).toEqual([
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
    store(ui).messages.filter((m: MessageView) => m.is_notice),
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
    store(ui).messages.map((m: MessageView) => [m.is_user, m.is_notice]),
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

test("waiting becomes generating at the first event, with elapsed, and idle writes nothing", async () => {
  let nowMs = 1_000_000;
  let toText: () => void = () => {};
  let toEnd: () => void = () => {};
  const gate1 = new Promise<void>((r) => {
    toText = r;
  });
  const gate2 = new Promise<void>((r) => {
    toEnd = r;
  });
  const { ui, client } = setup(
    async function* () {
      await gate1;
      yield { type: "text", text: "hi " };
      await gate2;
      yield { type: "message", message: { role: "assistant", content: "hi" } };
    },
    { now: () => nowMs },
  );
  await ui.fire("send", "go");
  expect(store(ui).status).toBe("Waiting for the model… · 0s");
  nowMs += 12_000;
  await client.flush();
  expect(store(ui).status).toBe("Waiting for the model… · 12s");
  toText();
  await until(() => store(ui).status.startsWith("Generating…"));
  expect(store(ui).status).toBe("Generating… · 12s");
  nowMs += 53_000;
  await client.flush();
  expect(store(ui).status).toBe("Generating… · 1m 05s");
  toEnd();
  await client.idle();
  expect(store(ui).status).toBe("Ready · /help for commands");
  // The timer is gone with the turn: time passing writes nothing more.
  const batches = ui.batches.length;
  nowMs += 120_000;
  await client.flush();
  await new Promise((r) => setTimeout(r, 20));
  expect(ui.batches.length).toBe(batches);
  expect(store(ui).status).toBe("Ready · /help for commands");
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
      await new Promise((resolve) =>
        options.signal.addEventListener("abort", () => {
          aborted = true;
          resolve(undefined);
        }),
      );
    }),
  );
  await ui.fire("send", "go");
  expect(await ui.fire("quit_request")).toBe("quit");
  await client.idle();
  expect(aborted).toBe(true);
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
  expect(store(ui).busy).toBe(false);
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
  const { ui, client } = setup(async function* () {
    for (const text of pieces) {
      yield { type: "text", text };
      await new Promise((r) => setTimeout(r, 12));
    }
    await new Promise((r) => setTimeout(r, 40));
    expect(liveText(store(ui))).toContain("The end.");
    yield {
      type: "message",
      message: { role: "assistant", content: pieces.join("") },
    };
  });
  await ui.fire("send", "go");
  await client.idle();
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
  await new Promise((r) => setTimeout(r, 150));
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
      if (c.commit.template === "la-turn") {
        const view = c.buf as MessageView;
        if (view.is_tool) return `tool ${view.card.name} ${view.card.status}`;
        const label = view.is_user
          ? "You"
          : view.is_notice
            ? "Error"
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
  await new Promise((r) => setTimeout(r, 15));
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
  expect(store(ui).status).toBe("Queued: second one");
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
  await new Promise((r) => setTimeout(r, 15));
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
  expect(store(ui).mouse_on).toBe(false);
  await ui.fire("send", "/mouse on");
  expect(store(ui).mouse_on).toBe(true);
  expect(store(ui).status).toBe("Mouse changed to on");
  await ui.fire("send", "/mouse off");
  expect(store(ui).mouse_on).toBe(false);

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
    store(failed.ui).messages.map((m: MessageView) => [m.is_user, m.is_notice]),
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
    "/theme",
    "/mouse",
    "/permissions",
    "/details",
    "/status",
    "/sessions",
    "/export",
    "/mcp",
    "/help",
  ]);
  expect(store(ui).slash.rows[0]).toMatchObject({ sel: true, mark: "›" });
  await ui.fire("typed", "/m");
  expect(store(ui).slash.rows.map((r) => r.cmd)).toEqual([
    "/mode",
    "/mouse",
    "/mcp",
  ]);
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  expect(store(ui).slash.rows[2]).toMatchObject({ cmd: "/mcp", sel: true });
  // The choice clamps at the last row, like the palette's list.
  await ui.fire("slash_next", "/m");
  expect(store(ui).slash.rows[2]?.sel).toBe(true);
  await ui.fire("slash_prev", "/m");
  expect(store(ui).slash.rows[1]?.sel).toBe(true);
  // Typing again resets the choice to the first match.
  await ui.fire("typed", "/mo");
  expect(store(ui).slash.rows.map((r) => r.cmd)).toEqual(["/mode", "/mouse"]);
  expect(store(ui).slash.rows[0]?.sel).toBe(true);
  // Tab completes the chosen row into the draft and closes the popup.
  await ui.fire("slash_next", "/mo");
  await ui.fire("slash_complete", "/mo");
  expect(store(ui).draft).toBe("/mouse ");
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
  expect(store(ui).busy).toBe(true);
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
  expect(store(ui).busy).toBe(true);
  // The dismissed popup stays closed for this draft, opens for the next.
  await ui.fire("typed", "/mo");
  expect(store(ui).slash.open).toBe(false);
  await ui.fire("typed", "");
  await ui.fire("typed", "/m");
  expect(store(ui).slash.open).toBe(true);
  release();
  await client.idle();
  expect(store(ui).busy).toBe(false);
});

test("enter completes over the popup only when the choice differs", async () => {
  const { ui, seen } = setup(reply("x"));
  // "/mo" is not yet the chosen "/mode ": enter completes, nothing runs.
  await ui.fire("typed", "/mo");
  await ui.fire("send", "/mo");
  expect(store(ui).draft).toBe("/mode ");
  expect(store(ui).slash.open).toBe(false);
  expect(store(ui).busy).toBe(false);
  expect(seen).toEqual([]);
  // A moved choice completes to that row, still without running it.
  await ui.fire("typed", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("slash_next", "/m");
  await ui.fire("send", "/m");
  expect(store(ui).draft).toBe("/mcp");
  expect(store(ui).status).toBe("Ready · /help for commands");
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
  expect(ui.commits).toEqual([]);
  await ui.fire("slash_complete", "/m");
  expect(store(ui).draft).toBe("/mode ");
  expect(ui.commits).toEqual([]);
  await ui.fire("send", "hi");
  await client.idle();
  // The turn committed its views; none of them is popup state.
  expect(ui.commits.length).toBeGreaterThan(0);
  expect(JSON.stringify(ui.commits)).not.toContain("tab complete");
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

test("shift+tab toggles the mode like /mode, and is refused while busy", async () => {
  const { ui } = setup(reply("x"));
  expect(store(ui).header.mode).toBe("build");
  await ui.fire("mode_toggle");
  expect(store(ui).header.mode).toBe("plan");
  expect(store(ui).status).toBe("Mode changed to plan");
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
    "/mode plan|build",
    "/theme dark|light|auto",
    "/mouse on|off",
    "/permissions [clear]",
    "/details",
    "/status",
    "/sessions",
    "/export",
    "/mcp",
    "/help",
  ]);
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
  expect(store(ui).approval.grant_ok).toBe(true);
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
        commandProposal(["bun", "test", "b.ts", "--watch"]),
        commandProposal(["bun", "run", "x"]),
      ],
      [commandProposal(["ls"])],
      [commandProposal(["ls", "-la"]), commandProposal(["git", "status"])],
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

  // `bun test …` runs; `bun run` still asks.
  await ui.fire("send", "two");
  await untilApproval(ui);
  expect(decisions).toEqual([true, true]);
  expect(store(ui).approval.grant_hint).toBe(
    "a allow `bun run …` this session",
  );
  await ui.fire("dialog_cancel");
  await client.idle();

  // A bare program grants the program: `ls -la` is covered, `git` is not.
  await ui.fire("send", "three");
  await untilApproval(ui);
  expect(store(ui).approval.grant_hint).toBe("a allow `ls …` this session");
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
  expect(store(ui).status).toBe("No session grants");
  await ui.fire("send", "one");
  await untilApproval(ui);
  await ui.fire("dialog_session");
  await client.idle();
  await ui.fire("send", "/permissions");
  expect(store(ui).status).toBe("Session grants: files");
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
  expect(store(ui).approval.grant_ok).toBe(false);
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

test("grants: an unarmed `a` is swallowed with feedback, like `y`", async () => {
  const { ui, client } = setup(turnsAsking([[editProposal()]]), {
    armMs: 80,
  });
  await ui.fire("send", "one");
  await until(() => store(ui).approval.open);
  expect(store(ui).status).toBe(APPROVAL_WAIT_STATUS);
  await ui.fire("dialog_session");
  expect(store(ui).approval.open).toBe(true);
  expect(store(ui).status).toBe(APPROVAL_UNARMED_SESSION_STATUS);
  expect(decisions).toEqual([]);
  await until(() => store(ui).status === APPROVAL_STATUS);
  await ui.fire("dialog_session");
  await client.idle();
  expect(decisions).toEqual([true]);
  expect(store(ui).header.grants).toBe("auto: files");
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
  await ui.fire("send", "/details");
  expect(store(ui).status).toBe(NO_DETAILS_STATUS);
  expect(store(ui).details.open).toBe(false);
});

test("/details shows the lines the card truncated, and closing rests at idle", async () => {
  const { ui, client } = setup(toolScript(LONG_OUTPUT));
  await ui.fire("send", "read it");
  await client.idle();
  // The committed card never held line 15.
  const card = store(ui).messages.find((m) => m.is_tool)?.card;
  expect(runsText(card?.out ?? [])).toContain(
    "… 12 more lines · /details for full output",
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
  expect(store(ui).status).toBe(IDLE_STATUS);
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
  await until(() => store(busy.ui).busy);
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
  expect(lines).toContain("Mouse: off");
  expect(lines).toContain("Context: 0/8.0k");
  await ui.fire("panel_close");
  expect(store(ui).panel.open).toBe(false);
  expect(store(ui).status).toBe(IDLE_STATUS);
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
  expect(store(ui).status).toBe("Session grants: files");
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
  expect(store(ui).messages.at(-1)?.is_notice).toBe(true);
  await ui.fire("send", "next");
  await client.idle();
  expect(JSON.stringify(seen)).not.toContain("Export failed");
});
