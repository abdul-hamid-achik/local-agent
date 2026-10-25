import { createApp } from "vue";
import App from "#desktop/renderer/App.vue";
import { store } from "#desktop/renderer/store";
import { reportUiError } from "#desktop/renderer/ui-error";
import "./styles.css";

const app = createApp(App);
// One place for what no component handled: logged, and shown by App.
app.config.errorHandler = (error, _instance, info) => {
  store.uiError = reportUiError(error, info);
};
app.mount("#app");
