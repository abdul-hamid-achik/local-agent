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
  /** Active session-grant labels, in grant order (`/permissions` lists them). */
  grants?: readonly string[];
  /** The model the provider currently uses (`/model` reports it). */
  model?: string;
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
  /** Set by `/permissions clear`: revoke every session grant. */
  clearGrants?: boolean;
  /**
   * Set by `/details`: open the viewer over the latest tool receipt. The
   * controller owns the receipt, so it writes the real status itself.
   */
  details?: boolean;
  /** Set by `/status`: open the panel over the effective config. */
  statusPanel?: boolean;
  /** Set by `/mcp`: open the panel over the servers (`status` is its count line). */
  mcpPanel?: boolean;
  /** Set by `/sessions`: open the session picker (or say why there is none). */
  sessions?: boolean;
  /** Set by `/export`: write the transcript, markdown views or raw JSON. */
  exportTranscript?: boolean;
  /** The format `/export` asked for; "json" writes the raw messages array. */
  exportFormat?: "markdown" | "json";
  /** Set by `/model NAME`: the controller validates it, then switches. */
  model?: string;
  /**
   * Set by `/find TEXT`: the query, whole, exactly as typed after /find.
   * The controller searches the workspace's saved sessions with it.
   */
  find?: string;
}

export const HELP_STATUS =
  "/mode plan|build · /model [NAME] · /theme dark|light|auto · /mouse on|off · /permissions [clear] · /details · /status · /sessions · /find TEXT · /export [json] · /mcp · /help · Esc cancel · Ctrl+C exit";

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
  const model = command.match(/^\/model(?:\s+(\S+))?\s*$/i);
  if (model) {
    const requested = model[1];
    if (!requested)
      return {
        status: `Current model: ${state.model ?? "unknown"}. Use /model NAME to switch`,
      };
    // The controller validates the name against the Ollama inventory
    // before it switches; the flash only says a check is running.
    return { status: `Switching model to ${requested}…`, model: requested };
  }
  const permissions = command.match(/^\/permissions(?:\s+(\S+))?\s*$/i);
  if (permissions) {
    const grants = state.grants ?? [];
    const requested = permissions[1]?.toLowerCase();
    if (!requested)
      return {
        status: grants.length
          ? `Session grants: ${grants.join(" · ")}`
          : "No session grants",
      };
    if (requested !== "clear")
      return {
        status: `Unknown permissions setting: ${requested}. Use /permissions or /permissions clear`,
      };
    return {
      status: grants.length ? "Session grants cleared" : "No session grants",
      clearGrants: true,
    };
  }
  if (command === "/details")
    return { status: "Latest tool output", details: true };
  if (command === "/status") return { status: "Status", statusPanel: true };
  if (command === "/sessions") return { status: "Sessions", sessions: true };
  const exportMatch = command.match(/^\/export(?:\s+(\S+))?\s*$/i);
  if (exportMatch) {
    const requested = exportMatch[1]?.toLowerCase();
    if (requested && requested !== "json")
      return {
        status: `Unknown export format: ${requested}. Use /export or /export json`,
      };
    return {
      status: "Exporting transcript…",
      exportTranscript: true,
      exportFormat: requested === "json" ? "json" : "markdown",
    };
  }
  const find = command.match(/^\/find(?:\s+(.+?))?\s*$/i);
  if (find) {
    const query = find[1]?.trim();
    if (!query) return { status: "Search the saved sessions. Use /find TEXT" };
    return { status: `Searching sessions for ${query}…`, find: query };
  }
  if (command === "/help") return { status: HELP_STATUS, help: true };
  if (command === "/mcp")
    return {
      // The count line heads the panel and rests on the status line, as
      // the flash alone did before the panel existed.
      status: state.mcp.servers
        ? `MCP servers: ${state.mcp.servers} · ${state.mcp.tools} tools`
        : "MCP servers: 0 · use --mcp-config to connect trusted servers",
      mcpPanel: true,
    };
  return { status: `Unknown command: ${command}. Use /help` };
}

export const BUSY_STATUS = "Busy · wait for the current turn to finish";

// ------------------------------------------------------------ the slash catalog

/**
 * The one list of slash commands: the parser above, the `/` popup over the
 * composer and the help overlay's command section all read it (the palette
 * below is a separate, coarser list of actions, some of which stand for one
 * of these). A command `runSlash` does not know must not be listed here.
 */
export interface SlashCommand {
  /** The command as typed, e.g. "/mode". Also the row key in the store. */
  name: string;
  /** What the help overlay shows, arguments included, e.g. "/mode plan|build". */
  usage: string;
  /** One line saying what it does (popup row and help row). */
  description: string;
  /**
   * What completing the command puts in the draft. A command that needs an
   * argument completes to its name plus a space, ready for the argument.
   */
  insert: string;
}

