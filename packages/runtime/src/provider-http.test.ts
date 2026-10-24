import type { Tool } from "ollama";
import { describe, expect, test, vi } from "vitest";
import { admitOpenAILocalModel } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";
import {
  chat,
  parseOpenAISSE,
  providerRetryDelayMs,
  type RunEvent,
} from "#runtime/index";

const localProvider = (baseURL: string) =>
  resolveProvider({
    LOCAL_AGENT_PROVIDER: "openai_local",
    LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
      type: "openai_local",
      baseURL,
      model: "fixture-model",
    }),
  });

const serve = (respond: (req: Request) => Response | Promise<Response>) =>
  Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: respond });

const messageOf = (result: object) =>
  "message" in result ? String(result.message) : "";

test("a /models body that is not a model list is unverified, not unreachable or rejected", async () => {
  const bodies = [
    ['{"models":[{"id":"fixture-model"}]}', /no `data` array/],
    ["<html>not json</html>", /not valid JSON/],
  ] as const;
  for (const [body, message] of bodies) {
    const server = serve(() => new Response(body));
    try {
      const result = await admitOpenAILocalModel(
        localProvider(`${server.url.origin}/v1`),
        new AbortController().signal,
      );
      expect(result.status).toBe("unverified");
      expect(messageOf(result)).toMatch(message);
      expect(messageOf(result)).not.toMatch(/unreachable/);
    } finally {
      server.stop(true);
    }
  }
});

test("a /models body past the metadata cap is refused, not buffered", async () => {
  const server = serve(
    () => new Response(`{"data":[{"id":"${"x".repeat(2 << 20)}"}]}`),
  );
  try {
    const result = await admitOpenAILocalModel(
      localProvider(`${server.url.origin}/v1`),
      new AbortController().signal,
    );
    expect(result.status).toBe("unverified");
    expect(messageOf(result)).toMatch(/exceeds size limit/);
  } finally {
    server.stop(true);
  }
});

const encoder = new TextEncoder();
const stream = (chunks: string[]) =>
  new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
const parts = async (chunks: string[]) => {
  const seen: unknown[] = [];
  for await (const part of parseOpenAISSE(stream(chunks))) seen.push(part);
  return seen;
};
const sse = (...lines: string[]) =>
  lines.map((line) => `data: ${line}\n\n`).join("");
const delta = (value: object, finish?: string) =>
  JSON.stringify({
    choices: [{ delta: value, ...(finish ? { finish_reason: finish } : {}) }],
  });
const sseResponse = (body: string | ReadableStream<Uint8Array>) =>
  new Response(body, { headers: { "Content-Type": "text/event-stream" } });
const events = async (
  provider: ReturnType<typeof localProvider>,
  signal: AbortSignal = AbortSignal.timeout(4_000),
  tools?: Tool[],
) => {
  const seen: RunEvent[] = [];
  for await (const event of chat(
    provider,
    [{ role: "user", content: "hi" }],
    signal,
    tools,
  ))
    seen.push(event);
  return seen;
};

