import { z } from "zod";
import { type Provider, ProviderSchema } from "#runtime/config";
import { withRetries } from "#runtime/retry";

type FailureKind = "transport" | "metadata" | "policy";

export type OllamaAdmission =
  | { status: "unverified"; kind: "transport"; message: string }
  | {
      status: "rejected";
      kind: Exclude<FailureKind, "transport">;
      message: string;
      /** The model is not in the inventory: the message is a complete instruction. */
      code?: "not_installed";
    }
  | {
      status: "validated";
      model: string;
      sizeBytes: number;
      nativeContext: number | undefined;
      /** f16 KV cache bytes per token of context, when model_info says. */
      kvBytesPerToken?: number;
      /**
       * Ollama serves it on its MLX runner, which ignores `num_ctx` and grows
       * the KV cache with the conversation (see `ollamaMLX`).
       */
      mlx?: true;
      capabilities: string[];
    };

const TagModelSchema = z
  .object({
    name: z.unknown().optional(),
    model: z.unknown().optional(),
    size: z.unknown().optional(),
    remote_model: z.unknown().optional(),
    remote_host: z.unknown().optional(),
  })
  .passthrough();
const TagsSchema = z.object({ models: z.array(TagModelSchema) });
const ShowSchema = z
  .object({
    capabilities: z.array(z.unknown()).optional(),
    model_info: z.record(z.string(), z.unknown()).optional(),
    details: z
      .object({
        context_length: z.unknown().optional(),
        runner: z.unknown().optional(),
        format: z.unknown().optional(),
      })
      .passthrough()
      .optional(),
    remote_model: z.unknown().optional(),
    remote_host: z.unknown().optional(),
  })
  .passthrough();
type TagModel = z.infer<typeof TagModelSchema>;
type ShowModel = z.infer<typeof ShowSchema>;

/** A model name as Ollama lists it: lowercased, without `:latest`. */
export const canonicalModel = (model: string) => {
  const value = model.trim().toLowerCase();
  return value.endsWith(":latest") ? value.slice(0, -7) : value;
};

const positiveInteger = (value: unknown) =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0
    ? value
    : undefined;

const contextLength = (show: ShowModel) => {
  const values = Object.entries(show.model_info ?? {})
    .filter(([key]) => key.endsWith(".context_length"))
    .map(([, value]) => positiveInteger(value))
    .filter((value): value is number => value !== undefined);
  const detail = positiveInteger(show.details?.context_length);
  if (detail !== undefined) values.push(detail);
  return values.length > 0 ? Math.max(...values) : undefined;
};

/**
 * Whether Ollama serves the model on its MLX runner (Apple silicon): there
 * `num_ctx` reserves nothing, the KV cache grows with the conversation and
 * Ollama never trims the history. The runner (`/api/show` `details.runner`,
 * `/api/ps` `runner`) decides when it is set ("mlx", "llamacpp" or "ggml");
 * for a model that predates the field the format decides, as Ollama's own
 * `manifest.MetadataForConfig` maps it (0.40.6): safetensors is MLX, gguf
 * and ggml are not.
 */
export function ollamaMLX(
  details:
    | { runner?: unknown; format?: unknown; [field: string]: unknown }
    | undefined,
): boolean {
  const runner = details?.runner;
  if (typeof runner === "string" && runner.trim())
    return runner.trim().toLowerCase() === "mlx";
  return (
    typeof details?.format === "string" &&
    details.format.toLowerCase() === "safetensors"
  );
}

const count = (value: unknown) =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0
    ? value
    : undefined;

/**
 * The f16 KV cache bytes one token of context costs, from `/api/show`
 * `model_info`: 2 (K and V) × KV heads × head size, over the layers that
 * keep a cache. A per-layer head count (an array) says which layers do; a
 * hybrid model's `full_attention_interval` says one in that many does.
 * Sliding-window layers are counted as full ones, which errs high.
 * Undefined when the fields are missing.
 */
export function kvBytesPerToken(
  modelInfo: Record<string, unknown> | undefined,
): number | undefined {
  const arch = modelInfo?.["general.architecture"];
  if (!modelInfo || typeof arch !== "string") return undefined;
  const field = (key: string) => modelInfo[`${arch}.${key}`];
  const blocks = count(field("block_count"));
  const heads = count(field("attention.head_count"));
  const embedding = count(field("embedding_length"));
  const keyLength =
    count(field("attention.key_length")) ??
    (embedding && heads ? embedding / heads : undefined);
  const valueLength = count(field("attention.value_length")) ?? keyLength;
  if (!blocks || !keyLength || !valueLength) return undefined;
  const kvHeads = field("attention.head_count_kv");
  let cachedHeads: number;
  if (Array.isArray(kvHeads))
    cachedHeads = kvHeads.reduce<number>(
      (total, value) => total + (count(value) ?? 0),
      0,
    );
  else {
    const perLayer = count(kvHeads) ?? heads;
    if (!perLayer) return undefined;
    const interval = count(field("full_attention_interval"));
    cachedHeads =
      perLayer *
      (interval && interval > 1 ? Math.ceil(blocks / interval) : blocks);
  }
  if (cachedHeads === 0) return undefined;
  return cachedHeads * (keyLength + valueLength) * 2;
}

