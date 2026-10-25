import vue from "@vitejs/plugin-vue";
import { defineConfig, type Plugin } from "vite";

const DEV_ORIGIN = "127.0.0.1:5174";

// index.html carries the packaged app's policy. The dev page also talks to
// its Vite server (HMR), so only `vite` serving adds that origin.
const devConnectSrc: Plugin = {
  name: "dev-connect-src",
  apply: "serve",
  transformIndexHtml: (html) =>
    html.replace(
      "connect-src 'self';",
      `connect-src 'self' http://${DEV_ORIGIN} ws://${DEV_ORIGIN};`,
    ),
};

export default defineConfig({
  plugins: [vue(), devConnectSrc],
  root: ".",
  base: "./",
  build: {
    outDir: "dist-renderer",
    emptyOutDir: true,
  },
  server: {
    host: "127.0.0.1",
    port: 5174,
    strictPort: true,
  },
});
