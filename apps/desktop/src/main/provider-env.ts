/**
 * A Chalupa profile is process-scoped: its LOCAL_AGENT_PROVIDER_CONFIG names
 * a tunnel that ends with the `chalupa exec` session that printed it, and a
 * later launch would talk to a dead loopback port. So the Chalupa variables
 * apply to this run of the app but are never written to settings.json. An
 * openai_local profile names a local server and is kept.
 */
const CHALUPA_KEYS = [
  "LOCAL_AGENT_PROVIDER",
  "LOCAL_AGENT_PROVIDER_CONFIG",
  "LOCAL_AGENT_LOCAL_ONLY",
] as const;

/** The part of `env` that may be saved. */
export function persistentProviderEnv(
  env: Record<string, string>,
): Record<string, string> {
  if (env.LOCAL_AGENT_PROVIDER?.trim().toLowerCase() !== "chalupa") return env;
  const kept = { ...env };
  for (const key of CHALUPA_KEYS) delete kept[key];
  return kept;
}

/**
 * Why the runtime would refuse `env`'s provider, or nothing. It matches the
 * name exactly, so "Chalupa" would be dropped here as a Chalupa profile
 * yet rejected at every run.
 */
export function providerEnvProblem(
  env: Record<string, string>,
): string | undefined {
  const name = env.LOCAL_AGENT_PROVIDER;
  if (name === undefined) return undefined;
  const lower = name.trim().toLowerCase();
  for (const known of ["chalupa", "openai_local", "ollama"])
    if (lower === known && name !== known)
      return `LOCAL_AGENT_PROVIDER must be exactly "${known}"`;
  return undefined;
}
