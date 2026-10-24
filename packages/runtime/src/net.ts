// The fetch tool's destination policy: what a model-chosen URL (and every
// redirect it leads to) may reach. Only http(s) to public addresses passes by
// default; loopback, private, link-local, metadata and the IPv6 forms that
// embed them are refused unless LOCAL_AGENT_FETCH_PRIVATE=1. Bun's fetch also
// reads file:// URLs, so the scheme is checked on every hop, not only once.

import { lookup } from "node:dns/promises";
import { isIP } from "node:net";

/** Every address a host name resolves to (A and AAAA). */
export type HostResolver = (hostname: string) => Promise<string[]>;

export type FetchPolicy = {
  /** Reach loopback and private networks too (a local dev server, the LAN). */
  allowPrivate: boolean;
  resolve: HostResolver;
  /** The user's deny rule for a host, if one refuses it (checked every hop). */
  deniedHost?: (hostname: string) => Promise<string | undefined>;
};

const systemResolver: HostResolver = async (hostname) =>
  (await lookup(hostname, { all: true, verbatim: true })).map(
    (entry) => entry.address,
  );

/** The policy for one run: environment opt-in, system DNS unless injected. */
export function fetchPolicy(
  override: Partial<FetchPolicy> = {},
  env: Record<string, string | undefined> = process.env,
): FetchPolicy {
  return {
    allowPrivate:
      override.allowPrivate ?? env.LOCAL_AGENT_FETCH_PRIVATE === "1",
    resolve: override.resolve ?? systemResolver,
    ...(override.deniedHost ? { deniedHost: override.deniedHost } : {}),
  };
}

function v4Octets(ip: string): number[] | undefined {
  const parts = ip.split(".");
  if (parts.length !== 4) return undefined;
  const octets = parts.map((part) =>
    /^\d{1,3}$/.test(part) ? Number(part) : NaN,
  );
  return octets.every((octet) => octet >= 0 && octet <= 255)
    ? octets
    : undefined;
}

/** IPv4: everything but the IANA special-purpose blocks that are not
 * globally reachable. */
function v4Public(octets: readonly number[]): boolean {
  const [a = 0, b = 0, c = 0] = octets;
  const special =
    a === 0 || // "this network"
    a === 10 ||
    a === 127 ||
    (a === 100 && b >= 64 && b <= 127) || // carrier-grade NAT
    (a === 169 && b === 254) || // link-local, cloud metadata
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 192 && b === 0 && (c === 0 || c === 2)) || // IETF, TEST-NET-1
    (a === 192 && b === 88 && c === 99) || // retired 6to4 relay anycast
    (a === 198 && (b === 18 || b === 19)) || // benchmarking
    (a === 198 && b === 51 && c === 100) || // TEST-NET-2
    (a === 203 && b === 0 && c === 113) || // TEST-NET-3
    a >= 224; // multicast, reserved, broadcast
  return !special;
}

/** The eight 16-bit groups of an IPv6 literal (a dotted IPv4 tail allowed). */
function v6Groups(ip: string): number[] | undefined {
  let text = ip.toLowerCase();
  const zone = text.indexOf("%");
  if (zone >= 0) text = text.slice(0, zone);
  if (text.includes(".")) {
    const at = text.lastIndexOf(":");
    const octets = v4Octets(text.slice(at + 1));
    if (!octets) return undefined;
    const [a = 0, b = 0, c = 0, d = 0] = octets;
    text = `${text.slice(0, at + 1)}${((a << 8) | b).toString(16)}:${((c << 8) | d).toString(16)}`;
  }
  const halves = text.split("::");
  if (halves.length > 2) return undefined;
  const head = halves[0] ? halves[0].split(":") : [];
  const tail = halves.length === 2 && halves[1] ? halves[1].split(":") : [];
  const fill = 8 - head.length - tail.length;
  if (halves.length === 1 ? fill !== 0 : fill < 1) return undefined;
  const groups = [
    ...head,
    ...Array<string>(halves.length === 2 ? fill : 0).fill("0"),
    ...tail,
  ];
  const numbers = groups.map((group) =>
    /^[0-9a-f]{1,4}$/.test(group) ? Number.parseInt(group, 16) : NaN,
  );
  return numbers.length === 8 && numbers.every((n) => !Number.isNaN(n))
    ? numbers
    : undefined;
}

function embeddedV4(high: number, low: number): number[] {
  return [high >> 8, high & 0xff, low >> 8, low & 0xff];
}

