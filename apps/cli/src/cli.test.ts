import { existsSync } from "node:fs";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const directory = dirname(fileURLToPath(import.meta.url));
const mainPath = join(directory, "main.ts");

/** A line only the fixture workspace's AGENTS.md has, so a run can be shown to have read it. */
const WORKSPACE_RULES = "Fixture workspace rule: answer in one word.";

/**
 * A scratch home for a spawned CLI: its HOME, and a workspace of its own
 * (with an AGENTS.md) as the working directory. Neither the repository's .env
 * nor the developer's home is where the CLI looks.
 */
async function makeHome() {
  const home = await mkdtemp(join(tmpdir(), "local-agent-cli-"));
  const workspace = join(home, "workspace");
  await mkdir(workspace);
  await writeFile(join(workspace, "AGENTS.md"), `${WORKSPACE_RULES}\n`);
  return { home, workspace };
}

/**
 * Spawns the CLI the way a user's shell would, alone: Bun is told not to load
 * a .env (none is expected in the scratch workspace; this keeps it so), and
 * the environment holds only what the test names, a PATH, the test's private
 * config home and the scratch HOME.
 */
async function startCli(
  args: string[],
  options: {
    env?: Record<string, string>;
    bunArgs?: string[];
    home?: { home: string; workspace: string };
  } = {},
) {
  const scratch = options.home ?? (await makeHome());
  const child = Bun.spawn(
    [
      process.execPath,
      "--no-env-file",
      ...(options.bunArgs ?? []),
      mainPath,
      ...args,
    ],
    {
      cwd: scratch.workspace,
      env: {
        PATH: process.env.PATH ?? "",
        HOME: scratch.home,
        // The test's private config home, never the developer's.
        XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
        ...options.env,
      },
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const output = Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]).then(([stdout, stderr, code]) => ({ stdout, stderr, code }));
  return {
    child,
    output,
    ...scratch,
    cleanup: () =>
      options.home
        ? Promise.resolve()
        : rm(scratch.home, { recursive: true, force: true }),
  };
}

async function runCli(
  args: string[],
  options: Parameters<typeof startCli>[1] = {},
) {
  const run = await startCli(args, options);
  try {
    return await run.output;
  } finally {
    await run.cleanup();
  }
}

/** A model that answers every chat with these pieces of text, one per line of the stream. */
async function withOllama(
  models: { name: string; size: number; context?: number }[],
  run: (origin: string, chats: unknown[]) => Promise<void>,
  pieces = ["ok"],
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
        [
          ...pieces.map((content) => ({
            message: { role: "assistant", content },
            done: false,
          })),
          { message: { role: "assistant", content: "" }, done: true },
        ]
          .map((line) => `${JSON.stringify(line)}\n`)
          .join(""),
      );
    },
  });
  try {
    await run(server.url.origin, chats);
  } finally {
    await server.stop(true);
  }
}

