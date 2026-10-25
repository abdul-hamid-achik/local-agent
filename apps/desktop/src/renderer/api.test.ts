import { afterEach, expect, test, vi } from "vitest";
import { reactive } from "vue";
import { api, errorMessage, rpc } from "#desktop/renderer/api";

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

// The stream table is module state wired once to the first bridge, so each
// test gets a fresh module and a bridge it can push envelopes through.
async function streamsOnFreshModule() {
  vi.resetModules();
  let deliver: (envelope: unknown) => void = () => {};
  (globalThis as { window?: unknown }).window = {
    desktop: {
      onStream: (callback: (envelope: unknown) => void) => {
        deliver = callback;
        return () => {};
      },
    },
  };
  const fresh = await import("#desktop/renderer/api");
  const emit = (stream: string, event: unknown) => deliver({ stream, event });
  return { subscribeStream: fresh.subscribeStream, emit };
}

test("a stream unsubscribe called twice leaves a later subscriber's Set alone", async () => {
  const { subscribeStream, emit } = await streamsOnFreshModule();
  const first: unknown[] = [];
  const second: unknown[] = [];
  const unsubscribeFirst = subscribeStream("chat", (event) =>
    first.push(event),
  );
  unsubscribeFirst();
  subscribeStream("chat", (event) => second.push(event));
  unsubscribeFirst();
  emit("chat", "after");
  expect(first).toEqual([]);
  expect(second).toEqual(["after"]);
});

test("a stream unsubscribe keeps the other subscribers of its stream", async () => {
  const { subscribeStream, emit } = await streamsOnFreshModule();
  const kept: unknown[] = [];
  const dropped: unknown[] = [];
  subscribeStream("chat", (event) => kept.push(event));
  const unsubscribe = subscribeStream("chat", (event) => dropped.push(event));
  unsubscribe();
  emit("chat", "x");
  expect(kept).toEqual(["x"]);
  expect(dropped).toEqual([]);
});

// The promise-returning wrappers reject like rpc() does; throwing before a
// promise exists skipped every .catch and try/await around them.
test("a missing bridge rejects the promise-returning wrappers", async () => {
  (globalThis as { window?: unknown }).window = {};
  const calls: Promise<unknown>[] = [];
  const reject = (call: () => Promise<unknown>) => {
    let returned: Promise<unknown> | undefined;
    expect(() => {
      returned = call();
    }).not.toThrow();
    calls.push(returned as Promise<unknown>);
  };
  reject(() => api.repoRoot());
  reject(() => api.restartSidecar());
  reject(() => api.setProviderEnv({}));
  reject(() => api.getProviderEnv());
  reject(() => api.pick({ kind: "directory" }));
  reject(() => api.readTextFile("a"));
  reject(() => api.writeTextFile("a", "b"));
  reject(() => api.openExternal("https://example.com"));
  await Promise.all(
    calls.map((call) =>
      expect(call).rejects.toThrow("Desktop bridge is unavailable"),
    ),
  );
});

// A subscription made before the bridge exists fails; once the bridge is
// there the next one must wire it, not find the stream "already wired".
test("a stream subscribed to before the bridge exists is wired by the next try", async () => {
  vi.resetModules();
  (globalThis as { window?: unknown }).window = {};
  const fresh = await import("#desktop/renderer/api");
  expect(() => fresh.subscribeStream("chat", () => {})).toThrow(
    "Desktop bridge is unavailable",
  );
  let deliver: (envelope: unknown) => void = () => {};
  (globalThis as { window?: unknown }).window = {
    desktop: {
      onStream: (callback: (envelope: unknown) => void) => {
        deliver = callback;
        return () => {};
      },
    },
  };
  const seen: unknown[] = [];
  fresh.subscribeStream("chat", (event) => seen.push(event));
  deliver({ stream: "chat", event: "hello" });
  expect(seen).toEqual(["hello"]);
});

test("errorMessage keeps the reason of a rejection that is not an Error", () => {
  expect(errorMessage(new Error("boom"))).toBe("boom");
  expect(errorMessage("Sidecar is not running")).toBe("Sidecar is not running");
  expect(errorMessage({ message: "from a plain object" })).toBe(
    "from a plain object",
  );
  // Anything else says nothing of itself: no stringified objects.
  expect(errorMessage("")).toBe("Request failed");
  expect(errorMessage({ code: 1 })).toBe("Request failed");
  expect(errorMessage(undefined)).toBe("Request failed");
});
