import {
  mkdir,
  mkdtemp,
  readdir,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { binaryPolicy } from "#cli/tuimark/binary-policy";

const policy = binaryPolicy(process.env);
const checkScript = join(dirname(fileURLToPath(import.meta.url)), "check.ts");
const uiDir = join(dirname(checkScript), "ui");

test("the tuimark binary is required in CI and optional elsewhere", () => {
  expect(binaryPolicy({ TUIMARK_BINARY: "/b/tuimark" })).toBe("run");
  expect(binaryPolicy({ CI: "true", TUIMARK_BINARY: "/b/tuimark" })).toBe(
    "run",
  );
  expect(binaryPolicy({})).toBe("skip");
  expect(binaryPolicy({ CI: "true" })).toBe("fail");
  expect(binaryPolicy({ CI: "1", TUIMARK_BINARY: "" })).toBe("fail");
});

/**
 * Runs check.ts to its end. A run that outlives `ms` is killed, and so is one
 * the test runner gave up on (the `finally`), so no check is left running
 * after its test.
 */
async function runCheck(env: Record<string, string | undefined>, ms = 50_000) {
  const check = Bun.spawn([process.execPath, checkScript], {
    env,
    stdout: "pipe",
    stderr: "pipe",
    timeout: ms,
  });
  try {
    const [out, err, code] = await Promise.all([
      new Response(check.stdout).text(),
      new Response(check.stderr).text(),
      check.exited,
    ]);
    return { out, err, code };
  } finally {
    check.kill();
  }
}

/** The binary the checks run against; in CI without one the test fails instead of skipping. */
function requireBinary(): string {
  if (policy === "fail")
    throw new Error(
      "CI is set but TUIMARK_BINARY is not: install the tuimark binary pinned by apps/cli/package.json before running the tests",
    );
  return process.env.TUIMARK_BINARY as string;
}

// Runs the Tuimark loop (validate, goldens at 80 and 120 columns, play) when a
// binary is at hand: `TUIMARK_BINARY=/path/to/tuimark bun run test`. With CI set
// and no binary it fails instead of skipping.
test.skipIf(policy === "skip")(
  "ui/app.tui validates, matches its goldens and fires every action",
  async () => {
    const real = requireBinary();
    await withFakePlay(async (fake, dir) => {
      const log = join(dir, "plays.log");
      const { out, err, code } = await runCheck({
        ...process.env,
        TUIMARK_BINARY: fake,
        REAL_TUIMARK: real,
        FAKE_LOG: log,
      });
      expect(code, err).toBe(0);
      // The closing line counts what the run did: one validate per sample
      // file, one comparison per golden file.
      const done = out.match(
        /^ok: (\d+) samples validated, (\d+) goldens, (\d+) play checks$/m,
      );
      expect(done, out).not.toBeNull();
      expect(Number(done?.[1])).toBe(
        (await readdir(join(uiDir, "samples"))).length,
      );
      expect(Number(done?.[2])).toBe(
        (await readdir(join(uiDir, "golden"))).length,
      );
      // Every play ran at an explicit height, never the binary's default.
      const plays = (await readFile(log, "utf8")).trim().split("\n");
      expect(plays.length).toBeGreaterThan(Number(done?.[3]));
      expect(plays.filter((args) => !/ --rows \d+ /.test(args))).toEqual([]);
    });
  },
  60_000,
);

// A tuimark that is the real one ($REAL_TUIMARK) except for `play`: FAKE_PLAY
// is the output of a play without --styles, FAKE_STYLES that of one with it,
// and FAKE_LOG a file every play's arguments are appended to.
async function withFakePlay<T>(
  run: (fake: string, dir: string) => Promise<T>,
): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), "local-agent-check-"));
  try {
    const fake = join(dir, "tuimark");
    await writeFile(
      fake,
      `#!/bin/sh
if [ "$1" = play ]; then
  [ -n "$FAKE_LOG" ] && echo "$*" >> "$FAKE_LOG"
  case "$*" in
    *--styles*) [ -n "$FAKE_STYLES" ] && { printf '%s' "$FAKE_STYLES"; exit 0; } ;;
    *) [ -n "$FAKE_PLAY" ] && { printf '%s' "$FAKE_PLAY"; exit 0; } ;;
  esac
fi
exec "$REAL_TUIMARK" "$@"
`,
      { mode: 0o755 },
    );
    return await run(fake, dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

/** The first line of check.ts's stderr that fails a play. */
const firstPlayFailure = (err: string) =>
  err.split("\n").find((line) => line.startsWith("FAIL play"));

test.skipIf(policy === "skip")(
  "a play whose JSON has the wrong shape is one short failure, not the whole output",
  async () => {
    const real = requireBinary();
    await withFakePlay(async (fake) => {
      const { err, code } = await runCheck({
        ...process.env,
        TUIMARK_BINARY: fake,
        REAL_TUIMARK: real,
        // No events: reading them is the harness's own access, not tuimark's.
        FAKE_PLAY: `{"grid":[],"padding":"${"x".repeat(5000)}"}`,
      });
      expect(code).toBe(1);
      const first = firstPlayFailure(err);
      expect(first).toContain("the play JSON has no events and grid");
      expect(first?.length).toBeLessThan(500);
    });
  },
  60_000,
);

test.skipIf(policy === "skip")(
  "a play that prints no JSON says so, with a bounded extract of what it printed",
  async () => {
    const real = requireBinary();
    await withFakePlay(async (fake) => {
      const { err, code } = await runCheck({
        ...process.env,
        TUIMARK_BINARY: fake,
        REAL_TUIMARK: real,
        FAKE_PLAY: `oops ${"y".repeat(5000)}`,
      });
      expect(code).toBe(1);
      const first = firstPlayFailure(err);
      expect(first).toContain("tuimark printed no JSON: oops");
      expect(first?.length).toBeLessThan(500);
    });
  },
  60_000,
);

test.skipIf(policy === "skip")(
  "a run that stops early removes its scratch directories",
  async () => {
    const real = requireBinary();
    await withFakePlay(async (fake, dir) => {
      const tmp = join(dir, "tmp");
      await mkdir(tmp);
      // The theme play prints what is not JSON: the check ends there.
      const { err, code } = await runCheck({
        ...process.env,
        TMPDIR: tmp,
        TUIMARK_BINARY: fake,
        REAL_TUIMARK: real,
        FAKE_STYLES: "not json",
      });
      expect(code).toBe(1);
      expect(err).toContain("later checks did not run");
      expect(await readdir(tmp)).toEqual([]);
    });
  },
  60_000,
);

test("a tuimark run that fails says why, from stdout when stderr is empty", async () => {
  // `tuimark validate` writes its diagnostics to stdout.
  const dir = await mkdtemp(join(tmpdir(), "local-agent-check-"));
  try {
    const fake = join(dir, "tuimark");
    await writeFile(
      fake,
      '#!/bin/sh\necho "error V005 app.tui:3:1 unknown node"\nexit 2\n',
      { mode: 0o755 },
    );
    const { err, code } = await runCheck({
      ...process.env,
      TUIMARK_BINARY: fake,
    });
    expect(code).toBe(1);
    expect(err).toContain(
      "FAIL tuimark validate app.tui: exit 2: error V005 app.tui:3:1 unknown node",
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
