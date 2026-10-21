import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

// Runs the Tuimark loop (validate, goldens at 80 and 120 columns, play) when a
// binary is at hand: `TUIMARK_BINARY=/path/to/tuimark bun run test`.
test.skipIf(!process.env.TUIMARK_BINARY)(
  "ui/app.tui validates, matches its goldens and fires every action",
  async () => {
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
  30_000,
);
