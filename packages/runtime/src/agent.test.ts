import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { type AgentEvent, runAgent, textToolCall } from "#runtime/agent";
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

test("an approved edit whose file changed before it was applied fails plainly and the run goes on", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-stale-edit-"));
  await writeFile(join(root, "notes.md"), "before\n");
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
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
      const body = (await request.json()) as { messages: { role: string }[] };
      const message = body.messages.some((m) => m.role === "tool")
        ? { role: "assistant", content: "Finished" }
        : {
            role: "assistant",
            content: "",
            tool_calls: [
              {
                function: {
                  name: "edit",
                  arguments: {
                    path: "notes.md",
                    old_string: "before",
                    new_string: "after",
                  },
                },
              },
            ],
          };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  try {
    const session = { store, state: await store.createSession(root) };
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Edit the notes" }],
      {
        workspace,
        session,
        signal: AbortSignal.timeout(5000),
        // A formatter rewrites the file while the card is open.
        approve: async () => {
          await writeFile(join(root, "notes.md"), "formatted\n");
          return true;
        },
      },
    ))
      events.push(event);
    expect(events).toContainEqual(
      expect.objectContaining({
        type: "tool_result",
        name: "edit",
        status: "failed",
        output: "File changed since edit was prepared",
      }),
    );
    expect(events.at(-1)).toMatchObject({ type: "turn_complete" });
    expect(await readFile(join(root, "notes.md"), "utf8")).toBe("formatted\n");
    const current = await store.loadSession(session.state.publicId, root);
    expect(store.listHazards(current.id, current.workspaceId)).toHaveLength(0);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("a gateway call is approved as the tool it runs, or not sent at all", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-mcp-gateway-"));
  using workspace = await Workspace.create(root);
  const calls = [
    { tool: "cortex__status", arguments: { deep: true } },
    { tool: "status" },
  ];
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
      const body = (await request.json()) as { messages: { role: string }[] };
      const step = body.messages.filter((m) => m.role === "tool").length;
      const message =
        step < calls.length
          ? {
              role: "assistant",
              content: "",
              tool_calls: [
                {
                  function: { name: "hub__call_tool", arguments: calls[step] },
                },
              ],
            }
          : { role: "assistant", content: "Finished" };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  const proposals: unknown[] = [];
  const sent: unknown[] = [];
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Check status" }],
      {
        workspace,
        signal: AbortSignal.timeout(5000),
        approve: async (proposal) => {
          proposals.push(proposal);
          return true;
        },
        mcp: [
          {
            tools: [
              {
                name: "hub__call_tool",
                inputSchema: { type: "object" },
                readOnly: false,
              },
            ],
            gateway: { server: "hub", call: "hub__call_tool" },
            async callTool(name, args) {
              sent.push({ name, args });
              return {
                transport: "success" as const,
                domain: "unknown" as const,
                evidence: "unverified" as const,
                isError: false,
                text: "ok",
              };
            },
            async close() {},
          },
        ],
      },
    ))
      events.push(event);
    expect(proposals).toEqual([
      {
        kind: "mcp",
        name: "hub__call_tool",
        argumentsJson: JSON.stringify(calls[0]),
        target: "hub__cortex__status",
        inner: "cortex__status",
        gateway: "hub",
        aliases: ["hub__cortex__cortex_status"],
      },
    ]);
    expect(sent).toEqual([{ name: "hub__call_tool", args: calls[0] }]);
    const results = events.filter((event) => event.type === "tool_result");
    expect(results.map((event) => event.status)).toEqual([
      "unverified",
      "failed",
    ]);
    expect(JSON.stringify(results[1])).toContain(
      "hub__call_tool needs a server and a tool name (or SERVER__TOOL)",
    );
  } finally {
    await server.stop(true);
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

test("an approved edit of a TypeScript file appends the diagnostics block when opted in", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-diagnostics-"));
  await writeFile(join(root, "greet.ts"), "const before = 1;\n");
  // The fixture workspace has no Biome install: the block says so, and
  // nothing is fetched or run in its place.
  process.env.LOCAL_AGENT_DIAGNOSTICS = "1";
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
    expect(result.output).toContain(
      "--- diagnostics unavailable: no @biomejs/biome install in this workspace ---",
    );
  } finally {
    delete process.env.LOCAL_AGENT_DIAGNOSTICS;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
}, 60_000);

