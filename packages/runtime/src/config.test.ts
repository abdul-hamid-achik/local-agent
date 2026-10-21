import { describe, expect, test } from "vitest";
import {
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
});
