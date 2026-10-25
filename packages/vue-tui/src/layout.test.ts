import { PassThrough } from "node:stream";
import { createTestRenderer } from "@opentui/core/testing";
import { expect, test } from "vitest";
import { createRenderer, defineComponent, h, nextTick, ref } from "vue";
import { createNodeOps, rendererInjectionKey } from "vue-termui";
import { Composer } from "./composer";
import { Box, Input, PanelLayout, Text, terminalOptions } from "./index";

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

test("the sidebar follows the container, not the terminal", async () => {
  const terminal = await createTestRenderer({ width: 120, height: 30 });
  const Root = defineComponent({
    setup: () => () =>
      h(Box, { width: 60, height: 20 }, () =>
        h(
          PanelLayout,
          { sidebar: true },
          {
            default: () => h(Text, {}, () => "Conversation"),
            sidebar: () => h(Text, {}, () => "Inspector"),
          },
        ),
      ),
  });
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(Root);
  app.provide(rendererInjectionKey, terminal.renderer);
  try {
    app.mount(terminal.renderer.root);
    await nextTick();
    await terminal.flush();
    await nextTick();
    await terminal.flush();
    // The terminal is wide enough for a sidebar; the 60-column container is not.
    const frame = terminal.captureCharFrame();
    expect(frame).toContain("Conversation");
    expect(frame).not.toContain("Inspector");
  } finally {
    app.unmount();
    terminal.renderer.destroy();
  }
});

test("inline keeps live frames in the footer and commits only stdout to scrollback", async () => {
  const stdout = new PassThrough();
  const terminal = await createTestRenderer({
    stdout: stdout as unknown as NodeJS.WriteStream,
    ...terminalOptions("inline"),
    width: 40,
    height: 24,
  });
  const draft = ref("");
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp(
    defineComponent({
      setup: () => () => [
        h(Text, {}, () => "status line"),
        h(Composer, {
          modelValue: draft.value,
          "onUpdate:modelValue": (value: string) => {
            draft.value = value;
          },
        }),
      ],
    }),
  );
  app.provide(rendererInjectionKey, terminal.renderer);
  try {
    app.mount(terminal.renderer.root);
    await nextTick();
    await terminal.flush();
    await terminal.mockInput.typeText("live draft");
    await terminal.flush();

    // The footer is the whole live frame; none of it is scrollback yet.
    const footer = terminal.captureCharFrame();
    expect(footer.replace(/\n$/, "").split("\n")).toHaveLength(
      terminalOptions("inline").footerHeight as number,
    );
    expect(footer).toContain("status line");
    expect(footer).toContain("live draft");
    expect(terminal.externalOutput.takeText()).toBe("");

    // Stdout is what the CLI commits: it reaches scrollback, not the footer.
    stdout.write("Conversation stays in scrollback\n");
    await terminal.flush();
    expect(terminal.captureCharFrame()).not.toContain("Conversation");
    expect(terminal.captureCharFrame()).toContain("live draft");
    expect(terminal.externalOutput.takeText()).toBe(
      "Conversation stays in scrollback",
    );

    // Redrawing the footer never re-commits what is already in history.
    await terminal.mockInput.typeText(" more");
    await terminal.flush();
    expect(terminal.captureCharFrame()).toContain("live draft more");
    expect(terminal.externalOutput.takeText()).toBe("");
  } finally {
    app.unmount();
    terminal.renderer.destroy();
  }
});
