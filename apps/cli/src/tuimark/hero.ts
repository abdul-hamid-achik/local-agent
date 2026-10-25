// Writes the website's hero terminal, docs/.vitepress/theme/hero.json: the
// hero samples (fixtures.ts) as the Tuimark client draws them in its dark
// theme, every row as text and color runs, stamped with the CLI version.
// The site shows the released client, so this runs in a release commit;
// scripts/site-hero.test.ts fails until the stamp is the CLI's version.
//
//   bun run site:hero

import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { SAMPLES } from "#cli/tuimark/fixtures";
import { resolvePinned } from "#cli/tuimark/launch";

const ui = join(dirname(import.meta.path), "ui");
const root = join(dirname(import.meta.path), "../../../..");
const out = join(root, "docs/.vitepress/theme/hero.json");

/** The frames the hero plays, in order, and the screens it plays them on. */
const FRAMES = ["hero-thinking", "hero-cards", "hero-approval"] as const;
const SCREENS = [
  { cols: 80, rows: 26 },
  { cols: 56, rows: 26 },
];

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

const bin = await resolvePinned();
const colors: string[] = [];
const color = (value = "") => {
  const at = colors.indexOf(value);
  return at >= 0 ? at : colors.push(value) - 1;
};

const scratch = await mkdtemp(join(tmpdir(), "la-hero-"));
const screens = [];
try {
  for (const { cols, rows } of SCREENS) {
    const frames = [];
    for (const name of FRAMES) {
      const data = join(scratch, `${name}.json`);
      await Bun.write(data, JSON.stringify(SAMPLES[name]()));
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
      // A run is [text, fg, bg, flags]; the runs of a row are contiguous from
      // its first cell, and a cell is one code point (the hero has no wide
      // characters), so the text is sliced by code point, not by UTF-16 unit.
      const lines = dump.grid.map((text, y) => {
        const cells = Array.from(text);
        let at = 0;
        return (dump.styles[y] ?? []).map((run) => {
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
      frames.push({ name, lines });
    }
    screens.push({ cols, rows, frames });
  }
} finally {
  await rm(scratch, { recursive: true, force: true });
}

const { version } = await Bun.file(join(root, "apps/cli/package.json")).json();
// One row a line, so a change to the client reads as a change to its rows.
const body = screens
  .map(
    (screen) =>
      `    {"cols": ${screen.cols}, "rows": ${screen.rows}, "frames": [\n${screen.frames
        .map(
          (frame) =>
            `      {"name": ${JSON.stringify(frame.name)}, "lines": [\n${frame.lines
              .map((line) => `        ${JSON.stringify(line)}`)
              .join(",\n")}\n      ]}`,
        )
        .join(",\n")}\n    ]}`,
  )
  .join(",\n");
await Bun.write(
  out,
  `{\n  "version": ${JSON.stringify(version)},\n  "theme": "dark",\n  "colors": ${JSON.stringify(colors)},\n  "screens": [\n${body}\n  ]\n}\n`,
);
console.log(`hero: ${out} (local-agent ${version})`);
