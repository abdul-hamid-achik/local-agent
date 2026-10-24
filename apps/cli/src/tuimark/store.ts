// The Tuimark store: every path ui/app.tui reads, with its shape. One place, so
// the controller, the samples the goldens are made from, and the tests cannot
// drift from the markup.

import {
  type ApprovalRequest,
  COMMAND_WRAPPERS,
  type Provider,
} from "@local-agent/runtime";
import { approvalPreview } from "#cli/preview";
import {
  type Block,
  cleanText,
  clip,
  IDLE_LIVE,
  IDLE_THINKING,
  type LiveView,
  type MessageView,
  NO_CARD,
  type ThinkingView,
  type ToolReceipt,
} from "#cli/tuimark/blocks";
import {
  filterPalette,
  type HelpRow,
  helpRows,
  type Intent,
  type PaletteItem,
  type SlashRow,
} from "#cli/tuimark/commands";
import type { TranscriptSnapshot } from "#cli/tuimark/transcript";

export const IDLE_STATUS = "Ready · /help for keys";

/** The idle status line, prefixed with the mode so the intent is always
 * visible; the MCP count stays for the whole session. */
export function idleStatus(mcpServers: number, intent?: Intent): string {
  const base = mcpServers ? `${IDLE_STATUS} · MCP ${mcpServers}` : IDLE_STATUS;
  return intent ? `${intent} · ${base}` : base;
}
export const PLACEHOLDER = "Message the model";
export const PLACEHOLDER_BUSY = "Generating…";
/** While the dialog is not armed yet: only deny works. */
export const APPROVAL_WAIT_STATUS = "Waiting for approval · n deny · Esc deny";
/** Once the dialog accepts `y` (and `a`, the session grant, `!` the rule). */
export const APPROVAL_STATUS =
  "Waiting for approval · y allow · a session · ! always · n deny · Esc deny";
export const APPROVAL_TRUNCATED_STATUS =
  "Preview truncated · y allow · n deny · Esc deny";
export const BUSY_STATUS_LINE = "Generating…";
/** Busy, but no text/thinking/tool event has arrived yet this turn. */
export const WAITING_STATUS = "Waiting for the model…";
/** A `y` swallowed by the arm delay says so instead of going silent. */
export const APPROVAL_UNARMED_STATUS = "Approval not armed yet — press y again";
/** An `a` swallowed by the arm delay, same rule as `y`. */
export const APPROVAL_UNARMED_SESSION_STATUS =
  "Approval not armed yet — press a again";
/** A `!` swallowed by the arm delay, same rule as `y` and `a`. */
export const APPROVAL_UNARMED_ALWAYS_STATUS =
  "Approval not armed yet — press ! again";
/** An `a` on a truncated preview: no grant is taken from an unread change. */
export const APPROVAL_TRUNCATED_SESSION_STATUS =
  "Preview truncated: session grant unavailable";
/** A `!` on a truncated preview: no persistent rule comes from an unread change. */
export const APPROVAL_TRUNCATED_ALWAYS_STATUS =
  "Preview truncated: always rule unavailable";

export interface DiffLine {
  n: number;
  t: string;
  add: boolean;
  del: boolean;
  /** A row the client wrote (the "more lines" marker), not part of the preview text. */
  note: boolean;
}

/** `/details` with no receipt yet. */
export const NO_DETAILS_STATUS =
  "No tool output yet. Run something first, then /details";

export interface DetailsState {
  open: boolean;
  /** "<tool> · <status>", as the receipt's card shows them. */
  title: string;
  /** The card's args line ("" when the call had none to summarize). */
  args: string;
  /** The whole output, hard-wrapped like an approval preview. */
  lines: DiffLine[];
}

/** One row of the `/sessions` picker: a saved session and the selection flag. */
export interface SessionRow {
  /** The session's public handle, also the row key. */
  id: string;
  /** "<handle> · N messages", plus " · current" on the live one. */
  label: string;
  sel: boolean;
  /** "›" on the chosen row, "" on the rest (selection must read as text). */
  mark: string;
}

