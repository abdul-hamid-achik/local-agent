import { expect, test } from "vitest";
import { measure } from "#cli/tuimark/stream-bytes";

test("stream-bytes refuses a flag without a number instead of printing NaN", () => {
  const script = new URL("./stream-bytes.ts", import.meta.url).pathname;
  for (const args of [
    ["--tokens"],
    ["--tokens", "lots"],
    ["--flush-ms", "-3"],
  ]) {
    const run = Bun.spawnSync(["bun", script, ...args]);
    expect(run.exitCode, args.join(" ")).toBe(2);
    expect(run.stderr.toString()).toContain(`${args[0]} needs a number`);
    expect(run.stdout.toString()).not.toContain("NaN");
  }
});

test("a measured run with nothing to stream reports 0 bytes per token, not NaN", async () => {
  const result = await measure({ tokens: 0, tokenMs: 0, flushMs: 5 });
  expect(result.tokens).toBe(0);
  expect(result.perToken).toBe(0);
});
