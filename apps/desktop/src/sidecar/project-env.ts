/**
 * The environment a Project-view command (`bun run check`, `git status`, ...)
 * runs with. The sidecar's own environment carries the provider profile and
 * any API key saved in the app, which the repo's scripts and tests (and
 * whatever they spawn) have no use for: only what the toolchain needs passes
 * and everything else is dropped. It extends the runtime's run_command list
 * (PATH, HOME, TERM, COLORTERM, LANG, LC_*) with the variables Bun, asdf,
 * the tests and git read; none holds a credential.
 */
const PASSED = new Set([
  "PATH",
  "HOME",
  "USER",
  "LOGNAME",
  "SHELL",
  "TMPDIR",
  "TERM",
  "COLORTERM",
  "NO_COLOR",
  "FORCE_COLOR",
  "LANG",
  "LANGUAGE",
  "TZ",
  "CI",
  // Bun's install and cache folders, and asdf's shims (without its data
  // folder they exit 126).
  "BUN_INSTALL",
  "BUN_INSTALL_CACHE_DIR",
  "ASDF_DIR",
  "ASDF_DATA_DIR",
  // The Tuimark binary the integration specs drive: a path, not a secret.
  "TUIMARK_BINARY",
]);

/** Locale and XDG folder variables: paths and language settings. */
const PASSED_PREFIXES = ["LC_", "XDG_"];

export function projectEnvironment(
  env: NodeJS.ProcessEnv,
): Record<string, string> {
  const passed: Record<string, string> = {};
  for (const [key, value] of Object.entries(env)) {
    if (value === undefined) continue;
    if (PASSED.has(key) || PASSED_PREFIXES.some((p) => key.startsWith(p)))
      passed[key] = value;
  }
  passed.PATH ??= "/usr/bin:/bin";
  return passed;
}
