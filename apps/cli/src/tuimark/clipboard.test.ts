import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  realpathSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  CLIPBOARD_TOOL_NAMES,
  type ClipboardTool,
  characters,
  charCount,
  clipboardEnv,
  clipboardText,
  clipboardTools,
  copyToClipboard,
  type RunResult,
  resolveTool,
  selectionHint,
  selectionKey,
  spawnTool,
} from "#cli/tuimark/clipboard";

const tools: ClipboardTool[] = [
  { name: "one", argv: ["one"] },
  { name: "two", argv: ["two", "-x"] },
  { name: "three", argv: ["three"] },
];

/** Every program is found under /bin; the lookup itself is tested apart. */
const resolve = (name: string) => `/bin/${name}`;

/** A runner that answers per program and records what it was given. */
function runner(answers: Record<string, RunResult>) {
  const calls: {
    argv: readonly string[];
    text: string;
    env: Record<string, string>;
  }[] = [];
  return {
    calls,
    run: async (
      argv: readonly string[],
      text: string,
      env: Record<string, string>,
    ): Promise<RunResult> => {
      calls.push({ argv, text, env });
      return (
        answers[argv[0]?.replace("/bin/", "") ?? ""] ?? { kind: "missing" }
      );
    },
  };
}

test("the platform's tools, in order: pbcopy, then wl-copy, xclip, xsel", () => {
  expect(clipboardTools("darwin").map((t) => t.argv)).toEqual([["pbcopy"]]);
  expect(clipboardTools("linux").map((t) => t.argv)).toEqual([
    ["wl-copy"],
    ["xclip", "-selection", "clipboard"],
    ["xsel", "--clipboard", "--input"],
  ]);
  expect(clipboardTools("win32")).toEqual([]);
  expect(CLIPBOARD_TOOL_NAMES).toBe("pbcopy, wl-copy, xclip, xsel");
});

test("the first tool that runs and exits 0 takes the text on its stdin", async () => {
  const { run, calls } = runner({
    one: { kind: "missing" },
    two: { kind: "exited", code: 0 },
    three: { kind: "exited", code: 0 },
  });
  expect(
    await copyToClipboard("hello\nworld", { tools, run, resolve }),
  ).toEqual({ ok: true, tool: "two" });
  // Tried in order, stopped at the first that worked, text passed whole, the
  // program by the absolute path the lookup gave.
  expect(calls.map((c) => [c.argv, c.text])).toEqual([
    [["/bin/one"], "hello\nworld"],
    [["/bin/two", "-x"], "hello\nworld"],
  ]);
});

test("a tool that fails falls through to the next, one that works wins", async () => {
  const { run } = runner({
    one: { kind: "exited", code: 1 },
    three: { kind: "exited", code: 0 },
  });
  expect(await copyToClipboard("x", { tools, run, resolve })).toEqual({
    ok: true,
    tool: "three",
  });
});

test("a timeout stops the chain: the tool may have set the clipboard, the next would write it twice", async () => {
  const { run, calls } = runner({
    one: { kind: "timeout" },
    two: { kind: "exited", code: 0 },
  });
  expect(await copyToClipboard("x", { tools, run, resolve })).toEqual({
    ok: false,
    reason: "timeout",
    tool: "one",
  });
  expect(calls).toHaveLength(1);
});

test("no tool at all is 'none'; a tool that ran and failed is reported by name", async () => {
  expect(
    await copyToClipboard("x", { tools, run: runner({}).run, resolve }),
  ).toEqual({ ok: false, reason: "none" });
  expect(
    await copyToClipboard("x", { tools: [], run: runner({}).run, resolve }),
  ).toEqual({ ok: false, reason: "none" });
  // A program the lookup does not find is never run.
  const unfound = runner({ one: { kind: "exited", code: 0 } });
  expect(
    await copyToClipboard("x", {
      tools,
      run: unfound.run,
      resolve: () => undefined,
    }),
  ).toEqual({ ok: false, reason: "none" });
  expect(unfound.calls).toEqual([]);
  const failed = runner({
    one: { kind: "exited", code: 1 },
    two: { kind: "missing" },
    three: { kind: "missing" },
  });
  expect(
    await copyToClipboard("x", { tools, run: failed.run, resolve }),
  ).toEqual({
    ok: false,
    reason: "failed",
    tool: "one",
    detail: "exited with code 1",
  });
});

test("the real runner writes the text to a program's stdin and reports a missing program", async () => {
  // `cat` reads stdin and exits 0: the spawn path, with no clipboard involved.
  expect(await spawnTool(["cat"], "text for stdin", {})).toEqual({
    kind: "exited",
    code: 0,
  });
  expect(
    await spawnTool(["sh", "-c", "cat >/dev/null; exit 3"], "x", {}),
  ).toEqual({ kind: "exited", code: 3 });
  expect(
    await spawnTool(["local-agent-no-such-clipboard-tool"], "x", {}),
  ).toEqual({ kind: "missing" });
});

