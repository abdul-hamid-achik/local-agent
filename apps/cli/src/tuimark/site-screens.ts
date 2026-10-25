// Writes the website's terminal screens, docs/.vitepress/theme/screens.json:
// the hero and site samples (fixtures.ts) as the Tuimark client draws them in
// its dark theme, every row as text and color runs, stamped with the CLI
// version. The site shows the released client, so this runs in a release
// commit; site-screens.test.ts fails until the stamp is the CLI's
// version.
//
//   bun run site:screens

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SAMPLES } from "#cli/tuimark/fixtures";
import { resolvePinned } from "#cli/tuimark/launch";

// import.meta.url, not import.meta.path: vitest imports this module too.
const here = dirname(fileURLToPath(import.meta.url));
const ui = join(here, "ui");
const root = join(here, "../../../..");
const out = join(root, "docs/.vitepress/theme/screens.json");

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
};

/** A run's attributes, as bits: what the page draws besides its colors. */
const FLAGS: Record<string, number> = {
  bold: 1,
  dim: 2,
  italic: 4,
  underline: 8,
};

type Dump = {
  errors: unknown[];
  grid: string[];
  styles: { x: number; w: number; fg?: string; bg?: string; a?: string[] }[][];
};
type Run = [text: string, fg: number, bg: number, flags: number];

const colors: string[] = [];
const color = (value = "") => {
  const at = colors.indexOf(value);
  return at >= 0 ? at : colors.push(value) - 1;
};

/** One frame: `name` drawn at cols x rows, as runs of each row. */
async function frame(
  bin: string,
  scratch: string,
  name: Sample,
  cols: number,
  rows: number,
): Promise<Run[][]> {
  // A sample that lays its text out to the terminal takes the width it is
  // drawn at; the others ignore it.
  const make = SAMPLES[name] as (cols?: number) => unknown;
  const data = join(scratch, `${name}-${cols}.json`);
  await Bun.write(data, JSON.stringify(make(cols)));
  const run = Bun.spawnSync(
    [
      bin,
      "dump",
      "app.tui",
      "--data",
      data,
      "--cols",
      String(cols),
      "--rows",
      String(rows),
      "--theme",
      "dark",
      "--styles",
      "--format",
      "json",
    ],
    { cwd: ui, timeout: 60_000 },
  );
  if (run.exitCode !== 0)
    throw new Error(`tuimark dump ${name}: ${run.stderr.toString()}`);
  const dump = JSON.parse(run.stdout.toString()) as Dump;
  if (dump.errors.length > 0)
    throw new Error(`tuimark dump ${name}: ${JSON.stringify(dump.errors)}`);
  // A run is [text, fg, bg, flags]; the runs of a row are contiguous from its
  // first cell, and a cell is one code point (the samples have no wide
  // characters), so the text is sliced by code point, not by UTF-16 unit.
  return dump.grid.map((text, y) => {
    const cells = Array.from(text);
    let at = 0;
    return (dump.styles[y] ?? []).map((run): Run => {
      if (run.x !== at)
        throw new Error(
          `${name} row ${y}: a run starts at ${run.x}, not ${at}`,
        );
      at = run.x + run.w;
      const flags = (run.a ?? []).reduce(
        (bits, a) => bits | (FLAGS[a] ?? 0),
        0,
      );
      return [
        cells.slice(run.x, run.x + run.w).join(""),
        color(run.fg),
        color(run.bg),
        flags,
      ];
    });
  });
}

// Imported (site-screens.test.ts reads SCENES), it draws nothing.
if (import.meta.main) {
  const bin = await resolvePinned();
  const scratch = await mkdtemp(join(tmpdir(), "la-screens-"));
  const scenes: [string, string][] = [];
  try {
    for (const [scene, { frames, screens }] of Object.entries(SCENES)) {
      const drawn = [];
      for (const { cols, rows } of screens) {
        const lines = [];
        for (const name of frames)
          lines.push({
            name,
            lines: await frame(bin, scratch, name, cols, rows),
          });
        drawn.push({ cols, rows, frames: lines });
      }
      // One row a line, so a change to the client reads as a change to its rows.
      const body = drawn
        .map(
          (screen) =>
            `      {"cols": ${screen.cols}, "rows": ${screen.rows}, "frames": [\n${screen.frames
              .map(
                (f) =>
                  `        {"name": ${JSON.stringify(f.name)}, "lines": [\n${f.lines
                    .map((line) => `          ${JSON.stringify(line)}`)
                    .join(",\n")}\n        ]}`,
              )
              .join(",\n")}\n      ]}`,
        )
        .join(",\n");
      scenes.push([scene, `{"screens": [\n${body}\n    ]}`]);
    }
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }

  const { version } = await Bun.file(
    join(root, "apps/cli/package.json"),
  ).json();
  await Bun.write(
    out,
    `{\n  "version": ${JSON.stringify(version)},\n  "theme": "dark",\n  "colors": ${JSON.stringify(colors)},\n  "scenes": {\n${scenes
      .map(([scene, text]) => `    ${JSON.stringify(scene)}: ${text}`)
      .join(",\n")}\n  }\n}\n`,
  );
  console.log(`screens: ${out} (local-agent ${version})`);
}
