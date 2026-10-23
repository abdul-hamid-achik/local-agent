// The Tuimark loop for ui/app.tui and ui/inline.tui as one reproducible command:
//
//   bun run tuimark:check            verify
//   bun run tuimark:check --update   regenerate samples and goldens, then verify
//
// It (1) rebuilds the sample stores from the code that builds the real store,
// (2) runs `tuimark validate --strict` on each, (3) compares `tuimark dump` text
// at 80 and 120 columns with the goldens in ui/golden/ (the inline document
// dumped with --inline, plus one play-scrollback golden), and (4) runs
// `tuimark play` to prove that every action of the documents fires from the
// keys the hints promise. The binary is TUIMARK_BINARY, else the SDK's own
// resolution (cache, then a checked download of the pinned release).

import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import {
  commitFixtures,
  DETAILS_HIDDEN,
  DRAFT,
  EXACT_LINE,
  HIDDEN_LINE,
  INLINE_SAMPLES,
  LONG_DRAFT,
  SAMPLES,
  WRAPPED_LINE,
} from "#cli/tuimark/fixtures";
import { resolvePinned } from "#cli/tuimark/launch";

const ui = join(dirname(import.meta.path), "ui");
const update = Bun.argv.includes("--update");
const rows = 30;
const widths = [80, 120];
const failures: string[] = [];

const bin = await resolvePinned();

function tuimark(args: string[]): { code: number; out: string; err: string } {
  const run = Bun.spawnSync([bin, ...args], { cwd: ui });
  return {
    code: run.exitCode,
    out: run.stdout.toString(),
    err: run.stderr.toString(),
  };
}

async function matches(path: string, expected: string, what: string) {
  const file = Bun.file(path);
  if (update) {
    await mkdir(dirname(path), { recursive: true });
    await Bun.write(path, expected);
    return;
  }
  if (!(await file.exists()) || (await file.text()) !== expected)
    failures.push(
      `${what} is stale: ${path} (run bun run tuimark:check --update)`,
    );
}

console.log(`tuimark: ${tuimark(["version"]).out.trim()} (${bin})`);

// 1 + 2 + 3: samples, validate, goldens.
for (const [name, make] of Object.entries(SAMPLES)) {
  const data = `samples/${name}.json`;
  await matches(
    join(ui, data),
    `${JSON.stringify(make(), null, 2)}\n`,
    "sample",
  );
  const valid = tuimark(["validate", "app.tui", "--data", data, "--strict"]);
  if (valid.code !== 0 || valid.out.trim() !== "ok")
    failures.push(`validate ${name}: ${valid.out}${valid.err}`.trim());
  for (const cols of widths) {
    const args = [
      "dump",
      "app.tui",
      "--data",
      data,
      "--cols",
      String(cols),
      "--rows",
      String(rows),
    ];
    const json = JSON.parse(tuimark([...args, "--format", "json"]).out) as {
      errors: unknown[];
    };
    if (json.errors.length > 0)
      failures.push(`dump ${name} at ${cols}: ${JSON.stringify(json.errors)}`);
    const text =
      tuimark([...args, "--format", "text"]).out.split("=== nodes ===")[0] ??
      "";
    await matches(join(ui, "golden", `${name}-${cols}.txt`), text, "golden");
  }
}

// The inline document: same loop, dumped as a live region (--inline), so the
// dump's `rows` is the region's height and `terminal_rows` the terminal's.
for (const [name, make] of Object.entries(INLINE_SAMPLES)) {
  const data = `samples/${name}.json`;
  await matches(
    join(ui, data),
    `${JSON.stringify(make(), null, 2)}\n`,
    "sample",
  );
  const valid = tuimark(["validate", "inline.tui", "--data", data, "--strict"]);
  if (valid.code !== 0 || valid.out.trim() !== "ok")
    failures.push(`validate ${name}: ${valid.out}${valid.err}`.trim());
  for (const cols of widths) {
    const args = [
      "dump",
      "inline.tui",
      "--data",
      data,
      "--cols",
      String(cols),
      "--rows",
      String(rows),
      "--inline",
    ];
    const json = JSON.parse(tuimark([...args, "--format", "json"]).out) as {
      errors: unknown[];
      rows: number;
      terminal_rows: number;
    };
    if (json.errors.length > 0)
      failures.push(`dump ${name} at ${cols}: ${JSON.stringify(json.errors)}`);
    if (json.terminal_rows !== rows || json.rows > json.terminal_rows)
      failures.push(
        `dump ${name} at ${cols}: region rows ${json.rows} of terminal ${json.terminal_rows}, expected within ${rows}`,
      );
    const text =
      tuimark([...args, "--format", "text"]).out.split("=== nodes ===")[0] ??
      "";
    await matches(join(ui, "golden", `${name}-${cols}.txt`), text, "golden");
  }
}

