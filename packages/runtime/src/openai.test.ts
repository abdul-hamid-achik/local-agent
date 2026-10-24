import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import { admitOpenAILocalModel } from "#runtime/admission";
import { type AgentEvent, runAgent } from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { chat } from "#runtime/index";
import { Workspace } from "#runtime/workspace";

const localProvider = (baseURL: string) =>
  resolveProvider({
    LOCAL_AGENT_PROVIDER: "openai_local",
    LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
      type: "openai_local",
      baseURL,
      model: "fixture-model",
    }),
  });

test("streams OpenAI-compatible SSE into the Ollama event shape", async () => {
  let request:
    | { url: string; body: unknown; authorization?: string }
    | undefined;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      request = {
        url: req.url,
        body: (await req.json()) as Record<string, unknown>,
        authorization: req.headers.get("authorization") ?? undefined,
      };
      const sse = [
        JSON.stringify({
          choices: [{ delta: { reasoning_content: "pondering " } }],
        }),
        JSON.stringify({
          choices: [{ delta: { content: "Hola " } }],
        }),
        JSON.stringify({
          choices: [{ delta: { content: "🛶" } }],
        }),
        JSON.stringify({
          choices: [
            {
              delta: {
                tool_calls: [
                  {
                    index: 0,
                    id: "call-1",
                    function: { name: "read", arguments: '{"path":"h' },
                  },
                ],
              },
            },
          ],
        }),
        JSON.stringify({
          choices: [
            {
              delta: {
                tool_calls: [
                  { index: 0, function: { arguments: 'ello.txt"}' } },
                ],
              },
            },
          ],
        }),
        JSON.stringify({
          choices: [],
          usage: { prompt_tokens: 11, completion_tokens: 3 },
        }),
        "[DONE]",
        JSON.stringify({
          choices: [{ delta: { content: "after done" } }],
        }),
      ]
        .map((line) => `data: ${line}\n`)
        .join("\n");
      return new Response(sse, {
        headers: { "Content-Type": "text/event-stream" },
      });
    },
  });
  try {
    const events = [];
    for await (const event of chat(
      localProvider(`${server.url.origin}/v1`),
      [{ role: "user", content: "Hello" }],
      new AbortController().signal,
    ))
      events.push(event);
    expect(request).toMatchObject({
      url: `${server.url.origin}/v1/chat/completions`,
      body: {
        model: "fixture-model",
        stream: true,
        stream_options: { include_usage: true },
        messages: [{ role: "user", content: "Hello" }],
      },
    });
    expect(request?.authorization).toBeUndefined();
    expect(events).toEqual([
      { type: "thinking", text: "pondering " },
      { type: "text", text: "Hola " },
      { type: "text", text: "🛶" },
      {
        type: "tool_call",
        call: {
          id: "call-1",
          function: { name: "read", arguments: { path: "hello.txt" } },
        },
      },
      { type: "done", inputTokens: 11, outputTokens: 3 },
    ]);
  } finally {
    server.stop(true);
  }
});

test("sends the bearer token when the profile carries an api_key", async () => {
  let authorization: string | undefined;
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(req) {
      authorization = req.headers.get("authorization") ?? undefined;
      return new Response("data: [DONE]\n\n", {
        headers: { "Content-Type": "text/event-stream" },
      });
    },
  });
  try {
    const provider = resolveProvider({
      LOCAL_AGENT_PROVIDER: "openai_local",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "openai_local",
        baseURL: `${server.url.origin}/v1`,
        model: "fixture-model",
        api_key: "sk-local-dummy",
      }),
    });
    const events = [];
    for await (const event of chat(
      provider,
      [{ role: "user", content: "Hi" }],
      new AbortController().signal,
    ))
      events.push(event);
    expect(authorization).toBe("Bearer sk-local-dummy");
    expect(events).toEqual([{ type: "done", inputTokens: 0, outputTokens: 0 }]);
  } finally {
    server.stop(true);
  }
});

test("admits against GET /models, skips on 404, rejects unknown models", async () => {
  let status = 200;
  const bodies: Record<number, unknown> = {
    200: { data: [{ id: "fixture-model" }, { id: "other" }] },
    404: { error: "not found" },
  };
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(req) {
      expect(new URL(req.url).pathname).toBe("/v1/models");
      return Response.json(bodies[status], { status });
    },
  });
  try {
    const provider = localProvider(`${server.url.origin}/v1`);
    const signal = new AbortController().signal;
    expect(await admitOpenAILocalModel(provider, signal)).toEqual({
      status: "validated",
      model: "fixture-model",
    });
    status = 404;
    const skipped = await admitOpenAILocalModel(provider, signal);
    expect(skipped).toMatchObject({ status: "unverified" });
    expect("message" in skipped ? skipped.message : "").toMatch(
      /skipped model validation/,
    );
    status = 200;
    const rejected = await admitOpenAILocalModel(
      { ...provider, model: "missing" },
      signal,
    );
    expect(rejected).toMatchObject({ status: "rejected" });
    expect("message" in rejected ? rejected.message : "").toMatch(
      /missing is not served/,
    );
  } finally {
    server.stop(true);
  }
});

test("an openai_local turn runs, admitted against /models once per process", async () => {
  const paths: string[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(req) {
      const path = new URL(req.url).pathname;
      paths.push(path);
      if (path === "/v1/models")
        return Response.json({ data: [{ id: "fixture-model" }] });
      const sse = [
        JSON.stringify({ choices: [{ delta: { content: "Hello" } }] }),
        JSON.stringify({
          choices: [],
          usage: { prompt_tokens: 20, completion_tokens: 1 },
        }),
        "[DONE]",
      ]
        .map((line) => `data: ${line}\n\n`)
        .join("");
      return new Response(sse, {
        headers: { "Content-Type": "text/event-stream" },
      });
    },
  });
  const root = await mkdtemp(join(tmpdir(), "local-agent-openai-"));
  try {
    using workspace = await Workspace.create(root);
    const provider = localProvider(`${server.url.origin}/v1`);
    for (const prompt of ["hi", "again"]) {
      const events: AgentEvent[] = [];
      for await (const event of runAgent(
        provider,
        [{ role: "user", content: prompt }],
        {
          workspace,
          signal: AbortSignal.timeout(5_000),
          approve: async () => false,
        },
      ))
        events.push(event);
      expect(events.at(-1)?.type).toBe("turn_complete");
    }
    // No Ollama metadata endpoint is asked, and /models only for the first turn.
    expect(paths).toEqual([
      "/v1/models",
      "/v1/chat/completions",
      "/v1/chat/completions",
    ]);
  } finally {
    server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});

test("an openai_local model the server does not list is refused before the chat", async () => {
  const paths: string[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(req) {
      paths.push(new URL(req.url).pathname);
      return Response.json({ data: [{ id: "other-model" }] });
    },
  });
  const root = await mkdtemp(join(tmpdir(), "local-agent-openai-"));
  try {
    using workspace = await Workspace.create(root);
    await expect(
      runAgent(
        localProvider(`${server.url.origin}/v1`),
        [{ role: "user", content: "hi" }],
        {
          workspace,
          signal: AbortSignal.timeout(5_000),
          approve: async () => false,
        },
      ).next(),
    ).rejects.toThrow("Model fixture-model is not served by");
    expect(paths).toEqual(["/v1/models"]);
  } finally {
    server.stop(true);
    await rm(root, { recursive: true, force: true });
  }
});
