import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { cachedBinaryPath } from "@thelacanians/tuimark";
import { expect, test } from "vitest";
import { uiDirectory } from "#cli/tuimark/launch";
import type { Store } from "#cli/tuimark/store";

test("the UI files are found next to the launcher", () => {
  expect(uiDirectory()).toMatch(/tuimark\/ui$/);
});

test("a missing app.tui is reported with the folder that was searched", async () => {
  const empty = await mkdtemp(join(tmpdir(), "local-agent-ui-"));
  try {
    expect(() => uiDirectory(empty)).toThrow(/not found in .*ui$/);
    await mkdir(join(empty, "ui"));
    await writeFile(join(empty, "ui", "app.tui"), "<tui/>");
    expect(() => uiDirectory(empty)).toThrow(/not found in .*ui$/);
    await writeFile(join(empty, "ui", "inline.tui"), "<tui/>");
    expect(uiDirectory(empty)).toBe(join(empty, "ui"));
  } finally {
    await rm(empty, { recursive: true, force: true });
  }
});

// ---------------------------------------------------------- the real entry point

import type {
  AgentEvent,
  AgentOptions,
  Provider,
  Workspace,
} from "@local-agent/runtime";
import type { Host, HostExit, Ready } from "@thelacanians/tuimark";
import {
  binaryVersion,
  fallbackNotice,
  findBinary,
  INLINE_TERMINAL_RESET,
  MIN_TUIMARK,
  PINNED_TUIMARK,
  readyProblem,
  resolvePinned,
  runTuimark,
  TERMINAL_RESET,
  versionAtLeast,
} from "#cli/tuimark/launch";

const provider = {
  type: "ollama",
  model: "m",
  context_size: 4096,
} as Provider;

function fakeSpawn(
  ready: Partial<Ready> = {},
  control: { exit?: (reason: HostExit | Error) => void } = {},
) {
  const spawnedOnce = Promise.withResolvers<void>();
  const attachedOnce = Promise.withResolvers<void>();
  const calls = {
    closed: 0,
    batches: [] as unknown[][],
    handlers: new Map<string, (ev: unknown) => unknown>(),
    spawned: [] as { file: string; options?: unknown }[],
  };
  const spawn = (async (file: string, options?: unknown) => {
    calls.spawned.push({ file, options });
    spawnedOnce.resolve();
    let settle: (value: HostExit) => void = () => {};
    let fail: (error: Error) => void = () => {};
    const exited = new Promise<HostExit>((resolve, reject) => {
      settle = resolve;
      fail = reject;
    });
    exited.catch(() => {});
    control.exit = (reason) =>
      reason instanceof Error ? fail(reason) : settle(reason);
    const host = {
      ready: {
        version: "0.5.1",
        diagnostics: [],
        cols: 90,
        rows: 30,
        ...ready,
      },
      exited,
      on(action: string, handler: (ev: unknown) => unknown) {
        calls.handlers.set(action, handler);
        attachedOnce.resolve();
        return host;
      },
      async batch(writes: unknown[]) {
        calls.batches.push(writes);
        return { type: "ack", id: "x", ok: true };
      },
      async commit() {
        return { type: "ack", id: "x", ok: true };
      },
      async get() {
        return { found: false };
      },
      async close() {
        calls.closed++;
        settle({ reason: "signal", code: 143 });
      },
    };
    return host as unknown as Host;
  }) as unknown as typeof import("@thelacanians/tuimark").spawnHost;
  // `spawned`: the launcher called spawn. `attached`: the client then
  // registered its handlers, so the host can be driven and can exit.
  return {
    spawn,
    calls,
    spawned: spawnedOnce.promise,
    attached: attachedOnce.promise,
  };
}

function fakeStdout(isTTY: boolean) {
  const written: string[] = [];
  return {
    isTTY,
    columns: 100,
    written,
    write: (chunk: string) => written.push(chunk),
  };
}

const launch = (
  deps: Parameters<typeof runTuimark>[8],
  signal = new AbortController().signal,
  presentation: Parameters<typeof runTuimark>[6] = "fullscreen",
  theme = "auto",
) =>
  runTuimark(
    provider,
    signal,
    {} as Workspace,
    "build",
    undefined,
    [],
    presentation,
    theme,
    deps,
  );

/** Waits on a condition the code under test brings about (it fails after 1 s). */
async function until(ok: () => boolean) {
  for (let i = 0; i < 200 && !ok(); i++)
    await new Promise((r) => setTimeout(r, 5));
  if (!ok()) throw new Error(`until: still false after 1 s: ${ok}`);
}

test("the pinned release is the minimum, not the pre-release the SDK is packed as", () => {
  expect(PINNED_TUIMARK).toBe("0.5.1");
  expect(PINNED_TUIMARK).toBe(MIN_TUIMARK);
});