export const SLASH_COMMANDS: readonly SlashCommand[] = [
  {
    name: "/mode",
    usage: "/mode plan|build",
    description: "plan reads only · build can edit",
    insert: "/mode ",
  },
  {
    name: "/model",
    usage: "/model [NAME]",
    description: "show or switch the Ollama model",
    insert: "/model ",
  },
  {
    name: "/theme",
    usage: "/theme dark|light|auto",
    description: "set the session theme",
    insert: "/theme ",
  },
  {
    name: "/mouse",
    usage: "/mouse on|off",
    description: "toggle mouse reporting (fullscreen)",
    insert: "/mouse ",
  },
  {
    name: "/permissions",
    usage: "/permissions [clear]",
    description: "show or clear session approval grants",
    insert: "/permissions ",
  },
  {
    name: "/details",
    usage: "/details",
    description: "show the last tool output in full",
    insert: "/details",
  },
  {
    name: "/status",
    usage: "/status",
    description: "show the effective config (model, workspace, session)",
    insert: "/status",
  },
  {
    name: "/sessions",
    usage: "/sessions",
    description: "switch to a saved session (needs --session-db)",
    insert: "/sessions",
  },
  {
    name: "/find",
    usage: "/find TEXT",
    description: "search saved session messages (needs --session-db)",
    insert: "/find ",
  },
  {
    name: "/export",
    usage: "/export [json]",
    description: "write the conversation to a markdown or JSON file",
    insert: "/export",
  },
  {
    name: "/mcp",
    usage: "/mcp",
    description: "show connected MCP servers and their tools",
    insert: "/mcp",
  },
  {
    name: "/help",
    usage: "/help",
    description: "show the commands and the keys",
    insert: "/help",
  },
];

/**
 * The commands a draft could still become, or undefined when the draft is
 * not a half-typed command at all. A command draft is one line starting
 * with "/" and holding no whitespace yet: once an argument starts (or a
 * pasted block begins with a slash) there is nothing to complete. An empty
 * result means no command starts this way ("/zzz").
 */
export function slashMatches(draft: string): SlashCommand[] | undefined {
  if (!draft.startsWith("/") || /\s/.test(draft)) return undefined;
  const typed = draft.toLowerCase();
  return SLASH_COMMANDS.filter((command) => command.name.startsWith(typed));
}

/** One row of the `/` popup: the name, its line, and the selection flag. */
export interface SlashRow {
  id: string;
  cmd: string;
  desc: string;
  sel: boolean;
  /** "›" on the chosen row, "" on the rest (selection must read as text). */
  mark: string;
}

/** Popup rows for a match list, the row at `selected` marked. */
export function slashRows(
  matches: readonly SlashCommand[],
  selected: number,
): SlashRow[] {
  return matches.map((command, index) => ({
    id: command.name,
    cmd: command.name,
    desc: command.description,
    sel: index === selected,
    mark: index === selected ? "›" : "",
  }));
}

/** One row of the help overlay's command section (usage and its line). */
export interface HelpRow {
  id: string;
  cmd: string;
  desc: string;
}

/** Every slash command as a help row, in catalog order. */
export function helpRows(): HelpRow[] {
  return SLASH_COMMANDS.map((command) => ({
    id: command.name,
    cmd: command.usage,
    desc: command.description,
  }));
}

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
  { id: "permissions", label: "Permissions: show", hint: "/permissions" },
  {
    id: "permissions-clear",
    label: "Permissions: clear all",
    hint: "/permissions clear",
  },
  { id: "details", label: "Details: last tool output", hint: "/details" },
  { id: "status", label: "Status: effective config", hint: "/status" },
  { id: "model", label: "Model: show current", hint: "/model" },
  { id: "find", label: "Find in saved sessions", hint: "/find" },
  { id: "sessions", label: "Sessions: switch session", hint: "/sessions" },
  { id: "export", label: "Export transcript to markdown", hint: "/export" },
  {
    id: "export-json",
    label: "Export transcript to JSON",
    hint: "/export json",
  },
  { id: "mcp", label: "MCP servers", hint: "/mcp" },
  { id: "help", label: "Help, commands and keys", hint: "/help" },
  // Two rows stand for keys, not slash commands: the controller runs them
  // itself (the hint only names the key that does the same). Load-earlier
  // is fullscreen only: inline history is the terminal's scrollback.
  { id: "load-earlier", label: "Load earlier messages", hint: "ctrl+o" },
  { id: "quit", label: "Quit", hint: "ctrl+c" },
];

/**
 * The slash command a palette row stands for. The key rows (quit,
 * load-earlier) stand for no command: undefined, and the controller
 * special-cases their ids before it asks.
 */
export function paletteCommand(id: string): string | undefined {
  const hint = PALETTE.find((item) => item.id === id)?.hint;
  return hint?.startsWith("/") ? hint : undefined;
}

/**
 * Rows whose label or command contains every word of the query. `inline`
 * drops the rows that presentation cannot run (load-earlier).
 */
export function filterPalette(query: string, inline = false): PaletteItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  return PALETTE.filter((item) => {
    if (inline && item.id === "load-earlier") return false;
    const haystack = `${item.label} ${item.hint}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}
