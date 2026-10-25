import { expect, test, vi } from "vitest";
import { reportUiError } from "#desktop/renderer/ui-error";

test("an error caught in a component is logged with where, and its reason is shown", () => {
  const log = vi.fn();
  const failure = new TypeError("Cannot read properties of undefined");
  expect(reportUiError(failure, "render function", log)).toBe(
    "The interface hit an error: Cannot read properties of undefined",
  );
  expect(log).toHaveBeenCalledWith("[renderer] render function", failure);
});

test("a rejection that is not an Error still gets a message", () => {
  expect(reportUiError(undefined, "native event handler", vi.fn())).toBe(
    "The interface hit an error: Request failed",
  );
});
