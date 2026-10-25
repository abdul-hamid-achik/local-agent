// The context size a run asks Ollama for when the user did not choose one:
// what the model's KV cache needs per token, against the memory left once
// its weights are loaded. See `planContextSize` for the rule.

import { totalmem } from "node:os";
import { admitOllamaModel, type OllamaAdmission } from "#runtime/admission";
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
/** Below this, a Chalupa-negotiated context is worth a warning. */
const LOW_REMOTE_CONTEXT = 16_384;

export type ContextPlan = { context: number; reason: string; warning?: string };

/** What the planner and `explicitContextNote` weigh a size against. */
export type ContextFacts = {
  nativeContext?: number;
  weightsBytes: number;
  /** f16 KV cache bytes per token of context. */
  kvBytesPerToken?: number;
  /** f16 bytes of its sliding-window caches, whatever the context. */
  kvWindowBytes?: number;
  totalMemory: number;
  /** Ollama serves it on MLX: the same size, but only as local-agent's own. */
  mlx?: boolean;
};

/** The facts a validated local Ollama admission gives the planner. */
export function contextFacts(
  admission: Extract<OllamaAdmission, { status: "validated" }>,
  totalMemory: number,
): ContextFacts {
  return {
    nativeContext: admission.nativeContext,
    weightsBytes: admission.sizeBytes,
    kvBytesPerToken: admission.kvBytesPerToken,
    ...(admission.kvWindowBytes
      ? { kvWindowBytes: admission.kvWindowBytes }
      : {}),
    totalMemory,
    ...(admission.mlx ? { mlx: true } : {}),
  };
}

const gib = (bytes: number) => `${(bytes / GIB).toFixed(1)} GiB`;

/**
 * On Ollama's MLX runner the size is local-agent's alone: it keeps each
 * request within it, but nothing is reserved at load (see `ollamaMLX`).
 */
const MLX_NOTE =
  "local-agent keeps each request within it (Ollama's MLX runner ignores num_ctx and grows its cache with the conversation)";
/** The warning a model on the MLX runner earns, with the way out. */
export const MLX_WARNING =
  "this model runs on Ollama's MLX runner, which ignores the context size and grows its memory with the conversation; a GGUF tag (one ending in -q4_K_M, say) runs on llama.cpp, where the size is reserved at load";

/**
 * The largest of 32k, 24k, 16k, 12k and 8k tokens whose KV cache fits, never
 * above the model's native context. What fits is 11/16 of the machine's
 * memory (about what Metal lets the GPU use), less the weights, less 2.5 GiB
 * for the compute buffer, Ollama's prompt cache and its checkpoints. The KV
 * cache is the per-token part times the size plus the sliding-window
 * caches, which do not grow with it. With
 * no KV facts the size stays at the long-standing 16384; when nothing fits,
 * 16384 or 12288 still stands if its cache fits without the headroom (as
 * it ran before planning), and 8192 otherwise. On Ollama's MLX runner the
 * size is the same, as the most local-agent sends in one request, with a
 * warning: that runner reserves nothing for it.
 */
export function planContextSize(facts: ContextFacts): ContextPlan {
  const plan = fitContext(facts);
  return facts.mlx
    ? {
        context: plan.context,
        reason: `planned: ${MLX_NOTE}`,
        warning: MLX_WARNING,
      }
    : plan;
}

function fitContext(facts: ContextFacts): ContextPlan {
  const native = facts.nativeContext ?? Number.POSITIVE_INFINITY;
  const kv = facts.kvBytesPerToken;
  if (kv === undefined)
    return {
      context: Math.min(16_384, native),
      reason: "the default: the model does not report its KV cache size",
    };
  const budget = (facts.totalMemory * 11) / 16 - facts.weightsBytes - HEADROOM;
  const cache = (size: number) => size * kv + (facts.kvWindowBytes ?? 0);
  for (const step of STEPS) {
    if (step > native) continue;
    if (cache(step) <= budget)
      return {
        context: step,
        reason: `planned: its KV cache takes ${gib(cache(step))} of the ${gib(Math.max(0, budget))} left after the weights and headroom`,
      };
  }
  // Memory is tight: the long-standing 16384 (or 12288) still stands when
  // its cache fits without the headroom, as it ran before the planner.
  const bare = budget + HEADROOM;
  const fallback =
    [16_384, 12_288].find((step) => step <= native && cache(step) <= bare) ??
    Math.min(8_192, native);
  return {
    context: fallback,
    reason: `planned: memory is tight (${gib(Math.max(0, budget))} left after the weights and headroom)`,
  };
}

