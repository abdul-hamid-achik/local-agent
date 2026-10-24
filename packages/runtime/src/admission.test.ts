import { expect, test, vi } from "vitest";
import { admitOllamaModel, ollamaMLX } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";

const remote = (base_url: string) =>
  resolveProvider({
    LOCAL_AGENT_PROVIDER: "chalupa",
    LOCAL_AGENT_LOCAL_ONLY: "false",
    LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
      type: "ollama_remote",
      base_url,
      model: "large:35b",
      context_size: 65536,
      max_model_bytes: 32 * 1024 * 1024 * 1024,
      stream_idle_timeout: "10m",
    }),
  });

test("admits only a model proved by exact Ollama metadata", async () => {
  let mode = "valid";
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const url = String(input);
      expect(url).toMatch(/^http:\/\/127\.0\.0\.1\/api\/(tags|show)$/);
      if (url.endsWith("/api/tags")) {
        expect(init?.method).toBe("GET");
        if (mode === "malformed") return Response.json({ models: null });
        if (mode === "huge")
          return new Response(
            JSON.stringify({ models: [], padding: "x".repeat(1 << 20) }),
          );
        return Response.json({
          models: [
            {
              name: "large:35b",
              size:
                mode === "oversized"
                  ? 40 * 1024 * 1024 * 1024
                  : 24 * 1024 * 1024 * 1024,
              ...(mode === "redirected"
                ? { remote_host: "https://other.example" }
                : {}),
            },
          ],
        });
      }
      expect(url).toMatch(/\/api\/show$/);
      expect(init?.method).toBe("POST");
      expect(JSON.parse(String(init?.body))).toMatchObject({
        model: "large:35b",
      });
      return Response.json({
        capabilities: ["completion", "tools"],
        model_info:
          mode === "missing-context" ? {} : { "qwen.context_length": 131072 },
        ...(mode === "redirected"
          ? { remote_host: "https://other.example" }
          : {}),
      });
    });
  try {
    const signal = new AbortController().signal;
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({
      status: "validated",
      sizeBytes: 24 * 1024 * 1024 * 1024,
      nativeContext: 131072,
    });
    mode = "oversized";
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "rejected", kind: "policy" });
    mode = "redirected";
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "rejected", kind: "policy" });
    mode = "missing-context";
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "rejected", kind: "metadata" });
    mode = "malformed";
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "rejected", kind: "metadata" });
    mode = "huge";
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "rejected", kind: "metadata" });

    fetchMock.mockRejectedValueOnce(new TypeError("connection refused"));
    await expect(
      admitOllamaModel(remote("http://127.0.0.1"), signal),
    ).resolves.toMatchObject({ status: "unverified", kind: "transport" });
  } finally {
    fetchMock.mockRestore();
  }
});

test("missing model explains a provider prefix without requesting show or switching models", async () => {
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(
      Response.json({ models: [{ name: "qwen3.5:0.8b", size: 1000 }] }),
    );
  try {
    const result = await admitOllamaModel(
      resolveProvider({}, "ollama/qwen3.5:0.8b"),
      new AbortController().signal,
    );
    expect(result).toMatchObject({ status: "rejected", kind: "metadata" });
    expect("message" in result && result.message).toContain(
      "Use --model qwen3.5:0.8b without the ollama/ prefix",
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toMatch(/\/api\/tags$/);
  } finally {
    fetchMock.mockRestore();
  }
});

test("a missing local model gets one actionable line; a remote one does not suggest a pull", async () => {
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(
      Response.json({ models: [{ name: "other:1b", size: 1000 }] }),
    );
  try {
    const signal = new AbortController().signal;
    const local = await admitOllamaModel(resolveProvider({}), signal);
    expect(local).toMatchObject({
      status: "rejected",
      message:
        "Model qwen3.5:4b-q4_K_M is not installed. Run: ollama pull qwen3.5:4b-q4_K_M",
    });
    const far = await admitOllamaModel(remote("http://127.0.0.1"), signal);
    expect("message" in far && far.message).not.toContain("ollama pull");
  } finally {
    fetchMock.mockRestore();
  }
});

test("a --context beyond the native context is rejected with the largest usable value", async () => {
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input) =>
      String(input).endsWith("/api/tags")
        ? Response.json({ models: [{ name: "small:1b", size: 1000 }] })
        : Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "small.context_length": 8192 },
          }),
    );
  try {
    const result = await admitOllamaModel(
      resolveProvider({}, "small:1b", { context: "32k" }),
      new AbortController().signal,
    );
    expect(result).toMatchObject({ status: "rejected", kind: "policy" });
    expect("message" in result && result.message).toContain("--context 8192");
  } finally {
    fetchMock.mockRestore();
  }
});

