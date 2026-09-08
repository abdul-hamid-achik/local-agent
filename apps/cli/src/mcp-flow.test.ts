import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { SessionStore } from "@local-agent/runtime";
import { expect, test } from "vitest";

const mainPath = fileURLToPath(new URL("./main.ts", import.meta.url));
const fixturePath = fileURLToPath(
  new URL(
    "../../../packages/runtime/src/fixtures/mcp_stdio.ts",
    import.meta.url,
  ),
);

async function runMcpCli(
  root: string,
  server: ReturnType<typeof Bun.serve>,
  flags: string[],
) {
  const configPath = join(root, "mcp.json");
  await writeFile(
    configPath,
    JSON.stringify([
      {
        name: "fixture",
        transport: {
          kind: "stdio",
          command: process.execPath,
          args: [fixturePath],
          cwd: root,
          env: {},
        },
      },
    ]),
  );
  const dbPath = join(root, "session.sqlite");
  const child = Bun.spawn(
    [
      process.execPath,
      mainPath,
      "--model",
      "fixture",
      "--mode",
      "headless",
      "--workspace",
      root,
      "--session-db",
      dbPath,
      "--mcp-config",
      configPath,
      "--prompt",
      "Use the fixture echo tool",
      "--json",
      ...flags,
    ],
    {
      env: {
        PATH: process.env.PATH ?? "",
        OLLAMA_HOST: server.url.origin,
        MCP_SECRET: "process-secret-must-not-leak",
      },
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { stdout, stderr, code, dbPath };
}

function jsonEvents(stdout: string): Record<string, unknown>[] {
  return stdout
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line) as Record<string, unknown>);
}

function fixtureOllama() {
  return Bun.serve({
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
      const body = (await request.json()) as {
        messages: { role: string }[];
      };
      const toolResults = body.messages.filter(
        (message) => message.role === "tool",
      ).length;
      const message =
        toolResults === 0
          ? {
              role: "assistant",
              content: "",
              tool_calls: [
                {
                  function: {
                    name: "fixture__echo",
                    arguments: { text: "hello" },
                  },
                },
              ],
            }
          : { role: "assistant", content: "Finished" };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
}

test("MCP approval is independent from command approval", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-mcp-flow-"));
  const server = fixtureOllama();
  try {
    const result = await runMcpCli(root, server, ["--approve-commands"]);
    expect(result.code).toBe(0);
    expect(result.stderr).toBe("");
    expect(result.stdout).toContain('"status":"denied"');
    expect(result.stdout).not.toContain("process-secret-must-not-leak");
    expect(result.stdout).not.toContain("do-not-project");
    const store = await SessionStore.open(result.dbPath);
    try {
      const state = (await store.listSessions(root))[0];
      expect(state).toBeDefined();
      if (!state) throw new Error("Missing durable MCP session");
      expect(JSON.stringify(state)).not.toContain(
        "process-secret-must-not-leak",
      );
      expect(JSON.stringify(state)).not.toContain("do-not-project");
      expect(store.listHazards(state.id, state.workspaceId)).toEqual([]);
    } finally {
      store.close();
    }
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("approved MCP calls are reported unverified and redact fixture secrets", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-mcp-flow-"));
  const server = fixtureOllama();
  try {
    const result = await runMcpCli(root, server, ["--approve-mcp"]);
    expect(result.code).toBe(0);
    expect(result.stderr).toBe("");
    const toolResult = jsonEvents(result.stdout).find(
      (event) => event.type === "tool_result",
    );
    expect(toolResult?.status).toBe("unverified");
    const projected = JSON.parse(String(toolResult?.output)) as {
      transport: string;
      domain: string;
      evidence: string;
      text: string;
    };
    expect(projected).toMatchObject({
      transport: "success",
      domain: "unknown",
      evidence: "unverified",
    });
    expect(result.stdout).not.toContain("process-secret-must-not-leak");
    expect(result.stdout).not.toContain("do-not-project");
    const store = await SessionStore.open(result.dbPath);
    try {
      const state = (await store.listSessions(root))[0];
      expect(state).toBeDefined();
      const serialized = JSON.stringify(state);
      expect(serialized).toContain("unverified");
      expect(serialized).not.toContain("process-secret-must-not-leak");
      expect(serialized).not.toContain("do-not-project");
    } finally {
      store.close();
    }
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("plan mode denies even read-only MCP annotations without invoking the tool", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-mcp-flow-"));
  const server = fixtureOllama();
  try {
    const result = await runMcpCli(root, server, ["--intent", "plan"]);
    expect(result.code).toBe(0);
    expect(result.stderr).toBe("");
    expect(result.stdout).toContain('"status":"denied"');
    expect(result.stdout).not.toContain("process-secret-must-not-leak");
    expect(result.stdout).not.toContain("do-not-project");
    const store = await SessionStore.open(result.dbPath);
    try {
      const state = (await store.listSessions(root))[0];
      expect(state).toBeDefined();
      expect(JSON.stringify(state)).not.toContain(
        "process-secret-must-not-leak",
      );
      expect(JSON.stringify(state)).not.toContain("do-not-project");
    } finally {
      store.close();
    }
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