test("a tool that never reads its stdin is killed when the time is up, even for a text that fills the pipe", async () => {
  const started = Date.now();
  // 4 MB to a program that never reads: the write blocks for good, so the
  // timer must already be running while it is fed.
  const result = await spawnTool(
    ["sleep", "30"],
    "x".repeat(4 * 1024 * 1024),
    {},
    300,
  );
  expect(result).toEqual({ kind: "timeout" });
  expect(Date.now() - started).toBeLessThan(5000);
});

test("the tool gets exactly the environment it was given, not the process's", async () => {
  // `env -i` prints nothing of ours: the spawned program has only `env`.
  const dir = mkdtempSync(join(tmpdir(), "clip-env-"));
  const out = join(dir, "seen");
  process.env.LA_TEST_SECRET = "must-not-leak";
  try {
    const result = await spawnTool(
      [
        "sh",
        "-c",
        `printenv LA_TEST_SECRET > ${out}; printenv KEPT >> ${out}; true`,
      ],
      "x",
      { KEPT: "yes", PATH: process.env.PATH ?? "/usr/bin:/bin" },
    );
    expect(result).toEqual({ kind: "exited", code: 0 });
    expect(await Bun.file(out).text()).toBe("yes\n");
  } finally {
    delete process.env.LA_TEST_SECRET;
  }
});

test("the environment of a tool: the session's display and locale, never the rest", () => {
  const source = {
    PATH: "/usr/bin:/bin",
    HOME: "/home/me",
    LANG: "de_DE.UTF-8",
    DISPLAY: ":0",
    WAYLAND_DISPLAY: "wayland-0",
    XDG_RUNTIME_DIR: "/run/user/1000",
    XAUTHORITY: "/home/me/.Xauthority",
    DBUS_SESSION_BUS_ADDRESS: "unix:path=/run/user/1000/bus",
    __CF_USER_TEXT_ENCODING: "0x1F5:0x8000100:0x0",
    ANTHROPIC_API_KEY: "sk-secret",
    OPENAI_API_KEY: "sk-other",
    TERM_PROGRAM: "iTerm.app",
  };
  const linux = clipboardEnv(source, "linux");
  expect(linux).toEqual({
    PATH: "/usr/bin:/bin",
    HOME: "/home/me",
    LANG: "de_DE.UTF-8",
    DISPLAY: ":0",
    WAYLAND_DISPLAY: "wayland-0",
    XDG_RUNTIME_DIR: "/run/user/1000",
    XAUTHORITY: "/home/me/.Xauthority",
    DBUS_SESSION_BUS_ADDRESS: "unix:path=/run/user/1000/bus",
  });
  expect(clipboardEnv(source, "darwin").__CF_USER_TEXT_ENCODING).toBe(
    "0x1F5:0x8000100:0x0",
  );
  // pbcopy takes its encoding from the locale: with none set, UTF-8.
  expect(clipboardEnv({ PATH: "/bin" }, "darwin")).toEqual({
    PATH: "/bin",
    LANG: "en_US.UTF-8",
  });
  expect(clipboardEnv({ PATH: "/bin", LC_ALL: "C.UTF-8" }, "linux")).toEqual({
    PATH: "/bin",
    LC_ALL: "C.UTF-8",
  });
});

test("copyToClipboard hands the tool the minimal environment and the resolved path", async () => {
  const { run, calls } = runner({ one: { kind: "exited", code: 0 } });
  await copyToClipboard("x", {
    tools,
    run,
    resolve,
    platform: "linux",
    env: { PATH: "/usr/bin", HOME: "/h", ANTHROPIC_API_KEY: "sk-secret" },
  });
  expect(calls[0]?.argv).toEqual(["/bin/one"]);
  expect(calls[0]?.env).toEqual({
    PATH: "/usr/bin",
    HOME: "/h",
    LANG: "en_US.UTF-8",
  });
});

