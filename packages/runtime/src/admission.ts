import { z } from "zod";
import { type Provider, ProviderSchema } from "#runtime/config";

type FailureKind = "transport" | "metadata" | "policy";

export type OllamaAdmission =
  | { status: "unverified"; kind: "transport"; message: string }
  | {
      status: "rejected";
      kind: Exclude<FailureKind, "transport">;
      message: string;
    }
  | {
      status: "validated";
      model: string;
      sizeBytes: number;
      nativeContext: number | undefined;
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
      .object({ context_length: z.unknown().optional() })
      .passthrough()
      .optional(),
    remote_model: z.unknown().optional(),
    remote_host: z.unknown().optional(),
  })
  .passthrough();
type TagModel = z.infer<typeof TagModelSchema>;
type ShowModel = z.infer<typeof ShowSchema>;

const canonicalModel = (model: string) => {
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

const maxMetadataBytes = 1 << 20;
class MetadataError extends Error {}

async function getJSON(
  url: string,
  signal: AbortSignal,
  init?: RequestInit,
): Promise<unknown> {
  const response = await fetch(url, { ...init, signal, redirect: "error" });
  if (!response.ok)
    throw new Error(`Ollama metadata request failed (${response.status})`);
  if (!response.body)
    throw new MetadataError("Ollama metadata response had no body");
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) break;
    total += chunk.value.byteLength;
    if (total > maxMetadataBytes) {
      await reader.cancel();
      throw new MetadataError("Ollama metadata response exceeds size limit");
    }
    chunks.push(chunk.value);
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return JSON.parse(new TextDecoder().decode(bytes));
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
    const rawTags = await getJSON(`${base}/api/tags`, metadataSignal, {
      method: "GET",
    });
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
      return {
        status: "rejected",
        kind: "metadata",
        message: `Ollama model ${config.model} was not listed.${hint ? ` Use --model ${unprefixed} without the ollama/ prefix.` : " Check the installed model name with ollama list."}`,
      };
    }
    const rawShow = await getJSON(`${base}/api/show`, metadataSignal, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ model: config.model, verbose: false }),
    });
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
      message: `Ollama model ${config.model} cannot satisfy context_size ${config.context_size}`,
    };
  return {
    status: "validated",
    model: config.model,
    sizeBytes,
    nativeContext,
    capabilities,
  };
}
