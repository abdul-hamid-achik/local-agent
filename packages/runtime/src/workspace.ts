import { randomUUID } from "node:crypto";
import {
  closeSync,
  constants,
  type Dirent,
  fchmodSync,
  fstatSync,
  fsyncSync,
  lstatSync,
  readdirSync,
  readSync,
  writeFileSync,
} from "node:fs";
import { lstat, realpath } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createTwoFilesPatch } from "diff";
import {
  DIRECTORY_FLAGS,
  openAt,
  openRoot,
  renameAt,
  unlinkAt,
  withParent,
  withParentCreating,
} from "#runtime/workspace_native";

const MAX_FILE_BYTES = 8 * 1024 * 1024;
export type WorkspaceEditProposal = Readonly<{
  kind: "edit";
  path: string;
  before: string;
  after: string;
  diff: string;
  identity: string;
}>;

/** A new file, or the replacement of an existing one, that waits for approval. */
export type WorkspaceWriteProposal = Readonly<{
  kind: "write";
  path: string;
  /** True when the file does not exist yet (the diff is all added lines). */
  created: boolean;
  before: string;
  after: string;
  diff: string;
  identity: string;
}>;

type Snapshot = { text: string; identity: string; mode: number };

const LIST_LIMIT = 500;
const SEARCH_MATCH_LIMIT = 100;
const SEARCH_FILE_LIMIT = 5000;
const SEARCH_FILE_BYTES = 1024 * 1024;
const SEARCH_LINE_CHARS = 200;
const SEARCH_SCAN_CHARS = 2000;
const SEARCH_DEPTH = 32;
const SEARCH_MILLISECONDS = 10_000;
/** Directories that are never listed or searched. */
const SKIPPED_DIRECTORIES = new Set([".git", "node_modules"]);
/** Where a model's absolute path most likely came from, not from the workspace. */
const SYSTEM_ROOTS = new Set([
  "bin",
  "dev",
  "etc",
  "home",
  "lib",
  "mnt",
  "opt",
  "private",
  "proc",
  "root",
  "sbin",
  "srv",
  "tmp",
  "users",
  "usr",
  "var",
  "volumes",
]);
/** Names a model gives to the workspace root (`/workspace/src/a.ts`). */
const WRAPPER_NAMES = new Set([
  "app",
  "code",
  "project",
  "repo",
  "repository",
  "work",
  "workspace",
  "ws",
]);

export class Workspace implements Disposable {
  private readonly proposals = new WeakMap<object, string>();
  /** The canonical root, and the path it was opened by when that differs (`/tmp` on macOS). */
  private readonly roots: readonly string[];
  private constructor(
    readonly root: string,
    private fd: number,
    openedAs: string,
  ) {
    const trim = (path: string) =>
      path.endsWith("/") ? path.slice(0, -1) : path;
    this.roots = [...new Set([trim(root), trim(openedAs)])];
  }

  static async create(root: string): Promise<Workspace> {
    const canonical = await realpath(root);
    const expected = await lstat(canonical, { bigint: true });
    const fd = openRoot(canonical);
    try {
      const actual = fstatSync(fd, { bigint: true });
      if (
        !actual.isDirectory() ||
        actual.dev !== expected.dev ||
        actual.ino !== expected.ino
      )
        throw new Error("Workspace root changed during initialization");
      return new Workspace(canonical, fd, resolve(root));
    } catch (error) {
      closeSync(fd);
      throw error;
    }
  }

  close(): void {
    if (this.fd >= 0) {
      closeSync(this.fd);
      this.fd = -1;
    }
  }
  [Symbol.dispose](): void {
    this.close();
  }

  async read(path: string): Promise<string> {
    return this.at(path, (parent, name) => snapshot(parent, name).text);
  }

  async readOptional(path: string): Promise<string | undefined> {
    try {
      return await this.read(path);
    } catch (error) {
      if (error instanceof Error && "code" in error && error.code === "ENOENT")
        return undefined;
      throw error;
    }
  }

