import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent } from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Step = { calls?: Call[]; text?: string };
type Body = {
  messages: { role: string; content: string }[];
  tools: { function: { name: string } }[];
};

const reply = (step: Step) =>
  new Response(
    `${JSON.stringify({
      message: {
        role: "assistant",
        content: step.text ?? (step.calls?.length ? "" : "Done"),
        ...(step.calls?.length
          ? {
              tool_calls: step.calls.map((call) => ({
                function: call,
              })),
            }
          : {}),
      },
      done: true,
    })}\n`,
  );

/**
 * A loopback Ollama with two scripted /api/chat sequences: the parent (its
 * requests offer the `task` tool) and the child (they do not). The parent
 * script falls back to a plain "Done" answer once it runs out of steps.
 */
async function harness(options: {
  parent: Step[];
  child: (index: number) => Step;
  onChildRequest?: (index: number) => void;
  signal?: AbortSignal;
  intent?: "plan" | "build" | "chat";
}) {
  const root = await mkdtemp(join(tmpdir(), "local-agent-task-"));
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
  const signal = options.signal ?? AbortSignal.timeout(10_000);
  const parentBodies: Body[] = [];
  const childBodies: Body[] = [];
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
      const isChild = !body.tools.some((tool) => tool.function.name === "task");
      const index = isChild
        ? childBodies.push(body) - 1
        : parentBodies.push(body) - 1;
      if (isChild) options.onChildRequest?.(index);
      return reply(
        isChild ? options.child(index) : (options.parent[index] ?? {}),
      );
    },
  });
  const events: AgentEvent[] = [];
  let runError: unknown;
  try {
    using workspace = await Workspace.create(root);
    try {
      for await (const event of runAgent(
        resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
        [{ role: "user", content: "go" }],
        {
          workspace,
          signal,
          intent: options.intent,
          approve: async () => false,
        },
      ))
        events.push(event);
    } catch (error) {
      runError = error;
    }
  } finally {
    await server.stop(true);
  }
  const results = events.flatMap((event) =>
    event.type === "tool_result" ? [event] : [],
  );
  return {
    root,
    events,
    parentBodies,
    childBodies,
    results,
    runError,
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

const list = (path?: string): Call => ({
  name: "list",
  arguments: path ? { path } : {},
});

test("task runs a read-only subagent; its calls and final answer surface only in the result", async () => {
  const run = await harness({
    parent: [
      {
        calls: [
          {
            name: "task",
            arguments: {
              prompt: "Find the exported value in src/stats.ts",
            },
          },
        ],
      },
    ],
    child: (index) =>
      index === 0
        ? { calls: [list("src")] }
        : index === 1
          ? { calls: [{ name: "read", arguments: { path: "src/stats.ts" } }] }
          : { text: "The value is 1." },
  });
  try {
    // The child drives its own /api/chat sequence, distinct from the parent's.
    expect(run.childBodies).toHaveLength(3);
    expect(run.parentBodies).toHaveLength(2);
    // The child is offered only read-only inspection tools: no effects,
    // no todo_write, no task (no recursion).
    for (const body of run.childBodies)
      expect(body.tools.map((tool) => tool.function.name)).toEqual([
        "read",
        "read_image",
        "list",
        "search",
        "fetch",
        "web_search",
      ]);
    // Child tool calls were observed: each later request carries the
    // previous call's tool result.
    expect(run.childBodies[1]?.messages.at(-1)).toMatchObject({
      role: "tool",
      tool_name: "list",
    });
    expect(run.childBodies[2]?.messages.at(-1)).toMatchObject({
      role: "tool",
      tool_name: "read",
    });
    // The prompt is the only conversation the child sees.
    expect(
      run.childBodies[0]?.messages.filter((m) => m.role === "user"),
    ).toEqual([
      { role: "user", content: "Find the exported value in src/stats.ts" },
    ]);
    const task = run.results.find((result) => result.name === "task");
    expect(task?.status).toBe("succeeded");
    expect(task?.output).toBe(
      "subagent finished after 3 turns\n\nThe value is 1.",
    );
  } finally {
    await run.cleanup();
  }
});

test("a subagent that calls task anyway is refused, not nested", async () => {
  const run = await harness({
    parent: [
      {
        calls: [{ name: "task", arguments: { prompt: "Look around" } }],
      },
    ],
    child: (index) =>
      index === 0
        ? { calls: [{ name: "task", arguments: { prompt: "Nest deeper" } }] }
        : { text: "Nothing to nest." },
  });
  try {
    // Two child requests and no grandchild: the nested task never ran.
    expect(run.childBodies).toHaveLength(2);
    const refused = run.childBodies[1]?.messages.at(-1);
    expect(refused).toMatchObject({ role: "tool", tool_name: "task" });
    expect(JSON.parse(refused?.content ?? "")).toEqual({
      status: "failed",
      output:
        "task is not available here; use one of: read, read_image, list, search, fetch, web_search",
    });
  } finally {
    await run.cleanup();
  }
});

test("the subagent stops at max_turns and reports the truncation", async () => {
  const run = await harness({
    parent: [
      {
        calls: [{ name: "task", arguments: { prompt: "loop", max_turns: 2 } }],
      },
    ],
    child: () => ({ calls: [list()] }),
  });
  try {
    expect(run.childBodies).toHaveLength(2);
    const task = run.results.find((result) => result.name === "task");
    expect(task?.output).toBe(
      "subagent finished after 2 turns (stopped at the turn limit)\n\n(no final answer)",
    );
  } finally {
    await run.cleanup();
  }
});

test("the subagent stops when it exceeds its tool-call budget", async () => {
  const run = await harness({
    parent: [
      {
        calls: [
          { name: "task", arguments: { prompt: "fan out", max_turns: 2 } },
        ],
      },
    ],
    // Budget = max_turns * 4 = 8 tool calls. Each child turn asks for five
    // list calls: the first batch executes (5), the second would reach 10
    // and is refused before execution.
    child: () => ({ calls: [list(), list(), list(), list(), list()] }),
  });
  try {
    expect(run.childBodies).toHaveLength(2);
    expect(
      run.childBodies[1]?.messages.filter((m) => m.role === "tool"),
    ).toHaveLength(5);
    const task = run.results.find((result) => result.name === "task");
    expect(task?.output).toBe(
      "subagent finished after 2 turns (stopped: tool-call budget exceeded)\n\n(no final answer)",
    );
  } finally {
    await run.cleanup();
  }
});

test("aborting mid-subagent returns the partial result, labeled", async () => {
  const controller = new AbortController();
  const run = await harness({
    signal: controller.signal,
    parent: [{ calls: [{ name: "task", arguments: { prompt: "slow work" } }] }],
    child: () => ({ calls: [list()] }),
    onChildRequest: (index) => {
      if (index === 1) controller.abort();
    },
  });
  try {
    const task = run.results.find((result) => result.name === "task");
    expect(task?.output).toBe(
      "subagent finished after 1 turn (aborted; partial result)\n\n(no final answer)",
    );
    // The aborted parent run itself does not finish normally.
    expect(run.runError).toBeInstanceOf(Error);
  } finally {
    await run.cleanup();
  }
});

test("chat offers task, and two task calls in one reply run as parallel read-only subagents", async () => {
  const run = await harness({
    intent: "chat",
    parent: [
      {
        calls: [
          { name: "task", arguments: { prompt: "Research topic A" } },
          { name: "task", arguments: { prompt: "Research topic B" } },
        ],
      },
    ],
    child: () => ({ text: "Found it." }),
  });
  try {
    // Chat is offered task (and the research set, still no todo_write or effects).
    expect(run.parentBodies[0]?.tools.map((t) => t.function.name)).toEqual([
      "read",
      "read_image",
      "list",
      "search",
      "fetch",
      "web_search",
      "task",
    ]);
    // The model is told what task is for, and that it must report only calls it made.
    const system = run.parentBodies[0]?.messages[0]?.content ?? "";
    expect(system).toContain("several task calls in one reply run in parallel");
    expect(system).toContain("Say only what your tool calls did");
    // Each child is a fresh read-only turn: no task, no todo_write, no effects.
    expect(run.childBodies).toHaveLength(2);
    for (const body of run.childBodies)
      expect(body.tools.map((tool) => tool.function.name)).toEqual([
        "read",
        "read_image",
        "list",
        "search",
        "fetch",
        "web_search",
      ]);
    expect(run.results.map((r) => [r.name, r.status])).toEqual([
      ["task", "succeeded"],
      ["task", "succeeded"],
    ]);
    // Parallel: both subagents had started before either one reported.
    const kinds = run.events
      .filter((e) => e.type === "tool_start" || e.type === "tool_result")
      .map((e) => e.type);
    expect(kinds).toEqual([
      "tool_start",
      "tool_start",
      "tool_result",
      "tool_result",
    ]);
  } finally {
    await run.cleanup();
  }
});

test("chat refuses todo_write: only task joined the research set", async () => {
  const run = await harness({
    intent: "chat",
    parent: [
      {
        calls: [
          {
            name: "todo_write",
            arguments: { todos: [{ content: "x", status: "pending" }] },
          },
        ],
      },
    ],
    child: () => ({ text: "unused" }),
  });
  try {
    expect(run.childBodies).toHaveLength(0);
    expect(run.results[0]).toMatchObject({
      name: "todo_write",
      status: "failed",
    });
    expect(run.results[0]?.output).toContain("not available here");
  } finally {
    await run.cleanup();
  }
});
