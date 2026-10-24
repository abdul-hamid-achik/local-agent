import { isIP } from "node:net";
import { z } from "zod";

/**
 * The model used with the local Ollama provider when neither `--model` nor
 * `LOCAL_AGENT_MODEL` names one: the smallest model that passed the agent
 * battery reliably. Chalupa profiles keep the model they negotiated.
 */
export const DEFAULT_OLLAMA_MODEL = "qwen3.5:4b-q4_K_M";

const defaultStreamIdleTimeoutMs = 300_000;
const maxStreamIdleTimeoutMs = 2_147_483_647;

const durationMilliseconds = (value: string) => {
  if (!/^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/u.test(value))
    return undefined;
  const units: Record<string, number> = {
    ns: 1e-6,
    us: 1e-3,
    µs: 1e-3,
    ms: 1,
    s: 1_000,
    m: 60_000,
    h: 3_600_000,
  };
  let milliseconds = 0;
  for (const [, amount, unit] of value.matchAll(
    /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/gu,
  ))
    milliseconds += Number(amount) * (units[unit ?? "ms"] ?? 0);
  const rounded = Math.ceil(milliseconds);
  return milliseconds > 0 &&
    rounded <= maxStreamIdleTimeoutMs &&
    Number.isSafeInteger(rounded)
    ? rounded
    : undefined;
};

export function providerStreamIdleTimeoutMs(provider: Provider): number {
  const parsed = provider.stream_idle_timeout
    ? durationMilliseconds(provider.stream_idle_timeout)
    : undefined;
  if (parsed === undefined && provider.stream_idle_timeout)
    throw new Error("Provider stream_idle_timeout is invalid");
  return (
    parsed ?? provider.stream_idle_timeout_ms ?? defaultStreamIdleTimeoutMs
  );
}

const checkOllamaOrigin = (value: string, ctx: z.RefinementCtx) => {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    ctx.addIssue({
      code: "custom",
      path: ["base_url"],
      message: "Use a valid HTTP(S) Ollama origin",
    });
    return;
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== "/"
  )
    ctx.addIssue({
      code: "custom",
      path: ["base_url"],
      message:
        "Use an HTTP(S) Ollama origin without credentials, query or path",
    });
};

/** OpenAI-compatible origins may carry a path such as `/v1`; no trailing slash. */
const checkOpenAIBaseURL = (value: string, ctx: z.RefinementCtx) => {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    ctx.addIssue({
      code: "custom",
      path: ["base_url"],
      message: "Use a valid HTTP(S) OpenAI-compatible base URL",
    });
    return;
  }
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password
  )
    ctx.addIssue({
      code: "custom",
      path: ["base_url"],
      message:
        "Use an HTTP(S) OpenAI-compatible base URL without embedded credentials",
    });
  if (url.search || url.hash || url.pathname.endsWith("/"))
    ctx.addIssue({
      code: "custom",
      path: ["base_url"],
      message: "Trim the trailing slash; drop query strings and fragments",
    });
};

/** Reused to normalize OLLAMA_HOST below. */
const endpoint = z.string().superRefine(checkOllamaOrigin);

