import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import {
  type AgentEvent,
  type GoalReport,
  runAgent,
  StepLimitError,
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

async function workspaceRoot() {
  const root = await mkdtemp(join(tmpdir(), "local-agent-goal-"));
  roots.push(root);
  await writeFile(join(root, "a.txt"), "alpha\n");
  return root;
}

/**
 * One turn against a loopback model that answers request n with turns[n]
 * (one batch of calls each), then "Done". `goal` is the host's callback;
 * approvals answer `approve`, and the log interleaves the host's marks
 * (`goal`, `approve`) with the runtime's events.
 */
async function turn(
  turns: Call[][],
  options: {
    root?: string;
    intent?: Intent;
    goal?: boolean;
    refused?: Set<string>;
    approve?: boolean;
    maxTurns?: number;
    mcpTool?: string;
  } = {},
) {
  const root = options.root ?? (await workspaceRoot());
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
  const log: (
    | AgentEvent
    | { type: "goal"; report: GoalReport }
    | { type: "approve" }
  )[] = [];
  let thrown: unknown;
  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent: options.intent ?? "build",
        signal: AbortSignal.timeout(10_000),
        approve: async () => {
          log.push({ type: "approve" });
          return options.approve ?? true;
        },
        ...(options.goal
          ? { goal: (report: GoalReport) => log.push({ type: "goal", report }) }
          : {}),
        ...(options.refused ? { refused: options.refused } : {}),
        ...(options.maxTurns ? { maxTurns: options.maxTurns } : {}),
        ...(options.mcpTool
          ? {
              mcp: [
                {
                  tools: [
                    {
                      name: options.mcpTool,
                      inputSchema: { type: "object" },
                      readOnly: true,
                    },
                  ],
                  async callTool(): Promise<never> {
                    throw new Error("never called");
                  },
                  async close() {},
                },
              ],
            }
          : {}),
      },
    ))
      log.push(event);
  } catch (error) {
    thrown = error;
  } finally {
    await server.stop(true);
  }
  return {
    bodies,
    log,
    thrown,
    results: log.flatMap((event) =>
      event.type === "tool_result" ? [event] : [],
    ),
    reports: log.flatMap((event) =>
      event.type === "goal" ? [event.report] : [],
    ),
    offered: (index = 0) =>
      (bodies[index]?.tools ?? []).map((tool) => tool.function.name),
    system: (index = 0) =>
      bodies[index]?.messages.find((m) => m.role === "system")?.content ?? "",
  };
}

const status = (args: Record<string, unknown>): Call => ({
  name: "goal_status",
  arguments: args,
});

test("goal_status is offered only while the host runs a goal", async () => {
  const without = await turn([]);
  expect(without.offered()).not.toContain("goal_status");
  expect(without.system()).not.toContain("A goal is running");
  const goal = await turn([], { goal: true });
  expect(goal.offered()).toContain("goal_status");
  expect(goal.system()).toContain("A goal is running");
  // Chat has no goal_status, so it is not told of one.
  const chat = await turn([], { goal: true, intent: "chat" });
  expect(chat.offered()).not.toContain("goal_status");
  expect(chat.system()).not.toContain("A goal is running");
  // Its name stays reserved: an MCP tool cannot take it while no goal runs.
  for (const name of ["goal_status", "ask_user"]) {
    const taken = await turn([], { mcpTool: name });
    expect(String(taken.thrown)).toContain(`Duplicate tool name: ${name}`);
  }
  // A model that calls it anyway, with no goal, is refused.
  const stray = await turn([[status({ state: "done", summary: "x" })]]);
  expect(stray.results[0]?.status).toBe("failed");
  expect(stray.results[0]?.output).toContain(
    "goal_status is not available here",
  );
});