test("the pinned release is the exact SDK version the CLI depends on", async () => {
  // The SDK trusts its embedded checksums only when the version it is asked
  // for is its own; any other falls back to the release's own checksums.txt.
  const manifest = JSON.parse(
    await readFile(new URL("../../package.json", import.meta.url), "utf8"),
  ) as { dependencies: Record<string, string> };
  expect(manifest.dependencies["@thelacanians/tuimark"]).toBe(PINNED_TUIMARK);
});

test("versions compare as a minimum, not as equality", () => {
  expect(MIN_TUIMARK).toBe("0.5.1");
  expect(versionAtLeast("0.5.1", "0.5.1")).toBe(true);
  // A pre-release suffix is ignored on purpose: the SDK is packed as
  // "0.5.1-pre" and still runs this client, while a pre-release of the
  // version below the minimum does not.
  expect(versionAtLeast("0.5.1-pre", "0.5.1")).toBe(true);
  expect(versionAtLeast("v0.5.2-dev.3", "0.5.1")).toBe(true);
  expect(versionAtLeast("0.5.0-rc.1", "0.5.1")).toBe(false);
  expect(versionAtLeast("0.6.0-dev", "0.5.1")).toBe(true);
  expect(versionAtLeast("1.0.0", "0.5.1")).toBe(true);
  expect(versionAtLeast("0.4.6", "0.5.1")).toBe(false);
  expect(versionAtLeast("dev", "0.5.1")).toBe(false);
});

test("an old binary, or one that loads the document with errors, is refused with its path and version", async () => {
  const stdout = fakeStdout(true);
  const old = fakeSpawn({
    version: "0.4.6",
    diagnostics: [{ severity: "error", code: "V001", msg: "no textarea" }],
  });
  await expect(
    launch({ spawn: old.spawn, resolve: async () => "/opt/tuimark", stdout }),
  ).rejects.toThrow(
    /tuimark >= 0\.5\.1; \/opt\/tuimark is 0\.4\.6 and loads app\.tui with 1 error \(V001: no textarea\)/,
  );
  expect(old.calls.closed).toBe(1);
  expect(stdout.written).toEqual([TERMINAL_RESET]);
  // Nothing was attached: no action handler is registered.
  expect(old.calls.handlers.size).toBe(0);

  const broken = fakeSpawn({
    diagnostics: [{ severity: "error", code: "V001", msg: "x" }],
  });
  await expect(
    launch({
      spawn: broken.spawn,
      resolve: async () => "/b/tuimark",
      stdout: fakeStdout(false),
    }),
  ).rejects.toThrow(/\/b\/tuimark is 0\.5\.1 and loads app\.tui with 1 error/);
});

test("readyProblem accepts a newer binary and ignores warnings", () => {
  expect(
    readyProblem({ version: "0.5.1", diagnostics: [] }, "/b"),
  ).toBeUndefined();
  expect(
    readyProblem(
      { version: "0.5.1", diagnostics: [{ severity: "warning" }] },
      "/b",
    ),
  ).toBeUndefined();
});

test("inline needs a child that accepts commit; fullscreen does not ask", async () => {
  const ready = { version: "0.5.1", diagnostics: [] };
  expect(readyProblem(ready, "/b", "fullscreen")).toBeUndefined();
  expect(readyProblem(ready, "/b", "inline")).toMatch(
    /does not accept commit \(inline mode\)/,
  );
  expect(
    readyProblem(
      { ...ready, messages: ["set", "batch", "commit"] },
      "/b",
      "inline",
    ),
  ).toBeUndefined();

  const host = fakeSpawn(ready);
  await expect(
    launch(
      {
        spawn: host.spawn,
        resolve: async () => "/b",
        stdout: fakeStdout(true),
      },
      new AbortController().signal,
      "inline",
    ),
  ).rejects.toThrow(/does not accept commit/);
  expect(host.calls.closed).toBe(1);
  expect(host.calls.handlers.size).toBe(0);
});

test("the inline presentation spawns inline.tui with inline: true", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn(
    { messages: ["set", "batch", "get", "commit"] },
    control,
  );
  const done = launch(
    { spawn: host.spawn, resolve: async () => "/b", stdout: fakeStdout(true) },
    new AbortController().signal,
    "inline",
  );
  await host.attached;
  expect(host.calls.spawned).toHaveLength(1);
  expect(host.calls.spawned[0]?.file).toMatch(/inline\.tui$/);
  expect(host.calls.spawned[0]?.options).toMatchObject({ inline: true });
  const options = host.calls.spawned[0]?.options as {
    data: Record<string, unknown>;
  };
  const data = options.data;
  // The region starts without history; the scratch paths are present.
  expect(data.messages).toEqual([]);
  expect(data.commit_blocks).toEqual([]);
  expect(data.commit_buf).toMatchObject({ id: "commit" });
  control.exit?.({ reason: "quit", code: 0 });
  await done;

  const control2: { exit?: (reason: HostExit | Error) => void } = {};
  const host2 = fakeSpawn({}, control2);
  const done2 = launch({
    spawn: host2.spawn,
    resolve: async () => "/b",
    stdout: fakeStdout(true),
  });
  await host2.attached;
  expect(host2.calls.spawned[0]?.file).toMatch(/app\.tui$/);
  expect(host2.calls.spawned[0]?.options).toMatchObject({ inline: false });
  control2.exit?.({ reason: "quit", code: 0 });
  await done2;
});