  async prepareEdit(
    path: string,
    oldText: string,
    newText: string,
  ): Promise<WorkspaceEditProposal> {
    if (!oldText) throw new Error("oldText must not be empty");
    if (oldText === newText) throw new Error("edit would not change the file");
    path = this.relative(path);
    return this.at(path, (parent, name) => {
      const current = snapshot(parent, name);
      const position = current.text.indexOf(oldText);
      if (position < 0)
        throw new Error("oldText does not match the current file");
      if (current.text.indexOf(oldText, position + 1) >= 0)
        throw new Error("oldText must match exactly one location");
      const after = current.text.replace(oldText, () => newText);
      if (Buffer.byteLength(after) > MAX_FILE_BYTES)
        throw new Error("Replacement exceeds size limit");
      const proposal = Object.freeze({
        kind: "edit" as const,
        path,
        before: current.text,
        after,
        identity: current.identity,
        diff: createTwoFilesPatch(
          `a/${path}`,
          `b/${path}`,
          current.text,
          after,
          "",
          "",
          { context: 3 },
        ),
      });
      this.proposals.set(proposal, parentIdentity(parent));
      return proposal;
    });
  }

  async applyEdit(proposal: WorkspaceEditProposal): Promise<void> {
    const expectedParent = proposal && this.proposals.get(proposal);
    if (!expectedParent)
      throw new Error(
        "Edit proposal was not issued by this workspace or was already used",
      );
    this.proposals.delete(proposal);
    this.at(proposal.path, (parent, name) => {
      if (parentIdentity(parent) !== expectedParent)
        throw new Error("Workspace parent changed since edit was prepared");
      const verify = () => {
        const current = snapshot(parent, name);
        if (
          current.identity !== proposal.identity ||
          current.text !== proposal.before
        )
          throw new Error("File changed since edit was prepared");
        return current;
      };
      const current = verify();
      const tempName = `.local-agent-${randomUUID()}`;
      let temporary = false;
      try {
        const fd = openAt(
          parent,
          tempName,
          constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY,
          0o600,
        );
        temporary = true;
        try {
          writeFileSync(fd, proposal.after, "utf8");
          fchmodSync(fd, current.mode);
          fsyncSync(fd);
        } finally {
          closeSync(fd);
        }
        verify();
        // POSIX rename has no compare-and-swap against other writers. Recheck
        // immediately before publication; all pathname operations stay pinned.
        renameAt(parent, tempName, name);
        temporary = false;
        fsyncSync(parent);
      } finally {
        if (temporary) unlinkAt(parent, tempName);
      }
    });
  }

  private at<T>(
    path: string,
    operation: (parent: number, name: string) => T,
  ): T {
    if (this.fd < 0) throw new Error("Workspace is closed");
    const relative = this.relative(path);
    // ponytail: synchronous, bounded file operations keep descriptors in one
    // scope; move to a worker if 8 MiB reads measurably stall terminal rendering.
    return withParent(this.fd, relative, operation);
  }

  /**
   * The workspace-relative form of a model's path, lexically: no `realpath`, no
   * `resolve`. An absolute path is accepted only when it lies under the root;
   * `..` is never resolved. Symbolic links are refused later, by the pinned
   * `openat` chain. `""` is the root itself.
   */
  normalize(path: string): string {
    if (typeof path !== "string" || /[\\\0]/u.test(path))
      throw new Error("Workspace path must be relative");
    let rest = path;
    if (path.startsWith("/")) {
      const root = this.roots.find(
        (candidate) => path === candidate || path.startsWith(`${candidate}/`),
      );
      if (root === undefined) throw this.absolute(path);
      rest = path.slice(root.length);
    }
    const parts = rest.split("/").filter((part) => part && part !== ".");
    if (parts.includes("..")) throw new Error("Workspace path escapes root");
    return parts.join("/");
  }

