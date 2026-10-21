import { expect, test } from "vitest";
import { SAMPLES } from "#cli/tuimark/fixtures";
import {
  approvalState,
  contextLabel,
  contextPct,
  initialStore,
  noApproval,
  tokens,
} from "#cli/tuimark/store";
import { Transcript } from "#cli/tuimark/transcript";

test("token counts read like the Vue sidebar", () => {
  expect(tokens(900)).toBe("900");
  expect(tokens(16_384)).toBe("16k");
  expect(tokens(32_768)).toBe("32k");
  expect(tokens(2048)).toBe("2.0k");
  expect(contextLabel(9_830, 32_768)).toBe("9.6k/32k");
  expect(contextPct(16_384, 32_768)).toBe(50);
  expect(contextPct(99_999, 32_768)).toBe(100);
});

test("the initial store has every path the document reads", () => {
  const store = initialStore({
    provider: { type: "ollama_remote", model: "m", context_size: 8192 },
    intent: "plan",
    mcpServers: 2,
    transcript: new Transcript().snapshot(),
  });
  expect(store.header).toMatchObject({
    provider: "Chalupa / remote",
    mode: "plan",
    session: "temporary",
  });
  expect(store.status).toContain("MCP 2");
  expect(Object.keys(store).sort()).toEqual(
    [
      "approval",
      "busy",
      "draft",
      "earlier",
      "found",
      "header",
      "help",
      "live",
      "messages",
      "palette",
      "placeholder",
      "status",
    ].sort(),
  );
  expect(store.approval).toEqual(noApproval());
});

test("the approval dialog says what is being approved", () => {
  const edit = approvalState({
    kind: "edit",
    path: "a.ts",
    before: "",
    after: "",
    diff: "--- a\n+++ b\n-old\n+new",
    identity: "i",
  });
  expect(edit).toMatchObject({
    open: true,
    title: "Edit a.ts?",
    danger: false,
  });
  expect(edit.lines.map((l) => [l.add, l.del])).toEqual([
    [false, false],
    [false, false],
    [false, true],
    [true, false],
  ]);
  const command = approvalState({
    kind: "command",
    argv: ["bun", "test"],
    cwd: "/w",
    timeoutMs: 1,
  });
  expect(command).toMatchObject({
    title: "Run command?",
    body: "bun test",
    danger: true,
  });
  expect(
    approvalState({ kind: "mcp", name: "fx__echo", argumentsJson: "{}" }),
  ).toMatchObject({
    title: "Call fx__echo?",
    danger: true,
  });
});

test("every committed sample is JSON the store can produce", () => {
  for (const make of Object.values(SAMPLES)) {
    const sample = JSON.parse(JSON.stringify(make())) as Record<
      string,
      unknown
    >;
    expect(Object.keys(sample).sort()).toEqual(
      Object.keys(
        initialStore({
          provider: { type: "ollama", model: "m", context_size: 1 },
          intent: "build",
          mcpServers: 0,
          transcript: new Transcript().snapshot(),
        }),
      ).sort(),
    );
  }
});
