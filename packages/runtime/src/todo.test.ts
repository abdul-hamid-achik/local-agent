import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  type AgentEvent,
  type ApprovalRequest,
  runAgent,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Body = {
  messages: { role: string; content: string }[];
  tools: { function: { name: string } }[];
};

async function session(calls: Call[], intent: "plan" | "build" = "build") {
  const root = await mkdtemp(join(tmpdir(), "local-agent-todo-"));
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
      const call = calls[body.messages.filter((m) => m.role === "tool").length];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Done", ...(call ? { tool_calls: [{ function: call }] } : {}) }, done: true })}\n`,
      );
    },
  });
  const events: AgentEvent[] = [];
  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent,
        signal: AbortSignal.timeout(10_000),
        approve: (async (proposal: ApprovalRequest) => true) as never,
      },
    ))
      events.push(event);
  } finally {
    await server.stop(true);
  }
  const results = events.flatMap((event) =>
    event.type === "tool_result" ? [event] : [],
  );
  return {
    root,
    bodies,
    results,
    output: (index: number) => results[index]?.output ?? "",
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

test("todo_write renders markers and a summary line", async () => {
  const run = await session([
    {
      name: "todo_write",
      arguments: {
        todos: [
          { content: "inspect files", status: "done" },
          { content: "edit agent.ts", status: "in_progress" },
          { content: "run tests", status: "pending" },
        ],
      },
    },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    expect(run.output(0)).toBe(
      "- [x] inspect files\n- [~] edit agent.ts\n- [ ] run tests\n3 todos: 1 done, 1 in progress, 1 pending",
    );
  } finally {
    await run.cleanup();
  }
});

// todo_write keeps no state of its own: the list is the latest write in the
// history (latestTodo, tested in compact.test.ts). What a call can show is that
// it renders the list it was given and no earlier one.
test("todo_write renders only the list it is given, never an earlier one with it", async () => {
  const run = await session([
    {
      name: "todo_write",
      arguments: {
        todos: [
          { content: "first", status: "done" },
          { content: "second", status: "pending" },
        ],
      },
    },
    {
      name: "todo_write",
      arguments: { todos: [{ content: "only task", status: "in_progress" }] },
    },
  ]);
  try {
    expect(run.results).toHaveLength(2);
    expect(run.output(1)).toBe(
      "- [~] only task\n1 todos: 0 done, 1 in progress, 0 pending",
    );
    expect(run.output(1)).not.toMatch(/first|second/u);
    // The first call's output is its own list, whole.
    expect(run.output(0)).toContain("first");
    expect(run.output(0)).toContain("second");
  } finally {
    await run.cleanup();
  }
});

test("todo_write rejects two in_progress items", async () => {
  const run = await session([
    {
      name: "todo_write",
      arguments: {
        todos: [
          { content: "a", status: "in_progress" },
          { content: "b", status: "in_progress" },
        ],
      },
    },
  ]);
  try {
    expect(run.results[0]?.status).toBe("failed");
    expect(run.output(0)).toContain("At most one todo may be in_progress");
  } finally {
    await run.cleanup();
  }
});

test("todo_write rejects empty content and over 50 items", async () => {
  const run = await session([
    {
      name: "todo_write",
      arguments: { todos: [{ content: "", status: "pending" }] },
    },
    {
      name: "todo_write",
      arguments: {
        todos: Array.from({ length: 51 }, (_, i) => ({
          content: `task ${i}`,
          status: "done",
        })),
      },
    },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual([
      "failed",
      "failed",
    ]);
    // Read as a list of problems, each with where it is, not as zod JSON.
    expect(run.output(0)).toContain("Invalid arguments for todo_write:");
    expect(run.output(0)).toContain("→ at todos[0].content");
    expect(run.output(1)).toContain("expected array to have <=50 items");
    expect(run.output(0)).not.toContain('"code"');
  } finally {
    await run.cleanup();
  }
});

test("todo_write tells the model its 50-item bound, and the bound holds", async () => {
  const run = await session([
    {
      name: "todo_write",
      arguments: {
        todos: Array.from({ length: 50 }, (_, i) => ({
          content: `task ${i}`,
          status: "pending",
        })),
      },
    },
  ]);
  try {
    const tool = run.bodies[0]?.tools.find(
      (entry) => entry.function.name === "todo_write",
    ) as unknown as {
      function: {
        description: string;
        parameters: { properties: { todos: { maxItems?: number } } };
      };
    };
    // What the model is shown is what the arguments are checked against.
    expect(tool.function.parameters.properties.todos.maxItems).toBe(50);
    expect(tool.function.description).toContain("At most 50 items");
    expect(run.results[0]?.status).toBe("succeeded");
    expect(run.output(0)).toContain(
      "50 todos: 0 done, 0 in progress, 50 pending",
    );
  } finally {
    await run.cleanup();
  }
});

test("todo_write with an empty list clears the todos and says so", async () => {
  const run = await session([{ name: "todo_write", arguments: { todos: [] } }]);
  try {
    expect(run.results[0]?.status).toBe("succeeded");
    expect(run.output(0)).toBe("0 todos: 0 done, 0 in progress, 0 pending");
  } finally {
    await run.cleanup();
  }
});

test("todo_write is offered for both plan and build intents", async () => {
  const plan = await session([], "plan");
  try {
    expect(plan.bodies[0]?.tools.map((tool) => tool.function.name)).toContain(
      "todo_write",
    );
  } finally {
    await plan.cleanup();
  }
  const build = await session([{ name: "list", arguments: {} }]);
  try {
    expect(build.bodies[0]?.tools.map((tool) => tool.function.name)).toContain(
      "todo_write",
    );
  } finally {
    await build.cleanup();
  }
});
