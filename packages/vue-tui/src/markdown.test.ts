import { expect, test } from "vitest";
import { createRenderer, h, ref } from "vue";
import { Box, createNodeOps, Markdown, SyntaxStyle } from "vue-termui";
import { createTestRenderer } from "#tui/testing";

test("upstream Markdown wraps without a fixed height", async () => {
  const terminal = await createTestRenderer({ width: 40, height: 24 });
  const streaming = ref(true);
  const content = ref("Hello **world**");
  const syntaxStyle = SyntaxStyle.fromStyles({
    default: { fg: "#ffffff" },
    "markup.bold": { bold: true },
  });
  const app = createRenderer(createNodeOps(terminal.renderer)).createApp({
    render: () =>
      h(Box, { width: "100%", flexDirection: "column" }, () =>
        h(Markdown, {
          content: content.value,
          syntaxStyle,
          width: "100%",
          streaming: streaming.value,
        }),
      ),
  });
  app.mount(terminal.renderer.root);
  try {
    await terminal.waitForFrame((frame) => frame.includes("world"));
    content.value +=
      "\n\nThis is a long paragraph that should wrap across several rows at a narrow width. END\n\n```text\nconst value = 42;\n```";
    streaming.value = false;
    await expect
      .poll(
        async () => {
          await terminal.flush();
          return terminal.captureCharFrame();
        },
        { timeout: 10000 },
      )
      .toContain("END");
    for (const width of [24, 70, 40]) {
      terminal.resize(width, 24);
      await terminal.flush();
      const frame = terminal.captureCharFrame();
      expect(frame).toContain("world");
      expect(frame).not.toContain("**world**");
      expect(frame).toContain("END");
      expect(frame).toContain("const value = 42;");
    }
  } finally {
    app.unmount();
    terminal.renderer.destroy();
  }
});
