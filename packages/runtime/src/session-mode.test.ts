import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test, vi } from "vitest";

// chmod does nothing here, so the mode a new database has is the mode it
// was created with: SQLite's own creation would follow the umask.
vi.mock("node:fs/promises", async (original) => ({
  ...(await original<typeof import("node:fs/promises")>()),
  chmod: async () => {},
}));
const { SessionStore } = await import("#runtime/session");

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});

test("a new session database is created private, before any chmod", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-session-mode-"));
  roots.push(root);
  const previous = process.umask(0o022);
  try {
    const db = join(root, "session.sqlite");
    const store = await SessionStore.open(db);
    expect((await stat(db)).mode & 0o777).toBe(0o600);
    store.close();
  } finally {
    process.umask(previous);
  }
});
