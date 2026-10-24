import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";

// A write is a file change: only --approve-edits grants it, never the command or MCP grants.
test.each([
  [[], "denied"],
  [["--approve-commands"], "denied"],
  [["--approve-mcp"], "denied"],
  [["--approve-edits"], "succeeded"],
] as const)("headless write with %j is %s", async (flags, status) => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-write-flow-"));
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
      const first = body.messages.every((message) => message.role !== "tool");
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: first ? "" : "Finished", ...(first ? { tool_calls: [{ function: { name: "write", arguments: { path: "docs/new.txt", content: "hello\n" } } }] } : {}) }, done: true })}\n`,
      );
    },
  });
  try {
    const child = Bun.spawn(
      [
        process.execPath,
        new URL("./main.ts", import.meta.url).pathname,
        "--model",
        "fixture",
        "--mode",
        "headless",
        "--workspace",
        root,
        "--prompt",
        "Write a file",
        "--json",
        ...flags,
      ],
      {
        env: {
          PATH: process.env.PATH ?? "",
          OLLAMA_HOST: server.url.origin,
          // The test's private config home, never the developer's.
          XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
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
    expect(stderr).toBe("");
    expect(code).toBe(0);
    const results = stdout
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line))
      .filter((event) => event.type === "tool_result");
    expect(results.map((event) => event.status)).toEqual([status]);
    const file = join(root, "docs", "new.txt");
    if (status === "succeeded") {
      expect(await readFile(file, "utf8")).toBe("hello\n");
      expect((await stat(file)).mode & 0o777).toBe(0o644);
    } else {
      expect(await Bun.file(file).exists()).toBe(false);
    }
  } finally {
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
