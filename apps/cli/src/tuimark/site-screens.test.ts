import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { SCENES } from "#cli/tuimark/site-screens";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../../..");

type Screens = {
  version: string;
  colors: string[];
  scenes: Record<
    string,
    {
      screens: {
        cols: number;
        rows: number;
        frames: { name: string; lines: [string, number, number, number][][] }[];
      }[];
    }
  >;
};

const screens = (): Promise<Screens> =>
  Bun.file(`${root}/docs/.vitepress/theme/screens.json`).json();

// The home page draws the released client: `bun run site:screens` writes its
// screens from the client's own render, in the release commit that bumps the
// CLI.
test("the site's screens are the render of the CLI's version", async () => {
  const cli = await Bun.file(`${root}/apps/cli/package.json`).json();
  expect((await screens()).version).toBe(cli.version);
});

test("the site's screens are the scenes site-screens.ts draws", async () => {
  const { scenes } = await screens();
  expect(
    Object.fromEntries(
      Object.entries(scenes).map(([name, scene]) => [
        name,
        {
          frames: scene.screens[0]?.frames.map((f) => f.name),
          screens: scene.screens.map(({ cols, rows }) => ({ cols, rows })),
        },
      ]),
    ),
  ).toEqual(SCENES);
});

test("every frame fills its screen, row by row", async () => {
  const { colors, scenes } = await screens();
  // TuiScreen paints a row's background only where it is not the screen's.
  expect(colors).toContain("#0d1117");
  for (const scene of Object.values(scenes))
    for (const screen of scene.screens)
      for (const frame of screen.frames) {
        expect(frame.lines).toHaveLength(screen.rows);
        for (const runs of frame.lines) {
          const cells = runs.reduce(
            (n, [text]) => n + Array.from(text).length,
            0,
          );
          expect(cells).toBe(screen.cols);
          for (const [, fg, bg] of runs) {
            expect(colors[fg]).toMatch(/^#[0-9a-f]{6}$/);
            expect(colors[bg]).toMatch(/^#[0-9a-f]{6}$/);
          }
        }
      }
});