/** IPv6: forms that carry an IPv4 address are judged by that address;
 * otherwise only global unicast (2000::/3) outside its special-purpose
 * blocks is public, so an unlisted range fails closed. */
function v6Public(groups: readonly number[]): boolean {
  const [g0 = 0, g1 = 0, g2 = 0, g3 = 0, g4 = 0, g5 = 0, g6 = 0, g7 = 0] =
    groups;
  const zeroTo = (n: number) =>
    groups.slice(0, n).every((group) => group === 0);
  // ::ffff:a.b.c.d (mapped) and ::ffff:0:a.b.c.d (SIIT translated).
  if (zeroTo(5) && g5 === 0xffff) return v4Public(embeddedV4(g6, g7));
  if (zeroTo(4) && g4 === 0xffff && g5 === 0)
    return v4Public(embeddedV4(g6, g7));
  if (g0 === 0x64 && g1 === 0xff9b) {
    // NAT64: the well-known prefix carries an IPv4 address; local-use is private.
    return (
      g2 === 0 &&
      g3 === 0 &&
      g4 === 0 &&
      g5 === 0 &&
      v4Public(embeddedV4(g6, g7))
    );
  }
  if ((g0 & 0xe000) !== 0x2000) return false; // ::, ::1, ULA, link-local, multicast, discard, …
  if (g0 === 0x2002) return v4Public(embeddedV4(g1, g2)); // 6to4
  if (g0 === 0x2001) {
    if (g1 === 0) return false; // Teredo tunnels to anywhere
    if (g1 === 0x0db8) return false; // documentation
    if (g1 >= 0x10 && g1 <= 0x2f) return false; // ORCHID, ORCHIDv2
  }
  if (g0 === 0x3fff && g1 <= 0x0fff) return false; // documentation (3fff::/20)
  return true;
}

/** Whether an IP literal is a public unicast address. Unparseable is not. */
export function isPublicAddress(ip: string): boolean {
  const family = isIP(ip);
  if (family === 4) {
    const octets = v4Octets(ip);
    return octets !== undefined && v4Public(octets);
  }
  if (family === 6) {
    const groups = v6Groups(ip);
    return groups !== undefined && v6Public(groups);
  }
  return false;
}

/**
 * Refuses a fetch destination outside the policy: any scheme but http(s),
 * and (unless private networks are allowed) a host that is, or resolves to,
 * a non-public address. Every resolved address must be public: one private
 * answer is enough to refuse. A host that re-resolves differently between
 * this check and the connection (DNS rebinding) is not covered.
 */
export async function assertFetchTarget(
  url: URL,
  policy: FetchPolicy,
  signal?: AbortSignal,
): Promise<void> {
  if (url.protocol !== "http:" && url.protocol !== "https:")
    throw new Error(`fetch: unsupported protocol ${url.protocol}`);
  // `docs.example.` is the same host as `docs.example`: rules and the
  // localhost check see the name without its root dot (the URL parser has
  // already lowercased it).
  const host = url.hostname.replace(/^\[|\]$/g, "").replace(/\.+$/, "");
  const rule = await policy.deniedHost?.(host);
  if (rule !== undefined)
    throw new Error(`fetch: ${host} is denied by rule: ${rule}`);
  if (policy.allowPrivate) return;
  const refuse = (address: string) =>
    new Error(
      `fetch: ${url.host} is a private or loopback address (${address}); fetch only reaches public hosts unless LOCAL_AGENT_FETCH_PRIVATE=1`,
    );
  if (host === "localhost" || host.endsWith(".localhost")) throw refuse(host);
  if (isIP(host)) {
    if (!isPublicAddress(host)) throw refuse(host);
    return;
  }
  let addresses: string[];
  try {
    // The lookup counts against the caller's deadline: a stalled resolver
    // must not outlive an abort.
    addresses = signal
      ? await Promise.race([
          policy.resolve(host),
          new Promise<never>((_, reject) => {
            signal.throwIfAborted();
            signal.addEventListener("abort", () => reject(signal.reason), {
              once: true,
            });
          }),
        ])
      : await policy.resolve(host);
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error(
      `fetch: cannot resolve ${host} (${error instanceof Error ? error.message : String(error)})`,
    );
  }
  if (addresses.length === 0) throw new Error(`fetch: cannot resolve ${host}`);
  const blocked = addresses.find((address) => !isPublicAddress(address));
  if (blocked !== undefined) throw refuse(blocked);
}
