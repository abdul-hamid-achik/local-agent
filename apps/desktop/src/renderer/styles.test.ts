import { readFileSync } from "node:fs";
import { expect, test } from "vitest";

const css = readFileSync(new URL("./styles.css", import.meta.url), "utf8");
const tokens = new Map(
  [
    ...(css.match(/:root\s*\{([^}]*)\}/)?.[1] ?? "").matchAll(
      /--([\w-]+):\s*(#[0-9a-f]{6})\s*;/gi,
    ),
  ].map(([, name, value]) => [name, value]),
);

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

test("every text color reads at WCAG AA on every background", () => {
  const backgrounds = ["bg", "bg-soft", "bg-raised", "bg-hover"];
  for (const text of ["text", "text-dim", "text-faint"])
    for (const background of backgrounds) {
      const fg = tokens.get(text);
      const bg = tokens.get(background);
      expect(fg && bg, `--${text} and --${background}`).toBeTruthy();
      expect(
        contrast(fg as string, bg as string),
        `--${text} on --${background}`,
      ).toBeGreaterThanOrEqual(4.5);
    }
  // Faint stays below dim, so the two still rank.
  expect(
    contrast(tokens.get("text-faint") as string, tokens.get("bg") as string),
  ).toBeLessThan(
    contrast(tokens.get("text-dim") as string, tokens.get("bg") as string),
  );
});