// A short terminal: the in-flow approval keeps its diff, the composer and
// the status in view at 15 rows.
{
  const args = [
    "dump",
    "inline.tui",
    "--data",
    "samples/inline-sample-approval-long.json",
    "--cols",
    "80",
    "--rows",
    "15",
    "--inline",
  ];
  const json = JSON.parse(tuimark([...args, "--format", "json"]).out) as {
    errors: unknown[];
    grid: string[];
    rows: number;
    terminal_rows: number;
  };
  if (json.errors.length > 0)
    failures.push(
      `dump inline approval at 15 rows: ${JSON.stringify(json.errors)}`,
    );
  const screen = json.grid.join("\n");
  for (const text of ["--- a/install.sh", "Generating…", "y allow"])
    if (!screen.includes(text))
      failures.push(`dump inline approval at 15 rows: no ${text}`);
  const text =
    tuimark([...args, "--format", "text"]).out.split("=== nodes ===")[0] ?? "";
  await matches(
    join(ui, "golden", "inline-sample-approval-long-80-r15.txt"),
    text,
    "golden",
  );
}

// Narrow and short terminals: 40/60 columns at 24 rows and 80 columns at 10
// rows, for the idle and approval samples of both documents. Each is a golden
// plus the facts that must survive the squeeze: at 40 columns the first hint
// (^P commands) is still on the status row (hints drop as a suffix, so the
// keymap lists commands first and the sheet caps the status text), and an
// approval 10 rows tall keeps its buttons and a diff line.
const extraDumps: {
  doc: string;
  sample: string;
  cols: number;
  rows: number;
  inline?: boolean;
  golden: string;
  contains?: string[];
}[] = [
  {
    doc: "app.tui",
    sample: "sample",
    cols: 40,
    rows: 24,
    golden: "sample-40-r24.txt",
    contains: ["^P commands", "┃"],
  },
  {
    doc: "app.tui",
    sample: "sample",
    cols: 60,
    rows: 24,
    golden: "sample-60-r24.txt",
    contains: ["^P commands", "esc cancel"],
  },
  {
    doc: "app.tui",
    sample: "sample-approval",
    cols: 40,
    rows: 24,
    golden: "sample-approval-40-r24.txt",
    contains: ["Deny", "Allow"],
  },
  {
    doc: "app.tui",
    sample: "sample-approval",
    cols: 60,
    rows: 24,
    golden: "sample-approval-60-r24.txt",
    contains: ["Deny", "Allow", "--- a/src/fetch.ts"],
  },
  {
    doc: "app.tui",
    sample: "sample",
    cols: 80,
    rows: 10,
    golden: "sample-80-r10.txt",
  },
  {
    doc: "app.tui",
    sample: "sample-approval",
    cols: 80,
    rows: 10,
    golden: "sample-approval-80-r10.txt",
    contains: ["Deny", "Allow", "--- a/src/fetch.ts"],
  },
  {
    doc: "app.tui",
    sample: "sample-empty",
    cols: 40,
    rows: 24,
    golden: "sample-empty-40-r24.txt",
    contains: ["/help lists keys", "^P commands"],
  },
  {
    doc: "inline.tui",
    sample: "inline-sample",
    cols: 40,
    rows: 24,
    inline: true,
    golden: "inline-sample-40-r24.txt",
    contains: ["^P commands"],
  },
  {
    doc: "inline.tui",
    sample: "inline-sample-approval",
    cols: 40,
    rows: 24,
    inline: true,
    golden: "inline-sample-approval-40-r24.txt",
    contains: ["y allow"],
  },
];
for (const d of extraDumps) {
  const args = [
    "dump",
    d.doc,
    "--data",
    `samples/${d.sample}.json`,
    "--cols",
    String(d.cols),
    "--rows",
    String(d.rows),
    ...(d.inline ? ["--inline"] : []),
  ];
  const label = `dump ${d.sample} at ${d.cols}x${d.rows}`;
  const json = JSON.parse(tuimark([...args, "--format", "json"]).out) as {
    errors: unknown[];
    grid: string[];
  };
  if (json.errors.length > 0)
    failures.push(`${label}: ${JSON.stringify(json.errors)}`);
  const screen = json.grid.join("\n");
  for (const text of d.contains ?? [])
    if (!screen.includes(text)) failures.push(`${label}: no ${text}`);
  const text =
    tuimark([...args, "--format", "text"]).out.split("=== nodes ===")[0] ?? "";
  await matches(join(ui, "golden", d.golden), text, "golden");
}

// The light theme gets one regression. Text goldens cannot see it (the glyphs
// are identical), so this goldens the `--styles` spans of a `--theme light`
// dump and asserts the light tokens paint the appbar; the @theme path a
// `/theme` command writes is asserted with play further down.
{
  const json = JSON.parse(
    tuimark([
      "dump",
      "app.tui",
      "--data",
      "samples/sample.json",
      "--cols",
      "80",
      "--rows",
      "10",
      "--theme",
      "light",
      "--styles",
      "--format",
      "json",
    ]).out,
  ) as { theme: string; styles: unknown; errors: unknown[] };
  if (json.errors.length > 0)
    failures.push(`dump sample light: ${JSON.stringify(json.errors)}`);
  if (json.theme !== "light")
    failures.push(`dump sample light: theme is ${json.theme}`);
  const styles = JSON.stringify(json.styles);
  if (!styles.includes("#eef1f4") || styles.includes("#1c2128"))
    failures.push("dump sample light: the appbar does not paint light tokens");
  await matches(
    join(ui, "golden", "sample-light-80.json"),
    `${JSON.stringify({ theme: json.theme, styles: json.styles }, null, 1)}\n`,
    "golden",
  );
}

