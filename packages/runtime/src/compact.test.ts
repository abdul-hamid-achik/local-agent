import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent, sessionProblem } from "#runtime/agent";
import {
  carriedTodo,
  compactHistory,
  compactNow,
  compactState,
  latestTodo,
  promptChars,
  STATE_SOFT_BYTES,
  STATE_TARGET_BYTES,
  stateBytes,
} from "#runtime/compact";
import { resolveProvider } from "#runtime/config";
import type { Message } from "#runtime/index";
import { SessionStore } from "#runtime/session";
import { Workspace } from "#runtime/workspace";

const receipt = (status: string, output: string) =>
  JSON.stringify({ status, output });
const call = (id: string) => ({
  id,
  function: { name: "read", arguments: { path: id } },
});

// Two turns; the second is mid-way: its last call (d) has not been read yet.
const history: Message[] = [
  { role: "user", content: "first", images: ["aaaa"] },
  {
    role: "assistant",
    content: "",
    thinking: "old reasoning",
    tool_calls: [call("a")],
  },
  {
    role: "tool",
    tool_name: "read",
    tool_call_id: "a",
    content: receipt("succeeded", "A".repeat(5_000)),
  },
  { role: "assistant", content: "done with a" },
  { role: "user", content: "second" },
  {
    role: "assistant",
    content: "",
    thinking: "T".repeat(2_000),
    tool_calls: [call("b"), call("c")],
  },
  {
    role: "tool",
    tool_name: "read",
    tool_call_id: "b",
    content: receipt("failed", "B".repeat(5_000)),
  },
  {
    role: "tool",
    tool_name: "read",
    tool_call_id: "c",
    content: receipt("succeeded", "ok"),
  },
  { role: "assistant", content: "", thinking: "now", tool_calls: [call("d")] },
  {
    role: "tool",
    tool_name: "read",
    tool_call_id: "d",
    content: receipt("succeeded", "D".repeat(5_000)),
  },
];

test("the prompt counts reasoning only after the last user message", () => {
  const chars = promptChars(history);
  const withoutOld = promptChars(
    history.map((message, index) =>
      index === 1 ? { ...message, thinking: undefined } : message,
    ),
  );
  expect(chars).toBe(withoutOld);
  const withoutCurrent = promptChars(
    history.map((message, index) =>
      index === 5 ? { ...message, thinking: undefined } : message,
    ),
  );
  expect(chars - withoutCurrent).toBe(2_000);
});

test("a wide character counts as a token, not a third of one", () => {
  expect(promptChars([{ role: "user", content: "abc" }])).toBe(3);
  expect(promptChars([{ role: "user", content: "日本語" }])).toBe(9);
  expect(promptChars([{ role: "user", content: "한글 ok" }])).toBe(9);
});

test("compaction frees images first, then read results, oldest first", () => {
  const little = compactHistory(history, 1);
  expect(little.elided).toBe(1);
  expect(little.messages[0]?.images).toBeUndefined();
  expect(little.messages[2]).toEqual(history[2]);

  const more = compactHistory(history, 10_000);
  expect(more.elided).toBe(3);
  expect(more.saved).toBeGreaterThanOrEqual(10_000);
  const [a, b] = [more.messages[2], more.messages[6]];
  expect(a).toMatchObject({
    role: "tool",
    tool_name: "read",
    tool_call_id: "a",
  });
  expect(JSON.parse(a?.content ?? "")).toEqual({
    status: "succeeded",
    output: `${"A".repeat(240)}\n[4760 more characters elided to keep the conversation within the context window. Run the tool again if you need them.]`,
  });
  // The status survives: a failed result still reads as failed.
  expect(JSON.parse(b?.content ?? "").status).toBe("failed");
  // So does the mark of a result from outside.
  const external = compactHistory(
    [
      {
        role: "tool",
        tool_name: "fetch",
        tool_call_id: "f",
        content: JSON.stringify({
          status: "succeeded",
          source: "external",
          output: "W".repeat(5_000),
        }),
      },
      { role: "user", content: "next" },
    ],
    1_000,
  );
  expect(JSON.parse(external.messages[0]?.content ?? "")).toMatchObject({
    status: "succeeded",
    source: "external",
  });
  // Enough was freed: the current turn's reasoning is untouched.
  expect(more.messages[5]?.thinking).toBe(history[5]?.thinking);
  // The input is not changed.
  expect(history[2]?.content).toContain("AAAA");
});

