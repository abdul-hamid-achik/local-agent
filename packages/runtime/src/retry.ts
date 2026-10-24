const maxProviderRetries = 2;
const maxRetryWaitMs = 10_000;
/** Answers that say "not now" before any reply: the server did nothing with the request. */
const retryableStatuses = new Set([429, 502, 503, 504]);

/** A connection that was refused, reset or cut before the reply ended. */
export function tunnelLost(error: unknown): boolean {
  const { code, message } = (error ?? {}) as {
    code?: unknown;
    message?: unknown;
  };
  return (
    /^(ConnectionRefused|ConnectionClosed|ECONNREFUSED|ECONNRESET|EPIPE)$/.test(
      String(code ?? ""),
    ) ||
    /Unable to connect|socket connection was closed|Did not receive done|stream ended before completion/.test(
      String(message ?? ""),
    )
  );
}

/**
 * The wait before retry `attempt` (0-based): the server's Retry-After, else a
 * short backoff. Undefined when the server asks for longer than we wait:
 * retrying early would only be refused again, so the answer stands.
 */
export function providerRetryDelayMs(
  response: Response | undefined,
  attempt: number,
): number | undefined {
  const header = response?.headers.get("retry-after")?.trim();
  if (header) {
    const ms = /^\d+$/.test(header)
      ? Number(header) * 1_000
      : Date.parse(header) - Date.now();
    if (Number.isFinite(ms))
      return ms > maxRetryWaitMs ? undefined : Math.max(ms, 0);
  }
  return 250 * 2 ** attempt;
}

/** A pause that the request's own signal can end. */
const pause = (ms: number, signal: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal.addEventListener("abort", onAbort, { once: true });
  });

/** A body that can be sent again; a stream cannot, so its request is never
 *  retried. Anything else (a Blob, FormData, a stream) counts as a stream. */
const replayable = (body: BodyInit | null | undefined) =>
  body == null ||
  typeof body === "string" ||
  body instanceof URLSearchParams ||
  body instanceof ArrayBuffer ||
  ArrayBuffer.isView(body);

/**
 * Sends a request again, up to twice, when the connection is refused or reset
 * and on 429/502/503/504: both come before any byte of the reply, and the
 * requests made here (model chat, metadata reads) have no side effects, so a
 * repeat cannot double anything. Once a response is returned its body is the
 * caller's and is never replayed. `send` must build each attempt afresh;
 * `body` is the request's body, which must be replayable for a retry.
 * Waits end with `caller`.
 */
export async function withRetries(
  send: () => Promise<Response>,
  caller: AbortSignal,
  body?: BodyInit | null,
): Promise<Response> {
  const retries = replayable(body) ? maxProviderRetries : 0;
  for (let attempt = 0; ; attempt++) {
    let response: Response;
    try {
      response = await send();
    } catch (error) {
      // Only a refused or reset connection; a timeout or a cancel is final
      // (their errors are not tunnelLost, and pause throws once cancelled).
      if (attempt < retries && tunnelLost(error)) {
        await pause(providerRetryDelayMs(undefined, attempt) ?? 0, caller);
        continue;
      }
      throw error;
    }
    const delay = providerRetryDelayMs(response, attempt);
    if (
      attempt < retries &&
      retryableStatuses.has(response.status) &&
      delay !== undefined
    ) {
      await response.body?.cancel().catch(() => undefined);
      await pause(delay, caller);
      continue;
    }
    return response;
  }
}
