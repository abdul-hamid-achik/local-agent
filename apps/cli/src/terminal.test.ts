import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  appendRule,
  MAX_MESSAGE_CHARS,
  type McpConnection,
  Workspace,
} from "@local-agent/runtime";
import {
  createNodeOps,
  exitInjectionKey,
  rendererInjectionKey,
  terminalOptions,
} from "@local-agent/vue-tui";
import { createTestRenderer } from "@local-agent/vue-tui/testing";
import { afterEach, expect, test, vi } from "vitest";
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

test("the workspace's rules apply in the Vue client: allow skips the prompt, deny refuses", async () => {
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
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-rules-"));
  await writeFile(join(root, "notes.txt"), "before\n");
  await appendRule(root, {
    effect: "allow",
    tool: "edit",
    pattern: "/notes.txt",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "mcp",
    pattern: "fixture__*",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "fetch",
    pattern: "domain:blocked.example",
  });
  const workspace = await Workspace.create(root);
  const requests: Array<{
    messages?: Array<{ role?: string; content?: string }>;
  }> = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (request.method === "GET" && path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (request.method === "POST" && path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await request.json()) as (typeof requests)[number];
      requests.push(body);
      const message = body.messages?.some((m) => m.role === "tool")
        ? { role: "assistant", content: "rules applied" }
        : {
            role: "assistant",
            content: "",
            tool_calls: [
              {
                function: {
                  name: "edit",
                  arguments: {
                    path: "notes.txt",
                    old_string: "before",
                    new_string: "after",
                  },
                },
              },
              { function: { name: "fixture__echo", arguments: {} } },
              {
                function: {
                  name: "fetch",
                  arguments: { url: "https://blocked.example/x" },
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
      [mcp],
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
  await terminal.mockInput.pasteBracketedText("go");
  terminal.mockInput.pressEnter();
  const frames: string[] = [];
  await expect
    .poll(async () => {
      await terminal.flush();
      frames.push(terminal.captureCharFrame());
      return frames.at(-1);
    })
    .toContain("rules applied");
  expect(frames.some((frame) => frame.includes("Edit notes.txt"))).toBe(false);
  expect(frames.some((frame) => frame.includes("Call fixture__echo"))).toBe(
    false,
  );
  expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("after\n");
  expect(mcpCalls).toBe(0);
  const results = requests[1]?.messages?.filter(
    (message) => message.role === "tool",
  );
  expect(results).toHaveLength(3);
  expect(results?.[2]?.content).toContain(
    "blocked.example is denied by rule: domain:blocked.example",
  );
});

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
    (frame) => frame.includes("Mode") && frame.includes("Read only"),
  );
  // Chat cannot edit, write or run commands: the sidebar does not say it can.
  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("/mode chat");
  terminal.mockInput.pressEnter();
  const frame = await terminal.waitForFrame((text) =>
    text.includes("Read · Research · MCP asks"),
  );
  expect(frame).not.toContain("Edit · Write");
});

test.each([
  ["/help", "/mode plan|build|chat"],
  ["/mcp", "MCP servers: 0"],
  ["/mode banana", "Unknown mode: banana"],
  // A paste reaches the status line as the composer took it: without the
  // sequences it carried.
  ["/mode a\u001b]0;pwned\u0007b", "Unknown mode: ab"],
  [
    "/theme dark",
    "/theme, /mouse and /copy are available in the Tuimark client",
  ],
  ["/mouse on", "/theme, /mouse and /copy are available in the Tuimark client"],
  [
    "/copy code",
    "/theme, /mouse and /copy are available in the Tuimark client",
  ],
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

test("a send past what a message holds stays in the composer", async () => {
  let requests = 0;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      requests++;
      return new Response("no", { status: 500 });
    },
  });
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-long-"));
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
        base_url: `http://127.0.0.1:${server.port}`,
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
    server.stop(true);
    await rm(root, { recursive: true, force: true });
  });

  await terminal.flush();
  await terminal.mockInput.pasteBracketedText(
    "x".repeat(MAX_MESSAGE_CHARS + 1),
  );
  terminal.mockInput.pressEnter();
  await terminal.waitForFrame((frame) =>
    frame.includes("Not sent: 1,000,001 characters"),
  );
  expect(requests).toBe(0);
});

