import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  type AgentEvent,
  type ApprovalRequest,
  runAgent,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Body = {
  messages: { role: string; content: string }[];
  tools: { function: { name: string } }[];
};
const BUILTIN_FETCH = globalThis.fetch;
let responder:
  | ((url: string) => Response | Promise<Response> | undefined)
  | undefined;
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const response = responder?.(String(input));
  return response ?? BUILTIN_FETCH(input, init);
}) as typeof fetch;
/** Non-loopback URLs answered by the responder; everything else hits real fetch. */
function stubFetch(
  fn: (url: string) => Response | Promise<Response> | undefined,
) {
  responder = fn;
}
function restoreFetch() {
  responder = undefined;
}
async function session(calls: Call[], intent: "plan" | "build" = "plan") {
  const root = await mkdtemp(join(tmpdir(), "local-agent-web-"));
  const bodies: Body[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities: ["completion", "tools"],
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await request.json()) as Body;
      bodies.push(body);
      const call = calls[body.messages.filter((m) => m.role === "tool").length];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Done", ...(call ? { tool_calls: [{ function: call }] } : {}) }, done: true })}\n`,
      );
    },
  });
  const events: AgentEvent[] = [];

  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent,
        signal: AbortSignal.timeout(10_000),
        approve: (async (proposal: ApprovalRequest) => true) as never,
      },
    ))
      events.push(event);
  } finally {
    await server.stop(true);
  }
  const results = events.flatMap((event) =>
    event.type === "tool_result" ? [event] : [],
  );
  return {
    root,
    bodies,
    results,
    output: (index: number) => results[index]?.output ?? "",
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

test("fetch follows redirects and converts HTML to readable text", async () => {
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (url === "http://docs.example/guide")
      return new Response(null, {
        status: 302,
        headers: { location: "https://docs.example/guide/" },
      });
    return new Response(
      `<html><head><title>t</title><script>alert(1)</script><style>a{}</style></head><body><h1>Install</h1><p>Run  bun  install.</p><p>Second paragraph.</p></body></html>`,
      { headers: { "content-type": "text/html; charset=utf-8" } },
    );
  });
  const run = await session([
    { name: "fetch", arguments: { url: "http://docs.example/guide" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("https://docs.example/guide/");
    expect(output).toContain("Install\nRun bun install.\nSecond paragraph.");
    expect(output).not.toContain("alert");
    expect(output).toMatch(/^(\d+) bytes from https:\/\/docs\.example/);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch truncates bodies over 512 KiB with a note", async () => {
  stubFetch((url) =>
    url.startsWith("http://127.0.0.1")
      ? undefined
      : new Response("a".repeat(600_000), {
          headers: { "content-type": "text/plain" },
        }),
  );
  const run = await session([
    { name: "fetch", arguments: { url: "https://big.example/file.txt" } },
  ]);
  try {
    const output = run.output(0);
    expect(output).toContain("(truncated at 512 KiB)");
    expect(output).toContain("a".repeat(100));
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch reports HTTP errors and refuses non-text content", async () => {
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    return url.includes("/missing")
      ? new Response("nope", {
          status: 404,
          headers: { "content-type": "text/plain" },
        })
      : new Response(new Uint8Array([0, 1, 2]), {
          headers: { "content-type": "image/png" },
        });
  });
  const run = await session([
    { name: "fetch", arguments: { url: "https://docs.example/missing" } },
    { name: "fetch", arguments: { url: "https://docs.example/logo.png" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual([
      "failed",
      "failed",
    ]);
    expect(run.output(0)).toContain("HTTP 404");
    expect(run.output(1)).toContain("unsupported content type");
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch keeps final output within the 100k-char tool limit", async () => {
  stubFetch((url) =>
    url.startsWith("http://127.0.0.1")
      ? undefined
      : new Response("word ".repeat(40_000), {
          headers: { "content-type": "text/plain" },
        }),
  );
  const run = await session([
    { name: "fetch", arguments: { url: "https://big.example/long.txt" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output.length).toBeLessThanOrEqual(100_000);
    expect(output).toContain("…(output truncated at 100k chars)");
    // The byte-count header reports the returned body, not the full download.
    const header = Number(output.match(/^(\d+) bytes from /)?.[1]);
    expect(header).toBeGreaterThan(0);
    expect(header).toBeLessThan(200_000 * 5);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("web_search parses SearXNG results and reports an unreachable instance", async () => {
  const searxng = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      expect(new URL(request.url).searchParams.get("q")).toBe("bun test");
      return Response.json({
        results: [
          {
            title: "Bun docs",
            url: "https://bun.sh/docs/test",
            content: "Run tests with bun test.",
          },
          { url: "https://example.org/other", content: "Second" },
        ],
      });
    },
  });
  process.env.LOCAL_AGENT_SEARXNG_URL = searxng.url.origin;
  let searxngUp = true;
  stubFetch((url) => {
    // Loopback Ollama (and the live SearXNG fixture) go through real fetch.
    if (url.includes("/search?") && !searxngUp)
      throw new Error("connection refused");
    return undefined;
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  searxngUp = false;
  const offline = await session([
    { name: "web_search", arguments: { query: "offline" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("1. Bun docs");
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output).toContain("Run tests with bun test.");
    expect(offline.results.map((result) => result.status)).toEqual(["failed"]);
    const failure = offline.output(0);
    expect(failure).toContain("unreachable");
    expect(failure).toContain("docker run -d -p 8888:8080 searxng/searxng");
  } finally {
    restoreFetch();
    delete process.env.LOCAL_AGENT_SEARXNG_URL;
    await searxng.stop(true);
    await run.cleanup();
    await offline.cleanup();
  }
});
