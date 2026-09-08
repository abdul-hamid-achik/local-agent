import { describe, expect, test } from "vitest";
import { chat, resolveProvider } from "./index";

describe("provider boundary", () => {
  test("requires an explicit model and rejects accidental remote endpoints", () => {
    expect(() => resolveProvider({})).toThrow();
    expect(resolveProvider({}, "fixture").base_url).toBe(
      "http://127.0.0.1:11434",
    );
    for (const host of [
      "https://example.com",
      "http://user:password@localhost",
      "http://localhost/path",
      "http://localhost?key=secret",
    ]) {
      expect(() => resolveProvider({ OLLAMA_HOST: host }, "fixture")).toThrow();
    }
  });
  test("accepts the Chalupa launch contract without hiding remote provenance", () => {
    const env = {
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "http://127.0.0.1:11435",
        model: "fixture",
        context_size: 65536,
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout: "10m",
      }),
    };
    expect(resolveProvider(env)).toMatchObject({
      type: "ollama_remote",
      context_size: 65536,
      model: "fixture",
    });
    expect(() =>
      resolveProvider({ ...env, LOCAL_AGENT_LOCAL_ONLY: "true" }),
    ).toThrow();
    expect(() => resolveProvider(env, "different")).toThrow();
    expect(() =>
      resolveProvider({ ...env, LOCAL_AGENT_PROVIDER_CONFIG: "{" }),
    ).toThrow();
  });
});

test("streams a real HTTP fixture and sends the negotiated context", async () => {
  let request: unknown;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      request = await req.json();
      return new Response(
        `${[
          JSON.stringify({
            message: { role: "assistant", content: "Hola 🛶" },
            done: false,
          }),
          JSON.stringify({
            message: { role: "assistant", content: "" },
            done: true,
            prompt_eval_count: 9,
            eval_count: 2,
          }),
        ].join("\n")}\n`,
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
    );
    const events = [];
    for await (const event of chat(
      provider,
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    ))
      events.push(event);
    expect(events).toEqual([
      { type: "text", text: "Hola 🛶" },
      { type: "done", inputTokens: 9, outputTokens: 2 },
    ]);
    expect(request).toMatchObject({
      model: "fixture",
      options: { num_ctx: 16384 },
      stream: true,
    });
  } finally {
    await server.stop(true);
  }
});

test("cancellation stops an HTTP request waiting for response headers", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Promise<Response>(() => {});
    },
  });
  try {
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
    );
    const stream = chat(
      provider,
      [{ role: "user", content: "Hello" }],
      AbortSignal.timeout(30),
    );
    await expect(stream.next()).rejects.toThrow();
  } finally {
    await server.stop(true);
  }
});

test("fails a provider that never sends response headers with an idle timeout", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Promise<Response>(() => {});
    },
  });
  try {
    const provider = resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: server.url.origin,
        model: "fixture",
        context_size: 16384,
        max_model_bytes: 1,
        stream_idle_timeout: "20ms",
      }),
    });
    await expect(
      chat(
        provider,
        [{ role: "user", content: "Hello" }],
        new AbortController().signal,
      ).next(),
    ).rejects.toThrow("Provider stream idle timeout after 20ms");
  } finally {
    await server.stop(true);
  }
});

test("fails when an Ollama stream goes idle between chunks", async () => {
  const encoder = new TextEncoder();
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(
              encoder.encode(
                `${JSON.stringify({ message: { role: "assistant", content: "partial" }, done: false })}\n`,
              ),
            );
            setTimeout(() => controller.close(), 100);
          },
        }),
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    const provider = resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: server.url.origin,
        model: "fixture",
        context_size: 16384,
        max_model_bytes: 1,
        stream_idle_timeout: "20ms",
      }),
    });
    const stream = chat(
      provider,
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    );
    await expect(stream.next()).resolves.toEqual({
      done: false,
      value: { type: "text", text: "partial" },
    });
    await expect(stream.next()).rejects.toThrow(
      "Provider stream idle timeout after 20ms",
    );
  } finally {
    await server.stop(true);
  }
});

test("closes the provider stream when the consumer returns early", async () => {
  const encoder = new TextEncoder();
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(
              encoder.encode(
                `${JSON.stringify({ message: { role: "assistant", content: "partial" }, done: false })}\n`,
              ),
            );
          },
        }),
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    const provider = resolveProvider(
      { OLLAMA_HOST: server.url.origin },
      "fixture",
    );
    const stream = chat(
      provider,
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    );
    await expect(stream.next()).resolves.toEqual({
      done: false,
      value: { type: "text", text: "partial" },
    });
    await expect(stream.return(undefined)).resolves.toEqual({
      done: true,
      value: undefined,
    });
  } finally {
    await server.stop(true);
  }
});

test("rejects a truncated stream without a completion event", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Response(
        `${JSON.stringify({
          message: { role: "assistant", content: "partial" },
          done: false,
        })}\n`,
        { headers: { "Content-Type": "application/x-ndjson" } },
      );
    },
  });
  try {
    const stream = chat(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Hello" }],
      AbortSignal.timeout(1000),
    );
    await expect(stream.next()).resolves.toEqual({
      done: false,
      value: { type: "text", text: "partial" },
    });
    await expect(stream.next()).rejects.toThrow(
      "Did not receive done or success response in stream.",
    );
  } finally {
    await server.stop(true);
  }
});

test("rejects redirects rather than forwarding a prompt to another host", async () => {
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return Response.redirect("http://127.0.0.1:1/api/chat", 307);
    },
  });
  try {
    const stream = chat(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "Hello" }],
      AbortSignal.timeout(1000),
    );
    await expect(stream.next()).rejects.toThrow();
  } finally {
    await server.stop(true);
  }
});