test.each(["fullscreen", "inline"] as const)(
  "a tool name the model chose reaches the %s screen without its escape sequences",
  async (mode) => {
    // An OSC title sequence, a bell and a line break inside the name of a
    // tool that does not exist: the run reports it as an error under that name.
    const name = "probe\u001b]0;pwned\u0007\u001b[2Jtool\nforged";
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
        const body = (await request.json()) as {
          messages?: Array<{ role?: string }>;
        };
        const message = body.messages?.some((entry) => entry.role === "tool")
          ? { role: "assistant", content: "finished" }
          : {
              role: "assistant",
              content: "",
              tool_calls: [{ function: { name, arguments: {} } }],
            };
        return new Response(
          `${JSON.stringify({ message, done: false })}\n${JSON.stringify({ message: { role: "assistant", content: "" }, done: true })}\n`,
          { headers: { "content-type": "application/x-ndjson" } },
        );
      },
    });
    const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-name-"));
    const workspace = await Workspace.create(root);
    const terminal = await createTestRenderer({
      ...terminalOptions(mode),
      width: 100,
      height: 30,
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
        mode,
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
      server.stop(true);
      await rm(root, { recursive: true, force: true });
    });

    // Inline writes the run to scrollback; fullscreen draws it.
    let written = "";
    const stdout = vi
      .spyOn(process.stdout, "write")
      .mockImplementation((chunk: string | Uint8Array) => {
        written += String(chunk);
        return true;
      });
    cleanups.push(async () => stdout.mockRestore());

    await terminal.flush();
    await terminal.mockInput.typeText("go");
    terminal.mockInput.pressEnter();
    // Two model turns go over loopback first: the wait is in real time,
    // which render passes are not.
    let shown = "";
    for (let i = 0; i < 1500 && !shown.includes("finished"); i++) {
      await new Promise((resolve) => setTimeout(resolve, 10));
      await terminal.flush();
      shown = written + terminal.captureCharFrame();
    }
    expect(shown).toContain("finished");
    // On one line: the break in the name is a space.
    expect(shown).toContain(
      mode === "inline"
        ? "\nprobetool forged: failed\n"
        : "Tool · probetool forged",
    );
    expect(shown).not.toContain("pwned");
    // biome-ignore lint/suspicious/noControlCharactersInRegex: the screen must hold none
    expect(shown).not.toMatch(/[\u0000-\u0008\u000b-\u001f\u007f]/u);
  },
  // Past the 15 s the loop above may wait, so its own message is what fails.
  20_000,
);

test("mode switch changes the Ollama tool catalog", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-tools-"));
  const workspace = await Workspace.create(root);
  const requests: Array<{
    tools?: Array<{ function?: { name?: string } }>;
    messages?: Array<{ role: string; content: string }>;
  }> = [];
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
    "read_image",
    "list",
    "search",
    "fetch",
    "web_search",
    "task",
    "todo_write",
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
    "read_image",
    "list",
    "search",
    "fetch",
    "web_search",
    "task",
    "edit",
    "write",
    "run_command",
    "todo_write",
  ]);

  // A line that starts with a path is a message, not an unknown command.
  await terminal.flush();
  await terminal.mockInput.pasteBracketedText("/tmp/a.log is empty");
  terminal.mockInput.pressEnter();
  await expect.poll(() => requests.length).toBe(3);
  expect(requests[2]?.messages?.at(-1)).toMatchObject({
    role: "user",
    content: "/tmp/a.log is empty",
  });
});