test("the current work stays whole, and a second pass finds nothing", () => {
  const all = compactHistory(history, Number.POSITIVE_INFINITY);
  // Images, a, b, and the reasoning of an earlier step of this turn; c is
  // too small to shorten, and reasoning before the last user message is
  // not in the prompt anyway.
  expect(all.elided).toBe(4);
  expect(all.messages[5]?.thinking).toBeUndefined();
  expect(all.messages[1]?.thinking).toBe("old reasoning");
  expect(all.messages[7]).toEqual(history[7]);
  // The last user message, the last call and its unread result.
  for (const index of [4, 8, 9])
    expect(all.messages[index]).toEqual(history[index]);
  expect(all.messages).toHaveLength(history.length);
  expect(all.messages.map((message) => message.tool_call_id)).toEqual(
    history.map((message) => message.tool_call_id),
  );
  const again = compactHistory(all.messages, Number.POSITIVE_INFINITY);
  expect(again.elided).toBe(0);
  expect(again.messages).toEqual(all.messages);
});

test("the latest todo list stays whole; an older one, and a failed write, are shortened", () => {
  const todo = (id: string, status: string, items: number): Message[] => [
    {
      role: "assistant",
      content: "",
      tool_calls: [{ id, function: { name: "todo_write", arguments: {} } }],
    },
    {
      role: "tool",
      tool_name: "todo_write",
      tool_call_id: id,
      content: receipt(
        status,
        Array.from({ length: items }, (_, i) =>
          `- [ ] step ${i} `.padEnd(60, "x"),
        ).join("\n"),
      ),
    },
  ];
  const plans: Message[] = [
    { role: "user", content: "plan it" },
    ...todo("t1", "succeeded", 30),
    ...todo("t2", "succeeded", 30),
    ...todo("t3", "failed", 30),
    { role: "assistant", content: "planned" },
    { role: "user", content: "go on" },
    { role: "assistant", content: "on it" },
  ];
  const { messages } = compactHistory(plans, Number.POSITIVE_INFINITY);
  expect(messages[2]?.content).not.toBe(plans[2]?.content);
  expect(messages[4]).toEqual(plans[4]);
  expect(messages[6]?.content).not.toBe(plans[6]?.content);
});

/**
 * A loopback Ollama that answers each chat request with the next reply,
 * reporting the prompt as a third of its characters, as Ollama reports the
 * whole prompt (cached or not).
 */
/** "read" reads big.txt from the next line on; a number, from that line. */
function ollama(replies: ("read" | "done" | "fail" | number)[]) {
  const bodies: { messages: Message[] }[] = [];
  // Each read starts a line further on: the same call again would be
  // answered with a note, not a second copy.
  let reads = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await request.json()) as { messages: Message[] };
      bodies.push(body);
      const reply = replies.shift() ?? "done";
      if (reply === "fail") return new Response("overloaded", { status: 500 });
      return new Response(
        `${JSON.stringify({
          message: {
            role: "assistant",
            content: reply === "done" ? "Done" : "",
            ...(reply === "read" || typeof reply === "number"
              ? {
                  tool_calls: [
                    {
                      function: {
                        name: "read",
                        arguments: {
                          path: "big.txt",
                          offset: typeof reply === "number" ? reply : ++reads,
                        },
                      },
                    },
                  ],
                }
              : {}),
          },
          done: true,
          prompt_eval_count: Math.round(
            JSON.stringify(body.messages).length / 3,
          ),
          eval_count: 5,
        })}\n`,
      );
    },
  });
  return { server, bodies };
}

const isPrefix = (a: Message[], b: Message[]) =>
  JSON.stringify(b.slice(0, a.length)) === JSON.stringify(a);

