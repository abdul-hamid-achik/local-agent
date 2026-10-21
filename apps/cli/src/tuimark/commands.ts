// Slash commands and the command palette. Pure: no I/O, no Tuimark.
//
// The strings are the Vue client's (apps/cli/src/terminal.ts) word for word,
// so the two clients can be compared.

export type Intent = "plan" | "build";

export interface CommandState {
  intent: Intent;
  /** Connected MCP servers and the tools they expose. */
  mcp: { servers: number; tools: number };
}

export interface CommandOutcome {
  status: string;
  /** Set when the command changed the mode. */
  intent?: Intent;
  /** Set when the command asks for the help overlay. */
  help?: boolean;
}

export const HELP_STATUS =
  "/mode plan|build · /mcp · /help · Esc cancel · Ctrl+C exit";

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