test("a host that dies resets the terminal, only when stdout is a terminal", async () => {
  for (const isTTY of [true, false]) {
    const control: { exit?: (reason: HostExit | Error) => void } = {};
    const host = fakeSpawn({}, control);
    const stdout = fakeStdout(isTTY);
    const done = launch({
      spawn: host.spawn,
      resolve: async () => "/b",
      stdout,
    });
    await host.attached;
    control.exit?.(
      new Error('tuimark host exited without an "exit" message (code 137)'),
    );
    await expect(done).rejects.toThrow(/code 137/);
    expect(stdout.written).toEqual(isTTY ? [TERMINAL_RESET] : []);
  }
});

test("an error exit reported by the host resets the terminal too; a quit does not", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  const stdout = fakeStdout(true);
  const done = launch({ spawn: host.spawn, resolve: async () => "/b", stdout });
  await host.attached;
  control.exit?.({ reason: "error", error: "boom", code: 1 });
  await expect(done).rejects.toThrow("boom");
  expect(stdout.written).toEqual([TERMINAL_RESET]);

  const control2: { exit?: (reason: HostExit | Error) => void } = {};
  const host2 = fakeSpawn({}, control2);
  const stdout2 = fakeStdout(true);
  const done2 = launch({
    spawn: host2.spawn,
    resolve: async () => "/b",
    stdout: stdout2,
  });
  await host2.attached;
  control2.exit?.({ reason: "quit", code: 0 });
  await done2;
  expect(stdout2.written).toEqual([]);
});

test("the process signal closes the host and the launcher returns", async () => {
  const host = fakeSpawn();
  const abort = new AbortController();
  const done = launch(
    { spawn: host.spawn, resolve: async () => "/b", stdout: fakeStdout(true) },
    abort.signal,
  );
  await host.attached;
  abort.abort();
  await done;
  expect(host.calls.closed).toBe(1);
});

test("the turn is awaited before the launcher returns when the host dies mid-turn", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  let turnEnded = false;
  const aborted = Promise.withResolvers<void>();
  const leaseReleased = Promise.withResolvers<void>();
  const abort = new AbortController();
  const script = async function* (
    _p: Provider,
    _m: unknown,
    options: { signal: AbortSignal },
  ): AsyncGenerator<AgentEvent> {
    yield { type: "text", text: "partial" };
    await new Promise((resolve) =>
      options.signal.addEventListener("abort", resolve),
    );
    aborted.resolve();
    await leaseReleased.promise; // the runtime releasing its lease
    turnEnded = true;
  };
  const done = launch(
    {
      spawn: host.spawn,
      resolve: async () => "/b",
      stdout: fakeStdout(true),
      run: script,
    },
    abort.signal,
  );
  let returned = false;
  void done.then(() => {
    returned = true;
  });
  await host.attached;
  const send = host.calls.handlers.get("send");
  await send?.({
    seq: 1,
    action: "send",
    source: "composer",
    keys: {},
    value: "go",
  });
  control.exit?.({ reason: "quit", code: 0 });
  // The host is gone and the turn knows it, but the lease is still held: the
  // launcher must be waiting for it, not returning.
  await aborted.promise;
  await new Promise((r) => setImmediate(r));
  expect(returned).toBe(false);
  leaseReleased.resolve();
  await done;
  expect(turnEnded).toBe(true);
});