test.each(["chalupa", "ollama"])(
  "headless CLI consumes %s configuration and emits only JSON events",
  async (provider) => {
    // What the server saw, checked after the run: an expectation inside the
    // handler would become a 500 the CLI reports without the comparison.
    const shows: unknown[] = [];
    const chats: Array<{
      model: string;
      messages: { content: string }[];
      options: { num_ctx: number };
    }> = [];
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        const path = new URL(request.url).pathname;
        if (path === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (path === "/api/show" && request.method === "POST") {
          shows.push(await request.json());
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 65536 },
          });
        }
        chats.push(await request.json());
        return new Response(
          `${JSON.stringify({ message: { role: "assistant", content: "Hello" }, done: true, prompt_eval_count: 1, eval_count: 1 })}\n`,
        );
      },
    });
    try {
      const { stdout, stderr, code } = await runCli(
        ["--mode", "headless", "-p", "Hello", "--json"],
        {
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
        },
      );
      expect(code).toBe(0);
      expect(stderr).toBe("");
      expect(shows[0]).toMatchObject({ model: "fixture" });
      expect(chats).toHaveLength(1);
      expect(chats[0]?.model).toBe("fixture");
      // The workspace is the scratch one, not the repository.
      expect(chats[0]?.messages[0]?.content).toContain(WORKSPACE_RULES);
      expect(chats[0]?.messages[0]?.content).not.toContain(
        "Public Website Boundary",
      );
      expect(chats[0]?.options.num_ctx).toBe(
        provider === "chalupa" ? 65536 : 16384,
      );
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

test("a .env in the working directory is not read by a spawned CLI", async () => {
  // Bun loads a .env from the working directory unless told not to, and it
  // would choose the model of a run that names none.
  const scratch = await makeHome();
  try {
    await writeFile(
      join(scratch.workspace, ".env"),
      "LOCAL_AGENT_MODEL=from-dotenv\n",
    );
    await withOllama([{ name: "other:1b", size: 1000 }], async (origin) => {
      const { stderr, code } = await runCli(
        ["--mode", "headless", "-p", "Hello"],
        { home: scratch, env: { OLLAMA_HOST: origin } },
      );
      expect(code).toBe(1);
      expect(stderr).toContain("Model qwen3.5:4b-q4_K_M is not installed");
      expect(stderr).not.toContain("from-dotenv");
    });
  } finally {
    await rm(scratch.home, { recursive: true, force: true });
  }
});

/** A reply that never finishes: one piece, then the stream stays open. */
function hungModel() {
  const asked = Promise.withResolvers<void>();
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
      asked.resolve();
      return new Response(
        new ReadableStream({
          start(stream) {
            stream.enqueue(
              new TextEncoder().encode(
                `${JSON.stringify({ message: { role: "assistant", content: "Hel" }, done: false })}\n`,
              ),
            );
          },
        }),
      );
    },
  });
  return { server, asked: asked.promise };
}

// The code a shell reads for a run a signal stopped is 128 plus its number;
// a hang-up exits on its own path, not killed by the signal's default.
test.each([
  ["SIGHUP", 129],
  ["SIGINT", 130],
  ["SIGTERM", 143],
] as const)(
  "%s stops a running turn and exits %i",
  async (signal, expected) => {
    const { server, asked } = hungModel();
    const run = await startCli(
      ["--mode", "headless", "-p", "Hello", "--json"],
      {
        env: {
          OLLAMA_HOST: `127.0.0.1:${server.port}`,
          LOCAL_AGENT_MODEL: "fixture",
        },
      },
    );
    try {
      await asked;
      const started = Date.now();
      run.child.kill(signal);
      const { stderr, code } = await run.output;
      expect(run.child.signalCode).toBeNull();
      expect(code).toBe(expected);
      // The stop was asked for: no "operation was aborted" error on top of it.
      expect(stderr).toBe("");
      expect(Date.now() - started).toBeLessThan(5_000);
    } finally {
      await server.stop(true);
      await run.cleanup();
    }
  },
);

/** The renderer packages the interactive client loads, and the module that loads them. */
const RENDERER_SPECIFIER = String.raw`(?:@local-agent/vue-tui|vue-termui|vue|@opentui/[\w.-]+|#cli/terminal)(?:/[^'"]*)?`;

/** Every way a module can pull a package in: from-imports, bare imports, requires. */
function importsOf(source: string): string[] {
  const found: string[] = [];
  for (const form of [
    /\b(?:import|export)\s[^'"`;]*?\bfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*['"]([^'"]+)['"]/g,
    /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  ])
    for (const match of source.matchAll(form)) found.push(match[1] ?? "");
  return found;
}

