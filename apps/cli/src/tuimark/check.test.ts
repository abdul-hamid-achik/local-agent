import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { binaryPolicy } from "#cli/tuimark/binary-policy";

const policy = binaryPolicy(process.env);

test("the tuimark binary is required in CI and optional elsewhere", () => {
  expect(binaryPolicy({ TUIMARK_BINARY: "/b/tuimark" })).toBe("run");
  expect(binaryPolicy({ CI: "true", TUIMARK_BINARY: "/b/tuimark" })).toBe(
    "run",
  );
  expect(binaryPolicy({})).toBe("skip");
  expect(binaryPolicy({ CI: "true" })).toBe("fail");
  expect(binaryPolicy({ CI: "1", TUIMARK_BINARY: "" })).toBe("fail");
});

// Runs the Tuimark loop (validate, goldens at 80 and 120 columns, play) when a
// binary is at hand: `TUIMARK_BINARY=/path/to/tuimark bun run test`. With CI set
// and no binary it fails instead of skipping.
test.skipIf(policy === "skip")(
  "ui/app.tui validates, matches its goldens and fires every action",
  async () => {
    if (policy === "fail")
      throw new Error(
        "CI is set but TUIMARK_BINARY is not: install the tuimark binary pinned by apps/cli/package.json before running the tests",
      );
    const check = Bun.spawn(
      [
        process.execPath,
        join(dirname(fileURLToPath(import.meta.url)), "check.ts"),
      ],
      {
        env: process.env,
        stdout: "pipe",
        stderr: "pipe",
      },
    );
    const [out, err, code] = await Promise.all([
      new Response(check.stdout).text(),
      new Response(check.stderr).text(),
      check.exited,
    ]);
    expect(err + out).toContain("ok:");
    expect(code).toBe(0);
  },
  60_000,
);