// 4: every action fires, from the sample where its key is live. The event
// list is compared exactly: an extra event (a dialog_confirm next to the
// dialog_cancel that esc must fire) is a failure.
interface Expect {
  sample: string;
  /** The document to replay. Default app.tui. */
  doc?: string;
  /** Replay as an inline region, recording commits into the scrollback. */
  inline?: boolean;
  /** Space-separated play steps, or `steps` when a step holds a space or a line break. */
  input: string;
  /** Explicit steps (`tuimark play --script`), used instead of `input`. */
  steps?: object[];
  /** The keyboard protocol of the replay (`kitty` or `legacy`). */
  keyboard?: string;
  /** The terminal width of the replay. Default 80. */
  cols?: number;
  /** The terminal rows of the replay (the region's cap inline). */
  rows?: number;
  /** Every event the replay fires, in order. */
  actions: string[];
  /** The value of the last event. */
  value?: unknown;
  /** Text that must be on the final screen, and text that must not. */
  screen?: string[];
  notScreen?: string[];
  /** Text that must be in the committed scrollback, and text that must not. */
  scrollback?: string[];
  notScrollback?: string[];
  /** The exact number of scrollback rows the replay commits. */
  scrollbackRows?: number;
  /** A golden file under golden/ the scrollback grid must match. */
  scrollbackGolden?: string;
}
const pages = (n: number) => Array(n).fill("pgdn").join(" ");
const commitData = commitFixtures();
const plays: Expect[] = [
  {
    sample: "sample",
    input: "text:hello enter",
    actions: ["typed", "send"],
    value: "hello",
  },
  {
    sample: "sample",
    input: "text:/help enter",
    actions: ["typed", "send"],
    value: "/help",
  },
  { sample: "sample", input: "esc", actions: ["cancel"] },
  { sample: "sample", input: "ctrl+c", actions: ["quit_request"] },
  // The composer: enter sends, shift+enter and alt+enter break the line, a paste
  // keeps its breaks, and the message is one message.
  {
    sample: "sample",
    input: "text:one shift+enter text:two enter",
    actions: ["typed", "typed", "typed", "send"],
    value: "one\ntwo",
  },
  {
    sample: "sample",
    input: "text:one alt+enter text:two enter",
    actions: ["typed", "typed", "typed", "send"],
    value: "one\ntwo",
  },
  {
    sample: "sample",
    input: "",
    steps: [{ paste: "line one\nline two\r\nline three" }, { key: "enter" }],
    actions: ["typed", "send"],
    value: "line one\nline two\nline three",
  },
  { sample: "sample-draft", input: "enter", actions: ["send"], value: DRAFT },
  // up and down fall through to the history rows on the first and last line only.
  { sample: "sample", input: "up", actions: ["history_prev"], value: "" },
  { sample: "sample", input: "down", actions: ["history_next"] },
  { sample: "sample-draft", input: "up up", actions: [] },
  {
    sample: "sample-draft",
    input: "up up up",
    actions: ["history_prev"],
    value: DRAFT,
  },
  { sample: "sample-draft", input: "down", actions: ["history_next"] },
  // The status line offers the history keys where they would fire.
  { sample: "sample-draft", input: "up up", actions: [], screen: ["history"] },
  { sample: "sample-draft", input: "", actions: [], notScreen: ["history"] },
  // A draft past eight rows scrolls with the caret.
  {
    sample: "sample-draft-long",
    input: "",
    actions: [],
    screen: ["draft line 12"],
    notScreen: ["draft line 4"],
  },
  {
    sample: "sample-draft-long",
    input: Array(11).fill("up").join(" "),
    actions: [],
    screen: ["draft line 1", "draft line 8"],
    notScreen: ["draft line 12"],
  },
  {
    sample: "sample-draft-long",
    input: "enter",
    actions: ["send"],
    value: LONG_DRAFT,
  },
  // The same at 120 columns (every check above runs at 80).
  {
    sample: "sample",
    cols: 120,
    input: "text:one shift+enter text:two alt+enter text:three enter",
    actions: ["typed", "typed", "typed", "typed", "typed", "send"],
    value: "one\ntwo\nthree",
  },
  {
    sample: "sample-draft",
    cols: 120,
    input: "up up",
    actions: [],
    screen: ["history"],
  },
  {
    sample: "sample-draft",
    cols: 120,
    input: "up up up",
    actions: ["history_prev"],
    value: DRAFT,
  },
  {
    sample: "sample-draft-long",
    cols: 120,
    input: "",
    actions: [],
    screen: ["draft line 12"],
    notScreen: ["draft line 4"],
  },
  {
    sample: "sample-draft-long",
    cols: 120,
    input: Array(11).fill("up").join(" "),
    actions: [],
    screen: ["draft line 1", "draft line 8"],
    notScreen: ["draft line 12"],
  },
  {
    sample: "sample-draft-long",
    cols: 120,
    input: "enter",
    actions: ["send"],
    value: LONG_DRAFT,
  },
  { sample: "sample", input: "ctrl+p", actions: ["palette_open"] },
  // Active session grants are named in the header, and only then.
  {
    sample: "sample-grants",
    input: "",
    actions: [],
    screen: ["auto: files · bun test"],
  },
  { sample: "sample", input: "", actions: [], notScreen: ["auto:"] },
  { sample: "sample-approval", input: "y", actions: ["dialog_confirm"] },
  // `a` is this document's own dialog row (the kit's y/n are fixed): it
  // fires dialog_session, and the dialog spells out what it would grant.
  // (Play has no parent: armed/truncated refusal is controller-tested.)
  {
    sample: "sample-approval",
    input: "a",
    actions: ["dialog_session"],
    screen: ["a allow file changes this session"],
  },
  {
    sample: "sample-approval-command",
    input: "a",
    actions: ["dialog_session"],
    screen: ["a allow `bun test …` this session"],
  },
  {
    sample: "sample-approval-mcp",
    input: "a",
    actions: ["dialog_session"],
    screen: ["from server db", "a allow `db__query` this session"],
  },
  {
    sample: "sample-approval-truncated",
    input: "a",
    actions: ["dialog_session"],
    screen: ["preview truncated: session grant unavailable"],
  },
  { sample: "sample-approval", input: "n", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "esc", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "enter", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "ctrl+c", actions: ["quit_request"] },
  // The whole preview is reachable by key while the dialog is open.
  {
    sample: "sample-approval-long",
    input: "pgdn",
    actions: [],
    notScreen: [HIDDEN_LINE],
  },
  {
    sample: "sample-approval-long",
    input: "down down down",
    actions: [],
    screen: ["+echo step 3"],
  },
  {
    sample: "sample-approval-long",
    input: "end",
    actions: [],
    screen: ["+echo step 249"],
  },
  {
    sample: "sample-approval-long",
    input: "end home",
    actions: [],
    screen: ["--- a/install.sh"],
  },
  // A line as wide as the viewport is whole; a wider one wraps with a marker.
  {
    sample: "sample-approval-long",
    input: "home",
    actions: [],
    screen: [EXACT_LINE, WRAPPED_LINE.slice(0, 68)],
  },
  {
    sample: "sample-approval-long",
    input: pages(1),
    actions: [],
    screen: [`↪ ${WRAPPED_LINE.slice(68)}`],
  },
  {
    sample: "sample-approval-mcp",
    input: "end",
    actions: [],
    screen: ["zzz_last", "DELETE FROM accounts WHERE 1=1"],
  },
  {
    sample: "sample-approval-truncated",
    input: "end",
    actions: [],
    screen: ["more lines not shown"],
  },
  {
    sample: "sample-approval-truncated",
    input: "enter",
    actions: ["dialog_cancel"],
  },
  {
    sample: "sample-streaming",
    input: "focus:#earlier enter",
    actions: ["load_earlier"],
  },
  { sample: "sample-streaming", input: "ctrl+o", actions: ["load_earlier"] },
  {
    sample: "sample-palette",
    input: "text:x",
    actions: ["palette_query"],
    value: "modex",
  },
  {
    sample: "sample-palette",
    input: "enter",
    actions: ["palette_run"],
    value: "mode",
  },
  { sample: "sample-palette", input: "esc", actions: ["palette_close"] },
  {
    sample: "sample-help",
    rows: 30,
    input: "",
    actions: [],
    screen: [
      "Commands",
      "/mode plan|build",
      "/theme dark|light|auto",
      "/details",
      "/sessions",
      "enter send",
      "shift/alt+enter new line",
      "↑/↓ history",
    ],
  },
  { sample: "sample-help", input: "q", actions: ["help_close"] },
  { sample: "sample-help", input: "esc", actions: ["help_close"] },
  // The slash popup: every edit of the composer reaches the parent (that
  // is what opens and filters it), and while it is open its keys are its
  // own — up and down are not the history, esc is not cancel, and the
  // status hints stop promising them. (Play has no parent, so the choice
  // itself does not move here; the controller tests move it.)
  {
    sample: "sample",
    input: "",
    steps: [{ key: "/" }, { key: "m" }, { key: "o" }],
    actions: ["typed", "typed", "typed"],
    value: "/mo",
  },
  {
    sample: "sample-slash",
    input: "",
    actions: [],
    screen: ["› /mode", "/mouse", "/mcp", "tab complete"],
    notScreen: ["esc cancel", "↑/↓ history"],
  },
  {
    sample: "sample-slash",
    input: "down",
    actions: ["slash_next"],
    value: "/m",
  },
  { sample: "sample-slash", input: "up", actions: ["slash_prev"], value: "/m" },
  {
    sample: "sample-slash",
    input: "tab",
    actions: ["slash_complete"],
    value: "/m",
  },
  { sample: "sample-slash", input: "esc", actions: ["slash_close"] },
  { sample: "sample-slash", input: "enter", actions: ["send"], value: "/m" },
  // With the popup closed, tab is focus travel again and fires nothing.
  { sample: "sample", input: "tab", actions: [] },
  // Shift+tab toggles the mode: the composer does not take the key, and
  // a legacy terminal (CSI Z, no kitty protocol) delivers it too.
  { sample: "sample", input: "shift+tab", actions: ["mode_toggle"] },
  {
    sample: "sample",
    keyboard: "legacy",
    input: "shift+tab",
    actions: ["mode_toggle"],
  },
  {
    sample: "sample-slash",
    keyboard: "legacy",
    input: "tab",
    actions: ["slash_complete"],
    value: "/m",
  },
  // The details viewer: a modal over the latest tool receipt, its output
  // whole where the card kept eight lines. Every row is reachable by
  // key; esc and q close it (never cancel). "output line 2 " with its
  // trailing space cannot match line 20.
  {
    sample: "sample-details",
    input: "",
    actions: [],
    screen: [
      "read · succeeded",
      "src/big.txt",
      "output line 2 ",
      "esc or q close",
    ],
    notScreen: [DETAILS_HIDDEN],
  },
  {
    sample: "sample-details",
    input: "down down down",
    actions: [],
    screen: ["output line 4 ", DETAILS_HIDDEN],
    notScreen: ["output line 2 ", "output line 20"],
  },
  {
    sample: "sample-details",
    input: "pgdn",
    actions: [],
    screen: ["output line 20"],
  },
  {
    sample: "sample-details",
    input: "end",
    actions: [],
    screen: ["output line 20"],
    notScreen: ["output line 2 "],
  },
  {
    sample: "sample-details",
    input: "end home",
    actions: [],
    screen: ["output line 2 "],
    notScreen: ["output line 20"],
  },
  { sample: "sample-details", input: "q", actions: ["details_close"] },
  { sample: "sample-details", input: "esc", actions: ["details_close"] },
  { sample: "sample-details", input: "ctrl+c", actions: ["quit_request"] },
  // The read-only panel (/status here): the details viewer's keys under
  // its own class, every fact reachable (the MCP line is the last), and
  // q fires panel_close — never details_close.
  {
    sample: "sample-panel",
    input: "",
    actions: [],
    screen: ["Status", "Model: qwen3-coder:30b", "Session: a1b2c3d"],
    notScreen: ["MCP: 1 servers"],
  },
  {
    sample: "sample-panel",
    input: "pgdn",
    actions: [],
    screen: ["MCP: 1 servers"],
  },
  {
    sample: "sample-panel",
    input: "end home",
    actions: [],
    screen: ["Model: qwen3-coder:30b"],
    notScreen: ["MCP: 1 servers"],
  },
  { sample: "sample-panel", input: "q", actions: ["panel_close"] },
  { sample: "sample-panel", input: "esc", actions: ["panel_close"] },
  { sample: "sample-panel", input: "ctrl+c", actions: ["quit_request"] },
  // The /mcp panel: the count line heads it, tool names under the server.
  {
    sample: "sample-mcp",
    input: "",
    actions: [],
    screen: ["MCP servers: 1 · 2 tools", "db · 2 tools", "query, insert"],
  },
  { sample: "sample-mcp", input: "q", actions: ["panel_close"] },
  // The sessions picker: the arrows move the parent's choice (play has
  // no parent, so the choice itself does not move), enter asks to load,
  // esc and q close — the scroll never swallows an arrow.
  {
    sample: "sample-sessions",
    input: "",
    actions: [],
    screen: ["› a1b2c3d · 5 messages · current", "e4f5g6h · 12 messages"],
  },
  { sample: "sample-sessions", input: "down", actions: ["panel_next"] },
  { sample: "sample-sessions", input: "up", actions: ["panel_prev"] },
  { sample: "sample-sessions", input: "enter", actions: ["panel_run"] },
  { sample: "sample-sessions", input: "esc", actions: ["panel_close"] },
  { sample: "sample-sessions", input: "q", actions: ["panel_close"] },
  { sample: "sample-sessions", input: "ctrl+c", actions: ["quit_request"] },

  // ---------------------------------------------------------- the inline doc
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:hello enter",
    actions: ["typed", "send"],
    value: "hello",
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:one shift+enter text:two enter",
    actions: ["typed", "typed", "typed", "send"],
    value: "one\ntwo",
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "esc",
    actions: ["cancel"],
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "ctrl+c",
    actions: ["quit_request"],
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "ctrl+p",
    actions: ["palette_open"],
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "up",
    actions: ["history_prev"],
    value: "",
  },
  {
    sample: "inline-sample-draft-long",
    doc: "inline.tui",
    inline: true,
    input: "enter",
    actions: ["send"],
    value: LONG_DRAFT,
  },
  // The approval has no modal: the composer is disabled, so y allows, n and
  // esc deny, and typing (even enter) fires nothing at all.
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "y",
    actions: ["dialog_confirm"],
  },
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "a",
    actions: ["dialog_session"],
    screen: ["a allow file changes this session"],
  },
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "n",
    actions: ["dialog_cancel"],
  },
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["dialog_cancel"],
  },
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:hello enter",
    actions: [],
    screen: ["Edit src/fetch.ts?"],
  },
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "ctrl+c",
    actions: ["quit_request"],
  },
  // The whole in-flow preview is reachable by key; at the inline width the
  // 70-column line the dialog wraps is whole.
  {
    sample: "inline-sample-approval-long",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "down down down down down",
    actions: [],
    screen: [EXACT_LINE, WRAPPED_LINE],
  },
  {
    sample: "inline-sample-approval-long",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "end",
    actions: [],
    screen: ["+echo step 249"],
  },
  {
    sample: "inline-sample-approval-long",
    doc: "inline.tui",
    inline: true,
    rows: 15,
    input: "end",
    actions: [],
    screen: ["+echo step 249", "Generating…"],
  },
  {
    sample: "inline-sample-palette",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:x",
    actions: ["palette_query"],
    value: "modex",
  },
  {
    sample: "inline-sample-palette",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "enter",
    actions: ["palette_run"],
    value: "mode",
  },
  {
    sample: "inline-sample-palette",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["palette_close"],
  },
  {
    sample: "inline-sample-help",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    actions: [],
    screen: [
      "Commands",
      "/mode plan|build",
      "enter send",
      "shift/alt+enter new line",
    ],
  },
  {
    sample: "inline-sample-help",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "q",
    actions: ["help_close"],
  },
  {
    sample: "inline-sample-help",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["help_close"],
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "shift+tab",
    actions: ["mode_toggle"],
  },
  // The slash popup lives in the region: its keys fire there, its rows
  // render there, and nothing of it is ever committed.
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    steps: [{ key: "/" }, { key: "m" }],
    actions: ["typed", "typed"],
    value: "/m",
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-slash",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    actions: [],
    screen: ["› /mode", "/mcp", "tab complete"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-slash",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "down",
    actions: ["slash_next"],
    value: "/m",
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-slash",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "up",
    actions: ["slash_prev"],
    value: "/m",
  },
  {
    sample: "inline-sample-slash",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "tab",
    actions: ["slash_complete"],
    value: "/m",
  },
  {
    sample: "inline-sample-slash",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["slash_close"],
  },
  // The details viewer in flow: region state only (nothing committed),
  // esc closes it instead of cancelling, the composer stays usable, and
  // the output scrolls by page and arrow (home/end are the composer's).
  {
    sample: "inline-sample-details",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    actions: [],
    screen: ["read · succeeded", "output line 2 ", "esc close"],
    notScreen: [DETAILS_HIDDEN],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-details",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["details_close"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-details",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "down down down",
    actions: [],
    screen: ["output line 4 "],
    notScreen: ["output line 2 ", DETAILS_HIDDEN],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-details",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "pgdn",
    actions: [],
    screen: ["output line 20"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-details",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:hi enter",
    actions: ["typed", "send"],
    value: "hi",
    scrollbackRows: 0,
  },
  // The panel in flow: esc closes it instead of cancelling, its lines
  // scroll by page, and nothing is committed.
  {
    sample: "inline-sample-panel",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    actions: [],
    screen: ["Model: qwen3-coder:30b", "Workspace: /home/me/project"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-panel",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["panel_close"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-panel",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "pgdn",
    actions: [],
    screen: ["MCP: 1 servers"],
    scrollbackRows: 0,
  },
  // The picker in flow: the arrows at the empty draft's edges move the
  // choice, enter is the composer's send (the parent loads instead of
  // sending), esc closes without cancelling.
  {
    sample: "inline-sample-sessions",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "down",
    actions: ["panel_next"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-sessions",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "up",
    actions: ["panel_prev"],
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-sessions",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "enter",
    actions: ["send"],
    value: "",
    scrollbackRows: 0,
  },
  {
    sample: "inline-sample-sessions",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["panel_close"],
    scrollbackRows: 0,
  },
  // Commits print the finished views into the scrollback and leave the
  // region alone: a user turn, an assistant turn, a tool card, then plain
  // blocks with no second label.
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    steps: [
      { set: { path: "commit_buf", value: commitData.views[0] } },
      { commit: { template: "la-turn", with: "commit_buf as msg" } },
      { set: { path: "commit_buf", value: commitData.views[1] } },
      { commit: { template: "la-turn", with: "commit_buf as msg" } },
      { set: { path: "commit_buf", value: commitData.views[2] } },
      { commit: { template: "la-turn", with: "commit_buf as msg" } },
      { set: { path: "commit_blocks", value: commitData.blocks } },
      { commit: { template: "la-blocks", with: "commit_blocks as blocks" } },
    ],
    actions: [],
    screen: ["Message the model"],
    notScreen: ["Add a retry to the fetch helper"],
    scrollback: [
      "You",
      "Add a retry to the fetch helper",
      "Assistant",
      "Tool · edit",
      "Done. The helper now retries three times.",
    ],
    notScrollback: ["Generating…", "Thinking…"],
    scrollbackGolden: "inline-commit-80.txt",
  },
  // A cleared buffer commits nothing.
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    steps: [
      { set: { path: "commit_buf", value: null } },
      { commit: { template: "la-turn", with: "commit_buf as msg" } },
    ],
    actions: [],
    scrollbackRows: 0,
  },

  // ---------------------------------------------------------- S2 polish
  // ctrl+l jumps back to the newest message from wherever pgup left the log.
  // (`end` cannot: the composer takes end/ctrl+e for its caret, and ctrl+end
  // is not a key token — probed against the pinned binary.)
  {
    sample: "sample",
    input: "pgup pgup",
    actions: [],
    notScreen: ["Done. The helper now retries three times."],
  },
  {
    sample: "sample",
    input: "pgup pgup ctrl+l",
    actions: [],
    screen: ["Done. The helper now retries three times."],
  },
  // The welcome shows before the first message and goes when `empty` clears.
  {
    sample: "sample-empty",
    input: "",
    actions: [],
    screen: [
      "Local Agent",
      "qwen3-coder:30b",
      "/help lists keys",
      "ctrl+p opens commands",
    ],
  },
  {
    sample: "sample-empty",
    input: "",
    steps: [{ set: { path: "empty", value: false } }],
    actions: [],
    notScreen: ["/help lists keys"],
  },
  {
    sample: "inline-sample-empty",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    actions: [],
    screen: ["Local Agent · qwen3-coder:30b", "ctrl+p opens commands"],
    scrollbackRows: 0,
  },
  // The wheel scrolls the transcript while `mouse_on` is set (fullscreen
  // default), and clicks are inert with it off. Cells are the 0-based dump
  // grid: at 80x30 the approval's Deny/Allow row is y=23, Deny at x=58,
  // Allow at x=68.
  {
    sample: "sample-approval",
    rows: 30,
    input: "",
    steps: [{ set: { path: "mouse_on", value: false } }, { click: [68, 23] }],
    actions: [],
  },
  {
    sample: "sample-approval",
    rows: 30,
    input: "",
    steps: [{ set: { path: "mouse_on", value: true } }, { click: [68, 23] }],
    actions: ["dialog_confirm"],
  },
  {
    sample: "sample-approval",
    rows: 30,
    input: "",
    steps: [{ set: { path: "mouse_on", value: true } }, { click: [59, 23] }],
    actions: ["dialog_cancel"],
  },
  {
    sample: "sample",
    rows: 12,
    input: "",
    steps: [
      { set: { path: "mouse_on", value: true } },
      ...Array.from({ length: 40 }, () => ({ wheel: "up", at: [10, 5] })),
    ],
    actions: [],
    screen: ["Add a retry to the fetch helper"],
    notScreen: ["Done. The helper now retries three times."],
  },
  {
    sample: "sample",
    rows: 12,
    input: "",
    steps: [
      { set: { path: "mouse_on", value: false } },
      ...Array.from({ length: 40 }, () => ({ wheel: "up", at: [10, 5] })),
    ],
    actions: [],
    screen: ["Done. The helper now retries three times."],
    notScreen: ["Add a retry to the fetch helper"],
  },
];
const scratch = await mkdtemp(join(tmpdir(), "la-tuimark-check-"));
for (const check of plays) {
  let script: string[] = ["--input", check.input];
  if (check.steps) {
    const file = join(scratch, "steps.ndjson");
    await writeFile(
      file,
      `${check.steps.map((step) => JSON.stringify(step)).join("\n")}\n`,
    );
    script = ["--script", file];
  }
  const run = tuimark([
    "play",
    check.doc ?? "app.tui",
    "--data",
    `samples/${check.sample}.json`,
    "--cols",
    String(check.cols ?? 80),
    ...(check.rows ? ["--rows", String(check.rows)] : []),
    ...(check.keyboard ? ["--keyboard", check.keyboard] : []),
    ...(check.inline ? ["--inline"] : []),
    ...script,
    "--format",
    "json",
  ]);
  const label = `play ${check.sample} @${check.cols ?? 80} "${(check.steps ? JSON.stringify(check.steps) : check.input).slice(0, 40)}"`;
  try {
    const result = JSON.parse(run.out) as {
      events: { action: string; value: unknown }[];
      grid: string[];
      scrollback?: { grid: string[] };
    };
    const actions = result.events.map((e) => e.action);
    if (JSON.stringify(actions) !== JSON.stringify(check.actions))
      failures.push(
        `${label}: events [${actions}], expected [${check.actions}]`,
      );
    else if ("value" in check && result.events.at(-1)?.value !== check.value)
      failures.push(
        `${label}: value ${JSON.stringify(result.events.at(-1)?.value)}, expected ${JSON.stringify(check.value)}`,
      );
    const screen = result.grid.join("\n");
    for (const text of check.screen ?? [])
      if (!screen.includes(text))
        failures.push(`${label}: the screen does not show ${text}`);
    for (const text of check.notScreen ?? [])
      if (screen.includes(text))
        failures.push(`${label}: the screen shows ${text} too early`);
    const scrollbackGrid = result.scrollback?.grid ?? [];
    const scrollback = scrollbackGrid.join("\n");
    for (const text of check.scrollback ?? [])
      if (!scrollback.includes(text))
        failures.push(`${label}: the scrollback does not hold ${text}`);
    for (const text of check.notScrollback ?? [])
      if (scrollback.includes(text))
        failures.push(`${label}: the scrollback holds ${text}`);
    if (
      check.scrollbackRows !== undefined &&
      scrollbackGrid.length !== check.scrollbackRows
    )
      failures.push(
        `${label}: scrollback has ${scrollbackGrid.length} rows, expected ${check.scrollbackRows}`,
      );
    if (check.scrollbackGolden)
      await matches(
        join(ui, "golden", check.scrollbackGolden),
        `${scrollbackGrid.map((row) => row.trimEnd()).join("\n")}\n`,
        "golden",
      );
  } catch {
    failures.push(`${label}: ${run.err || run.out}`.trim());
  }
}

