import { expect, test, vi } from "vitest";
import { decidable } from "#desktop/sidecar/decision";

/** The listeners `signal` holds for "abort": added minus removed. */
function trackListeners(signal: AbortSignal) {
  let held = 0;
  const add = signal.addEventListener.bind(signal);
  const remove = signal.removeEventListener.bind(signal);
  vi.spyOn(signal, "addEventListener").mockImplementation((...args) => {
    held += 1;
    add(...args);
  });
  vi.spyOn(signal, "removeEventListener").mockImplementation((...args) => {
    held -= 1;
    remove(...args);
  });
  return () => held;
}

test("an answered approval leaves no abort listener on the run's signal", () => {
  const controller = new AbortController();
  const held = trackListeners(controller.signal);
  const answers: boolean[] = [];
  for (let approval = 0; approval < 20; approval += 1) {
    const settle = decidable(controller.signal, (allowed) =>
      answers.push(allowed),
    );
    expect(held()).toBe(1);
    settle(approval % 2 === 0);
    expect(held()).toBe(0);
  }
  expect(answers).toHaveLength(20);
});

test("an abort refuses the waiting approval once", () => {
  const controller = new AbortController();
  const answers: boolean[] = [];
  decidable(controller.signal, (allowed) => answers.push(allowed));
  controller.abort();
  expect(answers).toEqual([false]);
});

test("an approval asked after the abort is refused at once", () => {
  const controller = new AbortController();
  controller.abort();
  const answers: boolean[] = [];
  decidable(controller.signal, (allowed) => answers.push(allowed));
  expect(answers).toEqual([false]);
});