describe("SSE framing", () => {
  /**
   * Parts as they arrive: the stream stays open until the first part is out,
   * so an event that is not recognised until the close fails the test.
   */
  const incremental = async (first: string[], rest: string) => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const chunk of first) controller.enqueue(encoder.encode(chunk));
      },
      async pull(controller) {
        await gate;
        controller.enqueue(encoder.encode(rest));
        controller.close();
      },
    });
    const seen: unknown[] = [];
    for await (const part of parseOpenAISSE(body)) {
      seen.push(part);
      release();
    }
    return seen;
  };

  test("accepts CRLF line endings as the events arrive", async () => {
    const event = delta({ content: "hi" });
    expect(
      await incremental([`data: ${event}\r\n\r\n`], "data: [DONE]\r\n\r\n"),
    ).toEqual([JSON.parse(event)]);
  });

  test("a CRLF split between chunks is one line ending, not two", async () => {
    // Read as two line endings, the blank line would cut the event in half.
    expect(
      await parts([
        'data: {"choices":[{"delta":\r',
        '\ndata: {"content":"split"}}]}\r\n\r',
        "\ndata: [DONE]\r\n\r\n",
      ]),
    ).toEqual([{ choices: [{ delta: { content: "split" } }] }]);
  });

  test("accepts bare CR line endings", async () => {
    const event = delta({ content: "hi" });
    expect(await parts([`data: ${event}\r\rdata: [DONE]\r\r`])).toEqual([
      JSON.parse(event),
    ]);
  });

  test("an event ending in a bare CR CR is not held back while the stream stays open", async () => {
    const event = delta({ content: "hi" });
    expect(
      await incremental([`data: ${event}\r\r`], "data: [DONE]\r\r"),
    ).toEqual([JSON.parse(event)]);
  });

  test("an LF opening the next chunk after a chunk-final CR is dropped, not read as a blank line", async () => {
    // Read as a blank line, it would end the event between its two data lines.
    expect(
      await incremental(
        ['data: {"choices":[{"delta":\r', '\ndata: {"content":"x"}}]}\r\n\r\n'],
        "data: [DONE]\n\n",
      ),
    ).toEqual([{ choices: [{ delta: { content: "x" } }] }]);
  });

  test("a CRLF server streams end to end through chat", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const server = serve(() =>
      sseResponse(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(
              encoder.encode(`data: ${delta({ content: "crlf" })}\r\n\r\n`),
            );
          },
          async pull(controller) {
            await gate;
            controller.enqueue(
              encoder.encode(
                `data: ${delta({}, "stop")}\r\n\r\ndata: [DONE]\r\n\r\n`,
              ),
            );
            controller.close();
          },
        }),
      ),
    );
    try {
      const seen: RunEvent[] = [];
      for await (const event of chat(
        localProvider(`${server.url.origin}/v1`),
        [{ role: "user", content: "hi" }],
        AbortSignal.timeout(2_000),
      )) {
        seen.push(event);
        release();
      }
      expect(seen).toMatchObject([
        { type: "text", text: "crlf" },
        { type: "done", finishReason: "stop" },
      ]);
    } finally {
      release();
      server.stop(true);
    }
  });

  test("data lines of one event are joined with a newline", async () => {
    expect(
      await parts([
        'data: {"choices":[{"delta":\ndata: {"content":"joined"}}]}\n\ndata: [DONE]\n\n',
      ]),
    ).toEqual([{ choices: [{ delta: { content: "joined" } }] }]);
  });

  test("a pending event past the cap fails with a clear error instead of growing", async () => {
    const big = `data: ${"a".repeat(1 << 20)}`;
    await expect(parts(Array.from({ length: 9 }, () => big))).rejects.toThrow(
      /exceeds 8 Mi UTF-16 code units/,
    );
  });
});

describe("stream ends", () => {
  test("a clean close after a finish_reason, without [DONE], is a complete reply", async () => {
    const server = serve(() =>
      sseResponse(
        sse(
          delta({ content: "whole" }),
          delta({}, "stop"),
          JSON.stringify({
            choices: [],
            usage: { prompt_tokens: 7, completion_tokens: 2 },
          }),
        ),
      ),
    );
    try {
      expect(await events(localProvider(`${server.url.origin}/v1`))).toEqual([
        { type: "text", text: "whole" },
        {
          type: "done",
          inputTokens: 7,
          outputTokens: 2,
          finishReason: "stop",
        },
      ]);
    } finally {
      server.stop(true);
    }
  });

  test("a clean close with no finish_reason and no [DONE] still fails, naming the cause", async () => {
    const server = serve(() => sseResponse(sse(delta({ content: "cut" }))));
    try {
      await expect(
        events(localProvider(`${server.url.origin}/v1`)),
      ).rejects.toThrow(/before \[DONE\] and without a finish_reason/);
    } finally {
      server.stop(true);
    }
  });
});