// The @theme write a `/theme` command sends flips the session in one frame:
// play's set step writes the same reserved path, and --styles shows the
// appbar repaint with light tokens.
{
  const file = join(scratch, "theme.ndjson");
  await writeFile(
    file,
    `${JSON.stringify({ set: { path: "@theme", value: "light" } })}\n`,
  );
  const run = tuimark([
    "play",
    "app.tui",
    "--data",
    "samples/sample.json",
    "--cols",
    "80",
    "--rows",
    "10",
    "--script",
    file,
    "--styles",
    "--format",
    "json",
  ]);
  const result = JSON.parse(run.out) as { theme: string; styles: unknown };
  if (result.theme !== "light")
    failures.push(`play set @theme: theme is ${result.theme}`);
  if (!JSON.stringify(result.styles).includes("#eef1f4"))
    failures.push("play set @theme: no light token painted");
}

// A palette row click selects that row (the accent bar moves), mouse on only.
// Row y=11 is "Mode: plan" (preselected), y=12 "Mode: build" at 80x30.
for (const on of [true, false]) {
  const steps = [{ set: { path: "mouse_on", value: on } }, { click: [20, 12] }];
  const file = join(scratch, "palette.ndjson");
  await writeFile(
    file,
    `${steps.map((step) => JSON.stringify(step)).join("\n")}\n`,
  );
  const run = tuimark([
    "play",
    "app.tui",
    "--data",
    "samples/sample-palette.json",
    "--cols",
    "80",
    "--rows",
    "30",
    "--script",
    file,
    "--styles",
    "--format",
    "json",
  ]);
  const result = JSON.parse(run.out) as {
    styles: { bg?: string }[][];
  };
  const accent = (row: number) =>
    result.styles[row]?.some((span) => span.bg === "#58a6ff") ?? false;
  const selectedRow = on ? 12 : 11;
  if (!accent(selectedRow) || accent(on ? 11 : 12))
    failures.push(
      `play palette click (mouse ${on ? "on" : "off"}): selection is not on row ${selectedRow}`,
    );
}