export const ProviderSchema = z
  .object({
    type: z.enum(["ollama", "ollama_remote", "openai_local"]),
    base_url: z.string(),
    model: z.string().trim().min(1).max(200),
    context_size: z.number().int().positive().max(2_097_152).default(16_384),
    /** The user set context_size (--context, LOCAL_AGENT_CONTEXT): no plan replaces it. */
    context_explicit: z.boolean().optional(),
    /** Why a planned context_size is what it is (see planContext). */
    context_plan: z.string().optional(),
    /** Set when an explicit context_size's KV cache does not fit in memory. */
    context_warning: z.string().optional(),
    /** Sent to Ollama as `think` on chat only when set; unset keeps the model's default. */
    think: z.boolean().optional(),
    max_model_bytes: z
      .number()
      .int()
      .positive()
      .max(Number.MAX_SAFE_INTEGER)
      .optional(),
    stream_idle_timeout: z.string().trim().optional(),
    /** Parsed milliseconds for the root chat client's request watchdog. */
    stream_idle_timeout_ms: z.number().int().positive().optional(),
    /** Some local servers require a dummy bearer token; optional for loopback servers. */
    api_key: z.string().trim().min(1).max(4096).optional(),
  })
  .superRefine((profile, ctx) => {
    if (profile.type === "openai_local")
      checkOpenAIBaseURL(profile.base_url, ctx);
    else checkOllamaOrigin(profile.base_url, ctx);
    const timeout = profile.stream_idle_timeout
      ? durationMilliseconds(profile.stream_idle_timeout)
      : undefined;
    if (profile.stream_idle_timeout && timeout === undefined)
      ctx.addIssue({
        code: "custom",
        path: ["stream_idle_timeout"],
        message: "Use a positive Go duration such as 10m or 250ms",
      });
    if (
      profile.stream_idle_timeout_ms !== undefined &&
      profile.stream_idle_timeout &&
      profile.stream_idle_timeout_ms !== timeout
    )
      ctx.addIssue({
        code: "custom",
        path: ["stream_idle_timeout_ms"],
        message: "Must match stream_idle_timeout",
      });
    if (profile.type === "ollama") {
      let hostname: string;
      try {
        hostname = new URL(profile.base_url).hostname;
      } catch {
        return;
      }
      if (!["127.0.0.1", "localhost", "[::1]"].includes(hostname))
        ctx.addIssue({
          code: "custom",
          message:
            "Local Ollama requires a loopback endpoint; use an explicit Chalupa profile for remote inference",
        });
    }
    if (profile.type === "ollama_remote") {
      if (profile.max_model_bytes === undefined)
        ctx.addIssue({
          code: "custom",
          path: ["max_model_bytes"],
          message: "Chalupa must negotiate a positive max_model_bytes",
        });
      if (profile.stream_idle_timeout === undefined)
        ctx.addIssue({
          code: "custom",
          path: ["stream_idle_timeout"],
          message: "Chalupa must negotiate a positive stream_idle_timeout",
        });
    } else if (
      profile.max_model_bytes !== undefined ||
      profile.stream_idle_timeout !== undefined ||
      (profile.stream_idle_timeout_ms !== undefined &&
        profile.type !== "ollama" &&
        profile.type !== "openai_local")
    )
      ctx.addIssue({
        code: "custom",
        message: "Negotiated remote limits require an ollama_remote profile",
      });
    if (profile.type !== "openai_local" && profile.api_key !== undefined)
      ctx.addIssue({
        code: "custom",
        path: ["api_key"],
        message: "api_key requires an openai_local profile",
      });
  });
export type Provider = z.infer<typeof ProviderSchema>;
export type OpenAILocalProvider = Provider & { type: "openai_local" };

/** Raw `--context` / `--think` values; the environment variables are the fallback. */
export type ProviderOptions = { context?: string; think?: string };

/** `32768`, `32k` or `32K` (k = 1024 tokens). */
export function parseContextSize(value: string, source = "--context"): number {
  const match = /^(\d+)([kK]?)$/u.exec(value.trim());
  const size = match ? Number(match[1]) * (match[2] ? 1024 : 1) : Number.NaN;
  if (!Number.isSafeInteger(size) || size < 512 || size > 2_097_152)
    throw new Error(
      `${source} must be a token count between 512 and 2097152, such as 32768 or 32k`,
    );
  return size;
}

/** `on|off`, `1|0` or `true|false`. */
export function parseThink(value: string, source = "--think"): boolean {
  switch (value.trim().toLowerCase()) {
    case "on":
    case "1":
    case "true":
      return true;
    case "off":
    case "0":
    case "false":
      return false;
    default:
      throw new Error(`${source} must be on or off`);
  }
}

const present = (value: string | undefined) =>
  value === undefined || value.trim() === "" ? undefined : value;

/** Chalupa owns provisioning, model placement and the tunnel; we consume its process profile. */
/** A provider profile from the environment, or one line saying what is wrong with it. */
function parseProfile(raw: unknown, label: string): Provider {
  const parsed = ProviderSchema.safeParse(raw);
  if (parsed.success) return parsed.data;
  const issues = parsed.error.issues
    .slice(0, 3)
    .map((issue) => `${issue.path.join(".") || "profile"}: ${issue.message}`);
  throw new Error(
    `${label} provider config (LOCAL_AGENT_PROVIDER_CONFIG) is invalid: ${issues.join("; ")}`,
  );
}

