import { afterEach, expect, test, vi } from "vitest";
import type { Provider } from "#runtime/config";
import {
  type LoadedModel,
  loadedModel,
  placementWarning,
  probeProvider,
} from "#runtime/probe";

const servers: { stop(force?: boolean): Promise<void> | void }[] = [];
afterEach(async () => {
  for (const server of servers.splice(0)) await server.stop(true);
});

/** A loopback endpoint answering `routes[path]`; anything else is a 404. */
function endpoint(routes: Record<string, () => Response>) {
  const seen: string[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      const { pathname } = new URL(request.url);
      seen.push(`${request.method} ${pathname}`);
      return routes[pathname]?.() ?? new Response("nope", { status: 404 });
    },
  });
  servers.push(server);
  return { origin: server.url.origin, seen };
}

const ollama = (base: string, model = "qwen3:4b"): Provider =>
  ({
    type: "ollama",
    base_url: base,
    model,
    context_size: 16_384,
  }) as Provider;

test("an Ollama endpoint: version, latency and the model as loaded, read-only", async () => {
  const { origin, seen } = endpoint({
    "/api/version": () => Response.json({ version: "0.12.6" }),
    "/api/ps": () =>
      Response.json({
        models: [
          { name: "other:1b", size: 10, size_vram: 10 },
          {
            name: "qwen3:4b",
            model: "qwen3:4b",
            size: 4_000,
            size_vram: 3_000,
            context_length: 8_192,
          },
        ],
      }),
  });
  let now = 100;
  const probe = await probeProvider(
    ollama(origin),
    AbortSignal.timeout(5_000),
    () => (now += 12),
  );
  expect(probe).toEqual({
    latencyMs: 12,
    version: "0.12.6",
    loaded: { sizeBytes: 4_000, vramBytes: 3_000, contextLength: 8_192 },
  });
  // Only GETs: a probe never loads, pulls or chats.
  expect(seen).toEqual(["GET /api/version", "GET /api/ps"]);
});

test("a model not loaded reads null; an /api/ps that does not answer leaves it unknown", async () => {
  const empty = endpoint({
    "/api/version": () => Response.json({ version: "0.12.6" }),
    "/api/ps": () => Response.json({ models: [] }),
  });
  expect(
    (await probeProvider(ollama(empty.origin), AbortSignal.timeout(5_000)))
      .loaded,
  ).toBeNull();
  const old = endpoint({
    "/api/version": () => Response.json({ version: "0.1.0" }),
  });
  const probe = await probeProvider(
    ollama(old.origin),
    AbortSignal.timeout(5_000),
  );
  expect(probe).not.toHaveProperty("loaded");
  expect(probe.version).toBe("0.1.0");
  // `:latest` and case do not hide the model.
  const latest = endpoint({
    "/api/ps": () =>
      Response.json({
        models: [{ model: "Gemma3:latest", size: 8, size_vram: 8 }],
      }),
  });
  expect(
    await loadedModel(
      ollama(latest.origin, "gemma3"),
      AbortSignal.timeout(5_000),
    ),
  ).toEqual({ sizeBytes: 8, vramBytes: 8 });
});

test("an endpoint that does not answer says why, without a latency", async () => {
  const { origin } = endpoint({});
  await servers.splice(0)[0]?.stop(true);
  const probe = await probeProvider(ollama(origin), AbortSignal.timeout(5_000));
  expect(probe.latencyMs).toBeUndefined();
  expect(probe.error).toBeTruthy();
  const refused = endpoint({
    "/api/version": () => new Response("down", { status: 503 }),
  });
  const failed = await probeProvider(
    ollama(refused.origin),
    AbortSignal.timeout(5_000),
  );
  expect(failed.error).toContain("503");
});

test("an OpenAI-compatible endpoint is timed on its model list; a 404 still answers", async () => {
  const listed = endpoint({
    "/v1/models": () => Response.json({ data: [{ id: "m" }] }),
  });
  const provider = (base: string) =>
    ({
      type: "openai_local",
      base_url: `${base}/v1`,
      model: "m",
      context_size: 8_192,
    }) as Provider;
  const ok = await probeProvider(
    provider(listed.origin),
    AbortSignal.timeout(5_000),
  );
  expect(ok.error).toBeUndefined();
  expect(ok.latencyMs).toBeGreaterThanOrEqual(0);
  expect(ok).not.toHaveProperty("loaded");
  const bare = endpoint({});
  expect(
    (await probeProvider(provider(bare.origin), AbortSignal.timeout(5_000)))
      .error,
  ).toBeUndefined();
  expect(
    await loadedModel(provider(bare.origin), AbortSignal.timeout(5_000)),
  ).toBeUndefined();
});