test("a report reaches the host whole, and the model reads what it recorded", async () => {
  const run = await turn(
    [
      [
        status({
          state: "done",
          summary: "  tests pass  ",
          evidence: [
            { kind: "command", ref: "bun test" },
            { kind: "file", ref: "src/a.ts" },
          ],
        }),
      ],
    ],
    { goal: true },
  );
  expect(run.reports).toEqual([
    {
      state: "done",
      summary: "tests pass",
      evidence: [
        { kind: "command", ref: "bun test" },
        { kind: "file", ref: "src/a.ts" },
      ],
    },
  ]);
  expect(run.results[0]).toMatchObject({ status: "succeeded" });
  expect(run.results[0]?.output).toContain("Recorded: done");
  // Evidence is optional; continue and blocked say what follows.
  const next = await turn(
    [[status({ state: "continue", summary: "half way" })]],
    { goal: true },
  );
  expect(next.reports[0]?.evidence).toEqual([]);
  expect(next.results[0]?.output).toContain("The host sends the next turn");
  const blocked = await turn(
    [[status({ state: "blocked", summary: "need a key" })]],
    { goal: true },
  );
  expect(blocked.results[0]?.output).toContain("what you need");
});

test("a malformed report fails before it reaches the host", async () => {
  for (const args of [
    { state: "maybe", summary: "x" },
    { state: "done", summary: "   " },
    { state: "done", summary: "x".repeat(501) },
    {
      state: "done",
      summary: "x",
      evidence: Array.from({ length: 9 }, () => ({ kind: "note", ref: "n" })),
    },
    { state: "done", summary: "x", evidence: [{ kind: "url", ref: "u" }] },
  ]) {
    const run = await turn([[status(args)]], { goal: true });
    expect(run.reports).toEqual([]);
    expect(run.results[0]?.status).toBe("failed");
  }
});

test("a subagent never reports on the goal, and a report never joins a parallel batch", async () => {
  const sub = await turn(
    [
      [{ name: "task", arguments: { prompt: "look", max_turns: 2 } }],
      [status({ state: "done", summary: "sub says done" })],
    ],
    { goal: true },
  );
  expect(sub.offered(1)).not.toContain("goal_status");
  expect(sub.reports).toEqual([]);
  const batch = await turn(
    [
      [
        { name: "read", arguments: { path: "a.txt" } },
        status({ state: "continue", summary: "read it" }),
        { name: "read", arguments: { path: "a.txt" } },
      ],
    ],
    { goal: true },
  );
  const order = batch.log.flatMap((event) =>
    event.type === "tool_requested" ||
    event.type === "tool_start" ||
    event.type === "tool_result"
      ? [`${event.type}:${event.name}`]
      : [],
  );
  expect(order).toEqual([
    "tool_requested:read",
    "tool_start:read",
    "tool_result:read",
    "tool_requested:goal_status",
    "tool_start:goal_status",
    "tool_result:goal_status",
    "tool_requested:read",
    "tool_start:read",
    "tool_result:read",
  ]);
});

test("a set the host passes keeps a refusal from one turn to the next", async () => {
  const root = await workspaceRoot();
  const write: Call = {
    name: "write",
    arguments: { path: "b.txt", content: "beta\n" },
  };
  const refused = new Set<string>();
  const first = await turn([[write]], { root, refused, approve: false });
  expect(first.results[0]?.status).toBe("denied");
  expect(first.log.filter((event) => event.type === "approve")).toHaveLength(1);
  // The next turn, same set: refused without a card.
  const second = await turn([[write]], { root, refused });
  expect(second.log.filter((event) => event.type === "approve")).toEqual([]);
  expect(second.results[0]?.status).toBe("denied");
  expect(second.results[0]?.output).toContain("already refused in this goal");
  // Without the set, each turn asks again.
  const fresh = await turn([[write]], { root, approve: false });
  expect(fresh.log.filter((event) => event.type === "approve")).toHaveLength(1);
});

test("a turn that uses every step ends in a StepLimitError, after its results", async () => {
  const read: Call = { name: "read", arguments: { path: "a.txt" } };
  const run = await turn([[read], [read], [read]], { maxTurns: 2 });
  expect(run.thrown).toBeInstanceOf(StepLimitError);
  expect((run.thrown as Error).message).toBe("Agent reached the 2-step limit");
  expect(run.results.map((result) => result.status)).toEqual([
    "succeeded",
    "succeeded",
  ]);
});
