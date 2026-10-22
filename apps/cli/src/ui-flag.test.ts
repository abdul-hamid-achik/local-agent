import { expect, test } from "vitest";
import { defaultUi, interactiveClient, parseUi } from "#cli/ui-flag";

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

test("the default client is Tuimark on macOS and Linux and Vue on Windows", () => {
  expect(defaultUi("darwin")).toBe("tuimark");
  expect(defaultUi("linux")).toBe("tuimark");
  expect(defaultUi("win32")).toBe("vue");
  expect(parseUi(undefined, "darwin")).toBe("tuimark");
  expect(parseUi(undefined, "linux")).toBe("tuimark");
  expect(parseUi(undefined, "win32")).toBe("vue");
  expect(parseUi(undefined)).toBe(defaultUi());
});

test("Tuimark inline is opt-in: only an explicit --ui tuimark uses it", () => {
  expect(interactiveClient("tuimark", "fullscreen")).toBe("tuimark");
  expect(interactiveClient("tuimark", "fullscreen", true)).toBe("tuimark");
  // A bare --mode inline (no --ui) still goes to the Vue client.
  expect(interactiveClient("tuimark", "inline")).toBe("vue");
  expect(interactiveClient("tuimark", "inline", false)).toBe("vue");
  // An explicit --ui tuimark --mode inline stays Tuimark.
  expect(interactiveClient("tuimark", "inline", true)).toBe("tuimark");
  expect(interactiveClient("vue", "fullscreen")).toBe("vue");
  expect(interactiveClient("vue", "inline", true)).toBe("vue");
});