test("a profile built by hand is held to the schema before anything is sent", async () => {
  const sent = vi.spyOn(globalThis, "fetch");
  const probe = await probeProvider(
    {
      type: "openai_local",
      base_url: "http://10.0.0.5:8080/v1",
      model: "m",
      context_size: 8_192,
      api_key: "sk-secret-value",
    } as Provider,
    AbortSignal.timeout(5_000),
  );
  expect(probe.error).toContain("use an https base URL or drop the api_key");
  expect(probe.error).not.toContain("sk-secret-value");
  expect(sent).not.toHaveBeenCalled();
  sent.mockRestore();
});

test("a placement warning names what is short, and nothing when the model is as asked", () => {
  const provider = { model: "qwen3:4b", context_size: 16_384 };
  expect(
    placementWarning(provider, {
      sizeBytes: 100,
      vramBytes: 100,
      contextLength: 16_384,
    }),
  ).toBeUndefined();
  expect(placementWarning(provider, null)).toBeUndefined();
  expect(placementWarning(provider, undefined)).toBeUndefined();
  // Floored: one byte short is 99%, never a reassuring 100%.
  expect(placementWarning(provider, { sizeBytes: 1_000, vramBytes: 999 })).toBe(
    "qwen3:4b: only 99% of it is in GPU memory; the rest runs on the CPU, slower",
  );
  expect(
    placementWarning(provider, {
      sizeBytes: 100,
      vramBytes: 0,
      contextLength: 4_096,
    }),
  ).toBe(
    "qwen3:4b: only 0% of it is in GPU memory; the rest runs on the CPU, slower; and it is loaded with a context of 4096 tokens, below the 16384 asked",
  );
});

const psEntry = (extra: Record<string, unknown>) =>
  endpoint({
    "/api/ps": () =>
      Response.json({
        models: [
          {
            name: "qwen3.5:4b",
            model: "qwen3.5:4b",
            size: 3_970_012_688,
            size_vram: 3_970_012_688,
            context_length: 16_384,
            ...extra,
          },
        ],
      }),
  });

test("an /api/ps entry is MLX by its runner, or by safetensors weights when none is named", async () => {
  const cases: [Record<string, unknown>, boolean][] = [
    // As /api/ps answered on this machine (Ollama 0.40.6, qwen3.5:4b).
    [{ runner: "mlx", details: { format: "safetensors" } }, true],
    [{ runner: " MLX " }, true],
    [{ runner: "llamacpp", details: { format: "safetensors" } }, false],
    [{ runner: "ollama" }, false],
    [{ details: { format: "safetensors" } }, true],
    [{ runner: "", details: { format: "SafeTensors" } }, true],
    [{ details: { format: "gguf" } }, false],
    [{ runner: null }, false],
    [{ runner: 5 }, false],
    [{}, false],
  ];
  for (const [extra, mlx] of cases) {
    const server = psEntry(extra);
    const loaded = await loadedModel(
      ollama(server.origin, "qwen3.5:4b"),
      AbortSignal.timeout(5_000),
    );
    expect(loaded, JSON.stringify(extra)).toEqual({
      sizeBytes: 3_970_012_688,
      vramBytes: 3_970_012_688,
      contextLength: 16_384,
      ...(mlx ? { mlx: true } : {}),
    });
  }
});

test("on Ollama's MLX runner the loaded context is a soft limit: no shortfall warning", () => {
  const asked = { model: "qwen3.5:4b", context_size: 32_768 };
  const loaded: LoadedModel = {
    sizeBytes: 3_970_012_688,
    vramBytes: 3_970_012_688,
    contextLength: 16_384,
    mlx: true,
  };
  expect(placementWarning(asked, loaded)).toBeUndefined();
  // Its GPU share still counts.
  expect(placementWarning(asked, { ...loaded, vramBytes: 0 })).toBe(
    "qwen3.5:4b: only 0% of it is in GPU memory; the rest runs on the CPU, slower",
  );
  // The same entry on another runner keeps the check.
  const { mlx: _, ...llama } = loaded;
  expect(placementWarning(asked, llama)).toContain("below the 32768 asked");
});
