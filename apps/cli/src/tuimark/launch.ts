// Entry of the Tuimark client (`--ui tuimark`, the default on macOS and Linux),
// fullscreen, or inline when asked (`--ui tuimark --mode inline`).
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
import { type Ready, resolveBinary, spawnHost } from "@thelacanians/tuimark";
import type { Intent } from "#cli/tuimark/commands";
import {
  type ClientOptions,
  type Presentation,
  TuimarkClient,
} from "#cli/tuimark/controller";

/**
 * The oldest tuimark this client's document runs on: <textarea>, `runs`, and the
 * append write of the stream (0.4.6; props and <slot/> came with 0.4.5).
 */
export const MIN_TUIMARK = "0.4.6";

/**
 * The tuimark release the client downloads when TUIMARK_BINARY is not set,
 * named here so the binary matches the SDK this client was checked against.
 */
export const PINNED_TUIMARK = "0.4.6";

/** The SDK's resolution (TUIMARK_BINARY, the cache, a checked download) for PINNED_TUIMARK. */
export function resolvePinned(): Promise<string> {
  return resolveBinary({ version: PINNED_TUIMARK });
}

/**
 * Leave the alternate screen, show the cursor, turn bracketed paste off and
 * autowrap on. A host that dies leaves the terminal as it was drawing it.
 */
export const TERMINAL_RESET = "\x1b[?1049l\x1b[?25h\x1b[?2004l\x1b[?7h\x1b[0m";

/**
 * The folder with app.tui, next to this file. The bundle (`bun run build`)
 * leaves `#cli/*` imports unresolved, so this module always runs from source and
 * the module's own folder is this one in both cases.
 */
export function uiDirectory(
  from = dirname(fileURLToPath(import.meta.url)),
): string {
  const directory = join(from, "ui");
  if (
    !existsSync(join(directory, "app.tui")) ||
    !existsSync(join(directory, "inline.tui"))
  )
    throw new Error(`Tuimark UI files not found in ${directory}`);
  return directory;
}

/** True when `version` ("0.4.6", "0.4.5-dev", "v1.0.0") is at least `minimum`. */
export function versionAtLeast(version: string, minimum: string): boolean {
  const parse = (text: string) =>
    (/^v?(\d+)\.(\d+)\.(\d+)/.exec(text) ?? []).slice(1).map(Number);
  const have = parse(version);
  const need = parse(minimum);
  if (have.length < 3) return false;
  for (let i = 0; i < 3; i++) {
    if ((have[i] ?? 0) !== (need[i] ?? 0))
      return (have[i] ?? 0) > (need[i] ?? 0);
  }
  return true;
}

/**
 * Why a tuimark that said `ready` cannot run this client, or undefined. An older
 * binary loads the document with errors (no props, no slot) and shows an
 * approval dialog without its diff, so it is refused before anything is drawn.
 */
export function readyProblem(
  ready: Pick<Ready, "version" | "diagnostics"> &
    Partial<Pick<Ready, "messages">>,
  bin: string,
  presentation: Presentation = "fullscreen",
): string | undefined {
  const errors = ready.diagnostics.filter(
    (d) => (d as { severity?: unknown }).severity === "error",
  );
  const tooOld = !versionAtLeast(ready.version, MIN_TUIMARK);
  // Inline printing needs the child to accept `commit`; one that predates it
  // would answer every commit as an unknown message.
  const noCommit =
    presentation === "inline" && !ready.messages?.includes("commit");
  if (!tooOld && errors.length === 0 && !noCommit) return undefined;
  const doc = presentation === "inline" ? "inline.tui" : "app.tui";
  const first = errors[0] as { code?: string; msg?: string } | undefined;
  return [
    `The Tuimark client needs tuimark >= ${MIN_TUIMARK}; ${bin} is ${ready.version}`,
    errors.length
      ? ` and loads ${doc} with ${errors.length} error${errors.length === 1 ? "" : "s"}${first ? ` (${first.code}: ${first.msg})` : ""}`
      : "",
    noCommit ? " and does not accept commit (inline mode)" : "",
    ". Set TUIMARK_BINARY to a newer tuimark, or use --ui vue.",
  ].join("");
}

/**
 * The tuimark binary, or why there is none. The default client uses this to fall
 * back to the Vue client with a line on stderr instead of failing a run that
 * never asked for Tuimark (offline with an empty cache, a release not out yet).
 */
export async function findBinary(
  resolve: () => Promise<string> = resolvePinned,
): Promise<{ bin: string } | { error: string }> {
  try {
    return { bin: await resolve() };
  } catch (error) {
    return {
      error: error instanceof Error ? error.message : "no tuimark binary",
    };
  }
}

/** The parts of the process the launcher uses (fakes in tests). */
export interface LaunchDeps {
  /** Replaces the runtime's agent loop (tests). */
  run?: ClientOptions["run"];
  spawn?: typeof spawnHost;
  resolve?: () => Promise<string>;
  stdout?: {
    isTTY?: boolean;
    columns?: number;
    write(chunk: string): unknown;
    on?(event: "resize", listener: () => void): unknown;
    off?(event: "resize", listener: () => void): unknown;
  };
}

export async function runTuimark(
  provider: Provider,
  signal: AbortSignal,
  workspace: Workspace,
  intent: Intent,
  session?: SessionContext,
  mcp: readonly McpConnection[] = [],
  presentation: Presentation = "fullscreen",
  deps: LaunchDeps = {},
): Promise<void> {
  signal.throwIfAborted();
  const stdout = deps.stdout ?? process.stdout;
  const resetTerminal = () => {
    if (stdout.isTTY) stdout.write(TERMINAL_RESET);
  };
  let readyColumns = 80;
  const client = new TuimarkClient({
    provider,
    workspace,
    intent,
    presentation,
    session,
    mcp,
    signal,
    run: deps.run,
    columns: () => stdout.columns ?? readyColumns,
  });
  const bin = await (deps.resolve ?? resolvePinned)();
  const doc = presentation === "inline" ? "inline.tui" : "app.tui";
  const ui = await (deps.spawn ?? spawnHost)(join(uiDirectory(), doc), {
    data: client.initialStore() as unknown as Record<string, unknown>,
    replyTimeout: "5s",
    bin,
    inline: presentation === "inline",
  });
  readyColumns = ui.ready.cols;
  const problem = readyProblem(ui.ready, bin, presentation);
  if (problem) {
    await ui.close();
    resetTerminal();
    throw new Error(problem);
  }
  client.attach(ui);
  const onResize = () => void client.resize();
  stdout.on?.("resize", onResize);
  const stop = () => {
    client.abort();
    void ui.close();
  };
  signal.addEventListener("abort", stop, { once: true });
  try {
    if (signal.aborted) stop();
    let failure: Error | undefined;
    try {
      const exit = await ui.exited;
      if (exit.reason === "error")
        failure = new Error(
          exit.error ?? "The Tuimark host ended with an error",
        );
    } catch (error) {
      failure = error instanceof Error ? error : new Error(String(error));
    }
    // Wait for the turn first: runAgent releases the session lease, and main.ts
    // closes the session store right after this returns.
    client.abort();
    await client.idle();
    if (failure) {
      resetTerminal();
      throw failure;
    }
  } finally {
    signal.removeEventListener("abort", stop);
    stdout.off?.("resize", onResize);
    client.abort();
    await client.idle();
  }
}