/**
 * The read-only panel (`/status`, `/mcp`, `/sessions`): the details
 * viewer's shape (title, a header line, wrapped lines) plus, for the
 * sessions picker, display rows the parent moves a choice over.
 */
export interface PanelState {
  open: boolean;
  title: string;
  /** A one-line summary under the title (the `/mcp` count line), "" for none. */
  args: string;
  /** The panel's text, hard-wrapped like a preview. Empty while picking. */
  lines: DiffLine[];
  /** The picker's rows. Empty unless `picking`. */
  rows: SessionRow[];
  /** True for `/sessions`: enter loads the chosen row. */
  picking: boolean;
}

export interface ApprovalState {
  open: boolean;
  title: string;
  body: string;
  danger: boolean;
  no: string;
  yes: string;
  /** The preview was cut at the row cap: the dialog says so and still defaults to Deny. */
  truncated: boolean;
  /**
   * What `a` would grant for the rest of the session, spelled out (or why
   * no grant is offered, when the preview is truncated). "" when closed.
   */
  grant_hint: string;
  lines: DiffLine[];
}

export interface Store {
  header: {
    provider: string;
    model: string;
    mode: Intent;
    session: string;
    /** Basename of the workspace root, for the empty-state welcome. "" when unknown. */
    workspace: string;
    ctx_label: string;
    ctx_pct: number;
    /** Threshold styling for the ctx text/bar: warn at >=75%, danger at >=90%. */
    ctx_warn: boolean;
    ctx_danger: boolean;
    /** Active session grants as one compact label ("auto: files · bun test"), "" for none. */
    grants: string;
    /** Whether any session grant is active (the header shows `grants` only then). */
    grants_on: boolean;
  };
  /** True only before the first message: the welcome shows. Never true again. */
  empty: boolean;
  /** Fullscreen mouse reporting (`<tui mouse="mouse_on">`). On by default in
   *  fullscreen — wheel scrolls the transcript under the pointer; inline has
   *  no mouse (the terminal's own scrollback is the history). */
  mouse_on: boolean;
  messages: MessageView[];
  earlier: { n: number };
  live: LiveView;
  /** The model's reasoning while it streams; gone once the answer starts. */
  thinking: ThinkingView;
  approval: ApprovalState;
  /**
   * The `/details` viewer over the latest tool receipt: a modal fullscreen,
   * an in-flow block inline (region state, never committed).
   */
  details: DetailsState;
  /**
   * The read-only panel over `/status`, `/mcp` and the `/sessions`
   * picker: a modal fullscreen, an in-flow block inline, like `details`.
   */
  panel: PanelState;
  palette: { open: boolean; query: string; cur: string };
  found: { any: boolean; items: PaletteItem[] };
  /**
   * The `/` popup over the composer: display-only rows (nothing in an
   * `each` can take focus), the chosen one carrying `sel`. The controller
   * rewrites it as the draft is typed; inline it lives in the region and
   * is never committed.
   */
  slash: { open: boolean; rows: SlashRow[] };
  /** The help overlay: `cmds` is the slash catalog, derived once at build. */
  help: { open: boolean; cmds: HelpRow[] };
  draft: string;
  placeholder: string;
  busy: boolean;
  status: string;
  /**
   * Scratch paths of the inline client (ui/inline.tui reads them only at
   * commit time): the view or blocks a `commit` is about to print into the
   * scrollback. Written right before the commit, cleared after its ack.
   */
  commit_buf: MessageView | null;
  commit_blocks: Block[];
}

/** The empty view a fresh store carries at `commit_buf`, before any commit. */
export const EMPTY_COMMIT_VIEW: MessageView = Object.freeze({
  id: "commit",
  is_user: false,
  is_asst: false,
  is_tool: false,
  is_notice: false,
  blocks: [],
  card: NO_CARD,
  links: [],
  images: [],
  images_more: 0,
}) as MessageView;