  /** A file path: normalized, not the root, not an excluded secret. */
  private relative(path: string): string {
    const relative = this.normalize(path);
    if (!relative)
      throw new Error(
        "Workspace path must name a file, not the workspace root",
      );
    rejectExcluded(relative);
    return relative;
  }

  private absolute(path: string): Error {
    const parts = path.split("/").filter(Boolean);
    const suggestion =
      parts.includes("..") || parts.includes(".")
        ? undefined
        : this.suggest(parts);
    return new Error(
      suggestion
        ? `Workspace path must be relative to the workspace root, not absolute: ${JSON.stringify(path)}. Use ${JSON.stringify(suggestion)}.`
        : `Workspace path must be relative to the workspace root, and ${JSON.stringify(path)} is outside it.`,
    );
  }

  /** The relative path an absolute one most likely meant, when that is obvious. */
  private suggest(parts: string[]): string | undefined {
    const first = parts[0]?.toLowerCase() ?? "";
    const stripped = WRAPPER_NAMES.has(first) && parts.length > 1;
    const candidates = [parts.join("/")];
    if (stripped) candidates.push(parts.slice(1).join("/"));
    for (const candidate of candidates)
      if (this.exists(candidate)) return candidate;
    if (SYSTEM_ROOTS.has(first)) return undefined;
    return stripped ? parts.slice(1).join("/") : parts.join("/");
  }

  private exists(relative: string): boolean {
    try {
      withParent(this.fd, relative, (parent, name) => {
        closeSync(openAt(parent, name, constants.O_RDONLY));
      });
      return true;
    } catch {
      return false;
    }
  }

  /** The entries of a directory (the root for `""`), dirs first, with `/` after a directory name. */
  async list(path = ""): Promise<string> {
    if (this.fd < 0) throw new Error("Workspace is closed");
    const relative = this.normalize(path);
    if (relative) rejectExcluded(relative);
    return this.inDirectory(relative, (fd) => {
      const entries = this.entries(fd, relative);
      const lines = entries.map((entry) =>
        entry.isDirectory()
          ? `${printable(entry.name)}/`
          : entry.isSymbolicLink()
            ? `${printable(entry.name)}@`
            : printable(entry.name),
      );
      if (lines.length === 0) return "(empty directory)";
      const shown = lines.slice(0, LIST_LIMIT);
      if (lines.length > LIST_LIMIT)
        shown.push(`… ${lines.length - LIST_LIMIT} more entries not shown`);
      return shown.join("\n");
    });
  }

  /**
   * Literal (or regular expression) search over the text files below a path,
   * as `path:line: text`. `.git`, `node_modules`, secrets, binaries and large
   * files are skipped, and the work and the output are bounded.
   */
  async search(
    pattern: string,
    options: { path?: string; regex?: boolean; ignoreCase?: boolean } = {},
  ): Promise<string> {
    if (this.fd < 0) throw new Error("Workspace is closed");
    if (!pattern) throw new Error("Search pattern must not be empty");
    if (pattern.length > 500) throw new Error("Search pattern is too long");
    const test = matcher(pattern, options);
    const relative = this.normalize(options.path ?? "");
    if (relative) rejectExcluded(relative);
    const state: SearchState = {
      test,
      matches: [],
      more: false,
      files: 0,
      stopped: undefined,
      deadline: Date.now() + SEARCH_MILLISECONDS,
    };
    const kind = relative
      ? withParent(this.fd, relative, (parent, name) => {
          const fd = openAt(parent, name, constants.O_RDONLY);
          try {
            const stat = fstatSync(fd);
            if (stat.isDirectory()) return "directory";
            if (stat.isFile()) return "file";
            return "other";
          } finally {
            closeSync(fd);
          }
        })
      : "directory";
    if (kind === "other")
      throw new Error("Search path must be a file or directory");
    if (kind === "file")
      withParent(this.fd, relative, (parent, name) =>
        this.searchFile(parent, name, relative, state),
      );
    else
      this.inDirectory(relative, (fd) =>
        this.searchDirectory(fd, relative, 0, state),
      );
    const lines = state.matches.map(
      (match) => `${match.path}:${match.line}: ${match.text}`,
    );
    if (state.more)
      lines.push(
        `… more matches not shown (first ${SEARCH_MATCH_LIMIT} listed); narrow the pattern or the path`,
      );
    if (state.stopped) lines.push(`… ${state.stopped}; narrow the path`);
    return lines.length ? lines.join("\n") : "No matches";
  }

