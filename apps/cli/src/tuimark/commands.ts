// Slash commands and the command palette. Pure: no I/O, no Tuimark.
//
// The /mode, /mcp and /help strings are the Vue client's
// (apps/cli/src/terminal.ts) word for word, so the two clients can be
// compared. /theme and /mouse are Tuimark-only (the Vue client says so):
// they drive the host's reserved `@theme` path and `<tui mouse>` flag.

export type Intent = "plan" | "build";

/** The session theme, written to the host's reserved `@theme` path. */
export type Theme = "dark" | "light" | "auto";

export interface CommandState {
  intent: Intent;
  /** Connected MCP servers and the tools they expose. */
  mcp: { servers: number; tools: number };
  /** The session theme (`/theme` reports and changes it). Default "auto". */
  theme?: Theme;
  /** Whether fullscreen mouse reporting is on. Default false (off). */
  mouse?: boolean;
}

export interface CommandOutcome {
  status: string;
  /** Set when the command changed the mode. */
  intent?: Intent;
  /** Set when the command asks for the help overlay. */
  help?: boolean;
  /** Set when the command changed the theme: write it to `@theme`. */
  theme?: Theme;
  /** Set when the command changed the mouse toggle: write it to `mouse_on`. */
  mouse?: boolean;
}

export const HELP_STATUS =
  "/mode plan|build · /theme dark|light|auto · /mouse on|off · /mcp · /help · Esc cancel · Ctrl+C exit";

/** Inline has no mouse (the engine ignores it there): the controller refuses with this. */
export const MOUSE_INLINE_STATUS = "Mouse is not available in inline mode";

/** The text a prompt that starts with "/" resolves to. Never calls the model. */
export function runSlash(text: string, state: CommandState): CommandOutcome {
  const command = text.trim();
  const mode = command.match(/^\/mode(?:\s+(\S+))?\s*$/i);
  if (mode) {
    const requested = mode[1]?.toLowerCase();
    if (!requested)
      return {
        status: `Current mode: ${state.intent}. Use /mode plan or /mode build`,
      };
    if (requested !== "plan" && requested !== "build")
      return {
        status: `Unknown mode: ${requested}. Use /mode plan or /mode build`,
      };
    return { status: `Mode changed to ${requested}`, intent: requested };
  }
  const theme = command.match(/^\/theme(?:\s+(\S+))?\s*$/i);
  if (theme) {
    const current = state.theme ?? "auto";
    const requested = theme[1]?.toLowerCase();
    if (!requested)
      return {
        status: `Current theme: ${current}. Use /theme dark, /theme light or /theme auto`,
      };
    if (requested !== "dark" && requested !== "light" && requested !== "auto")
      return {
        status: `Unknown theme: ${requested}. Use /theme dark, /theme light or /theme auto`,
      };
    return { status: `Theme changed to ${requested}`, theme: requested };
  }
  const mouse = command.match(/^\/mouse(?:\s+(\S+))?\s*$/i);
  if (mouse) {
    const current = state.mouse ?? false;
    const requested = mouse[1]?.toLowerCase();
    if (!requested)
      return {
        status: `Mouse is ${current ? "on" : "off"}. Use /mouse on or /mouse off`,
      };
    if (requested !== "on" && requested !== "off")
      return {
        status: `Unknown mouse setting: ${requested}. Use /mouse on or /mouse off`,
      };
    return {
      status: `Mouse changed to ${requested}`,
      mouse: requested === "on",
    };
  }
  if (command === "/help") return { status: HELP_STATUS, help: true };
  if (command === "/mcp")
    return {
      status: state.mcp.servers
        ? `MCP servers: ${state.mcp.servers} · ${state.mcp.tools} tools`
        : "MCP servers: 0 · use --mcp-config to connect trusted servers",
    };
  return { status: `Unknown command: ${command}. Use /help` };
}

export const BUSY_STATUS = "Busy · wait for the current turn to finish";

// ------------------------------------------------------------------ the palette

export interface PaletteItem {
  id: string;
  label: string;
  /** The slash command the row runs, shown at the right. */
  hint: string;
}

/** Every command the palette can run. The id is the row key. */
export const PALETTE: readonly PaletteItem[] = [
  { id: "mode-plan", label: "Mode: plan (read only)", hint: "/mode plan" },
  {
    id: "mode-build",
    label: "Mode: build (read, edit, command)",
    hint: "/mode build",
  },
  { id: "theme-dark", label: "Theme: dark", hint: "/theme dark" },
  { id: "theme-light", label: "Theme: light", hint: "/theme light" },
  { id: "theme-auto", label: "Theme: auto", hint: "/theme auto" },
  { id: "mouse-on", label: "Mouse: on", hint: "/mouse on" },
  { id: "mouse-off", label: "Mouse: off", hint: "/mouse off" },
  { id: "mcp", label: "MCP servers", hint: "/mcp" },
  { id: "help", label: "Help and keys", hint: "/help" },
];

/** The slash command a palette row stands for. */
export function paletteCommand(id: string): string | undefined {
  return PALETTE.find((item) => item.id === id)?.hint;
}

/** Rows whose label or command contains every word of the query. */
export function filterPalette(query: string): PaletteItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  return PALETTE.filter((item) => {
    const haystack = `${item.label} ${item.hint}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}