test("a compacted session survives a provider failure and the next turn runs", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-compact-"));
  await writeFile(
    join(root, "big.txt"),
    Array.from({ length: 2_000 }, (_, i) => `row ${i + 1}`).join("\n"),
  );
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  const { server, bodies } = ollama(["read", "read", "read", "read", "fail"]);
  try {
    const session = { store, state: await store.createSession(root) };
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
      { context: "8192" },
    );
    const options = {
      workspace,
      session,
      intent: "plan" as const,
      signal: AbortSignal.timeout(10_000),
      approve: async () => true,
      compact: true,
    };
    // The client's copy, kept as the Tuimark client keeps it.
    const client: Message[] = [{ role: "user", content: "go" }];
    const events: AgentEvent[] = [];
    await expect(
      (async () => {
        for await (const event of runAgent(provider, client.slice(), options)) {
          events.push(event);
          if (event.type === "message") client.push(event.message);
          if (event.type === "compacted")
            client.splice(0, client.length, ...event.messages);
        }
      })(),
    ).rejects.toThrow();
    const compacted = events.filter((event) => event.type === "compacted");
    expect(compacted.length).toBeGreaterThan(0);
    const first = compacted[0];
    if (first?.type !== "compacted") throw new Error("unreachable");
    expect(first.tokensAfter).toBeLessThan(first.tokensBefore);
    expect(first.tokensBefore).toBeGreaterThan(0.7 * 8192);

    // Until it compacted, each request extended the last one: Ollama's
    // prompt cache holds. The request after it carries the short notes,
    // and the result the model had not read yet is whole.
    const at = bodies.findIndex((body) =>
      body.messages.some((message) => message.content.includes(" elided ")),
    );
    expect(at).toBeGreaterThan(1);
    for (let index = 1; index < at; index++)
      expect(
        isPrefix(
          bodies[index - 1]?.messages ?? [],
          bodies[index]?.messages ?? [],
        ),
      ).toBe(true);
    const sent = bodies[at]?.messages ?? [];
    expect(sent.at(-1)?.role).toBe("tool");
    expect(sent.at(-1)?.content).not.toContain(" elided ");

    // The failure left the durable state where the client is.
    const stored = await store.loadSession(session.state.publicId, root);
    expect(stored.messages).toEqual(client);
    expect(sessionProblem(store, stored)).toBeUndefined();

    // The next turn starts from the client's copy and completes.
    const next: AgentEvent[] = [];
    for await (const event of runAgent(
      provider,
      [...client, { role: "user", content: "continue" }],
      { ...options, session: { store, state: stored } },
    ))
      next.push(event);
    expect(next.at(-1)?.type).toBe("turn_complete");
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("a read repeated after compaction shortened its first result gets the whole text again", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-compact-repeat-"));
  await writeFile(
    join(root, "big.txt"),
    Array.from({ length: 2_000 }, (_, i) => `row ${i + 1}`).join("\n"),
  );
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  // offset 1, 2, 3, 4, then 1 again: the first read's result is the one
  // compaction shortens first.
  const { server } = ollama(["read", "read", "read", "read", 1]);
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture", {
        context: "8192",
      }),
      [{ role: "user", content: "go" }],
      {
        workspace,
        session: { store, state: await store.createSession(root) },
        intent: "plan",
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
        compact: true,
      },
    ))
      events.push(event);
    const results = events.flatMap((event) =>
      event.type === "tool_result" ? [event] : [],
    );
    expect(results).toHaveLength(5);
    expect(
      events.findIndex((event) => event.type === "compacted"),
    ).toBeLessThan(events.indexOf(results[4] as AgentEvent));
    expect(results[4]?.output).toBe(results[0]?.output);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("without compact the history is never rewritten", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-compact-off-"));
  await writeFile(
    join(root, "big.txt"),
    Array.from({ length: 2_000 }, (_, i) => `row ${i + 1}`).join("\n"),
  );
  using workspace = await Workspace.create(root);
  const { server, bodies } = ollama(["read", "read", "read", "read"]);
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture", {
        context: "8192",
      }),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent: "plan",
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    expect(events.some((event) => event.type === "compacted")).toBe(false);
    for (let index = 1; index < bodies.length; index++)
      expect(
        isPrefix(
          bodies[index - 1]?.messages ?? [],
          bodies[index]?.messages ?? [],
        ),
      ).toBe(true);
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