  private inDirectory<T>(relative: string, operation: (fd: number) => T): T {
    if (!relative) return operation(this.fd);
    let opened: number;
    try {
      opened = withParent(this.fd, relative, (parent, name) =>
        openAt(parent, name, DIRECTORY_FLAGS),
      );
    } catch (error) {
      if (error instanceof Error && /escapes|excluded/u.test(error.message))
        throw error;
      throw new Error(
        `Cannot list ${relative}: it is missing, not a directory, or a symbolic link`,
      );
    }
    try {
      return operation(opened);
    } finally {
      closeSync(opened);
    }
  }

  /**
   * The entries of an open directory. The names come from a path-based read, so
   * the directory is checked to be the very one the descriptor pins before and
   * after (Node cannot read a directory descriptor).
   */
  private entries(fd: number, relative: string): Dirent[] {
    const absolute = relative ? join(this.root, relative) : this.root;
    const pinned = fstatSync(fd, { bigint: true });
    const same = () => {
      const current = lstatSync(absolute, { bigint: true });
      return (
        current.isDirectory() &&
        current.dev === pinned.dev &&
        current.ino === pinned.ino
      );
    };
    if (!same()) throw new Error("Workspace directory changed during listing");
    const entries = readdirSync(absolute, { withFileTypes: true });
    if (!same()) throw new Error("Workspace directory changed during listing");
    return entries
      .filter((entry) =>
        entry.isDirectory()
          ? !SKIPPED_DIRECTORIES.has(entry.name) &&
            !isExcludedName(entry.name, false)
          : !isExcludedName(entry.name, true),
      )
      .sort(
        (a, b) =>
          Number(b.isDirectory()) - Number(a.isDirectory()) ||
          (a.name < b.name ? -1 : a.name > b.name ? 1 : 0),
      );
  }

  private searchDirectory(
    fd: number,
    relative: string,
    depth: number,
    state: SearchState,
  ): void {
    for (const entry of this.entries(fd, relative)) {
      if (state.more || state.stopped) return;
      const child = relative ? `${relative}/${entry.name}` : entry.name;
      if (entry.isDirectory()) {
        if (depth >= SEARCH_DEPTH) continue;
        let opened: number;
        try {
          opened = openAt(fd, entry.name, DIRECTORY_FLAGS);
        } catch {
          continue;
        }
        try {
          this.searchDirectory(opened, child, depth + 1, state);
        } catch (error) {
          if (
            !(error instanceof Error) ||
            !/changed during/u.test(error.message)
          )
            throw error;
        } finally {
          closeSync(opened);
        }
      } else if (entry.isFile()) {
        this.searchFile(fd, entry.name, child, state);
      }
    }
  }

  private searchFile(
    parent: number,
    name: string,
    relative: string,
    state: SearchState,
  ): void {
    if (state.files >= SEARCH_FILE_LIMIT) {
      state.stopped = `stopped after scanning ${SEARCH_FILE_LIMIT} files`;
      return;
    }
    if (Date.now() > state.deadline) {
      state.stopped = `stopped after ${SEARCH_MILLISECONDS / 1000} seconds`;
      return;
    }
    state.files++;
    let text: string;
    try {
      text = snapshot(parent, name, SEARCH_FILE_BYTES).text;
    } catch {
      return; // binary, not UTF-8, too large, hard-linked, or gone: not searchable text
    }
    const lines = text.split("\n");
    for (const [index, raw] of lines.entries()) {
      const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
      if (!state.test(line.slice(0, SEARCH_SCAN_CHARS))) continue;
      if (state.matches.length >= SEARCH_MATCH_LIMIT) {
        state.more = true;
        return;
      }
      state.matches.push({
        path: relative,
        line: index + 1,
        text: printable(
          line.length > SEARCH_LINE_CHARS
            ? `${line.slice(0, SEARCH_LINE_CHARS)}…`
            : line,
        ),
      });
    }
  }