/** A token count in k = 1024 tokens: 32768 reads "32k", 9800 reads "9.6k". */
export function tokens(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1024;
  return `${k >= 10 ? Math.round(k) : k.toFixed(1)}k`;
}

export function contextLabel(used: number, size: number): string {
  return `${tokens(used)}/${tokens(size)}`;
}

export function contextPct(used: number, size: number): number {
  return Math.max(0, Math.min(100, Math.round((used / size) * 100)));
}

/** The ctx threshold flags the header styles by: warn at >=75%, danger at >=90%. */
export function ctxFlags(pct: number): { warn: boolean; danger: boolean } {
  return { warn: pct >= 75, danger: pct >= 90 };
}

/**
 * What the header's ctx meter shows, turn by turn.
 *
 * The rule: within a turn the display is the LARGEST input+output any `done`
 * event of that turn has reported (a multi-step turn's later steps resend
 * the whole history, so a smaller mid-turn value is a step artifact, not
 * freed context). The max is scoped to the turn: when the turn finishes,
 * its LAST observed value becomes the baseline the next turn starts from,
 * so a legitimately smaller conversation (after a resume, say) is not
 * shown the old turn's max forever. A turn with no `done` event leaves the
 * baseline alone.
 */
export class CtxMeter {
  private base = 0;
  private max = 0;
  private last: number | undefined;

  beginTurn(): void {
    this.max = this.base;
    this.last = undefined;
  }

  /** One usage-bearing event; returns what the header should show now. */
  observe(used: number): number {
    this.last = used;
    if (used > this.max) this.max = used;
    return this.max;
  }

  /** The turn is over; returns the baseline the header rests at. */
  finishTurn(): number {
    if (this.last !== undefined) this.base = this.last;
    this.max = this.base;
    return this.base;
  }

  /** What the header shows right now (the turn's max, or the resting baseline). */
  get current(): number {
    return this.max;
  }

  /** A different conversation starts from zero (a session switch). */
  reset(): void {
    this.base = 0;
    this.max = 0;
    this.last = undefined;
  }
}

export function providerLabel(provider: Pick<Provider, "type">): string {
  return provider.type === "ollama_remote"
    ? "Chalupa / remote"
    : "Ollama / loopback";
}

export function noDetails(): DetailsState {
  return { open: false, title: "", args: "", lines: [] };
}

export function noPanel(): PanelState {
  return {
    open: false,
    title: "",
    args: "",
    lines: [],
    rows: [],
    picking: false,
  };
}

export function noApproval(): ApprovalState {
  return {
    open: false,
    title: "",
    body: "",
    danger: false,
    no: "Deny",
    yes: "Allow",
    truncated: false,
    grant_hint: "",
    lines: [],
  };
}

export interface StoreInit {
  provider: Pick<Provider, "type" | "model" | "context_size">;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  mcpServers: number;
  transcript: TranscriptSnapshot;
  /** Workspace root (or basename); only its basename is shown. */
  workspace?: string;
  /**
   * Overrides the derived empty flag. The inline client passes its real
   * transcript length here: its snapshot always starts empty.
   */
  empty?: boolean;
  /** Inline presentation: its palette starts without the load-earlier row. */
  inline?: boolean;
}

/** The last path segment of a workspace root ("" for "/" or ""). */
export function workspaceBasename(root: string): string {
  return root.split("/").filter(Boolean).at(-1) ?? "";
}

