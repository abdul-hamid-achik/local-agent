import { readFileSync } from "node:fs";
import { expect, test } from "vitest";

const css = readFileSync(
  new URL("../docs/.vitepress/theme/style.css", import.meta.url),
  "utf8",
);

/** The `--la-*` colors a theme block sets, by name. */
function tokens(selector: string): Map<string, string> {
  const at = css.indexOf(`${selector} {`);
  const block = css.slice(at, css.indexOf("}", at));
  return new Map(
    [...block.matchAll(/--la-([\w-]+):\s*(#[0-9a-f]{6})\s*;/gi)].map(
      ([, name = "", value = ""]) => [name, value],
    ),
  );
}

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

test.each([":root", ".dark"])(
  "the site's text colors read at WCAG AA on its surfaces (%s)",
  (selector) => {
    const theme = tokens(selector);
    for (const text of ["ink", "muted", "faint"])
      for (const surface of ["bg", "panel", "panel-muted"]) {
        const fg = theme.get(text);
        const bg = theme.get(surface);
        expect(fg && bg, `--la-${text} and --la-${surface}`).toBeTruthy();
        expect(
          contrast(fg as string, bg as string),
          `--la-${text} on --la-${surface}`,
        ).toBeGreaterThanOrEqual(4.5);
      }
    // Faint stays below muted, so the two still rank.
    const bg = theme.get("bg") as string;
    expect(contrast(theme.get("faint") as string, bg)).toBeLessThan(
      contrast(theme.get("muted") as string, bg),
    );
  },
);
