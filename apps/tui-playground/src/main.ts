import { createApp } from "@local-agent/vue-tui";
import App from "#playground/App.vue";

const app = await createApp(App, null);
app.mount();
await app.waitUntilExit();
