import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { uiDirectory } from "#cli/tuimark/launch";

test("the UI files are found next to the launcher", () => {
  expect(uiDirectory()).toMatch(/tuimark\/ui$/);
});

test("a missing app.tui is reported with the folder that was searched", async () => {
  const empty = await mkdtemp(join(tmpdir(), "local-agent-ui-"));
  try {
    expect(() => uiDirectory(empty)).toThrow(/not found in .*ui$/);
    await mkdir(join(empty, "ui"));
    await writeFile(join(empty, "ui", "app.tui"), "<tui/>");
    expect(uiDirectory(empty)).toBe(join(empty, "ui"));
  } finally {
    await rm(empty, { recursive: true, force: true });
  }
});
