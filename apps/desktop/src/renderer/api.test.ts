import { afterEach, expect, test } from "vitest";
import { reactive } from "vue";
import { rpc } from "#desktop/renderer/api";

afterEach(() => {
  delete (globalThis as { window?: unknown }).window;
});

// Electron's IPC structured-clones what it sends, and a Proxy cannot be
// cloned: a Vue reactive transcript failed every chat start with "An object
// could not be cloned".
test("rpc sends reactive state as the plain data the sidecar receives", async () => {
  const sent: unknown[] = [];
  (globalThis as { window?: unknown }).window = {
    desktop: {
      invoke: async (_method: string, params: unknown) => {
        sent.push(structuredClone(params));
        return { ok: true };
      },
    },
  };
  const state = reactive({
    messages: [{ role: "user", content: "hi" }],
  });
  await expect(
    rpc("agent.start", { runId: "r1", messages: state.messages }),
  ).resolves.toEqual({ ok: true });
  expect(sent).toEqual([
    { runId: "r1", messages: [{ role: "user", content: "hi" }] },
  ]);
  await rpc("hello");
  expect(sent.at(-1)).toBeUndefined();
});
