import { expect, test } from "vitest";
import { latest } from "#desktop/renderer/latest";

test("only the newest request is current, whatever order the answers arrive in", () => {
  const guard = latest();
  const first = guard.begin();
  expect(first()).toBe(true);
  const second = guard.begin();
  expect(first()).toBe(false);
  expect(second()).toBe(true);
});

test("invalidate supersedes the requests in flight, and a later one is current again", () => {
  const guard = latest();
  const inFlight = guard.begin();
  guard.invalidate();
  expect(inFlight()).toBe(false);
  expect(guard.begin()()).toBe(true);
});

test("guards are independent of each other", () => {
  const listing = latest();
  const reading = latest();
  const list = listing.begin();
  reading.begin();
  reading.begin();
  expect(list()).toBe(true);
});

test("a slow earlier answer does not overwrite a newer one", async () => {
  const guard = latest();
  let shown = "";
  const load = async (answer: string, delay: number) => {
    const current = guard.begin();
    await new Promise((resolve) => setTimeout(resolve, delay));
    if (current()) shown = answer;
  };
  await Promise.all([load("old", 20), load("new", 1)]);
  expect(shown).toBe("new");
});
