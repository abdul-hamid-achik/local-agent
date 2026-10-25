import { expect, test, vi } from "vitest";
import { kvBytesPerToken } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";
import {
  explicitContextNote,
  MLX_WARNING,
  planContext,
  planContextAdmitted,
  planContextSize,
} from "#runtime/context";

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
  // The budget is what is left after the weights and the headroom both.
  expect(small.reason).toBe(
    "planned: its KV cache takes 1.0 GiB of the 5.2 GiB left after the weights and headroom",
  );

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

test("unknown KV facts keep 16384; tight memory keeps what ran before", () => {
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
  // An 8 GB Mac with a 4B model: nothing fits with the headroom, but the
  // old 16384 (0.5 GiB of cache) did run there, so it stays.
  const small = planContextSize({
    nativeContext: 262_144,
    weightsBytes: 3.4 * GIB,
    kvBytesPerToken: 32_768,
    totalMemory: 8 * GIB,
  });
  expect(small.context).toBe(16_384);
  expect(small.reason).toBe(
    "planned: memory is tight (0.0 GiB left after the weights and headroom)",
  );
  // A dense model that leaves half a GiB: 8192.
  expect(
    planContextSize({
      nativeContext: 131_072,
      weightsBytes: 10.5 * GIB,
      kvBytesPerToken: 131_072,
      totalMemory: MAC_16GB,
    }).context,
  ).toBe(8_192);
});

function ollama(
  modelInfo: Record<string, unknown>,
  size = 3.3 * GIB,
  details?: Record<string, unknown>,
) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = String(input);
    if (url.endsWith("/api/tags"))
      return Response.json({
        models: [{ name: "qwen3.5:4b", size: Math.round(size) }],
      });
    return Response.json({
      capabilities: ["completion", "tools"],
      model_info: modelInfo,
      ...(details ? { details } : {}),
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

test("planContext keeps a chosen size and says what it costs", async () => {
  const fetchMock = ollama(qwen35);
  try {
    const chosen = resolveProvider({}, "qwen3.5:4b", { context: "8k" });
    const kept = await planContext(
      chosen,
      AbortSignal.timeout(5_000),
      MAC_16GB,
    );
    expect(kept).toMatchObject({ context_size: 8_192, context_explicit: true });
    expect(kept.context_plan).toBe(
      "set by --context or LOCAL_AGENT_CONTEXT; its KV cache takes 0.3 GiB of the 5.2 GiB left after the weights and headroom",
    );
    expect(kept.context_warning).toBeUndefined();
    // 256k tokens at 32 KiB a token is 8 GiB: more than is left. The
    // budget is the planner's: 11/16 of 16 GiB, less 3.3 GiB of weights,
    // less 2.5 GiB of headroom.
    const big = await planContext(
      resolveProvider({ LOCAL_AGENT_CONTEXT: "262144" }, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
      MAC_16GB,
    );
    expect(big.context_size).toBe(262_144);
    expect(big.context_warning).toBe(
      "context 262144 needs a 8.0 GiB KV cache, more than the 5.2 GiB left after the weights and headroom: Ollama may run part of the model on the CPU, or fail to load it",
    );
    expect(big.context_plan).toContain(big.context_warning);
  } finally {
    fetchMock.mockRestore();
  }
  expect(
    explicitContextNote({ weightsBytes: GIB, totalMemory: MAC_16GB }, 65_536),
  ).toEqual({ reason: "set by --context or LOCAL_AGENT_CONTEXT" });
});

test("planContext keeps Chalupa's negotiated size, and warns when it is small", async () => {
  const fetchMock = ollama(qwen35);
  const chalupa = (context_size: number) =>
    resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "http://127.0.0.1:1",
        model: "large:35b",
        context_size,
        max_model_bytes: 32 * GIB,
        stream_idle_timeout: "10m",
      }),
    });
  try {
    expect(
      await planContext(chalupa(65_536), AbortSignal.timeout(5_000)),
    ).toEqual({ ...chalupa(65_536), context_plan: "set by Chalupa" });
    const small = await planContext(chalupa(8_192), AbortSignal.timeout(5_000));
    expect(small.context_size).toBe(8_192);
    expect(small.context_warning).toBe(
      "Chalupa set a context of 8192 tokens: long turns are compacted early. A larger GPU or a smaller model gives more.",
    );
    // Nothing is asked of the remote host.
    expect(fetchMock).not.toHaveBeenCalled();
  } finally {
    fetchMock.mockRestore();
  }
});

