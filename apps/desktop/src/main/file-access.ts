import { readFileSync, realpathSync, statSync, writeFileSync } from "node:fs";
import { extname, isAbsolute, join } from "node:path";
import { SidecarMethods } from "#desktop/shared/protocol";

export const MAX_TEXT_FILE_BYTES = 1_000_000;

const NOT_GRANTED = "Choose the file with Browse first";
const TOO_BIG = "File exceeds 1 MB";

const isJson = (path: string) => extname(path).toLowerCase() === ".json";

/**
 * The user's MCP config, where the CLI looks by default. As in the runtime's
 * userConfigDir, XDG_CONFIG_HOME counts only when it is absolute once trimmed.
 */
export function userMcpConfigPath(
  env: Record<string, string | undefined>,
  home: string,
): string {
  const xdg = env.XDG_CONFIG_HOME?.trim();
  return join(
    xdg && isAbsolute(xdg) ? xdg : join(home, ".config"),
    "local-agent",
    "mcp.json",
  );
}

/**
 * The renderer reads and writes text files only for the MCP config editor.
 * A path is usable only after the user chose it in the native file dialog, so
 * a compromised renderer cannot name an arbitrary file. Grants are keyed by
 * real path, which also settles symlinks, and the real file must be JSON too.
 */
export class TextFileGrants {
  private readonly granted = new Set<string>();
  // Granted by name and resolved at each use: the file may not exist yet.
  private readonly named = new Set<string>();

  grant(path: string): void {
    if (!isJson(path)) return;
    try {
      const real = realpathSync(path);
      // A picked `x.json` that links to a key file is not a JSON file.
      if (isJson(real)) this.granted.add(real);
    } catch {
      // A file that cannot be resolved is never granted.
    }
  }

  /** Grants `path` whenever it exists, now or once something creates it. */
  grantByName(path: string): void {
    if (isJson(path)) this.named.add(path);
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
    if (!this.granted.has(real) && !this.namedAs(real))
      throw new Error(NOT_GRANTED);
    // A FIFO or device named *.json would block the main process's read.
    if (!statSync(real).isFile()) throw new Error("Not a regular file");
    return real;
  }

  private namedAs(real: string): boolean {
    if (!isJson(real)) return false;
    for (const name of this.named) {
      try {
        if (realpathSync(name) === real) return true;
      } catch {
        // Not there (yet).
      }
    }
    return false;
  }

  /** The real path of a granted file, for a caller that opens it itself. */
  real(path: unknown): string {
    return this.resolve(path);
  }

  read(path: unknown): string {
    const real = this.resolve(path);
    // Checked before reading, so an oversized file is never loaded.
    if (statSync(real).size > MAX_TEXT_FILE_BYTES) throw new Error(TOO_BIG);
    const text = readFileSync(real, "utf8");
    if (Buffer.byteLength(text) > MAX_TEXT_FILE_BYTES) throw new Error(TOO_BIG);
    return text;
  }

  write(path: unknown, text: unknown): void {
    const real = this.resolve(path);
    if (typeof text !== "string") throw new Error("Text is required");
    if (Buffer.byteLength(text) > MAX_TEXT_FILE_BYTES) throw new Error(TOO_BIG);
    writeFileSync(real, text);
  }
}

/**
 * An MCP config makes the sidecar start the commands it names, so it takes
 * the editor's grant: a file the user picked, or their own mcp.json. The
 * params go on with the config's real path; an ungranted one throws.
 */
export function grantedMcpParams(
  method: string,
  params: unknown,
  grants: TextFileGrants,
): unknown {
  const key =
    method === SidecarMethods.mcpStatus
      ? "configPath"
      : method === SidecarMethods.agentStart
        ? "mcpConfig"
        : undefined;
  if (!key || !params || typeof params !== "object") return params;
  const path = (params as Record<string, unknown>)[key];
  // A run without MCP names no config.
  if (key === "mcpConfig" && (path === undefined || path === "")) return params;
  return { ...params, [key]: grants.real(path) };
}
