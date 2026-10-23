import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  availablePrograms,
  hostInstructions,
  projectInstructions,
} from "#runtime/instructions";
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

test("the system prompt states relative paths, the tool list, and the programs found on PATH", () => {
  const prompt = hostInstructions("build", ["bun", "git"]);
  expect(prompt).toContain("relative to the workspace root");
  expect(prompt).toContain("src/main.ts");
  for (const tool of ["read", "list", "search", "edit", "write", "run_command"])
    expect(prompt).toContain(tool);
  expect(prompt).toContain("no shell");
  expect(prompt).toContain("Programs found on PATH: bun, git.");
  expect(hostInstructions("build", [])).not.toContain("Programs found");
});

test("chat intent gets its own conversational preamble, not the coding framing", () => {
  const chat = hostInstructions("chat", ["bun"]);
  expect(chat).toContain("conversational assistant");
  expect(chat).toContain("web_search and fetch");
  expect(chat).toContain("summarize and cite links");
  expect(chat).not.toContain("coding agent");
  expect(chat).not.toContain("Programs found");
  expect(chat).not.toBe(hostInstructions("plan", ["bun"]));
  expect(chat).not.toBe(hostInstructions("build", ["bun"]));
});

test("programs are probed from a small fixed set, once", () => {
  const found = availablePrograms();
  expect(
    found.every((name) =>
      ["bun", "node", "npm", "python3", "go", "cargo", "git"].includes(name),
    ),
  ).toBe(true);
  expect(found).toContain("bun");
  expect(availablePrograms()).toBe(found);
});
