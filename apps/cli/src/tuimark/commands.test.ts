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

test("/theme reports, validates and changes the session theme", () => {
  expect(runSlash("/theme", state)).toEqual({
    status: "Current theme: auto. Use /theme dark, /theme light or /theme auto",
  });
  expect(runSlash("/theme", { ...state, theme: "light" }).status).toBe(
    "Current theme: light. Use /theme dark, /theme light or /theme auto",
  );
  expect(runSlash("/theme DARK", state)).toEqual({
    status: "Theme changed to dark",
    theme: "dark",
  });
  expect(runSlash("/theme blue", state)).toEqual({
    status: "Unknown theme: blue. Use /theme dark, /theme light or /theme auto",
  });
  expect(runSlash("/theme blue", state).theme).toBeUndefined();
});

test("/mouse reports, validates and changes the toggle (off by default)", () => {
  expect(runSlash("/mouse", state).status).toBe(
    "Mouse is off. Use /mouse on or /mouse off",
  );
  expect(runSlash("/mouse", { ...state, mouse: true }).status).toBe(
    "Mouse is on. Use /mouse on or /mouse off",
  );
  expect(runSlash("/mouse on", state)).toEqual({
    status: "Mouse changed to on",
    mouse: true,
  });
  expect(runSlash("/mouse off", { ...state, mouse: true })).toEqual({
    status: "Mouse changed to off",
    mouse: false,
  });
  expect(runSlash("/mouse maybe", state).mouse).toBeUndefined();
  expect(runSlash("/mouse maybe", state).status).toBe(
    "Unknown mouse setting: maybe. Use /mouse on or /mouse off",
  );
});

test("the palette carries the theme and mouse rows", () => {
  expect(filterPalette("theme").map((r) => r.id)).toEqual([
    "theme-dark",
    "theme-light",
    "theme-auto",
  ]);
  expect(filterPalette("mouse on").map((r) => r.id)).toEqual(["mouse-on"]);
  expect(paletteCommand("theme-light")).toBe("/theme light");
  expect(paletteCommand("mouse-off")).toBe("/mouse off");
  expect(HELP_STATUS).toContain("/theme dark|light|auto");
  expect(HELP_STATUS).toContain("/mouse on|off");
});
