// The inline document copies the card templates from app.tui (a separate
// document, so the two never share ids or keymap rows). A copy that drifts
// prints one look in the scrollback and another in fullscreen: this keeps
// them the same, comments and indentation aside.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const ui = join(dirname(fileURLToPath(import.meta.url)), "ui");

/** A template's markup without comments, one trimmed line per element line. */
function template(file: string, id: string): string {
  const doc = readFileSync(join(ui, file), "utf8").replace(
    /<!--[\s\S]*?-->/gu,
    "",
  );
  const start = doc.indexOf(`<template id="${id}"`);
  if (start < 0) throw new Error(`${file} has no template ${id}`);
  const end = doc.indexOf("</template>", start);
  return doc
    .slice(start, end)
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .join("\n");
}

test.each(["la-message", "la-blocks", "la-tool", "la-turn"])(
  "inline.tui's %s is app.tui's",
  (id) => {
    expect(template("inline.tui", id)).toBe(template("app.tui", id));
  },
);