test("without LOCAL_AGENT_DIAGNOSTICS=1 an edit carries no diagnostics block", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-diagnostics-off-"));
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

type BatchCall = { name: string; arguments: Record<string, unknown> };
type BatchBody = { messages: { role: string; tool_call_id?: string }[] };

/** Loopback Ollama: the first /api/chat answers with the whole scripted
 * tool-call batch, the second with "Done". */
async function batchRun(
  batch: BatchCall[],
  serve?: (
    request: Request,
    path: string,
  ) => Response | Promise<Response | undefined> | undefined,
) {
  const root = await mkdtemp(join(tmpdir(), "local-agent-parallel-"));
  await writeFile(join(root, "a.txt"), "alpha\n");
  await writeFile(join(root, "b.txt"), "beta\n");
  const bodies: BatchBody[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request): Promise<Response> {
      const path = new URL(request.url).pathname;
      const custom = await serve?.(request, path);
      if (custom) return custom;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await request.json()) as BatchBody;
      bodies.push(body);
      return new Response(
        `${JSON.stringify(
          bodies.length === 1
            ? {
                message: {
                  role: "assistant",
                  content: "",
                  tool_calls: batch.map((call, index) => ({
                    id: `call-${index}`,
                    function: {
                      ...call,
                      arguments: {
                        ...call.arguments,
                        // fetch stubs pass workspace-relative URLs; the
                        // fetch tool needs an absolute origin.
                        ...(typeof call.arguments.url === "string" &&
                        call.arguments.url.startsWith("/")
                          ? { url: `${server.url.origin}${call.arguments.url}` }
                          : {}),
                      },
                    },
                  })),
                },
                done: true,
              }
            : { message: { role: "assistant", content: "Done" }, done: true },
        )}\n`,
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
        signal: AbortSignal.timeout(15_000),
        approve: async () => true,
        // The scripted fetch URLs point at this loopback fixture.
        fetchPolicy: { allowPrivate: true },
      },
    ))
      events.push(event);
  } finally {
    await server.stop(true);
  }
  return {
    events,
    bodies,
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

test("a read-only batch runs in parallel: starts in call order, all results present, history in call order", async () => {
  const run = await batchRun([
    { name: "read", arguments: { path: "a.txt" } },
    { name: "list", arguments: { path: "." } },
  ]);
  try {
    const starts = run.events.filter((event) => event.type === "tool_start");
    const results = run.events.filter((event) => event.type === "tool_result");
    expect(starts.map((event) => event.name)).toEqual(["read", "list"]);
    expect(results).toHaveLength(2);
    expect(results.map((event) => event.status)).toEqual([
      "succeeded",
      "succeeded",
    ]);
    // tool_start cards all appear before any result streams back.
    expect(
      run.events.findLastIndex((event) => event.type === "tool_start"),
    ).toBeLessThan(
      run.events.findIndex((event) => event.type === "tool_result"),
    );
    // Each result is tagged with its own call's start id (completion order
    // may vary, so compare as sets).
    expect([...results.map((event) => event.id)].sort()).toEqual(
      [...starts.map((event) => event.id)].sort(),
    );
    // The next provider request sees both tool messages in call order.
    expect(
      run.bodies[1]?.messages
        .filter((message) => message.role === "tool")
        .map((message) => message.tool_call_id),
    ).toEqual(["call-0", "call-1"]);
  } finally {
    await run.cleanup();
  }
});

test("a batch reports results as they finish and messages in call order", async () => {
  const run = await batchRun(
    [
      { name: "fetch", arguments: { url: "/slow" } },
      { name: "read", arguments: { path: "a.txt" } },
    ],
    async (_request, path) => {
      if (path !== "/slow") return undefined;
      await Bun.sleep(300);
      return new Response("slow page", {
        headers: { "content-type": "text/plain" },
      });
    },
  );
  try {
    const results = run.events.filter((event) => event.type === "tool_result");
    // The quick read finishes first and says so first...
    expect(results.map((event) => event.name)).toEqual(["read", "fetch"]);
    // ...but the messages a client builds its history from keep call order,
    // the order the next request (and a session) holds.
    const messages = run.events.flatMap((event) =>
      event.type === "message" && event.message.role === "tool"
        ? [event.message.tool_call_id]
        : [],
    );
    expect(messages).toEqual(["call-0", "call-1"]);
    expect(
      run.bodies[1]?.messages
        .filter((message) => message.role === "tool")
        .map((message) => message.tool_call_id),
    ).toEqual(messages);
  } finally {
    await run.cleanup();
  }
});

test("a failing read-only call does not sink its parallel siblings", async () => {
  const run = await batchRun([
    { name: "read", arguments: { path: "missing.txt" } },
    { name: "read", arguments: { path: "a.txt" } },
  ]);
  try {
    const results = run.events.filter((event) => event.type === "tool_result");
    expect(results).toHaveLength(2);
    expect(results.map((event) => event.status).sort()).toEqual([
      "failed",
      "succeeded",
    ]);
    expect(
      results.find((event) => event.status === "succeeded")?.output,
    ).toContain("alpha");
    expect(
      results.find((event) => event.status === "failed")?.output,
    ).toContain("Workspace entry cannot be opened");
  } finally {
    await run.cleanup();
  }
});

test("a batch containing an effect call stays fully serial", async () => {
  const run = await batchRun([
    { name: "read", arguments: { path: "a.txt" } },
    {
      name: "write",
      arguments: { path: "out.txt", content: "written\n" },
    },
  ]);
  try {
    const readResult = run.events.findIndex(
      (event) => event.type === "tool_result" && event.name === "read",
    );
    const writeStart = run.events.findIndex(
      (event) => event.type === "tool_start" && event.name === "write",
    );
    expect(readResult).toBeGreaterThanOrEqual(0);
    expect(writeStart).toBeGreaterThan(readResult);
    expect(
      run.events.filter((event) => event.type === "tool_result"),
    ).toHaveLength(2);
  } finally {
    await run.cleanup();
  }
});

test("more than four parallel read-only calls are bounded at four in flight", async () => {
  // Deterministic concurrency probe: every in-flight request holds until the
  // fourth arrives (the gate then stays open), so overlap is observed
  // without wall-clock timers; a serial implementation would deadlock into
  // the run's abort timeout instead of reaching four in flight.
  let inFlight = 0;
  let maxInFlight = 0;
  let arrivals = 0;
  const fourth = Promise.withResolvers<void>();
  const run = await batchRun(
    Array.from({ length: 6 }, (_, index) => ({
      name: "fetch",
      arguments: { url: `/slow?i=${index}` },
    })),
    (_request, path) => {
      if (path !== "/slow") return undefined;
      return (async () => {
        arrivals++;
        if (arrivals >= 4) fourth.resolve();
        inFlight++;
        maxInFlight = Math.max(maxInFlight, inFlight);
        await fourth.promise;
        inFlight--;
        return new Response("ok", {
          headers: { "Content-Type": "text/plain" },
        });
      })();
    },
  );
  try {
    const results = run.events.filter((event) => event.type === "tool_result");
    expect(results).toHaveLength(6);
    expect(results.every((event) => event.status === "succeeded")).toBe(true);
    expect(maxInFlight).toBe(4);
  } finally {
    await run.cleanup();
  }
});

test("a text-only tool call is recognized only when the reply is the call", () => {
  const offered = new Set(["read", "search"]);
  for (const content of [
    '{"name": "read", "arguments": {"path": "a.txt"}}',
    '<tool_call>\n{"name": "read", "arguments": {"path": "a.txt"}}\n</tool_call>\n<tool_call>{"name": "search", "arguments": {}}</tool_call>',
    '```json\n{"name": "search", "parameters": {"pattern": "x"}}\n```',
    '[{"function": {"name": "read", "arguments": {"path": "a"}}}]',
  ])
    expect(textToolCall(content, offered), content).toMatch(/^(read|search)$/);
  for (const content of [
    'To read it, call {"name": "read", "arguments": {"path": "a"}}.',
    '{"name": "write", "arguments": {"path": "a"}}',
    '{"name": "read"}',
    "{not json",
    "Done.",
  ])
    expect(textToolCall(content, offered), content).toBeUndefined();
});

/** A loopback Ollama that answers each chat request with the next reply. */
function scripted(
  replies: { content: string; thinking?: string; tool?: string }[],
) {
  const bodies: { messages: { role: string; content: string }[] }[] = [];
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
      bodies.push((await request.json()) as (typeof bodies)[number]);
      const reply = replies.shift() ?? { content: "Done" };
      return new Response(
        `${JSON.stringify({
          message: {
            role: "assistant",
            content: reply.content,
            ...(reply.thinking ? { thinking: reply.thinking } : {}),
            ...(reply.tool
              ? {
                  tool_calls: [
                    { function: { name: reply.tool, arguments: {} } },
                  ],
                }
              : {}),
          },
          done: true,
        })}\n`,
      );
    },
  });
  return { server, bodies };
}

