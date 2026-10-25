import { expect, test } from "vitest";
import { FakeHost } from "#cli/tuimark/fake-host";

test("a batch with a write the store cannot take changes nothing and is acked ok: false", async () => {
  const ui = new FakeHost();
  ui.store = { live: { blocks: [] }, thinking: { runs: "not a list" } };
  const ack = await ui.batch([
    { path: "status", value: "never shown" },
    { path: "live.blocks", op: "append", value: "x" },
    { path: "thinking.runs", op: "append", value: 1 },
  ]);
  expect(ack.ok).toBe(false);
  expect(ack.error).toContain("thinking.runs");
  // The writes before the refused one were undone with it.
  expect(ui.store).toEqual({
    live: { blocks: [] },
    thinking: { runs: "not a list" },
  });
  // The SDK wrote the batch to fd 3 all the same.
  expect(ui.batches).toHaveLength(1);
  expect(ui.bytes).toBeGreaterThan(0);
});

test("statusLog lists the statuses the store took, not the ones it refused", async () => {
  const ui = new FakeHost();
  ui.refuse = (_writes, index) => (index === 1 ? "pipe closed" : undefined);
  await ui.batch([{ path: "status", value: "first" }]);
  const refused = await ui.batch([{ path: "status", value: "refused" }]);
  await ui.batch([{ path: "status", value: "third" }]);
  expect(refused.ok).toBe(false);
  expect(ui.batches).toHaveLength(3);
  expect(ui.statusLog()).toEqual(["first", "third"]);
  expect(ui.store.status).toBe("third");
});

test("an undefined value is stored as null, as the wire carries it", async () => {
  const ui = new FakeHost();
  await ui.batch([
    { path: "draft", value: undefined },
    { path: "nested", value: { kept: 1, dropped: undefined } },
  ]);
  expect(await ui.get("draft")).toEqual({ found: true, value: null });
  expect(ui.store.nested).toEqual({ kept: 1 });
  expect(await ui.get("never-written")).toMatchObject({ found: false });
});

test("a second handler for an action replaces the first, as the SDK's on does", async () => {
  const ui = new FakeHost();
  const seen: string[] = [];
  ui.on("send", () => seen.push("first"));
  ui.on("send", () => seen.push("second"));
  await ui.fire("send");
  expect(seen).toEqual(["second"]);
});
