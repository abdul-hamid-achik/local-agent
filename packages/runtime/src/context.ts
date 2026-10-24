// The context size a run asks Ollama for when the user did not choose one:
// what the model's KV cache needs per token, against the memory left once
// its weights are loaded. See `planContextSize` for the rule.

import { totalmem } from "node:os";
import { admitOllamaModel } from "#runtime/admission";
import type { Provider } from "#runtime/config";

const GIB = 1024 ** 3;
/** Compute buffer, Ollama's prompt cache and its checkpoints. */
const HEADROOM = 2.5 * GIB;
/**
 * The largest planned size. Memory is rarely the limit on a 16 GB machine;
 * prefill is: refilling 64k tokens after a trim takes about a minute on a
 * 4B model. A larger window stays one --context away.
 */
export const PLANNED_CONTEXT_MAX = 32_768;
const STEPS = [32_768, 24_576, 16_384, 12_288, 8_192];

export type ContextPlan = { context: number; reason: string };

const gib = (bytes: number) => `${(bytes / GIB).toFixed(1)} GiB`;

/**
 * The largest of 32k, 24k, 16k, 12k and 8k tokens whose KV cache fits, never
 * above the model's native context. What fits is 11/16 of the machine's
 * memory (about what Metal lets the GPU use), less the weights, less 2.5 GiB
 * for the compute buffer, Ollama's prompt cache and its checkpoints. With
 * no KV facts the size stays at the long-standing 16384; when nothing fits,
 * 16384 or 12288 still stands if its cache fits without the headroom (as
 * it ran before planning), and 8192 otherwise.
 */
export function planContextSize(facts: {
  nativeContext?: number;
  weightsBytes: number;
  kvBytesPerToken?: number;
  totalMemory: number;
}): ContextPlan {
  const native = facts.nativeContext ?? Number.POSITIVE_INFINITY;
  const kv = facts.kvBytesPerToken;
  if (kv === undefined)
    return {
      context: Math.min(16_384, native),
      reason: "the default: the model does not report its KV cache size",
    };
  const budget = (facts.totalMemory * 11) / 16 - facts.weightsBytes - HEADROOM;
  for (const step of STEPS) {
    if (step > native) continue;
    if (step * kv <= budget)
      return {
        context: step,
        reason: `planned: its KV cache takes ${gib(step * kv)} of the ${gib(Math.max(0, budget))} left after the weights`,
      };
  }
  // Memory is tight: the long-standing 16384 (or 12288) still stands when
  // its cache fits without the headroom, as it ran before the planner.
  const bare = budget + HEADROOM;
  const fallback =
    [16_384, 12_288].find((step) => step <= native && step * kv <= bare) ??
    Math.min(8_192, native);
  return {
    context: fallback,
    reason: `planned: memory is tight (${gib(Math.max(0, budget))} left after the weights and headroom)`,
  };
}

/**
 * The provider with a planned `context_size`, for a local Ollama model whose
 * size the user did not set (--context, LOCAL_AGENT_CONTEXT). Chalupa and
 * openai_local keep theirs. When the model cannot be inspected the provider
 * is returned as it is: admission reports why on its own.
 */
export async function planContext(
  provider: Provider,
  signal: AbortSignal,
  totalMemory = totalmem(),
): Promise<Provider> {
  if (provider.type !== "ollama" || provider.context_explicit) return provider;
  // The plan picks the size, so admission is asked only for the model's
  // facts: the smallest size passes its native-context check.
  const admission = await admitOllamaModel(
    { ...provider, context_size: 512 },
    signal,
  ).catch(() => undefined);
  if (admission?.status !== "validated") return provider;
  const plan = planContextSize({
    nativeContext: admission.nativeContext,
    weightsBytes: admission.sizeBytes,
    kvBytesPerToken: admission.kvBytesPerToken,
    totalMemory,
  });
  return { ...provider, context_size: plan.context, context_plan: plan.reason };
}
