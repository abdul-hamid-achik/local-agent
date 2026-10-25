import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { namedThemes, resolveTheme } from "#cli/tuimark/themes";

const listing = (entries: string[]) => () => entries;

test("namedThemes lists the .tcss basenames of a folder, sorted", () => {
  expect(
    namedThemes(
      "/themes",
      listing(["sepia.tcss", "midnight.tcss", "x.txt", "y"]),
    ),
  ).toEqual(["midnight", "sepia"]);
});

test("a missing themes folder means no named themes", () => {
  expect(
    namedThemes("/themes", () => {
      throw new Error("ENOENT");
    }),
  ).toEqual([]);
});

test("the builtin themes resolve as builtins", () => {
  for (const theme of ["dark", "light", "auto"] as const)
    expect(resolveTheme(theme, "/themes", listing([]))).toEqual({
      kind: "builtin",
      theme,
    });
});

test("a named theme resolves to its file", () => {
  expect(
    resolveTheme("midnight", "/themes", listing(["midnight.tcss"])),
  ).toEqual({
    kind: "named",
    name: "midnight",
    file: "/themes/midnight.tcss",
  });
});

test("an unknown theme lists every theme that exists", () => {
  expect(() =>
    resolveTheme(
      "solarized",
      "/themes",
      listing(["sepia.tcss", "midnight.tcss"]),
    ),
  ).toThrow(
    "Unknown theme: solarized. Available themes: dark, light, auto, midnight, sepia",
  );
});

const ui = join(dirname(fileURLToPath(import.meta.url)), "ui");

/** A sheet's text without comments. */
function css(file: string): string {
  return readFileSync(join(ui, file), "utf8").replace(/\/\*[\s\S]*?\*\//gu, "");
}

const DECLARATION = /--([\w-]+):\s*(#[0-9a-f]{6});/giu;

/**
 * The color tokens a sheet sets on :root, later blocks winning. With `light`,
 * a token its `@media (theme: light)` block re-declares takes that value; the
 * tokens the block leaves out keep their :root value.
 */
function sheet(file: string, light: boolean): Record<string, string> {
  const text = css(file);
  const blocks = [
    ...text.matchAll(
      /@media \(theme: light\)\s*\{\s*:root\s*\{([^}]*)\}\s*\}|:root\s*\{([^}]*)\}/gu,
    ),
  ];
  // Every token is read from a block this parser understands: a light block
  // holding more than its :root, or a token set anywhere else, fails here
  // instead of being read as a dark value or skipped.
  const lightBlocks = blocks.filter(([, lit]) => lit !== undefined).length;
  expect(lightBlocks, `${file}: @media (theme: light) blocks`).toBe(
    text.match(/@media \(theme: light\)/gu)?.length ?? 0,
  );
  const parsed = blocks.reduce(
    (count, [, lit = "", always = ""]) =>
      count + [...`${lit}${always}`.matchAll(DECLARATION)].length,
    0,
  );
  expect(parsed, `${file}: color tokens outside :root`).toBe(
    [...text.matchAll(DECLARATION)].length,
  );
  const tokens: Record<string, string> = {};
  for (const [, lit, always] of blocks)
    for (const [, name = "", value = ""] of (
      (light ? lit : undefined) ??
      always ??
      ""
    ).matchAll(DECLARATION))
      tokens[name] = value;
  return tokens;
}

