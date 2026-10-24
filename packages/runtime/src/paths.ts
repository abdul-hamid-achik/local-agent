// Per-user locations shared by the runtime's policy code.

import { homedir } from "node:os";
import { isAbsolute, join } from "node:path";

/**
 * local-agent's directory under the user's config home. XDG_CONFIG_HOME
 * counts only when absolute: the XDG spec says to ignore a relative value,
 * which would otherwise resolve against whatever directory the run started
 * in (often the workspace).
 */
export function userConfigDir(
  env: Record<string, string | undefined> = process.env,
): string {
  const xdg = env.XDG_CONFIG_HOME?.trim();
  return join(
    xdg && isAbsolute(xdg) ? xdg : join(homedir(), ".config"),
    "local-agent",
  );
}
