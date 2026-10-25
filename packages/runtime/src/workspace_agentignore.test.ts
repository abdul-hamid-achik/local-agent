import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
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

async function ignoring(text: string) {
  const root = await temp("local-agent-agentignore-");
  await writeFile(join(root, ".agentignore"), text, "utf8");
  await mkdir(join(root, "build", "out"), { recursive: true });
  await mkdir(join(root, "src", "gen"), { recursive: true });
  await writeFile(join(root, "keep.txt"), "NEEDLE keep\n", "utf8");
  await writeFile(join(root, "debug.log"), "NEEDLE log\n", "utf8");
  await writeFile(join(root, "build", "out", "a.js"), "NEEDLE build\n", "utf8");
  await writeFile(join(root, "src", "main.ts"), "NEEDLE main\n", "utf8");
  await writeFile(join(root, "src", "gen", "x.ts"), "NEEDLE gen\n", "utf8");
  await writeFile(join(root, "src", "trace.log"), "NEEDLE nested\n", "utf8");
  const workspace = await Workspace.create(root);
  workspaces.push(workspace);
  return { root, workspace };
}

test("hides ignored files and directories from list and search, at any depth", async () => {
  const { workspace } = await ignoring(
    "# build output\nbuild/\n*.log\nsrc/gen\n",
  );
  expect(workspace.warnings).toEqual([]);
  expect(await workspace.list()).toBe("src/\nkeep.txt");
  expect(await workspace.list("src")).toBe("main.ts");
  const found = await workspace.search("NEEDLE");
  expect(found).toContain("keep.txt:1");
  expect(found).toContain("src/main.ts:1");
  expect(found).not.toMatch(/build|debug\.log|trace\.log|gen/u);
  const { entries } = await workspace.listEntries();
  expect(entries.map((entry) => entry.name)).toEqual(["src", "keep.txt"]);
});

test("refuses read, edit, write and a listing of an ignored path, and says why", async () => {
  const { workspace } = await ignoring("build\n*.log\nsrc/gen\n");
  for (const path of [
    "debug.log",
    "src/trace.log",
    "build/out/a.js",
    "src/gen/x.ts",
  ]) {
    await expect(workspace.read(path), path).rejects.toThrow(
      "ignored by .agentignore",
    );
    await expect(
      workspace.prepareEdit(path, "NEEDLE", "x"),
      path,
    ).rejects.toThrow("ignored by .agentignore");
    await expect(workspace.prepareWrite(path, "new"), path).rejects.toThrow(
      "ignored by .agentignore",
    );
  }
  // A path that does not exist yet is covered too: no file is created there.
  await expect(workspace.prepareWrite("build/new.js", "x")).rejects.toThrow(
    "ignored by .agentignore",
  );
  await expect(workspace.list("build")).rejects.toThrow(
    "ignored by .agentignore",
  );
  await expect(workspace.search("NEEDLE", { path: "src/gen" })).rejects.toThrow(
    "ignored by .agentignore",
  );
  // An absolute spelling of the same path is the same path.
  await expect(
    workspace.read(join(workspace.root, "debug.log")),
  ).rejects.toThrow("ignored by .agentignore");
  await expect(workspace.read("src/main.ts")).resolves.toBe("NEEDLE main\n");
});

test("the last matching line decides, so a negation re-includes", async () => {
  const { workspace } = await ignoring("*.log\n!trace.log\n");
  expect(await workspace.read("src/trace.log")).toBe("NEEDLE nested\n");
  await expect(workspace.read("debug.log")).rejects.toThrow("ignored");
  expect(await workspace.list("src")).toContain("trace.log");
  expect(await workspace.list()).not.toContain("debug.log");
});

test("matches case-insensitively, as the file system opens names", async () => {
  const { workspace } = await ignoring("BUILD\n");
  await expect(workspace.read("build/out/a.js")).rejects.toThrow("ignored");
  expect(await workspace.list()).not.toContain("build");
});

test("the policy is read once, when the workspace is opened", async () => {
  const { root, workspace } = await ignoring("build\n");
  await rm(join(root, ".agentignore"));
  await expect(workspace.read("build/out/a.js")).rejects.toThrow("ignored");
  expect(await workspace.list()).not.toContain("build");
});

test("the .agentignore file itself is not readable or writable through the tools", async () => {
  const { workspace } = await ignoring("build\n");
  expect(await workspace.list()).not.toContain(".agentignore");
  await expect(workspace.read(".agentignore")).rejects.toThrow("excluded");
  await expect(workspace.prepareWrite(".agentignore", "")).rejects.toThrow(
    "excluded",
  );
  expect(await workspace.search("build")).not.toContain(".agentignore");
});

test("a malformed line is reported and the rest still applies", async () => {
  const { workspace } = await ignoring("build\nbad[\n!\n*.log\n");
  expect(workspace.warnings).toEqual([
    '.agentignore line 2: "bad[" has an unterminated "["; the line is ignored',
    '.agentignore line 3: "!" names no path; the line is ignored',
  ]);
  await expect(workspace.read("build/out/a.js")).rejects.toThrow("ignored");
  await expect(workspace.read("debug.log")).rejects.toThrow("ignored");
});

test("a symbolic-link or non-regular .agentignore refuses the workspace", async () => {
  const root = await temp("local-agent-agentignore-link-");
  const outside = await temp("local-agent-outside-");
  await writeFile(join(outside, "rules"), "nothing\n", "utf8");
  await symlink(join(outside, "rules"), join(root, ".agentignore"));
  await expect(Workspace.create(root)).rejects.toThrow(
    "refused: .agentignore is a symbolic link",
  );
  await rm(join(root, ".agentignore"));
  await mkdir(join(root, ".agentignore"));
  await expect(Workspace.create(root)).rejects.toThrow(
    "refused: .agentignore is not a plain file",
  );
  await rm(join(root, ".agentignore"), { recursive: true });
  await writeFile(join(root, ".agentignore"), Buffer.from([0xff, 0xfe, 0x41]));
  await expect(Workspace.create(root)).rejects.toThrow("not valid UTF-8");
  await rm(join(root, ".agentignore"));
  const missing = await Workspace.create(root);
  workspaces.push(missing);
  expect(missing.warnings).toEqual([]);
});
