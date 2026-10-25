import { describe, expect, test } from "vitest";
import {
  DEFAULT_OLLAMA_MODEL,
  ProviderSchema,
  providerLocality,
  providerStreamIdleTimeoutMs,
  resolveProvider,
} from "#runtime/config";

describe("provider configuration boundary", () => {
  test.each([
    ["0.0.0.0", "http://127.0.0.1:11434"],
    ["0.0.0.0:11500", "http://127.0.0.1:11500"],
    ["http://0.0.0.0:11434", "http://127.0.0.1:11434"],
    ["localhost", "http://localhost:11434"],
    ["localhost:80", "http://localhost"],
    ["::", "http://[::1]:11434"],
    ["[::]:11500", "http://[::1]:11500"],
    ["https://localhost", "https://localhost"],
  ])("normalizes Ollama bind address %s", (host, expected) => {
    expect(resolveProvider({ OLLAMA_HOST: host }, "fixture").base_url).toBe(
      expected,
    );
  });

  test("bind normalization does not authorize remote hosts or hide invalid origins", () => {
    for (const host of [
      "192.168.1.20:11434",
      "https://remote.example",
      "http://0.0.0.0:11434/private",
      "http://user:secret@0.0.0.0:11434",
    ])
      expect(() => resolveProvider({ OLLAMA_HOST: host }, "fixture")).toThrow();
  });
  test("rejects malformed endpoint input without leaking a URL exception", () => {
    const result = ProviderSchema.safeParse({
      type: "ollama",
      base_url: "http://%",
      model: "fixture",
    });
    expect(result.success).toBe(false);
    if (!result.success)
      expect(result.error.issues[0]?.message).toBe(
        "Use a valid HTTP(S) Ollama origin",
      );
  });

  test("preserves Chalupa negotiated limits and exposes milliseconds", () => {
    const provider = resolveProvider({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "http://127.0.0.1:11435",
        model: "fixture",
        context_size: 65536,
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout: "1.5s",
      }),
    });
    expect(provider).toMatchObject({
      context_size: 65536,
      max_model_bytes: 40_000_000_000,
      stream_idle_timeout: "1.5s",
      stream_idle_timeout_ms: 1500,
    });
  });

  test("keeps a Chalupa keep_alive, and refuses a bad one or one on another provider", () => {
    const profile = (keep_alive: unknown) => ({
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "http://127.0.0.1:11435",
        model: "fixture",
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout: "10m",
        keep_alive,
      }),
    });
    for (const value of ["30m", "1h30m", "-1m", "-1", "0", 3600, -1])
      expect(resolveProvider(profile(value)).keep_alive).toBe(value);
    for (const value of ["forever", "30 m", "", 1.5])
      expect(() => resolveProvider(profile(value))).toThrow(/keep_alive/);
    // Without it, nothing is invented: the server keeps its default.
    expect(resolveProvider(profile(undefined))).not.toHaveProperty(
      "keep_alive",
    );
    for (const type of ["ollama", "openai_local"])
      expect(
        ProviderSchema.safeParse({
          type,
          base_url: "http://127.0.0.1:11434",
          model: "fixture",
          keep_alive: "30m",
        }).success,
      ).toBe(false);
  });

  test("requires remote negotiated limits and rejects invalid durations", () => {
    expect(() =>
      resolveProvider({
        LOCAL_AGENT_PROVIDER: "chalupa",
        LOCAL_AGENT_LOCAL_ONLY: "false",
        LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
          type: "ollama_remote",
          base_url: "https://remote.example",
          model: "fixture",
        }),
      }),
    ).toThrow(/max_model_bytes|stream_idle_timeout/);
    expect(() =>
      resolveProvider({
        LOCAL_AGENT_PROVIDER: "chalupa",
        LOCAL_AGENT_LOCAL_ONLY: "false",
        LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
          type: "ollama_remote",
          base_url: "https://remote.example",
          model: "fixture",
          context_size: 65536,
          max_model_bytes: 40_000_000_000,
          stream_idle_timeout: "0s",
        }),
      }),
    ).toThrow(/stream_idle_timeout/);
  });

  test.each(["", "   "])(
    "rejects an empty Chalupa stream_idle_timeout (%j) instead of defaulting to 5 minutes",
    (stream_idle_timeout) => {
      const result = ProviderSchema.safeParse({
        type: "ollama_remote",
        base_url: "https://remote.example",
        model: "fixture",
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout,
      });
      expect(result.success).toBe(false);
      if (!result.success)
        expect(result.error.issues).toContainEqual(
          expect.objectContaining({
            path: ["stream_idle_timeout"],
            message: expect.stringMatching(/must not be empty/),
          }),
        );
    },
  );

  test("does not allow a Chalupa launch to override its negotiated model", () => {
    const env = {
      LOCAL_AGENT_PROVIDER: "chalupa",
      LOCAL_AGENT_LOCAL_ONLY: "false",
      LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
        type: "ollama_remote",
        base_url: "https://remote.example",
        model: "fixture",
        context_size: 65536,
        max_model_bytes: 40_000_000_000,
        stream_idle_timeout: "10m",
      }),
    };
    expect(() => resolveProvider(env, "other-model")).toThrow(
      "Select the model in Chalupa before launching",
    );
  });

  test("derives the watchdog when callers parse a profile directly", () => {
    const provider = ProviderSchema.parse({
      type: "ollama_remote",
      base_url: "https://remote.example",
      model: "fixture",
      context_size: 65536,
      max_model_bytes: 40_000_000_000,
      stream_idle_timeout: "10m",
    });
    expect(providerStreamIdleTimeoutMs(provider)).toBe(600_000);
    // An unparsed profile with an empty value is invalid, not the default.
    expect(() =>
      providerStreamIdleTimeoutMs({ ...provider, stream_idle_timeout: "" }),
    ).toThrow(/stream_idle_timeout is invalid/);
    expect(() =>
      ProviderSchema.parse({
        ...provider,
        stream_idle_timeout: "999999999999999999999h",
      }),
    ).toThrow(/stream_idle_timeout/);
  });

  const chalupa = (extra: Record<string, unknown> = {}) => ({
    LOCAL_AGENT_PROVIDER: "chalupa",
    LOCAL_AGENT_LOCAL_ONLY: "false",
    LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
      type: "ollama_remote",
      base_url: "https://remote.example",
      model: "ornith-1.5:9b",
      context_size: 65536,
      max_model_bytes: 40_000_000_000,
      stream_idle_timeout: "10m",
      ...extra,
    }),
  });

  test("local Ollama defaults to the small model; flag and variable override it", () => {
    expect(DEFAULT_OLLAMA_MODEL).toBe("qwen3.5:4b-q4_K_M");
    expect(resolveProvider({}).model).toBe(DEFAULT_OLLAMA_MODEL);
    expect(resolveProvider({ LOCAL_AGENT_MODEL: "  " }).model).toBe(
      DEFAULT_OLLAMA_MODEL,
    );
    expect(resolveProvider({ LOCAL_AGENT_MODEL: "env:1b" }).model).toBe(
      "env:1b",
    );
    expect(
      resolveProvider({ LOCAL_AGENT_MODEL: "env:1b" }, "flag:2b").model,
    ).toBe("flag:2b");
  });

  test("an invalid profile is named in one line, not as a schema dump", () => {
    let message = "";
    try {
      resolveProvider(chalupa({ max_model_bytes: undefined, model: "" }));
    } catch (error) {
      message = error instanceof Error ? error.message : String(error);
    }
    expect(message).toMatch(
      /^Chalupa provider config \(LOCAL_AGENT_PROVIDER_CONFIG\) is invalid: model: /,
    );
    expect(message).not.toContain("\n");
  });

  test("Chalupa keeps the model of its profile", () => {
    expect(resolveProvider(chalupa()).model).toBe("ornith-1.5:9b");
  });

  test("think is unset by default and read from the flag before the variable", () => {
    expect(resolveProvider({})).not.toHaveProperty("think");
    expect(resolveProvider({ LOCAL_AGENT_THINK: "0" }).think).toBe(false);
    expect(resolveProvider({ LOCAL_AGENT_THINK: "1" }).think).toBe(true);
    expect(
      resolveProvider({ LOCAL_AGENT_THINK: "1" }, undefined, { think: "off" })
        .think,
    ).toBe(false);
    expect(resolveProvider({}, undefined, { think: "on" }).think).toBe(true);
    expect(() => resolveProvider({}, undefined, { think: "maybe" })).toThrow(
      "--think must be on or off",
    );
    expect(() => resolveProvider({ LOCAL_AGENT_THINK: "x" })).toThrow(
      "LOCAL_AGENT_THINK must be on or off",
    );
    // It applies on top of a Chalupa profile, which does not set it.
    expect(resolveProvider(chalupa(), undefined, { think: "off" }).think).toBe(
      false,
    );
    expect(resolveProvider(chalupa())).not.toHaveProperty("think");
  });

  test("context overrides the local default only, and is parsed strictly", () => {
    expect(resolveProvider({}).context_size).toBe(16_384);
    expect(resolveProvider({ LOCAL_AGENT_CONTEXT: "32768" }).context_size).toBe(
      32_768,
    );
    expect(
      resolveProvider({ LOCAL_AGENT_CONTEXT: "8192" }, undefined, {
        context: "32k",
      }).context_size,
    ).toBe(32_768);
    for (const bad of ["abc", "0", "100", "-5", "1.5k", "9999999999"])
      expect(() => resolveProvider({}, undefined, { context: bad })).toThrow(
        /--context must be a token count/,
      );
    // Chalupa negotiates its context: the variable is ignored, the flag refused.
    expect(
      resolveProvider({ ...chalupa(), LOCAL_AGENT_CONTEXT: "8192" })
        .context_size,
    ).toBe(65_536);
    expect(() =>
      resolveProvider(chalupa(), undefined, { context: "8192" }),
    ).toThrow("Chalupa negotiates the context size");
  });
});

