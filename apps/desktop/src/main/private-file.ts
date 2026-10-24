import { chmodSync, writeFileSync } from "node:fs";

/** Writes a file only the current user can read; tightens an older, looser mode. */
export function writePrivateFile(path: string, text: string): void {
  // `mode` applies only when the file is created, so fix an existing one too.
  writeFileSync(path, text, { mode: 0o600 });
  chmodSync(path, 0o600);
}
