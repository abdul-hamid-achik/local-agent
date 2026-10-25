import { expect, test } from "vitest";
import { approvalPreview, messageText, tokensPerSecond } from "#cli/preview";

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
    "$ rm -rf $'build\\nok' $'x\\x1b[2Kdone' $'\\xe2\\x80\\xaetxt.exe' ''",
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

test.each([
  // A workspace at the filesystem root: its prefix is "/", not "//".
  ["/", "/usr/bin", "in usr/bin ·"],
  ["/", "/", "in the workspace ·"],
  // A root written with a trailing slash is the same root.
  ["/w/", "/w/pkg", "in pkg ·"],
  ["/w/", "/w", "in the workspace ·"],
  // Outside the workspace the whole path is shown, not a climb out of it.
  ["/w", "/other/pkg", "in /other/pkg ·"],
  ["/w", "/w2/pkg", "in /w2/pkg ·"],
  // A folder whose name starts with two dots is inside.
  ["/w", "/w/..cache", "in ..cache ·"],
])("a command in %s that runs in %s is placed as %j", (root, cwd, expected) => {
  expect(
    approvalPreview(
      { kind: "command", argv: ["ls"], cwd, timeoutMs: 1000 },
      root,
    ),
  ).toContain(expected);
});

test("tokensPerSecond needs both of Ollama's counters", () => {
  expect(tokensPerSecond(30, 1_000_000_000)).toBe("30 tok/s");
  expect(tokensPerSecond(5, 1_000_000_000)).toBe("5.0 tok/s");
  expect(tokensPerSecond(0, 1_000_000_000)).toBeUndefined();
  expect(tokensPerSecond(30, 0)).toBeUndefined();
});

test("a replayed MCP result keeps what is and is not known about it", () => {
  const text = messageText({
    role: "tool",
    tool_name: "hub__snapshot",
    content: JSON.stringify({
      status: "unverified",
      source: "external",
      transport: "success",
      domain: "unknown",
      evidence: "unverified",
      isError: false,
      output: "cpu 12%",
    }),
  });
  const [status, output] = [
    text.slice(0, text.indexOf("\n")),
    text.slice(text.indexOf("\n") + 1),
  ];
  expect(status).toBe("unverified");
  expect(JSON.parse(output)).toEqual({
    transport: "success",
    domain: "unknown",
    evidence: "unverified",
    isError: false,
    text: "cpu 12%",
  });
  expect(
    messageText({
      role: "tool",
      content: JSON.stringify({ status: "succeeded", output: "ok" }),
    }),
  ).toBe("succeeded\nok");
  expect(messageText({ role: "tool", content: "raw" })).toBe("raw");
});
