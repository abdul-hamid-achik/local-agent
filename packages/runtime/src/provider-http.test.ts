import { describe, expect, test } from "vitest";
import { admitOpenAILocalModel } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";
import { chat, parseOpenAISSE, type RunEvent } from "#runtime/index";

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
) => {
  const seen: RunEvent[] = [];
  for await (const event of chat(
    provider,
    [{ role: "user", content: "hi" }],
    signal,
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
        { type: "done" },
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
      /exceeds 8 MiB/,
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
