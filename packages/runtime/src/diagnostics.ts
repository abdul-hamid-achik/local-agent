// Post-edit diagnostics: after an approved edit or write, the host may lint
// the changed files and append the findings to the tool result. Opt-in
// (LOCAL_AGENT_DIAGNOSTICS=1), and only with the workspace's own installs:
// the native binary of its @biomejs/biome and its typescript, resolved the
// way their own launchers do. Never `bunx`: with no local install it
// fetches whatever npm calls "biome" (an unrelated package) or "tsc".

import { realpathSync, statSync } from "node:fs";
import { basename, dirname, extname, join } from "node:path";
import {
  createCommandProposal,
  executeApprovedCommand,
} from "#runtime/command";

const BIOME_TIMEOUT_MS = 30_000;
const TSC_TIMEOUT_MS = 90_000;
const MAX_BLOCK_CHARS = 20_000;
const TRUNCATION_NOTE = "…(diagnostics truncated at 20k chars)";

const TS_EXTENSIONS = new Set([".ts", ".tsx"]);
/** Extensions biome can lint/format; edits to other files skip diagnostics. */
const DIAGNOSTIC_EXTENSIONS = new Set([
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".json",
  ".css",
  ".scss",
  ".html",
  ".vue",
  ".svelte",
  ".astro",
  ".md",
  ".yml",
  ".yaml",
  ".graphql",
]);

export type DiagnosticsRunner = (
  argv: readonly string[],
  cwd: string,
  timeoutMs: number,
  signal: AbortSignal,
) => Promise<{ output: string; exitCode: number | null; cancelled: boolean }>;

/**
 * The same no-shell spawn path run_command uses after host approval:
 * createCommandProposal + executeApprovedCommand (user permissions, cwd,
 * timeouts, abort handling).
 */
export const commandDiagnosticsRunner: DiagnosticsRunner = async (
  argv,
  cwd,
  timeoutMs,
  signal,
) => {
  const proposal = await createCommandProposal(argv, cwd, timeoutMs, {
    // Host-driven diagnostics (never model-chosen argv) may carry HOME:
    // biome resolves its user-level config through it.
    ...(process.env.HOME === undefined ? {} : { HOME: process.env.HOME }),
  });
  const result = await executeApprovedCommand(proposal, signal);
  return {
    output: result.output,
    exitCode: result.exitCode,
    cancelled: result.cancelled,
  };
};

export function diagnosticsEnabled(
  env: Record<string, string | undefined> = process.env,
): boolean {
  return env.LOCAL_AGENT_DIAGNOSTICS === "1";
}

/** Where the workspace's tools live; tests inject fixtures. */
export type DiagnosticsTools = {
  biome: (root: string) => string | undefined;
  tsc: (root: string) => readonly string[] | undefined;
};

/**
 * A file inside a package the workspace installed, found the way Node finds
 * packages (each ancestor's `node_modules`, from `from` upward) but never
 * above the workspace root: no parent directory, global cache or Bun
 * auto-install. Symlinked layouts (Bun's `.bun/`, pnpm's `.pnpm/`) resolve
 * to their real directories, which must stay inside the root as well.
 */
function installedFile(
  root: string,
  from: string,
  file: string,
): string | undefined {
  const inside = (path: string) => path === root || path.startsWith(`${root}/`);
  for (let dir = from; inside(dir); dir = dirname(dir)) {
    if (basename(dir) !== "node_modules") {
      const candidate = join(dir, "node_modules", file);
      try {
        const real = realpathSync(candidate);
        if (inside(real) && statSync(real).isFile()) return real;
      } catch {
        // Not installed at this level.
      }
    }
    if (dir === root) break;
  }
  return undefined;
}

