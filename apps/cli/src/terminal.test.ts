import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type McpConnection, Workspace } from "@local-agent/runtime";
import {
  createNodeOps,
  exitInjectionKey,
  rendererInjectionKey,
  terminalOptions,
} from "@local-agent/vue-tui";
import { createTestRenderer } from "@local-agent/vue-tui/testing";
import { afterEach, expect, test } from "vitest";
import { createRenderer } from "vue";
import { createTerminalRoot } from "#cli/terminal";

const cleanups: Array<() => Promise<void>> = [];

afterEach(async () => {
  for (const cleanup of cleanups.splice(0)) await cleanup();
});

test.each(["edit", "mcp"])(
  "native terminal denies %s approval and cleans up",
  async (kind) => {
    let mcpCalls = 0;
    const mcp: McpConnection = {
      tools: [
        {
          name: "fixture__echo",
          inputSchema: { type: "object", properties: {} },
          readOnly: true,
        },
      ],
      async callTool() {
        mcpCalls++;
        return {
          transport: "success",
          domain: "unknown",
          evidence: "unverified",
          isError: false,
          text: "unexpected",
        };
      },
      async close() {},
    };
    const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-test-"));
    await writeFile(join(root, "notes.txt"), "before\n");
    const workspace = await Workspace.create(root);
    const requests: Array<{
      messages?: Array<{ role?: string; content?: string }>;
    }> = [];
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        if (
          request.method === "GET" &&
          new URL(request.url).pathname === "/api/tags"
        )
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (
          request.method === "POST" &&
          new URL(request.url).pathname === "/api/show"
        )
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 16384 },
          });
        const body = (await request.json()) as (typeof requests)[number];
        requests.push(body);
        const hasToolResult = body.messages?.some(
          (message) => message.role === "tool",
        );
        const message = hasToolResult
          ? { role: "assistant", content: "edit denied" }
          : {
              role: "assistant",
              content: "",
              tool_calls: [
                {
                  function: {
                    name: kind === "edit" ? "edit" : "fixture__echo",
                    arguments: {
                      path: "notes.txt",
                      old_string: "before",
                      new_string: "after",
                    },
                  },
                },
              ],
            };
        return new Response(
          `${JSON.stringify({ message, done: false })}\n${JSON.stringify({ message: { role: "assistant", content: "" }, done: true })}\n`,
          { headers: { "content-type": "application/x-ndjson" } },
        );
      },
    });
    const terminal = await createTestRenderer({
      ...terminalOptions("fullscreen"),
      width: 80,
      height: 24,
      kittyKeyboard: true,
    });
    const signal = new AbortController();
    const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
      createTerminalRoot(
        {
          type: "ollama",
          base_url: server.url.origin,
          model: "fixture",
          context_size: 16384,
        },
        "fullscreen",
        signal.signal,
        workspace,
        "build",
        undefined,
        kind === "mcp" ? [mcp] : [],
      ),
    );
    app.provide(rendererInjectionKey, terminal.renderer);
    app.provide(exitInjectionKey, () => terminal.renderer.destroy());
    app.mount(terminal.renderer.root);
    cleanups.push(async () => {
      signal.abort();
      app.unmount();
      terminal.renderer.destroy();
      server.stop(true);
      workspace.close();
      await rm(root, { recursive: true, force: true });
    });

    await terminal.flush();
    await terminal.mockInput.pasteBracketedText("edit\nnext line");
    terminal.mockInput.pressEnter();
    await expect
      .poll(async () => {
        await terminal.flush();
        return terminal.captureCharFrame();
      })
      .toContain(kind === "edit" ? "Edit notes.txt" : "Call fixture__echo");
    terminal.mockInput.pressKey("n");
    await expect
      .poll(async () => {
        await terminal.flush();
        return terminal.captureCharFrame();
      })
      .toContain("edit denied");

    await terminal.mockInput.pasteBracketedText("next\nline");
    terminal.mockInput.pressEnter();
    await terminal.waitForFrame((frame) => frame.includes("You"));
    expect(terminal.captureCharFrame()).toContain("edit denied");
    expect(terminal.captureCharFrame()).toContain("next line");
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("before\n");
    await expect.poll(() => requests.length).toBeGreaterThanOrEqual(3);
    expect(
      requests[1]?.messages?.some((message) => message.role === "tool"),
    ).toBe(true);
    expect(
      requests[2]?.messages?.some((message) =>
        message.content?.includes("next\nline"),
      ),
    ).toBe(true);
    expect(mcpCalls).toBe(0);
  },
);