test("a model missing from a Chalupa host points at chalupa status, not ollama pull", async () => {
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async () => Response.json({ models: [] }));
  try {
    const admission = await admitOllamaModel(
      remote("http://127.0.0.1"),
      AbortSignal.timeout(5_000),
    );
    expect(admission).toMatchObject({ status: "rejected", kind: "metadata" });
    expect(admission.status === "rejected" && admission.message).toBe(
      "Ollama model large:35b is not on the Chalupa host yet. Run chalupa status to see the download, or set inference.pull: true in chalupa.yml.",
    );
  } finally {
    fetchMock.mockRestore();
  }
});

test("a declared runner decides MLX; an older manifest's safetensors weights mean MLX too", async () => {
  // The first two as Ollama 0.40.6 reported them on this machine (a GGUF
  // tag declares llamacpp; ornith-1.5 predates the field and is GGUF); the
  // mlx shape is the one its source writes for a safetensors model.
  expect(ollamaMLX({ format: "gguf", runner: "llamacpp" })).toBe(false);
  expect(ollamaMLX({ format: "gguf", parent_model: "" })).toBe(false);
  expect(ollamaMLX({ format: "safetensors", runner: "mlx" })).toBe(true);
  expect(ollamaMLX({ runner: " MLX " })).toBe(true);
  // The declared runner wins over the format, both ways.
  expect(ollamaMLX({ format: "safetensors", runner: "ggml" })).toBe(false);
  expect(ollamaMLX({ format: "gguf", runner: "mlx" })).toBe(true);
  // No runner (or an empty one): safetensors weights run on MLX.
  expect(ollamaMLX({ format: "safetensors" })).toBe(true);
  expect(ollamaMLX({ format: "safetensors", runner: "" })).toBe(true);
  expect(ollamaMLX({ format: "safetensors", runner: "  " })).toBe(true);
  expect(ollamaMLX({})).toBe(false);
  expect(ollamaMLX(undefined)).toBe(false);
  expect(ollamaMLX({ runner: 1, format: null })).toBe(false);

  // The admission carries it only when it holds.
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      if (String(input).endsWith("/api/tags"))
        return Response.json({ models: [{ name: "qwen3.5:4b", size: 3_000 }] });
      const model = JSON.parse(String(init?.body)).model;
      return Response.json({
        capabilities: ["completion", "tools"],
        details:
          model === "qwen3.5:4b"
            ? { format: "safetensors", runner: "mlx" }
            : { format: "gguf", runner: "llamacpp" },
      });
    });
  try {
    const mlx = await admitOllamaModel(
      resolveProvider({}, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
    );
    expect(mlx).toMatchObject({ status: "validated", mlx: true });
    fetchMock.mockImplementation(async (input) =>
      String(input).endsWith("/api/tags")
        ? Response.json({ models: [{ name: "qwen3.5:4b", size: 3_000 }] })
        : Response.json({
            capabilities: ["completion", "tools"],
            details: { format: "gguf", runner: "llamacpp" },
          }),
    );
    const gguf = await admitOllamaModel(
      resolveProvider({}, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
    );
    expect(gguf.status).toBe("validated");
    expect(gguf).not.toHaveProperty("mlx");
  } finally {
    fetchMock.mockRestore();
  }
});