test.each(["fullscreen", "inline"] as const)(
  "a prompt and its enter in one read are sent from the %s screen",
  async (mode) => {
    const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-burst-"));
    const workspace = await Workspace.create(root);
    const requests: Array<{
      messages?: Array<{ role: string; content: string }>;
    }> = [];
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname;
        if (request.method === "GET" && path === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (request.method === "POST" && path === "/api/show")
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 16384 },
          });
        requests.push(await request.json());
        return new Response(
          `${JSON.stringify({ message: { role: "assistant", content: "sent once" }, done: true })}\n`,
          { headers: { "content-type": "application/x-ndjson" } },
        );
      },
    });
    const terminal = await createTestRenderer({
      ...terminalOptions(mode),
      width: 100,
      height: 30,
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
        mode,
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
    // Inline writes the run to scrollback; fullscreen draws it.
    let written = "";
    const stdout = vi
      .spyOn(process.stdout, "write")
      .mockImplementation((chunk: string | Uint8Array) => {
        written += String(chunk);
        return true;
      });
    cleanups.push(async () => stdout.mockRestore());

    await terminal.flush();
    // A terminal hands the client what is waiting: on a busy machine the
    // typed keys and their enter come in one read, and the enter used to
    // submit the draft before the keys reached it, so nothing was sent.
    terminal.renderer.stdin.emit("data", Buffer.from("stream a reply\r"));
    await expect.poll(() => requests.length).toBe(1);
    expect(requests[0]?.messages?.at(-1)).toMatchObject({
      role: "user",
      content: "stream a reply",
    });
    await expect
      .poll(async () => {
        await terminal.flush();
        return written + terminal.captureCharFrame();
      })
      .toContain("sent once");
  },
);

test("a finished reply keeps its prose on every frame as it leaves the stream", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-terminal-reply-"));
  const workspace = await Workspace.create(root);
  const pieces = [
    "Here is a **bounded retry** for the helper:\n\n",
    "```ts\n",
    "export async function fetchRetry(url, tries = 3) {\n",
    "  return fetch(url);\n",
    "}\n",
    "```\n\n",
    "## Notes\n",
    "- stream complete\n",
  ];
  const encoder = new TextEncoder();
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      const path = new URL(request.url).pathname;
      if (request.method === "GET" && path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (request.method === "POST" && path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const lines = [
        ...pieces.map((content) => ({
          message: { role: "assistant", content },
          done: false,
        })),
        { message: { role: "assistant", content: "" }, done: true },
      ];
      return new Response(
        new ReadableStream({
          async start(controller) {
            for (const line of lines) {
              controller.enqueue(encoder.encode(`${JSON.stringify(line)}\n`));
              await Bun.sleep(5);
            }
            controller.close();
          },
        }),
        { headers: { "content-type": "application/x-ndjson" } },
      );
    },
  });
  const terminal = await createTestRenderer({
    ...terminalOptions("fullscreen"),
    width: 100,
    height: 30,
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
  await terminal.mockInput.pasteBracketedText("go");
  terminal.mockInput.pressEnter();
  // Every frame from the stream to the settled reply: the finished message
  // used to mount anew, and for a frame only its code block was drawn.
  const frames: string[] = [];
  await expect
    .poll(
      async () => {
        await terminal.flush();
        frames.push(terminal.captureCharFrame());
        const frame = frames.at(-1) ?? "";
        return (
          frame.includes("stream complete") && !frame.includes("Generating")
        );
      },
      { interval: 1 },
    )
    .toBe(true);
  for (let i = 0; i < 3; i++) {
    await terminal.flush();
    frames.push(terminal.captureCharFrame());
  }
  const first = frames.findIndex((frame) => frame.includes("Here is a"));
  expect(first).toBeGreaterThanOrEqual(0);
  // The settled frame, after the message landed in the history, has it too.
  expect(frames.at(-1)).toContain("Here is a bounded retry");
  const blank = frames
    .slice(first)
    .filter((frame) => !frame.includes("Here is a"));
  expect(blank).toEqual([]);
});
