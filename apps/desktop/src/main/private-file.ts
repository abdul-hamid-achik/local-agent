import { randomBytes } from "node:crypto";
import { renameSync, rmSync, writeFileSync } from "node:fs";

/**
 * Writes a file only the current user can read. The text goes into a new
 * 0600 file that then replaces the old one whole, so it is never in a file
 * with a looser mode, even an older file's, and a failed write leaves the
 * old file as it was.
 */
export function writePrivateFile(path: string, text: string): void {
  const temp = `${path}.${process.pid}.${randomBytes(4).toString("hex")}.tmp`;
  try {
    writeFileSync(temp, text, { mode: 0o600, flag: "wx" });
    renameSync(temp, path);
  } catch (error) {
    rmSync(temp, { force: true });
    throw error;
  }
}