describe("tool_call fragments", () => {
  type Fragment = {
    index?: number;
    id?: string;
    function?: { name?: string; arguments?: string };
  };
  const declare = (...names: string[]) =>
    names.map((name) => ({
      type: "function",
      function: { name, description: "", parameters: {} },
    })) as Tool[];
  const run = async (fragments: Fragment[], tools: Tool[] = []) => {
    const server = serve(() =>
      sseResponse(
        sse(
          ...fragments.map((fragment) => delta({ tool_calls: [fragment] })),
          "[DONE]",
        ),
      ),
    );
    try {
      return (
        await events(
          localProvider(`${server.url.origin}/v1`),
          AbortSignal.timeout(4_000),
          tools,
        )
      ).filter((event) => event.type === "tool_call");
    } finally {
      server.stop(true);
    }
  };

  test("a later fragment of the same index neither clobbers the id nor doubles the name", async () => {
    expect(
      await run(
        [
          {
            index: 0,
            id: "call-1",
            function: { name: "read", arguments: "{" },
          },
          // Servers that repeat the name and send an empty or new id per chunk.
          { index: 0, id: "", function: { name: "read", arguments: '"path"' } },
          {
            index: 0,
            id: "other",
            function: { name: "read", arguments: ':"a"}' },
          },
        ],
        declare("read"),
      ),
    ).toEqual([
      {
        type: "tool_call",
        call: {
          id: "call-1",
          function: { name: "read", arguments: { path: "a" } },
        },
      },
    ]);
  });

  test("a name streamed in pieces is still joined", async () => {
    expect(
      await run([
        { index: 0, id: "c", function: { name: "re", arguments: "{" } },
        { index: 0, function: { name: "ad", arguments: "}" } },
      ]),
    ).toMatchObject([{ call: { id: "c", function: { name: "read" } } }]);
  });

  test("a repeated name is joined when only the joined name is a declared tool", async () => {
    expect(
      await run(
        [
          { index: 0, id: "c", function: { name: "read", arguments: "{" } },
          { index: 0, function: { name: "read", arguments: "}" } },
        ],
        declare("readread"),
      ),
    ).toMatchObject([{ call: { function: { name: "readread" } } }]);
  });

  test("calls without index or id are told apart by a different name", async () => {
    expect(
      await run(
        [
          { function: { name: "read", arguments: '{"path":"x"}' } },
          { function: { name: "list", arguments: '{"path":"y"}' } },
        ],
        declare("read", "list"),
      ),
    ).toMatchObject([
      { call: { function: { name: "read", arguments: { path: "x" } } } },
      { call: { function: { name: "list", arguments: { path: "y" } } } },
    ]);
  });

  test("without index or id, a name split before any arguments is one call", async () => {
    expect(
      await run(
        [
          { function: { name: "re" } },
          { function: { name: "ad", arguments: '{"path":"x"}' } },
        ],
        declare("read", "list"),
      ),
    ).toMatchObject([
      { call: { function: { name: "read", arguments: { path: "x" } } } },
    ]);
  });

  test("fragments of two calls interleaved by index stay apart", async () => {
    expect(
      await run([
        { index: 0, id: "a", function: { name: "read", arguments: '{"p":' } },
        { index: 1, id: "b", function: { name: "list", arguments: '{"q":' } },
        { index: 0, function: { arguments: "1}" } },
        { index: 1, function: { arguments: "2}" } },
      ]),
    ).toMatchObject([
      { call: { id: "a", function: { name: "read", arguments: { p: 1 } } } },
      { call: { id: "b", function: { name: "list", arguments: { q: 2 } } } },
    ]);
  });

  test("an id that arrives after the first fragment is kept", async () => {
    expect(
      await run([
        { index: 0, function: { name: "read", arguments: "{}" } },
        { index: 0, id: "late" },
      ]),
    ).toMatchObject([{ call: { id: "late", function: { name: "read" } } }]);
  });

  test("parallel calls without an index are told apart by their id", async () => {
    expect(
      await run([
        { id: "a", function: { name: "read", arguments: '{"path":' } },
        { function: { arguments: '"x"}' } },
        { id: "b", function: { name: "list", arguments: '{"path":"y"}' } },
      ]),
    ).toMatchObject([
      {
        call: { id: "a", function: { name: "read", arguments: { path: "x" } } },
      },
      {
        call: { id: "b", function: { name: "list", arguments: { path: "y" } } },
      },
    ]);
  });
});

