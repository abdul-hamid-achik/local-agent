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

/** A navigation or a redirect, as the window reports it. */
interface Navigation {
  url: string;
  isMainFrame: boolean;
  preventDefault(): void;
}

/** The part of a window's webContents that `confine` uses. */
export interface ConfinedContents {
  setWindowOpenHandler(
    handler: (details: { url: string }) => { action: "deny" },
  ): void;
  on(
    event: "will-frame-navigate",
    listener: (details: Navigation) => void,
  ): unknown;
  on(event: "will-redirect", listener: (details: Navigation) => void): unknown;
}

/**
 * Keeps the window on `home`, the app's page: a new window is denied, and a
 * navigation of any frame, or a redirect during one, that would leave it is
 * refused. A main frame's web link opens in the system browser instead.
 */
export function confine(
  contents: ConfinedContents,
  home: string,
  openOutside: (url: string) => void,
): void {
  contents.setWindowOpenHandler(({ url }) => {
    openOutside(url);
    return { action: "deny" };
  });
  const guard = (navigation: Navigation) => {
    if (staysInApp(navigation.url, home)) return;
    navigation.preventDefault();
    if (navigation.isMainFrame) openOutside(navigation.url);
  };
  contents.on("will-frame-navigate", guard);
  contents.on("will-redirect", guard);
}