const maxMetadataBytes = 1 << 20;
class MetadataError extends Error {}

/** Reads a response body, refusing more than 1 MiB. */
async function readCapped(response: Response, what: string) {
  if (!response.body) throw new MetadataError(`${what} had no body`);
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) break;
    total += chunk.value.byteLength;
    if (total > maxMetadataBytes) {
      await reader.cancel();
      throw new MetadataError(`${what} exceeds size limit`);
    }
    chunks.push(chunk.value);
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

/**
 * GET or POST JSON metadata, redirects refused and the body capped at 1 MiB.
 * `retry` waits out a refused connection or a 503 "model loading" (see
 * withRetries); liveness probes leave it off, as they report the first answer.
 */
export async function getJSON(
  url: string,
  signal: AbortSignal,
  init?: RequestInit,
  retry = false,
): Promise<unknown> {
  const send = () => fetch(url, { ...init, signal, redirect: "error" });
  const response = retry
    ? await withRetries(send, signal, init?.body)
    : await send();
  if (!response.ok)
    throw new Error(`Ollama metadata request failed (${response.status})`);
  return JSON.parse(
    new TextDecoder().decode(
      await readCapped(response, "Ollama metadata response"),
    ),
  );
}

/**
 * Proves that the negotiated model is present at this exact Ollama endpoint.
 * Reachability alone returns `unverified`; remote profiles fail closed when
 * tags/show omit identity, weight, capability, or native-context evidence.
 */
export async function admitOllamaModel(
  provider: Provider,
  signal: AbortSignal,
): Promise<OllamaAdmission> {
  const config = ProviderSchema.parse(provider);
  const base = config.base_url;
  let entry: TagModel | undefined;
  let show: ShowModel;
  const metadataSignal = AbortSignal.any([signal, AbortSignal.timeout(10_000)]);
  try {
    const rawTags = await getJSON(
      `${base}/api/tags`,
      metadataSignal,
      { method: "GET" },
      true,
    );
    const tags = TagsSchema.parse(rawTags);
    const wanted = canonicalModel(config.model);
    entry = tags.models.find(
      (item) =>
        canonicalModel(String(item.model ?? item.name ?? "")) === wanted,
    );
    if (!entry) {
      const unprefixed = config.model.startsWith("ollama/")
        ? config.model.slice(7)
        : undefined;
      const hint =
        unprefixed &&
        tags.models.some(
          (item) =>
            canonicalModel(String(item.model ?? item.name ?? "")) ===
            canonicalModel(unprefixed),
        );
      // Local Ollama can pull the model; a remote (Chalupa) profile cannot.
      const message = hint
        ? `Ollama model ${config.model} was not listed. Use --model ${unprefixed} without the ollama/ prefix.`
        : config.type === "ollama"
          ? `Model ${config.model} is not installed. Run: ollama pull ${config.model}`
          : `Ollama model ${config.model} is not on the Chalupa host yet. Run chalupa status to see the download, or set inference.pull: true in chalupa.yml.`;
      return {
        status: "rejected",
        kind: "metadata",
        message,
        ...(config.type === "ollama" && !hint
          ? { code: "not_installed" as const }
          : {}),
      };
    }
    const rawShow = await getJSON(
      `${base}/api/show`,
      metadataSignal,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ model: config.model, verbose: false }),
      },
      true,
    );
    show = ShowSchema.parse(rawShow);
  } catch (error) {
    if (
      error instanceof MetadataError ||
      error instanceof z.ZodError ||
      error instanceof SyntaxError
    )
      return {
        status: "rejected",
        kind: "metadata",
        message: "Ollama metadata response was malformed",
      };
    return {
      status: "unverified",
      kind: "transport",
      message:
        error instanceof Error
          ? error.message
          : "Ollama metadata is unreachable",
    };
  }
  if (!show.capabilities)
    return {
      status: "rejected",
      kind: "metadata",
      message: "Ollama /api/show omitted capabilities",
    };

  const sizeBytes = positiveInteger(entry.size);
  if (sizeBytes === undefined)
    return {
      status: "rejected",
      kind: "metadata",
      message: `Ollama model ${config.model} omitted a positive weight size`,
    };
  if (
    entry.remote_model ||
    entry.remote_host ||
    show.remote_model ||
    show.remote_host
  )
    return {
      status: "rejected",
      kind: "policy",
      message: `Ollama model ${config.model} redirects to another model or host`,
    };

  const capabilities = show.capabilities.filter(
    (value): value is string => typeof value === "string",
  );
  if (!capabilities.includes("completion"))
    return {
      status: "rejected",
      kind: "policy",
      message: `Ollama model ${config.model} lacks completion capability`,
    };
  const nativeContext = contextLength(show);
  if (config.type === "ollama_remote") {
    if (
      config.max_model_bytes === undefined ||
      sizeBytes > config.max_model_bytes
    )
      return {
        status: "rejected",
        kind: "policy",
        message: `Ollama model ${config.model} exceeds the negotiated weight limit`,
      };
    if (!capabilities.includes("tools"))
      return {
        status: "rejected",
        kind: "policy",
        message: `Ollama model ${config.model} lacks tools capability`,
      };
    if (nativeContext === undefined)
      return {
        status: "rejected",
        kind: "metadata",
        message: `Ollama model ${config.model} omitted native context metadata`,
      };
  }
  if (nativeContext !== undefined && nativeContext < config.context_size)
    return {
      status: "rejected",
      kind: "policy",
      message: `Ollama model ${config.model} cannot satisfy context_size ${config.context_size}${config.type === "ollama" ? `; its native context is ${nativeContext}, so use --context ${nativeContext} or less` : ""}`,
    };
  return {
    status: "validated",
    model: config.model,
    sizeBytes,
    nativeContext,
    kvBytesPerToken: kvBytesPerToken(show.model_info),
    ...(ollamaMLX(show.details) ? { mlx: true as const } : {}),
    capabilities,
  };
}

