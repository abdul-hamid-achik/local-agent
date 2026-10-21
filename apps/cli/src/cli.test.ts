import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const directory = dirname(fileURLToPath(import.meta.url));

test.each(["chalupa", "ollama"])(
  "headless CLI consumes %s configuration and emits only JSON events",
  async (provider) => {
    const server = Bun.serve({
      hostname: "127.0.0.1",
      port: 0,
      async fetch(request) {
        if (new URL(request.url).pathname === "/api/tags")
          return Response.json({ models: [{ name: "fixture", size: 1000 }] });
        if (
          new URL(request.url).pathname === "/api/show" &&
          request.method === "POST"
        ) {
          expect(await request.json()).toMatchObject({ model: "fixture" });
          return Response.json({
            capabilities: ["completion", "tools"],
            model_info: { "fixture.context_length": 65536 },
          });
        }
        const body = await request.json();
        expect(body.model).toBe("fixture");
        expect(body.messages[0].content).toContain("Public Website Boundary");
        expect(body.options.num_ctx).toBe(
          provider === "chalupa" ? 65536 : 16384,
        );
        return new Response(
          `${JSON.stringify({ message: { role: "assistant", content: "Hello" }, done: true, prompt_eval_count: 1, eval_count: 1 })}\n`,
        );
      },
    });
    try {
      const child = Bun.spawn(
        [
          process.execPath,
          "run",
          "--silent",
          "dev",
          "--mode",
          "headless",
          "-p",
          "Hello",
          "--json",
        ],
        {
          cwd: join(directory, "../../.."),
          env:
            provider === "ollama"
              ? {
                  OLLAMA_HOST: `0.0.0.0:${server.port}`,
                  LOCAL_AGENT_MODEL: "fixture",
                }
              : {
                  LOCAL_AGENT_PROVIDER: "chalupa",
                  LOCAL_AGENT_LOCAL_ONLY: "false",
                  LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
                    type: "ollama_remote",
                    model: "fixture",
                    base_url: server.url.origin,
                    context_size: 65536,
                    max_model_bytes: 40_000_000_000,
                    stream_idle_timeout: "10m",
                  }),
                },
          stdout: "pipe",
          stderr: "pipe",
        },
      );
      const [stdout, stderr, code] = await Promise.all([
        new Response(child.stdout).text(),
        new Response(child.stderr).text(),
        child.exited,
      ]);
      expect(code).toBe(0);
      expect(stderr).toBe("");
      expect(
        stdout
          .trim()
          .split("\n")
          .map((line) => JSON.parse(line)),
      ).toEqual([
        { type: "text", text: "Hello" },
        { type: "done", inputTokens: 1, outputTokens: 1 },
        { type: "message", message: { role: "assistant", content: "Hello" } },
        {
          type: "turn_complete",
          messages: [
            { role: "user", content: "Hello" },
            { role: "assistant", content: "Hello" },
          ],
        },
      ]);
    } finally {
      await server.stop(true);
    }
  },
);

test("production bundle keeps native rendering behind a dynamic import", async () => {
  const result = await Bun.build({
    entrypoints: [join(directory, "main.ts")],
    target: "bun",
    packages: "external",
    splitting: true,
  });
  expect(result.success).toBe(true);
  const entry = result.outputs.find((output) => output.kind === "entry-point");
  expect(entry).toBeDefined();
  const source = await entry?.text();
  expect(source).toContain("import(");
  expect(source).not.toMatch(
    /from\s+['"](?:@local-agent\/vue-tui['"]|vue['"]|@opentui\/)/,
  );
});
