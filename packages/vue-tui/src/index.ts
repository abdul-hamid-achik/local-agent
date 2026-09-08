import type { CliRendererConfig } from "@opentui/core";
import { defineComponent, h } from "vue";
import { Box, createApp, useTerminalSize } from "vue-termui";

export {
  Box,
  createNodeOps,
  exitInjectionKey,
  Input,
  Markdown,
  onKeyDown,
  rendererInjectionKey,
  ScrollBox,
  Text,
  Textarea,
  useExit,
  useFocusManager,
  useRenderer,
  useTerminalSize,
} from "vue-termui";
export { createApp };

export type Presentation = "fullscreen" | "inline";

export function terminalOptions(mode: Presentation): CliRendererConfig {
  return {
    screenMode: mode === "fullscreen" ? "alternate-screen" : "split-footer",
    footerHeight: 6,
    externalOutputMode: mode === "inline" ? "capture-stdout" : "passthrough",
    exitOnCtrlC: true,
    openConsoleOnError: false,
  };
}

export { Approval } from "#tui/approval";
export { Composer } from "#tui/composer";
export { terminalText } from "#tui/text";

/** The parent owns space. A sidebar disappears when the primary pane would be squeezed. */
export const PanelLayout = defineComponent({
  name: "PanelLayout",
  props: { sidebar: { type: Boolean, default: false } },
  setup(props, { slots }) {
    const { width, height } = useTerminalSize();
    return () =>
      h(Box, { width: "100%", height: "100%", flexDirection: "row" }, () => [
        h(
          Box,
          { flexGrow: 1, minWidth: 0, flexDirection: "column" },
          slots.default,
        ),
        props.sidebar && width.value >= 100 && height.value >= 16
          ? h(
              Box,
              {
                width: 28,
                flexShrink: 0,
                border: true,
                flexDirection: "column",
              },
              slots.sidebar,
            )
          : null,
      ]);
  },
});
