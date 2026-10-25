import { closeSync, constants, fstatSync, readSync } from "node:fs";
import { openAt } from "#runtime/workspace_native";

/**
 * `<workspace>/.agentignore`: the user's own list of paths the file tools must
 * not touch. It is the retired Go version's file, with its syntax and its
 * semantics (reference/GO_IMPLEMENTATION.md, `internal/config/ignore.go`):
 *
 * - One pattern per line; blank lines and `#` comments are skipped.
 * - `*` (within one name), `?`, `[abc]`, `[a-z]`, `[!abc]` and `**` (across
 *   `/`; `**` + `/` also matches nothing) are the wildcards. `\x` is a literal.
 * - A pattern with no `/` (after dropping one trailing and one leading `/`)
 *   matches a name at any depth; one with a `/` matches the path from the
 *   workspace root.
 * - A match covers everything below it: ignoring a directory ignores its
 *   contents.
 * - A leading `!` negates; the LAST line that matches a path decides it.
 *
 * Unlike the Go version, names are compared case-insensitively (and in one
 * Unicode form): APFS opens `Secrets` and `secrets` alike, and a boundary that
 * compares bytes is bypassed by the other spelling.
 *
 * This is a privacy boundary, not only noise reduction: an ignored path is
 * hidden from list and search AND refused for read, edit and write (Go refused
 * the same, "excluded by .agentignore"). It governs the built-in file tools; a
 * shell command is gated by the approval card and the sandbox instead.
 */
export const AGENT_IGNORE = ".agentignore";

/** A larger file is refused rather than read: every path is tested against every rule. */
const MAX_BYTES = 64 * 1024;
/** Warnings listed by name; the rest are counted. */
const MAX_WARNINGS = 5;

type Rule = {
  negated: boolean;
  /** The pattern names a path from the root; otherwise a name at any depth. */
  rooted: boolean;
  expression: RegExp;
};

const fold = (text: string) => text.normalize("NFC").toLowerCase();

const SYNTAX = new Set([..."\\^$.*+?()[]{}|/"]);
const literal = (character: string) =>
  SYNTAX.has(character) ? `\\${character}` : character;

/** A glob as a regular expression over a whole name or path, or the reason it is malformed. */
function compile(glob: string): RegExp | string {
  let source = "^";
  for (let index = 0; index < glob.length; index++) {
    const character = glob.codePointAt(index) as number;
    const text = String.fromCodePoint(character);
    if (character > 0xffff) index++;
    if (text === "\\") {
      const next = glob.codePointAt(index + 1);
      if (next === undefined) return "a trailing backslash";
      const escaped = String.fromCodePoint(next);
      index += escaped.length;
      source += literal(escaped);
    } else if (text === "*") {
      if (glob[index + 1] === "*") {
        index++;
        if (glob[index + 1] === "/") {
          index++;
          source += "(?:.*/)?";
        } else source += ".*";
      } else source += "[^/]*";
    } else if (text === "?") {
      source += "[^/]";
    } else if (text === "[") {
      let end = index + 1;
      const negated = glob[end] === "!" || glob[end] === "^";
      if (negated) end++;
      const start = end;
      // A `]` first in the class is a member of it.
      if (glob[end] === "]") end++;
      while (end < glob.length && glob[end] !== "]") end++;
      if (end >= glob.length) return 'an unterminated "["';
      let members = "";
      for (const member of glob.slice(start, end)) {
        members +=
          member === "\\" || member === "]" || member === "[" || member === "^"
            ? `\\${member}`
            : member;
      }
      source += negated ? `[^/${members}]` : `(?!/)[${members}]`;
      index = end;
    } else source += literal(text);
  }
  try {
    return new RegExp(`${source}$`, "su");
  } catch {
    return "an invalid character range";
  }
}

export class AgentIgnore {
  static readonly none = new AgentIgnore([]);
  private constructor(private readonly rules: readonly Rule[]) {}

  /** Parses the file's text; a malformed line is dropped and named in `warnings`. */
  static parse(text: string): { ignore: AgentIgnore; warnings: string[] } {
    const rules: Rule[] = [];
    const problems: string[] = [];
    const lines = text.replace(/^\uFEFF/u, "").split(/\r?\n/u);
    for (const [index, line] of lines.entries()) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith("#")) continue;
      const negated = trimmed.startsWith("!");
      let pattern = (negated ? trimmed.slice(1) : trimmed).trim();
      if (pattern.endsWith("/")) pattern = pattern.slice(0, -1);
      if (pattern.startsWith("/")) pattern = pattern.slice(1);
      const where = `${AGENT_IGNORE} line ${index + 1}`;
      if (!pattern) {
        problems.push(`${where}: ${JSON.stringify(trimmed)} names no path`);
        continue;
      }
      const expression = compile(fold(pattern));
      if (typeof expression === "string") {
        problems.push(`${where}: ${JSON.stringify(trimmed)} has ${expression}`);
        continue;
      }
      rules.push({ negated, rooted: pattern.includes("/"), expression });
    }
    const warnings = problems
      .slice(0, MAX_WARNINGS)
      .map((problem) => `${problem}; the line is ignored`);
    if (problems.length > MAX_WARNINGS)
      warnings.push(
        `${AGENT_IGNORE}: ${problems.length - MAX_WARNINGS} more malformed lines`,
      );
    return {
      ignore: rules.length ? new AgentIgnore(rules) : AgentIgnore.none,
      warnings,
    };
  }

  /** Whether a workspace-relative path (`/`-separated, no `..`) is ignored. */
  matches(relative: string): boolean {
    if (!this.rules.length) return false;
    const names = fold(relative).split("/").filter(Boolean);
    // The path and each directory above it: a match on one covers the rest.
    const prefixes = names.map((_, index) =>
      names.slice(0, index + 1).join("/"),
    );
    let ignored = false;
    for (const rule of this.rules) {
      const hit = prefixes.some((prefix, index) =>
        rule.expression.test(rule.rooted ? prefix : (names[index] ?? "")),
      );
      if (hit) ignored = !rule.negated;
    }
    return ignored;
  }
}

/**
 * Reads `.agentignore` from the workspace root through its descriptor: a
 * missing file is no policy; a symbolic link, anything but a regular file, an
 * oversized or non-UTF-8 file is refused (the caller fails closed), as a
 * planted link is for permissions.json.
 */
export function readAgentIgnore(root: number): {
  ignore: AgentIgnore;
  warnings: string[];
} {
  let fd: number;
  try {
    fd = openAt(root, AGENT_IGNORE, constants.O_RDONLY);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT")
      return { ignore: AgentIgnore.none, warnings: [] };
    throw new Error(
      `refused: ${AGENT_IGNORE} is a symbolic link or cannot be opened; remove it or make it a plain file`,
    );
  }
  try {
    const info = fstatSync(fd);
    if (!info.isFile())
      throw new Error(`refused: ${AGENT_IGNORE} is not a plain file`);
    if (info.size > MAX_BYTES)
      throw new Error(
        `refused: ${AGENT_IGNORE} is larger than ${MAX_BYTES / 1024} KiB`,
      );
    const buffer = Buffer.alloc(info.size);
    let filled = 0;
    while (filled < buffer.length) {
      const count = readSync(fd, buffer, filled, buffer.length - filled, null);
      if (count === 0) break;
      filled += count;
    }
    let text: string;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(
        buffer.subarray(0, filled),
      );
    } catch {
      throw new Error(`refused: ${AGENT_IGNORE} is not valid UTF-8`);
    }
    return AgentIgnore.parse(text);
  } finally {
    closeSync(fd);
  }
}