/** The native Biome binary of the workspace's @biomejs/biome install. */
function workspaceBiome(workspaceRoot: string): string | undefined {
  let root: string;
  try {
    root = realpathSync(workspaceRoot);
  } catch {
    return undefined;
  }
  const manifest = installedFile(root, root, "@biomejs/biome/package.json");
  if (manifest === undefined) return undefined;
  const exe = process.platform === "win32" ? "biome.exe" : "biome";
  for (const suffix of ["", "-musl"]) {
    const native = installedFile(
      root,
      dirname(manifest),
      `@biomejs/cli-${process.platform}-${process.arch}${suffix}/${exe}`,
    );
    if (native !== undefined) return native;
  }
  return undefined;
}

/** The workspace's `tsc`, run by this very Bun or Node binary. */
function workspaceTsc(workspaceRoot: string): readonly string[] | undefined {
  if (!/^(bun|node)(\.exe)?$/.test(basename(process.execPath)))
    return undefined;
  let root: string;
  try {
    root = realpathSync(workspaceRoot);
  } catch {
    return undefined;
  }
  const tsc = installedFile(root, root, "typescript/bin/tsc");
  return tsc === undefined ? undefined : [process.execPath, tsc];
}

export const workspaceTools: DiagnosticsTools = {
  biome: workspaceBiome,
  tsc: workspaceTsc,
};

function unavailable(error: unknown): string {
  return `--- diagnostics unavailable: ${
    error instanceof Error ? error.message : String(error)
  } ---`;
}

function cap(text: string): string {
  return text.length <= MAX_BLOCK_CHARS
    ? text
    : `${text.slice(0, MAX_BLOCK_CHARS)}\n${TRUNCATION_NOTE}`;
}

/**
 * Diagnostics appended to an approved edit/write tool result so the model can
 * fix what its change broke. Empty string means "nothing to append" (disabled,
 * unsupported file type, or aborted). Never throws.
 */
export async function postEditDiagnostics(
  paths: readonly string[],
  cwd: string,
  signal: AbortSignal,
  runner: DiagnosticsRunner = commandDiagnosticsRunner,
  env: Record<string, string | undefined> = process.env,
  tools: DiagnosticsTools = workspaceTools,
): Promise<string> {
  if (!diagnosticsEnabled(env)) return "";
  const targets = paths.filter((path) =>
    DIAGNOSTIC_EXTENSIONS.has(extname(path)),
  );
  if (targets.length === 0 || signal.aborted) return "";
  const sections: string[] = [];
  try {
    const biomeBin = tools.biome(cwd);
    if (biomeBin === undefined)
      throw new Error("no @biomejs/biome install in this workspace");
    // `./` keeps a path that starts with `-` from reading as an option.
    const biome = await runner(
      [biomeBin, "check", ...targets.map((path) => `./${path}`)],
      cwd,
      BIOME_TIMEOUT_MS,
      signal,
    );
    if (biome.cancelled || signal.aborted) return "";
    sections.push(
      biome.exitCode === 0
        ? "--- diagnostics: clean (biome) ---"
        : cap(`--- diagnostics (biome) ---\n${biome.output.trim()}`),
    );
  } catch (error) {
    sections.push(unavailable(error));
  }
  if (
    env.LOCAL_AGENT_DIAGNOSTICS_TSC === "1" &&
    targets.some((path) => TS_EXTENSIONS.has(extname(path)))
  ) {
    try {
      const tscArgv = tools.tsc(cwd);
      if (tscArgv === undefined)
        throw new Error("no typescript install in this workspace");
      const tsc = await runner(
        [...tscArgv, "--noEmit"],
        cwd,
        TSC_TIMEOUT_MS,
        signal,
      );
      if (!tsc.cancelled && !signal.aborted && tsc.exitCode !== 0)
        sections.push(cap(`--- diagnostics (tsc) ---\n${tsc.output.trim()}`));
    } catch (error) {
      sections.push(unavailable(error));
    }
  }
  if (sections.length === 0) return "";
  return `\n\n${cap(sections.join("\n\n"))}`;
}