export type OpenAILocalAdmission =
  | { status: "unverified"; message: string }
  | { status: "rejected"; message: string }
  | { status: "validated"; model: string };

/**
 * Checks the configured model against GET {base_url}/models (15s budget).
 * Servers that do not implement the endpoint (404) skip validation: a
 * streaming warm-up would double the first chat request and cost tokens,
 * while the chat transport itself still fails loudly on an unknown model.
 */
export async function admitOpenAILocalModel(
  provider: Provider,
  signal: AbortSignal,
): Promise<OpenAILocalAdmission> {
  const config = ProviderSchema.parse(provider);
  if (config.type !== "openai_local")
    throw new Error("admitOpenAILocalModel requires an openai_local provider");
  const metadataSignal = AbortSignal.any([signal, AbortSignal.timeout(15_000)]);
  try {
    const response = await withRetries(
      () =>
        fetch(`${config.base_url}/models`, {
          method: "GET",
          signal: metadataSignal,
          redirect: "error",
          ...(config.api_key
            ? { headers: { authorization: `Bearer ${config.api_key}` } }
            : {}),
        }),
      metadataSignal,
    );
    if (response.status === 404)
      return {
        status: "unverified",
        message:
          "Server does not expose GET /v1/models; skipped model validation",
      };
    if (!response.ok)
      return {
        status: "unverified",
        message: `Model list request failed (${response.status})`,
      };
    // A body we cannot read as a model list proves nothing about the model:
    // say so, instead of "unreachable" or an empty list that rejects it.
    let payload: unknown;
    try {
      payload = JSON.parse(
        new TextDecoder().decode(
          await readCapped(response, "Model list response"),
        ),
      );
    } catch (error) {
      if (error instanceof MetadataError)
        return {
          status: "unverified",
          message: `${error.message}; skipped model validation`,
        };
      if (error instanceof SyntaxError)
        return {
          status: "unverified",
          message:
            "Model list response was not valid JSON; skipped model validation",
        };
      throw error;
    }
    const listed = z.object({ data: z.array(z.unknown()) }).safeParse(payload);
    if (!listed.success)
      return {
        status: "unverified",
        message:
          "Model list response had no `data` array; skipped model validation",
      };
    const models = listed.data.data;
    const ids = models
      .map((entry) =>
        typeof entry === "object" && entry !== null && "id" in entry
          ? String(entry.id)
          : undefined,
      )
      .filter((id): id is string => id !== undefined);
    if (!ids.includes(config.model))
      return {
        status: "rejected",
        message: `Model ${config.model} is not served by ${config.base_url}. Available: ${ids.slice(0, 5).join(", ") || "(none)"}.`,
      };
    return { status: "validated", model: config.model };
  } catch {
    return {
      status: "unverified",
      message: "OpenAI-compatible endpoint is unreachable",
    };
  }
}