// The slash popup's chosen row is painted like the palette's selected
// item (the "›" marker in the text goldens, the accent fill here).
{
  const run = tuimark([
    "dump",
    "app.tui",
    "--data",
    "samples/sample-slash.json",
    "--cols",
    "80",
    "--rows",
    "30",
    "--styles",
    "--format",
    "json",
  ]);
  const result = JSON.parse(run.out) as {
    grid: string[];
    styles: { bg?: string }[][];
  };
  const accentRow = (text: string) => {
    const row = result.grid.findIndex((line) => line.includes(text));
    return (
      row >= 0 &&
      (result.styles[row]?.some((span) => span.bg === "#58a6ff") ?? false)
    );
  };
  if (!accentRow("› /mode") || accentRow("/mouse"))
    failures.push("dump sample-slash: the chosen row is not accent-filled");
}

await rm(scratch, { recursive: true, force: true });

// The command on line 240 of the long diff is reached by paging down.
{
  let reached = 0;
  for (let n = 1; n <= 40 && !reached; n++) {
    const run = tuimark([
      "play",
      "app.tui",
      "--data",
      "samples/sample-approval-long.json",
      "--input",
      pages(n),
      "--format",
      "json",
    ]);
    const grid = (JSON.parse(run.out) as { grid: string[] }).grid.join("\n");
    if (grid.includes(HIDDEN_LINE)) reached = n;
  }
  if (!reached)
    failures.push(`${HIDDEN_LINE} is not reachable by pgdn in the long diff`);
}