test("planContextAdmitted hands back the one admission it made, for the size it returns", async () => {
  const fetchMock = ollama(qwen35);
  try {
    const planned = await planContextAdmitted(
      resolveProvider({}, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
      MAC_16GB,
    );
    expect(planned.provider.context_size).toBe(32_768);
    expect(planned.admission?.status).toBe("validated");
    // One look: /api/tags and /api/show, nothing asked twice.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    // A size the user set is the size checked: beyond the native context,
    // the plan's admission is the refusal a client shows.
    const beyond = await planContextAdmitted(
      resolveProvider({ LOCAL_AGENT_CONTEXT: "524288" }, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
      MAC_16GB,
    );
    expect(beyond.provider.context_size).toBe(524_288);
    expect(beyond.admission?.status).toBe("rejected");
    // Chalupa's profile is not admitted here.
    expect(
      (
        await planContextAdmitted(
          resolveProvider({
            LOCAL_AGENT_PROVIDER: "chalupa",
            LOCAL_AGENT_LOCAL_ONLY: "false",
            LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
              type: "ollama_remote",
              base_url: "http://127.0.0.1:1",
              model: "large:35b",
              context_size: 32_768,
              max_model_bytes: 32 * GIB,
              stream_idle_timeout: "10m",
            }),
          }),
          AbortSignal.timeout(5_000),
        )
      ).admission,
    ).toBeUndefined();
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

test("on Ollama's MLX runner the size is local-agent's own, with a warning", async () => {
  const facts = {
    nativeContext: 262_144,
    weightsBytes: 3.3 * GIB,
    kvBytesPerToken: 32_768,
    totalMemory: MAC_16GB,
  };
  // The same size: the most local-agent sends in one request.
  const gguf = planContextSize(facts);
  const mlx = planContextSize({ ...facts, mlx: true });
  expect(mlx.context).toBe(gguf.context);
  expect(gguf.warning).toBeUndefined();
  expect(mlx).toEqual({
    context: 32_768,
    reason:
      "planned: local-agent keeps each request within it (Ollama's MLX runner ignores num_ctx and grows its cache with the conversation)",
    warning: MLX_WARNING,
  });
  // Without KV facts, or with memory tight, it still warns.
  expect(
    planContextSize({ weightsBytes: GIB, totalMemory: MAC_16GB, mlx: true })
      .warning,
  ).toBe(MLX_WARNING);
  // A size the user set: nothing is reserved, so no cost is claimed.
  expect(explicitContextNote({ ...facts, mlx: true }, 262_144)).toEqual({
    reason:
      "set by --context or LOCAL_AGENT_CONTEXT; local-agent keeps each request within it (Ollama's MLX runner ignores num_ctx and grows its cache with the conversation)",
    warning: MLX_WARNING,
  });

  // Through planContext: Ollama's details decide.
  for (const [details, warned] of [
    [{ format: "safetensors", runner: "mlx" }, true],
    // ornith-1.5:9b as this machine's Ollama reports it: no runner, GGUF.
    [{ parent_model: "", format: "gguf", family: "qwen35" }, false],
    [{ format: "gguf", runner: "llamacpp" }, false],
  ] as const) {
    const fetchMock = ollama(qwen35, 3.3 * GIB, details);
    try {
      const planned = await planContext(
        resolveProvider({}, "qwen3.5:4b"),
        AbortSignal.timeout(5_000),
        MAC_16GB,
      );
      expect(planned.context_size).toBe(32_768);
      expect(planned.context_warning).toBe(warned ? MLX_WARNING : undefined);
      const chosen = await planContext(
        resolveProvider({}, "qwen3.5:4b", { context: "8k" }),
        AbortSignal.timeout(5_000),
        MAC_16GB,
      );
      expect(chosen.context_size).toBe(8_192);
      expect(chosen.context_warning).toBe(warned ? MLX_WARNING : undefined);
    } finally {
      fetchMock.mockRestore();
    }
  }
});