/**
 * `n` exchanges, each a user message, a read call and its result (`size`
 * characters, and an image of `imageSize` when given), then the current
 * turn's user message.
 */
function exchanges(n: number, size: number, imageSize = 0): Message[] {
  return [
    ...Array.from({ length: n }, (_, i): Message[] => [
      { role: "user", content: `question ${i}` },
      { role: "assistant", content: "", tool_calls: [call(`r${i}`)] },
      {
        role: "tool",
        tool_name: "read",
        tool_call_id: `r${i}`,
        content: receipt("succeeded", "x".repeat(size)),
        ...(imageSize ? { images: ["A".repeat(imageSize)] } : {}),
      },
      { role: "assistant", content: `answer ${i}` },
    ]).flat(),
    { role: "user", content: "and now?" },
  ];
}

test("an image counts as its own length only for the saved state", () => {
  const images = exchanges(1, 10, 50_000);
  const window = compactHistory(images, 1);
  const exact = compactHistory(images, 1, 0, { state: true });
  expect(window.saved).toBe(3_000);
  expect(exact.saved).toBe(50_000);
});

test("state under the soft budget is left alone", () => {
  const small = exchanges(30, 100_000);
  expect(stateBytes(small)).toBeLessThan(STATE_SOFT_BYTES);
  const result = compactState(small);
  expect(result.elided).toBe(0);
  expect(result.messages).toEqual(small);
});

test("past the soft budget the state is shortened to the target, images first", () => {
  // 3.2 MB of images and 3.6 MB of results: dropping the images is enough.
  const big = exchanges(36, 100_000, 90_000);
  expect(stateBytes(big)).toBeGreaterThan(STATE_SOFT_BYTES);
  const result = compactState(big);
  expect(result.before).toBe(stateBytes(big));
  expect(result.after).toBe(stateBytes(result.messages));
  expect(result.after).toBeLessThanOrEqual(STATE_TARGET_BYTES);
  // Oldest first, and only as many as needed.
  const withImage = result.messages.flatMap((message, index) =>
    message.images ? [index] : [],
  );
  expect(result.messages[2]?.images).toBeUndefined();
  expect(withImage.length).toBeGreaterThan(0);
  expect(withImage.at(-1)).toBe(big.length - 3);
  // No result was shortened that did not have to be.
  expect(
    result.messages.filter((message) => message.content.includes(" elided ")),
  ).toEqual([]);
  expect(result.elided).toBe(36 - withImage.length);
  expect(big[2]?.images).toBeDefined();
});

test("past the soft budget the state is shortened to the target, then old results, oldest first", () => {
  const big = exchanges(70, 100_000);
  expect(stateBytes(big)).toBeGreaterThan(STATE_SOFT_BYTES);
  const result = compactState(big);
  expect(result.after).toBeLessThanOrEqual(STATE_TARGET_BYTES);
  const elided = result.messages.flatMap((message, index) =>
    message.content.includes(" elided ") ? [index] : [],
  );
  expect(elided.length).toBeGreaterThan(0);
  // The oldest go; the newest results stay whole.
  expect(elided[0]).toBe(2);
  expect(elided.at(-1)).toBeLessThan(big.length - 4);
  expect(result.messages.at(-2)?.content).toBe(big.at(-2)?.content);
  expect(result.messages.at(-1)).toEqual(big.at(-1));
  expect(result.elided).toBe(elided.length);
  // It stops when it is down, rather than shortening everything.
  expect(elided.length).toBeLessThan(70);
});

test("the current work stays whole, whatever the state holds", () => {
  const huge = [
    ...exchanges(3, 10),
    {
      role: "assistant" as const,
      content: "",
      tool_calls: [call("now")],
    },
    {
      role: "tool" as const,
      tool_name: "read",
      tool_call_id: "now",
      content: receipt("succeeded", "N".repeat(900_000)),
      images: ["I".repeat(6_900_000)],
    },
  ];
  const result = compactState(huge);
  expect(result.messages.at(-1)).toEqual(huge.at(-1));
  expect(result.after).toBeGreaterThan(STATE_SOFT_BYTES);
});

