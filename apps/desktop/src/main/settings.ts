import { chmodSync, readFileSync, renameSync } from "node:fs";
import { writePrivateFile } from "#desktop/main/private-file";
import { persistentProviderEnv } from "#desktop/main/provider-env";

export type Settings = {
  repoRoot?: string;
  providerEnv?: Record<string, string>;
};

/**
 * Moves a settings file that cannot be used out of the way, so the next save
 * does not overwrite the API keys it may still hold. It stays 0600.
 */
function setAside(path: string, reason: string): void {
  const kept = `${path}.corrupt-${Date.now()}`;
  try {
    renameSync(path, kept);
    chmodSync(kept, 0o600);
    console.error(`[main] settings unusable (${reason}); kept as ${kept}`);
  } catch (error) {
    console.error(
      `[main] settings unusable (${reason}) and cannot be set aside: ${(error as Error).message}`,
    );
  }
}

/**
 * The saved settings: none when the file is missing, and none when it is
 * unreadable or not a settings object, which is set aside first.
 */
export function loadSettings(path: string): Settings {
  let settings: Settings;
  try {
    const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
      throw new Error("not a JSON object");
    settings = parsed as Settings;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT")
      setAside(path, (error as Error).message);
    return {};
  }
  // A Chalupa profile an older version saved names a tunnel long gone.
  if (settings.providerEnv)
    settings.providerEnv = persistentProviderEnv(settings.providerEnv);
  // providerEnv holds API keys; an older file may have been written 0644.
  // A file that cannot be tightened still loads.
  try {
    chmodSync(path, 0o600);
  } catch (error) {
    console.error(
      `[main] cannot tighten settings: ${(error as Error).message}`,
    );
  }
  return settings;
}

/**
 * Saves `settings` without the parts that must not outlive this run of the
 * app (a Chalupa profile), whichever caller saves: the in-memory settings
 * keep them for the sidecar.
 */
export function saveSettings(path: string, settings: Settings): void {
  const saved = settings.providerEnv
    ? { ...settings, providerEnv: persistentProviderEnv(settings.providerEnv) }
    : settings;
  try {
    writePrivateFile(path, JSON.stringify(saved, null, 2));
  } catch (error) {
    console.error(`[main] cannot save settings: ${(error as Error).message}`);
  }
}
