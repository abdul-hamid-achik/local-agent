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
    await terminal.waitForFrame((frame) =>
      frame.includes(kind === "edit" ? "Edit notes.txt" : "Call fixture__echo"),
    );
    terminal.mockInput.pressKey("n");
    await terminal.waitForFrame((frame) => frame.includes("edit denied"));

    await terminal.mockInput.pasteBracketedText("next\nline");
    terminal.mockInput.pressEnter();
    await terminal.waitForFrame((frame) => frame.includes("user: next"));
    expect(terminal.captureCharFrame()).toContain("edit denied");
    expect(terminal.captureCharFrame()).toContain("next line");
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("before\n");
    expect(
      requests[1]?.messages?.some((message) => message.role === "tool"),
    ).toBe(true);
    expect(
      requests[2]?.messages?.some((message) =>
        message.content?.includes("next\nline"),
      ),
    ).toBe(true);
    expect(requests.length).toBeGreaterThanOrEqual(3);
    expect(mcpCalls).toBe(0);
  },
);