  /**
   * A new file, or the replacement of an existing one, for the host to approve.
   * The diff shows a new file as added lines. Missing directories are created
   * only when the proposal is applied.
   */
  async prepareWrite(
    path: string,
    content: string,
  ): Promise<WorkspaceWriteProposal> {
    if (typeof content !== "string") throw new Error("content must be text");
    if (content.includes("\0"))
      throw new Error("Binary content is not supported");
    if (Buffer.byteLength(content) > MAX_FILE_BYTES)
      throw new Error("Content exceeds size limit");
    const relative = this.relative(path);
    let current: Snapshot | undefined;
    let parentIdentityValue = "create";
    try {
      this.at(relative, (parent, name) => {
        current = snapshot(parent, name);
        parentIdentityValue = parentIdentity(parent);
      });
    } catch (error) {
      if (
        !(error instanceof Error && "code" in error && error.code === "ENOENT")
      )
        throw error;
      current = undefined;
    }
    if (current && current.text === content)
      throw new Error("write would not change the file");
    const before = current?.text ?? "";
    const proposal = Object.freeze({
      kind: "write" as const,
      path: relative,
      created: current === undefined,
      before,
      after: content,
      identity: current?.identity ?? "new",
      diff: createTwoFilesPatch(
        current ? `a/${relative}` : "/dev/null",
        `b/${relative}`,
        before,
        content,
        "",
        "",
        { context: 3 },
      ),
    });
    this.proposals.set(proposal, parentIdentityValue);
    return proposal;
  }

  async applyWrite(proposal: WorkspaceWriteProposal): Promise<void> {
    const expectedParent = proposal && this.proposals.get(proposal);
    if (!expectedParent || proposal.kind !== "write")
      throw new Error(
        "Write proposal was not issued by this workspace or was already used",
      );
    this.proposals.delete(proposal);
    if (this.fd < 0) throw new Error("Workspace is closed");
    const relative = this.relative(proposal.path);
    const run = proposal.created ? withParentCreating : withParent;
    run(this.fd, relative, (parent, name) => {
      if (!proposal.created && parentIdentity(parent) !== expectedParent)
        throw new Error("Workspace parent changed since write was prepared");
      const verify = () => {
        if (proposal.created) {
          let exists = true;
          try {
            closeSync(openAt(parent, name, constants.O_RDONLY));
          } catch (error) {
            if (
              error instanceof Error &&
              "code" in error &&
              error.code === "ENOENT"
            )
              exists = false;
          }
          if (exists)
            throw new Error("File was created since write was prepared");
          return undefined;
        }
        const now = snapshot(parent, name);
        if (now.identity !== proposal.identity || now.text !== proposal.before)
          throw new Error("File changed since write was prepared");
        return now;
      };
      const existing = verify();
      const tempName = `.local-agent-${randomUUID()}`;
      let temporary = false;
      try {
        const fd = openAt(
          parent,
          tempName,
          constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY,
          0o600,
        );
        temporary = true;
        try {
          writeFileSync(fd, proposal.after, "utf8");
          fchmodSync(fd, existing?.mode ?? 0o644);
          fsyncSync(fd);
        } finally {
          closeSync(fd);
        }
        verify();
        renameAt(parent, tempName, name);
        temporary = false;
        fsyncSync(parent);
      } finally {
        if (temporary) unlinkAt(parent, tempName);
      }
    });
  }
}

type SearchState = {
  test: (line: string) => boolean;
  matches: { path: string; line: number; text: string }[];
  more: boolean;
  files: number;
  stopped: string | undefined;
  deadline: number;
};

