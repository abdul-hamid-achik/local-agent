import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent } from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { SessionStore } from "#runtime/session";
import { Workspace } from "#runtime/workspace";

test("an interrupted MCP call persists uncertainty without leaking transport errors or replaying", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-mcp-recovery-"));
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  let requests = 0;
  let executions = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      requests++;
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "", tool_calls: [{ function: { name: "fixture__effect", arguments: {} } }] }, done: true })}\n`,
      );
    },
  });
  try {
    const session = { store, state: await store.createSession(root) };
    const events: AgentEvent[] = [];
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
    );
    const options = {
      workspace,
      session,
      signal: AbortSignal.timeout(5000),
      approve: async () => true,
      mcp: [
        {
          tools: [
            {
              name: "fixture__effect",
              inputSchema: { type: "object" },
              readOnly: true,
            },
          ],
          async callTool(): Promise<never> {
            executions++;
            throw new Error("private structured payload: do-not-project");
          },
          async close() {},
        },
      ],
    };
    await expect(
      (async () => {
        for await (const event of runAgent(
          provider,
          [{ role: "user", content: "Run tool" }],
          options,
        ))
          events.push(event);
      })(),
    ).rejects.toThrow("Tool outcome is uncertain");
    expect(events).toContainEqual(
      expect.objectContaining({
        type: "tool_result",
        status: "outcome_unknown",
      }),
    );
    expect(JSON.stringify(events)).not.toContain("do-not-project");
    const current = await store.loadSession(session.state.publicId, root);
    expect(JSON.stringify(current)).not.toContain("do-not-project");
    expect(store.listHazards(current.id, current.workspaceId)).toHaveLength(1);
    const beforeResume = requests;
    await expect(
      runAgent(
        provider,
        [...current.messages, { role: "user", content: "Try again" }],
        options,
      ).next(),
    ).rejects.toThrow("uncertain tool outcome");
    expect(requests).toBe(beforeResume);
    expect(executions).toBe(1);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("admission rejects a cloud alias before sending conversation data", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-admission-"));
  let chatRequests = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(req) {
      const path = new URL(req.url).pathname;
      if (path === "/api/chat") {
        chatRequests++;
        return new Response("unexpected", { status: 500 });
      }
      if (path === "/api/tags")
        return Response.json({
          models: [
            { name: "fixture", size: 1000, remote_host: "https://ollama.com" },
          ],
        });
      return Response.json({
        capabilities: ["completion", "tools"],
        model_info: { "fixture.context_length": 65536 },
      });
    },
  });
  try {
    using workspace = await Workspace.create(root);
    const run = runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "private workspace prompt" }],
      {
        workspace,
        signal: AbortSignal.timeout(1000),
        approve: async () => {
          throw new Error("Approval must not run");
        },
      },
    );
    await expect(run.next()).rejects.toThrow("admission rejected");
    expect(chatRequests).toBe(0);
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test.each(["allow", "deny", "plan", "truncated", "cancel"] as const)(
  "tool authority: %s",
  async (decision) => {
    const root = await mkdtemp(join(tmpdir(), "local-agent-loop-"));
    await writeFile(join(root, "hello.txt"), "before\n");
    const requests: { messages: { role: string; content: string }[] }[] = [];
    let approvals = 0;
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(req) {
        if (new URL(req.url).pathname === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (
          new URL(req.url).pathname === "/api/show" &&
          req.method === "POST"
        ) {
          expect(await req.json()).toMatchObject({ model: "fixture" });
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 65536 },
          });
        }
        requests.push((await req.json()) as (typeof requests)[number]);
        const message =
          requests.length === 1
            ? {
                role: "assistant",
                content: "",
                tool_calls: [
                  {
                    id: "provider-edit-call",
                    function: {
                      name: "edit",
                      arguments: {
                        path: "hello.txt",
                        old_string: "before",
                        new_string: "$& after",
                      },
                    },
                  },
                ],
              }
            : { role: "assistant", content: "Finished" };
        return new Response(
          `${JSON.stringify({ message, done: decision !== "truncated" })}\n`,
          { headers: { "Content-Type": "application/x-ndjson" } },
        );
      },
    });
    try {
      using workspace = await Workspace.create(root);
      const events: AgentEvent[] = [];
      const controller = new AbortController();
      const collect = async () => {
        for await (const event of runAgent(
          resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
          new Proxy([{ role: "user" as const, content: "Edit hello.txt" }], {}),
          {
            workspace,
            intent: decision === "plan" ? "plan" : "build",
            signal: AbortSignal.any([
              controller.signal,
              AbortSignal.timeout(5000),
            ]),
            approve: async (proposal) => {
              approvals++;
              if (decision === "cancel") controller.abort();
              if (proposal.kind !== "edit")
                throw new Error("Unexpected command approval");
              expect(proposal.diff).toContain("+$& after");
              return decision === "allow";
            },
          },
        ))
          events.push(event);
      };
      if (decision === "truncated" || decision === "cancel")
        await expect(collect()).rejects.toThrow();
      else await collect();
      expect(
        events.filter((event) => event.type === "tool_start"),
      ).toHaveLength(decision === "allow" ? 1 : 0);
      expect(await readFile(join(root, "hello.txt"), "utf8")).toBe(
        decision === "allow" ? "$& after\n" : "before\n",
      );
      expect(approvals).toBe(
        decision === "allow" || decision === "deny" || decision === "cancel"
          ? 1
          : 0,
      );
      if (decision !== "truncated" && decision !== "cancel") {
        const toolMessage = events.find(
          (event) => event.type === "message" && event.message.role === "tool",
        );
        expect(toolMessage).toMatchObject({
          type: "message",
          message: { tool_call_id: "provider-edit-call" },
        });
        expect(requests[1]?.messages.at(-1)).toMatchObject({ role: "tool" });
        expect(
          JSON.parse(requests[1]?.messages.at(-1)?.content ?? "").status,
        ).toBe(decision === "allow" ? "succeeded" : "denied");
        expect(events.at(-1)).toMatchObject({ type: "turn_complete" });
      }
    } finally {
      await server.stop(true);
      await rm(root, { recursive: true, force: true });
    }
  },
);

test("an approved edit of a TypeScript file appends biome diagnostics to the tool result", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-diagnostics-"));
  await writeFile(join(root, "greet.ts"), "const before = 1;\n");
  delete process.env.LOCAL_AGENT_DIAGNOSTICS;
  let requests = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      if (new URL(req.url).pathname === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (new URL(req.url).pathname === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 65536 },
        });
      requests++;
      const first = requests === 1;
      return new Response(
        `${JSON.stringify({
          message: first
            ? {
                role: "assistant",
                content: "",
                tool_calls: [
                  {
                    id: "provider-edit-call",
                    function: {
                      name: "edit",
                      arguments: {
                        path: "greet.ts",
                        old_string: "const before = 1;",
                        new_string: "const after = 2;",
                      },
                    },
                  },
                ],
              }
            : { role: "assistant", content: "Finished" },
          done: true,
        })}\n`,
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    using workspace = await Workspace.create(root);
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Edit greet.ts" }],
      {
        workspace,
        intent: "build",
        signal: AbortSignal.timeout(30_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    const toolMessage = events.find(
      (event) => event.type === "message" && event.message.role === "tool",
    );
    if (toolMessage?.type !== "message")
      throw new Error("missing tool message");
    const { content } = toolMessage.message;
    if (typeof content !== "string") throw new Error("tool content missing");
    const result = JSON.parse(content) as { status: string; output: string };
    expect(result.status).toBe("succeeded");
    expect(result.output).toContain("Updated greet.ts");
    expect(result.output).toContain("--- diagnostics");
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
}, 60_000);

test("LOCAL_AGENT_DIAGNOSTICS=0 suppresses the diagnostics block", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-diagnostics-off-"));
  await writeFile(join(root, "greet.ts"), "const before = 1;\n");
  process.env.LOCAL_AGENT_DIAGNOSTICS = "0";
  let requests = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      if (new URL(req.url).pathname === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (new URL(req.url).pathname === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 65536 },
        });
      requests++;
      const first = requests === 1;
      return new Response(
        `${JSON.stringify({
          message: first
            ? {
                role: "assistant",
                content: "",
                tool_calls: [
                  {
                    id: "provider-edit-call",
                    function: {
                      name: "edit",
                      arguments: {
                        path: "greet.ts",
                        old_string: "const before = 1;",
                        new_string: "const after = 2;",
                      },
                    },
                  },
                ],
              }
            : { role: "assistant", content: "Finished" },
          done: true,
        })}\n`,
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    using workspace = await Workspace.create(root);
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Edit greet.ts" }],
      {
        workspace,
        intent: "build",
        signal: AbortSignal.timeout(5000),
        approve: async () => true,
      },
    ))
      events.push(event);
    const toolMessage = events.find(
      (event) => event.type === "message" && event.message.role === "tool",
    );
    if (toolMessage?.type !== "message")
      throw new Error("missing tool message");
    const { content } = toolMessage.message;
    if (typeof content !== "string") throw new Error("tool content missing");
    const result = JSON.parse(content) as { output: string };
    expect(result.output).toBe(
      "Updated greet.ts. Verification has not been run.",
    );
  } finally {
    delete process.env.LOCAL_AGENT_DIAGNOSTICS;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
