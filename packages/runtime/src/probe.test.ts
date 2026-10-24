import { afterEach, expect, test } from "vitest";
import type { Provider } from "#runtime/config";
import { loadedModel, placementWarning, probeProvider } from "#runtime/probe";

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
