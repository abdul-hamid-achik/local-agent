import vueTermui from "@local-agent/vue-tui/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [vueTermui({ entry: "src/main.ts" })],
  build: { outDir: "dist" },
});
