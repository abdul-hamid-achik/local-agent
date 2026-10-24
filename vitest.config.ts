import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    conditions: ["bun"],
    dedupe: ["@opentui/core", "vue", "@vue/runtime-core", "@vue/reactivity"],
  },
  ssr: {
    resolve: {
      externalConditions: ["bun"],
    },
  },
  test: {
    include: [
      "packages/*/src/**/*.test.ts",
      "apps/*/src/**/*.test.ts",
      "scripts/**/*.test.ts",
    ],
    // Every test file gets a private XDG_CONFIG_HOME, so the permission
    // trust store never reads or writes the developer's real one.
    setupFiles: ["./vitest.setup.ts"],
    pool: "forks",
    maxWorkers: 1,
    testTimeout: 10_000,
    server: {
      deps: {
        inline: ["vue-termui"],
        external: ["@opentui/core"],
      },
    },
  },
});
