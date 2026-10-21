import { expect, test } from "vitest";
import {
  BUSY_STATUS,
  filterPalette,
  HELP_STATUS,
  paletteCommand,
  runSlash,
} from "#cli/tuimark/commands";

const state = { intent: "build" as const, mcp: { servers: 0, tools: 0 } };

test("/mode reports, validates and changes the mode with the Vue client's words", () => {
  expect(runSlash("/mode", state)).toEqual({
    status: "Current mode: build. Use /mode plan or /mode build",
  });
  expect(runSlash("/mode PLAN", state)).toEqual({
    status: "Mode changed to plan",
    intent: "plan",
  });
  expect(runSlash("  /mode build  ", state).intent).toBe("build");
  expect(runSlash("/mode fast", state)).toEqual({
    status: "Unknown mode: fast. Use /mode plan or /mode build",
  });
});

test("/help opens the overlay and /mcp counts servers and tools", () => {
  expect(runSlash("/help", state)).toEqual({ status: HELP_STATUS, help: true });
  expect(runSlash("/mcp", state).status).toBe(
    "MCP servers: 0 · use --mcp-config to connect trusted servers",
  );
  expect(
    runSlash("/mcp", { ...state, mcp: { servers: 2, tools: 5 } }).status,
  ).toBe("MCP servers: 2 · 5 tools");
});

test("anything else that starts with a slash is an unknown command", () => {
  expect(runSlash("/nope now", state).status).toBe(
    "Unknown command: /nope now. Use /help",
  );
  expect(BUSY_STATUS).toBe("Busy · wait for the current turn to finish");
});

test("the palette filters on every word and maps rows to slash commands", () => {
  expect(filterPalette("").length).toBeGreaterThan(3);
  expect(filterPalette("mode plan").map((r) => r.id)).toEqual(["mode-plan"]);
  expect(filterPalette("MCP").map((r) => r.id)).toEqual(["mcp"]);
  expect(filterPalette("zzz")).toEqual([]);
  expect(paletteCommand("mode-build")).toBe("/mode build");
  expect(paletteCommand("missing")).toBeUndefined();
});
