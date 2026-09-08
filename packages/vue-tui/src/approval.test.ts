import { createTestRenderer } from "@opentui/core/testing";
import { afterEach, expect, test } from "vitest";
import { createRenderer, defineComponent, h, nextTick } from "vue";
import { createNodeOps, rendererInjectionKey } from "vue-termui";
import { Approval } from "./approval";

const mounted: Array<{ unmount: () => void; destroy: () => void }> = [];

afterEach(() => {
  for (const app of mounted.splice(0)) {
    app.unmount();
    app.destroy();
  }
});

async function mount(onDecision: (value: boolean) => void, preview = "") {
  const terminal = await createTestRenderer({ width: 80, height: 12 });
  const Root = defineComponent({
    setup: () => () => h(Approval, { preview, maxLines: 3, onDecision }),
  });
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(Root);
  app.provide(rendererInjectionKey, terminal.renderer);
  app.mount(terminal.renderer.root);
  mounted.push({
    unmount: () => app.unmount(),
    destroy: () => terminal.renderer.destroy(),
  });
  await nextTick();
  await terminal.flush();
  return terminal;
}

test("Enter denies by default and the decision is emitted once", async () => {
  const decisions: boolean[] = [];
  const terminal = await mount((value) => decisions.push(value));
  terminal.mockInput.pressEnter();
  terminal.mockInput.pressKey("y");
  await nextTick();
  expect(decisions).toEqual([false]);
});

test("y approves and the full preview scrolls inside a bounded viewport", async () => {
  const decisions: boolean[] = [];
  const preview = Array.from(
    { length: 13 },
    (_, index) => `line ${index + 1}`,
  ).join("\n");
  const terminal = await mount((value) => decisions.push(value), preview);
  for (let index = 0; index < 13; index++)
    terminal.mockInput.pressArrow("down");
  await terminal.flush();
  expect(terminal.captureCharFrame()).toContain("line 13");
  terminal.resize(60, 10);
  await terminal.flush();
  terminal.mockInput.pressKey("y");
  await nextTick();
  expect(decisions).toEqual([true]);
});
