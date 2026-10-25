/**
 * The workbench window shows the app and nothing else: its preload bridge
 * drives the sidecar (runs, approvals, files), so no other page may load
 * in it. A web link opens in the system browser instead.
 */

/** A link the system browser may open: http or https, nothing else. */
export function externalUrl(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:"
      ? parsed.href
      : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Whether a navigation from `current` to `url` stays on the app's own page:
 * the same origin for the dev server, the same file once packaged (every
 * file: URL shares one opaque origin, so the path decides).
 */
export function staysInApp(url: string, current: string): boolean {
  try {
    const next = new URL(url);
    const here = new URL(current);
    if (next.protocol !== here.protocol) return false;
    if (next.protocol === "file:") return next.pathname === here.pathname;
    return (
      (next.protocol === "http:" || next.protocol === "https:") &&
      next.origin === here.origin
    );
  } catch {
    return false;
  }
}
