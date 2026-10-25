// Writes the website's terminal screens, docs/.vitepress/theme/screens.json:
// the hero and site samples (fixtures.ts) as the Tuimark client draws them in
// its dark theme, every row as text and color runs, stamped with the CLI
// version. It also writes themes.json, the configuration page's gallery: one
// sample in every theme, builtin and named. The site shows the released
// client, so this runs in a release commit; site-screens.test.ts fails until
// both stamps are the CLI's version.
//
//   bun run site:screens

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SAMPLES } from "#cli/tuimark/fixtures";
import { resolvePinned } from "#cli/tuimark/launch";
import {
  type Run as Runs,
  type StyledDump,
  styledRuns,
} from "#cli/tuimark/runs";
import { namedThemes, themedDocument } from "#cli/tuimark/themes";

// import.meta.url, not import.meta.path: vitest imports this module too.
const here = dirname(fileURLToPath(import.meta.url));
const ui = join(here, "ui");
const root = join(here, "../../../..");
const out = join(root, "docs/.vitepress/theme/screens.json");
const galleryOut = join(root, "docs/.vitepress/theme/themes.json");

type Sample = keyof typeof SAMPLES;
type Scene = {
  /** The frames a scene plays, in order; the last is its resting look. */
  frames: Sample[];
  /** Its screens, widest first: the page shows the widest that reads. */
  screens: { cols: number; rows: number }[];
};

/** Each scene of the home page and the screens it plays on. */
export const SCENES: Record<string, Scene> = {
  hero: {
    frames: ["hero-thinking", "hero-cards", "hero-approval"],
    screens: [
      { cols: 80, rows: 26 },
      { cols: 56, rows: 26 },
    ],
  },
  provider: {
    frames: ["site-provider"],
    screens: [
      { cols: 64, rows: 18 },
      { cols: 48, rows: 22 },
    ],
  },
  approval: {
    frames: ["site-approval"],
    screens: [
      { cols: 64, rows: 22 },
      { cols: 48, rows: 24 },
    ],
  },
  question: {
    frames: ["site-question"],
    screens: [
      { cols: 64, rows: 20 },
      { cols: 48, rows: 22 },
    ],
  },
  goal: {
    frames: ["site-goal-first", "site-goal-test", "site-goal-done"],
    screens: [
      { cols: 64, rows: 22 },
      { cols: 48, rows: 24 },
    ],
  },
  tools: {
    frames: ["site-tool-approval", "site-tool-result"],
    screens: [
      { cols: 64, rows: 26 },
      { cols: 48, rows: 28 },
    ],
  },
  subagents: {
    frames: ["site-subagents-running", "site-subagents-done"],
    screens: [
      { cols: 64, rows: 26 },
      { cols: 48, rows: 26 },
    ],
  },
  search: {
    frames: ["site-search", "site-search-viewer"],
    screens: [
      { cols: 64, rows: 24 },
      { cols: 48, rows: 26 },
    ],
  },
};

/** The theme gallery: this sample at this size, in every theme. */
export const GALLERY = { sample: "hero-cards" as Sample, cols: 72, rows: 22 };

/** The gallery's themes in its order: the builtin two, then the named ones. */
export const galleryThemes = (): string[] => [
  "dark",
  "light",
  ...namedThemes(join(ui, "themes")),
];

/** A run's attributes, as bits: what the page draws besides its colors. */
type Run = Runs<number>;

/** A color table: each color's index, added the first time it is seen. */
function palette(): { colors: string[]; color: (value?: string) => number } {
  const colors: string[] = [];
  return {
    colors,
    color: (value = "") => {
      const at = colors.indexOf(value);
      return at >= 0 ? at : colors.push(value) - 1;
    },
  };
}

/**
 * One frame: `name` drawn at cols x rows, as runs of each row: app.tui in the
 * dark theme, or `doc` (a themed copy) in the host `theme` given.
 */
