import { readFileSync, realpathSync, writeFileSync } from "node:fs";
import { extname } from "node:path";

export const MAX_TEXT_FILE_BYTES = 1_000_000;

const NOT_GRANTED = "Choose the file with Browse first";

/**
 * The renderer reads and writes text files only for the MCP config editor.
 * A path is usable only after the user chose it in the native file dialog, so
 * a compromised renderer cannot name an arbitrary file. Grants are keyed by
 * real path, which also settles symlinks.
 */
export class TextFileGrants {
  private readonly granted = new Set<string>();

  grant(path: string): void {
    if (extname(path).toLowerCase() !== ".json") return;
    try {
      this.granted.add(realpathSync(path));
    } catch {
      // A file that cannot be resolved is never granted.
    }
  }

  private resolve(path: unknown): string {
    if (typeof path !== "string" || !path)
      throw new Error("A file path is required");
    let real: string;
    try {
      real = realpathSync(path);
    } catch {
      throw new Error(NOT_GRANTED);
    }
    if (!this.granted.has(real)) throw new Error(NOT_GRANTED);
    return real;
  }

  read(path: unknown): string {
    const real = this.resolve(path);
    const text = readFileSync(real, "utf8");
    if (Buffer.byteLength(text) > MAX_TEXT_FILE_BYTES)
      throw new Error("File exceeds 1 MB");
    return text;
  }

  write(path: unknown, text: unknown): void {
    const real = this.resolve(path);
    if (typeof text !== "string") throw new Error("Text is required");
    if (Buffer.byteLength(text) > MAX_TEXT_FILE_BYTES)
      throw new Error("File exceeds 1 MB");
    writeFileSync(real, text);
  }
}
