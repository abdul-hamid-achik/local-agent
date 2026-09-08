import {
  link,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import { Workspace } from "#runtime/workspace";

const roots: string[] = [];
const workspaces: Workspace[] = [];
afterEach(async () => {
  for (const workspace of workspaces.splice(0)) workspace.close();
  for (const root of roots.splice(0))
    await rm(root, { recursive: true, force: true });
});
async function temp(prefix: string) {
  const root = await mkdtemp(join(tmpdir(), prefix));
  roots.push(root);
  return root;
}
async function fixture() {
  const root = await temp("local-agent-workspace-");
  await writeFile(join(root, "notes.md"), "before\nkeep\n", "utf8");
  const workspace = await Workspace.create(root);
  workspaces.push(workspace);
  return { root, workspace };
}

test("reads bounded UTF-8 files and prepares immutable exact edits", async () => {
  const { root, workspace } = await fixture();
  expect(await workspace.read("notes.md")).toBe("before\nkeep\n");
  const proposal = await workspace.prepareEdit("notes.md", "before", "after");
  expect(proposal).toMatchObject({
    path: "notes.md",
    before: "before\nkeep\n",
    after: "after\nkeep\n",
  });
  expect(proposal.diff).toContain("-before");
  expect(proposal.diff).toContain("+after");
  expect(Object.isFrozen(proposal)).toBe(true);
  await workspace.applyEdit(proposal);
  expect(await readFile(join(root, "notes.md"), "utf8")).toBe("after\nkeep\n");
});

test("rejects ambiguous, empty, invalid, escaped, secret, and non-regular paths", async () => {
  const { root, workspace } = await fixture();
  await writeFile(join(root, "duplicate.txt"), "same same", "utf8");
  await writeFile(join(root, ".env"), "TOKEN=secret", "utf8");
  await expect(
    workspace.prepareEdit("duplicate.txt", "same", "new"),
  ).rejects.toThrow("exactly one");
  await expect(workspace.prepareEdit("notes.md", "", "new")).rejects.toThrow(
    "must not be empty",
  );
  await expect(workspace.read("../notes.md")).rejects.toThrow("escapes");
  await expect(workspace.read(".env")).rejects.toThrow("excluded");
  await expect(workspace.read(".git/config")).rejects.toThrow("excluded");
  await expect(workspace.read(".")).rejects.toThrow("escapes");
  await expect(workspace.read("missing.txt")).rejects.toThrow();
  await expect(workspace.read("notes.md/child")).rejects.toThrow();
});

test("rejects symlink entries and symlinked parents", async () => {
  const { root, workspace } = await fixture();
  const outside = await temp("local-agent-outside-");
  await writeFile(join(outside, "secret.txt"), "secret", "utf8");
  await symlink(join(outside, "secret.txt"), join(root, "link.txt"));
  await symlink(outside, join(root, "linked-dir"));
  await expect(workspace.read("link.txt")).rejects.toThrow("symbolic links");
  await expect(workspace.read("linked-dir/secret.txt")).rejects.toThrow();
});

test("rejects a parent replaced by a symlink before apply", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "file.txt"), "before", "utf8");
  const outside = await temp("local-agent-outside-");
  await writeFile(join(outside, "file.txt"), "outside", "utf8");
  const proposal = await workspace.prepareEdit(
    "src/file.txt",
    "before",
    "after",
  );
  await rename(join(root, "src"), join(root, "src-old"));
  await symlink(outside, join(root, "src"));
  await expect(workspace.applyEdit(proposal)).rejects.toThrow();
  expect(await readFile(join(outside, "file.txt"), "utf8")).toBe("outside");
});

test("rejects a proposal when the file changes after preparation", async () => {
  const { root, workspace } = await fixture();
  const proposal = await workspace.prepareEdit("notes.md", "before", "after");
  await writeFile(join(root, "notes.md"), "changed\nkeep\n", "utf8");
  await expect(workspace.applyEdit(proposal)).rejects.toThrow("changed since");
  expect(await workspace.read("notes.md")).toBe("changed\nkeep\n");
});

test("requires a workspace-issued proposal and consumes it after one apply", async () => {
  const { workspace } = await fixture();
  const proposal = await workspace.prepareEdit("notes.md", "before", "after");
  await expect(workspace.applyEdit({ ...proposal })).rejects.toThrow(
    "issued by this workspace",
  );
  await workspace.applyEdit(proposal);
  await expect(workspace.applyEdit(proposal)).rejects.toThrow(
    "issued by this workspace",
  );
});

test("supports literal deletion without replacement metacharacter expansion", async () => {
  const { workspace } = await fixture();
  const proposal = await workspace.prepareEdit("notes.md", "before", "");
  expect(proposal.after).toBe("\nkeep\n");
  await workspace.applyEdit(proposal);
  expect(await workspace.read("notes.md")).toBe("\nkeep\n");
});

test("rejects invalid UTF-8 and oversized files", async () => {
  const { root, workspace } = await fixture();
  await writeFile(join(root, "invalid.bin"), Buffer.from([0xff, 0xfe]));
  await writeFile(join(root, "large.bin"), Buffer.alloc(8 * 1024 * 1024 + 1));
  await expect(workspace.read("invalid.bin")).rejects.toThrow("UTF-8");
  await expect(workspace.read("large.bin")).rejects.toThrow("size limit");
});

test("keeps the original root capability after the root path is replaced", async () => {
  const { root, workspace } = await fixture();
  const moved = `${root}-original`;
  roots.push(moved);
  const outside = await temp("local-agent-outside-");
  await writeFile(join(outside, "notes.md"), "outside");
  const proposal = await workspace.prepareEdit("notes.md", "before", "after");
  await rename(root, moved);
  await symlink(outside, root);
  expect(await workspace.read("notes.md")).toBe("before\nkeep\n");
  await workspace.applyEdit(proposal);
  expect(await readFile(join(outside, "notes.md"), "utf8")).toBe("outside");
  expect(await readFile(join(moved, "notes.md"), "utf8")).toBe("after\nkeep\n");
  workspace.close();
  await expect(workspace.read("notes.md")).rejects.toThrow("closed");
});

test("rejects hard links, NUL paths, and overlapping exact matches", async () => {
  const { root, workspace } = await fixture();
  await link(join(root, "notes.md"), join(root, "alias.md"));
  await expect(workspace.read("alias.md")).rejects.toThrow("Hard-linked");
  await expect(workspace.read("notes.md\0ignored")).rejects.toThrow();
  await writeFile(join(root, "overlap.txt"), "aaa");
  await expect(workspace.prepareEdit("overlap.txt", "aa", "b")).rejects.toThrow(
    "exactly one",
  );
});

test("host secret exclusions match leaves and cannot be bypassed by template directories", async () => {
  const { root, workspace } = await fixture();
  for (const name of [
    ".npmrc",
    ".netrc",
    "id_ed25519",
    "signing.p12",
    "android.keystore",
  ]) {
    await writeFile(join(root, name), "secret");
    await expect(workspace.read(name)).rejects.toThrow("excluded");
  }
  await mkdir(join(root, ".env.example"));
  await writeFile(join(root, ".env.example", "hidden.txt"), "secret");
  await expect(workspace.read(".env.example/hidden.txt")).rejects.toThrow(
    "excluded",
  );
  for (const name of [".env.dist", ".env.sample", ".env.template"]) {
    await writeFile(join(root, name), "template");
    expect(await workspace.read(name)).toBe("template");
  }
});
