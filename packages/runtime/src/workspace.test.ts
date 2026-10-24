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
  await expect(workspace.read(".")).rejects.toThrow("must name a file");
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

test("accepts an absolute path under the root and a leading ./, normalized to the relative form", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "a.ts"), "const a = 1;\n");
  expect(await workspace.read(join(root, "src", "a.ts"))).toBe(
    "const a = 1;\n",
  );
  expect(await workspace.read("./src/a.ts")).toBe("const a = 1;\n");
  expect(await workspace.read("src//a.ts")).toBe("const a = 1;\n");
  const proposal = await workspace.prepareEdit(
    join(root, "src", "a.ts"),
    "1",
    "2",
  );
  expect(proposal.path).toBe("src/a.ts");
  expect(proposal.diff).toContain("a/src/a.ts");
  expect(workspace.normalize(root)).toBe("");
  expect(workspace.normalize("./")).toBe("");
  expect(workspace.normalize("src/")).toBe("src");
});

test("rejects any other absolute path and suggests the relative one when it is obvious", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "a.ts"), "x");
  await expect(workspace.read("/src/a.ts")).rejects.toThrow('Use "src/a.ts"');
  await expect(workspace.read("/workspace/src/a.ts")).rejects.toThrow(
    'Use "src/a.ts"',
  );
  // Not there yet, but the relative form is still the obvious meaning.
  await expect(workspace.read("/workspace/new/b.ts")).rejects.toThrow(
    'Use "new/b.ts"',
  );
  const outside = await workspace.read("/etc/passwd").catch((e: Error) => e);
  expect((outside as Error).message).toContain("is outside it");
  expect((outside as Error).message).not.toContain("Use ");
  // Nothing was read from the absolute path itself.
  await expect(workspace.read(`${root}-sibling/x`)).rejects.toThrow("relative");
});

test("path escapes stay forbidden, however they are written", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  for (const path of [
    "..",
    "../notes.md",
    "src/../../notes.md",
    "src/../notes.md",
    `${root}/../notes.md`,
    `${root}/src/../notes.md`,
    `${root}/..`,
    "/..",
    "/workspace/../etc/passwd",
  ]) {
    await expect(workspace.read(path), path).rejects.toThrow();
    await expect(workspace.list(path), path).rejects.toThrow();
    await expect(workspace.search("x", { path }), path).rejects.toThrow();
    await expect(workspace.prepareWrite(path, "x"), path).rejects.toThrow();
  }
});

test("a symbolic link out of the workspace is not followed by read, list, search, or write", async () => {
  const { root, workspace } = await fixture();
  const outside = await temp("local-agent-outside-");
  await writeFile(join(outside, "secret.txt"), "TOPSECRET");
  await symlink(outside, join(root, "linked-dir"));
  await symlink(join(outside, "secret.txt"), join(root, "link.txt"));
  await expect(workspace.read("linked-dir/secret.txt")).rejects.toThrow();
  await expect(workspace.list("linked-dir")).rejects.toThrow();
  await expect(
    workspace.search("TOPSECRET", { path: "linked-dir" }),
  ).rejects.toThrow();
  await expect(
    workspace
      .prepareWrite("linked-dir/new.txt", "x")
      .then((p) => workspace.applyWrite(p)),
  ).rejects.toThrow();
  expect(await Bun.file(join(outside, "new.txt")).exists()).toBe(false);
  // Searching the whole workspace never reads through the links.
  expect(await workspace.search("TOPSECRET")).toBe("No matches");
  const listing = await workspace.list();
  expect(listing).toContain("link.txt@");
  expect(listing).toContain("linked-dir@");
});

test("list marks directories, puts them first, skips .git, node_modules and secrets, and caps the output", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  await mkdir(join(root, ".git"));
  await mkdir(join(root, "node_modules"));
  await writeFile(join(root, ".env"), "TOKEN=x");
  await writeFile(join(root, "src", "b.ts"), "");
  expect(await workspace.list()).toBe("src/\nnotes.md");
  expect(await workspace.list(".")).toBe("src/\nnotes.md");
  expect(await workspace.list("src/")).toBe("b.ts");
  expect(await workspace.list(root)).toBe("src/\nnotes.md");
  await mkdir(join(root, "empty"));
  expect(await workspace.list("empty")).toBe("(empty directory)");
  await expect(workspace.list("notes.md")).rejects.toThrow("not a directory");
  await expect(workspace.list(".git")).rejects.toThrow("excluded");
  await mkdir(join(root, "many"));
  for (let i = 0; i < 520; i++)
    await writeFile(
      join(root, "many", `f${String(i).padStart(3, "0")}.txt`),
      "",
    );
  const lines = (await workspace.list("many")).split("\n");
  expect(lines).toHaveLength(501);
  expect(lines[0]).toBe("f000.txt");
  expect(lines[500]).toBe("… 20 more entries not shown");
});