function matcher(
  pattern: string,
  options: { regex?: boolean; ignoreCase?: boolean },
): (line: string) => boolean {
  if (options.regex) {
    let expression: RegExp;
    try {
      // grep-style `a\|b` means alternation here too.
      expression = new RegExp(
        pattern.replaceAll("\\|", "|"),
        options.ignoreCase ? "i" : "",
      );
    } catch (error) {
      throw new Error(
        `Invalid regular expression: ${error instanceof Error ? error.message : "syntax error"}`,
      );
    }
    return (line) => expression.test(line);
  }
  if (options.ignoreCase) {
    const needle = pattern.toLowerCase();
    return (line) => line.toLowerCase().includes(needle);
  }
  return (line) => line.includes(pattern);
}

/** A name or line without control characters, so one entry stays on one line. */
function printable(text: string): string {
  let out = "";
  for (const char of text) {
    const code = char.codePointAt(0) ?? 0;
    out += code < 0x20 || code === 0x7f ? "?" : char;
  }
  return out;
}

function parentIdentity(fd: number): string {
  const stat = fstatSync(fd, { bigint: true });
  return `${stat.dev}:${stat.ino}`;
}

function snapshot(
  parent: number,
  name: string,
  maxBytes = MAX_FILE_BYTES,
): Snapshot {
  const fd = openAt(parent, name, constants.O_RDONLY);
  try {
    const before = fstatSync(fd, { bigint: true });
    if (!before.isFile())
      throw new Error("Workspace path must be a regular file");
    if (before.nlink !== 1n)
      throw new Error("Hard-linked files are not allowed");
    if (before.size > BigInt(maxBytes))
      throw new Error("File exceeds size limit");
    const buffer = Buffer.allocUnsafe(Number(before.size) + 1);
    let offset = 0;
    while (offset < buffer.length) {
      const count = readSync(
        fd,
        buffer,
        offset,
        buffer.length - offset,
        offset,
      );
      if (count === 0) break;
      offset += count;
    }
    const after = fstatSync(fd, { bigint: true });
    if (
      before.size !== BigInt(offset) ||
      before.mtimeNs !== after.mtimeNs ||
      before.ctimeNs !== after.ctimeNs
    )
      throw new Error("File changed during read");
    let text: string;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(
        buffer.subarray(0, offset),
      );
    } catch {
      throw new Error("File is not valid UTF-8");
    }
    if (text.includes("\0")) throw new Error("Binary files are not supported");
    return {
      text,
      mode: Number(before.mode & 0o777n),
      identity: `${before.dev}:${before.ino}:${before.size}:${before.mtimeNs}:${before.ctimeNs}`,
    };
  } finally {
    closeSync(fd);
  }
}

const EXCLUDED_NAMES = new Set([
  ".git",
  ".ssh",
  ".gnupg",
  ".kube",
  ".aws",
  "keychains",
  ".env",
  ".npmrc",
  ".netrc",
  "credentials",
]);
const ENV_TEMPLATES = new Set([
  ".env.example",
  ".env.sample",
  ".env.template",
  ".env.dist",
]);

/** Host secret exclusions; a template such as `.env.example` is allowed only as a leaf. */
function isExcludedName(part: string, leaf: boolean): boolean {
  const lower = part.toLowerCase();
  return (
    EXCLUDED_NAMES.has(lower) ||
    (lower.startsWith(".env.") && !(leaf && ENV_TEMPLATES.has(part))) ||
    lower.endsWith(".pem") ||
    lower.endsWith(".key") ||
    lower.startsWith("id_rsa") ||
    lower.startsWith("id_ed25519") ||
    lower.endsWith(".p12") ||
    lower.endsWith(".keystore")
  );
}

function rejectExcluded(relative: string): void {
  const parts = relative.split("/");
  if (
    parts.some((part, index) =>
      isExcludedName(part, index === parts.length - 1),
    )
  )
    throw new Error("Workspace path is excluded");
}
