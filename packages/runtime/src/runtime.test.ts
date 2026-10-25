import { describe, expect, test } from "vitest";
import { chat, resolveProvider } from "./index";

describe("provider boundary", () => {
  test("rejects accidental remote endpoints", () => {
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
      {
        type: "done",
        inputTokens: 9,
        outputTokens: 2,
        promptEvalCount: 9,
        evalCount: 2,
      },
    ]);
    expect(request).toMatchObject({
      model: "fixture",
      options: { num_ctx: 16384 },
      stream: true,
    });
    // An unset think is not sent: the model keeps its own default.
    expect(request).not.toHaveProperty("think");
    // Nor a keep_alive: only a Chalupa profile carries one.
    expect(request).not.toHaveProperty("keep_alive");
  } finally {
    await server.stop(true);
  }
});

test("sends a Chalupa profile's keep_alive with the chat", async () => {
  let request: unknown;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      request = await req.json();
      return new Response(
        `${JSON.stringify({
          message: { role: "assistant", content: "" },
          done: true,
          prompt_eval_count: 1,
          eval_count: 1,
        })}\n`,
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
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout: "10m",
        keep_alive: "30m",
      }),
    });
    for await (const _ of chat(
      provider,
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    ));
    expect(request).toMatchObject({ model: "fixture", keep_alive: "30m" });
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

// The network cuts a stream where it likes: a line in two reads (here
// inside an emoji's bytes and inside a tool call), or two lines in one.
test("an Ollama stream cut anywhere reads as the same events", async () => {
  const lines = [
    { message: { role: "assistant", content: "Hola 🛶" }, done: false },
    { message: { role: "assistant", content: " otra vez" }, done: false },
    {
      message: {
        role: "assistant",
        content: "",
        tool_calls: [
          { function: { name: "read", arguments: { path: "README.md" } } },
        ],
      },
      done: false,
    },
    {
      message: { role: "assistant", content: "" },
      done: true,
      prompt_eval_count: 9,
      eval_count: 4,
    },
  ];
  const bytes = new TextEncoder().encode(
    `${lines.map((line) => JSON.stringify(line)).join("\n")}\n`,
  );
  const first = bytes.indexOf(0xf0) + 2; // two of the emoji's four bytes
  const call = new TextDecoder().decode(bytes).indexOf("README") + 3;
  const second = new TextEncoder().encode(
    new TextDecoder().decode(bytes).slice(0, call),
  ).length;
  const cuts = [0, 5, first, second - 40, second, bytes.length];
  expect(cuts.every((cut, at) => at === 0 || cut > (cuts[at - 1] ?? 0))).toBe(
    true,
  );
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return new Response(
        new ReadableStream<Uint8Array>({
          async start(controller) {
            for (let at = 1; at < cuts.length; at++) {
              controller.enqueue(bytes.slice(cuts[at - 1], cuts[at]));
              await Bun.sleep(1);
            }
            controller.close();
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
    const events = [];
    for await (const event of chat(
      provider,
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    ))
      events.push(event);
    expect(events).toEqual([
      { type: "text", text: "Hola 🛶" },
      { type: "text", text: " otra vez" },
      {
        type: "tool_call",
        call: { function: { name: "read", arguments: { path: "README.md" } } },
      },
      {
        type: "done",
        inputTokens: 9,
        outputTokens: 4,
        promptEvalCount: 9,
        evalCount: 4,
      },
    ]);
  } finally {
    await server.stop(true);
  }
});

test("a stream line that is not JSON fails the reply instead of being skipped", async () => {
  for (const [body, start] of [
    // A proxy's page in the middle of the stream, escape sequence and all.
    [
      `{"message":{"role":"assistant","content":"a"},"done":false}\n<html>\u001b[31m 502\n{"message":{"role":"assistant","content":"b"},"done":true}\n`,
      "<html> 502",
    ],
    // The last line, with no newline after it.
    [
      `{"message":{"role":"assistant","content":"a"},"done":false}\n{"done":tr`,
      '{"done":tr',
    ],
    // Cut at 60 code points: the 60th, an emoji, stays whole.
    [`${"x".repeat(59)}🛶 and more\n`, `${"x".repeat(59)}🛶`],
  ] as const) {
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      fetch: () =>
        new Response(body, {
          headers: { "Content-Type": "application/x-ndjson" },
        }),
    });
    try {
      const provider = resolveProvider(
        { OLLAMA_HOST: server.url.origin },
        "fixture",
      );
      const events: unknown[] = [];
      await expect(
        (async () => {
          for await (const event of chat(
            provider,
            [{ role: "user", content: "Hello" }],
            new AbortController().signal,
          ))
            events.push(event);
        })(),
      ).rejects.toThrow(
        new RegExp(
          `Ollama sent a line that is not JSON: ${RegExp.escape(start)}$`,
          "u",
        ),
      );
      expect(events).not.toContainEqual({ type: "text", text: "b" });
    } finally {
      await server.stop(true);
    }
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
            // Real timer: this test exercises the provider's real idle-timeout
            // against the platform clock; deterministic time control won't.
            setTimeout(() => {
              try {
                controller.close();
              } catch {
                // The consumer already cancelled the stream (idle timeout).
              }
            }, 100);
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
    // The error comes from the ollama client (or, failing that, from chat
    // itself): either way a stream without `done` fails and never reads as a
    // finished reply. The wording is not ours, so only its subject is checked.
    await expect(stream.next()).rejects.toThrow(/done|before completion/u);
    await expect(stream.next()).resolves.toEqual({
      done: true,
      value: undefined,
    });
  } finally {
    await server.stop(true);
  }
});

test("a lost Chalupa tunnel is named as one; a local Ollama's error is not", async () => {
  const remote = (base_url: string) =>
    resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url,
        model: "fixture",
        context_size: 8192,
        max_model_bytes: 1_000_000_000,
        stream_idle_timeout: "10m",
      }),
    });
  const drain = async (provider: ReturnType<typeof resolveProvider>) => {
    for await (const _ of chat(
      provider,
      [{ role: "user", content: "Hello" }],
      AbortSignal.timeout(5000),
    ));
  };
  // A port nothing listens on: the tunnel's forward is gone.
  const closed = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: () => new Response(),
  });
  const gone = closed.url.origin;
  await closed.stop(true);
  await expect(drain(remote(gone))).rejects.toThrow(
    `The Chalupa tunnel to ${gone} was lost (Unable to connect. Is the computer able to access the url?). The GPU may still be running: run chalupa status.`,
  );
  // Cut mid-reply.
  const cut = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch: () =>
      new Response(
        `${JSON.stringify({ message: { role: "assistant", content: "a" }, done: false })}\n`,
      ),
  });
  try {
    await expect(drain(remote(cut.url.origin))).rejects.toThrow(
      /^The Chalupa tunnel to .* was lost \(Did not receive done/,
    );
    await expect(
      drain(resolveProvider({ OLLAMA_HOST: cut.url.origin }, "fixture")),
    ).rejects.toThrow(/^Did not receive done/);
  } finally {
    await cut.stop(true);
  }
});