test("search finds literal and regular-expression matches as path:line: text, with bounds", async () => {
  const { root, workspace } = await fixture();
  await mkdir(join(root, "src"));
  await mkdir(join(root, "node_modules"));
  await mkdir(join(root, ".git"));
  await writeFile(
    join(root, "src", "text.ts"),
    "export function slugify(s) {\n  return s;\n}\n",
  );
  await writeFile(
    join(root, "src", "use.ts"),
    "import { slugify } from './text';\nSLUGIFY();\n",
  );
  await writeFile(join(root, "node_modules", "dep.js"), "slugify");
  await writeFile(join(root, ".git", "config"), "slugify");
  await writeFile(join(root, ".env"), "slugify=secret");
  await writeFile(
    join(root, "image.bin"),
    Buffer.from([0x73, 0x6c, 0x75, 0x67, 0x69, 0x66, 0x79, 0x00, 0xff]),
  );
  expect(await workspace.search("slugify")).toBe(
    "src/text.ts:1: export function slugify(s) {\nsrc/use.ts:1: import { slugify } from './text';",
  );
  expect(await workspace.search("slugify", { path: "src/text.ts" })).toBe(
    "src/text.ts:1: export function slugify(s) {",
  );
  expect(
    await workspace.search("slugify", { ignoreCase: true, path: "src" }),
  ).toContain("src/use.ts:2: SLUGIFY();");
  expect(await workspace.search("^export \\w+ slugify", { regex: true })).toBe(
    "src/text.ts:1: export function slugify(s) {",
  );
  expect(await workspace.search("nothing here")).toBe("No matches");
  expect(
    await workspace.search("slugify\\|SLUGIFY", {
      regex: true,
      path: "src/use.ts",
    }),
  ).toContain("src/use.ts:2: SLUGIFY();");
  await expect(workspace.search("(", { regex: true })).rejects.toThrow(
    "Invalid regular expression",
  );
  await expect(workspace.search("")).rejects.toThrow("empty");
  // A literal pattern is not a regular expression.
  expect(await workspace.search("slugify(s)")).toContain("src/text.ts:1");
  expect(await workspace.search("s.ugify")).toBe("No matches");
});

test("search caps matches and long lines", async () => {
  const { root, workspace } = await fixture();
  await writeFile(
    join(root, "big.txt"),
    `${"hit\n".repeat(150)}${"x".repeat(500)}hit\n`,
  );
  await writeFile(join(root, "wide.txt"), `hit ${"y".repeat(500)}\n`);
  const lines = (await workspace.search("hit", { path: "big.txt" })).split(
    "\n",
  );
  expect(lines).toHaveLength(101);
  expect(lines[0]).toBe("big.txt:1: hit");
  expect(lines[100]).toContain("more matches not shown");
  const wide = await workspace.search("hit", { path: "wide.txt" });
  expect(wide.length).toBeLessThan(260);
  expect(wide.endsWith("…")).toBe(true);
});

test("write creates a file (and its directories) after approval, with a diff of added lines", async () => {
  const { root, workspace } = await fixture();
  const proposal = await workspace.prepareWrite(
    "tests/deep/new.test.ts",
    "a\nb\n",
  );
  expect(proposal).toMatchObject({
    kind: "write",
    path: "tests/deep/new.test.ts",
    created: true,
    before: "",
    after: "a\nb\n",
  });
  expect(proposal.diff).toContain("--- /dev/null");
  expect(proposal.diff).toContain("+a\n+b");
  expect(Object.isFrozen(proposal)).toBe(true);
  // Nothing exists before the approval is applied.
  expect(
    await Bun.file(join(root, "tests", "deep", "new.test.ts")).exists(),
  ).toBe(false);
  await workspace.applyWrite(proposal);
  expect(
    await readFile(join(root, "tests", "deep", "new.test.ts"), "utf8"),
  ).toBe("a\nb\n");
  expect(
    (await Bun.file(join(root, "tests", "deep", "new.test.ts")).stat()).mode &
      0o777,
  ).toBe(0o644);
  await expect(workspace.applyWrite(proposal)).rejects.toThrow(
    "issued by this workspace",
  );
});

test("write replaces an existing file with a diff, and refuses stale, racing, excluded, or identical writes", async () => {
  const { root, workspace } = await fixture();
  const proposal = await workspace.prepareWrite("notes.md", "after\nkeep\n");
  expect(proposal.created).toBe(false);
  expect(proposal.diff).toContain("-before");
  expect(proposal.diff).toContain("+after");
  await writeFile(join(root, "notes.md"), "changed");
  await expect(workspace.applyWrite(proposal)).rejects.toThrow("changed since");
  expect(await readFile(join(root, "notes.md"), "utf8")).toBe("changed");
  await expect(workspace.prepareWrite("notes.md", "changed")).rejects.toThrow(
    "would not change",
  );
  const created = await workspace.prepareWrite("fresh.txt", "x");
  await writeFile(join(root, "fresh.txt"), "someone else");
  await expect(workspace.applyWrite(created)).rejects.toThrow("created since");
  expect(await readFile(join(root, "fresh.txt"), "utf8")).toBe("someone else");
  await expect(workspace.prepareWrite(".env", "TOKEN=1")).rejects.toThrow(
    "excluded",
  );
  await expect(workspace.prepareWrite("a.bin", "x\0y")).rejects.toThrow(
    "Binary",
  );
  await expect(workspace.prepareWrite(".", "x")).rejects.toThrow(
    "must name a file",
  );
  const edit = await workspace.prepareEdit("notes.md", "changed", "again");
  await expect(workspace.applyWrite(edit as never)).rejects.toThrow(
    "issued by this workspace",
  );
});

