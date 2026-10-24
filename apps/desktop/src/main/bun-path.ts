import { accessSync, constants, statSync } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";

export type BunLookup = {
  env: Record<string, string | undefined>;
  home: string;
  /** True when the path is a regular file the process may execute. */
  isExecutable?: (path: string) => boolean;
};

export type BunResolution = { path?: string; tried: string[] };

export function isExecutableFile(path: string): boolean {
  try {
    if (!statSync(path).isFile()) return false;
    accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

/**
 * A `.app` opened from Finder inherits a minimal PATH (no Homebrew, no
 * `~/.bun/bin`), so the usual install locations are probed explicitly before
 * falling back to whatever PATH holds.
 */
export function resolveBun(lookup: BunLookup): BunResolution {
  const isExecutable = lookup.isExecutable ?? isExecutableFile;
  const candidates: string[] = [];
  const bunInstall = lookup.env.BUN_INSTALL;
  if (bunInstall) candidates.push(join(bunInstall, "bin", "bun"));
  candidates.push(
    join(lookup.home, ".bun", "bin", "bun"),
    "/opt/homebrew/bin/bun",
    "/usr/local/bin/bun",
  );
  // A relative entry would be probed against the app's cwd ("/" from Finder).
  for (const dir of (lookup.env.PATH ?? "").split(delimiter)) {
    if (isAbsolute(dir)) candidates.push(join(dir, "bun"));
  }
  const tried = [...new Set(candidates)];
  const path = tried.find((candidate) => isExecutable(candidate));
  return { path, tried };
}

export function bunNotFoundMessage(tried: string[]): string {
  return [
    "Bun was not found. The workbench runs its sidecar with Bun.",
    `Looked in: ${tried.join(", ")}.`,
    "Install Bun (https://bun.sh) or set BUN_INSTALL to its install directory, then restart the sidecar.",
    "An app opened from Finder does not inherit the shell PATH, so a Bun that works in a terminal can still be missed here.",
  ].join(" ");
}