export function initialStore(init: StoreInit): Store {
  const found = filterPalette("", init.inline);
  return {
    header: {
      provider: providerLabel(init.provider),
      model: clip(cleanText(init.provider.model), 40),
      mode: init.intent,
      session: init.sessionId ? `session ${init.sessionId}` : "temporary",
      workspace: init.workspace ? workspaceBasename(init.workspace) : "",
      ctx_label: contextLabel(0, init.provider.context_size),
      ctx_pct: 0,
      ctx_warn: false,
      ctx_danger: false,
      grants: "",
      grants_on: false,
    },
    empty:
      init.empty ??
      (init.transcript.messages.length === 0 &&
        init.transcript.earlier.n === 0),
    mouse_on: !init.inline,
    messages: init.transcript.messages,
    earlier: init.transcript.earlier,
    live: IDLE_LIVE,
    thinking: IDLE_THINKING,
    approval: noApproval(),
    details: noDetails(),
    panel: noPanel(),
    palette: { open: false, query: "", cur: found[0]?.id ?? "" },
    found: { any: found.length > 0, items: found },
    slash: { open: false, rows: [] },
    help: { open: false, cmds: helpRows() },
    draft: "",
    placeholder: PLACEHOLDER,
    busy: false,
    status: idleStatus(init.mcpServers, init.intent),
    commit_buf: EMPTY_COMMIT_VIEW,
    commit_blocks: [],
  };
}

// ---------------------------------------------------------------------- approval

/** Rows of the approval preview; past this the preview says it is truncated. */
export const APPROVAL_MAX_ROWS = 2000;
/** The dialog is `width: 76` (see ui/app.tui), or the whole screen when narrower. */
const DIALOG_WIDTH = 76;
/** Border (2) and padding (4) of the dialog, plus the border of the preview (2). */
const DIALOG_CHROME = 8;

/** Columns a preview row may use inside the dialog on a terminal `cols` wide. */
export function previewWidth(cols: number): number {
  return Math.max(20, Math.min(DIALOG_WIDTH, cols) - DIALOG_CHROME);
}

/** Border (2) and padding (2) of the in-flow approval box, plus the preview's border (2). */
const INLINE_CHROME = 6;

