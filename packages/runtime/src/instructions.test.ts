import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { projectInstructions } from "#runtime/instructions";
import { Workspace } from "#runtime/workspace";

test("loads bounded project instructions and refuses unsafe sources", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-instructions-"));
  try {
    using workspace = await Workspace.create(root);
    expect(await projectInstructions(workspace)).toBe("");
    await writeFile(join(root, "AGENTS.md"), "Use the project check command.");
    expect(await projectInstructions(workspace)).toContain(
      "Use the project check command.",
    );
    await writeFile(join(root, "AGENTS.md"), "x".repeat(65 * 1024));
    await expect(projectInstructions(workspace)).rejects.toThrow(
      "instruction limit",
    );
    await rm(join(root, "AGENTS.md"));
    await writeFile(join(root, "other.md"), "untrusted");
    await symlink(join(root, "other.md"), join(root, "AGENTS.md"));
    await expect(projectInstructions(workspace)).rejects.toThrow(
      "symbolic links",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
