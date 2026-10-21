// Entry of the opt-in Tuimark client (`--ui tuimark`), fullscreen only.
//
// This module is imported dynamically by main.ts, after every flag is checked,
// so the headless and Vue paths never load it. It must not import the Vue
// client: `tuimark host` owns the terminal (fds 0, 1 and 2) while the UI runs,
// and a second renderer would fight it.

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type {
  McpConnection,
  Provider,
  SessionContext,
  Workspace,
} from "@local-agent/runtime";
import { spawnHost } from "@thelacanians/tuimark";
import type { Intent } from "#cli/tuimark/commands";
import { TuimarkClient } from "#cli/tuimark/controller";

/**
 * The folder with app.tui, next to this file. The bundle (`bun run build`)
 * leaves `#cli/*` imports unresolved, so this module always runs from source and
 * the module's own folder is this one in both cases.
 */
export function uiDirectory(
  from = dirname(fileURLToPath(import.meta.url)),
): string {
  const directory = join(from, "ui");
  if (!existsSync(join(directory, "app.tui")))
    throw new Error(`Tuimark UI files not found in ${directory}`);
  return directory;
}

export async function runTuimark(
  provider: Provider,
  signal: AbortSignal,
  workspace: Workspace,
  intent: Intent,
  session?: SessionContext,
  mcp: readonly McpConnection[] = [],
): Promise<void> {
  signal.throwIfAborted();
  const client = new TuimarkClient({
    provider,
    workspace,
    intent,
    session,
    mcp,
    signal,
  });
  const ui = await spawnHost(join(uiDirectory(), "app.tui"), {
    data: client.initialStore() as unknown as Record<string, unknown>,
    replyTimeout: "5s",
  });
  client.attach(ui);
  const stop = () => {
    client.abort();
    void ui.close();
  };
  signal.addEventListener("abort", stop, { once: true });
  try {
    if (signal.aborted) stop();
    const exit = await ui.exited;
    // The session lease and the store are released by the turn's own cleanup.
    client.abort();
    await client.idle();
    if (exit.reason === "error")
      throw new Error(exit.error ?? "The Tuimark host ended with an error");
  } finally {
    signal.removeEventListener("abort", stop);
    client.abort();
    await client.idle();
  }
}
