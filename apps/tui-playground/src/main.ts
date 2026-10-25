import { createApp, terminalOptions } from "@local-agent/vue-tui";
import App from "#playground/App.vue";

// Same presentations as the real client; TUI_PLAYGROUND_MODE=inline previews
// the footer and scrollback behaviour, the default is fullscreen.
const mode =
  process.env.TUI_PLAYGROUND_MODE === "inline" ? "inline" : "fullscreen";
const app = await createApp(App, null, terminalOptions(mode));
app.mount();
await app.waitUntilExit();
