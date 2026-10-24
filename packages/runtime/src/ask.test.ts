import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import {
  type AgentEvent,
  type Answer,
  type ApprovalRequest,
  type Question,
  runAgent,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import type { Intent } from "#runtime/instructions";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Body = {
  messages: { role: string; content: string }[];
  tools: { function: { name: string } }[];
};

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});

/**
 * One run against a loopback model that answers request n with turns[n]
 * (one batch of calls each), then "Done". `ask` is the host's callback;
 * the event log interleaves host-side marks (`ask`, `approve`) with the
 * runtime's events, in the order they happened.
 */
async function session(
  turns: Call[][],
  options: {
    intent?: Intent;
    ask?: (question: Question) => Answer | Promise<Answer>;
  } = {},
) {
  const root = await mkdtemp(join(tmpdir(), "local-agent-ask-"));
  roots.push(root);
  await writeFile(join(root, "a.txt"), "alpha\n");
  const bodies: Body[] = [];
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
      const body = (await request.json()) as Body;
      bodies.push(body);
      const calls = turns[bodies.length - 1];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: calls ? "" : "Done", ...(calls ? { tool_calls: calls.map((call) => ({ function: call })) } : {}) }, done: true })}\n`,
      );
    },
  });
  const log: (AgentEvent | { type: "ask" | "approve"; detail: string })[] = [];
  const questions: Question[] = [];
  const { ask } = options;
  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent: options.intent ?? "build",
        signal: AbortSignal.timeout(10_000),
        approve: (async (proposal: ApprovalRequest) => {
          log.push({ type: "approve", detail: proposal.kind });
          return true;
        }) as never,
        ...(ask
          ? {
              ask: async (question: Question) => {
                questions.push(question);
                log.push({ type: "ask", detail: question.question });
                return ask(question);
              },
            }
          : {}),
      },
    ))
      log.push(event);
  } finally {
    await server.stop(true);
  }
  const results = log.flatMap((event) =>
    event.type === "tool_result" ? [event] : [],
  );
  return {
    bodies,
    log,
    questions,
    results,
    offered: (index = 0) =>
      (bodies[index]?.tools ?? []).map((tool) => tool.function.name),
  };
}

const ask = (args: Record<string, unknown>): Call => ({
  name: "ask_user",
  arguments: args,
});

test("ask_user is offered only where the host can ask, in every intent", async () => {
  for (const intent of ["build", "plan", "chat"] as const) {
    const without = await session([], { intent });
    expect(without.offered()).not.toContain("ask_user");
    const host = await session([], {
      intent,
      ask: () => ({ kind: "declined" }),
    });
    expect(host.offered()).toContain("ask_user");
    // The system prompt says what an answer is not, only where it is offered.
    const system = (b: Body | undefined) =>
      b?.messages.find((m) => m.role === "system")?.content ?? "";
    expect(system(host.bodies[0])).toContain("ask_user asks the user");
    expect(system(without.bodies[0])).not.toContain("ask_user");
  }
  // A model that calls it anyway, with no host to ask, is refused.
  const run = await session([[ask({ question: "which?" })]]);
  expect(run.results[0]?.status).toBe("failed");
  expect(run.results[0]?.output).toContain("ask_user is not available here");
});

test("a choice reads back as its label, a yes/no as yes or no, text as typed", async () => {
  const single = await session(
    [
      [
        ask({
          question: "Which store?",
          options: [
            { id: "pg", label: "Postgres" },
            { id: "lite", label: "SQLite", detail: "one file" },
          ],
        }),
      ],
    ],
    { ask: () => ({ kind: "choice", id: "lite" }) },
  );
  // A list with no kind is a single choice.
  expect(single.questions[0]).toMatchObject({
    question: "Which store?",
    kind: "single",
  });
  expect(single.results[0]).toMatchObject({
    status: "succeeded",
    output: "The user chose: SQLite (id lite)",
  });

  const yesNo = await session(
    [[ask({ question: "Keep the old API?", kind: "yes_no" })]],
    { ask: () => ({ kind: "choice", id: "yes" }) },
  );
  expect(yesNo.results[0]?.output).toBe("The user answered: yes");

  const text = await session([[ask({ question: "  Which port?  " })]], {
    ask: () => ({ kind: "text", text: " 8080 \n" }),
  });
  expect(text.questions[0]).toMatchObject({
    question: "Which port?",
    kind: "freetext",
    options: [],
  });
  expect(text.results[0]?.output).toBe("The user answered:\n8080");

  const long = await session([[ask({ question: "Paste it" })]], {
    ask: () => ({ kind: "text", text: "x".repeat(10_000) }),
  });
  expect(long.results[0]?.output.length).toBeLessThanOrEqual(
    "The user answered:\n".length + 4000,
  );
  expect(long.results[0]?.output).toContain("[output cut at 4000");
});

test("a declined question is not asked again in the same turn", async () => {
  let asked = 0;
  const question = ask({ question: "Deploy now?", kind: "yes_no" });
  const run = await session([[question], [question]], {
    ask: () => {
      asked++;
      return { kind: "declined" };
    },
  });
  expect(asked).toBe(1);
  expect(run.results.map((result) => result.status)).toEqual([
    "denied",
    "denied",
  ]);
  expect(run.results[0]?.output).toContain("The user declined to answer");
  expect(run.results[1]?.output).toContain("already declined this question");
  // An empty answer and an unknown choice are no answer either.
  const empty = await session([[ask({ question: "Name?" })]], {
    ask: () => ({ kind: "text", text: "   " }),
  });
  expect(empty.results[0]?.output).toContain("empty answer");
  const stray = await session(
    [
      [
        ask({
          question: "Pick",
          options: [
            { id: "a", label: "A" },
            { id: "b", label: "B" },
          ],
        }),
      ],
    ],
    { ask: () => ({ kind: "choice", id: "zzz" }) },
  );
  expect(stray.results[0]?.output).toContain("declined");
  // ...and read as such: denied, never turned into an answer.
  expect(stray.results[0]?.status).toBe("denied");
  const notYesNo = await session([[ask({ question: "Go?", kind: "yes_no" })]], {
    ask: () => ({ kind: "choice", id: "maybe" }),
  });
  expect(notYesNo.results[0]).toMatchObject({ status: "denied" });
  expect(notYesNo.results[0]?.output).not.toContain("answered");
});

test("a declined question is remembered by what the user read, not its spacing", async () => {
  let asked = 0;
  const run = await session(
    [
      [ask({ question: "Deploy now?", kind: "yes_no" })],
      [ask({ question: "  deploy   NOW? ", kind: "yes_no" })],
      [ask({ question: "Deploy to staging?", kind: "yes_no" })],
    ],
    {
      ask: () => {
        asked++;
        return { kind: "declined" };
      },
    },
  );
  // The respaced re-ask is refused; a different question is asked.
  expect(asked).toBe(2);
  expect(run.results[1]?.output).toContain("already declined this question");
});

test("only a single choice keeps options: yes/no and typed answers drop them", async () => {
  const seen: Question[] = [];
  const options = [
    { id: "a", label: "A" },
    { id: "b", label: "B" },
  ];
  const yesNo = await session(
    [[ask({ question: "Go?", kind: "yes_no", options })]],
    {
      ask: (question) => {
        seen.push(question);
        return { kind: "choice", id: "a" };
      },
    },
  );
  const text = await session(
    [[ask({ question: "Name?", kind: "freetext", options })]],
    {
      ask: (question) => {
        seen.push(question);
        return { kind: "choice", id: "a" };
      },
    },
  );
  expect(seen.map((question) => question.options)).toEqual([[], []]);
  // A choice the question did not offer is no answer.
  expect(yesNo.results[0]?.status).toBe("denied");
  expect(text.results[0]?.status).toBe("denied");
});

test("a subagent never reaches the host's question, even when it calls one", async () => {
  let asked = 0;
  const run = await session(
    [
      [{ name: "task", arguments: { prompt: "find out", max_turns: 2 } }],
      // The subagent's first request: it tries to ask.
      [ask({ question: "Which one?" })],
    ],
    {
      ask: () => {
        asked++;
        return { kind: "text", text: "x" };
      },
    },
  );
  expect(asked).toBe(0);
  // The subagent is offered no ask_user.
  expect(run.offered(1)).not.toContain("ask_user");
});

test("a malformed question fails before the user sees it", async () => {
  let asked = 0;
  const host = {
    ask: (): Answer => {
      asked++;
      return { kind: "declined" };
    },
  };
  const one = await session(
    [
      [
        ask({
          question: "Pick",
          kind: "single",
          options: [{ id: "a", label: "A" }],
        }),
      ],
    ],
    host,
  );
  expect(one.results[0]?.status).toBe("failed");
  expect(one.results[0]?.output).toContain("at least two options");
  const twins = await session(
    [
      [
        ask({
          question: "Pick",
          options: [
            { id: "a", label: "A" },
            { id: "a", label: "B" },
          ],
        }),
      ],
    ],
    host,
  );
  expect(twins.results[0]?.output).toContain("unique");
  const blank = await session([[ask({ question: "   " })]], host);
  expect(blank.results[0]?.status).toBe("failed");
  expect(asked).toBe(0);
});

test("a question never runs in a parallel batch, and its start follows the answer", async () => {
  const run = await session(
    [
      [
        { name: "read", arguments: { path: "a.txt" } },
        ask({ question: "Which file next?" }),
        { name: "read", arguments: { path: "a.txt" } },
      ],
    ],
    { ask: () => ({ kind: "text", text: "b.txt" }) },
  );
  const order = run.log.flatMap((event) =>
    event.type === "tool_requested" ||
    event.type === "tool_start" ||
    event.type === "tool_result" ||
    event.type === "ask"
      ? [`${event.type}:${"name" in event ? event.name : ""}`]
      : [],
  );
  // Serial: each call is requested, (asked,) started and done before the
  // next is requested; the question's start comes after the answer.
  expect(order).toEqual([
    "tool_requested:read",
    "tool_start:read",
    "tool_result:read",
    "tool_requested:ask_user",
    "ask:",
    "tool_start:ask_user",
    "tool_result:ask_user",
    "tool_requested:read",
    "tool_start:read",
    "tool_result:read",
  ]);
});

test("an answer is never permission: a later edit still asks the host", async () => {
  const run = await session(
    [
      [ask({ question: "May I edit a.txt?", kind: "yes_no" })],
      [
        {
          name: "edit",
          arguments: { path: "a.txt", old_string: "alpha", new_string: "beta" },
        },
      ],
    ],
    { ask: () => ({ kind: "choice", id: "yes" }) },
  );
  const marks = run.log.flatMap((event) =>
    event.type === "ask" || event.type === "approve"
      ? [`${event.type}:${event.detail}`]
      : [],
  );
  expect(marks).toEqual(["ask:May I edit a.txt?", "approve:edit"]);
});