/** Each palette as the host cascades it: the kit, app.tcss, then a named theme. */
const palettes: [string, Record<string, string>][] = [
  ["dark", { ...sheet("kit/theme.tcss", false), ...sheet("app.tcss", false) }],
  ["light", { ...sheet("kit/theme.tcss", true), ...sheet("app.tcss", true) }],
  // A named theme pins the host to dark, so no light block applies.
  ...namedThemes(join(ui, "themes")).map(
    (name): [string, Record<string, string>] => [
      name,
      {
        ...sheet("kit/theme.tcss", false),
        ...sheet("app.tcss", false),
        ...sheet(`themes/${name}.tcss`, false),
      },
    ],
  ),
];

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((at) => {
    const c = Number.parseInt(hex.slice(at, at + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [
    number,
    number,
  ];
  return (light + 0.05) / (dark + 0.05);
}

const TEXT = [
  "fg",
  "muted",
  "subtle",
  "accent",
  "ok",
  "warn",
  "danger",
  "info",
  "code",
  "kw",
  "str",
  "num",
  "com",
  "user",
  "tool",
];
const SURFACES = ["bg", "panel", "raised", "sel"];
// The fills kit.tcss draws a color on: an on-* on a badge, a button or a
// segment, and bg on fg for a focused primary or danger button.
const FILLS = [
  ["bg", "fg"],
  ["on-accent", "accent"],
  ["on-accent", "info"],
  ["on-accent", "focus"],
  ["on-ok", "ok"],
  ["on-warn", "warn"],
  ["on-danger", "danger"],
];

test("the palettes cover the builtins and every named theme", () => {
  expect(palettes.map(([name, palette]) => [name, palette.bg])).toEqual([
    ["dark", "#0d1117"],
    ["light", "#ffffff"],
    ["catppuccin-frappe", "#303446"],
    ["catppuccin-latte", "#eff1f5"],
    ["catppuccin-macchiato", "#24273a"],
    ["catppuccin-mocha", "#1e1e2e"],
    ["dracula", "#282a36"],
    ["everforest", "#2d353b"],
    ["gruvbox-dark", "#282828"],
    ["gruvbox-light", "#fbf1c7"],
    ["kanagawa", "#1f1f28"],
    ["midnight", "#0a0e1a"],
    ["nord", "#2e3440"],
    ["one-dark", "#282c34"],
    ["rose-pine", "#191724"],
    ["rose-pine-dawn", "#faf4ed"],
    ["rose-pine-moon", "#232136"],
    ["sepia", "#f4ecd8"],
    ["tokyo-night", "#1a1b26"],
    ["tokyo-night-storm", "#24283b"],
  ]);
});

test.each(palettes)(
  "every text color reads at WCAG AA on every surface (%s)",
  (name, palette) => {
    const pairs = [
      ...TEXT.flatMap((text) => SURFACES.map((surface) => [text, surface])),
      ...FILLS,
    ];
    for (const [text = "", surface = ""] of pairs) {
      const fg = palette[text];
      const bg = palette[surface];
      expect(fg && bg, `${name}: --${text} and --${surface}`).toBeTruthy();
      expect(
        contrast(fg as string, bg as string),
        `${name}: --${text} on --${surface}`,
      ).toBeGreaterThanOrEqual(4.5);
    }
  },
);

test.each(palettes)(
  "fg, muted and subtle still rank, a visible step apart (%s)",
  (name, palette) => {
    const bg = palette.bg as string;
    const [fg, muted, subtle] = ["fg", "muted", "subtle"].map((token) =>
      contrast(palette[token] as string, bg),
    ) as [number, number, number];
    expect(fg / muted, `${name}: fg over muted`).toBeGreaterThanOrEqual(1.1);
    expect(muted / subtle, `${name}: muted over subtle`).toBeGreaterThanOrEqual(
      1.1,
    );
  },
);

test("app styles fade only struck-through text", () => {
  // A terminal renders dim as it likes (often half the intensity), so a dim
  // color's contrast is the terminal's, not the palette's: text that carries
  // content stays undimmed. The kit's own dim rules (a disabled button, a
  // toast's key) are the kit's.
  const dimmed = ["app.tcss", "inline.tcss"].flatMap((file) =>
    [...css(file).matchAll(/([^{}]+)\{[^}]*\bdim:\s*true/gu)].map(
      ([, selector = ""]) => selector.trim(),
    ),
  );
  expect(dimmed).toEqual(["span.strike"]);
});