test("TUIMARK_MOTION=reduce (or 0) reaches the client: a whole turn writes no spinner", async () => {
  /** Runs one turn with a tool call and returns every write the host took. */
  async function turn(motion: string | undefined) {
    const before = process.env.TUIMARK_MOTION;
    if (motion === undefined) delete process.env.TUIMARK_MOTION;
    else process.env.TUIMARK_MOTION = motion;
    try {
      const control: { exit?: (reason: HostExit | Error) => void } = {};
      const host = fakeSpawn({}, control);
      const gate = Promise.withResolvers<void>();
      const script = async function* (): AsyncGenerator<AgentEvent> {
        yield { type: "tool_requested", id: "c1", name: "read" };
        yield { type: "tool_start", id: "c1", name: "read" };
        await gate.promise;
        yield { type: "done", inputTokens: 1, outputTokens: 1 };
      };
      const done = launch(
        {
          spawn: host.spawn,
          resolve: async () => "/b",
          stdout: fakeStdout(true),
          run: script,
        },
        new AbortController().signal,
      );
      await host.attached;
      await host.calls.handlers.get("send")?.({
        seq: 1,
        action: "send",
        source: "composer",
        keys: {},
        value: "go",
      });
      await until(() =>
        host.calls.batches.flat().some((write) => {
          const w = write as { path: string; value: unknown };
          return (
            w.path === "live" &&
            (w.value as { card?: { running: boolean } }).card?.running === true
          );
        }),
      );
      gate.resolve();
      control.exit?.({ reason: "quit", code: 0 });
      await done;
      return host.calls.batches.flat() as { path: string; value: unknown }[];
    } finally {
      if (before === undefined) delete process.env.TUIMARK_MOTION;
      else process.env.TUIMARK_MOTION = before;
    }
  }
  const spins = (writes: { path: string; value: unknown }[]) =>
    writes.filter(
      (w) =>
        (w.path === "head" && (w.value as { spin: boolean }).spin) ||
        (w.path === "live" &&
          (w.value as { card?: { spin: boolean } }).card?.spin),
    );
  // Motion on (the variable unset, or not a reducing value): spinners turn.
  expect(spins(await turn(undefined)).length).toBeGreaterThan(0);
  expect(spins(await turn("1")).length).toBeGreaterThan(0);
  // Reduced: the head's glyph and the running card's stay still.
  for (const motion of ["reduce", "0"]) {
    const writes = await turn(motion);
    expect(spins(writes)).toEqual([]);
    const live = writes.filter((w) => w.path === "live");
    expect(
      live.some(
        (w) => (w.value as { card?: { still: boolean } }).card?.still === true,
      ),
    ).toBe(true);
  }
});

test("a terminal resize that fails is reported on the status line, and the session goes on", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  let broken = false;
  let resize: () => void = () => {};
  const stdout = {
    isTTY: true,
    write: () => true,
    // The client reads the width when it wraps an open preview again.
    get columns(): number {
      if (broken) throw new Error("no terminal size");
      return 100;
    },
    on(_event: "resize", listener: () => void) {
      resize = listener;
    },
    off() {},
  };
  const approval = async function* (
    _p: Provider,
    _m: unknown,
    options: AgentOptions,
  ): AsyncGenerator<AgentEvent> {
    yield { type: "tool_requested", id: "e1", name: "edit" };
    await options.approve(
      {
        kind: "edit",
        path: "a.txt",
        before: "",
        after: "",
        diff: "-x\n+y",
        identity: "i",
      },
      options.signal,
    );
  };
  const done = launch({
    spawn: host.spawn,
    resolve: async () => "/b",
    stdout,
    run: approval,
  });
  await host.attached;
  await host.calls.handlers.get("send")?.({
    seq: 1,
    action: "send",
    source: "composer",
    keys: {},
    value: "edit it",
  });
  const written = () =>
    host.calls.batches.flat() as { path: string; value: unknown }[];
  await until(() => written().some((w) => w.path === "approval"));
  broken = true;
  resize();
  await until(() =>
    written().some(
      (w) =>
        w.path === "status" &&
        w.value === "Action resize failed: no terminal size",
    ),
  );
  broken = false;
  control.exit?.({ reason: "quit", code: 0 });
  await done;
});

test("findBinary returns the path, or the reason there is none, and never throws", async () => {
  const unasked = async (): Promise<string | undefined> => {
    throw new Error("the pinned release is not asked its version");
  };
  expect(
    await findBinary(async () => "/opt/tuimark", undefined, unasked),
  ).toEqual({
    bin: "/opt/tuimark",
  });
  expect(
    await findBinary(
      async () => {
        throw new Error("release v0.4.6 was not found");
      },
      undefined,
      unasked,
    ),
  ).toEqual({ error: "release v0.4.6 was not found" });
});

test("findBinary refuses a TUIMARK_BINARY older than the client, so the default client falls back", async () => {
  const named = (version: string | undefined) =>
    findBinary(
      async () => "/opt/old/tuimark",
      "/opt/old/tuimark",
      async (bin) => (bin === "/opt/old/tuimark" ? version : "9.9.9"),
    );
  expect(await named(MIN_TUIMARK)).toEqual({ bin: "/opt/old/tuimark" });
  expect(await named("1.0.0")).toEqual({ bin: "/opt/old/tuimark" });
  expect(await named("0.4.6")).toEqual({
    error: "TUIMARK_BINARY is /opt/old/tuimark, which is tuimark 0.4.6",
  });
  expect(await named(undefined)).toEqual({
    error: "TUIMARK_BINARY is /opt/old/tuimark, which names no tuimark version",
  });
  const unasked = async (): Promise<string | undefined> => {
    throw new Error("only the binary TUIMARK_BINARY names is asked");
  };
  // The SDK resolved something other than the override: it is not asked.
  expect(
    await findBinary(async () => "/cache/tuimark", "/opt/old/tuimark", unasked),
  ).toEqual({ bin: "/cache/tuimark" });
  // TUIMARK_BINARY names no file: the SDK's reason, unasked.
  expect(
    await findBinary(
      async () => {
        throw new Error("TUIMARK_BINARY is set to /nope, which is not a file");
      },
      "/nope",
      unasked,
    ),
  ).toEqual({ error: "TUIMARK_BINARY is set to /nope, which is not a file" });
});

