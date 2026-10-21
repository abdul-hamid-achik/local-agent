import { expect, test } from "vitest";
import { parseUi } from "#cli/ui-flag";

test("--ui accepts vue and tuimark and refuses anything else", () => {
  expect(parseUi("vue")).toBe("vue");
  expect(parseUi("tuimark", "linux")).toBe("tuimark");
  expect(parseUi("tuimark", "darwin")).toBe("tuimark");
  expect(() => parseUi("opentui")).toThrow("Unknown UI client");
});

test("--ui tuimark is refused up front on Windows, vue still works there", () => {
  expect(() => parseUi("tuimark", "win32")).toThrow("not available on Windows");
  expect(parseUi("vue", "win32")).toBe("vue");
});
