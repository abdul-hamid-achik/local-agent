import { dlopen, type FFIFunction, ptr, read } from "bun:ffi";
import { closeSync, constants, openSync } from "node:fs";

// Node does not expose O_CLOEXEC, but native descriptors must not reach children.
export const CLOEXEC = process.platform === "darwin" ? 0x1000000 : 0x80000;
export const FILE_FLAGS = constants.O_NOFOLLOW | constants.O_NONBLOCK | CLOEXEC;
const DIRECTORY_FLAGS = constants.O_RDONLY | constants.O_DIRECTORY | FILE_FLAGS;
function loadLibrary(path: string) {
  const symbols = {
    openat: { args: ["int", "cstring", "int", "int"], returns: "int" },
    renameat: { args: ["int", "cstring", "int", "cstring"], returns: "int" },
    unlinkat: { args: ["int", "cstring", "int"], returns: "int" },
    flock: { args: ["int", "int"], returns: "int" },
  } satisfies Record<string, FFIFunction>;
  return process.platform === "darwin"
    ? dlopen(path, { ...symbols, __error: { args: [], returns: "ptr" } })
    : dlopen(path, {
        ...symbols,
        __errno_location: { args: [], returns: "ptr" },
      });
}
let library: ReturnType<typeof loadLibrary> | undefined;
function libc() {
  if (library) return library;
  if (process.platform === "darwin") {
    library = loadLibrary("/usr/lib/libSystem.B.dylib");
    return library;
  }
  if (process.platform === "linux") {
    try {
      library = loadLibrary("libc.so.6");
      return library;
    } catch (error) {
      const architecture =
        process.arch === "arm64"
          ? "aarch64"
          : process.arch === "x64"
            ? "x86_64"
            : undefined;
      if (!architecture) throw error;
      library = loadLibrary(`/lib/ld-musl-${architecture}.so.1`);
      return library;
    }
  }
  throw new Error("Workspace file authority currently requires macOS or Linux");
}

function component(value: string): Buffer {
  if (!value || value === "." || value === ".." || /[/\\\0]/u.test(value))
    throw new Error("Invalid workspace path component");
  return Buffer.from(`${value}\0`);
}

function errno(): number {
  const symbols = libc().symbols;
  const address =
    "__error" in symbols ? symbols.__error() : symbols.__errno_location();
  if (!address) throw new Error("Unable to read filesystem error");
  return read.i32(address);
}

/** An exclusive kernel lease; closing the descriptor releases it after crashes. */
export function tryLockFile(fd: number): boolean {
  if (libc().symbols.flock(fd, 2 | 4) === 0) return true;
  const code = errno();
  if (code === (process.platform === "darwin" ? 35 : 11)) return false;
  throw new Error(`Unable to lock file (errno ${code})`);
}

export function openRoot(root: string): number {
  libc();
  return openSync(root, DIRECTORY_FLAGS);
}

export function openAt(
  parent: number,
  name: string,
  flags: number,
  mode = 0,
): number {
  const bytes = component(name);
  const fd = libc().symbols.openat(
    parent,
    ptr(bytes),
    flags | FILE_FLAGS,
    mode,
  );
  if (fd < 0) {
    const code = errno();
    throw Object.assign(
      new Error(
        "Workspace entry cannot be opened: missing, inaccessible, or symbolic links",
      ),
      { code: code === 2 ? "ENOENT" : "EACCES", errno: code },
    );
  }
  return fd;
}

/** Every path component is opened relative to the original root capability. */
export function withParent<T>(
  root: number,
  path: string,
  operation: (parent: number, name: string) => T,
): T {
  const parts = path.split("/");
  for (const part of parts) component(part);
  const name = parts.pop();
  if (!name) throw new Error("Missing workspace filename");
  let parent = root;
  try {
    for (const part of parts) {
      const next = openAt(parent, part, DIRECTORY_FLAGS);
      if (parent !== root) closeSync(parent);
      parent = next;
    }
    return operation(parent, name);
  } finally {
    if (parent !== root) closeSync(parent);
  }
}

export function renameAt(parent: number, source: string, target: string): void {
  const from = component(source);
  const to = component(target);
  if (libc().symbols.renameat(parent, ptr(from), parent, ptr(to)) !== 0)
    throw new Error("Workspace replacement could not be published");
}

export function unlinkAt(parent: number, name: string): void {
  const bytes = component(name);
  if (libc().symbols.unlinkat(parent, ptr(bytes), 0) !== 0)
    throw new Error("Workspace temporary file cleanup failed");
}
