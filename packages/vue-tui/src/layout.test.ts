import { PassThrough } from "node:stream";
import { createTestRenderer } from "@opentui/core/testing";
import { expect, test } from "vitest";
import { createRenderer, defineComponent, h, nextTick, ref } from "vue";
import { createNodeOps, rendererInjectionKey } from "vue-termui";
import {
  Input,
  PanelLayout,
  Text,
  terminalOptions,
  terminalText,
} from "./index";

test("layout adapts without losing the composer or its draft", async () => {
  const terminal = await createTestRenderer({ width: 120, height: 30 });
  const draft = ref("");
  const Root = defineComponent({
    setup: () => () =>
      h(
        PanelLayout,
        { sidebar: true },
        {
          default: () => [
            h(Text, {}, () => "Conversation"),
            h(Input, {
              modelValue: draft.value,
              "onUpdate:modelValue": (value: string) => {
                draft.value = value;
              },
              autofocus: true,
            }),
          ],
          sidebar: () => h(Text, {}, () => "Inspector"),
        },
      ),
  });
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(Root);
  app.provide(rendererInjectionKey, terminal.renderer);
  try {
    app.mount(terminal.renderer.root);
    await nextTick();
    await terminal.flush();
    expect(terminal.captureCharFrame()).toContain("Inspector");
    await terminal.mockInput.typeText("hello");
    await nextTick();
    expect(draft.value).toBe("hello");
    for (const [width, height] of [
      [60, 18],
      [30, 10],
      [120, 30],
    ] as const) {
      terminal.resize(width, height);
      await nextTick();
      await terminal.flush();
      const frame = terminal.captureCharFrame();
      expect(frame).toContain("Conversation");
      expect(frame).toContain("hello");
      expect(frame.includes("Inspector")).toBe(width >= 100);
    }
  } finally {
    app.unmount();
    terminal.renderer.destroy();
  }
});

test("inline commits output to scrollback above the live footer", async () => {
  const stdout = new PassThrough();
  const terminal = await createTestRenderer({
    stdout: stdout as unknown as NodeJS.WriteStream,
    ...terminalOptions("inline"),
    width: 80,
    height: 24,
  });
  try {
    // The configured stdout path is what the inline CLI uses, not a viewport.
    stdout.write("Conversation stays in scrollback\n");
    await terminal.flush();
    expect(terminal.externalOutput.takeText()).toContain(
      "Conversation stays in scrollback",
    );
  } finally {
    terminal.renderer.destroy();
  }
});

test("terminal text keeps Unicode and removes escape commands", () => {
  expect(terminalText("\x1b[31mHola 🛶\x1b[0m\x07")).toBe("Hola 🛶");
});
