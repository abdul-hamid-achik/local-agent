/**
 * What the suite does about the tuimark binary the Tuimark loop needs:
 * run it when TUIMARK_BINARY is set, skip it on a developer machine without one,
 * and fail when CI is set without one, so a CI job that forgot to install the
 * binary cannot pass by silently skipping the loop.
 */
export function binaryPolicy(
  env: Record<string, string | undefined>,
): "run" | "skip" | "fail" {
  if (env.TUIMARK_BINARY) return "run";
  return env.CI ? "fail" : "skip";
}
