import { expect, test, vi } from "vitest";
import { kvBytesPerToken } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";
import { planContext, planContextSize } from "#runtime/context";

const GIB = 1024 ** 3;
const MAC_16GB = 16 * GIB;

// qwen3.5:4b as `ollama show --verbose` reports it: a hybrid model whose
// every fourth layer keeps a KV cache.
const qwen35 = {
  "general.architecture": "qwen35",
  "qwen35.attention.head_count": 16,
  "qwen35.attention.head_count_kv": 4,
  "qwen35.attention.key_length": 256,
  "qwen35.attention.value_length": 256,
  "qwen35.block_count": 32,
  "qwen35.context_length": 262144,
  "qwen35.embedding_length": 2560,
  "qwen35.full_attention_interval": 4,
};

test("KV bytes per token follow the layers that keep a cache", () => {
  // 8 of 32 layers × 4 KV heads × (256 + 256) × 2 bytes.
  expect(kvBytesPerToken(qwen35)).toBe(32_768);
  // A dense model without key_length: head size is embedding / heads.
  expect(
    kvBytesPerToken({
      "general.architecture": "llama",
      "llama.block_count": 32,
      "llama.attention.head_count": 32,
      "llama.attention.head_count_kv": 8,
      "llama.embedding_length": 4096,
    }),
  ).toBe(32 * 8 * 256 * 2);
  // A per-layer array counts only the layers with KV heads.
  expect(
    kvBytesPerToken({
      "general.architecture": "hybrid",
      "hybrid.block_count": 4,
      "hybrid.attention.head_count": 8,
      "hybrid.attention.head_count_kv": [0, 2, 0, 2],
      "hybrid.attention.key_length": 64,
    }),
  ).toBe(4 * 128 * 2);
  expect(kvBytesPerToken(undefined)).toBeUndefined();
  expect(kvBytesPerToken({ "general.architecture": "qwen35" })).toBeUndefined();
  expect(
    kvBytesPerToken({ ...qwen35, "qwen35.attention.head_count_kv": [0, 0] }),
  ).toBeUndefined();
});

test("the planned size is the largest step that fits", () => {
  const small = planContextSize({
    nativeContext: 262_144,
    weightsBytes: 3.3 * GIB,
    kvBytesPerToken: 32_768,
    totalMemory: MAC_16GB,
  });
  expect(small.context).toBe(32_768);
  expect(small.reason).toMatch(/^planned: its KV cache takes 1\.0 GiB of/);

  // A dense 9B model: 16k would need 2 GiB of the 1.9 GiB left.
  expect(
    planContextSize({
      nativeContext: 131_072,
      weightsBytes: 6.6 * GIB,
      kvBytesPerToken: 131_072,
      totalMemory: MAC_16GB,
    }).context,
  ).toBe(12_288);

  // Never above the native context.
  expect(
    planContextSize({
      nativeContext: 8_192,
      weightsBytes: GIB,
      kvBytesPerToken: 1_024,
      totalMemory: MAC_16GB,
    }).context,
  ).toBe(8_192);
  expect(
    planContextSize({
      nativeContext: 4_096,
      weightsBytes: GIB,
      kvBytesPerToken: 1_024,
      totalMemory: MAC_16GB,
    }).context,
  ).toBe(4_096);
});

test("unknown KV facts keep 16384 and no room keeps 8192", () => {
  expect(planContextSize({ weightsBytes: GIB, totalMemory: MAC_16GB })).toEqual(
    {
      context: 16_384,
      reason: "the default: the model does not report its KV cache size",
    },
  );
  expect(
    planContextSize({
      nativeContext: 2_048,
      weightsBytes: GIB,
      totalMemory: MAC_16GB,
    }).context,
  ).toBe(2_048);
  const tight = planContextSize({
    nativeContext: 131_072,
    weightsBytes: 9 * GIB,
    kvBytesPerToken: 131_072,
    totalMemory: MAC_16GB,
  });
  expect(tight.context).toBe(8_192);
  expect(tight.reason).toBe(
    "planned: memory is tight (0.0 GiB left after the weights)",
  );
});

function ollama(modelInfo: Record<string, unknown>, size = 3.3 * GIB) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = String(input);
    if (url.endsWith("/api/tags"))
      return Response.json({
        models: [{ name: "qwen3.5:4b", size: Math.round(size) }],
      });
    return Response.json({
      capabilities: ["completion", "tools"],
      model_info: modelInfo,
    });
  });
}

test("planContext plans an unset local Ollama size", async () => {
  const fetchMock = ollama(qwen35);
  try {
    const provider = await planContext(
      resolveProvider({}, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
      MAC_16GB,
    );
    expect(provider.context_size).toBe(32_768);
    expect(provider.context_plan).toMatch(/^planned: /);
    // Admission is asked only for facts, so a small native context passes.
    const body = JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body));
    expect(body.model).toBe("qwen3.5:4b");
  } finally {
    fetchMock.mockRestore();
  }
});

test("planContext keeps a chosen size, Chalupa's and openai_local's", async () => {
  const fetchMock = ollama(qwen35);
  try {
    const chosen = resolveProvider({}, "qwen3.5:4b", { context: "8k" });
    expect(await planContext(chosen, AbortSignal.timeout(5_000))).toBe(chosen);
    const fromEnv = resolveProvider({ LOCAL_AGENT_CONTEXT: "65536" });
    expect(await planContext(fromEnv, AbortSignal.timeout(5_000))).toBe(
      fromEnv,
    );
    const chalupa = resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "http://127.0.0.1:1",
        model: "large:35b",
        context_size: 65536,
        max_model_bytes: 32 * GIB,
        stream_idle_timeout: "10m",
      }),
    });
    expect(await planContext(chalupa, AbortSignal.timeout(5_000))).toBe(
      chalupa,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  } finally {
    fetchMock.mockRestore();
  }
});

test("planContext leaves the provider alone when Ollama cannot say", async () => {
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockRejectedValue(new TypeError("connection refused"));
  try {
    const provider = resolveProvider({}, "qwen3.5:4b");
    expect(await planContext(provider, AbortSignal.timeout(5_000))).toBe(
      provider,
    );
  } finally {
    fetchMock.mockRestore();
  }
});
