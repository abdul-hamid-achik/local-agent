import { expect, test } from "vitest";
import { formatBytes, previewText } from "#desktop/renderer/format";

test("a byte count reads in B, KB, MB or GB", () => {
  expect(formatBytes(0)).toBe("0 B");
  expect(formatBytes(1023)).toBe("1023 B");
  expect(formatBytes(1536)).toBe("1.5 KB");
  expect(formatBytes(8 * 1024 * 1024)).toBe("8.0 MB");
  expect(formatBytes(4.5 * 1024 ** 3)).toBe("4.50 GB");
  expect(formatBytes(-5)).toBe("0 B");
});

test("a text within the limit is shown whole", () => {
  expect(previewText("abc", 3)).toEqual({ shown: "abc", clipped: false });
  expect(previewText("", 3)).toEqual({ shown: "", clipped: false });
});

test("a longer text is cut at the limit, not in the middle of a character", () => {
  expect(previewText("abcdef", 3)).toEqual({ shown: "abc", clipped: true });
  // 😀 is two UTF-16 units: a cut between them would leave half of it.
  expect(previewText("ab😀cd", 3)).toEqual({ shown: "ab", clipped: true });
  expect(previewText("ab😀cd", 4)).toEqual({ shown: "ab😀", clipped: true });
});
