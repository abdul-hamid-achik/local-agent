import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { uiDirectory } from "#cli/tuimark/launch";

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

import type { AgentEvent, Provider, Workspace } from "@local-agent/runtime";
import type { Host, HostExit, Ready } from "@thelacanians/tuimark";
import {
  findBinary,
  MIN_TUIMARK,
  PINNED_TUIMARK,
  readyProblem,
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
  const calls = {
    closed: 0,
    handlers: new Map<string, (ev: unknown) => unknown>(),
    spawned: [] as { file: string; options?: unknown }[],
  };
  const spawn = (async (file: string, options?: unknown) => {
    calls.spawned.push({ file, options });
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
        version: "0.4.6",
        diagnostics: [],
        cols: 90,
        rows: 30,
        ...ready,
      },
      exited,
      on(action: string, handler: (ev: unknown) => unknown) {
        calls.handlers.set(action, handler);
        return host;
      },
      async batch() {
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
  return { spawn, calls };
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
  deps: Parameters<typeof runTuimark>[7],
  signal = new AbortController().signal,
  presentation: Parameters<typeof runTuimark>[6] = "fullscreen",
) =>
  runTuimark(
    provider,
    signal,
    {} as Workspace,
    "build",
    undefined,
    [],
    presentation,
    deps,
  );

test("the pinned release is the minimum, not the pre-release the SDK is packed as", () => {
  expect(PINNED_TUIMARK).toBe("0.4.6");
  expect(PINNED_TUIMARK).toBe(MIN_TUIMARK);
});

test("versions compare as a minimum, not as equality", () => {
  expect(MIN_TUIMARK).toBe("0.4.6");
  expect(versionAtLeast("0.4.6", "0.4.6")).toBe(true);
  expect(versionAtLeast("0.4.6-pre", "0.4.6")).toBe(true);
  expect(versionAtLeast("0.4.7", "0.4.6")).toBe(true);
  expect(versionAtLeast("0.5.0-dev", "0.4.6")).toBe(true);
  expect(versionAtLeast("1.0.0", "0.4.6")).toBe(true);
  expect(versionAtLeast("0.4.5", "0.4.6")).toBe(false);
  expect(versionAtLeast("dev", "0.4.6")).toBe(false);
});

test("an old binary, or one that loads the document with errors, is refused with its path and version", async () => {
  const stdout = fakeStdout(true);
  const old = fakeSpawn({
    version: "0.4.5",
    diagnostics: [{ severity: "error", code: "V001", msg: "no textarea" }],
  });
  await expect(
    launch({ spawn: old.spawn, resolve: async () => "/opt/tuimark", stdout }),
  ).rejects.toThrow(
    /tuimark >= 0\.4\.6; \/opt\/tuimark is 0\.4\.5 and loads app\.tui with 1 error \(V001: no textarea\)/,
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
  ).rejects.toThrow(/\/b\/tuimark is 0\.4\.6 and loads app\.tui with 1 error/);
});

test("readyProblem accepts a newer binary and ignores warnings", () => {
  expect(
    readyProblem({ version: "0.4.7", diagnostics: [] }, "/b"),
  ).toBeUndefined();
  expect(
    readyProblem(
      { version: "0.4.6", diagnostics: [{ severity: "warning" }] },
      "/b",
    ),
  ).toBeUndefined();
});

test("inline needs a child that accepts commit; fullscreen does not ask", async () => {
  const ready = { version: "0.4.6", diagnostics: [] };
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
  await new Promise((r) => setTimeout(r, 10));
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
  await new Promise((r) => setTimeout(r, 10));
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
    await new Promise((r) => setTimeout(r, 10));
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
  await new Promise((r) => setTimeout(r, 10));
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
  await new Promise((r) => setTimeout(r, 10));
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
  await new Promise((r) => setTimeout(r, 10));
  abort.abort();
  await done;
  expect(host.calls.closed).toBe(1);
});

test("the turn is awaited before the launcher returns when the host dies mid-turn", async () => {
  const control: { exit?: (reason: HostExit | Error) => void } = {};
  const host = fakeSpawn({}, control);
  let turnEnded = false;
  let release: () => void = () => {};
  const finished = new Promise<void>((r) => {
    release = r;
  });
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
    await new Promise((r) => setTimeout(r, 30)); // the runtime releasing its lease
    turnEnded = true;
    release();
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
  await new Promise((r) => setTimeout(r, 10));
  const send = host.calls.handlers.get("send");
  await send?.({
    seq: 1,
    action: "send",
    source: "composer",
    keys: {},
    value: "go",
  });
  control.exit?.({ reason: "quit", code: 0 });
  await done;
  expect(turnEnded).toBe(true);
  await finished;
});

test("findBinary returns the path, or the reason there is none, and never throws", async () => {
  expect(await findBinary(async () => "/opt/tuimark")).toEqual({
    bin: "/opt/tuimark",
  });
  expect(
    await findBinary(async () => {
      throw new Error("release v0.4.6 was not found");
    }),
  ).toEqual({ error: "release v0.4.6 was not found" });
});

// ------------------------------------------------------- the sessions capability

import { SessionStore } from "@local-agent/runtime";
import { sessionCapability, sessionRefusal } from "#cli/tuimark/launch";

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

test("sessionRefusal mirrors runAgent's resume checks in its own words", () => {
  const clean = { listHazards: () => [] };
  const state = (messages: unknown[]) =>
    ({ id: 1, workspaceId: "/w", messages }) as never;
  expect(
    sessionRefusal(clean as never, state([{ role: "user", content: "hi" }])),
  ).toBeUndefined();
  expect(
    sessionRefusal(
      { listHazards: () => [{}] } as never,
      state([{ role: "user", content: "hi" }]),
    ),
  ).toBe(
    "Session has an uncertain tool outcome; inspect recovery before continuing",
  );
  const interrupted = sessionRefusal(
    clean as never,
    state([
      {
        role: "assistant",
        content: "",
        tool_calls: [{ id: "c1", function: { name: "edit", arguments: {} } }],
      },
    ]),
  );
  expect(interrupted).toBe(
    "Session contains interrupted tool calls; recovery is required",
  );
  const matched = sessionRefusal(
    clean as never,
    state([
      {
        role: "assistant",
        content: "",
        tool_calls: [{ id: "c1", function: { name: "edit", arguments: {} } }],
      },
      { role: "tool", content: "{}", tool_name: "edit", tool_call_id: "c1" },
      { role: "assistant", content: "done" },
    ]),
  );
  expect(matched).toBeUndefined();
});