test("a tool is found only through absolute PATH entries outside the workspace and the cwd", () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "clip-lookup-")));
  const plant = (dir: string) => {
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, "pbcopy"), "#!/bin/sh\n");
    chmodSync(join(dir, "pbcopy"), 0o755);
    return dir;
  };
  const good = plant(join(root, "good"));
  const workspace = plant(join(root, "workspace", "bin"));
  const cwd = plant(join(root, "cwd"));
  const path = (...entries: string[]) => entries.join(":");
  // The system one.
  expect(resolveTool("pbcopy", path(good), [])).toBe(join(good, "pbcopy"));
  // A "." or empty entry, or a relative one, is never searched (the cwd has a pbcopy).
  const here = process.cwd();
  process.chdir(cwd);
  try {
    expect(resolveTool("pbcopy", path(".", ""), [])).toBeUndefined();
    expect(resolveTool("pbcopy", path("", ".", "cwd", good), [])).toBe(
      join(good, "pbcopy"),
    );
  } finally {
    process.chdir(here);
  }
  // An absolute entry inside the workspace or the cwd is skipped, the next one used.
  expect(
    resolveTool("pbcopy", path(workspace, good), [join(root, "workspace")]),
  ).toBe(join(good, "pbcopy"));
  expect(resolveTool("pbcopy", path(cwd, good), [cwd])).toBe(
    join(good, "pbcopy"),
  );
  expect(
    resolveTool("pbcopy", path(workspace), [join(root, "workspace")]),
  ).toBeUndefined();
  // A file that is not executable is not a tool.
  chmodSync(join(good, "pbcopy"), 0o644);
  expect(resolveTool("pbcopy", path(good), [])).toBeUndefined();
  expect(resolveTool("pbcopy", undefined, [])).toBeUndefined();
});

test("what is copied loses the control bytes the view strips, and keeps tabs and newlines", () => {
  expect(clipboardText("a\x1b[31mred\x1b[0m b")).toBe("ared b");
  expect(clipboardText("x\x1b]0;title\x07y")).toBe("xy");
  expect(clipboardText("bell\x07 nul\x00 del\x7f c1\u009b")).toBe(
    "bell nul del c1",
  );
  expect(clipboardText("\tindented\nline two\r\nline three\r")).toBe(
    "\tindented\nline two\nline three",
  );
});

test("a count reads with a thousands separator whatever the locale", () => {
  expect(charCount(0)).toBe("0");
  expect(charCount(999)).toBe("999");
  expect(charCount(1234)).toBe("1,234");
  expect(charCount(1234567)).toBe("1,234,567");
});

test("characters are what a reader sees: graphemes, not code units or code points", () => {
  expect(characters("")).toBe(0);
  expect(characters("abc")).toBe(3);
  expect(characters("😀😀😀")).toBe(3);
  // One family emoji is seven code points and one character.
  expect(characters("👨‍👩‍👧")).toBe(1);
  // An e and a combining accent are two code points and one character.
  expect(characters("é")).toBe(1);
  expect(characters("日本語")).toBe(3);
});

test("the native-selection modifier follows TERM_PROGRAM, and only matters while mouse reporting is on", () => {
  expect(selectionKey("iTerm.app")).toBe("option");
  expect(selectionKey("Apple_Terminal")).toBe("fn");
  expect(selectionKey("ghostty")).toBe("shift");
  expect(selectionKey("WezTerm")).toBe("shift");
  expect(selectionKey("")).toBe("shift");
  expect(selectionKey(undefined)).toBe("shift");
  expect(selectionHint(true, "iTerm.app")).toBe("option-drag");
  expect(selectionHint(true, "Apple_Terminal")).toBe("fn-drag");
  expect(selectionHint(true, undefined)).toBe("shift-drag");
  // Mouse reporting off (or inline): the terminal's own selection, no modifier.
  expect(selectionHint(false, "iTerm.app")).toBe("drag");
});

test("copyToClipboard never takes a tool from the working directory or an excluded directory", async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "clip-cwd-")));
  const bin = join(root, "bin");
  mkdirSync(bin);
  writeFileSync(join(bin, "pbcopy"), "#!/bin/sh\n");
  chmodSync(join(bin, "pbcopy"), 0o755);
  const calls: { argv: readonly string[] }[] = [];
  const run = async (argv: readonly string[]): Promise<RunResult> => {
    calls.push({ argv });
    return { kind: "exited", code: 0 };
  };
  const pb = [{ name: "pbcopy", argv: ["pbcopy"] }];
  const here = process.cwd();
  process.chdir(root);
  try {
    // The working directory holds a bin/ the PATH names: not a place for a tool.
    expect(
      await copyToClipboard("x", { tools: pb, run, env: { PATH: bin } }),
    ).toEqual({ ok: false, reason: "none" });
  } finally {
    process.chdir(here);
  }
  // Nor is an excluded directory (the workspace).
  expect(
    await copyToClipboard("x", {
      tools: pb,
      run,
      env: { PATH: bin },
      exclude: [root],
    }),
  ).toEqual({ ok: false, reason: "none" });
  expect(calls).toEqual([]);
  // Found elsewhere, it runs by its absolute path.
  const ok = await copyToClipboard("x", {
    tools: pb,
    run,
    env: { PATH: bin },
  });
  expect(ok).toEqual({ ok: true, tool: "pbcopy" });
  expect(calls[0]?.argv).toEqual([join(bin, "pbcopy")]);
});
