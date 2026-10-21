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
import type { Host, HostEvent } from "@thelacanians/tuimark";
import { expect, test } from "vitest";
import type { MessageView } from "#cli/tuimark/blocks";
import type { PaletteItem } from "#cli/tuimark/commands";
import { TuimarkClient, type UiHost } from "#cli/tuimark/controller";
import type { Store } from "#cli/tuimark/store";

// ------------------------------------------------------------------ a fake host

class FakeHost implements UiHost {
  store: Record<string, unknown> = {};
  readonly handlers = new Map<string, (ev: HostEvent) => unknown>();
  readonly batches: { path: string; value: unknown }[][] = [];
  private inflight = 0;
  maxInflight = 0;
  private seq = 0;

  on(action: string, handler: (ev: HostEvent) => unknown): Host {
    this.handlers.set(action, handler);
    return this as unknown as Host;
  }

  async batch(writes: { path: string; value: unknown }[]) {
    this.maxInflight = Math.max(this.maxInflight, ++this.inflight);
    this.batches.push(writes);
    await new Promise((r) => setTimeout(r, 1));
    for (const { path, value } of writes) this.apply(path, value);
    this.inflight--;
    return { type: "ack" as const, id: "x", ok: true };
  }

  async get(path: string) {
    const value = path
      .split(".")
      .reduce<unknown>(
        (o, k) => (o as Record<string, unknown> | undefined)?.[k],
        this.store,
      );
    return { found: value !== undefined, value } as Awaited<
      ReturnType<UiHost["get"]>
    >;
  }

  private apply(path: string, value: unknown) {
    const keys = path.split(".");
    let node = this.store;
    for (const key of keys.slice(0, -1)) {
      node[key] ??= {};
      node = node[key] as Record<string, unknown>;
    }
    node[keys.at(-1) as string] = value;
  }

  /** Fires an action as the document would, and waits for the handler like the host does. */
  async fire(action: string, value: unknown = null): Promise<unknown> {
    const handler = this.handlers.get(action);
    if (!handler) throw new Error(`no handler for ${action}`);
    return handler({ seq: ++this.seq, action, source: "", keys: {}, value });
  }

  statusLog(): unknown[] {
    return this.batches
      .flat()
      .filter((w) => w.path === "status")
      .map((w) => w.value);
  }
}

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
    run: (_provider, messages, options) => {
      seen.push({ messages: messages.map((m) => `${m.role}:${m.content}`) });
      return script({ options, messages });
    },
    ...extra,
  });
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
  expect(store(ui).live.blocks[0]?.t.trim()).toBe("partial reply");
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

test("while busy, a slash command says Busy and a prompt is ignored", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => {
    release = r;
  });
  const { ui, client, seen } = setup(
    quiet(async () => {
      await gate;
    }),
  );
  await ui.fire("send", "go");
  await ui.fire("send", "/mode plan");
  expect(store(ui).status).toBe("Busy · wait for the current turn to finish");
  await ui.fire("send", "second prompt");
  release();
  await client.idle();
  expect(seen).toHaveLength(1);
});

function editProposal(): ApprovalRequest {
  return {
    kind: "edit",
    path: "a.txt",
    before: "",
    after: "",
    diff: "-x\n+y",
    identity: "i",
  };
}

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

async function untilApproval(ui: FakeHost) {
  for (let i = 0; i < 100 && !store(ui).approval?.open; i++)
    await new Promise((r) => setTimeout(r, 5));
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
  expect(store(ui).status).toContain("Waiting for approval");
  await ui.fire("dialog_confirm");
  expect(store(ui).approval.open).toBe(false);
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
  expect(store(ui).messages).toHaveLength(0);
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
  const { ui, client } = setup(
    quiet(async () => {
      throw new Error("boom");
    }),
    { session },
  );
  expect(store(ui).messages).toHaveLength(2);
  await ui.fire("send", "next");
  await client.idle();
  expect(store(ui).messages.map((m: MessageView) => m.is_user)).toEqual([
    true,
    false,
    true,
  ]);
  expect(store(ui).status).toBe("boom");
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
  expect(await ui.fire("quit")).toBe("quit");
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
    { tools: [{}, {}] },
    { tools: [{}] },
  ] as unknown as McpConnection[];
  const { ui } = setup(reply("x"), { mcp });
  await ui.fire("send", "/mcp");
  expect(store(ui).status).toBe("MCP servers: 2 · 3 tools");
});

test("no write is ever refused by the host", async () => {
  const { ui, client } = setup(withApproval);
  await ui.fire("send", "edit");
  await untilApproval(ui);
  await ui.fire("dialog_confirm");
  await client.idle();
  expect(client.writeErrors).toEqual([]);
});
