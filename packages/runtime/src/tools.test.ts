import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
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
  tools: { function: { name: string; description: string } }[];
};

/** A loopback Ollama that answers each step with the next scripted tool call, then "Done". */
async function session(
  calls: Call[],
  intent: "plan" | "build",
  approve: (proposal: ApprovalRequest) => boolean,
) {
  const root = await mkdtemp(join(tmpdir(), "local-agent-tools-"));
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
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
  const proposals: ApprovalRequest[] = [];
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
        approve: async (proposal) => {
          proposals.push(proposal);
          return approve(proposal);
        },
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
    proposals,
    results,
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

test("build intent offers every tool with a relative-path example; the prompt states the rules", async () => {
  const run = await session([], "build", () => true);
  try {
    const body = run.bodies[0] as Body;
    expect(body.tools.map((tool) => tool.function.name)).toEqual([
      "read",
      "list",
      "search",
      "fetch",
      "web_search",
      "edit",
      "write",
      "run_command",
      "todo_write",
    ]);
    for (const name of ["read", "edit", "write"])
      expect(
        body.tools.find((tool) => tool.function.name === name)?.function
          .description,
      ).toContain("src/stats.ts");
    const command = body.tools.find(
      (tool) => tool.function.name === "run_command",
    );
    expect(command?.function.description).toContain('"argv": ["bun", "test"');
    expect(command?.function.description).toContain("There is no shell");
    expect(body.messages[0]?.content).toContain(
      "relative to the workspace root",
    );
  } finally {
    await run.cleanup();
  }
});

test("plan intent offers the read-only tools and denies writes", async () => {
  const run = await session(
    [{ name: "write", arguments: { path: "a.txt", content: "x" } }],
    "plan",
    () => true,
  );
  try {
    expect(run.bodies[0]?.tools.map((tool) => tool.function.name)).toEqual([
      "read",
      "list",
      "search",
      "fetch",
      "web_search",
      "todo_write",
    ]);
    expect(run.results.map((result) => result.status)).toEqual(["denied"]);
    expect(run.proposals).toEqual([]);
    expect(await Bun.file(join(run.root, "a.txt")).exists()).toBe(false);
  } finally {
    await run.cleanup();
  }
});

test("list and search need no approval; write and run_command do, and show what is approved", async () => {
  const run = await session(
    [
      { name: "list", arguments: {} },
      { name: "list", arguments: { path: "src" } },
      {
        name: "search",
        arguments: { pattern: "mean", regex: "false", recursive: true },
      },
      { name: "read", arguments: { path: "/src/stats.ts" } },
      {
        name: "write",
        arguments: { path: "src/new.ts", content: "export {};\n" },
      },
      {
        name: "run_command",
        arguments: { argv: 'bun test "my tests/a b.ts"' },
      },
      { name: "run_command", arguments: { argv: "find . | head" } },
    ],
    "build",
    (proposal) => proposal.kind === "write",
  );
  try {
    expect(run.results.map((result) => [result.name, result.status])).toEqual([
      ["list", "succeeded"],
      ["list", "succeeded"],
      ["search", "succeeded"],
      ["read", "failed"],
      ["write", "succeeded"],
      ["run_command", "denied"],
      ["run_command", "failed"],
    ]);
    expect(run.results[0]?.output).toBe("src/");
    expect(run.results[1]?.output).toBe("stats.ts");
    expect(run.results[2]?.output).toBe(
      "src/stats.ts:1: export const mean = 1;",
    );
    expect(run.results[3]?.output).toContain('Use "src/stats.ts"');
    expect(run.results[4]?.output).toContain("Created src/new.ts");
    expect(await readFile(join(run.root, "src", "new.ts"), "utf8")).toBe(
      "export {};\n",
    );
    // Only the write and the commands asked; the command shows its parsed argv.
    expect(run.proposals.map((proposal) => proposal.kind)).toEqual([
      "write",
      "command",
    ]);
    expect(run.proposals[0]).toMatchObject({
      created: true,
      path: "src/new.ts",
    });
    expect(run.proposals[1]).toMatchObject({
      argv: ["bun", "test", "my tests/a b.ts"],
    });
    // The shell string was refused before any approval, with the reason.
    expect(run.results[6]?.output).toContain("no shell");
  } finally {
    await run.cleanup();
  }
});

test("an absolute path inside the workspace is accepted and edited by its relative form", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-abs-"));
  try {
    await mkdir(join(root, "src"));
    await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
    using workspace = await Workspace.create(root);
    const absolute = join(workspace.root, "src", "stats.ts");
    expect(await workspace.read(absolute)).toContain("mean");
    expect((await workspace.prepareEdit(absolute, "1", "2")).path).toBe(
      "src/stats.ts",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