/** Columns a preview row may use in the inline approval on a terminal `cols` wide. */
export function previewWidthInline(cols: number): number {
  return Math.max(20, cols - INLINE_CHROME);
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

/**
 * Hard-wraps one line by display width. It keeps every character and every
 * space (CSS `wrap: wrap` would collapse repeated spaces, which corrupts a
 * diff). The continuation rows are marked with a leading arrow.
 */
export function wrapLine(text: string, width: number): string[] {
  if (Bun.stringWidth(text) <= width) return [text];
  const rows: string[] = [];
  let row = "";
  let used = 0;
  let limit = width;
  for (const { segment } of graphemes.segment(text)) {
    const w = Bun.stringWidth(segment);
    if (used + w > limit && row !== "") {
      rows.push(row);
      row = "↪ ";
      used = 2;
      limit = width;
    }
    row += segment;
    used += w;
  }
  rows.push(row);
  return rows;
}

export interface Preview {
  lines: DiffLine[];
  /** Logical lines of the text, and how many of them are fully shown. */
  total: number;
  shown: number;
  truncated: boolean;
}

/** The rows of a preview text: wrapped to `width`, capped at `maxRows` plus a marker row. */
export function previewRows(
  text: string,
  width: number,
  maxRows = APPROVAL_MAX_ROWS,
): Preview {
  const source = cleanText(text).split("\n");
  // A diff ends with a newline: that is not an empty row to scroll to.
  while (source.length > 1 && source[source.length - 1] === "") source.pop();
  const lines: DiffLine[] = [];
  let shown = 0;
  let truncated = false;
  outer: for (const line of source) {
    const add = line.startsWith("+") && !line.startsWith("+++");
    const del = line.startsWith("-") && !line.startsWith("---");
    for (const t of wrapLine(line, width)) {
      if (lines.length >= maxRows) {
        truncated = true;
        break outer;
      }
      lines.push({ n: lines.length, t, add, del, note: false });
    }
    shown++;
  }
  if (truncated)
    lines.push({
      n: lines.length,
      t: `… ${source.length - shown} more lines not shown`,
      add: false,
      del: false,
      note: true,
    });
  return { lines, total: source.length, shown, truncated };
}

function prettyJson(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/** The full text the dialog previews: nothing the user approves is left out of it. */
export function approvalText(proposal: ApprovalRequest, root?: string): string {
  if (proposal.kind === "mcp")
    return `${proposal.name}\n${prettyJson(proposal.argumentsJson)}\n\nMCP effects are not verified by transport success.`;
  return approvalPreview(proposal, root);
}

export const TRUNCATED_BODY =
  "Preview truncated. Deny unless you have read the whole change.";

// ------------------------------------------------------------ session grants
//
// `a` on an armed approval allows the call and remembers the grant for the
// rest of the session (controller memory only; nothing is persisted). The
// matching rules are deliberately coarse and spelled out in the dialog:
// edits and writes share one "file changes" grant, a command grants its
// program plus its first argument (`bun test`, `git status` cover further
// arguments), and an MCP call grants its exact SERVER__TOOL name.
//
// A prefix grant covers new positional arguments but not new options: an
// option the approved command did not carry (`bun test --preload x.ts`,
// `make build -f other.mk`, `git grep -O…`) asks again, and `a` on that
// card adds it to the grant.
//
// Two narrower shapes keep a grant from covering code nobody saw:
// - a runner subcommand of a package manager or toolchain (`bun run`,
//   `npm exec`, `docker run`, `go run`, `bun add`, …) grants three words,
//   so `bun run test …` never covers `bun run evil.ts` (`git add` is not a
//   runner: it keeps the two-word prefix);
// - a bare program, a command whose second word is an option (`git -c …`,
//   `bash -c …`, `node -e …`), a program that runs another program (`env`,
//   `xargs`, `timeout`, `ssh`, …) and the git subcommands that run code
//   from their arguments or write config that later runs (`git config`,
//   `git rebase`, `git bisect`, …) grant only that exact argv.

/** Per program, the second words that run code (or install a package)
 * named by the third. */
const RUNNER_SUBCOMMANDS: Record<string, ReadonlySet<string>> = {
  bun: new Set(["run", "x", "exec", "add", "install", "i"]),
  npm: new Set(["run", "run-script", "exec", "x", "install", "i", "add"]),
  pnpm: new Set(["run", "exec", "dlx", "add", "install", "i"]),
  yarn: new Set(["run", "exec", "dlx", "add"]),
  deno: new Set(["run", "x", "install", "add"]),
  docker: new Set(["run", "exec"]),
  podman: new Set(["run", "exec"]),
  kubectl: new Set(["run", "exec"]),
  go: new Set(["run", "install"]),
  cargo: new Set(["run", "install"]),
  uv: new Set(["run", "add"]),
  pip: new Set(["install"]),
  pip3: new Set(["install"]),
  pipx: new Set(["run", "install"]),
  poetry: new Set(["run", "add"]),
  gem: new Set(["install"]),
  brew: new Set(["install"]),
};

/** git subcommands that execute their arguments or set config that executes. */
const GIT_EXACT = new Set([
  "config",
  "submodule",
  "bisect",
  "rebase",
  "filter-branch",
  "filter-repo",
  "difftool",
  "mergetool",
  "daemon",
]);

/** A command grant's shape (see above): its prefix, and whether it is exact. */
function commandGrant(argv: readonly string[]): {
  prefix: string[];
  exact: boolean;
} {
  const program = (argv[0] ?? "").split("/").at(-1)?.toLowerCase() ?? "";
  const second = argv[1] ?? "";
  const runner =
    RUNNER_SUBCOMMANDS[program]?.has(second.toLowerCase()) ?? false;
  const exact =
    argv.length <= 1 ||
    COMMAND_WRAPPERS.has(program) ||
    second.startsWith("-") ||
    (program === "git" && GIT_EXACT.has(second.toLowerCase())) ||
    // A runner's third word is what it runs; an option there (`npm i -g`,
    // `pip install --index-url …`) would make the grant cover any target.
    (runner && (argv.length <= 2 || (argv[2] ?? "").startsWith("-")));
  if (exact) return { prefix: [...argv], exact };
  return { prefix: argv.slice(0, runner ? 3 : 2), exact };
}

/** The server and tool of an MCP `SERVER__TOOL` name, or undefined without the separator. */
export function mcpParts(
  name: string,
): { server: string; tool: string } | undefined {
  const at = name.indexOf("__");
  if (at <= 0 || at + 2 >= name.length) return undefined;
  return { server: name.slice(0, at), tool: name.slice(at + 2) };
}

/** An argv word that is an option (`-x`, `--flag=value`, `--`), not `-` (stdin). */
const isOption = (word: string): boolean =>
  word.length > 1 && word.startsWith("-");

/** The grant a proposal falls under: its identity key and its short label. */
export function grantFor(proposal: ApprovalRequest): {
  key: string;
  label: string;
  /**
   * A command grant's argv prefix (program + first argument, or three
   * words after a runner subcommand). It matches as a prefix — the
   * dialog's "`bun test …`" — and nothing wider ever matches. An exact
   * grant's prefix is the whole argv, and it matches only itself.
   */
  prefix?: readonly string[];
  /** A prefix grant's option words after the prefix, as approved. */
  options?: readonly string[];
  exact?: boolean;
} {
  switch (proposal.kind) {
    case "edit":
    case "write":
      return { key: "files", label: "files" };
    case "command": {
      // Identity is the raw argv and cwd the runtime would execute;
      // cleaned text is only ever the label the user reads.
      const { prefix, exact } = commandGrant(proposal.argv);
      const label = prefix.map(cleanText).join(" ");
      return {
        key: `${exact ? "cmd=" : "cmd:"}${JSON.stringify(prefix)}@${proposal.cwd}`,
        label,
        prefix,
        ...(exact
          ? { exact }
          : { options: proposal.argv.slice(prefix.length).filter(isOption) }),
      };
    }
    case "mcp": {
      return { key: `mcp:${proposal.name}`, label: cleanText(proposal.name) };
    }
  }
}

/**
 * Whether a remembered grant covers a proposal: exact for files and MCP,
 * an argv-prefix match for commands whose options were all approved
 * before (see `grantFor`).
 */
export function grantCovers(
  grant: {
    key: string;
    prefix?: readonly string[];
    options?: readonly string[];
  },
  proposal: ApprovalRequest,
): boolean {
  if (proposal.kind === "command") {
    // A grant with no program word covers nothing: `every` over an empty
    // prefix is vacuously true and would auto-allow every command.
    if (!grant.prefix || grant.prefix.length === 0 || grant.prefix[0] === "")
      return false;
    // The cwd is part of the grant's key: the same prefix elsewhere is
    // a different command.
    const encoded = `${JSON.stringify(grant.prefix)}@${proposal.cwd}`;
    if (grant.key === `cmd=${encoded}`)
      return (
        proposal.argv.length === grant.prefix.length &&
        grant.prefix.every((word, i) => proposal.argv[i] === word)
      );
    if (grant.key !== `cmd:${encoded}`) return false;
    const approved = new Set(grant.options ?? []);
    return (
      grant.prefix.every((word, i) => (proposal.argv[i] ?? "") === word) &&
      proposal.argv
        .slice(grant.prefix.length)
        .every((word) => !isOption(word) || approved.has(word))
    );
  }
  return grantFor(proposal).key === grant.key;
}

/** What `a` would grant, as the dialog spells it out. */
export function grantHint(
  proposal: ApprovalRequest,
  truncated: boolean,
): string {
  if (truncated) return "preview truncated: session grant unavailable";
  if (proposal.kind === "edit" || proposal.kind === "write")
    return "a allow file changes this session";
  const { label, exact } = grantFor(proposal);
  if (proposal.kind !== "command" || exact)
    return `a allow \`${label}\` this session`;
  return `a allow \`${label} …\` this session`;
}

/** The header's compact grants label: "auto: files · bun test", "" for none. */
export function grantsLabel(labels: readonly string[]): string {
  return labels.length === 0 ? "" : `auto: ${labels.join(" · ")}`;
}

/** What the approval dialog shows for a runtime proposal, wrapped for `cols` columns. */
export function approvalState(
  proposal: ApprovalRequest,
  cols = 80,
  root?: string,
  inline = false,
): ApprovalState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  const preview = previewRows(approvalText(proposal, root), width);
  const base = {
    ...noApproval(),
    open: true,
    lines: preview.lines,
    truncated: preview.truncated,
    grant_hint: grantHint(proposal, preview.truncated),
  };
  const body = (text: string) => (preview.truncated ? TRUNCATED_BODY : text);
  switch (proposal.kind) {
    case "edit":
      return {
        ...base,
        title: clip(`Edit ${cleanText(proposal.path)}?`, 70),
        body: body("Apply this change to the workspace."),
        danger: preview.truncated,
      };
    case "write":
      return {
        ...base,
        title: clip(
          `${proposal.created ? "Create" : "Replace"} ${cleanText(proposal.path)}?`,
          70,
        ),
        body: body(
          proposal.created
            ? "Create this file in the workspace."
            : "Replace this file in the workspace.",
        ),
        danger: preview.truncated,
      };
    case "mcp": {
      // The dialog names the server, not only the joined SERVER__TOOL name:
      // which process would run this is the decision the user is making.
      const parts = mcpParts(proposal.name);
      const server = parts ? cleanText(parts.server) : undefined;
      const tool = cleanText(parts ? parts.tool : proposal.name);
      return {
        ...base,
        title: clip(
          server ? `Call ${tool} from server ${server}?` : `Call ${tool}?`,
          70,
        ),
        body: body(
          server
            ? `MCP tool ${tool} from server ${server}. Call it with the arguments below.`
            : "Call this MCP tool with the arguments below.",
        ),
        danger: true,
      };
    }
    default:
      return {
        ...base,
        title: "Run command?",
        body: body("Runs exactly as written: there is no shell."),
        danger: true,
      };
  }
}

// --------------------------------------------------------------------- details

/** Rows of the details viewer; past this it says how many lines it left out. */
export const DETAILS_MAX_ROWS = APPROVAL_MAX_ROWS;

/**
 * The viewer's rows: the output hard-wrapped with the approval preview's
 * machinery (every character kept, `↪` continuations), capped like it.
 * No add/delete coloring: tool output is not a diff.
 */
export function detailsRows(text: string, width: number): DiffLine[] {
  const source = cleanText(text).split("\n");
  while (source.length > 1 && source[source.length - 1] === "") source.pop();
  if (source.length === 1 && source[0] === "")
    return [{ n: 0, t: "(no output)", add: false, del: false, note: true }];
  const lines: DiffLine[] = [];
  let shown = 0;
  let truncated = false;
  outer: for (const line of source) {
    for (const t of wrapLine(line, width)) {
      if (lines.length >= DETAILS_MAX_ROWS) {
        truncated = true;
        break outer;
      }
      lines.push({ n: lines.length, t, add: false, del: false, note: false });
    }
    shown++;
  }
  if (truncated)
    lines.push({
      n: lines.length,
      t: `… ${source.length - shown} more lines not shown`,
      add: false,
      del: false,
      note: true,
    });
  return lines;
}

/**
 * What the viewer shows for a receipt, wrapped for `cols` columns: at the
 * approval dialog's width fullscreen, the full-width box's inline.
 */
export function detailsState(
  receipt: ToolReceipt,
  cols = 80,
  inline = false,
): DetailsState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  return {
    open: true,
    title: clip(`${receipt.name} · ${receipt.status}`, 70),
    args: receipt.args,
    lines: detailsRows(receipt.output, width),
  };
}

