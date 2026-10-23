import { expect, test } from "vitest";
import {
  type DiagnosticsRunner,
  postEditDiagnostics,
} from "#runtime/diagnostics";

const signal = new AbortController().signal;

type Call = { argv: readonly string[]; timeoutMs: number };

function fakeRunner(
  results: Array<{
    output?: string;
    exitCode?: number | null;
    cancelled?: boolean;
  }>,
  calls: Call[] = [],
): DiagnosticsRunner {
  let index = 0;
  return async (argv, _cwd, timeoutMs, _signal) => {
    calls.push({ argv, timeoutMs });
    const result = results[Math.min(index++, results.length - 1)] ?? {
      output: "",
    };
    return {
      output: result.output ?? "",
      exitCode: result.exitCode ?? 0,
      cancelled: result.cancelled ?? false,
    };
  };
}

test("a clean biome run appends a single labeled line", async () => {
  const calls: Call[] = [];
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    {},
  );
  expect(appended).toBe("\n\n--- diagnostics: clean (biome) ---");
  expect(calls).toEqual([
    { argv: ["bunx", "biome", "check", "src/a.ts"], timeoutMs: 30_000 },
  ]);
});

test("biome findings are appended under a diagnostics header", async () => {
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([
      { exitCode: 1, output: "src/a.ts:1:1 lint/suspicious/noExplicitAny\n" },
    ]),
    {},
  );
  expect(appended).toContain("--- diagnostics (biome) ---");
  expect(appended).toContain("lint/suspicious/noExplicitAny");
});

test("LOCAL_AGENT_DIAGNOSTICS=0 disables diagnostics entirely", async () => {
  const calls: Call[] = [];
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    { LOCAL_AGENT_DIAGNOSTICS: "0", LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
  );
  expect(appended).toBe("");
  expect(calls).toHaveLength(0);
});

test("a failing spawner degrades to an unavailable note", async () => {
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    async () => {
      throw new Error("bunx missing");
    },
    {},
  );
  expect(appended).toBe("\n\n--- diagnostics unavailable: bunx missing ---");
});

test("LOCAL_AGENT_DIAGNOSTICS_TSC=1 adds tsc output only when it fails", async () => {
  const calls: Call[] = [];
  const clean = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }, { exitCode: 0 }], calls),
    { LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
  );
  expect(clean).toBe("\n\n--- diagnostics: clean (biome) ---");
  const failing = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner(
      [
        { exitCode: 0 },
        { exitCode: 2, output: "src/a.ts(1,7): error TS2322: bad type" },
      ],
      [],
    ),
    { LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
  );
  expect(failing).toContain("--- diagnostics (tsc) ---");
  expect(failing).toContain("error TS2322");
  expect(calls[1]?.argv).toEqual(["bunx", "tsc", "--noEmit"]);
  expect(calls[1]?.timeoutMs).toBe(90_000);
});

test("tsc is not run for non-TypeScript targets", async () => {
  const calls: Call[] = [];
  await postEditDiagnostics(
    ["notes.md"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    { LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
  );
  expect(calls).toHaveLength(1);
});

test("files biome cannot handle are skipped", async () => {
  const calls: Call[] = [];
  const appended = await postEditDiagnostics(
    ["data.bin"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    {},
  );
  expect(appended).toBe("");
  expect(calls).toHaveLength(0);
});

test("the diagnostics block is capped at 20k chars keeping the head", async () => {
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 1, output: "x".repeat(50_000) }]),
    {},
  );
  expect(appended.length).toBeLessThanOrEqual(20_100);
  expect(appended).toContain("…(diagnostics truncated at 20k chars)");
  expect(appended.endsWith("…(diagnostics truncated at 20k chars)")).toBe(true);
});

test("a cancelled diagnostic run appends nothing", async () => {
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 1, cancelled: true }]),
    {},
  );
  expect(appended).toBe("");
});