export function resolveProvider(
  env: Record<string, string | undefined>,
  model?: string,
  options: ProviderOptions = {},
): Provider {
  const thinkSetting = present(options.think);
  const envThink = present(env.LOCAL_AGENT_THINK);
  const think =
    thinkSetting !== undefined
      ? parseThink(thinkSetting)
      : envThink !== undefined
        ? parseThink(envThink, "LOCAL_AGENT_THINK")
        : undefined;
  if (env.LOCAL_AGENT_PROVIDER === "chalupa") {
    if (env.LOCAL_AGENT_LOCAL_ONLY !== "false")
      throw new Error("Chalupa requires explicit LOCAL_AGENT_LOCAL_ONLY=false");
    let rawProfile: unknown;
    try {
      rawProfile = JSON.parse(env.LOCAL_AGENT_PROVIDER_CONFIG ?? "{}");
    } catch {
      throw new Error("Chalupa provider config must be valid JSON");
    }
    const profile = parseProfile(rawProfile, "Chalupa");
    if (profile.type !== "ollama_remote")
      throw new Error("Chalupa requires an ollama_remote profile");
    if (model && model !== profile.model)
      throw new Error("Select the model in Chalupa before launching");
    // The negotiated context belongs to the Chalupa profile; the variable is ignored.
    if (present(options.context) !== undefined)
      throw new Error(
        "Chalupa negotiates the context size; pass --context to chalupa exec",
      );
    return {
      ...profile,
      ...(think === undefined ? {} : { think }),
      stream_idle_timeout_ms: providerStreamIdleTimeoutMs(profile),
    };
  }
  if (env.LOCAL_AGENT_PROVIDER === "openai_local") {
    let rawProfile: unknown;
    try {
      rawProfile = JSON.parse(env.LOCAL_AGENT_PROVIDER_CONFIG ?? "{}");
    } catch {
      throw new Error("openai_local provider config must be valid JSON");
    }
    // The documented config key is `baseURL`; profiles normalize to base_url.
    if (
      typeof rawProfile === "object" &&
      rawProfile !== null &&
      "baseURL" in rawProfile &&
      !("base_url" in rawProfile)
    ) {
      const { baseURL, ...rest } = rawProfile as {
        baseURL: string;
        [key: string]: unknown;
      };
      rawProfile = { ...rest, base_url: baseURL };
    }
    const profile = parseProfile(rawProfile, "openai_local");
    if (profile.type !== "openai_local")
      throw new Error("openai_local requires an openai_local profile");
    // Same boundary the MCP transport enforces: loopback unless explicitly widened.
    if (env.LOCAL_AGENT_LOCAL_ONLY !== "false") {
      const hostname = new URL(profile.base_url).hostname;
      if (!["127.0.0.1", "localhost", "[::1]"].includes(hostname))
        throw new Error(
          "openai_local requires a loopback base URL; set LOCAL_AGENT_LOCAL_ONLY=false to target a remote OpenAI-compatible server",
        );
    }
    if (model && model !== profile.model)
      throw new Error("Select the model in LOCAL_AGENT_PROVIDER_CONFIG");
    const contextSetting = present(options.context);
    const envContext = present(env.LOCAL_AGENT_CONTEXT);
    const context =
      contextSetting !== undefined
        ? parseContextSize(contextSetting)
        : envContext !== undefined
          ? parseContextSize(envContext, "LOCAL_AGENT_CONTEXT")
          : undefined;
    return {
      ...profile,
      ...(context === undefined
        ? {}
        : { context_size: context, context_explicit: true }),
      ...(think === undefined ? {} : { think }),
      stream_idle_timeout_ms: defaultStreamIdleTimeoutMs,
    };
  }
  if (env.LOCAL_AGENT_PROVIDER && env.LOCAL_AGENT_PROVIDER !== "ollama")
    throw new Error(
      "This Bun prototype supports Ollama, Chalupa and openai_local only",
    );
  if (env.LOCAL_AGENT_PROVIDER_CONFIG)
    throw new Error(
      "Provider config requires LOCAL_AGENT_PROVIDER=chalupa or openai_local",
    );
  const host = env.OLLAMA_HOST?.trim() || "127.0.0.1:11434";
  const hasScheme = host.includes("://");
  const address = isIP(host) === 6 ? `[${host}]` : host;
  const url = new URL(
    endpoint.parse(hasScheme ? address : `http://${address}`),
  );
  // OLLAMA_HOST also configures the server's bind address. Clients must dial
  // loopback for unspecified addresses, using Ollama's schemeless default port.
  if (!hasScheme && !/:\d+$/.test(address.split("/")[0] ?? ""))
    url.port = "11434";
  if (url.hostname === "0.0.0.0") url.hostname = "127.0.0.1";
  if (url.hostname === "[::]") url.hostname = "[::1]";
  const contextSetting = present(options.context);
  const envContext = present(env.LOCAL_AGENT_CONTEXT);
  const context =
    contextSetting !== undefined
      ? parseContextSize(contextSetting)
      : envContext !== undefined
        ? parseContextSize(envContext, "LOCAL_AGENT_CONTEXT")
        : undefined;
  return {
    ...ProviderSchema.parse({
      type: "ollama",
      base_url: url.toString().replace(/\/$/, ""),
      model: model || present(env.LOCAL_AGENT_MODEL) || DEFAULT_OLLAMA_MODEL,
      ...(context === undefined
        ? {}
        : { context_size: context, context_explicit: true }),
      ...(think === undefined ? {} : { think }),
    }),
    stream_idle_timeout_ms: defaultStreamIdleTimeoutMs,
  };
}
