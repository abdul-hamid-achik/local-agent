// The runs of a styled `tuimark dump`, row by row: what site-screens.ts
// writes for the site and what check.ts compares with the colour goldens.

/** A dump, as `tuimark dump --styles --format json` prints it. */
export type StyledDump = {
  errors: unknown[];
  grid: string[];
  styles: { x: number; w: number; fg?: string; bg?: string; a?: string[] }[][];
};

/** A run: its text, its colors as the caller spells them, and its flags. */
export type Run<C> = [text: string, fg: C, bg: C, flags: number];

const FLAGS: Record<string, number> = {
  bold: 1,
  dim: 2,
  italic: 4,
  underline: 8,
};

/**
 * The runs of each row of `dump`, with flags bold 1, dim 2, italic 4,
 * underline 8 and the colors as `color` spells them. The runs of a row cover
 * it from its first cell to its last, and a cell is one code point (the
 * samples have no wide characters), so the text is sliced by code point, not
 * by UTF-16 unit. Throws, naming `label`, when a row's runs do not.
 */
export function styledRuns<C>(
  dump: StyledDump,
  label: string,
  color: (value?: string) => C,
): Run<C>[][] {
  return dump.grid.map((text, y) => {
    const cells = Array.from(text);
    let at = 0;
    const runs = (dump.styles[y] ?? []).map((run): Run<C> => {
      if (run.x !== at)
        throw new Error(
          `${label} row ${y}: a run starts at ${run.x}, not ${at}`,
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
    if (at !== cells.length)
      throw new Error(
        `${label} row ${y}: runs end at ${at} of ${cells.length}`,
      );
    return runs;
  });
}
