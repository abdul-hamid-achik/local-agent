import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  type DiagnosticsRunner,
  type DiagnosticsTools,
  postEditDiagnostics,
  workspaceTools,
} from "#runtime/diagnostics";

const signal = new AbortController().signal;
/** Diagnostics are opt-in. */
const ON = { LOCAL_AGENT_DIAGNOSTICS: "1" };
/** A workspace with both tools installed (paths are never executed here). */
const TOOLS: DiagnosticsTools = {
  biome: () => "/w/node_modules/@biomejs/cli/biome",
  tsc: () => ["/usr/bin/bun", "/w/node_modules/typescript/bin/tsc"],
};

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
    ON,
    TOOLS,
  );
  expect(appended).toBe("\n\n--- diagnostics: clean (biome) ---");
  expect(calls).toEqual([
    {
      argv: [
        "/w/node_modules/@biomejs/cli/biome",
        "check",
        `--config-path=${join(process.cwd(), "biome.json")}`,
        "./src/a.ts",
      ],
      timeoutMs: 30_000,
    },
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
    ON,
    TOOLS,
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
    TOOLS,
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
      throw new Error("spawn failed");
    },
    ON,
    TOOLS,
  );
  expect(appended).toBe("\n\n--- diagnostics unavailable: spawn failed ---");
});

test("LOCAL_AGENT_DIAGNOSTICS_TSC=1 adds tsc output only when it fails", async () => {
  const calls: Call[] = [];
  const clean = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }, { exitCode: 0 }], calls),
    { ...ON, LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
    TOOLS,
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
    { ...ON, LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
    TOOLS,
  );
  expect(failing).toContain("--- diagnostics (tsc) ---");
  expect(failing).toContain("error TS2322");
  expect(calls[1]?.argv).toEqual([
    "/usr/bin/bun",
    "/w/node_modules/typescript/bin/tsc",
    "--noEmit",
    "-p",
    join(process.cwd(), "tsconfig.json"),
  ]);
  expect(calls[1]?.timeoutMs).toBe(90_000);
});

test("tsc is not run for non-TypeScript targets", async () => {
  const calls: Call[] = [];
  await postEditDiagnostics(
    ["notes.md"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    { ...ON, LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
    TOOLS,
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
    ON,
    TOOLS,
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
    ON,
    TOOLS,
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
    ON,
    TOOLS,
  );
  expect(appended).toBe("");
});

test("diagnostics are off unless LOCAL_AGENT_DIAGNOSTICS=1", async () => {
  const calls: Call[] = [];
  for (const env of [{}, { LOCAL_AGENT_DIAGNOSTICS: "true" }])
    expect(
      await postEditDiagnostics(
        ["src/a.ts"],
        process.cwd(),
        signal,
        fakeRunner([{ exitCode: 0 }], calls),
        env,
        TOOLS,
      ),
    ).toBe("");
  expect(calls).toHaveLength(0);
});

test("without the workspace's own install nothing runs, and nothing is fetched", async () => {
  const calls: Call[] = [];
  const appended = await postEditDiagnostics(
    ["src/a.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    { ...ON, LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
    { biome: () => undefined, tsc: () => undefined },
  );
  expect(appended).toBe(
    "\n\n--- diagnostics unavailable: no @biomejs/biome install in this workspace ---\n\n--- diagnostics unavailable: no typescript install in this workspace ---",
  );
  expect(calls).toHaveLength(0);
});

test("a path that looks like an option stays a path", async () => {
  const calls: Call[] = [];
  await postEditDiagnostics(
    ["--write.ts"],
    process.cwd(),
    signal,
    fakeRunner([{ exitCode: 0 }], calls),
    ON,
    TOOLS,
  );
  expect(calls[0]?.argv.at(-1)).toBe("./--write.ts");
});

test("workspace tools resolve inside the workspace only", async () => {
  const empty = await mkdtemp(join(tmpdir(), "la-diag-empty-"));
  try {
    // No node_modules here: no parent directory, global cache or Bun
    // auto-install may stand in for the workspace's own install.
    expect(workspaceTools.biome(empty)).toBeUndefined();
    expect(workspaceTools.tsc(empty)).toBeUndefined();
    // A planted file outside the expected package layout is not a tool.
    await mkdir(join(empty, "node_modules", ".bin"), { recursive: true });
    await writeFile(
      join(empty, "node_modules", ".bin", "biome"),
      "#!/bin/sh\n",
    );
    expect(workspaceTools.biome(empty)).toBeUndefined();
  } finally {
    await rm(empty, { recursive: true, force: true });
  }
  // This repository installs both.
  expect(workspaceTools.biome(process.cwd())).toContain("@biomejs/cli-");
  expect(workspaceTools.tsc(process.cwd())?.[1]).toContain(
    "typescript/bin/tsc",
  );
});

test("biome and tsc use the root's own config, or say there is none", async () => {
  const bare = await mkdtemp(join(tmpdir(), "la-diag-bare-"));
  try {
    const calls: Call[] = [];
    const appended = await postEditDiagnostics(
      ["src/a.ts"],
      bare,
      signal,
      fakeRunner([{ exitCode: 0 }], calls),
      { ...ON, LOCAL_AGENT_DIAGNOSTICS_TSC: "1" },
      TOOLS,
    );
    // No biome.json: Biome's own defaults, no --config-path.
    expect(calls).toHaveLength(1);
    expect(calls[0]?.argv.slice(1)).toEqual(["check", "./src/a.ts"]);
    // No tsconfig.json: tsc never runs (it would walk up to a parent's).
    expect(appended).toContain(
      "--- diagnostics unavailable: no tsconfig.json at the workspace root ---",
    );
    await writeFile(join(bare, "biome.jsonc"), "{}");
    await postEditDiagnostics(
      ["a.ts"],
      bare,
      signal,
      fakeRunner([{ exitCode: 0 }], calls),
      ON,
      TOOLS,
    );
    expect(calls[1]?.argv[2]).toBe(
      `--config-path=${join(bare, "biome.jsonc")}`,
    );
  } finally {
    await rm(bare, { recursive: true, force: true });
  }
});
