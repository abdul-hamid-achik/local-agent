import { existsSync } from "node:fs";
import {
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { appendRule, LIMITS, toolReference } from "@local-agent/runtime";
import { expect, test } from "vitest";

// The desktop sidecar consults the workspace's permission file like every
// other client: a trusted allow rule skips the approval card, a deny rule
// refuses before one is requested, and a fetch deny rule applies through
// the runtime's read hook.
test("the sidecar applies the workspace's rules before requesting approval", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await writeFile(join(root, "notes.txt"), "before");
  await appendRule(root, {
    effect: "allow",
    tool: "edit",
    pattern: "/notes.txt",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "run_command",
    pattern: "rm *",
  });
  await appendRule(root, {
    effect: "deny",
    tool: "fetch",
    pattern: "domain:blocked.example",
  });
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
      const done = body.messages.some((message) => message.role === "tool");
      const message = done
        ? { role: "assistant", content: "Finished" }
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
              {
                function: {
                  name: "run_command",
                  arguments: { argv: ["sudo", "rm", "notes.txt"] },
                },
              },
              {
                function: {
                  name: "fetch",
                  arguments: { url: "https://blocked.example/x" },
                },
              },
            ],
          };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  const child = Bun.spawn(
    [process.execPath, new URL("./sidecar.ts", import.meta.url).pathname],
    {
      env: {
        PATH: process.env.PATH ?? "",
        OLLAMA_HOST: server.url.origin,
        DESKTOP_REPO_ROOT: root,
        // The test's private config home: never the user's trust store.
        XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
      },
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  try {
    child.stdin.write(
      `${JSON.stringify({
        id: 1,
        method: "agent.start",
        params: {
          runId: "r1",
          workspace: root,
          intent: "build",
          model: "fixture",
          messages: [{ role: "user", content: "go" }],
        },
      })}\n`,
    );
    child.stdin.flush();
    const events: { type: string; [key: string]: unknown }[] = [];
    const reader = child.stdout.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    const deadline = Date.now() + 8_000;
    while (!events.some((event) => event.type.startsWith("run_"))) {
      if (Date.now() > deadline) throw new Error("sidecar run never ended");
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
        const frame = JSON.parse(buffer.slice(0, at));
        buffer = buffer.slice(at + 1);
        if (frame.stream === "r1") events.push(frame.event);
      }
    }
    expect(events.at(-1)?.type).toBe("run_done");
    expect(events.some((event) => event.type === "approval_requested")).toBe(
      false,
    );
    const results = events.filter((event) => event.type === "tool_result");
    expect(results.map((event) => event.status)).toEqual([
      "succeeded",
      "denied",
      "failed",
    ]);
    expect(await readFile(join(root, "notes.txt"), "utf8")).toBe("after");
  } finally {
    child.kill();
    await child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

/** A sidecar child and the frames it writes, one JSON object per line. */
function sidecarChild(env: Record<string, string>) {
  const child = Bun.spawn(
    [process.execPath, new URL("./sidecar.ts", import.meta.url).pathname],
    {
      env: {
        PATH: process.env.PATH ?? "",
        XDG_CONFIG_HOME: process.env.XDG_CONFIG_HOME ?? "",
        ...env,
      },
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  const reader = child.stdout.getReader();
  const decoder = new TextDecoder();
  const frames: Record<string, unknown>[] = [];
  let buffer = "";
  return {
    child,
    send(request: unknown) {
      child.stdin.write(`${JSON.stringify(request)}\n`);
      child.stdin.flush();
    },
    /** Reads until a frame matches, or fails after a few seconds. */
    async frame(
      match: (frame: Record<string, unknown>) => boolean,
    ): Promise<Record<string, unknown>> {
      const deadline = Date.now() + 8_000;
      for (;;) {
        const found = frames.find(match);
        if (found) return found;
        if (Date.now() > deadline) throw new Error("no such frame");
        const chunk = await reader.read();
        if (chunk.done) throw new Error("sidecar closed its output");
        buffer += decoder.decode(chunk.value, { stream: true });
        for (
          let at = buffer.indexOf("\n");
          at >= 0;
          at = buffer.indexOf("\n")
        ) {
          frames.push(JSON.parse(buffer.slice(0, at)));
          buffer = buffer.slice(at + 1);
        }
      }
    },
    frames,
  };
}

test("a cancel is answered while a slow request is still waiting", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch() {
      await held;
      return Response.json({ models: [{ name: "fixture", size: 1000 }] });
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({ id: 1, method: "ollama.models", params: {} });
    side.send({ id: 2, method: "agent.cancel", params: { runId: "r1" } });
    side.send({ id: 3, method: "hello" });
    // A start takes its turn too, though it fails at once (no such intent).
    side.send({
      id: 4,
      method: "agent.start",
      params: { runId: "r4", workspace: root, intent: "nope", messages: [] },
    });
    side.send({
      id: 5,
      method: "agent.decide",
      params: { runId: "r1", token: "t", allowed: true },
    });
    expect(await side.frame((frame) => frame.id === 2)).toMatchObject({
      ok: true,
      result: { cancelled: false },
    });
    expect(await side.frame((frame) => frame.id === 5)).toMatchObject({
      ok: true,
      result: { decided: false },
    });
    // The rest keep their order: hello and the start wait for the
    // inventory (a start that ran at once would have answered by now).
    await Bun.sleep(100);
    expect(
      side.frames.some((frame) => [1, 3, 4].includes(frame.id as number)),
    ).toBe(false);
    release();
    expect(await side.frame((frame) => frame.id === 4)).toMatchObject({
      ok: false,
    });
    expect(side.frames.map((frame) => frame.id)).toEqual([2, 5, 1, 3, 4]);
    expect(side.frames[2]).toMatchObject({ ok: true });
  } finally {
    release();
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("a running turn does not hold the requests after its start", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    idleTimeout: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      await held;
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "done" }, done: true })}\n`,
      );
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({
      id: 1,
      method: "agent.start",
      params: {
        runId: "r1",
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
      },
    });
    expect(await side.frame((frame) => frame.id === 1)).toMatchObject({
      ok: true,
    });
    side.send({ id: 2, method: "hello" });
    expect(await side.frame((frame) => frame.id === 2)).toMatchObject({
      ok: true,
    });
    expect(
      side.frames.some(
        (frame) =>
          frame.stream === "r1" &&
          String((frame.event as { type: string }).type).startsWith("run_"),
      ),
    ).toBe(false);
    release();
    await side.frame(
      (frame) =>
        frame.stream === "r1" &&
        (frame.event as { type: string }).type === "run_done",
    );
  } finally {
    release();
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("past 64 waiting requests one is refused at once, and a cancel is still answered", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let reached = () => {};
  const arrived = new Promise<void>((resolve) => {
    reached = resolve;
  });
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch() {
      reached();
      await held;
      return Response.json({ models: [] });
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({ id: 1, method: "ollama.models", params: {} });
    // Its request reached the server: id 1 is in its turn, so 2..65 are
    // the ones waiting.
    await arrived;
    for (let id = 2; id <= 65; id++) side.send({ id, method: "hello" });
    side.send({ id: 66, method: "hello" });
    side.send({ id: 67, method: "agent.cancel", params: { runId: "r1" } });
    expect(await side.frame((frame) => frame.id === 66)).toEqual({
      id: 66,
      ok: false,
      error: "Too many requests waiting",
    });
    expect(await side.frame((frame) => frame.id === 67)).toMatchObject({
      ok: true,
      result: { cancelled: false },
    });
    expect(side.frames.some((frame) => frame.id === 2)).toBe(false);
    release();
    await side.frame((frame) => frame.id === 65);
    expect(
      side.frames.map((frame) => frame.id).filter((id) => Number(id) <= 65),
    ).toEqual(Array.from({ length: 65 }, (_, at) => at + 1));
  } finally {
    release();
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("an MCP server that does not start says why, in mcp.status and in a run", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  const config = join(root, "mcp.json");
  await writeFile(
    config,
    JSON.stringify([
      {
        name: "broken",
        transport: {
          kind: "stdio",
          command: process.execPath,
          args: ["-e", "console.error('no token for broken'); process.exit(2)"],
        },
      },
      {
        name: "missing",
        transport: { kind: "stdio", command: join(root, "no-such-server") },
      },
    ]),
  );
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) =>
      new URL(request.url).pathname === "/api/tags"
        ? Response.json({ models: [{ name: "fixture", size: 1000 }] })
        : Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 16384 },
          }),
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({ id: 1, method: "mcp.status", params: { configPath: config } });
    const status = await side.frame((frame) => frame.id === 1);
    expect(status).toMatchObject({ ok: true });
    const servers = (status.result as { servers: Record<string, unknown>[] })
      .servers;
    expect(servers.map((entry) => [entry.name, entry.status])).toEqual([
      ["broken", "unavailable"],
      ["missing", "unavailable"],
    ]);
    expect(String(servers[0]?.error)).toContain("it said: no token for broken");
    expect(String(servers[1]?.error)).toContain("no-such-server");

    side.send({
      id: 2,
      method: "agent.start",
      params: {
        runId: "r2",
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
        mcpConfig: config,
      },
    });
    const start = await side.frame((frame) => frame.id === 2);
    expect(start).toMatchObject({ ok: false });
    expect(String(start.error)).toContain("MCP server broken:");
    expect(String(start.error)).toContain("no token for broken");
    expect(String(start.error)).toContain("MCP server missing:");
    expect(side.frames.some((frame) => frame.stream === "r2")).toBe(false);
  } finally {
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("workspace.list lists a broken link instead of failing, and no secrets", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "main.ts"), "export {};\n");
  await writeFile(join(root, ".env"), "TOKEN=secret\n");
  await symlink(join(root, "gone"), join(root, "src", "broken"));
  const side = sidecarChild({ DESKTOP_REPO_ROOT: root });
  try {
    side.send({
      id: 1,
      method: "workspace.list",
      params: { workspace: root, dir: "" },
    });
    expect(await side.frame((frame) => frame.id === 1)).toMatchObject({
      ok: true,
      result: {
        dir: "",
        entries: [{ name: "src", kind: "directory", size: 0 }],
        truncated: false,
      },
    });
    side.send({
      id: 2,
      method: "workspace.list",
      params: { workspace: root, dir: "src" },
    });
    expect(await side.frame((frame) => frame.id === 2)).toMatchObject({
      ok: true,
      result: {
        dir: "src",
        entries: [
          { name: "broken", kind: "link", size: 0 },
          { name: "main.ts", kind: "file", size: 11 },
        ],
      },
    });
    side.send({
      id: 3,
      method: "workspace.list",
      params: { workspace: root, dir: "../" },
    });
    expect(await side.frame((frame) => frame.id === 3)).toMatchObject({
      ok: false,
    });
  } finally {
    side.child.kill();
    await side.child.exited;
    await rm(root, { recursive: true, force: true });
  }
});

/**
 * A repo whose `bun run test` starts a sleeping grandchild and says its pid.
 * On SIGTERM the command takes 200 ms to clean up, then leaves `cleaned`.
 */
async function sleeperRepo() {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await writeFile(
    join(root, "package.json"),
    JSON.stringify({ scripts: { test: "bun grand.ts" } }),
  );
  await writeFile(
    join(root, "grand.ts"),
    [
      'import { writeFileSync } from "node:fs";',
      'process.on("SIGTERM", () => setTimeout(() => { writeFileSync("cleaned", ""); process.exit(0); }, 200));',
      'const child = Bun.spawn(["sleep", "30"]);',
      'await Bun.write("grandchild.pid", String(child.pid));',
      "await child.exited;",
    ].join("\n"),
  );
  return root;
}

async function grandchildPid(root: string): Promise<number> {
  const file = join(root, "grandchild.pid");
  for (let tries = 0; tries < 250; tries += 1) {
    const text = await readFile(file, "utf8").catch(() => "");
    if (text) return Number(text);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("the grandchild never started");
}

const alive = (pid: number) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

async function gone(pid: number): Promise<boolean> {
  for (let tries = 0; tries < 150 && alive(pid); tries += 1)
    await new Promise((resolve) => setTimeout(resolve, 20));
  return !alive(pid);
}

test("a project cancel stops what the command started too", async () => {
  const root = await sleeperRepo();
  const side = sidecarChild({ DESKTOP_REPO_ROOT: root });
  try {
    side.send({
      id: 1,
      method: "project.run",
      params: { name: "test", runId: "p1" },
    });
    expect(await side.frame((frame) => frame.id === 1)).toMatchObject({
      ok: true,
    });
    const pid = await grandchildPid(root);
    side.send({ id: 2, method: "project.cancel", params: { runId: "p1" } });
    expect(await side.frame((frame) => frame.id === 2)).toMatchObject({
      ok: true,
      result: { cancelled: true },
    });
    await side.frame(
      (frame) =>
        frame.stream === "p1" &&
        (frame.event as { type?: string }).type === "exit",
    );
    expect(await gone(pid)).toBe(true);
  } finally {
    side.child.kill();
    await side.child.exited;
    await rm(root, { recursive: true, force: true });
  }
});

type SidecarProcess = ReturnType<typeof sidecarChild>["child"];

for (const [how, end] of [
  ["SIGTERM", (child: SidecarProcess) => child.kill("SIGTERM")],
  ["closing stdin", (child: SidecarProcess) => void child.stdin.end()],
] as const)
  test(`${how} ends the sidecar and the commands it started`, async () => {
    const root = await sleeperRepo();
    const side = sidecarChild({ DESKTOP_REPO_ROOT: root });
    try {
      side.send({
        id: 1,
        method: "project.run",
        params: { name: "test", runId: "p1" },
      });
      await side.frame((frame) => frame.id === 1);
      const pid = await grandchildPid(root);
      const started = Date.now();
      end(side.child);
      expect(await side.child.exited).toBe(0);
      expect(await gone(pid)).toBe(true);
      // The command stopped on SIGTERM, with the time it took to clean up,
      // and the sidecar did not wait for the grace.
      expect(existsSync(join(root, "cleaned"))).toBe(true);
      expect(Date.now() - started).toBeLessThan(1500);
    } finally {
      side.child.kill();
      await side.child.exited;
      await rm(root, { recursive: true, force: true });
    }
  });

test("a shutdown cancels an MCP status check and its servers go too", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  const config = join(root, "mcp.json");
  const pidFile = join(root, "server.pid");
  // A stdio server that starts and never answers.
  await writeFile(
    config,
    JSON.stringify([
      {
        name: "mute",
        transport: {
          kind: "stdio",
          command: process.execPath,
          args: [
            "-e",
            `require("node:fs").writeFileSync(${JSON.stringify(pidFile)}, String(process.pid)); setInterval(() => {}, 1000);`,
          ],
        },
      },
    ]),
  );
  const side = sidecarChild({ DESKTOP_REPO_ROOT: root });
  try {
    side.send({ id: 1, method: "mcp.status", params: { configPath: config } });
    let pid = 0;
    for (let tries = 0; tries < 250 && !pid; tries += 1) {
      pid = Number(await readFile(pidFile, "utf8").catch(() => "0"));
      if (!pid) await Bun.sleep(20);
    }
    expect(pid).toBeGreaterThan(0);
    const started = Date.now();
    side.child.kill("SIGTERM");
    expect(await side.child.exited).toBe(0);
    // Well before the server's 15 s connect timeout.
    expect(Date.now() - started).toBeLessThan(2_500);
    expect(await gone(pid)).toBe(true);
  } finally {
    side.child.kill();
    await side.child.exited;
    await rm(root, { recursive: true, force: true });
  }
});

test("a request served after the shutdown began starts nothing", async () => {
  const root = await sleeperRepo();
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let reached = () => {};
  const arrived = new Promise<void>((resolve) => {
    reached = resolve;
  });
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch() {
      reached();
      await held;
      return Response.json({ models: [] });
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({
      id: 1,
      method: "project.run",
      params: { name: "test", runId: "p1" },
    });
    const first = await grandchildPid(root);
    // A second run waits behind a slow request, which ends only once the
    // shutdown has begun (the first run's cleanup holds it open).
    side.send({ id: 2, method: "ollama.models", params: {} });
    side.send({
      id: 3,
      method: "project.run",
      params: { name: "test", runId: "p2" },
    });
    await arrived;
    side.child.kill("SIGTERM");
    // A control is still answered while the shutdown waits for p1.
    await Bun.sleep(50);
    side.send({ id: 4, method: "project.cancel", params: { runId: "none" } });
    expect(await side.frame((frame) => frame.id === 4)).toMatchObject({
      ok: true,
      result: { cancelled: false },
    });
    release();
    expect(await side.child.exited).toBe(0);
    expect(await gone(first)).toBe(true);
    // The pid file is the first run's: the second never started.
    expect(Number(await readFile(join(root, "grandchild.pid"), "utf8"))).toBe(
      first,
    );
  } finally {
    release();
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("input past the buffer limit ends the sidecar with an error, and its commands too", async () => {
  const root = await sleeperRepo();
  const side = sidecarChild({ DESKTOP_REPO_ROOT: root });
  try {
    side.send({
      id: 1,
      method: "project.run",
      params: { name: "test", runId: "p1" },
    });
    const pid = await grandchildPid(root);
    // The sidecar stops reading partway: the rest of the write fails.
    try {
      side.child.stdin.write("x".repeat(33 * 1024 * 1024));
      await side.child.stdin.flush();
    } catch {}
    expect(await side.child.exited).toBe(1);
    expect(await gone(pid)).toBe(true);
  } finally {
    side.child.kill();
    await side.child.exited;
    await rm(root, { recursive: true, force: true });
  }
});

test("everything the console writes goes to stderr, so stdout stays frames", async () => {
  const child = Bun.spawn(
    [
      process.execPath,
      "-e",
      [
        `import ${JSON.stringify(new URL("./frames-only.ts", import.meta.url).pathname)};`,
        'console.log("log"); console.info("info"); console.debug("debug");',
        'console.count("count"); console.table([1]); console.dir({ dir: 1 });',
        'console.group("group"); console.groupEnd(); console.time("t"); console.timeEnd("t");',
        'console.write("write");',
      ].join("\n"),
    ],
    { stdout: "pipe", stderr: "pipe" },
  );
  const [out, err] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);
  expect(await child.exited).toBe(0);
  expect(out).toBe("");
  for (const word of [
    "log",
    "info",
    "debug",
    "count",
    "dir",
    "group",
    "t:",
    "write",
  ])
    expect(err).toContain(word);
});

// The Reference view draws its tools and bounds from this answer, so it
// must list what this host's runs really offer: a build turn's tools, and a
// plan turn's as the read-only ones.
test("reference lists the tools a desktop run offers, and the runtime's bounds", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  const offered: string[][] = [];
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
        tools: { function: { name: string } }[];
      };
      offered.push(body.tools.map((tool) => tool.function.name));
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "Done" }, done: true })}\n`,
      );
    },
  });
  const side = sidecarChild({
    OLLAMA_HOST: server.url.origin,
    DESKTOP_REPO_ROOT: root,
  });
  try {
    for (const [runId, intent] of [
      ["r1", "build"],
      ["r2", "plan"],
    ]) {
      side.send({
        id: runId === "r1" ? 1 : 2,
        method: "agent.start",
        params: {
          runId,
          workspace: root,
          intent,
          model: "fixture",
          messages: [{ role: "user", content: "go" }],
        },
      });
      await side.frame(
        (frame) =>
          frame.stream === runId &&
          (frame.event as { type: string }).type === "run_done",
      );
    }
    side.send({ id: 7, method: "reference" });
    const frame = (await side.frame((frame) => frame.id === 7)) as {
      ok: boolean;
      result: { tools: { name: string; readOnly: boolean }[] };
    };
    expect(frame.ok).toBe(true);
    expect(frame.result.tools.map((tool) => tool.name)).toEqual(offered[0]);
    expect(
      frame.result.tools
        .filter((tool) => tool.readOnly)
        .map((tool) => tool.name),
    ).toEqual(offered[1]);
    expect(frame.result).toEqual({
      tools: toolReference({ ask: false, goal: false }),
      limits: LIMITS,
    });
  } finally {
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("a request with an id but no method is answered, not dropped", async () => {
  const side = sidecarChild({});
  try {
    side.send({ id: 5 });
    side.send({ id: 6, method: "hello" });
    expect(await side.frame((frame) => frame.id === 5)).toEqual({
      id: 5,
      ok: false,
      error: "Malformed request",
    });
    expect(await side.frame((frame) => frame.id === 6)).toMatchObject({
      ok: true,
    });
  } finally {
    side.child.kill();
    await side.child.exited;
  }
});

test("a start that fails before it is answered gets its error and frees its run", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    // Refused by resolveProvider at every start.
    LOCAL_AGENT_PROVIDER_CONFIG: "{}",
  });
  const start = (id: number, extra: Record<string, unknown> = {}) =>
    side.send({
      id,
      method: "agent.start",
      params: {
        runId: `r${id}`,
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
        ...extra,
      },
    });
  try {
    // More starts than the four concurrent runs: none of them is left
    // holding a slot.
    for (let id = 1; id <= 6; id++) {
      start(id);
      const answer = await side.frame((frame) => frame.id === id);
      expect(answer).toMatchObject({ ok: false });
      expect(String(answer.error)).toContain("LOCAL_AGENT_PROVIDER");
    }
  } finally {
    side.child.kill();
    await side.child.exited;
  }

  // A failure past the provider (here, the MCP config) answers the start
  // too, instead of leaving it pending behind stream events.
  const config = join(root, "mcp.json");
  await writeFile(config, "not json");
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: (request) =>
      new URL(request.url).pathname === "/api/tags"
        ? Response.json({ models: [{ name: "fixture", size: 1000 }] })
        : Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 16384 },
          }),
  });
  const mcp = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    mcp.send({
      id: 7,
      method: "agent.start",
      params: {
        runId: "r7",
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
        mcpConfig: config,
      },
    });
    const answer = await mcp.frame((frame) => frame.id === 7);
    expect(answer).toMatchObject({ ok: false });
    expect(mcp.frames.some((frame) => frame.stream === "r7")).toBe(false);
  } finally {
    mcp.child.kill();
    await mcp.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("an allowed command that names a secret path still asks, and the card carries the path", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await writeFile(join(root, ".env"), "TOKEN=planted");
  await appendRule(root, {
    effect: "allow",
    tool: "run_command",
    pattern: "cat *",
  });
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
      const message = {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            function: {
              name: "run_command",
              arguments: { argv: ["cat", "--file=.env"] },
            },
          },
        ],
      };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({
      id: 1,
      method: "agent.start",
      params: {
        runId: "r1",
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
      },
    });
    const card = await side.frame(
      (frame) =>
        frame.stream === "r1" &&
        (frame.event as { type?: string } | undefined)?.type ===
          "approval_requested",
    );
    expect((card.event as { proposal: unknown }).proposal).toMatchObject({
      kind: "command",
      argv: ["cat", "--file=.env"],
      command_line: "cat --file=.env",
      secret_paths: [".env"],
    });
  } finally {
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("a command with too many path words to check is flagged on its card as that, not as a secret path", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "la-sidecar-")));
  await writeFile(join(root, ".env"), "TOKEN=planted");
  await appendRule(root, {
    effect: "allow",
    tool: "run_command",
    pattern: "cat *",
  });
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
      const message = {
        role: "assistant",
        content: "",
        tool_calls: [
          {
            function: {
              name: "run_command",
              arguments: {
                argv: [
                  "cat",
                  ...Array.from({ length: 65 }, (_, i) => `w${i}.txt`),
                ],
              },
            },
          },
        ],
      };
      return new Response(`${JSON.stringify({ message, done: true })}\n`);
    },
  });
  const side = sidecarChild({
    DESKTOP_REPO_ROOT: root,
    OLLAMA_HOST: server.url.origin,
  });
  try {
    side.send({
      id: 1,
      method: "agent.start",
      params: {
        runId: "r1",
        workspace: root,
        intent: "build",
        model: "fixture",
        messages: [{ role: "user", content: "go" }],
      },
    });
    const card = await side.frame(
      (frame) =>
        frame.stream === "r1" &&
        (frame.event as { type?: string } | undefined)?.type ===
          "approval_requested",
    );
    expect((card.event as { proposal: unknown }).proposal).toMatchObject({
      kind: "command",
      unchecked_path_words: 65,
    });
    expect(
      (card.event as { proposal: Record<string, unknown> }).proposal,
    ).not.toHaveProperty("secret_paths");
  } finally {
    side.child.kill();
    await side.child.exited;
    await server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