test("a reply that ends the turn by mistake is answered once, outside the history", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-nudge-"));
  await writeFile(join(root, "a.txt"), "alpha\n");
  using workspace = await Workspace.create(root);
  const store = await SessionStore.open(join(root, "session.sqlite"));
  const call =
    '<tool_call>{"name": "read", "arguments": {"path": "a.txt"}}</tool_call>';
  const { server, bodies } = scripted([
    { content: call },
    { content: "", tool: "list" },
    { content: "", thinking: "The answer is alpha." },
  ]);
  try {
    const session = { store, state: await store.createSession(root) };
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "what is in a.txt?" }],
      {
        workspace,
        session,
        intent: "plan",
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    // The text call got a note; the empty reply after the tool did not
    // (one note a turn), so the turn ended there.
    expect(bodies).toHaveLength(3);
    expect(bodies[1]?.messages.at(-1)).toEqual({
      role: "user",
      content:
        "Your last reply is a read call written as text, which runs nothing. To run read, call it through the tool interface. If that text was your answer, reply with nothing.",
    });
    expect(bodies[2]?.messages.at(-1)?.role).toBe("tool");
    expect(events.at(-1)?.type).toBe("turn_complete");
    // The note is never part of the conversation, stored or sent later.
    const stored = await store.loadSession(session.state.publicId, root);
    const users = stored.messages.filter((message) => message.role === "user");
    expect(users.map((message) => message.content)).toEqual([
      "what is in a.txt?",
    ]);
    expect(
      bodies[2]?.messages.some((message) =>
        message.content.includes("as text"),
      ),
    ).toBe(false);
  } finally {
    await server.stop(true);
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("a call written as text that was the answer stands: the empty reply after the note is dropped", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-nudge-answer-"));
  using workspace = await Workspace.create(root);
  const answer = '{"name": "read", "arguments": {"path": "a.txt"}}';
  const { server, bodies } = scripted([{ content: answer }, { content: "" }]);
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "show me a read call as JSON" }],
      {
        workspace,
        intent: "chat",
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    expect(bodies).toHaveLength(2);
    const last = events.at(-1);
    expect(last?.type).toBe("turn_complete");
    // The answer is the last message; no empty one follows it.
    const messages = last?.type === "turn_complete" ? last.messages : [];
    expect(messages.map((message) => [message.role, message.content])).toEqual([
      ["user", "show me a read call as JSON"],
      ["assistant", answer],
    ]);
    expect(
      events.filter(
        (event) =>
          event.type === "message" && event.message.role === "assistant",
      ),
    ).toHaveLength(1);
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("the last step gets no note: the turn completes instead of hitting the limit", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-nudge-last-"));
  using workspace = await Workspace.create(root);
  const { server, bodies } = scripted([
    { content: "", tool: "list" },
    { content: "" },
  ]);
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "list" }],
      {
        workspace,
        intent: "plan",
        maxTurns: 2,
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    expect(bodies).toHaveLength(2);
    expect(events.at(-1)?.type).toBe("turn_complete");
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("an answer left only in the reasoning is asked for once", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-nudge-think-"));
  using workspace = await Workspace.create(root);
  const { server, bodies } = scripted([
    { content: "", thinking: "It is 4." },
    { content: "2 + 2 is 4." },
  ]);
  try {
    const events: AgentEvent[] = [];
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "2 + 2?" }],
      {
        workspace,
        intent: "chat",
        signal: AbortSignal.timeout(10_000),
        approve: async () => true,
      },
    ))
      events.push(event);
    expect(bodies).toHaveLength(2);
    expect(bodies[1]?.messages.at(-1)?.content).toBe(
      "Your last reply had reasoning but no text for the user. Write your answer as the reply.",
    );
    const last = events.at(-1);
    expect(
      last?.type === "turn_complete" && last.messages.at(-1)?.content,
    ).toBe("2 + 2 is 4.");
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
