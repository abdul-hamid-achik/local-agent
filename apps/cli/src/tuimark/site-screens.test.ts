import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { GALLERY, galleryThemes, SCENES } from "#cli/tuimark/site-screens";

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

type Gallery = {
  version: string;
  sample: string;
  cols: number;
  rows: number;
  colors: string[];
  themes: {
    name: string;
    screen: number;
    lines: [string, number, number, number][][];
  }[];
};

const gallery = (): Promise<Gallery> =>
  Bun.file(`${root}/docs/.vitepress/theme/themes.json`).json();

// The home page draws the released client: `bun run site:screens` writes its
// screens from the client's own render, in the release commit that bumps the
// CLI.
test("the site's screens are the render of the CLI's version", async () => {
  const cli = await Bun.file(`${root}/apps/cli/package.json`).json();
  expect((await screens()).version).toBe(cli.version);
  expect((await gallery()).version).toBe(cli.version);
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

// The configuration page's gallery: one screen in every theme the client
// accepts, each on its own background, so a theme added to ui/themes shows
// up there with the next release commit.
test("the theme gallery is every theme, each on its own screen", async () => {
  const { sample, cols, rows, colors, themes } = await gallery();
  expect({ sample, cols, rows }).toEqual(GALLERY);
  expect(themes.map((theme) => theme.name)).toEqual(galleryThemes());
  for (const theme of themes) {
    expect(theme.lines).toHaveLength(rows);
    const backgrounds = new Map<number, number>();
    for (const runs of theme.lines) {
      expect(runs.reduce((n, [text]) => n + Array.from(text).length, 0)).toBe(
        cols,
      );
      for (const [text, fg, bg] of runs) {
        expect(colors[fg]).toMatch(/^#[0-9a-f]{6}$/);
        expect(colors[bg]).toMatch(/^#[0-9a-f]{6}$/);
        backgrounds.set(
          bg,
          (backgrounds.get(bg) ?? 0) + Array.from(text).length,
        );
      }
    }
    // The window around the screen wears the color most of its cells do.
    const [screen] = [...backgrounds].sort((a, b) => b[1] - a[1])[0] ?? [];
    expect(theme.screen).toBe(screen);
  }
  // Light and dark themes do not share a screen.
  const screenOf = (name: string) =>
    colors[themes.find((theme) => theme.name === name)?.screen ?? -1];
  expect(screenOf("dark")).not.toBe(screenOf("light"));
  expect(new Set(themes.map((theme) => theme.screen)).size).toBeGreaterThan(
    themes.length / 2,
  );
});
