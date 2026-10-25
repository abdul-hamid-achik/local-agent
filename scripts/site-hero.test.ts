import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

type Hero = {
  version: string;
  colors: string[];
  screens: {
    cols: number;
    rows: number;
    frames: { name: string; lines: [string, number, number, number][][] }[];
  }[];
};

// The home page's hero draws the released client: `bun run site:hero` writes
// it from the client's own render, in the release commit that bumps the CLI.
test("the hero terminal is the render of the CLI's version", async () => {
  const hero: Hero = await Bun.file(
    `${root}/docs/.vitepress/theme/hero.json`,
  ).json();
  const cli = await Bun.file(`${root}/apps/cli/package.json`).json();
  expect(hero.version).toBe(cli.version);
});

test("every hero frame fills its screen, row by row", async () => {
  const hero: Hero = await Bun.file(
    `${root}/docs/.vitepress/theme/hero.json`,
  ).json();
  // HeroTerminal paints a row's background only where it is not the screen's.
  expect(hero.colors).toContain("#0d1117");
  expect(hero.screens.map((s) => s.cols)).toEqual([80, 56]);
  for (const screen of hero.screens) {
    expect(screen.frames.map((f) => f.name)).toEqual([
      "hero-thinking",
      "hero-cards",
      "hero-approval",
    ]);
    for (const frame of screen.frames) {
      expect(frame.lines).toHaveLength(screen.rows);
      for (const runs of frame.lines) {
        const cells = runs.reduce(
          (n, [text]) => n + Array.from(text).length,
          0,
        );
        expect(cells).toBe(screen.cols);
        for (const [, fg, bg] of runs) {
          expect(hero.colors[fg]).toMatch(/^#[0-9a-f]{6}$/);
          expect(hero.colors[bg]).toMatch(/^#[0-9a-f]{6}$/);
        }
      }
    }
  }
});
