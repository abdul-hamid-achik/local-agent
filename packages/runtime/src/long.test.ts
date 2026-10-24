import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import {
  type AgentEvent,
  MAX_REQUEST_MESSAGES,
  runAgent,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import type { Message } from "#runtime/index";
import { ConversationFullError, SessionStore } from "#runtime/session";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});

/** `n` messages of earlier conversation: user and assistant in turn, starting with a user. */
function earlier(n: number): Message[] {
  return Array.from({ length: n }, (_, i) => ({
    role: i % 2 === 0 ? "user" : "assistant",
    content: `m${i}`,
  })) as Message[];
}

/**
 * One run of `history` against a loopback model that answers request n
 * with `turns[n]` (one batch of calls), then "Done". Returns the requests'
 * message counts and what the run threw.
 */
async function run(history: Message[], turns: Call[][] = []) {
  const root = await mkdtemp(join(tmpdir(), "local-agent-long-"));
  roots.push(root);
  await writeFile(join(root, "a.txt"), "alpha\n");
  const requests: number[] = [];
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
      const body = (await request.json()) as { messages: unknown[] };
      requests.push(body.messages.length);
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
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
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

test("a conversation the request cannot carry is refused in a sentence, before the model is asked", async () => {
  const full = await run([
    ...earlier(MAX_REQUEST_MESSAGES - 1),
    { role: "user", content: "and now?" },
  ]);
  expect(full.thrown).toBeInstanceOf(ConversationFullError);
  expect((full.thrown as Error).message).toBe(
    "This conversation is full: it has 1000 messages, and a request carries at most 999 besides the system prompt.",
  );
  expect(full.requests).toEqual([]);
  // One message fewer still runs: 999 plus the system prompt.
  const fits = await run([
    ...earlier(MAX_REQUEST_MESSAGES - 2),
    { role: "user", content: "and now?" },
  ]);
  expect(fits.thrown).toBeUndefined();
  expect(fits.requests).toEqual([MAX_REQUEST_MESSAGES]);
});

test("a turn whose calls fill the request stops before the next request, not inside the transport", async () => {
  const read: Call = { name: "read", arguments: { path: "a.txt" } };
  // 980 earlier messages and the prompt, then one batch of 12 reads: 994
  // messages after it, so the next request (995 with the system prompt)
  // fits; a second batch of 12 does not.
  const result = await run(
    [...earlier(980), { role: "user", content: "read it all" }],
    [Array(12).fill(read), Array(12).fill(read)],
  );
  expect(result.thrown).toBeInstanceOf(ConversationFullError);
  expect((result.thrown as Error).message).toContain(
    "filled up during the turn",
  );
  expect(result.requests).toEqual([982, 995]);
  // Every call it made has its result.
  expect(
    result.events.filter((event) => event.type === "tool_result"),
  ).toHaveLength(24);
});

test("a session whose saved state would pass 8 MiB is full, not a bare error", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-long-db-"));
  roots.push(root);
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
