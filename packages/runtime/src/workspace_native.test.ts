import {
  closeSync,
  constants,
  existsSync,
  mkdtempSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test, vi } from "vitest";
import { openAt, openRoot } from "#runtime/workspace_native";

// Lets a test make the explicit fchmod fail, and act at that moment.
const hooks = vi.hoisted(() => ({
  fchmod: undefined as ((fd: number, mode: number) => void) | undefined,
}));
vi.mock("node:fs", async (importOriginal) => {
  const actual = await importOriginal<typeof import("node:fs")>();
  return {
    ...actual,
    fchmodSync: (fd: number, mode: number) =>
      hooks.fchmod ? hooks.fchmod(fd, mode) : actual.fchmodSync(fd, mode),
  };
});

const created = constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY;
const cleanup: (() => void)[] = [];
afterEach(() => {
  hooks.fchmod = undefined;
  vi.restoreAllMocks();
  for (const undo of cleanup.splice(0)) undo();
});

function directory() {
  const dir = mkdtempSync(join(tmpdir(), "local-agent-native-"));
  const root = openRoot(dir);
  cleanup.push(() => {
    closeSync(root);
    rmSync(dir, { recursive: true, force: true });
  });
  return { dir, root };
}

// openat is variadic in libc. On Apple arm64 the `mode` of a non-variadic FFI
// call lands in a register while the kernel reads the stack: the new file came
// out with mode 0 instead of the 0o600 the caller asked for. This test guards
// Apple arm64 only: on x86_64 (and Linux) it passes without the fix.
test("a file created through openAt has exactly the requested mode, whatever the umask", () => {
  const { dir, root } = directory();
  const previous = process.umask();
  cleanup.push(() => process.umask(previous));
  let n = 0;
  for (const umask of [0o022, 0o077, 0]) {
    process.umask(umask);
    for (const mode of [0o600, 0o644, 0o755]) {
      const name = `file-${n++}`;
      closeSync(openAt(root, name, created, mode));
      // Exactly the requested bits: fchmod ignores the umask, as the callers
      // that go on to fchmod an existing file's mode expect.
      expect(
        statSync(join(dir, name)).mode & 0o7777,
        `umask ${umask.toString(8)} mode ${mode.toString(8)}`,
      ).toBe(mode);
    }
  }
});

test("openAt refuses a mode it cannot promise: O_CREAT without O_EXCL", () => {
  const { root } = directory();
  expect(() =>
    openAt(root, "maybe-new", constants.O_CREAT | constants.O_WRONLY, 0o600),
  ).toThrow("O_EXCL");
});

test("the file is created under a private umask, restored whether or not the open succeeds", () => {
  const { dir, root } = directory();
  const umask = vi.spyOn(process, "umask");
  closeSync(openAt(root, "first", created, 0o644));
  expect(umask.mock.calls).toEqual([[0o077], [expect.any(Number)]]);
  umask.mockClear();
  // O_EXCL on an existing name fails; the umask still comes back.
  writeFileSync(join(dir, "taken"), "x");
  expect(() => openAt(root, "taken", created, 0o600)).toThrow();
  expect(umask.mock.calls).toEqual([[0o077], [expect.any(Number)]]);
  expect(umask.mock.calls[1]?.[0]).toBe(umask.mock.results[0]?.value);
  umask.mockClear();
  // A plain open does not touch it.
  closeSync(openAt(root, "taken", constants.O_RDONLY));
  expect(umask).not.toHaveBeenCalled();
});

test("a failed fchmod removes the file the call created", () => {
  const { dir, root } = directory();
  hooks.fchmod = () => {
    throw new Error("fchmod failed");
  };
  expect(() => openAt(root, "doomed", created, 0o600)).toThrow("fchmod failed");
  expect(existsSync(join(dir, "doomed"))).toBe(false);
});

test("a failed fchmod leaves alone an entry that has been replaced since", () => {
  const { dir, root } = directory();
  hooks.fchmod = () => {
    // Someone swaps the name for another file between open and cleanup.
    rmSync(join(dir, "swapped"));
    writeFileSync(join(dir, "swapped"), "theirs");
    throw new Error("fchmod failed");
  };
  expect(() => openAt(root, "swapped", created, 0o600)).toThrow(
    "fchmod failed",
  );
  expect(existsSync(join(dir, "swapped"))).toBe(true);
});
