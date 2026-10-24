import { z } from "zod";
import { canonicalModel, getJSON } from "#runtime/admission";
import type { Provider } from "#runtime/config";

/** The provider's model as its server holds it loaded (Ollama's GET /api/ps). */
export type LoadedModel = {
  /** Bytes the loaded model takes: weights and KV cache together. */
  sizeBytes: number;
  /** Of those, the bytes in GPU memory. */
  vramBytes: number;
  /** The context it was loaded with, when the server says. */
  contextLength?: number;
};

/** What one read-only look at the provider's endpoint found; nothing guessed. */
export type ProviderProbe = {
  /** Round trip of the version (Ollama) or model list (OpenAI-compatible) request. */
  latencyMs?: number;
  /** Ollama's version string. */
  version?: string;
  /** Why the endpoint did not answer. */
  error?: string;
  /**
   * Ollama only: the model as loaded, `null` when /api/ps answered without
   * it, absent when /api/ps did not answer.
   */
  loaded?: LoadedModel | null;
};

const PROBE_TIMEOUT_MS = 5_000;

const PsSchema = z.object({
  models: z.array(
    z
      .object({
        name: z.unknown().optional(),
        model: z.unknown().optional(),
        size: z.unknown().optional(),
        size_vram: z.unknown().optional(),
        context_length: z.unknown().optional(),
      })
      .passthrough(),
  ),
});

const bytes = (value: unknown) =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value
    : undefined;

/**
 * Ollama's GET /api/ps entry for the provider's model: `null` when it is
 * not loaded, undefined when that cannot be told (an OpenAI-compatible
 * endpoint, no answer, or an entry without sizes). Read-only.
 */
export async function loadedModel(
  provider: Provider,
  signal: AbortSignal,
): Promise<LoadedModel | null | undefined> {
  if (provider.type === "openai_local") return undefined;
  try {
    const ps = PsSchema.parse(
      await getJSON(
        `${provider.base_url}/api/ps`,
        AbortSignal.any([signal, AbortSignal.timeout(PROBE_TIMEOUT_MS)]),
        { method: "GET" },
      ),
    );
    const wanted = canonicalModel(provider.model);
    const entry = ps.models.find(
      (item) =>
        canonicalModel(String(item.model ?? item.name ?? "")) === wanted,
    );
    if (!entry) return null;
    const size = bytes(entry.size);
    const vram = bytes(entry.size_vram);
    if (!size || vram === undefined) return undefined;
    const context = bytes(entry.context_length);
    return {
      sizeBytes: size,
      vramBytes: vram,
      ...(context ? { contextLength: context } : {}),
    };
  } catch {
    return undefined;
  }
}

/**
 * Asks the provider's endpoint, read-only, whether it answers and how fast:
 * Ollama's GET /api/version, then GET /api/ps for the model; an
 * OpenAI-compatible server's GET /models (a 404 is still an answer).
 * Never loads, pulls or runs anything.
 */
export async function probeProvider(
  provider: Provider,
  signal: AbortSignal,
  clock: () => number = () => performance.now(),
): Promise<ProviderProbe> {
  const budget = AbortSignal.any([
    signal,
    AbortSignal.timeout(PROBE_TIMEOUT_MS),
  ]);
  const started = clock();
  try {
    if (provider.type === "openai_local") {
      const response = await fetch(`${provider.base_url}/models`, {
        method: "GET",
        signal: budget,
        redirect: "error",
        ...(provider.api_key
          ? { headers: { authorization: `Bearer ${provider.api_key}` } }
          : {}),
      });
      await response.body?.cancel();
      const latencyMs = Math.round(clock() - started);
      return response.ok || response.status === 404
        ? { latencyMs }
        : { latencyMs, error: `GET /models answered ${response.status}` };
    }
    const raw = await getJSON(`${provider.base_url}/api/version`, budget, {
      method: "GET",
    });
    const latencyMs = Math.round(clock() - started);
    const version = z
      .object({ version: z.string().min(1).max(64) })
      .safeParse(raw);
    const loaded = await loadedModel(provider, signal);
    return {
      latencyMs,
      ...(version.success ? { version: version.data.version } : {}),
      ...(loaded !== undefined ? { loaded } : {}),
    };
  } catch (error) {
    return {
      error: signal.aborted
        ? "cancelled"
        : budget.aborted
          ? `no answer in ${PROBE_TIMEOUT_MS / 1000}s`
          : error instanceof Error
            ? error.message
            : "unreachable",
    };
  }
}

/**
 * The warning a loaded model earns when it is not as asked: partly outside
 * GPU memory (the rest runs on the CPU, slower), or loaded with a smaller
 * context than the provider's. Undefined when it is as asked, or unknown.
 */
export function placementWarning(
  provider: Pick<Provider, "model" | "context_size">,
  loaded: LoadedModel | null | undefined,
): string | undefined {
  if (!loaded) return undefined;
  const problems: string[] = [];
  if (loaded.vramBytes < loaded.sizeBytes)
    problems.push(
      // Floored, so a model one byte short never reads as 100%.
      `only ${Math.floor((loaded.vramBytes / loaded.sizeBytes) * 100)}% of it is in GPU memory; the rest runs on the CPU, slower`,
    );
  if (
    loaded.contextLength !== undefined &&
    loaded.contextLength < provider.context_size
  )
    problems.push(
      `it is loaded with a context of ${loaded.contextLength} tokens, below the ${provider.context_size} asked`,
    );
  return problems.length
    ? `${provider.model}: ${problems.join("; and ")}`
    : undefined;
}
