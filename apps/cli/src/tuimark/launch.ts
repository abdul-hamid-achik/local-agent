// Entry of the Tuimark client (`--ui tuimark`, the default on macOS and Linux),
// fullscreen, or inline when asked (`--ui tuimark --mode inline`).
//
// This module is imported dynamically by main.ts, after every flag is checked,
// so the headless and Vue paths never load it. It must not import the Vue
// client: `tuimark host` owns the terminal (fds 0, 1 and 2) while the UI runs,
// and a second renderer would fight it.

import { existsSync, realpathSync } from "node:fs";
import { rm, rmdir } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  type McpConnection,
  type Provider,
  reconcileExecution,
  recoveryItems,
  type SessionContext,
  type SessionStore,
  sessionProblem,
  settleSession,
  type Workspace,
} from "@local-agent/runtime";
import {
  type Host,
  type Ready,
  type ResolveOptions,
  resolveBinary,
  spawnHost,
} from "@thelacanians/tuimark";
import { settledNow } from "#cli/model-check";
import { terminalText } from "#cli/text";
import { copyToClipboard } from "#cli/tuimark/clipboard";
import type { Intent } from "#cli/tuimark/commands";
import {
  type ClientOptions,
  type FindHit,
  type Presentation,
  type SessionsCapability,
  TuimarkClient,
} from "#cli/tuimark/controller";
import { reducedMotion } from "#cli/tuimark/store";
import {
  resolveTheme,
  THEME_FOLDER_PREFIX,
  themedDocument,
} from "#cli/tuimark/themes";

/**
 * The oldest tuimark this client's document runs on: links and images whose
 * `href` and `src` are data-bound (0.5.1), on top of <textarea>, `runs` and
 * the stream's append write (0.4.6) and props and <slot/> (0.4.5).
 */
export const MIN_TUIMARK = "0.5.1";

/**
 * The tuimark release the client downloads when TUIMARK_BINARY is not set,
 * named here so the binary matches the SDK this client was checked against.
 * 0.5.5 changes no language: its kit palettes read at WCAG AA, on top of
 * 0.5.4's photo mode (an `<img>` is transmitted only when a frame paints one
 * of its cells), 0.5.3's refusal to open a FIFO, socket or device at a path a
 * document names and 0.5.2's fixes to data-bound links and kitty images.
 */
export const PINNED_TUIMARK = "0.5.5";

/**
 * The SDK's resolution (TUIMARK_BINARY, the cache, a checked download) for
 * PINNED_TUIMARK. A download is slow and the screen is not open yet, so it
 * says so once on stderr (the SDK's `log` runs only before a download, and
 * again before a re-fetch of a cached copy that failed its check); a binary
 * that is there costs no output.
 */
export function resolvePinned(
  options: ResolveOptions = {},
  write: (text: string) => unknown = (text) => process.stderr.write(text),
): Promise<string> {
  let said = false;
  return resolveBinary({
    version: PINNED_TUIMARK,
    log: () => {
      if (said) return;
      said = true;
      write(`Downloading Tuimark ${PINNED_TUIMARK}…\n`);
    },
    ...options,
  });
}

/**
 * Leave the alternate screen, show the cursor, turn bracketed paste off and
 * autowrap on. A host that dies leaves the terminal as it was drawing it.
 */
export const TERMINAL_RESET = "\x1b[?1049l\x1b[?25h\x1b[?2004l\x1b[?7h\x1b[0m";
/**
 * Inline never entered the alternate screen, and leaving it restores the
 * cursor saved on entry: the printed transcript would be written over.
 */
export const INLINE_TERMINAL_RESET = "\x1b[?25h\x1b[?2004l\x1b[?7h\x1b[0m";

/**
 * The folder with app.tui. From source it is next to this file (the bundle,
 * `bun run build`, leaves `#cli/*` imports unresolved, so this module runs
 * from source there too). A compiled release carries its modules inside the
 * executable, where the tuimark host, another process, cannot read them, so
 * the release puts ui/ next to the executable; that is found through the
 * symlink Homebrew or the installer puts on PATH.
 */