test("the state also sheds the reasoning of earlier turns, which a request never carries", () => {
  const reasoned: Message[] = [
    ...Array.from({ length: 8 }, (_, i): Message[] => [
      { role: "user", content: `question ${i}` },
      {
        role: "assistant",
        content: `answer ${i}`,
        thinking: "t".repeat(900_000),
      },
    ]).flat(),
    { role: "user", content: "and now?" },
  ];
  expect(stateBytes(reasoned)).toBeGreaterThan(STATE_SOFT_BYTES);
  // The window leaves earlier turns' reasoning alone: it frees nothing.
  expect(compactHistory(reasoned, 1).saved).toBe(0);
  const result = compactState(reasoned);
  expect(result.after).toBeLessThanOrEqual(STATE_TARGET_BYTES);
  const left = result.messages.flatMap((message, index) =>
    message.thinking ? [index] : [],
  );
  // Oldest first, and only as many as needed; the answers stay.
  expect(result.messages[1]?.thinking).toBeUndefined();
  expect(left.length).toBeGreaterThan(0);
  expect(result.messages.map((message) => message.content)).toEqual(
    reasoned.map((message) => message.content),
  );
  expect(result.elided).toBe(8 - left.length);
});

test("the todo list carried is the latest one written, and a failed write is not a list", () => {
  const write = (status: string, output: string): Message => ({
    role: "tool",
    tool_name: "todo_write",
    content: receipt(status, output),
  });
  const history: Message[] = [
    { role: "user", content: "go" },
    write(
      "succeeded",
      "- [~] first plan\n1 todos: 0 done, 1 in progress, 0 pending",
    ),
    write(
      "succeeded",
      "- [~] second plan\n1 todos: 0 done, 1 in progress, 0 pending",
    ),
    write("failed", "At most one todo may be in_progress"),
  ];
  expect(latestTodo(history)).toEqual({
    index: 2,
    text: "- [~] second plan\n1 todos: 0 done, 1 in progress, 0 pending",
  });
  expect(carriedTodo(history, 1_000)?.text).toContain("second plan");
  expect(carriedTodo(history, 1_000)?.text).not.toContain("first plan");
});

test("a carried todo list keeps what is in progress and the first pending items, in order", () => {
  const list = (lines: string[]): Message[] => [
    { role: "user", content: "go" },
    {
      role: "tool",
      tool_name: "todo_write",
      content: receipt("succeeded", lines.join("\n")),
    },
  ];
  const counts = "5 todos: 1 done, 1 in progress, 3 pending";
  const history = list([
    "- [x] done",
    `- [ ] ${"a".repeat(20)}`,
    `- [ ] ${"b".repeat(60)}`,
    "- [ ] c",
    "- [~] now",
    counts,
  ]);
  // Whole when it fits; done items give way first.
  expect(carriedTodo(history, 1_000)?.text).toBe(
    JSON.parse(history[1]?.content ?? "").output,
  );
  expect(carriedTodo(history, 160)?.text).toBe(
    `- [ ] ${"a".repeat(20)}\n- [ ] ${"b".repeat(60)}\n- [ ] c\n- [~] now\n${counts}`,
  );
  // Then pending items from the first one that does not fit: a shorter one
  // after it is left out too, so "…" stands for all of them.
  const cut = `- [ ] ${"a".repeat(20)}\n…\n- [~] now\n${counts}`;
  expect(cut).toHaveLength(80);
  for (const limit of [80, 100, 130])
    expect(carriedTodo(history, limit)?.text).toBe(cut);
  expect(carriedTodo(history, 79)?.text).toBe(`…\n- [~] now\n${counts}`);
  // A limit too small for the item in progress and the counts cuts mid-line.
  expect(carriedTodo(history, 30)?.text).toHaveLength(30);
  // Nothing left to do: nothing carried.
  expect(carriedTodo(list(["- [x] done", "1 todos: 1 done"]), 1_000)).toBe(
    undefined,
  );
});

