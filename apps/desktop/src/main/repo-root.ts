import { existsSync } from "node:fs";
import { join } from "node:path";

export function looksLikeRepoRoot(path: string): boolean {
  return (
    existsSync(join(path, "package.json")) &&
    existsSync(join(path, "apps", "desktop", "src", "sidecar", "sidecar.ts"))
  );
}

/**
 * The checkout the app runs from: `override` when it is one, else the one
 * above the built main process (`distMain` is apps/desktop/dist-main, so the
 * root is three levels up), else the one the app was started in (the
 * desktop workspace, or the root itself).
 */
export function detectRepoRoot(where: {
  override?: string;
  distMain: string;
  cwd: string;
}): string | undefined {
  const { override, distMain, cwd } = where;
  if (override && looksLikeRepoRoot(override)) return override;
  for (const candidate of [
    join(distMain, "..", "..", ".."),
    cwd,
    join(cwd, "..", ".."),
  ])
    if (looksLikeRepoRoot(candidate)) return candidate;
  return undefined;
}
