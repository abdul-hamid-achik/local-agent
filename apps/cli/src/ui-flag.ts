export type UiClient = "vue" | "tuimark";

/**
 * The interactive client when --ui is not given: Tuimark on macOS and Linux
 * (`tuimark host` hands the terminal over file descriptors 3 and 4, POSIX only),
 * Vue on Windows.
 */
export function defaultUi(platform: string = process.platform): UiClient {
  return platform === "win32" ? "vue" : "tuimark";
}

/**
 * Validates --ui before anything else runs, whatever the other flags are. An
 * absent flag is the platform's default.
 */
export function parseUi(
  value: string | undefined,
  platform: string = process.platform,
): UiClient {
  if (value === undefined) return defaultUi(platform);
  if (value !== "vue" && value !== "tuimark")
    throw new Error("Unknown UI client; use --ui vue or --ui tuimark");
  // `tuimark host` hands the terminal over file descriptors 3 and 4: POSIX only.
  if (value === "tuimark" && platform === "win32")
    throw new Error("--ui tuimark is not available on Windows; use --ui vue");
  return value;
}

/**
 * The client an interactive run starts. Tuimark inline is opt-in: an explicit
 * `--ui tuimark --mode inline` runs it, while a bare `--mode inline` (no
 * `--ui`) stays on the Vue client until the inline presentation becomes the
 * default. Fullscreen follows --ui as given.
 */
export function interactiveClient(
  ui: UiClient,
  mode: "fullscreen" | "inline",
  explicit = false,
): UiClient {
  if (ui !== "tuimark") return "vue";
  return mode === "fullscreen" || explicit ? "tuimark" : "vue";
}
