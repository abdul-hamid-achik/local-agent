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
import type { McpConnection } from "#runtime/mcp";
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
/** Every stubbed request's init, in call order (header assertions). */
let requestInits: { url: string; headers: Record<string, string> }[] = [];
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  if (init?.headers)
    requestInits.push({
      url,
      headers: init.headers as Record<string, string>,
    });
  const response = responder?.(url);
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
  requestInits = [];
}
async function session(
  calls: Call[],
  intent: "plan" | "build" | "chat" = "plan",
  vision = false,
  mcp?: McpConnection[],
) {
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
          capabilities: vision
            ? ["completion", "tools", "vision"]
            : ["completion", "tools"],
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
        mcp,
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
    toolMessages: events.flatMap((event) =>
      event.type === "message" && event.message.role === "tool"
        ? [event.message]
        : [],
    ),
    output: (index: number) => results[index]?.output ?? "",
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

/** One fake MCP server holding a single browser-shaped tool. */
function fakeBrowserMcp(): McpConnection[] {
  return [
    {
      tools: [
        {
          name: "browser__browser_navigate",
          description: "navigate",
          inputSchema: { type: "object" },
          readOnly: false,
        },
      ],
      async callTool() {
        return {
          transport: "success" as const,
          domain: "unknown" as const,
          evidence: "unverified" as const,
          isError: false,
          text: "navigated",
        };
      },
      async close() {},
    },
  ];
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
          headers: { "content-type": "application/octet-stream" },
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

const DDG_LITE_HTML = `<html><body><table>
<tr><td>1.</td><td><a rel="nofollow" href="/l/?uddg=https%3A%2F%2Fbun.sh%2Fdocs%2Ftest&amp;rut=abc">Bun docs</a></td></tr>
<tr><td class="result-snippet">Run tests with bun test.</td></tr>
<tr><td>2.</td><td><a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fa+b&amp;rut=def">Second &amp; result</a></td></tr>
<tr><td class="result-snippet">Another snippet</td></tr>
</table></body></html>`;
const ddgResponse = () =>
  new Response(DDG_LITE_HTML, {
    headers: { "content-type": "text/html; charset=utf-8" },
  });

const DDG_HTML_HTML = `<html><body><div class="result results_links">
<h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fbun.sh%2Fdocs%2Ftest&amp;rut=abc">Bun docs</a></h2>
<a class="result__snippet" href="https://bun.sh/docs/test">Run tests with bun test.</a>
</div></body></html>`;
const ddgHtmlResponse = () =>
  new Response(DDG_HTML_HTML, {
    headers: { "content-type": "text/html; charset=utf-8" },
  });
const ddgCaptcha = () =>
  new Response('<html><body><div id="challenge">captcha</div></body></html>', {
    headers: { "content-type": "text/html; charset=utf-8" },
  });

test("web_search falls back to DuckDuckGo Lite and decodes uddg links", async () => {
  delete process.env.LOCAL_AGENT_SEARXNG_URL;
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (url.startsWith("https://lite.duckduckgo.com/lite/"))
      return ddgResponse();
    throw new Error(`unexpected fetch: ${url}`);
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("1. Bun docs");
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output).toContain("Run tests with bun test.");
    expect(output).toContain("2. Second & result");
    expect(output).toContain("https://example.org/a b");
    expect(output.trimEnd().endsWith("via duckduckgo")).toBe(true);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("web_search retries DuckDuckGo Lite on a 5xx then uses the html endpoint", async () => {
  delete process.env.LOCAL_AGENT_SEARXNG_URL;
  const liteAttempts: number[] = [];
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (url.startsWith("https://lite.duckduckgo.com/lite/")) {
      liteAttempts.push(requestInits.length);
      return new Response("upstream error", { status: 503 });
    }
    if (url.startsWith("https://html.duckduckgo.com/html/"))
      return ddgHtmlResponse();
    throw new Error(`unexpected fetch: ${url}`);
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    // One retry per endpoint: Lite answered 503 twice before the fallback.
    expect(liteAttempts).toHaveLength(2);
    const output = run.output(0);
    expect(output).toContain("1. Bun docs");
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output).toContain("Run tests with bun test.");
    expect(output.trimEnd().endsWith("via duckduckgo html")).toBe(true);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("web_search falls back to the html endpoint when Lite returns a captcha page", async () => {
  delete process.env.LOCAL_AGENT_SEARXNG_URL;
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (url.startsWith("https://lite.duckduckgo.com/lite/"))
      return ddgCaptcha();
    if (url.startsWith("https://html.duckduckgo.com/html/"))
      return ddgHtmlResponse();
    throw new Error(`unexpected fetch: ${url}`);
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("1. Bun docs");
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output.trimEnd().endsWith("via duckduckgo html")).toBe(true);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("web_search errors list every endpoint tried when all parse zero hits", async () => {
  delete process.env.LOCAL_AGENT_SEARXNG_URL;
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (
      url.startsWith("https://lite.duckduckgo.com/lite/") ||
      url.startsWith("https://html.duckduckgo.com/html/")
    )
      return ddgCaptcha();
    throw new Error(`unexpected fetch: ${url}`);
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["failed"]);
    const failure = run.output(0);
    expect(failure).toContain("no search provider answered");
    expect(failure).toContain("https://lite.duckduckgo.com/lite/");
    expect(failure).toContain("https://html.duckduckgo.com/html/");
    expect(failure).toContain("LOCAL_AGENT_SEARXNG_URL");
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("web_search prefers a reachable SearXNG when configured", async () => {
  const searxng = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    fetch(request) {
      expect(new URL(request.url).searchParams.get("q")).toBe("bun test");
      expect(new URL(request.url).searchParams.get("format")).toBe("json");
      return Response.json({
        results: [
          {
            title: "Bun docs",
            url: "https://bun.sh/docs/test",
            content: "Run tests with bun test.",
          },
        ],
      });
    },
  });
  process.env.LOCAL_AGENT_SEARXNG_URL = searxng.url.origin;
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    throw new Error(`unexpected fetch: ${url}`);
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("1. Bun docs");
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output.trimEnd().endsWith("via searxng")).toBe(true);
  } finally {
    restoreFetch();
    delete process.env.LOCAL_AGENT_SEARXNG_URL;
    await searxng.stop(true);
    await run.cleanup();
  }
});

test("web_search falls through to DuckDuckGo when SearXNG is down", async () => {
  process.env.LOCAL_AGENT_SEARXNG_URL = "http://127.0.0.1:59999";
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1:59999"))
      throw new Error("connection refused");
    if (url.startsWith("https://lite.duckduckgo.com/lite/"))
      return ddgResponse();
    return undefined;
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("https://bun.sh/docs/test");
    expect(output.trimEnd().endsWith("via duckduckgo")).toBe(true);
  } finally {
    restoreFetch();
    delete process.env.LOCAL_AGENT_SEARXNG_URL;
    await run.cleanup();
  }
});

test("web_search errors mentioning LOCAL_AGENT_SEARXNG_URL when both providers fail", async () => {
  process.env.LOCAL_AGENT_SEARXNG_URL = "http://127.0.0.1:59999";
  stubFetch((url) => {
    if (url.startsWith("http://127.0.0.1")) return undefined;
    if (
      url.startsWith("https://lite.duckduckgo.com/lite/") ||
      url.startsWith("https://html.duckduckgo.com/html/")
    )
      return new Response("blocked", { status: 403 });
    return undefined;
  });
  const run = await session([
    { name: "web_search", arguments: { query: "bun test" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["failed"]);
    const failure = run.output(0);
    expect(failure).toContain("no search provider answered");
    expect(failure).toContain("SearXNG at http://127.0.0.1:59999");
    expect(failure).toContain("https://lite.duckduckgo.com/lite/");
    expect(failure).toContain("https://html.duckduckgo.com/html/");
    expect(failure).toContain("LOCAL_AGENT_SEARXNG_URL");
  } finally {
    restoreFetch();
    delete process.env.LOCAL_AGENT_SEARXNG_URL;
    await run.cleanup();
  }
});

test("chat intent offers the research set and configured MCP tools, without todo_write/task/effects", async () => {
  const run = await session([], "chat", false, fakeBrowserMcp());
  try {
    expect(run.bodies[0]?.tools.map((tool) => tool.function.name)).toEqual([
      "read",
      "read_image",
      "list",
      "search",
      "fetch",
      "web_search",
      "browser__browser_navigate",
    ]);
  } finally {
    await run.cleanup();
  }
  // Plan stays strictly read-only: the same MCP server is not offered there.
  const plan = await session([], "plan", false, fakeBrowserMcp());
  try {
    expect(
      plan.bodies[0]?.tools.map((tool) => tool.function.name),
    ).not.toContain("browser__browser_navigate");
  } finally {
    await plan.cleanup();
  }
});

test("chat intent hard-denies edits exactly like plan, but keeps MCP approval-gated", async () => {
  const run = await session(
    [
      {
        name: "edit",
        arguments: { path: "hello.txt", old_string: "a", new_string: "b" },
      },
    ],
    "chat",
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual(["denied"]);
    expect(run.output(0)).toBe("Chat mode does not permit edits");
    const plan = await session(
      [
        {
          name: "write",
          arguments: { path: "hello.txt", content: "b" },
        },
      ],
      "plan",
    );
    try {
      expect(plan.results.map((result) => result.status)).toEqual(["denied"]);
      expect(plan.output(0)).toBe("Plan mode does not permit writes");
    } finally {
      await plan.cleanup();
    }
  } finally {
    await run.cleanup();
  }
});

// 1x1 PNG, magic bytes intact.
const TINY_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

test("fetch attaches image/png downloads for vision models with a short descriptor", async () => {
  stubFetch((url) => {
    if (url === "http://img.example/pic.png")
      return new Response(TINY_PNG, {
        headers: { "content-type": "image/png" },
      });
    return undefined;
  });
  const run = await session(
    [{ name: "fetch", arguments: { url: "http://img.example/pic.png" } }],
    "plan",
    true,
  );
  try {
    console.log("OUTPUT:", JSON.stringify(run.results[0]));
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    expect(run.output(0)).toBe(
      "image 1x1 PNG from http://img.example/pic.png (attached)",
    );
    expect(run.toolMessages[0]?.images).toEqual([TINY_PNG.toString("base64")]);
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch describes images without attaching when the model lacks vision", async () => {
  stubFetch((url) => {
    if (url === "http://img.example/pic.png")
      return new Response(TINY_PNG, {
        headers: { "content-type": "image/png" },
      });
    return undefined;
  });
  const run = await session([
    { name: "fetch", arguments: { url: "http://img.example/pic.png" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    expect(run.output(0)).toBe(
      "image 1x1 PNG from http://img.example/pic.png (model fixture has no vision; image not attached)",
    );
    expect(run.toolMessages[0]?.images).toBeUndefined();
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch rejects oversized and bad-magic image downloads", async () => {
  const big = Buffer.alloc(5 * 1024 * 1024 + 1);
  TINY_PNG.copy(big, 0);
  stubFetch((url) => {
    if (url === "http://img.example/big.png")
      return new Response(big, { headers: { "content-type": "image/png" } });
    if (url === "http://img.example/fake.png")
      return new Response("definitely not a png", {
        headers: { "content-type": "image/png" },
      });
    return undefined;
  });
  const run = await session([
    { name: "fetch", arguments: { url: "http://img.example/big.png" } },
    { name: "fetch", arguments: { url: "http://img.example/fake.png" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual([
      "failed",
      "failed",
    ]);
    expect(run.output(0)).toContain("5 MiB");
    expect(run.output(1)).toContain("not a PNG");
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch appends a bounded extracted footer of top links and images", async () => {
  stubFetch((url) => {
    if (url !== "http://page.example/article") return undefined;
    return new Response(
      `<html><body><h1>Guide</h1>
<a href="/wiki/Boss">Boss name</a>
<a href="https://else.example/page">Elsewhere walkthrough notes</a>
<a href="#top">skip anchor</a>
<a href="mailto:x@example.com">skip mail</a>
<a href="https://else.example/page">duplicate</a>
<a href="/styles.css">skip css</a>
<img src="/icons/tiny.png" width="12" alt="icon">
<img src="/map.png" alt="Map of the region" srcset="/map.png 100w, /map-big.png 2000x1500">
</body></html>`,
      { headers: { "content-type": "text/html; charset=utf-8" } },
    );
  });
  const run = await session([
    { name: "fetch", arguments: { url: "http://page.example/article" } },
  ]);
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const output = run.output(0);
    expect(output).toContain("--- extracted ---");
    expect(output).toContain("links: 2");
    expect(output).toContain("1. http://page.example/wiki/Boss  Boss name");
    expect(output).toContain("2. https://else.example/page");
    expect(output).toContain("images: 1");
    expect(output).toContain(
      "1. http://page.example/map.png 2000x1500 map: Map of the region",
    );
    expect(output).not.toContain("icons");
    expect(output).not.toContain("styles.css");
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});

test("fetch sends browser headers and a same-origin referer on image requests", async () => {
  stubFetch((url) => {
    if (url === "http://img.example/pic.png")
      return new Response(TINY_PNG, {
        headers: { "content-type": "image/png" },
      });
    return undefined;
  });
  const run = await session(
    [{ name: "fetch", arguments: { url: "http://img.example/pic.png" } }],
    "plan",
    true,
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual(["succeeded"]);
    const request = requestInits.find((entry) =>
      entry.url.startsWith("http://img.example"),
    );
    expect(request?.headers["user-agent"]).toContain("Mozilla/5.0");
    expect(request?.headers["accept"]).toBe(
      "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8",
    );
    expect(request?.headers["referer"]).toBe("http://img.example");
  } finally {
    restoreFetch();
    await run.cleanup();
  }
});
