import { expect, test } from "vitest";
import { approvalPreview, shellQuote, tokensPerSecond } from "#cli/preview";

test("shellQuote quotes only what a shell would need quoted", () => {
  expect(shellQuote("tests/a-b_c.test.ts")).toBe("tests/a-b_c.test.ts");
  expect(shellQuote("a b")).toBe("'a b'");
  expect(shellQuote("it's")).toBe("'it'\\''s'");
  expect(shellQuote("")).toBe("''");
  expect(shellQuote("a|b")).toBe("'a|b'");
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
});

test("tokensPerSecond needs both of Ollama's counters", () => {
  expect(tokensPerSecond(30, 1_000_000_000)).toBe("30 tok/s");
  expect(tokensPerSecond(5, 1_000_000_000)).toBe("5.0 tok/s");
  expect(tokensPerSecond(0, 1_000_000_000)).toBeUndefined();
  expect(tokensPerSecond(30, 0)).toBeUndefined();
});
