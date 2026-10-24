import { Database } from "bun:sqlite";
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
import {
  promptChars,
  STATE_SOFT_BYTES,
  stateBytes,
  windowStart,
} from "#runtime/compact";
import { resolveProvider } from "#runtime/config";
import type { Message } from "#runtime/index";
import {
  ConversationFullError,
  MAX_STATE_BYTES,
  SessionStore,
} from "#runtime/session";
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
  options: {
    root?: string;
    session?: SessionContext;
    maxTurns?: number;
    compact?: boolean;
    /** The prompt size the model reports, in tokens (none when absent). */
    promptTokens?: number;
  } = {},
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
        `${JSON.stringify({ message: { role: "assistant", content: calls ? "" : "Done", ...(calls ? { tool_calls: calls.map((call) => ({ function: call })) } : {}) }, done: true, ...(options.promptTokens ? { prompt_eval_count: options.promptTokens } : {}) })}\n`,
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
        ...(options.compact ? { compact: true } : {}),
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

test("a todo list the window leaves out rides in the system prompt", async () => {
  const list =
    "- [x] read the spec\n- [~] write the parser\n- [ ] run the tests\n3 todos: 1 done, 1 in progress, 1 pending";
  const plan: Message[] = [
    { role: "user", content: "plan it" },
    {
      role: "assistant",
      content: "",
      tool_calls: [
        { id: "t1", function: { name: "todo_write", arguments: {} } },
      ],
    },
    {
      role: "tool",
      tool_name: "todo_write",
      tool_call_id: "t1",
      content: JSON.stringify({ status: "succeeded", output: list }),
    },
    { role: "assistant", content: "planned" },
  ];
  const far = await run([
    ...plan,
    ...earlier(118, 1_000),
    { role: "user", content: "go on" },
  ]);
  const system = far.requests[0]?.[0]?.content ?? "";
  expect(windowed(far.events)).toHaveLength(1);
  expect(
    far.requests[0]?.some((m) => m.content.includes("write the parser")),
  ).toBe(true);
  expect(system).toContain(
    `Your todo list, as you last wrote it (that message is no longer in this request):\n${list}`,
  );
  // The list takes its room from the window, not past it.
  const big = Array.from(
    { length: 50 },
    (_, i) => `- [ ] ${`step ${i} `.padEnd(200, "x")}`,
  ).join("\n");
  const long = await run([
    ...plan.map((m) =>
      m.role === "tool"
        ? {
            ...m,
            content: JSON.stringify({ status: "succeeded", output: big }),
          }
        : m,
    ),
    ...earlier(118, 1_000),
    { role: "user", content: "go on" },
  ]);
  const sent = long.requests[0] ?? [];
  expect(sent[0]?.content).toContain(big);
  expect(
    (sent[0]?.content.length ?? 0) + promptChars(sent.slice(1) as Message[]),
  ).toBeLessThan(16_384 * 3 * 0.7);
  // In the window, it is not said twice.
  const near = await run([...plan, { role: "user", content: "go on" }]);
  expect(near.requests[0]?.[0]?.content).not.toContain("Your todo list");
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

/** `n` earlier exchanges, each a read of `size` characters and its answer. */
function reads(n: number, size: number): Message[] {
  return Array.from({ length: n }, (_, i): Message[] => [
    { role: "user", content: `question ${i}` },
    {
      role: "assistant",
      content: "",
      tool_calls: [
        {
          id: `r${i}`,
          function: { name: "read", arguments: { path: "a.txt" } },
        },
      ],
    },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: `r${i}`,
      content: JSON.stringify({
        status: "succeeded",
        output: "x".repeat(size),
      }),
    },
    { role: "assistant", content: `answer ${i}` },
  ]).flat();
}

test("old results are shortened before any exchange is left out", async () => {
  // 12 reads of 4,000 characters: past the window's budget whole, well
  // under it once the reads are shortened.
  const history = [
    ...reads(12, 4_000),
    { role: "user", content: "and?" },
  ] as Message[];
  const compacted = await run(history, [], { compact: true });
  expect(compacted.thrown).toBeUndefined();
  const sent = compacted.requests[0] ?? [];
  // Every exchange goes, the old reads as short notes.
  expect(sent).toHaveLength(history.length + 1);
  expect(
    sent.filter((message) => message.content.includes(" elided ")),
  ).not.toEqual([]);
  expect(windowed(compacted.events)).toEqual([]);
  expect(compacted.events.some((event) => event.type === "compacted")).toBe(
    true,
  );
  // Without compaction the same conversation is windowed.
  const plain = await run(history);
  expect(windowed(plain.events)).toHaveLength(1);
  expect((plain.requests[0] ?? []).length).toBeLessThan(history.length + 1);
});

test("a server that counts the prompt small does not keep the window from shortening first", async () => {
  const root = await workspaceRoot();
  await writeFile(join(root, "big.txt"), `${"y".repeat(99)}\n`.repeat(200));
  // Five reads fit; the turn's own read of big.txt then passes the
  // window's budget, while the server's count (10 tokens) says it is far
  // under: the old reads are shortened, and nothing is left out.
  const history = [
    ...reads(5, 4_000),
    { role: "user", content: "and big.txt?" },
  ] as Message[];
  const result = await run(
    history,
    [[{ name: "read", arguments: { path: "big.txt" } }]],
    { root, compact: true, promptTokens: 10 },
  );
  expect(result.thrown).toBeUndefined();
  expect(result.requests).toHaveLength(2);
  expect(result.events.some((event) => event.type === "compacted")).toBe(true);
  expect(windowed(result.events)).toEqual([]);
  expect(result.requests[1]).toHaveLength(history.length + 3);
});

test("what compaction cannot free is left out whole, oldest exchanges first", async () => {
  // 200 exchanges (under the message count): even shortened, far past a
  // 16k window.
  const history = [
    ...reads(200, 4_000),
    { role: "user", content: "and?" },
  ] as Message[];
  const result = await run(history, [], { compact: true });
  expect(result.thrown).toBeUndefined();
  const sent = result.requests[0] ?? [];
  const [event] = windowed(result.events);
  expect(event).toBeDefined();
  expect(sent.length).toBe((event?.sent ?? 0) + 1);
  expect(sent[1]?.role).toBe("user");
  expect(sent.at(-1)?.content).toBe("and?");
  // The request fits the window's budget by the characters' measure.
  expect(promptChars(sent.slice(1) as Message[])).toBeLessThan(
    16_384 * 3 * 0.7,
  );
  // What it leaves out is not shortened: the session keeps it as it was.
  const compacted = result.events.flatMap((e) =>
    e.type === "compacted" ? [e] : [],
  );
  expect(compacted).toHaveLength(1);
  const kept = compacted[0]?.messages ?? [];
  const left = event?.left ?? 0;
  const content = (messages: Message[]) => messages.map((m) => m.content);
  expect(content(kept.slice(0, left))).toEqual(content(history.slice(0, left)));
  expect(kept.slice(left).some((m) => m.content.includes(" elided "))).toBe(
    true,
  );
  // Its notice counts what the request carries (plus the system prompt
  // and the tools), not the part left out.
  expect(compacted[0]?.tokensBefore ?? 0).toBeLessThan(
    (promptChars(history.slice(left)) + 9_000) / 3,
  );
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

/**
 * Earlier exchanges whose tool results carry images, so that the saved state
 * (with a closing user message) is `slack` bytes under the 8 MiB cap.
 */
function nearCap(slack: number): Message[] {
  const exchange = (i: number, image: number): Message[] => [
    { role: "user", content: `question ${i}` },
    {
      role: "assistant",
      content: "",
      tool_calls: [
        {
          id: `i${i}`,
          function: { name: "read", arguments: { path: "a.txt" } },
        },
      ],
    },
    {
      role: "tool",
      tool_name: "read",
      tool_call_id: `i${i}`,
      content: JSON.stringify({ status: "succeeded", output: "an image" }),
      images: ["A".repeat(image)],
    },
    { role: "assistant", content: `answer ${i}` },
  ];
  const close: Message = { role: "user", content: "and now?" };
  const base = [...exchange(0, 4_000_000), ...exchange(1, 0), close];
  const missing = MAX_STATE_BYTES - slack - stateBytes(base);
  return [...exchange(0, 4_000_000), ...exchange(1, missing), close];
}

/** The ledger's events, oldest first, as `tool:type`. */
function ledger(path: string): string[] {
  const raw = new Database(path, { readonly: true });
  try {
    return raw
      .query<{ tool_name: string; event_type: string }, []>(
        "SELECT tool_name, event_type FROM execution_events ORDER BY id",
      )
      .all()
      .map((row) => `${row.tool_name}:${row.event_type}`);
  } finally {
    raw.close();
  }
}

const catBig: Call = {
  name: "run_command",
  arguments: { argv: "cat big.txt" },
};
const readBig: Call = { name: "read", arguments: { path: "big.txt" } };

/** A session holding `nearCap(slack)`, and a file whose read result (3,900 characters) is more than `slack` leaves room for. */
async function nearCapSession(slack: number) {
  const root = await workspaceRoot();
  await writeFile(join(root, "big.txt"), "y".repeat(3_900));
  const dbPath = join(root, "sessions.db");
  const store = await SessionStore.open(dbPath);
  const history = nearCap(slack);
  const state = await store.createSession(root, history);
  expect(stateBytes(history)).toBe(MAX_STATE_BYTES - slack);
  return { root, dbPath, store, history, session: { store, state } };
}

const messageEvents = (events: AgentEvent[]) =>
  events.flatMap((event) => (event.type === "message" ? [event.message] : []));

test("a result the saved state cannot hold is saved as a note with its terminal event, and the turn goes on", async () => {
  const { root, dbPath, store, history, session } = await nearCapSession(1_500);
  try {
    const result = await run(history, [[catBig]], { root, session });
    expect(result.thrown).toBeUndefined();
    // The model answered the note: a second request, then "Done".
    expect(result.requests).toHaveLength(2);
    const sentResult = result.requests[1]?.find((m) => m.role === "tool");
    const kept = await store.loadSession(
      session.state.publicId,
      session.state.workspaceId,
    );
    const note = kept.messages.findLast((message) => message.role === "tool");
    expect(JSON.parse(note?.content ?? "{}")).toEqual({
      status: "succeeded",
      output: expect.stringContaining("too large for the session to keep"),
    });
    expect(kept.messages.at(-1)?.content).toBe("Done");
    expect(kept.messages.some((m) => m.content.includes("yyyy"))).toBe(false);
    // What the client was shown, and what the model read, is what is saved.
    expect(messageEvents(result.events)).toContainEqual(note);
    expect(
      result.requests[1]?.filter((m) => m.role === "tool").at(-1)?.content,
    ).toBe(note?.content);
    expect(sentResult).toBeDefined();
    // The tool_result event still carries the output, for display.
    expect(
      result.events.find((event) => event.type === "tool_result"),
    ).toMatchObject({ status: "succeeded" });
    expect(ledger(dbPath)).toEqual([
      "run_command:requested",
      "run_command:approval_requested",
      "run_command:approved",
      "run_command:started",
      "run_command:completed",
    ]);
    const raw = new Database(dbPath, { readonly: true });
    const receipt = raw
      .query<{ result_receipt: string }, []>(
        "SELECT result_receipt FROM execution_events WHERE event_type = 'completed'",
      )
      .get();
    raw.close();
    expect(receipt?.result_receipt).toContain("yyyy");
    expect(
      store.listHazards(session.state.id, session.state.workspaceId),
    ).toEqual([]);
  } finally {
    store.close();
  }
});

test("a note that does not fit ends the turn with the sentence, and the call stays started", async () => {
  // After the call's message (160 bytes), less than its note takes (316).
  const { root, dbPath, store, history, session } = await nearCapSession(300);
  try {
    const result = await run(history, [[catBig]], { root, session });
    expect(result.thrown).toBeInstanceOf(ConversationFullError);
    expect((result.thrown as Error).message).toBe(
      "This session is full: its saved state would pass 8 MiB.",
    );
    expect(ledger(dbPath).at(-1)).toBe("run_command:started");
    expect(
      store.listHazards(session.state.id, session.state.workspaceId),
    ).toHaveLength(1);
  } finally {
    store.close();
  }
});

test("every call of a batch the state cannot hold ends in the ledger with a note", async () => {
  const { root, dbPath, store, history, session } = await nearCapSession(1_500);
  try {
    const result = await run(history, [[readBig, readBig, readBig]], {
      root,
      session,
    });
    expect(result.thrown).toBeUndefined();
    const events = ledger(dbPath);
    expect(events.filter((entry) => entry.endsWith(":started"))).toHaveLength(
      3,
    );
    expect(events.filter((entry) => entry.endsWith(":completed"))).toHaveLength(
      3,
    );
    const kept = await store.loadSession(
      session.state.publicId,
      session.state.workspaceId,
    );
    const notes = kept.messages.filter(
      (message) =>
        message.role === "tool" &&
        message.tool_call_id?.startsWith("i") !== true,
    );
    expect(notes).toHaveLength(3);
    for (const note of notes)
      expect(note.content).toContain("too large for the session to keep");
    // The client was shown the notes, in call order, as saved.
    expect(
      messageEvents(result.events).filter((m) => m.role === "tool"),
    ).toEqual(notes);
    expect(kept.messages.at(-1)?.content).toBe("Done");
  } finally {
    store.close();
  }
});

test("in a batch, only a call whose own note does not fit is left started", async () => {
  // After the batch's message (361 bytes), room for one note (316), not two.
  const { root, dbPath, store, history, session } = await nearCapSession(800);
  try {
    const result = await run(history, [[readBig, readBig, readBig]], {
      root,
      session,
    });
    expect(result.thrown).toBeInstanceOf(ConversationFullError);
    const events = ledger(dbPath);
    expect(events.filter((entry) => entry.endsWith(":completed"))).toHaveLength(
      1,
    );
    expect(events.filter((entry) => entry.endsWith(":started"))).toHaveLength(
      3,
    );
    const kept = await store.loadSession(
      session.state.publicId,
      session.state.workspaceId,
    );
    // The one that ended has its answer; the rest are not in the history.
    expect(kept.messages.at(-1)?.content).toContain("too large");
    expect(kept.messages.at(-2)?.role).toBe("assistant");
    // Nor were they shown: the client saw the saved prefix and no more.
    expect(
      messageEvents(result.events).filter((m) => m.role === "tool"),
    ).toEqual([kept.messages.at(-1)]);
  } finally {
    store.close();
  }
});

test("past the soft budget a compacting client shortens the saved state, and the result is kept whole", async () => {
  const { root, dbPath, store, history, session } = await nearCapSession(1_500);
  try {
    expect(stateBytes(history)).toBeGreaterThan(STATE_SOFT_BYTES);
    const result = await run(history, [[catBig]], {
      root,
      session,
      compact: true,
    });
    expect(result.thrown).toBeUndefined();
    expect(result.requests).toHaveLength(2);
    const compacted = result.events.flatMap((event) =>
      event.type === "compacted" ? [event] : [],
    );
    // Once, before the first request: the images go, and that is enough.
    expect(compacted).toHaveLength(1);
    expect(result.events.indexOf(compacted[0] as AgentEvent)).toBeLessThan(
      result.events.findIndex((event) => event.type === "tool_requested"),
    );
    expect(compacted[0]?.elided).toBe(2);
    expect(compacted[0]?.messages.some((message) => message.images)).toBe(
      false,
    );
    expect(compacted[0]?.messages.at(-1)?.content).toBe("and now?");
    // The history the client now holds is the one saved, and it is small.
    const saved = await store.loadSession(
      session.state.publicId,
      session.state.workspaceId,
    );
    expect(stateBytes(saved.messages)).toBeLessThan(STATE_SOFT_BYTES);
    expect(saved.messages.slice(0, compacted[0]?.messages.length)).toEqual(
      compacted[0]?.messages,
    );
    expect(saved.messages.at(-1)?.content).toBe("Done");
    // The result fit: it is the output, not a note.
    expect(
      saved.messages.some((message) => message.content.includes("yyyy")),
    ).toBe(true);
    expect(
      saved.messages.some((message) => message.content.includes("too large")),
    ).toBe(false);
    expect(ledger(dbPath)).toEqual([
      "run_command:requested",
      "run_command:approval_requested",
      "run_command:approved",
      "run_command:started",
      "run_command:completed",
    ]);
  } finally {
    store.close();
  }
});

test("the state budget does not apply without a session, nor without compact", async () => {
  const without = await run(nearCap(1_500), [], { compact: true });
  expect(without.thrown).toBeUndefined();
  expect(without.events.some((event) => event.type === "compacted")).toBe(
    false,
  );
  const { root, store, history, session } = await nearCapSession(1_500);
  try {
    const plain = await run(history, [], { root, session });
    expect(plain.events.some((event) => event.type === "compacted")).toBe(
      false,
    );
    const kept = await store.loadSession(
      session.state.publicId,
      session.state.workspaceId,
    );
    expect(kept.messages.filter((message) => message.images)).toHaveLength(2);
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
