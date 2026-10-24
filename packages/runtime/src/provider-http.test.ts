import { expect, test } from "vitest";
import { admitOpenAILocalModel } from "#runtime/admission";
import { resolveProvider } from "#runtime/config";

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
