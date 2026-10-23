import { extname } from "node:path";
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
    // bunx resolves its cache and biome its config through it.
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
  return env.LOCAL_AGENT_DIAGNOSTICS !== "0";
}

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
): Promise<string> {
  if (!diagnosticsEnabled(env)) return "";
  const targets = paths.filter((path) =>
    DIAGNOSTIC_EXTENSIONS.has(extname(path)),
  );
  if (targets.length === 0 || signal.aborted) return "";
  const sections: string[] = [];
  try {
    const biome = await runner(
      ["bunx", "biome", "check", ...targets],
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
      const tsc = await runner(
        ["bunx", "tsc", "--noEmit"],
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
