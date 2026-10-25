import { expect, test } from "vitest";
import {
  BUSY_STATUS,
  filterPalette,
  HELP_STATUS,
  helpRows,
  PALETTE,
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
  // Words match whole: extra spaces between them change nothing.
  expect(runSlash("/permissions clear   rules", ruled)).toEqual(
    runSlash("/permissions clear rules", ruled),
  );
  expect(runSlash("/permissions  trust \t confirm", ruled).trustRules).toBe(
    "confirm",
  );
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

// Every palette row that stands for a slash command runs it through the parser
// and gets the outcome its label promises, so renaming a subcommand in runSlash
// cannot leave a row that answers "Unknown command".
test("each palette row's hint runs the command its label names", () => {
  const effect: Record<string, object> = {
    "mode-plan": { intent: "plan" },
    "mode-build": { intent: "build" },
    "mode-chat": { intent: "chat" },
    "theme-dark": { theme: "dark" },
    "theme-light": { theme: "light" },
    "theme-auto": { theme: "auto" },
    "mouse-on": { mouse: true },
    "mouse-off": { mouse: false },
    copy: { copy: "reply" },
    "copy-code": { copy: "code" },
    permissions: { status: "No permissions: no session grants, no file rules" },
    "permissions-clear": { clearGrants: true },
    "permissions-trust": { trustRules: "review" },
    details: { details: true },
    compact: { compact: true },
    diff: { diffPanel: true },
    status: { statusPanel: true },
    provider: { providerPanel: true },
    model: { status: "Current model: unknown. Use /model NAME to switch" },
    find: { status: "Search the saved sessions. Use /find TEXT" },
    search: { search: "" },
    sessions: { sessions: true },
    "sessions-new": { newSession: true },
    export: { exportTranscript: true, exportFormat: "markdown" },
    "export-json": { exportTranscript: true, exportFormat: "json" },
    mcp: { mcpPanel: true },
    goal: { goal: { kind: "show" } },
    "goal-stop": { goal: { kind: "stop" } },
    help: { help: true },
  };
  const commands = PALETTE.filter((row) => row.hint.startsWith("/"));
  // A new command row must say here what it does.
  expect(commands.map((row) => row.id).sort()).toEqual(
    Object.keys(effect).sort(),
  );
  for (const row of commands) {
    const outcome = runSlash(row.hint, state);
    expect(outcome.unknown, row.id).toBeUndefined();
    expect(outcome.status, row.id).not.toContain("Unknown");
    expect(outcome, row.id).toMatchObject(effect[row.id] as object);
  }
});

test("/details asks the controller for the viewer over the latest receipt", () => {
  expect(runSlash("/details", state)).toEqual({
    status: "Latest tool output",
    details: true,
  });
  // A card's pointer names its number: /details N asks for that one.
  expect(runSlash("/details 3", state)).toEqual({
    status: "Tool output 3",
    details: 3,
  });
  expect(runSlash("/DETAILS  12 ", state).details).toBe(12);
  // Cards count from 1; anything else is not a number one names.
  for (const wanted of ["0", "03", "3x", "-1", "1234567890"])
    expect(runSlash(`/details ${wanted}`, state)).toEqual({
      status: `/details N takes a number from 1: ${wanted} is not one`,
      unknown: true,
    });
  expect(runSlash("/details 1 2", state).unknown).toBe(true);
  expect(filterPalette("details").map((r) => r.id)).toEqual(["details"]);
  expect(filterPalette("details")[0]?.label).toBe("Details: last tool output");
  expect(paletteCommand("details")).toBe("/details");
  expect(HELP_STATUS).toContain("/details [N]");
  // /details stays the first match of "/d": completing it does what it did.
  expect(slashMatches("/d")?.map((c) => c.name)).toEqual(["/details", "/diff"]);
});

test("/new and /clear are /sessions new: the conversation starts over", () => {
  for (const command of ["/new", "/clear", "/NEW", "/Clear"])
    expect(runSlash(command, state), command).toEqual({
      status: "Starting a new session…",
      newSession: true,
    });
  // No argument: a stray one is a typo to fix, not a silent restart.
  for (const command of ["/new now", "/clear all"])
    expect(runSlash(command, state).unknown, command).toBe(true);
  expect(HELP_STATUS).toContain("/new");
  expect(HELP_STATUS).toContain("/clear");
  expect(helpRows().map((r) => r.cmd)).toEqual(
    expect.arrayContaining(["/new", "/clear"]),
  );
  // Neither takes a first-match slot another command had.
  expect(slashMatches("/n")?.map((c) => c.name)).toEqual(["/new"]);
  expect(slashMatches("/cl")?.map((c) => c.name)).toEqual(["/clear"]);
  expect(slashMatches("/c")?.map((c) => c.name)).toEqual([
    "/copy",
    "/compact",
    "/clear",
  ]);
  // The palette's row for it is found by "new" too.
  expect(filterPalette("new").map((r) => r.id)).toContain("sessions-new");
});

test("/compact asks the controller to shorten the conversation now", () => {
  expect(runSlash("/compact", state)).toEqual({
    status: "Compacting the conversation…",
    compact: true,
  });
  expect(runSlash("/COMPACT", state).compact).toBe(true);
  // No argument: a stray one is a typo to fix, not a silent /compact.
  expect(runSlash("/compact now", state).unknown).toBe(true);
  expect(paletteCommand("compact")).toBe("/compact");
  expect(filterPalette("compact").map((r) => r.id)).toEqual(["compact"]);
  expect(HELP_STATUS).toContain("/compact");
  expect(helpRows().map((r) => r.cmd)).toContain("/compact");
});

test("/diff asks the controller for the panel over what the session changed", () => {
  expect(runSlash("/diff", state)).toEqual({
    status: "Changes",
    diffPanel: true,
  });
  expect(runSlash("/DIFF", state).diffPanel).toBe(true);
  // It takes no argument: a stray one is a typo to fix, not a silent /diff.
  expect(runSlash("/diff src/a.ts", state).unknown).toBe(true);
  expect(paletteCommand("diff")).toBe("/diff");
  expect(filterPalette("diff").map((r) => r.id)).toEqual(["diff"]);
  expect(HELP_STATUS).toContain("/diff");
  expect(helpRows().map((r) => r.cmd)).toContain("/diff");
  expect(slashMatches("/di")?.map((c) => c.name)).toEqual(["/diff"]);
});

test("the slash catalog lists exactly the commands the parser knows", () => {
  expect(SLASH_COMMANDS.map((c) => c.name)).toEqual([
    "/mode",
    "/model",
    "/theme",
    "/mouse",
    "/copy",
    "/compact",
    "/clear",
    "/permissions",
    "/sandbox",
    "/details",
    "/diff",
    "/status",
    "/provider",
    "/sessions",
    "/new",
    "/recover",
    "/find",
    "/search",
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
    "/compact",
    "/clear",
    "/permissions [clear|trust]",
    "/sandbox on|off",
    "/details [N]",
    "/diff",
    "/status",
    "/provider",
    "/sessions [new]",
    "/new",
    "/recover [N outcome]",
    "/find TEXT",
    "/search [TEXT]",
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
    "/search",
  ]);
  expect(slashMatches("/e")?.map((c) => c.name)).toEqual(["/export"]);
  expect(filterPalette("status").map((r) => r.id)).toEqual(["status"]);
  expect(paletteCommand("status")).toBe("/status");
  expect(paletteCommand("sessions")).toBe("/sessions");
  expect(paletteCommand("export")).toBe("/export");
});

test("/search asks the controller to search this conversation, with its text or without", () => {
  // No text: the box opens empty (fullscreen), or inline says what it takes.
  expect(runSlash("/search", state)).toEqual({
    status: "Search this conversation",
    search: "",
  });
  // The text is whole and as typed, trimmed at its ends; any case of the name.
  expect(runSlash("/search  retry   loop ", state)).toEqual({
    status: "Searching for retry   loop…",
    search: "retry   loop",
  });
  expect(runSlash("/SEARCH Foo", state).search).toBe("Foo");
  // It is a command of its own, not /find (the saved sessions).
  expect(runSlash("/search x", state).find).toBeUndefined();
  expect(runSlash("/find x", state).search).toBeUndefined();
  expect(runSlash("/searching", state).unknown).toBe(true);
  expect(HELP_STATUS).toContain("/search TEXT");
  expect(paletteCommand("search")).toBe("/search");
  expect(filterPalette("search").map((r) => r.id)).toEqual(["search"]);
  expect(slashMatches("/sea")?.map((c) => c.name)).toEqual(["/search"]);
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
  // /copy stays the first match of "/co": completing it does what it did.
  expect(slashMatches("/co")?.map((c) => c.name)).toEqual([
    "/copy",
    "/compact",
  ]);
});