test("compactNow shortens everything the model has read, whatever the state weighs, and says by how much", () => {
  const history = exchanges(6, 5_000, 2_000);
  expect(stateBytes(history)).toBeLessThan(STATE_SOFT_BYTES);
  const done = compactNow(history);
  // Every earlier result and image goes; the current question stays whole.
  expect(done.elided).toBe(6);
  expect(done.messages).toHaveLength(history.length);
  expect(done.messages.at(-1)).toEqual(history.at(-1));
  for (const index of [2, 6, 10, 14, 18, 22]) {
    expect(done.messages[index]?.content).toContain(" elided ");
    expect(done.messages[index]?.images).toBeUndefined();
  }
  expect(done.bytesBefore).toBe(stateBytes(history));
  expect(done.bytesAfter).toBe(stateBytes(done.messages));
  expect(done.bytesAfter).toBeLessThan(done.bytesBefore / 4);
  expect(done.tokensAfter).toBeLessThan(done.tokensBefore);
  // The caller's history is not changed; a second pass finds nothing.
  expect(history[2]?.content).not.toContain(" elided ");
  const again = compactNow(done.messages);
  expect(again.elided).toBe(0);
  expect(again.bytesAfter).toBe(again.bytesBefore);
  expect(again.messages).toEqual(done.messages);
});

test("compactNow with a session saves the shortened history, and the next turn runs on it", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-compact-now-"));
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  const { server, bodies } = ollama(["done"]);
  try {
    const created = await store.createSession(root);
    const history = exchanges(4, 5_000);
    const session = {
      store,
      state: store.saveStateCAS(
        created.id,
        created.workspaceId,
        created.revision,
        history,
      ),
    };
    const before = session.state.revision;
    const done = compactNow(history, session);
    expect(done.elided).toBe(4);
    expect(session.state.revision).toBeGreaterThan(before);
    expect(session.state.messages).toEqual(done.messages);
    const stored = await store.loadSession(session.state.publicId, root);
    expect(stored.messages).toEqual(done.messages);
    expect(sessionProblem(store, stored)).toBeUndefined();

    // The client's copy is now the saved one: a turn accepts it, and the
    // provider is sent the short notes, not the results.
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
      { context: "8192" },
    );
    const events: AgentEvent[] = [];
    for await (const event of runAgent(provider, done.messages, {
      workspace,
      session,
      intent: "plan",
      signal: AbortSignal.timeout(10_000),
      approve: async () => true,
      compact: true,
    }))
      events.push(event);
    expect(events.at(-1)?.type).toBe("turn_complete");
    const sent = bodies[0]?.messages ?? [];
    expect(sent.some((message) => message.content.includes(" elided "))).toBe(
      true,
    );
    expect(
      sent.some((message) => message.content.includes("x".repeat(1_000))),
    ).toBe(false);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("compactNow saves nothing when nothing can be shortened, the copy is not the session's, or the session moved on", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-compact-now-"));
  const store = await SessionStore.open(join(root, "session.sqlite"));
  try {
    const created = await store.createSession(root);
    const short = exchanges(2, 10);
    const first = store.saveStateCAS(
      created.id,
      created.workspaceId,
      created.revision,
      short,
    );
    // Nothing to shorten: the revision stays.
    const quiet = { store, state: first };
    expect(compactNow(short, quiet).elided).toBe(0);
    expect(quiet.state.revision).toBe(first.revision);

    // A copy that is not the session's is refused, the session untouched.
    expect(() => compactNow(exchanges(2, 5_000), quiet)).toThrow(
      "Session history differs from its durable state",
    );
    expect(quiet.state.revision).toBe(first.revision);

    // Another writer moved the session on: the save is refused, and so is
    // the stale copy's change of state.
    const big = exchanges(2, 5_000);
    const stale = {
      store,
      state: store.saveStateCAS(
        first.id,
        first.workspaceId,
        first.revision,
        big,
      ),
    };
    const moved = store.saveStateCAS(
      stale.state.id,
      stale.state.workspaceId,
      stale.state.revision,
      big,
    );
    expect(() => compactNow(big, stale)).toThrow();
    expect(stale.state.revision).toBeLessThan(moved.revision);
    const stored = await store.loadSession(moved.publicId, root);
    expect(stored.revision).toBe(moved.revision);
    expect(stored.messages).toEqual(big);
  } finally {
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});
