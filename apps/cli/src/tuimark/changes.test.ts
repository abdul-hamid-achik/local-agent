import type { Message } from "@local-agent/runtime";
import { expect, test } from "vitest";
import {
  changesSummary,
  changesText,
  NO_CHANGES_STATUS,
  sessionChanges,
} from "#cli/tuimark/changes";

const call = (
  id: string,
  name: string,
  args: Record<string, unknown>,
): Message => ({
  role: "assistant",
  content: "",
  tool_calls: [{ id, function: { name, arguments: args } }],
});

const result = (
  id: string,
  name: string,
  output: string,
  status = "succeeded",
): Message => ({
  role: "tool",
  tool_name: name,
  tool_call_id: id,
  content: JSON.stringify({ status, output }),
});

const edit = (
  id: string,
  path: string,
  from: string,
  to: string,
  status = "succeeded",
): Message[] => [
  call(id, "edit", { path, old_string: from, new_string: to }),
  result(id, "edit", `Updated ${path}. Verification has not been run.`, status),
];

test("an empty or change-free history lists nothing", () => {
  expect(NO_CHANGES_STATUS).toBe("No changes in this session");
  expect(sessionChanges([]).files).toEqual([]);
  expect(
    sessionChanges([
      { role: "user", content: "hi" },
      call("r1", "read", { path: "a.ts" }),
      result("r1", "read", "const a = 1;"),
    ]).files,
  ).toEqual([]);
});

test("edits are grouped by file in first-touched order, with their rows", () => {
  const changes = sessionChanges([
    { role: "user", content: "go" },
    ...edit("c1", "src/a.ts", "const a = 1;", "const a = 2;"),
    ...edit("c2", "src/b.ts", "x", "y"),
    ...edit("c3", "src/a.ts", "let b", "let c"),
  ]);
  expect(changes.files.map((file) => [file.path, file.edits])).toEqual([
    ["src/a.ts", 2],
    ["src/b.ts", 1],
  ]);
  expect(changesSummary(changes)).toBe("2 files · +3 −3");
  expect(changesText(changes).split("\n")).toEqual([
    "── src/a.ts · edited ×2 · +2 −2",
    "- const a = 1;",
    "+ const a = 2;",
    "…",
    "- let b",
    "+ let c",
    "",
    "── src/b.ts · edited · +1 −1",
    "- x",
    "+ y",
    "",
    "Only applied edits and writes are listed, not commands.",
  ]);
});

test("a created file shows its lines; a replaced one says no earlier text is kept", () => {
  const changes = sessionChanges([
    call("w1", "write", { path: "new.txt", content: "one\ntwo\n" }),
    result("w1", "write", "Created new.txt. Verification has not been run."),
    call("w2", "write", { path: "old.txt", content: "fresh\n" }),
    result("w2", "write", "Replaced old.txt. Verification has not been run."),
  ]);
  expect(changes.files.map((f) => [f.path, f.created, f.replaced])).toEqual([
    ["new.txt", true, false],
    ["old.txt", false, true],
  ]);
  const text = changesText(changes).split("\n");
  expect(text.slice(0, 4)).toEqual([
    "── new.txt · created · +2",
    "+ one",
    "+ two",
    "",
  ]);
  expect(text[4]).toBe(
    "── old.txt · replaced (earlier text not kept: no diff)",
  );
});

test("only applied calls count: denied, failed and unverified ones do not", () => {
  const changes = sessionChanges([
    ...edit("c1", "a.ts", "x", "y", "denied"),
    ...edit("c2", "b.ts", "x", "y", "failed"),
    ...edit("c3", "c.ts", "x", "y", "unverified"),
    ...edit("c4", "d.ts", "x", "y", "outcome_unknown"),
  ]);
  expect(changes.files).toEqual([]);
  expect(changes.unknown).toBe(1);
  expect(changesText(changes)).toContain(
    "1 call with an unknown outcome not listed: /recover.",
  );
});

test("a tool message is matched to the reply before it: call ids repeat across replies", () => {
  const changes = sessionChanges([
    ...edit("call_0", "a.ts", "1", "2"),
    call("call_0", "read", { path: "a.ts" }),
    result("call_0", "read", "2"),
    ...edit("call_0", "b.ts", "3", "4"),
  ]);
  expect(changes.files.map((f) => f.path)).toEqual(["a.ts", "b.ts"]);
  // A result whose call is not in the reply before it is not guessed at.
  expect(
    sessionChanges([
      ...edit("call_0", "a.ts", "1", "2").slice(0, 1),
      { role: "assistant", content: "ok" },
      result("call_0", "edit", "Updated a.ts. Verification has not been run."),
    ]).files,
  ).toEqual([]);
});

test("the path is the one the runtime reported, and still reads once its output was shortened", () => {
  const long = `Updated src/deep/a.ts. Verification has not been run.\n${"x".repeat(900)}`;
  const shortened = `Updated src/deep/a.ts. Verification has not been run.\n[880 more characters elided to keep the conversation within the context window. Run the tool again if you need them.]`;
  for (const output of [long, shortened]) {
    const changes = sessionChanges([
      call("c1", "edit", {
        path: "./src/deep/../deep/a.ts",
        old_string: "a",
        new_string: "b",
      }),
      result("c1", "edit", output),
    ]);
    expect(changes.files.map((f) => f.path)).toEqual(["src/deep/a.ts"]);
  }
});

test("control characters in a path or a line do not reach the panel", () => {
  const changes = sessionChanges([
    ...edit("c1", "a\u001b[31m.ts", "x\u001b[2Jy", "z"),
  ]);
  expect(changesText(changes)).not.toContain("\u001b");
});