export function uiDirectory(
  from = dirname(fileURLToPath(import.meta.url)),
  executable = process.execPath,
): string {
  const real = (path: string) => {
    try {
      return realpathSync(path);
    } catch {
      return path;
    }
  };
  const candidates = [join(from, "ui"), join(dirname(real(executable)), "ui")];
  for (const directory of candidates)
    if (
      existsSync(join(directory, "app.tui")) &&
      existsSync(join(directory, "inline.tui"))
    )
      return directory;
  throw new Error(`Tuimark UI files not found in ${candidates.join(" or ")}`);
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
 * back to the Vue client with fallbackNotice instead of failing a run that never
 * asked for Tuimark (offline with an empty cache, a release not out yet, a
 * TUIMARK_BINARY older than MIN_TUIMARK). The pinned release is checked by its
 * hash; a binary TUIMARK_BINARY names is asked its version here, since
 * readyProblem only sees it once the client has started. One that is new
 * enough by its version but still loads the document with errors is left to
 * readyProblem, which stops the run.
 */
export async function findBinary(
  resolve: () => Promise<string> = resolvePinned,
  override: string | undefined = process.env.TUIMARK_BINARY || undefined,
  version: (bin: string) => Promise<string | undefined> = binaryVersion,
): Promise<{ bin: string } | { error: string }> {
  let bin: string;
  try {
    bin = await resolve();
  } catch (error) {
    return {
      error: error instanceof Error ? error.message : "no tuimark binary",
    };
  }
  if (bin !== override) return { bin };
  const have = await version(bin);
  if (have !== undefined && versionAtLeast(have, MIN_TUIMARK)) return { bin };
  return {
    error: `TUIMARK_BINARY is ${bin}, ${have === undefined ? "which names no tuimark version" : `which is tuimark ${have}`}`,
  };
}

/** The stderr line of the default client's fall back to the Vue client. */
export function fallbackNotice(error: string): string {
  return `No usable tuimark binary (${terminalText(error)}); using the Vue client. Set TUIMARK_BINARY to a tuimark >= ${MIN_TUIMARK}, or pass --ui vue to silence this.\n`;
}

/**
 * The version `bin --version` names ("tuimark 0.5.1 (commit …)" is
 * "0.5.1"), or undefined when it names none or fails. Only the head of its
 * output is read, until `timeoutMs` at most; then the binary is killed if it
 * still runs, so the deadline holds against one that ignores SIGTERM or
 * leaves a child holding its output.
 */
export async function binaryVersion(
  bin: string,
  timeoutMs = 5_000,
): Promise<string | undefined> {
  let child: Bun.Subprocess<"ignore", "pipe", "ignore">;
  try {
    child = Bun.spawn([bin, "--version"], {
      stdin: "ignore",
      stdout: "pipe",
      stderr: "ignore",
    });
  } catch {
    return undefined;
  }
  const reader = child.stdout.getReader();
  // Cancelling ends a pending read, however long its writer stays.
  const timer = setTimeout(
    () => void reader.cancel().catch(() => {}),
    timeoutMs,
  );
  try {
    const decoder = new TextDecoder();
    let out = "";
    while (out.length < 4096) {
      const { done, value } = await reader.read();
      if (done) break;
      out += decoder.decode(value, { stream: true });
    }
    return /^tuimark (v?\d+\.\d+\.\d+\S*)/.exec(out)?.[1];
  } catch {
    return undefined;
  } finally {
    clearTimeout(timer);
    if (child.exitCode === null) child.kill("SIGKILL");
  }
}

/**
 * The controller's `/sessions` capability over an open session store:
 * the same `listSessions` / `loadSession` calls `--sessions` and
 * `--resume` make in main.ts, nothing more.
 */
export function sessionCapability(
  store: SessionStore,
  workspaceRoot: string,
): SessionsCapability {
  return {
    async list() {
      const sessions = await store.listSessionSummaries(workspaceRoot);
      return sessions.map((session) => ({
        handle: session.publicId,
        messages: session.messageCount,
      }));
    },
    async create() {
      return { store, state: await store.createSession(workspaceRoot) };
    },
    async load(handle) {
      // Calls that never ran or only read are closed first; a session whose
      // only problem is an unknown outcome loads, for /recover.
      const { state } = await settleSession(store, handle, workspaceRoot);
      const refusal = store.listHazards(state.id, state.workspaceId).length
        ? undefined
        : sessionProblem(store, state);
      if (refusal) throw new Error(refusal);
      return { store, state };
    },
    recovery: (session) => recoveryItems(session.store, session.state),
    reconcile: (session, executionId, disposition, note) =>
      reconcileExecution(session.store, session.state.publicId, workspaceRoot, {
        executionId,
        disposition,
        note,
      }),
    async search(query) {
      // Bounded by design: the newest 200 sessions, the first 20 that match.
      const sessions = (await store.listSessions(workspaceRoot)).slice(0, 200);
      const needle = query.toLowerCase();
      const hits: FindHit[] = [];
      for (const session of sessions) {
        if (hits.length >= 20) break;
        for (const message of session.messages) {
          const at = message.content.toLowerCase().indexOf(needle);
          if (at < 0) continue;
          hits.push({
            handle: session.publicId,
            snippet: message.content
              .slice(Math.max(0, at - 40), at + 120)
              .replace(/\s+/g, " ")
              .trim(),
          });
          break;
        }
      }
      return hits;
    },
  };
}

/** The parts of the process the launcher uses (fakes in tests). */
export interface LaunchDeps {
  /** Replaces the runtime's agent loop (tests). */
  run?: ClientOptions["run"];
  /** The configured MCP server names, in connection order (the `/mcp` panel). */
  mcpNames?: readonly string[];
  /** Servers still connecting (see ClientOptions.mcpReady). */
  mcpReady?: ClientOptions["mcpReady"];
  /**
   * The model check still running (see ClientOptions.admission). One that
   * refused the model before the screen opens stops the run with its error
   * and no screen; a refusal while the screen is up closes it and stops the
   * run with the same error.
   */
  admission?: ClientOptions["admission"];
  /** `--sandbox-commands`: commands start in the sandbox (ClientOptions.sandbox). */
  sandbox?: boolean;
  /** `--sandbox-loopback`: with `sandbox`, the sandbox opens loopback (ClientOptions.sandboxLoopback). */
  sandboxLoopback?: boolean;
  /** Reads how the model was loaded after its first turn (ClientOptions.placement). */
  placement?: ClientOptions["placement"];
  /** Chalupa's environment name (ClientOptions.environment). */
  environment?: string;
  /** Replaces the system clipboard (tests; ClientOptions.copy). */
  copy?: ClientOptions["copy"];
  /** Replaces `$TERM_PROGRAM` for the native-selection hint (tests). */
  termProgram?: string;
  /** Replaces `$TUIMARK_MOTION` (tests; ClientOptions.reduceMotion). */
  reduceMotion?: boolean;
  /** Replaces the store-built `/sessions` capability (tests). */
  sessions?: SessionsCapability;
  spawn?: typeof spawnHost;
  /** Replaces the generated named-palette document (tests). */
  writeThemeDoc?: (
    uiDirectory: string,
    doc: string,
    name: string,
  ) => Promise<string>;
  resolve?: () => Promise<string>;
  /** Replaces process.stderr for the line said once the host is gone (tests). */
  stderr?: { write(chunk: string): unknown };
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
  /** The `--theme` flag: a builtin (dark/light/auto) or a named palette. */
  theme: string = "auto",
  deps: LaunchDeps = {},
): Promise<void> {
  signal.throwIfAborted();
  // Resolved before anything is built: an unknown --theme name stops the run
  // with the themes that do exist, before a screen is opened.
  const ui = uiDirectory();
  const resolved = resolveTheme(theme, join(ui, "themes"));
  // The host theme passed to spawnHost: a builtin dark/light, or dark for a
  // named palette, so the sheets' `@media (theme: light)` blocks never fire
  // against the palette's own tokens.
  const hostTheme = resolved.kind === "named" ? "dark" : resolved.theme;
  const stdout = deps.stdout ?? process.stdout;
  const resetTerminal = () => {
    if (stdout.isTTY)
      stdout.write(
        presentation === "inline" ? INLINE_TERMINAL_RESET : TERMINAL_RESET,
      );
  };
  let readyColumns = 80;
  const bin = await (deps.resolve ?? resolvePinned)();
  // A download can take a while: ctrl+c during it stops here, not after a
  // screen has opened.
  signal.throwIfAborted();
  // The model check has often answered by now (finding the binary took
  // time): a refusal is then reported before any screen opens, and an answer
  // opens the screen on the provider it planned.
  const answered = deps.admission && (await settledNow(deps.admission));
  if (answered && "error" in answered) throw answered.error;
  const checked = answered && "value" in answered ? answered.value : undefined;
  const client = new TuimarkClient({
    provider: checked ?? provider,
    workspace,
    intent,
    presentation,
    initialTheme: hostTheme === "auto" ? undefined : hostTheme,
    session,
    // `/sessions` exists exactly when the process has a session store
    // (`--session-db`): the session context carries it.
    sessions:
      deps.sessions ??
      (session ? sessionCapability(session.store, workspace.root) : undefined),
    mcp,
    mcpNames: deps.mcpNames,
    ...(deps.mcpReady ? { mcpReady: deps.mcpReady } : {}),
    ...(deps.admission && !checked ? { admission: deps.admission } : {}),
    ...(deps.sandbox ? { sandbox: true } : {}),
    ...(deps.sandbox && deps.sandboxLoopback ? { sandboxLoopback: true } : {}),
    ...(deps.placement ? { placement: deps.placement } : {}),
    ...(deps.environment ? { environment: deps.environment } : {}),
    // The real clipboard tools; Tuimark owns the TTY, so never OSC 52.
    // A tool is never taken from the workspace or the working directory.
    copy:
      deps.copy ??
      ((text: string) => copyToClipboard(text, { exclude: [workspace.root] })),
    termProgram: deps.termProgram ?? process.env.TERM_PROGRAM,
    // The engine reads the same variable once it has a switch for motion.
    reduceMotion: deps.reduceMotion ?? reducedMotion(process.env),
    signal,
    run: deps.run,
    columns: () => stdout.columns ?? readyColumns,
  });
  // A named palette is a generated copy of the document with the palette's
  // .tcss loaded after the last sheet the document itself lists; a builtin
  // theme is the host's own --theme flag.
  const doc = presentation === "inline" ? "inline.tui" : "app.tui";
  let docPath = join(ui, doc);
  if (resolved.kind === "named") {
    docPath = await (deps.writeThemeDoc ?? themedDocument)(
      ui,
      doc,
      resolved.name,
    );
  }
  let host: Host;
  try {
    host = await (deps.spawn ?? spawnHost)(docPath, {
      data: client.initialStore() as unknown as Record<string, unknown>,
      replyTimeout: "5s",
      bin,
      inline: presentation === "inline",
      theme: hostTheme === "auto" ? undefined : hostTheme,
      // A handler that throws would end the session (the SDK replies
      // `error`): the client says what failed and the session goes on.
      onError: (event, error) => client.handlerFailed(event.action, error),
    });
  } catch (error) {
    // A child that died before `ready` may have drawn already.
    resetTerminal();
    throw error;
  } finally {
    // The host has loaded the document (or failed) once spawn resolves. Its
    // private folder goes too, but only that one, and only when empty.
    if (docPath !== join(ui, doc)) {
      await rm(docPath, { force: true });
      const folder = dirname(docPath);
      if (basename(folder).startsWith(THEME_FOLDER_PREFIX))
        await rmdir(folder).catch(() => {});
    }
  }
  readyColumns = host.ready.cols;
  const problem = readyProblem(host.ready, bin, presentation);
  if (problem) {
    await host.close();
    resetTerminal();
    throw new Error(problem);
  }
  client.attach(host);
  // Re-wrapping a preview is cosmetic: if it throws, the status line says so
  // instead of an unhandled rejection ending the process.
  const onResize = () =>
    void client
      .resize()
      .catch((error: unknown) => client.handlerFailed("resize", error));
  stdout.on?.("resize", onResize);
  const stop = () => {
    client.abort();
    void host.close();
  };
  signal.addEventListener("abort", stop, { once: true });
  // The model check, still running with the screen up, refused the model:
  // the screen closes and the run fails with that error, as it did when the
  // check came before the screen.
  let refusal: Error | undefined;
  if (deps.admission && !checked)
    deps.admission.catch((error: unknown) => {
      refusal = error instanceof Error ? error : new Error(String(error));
      stop();
    });
  try {
    if (signal.aborted) stop();
    let failure: Error | undefined;
    try {
      const exit = await host.exited;
      if (exit.reason === "error")
        failure = new Error(
          exit.error ?? "The Tuimark host ended with an error",
        );
    } catch (error) {
      failure = error instanceof Error ? error : new Error(String(error));
    }
    failure = refusal ?? failure;
    // Wait for the turn first: runAgent releases the session lease, and main.ts
    // closes the session store right after this returns.
    client.abort();
    await client.idle();
    if (failure) {
      resetTerminal();
      throw failure;
    }
    if (client.exitNotice)
      (deps.stderr ?? process.stderr).write(`note: ${client.exitNotice}\n`);
  } finally {
    signal.removeEventListener("abort", stop);
    stdout.off?.("resize", onResize);
    client.abort();
    await client.idle();
  }
}