async function frame(
  bin: string,
  scratch: string,
  name: Sample,
  cols: number,
  rows: number,
  color: (value?: string) => number,
  doc = "app.tui",
  theme = "dark",
): Promise<Run[][]> {
  // A sample that lays its text out to the terminal takes the width it is
  // drawn at; the others ignore it.
  const make = SAMPLES[name] as (cols?: number) => unknown;
  const data = join(scratch, `${name}-${cols}-${theme}.json`);
  await Bun.write(data, JSON.stringify(make(cols)));
  const run = Bun.spawnSync(
    [
      bin,
      "dump",
      doc,
      "--data",
      data,
      "--cols",
      String(cols),
      "--rows",
      String(rows),
      "--theme",
      theme,
      "--styles",
      "--format",
      "json",
    ],
    { cwd: ui, timeout: 60_000 },
  );
  if (run.exitCode !== 0)
    throw new Error(`tuimark dump ${name}: ${run.stderr.toString()}`);
  const dump = JSON.parse(run.stdout.toString()) as StyledDump;
  if (dump.errors.length > 0)
    throw new Error(`tuimark dump ${name}: ${JSON.stringify(dump.errors)}`);
  return styledRuns(dump, name, color);
}

/** The background most of a frame's cells have: its screen's own. */
function screenColor(lines: Run[][]): number {
  const cells = new Map<number, number>();
  for (const runs of lines)
    for (const [text, , bg] of runs)
      cells.set(bg, (cells.get(bg) ?? 0) + Array.from(text).length);
  return [...cells].sort((a, b) => b[1] - a[1])[0]?.[0] ?? 0;
}

/** Rows one a line, so a change to the client reads as a change to its rows. */
const rowsText = (lines: Run[][], indent: string) =>
  lines.map((line) => `${indent}${JSON.stringify(line)}`).join(",\n");

// Imported (site-screens.test.ts reads SCENES), it draws nothing.
if (import.meta.main) {
  const bin = await resolvePinned();
  const scratch = await mkdtemp(join(tmpdir(), "la-screens-"));
  const scenes: [string, string][] = [];
  const home = palette();
  const gallery = palette();
  const themes: string[] = [];
  try {
    for (const [scene, { frames, screens }] of Object.entries(SCENES)) {
      const drawn = [];
      for (const { cols, rows } of screens) {
        const lines = [];
        for (const name of frames)
          lines.push({
            name,
            lines: await frame(bin, scratch, name, cols, rows, home.color),
          });
        drawn.push({ cols, rows, frames: lines });
      }
      const body = drawn
        .map(
          (screen) =>
            `      {"cols": ${screen.cols}, "rows": ${screen.rows}, "frames": [\n${screen.frames
              .map(
                (f) =>
                  `        {"name": ${JSON.stringify(f.name)}, "lines": [\n${rowsText(f.lines, "          ")}\n        ]}`,
              )
              .join(",\n")}\n      ]}`,
        )
        .join(",\n");
      scenes.push([scene, `{"screens": [\n${body}\n    ]}`]);
    }
    // The gallery: a builtin theme is the host's own --theme; a named one is
    // a themed copy of the document on a host pinned to dark, as launch.ts
    // runs it.
    const { sample, cols, rows } = GALLERY;
    for (const name of galleryThemes()) {
      const builtin = name === "dark" || name === "light";
      const doc = builtin
        ? "app.tui"
        : await themedDocument(
            ui,
            "app.tui",
            name,
            undefined,
            async () => scratch,
          );
      const lines = await frame(
        bin,
        scratch,
        sample,
        cols,
        rows,
        gallery.color,
        doc,
        builtin ? name : "dark",
      );
      themes.push(
        `    {"name": ${JSON.stringify(name)}, "screen": ${screenColor(lines)}, "lines": [\n${rowsText(lines, "      ")}\n    ]}`,
      );
    }
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }

  const { version } = await Bun.file(
    join(root, "apps/cli/package.json"),
  ).json();
  await Bun.write(
    out,
    `{\n  "version": ${JSON.stringify(version)},\n  "theme": "dark",\n  "colors": ${JSON.stringify(home.colors)},\n  "scenes": {\n${scenes
      .map(([scene, text]) => `    ${JSON.stringify(scene)}: ${text}`)
      .join(",\n")}\n  }\n}\n`,
  );
  await Bun.write(
    galleryOut,
    `{\n  "version": ${JSON.stringify(version)},\n  "sample": ${JSON.stringify(GALLERY.sample)},\n  "cols": ${GALLERY.cols},\n  "rows": ${GALLERY.rows},\n  "colors": ${JSON.stringify(gallery.colors)},\n  "themes": [\n${themes.join(",\n")}\n  ]\n}\n`,
  );
  console.log(`screens: ${out} (local-agent ${version})`);
  console.log(`themes: ${galleryOut} (${themes.length} themes)`);
}