test("the host's permission rules and the user's local-agent config are out of reach", async () => {
  const { root, workspace } = await fixture();
  const rules = '{"version":1,"rules":[]}';
  await mkdir(join(root, ".local-agent"));
  await writeFile(join(root, ".local-agent", "permissions.json"), rules);
  await mkdir(join(root, "pkg", ".local-agent"), { recursive: true });
  await mkdir(join(root, ".config", "local-agent"), { recursive: true });
  await writeFile(
    join(root, ".config", "local-agent", "trusted-rules.json"),
    "trusted needle",
  );
  await writeFile(join(root, ".config", "other.json"), "other needle");
  for (const path of [
    ".local-agent/permissions.json",
    "pkg/.local-agent/permissions.json",
    ".config/local-agent/trusted-rules.json",
    ".CONFIG/Local-Agent/trusted-rules.json",
  ]) {
    await expect(workspace.read(path)).rejects.toThrow("excluded");
    await expect(
      workspace.prepareWrite(path, '{"version":1,"rules":[]}\n'),
    ).rejects.toThrow("excluded");
  }
  await expect(
    workspace.prepareEdit(".local-agent/permissions.json", "[]", "[1]"),
  ).rejects.toThrow("excluded");
  await expect(workspace.list(".local-agent")).rejects.toThrow("excluded");
  await expect(workspace.list(".config/local-agent")).rejects.toThrow(
    "excluded",
  );
  expect(await workspace.list()).not.toContain(".local-agent");
  expect(await workspace.list(".config")).toBe("other.json");
  // A search from the root never walks into the trust store.
  const found = await workspace.search("needle");
  expect(found).toContain(".config/other.json");
  expect(found).not.toContain("trusted-rules");
  // Only the pair is excluded: a project directory named local-agent is fine.
  await mkdir(join(root, "src", "local-agent"), { recursive: true });
  await writeFile(join(root, "src", "local-agent", "a.ts"), "x");
  expect(await workspace.read("src/local-agent/a.ts")).toBe("x");
});

test("the config check is not fooled by `..` names or Unicode forms", async () => {
  for (const dir of ["..cfg", "caf\u00e9"]) {
    const root = await temp("local-agent-xdg-names-");
    const saved = process.env.XDG_CONFIG_HOME;
    process.env.XDG_CONFIG_HOME = join(root, dir);
    let workspace: Workspace;
    try {
      workspace = await Workspace.create(root);
      workspaces.push(workspace);
    } finally {
      process.env.XDG_CONFIG_HOME = saved;
    }
    await mkdir(join(root, dir, "local-agent"), { recursive: true });
    await writeFile(join(root, dir, "local-agent", "trusted-rules.json"), "{}");
    for (const spelling of [dir, dir.normalize("NFD")])
      await expect(
        workspace.read(`${spelling}/local-agent/trusted-rules.json`),
      ).rejects.toThrow("excluded");
  }
});

test("an XDG_CONFIG_HOME inside the workspace is out of reach too", async () => {
  const root = await temp("local-agent-xdg-");
  const saved = process.env.XDG_CONFIG_HOME;
  // The directory need not exist when the workspace opens.
  process.env.XDG_CONFIG_HOME = join(root, "dot", "cfg");
  let workspace: Workspace;
  try {
    workspace = await Workspace.create(root);
    workspaces.push(workspace);
  } finally {
    process.env.XDG_CONFIG_HOME = saved;
  }
  await mkdir(join(root, "dot", "cfg", "local-agent"), { recursive: true });
  await writeFile(
    join(root, "dot", "cfg", "local-agent", "trusted-rules.json"),
    "trusted needle",
  );
  await writeFile(join(root, "dot", "cfg", "git.conf"), "other needle");
  for (const path of [
    "dot/cfg/local-agent/trusted-rules.json",
    "DOT/cfg/Local-Agent/trusted-rules.json",
  ]) {
    await expect(workspace.read(path)).rejects.toThrow("excluded");
    await expect(workspace.prepareWrite(path, "{}\n")).rejects.toThrow(
      "excluded",
    );
  }
  await expect(workspace.list("dot/cfg/local-agent")).rejects.toThrow(
    "excluded",
  );
  expect(await workspace.list("dot/cfg")).toBe("git.conf");
  const found = await workspace.search("needle");
  expect(found).toContain("dot/cfg/git.conf");
  expect(found).not.toContain("trusted-rules");
});
