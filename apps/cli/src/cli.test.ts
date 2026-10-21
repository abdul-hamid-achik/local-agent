import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const directory = dirname(fileURLToPath(import.meta.url));

test.each(["chalupa", "ollama"])(
  "headless CLI consumes %s configuration and emits only JSON events",
  async (provider) => {
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        if (new URL(request.url).pathname === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (
          new URL(request.url).pathname === "/api/show" &&
          request.method === "POST"
        ) {
          expect(await request.json()).toMatchObject({ model: "fixture" });
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 65536 },
          });
        }
        const body = await request.json();
        expect(body.model).toBe("fixture");
        expect(body.messages[0].content).toContain("Public Website Boundary");
        expect(body.options.num_ctx).toBe(
          provider === "chalupa" ? 65536 : 16384,
        );
        return new Response(
          `${JSON.stringify({ message: { role: "assistant", content: "Hello" }, done: true, prompt_eval_count: 1, eval_count: 1 })}\n`,
        );
      },
    });
    try {
      const child = Bun.spawn(
        [
          process.execPath,
          "run",
          "--silent",
          "dev",
          "--mode",
          "headless",
          "-p",
          "Hello",
          "--json",
        ],
        {
          cwd: join(directory, "../../.."),
          env:
            provider === "ollama"
              ? {
                  OLLAMA_HOST: `0.0.0.0:${server.port}`,
                  LOCAL_AGENT_MODEL: "fixture",
                }
              : {
                  LOCAL_AGENT_PROVIDER: "chalupa",
                  LOCAL_AGENT_LOCAL_ONLY: "false",
                  LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
                    type: "ollama_remote",
                    model: "fixture",
                    base_url: server.url.origin,
                    context_size: 65536,
                    max_model_bytes: 40_000_000_000,
                    stream_idle_timeout: "10m",
                  }),
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
      expect(code).toBe(0);
      expect(stderr).toBe("");
      expect(
        stdout
          .trim()
          .split("\n")
          .map((line) => JSON.parse(line)),
      ).toEqual([
        { type: "text", text: "Hello" },
        { type: "done", inputTokens: 1, outputTokens: 1 },
        { type: "message", message: { role: "assistant", content: "Hello" } },
        {
          type: "turn_complete",
          messages: [
            { role: "user", content: "Hello" },
            { role: "assistant", content: "Hello" },
          ],
        },
      ]);
    } finally {
      await server.stop(true);
    }
  },
);

test("production bundle keeps native rendering behind a dynamic import", async () => {
  const result = await Bun.build({
    entrypoints: [join(directory, "main.ts")],
    target: "bun",
    packages: "external",
    splitting: true,
  });
  expect(result.success).toBe(true);
  const entry = result.outputs.find((output) => output.kind === "entry-point");
  expect(entry).toBeDefined();
  const source = await entry?.text();
  expect(source).toContain("import(");
  expect(source).not.toMatch(
    /from\s+['"](?:@local-agent\/vue-tui['"]|vue['"]|@opentui\/)/,
  );
});

async function runCli(...args: string[]) {
  const child = Bun.spawn(
    [process.execPath, "run", "--silent", "dev", ...args],
    {
      cwd: join(directory, "../../.."),
      env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "" },
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { stdout, stderr, code };
}

test.each([
  [["--ui", "bogus"], "Unknown UI client"],
  [["--ui", "bogus", "--help"], "Unknown UI client"],
  [["--ui", "bogus", "--mcp-status"], "Unknown UI client"],
  [["--ui", "tuimark", "--mode", "inline"], "--ui tuimark is fullscreen only"],
  [
    ["--ui", "tuimark", "--mode", "headless", "-p", "x"],
    "--ui tuimark is fullscreen only",
  ],
  [["--ui", "tuimark", "-p", "x", "--json"], "--ui tuimark is fullscreen only"],
])("the Tuimark client is fullscreen only: %j", async (args, message) => {
  const { stderr, code } = await runCli(...args);
  expect(code).toBe(1);
  expect(stderr).toContain(message);
});

test("--ui tuimark leaves session inspection and --mcp-status on their own paths", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "local-agent-ui-flag-"));
  try {
    const sessions = await runCli(
      "--ui",
      "tuimark",
      "--sessions",
      "--session-db",
      join(scratch, "sessions.db"),
    );
    expect(sessions.stderr).toBe("");
    expect(sessions.code).toBe(0);
    expect(JSON.parse(sessions.stdout)).toEqual([]);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
  const status = await runCli("--ui", "tuimark", "--mcp-status");
  expect(status.code).toBe(1);
  expect(status.stderr).toContain("--mcp-status requires --mcp-config PATH");
});
