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
