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
import { namedThemes, themedDocument } from "#cli/tuimark/themes";

const ui = join(dirname(import.meta.path), "ui");
const update = Bun.argv.includes("--update");
const rows = 30;
const widths = [80, 120];
const failures: string[] = [];

const bin = await resolvePinned();

/** How long one tuimark run may take before it counts as hung. */
const RUN_MS = 60_000;

/**
 * One tuimark run. A run that exits non-zero or hangs is a failure of its
 * own, with the end of its stderr (or of its stdout, where `validate`
 * writes its diagnostics), so a later check never reads its output as if
 * it had succeeded.
 */
function tuimark(args: string[]): { code: number; out: string; err: string } {
  const run = Bun.spawnSync([bin, ...args], { cwd: ui, timeout: RUN_MS });
  const result = {
    code: run.exitCode,
    out: run.stdout.toString(),
    err: run.stderr.toString(),
  };
  const what = `tuimark ${args.slice(0, 2).join(" ")}`;
  if (run.exitedDueToTimeout)
    failures.push(`${what}: no result after ${RUN_MS / 1000} s (killed)`);
  else if (result.code !== 0) {
    const why = result.err.trim() || result.out.trim();
    failures.push(
      `${what}: exit ${result.code}${why ? `: ${why.slice(-500)}` : ""}`,
    );
  }
  return result;
}

/** Prints every failure so far and exits; `reason` is why the run stops here. */
function finish(reason?: string): never {
  if (reason) failures.push(`${reason} (later checks did not run)`);
  console.error(failures.map((f) => `FAIL ${f}`).join("\n"));
  process.exit(1);
}

/**
 * tuimark's JSON output. Output that does not parse ends the check with
 * every failure found so far, instead of a stack trace that hides them.
 */
function parsed(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    finish(
      `tuimark printed no JSON: ${text.trim().slice(0, 200) || "(empty)"}`,
    );
  }
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

