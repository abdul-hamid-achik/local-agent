import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent, sessionProblem } from "#runtime/agent";
import { compactHistory, promptChars } from "#runtime/compact";
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

/**
 * A loopback Ollama that answers each chat request with the next reply,
 * reporting the prompt as a third of its characters, as Ollama reports the
 * whole prompt (cached or not).
 */
function ollama(replies: ("read" | "done" | "fail")[]) {
  const bodies: { messages: Message[] }[] = [];
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
            ...(reply === "read"
              ? {
                  tool_calls: [
                    {
                      function: {
                        name: "read",
                        arguments: { path: "big.txt" },
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
