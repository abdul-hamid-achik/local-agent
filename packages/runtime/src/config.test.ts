import { describe, expect, test } from "vitest";
import {
  DEFAULT_OLLAMA_MODEL,
  ProviderSchema,
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
