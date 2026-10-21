import { describe, expect, test } from "vitest";
import { parseSlashCommand } from "#desktop/renderer/slash";

describe("parseSlashCommand", () => {
  test("parses /mode with and without an argument", () => {
    expect(parseSlashCommand("/mode")).toEqual({
      kind: "mode",
      requested: undefined,
    });
    expect(parseSlashCommand("/mode plan")).toEqual({
      kind: "mode",
      requested: "plan",
    });
    expect(parseSlashCommand("/MODE BUILD")).toEqual({
      kind: "mode",
      requested: "build",
    });
    expect(parseSlashCommand("  /mode yolo  ")).toEqual({
      kind: "mode",
      requested: "yolo",
    });
  });

  test("parses /help and /mcp", () => {
    expect(parseSlashCommand("/help")).toEqual({ kind: "help" });
    expect(parseSlashCommand("/mcp")).toEqual({ kind: "mcp" });
  });

  test("flags unknown slash commands and passes plain text through", () => {
    expect(parseSlashCommand("/theme")).toEqual({
      kind: "unknown",
      command: "/theme",
    });
    expect(parseSlashCommand("/mode plan extra")).toEqual({
      kind: "unknown",
      command: "/mode plan extra",
    });
    expect(parseSlashCommand("hello")).toEqual({ kind: "not-slash" });
    expect(parseSlashCommand("")).toEqual({ kind: "not-slash" });
  });
});
