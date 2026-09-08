import { randomUUID } from "node:crypto";
import {
  closeSync,
  constants,
  fchmodSync,
  fstatSync,
  fsyncSync,
  readSync,
  writeFileSync,
} from "node:fs";
import { lstat, realpath } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { createTwoFilesPatch } from "diff";
import {
  openAt,
  openRoot,
  renameAt,
  unlinkAt,
  withParent,
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

type Snapshot = { text: string; identity: string; mode: number };

export class Workspace implements Disposable {
  private readonly proposals = new WeakMap<object, string>();
  private constructor(
    readonly root: string,
    private fd: number,
  ) {}

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
      return new Workspace(canonical, fd);
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
    validatePath(path);
    // ponytail: synchronous, bounded file operations keep descriptors in one
    // scope; move to a worker if 8 MiB reads measurably stall terminal rendering.
    return withParent(this.fd, path, operation);
  }
}

function parentIdentity(fd: number): string {
  const stat = fstatSync(fd, { bigint: true });
  return `${stat.dev}:${stat.ino}`;
}

function snapshot(parent: number, name: string): Snapshot {
  const fd = openAt(parent, name, constants.O_RDONLY);
  try {
    const before = fstatSync(fd, { bigint: true });
    if (!before.isFile())
      throw new Error("Workspace path must be a regular file");
    if (before.nlink !== 1n)
      throw new Error("Hard-linked files are not allowed");
    if (before.size > BigInt(MAX_FILE_BYTES))
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

function validatePath(path: string): void {
  if (!path || isAbsolute(path) || /[\\\0]/u.test(path))
    throw new Error("Workspace path must be relative");
  const parts = path.split("/");
  if (parts.some((part) => !part || part === "." || part === ".."))
    throw new Error("Workspace path escapes root");
  if (
    parts.some((part, index) => {
      const lower = part.toLowerCase();
      return (
        [
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
        ].includes(lower) ||
        (lower.startsWith(".env.") &&
          !(
            index === parts.length - 1 &&
            [
              ".env.example",
              ".env.sample",
              ".env.template",
              ".env.dist",
            ].includes(part)
          )) ||
        lower.endsWith(".pem") ||
        lower.endsWith(".key") ||
        lower.startsWith("id_rsa") ||
        lower.startsWith("id_ed25519") ||
        lower.endsWith(".p12") ||
        lower.endsWith(".keystore")
      );
    })
  )
    throw new Error("Workspace path is excluded");
}