test("fullscreen mode can switch intent from the composer", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-mode-"));
  const workspace = await Workspace.create(root);
  const terminal = await createTestRenderer({
    ...terminalOptions("fullscreen"),
    width: 100,
    height: 24,
    kittyKeyboard: true,
  });
  const signal = new AbortController();
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
    createTerminalRoot(
      {
        type: "ollama",
        base_url: "http://127.0.0.1:11434",
        model: "fixture",
        context_size: 16384,
      },
      "fullscreen",
      signal.signal,
      workspace,
      "build",
    ),
  );
  app.provide(rendererInjectionKey, terminal.renderer);
  app.provide(exitInjectionKey, () => terminal.renderer.destroy());
  app.mount(terminal.renderer.root);
  cleanups.push(async () => {
    signal.abort();
    app.unmount();
    terminal.renderer.destroy();
    workspace.close();
    await rm(root, { recursive: true, force: true });
  });

  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("/mode plan");
  terminal.mockInput.pressEnter();
  await terminal.waitForFrame(
    (frame) => frame.includes("Mode") && frame.includes("plan"),
  );
});

test.each([
  ["/help", "/mode plan|build"],
  ["/mcp", "MCP servers: 0"],
  ["/mode banana", "Unknown mode: banana"],
  ["/theme dark", "/theme and /mouse are available in the Tuimark client"],
  ["/mouse on", "/theme and /mouse are available in the Tuimark client"],
] as const)(
  "handles %s locally without contacting the model",
  async (command, expected) => {
    const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-command-"));
    const workspace = await Workspace.create(root);
    const terminal = await createTestRenderer({
      ...terminalOptions("fullscreen"),
      width: 100,
      height: 24,
      kittyKeyboard: true,
    });
    const signal = new AbortController();
    const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
      createTerminalRoot(
        {
          type: "ollama",
          base_url: "http://127.0.0.1:11434",
          model: "fixture",
          context_size: 16384,
        },
        "fullscreen",
        signal.signal,
        workspace,
        "build",
      ),
    );
    app.provide(rendererInjectionKey, terminal.renderer);
    app.provide(exitInjectionKey, () => terminal.renderer.destroy());
    app.mount(terminal.renderer.root);
    cleanups.push(async () => {
      signal.abort();
      app.unmount();
      terminal.renderer.destroy();
      workspace.close();
      await rm(root, { recursive: true, force: true });
    });

    await terminal.flush();
    await terminal.mockInput.pasteBracketedText(command);
    terminal.mockInput.pressEnter();
    await terminal.waitForFrame((frame) => frame.includes(expected));
  },
);

test("mode switch changes the Ollama tool catalog", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-tools-"));
  const workspace = await Workspace.create(root);
  const requests: Array<{ tools?: Array<{ function?: { name?: string } }> }> =
    [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const pathname = new URL(request.url).pathname;
      if (request.method === "GET" && pathname === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (request.method === "POST" && pathname === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      requests.push((await request.json()) as (typeof requests)[number]);
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "ok" }, done: true })}\n`,
        { headers: { "content-type": "application/x-ndjson" } },
      );
    },
  });
  const terminal = await createTestRenderer({
    ...terminalOptions("fullscreen"),
    width: 100,
    height: 24,
    kittyKeyboard: true,
  });
  const signal = new AbortController();
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
    createTerminalRoot(
      {
        type: "ollama",
        base_url: server.url.origin,
        model: "fixture",
        context_size: 16384,
      },
      "fullscreen",
      signal.signal,
      workspace,
      "build",
    ),
  );
  app.provide(rendererInjectionKey, terminal.renderer);
  app.provide(exitInjectionKey, () => terminal.renderer.destroy());
  app.mount(terminal.renderer.root);
  cleanups.push(async () => {
    signal.abort();
    app.unmount();
    terminal.renderer.destroy();
    server.stop(true);
    workspace.close();
    await rm(root, { recursive: true, force: true });
  });

  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("/mode plan");
  terminal.mockInput.pressEnter();
  await terminal.waitForFrame(
    (frame) => frame.includes("Mode") && frame.includes("plan"),
  );
  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("inspect");
  terminal.mockInput.pressEnter();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0]?.tools?.map((tool) => tool.function?.name)).toEqual([
    "read",
    "list",
    "search",
  ]);

  // The command is handled before inference and the composer refocuses after the turn.
  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("/mode build");
  terminal.mockInput.pressEnter();
  await terminal.waitForFrame(
    (frame) => frame.includes("Mode") && frame.includes("build"),
  );
  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("build");
  terminal.mockInput.pressEnter();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]?.tools?.map((tool) => tool.function?.name)).toEqual([
    "read",
    "list",
    "search",
    "edit",
    "write",
    "run_command",
  ]);
});
