export type UiClient = "vue" | "tuimark";

/** Validates --ui before anything else runs, whatever the other flags are. */
export function parseUi(
  value: string,
  platform: string = process.platform,
): UiClient {
  if (value !== "vue" && value !== "tuimark")
    throw new Error("Unknown UI client; use --ui vue or --ui tuimark");
  // `tuimark host` hands the terminal over file descriptors 3 and 4: POSIX only.
  if (value === "tuimark" && platform === "win32")
    throw new Error("--ui tuimark is not available on Windows; use --ui vue");
  return value;
}
