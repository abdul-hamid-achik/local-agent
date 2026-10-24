import { chmodSync, readFileSync } from "node:fs";
import { writePrivateFile } from "#desktop/main/private-file";
import { persistentProviderEnv } from "#desktop/main/provider-env";

export type Settings = {
  repoRoot?: string;
  providerEnv?: Record<string, string>;
};

/** The saved settings, or none when the file is missing or unreadable. */
export function loadSettings(path: string): Settings {
  try {
    const settings = JSON.parse(readFileSync(path, "utf8")) as Settings;
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
  } catch {
    return {};
  }
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