// ----------------------------------------------------------------------- panel

/** A text panel (`/status`, `/mcp`), wrapped like the details viewer. */
export function panelState(
  title: string,
  args: string,
  text: string,
  cols = 80,
  inline = false,
): PanelState {
  const width = inline ? previewWidthInline(cols) : previewWidth(cols);
  return {
    open: true,
    title: clip(cleanText(title), 70),
    args: clip(cleanText(args), 160),
    lines: detailsRows(text, width),
    rows: [],
    picking: false,
  };
}

/** One saved session as the picker lists it. */
export interface SessionSummary {
  handle: string;
  messages: number;
}

/** Picker rows for `sessions`, the row at `selected` marked; `current` is the live handle. */
export function sessionRows(
  sessions: readonly SessionSummary[],
  selected: number,
  current?: string,
): SessionRow[] {
  return sessions.map((session, index) => ({
    id: session.handle,
    label: cleanText(
      `${session.handle} · ${session.messages} messages${session.handle === current ? " · current" : ""}`,
    ),
    sel: index === selected,
    mark: index === selected ? "›" : "",
  }));
}

/** The `/status` panel's key/value facts, exactly those the client holds. */
export interface StatusFacts {
  provider: Pick<
    Provider,
    "type" | "model" | "context_size" | "base_url" | "think"
  >;
  /** The workspace root, shown whole. */
  workspace: string;
  intent: Intent;
  /** Public session handle, or undefined for a temporary session. */
  sessionId?: string;
  /** The session database path, when the session's store can name it. */
  sessionDb?: string;
  theme: string;
  /** "on" / "off", or "n/a (inline)" where the engine has no mouse. */
  mouse: string;
  /** Session-grant labels, in grant order. */
  grants: readonly string[];
  /** Tokens the header's meter shows used. */
  usedTokens: number;
  mcpServers: number;
  mcpTools: number;
}