test("the fall back's notice says why, what would do, and how to silence it", () => {
  expect(
    fallbackNotice(
      "TUIMARK_BINARY is /opt/old/tuimark, which is tuimark 0.4.6",
    ),
  ).toBe(
    `No usable tuimark binary (TUIMARK_BINARY is /opt/old/tuimark, which is tuimark 0.4.6); using the Vue client. Set TUIMARK_BINARY to a tuimark >= ${MIN_TUIMARK}, or pass --ui vue to silence this.\n`,
  );
  // A path is the user's text: it cannot draw on the terminal.
  expect(fallbackNotice("TUIMARK_BINARY is /tmp/\x1b[2Jx")).not.toContain(
    "\x1b",
  );
});

test("binaryVersion reads what --version names, and nothing from a binary that names none", async () => {
  const dir = await mkdtemp(join(tmpdir(), "la-tuimark-version-"));
  const script = async (name: string, body: string) => {
    const file = join(dir, name);
    await writeFile(file, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
    return file;
  };
  // A binary left running after the probe: `kill -0` still reaches it.
  const running = async (name: string) => {
    const pid = Number(await readFile(join(dir, `${name}.pid`), "utf8"));
    await new Promise((resolve) => setTimeout(resolve, 100));
    try {
      process.kill(pid, 0);
      return true;
    } catch {
      return false;
    }
  };
  try {
    expect(
      await binaryVersion(
        await script("old", 'echo "tuimark 0.4.6 (commit abc, built today)"'),
      ),
    ).toBe("0.4.6");
    expect(
      await binaryVersion(await script("dev", 'echo "tuimark v0.6.0-dev"')),
    ).toBe("v0.6.0-dev");
    expect(
      await binaryVersion(await script("other", 'echo "glyph 0.5.1"')),
    ).toBeUndefined();
    expect(
      await binaryVersion(await script("silent", "exit 0")),
    ).toBeUndefined();
    expect(await binaryVersion(join(dir, "missing"))).toBeUndefined();
    // Only the head is read: a version then an endless flood names the
    // version at once, not at the deadline.
    {
      const started = Date.now();
      expect(
        await binaryVersion(
          await script(
            "loud",
            `echo $$ > ${join(dir, "loud.pid")}; echo "tuimark 0.5.1"; exec yes flood`,
          ),
          3_000,
        ),
      ).toBe("0.5.1");
      expect(Date.now() - started).toBeLessThan(1_500);
      expect(await running("loud")).toBe(false);
    }
    // The deadline holds when the binary itself exits on SIGTERM, when it
    // ignores SIGTERM, and when a child it left behind holds its output.
    for (const [name, body] of [
      ["stuck", "exec sleep 5"],
      ["deaf", `echo $$ > ${join(dir, "deaf.pid")}; trap '' TERM; sleep 5`],
      ["orphan", "sleep 5 & echo partial; wait"],
    ] as const) {
      const started = Date.now();
      expect(
        await binaryVersion(await script(name, body), 200),
        name,
      ).toBeUndefined();
      expect(Date.now() - started, name).toBeLessThan(3_000);
    }
    expect(await running("deaf")).toBe(false);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

// ------------------------------------------------------- the sessions capability

import { SessionStore } from "@local-agent/runtime";
import { sessionCapability } from "#cli/tuimark/launch";

test("the capability lists and loads through the store main uses", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-capability-"));
  const store = await SessionStore.open(join(root, "sessions.db"));
  try {
    const saved = await store.createSession(root, [
      { role: "user", content: "saved hi" },
      { role: "assistant", content: "saved hello" },
    ]);
    const capability = sessionCapability(store, root);
    expect(await capability.list()).toEqual([
      { handle: saved.publicId, messages: 2 },
    ]);
    const loaded = await capability.load(saved.publicId);
    expect(loaded.store).toBe(store);
    expect(loaded.state.publicId).toBe(saved.publicId);
    expect(loaded.state.messages).toEqual(saved.messages);
    await expect(capability.load("nope404")).rejects.toThrow(
      "session not found",
    );
  } finally {
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("load closes calls that never ran, loads an unknown outcome for /recover, and refuses a broken history", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-capability-"));
  const store = await SessionStore.open(join(root, "sessions.db"));
  const asked = (id: string, name: string) => ({
    role: "assistant" as const,
    content: "",
    tool_calls: [{ id, function: { name, arguments: {} } }],
  });
  try {
    const capability = sessionCapability(store, root);
    // Interrupted before its call was recorded: closed as not run.
    const never = await store.createSession(root, [
      { role: "user", content: "go" },
      asked("c1", "edit"),
    ]);
    const loaded = await capability.load(never.publicId);
    expect(loaded.state.messages.at(-1)).toMatchObject({
      role: "tool",
      tool_call_id: "c1",
    });
    expect(loaded.state.messages.at(-1)?.content).toContain("Not run");
    // Started and maybe applied: it loads, and /recover lists it.
    const ran = await store.createSession(root, [
      { role: "user", content: "go" },
      asked("c2", "write"),
    ]);
    const base = {
      sessionId: ran.id,
      workspaceId: ran.workspaceId,
      runId: "r",
      turnId: "r",
      executionId: "e2",
      idempotencyKey: "e2",
      canonicalCallId: "c2",
      providerCallId: "",
      iteration: 1,
      ordinal: 1,
      kind: "builtin" as const,
      toolName: "write",
      effectClass: "effectful" as const,
      argumentsSha256: "0".repeat(64),
    };
    store.appendEvent({ ...base, type: "requested" });
    store.appendEvent({ ...base, type: "started" });
    const blocked = await capability.load(ran.publicId);
    expect(capability.recovery?.(blocked)).toMatchObject([
      { executionId: "e2", toolName: "write", state: "ran" },
    ]);
    const after = await capability.reconcile?.(
      blocked,
      "e2",
      "effect_applied",
      "file is there",
    );
    expect(after?.closed).toBe(1);
    expect(after?.state.messages.at(-1)?.content).toContain(
      "The user checked: it ran and its change is in place. Their note: file is there",
    );
    if (!after) throw new Error("reconcile is part of the capability");
    expect(capability.recovery?.({ store, state: after.state })).toEqual([]);
    // A history no settling explains is refused in the runtime's words.
    const broken = await store.createSession(root, [
      asked("c3", "read"),
      { role: "user", content: "between" },
      { role: "tool", content: "{}", tool_name: "read", tool_call_id: "c3" },
    ]);
    await expect(capability.load(broken.publicId)).rejects.toThrow(
      "recovery is required",
    );
  } finally {
    store.close();
    await rm(root, { recursive: true, force: true });
  }
});

// ----------------------------------------------------------------- --theme

import { existsSync, realpathSync } from "node:fs";
import { themedDocument } from "#cli/tuimark/themes";

test("a builtin --theme is passed to the host; the default sends none", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  const done = launch(
    { spawn: host.spawn, resolve: async () => "/b", stdout: fakeStdout(true) },
    new AbortController().signal,
    "fullscreen",
    "dark",
  );
  await host.spawned;
  expect(host.calls.spawned[0]?.file).toMatch(/app\.tui$/);
  expect(host.calls.spawned[0]?.options).toMatchObject({ theme: "dark" });
  control.exit?.({ reason: "quit", code: 0 });
  await done;

  const control2: { exit?: (reason: HostExit | Error) => void } = {};
  const host2 = fakeSpawn({}, control2);
  const done2 = launch({
    spawn: host2.spawn,
    resolve: async () => "/b",
    stdout: fakeStdout(true),
  });
  await host2.spawned;
  expect(
    (host2.calls.spawned[0]?.options as Record<string, unknown> | undefined)
      ?.theme,
  ).toBeUndefined();
  control2.exit?.({ reason: "quit", code: 0 });
  await done2;
});

test("a named --theme spawns a generated document pinned to dark, then removes it", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "local-agent-theme-"));
  const doc = join(scratch, "midnight-app.tui");
  await writeFile(doc, "<tui/>");
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn(
    { messages: ["set", "batch", "get", "commit"] },
    control,
  );
  const done = launch(
    {
      spawn: host.spawn,
      resolve: async () => "/b",
      stdout: fakeStdout(true),
      writeThemeDoc: async () => doc,
    },
    new AbortController().signal,
    "inline",
    "midnight",
  );
  await host.spawned;
  expect(host.calls.spawned[0]?.file).toBe(doc);
  expect(host.calls.spawned[0]?.options).toMatchObject({
    theme: "dark",
    inline: true,
  });
  control.exit?.({ reason: "quit", code: 0 });
  await done;
  // The host has loaded the document, so the generated copy is gone.
  expect(existsSync(doc)).toBe(false);
  await rm(scratch, { recursive: true, force: true });
});

test("an unknown --theme name is refused with the themes that exist", async () => {
  await expect(
    launch(
      {
        spawn: fakeSpawn().spawn,
        resolve: async () => "/b",
        stdout: fakeStdout(true),
      },
      new AbortController().signal,
      "fullscreen",
      "solarized",
    ),
  ).rejects.toThrow(
    /^Unknown theme: solarized\. Available themes: dark, light, auto, catppuccin-frappe, .*, midnight, .*, sepia, tokyo-night, tokyo-night-storm$/,
  );
});

test("themedDocument loads the palette after the document's last sheet", async () => {
  const ui = await mkdtemp(join(tmpdir(), "local-agent-doc-"));
  try {
    await writeFile(
      join(ui, "app.tui"),
      [
        '<tui theme="auto">',
        '  <include src="kit/kit.tui"/>',
        '  <style src="app.tcss"/>',
        "</tui>",
        "",
      ].join("\n"),
    );
    // Its folder is new and private, not the client's own (which an install
    // may keep read-only), so every static src is absolute.
    const path = await themedDocument(ui, "app.tui", "sepia");
    expect(basename(path)).toBe("sepia-app.tui");
    expect(basename(dirname(path))).toMatch(/^local-agent-theme-/);
    expect(dirname(path)).not.toBe(ui);
    expect(await Bun.file(path).text()).toBe(
      [
        '<tui theme="auto">',
        `  <include src="${join(ui, "kit/kit.tui")}"/>`,
        `  <style src="${join(ui, "app.tcss")}"/>`,
        `  <style src="${join(ui, "themes/sepia.tcss")}"/>`,
        "</tui>",
        "",
      ].join("\n"),
    );
    await rm(dirname(path), { recursive: true, force: true });
    // A data-bound src stays as it is; an absolute one is kept.
    await writeFile(
      join(ui, "app.tui"),
      '<tui>\n  <style src="/abs/a.tcss"/>\n  <img src="{im.src}"/>\n</tui>\n',
    );
    const bound = await themedDocument(ui, "app.tui", "nord");
    expect(await Bun.file(bound).text()).toContain('<img src="{im.src}"/>');
    expect(await Bun.file(bound).text()).toContain(
      '<style src="/abs/a.tcss"/>',
    );
    await rm(dirname(bound), { recursive: true, force: true });
  } finally {
    await rm(ui, { recursive: true, force: true });
  }
});

test("a compiled release finds ui/ next to its real executable", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-ui-"));
  try {
    const release = join(root, "share", "local-agent");
    await mkdir(join(release, "ui"), { recursive: true });
    await writeFile(join(release, "ui", "app.tui"), "<tui/>");
    await writeFile(join(release, "ui", "inline.tui"), "<tui/>");
    await writeFile(join(release, "local-agent"), "");
    // On PATH through a symlink, as Homebrew and the installer leave it.
    await mkdir(join(root, "bin"));
    await symlink(
      join(release, "local-agent"),
      join(root, "bin", "local-agent"),
    );
    // A compiled module's own folder is inside the executable: no ui/ there.
    const bundled = "/$bunfs/root";
    expect(uiDirectory(bundled, join(root, "bin", "local-agent"))).toBe(
      join(realpathSync(release), "ui"),
    );
    expect(() => uiDirectory(bundled, join(root, "elsewhere"))).toThrow(
      /Tuimark UI files not found in \/\$bunfs\/root\/ui or /,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("inline resets the terminal without leaving an alternate screen it never entered", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn(
    { messages: ["set", "batch", "get", "commit"] },
    control,
  );
  const stdout = fakeStdout(true);
  const done = launch(
    { spawn: host.spawn, resolve: async () => "/b", stdout },
    new AbortController().signal,
    "inline",
  );
  await host.attached;
  control.exit?.({ reason: "error", error: "boom", code: 1 });
  await expect(done).rejects.toThrow("boom");
  expect(stdout.written).toEqual([INLINE_TERMINAL_RESET]);
  expect(INLINE_TERMINAL_RESET).not.toContain("\x1b[?1049l");
  expect(TERMINAL_RESET).toBe(`\x1b[?1049l${INLINE_TERMINAL_RESET}`);
});

test("a spawn that fails resets the terminal and is reported", async () => {
  for (const [presentation, reset] of [
    ["fullscreen", TERMINAL_RESET],
    ["inline", INLINE_TERMINAL_RESET],
  ] as const) {
    const stdout = fakeStdout(true);
    const spawn = (async () => {
      throw new Error("tuimark host exited before ready (code 2)");
    }) as unknown as typeof import("@thelacanians/tuimark").spawnHost;
    await expect(
      launch(
        { spawn, resolve: async () => "/b", stdout },
        new AbortController().signal,
        presentation,
      ),
    ).rejects.toThrow(/before ready/);
    expect(stdout.written).toEqual([reset]);
  }
});

test("ctrl+c while the binary resolves stops before a host is spawned", async () => {
  const host = fakeSpawn();
  const abort = new AbortController();
  const done = launch(
    {
      spawn: host.spawn,
      resolve: async () => {
        abort.abort();
        return "/b";
      },
      stdout: fakeStdout(true),
    },
    abort.signal,
  );
  await expect(done).rejects.toThrow();
  expect(host.calls.spawned).toHaveLength(0);
});

test("a handler that throws goes to onError: a status line, and the session goes on", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  const done = launch({
    spawn: host.spawn,
    resolve: async () => "/b",
    stdout: fakeStdout(true),
  });
  await host.attached;
  const options = host.calls.spawned[0]?.options as {
    onError?: (event: { action: string }, error: unknown) => unknown;
  };
  // Returning (not throwing) is what keeps the SDK's reply plain.
  await expect(
    Promise.resolve(
      options.onError?.({ action: "dialog_always" }, new Error("EACCES")),
    ),
  ).resolves.toBeUndefined();
  expect(host.calls.batches.flat()).toContainEqual({
    path: "status",
    value: "Action dialog_always failed: EACCES",
  });
  control.exit?.({ reason: "quit", code: 0 });
  await done;
});

// -------------------------------------------------- the binary and the model check

test("a binary that is cached costs no output; a download says so once, before it starts", async () => {
  const home = await mkdtemp(join(tmpdir(), "local-agent-cache-"));
  try {
    const offline = { home, platform: "darwin", arch: "arm64", env: {} };
    const options = { ...offline, embedded: null };
    const cached = cachedBinaryPath(PINNED_TUIMARK, offline);
    await mkdir(dirname(cached), { recursive: true });
    await writeFile(cached, "#!/bin/sh\n");
    const quiet: string[] = [];
    expect(await resolvePinned(options, (text) => quiet.push(text))).toBe(
      cached,
    );
    expect(quiet).toEqual([]);

    await rm(cached);
    const said: string[] = [];
    const asked: number[] = [];
    await expect(
      resolvePinned(
        {
          ...options,
          fetch: async () => {
            asked.push(said.length);
            throw new Error("offline");
          },
        },
        (text) => said.push(text),
      ),
    ).rejects.toThrow(/cannot download/);
    // One line, already written when the first request went out.
    expect(said).toEqual([`Downloading Tuimark ${PINNED_TUIMARK}…\n`]);
    expect(asked).toEqual([1]);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test("the model check does not hold the screen back: the first frame says it is checking, and the planned provider takes over", async () => {
  const check = Promise.withResolvers<Provider>();
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  const done = launch({
    spawn: host.spawn,
    resolve: async () => "/b",
    stdout: fakeStdout(true),
    admission: check.promise,
  });
  // The screen is up and its composer takes input while the check is pending.
  await host.attached;
  const data = (host.calls.spawned[0]?.options as { data: Store } | undefined)
    ?.data;
  expect(data?.status).toBe("build · Checking model…");
  expect(data?.header.ctx_label).toBe("0/4.0k");
  check.resolve({ ...provider, context_size: 8192 });
  await until(() =>
    (host.calls.batches.flat() as { path: string; value: unknown }[]).some(
      (w) => w.path === "header.ctx_label" && w.value === "0/8.0k",
    ),
  );
  control.exit?.({ reason: "quit", code: 0 });
  await done;
});

test("a model the check refuses while the screen is up closes it and fails the run with that error", async () => {
  const check = Promise.withResolvers<Provider>();
  const host = fakeSpawn();
  const stdout = fakeStdout(true);
  const done = launch({
    spawn: host.spawn,
    resolve: async () => "/b",
    stdout,
    admission: check.promise,
  });
  const failed = expect(done).rejects.toThrow(
    "Model m is not installed. Run: ollama pull m",
  );
  await host.attached;
  check.reject(new Error("Model m is not installed. Run: ollama pull m"));
  await failed;
  expect(host.calls.closed).toBe(1);
  expect(stdout.written).toEqual([TERMINAL_RESET]);
});

test("a refusal that is already in opens no screen, and an answer that is already in opens it on the planned provider", async () => {
  const refused = Promise.reject(new Error("Model m is not installed."));
  refused.catch(() => {});
  const none = fakeSpawn();
  await expect(
    launch({
      spawn: none.spawn,
      resolve: async () => "/b",
      stdout: fakeStdout(true),
      admission: refused,
    }),
  ).rejects.toThrow("Model m is not installed.");
  expect(none.calls.spawned).toHaveLength(0);

  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  const done = launch({
    spawn: host.spawn,
    resolve: async () => "/b",
    stdout: fakeStdout(true),
    admission: Promise.resolve({ ...provider, context_size: 8192 }),
  });
  await host.attached;
  const data = (host.calls.spawned[0]?.options as { data: Store } | undefined)
    ?.data;
  expect(data?.status).toBe("build");
  expect(data?.header.ctx_label).toBe("0/8.0k");
  control.exit?.({ reason: "quit", code: 0 });
  await done;
});