// The help's keys are plain rows grouped by where they work (a <hints
// scope="all"> lists every labeled row with no context), so nothing ties
// them to the keymap but this: every labeled row of a document has its
// keycap (or its first key) among the keys of that document's help. A
// row's `when` may hold a `>` (a child selector), so a tag ends at the
// first `>` outside quotes.
for (const doc of ["app.tui", "inline.tui"]) {
  const source = await Bun.file(join(ui, doc)).text();
  const start = source.indexOf('<modal id="help_dialog"');
  const help = source.slice(start, source.indexOf("</modal>", start));
  const shown = new Set(
    [...help.matchAll(/<text class="la-helpkey">([^<]*)<\/text>/g)].flatMap(
      ([, key = ""]) => [key, ...key.split("/")],
    ),
  );
  if (start < 0 || shown.size === 0)
    failures.push(`${doc}: no help keys to check the keymap against`);
  for (const [bind] of source.matchAll(
    /<bind\b(?:[^>"']|"[^"]*"|'[^']*')*>/g,
  )) {
    const attr = (name: string) =>
      bind.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1];
    if (!attr("label")) continue;
    const keycap = attr("keycap") || attr("keys")?.split(",")[0] || "";
    if (!shown.has(keycap))
      failures.push(
        `${doc}: the help lists no ${keycap} (${attr("label")}), a labeled keymap row`,
      );
  }
}

// 1 + 2 + 3: samples, validate, goldens.
for (const [name, make] of Object.entries(SAMPLES)) {
  const data = `samples/${name}.json`;
  await matches(
    join(ui, data),
    `${JSON.stringify(make(), null, 2)}\n`,
    "sample",
  );
  const valid = tuimark(["validate", "app.tui", "--data", data, "--strict"]);
  if (valid.code === 0 && valid.out.trim() !== "ok")
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
    const json = parsed(tuimark([...args, "--format", "json"]).out) as {
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

// Links and images in the rendered document (tuimark 0.5.1 and later: the
// data-bound href/src contract these samples are written against). A link
// paints OSC 8 cells whose `link` index is non-zero, and an image lays out an
// <img> node with a size. This fails on 0.5.0, where href/src stay literal.
{
  const links = parsed(
    tuimark([
      "dump",
      "app.tui",
      "--data",
      "samples/sample-links.json",
      "--cols",
      "80",
      "--rows",
      String(rows),
      "--format",
      "json",
      "--cells",
    ]).out,
  ) as { cells?: { link?: number }[] };
  if (!links.cells?.some((cell) => cell.link !== undefined && cell.link > 0))
    failures.push(
      "sample-links: no cell carries a hyperlink index (needs tuimark >= 0.5.1)",
    );
  const image = parsed(
    tuimark([
      "dump",
      "app.tui",
      "--data",
      "samples/sample-image.json",
      "--cols",
      "80",
      "--rows",
      String(rows),
      "--format",
      "json",
    ]).out,
  ) as { nodes: { tag: string; w: number; h: number }[] };
  const img = image.nodes.find((node) => node.tag === "img");
  if (!img || img.w < 1 || img.h < 1)
    failures.push(
      "sample-image: no laid-out <img> node (needs tuimark >= 0.5.1)",
    );
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
  if (valid.code === 0 && valid.out.trim() !== "ok")
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
    const json = parsed(tuimark([...args, "--format", "json"]).out) as {
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
  const json = parsed(tuimark([...args, "--format", "json"]).out) as {
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
  for (const text of [
    "--- a/install.sh",
    "Answer the approval first",
    "y allow",
  ])
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

// A tall terminal: while the help or the palette is open the inline region
// is that modal and nothing else, with the modal's content all inside it.
// So the spacer in inline.tcss is the modal's height, and the help's
// commands and keys fit unscrolled; a new command or key fails here until
// the spacer grows with it. Each modal is dumped from its sample and over
// each card the region can hold (a streaming reply, an approval, a
// question, the details, a panel, the slash popup), one at a time, since
// an approval would hide the live reply itself: a card that is not hidden
// shows at the modal's edges and grows the region.
{
  const idle: Record<string, unknown> = {
    ...INLINE_SAMPLES["inline-sample"](),
  };
  const help = INLINE_SAMPLES["inline-sample-help"]();
  const palette = INLINE_SAMPLES["inline-sample-palette"]();
  const modals = [
    ["inline-sample-help", "help_dialog", { help: help.help }],
    [
      "inline-sample-palette",
      "palette__dialog",
      { palette: palette.palette, found: palette.found },
    ],
  ] as const;
  const scratchCards = await mkdtemp(join(tmpdir(), "la-tuimark-cards-"));
  const cases: [string, string, string][] = modals.map(([sample, modal]) => [
    sample,
    modal,
    `samples/${sample}.json`,
  ]);
  for (const card of [
    "inline-sample-streaming",
    "inline-sample-approval",
    "inline-sample-question",
    "inline-sample-details",
    "inline-sample-panel",
    "inline-sample-slash",
  ] as const) {
    // A card's sample is a whole state: only what it changes from the idle
    // one is the card.
    const changed = Object.fromEntries(
      Object.entries(INLINE_SAMPLES[card]()).filter(
        ([key, value]) => JSON.stringify(value) !== JSON.stringify(idle[key]),
      ),
    );
    for (const [sample, modal, open] of modals) {
      const file = join(scratchCards, `${sample}-over-${card}.json`);
      await writeFile(
        file,
        JSON.stringify({ ...idle, ...changed, help: idle.help, ...open }),
      );
      cases.push([`${sample} over ${card}`, modal, file]);
    }
  }
  for (const [label, modal, data] of cases)
    for (const cols of [40, 80, 120]) {
      const json = parsed(
        tuimark([
          "dump",
          "inline.tui",
          "--data",
          data,
          "--cols",
          String(cols),
          "--rows",
          "80",
          "--inline",
          "--format",
          "json",
        ]).out,
      ) as {
        errors: unknown[];
        rows: number;
        nodes: { id?: string; tag: string; y: number; h: number }[];
      };
      const at = json.nodes.findIndex((node) => node.id === modal);
      const box = json.nodes[at];
      // The open modal is the screen's last laid-out child (a closed modal
      // is not in the dump), so the nodes after it are its own.
      const after = json.nodes.slice(at + 1);
      const bottom = Math.max(...after.map((node) => node.y + node.h));
      const where = `dump ${label} at ${cols}x80`;
      if (json.errors.length > 0 || !box)
        failures.push(
          `${where}: ${JSON.stringify(json.errors)}${box ? "" : ` no #${modal}`}`,
        );
      else if (after.some((node) => node.tag === "modal"))
        failures.push(`${where}: another modal is laid out after #${modal}`);
      else if (box.y !== 0 || box.h !== json.rows || bottom > box.h - 1)
        failures.push(
          `${where}: #${modal} is ${box.h} rows at ${box.y} with content to ${bottom}, in a region of ${json.rows}: the region must be the modal alone (inline.tcss hides the rest; its spacer is the modal's height)`,
        );
    }
  await rm(scratchCards, { recursive: true, force: true });
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
    // At rest: no esc hint (nothing to cancel), and room for the model.
    contains: ["^P commands", "qwen3-coder:30b"],
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
  // Before it arms, the narrow status says only that n and esc deny: the
  // hints, which would name a and ! there, give the row to the status.
  {
    doc: "app.tui",
    sample: "sample-approval-unarmed",
    cols: 60,
    rows: 24,
    golden: "sample-approval-unarmed-60-r24.txt",
    contains: ["Waiting for approval · n deny · esc deny"],
  },
  // The status names no key while a question waits: the box's hint does,
  // and at 60 columns its longest one still keeps esc skip.
  {
    doc: "app.tui",
    sample: "sample-question-text",
    cols: 60,
    rows: 24,
    golden: "sample-question-text-60-r24.txt",
    contains: ["Waiting for your answer", "esc skip"],
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
  // A status longer than its row ends in an ellipsis at a word: a fixed
  // width cut it mid-word, with nothing to say it went on.
  {
    doc: "app.tui",
    sample: "site-goal-done",
    cols: 64,
    rows: 22,
    golden: "site-goal-done-64-r22.txt",
    contains: ["evidence: `bun test src/fetch.test.ts`…"],
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
  const json = parsed(tuimark([...args, "--format", "json"]).out) as {
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
  const json = parsed(
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

// The dark goldens are text too, so they cannot see app.tcss re-declare the
// kit's dark muted, subtle and danger (for contrast, themes.test.ts). The
// palette sample paints all three: with app.tcss's values, never the kit's.
{
  const json = parsed(
    tuimark([
      "dump",
      "app.tui",
      "--data",
      "samples/sample-palette.json",
      "--cols",
      "80",
      "--rows",
      "24",
      "--styles",
      "--format",
      "json",
    ]).out,
  ) as { styles: unknown; errors: unknown[] };
  if (json.errors.length > 0)
    failures.push(`dump palette dark: ${JSON.stringify(json.errors)}`);
  const styles = JSON.stringify(json.styles);
  for (const ours of ["#939ca5", "#8a929c", "#f85c55"])
    if (!styles.includes(ours))
      failures.push(`dump palette dark: app.tcss's ${ours} is not painted`);
  for (const kits of ["#6e7681", "#f85149"])
    if (styles.includes(kits))
      failures.push(`dump palette dark: the kit's ${kits} is painted`);
}

// A named theme is a generated copy of the document that loads one more sheet
// (themes.ts); themes.test.ts reads the sheets, this proves the host loads
// each one. Pinned to dark as launch.ts pins it, every color either document
// paints is one of the theme's own tokens: a kit or app.tcss value that
// shows through means a token the theme leaves out.
for (const name of namedThemes(join(ui, "themes"))) {
  const sheet = await Bun.file(join(ui, "themes", `${name}.tcss`)).text();
  const values = new Set(
    [...sheet.matchAll(/--[\w-]+:\s*(#[0-9a-f]{6});/gu)].map(([, v]) => v),
  );
  for (const [doc, sample, inline] of [
    ["app.tui", "sample-palette", false],
    ["inline.tui", "inline-sample-palette", true],
  ] as const) {
    const themed = await themedDocument(ui, doc, name);
    const label = `dump ${sample} --theme ${name}`;
    try {
      const json = parsed(
        tuimark([
          "dump",
          themed,
          "--data",
          `samples/${sample}.json`,
          "--cols",
          "80",
          "--rows",
          "24",
          "--theme",
          "dark",
          ...(inline ? ["--inline"] : []),
          "--styles",
          "--format",
          "json",
        ]).out,
      ) as {
        styles: { fg?: string; bg?: string }[][];
        errors: unknown[];
      };
      if (json.errors.length > 0)
        failures.push(`${label}: ${JSON.stringify(json.errors)}`);
      const painted = new Set(
        json.styles.flat().flatMap(({ fg, bg }) => [fg, bg].filter(Boolean)),
      );
      const foreign = [...painted].filter((color) => !values.has(color));
      if (foreign.length > 0)
        failures.push(`${label}: paints ${foreign.join(", ")}, not its own`);
      if (painted.size < 5)
        failures.push(`${label}: paints only ${painted.size} colors`);
    } finally {
      await rm(dirname(themed), { recursive: true, force: true });
    }
  }
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
  // esc cancels a running turn, and its hint shows only then: at rest it
  // has nothing to cancel, fires nothing and is not offered. ^O earlier
  // likewise shows only when earlier messages are there to load.
  {
    sample: "sample-streaming",
    input: "esc",
    cols: 120,
    actions: ["cancel"],
    screen: ["esc cancel", "^O earlier"],
  },
  {
    sample: "sample",
    input: "esc",
    actions: [],
    notScreen: ["esc cancel", "^O earlier"],
  },
  // ctrl+y copies the last reply (the composer does not take it), and its
  // hint is second on the status line at rest, so it survives a narrow
  // terminal.
  {
    sample: "sample",
    input: "ctrl+y",
    actions: ["copy_reply"],
    screen: ["^P commands", "^Y copy"],
  },
  { sample: "sample", input: "ctrl+c", actions: ["quit_request"] },
  // The welcome names the native selection (a modifier-drag while mouse
  // reporting is on) and /copy.
  {
    sample: "sample-empty",
    input: "",
    actions: [],
    screen: ["shift-drag selects text", "/copy copies the last reply"],
  },
  // One indicator, in the live assistant message: a spinner and the phase
  // with the elapsed time beside "Assistant", never a second one in the
  // status bar, the placeholder or a "Thinking…" block of its own.
  {
    sample: "sample-streaming",
    input: "",
    actions: [],
    screen: ["Assistant", "12s", "Type to queue a message"],
    notScreen: ["Generating", "Thinking", "Waiting for the model"],
  },
  {
    sample: "sample-thinking",
    input: "",
    actions: [],
    screen: ["thinking · 7s", "I should read src/fetch.ts first"],
    notScreen: ["Generating", "Thinking…"],
  },
  {
    sample: "sample-question",
    input: "",
    actions: [],
    screen: ["⠿  waiting for your answer · 12s"],
  },
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
  // The hints name `a` and `!` only while the client would take them; the
  // keys still fire, so the client can say why it refuses.
  {
    sample: "sample-approval",
    input: "",
    cols: 120,
    actions: [],
    screen: ["a session  ! always  pgup/pgdn scroll"],
  },
  {
    sample: "sample-approval-truncated",
    input: "",
    cols: 120,
    actions: [],
    screen: ["esc deny   pgup/pgdn scroll"],
    notScreen: ["a session", "! always"],
  },
  {
    sample: "sample-approval-truncated",
    input: "!",
    actions: ["dialog_always"],
  },
  {
    sample: "sample-approval-sandbox",
    input: "",
    cols: 120,
    actions: [],
    screen: ["esc deny   ! always  pgup/pgdn scroll"],
    notScreen: ["a session"],
  },
  {
    sample: "sample-approval-sandbox",
    input: "a",
    actions: ["dialog_session"],
    screen: ["sandbox on: session grant unavailable"],
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
    // Every command is a row of the help, and the keys follow them.
    rows: 31,
    input: "",
    actions: [],
    screen: [
      "Commands",
      "/mode plan|build",
      "/theme dark|light|auto",
      "/copy [code]",
      "/details",
      "/sessions",
      "Keys",
      "send · shift/alt+enter new line",
      "select text (mouse reporting is on)",
      "cancel the turn, and a running goal",
    ],
  },
  { sample: "sample-help", input: "q", actions: ["help_close"] },
  { sample: "sample-help", input: "esc", actions: ["help_close"] },
  // A short terminal scrolls the commands and the keys together: the last
  // key is below at first, and three pages down the first command is above.
  {
    sample: "sample-help",
    rows: 15,
    input: "",
    actions: [],
    screen: ["? close help", "/mode plan|build"],
    notScreen: ["its top or its end"],
  },
  {
    sample: "sample-help",
    rows: 15,
    input: "pgdn pgdn pgdn",
    actions: [],
    screen: ["? close help", "In an approval", "its top or its end"],
    notScreen: ["/mode plan|build"],
  },
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
  // An ask_user question: the box names its choices and keys; the arrows
  // move the choice and esc skips it (never the history, never cancel);
  // enter, a typed number or a typed answer is the composer's send.
  {
    sample: "sample-question",
    input: "",
    actions: [],
    screen: [
      "Question · choose one",
      "1 Postgres",
      "2 SQLite",
      "the existing database",
      "Which store should the cache use?",
      "waiting for your answer",
      "esc skip",
    ],
    notScreen: ["esc cancel", "↑/↓ history"],
  },
  { sample: "sample-question", input: "down", actions: ["question_next"] },
  { sample: "sample-question", input: "up", actions: ["question_prev"] },
  { sample: "sample-question", input: "esc", actions: ["question_skip"] },
  { sample: "sample-question", input: "enter", actions: ["send"], value: "" },
  {
    sample: "sample-question",
    input: "text:2 enter",
    actions: ["typed", "send"],
    value: "2",
  },
  {
    sample: "sample-question-text",
    input: "text:8080 enter",
    actions: ["typed", "send"],
    value: "8080",
    screen: ["type the answer · enter send", "Which port does the staging"],
  },
  {
    sample: "sample-question-text",
    input: "esc",
    actions: ["question_skip"],
  },
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
    sample: "inline-sample-question",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "down",
    actions: ["question_next"],
    screen: ["1 Postgres", "esc skip"],
  },
  {
    sample: "inline-sample-question",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "esc",
    actions: ["question_skip"],
  },
  {
    sample: "inline-sample-question",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "text:y enter",
    actions: ["typed", "send"],
    value: "y",
  },
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
  // Inline too, esc cancels only a running turn.
  {
    sample: "inline-sample-streaming",
    doc: "inline.tui",
    inline: true,
    input: "esc",
    actions: ["cancel"],
    screen: ["esc cancel"],
  },
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "esc",
    actions: [],
    notScreen: ["esc cancel"],
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
  // As in the fullscreen dialog, `a` and `!` are named only while offered.
  {
    sample: "inline-sample-approval",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    cols: 120,
    input: "",
    actions: [],
    screen: ["y allow  a session  ! always  n deny"],
  },
  {
    sample: "inline-sample-approval-truncated",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    cols: 120,
    input: "",
    actions: [],
    screen: ["esc deny   y allow  n deny"],
    notScreen: ["a session", "! always"],
  },
  {
    sample: "inline-sample-approval-truncated",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "a",
    actions: ["dialog_session"],
  },
  {
    sample: "inline-sample-approval-truncated",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "!",
    actions: ["dialog_always"],
  },
  {
    sample: "inline-sample-approval-sandbox",
    doc: "inline.tui",
    inline: true,
    rows: 30,
    cols: 120,
    input: "",
    actions: [],
    screen: ["esc deny   y allow  ! always  n deny"],
    notScreen: ["a session"],
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
    screen: ["+echo step 249", "Answer the approval first"],
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
    notScreen: ["Local Agent", "Message the model", "build · last"],
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
      "/copy [code]",
      "Keys",
      "send · shift/alt+enter new line",
    ],
    // The region is the help alone: nothing shows at its edges.
    notScreen: ["Local Agent", "Message the model", "build · last"],
  },
  // A short terminal scrolls the commands and the keys together: the last
  // key is below at first, and two pages down the first command is above.
  {
    sample: "inline-sample-help",
    doc: "inline.tui",
    inline: true,
    rows: 15,
    input: "",
    actions: [],
    screen: ["? close help", "/mode plan|build"],
    notScreen: ["its top or its end"],
  },
  {
    sample: "inline-sample-help",
    doc: "inline.tui",
    inline: true,
    rows: 15,
    input: "pgdn pgdn",
    actions: [],
    screen: ["? close help", "In an approval", "its top or its end"],
    notScreen: ["/mode plan|build"],
  },
  // The composer is back, and focused, once the help or the palette closes.
  ...(
    [
      // The help takes no typing; the palette's query takes it.
      ["inline-sample-help", "help.open", "help_close", []],
      [
        "inline-sample-palette",
        "palette.open",
        "palette_close",
        ["palette_query"],
      ],
    ] as const
  ).map(([sample, path, close, typing]) => ({
    sample,
    doc: "inline.tui",
    inline: true,
    rows: 30,
    input: "",
    // A key typed while the modal is open is not the draft's; the set
    // stands in for the controller, which answers the close event so.
    steps: [
      { key: "x" },
      { key: "esc" },
      { set: { path, value: false } },
      { key: "h" },
      { key: "i" },
    ],
    actions: [...typing, close, "typed", "typed"],
    value: "hi",
    screen: ["Local Agent", "build · last"],
  })),
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
  {
    sample: "inline-sample",
    doc: "inline.tui",
    inline: true,
    input: "ctrl+y",
    actions: ["copy_reply"],
    screen: ["^Y copy"],
  },
  {
    sample: "inline-sample-thinking",
    doc: "inline.tui",
    inline: true,
    input: "",
    actions: [],
    screen: ["Assistant", "thinking · 7s", "Type to queue a message"],
    notScreen: ["Generating", "Thinking…"],
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
      "Edit",
      "✓ applied",
      "Done. The helper now retries three times.",
    ],
    notScrollback: ["waiting for the model", "Type to queue a message"],
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
  const result = parsed(run.out) as { theme: string; styles: unknown };
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
  const result = parsed(run.out) as {
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
  const result = parsed(run.out) as {
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
    const grid = (parsed(run.out) as { grid: string[] }).grid.join("\n");
    if (grid.includes(HIDDEN_LINE)) reached = n;
  }
  if (!reached)
    failures.push(`${HIDDEN_LINE} is not reachable by pgdn in the long diff`);
}

// Focus lands on Deny when the approval opens (the safe button).
const approval = parsed(
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
const details = parsed(
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

if (failures.length > 0) finish();
const sampleCount =
  Object.keys(SAMPLES).length + Object.keys(INLINE_SAMPLES).length;
console.log(
  `ok: ${sampleCount} samples validated, ${sampleCount * widths.length + 2 + extraDumps.length + 1} goldens, ${plays.length} play checks${update ? " (updated)" : ""}`,
);
