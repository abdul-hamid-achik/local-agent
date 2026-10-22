import { mkdtemp, rm, symlink } from "node:fs/promises";
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
        {
          type: "done",
          inputTokens: 1,
          outputTokens: 1,
          promptEvalCount: 1,
          evalCount: 1,
        },
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
  [
    ["--ui", "tuimark", "--mode", "headless", "-p", "x"],
    "--ui tuimark needs an interactive terminal",
  ],
  [
    ["--ui", "tuimark", "-p", "x", "--json"],
    "--ui tuimark needs an interactive terminal",
  ],
])("the Tuimark client refuses headless runs: %j", async (args, message) => {
  const { stderr, code } = await runCli(...args);
  expect(code).toBe(1);
  expect(stderr).toContain(message);
});

test("an explicit --ui tuimark --mode inline is accepted (it stops at the terminal check)", async () => {
  const { stderr, code } = await runCli("--ui", "tuimark", "--mode", "inline");
  expect(code).toBe(1);
  expect(stderr).toContain("Interactive modes require a terminal");
  expect(stderr).not.toContain("needs an interactive terminal; use it without");
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

test("the default client is not refused for a mode: only an explicit --ui tuimark is", async () => {
  // Without a terminal both stop at the same place, after the flags were accepted.
  for (const args of [
    ["--mode", "inline"],
    ["--ui", "vue", "--mode", "inline"],
  ]) {
    const { stderr, code } = await runCli(...args);
    expect(code).toBe(1);
    expect(stderr).toContain("Interactive modes require a terminal");
    expect(stderr).not.toContain("fullscreen only");
  }
});

test("the local-agent launcher runs the CLI from the checkout, also through a symlink on PATH", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "local-agent-launcher-"));
  try {
    const launcher = join(directory, "../launcher.ts");
    const link = join(scratch, "local-agent");
    await symlink(launcher, link);
    for (const command of [launcher, link]) {
      const child = Bun.spawn([process.execPath, command, "--ui", "bogus"], {
        cwd: scratch,
        env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "" },
        stdout: "pipe",
        stderr: "pipe",
      });
      const [stderr, code] = await Promise.all([
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(stderr).toContain("Unknown UI client");
      expect(code).toBe(1);
    }
    // The package declares it, so `bun link` puts it on PATH under this name.
    const manifest = (await Bun.file(
      join(directory, "../package.json"),
    ).json()) as {
      bin: Record<string, string>;
    };
    expect(manifest.bin).toEqual({ "local-agent": "./launcher.ts" });
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
});

test("--help shows the default model and the new flags", async () => {
  const { stdout, code } = await runCli("--help");
  expect(code).toBe(0);
  expect(stdout).toContain("tuimark (default on macOS and Linux)");
  expect(stdout).toContain("default: qwen3.5:4b-q4_K_M");
  expect(stdout).toContain("--think on|off");
  expect(stdout).toContain("--context N");
});

async function withOllama(
  models: { name: string; size: number; context?: number }[],
  run: (origin: string, chats: unknown[]) => Promise<void>,
) {
  const chats: unknown[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags") return Response.json({ models });
      if (path === "/api/show") {
        const { model } = (await request.json()) as { model: string };
        const entry = models.find((item) => item.name === model);
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": entry?.context ?? 65536 },
        });
      }
      chats.push(await request.json());
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "ok" }, done: true })}\n`,
      );
    },
  });
  try {
    await run(server.url.origin, chats);
  } finally {
    await server.stop(true);
  }
}

test("a missing default model fails with one actionable line before any chat request", async () => {
  await withOllama(
    [{ name: "other:1b", size: 1000 }],
    async (origin, chats) => {
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
          env: { PATH: process.env.PATH ?? "", OLLAMA_HOST: origin },
          stdout: "pipe",
          stderr: "pipe",
        },
      );
      const [stdout, stderr, code] = await Promise.all([
        new Response(child.stdout).text(),
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(code).toBe(1);
      expect(stdout).toBe("");
      expect(stderr).toBe(
        "Model qwen3.5:4b-q4_K_M is not installed. Run: ollama pull qwen3.5:4b-q4_K_M\n",
      );
      expect(chats).toEqual([]);
    },
  );
});

test("the installed default model is used without --model, with --think and --context applied", async () => {
  await withOllama(
    [{ name: "qwen3.5:4b-q4_K_M", size: 1000, context: 262144 }],
    async (origin, chats) => {
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
          "--think",
          "off",
          "--context",
          "32k",
        ],
        {
          cwd: join(directory, "../../.."),
          env: { PATH: process.env.PATH ?? "", OLLAMA_HOST: origin },
          stdout: "pipe",
          stderr: "pipe",
        },
      );
      const [stderr, code] = await Promise.all([
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(stderr).toBe("");
      expect(code).toBe(0);
      expect(chats[0]).toMatchObject({
        model: "qwen3.5:4b-q4_K_M",
        think: false,
        options: { num_ctx: 32768 },
      });
    },
  );
});

test("a --context beyond the model's native context is refused before any chat request", async () => {
  await withOllama(
    [{ name: "qwen3.5:4b-q4_K_M", size: 1000, context: 8192 }],
    async (origin, chats) => {
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
          "--context",
          "32768",
        ],
        {
          cwd: join(directory, "../../.."),
          env: { PATH: process.env.PATH ?? "", OLLAMA_HOST: origin },
          stdout: "pipe",
          stderr: "pipe",
        },
      );
      const [stderr, code] = await Promise.all([
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(code).toBe(1);
      expect(stderr).toContain("--context 8192");
      expect(chats).toEqual([]);
    },
  );
});
