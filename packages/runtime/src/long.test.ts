import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import {
  type AgentEvent,
  MAX_HISTORY_MESSAGES,
  MAX_REQUEST_MESSAGES,
  runAgent,
  type SessionContext,
} from "#runtime/agent";
import { promptChars, windowStart } from "#runtime/compact";
import { resolveProvider } from "#runtime/config";
import type { Message } from "#runtime/index";
import { ConversationFullError, SessionStore } from "#runtime/session";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Sent = { role: string; content: string };

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});

/** `n` messages of earlier chat, user and assistant in turn, from a user; each `size` characters. */
function earlier(n: number, size = 0): Message[] {
  return Array.from({ length: n }, (_, i) => ({
    role: i % 2 === 0 ? "user" : "assistant",
    content: `m${i} `.padEnd(size, "x"),
  })) as Message[];
}

async function workspaceRoot() {
  const root = await mkdtemp(join(tmpdir(), "local-agent-long-"));
  roots.push(root);
  await writeFile(join(root, "a.txt"), "alpha\n");
  return root;
}

/**
 * One run of `history` against a loopback model that answers request n
 * with `turns[n]` (one batch of calls), then "Done". Returns each
 * request's messages, the events, and what the run threw.
 */
async function run(
  history: Message[],
  turns: Call[][] = [],
  options: { root?: string; session?: SessionContext; maxTurns?: number } = {},
) {
  const root = options.root ?? (await workspaceRoot());
  const requests: Sent[][] = [];
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
      const body = (await request.json()) as { messages: Sent[] };
      requests.push(body.messages);
      const calls = turns[requests.length - 1];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: calls ? "" : "Done", ...(calls ? { tool_calls: calls.map((call) => ({ function: call })) } : {}) }, done: true })}\n`,
      );
    },
  });
  const events: AgentEvent[] = [];
  let thrown: unknown;
  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      history,
      {
        workspace,
        intent: "build",
        signal: AbortSignal.timeout(20_000),
        approve: async () => true,
        ...(options.session ? { session: options.session } : {}),
        ...(options.maxTurns ? { maxTurns: options.maxTurns } : {}),
      },
    ))
      events.push(event);
  } catch (error) {
    thrown = error;
  } finally {
    await server.stop(true);
  }
  return { requests, events, thrown };
}

const windowed = (events: AgentEvent[]) =>
  events.flatMap((event) => (event.type === "windowed" ? [event] : []));

test("a long conversation sends its newest exchanges, says what it left out, and keeps all of it", async () => {
  const root = await workspaceRoot();
  const store = await SessionStore.open(join(root, "sessions.db"));
  try {
    const history = [
      ...earlier(1199),
      { role: "user", content: "and now?" },
    ] as Message[];
    const state = await store.createSession(root, history);
    const session: SessionContext = { store, state };
    const first = await run(history, [], { root, session });
    expect(first.thrown).toBeUndefined();
    const sent = first.requests[0] ?? [];
    expect(sent.length).toBeLessThanOrEqual(MAX_REQUEST_MESSAGES);
    expect(sent[0]?.role).toBe("system");
    // From a user message on: no exchange is cut in two.
    expect(sent[1]?.role).toBe("user");
    expect(sent.at(-1)?.content).toBe("and now?");
    const [event] = windowed(first.events);
    expect(event).toBeDefined();
    expect((event?.left ?? 0) + (event?.sent ?? 0)).toBe(1200);
    expect(sent.length).toBe((event?.sent ?? 0) + 1);
    // The model is told; the user's client gets the counts.
    expect(sent[0]?.content).toContain(
      `The oldest ${event?.left} messages of this conversation are not in this request`,
    );
    // The durable state keeps every message, and the next turn continues it.
    expect(session.state.messages).toHaveLength(1201);
    const next = await run(
      [...session.state.messages, { role: "user", content: "again" }],
      [],
      { root, session },
    );
    expect(next.thrown).toBeUndefined();
    expect(session.state.messages).toHaveLength(1203);
  } finally {
    store.close();
  }
});

test("a short conversation is sent whole, with no note", async () => {
  const short = await run([...earlier(6), { role: "user", content: "hi" }]);
  expect(short.requests[0]).toHaveLength(8);
  expect(short.requests[0]?.[0]?.content).not.toContain(
    "are not in this request",
  );
  expect(windowed(short.events)).toEqual([]);
});

test("a conversation too long for the window, not for the count, is windowed by size", async () => {
  // 60 exchanges of 1,000 characters: far past a 16k window's budget.
  const result = await run([
    ...earlier(119, 1_000),
    { role: "user", content: "summarize" },
  ]);
  const sent = result.requests[0] ?? [];
  const chars = promptChars(sent.slice(1) as Message[]);
  // Under the share compaction aims below (0.7 of 16384 tokens at three
  // characters each), before the system prompt and the tools.
  expect(chars).toBeLessThan(16_384 * 3 * 0.7);
  expect(sent[1]?.role).toBe("user");
  expect(windowed(result.events)).toHaveLength(1);
});

test("a turn whose own calls fill the request stops before the next request, not inside the transport", async () => {
  const read: Call = { name: "read", arguments: { path: "a.txt" } };
  // 31 steps of 32 reads (a response's most): the turn alone (prompt, then
  // a call and its 32 results per step) passes what a request carries, and
  // no earlier exchange can be left out of it. Only a turn longer than the
  // default 20 steps gets there.
  const result = await run(
    [...earlier(10), { role: "user", content: "read it all" }],
    Array.from({ length: 31 }, () => Array(32).fill(read)),
    { maxTurns: 40 },
  );
  expect(result.thrown).toBeInstanceOf(ConversationFullError);
  expect((result.thrown as Error).message).toContain(
    "its current turn alone is 1024 messages",
  );
  expect(result.requests).toHaveLength(31);
  // Every call it made has its result.
  expect(
    result.events.filter((event) => event.type === "tool_result"),
  ).toHaveLength(31 * 32);
});

test("a conversation of the most a session holds is refused in a sentence, before the model is asked", async () => {
  const full = await run([
    ...earlier(MAX_HISTORY_MESSAGES - 1),
    { role: "user", content: "and now?" },
  ]);
  expect(full.thrown).toBeInstanceOf(ConversationFullError);
  expect((full.thrown as Error).message).toBe(
    `This conversation is full: it has ${MAX_HISTORY_MESSAGES} messages, and a session holds at most ${MAX_HISTORY_MESSAGES - 1}.`,
  );
  expect(full.requests).toEqual([]);
});

test("a session whose saved state would pass 8 MiB is full, not a bare error", async () => {
  const root = await workspaceRoot();
  const store = await SessionStore.open(join(root, "sessions.db"));
  try {
    const session = await store.createSession(root);
    expect(() =>
      store.saveStateCAS(session.id, session.workspaceId, session.revision, [
        // Each under the message's own 1,000,000-character cap.
        ...Array.from({ length: 9 }, () => ({
          role: "user" as const,
          content: "x".repeat(999_999),
        })),
      ]),
    ).toThrow(ConversationFullError);
  } finally {
    store.close();
  }
});

test("windowStart: whole exchanges, the current turn kept, and a start that holds still", () => {
  const chat = earlier(20, 100);
  // All of it fits: from the start.
  expect(windowStart(chat, 10_000, 0, 999)).toBe(0);
  // 2,000 characters in all; 1,000 fit: the first user message whose rest fits.
  const start = windowStart(chat, 1_000, 0, 999);
  expect(chat[start]?.role).toBe("user");
  expect(promptChars(chat.slice(start))).toBeLessThanOrEqual(1_000);
  expect(promptChars(chat.slice(start - 2))).toBeGreaterThan(1_000);
  // By count too.
  expect(windowStart(chat, 10_000, 0, 5)).toBe(16);
  // Never past the last user message, even when that is still too long.
  const turn = [
    ...chat,
    { role: "user", content: "x".repeat(5_000) },
  ] as Message[];
  expect(windowStart(turn, 1_000, 0, 999)).toBe(20);
  // A step of 500: 750 over rounds to 1,000 left out; one more exchange
  // (950 over) still does, so the start holds still.
  const a = windowStart(chat, 1_250, 500, 999);
  const b = windowStart([...chat, ...earlier(2, 100)], 1_250, 500, 999);
  expect(a).toBe(10);
  expect(b).toBe(a);
  // Without the step it moves with every exchange added.
  expect(windowStart(chat, 1_250, 0, 999)).toBe(8);
  expect(windowStart([...chat, ...earlier(2, 100)], 1_250, 0, 999)).toBe(10);
});