/** The `/status` lines: `Key: value`, one fact per line, nothing invented. */
export function statusLines(facts: StatusFacts): string[] {
  const lines = [
    `Model: ${cleanText(facts.provider.model)}`,
    `Provider: ${providerLabel(facts.provider)}`,
    `Endpoint: ${cleanText(facts.provider.base_url)}`,
    `Think: ${facts.provider.think === undefined ? "model default" : facts.provider.think ? "on" : "off"}`,
    `Context: ${contextLabel(facts.usedTokens, facts.provider.context_size)}`,
    `Workspace: ${cleanText(facts.workspace)}`,
    `Mode: ${facts.intent}`,
    `Session: ${facts.sessionId ?? "temporary"}`,
  ];
  if (facts.sessionDb) lines.push(`Session db: ${cleanText(facts.sessionDb)}`);
  lines.push(
    `Theme: ${facts.theme}`,
    `Mouse: ${facts.mouse}`,
    `Grants: ${facts.grants.length ? grantsLabel(facts.grants).slice("auto: ".length) : "none"}`,
    `MCP: ${facts.mcpServers} servers · ${facts.mcpTools} tools`,
  );
  return lines;
}

/** One connected MCP server as the panel lists it: its name and tool names only. */
export interface McpServerFacts {
  name: string;
  /** Tool names as shown under the server (local names when the prefix is known). */
  tools: readonly string[];
}

/**
 * The `/mcp` panel's lines: one heading line per server, then its tool
 * names. Names only — never a result or structured content (those stay
 * inside the runtime's parser boundary).
 */
export function mcpLines(servers: readonly McpServerFacts[]): string[] {
  if (servers.length === 0)
    return [
      "No MCP servers configured",
      "Use --mcp-config to connect trusted servers",
    ];
  const lines: string[] = [];
  for (const server of servers) {
    lines.push(
      cleanText(
        `${server.name} · ${server.tools.length} tool${server.tools.length === 1 ? "" : "s"}`,
      ),
    );
    lines.push(
      server.tools.length
        ? `  ${server.tools.map((tool) => cleanText(tool)).join(", ")}`
        : "  (no tools)",
    );
  }
  return lines;
}
