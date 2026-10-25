import { expect, test } from "vitest";
import { approvalPreview, tokensPerSecond } from "#cli/preview";

test("a command approval shows each argument as it runs: no line break, escape or override hides in it", () => {
  const preview = approvalPreview(
    {
      kind: "command",
      argv: ["rm", "-rf", "build\nok", "x\u001b[2Kdone", "\u202etxt.exe", ""],
      cwd: "/w",
      timeoutMs: 1000,
    },
    "/w",
  );
  expect(preview.split("\n")[0]).toBe(
    "$ rm -rf $'build\\nok' $'x\\x1b[2Kdone' $'\\u202etxt.exe' ''",
  );
});

test("a command approval is one command line, then where, how long, and with what permissions", () => {
  const command = {
    kind: "command" as const,
    argv: ["bun", "test", "my tests"] as const,
    cwd: "/w/pkg",
    timeoutMs: 120_000,
  };
  expect(approvalPreview(command, "/w")).toBe(
    "$ bun test 'my tests'\nin pkg · timeout 120s · runs with your permissions",
  );
  expect(approvalPreview({ ...command, cwd: "/w" }, "/w")).toContain(
    "in the workspace ·",
  );
});

test("a command that names a secret path says so under the command line", () => {
  const command = {
    kind: "command" as const,
    argv: ["cat", ".env"] as const,
    cwd: "/w",
    timeoutMs: 1000,
    secretPaths: [".env", "~/my keys/id_rsa"],
  };
  expect(approvalPreview(command, "/w")).toBe(
    "$ cat .env\nin the workspace · timeout 1s · runs with your permissions\nnames a path the workspace treats as secret: .env, '~/my keys/id_rsa'",
  );
  expect(approvalPreview({ ...command, secretPaths: [] }, "/w")).not.toContain(
    "secret",
  );
  // Too many path words to check is said as that, not as a secret path.
  expect(
    approvalPreview(
      { ...command, secretPaths: ["65 path words, only 64 checked"] },
      "/w",
    ),
  ).toBe(
    "$ cat .env\nin the workspace · timeout 1s · runs with your permissions\nnames 65 path words, too many to check each for a link to a secret",
  );
  expect(
    approvalPreview(
      { ...command, secretPaths: ["73 path words, only 64 checked", ".env"] },
      "/w",
    ),
  ).toBe(
    "$ cat .env\nin the workspace · timeout 1s · runs with your permissions\nnames a path the workspace treats as secret: .env\nnames 73 path words, too many to check each for a link to a secret",
  );
});

test("tokensPerSecond needs both of Ollama's counters", () => {
  expect(tokensPerSecond(30, 1_000_000_000)).toBe("30 tok/s");
  expect(tokensPerSecond(5, 1_000_000_000)).toBe("5.0 tok/s");
  expect(tokensPerSecond(0, 1_000_000_000)).toBeUndefined();
  expect(tokensPerSecond(30, 0)).toBeUndefined();
});
