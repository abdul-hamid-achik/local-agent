import { closeSync, constants, mkdtempSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import { openAt, openRoot } from "#runtime/workspace_native";

const created = constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY;
const cleanup: (() => void)[] = [];
afterEach(() => {
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
// out with mode 0 instead of the 0o600 the caller asked for.
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
