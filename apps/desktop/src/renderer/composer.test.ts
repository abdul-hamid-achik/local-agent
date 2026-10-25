import { expect, test } from "vitest";
import { isSendKey } from "#desktop/renderer/composer";

test("Enter sends; Shift+Enter, another key and an input method's Enter do not", () => {
  const key = (overrides: Partial<KeyboardEvent>) => ({
    key: "Enter",
    shiftKey: false,
    isComposing: false,
    keyCode: 13,
    ...overrides,
  });
  expect(isSendKey(key({}))).toBe(true);
  expect(isSendKey(key({ shiftKey: true }))).toBe(false);
  expect(isSendKey(key({ key: "a", keyCode: 65 }))).toBe(false);
  expect(isSendKey(key({ isComposing: true }))).toBe(false);
  expect(isSendKey(key({ keyCode: 229 }))).toBe(false);
});
