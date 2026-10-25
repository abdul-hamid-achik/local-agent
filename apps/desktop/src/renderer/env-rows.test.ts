import { expect, test } from "vitest";
import { type EnvRow, envFromRows } from "#desktop/renderer/env-rows";

const row = (key: string, value = "v"): EnvRow => ({
  key,
  value,
  shown: false,
});

test("rows become the environment; blank names are skipped and names trimmed", () => {
  expect(
    envFromRows([row(" LOCAL_AGENT_THINK ", "0"), row(""), row("   ", "x")]),
  ).toEqual({ env: { LOCAL_AGENT_THINK: "0" } });
});

test("a name given twice is refused, not resolved to the last value", () => {
  expect(
    envFromRows([row("OPENAI_API_KEY", "sk-old"), row("OPENAI_API_KEY ", "x")]),
  ).toEqual({ error: "Variable OPENAI_API_KEY is set more than once" });
});

test("a name the runtime would not take is refused", () => {
  expect(envFromRows([row("lower")])).toEqual({
    error: "Invalid variable name: lower",
  });
});
