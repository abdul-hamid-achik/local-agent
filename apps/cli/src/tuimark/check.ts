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
  /** The terminal width of the replay. Default 80. */
  cols?: number;
  /** The terminal rows of the replay (the region's cap inline). */
  rows?: number;
  /** Every event the replay fires, in order. */
  actions: string[];
  /** The value of the first event. */
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
    actions: ["send"],
    value: "hello",
  },
  {
    sample: "sample",
    input: "text:/help enter",
    actions: ["send"],
    value: "/help",
  },
  { sample: "sample", input: "esc", actions: ["cancel"] },
  { sample: "sample", input: "ctrl+c", actions: ["quit"] },
  // The composer: enter sends, shift+enter and alt+enter break the line, a paste
  // keeps its breaks, and the message is one message.
  {
    sample: "sample",
    input: "text:one shift+enter text:two enter",
    actions: ["send"],
    value: "one\ntwo",
  },
  {
    sample: "sample",
    input: "text:one alt+enter text:two enter",
    actions: ["send"],
    value: "one\ntwo",
  },
  {
    sample: "sample",
    input: "",
    steps: [{ paste: "line one\nline two\r\nline three" }, { key: "enter" }],
    actions: ["send"],
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
    actions: ["send"],
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
  { sample: "sample-approval", input: "y", actions: ["dialog_confirm"] },
  { sample: "sample-approval", input: "n", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "esc", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "enter", actions: ["dialog_cancel"] },
  { sample: "sample-approval", input: "ctrl+c", actions: ["quit"] },
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
    input: "",
    actions: [],
    screen: ["enter send", "shift/alt+enter new line", "↑/↓ history"],
  },
  { sample: "sample-help", input: "q", actions: ["help_close"] },
  { sample: "sample-help", input: "esc", actions: ["help_close"] },

  // ---------------------------------------------------------- the inline doc
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:hello enter",
    actions: ["send"],
    value: "hello",
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:one shift+enter text:two enter",
    actions: ["send"],
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
    actions: ["quit"],
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
    actions: ["quit"],
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
    screen: ["enter send", "shift/alt+enter new line"],
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
    else if ("value" in check && result.events[0]?.value !== check.value)
      failures.push(
        `${label}: value ${JSON.stringify(result.events[0]?.value)}, expected ${JSON.stringify(check.value)}`,
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

if (failures.length > 0) {
  console.error(failures.map((f) => `FAIL ${f}`).join("\n"));
  process.exit(1);
}
const sampleCount =
  Object.keys(SAMPLES).length + Object.keys(INLINE_SAMPLES).length;
console.log(
  `ok: ${sampleCount} samples validated, ${sampleCount * widths.length + 2} goldens, ${plays.length} play checks${update ? " (updated)" : ""}`,
);