/**
 * What a size the user set (--context, LOCAL_AGENT_CONTEXT, or the Tuimark
 * client's /context, named by `setBy`) costs: it is kept, and a warning says
 * when its KV cache needs more than the memory left after the weights and
 * headroom, the planner's own budget (Ollama then runs part of the model on
 * the CPU, or fails to load it). On the MLX runner it warns that the size
 * reserves nothing.
 */
export function explicitContextNote(
  facts: ContextFacts,
  context: number,
  setBy = "--context or LOCAL_AGENT_CONTEXT",
): { reason: string; warning?: string } {
  // Nothing is reserved on MLX, so its cost cannot be told from the size.
  if (facts.mlx)
    return {
      reason: `set by ${setBy}; ${MLX_NOTE}`,
      warning: MLX_WARNING,
    };
  const kv = facts.kvBytesPerToken;
  if (kv === undefined) return { reason: `set by ${setBy}` };
  const need = context * kv + (facts.kvWindowBytes ?? 0);
  const left = Math.max(
    0,
    (facts.totalMemory * 11) / 16 - facts.weightsBytes - HEADROOM,
  );
  if (need <= left)
    return {
      reason: `set by ${setBy}; its KV cache takes ${gib(need)} of the ${gib(left)} left after the weights and headroom`,
    };
  const warning = `context ${context} needs a ${gib(need)} KV cache, more than the ${gib(left)} left after the weights and headroom: Ollama may run part of the model on the CPU, or fail to load it`;
  return {
    reason: `set by ${setBy}; ${warning}`,
    warning,
  };
}

/**
 * The provider with a planned `context_size`, for a local Ollama model whose
 * size the user did not set (--context, LOCAL_AGENT_CONTEXT); a size the
 * user set is kept, with what it costs (`explicitContextNote`). Chalupa's
 * size is kept, with a warning when it is small; openai_local keeps its
 * own. When the model cannot be inspected the provider is returned as it
 * is: admission reports why on its own.
 */
export async function planContext(
  provider: Provider,
  signal: AbortSignal,
  totalMemory = totalmem(),
): Promise<Provider> {
  return (await planContextAdmitted(provider, signal, totalMemory)).provider;
}

/**
 * `planContext`, with the local Ollama admission it made. That admission
 * holds for the provider it returns (a planned size never exceeds the
 * native context, and a size the user set is the one checked), so a
 * client can refuse a missing model without asking Ollama again.
 */
export async function planContextAdmitted(
  provider: Provider,
  signal: AbortSignal,
  totalMemory = totalmem(),
): Promise<{ provider: Provider; admission?: OllamaAdmission }> {
  if (provider.type === "ollama_remote")
    return {
      provider: {
        ...provider,
        // Negotiated by chalupa exec and launch; exported as it is by
        // chalupa connect.
        context_plan: "set by Chalupa",
        // Chalupa's automatic size can go well below what the planner gives
        // a local model; long turns then compact early.
        ...(provider.context_size < LOW_REMOTE_CONTEXT
          ? {
              context_warning: `Chalupa set a context of ${provider.context_size} tokens: long turns are compacted early. A larger GPU or a smaller model gives more.`,
            }
          : {}),
      },
    };
  if (provider.type !== "ollama") return { provider };
  // A planned size is asked only for the model's facts: the smallest size
  // passes its native-context check. A size the user set is checked as it is.
  const admission = await admitOllamaModel(
    provider.context_explicit ? provider : { ...provider, context_size: 512 },
    signal,
  ).catch(() => undefined);
  if (admission?.status !== "validated")
    return { provider, ...(admission ? { admission } : {}) };
  if (provider.context_explicit) {
    const note = explicitContextNote(
      contextFacts(admission, totalMemory),
      provider.context_size,
    );
    return {
      provider: {
        ...provider,
        context_plan: note.reason,
        ...(note.warning ? { context_warning: note.warning } : {}),
      },
      admission,
    };
  }
  const plan = planContextSize(contextFacts(admission, totalMemory));
  return {
    provider: {
      ...provider,
      context_size: plan.context,
      context_plan: plan.reason,
      ...(plan.warning ? { context_warning: plan.warning } : {}),
    },
    admission,
  };
}
