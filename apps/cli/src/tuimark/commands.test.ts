import { expect, test } from "vitest";
import {
  BUSY_STATUS,
  filterPalette,
  HELP_STATUS,
  helpRows,
  paletteCommand,
  runSlash,
  SLASH_COMMANDS,
  slashMatches,
  slashRows,
} from "#cli/tuimark/commands";

const state = { intent: "build" as const, mcp: { servers: 0, tools: 0 } };

test("/mode reports, validates and changes the mode with the Vue client's words", () => {
  expect(runSlash("/mode", state)).toEqual({
    status: "Current mode: build. Use /mode plan, /mode build or /mode chat",
  });
  expect(runSlash("/mode PLAN", state)).toEqual({
    status: "Mode changed to plan",
    intent: "plan",
  });
  expect(runSlash("  /mode build  ", state).intent).toBe("build");
  expect(runSlash("/mode chat", state)).toEqual({
    status: "Mode changed to chat",
    intent: "chat",
  });
  expect(runSlash("/mode fast", state)).toEqual({
    status: "Unknown mode: fast. Use /mode plan, /mode build or /mode chat",
    unknown: true,
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
    unknown: true,
  });
  expect(runSlash("/theme blue", state).theme).toBeUndefined();
});

test("/model reports the current model and asks the controller to switch", () => {
  expect(runSlash("/model", state).status).toBe(
    "Current model: unknown. Use /model NAME to switch",
  );
  expect(runSlash("/model", { ...state, model: "llama3.2" }).status).toBe(
    "Current model: llama3.2. Use /model NAME to switch",
  );
  expect(runSlash("/model qwen3", state)).toEqual({
    status: "Switching model to qwen3…",
    model: "qwen3",
  });
  expect(HELP_STATUS).toContain("/model [NAME]");
  expect(paletteCommand("model")).toBe("/model");
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

test("/permissions lists session grants and file rules, and clears them", () => {
  expect(runSlash("/permissions", state)).toEqual({
    status: "No permissions: no session grants, no file rules",
  });
  const granted = { ...state, grants: ["files", "bun test"] };
  expect(runSlash("/permissions", granted)).toEqual({
    status: "Permissions: session: files · bun test",
  });
  const ruled = {
    ...granted,
    rules: ["allow edit:src/*", "deny run_command:rm *"],
  };
  expect(runSlash("/permissions", ruled)).toEqual({
    status:
      "Permissions: session: files · bun test · file: allow edit:src/* · deny run_command:rm *",
  });
  expect(
    runSlash("/permissions", { ...state, rules: ["allow mcp:fs__readFile"] }),
  ).toEqual({ status: "Permissions: file: allow mcp:fs__readFile" });
  expect(runSlash("/permissions CLEAR", granted)).toEqual({
    status: "Session grants cleared",
    clearGrants: true,
  });
  expect(runSlash("/permissions clear", state)).toEqual({
    status: "No session grants",
    clearGrants: true,
  });
  expect(runSlash("/permissions clear rules", ruled)).toEqual({
    status: "File rules cleared",
    clearRules: true,
  });
  expect(runSlash("/permissions clear rules", state)).toEqual({
    status: "No file rules",
    clearRules: true,
  });
  expect(runSlash("/permissions trust", ruled)).toEqual({
    status: "Reviewing file rules…",
    trustRules: "review",
  });
  expect(runSlash("/permissions trust confirm", ruled)).toEqual({
    status: "Trusting the reviewed rules…",
    trustRules: "confirm",
  });
  expect(runSlash("/permissions maybe", granted)).toEqual({
    status:
      "Unknown permissions setting: maybe. Use /permissions, /permissions clear, /permissions clear rules, /permissions trust or /permissions trust confirm",
    unknown: true,
  });
  expect(filterPalette("permissions").map((r) => r.id)).toEqual([
    "permissions",
    "permissions-clear",
    "permissions-trust",
  ]);
  expect(paletteCommand("permissions-trust")).toBe("/permissions trust");
  expect(paletteCommand("permissions")).toBe("/permissions");
  expect(paletteCommand("permissions-clear")).toBe("/permissions clear");
  expect(HELP_STATUS).toContain("/permissions [clear|trust]");
});

test("/details asks the controller for the viewer over the latest receipt", () => {
  expect(runSlash("/details", state)).toEqual({
    status: "Latest tool output",
    details: true,
  });
  expect(filterPalette("details").map((r) => r.id)).toEqual(["details"]);
  expect(filterPalette("details")[0]?.label).toBe("Details: last tool output");
  expect(paletteCommand("details")).toBe("/details");
  expect(HELP_STATUS).toContain("/details");
  expect(slashMatches("/d")?.map((c) => c.name)).toEqual(["/details"]);
});

test("the slash catalog lists exactly the commands the parser knows", () => {
  expect(SLASH_COMMANDS.map((c) => c.name)).toEqual([
    "/mode",
    "/model",
    "/theme",
    "/mouse",
    "/copy",
    "/permissions",
    "/sandbox",
    "/details",
    "/status",
    "/provider",
    "/sessions",
    "/recover",
    "/find",
    "/export",
    "/mcp",
    "/goal",
    "/help",
  ]);
  for (const command of SLASH_COMMANDS) {
    expect(runSlash(command.name, state).status).not.toContain(
      "Unknown command",
    );
    expect(command.insert.startsWith(command.name)).toBe(true);
    expect(command.description.length).toBeGreaterThan(0);
  }
  expect(slashMatches("/")?.map((c) => c.name)).toEqual(
    SLASH_COMMANDS.map((c) => c.name),
  );
  expect(slashMatches("/m")?.map((c) => c.name)).toEqual([
    "/mode",
    "/model",
    "/mouse",
    "/mcp",
  ]);
  expect(slashMatches("/MO")?.map((c) => c.name)).toEqual([
    "/mode",
    "/model",
    "/mouse",
  ]);
  expect(slashMatches("/zzz")).toEqual([]);
  // Not half-typed commands: an argument started, a pasted block, plain text.
  expect(slashMatches("/mode ")).toBeUndefined();
  expect(slashMatches("/mode plan")).toBeUndefined();
  expect(slashMatches("/a\nb")).toBeUndefined();
  expect(slashMatches("hello")).toBeUndefined();
  expect(slashMatches("")).toBeUndefined();
  const rows = slashRows(slashMatches("/m") ?? [], 1);
  expect(rows.map((r) => [r.cmd, r.sel, r.mark])).toEqual([
    ["/mode", false, ""],
    ["/model", true, "›"],
    ["/mouse", false, ""],
    ["/mcp", false, ""],
  ]);
  expect(helpRows().map((r) => r.cmd)).toEqual([
    "/mode plan|build|chat",
    "/model [NAME]",
    "/theme dark|light|auto",
    "/mouse on|off",
    "/copy [code]",
    "/permissions [clear|trust]",
    "/sandbox on|off",
    "/details",
    "/status",
    "/provider",
    "/sessions [new]",
    "/recover [N outcome]",
    "/find TEXT",
    "/export [json]",
    "/mcp",
    "/goal [OBJECTIVE|stop]",
  ]);
  expect(slashMatches("/p")?.map((c) => c.name)).toEqual([
    "/permissions",
    "/provider",
  ]);
  expect(slashMatches("/f")?.map((c) => c.name)).toEqual(["/find"]);
});

test("/status, /sessions and /export ask the controller for their panels and file", () => {
  expect(runSlash("/status", state)).toEqual({
    status: "Status",
    statusPanel: true,
  });
  expect(runSlash("/sessions", state)).toEqual({
    status: "Sessions",
    sessions: true,
  });
  expect(runSlash("/provider", state)).toEqual({
    status: "Checking the provider…",
    providerPanel: true,
  });
  expect(paletteCommand("provider")).toBe("/provider");
  expect(runSlash("/sessions new", state)).toEqual({
    status: "Starting a new session…",
    newSession: true,
  });
  expect(runSlash("/sessions NEW", state).newSession).toBe(true);
  expect(runSlash("/sessions old", state)).toMatchObject({
    unknown: true,
    status: "Unknown sessions argument: old. Use /sessions or /sessions new",
  });
  expect(paletteCommand("sessions-new")).toBe("/sessions new");
  expect(runSlash("/export", state)).toEqual({
    status: "Exporting transcript…",
    exportTranscript: true,
    exportFormat: "markdown",
  });
  // /mcp keeps its count line and now opens the panel over it.
  expect(runSlash("/mcp", state).mcpPanel).toBe(true);
  expect(HELP_STATUS).toContain("/status");
  expect(HELP_STATUS).toContain("/sessions");
  expect(HELP_STATUS).toContain("/export");
  expect(slashMatches("/s")?.map((c) => c.name)).toEqual([
    "/sandbox",
    "/status",
    "/sessions",
  ]);
  expect(slashMatches("/e")?.map((c) => c.name)).toEqual(["/export"]);
  expect(filterPalette("status").map((r) => r.id)).toEqual(["status"]);
  expect(paletteCommand("status")).toBe("/status");
  expect(paletteCommand("sessions")).toBe("/sessions");
  expect(paletteCommand("export")).toBe("/export");
});

test("/export json and /find parse their arguments", () => {
  expect(runSlash("/export json", state)).toEqual({
    status: "Exporting transcript…",
    exportTranscript: true,
    exportFormat: "json",
  });
  expect(runSlash("/export yaml", state).status).toBe(
    "Unknown export format: yaml. Use /export or /export json",
  );
  expect(runSlash("/export yaml", state).exportTranscript).toBeUndefined();
  expect(runSlash("/find needle", state)).toEqual({
    status: "Searching sessions for needle…",
    find: "needle",
  });
  expect(runSlash("/find two words", state).find).toBe("two words");
  expect(runSlash("/find", state).status).toBe(
    "Search the saved sessions. Use /find TEXT",
  );
  expect(runSlash("/find", state).find).toBeUndefined();
  expect(paletteCommand("export-json")).toBe("/export json");
  expect(paletteCommand("find")).toBe("/find");
});

test("the palette's key rows: quit everywhere, load-earlier fullscreen only", () => {
  const all = filterPalette("").map((r) => r.id);
  expect(all).toContain("quit");
  expect(all).toContain("load-earlier");
  const inline = filterPalette("", true).map((r) => r.id);
  expect(inline).toContain("quit");
  expect(inline).not.toContain("load-earlier");
  expect(filterPalette("earlier", true)).toEqual([]);
  // The key rows stand for no slash command.
  expect(paletteCommand("quit")).toBeUndefined();
  expect(paletteCommand("load-earlier")).toBeUndefined();
});

test("/recover lists, or records a conclusion for one call; anything else is refused", () => {
  expect(runSlash("/recover", state)).toEqual({
    status: "Recovery",
    recover: "list",
  });
  expect(
    runSlash("/RECOVER 2 Not-Applied  checked git status ", state),
  ).toEqual({
    status: "Recording what you found…",
    recover: {
      index: 2,
      conclusion: "not-applied",
      note: "checked git status",
    },
  });
  expect(runSlash("/recover 1 undone", state).recover).toEqual({
    index: 1,
    conclusion: "undone",
    note: "",
  });
  for (const bad of [
    "/recover 0 applied",
    "/recover x applied",
    "/recover 1",
    "/recover 1 maybe",
    "/recover 1.5 applied",
    "/recover 0x1 applied",
    "/recover 1e0 applied",
    "/recover 01 applied",
  ])
    expect(runSlash(bad, state), bad).toEqual({
      status:
        "Use /recover to list, or /recover N applied|not-applied|undone [note]",
      unknown: true,
    });
});

test("/copy and /copy code ask the controller for the last reply or its last code block", () => {
  expect(runSlash("/copy", state)).toEqual({
    status: "Copying the last reply…",
    copy: "reply",
  });
  expect(runSlash("/COPY", state).copy).toBe("reply");
  expect(runSlash("/copy code", state)).toEqual({
    status: "Copying the last code block…",
    copy: "code",
  });
  expect(runSlash("/copy Code", state).copy).toBe("code");
  const bad = runSlash("/copy everything", state);
  expect(bad).toEqual({
    status: "Unknown copy argument: everything. Use /copy or /copy code",
    unknown: true,
  });
  expect(HELP_STATUS).toContain("/copy [code]");
  expect(paletteCommand("copy")).toBe("/copy");
  expect(paletteCommand("copy-code")).toBe("/copy code");
  expect(filterPalette("copy").map((row) => row.id)).toEqual([
    "copy",
    "copy-code",
  ]);
  expect(slashMatches("/co")?.map((c) => c.name)).toEqual(["/copy"]);
});
