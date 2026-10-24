import { expect, test } from "vitest";
import {
  CLIPBOARD_TOOL_NAMES,
  type ClipboardTool,
  charCount,
  clipboardTools,
  copyToClipboard,
  type RunResult,
  selectionHint,
  selectionKey,
  spawnTool,
} from "#cli/tuimark/clipboard";

const tools: ClipboardTool[] = [
  { name: "one", argv: ["one"] },
  { name: "two", argv: ["two", "-x"] },
  { name: "three", argv: ["three"] },
];

/** A runner that answers per program and records what it was given. */
function runner(answers: Record<string, RunResult>) {
  const calls: { argv: readonly string[]; text: string }[] = [];
  return {
    calls,
    run: async (argv: readonly string[], text: string): Promise<RunResult> => {
      calls.push({ argv, text });
      return answers[argv[0] ?? ""] ?? { kind: "missing" };
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
  expect(await copyToClipboard("hello\nworld", { tools, run })).toEqual({
    ok: true,
    tool: "two",
  });
  // Tried in order, stopped at the first that worked, text passed whole.
  expect(calls).toEqual([
    { argv: ["one"], text: "hello\nworld" },
    { argv: ["two", "-x"], text: "hello\nworld" },
  ]);
});

test("a tool that fails falls through to the next, one that works wins", async () => {
  const { run } = runner({
    one: { kind: "exited", code: 1 },
    two: { kind: "timeout" },
    three: { kind: "exited", code: 0 },
  });
  expect(await copyToClipboard("x", { tools, run })).toEqual({
    ok: true,
    tool: "three",
  });
});

test("no tool at all is 'none'; a tool that ran and failed is reported by name", async () => {
  expect(await copyToClipboard("x", { tools, run: runner({}).run })).toEqual({
    ok: false,
    reason: "none",
  });
  expect(
    await copyToClipboard("x", { tools: [], run: runner({}).run }),
  ).toEqual({ ok: false, reason: "none" });
  const failed = runner({
    one: { kind: "exited", code: 1 },
    two: { kind: "missing" },
    three: { kind: "missing" },
  });
  expect(await copyToClipboard("x", { tools, run: failed.run })).toEqual({
    ok: false,
    reason: "failed",
    tool: "one",
    detail: "exited with code 1",
  });
  const hung = runner({ one: { kind: "timeout" } });
  expect(await copyToClipboard("x", { tools, run: hung.run })).toMatchObject({
    ok: false,
    reason: "failed",
    tool: "one",
    detail: "did not finish in time",
  });
});

test("the real runner writes the text to a program's stdin and reports a missing program", async () => {
  // `cat` reads stdin and exits 0: the spawn path, with no clipboard involved.
  expect(await spawnTool(["cat"], "text for stdin")).toEqual({
    kind: "exited",
    code: 0,
  });
  expect(await spawnTool(["sh", "-c", "cat >/dev/null; exit 3"], "x")).toEqual({
    kind: "exited",
    code: 3,
  });
  expect(await spawnTool(["local-agent-no-such-clipboard-tool"], "x")).toEqual({
    kind: "missing",
  });
});

test("a count reads with a thousands separator whatever the locale", () => {
  expect(charCount(0)).toBe("0");
  expect(charCount(999)).toBe("999");
  expect(charCount(1234)).toBe("1,234");
  expect(charCount(1234567)).toBe("1,234,567");
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
