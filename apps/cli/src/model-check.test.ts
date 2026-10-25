import type { OllamaAdmission, Provider } from "@local-agent/runtime";
import { expect, test } from "vitest";
import { checkModel, settledNow } from "#cli/model-check";

const resolved = {
  type: "ollama",
  base_url: "http://127.0.0.1:11434",
  model: "m",
  context_size: 16384,
} as Provider;
const planned = { ...resolved, context_size: 8192 } as Provider;
const signal = new AbortController().signal;

const admission = (
  status: "validated" | "rejected",
  message = "Model m is not installed. Run: ollama pull m",
) => ({ status, message }) as unknown as OllamaAdmission;

test("an interactive check plans the context, and refuses a model the plan found missing with the line the plan carries", async () => {
  const asked: string[] = [];
  const deps = {
    plan: async () => ({
      provider: planned,
      admission: admission("rejected"),
    }),
    admit: async () => {
      asked.push("admit");
      return admission("validated");
    },
  };
  await expect(checkModel(resolved, signal, true, deps)).rejects.toThrow(
    "Model m is not installed. Run: ollama pull m",
  );
  // The plan's own admission already answered: Ollama is not asked twice.
  expect(asked).toEqual([]);
  expect(
    await checkModel(resolved, signal, true, {
      ...deps,
      plan: async () => ({
        provider: planned,
        admission: admission("validated"),
      }),
    }),
  ).toBe(planned);
});

test("a plan that could not look leaves the provider and asks the endpoint itself", async () => {
  const asked: Provider[] = [];
  const deps = {
    plan: async () => ({ provider: resolved }),
    admit: async (provider: Provider) => {
      asked.push(provider);
      return admission("rejected");
    },
  };
  await expect(checkModel(resolved, signal, true, deps)).rejects.toThrow(
    "not installed",
  );
  expect(asked).toEqual([resolved]);
});

test("a headless check only plans: the model is the agent's to admit", async () => {
  const provider = await checkModel(resolved, signal, false, {
    plan: async () => ({ provider: planned, admission: admission("rejected") }),
    admit: async () => {
      throw new Error("headless asks nothing more");
    },
  });
  expect(provider).toBe(planned);
});

test("settledNow reports what has settled and never waits for the rest", async () => {
  expect(await settledNow(Promise.resolve(3))).toEqual({ value: 3 });
  const failure = new Error("no");
  expect(await settledNow(Promise.reject(failure))).toEqual({
    error: failure,
  });
  expect(await settledNow(new Promise(() => {}))).toBeUndefined();
});