test("production bundle keeps native rendering behind a dynamic import", async () => {
  // The renderer packages stay external so they show as imports; every #cli
  // module is bundled in, so what a module such as text.ts or preview.ts
  // imports is read too.
  const result = await Bun.build({
    entrypoints: [mainPath],
    target: "bun",
    splitting: true,
    external: [
      "@local-agent/*",
      "@thelacanians/*",
      "@opentui/*",
      "vue",
      "vue-termui",
    ],
  });
  expect(result.success).toBe(true);
  const entry = result.outputs.find((output) => output.kind === "entry-point");
  expect(entry).toBeDefined();
  const texts = new Map<string, string>();
  for (const output of result.outputs)
    texts.set(output.path.replace(/^\.\//, ""), await output.text());
  const entryText = (await entry?.text()) ?? "";
  const renderer = new RegExp(`^${RENDERER_SPECIFIER}$`);

  // What the entry loads at startup: itself and the chunks it imports, and
  // theirs, by a static import.
  const loadedAtStart = new Set<string>();
  const pending = [entry?.path.replace(/^\.\//, "") ?? ""];
  while (pending.length > 0) {
    const path = pending.pop() ?? "";
    if (loadedAtStart.has(path)) continue;
    loadedAtStart.add(path);
    for (const specifier of importsOf(texts.get(path) ?? ""))
      if (specifier.startsWith("./"))
        pending.push(specifier.replace(/^\.\//, ""));
  }
  for (const path of loadedAtStart)
    expect(
      importsOf(texts.get(path) ?? "").filter((specifier) =>
        renderer.test(specifier),
      ),
      `${path} imports the renderer at startup`,
    ).toEqual([]);

  // It is reached by a dynamic import, from a chunk the start does not load;
  // without that, the check above would be of a build that never has it.
  expect(entryText).toMatch(/\bimport\(\s*["']\.\/[^"']+["']\s*\)/);
  const lazy = [...texts].filter(
    ([path]) => !loadedAtStart.has(path) && path !== "",
  );
  expect(
    lazy.some(([, text]) =>
      importsOf(text).some((specifier) => renderer.test(specifier)),
    ),
    "a chunk loaded on demand imports the renderer",
  ).toBe(true);
});

/** The modules a process had loaded when it exited, one path per line, written by a --preload. */
function loadedModulesSpy(home: string) {
  const log = join(home, "loaded.log");
  const spy = join(home, "spy.ts");
  return {
    log,
    spy,
    write: () =>
      writeFile(
        spy,
        `import { writeFileSync } from "node:fs";
process.on("exit", () => writeFileSync(${JSON.stringify(log)}, Object.keys(require.cache).join("\\n")));
`,
      ),
  };
}

test("a headless run never loads the terminal renderer", async () => {
  // The paths of the modules the process loaded, read when it exits. A run
  // that imports the renderer on purpose is listed by the same spy, which is
  // what gives the headless run's list its meaning.
  const scratch = await makeHome();
  try {
    const loaded = loadedModulesSpy(scratch.home);
    await loaded.write();
    const renderer =
      /\/(?:vue-termui|@opentui|vue@[^/]+|packages\/vue-tui)\/|\/apps\/cli\/src\/terminal\.ts$/;
    await withOllama([{ name: "fixture", size: 1000 }], async (origin) => {
      const headless = await runCli(
        ["--mode", "headless", "-p", "Hello", "--model", "fixture"],
        {
          home: scratch,
          bunArgs: ["--preload", loaded.spy],
          env: { OLLAMA_HOST: origin },
        },
      );
      expect(headless.stderr).toBe("");
      expect(headless.code).toBe(0);
      expect(headless.stdout).toBe("ok\n");
    });
    const modules = (await Bun.file(loaded.log).text()).split("\n");
    expect(modules.some((path) => path.endsWith("/apps/cli/src/main.ts"))).toBe(
      true,
    );
    expect(modules.filter((path) => renderer.test(path))).toEqual([]);

    await rm(loaded.log);
    const control = Bun.spawn(
      [
        process.execPath,
        "--no-env-file",
        "--preload",
        loaded.spy,
        "-e",
        'await import("#cli/terminal")',
      ],
      {
        cwd: directory,
        env: { PATH: process.env.PATH ?? "", HOME: scratch.home },
        stdout: "pipe",
        stderr: "pipe",
      },
    );
    await control.exited;
    const interactive = (await Bun.file(loaded.log).text()).split("\n");
    expect(
      interactive.filter((path) => renderer.test(path)).length,
    ).toBeGreaterThan(0);
  } finally {
    await rm(scratch.home, { recursive: true, force: true });
  }
});

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
  const { stderr, code } = await runCli(args);
  expect(code).toBe(1);
  expect(stderr).toContain(message);
});

test.each([
  [["--mode", "inline", "-p", "x"], "--mode inline is interactive"],
  [["--mode", "fullscreen", "--json"], "--mode fullscreen is interactive"],
  [
    ["--mode", "inline", "--ui", "vue", "-p", "x", "--json"],
    "--mode inline is interactive",
  ],
])(
  "a --mode that is not headless beside --prompt or --json is refused: %j",
  async (args, message) => {
    const { stderr, code } = await runCli(args);
    expect(code).toBe(1);
    expect(stderr).toContain(message);
    expect(stderr).toContain("run headless");
  },
);

test("--prompt or --json alone, or with --mode headless, still run headless", async () => {
  // Only a mode asked for and then dropped is refused.
  await withOllama([{ name: "fixture", size: 1000 }], async (origin, chats) => {
    for (const args of [
      ["-p", "Hello"],
      ["--mode", "headless", "-p", "Hello"],
    ]) {
      const { stderr, code } = await runCli([...args, "--model", "fixture"], {
        env: { OLLAMA_HOST: origin },
      });
      expect(stderr).toBe("");
      expect(code).toBe(0);
    }
    expect(chats).toHaveLength(2);
  });
});

test.each([
  // Headless has no theme, and "use --ui tuimark" would be refused in turn.
  [["--theme", "dark", "-p", "x"], "has no effect with --prompt"],
  [
    ["--theme", "dark", "--mode", "headless", "-p", "x"],
    "has no effect with --prompt",
  ],
  // The Vue client is known from the flags: refused before a terminal is asked for.
  [["--theme", "dark", "--ui", "vue"], "--theme needs the Tuimark client"],
  [["--theme", "dark", "--mode", "inline"], "--theme needs the Tuimark client"],
])(
  "--theme is refused before anything is opened: %j",
  async (args, message) => {
    const scratch = await makeHome();
    try {
      const database = join(scratch.home, "sessions.db");
      const { stderr, code } = await runCli(
        [
          ...args,
          "--session-db",
          database,
          "--mcp-config",
          "/nonexistent.json",
        ],
        { home: scratch },
      );
      expect(code).toBe(1);
      expect(stderr).toContain(message);
      expect(stderr).not.toContain("Interactive modes require a terminal");
      expect(stderr).not.toContain("nonexistent.json");
      // Not even the session database was created.
      expect(existsSync(database)).toBe(false);
    } finally {
      await rm(scratch.home, { recursive: true, force: true });
    }
  },
);

test("a run that fails to start leaves no session behind", async () => {
  // An explicit MCP server that cannot start fails the run after the model
  // was admitted: the session is made only once everything it needs is up.
  const scratch = await makeHome();
  try {
    const database = join(scratch.home, "sessions.db");
    const config = join(scratch.home, "mcp.json");
    await writeFile(
      config,
      JSON.stringify([
        {
          name: "broken",
          transport: {
            kind: "stdio",
            command: join(scratch.home, "no-such-server"),
            args: [],
            cwd: scratch.workspace,
            env: {},
          },
        },
      ]),
    );
    await withOllama(
      [{ name: "fixture", size: 1000 }],
      async (origin, chats) => {
        const failed = await runCli(
          [
            "--mode",
            "headless",
            "-p",
            "Hello",
            "--model",
            "fixture",
            "--session-db",
            database,
            "--mcp-config",
            config,
          ],
          { home: scratch, env: { OLLAMA_HOST: origin } },
        );
        expect(failed.code).toBe(1);
        expect(failed.stderr).toContain("MCP server broken unavailable");
        expect(chats).toEqual([]);
        const listed = await runCli(["--sessions", "--session-db", database], {
          home: scratch,
        });
        expect(listed.code).toBe(0);
        expect(JSON.parse(listed.stdout)).toEqual([]);
      },
    );
  } finally {
    await rm(scratch.home, { recursive: true, force: true });
  }
});

test("a reply that splits an escape sequence between two pieces is printed without it", async () => {
  await withOllama(
    [{ name: "fixture", size: 1000 }],
    async (origin) => {
      const { stdout, stderr, code } = await runCli(
        ["--mode", "headless", "-p", "Hello", "--model", "fixture"],
        { env: { OLLAMA_HOST: origin } },
      );
      expect(stderr).toBe("");
      expect(code).toBe(0);
      expect(stdout).toBe("ared then  end\n");
    },
    ["a\u001b", "[31mred\u001b[0", "m then \u001b]0;pw", "ned\u0007 end"],
  );
});

test("an explicit --ui tuimark --mode inline is accepted (it stops at the terminal check)", async () => {
  const { stderr, code } = await runCli([
    "--ui",
    "tuimark",
    "--mode",
    "inline",
  ]);
  expect(code).toBe(1);
  expect(stderr).toContain("Interactive modes require a terminal");
  expect(stderr).not.toContain("needs an interactive terminal; use it without");
});

test("--ui tuimark leaves session inspection and --mcp-status on their own paths", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "local-agent-ui-flag-"));
  try {
    const sessions = await runCli([
      "--ui",
      "tuimark",
      "--sessions",
      "--session-db",
      join(scratch, "sessions.db"),
    ]);
    expect(sessions.stderr).toBe("");
    expect(sessions.code).toBe(0);
    expect(JSON.parse(sessions.stdout)).toEqual([]);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
  const status = await runCli(["--ui", "tuimark", "--mcp-status"]);
  expect(status.code).toBe(1);
  expect(status.stderr).toContain("--mcp-status needs --mcp-config PATH or ");
});

test("the default client is not refused for a mode: only an explicit --ui tuimark is", async () => {
  // Without a terminal both stop at the same place, after the flags were accepted.
  for (const args of [
    ["--mode", "inline"],
    ["--ui", "vue", "--mode", "inline"],
  ]) {
    const { stderr, code } = await runCli(args);
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
    const manifest = (await Bun.file(
      join(directory, "../package.json"),
    ).json()) as {
      version: string;
      bin: Record<string, string>;
    };
    for (const command of [launcher, link]) {
      // --version is read from the package.json next to the main.ts the
      // launcher loaded: a copy of the CLI elsewhere would name its own.
      const child = Bun.spawn(
        [process.execPath, "--no-env-file", command, "--version"],
        {
          cwd: scratch,
          env: {
            PATH: process.env.PATH ?? "",
            HOME: scratch,
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
      expect(stdout).toBe(`local-agent ${manifest.version}\n`);
    }
    // The package declares it, so `bun link` puts it on PATH under this name.
    expect(manifest.bin).toEqual({ "local-agent": "./launcher.ts" });
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
});

test("--help shows the default model and the new flags", async () => {
  const { stdout, code } = await runCli(["--help"]);
  expect(code).toBe(0);
  expect(stdout).toContain("tuimark (default on macOS and Linux)");
  expect(stdout).toContain("default: qwen3.5:4b-q4_K_M");
  expect(stdout).toContain("--think on|off");
  expect(stdout).toContain("--context N");
  expect(stdout).toContain("--sandbox-loopback");
});

test("a missing default model fails with one actionable line before any chat request", async () => {
  await withOllama(
    [{ name: "other:1b", size: 1000 }],
    async (origin, chats) => {
      const { stdout, stderr, code } = await runCli(
        ["--mode", "headless", "-p", "Hello", "--json"],
        { env: { OLLAMA_HOST: origin } },
      );
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
      const { stderr, code } = await runCli(
        [
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
        { env: { OLLAMA_HOST: origin } },
      );
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
      const { stderr, code } = await runCli(
        ["--mode", "headless", "-p", "Hello", "--context", "32768"],
        { env: { OLLAMA_HOST: origin } },
      );
      expect(code).toBe(1);
      expect(stderr).toContain("--context 8192");
      expect(chats).toEqual([]);
    },
  );
});