describe("openai_local provider configuration", () => {
  const local = (baseURL: string, extra: Record<string, unknown> = {}) => ({
    LOCAL_AGENT_PROVIDER: "openai_local",
    LOCAL_AGENT_PROVIDER_CONFIG: JSON.stringify({
      type: "openai_local",
      baseURL,
      model: "qwen3-4b",
      ...extra,
    }),
  });

  test("resolves a loopback OpenAI-compatible profile with optional api_key", () => {
    expect(resolveProvider(local("http://127.0.0.1:8080/v1"))).toMatchObject({
      type: "openai_local",
      base_url: "http://127.0.0.1:8080/v1",
      model: "qwen3-4b",
      context_size: 16_384,
    });
    expect(
      resolveProvider(local("http://localhost:1234/v1", { api_key: "x" }))
        .api_key,
    ).toBe("x");
  });

  test("rejects non-loopback base URLs unless LOCAL_AGENT_LOCAL_ONLY=false", () => {
    expect(() => resolveProvider(local("http://192.168.1.5:8080/v1"))).toThrow(
      /loopback/,
    );
    expect(() => resolveProvider(local("http://example.com/v1"))).toThrow(
      /loopback/,
    );
    expect(
      resolveProvider({
        ...local("http://192.168.1.5:8080/v1"),
        LOCAL_AGENT_LOCAL_ONLY: "false",
      }).type,
    ).toBe("openai_local");
  });

  test("refuses an api_key over cleartext http to a non-loopback host, in every spelling", () => {
    const open = { LOCAL_AGENT_LOCAL_ONLY: "false" };
    for (const url of [
      "http://192.168.1.5:8080/v1",
      "http://10.0.0.5:8080/v1",
      "http://example.com/v1",
      "http://[2001:db8::1]:8080/v1",
      "http://128.0.0.1/v1",
      "http://localhost.example.com/v1",
    ])
      expect(
        () => resolveProvider({ ...local(url, { api_key: "k" }), ...open }),
        url,
      ).toThrow(/use an https base URL or drop the api_key/);
    // The message says what to do, and the key never appears in it.
    expect(() =>
      resolveProvider({
        ...local("http://10.0.0.5/v1", { api_key: "sk-secret-value" }),
        ...open,
      }),
    ).toThrow(/^(?!.*sk-secret-value)/s);
  });

  // The classification is of the host the URL parser resolves, not its spelling.
  test.each([
    "http://localhost.evil.com/v1",
    "http://localhost./v1",
    "http://127.0.0.1.nip.io/v1",
    "http://0.0.0.0/v1",
    "HTTP://10.0.0.5/v1",
    "http://[::ffff:127.0.0.1]/v1",
  ])("refuses an api_key over http to %s", (url) => {
    expect(() =>
      resolveProvider({
        ...local(url, { api_key: "k" }),
        LOCAL_AGENT_LOCAL_ONLY: "false",
      }),
    ).toThrow(/use an https base URL,? or drop the api_key/);
  });

  // The URL parser folds 127.0.0.1. itself; localhost. it keeps.
  test.each(["http://localhost./v1", "http://LOCALHOST./v1"])(
    "says how to fix a rooted loopback name: %s",
    (url) => {
      expect(() =>
        resolveProvider({
          ...local(url, { api_key: "k" }),
          LOCAL_AGENT_LOCAL_ONLY: "false",
        }),
      ).toThrow(
        "write the host without its trailing dot, use an https base URL, or drop the api_key",
      );
      // Only a name that is loopback without its dot gets the hint.
      for (const other of ["http://evil.com./v1", "http://localhosts/v1"])
        expect(() =>
          resolveProvider({
            ...local(other, { api_key: "k" }),
            LOCAL_AGENT_LOCAL_ONLY: "false",
          }),
        ).toThrow(/^(?![\s\S]*trailing dot)[\s\S]*use an https base URL/);
    },
  );

  test("a userinfo trick is refused whatever the key", () => {
    expect(() =>
      resolveProvider({
        ...local("http://127.0.0.1@evil.com/v1", { api_key: "k" }),
        LOCAL_AGENT_LOCAL_ONLY: "false",
      }),
    ).toThrow(/embedded credentials/);
  });

  test.each([
    "http://127.1/v1",
    "http://2130706433/v1",
    "HTTP://127.0.0.1:8080/v1",
  ])("takes an api_key over http to %s, which is loopback", (url) => {
    expect(
      resolveProvider({
        ...local(url, { api_key: "k" }),
        LOCAL_AGENT_LOCAL_ONLY: "false",
      }).api_key,
    ).toBe("k");
  });

  test("keeps http to loopback and keyless http to a LAN host, and https with a key", () => {
    const open = { LOCAL_AGENT_LOCAL_ONLY: "false" };
    for (const url of [
      "http://127.0.0.1:8080/v1",
      "http://127.5.6.7:8080/v1",
      "http://localhost:1234/v1",
      "http://[::1]:1234/v1",
    ])
      expect(
        resolveProvider({ ...local(url, { api_key: "k" }), ...open }).api_key,
        url,
      ).toBe("k");
    expect(
      resolveProvider({ ...local("http://192.168.1.5:8080/v1"), ...open }).type,
    ).toBe("openai_local");
    expect(
      resolveProvider({
        ...local("https://models.example.com/v1", { api_key: "k" }),
        ...open,
      }).api_key,
    ).toBe("k");
  });

  test("rejects malformed base URLs and profile mixing", () => {
    for (const bad of [
      "http://127.0.0.1:8080/v1/",
      "http://127.0.0.1:8080/v1?x=1",
      "http://user:pw@127.0.0.1:8080/v1",
      "not-a-url",
    ])
      expect(() => resolveProvider(local(bad))).toThrow();
    expect(() =>
      resolveProvider(
        local("http://127.0.0.1:8080/v1", { max_model_bytes: 5 }),
      ),
    ).toThrow(/ollama_remote/);
    expect(() =>
      resolveProvider({
        ...local("http://127.0.0.1:8080/v1"),
        LOCAL_AGENT_PROVIDER_CONFIG: "{",
      }),
    ).toThrow(/JSON/);
    expect(() =>
      resolveProvider(local("http://127.0.0.1:8080/v1"), "other"),
    ).toThrow(/LOCAL_AGENT_PROVIDER_CONFIG/);
  });
});

test("a provider's locality: Chalupa is remote, loopback is local, any other host is remote", () => {
  expect(
    providerLocality({
      type: "ollama_remote",
      base_url: "http://127.0.0.1:11434",
    }),
  ).toBe("remote");
  expect(
    providerLocality({ type: "ollama", base_url: "http://localhost:11434" }),
  ).toBe("local");
  for (const base_url of [
    "http://127.0.0.2:8080/v1",
    "http://[::1]:8080/v1",
    "http://localhost:8080/v1",
  ])
    expect(providerLocality({ type: "openai_local", base_url })).toBe("local");
  for (const base_url of [
    "http://192.168.1.20:8080/v1",
    "https://models.example.com/v1",
    "http://localhost.:8080/v1",
    "not a url",
  ])
    expect(providerLocality({ type: "openai_local", base_url })).toBe("remote");
});