describe("finish reasons", () => {
  test("openai finish_reason length is surfaced on done", async () => {
    const server = serve(() =>
      sseResponse(sse(delta({ content: "cut off" }, "length"), "[DONE]")),
    );
    try {
      expect(await events(localProvider(`${server.url.origin}/v1`))).toEqual([
        { type: "text", text: "cut off" },
        {
          type: "done",
          inputTokens: 0,
          outputTokens: 0,
          finishReason: "length",
        },
      ]);
    } finally {
      server.stop(true);
    }
  });

  test.each(["stop", "length"])(
    "ollama done_reason %s is surfaced on done",
    async (reason) => {
      const server = serve(
        () =>
          new Response(
            `${JSON.stringify({
              message: { role: "assistant", content: "x" },
              done: true,
              done_reason: reason,
            })}\n`,
          ),
      );
      try {
        const seen = await events(
          resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
        );
        expect(seen.at(-1)).toMatchObject({
          type: "done",
          finishReason: reason,
        });
      } finally {
        server.stop(true);
      }
    },
  );
});

describe("provider request retries", () => {
  const ollamaDone = `${JSON.stringify({
    message: { role: "assistant", content: "ok" },
    done: true,
  })}\n`;
  const providers = {
    openai: (origin: string) => localProvider(`${origin}/v1`),
    ollama: (origin: string) =>
      resolveProvider({ OLLAMA_HOST: origin }, "fixture"),
  };
  const ok = (kind: "openai" | "ollama") =>
    kind === "openai"
      ? sseResponse(sse(delta({ content: "ok" }, "stop"), "[DONE]"))
      : new Response(ollamaDone);

  test.each([
    ["openai", 429],
    ["openai", 502],
    ["openai", 503],
    ["openai", 504],
    ["ollama", 503],
    ["ollama", 429],
  ] as const)(
    "%s: a %i before any reply is retried and succeeds",
    async (kind, status) => {
      let requests = 0;
      const server = serve(() =>
        ++requests === 1
          ? new Response("busy", { status, headers: { "retry-after": "0" } })
          : ok(kind),
      );
      try {
        const seen = await events(providers[kind](server.url.origin));
        expect(seen.some((event) => event.type === "text")).toBe(true);
        expect(requests).toBe(2);
      } finally {
        server.stop(true);
      }
    },
  );

  test("gives up after two retries and reports the last status", async () => {
    let requests = 0;
    const server = serve(() => {
      requests++;
      return new Response("busy", {
        status: 503,
        headers: { "retry-after": "0" },
      });
    });
    try {
      await expect(events(providers.openai(server.url.origin))).rejects.toThrow(
        /\(503\)/,
      );
      expect(requests).toBe(3);
    } finally {
      server.stop(true);
    }
  });

  test.each([400, 401, 404, 500])("a %i is final: no retry", async (status) => {
    let requests = 0;
    const server = serve(() => {
      requests++;
      return new Response("no", { status });
    });
    try {
      await expect(
        events(providers.openai(server.url.origin)),
      ).rejects.toThrow();
      expect(requests).toBe(1);
    } finally {
      server.stop(true);
    }
  });

  test("a stream cut after its first byte is never retried", async () => {
    let requests = 0;
    const server = serve(() => {
      requests++;
      return sseResponse(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(encoder.encode(sse(delta({ content: "a" }))));
            setTimeout(() => controller.close(), 30);
          },
        }),
      );
    });
    try {
      await expect(events(providers.openai(server.url.origin))).rejects.toThrow(
        /before \[DONE\]/,
      );
      expect(requests).toBe(1);
    } finally {
      server.stop(true);
    }
  });

  test("a wait for Retry-After ends the moment the request is aborted", async () => {
    let requests = 0;
    const controller = new AbortController();
    const server = serve(() => {
      requests++;
      // Abort once the server has the request, not on a clock of the test's own.
      setTimeout(() => controller.abort(new Error("stop")), 50);
      return new Response("busy", {
        status: 503,
        headers: { "retry-after": "10" },
      });
    });
    try {
      const started = Date.now();
      await expect(
        events(providers.openai(server.url.origin), controller.signal),
      ).rejects.toThrow("stop");
      expect(Date.now() - started).toBeLessThan(2_000);
      expect(requests).toBe(1);
    } finally {
      server.stop(true);
    }
  });

  test("a refused connection is retried, then succeeds", async () => {
    const server = serve(() => ok("openai"));
    const real = globalThis.fetch;
    let attempts = 0;
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(((
      input: RequestInfo | URL,
      init?: RequestInit,
    ) => {
      if (++attempts === 1)
        return Promise.reject(
          Object.assign(new Error("Unable to connect"), {
            code: "ConnectionRefused",
          }),
        );
      return real(input, init);
    }) as typeof fetch);
    try {
      const seen = await events(providers.openai(server.url.origin));
      expect(seen.some((event) => event.type === "text")).toBe(true);
      expect(attempts).toBe(2);
    } finally {
      spy.mockRestore();
      server.stop(true);
    }
  });

  test("a cancelled request is not retried, whatever its error looks like", async () => {
    const controller = new AbortController();
    let attempts = 0;
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation((() => {
      attempts++;
      controller.abort(new Error("stop"));
      return Promise.reject(
        Object.assign(new Error("Unable to connect"), {
          code: "ConnectionRefused",
        }),
      );
    }) as unknown as typeof fetch);
    try {
      await expect(
        events(providers.openai("http://127.0.0.1:9"), controller.signal),
      ).rejects.toThrow();
      expect(attempts).toBe(1);
    } finally {
      spy.mockRestore();
    }
  });

  test("a Retry-After longer than the cap is not retried early: the answer is returned at once", async () => {
    let requests = 0;
    const server = serve(() => {
      requests++;
      return new Response("busy", {
        status: 503,
        headers: { "retry-after": "60" },
      });
    });
    try {
      const started = Date.now();
      await expect(events(providers.openai(server.url.origin))).rejects.toThrow(
        /\(503\)/,
      );
      expect(Date.now() - started).toBeLessThan(1_000);
      expect(requests).toBe(1);
    } finally {
      server.stop(true);
    }
  });

  test("Retry-After is honoured up to the cap; a missing one backs off", () => {
    const after = (value?: string) =>
      providerRetryDelayMs(
        new Response(null, {
          status: 503,
          ...(value === undefined ? {} : { headers: { "retry-after": value } }),
        }),
        0,
      );
    expect(after("2")).toBe(2_000);
    expect(after("0")).toBe(0);
    expect(after("10")).toBe(10_000);
    // Longer than we wait: no retry at all, the answer stands.
    expect(after("11")).toBeUndefined();
    expect(after("3600")).toBeUndefined();
    expect(
      after(new Date(Date.now() + 3_600_000).toUTCString()),
    ).toBeUndefined();
    expect(after("soon")).toBeGreaterThan(0);
    expect(after()).toBeGreaterThan(0);
    expect(after()).toBeLessThan(10_000);
  });
});
