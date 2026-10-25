import { expect, test } from "vitest";
import { atEnd } from "#desktop/renderer/scroll";

test("a scroller at its end, or within the slack of it, is at the end", () => {
  expect(atEnd({ scrollTop: 600, clientHeight: 400, scrollHeight: 1000 })).toBe(
    true,
  );
  expect(atEnd({ scrollTop: 575, clientHeight: 400, scrollHeight: 1000 })).toBe(
    true,
  );
  // Content that does not fill the box has no other place to be.
  expect(atEnd({ scrollTop: 0, clientHeight: 400, scrollHeight: 300 })).toBe(
    true,
  );
});

test("a reader who scrolled up is not at the end", () => {
  expect(atEnd({ scrollTop: 100, clientHeight: 400, scrollHeight: 1000 })).toBe(
    false,
  );
  expect(
    atEnd({ scrollTop: 550, clientHeight: 400, scrollHeight: 1000 }, 10),
  ).toBe(false);
});
