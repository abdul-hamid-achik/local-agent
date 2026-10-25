// --theme resolution and the named-palette document, shared by main.ts's flag
// and launch.ts's spawn. Pure except for the themes directory listing and the
// generated document write, both injectable for tests.
//
// A named palette is one .tcss file in ui/themes/: it re-declares :root tokens
// and is loaded by a generated copy of the document, after the last <style> the
// document itself lists, so its tokens win every tie (kit/theme.tcss documents
// the cascade). It is applied at document load only: the runtime /theme command
// still switches dark/light/auto, and the host is pinned to dark for a named
// palette so the sheets' `@media (theme: light)` blocks never fire against it.

import { readdirSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, isAbsolute, join } from "node:path";

/** The themes the tuimark host renders itself: --theme dark|light|auto. */
export const BUILTIN_THEMES = ["dark", "light", "auto"] as const;
export type BuiltinTheme = (typeof BUILTIN_THEMES)[number];

/** A directory listing, so tests can supply one without a filesystem. */
export type ListThemes = (directory: string) => string[];

/** The named palettes: the .tcss basenames in `directory`, sorted. */
export function namedThemes(
  directory: string,
  readdir: ListThemes = readdirSync,
): string[] {
  let entries: string[];
  try {
    entries = readdir(directory);
  } catch {
    // No themes folder: only the builtin themes exist.
    return [];
  }
  return entries
    .filter((entry) => entry.endsWith(".tcss"))
    .map((entry) => basename(entry, ".tcss"))
    .sort();
}

export type ResolvedTheme =
  | { kind: "builtin"; theme: BuiltinTheme }
  | { kind: "named"; name: string; file: string };

/**
 * What `--theme value` means: a builtin theme the host renders itself, or a
 * named palette's .tcss file. An unknown name throws with every theme that
 * does exist, so main.ts exits non-zero with a clear line.
 */
export function resolveTheme(
  value: string,
  themesDirectory: string,
  readdir: ListThemes = readdirSync,
): ResolvedTheme {
  if ((BUILTIN_THEMES as readonly string[]).includes(value))
    return { kind: "builtin", theme: value as BuiltinTheme };
  const named = namedThemes(themesDirectory, readdir);
  if (named.includes(value))
    return {
      kind: "named",
      name: value,
      file: join(themesDirectory, `${value}.tcss`),
    };
  throw new Error(
    `Unknown theme: ${value}. Available themes: ${[...BUILTIN_THEMES, ...named].join(", ")}`,
  );
}

/** The prefix of the private folder a themed document is written to. */
export const THEME_FOLDER_PREFIX = "local-agent-theme-";

/** `text` as an attribute value: a path may hold any of these. */
const attribute = (text: string) =>
  text
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;");

/**
 * A copy of `ui/<doc>` with the named palette's <style> inserted after the
 * document's last <style>. It is written to a new private folder under the
 * system's temporary one, never into the client's own files (an installed
 * client may be read-only, and must not collect leftovers), so its static
 * src paths are made absolute against `uiDirectory`: kit/kit.tui and app.tcss
 * resolve as they do from the original. Data-bound srcs (`{…}`) are left as
 * they are. The caller removes the file and its folder once the host has
 * loaded it.
 */
export async function themedDocument(
  uiDirectory: string,
  doc: string,
  name: string,
  write: (path: string, text: string) => Promise<unknown> = (path, text) =>
    Bun.write(path, text),
  folder: () => Promise<string> = () =>
    mkdtemp(join(tmpdir(), THEME_FOLDER_PREFIX)),
): Promise<string> {
  const source = await Bun.file(join(uiDirectory, doc)).text();
  const lines = source.split("\n");
  let lastStyle = -1;
  for (const [i, line] of lines.entries())
    if (/^\s*<style\b/.test(line)) lastStyle = i;
  if (lastStyle < 0)
    throw new Error(`${doc} has no <style> for a theme to follow`);
  const absolute = (line: string) =>
    line.replace(/\bsrc="([^"{}]+)"/g, (whole, src: string) =>
      isAbsolute(src) ? whole : `src="${attribute(join(uiDirectory, src))}"`,
    );
  const themed = [
    ...lines.slice(0, lastStyle + 1).map(absolute),
    `  <style src="${attribute(join(uiDirectory, "themes", `${name}.tcss`))}"/>`,
    ...lines.slice(lastStyle + 1).map(absolute),
  ].join("\n");
  const path = join(await folder(), `${name}-${doc}`);
  await write(path, themed);
  return path;
}