// Focus lands on Deny when the approval opens (the safe button).
const approval = JSON.parse(
  tuimark([
    "dump",
    "app.tui",
    "--data",
    "samples/sample-approval.json",
    "--cols",
    "80",
    "--rows",
    String(rows),
    "--format",
    "json",
  ]).out,
) as { focus: string };
if (approval.focus !== "approval__no")
  failures.push(`approval focus is ${approval.focus}, expected approval__no`);

// Focus lands on the details viewer's scroll when it opens.
const details = JSON.parse(
  tuimark([
    "dump",
    "app.tui",
    "--data",
    "samples/sample-details.json",
    "--cols",
    "80",
    "--rows",
    String(rows),
    "--format",
    "json",
  ]).out,
) as { focus: string };
if (details.focus !== "details_body")
  failures.push(`details focus is ${details.focus}, expected details_body`);

if (failures.length > 0) {
  console.error(failures.map((f) => `FAIL ${f}`).join("\n"));
  process.exit(1);
}
const sampleCount =
  Object.keys(SAMPLES).length + Object.keys(INLINE_SAMPLES).length;
console.log(
  `ok: ${sampleCount} samples validated, ${sampleCount * widths.length + 2 + extraDumps.length + 1} goldens, ${plays.length} play checks${update ? " (updated)" : ""}`,
);
