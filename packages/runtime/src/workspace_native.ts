import { dlopen, type FFIFunction, ptr, read } from "bun:ffi";
import { closeSync, constants, fchmodSync, fstatSync, openSync } from "node:fs";

// Node does not expose O_CLOEXEC, but native descriptors must not reach children.
export const CLOEXEC = process.platform === "darwin" ? 0x1000000 : 0x80000;
export const FILE_FLAGS = constants.O_NOFOLLOW | constants.O_NONBLOCK | CLOEXEC;
export const DIRECTORY_FLAGS =
  constants.O_RDONLY | constants.O_DIRECTORY | FILE_FLAGS;
function loadLibrary(path: string) {
  const symbols = {
    openat: { args: ["int", "cstring", "int", "int"], returns: "int" },
    renameat: { args: ["int", "cstring", "int", "cstring"], returns: "int" },
    unlinkat: { args: ["int", "cstring", "int"], returns: "int" },
    mkdirat: { args: ["int", "cstring", "int"], returns: "int" },
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

let fstatatSymbol:
  | ((dirfd: number, path: Buffer, buffer: Buffer, flags: number) => number)
  | null
  | undefined;

/**
 * The inode and the low 32 bits of the device of an entry of `parent`, read
 * without opening it (a file whose mode could not be set may be unreadable).
 * Both sit at the same offsets in `struct stat` on macOS and 64-bit Linux.
 * `fstatat` is looked up on its own: a libc without it must not stop the rest
 * of this module from loading. Undefined when it cannot be read.
 */
function entryIdentity(
  parent: number,
  name: string,
): { dev: bigint; ino: bigint } | undefined {
  if (fstatatSymbol === undefined) {
    fstatatSymbol = null;
    const candidates =
      process.platform === "darwin"
        ? ["/usr/lib/libSystem.B.dylib"]
        : [
            "libc.so.6",
            `/lib/ld-musl-${process.arch === "arm64" ? "aarch64" : "x86_64"}.so.1`,
          ];
    for (const path of candidates) {
      try {
        const { symbols } = dlopen(path, {
          fstatat: { args: ["int", "ptr", "ptr", "int"], returns: "int" },
        });
        fstatatSymbol = (dirfd, entry, buffer, flags) =>
          symbols.fstatat(dirfd, ptr(entry), ptr(buffer), flags);
        break;
      } catch {
        // Try the next library.
      }
    }
  }
  if (!fstatatSymbol) return undefined;
  const buffer = Buffer.alloc(256);
  const noFollow = process.platform === "darwin" ? 0x20 : 0x100;
  if (fstatatSymbol(parent, component(name), buffer, noFollow) !== 0)
    return undefined;
  return {
    dev: BigInt(buffer.readUInt32LE(0)),
    ino: buffer.readBigUInt64LE(8),
  };
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

/**
 * `mode` applies only to a file this call creates (O_CREAT with O_EXCL) and
 * is exact: the umask does not mask it, as with fchmod.
 */
export function openAt(
  parent: number,
  name: string,
  flags: number,
  mode = 0,
): number {
  const creating = (flags & constants.O_CREAT) !== 0;
  if (creating && (flags & constants.O_EXCL) === 0)
    throw new Error("Creating a workspace file requires O_EXCL");
  const bytes = component(name);
  // Whatever `mode` the kernel reads (see below), a new file is created
  // private to the owner; the umask is restored whatever happens. The umask
  // is process-wide while it is set: another native creator in that window
  // gets owner-only modes, the restrictive direction. The new file is
  // private only when the umask could be set (not in a worker thread);
  // otherwise it has whatever mode the kernel read until the fchmod below.
  let umask: number | undefined;
  if (creating) {
    try {
      umask = process.umask(0o077);
    } catch {
      // A worker thread cannot change it.
    }
  }
  let fd: number;
  let code = 0;
  try {
    fd = libc().symbols.openat(parent, ptr(bytes), flags | FILE_FLAGS, mode);
    if (fd < 0) code = errno();
  } finally {
    if (umask !== undefined) process.umask(umask);
  }
  if (fd < 0) {
    throw Object.assign(
      new Error(
        "Workspace entry cannot be opened: missing, inaccessible, or symbolic links",
      ),
      { code: code === 2 ? "ENOENT" : "EACCES", errno: code },
    );
  }
  if (creating) {
    // openat is variadic: on Apple arm64 the FFI passes `mode` where the
    // kernel does not read it, and the file was created with mode 0. Set it
    // on the descriptor before anything is written.
    try {
      fchmodSync(fd, mode);
    } catch (error) {
      try {
        // Remove only the file this descriptor is: the name may have been
        // replaced by now, and that is not ours to unlink. The dev/ino check
        // narrows the window between fstatat and unlinkAt; it does not close
        // it, since the name can still be swapped in between.
        const created = fstatSync(fd, { bigint: true });
        const entry = entryIdentity(parent, name);
        if (
          entry &&
          entry.ino === created.ino &&
          entry.dev === (created.dev & 0xffffffffn)
        )
          unlinkAt(parent, name);
      } catch {
        // The entry is gone or unreachable.
      }
      closeSync(fd);
      throw error;
    }
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

/**
 * Like `withParent`, but creates the missing directories of the path (relative
 * to the pinned parent, never following a symbolic link), naming each in
 * `made`. Used only to publish a file the host has already approved.
 */
export function withParentCreating<T>(
  root: number,
  path: string,
  operation: (parent: number, name: string) => T,
  made: string[] = [],
): T {
  const parts = path.split("/");
  for (const part of parts) component(part);
  const name = parts.pop();
  if (!name) throw new Error("Missing workspace filename");
  let parent = root;
  try {
    for (const [index, part] of parts.entries()) {
      let next: number;
      try {
        next = openAt(parent, part, DIRECTORY_FLAGS);
      } catch (error) {
        if (
          !(
            error instanceof Error &&
            "code" in error &&
            error.code === "ENOENT"
          )
        )
          throw error;
        if (libc().symbols.mkdirat(parent, ptr(component(part)), 0o755) !== 0) {
          const code = errno();
          if (code !== 17)
            throw new Error(
              `Workspace directory could not be created (errno ${code})`,
            );
        } else made.push(parts.slice(0, index + 1).join("/"));
        next = openAt(parent, part, DIRECTORY_FLAGS);
      }
      if (parent !== root) closeSync(parent);
      parent = next;
    }
    return operation(parent, name);
  } finally {
    if (parent !== root) closeSync(parent);
  }
}

export function unlinkAt(parent: number, name: string): void {
  const bytes = component(name);
  if (libc().symbols.unlinkat(parent, ptr(bytes), 0) !== 0)
    throw new Error("Workspace temporary file cleanup failed");
}