test("rejects redirects rather than forwarding a prompt to another host", async () => {
  // Where the redirect points: a server that counts what reaches it.
  const hits: string[] = [];
  const elsewhere = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      hits.push(await request.text());
      return new Response("{}");
    },
  });
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch() {
      return Response.redirect(`${elsewhere.url.origin}/api/chat`, 307);
    },
  });
  try {
    const stream = chat(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "a prompt that stays here" }],
      AbortSignal.timeout(1000),
    );
    await expect(stream.next()).rejects.toThrow(/redirect/iu);
    // Neither the request nor its body reached the other host.
    expect(hits).toEqual([]);
  } finally {
    await server.stop(true);
    await elsewhere.stop(true);
  }
});

describe("thinking and timings over a loopback fixture", () => {
  const serve = (requests: unknown[]) =>
    Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(req) {
        requests.push(await req.json());
        return new Response(
          `${[
            {
              message: { role: "assistant", thinking: "Let me " },
              done: false,
            },
            { message: { role: "assistant", thinking: "think." }, done: false },
            { message: { role: "assistant", content: "Answer" }, done: false },
            {
              message: { role: "assistant", content: "" },
              done: true,
              total_duration: 3_000_000_000,
              prompt_eval_count: 40,
              prompt_eval_duration: 500_000_000,
              eval_count: 30,
              eval_duration: 1_000_000_000,
            },
          ]
            .map((line) => JSON.stringify(line))
            .join("\n")}\n`,
          { headers: { "Content-Type": "application/x-ndjson" } },
        );
      },
    });

  test.each([
    ["on", true],
    ["off", false],
  ])("--think %s is sent as think: %s", async (flag, expected) => {
    const requests: unknown[] = [];
    const server = serve(requests);
    try {
      const provider = resolveProvider(
        { OLLAMA_HOST: server.url.origin },
        "fixture",
        { think: flag },
      );
      for await (const _ of chat(
        provider,
        [{ role: "user", content: "Hi" }],
        new AbortController().signal,
      ));
      expect(requests[0]).toMatchObject({ think: expected });
    } finally {
      await server.stop(true);
    }
  });

  test("thinking streams as its own event, never as text, and done carries Ollama's timings", async () => {
    const server = serve([]);
    try {
      const events = [];
      for await (const event of chat(
        resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
        [{ role: "user", content: "Hi" }],
        new AbortController().signal,
      ))
        events.push(event);
      expect(events).toEqual([
        { type: "thinking", text: "Let me " },
        { type: "thinking", text: "think." },
        { type: "text", text: "Answer" },
        {
          type: "done",
          inputTokens: 40,
          outputTokens: 30,
          promptEvalCount: 40,
          promptEvalDuration: 500_000_000,
          evalCount: 30,
          evalDuration: 1_000_000_000,
          totalDuration: 3_000_000_000,
        },
      ]);
    } finally {
      await server.stop(true);
    }
  });
});
