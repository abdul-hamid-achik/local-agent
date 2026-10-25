import { expect, test } from "vitest";
import { terminalText } from "./text";

test.each([
  ["SGR and BEL, keeping Unicode", "\x1b[31mHola 🛶\x1b[0m\x07", "Hola 🛶"],
  ["OSC title ended by BEL", "a\x1b]0;title\x07b", "ab"],
  [
    "OSC hyperlink ended by ST",
    "a\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\b",
    "alinkb",
  ],
  ["DCS ended by ST", "a\x1bPq...\x1b\\b", "ab"],
  ["8-bit CSI", "a\x9b31mb\x9b0m", "ab"],
  ["other C1 controls", "a\x85b\x80c", "abc"],
  ["8-bit OSC swallows its payload", "a\x9d0;title\x07b", "ab"],
  ["lone carriage return", "a\rb", "ab"],
  ["CRLF keeps the line break", "a\r\nb", "a\nb"],
  ["newline and tab", "a\nb\tc", "a\nb\tc"],
  ["DEL", "a\x7fb", "ab"],
  [
    "line and paragraph separators are not terminal controls",
    "a\u2028b\u2029c",
    "a\u2028b\u2029c",
  ],
  ["emoji modifiers and ZWJ sequences", "👍🏽 👨‍👩‍👧", "👍🏽 👨‍👩‍👧"],
])("terminalText: %s", (_name, input, expected) => {
  expect(terminalText(input)).toBe(expected);
});
