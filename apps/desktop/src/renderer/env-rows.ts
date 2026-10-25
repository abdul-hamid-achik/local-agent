/** One variable of the Process profile form. */
export type EnvRow = { key: string; value: string; shown: boolean };

/**
 * The environment the rows describe, or why they cannot be saved. A name
 * given twice is refused: the later row would silently replace the earlier
 * one's value (possibly a token) when the profile is saved.
 */
export function envFromRows(
  rows: readonly EnvRow[],
): { env: Record<string, string> } | { error: string } {
  const env: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    if (!/^[A-Z_][A-Z0-9_]*$/.test(key))
      return { error: `Invalid variable name: ${key}` };
    if (Object.hasOwn(env, key))
      return { error: `Variable ${key} is set more than once` };
    env[key] = row.value;
  }
  return { env };
}
