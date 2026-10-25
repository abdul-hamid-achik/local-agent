import { type ChildProcess, spawn } from "node:child_process";
import { lstat, readlink, realpath, stat } from "node:fs/promises";
import { isAbsolute, relative, resolve, sep } from "node:path";
import {
  type CommandSandbox,
  sandboxedCommand,
  sandboxUnavailable,
} from "#runtime/sandbox";
import { isSecretPath, pathTokens, secretPathsIn } from "#runtime/workspace";

const MAX_OUTPUT_BYTES = 1 << 20;
const MAX_TIMEOUT_MS = 2_147_483_647;
const MAX_ARG_COUNT = 256;
const MAX_ARG_BYTES = 128 * 1024;
const KILL_GRACE_MS = 250;
const DRAIN_GRACE_MS = 500;
const TERMINATION_DEADLINE_MS = KILL_GRACE_MS + DRAIN_GRACE_MS;
const ALLOWED_ENV = [
  "PATH",
  "HOME",
  "TERM",
  "COLORTERM",
  "LANG",
  "LC_ALL",
  "LC_CTYPE",
] as const;

const OPERATOR_HELP =
  'run_command has no shell, so {op} cannot be used. Run one command per call and pass argv as an array, for example {"argv": ["grep", "-rn", "slugify", "src"]}';

/**
 * Splits a command line a model sent as a string into argv, quote-aware
 * (`'…'`, `"…"` and backslash escapes). There is no shell: a line that needs one
 * (`|`, `&`, `;`, `>`, `<`, `$(`, backticks outside quotes, or a second line)
 * is refused, never reinterpreted.
 */
export function splitCommandLine(line: string): string[] {
  const argv: string[] = [];
  let current = "";
  let started = false;
  let quote: "'" | '"' | undefined;
  const refuse = (operator: string): never => {
    throw new Error(OPERATOR_HELP.replace("{op}", operator));
  };
  for (let i = 0; i < line.length; i++) {
    const char = line.charAt(i);
    if (quote === "'") {
      if (char === "'") quote = undefined;
      else current += char;
      continue;
    }
    if (char === "`") refuse("a backtick");
    if (char === "$" && line.charAt(i + 1) === "(") refuse("$(");
    if (quote === '"') {
      if (char === '"') quote = undefined;
      else if (
        char === "\\" &&
        '"\\$`'.includes(line.charAt(i + 1)) &&
        i + 1 < line.length
      )
        current += line.charAt(++i);
      else current += char;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      started = true;
    } else if (char === "\\") {
      if (i + 1 >= line.length)
        throw new Error("Command ends with a backslash");
      current += line.charAt(++i);
      started = true;
    } else if ("|&;<>".includes(char)) {
      refuse(char === "&" && line.charAt(i + 1) === "&" ? "&&" : char);
    } else if (char === "\n" || char === "\r") {
      refuse("a second line");
    } else if (char === " " || char === "\t") {
      if (started || current) argv.push(current);
      current = "";
      started = false;
    } else {
      current += char;
      started = true;
    }
  }
  if (quote) throw new Error(`Command has an unterminated ${quote} quote`);
  if (started || current) argv.push(current);
  if (argv.length === 0) throw new Error("Command must not be empty");
  return argv;
}

/** argv as a model sends it: an array, a JSON array in a string, or a command line. */
export function parseArgv(value: string | readonly string[]): string[] {
  if (typeof value !== "string") return [...value];
  const text = value.trim();
  if (text.startsWith("[")) {
    try {
      const parsed: unknown = JSON.parse(text);
      if (
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === "string")
      )
        return parsed as string[];
    } catch {
      // Not JSON: treated as a command line below.
    }
  }
  return splitCommandLine(text);
}

export type ApprovedCommandProposal = Readonly<{
  kind: "command";
  argv: readonly [string, ...string[]];
  cwd: string;
  timeoutMs: number;
  /**
   * Arguments that name a path the workspace file tools treat as secret
   * (`.env`, `~/.ssh/id_rsa`). Present only when there are some: the host
   * shows them on the approval, and does not let a rule or the sandbox
   * approve such a command without that.
   */
  secretPaths?: readonly string[];
}>;

export type CommandResult = Readonly<{
  output: string;
  exitCode: number | null;
  signal: NodeJS.Signals | null;
  timedOut: boolean;
  cancelled: boolean;
  truncated: boolean;
  cleanupIncomplete: boolean;
  /** Ran under the command sandbox (see sandbox.ts). */
  sandboxed?: true;
}>;

type CwdIdentity = { dev: bigint; ino: bigint; birthtimeNs?: bigint };
const proposalCwdIdentity = new WeakMap<object, CwdIdentity>();
const proposalEnvironment = new WeakMap<object, NodeJS.ProcessEnv>();
const consumedProposals = new WeakSet<object>();

/**
 * Validates and canonicalizes a host-approved command proposal.
 * This does not decide whether a command is allowed; that decision belongs to
 * the caller's authority layer.
 */
export async function createCommandProposal(
  argv: readonly string[],
  cwd: string,
  timeoutMs: number,
  extraEnv?: Record<string, string>,
): Promise<ApprovedCommandProposal> {
  if (
    argv.length === 0 ||
    argv.length > MAX_ARG_COUNT ||
    typeof argv[0] !== "string" ||
    argv[0].length === 0 ||
    argv.some(
      (argument) => typeof argument !== "string" || argument.includes("\0"),
    ) ||
    argv.reduce((bytes, argument) => bytes + Buffer.byteLength(argument), 0) >
      MAX_ARG_BYTES
  )
    throw new Error(
      "Command argv must contain non-empty strings without NUL bytes",
    );
  if (
    !Number.isSafeInteger(timeoutMs) ||
    timeoutMs <= 0 ||
    timeoutMs > MAX_TIMEOUT_MS
  )
    throw new Error("Command timeout must be a positive safe integer");
  if (typeof cwd !== "string" || cwd.length === 0 || cwd.includes("\0"))
    throw new Error("Command cwd must be a non-empty path without NUL bytes");

  const canonicalCwd = await realpath(cwd);
  const info = await stat(canonicalCwd, { bigint: true });
  if (!info.isDirectory()) throw new Error("Command cwd must be a directory");

  const secretPaths = (await secretArguments(argv, canonicalCwd)).slice(0, 8);
  const proposal = Object.freeze({
    kind: "command" as const,
    argv: Object.freeze([...argv]) as readonly [string, ...string[]],
    cwd: canonicalCwd,
    timeoutMs,
    ...(secretPaths.length ? { secretPaths: Object.freeze(secretPaths) } : {}),
  });
  proposalCwdIdentity.set(proposal, {
    dev: info.dev,
    ino: info.ino,
    birthtimeNs: info.birthtimeNs,
  });
  proposalEnvironment.set(proposal, { ...childEnvironment(), ...extraEnv });
  return proposal;
}

// A character that would not show as itself: controls (an escape sequence
// starts with one), format characters (zero-width, direction overrides),
// separators that look like a space or a line break, private-use,
// unassigned and lone surrogate code points, and the letters and marks
// that draw nothing: the combining grapheme joiner, Hangul and Khmer
// fillers, variation selectors, the braille blank and the object
// replacement character. Accents and other combining marks stay as they
// are: they show on the letter they join.
const HIDDEN =
  // biome-ignore lint/suspicious/noMisleadingCharacterClass: with the u flag each code point is matched on its own, and a mark listed here is a character to escape, not part of the one before it
  /[\p{Cc}\p{Cf}\p{Cs}\p{Co}\p{Cn}\p{Zl}\p{Zp}\u00a0\u1680\u2000-\u200a\u202f\u205f\u3000\u034f\u115f\u1160\u17b4\u17b5\u180b-\u180f\u2800\u3164\ufe00-\ufe0f\uffa0\ufffc\u{e0100}-\u{e01ef}]/u;
const ESCAPES: Readonly<Record<string, string>> = {
  "\\": "\\\\",
  "'": "\\'",
  "\n": "\\n",
  "\t": "\\t",
  "\r": "\\r",
};

const UTF8 = new TextEncoder();

function escaped(char: string): string {
  const known = ESCAPES[char];
  if (known) return known;
  if (!HIDDEN.test(char)) return char;
  // Byte by byte, as UTF-8: every shell that reads $'…' reads \xHH, while
  // \u and \U are past bash 3.2, the /bin/bash macOS ships.
  return Array.from(
    UTF8.encode(char),
    (byte) => `\\x${byte.toString(16).padStart(2, "0")}`,
  ).join("");
}

/**
 * One argument as a POSIX shell would read it back, for display only
 * (nothing is run through a shell): bare when it needs no quoting, in single
 * quotes otherwise, and in ANSI-C quotes (`$'…'`, as bash and zsh read them)
 * when it holds a character that would not show as itself, which is then
 * written as its escape (its UTF-8 bytes, past ASCII). What an approval shows is what runs.
 */
export function shellWord(argument: string): string {
  if (argument === "") return "''";
  if (/^[A-Za-z0-9_@%+=:,./-]+$/u.test(argument)) return argument;
  if (!HIDDEN.test(argument)) return `'${argument.replaceAll("'", "'\\''")}'`;
  return `$'${Array.from(argument, escaped).join("")}'`;
}

/** A command's argv as one line of shell words (see shellWord). */
export function commandLine(argv: readonly string[]): string {
  return argv.map(shellWord).join(" ");
}

const MAX_LINK_CHECKS = 64;
/** The secretPaths entry for path words past the link check: a reason, not a path. */
const UNCHECKED = /^(\d+) path words, only \d+ checked$/;

/**
 * A command's secretPaths for display: the paths, and the number of path
 * words when there are more than are checked for a link to a secret (that
 * entry withholds the grants as a path does, but names none).
 */
export function secretPathNotes(secretPaths: readonly string[] = []): {
  paths: string[];
  unchecked?: number;
} {
  const paths: string[] = [];
  let unchecked: number | undefined;
  for (const entry of secretPaths) {
    const match = UNCHECKED.exec(entry);
    if (match) unchecked = Number(match[1]);
    else paths.push(entry);
  }
  return unchecked === undefined ? { paths } : { paths, unchecked };
}

/**
 * The words of argv that name a secret path (see secretPathsIn), then the
 * ones that name a link to one: a word that resolves inside `cwd` through a
 * symbolic link whose target the file tools would hide, shown as
 * `notes.txt -> .env`. A dangling link is judged by its text. Checked when
 * the proposal is made; a link changed afterwards is not seen. At most
 * MAX_LINK_CHECKS words are followed; with more, the proposal is flagged
 * rather than passed unchecked.
 */
async function secretArguments(
  argv: readonly string[],
  cwd: string,
): Promise<string[]> {
  const found = secretPathsIn(argv);
  const words = [
    ...new Set(
      pathTokens(argv).filter(
        (word) => !word.startsWith("-") && !word.startsWith("~"),
      ),
    ),
  ];
  for (const word of words.slice(0, MAX_LINK_CHECKS)) {
    const path = resolve(cwd, word);
    if (path !== cwd && !path.startsWith(`${cwd}${sep}`)) continue;
    let target: string | undefined;
    try {
      const real = await realpath(path);
      if (real !== path) target = real;
    } catch {
      try {
        if ((await lstat(path)).isSymbolicLink()) target = await readlink(path);
      } catch {
        // Not there: nothing to follow.
      }
    }
    if (target === undefined) continue;
    const inside = isAbsolute(target) ? relative(cwd, target) : target;
    const shown =
      inside === ".." || inside.startsWith(`..${sep}`) ? target : inside;
    if (isSecretPath(shown)) found.push(`${word} -> ${shown}`);
  }
  // Words past the cap are not looked at, and a link among them could point
  // at a secret: say so, which withholds the grants, instead of passing them.
  // First, so the proposal's cut to 8 entries keeps it.
  if (words.length > MAX_LINK_CHECKS)
    found.unshift(
      `${words.length} path words, only ${MAX_LINK_CHECKS} checked`,
    );
  return found;
}

function childEnvironment(): NodeJS.ProcessEnv {
  const environment: NodeJS.ProcessEnv = {};
  for (const key of ALLOWED_ENV) {
    const value = process.env[key];
    if (value !== undefined) environment[key] = value;
  }
  if (environment.PATH === undefined) environment.PATH = "/usr/bin:/bin";
  return Object.freeze(environment);
}

/**
 * Process groups of commands still running, by leader pid. A command runs
 * detached (its own group, so a cancel reaches its descendants), which also
 * means a host that dies would leave them behind: the hooks below kill
 * whatever is still listed. SIGKILL of the host cannot be caught.
 */
const liveGroups = new Set<number>();
const GROUP_SIGNALS = ["SIGINT", "SIGTERM", "SIGHUP"] as const;
const signalHooks = new Map<NodeJS.Signals, () => void>();

function groupAlive(pid: number): boolean {
  try {
    process.kill(-pid, 0);
    return true;
  } catch (error) {
    // EPERM still means the group exists.
    return error instanceof Error && "code" in error && error.code === "EPERM";
  }
}

/**
 * SIGTERM every live group, then SIGKILL what outlives the grace period.
 * Synchronous: it also runs from the `exit` event, where nothing async is
 * left to run.
 */
export function killLiveCommandGroups(): void {
  const groups = [...liveGroups];
  liveGroups.clear();
  removeHooks();
  const signalled = groups.filter((pid) => {
    try {
      process.kill(-pid, "SIGTERM");
      return true;
    } catch {
      return false;
    }
  });
  const deadline = Date.now() + KILL_GRACE_MS;
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  while (signalled.some(groupAlive) && Date.now() < deadline)
    Atomics.wait(sleeper, 0, 0, 10);
  // Only a group that outlived SIGTERM: a pid that is gone may be reused.
  for (const pid of signalled.filter(groupAlive)) {
    try {
      process.kill(-pid, "SIGKILL");
    } catch {
      // The group is gone.
    }
  }
}

function removeHooks(): void {
  process.off("exit", killLiveCommandGroups);
  for (const [signal, hook] of signalHooks) process.off(signal, hook);
  signalHooks.clear();
}

function trackGroup(pid: number): void {
  if (liveGroups.size === 0) {
    process.on("exit", killLiveCommandGroups);
    for (const signal of GROUP_SIGNALS) {
      const hook = () => {
        // First in line (prepended), so the count still includes handlers
        // that remove themselves when they run. When the host has its own,
        // it decides: its cancel cleans the group, and if it then exits the
        // exit hook kills what is left. A listener suppresses the default
        // death by the signal, so as the only one this hook kills the
        // groups and dies by it, as the process would have.
        //
        // The contract: with a host listener installed, this hook leaves the
        // groups alone, so that listener must cancel the command or exit.
        // Every host here does: main.ts aborts its controller,
        // tuimark/launch.ts aborts after host.exited, and the desktop
        // sidecar cancels its runs and exits.
        if (process.listenerCount(signal) > 1) return;
        killLiveCommandGroups();
        process.kill(process.pid, signal);
      };
      signalHooks.set(signal, hook);
      process.prependListener(signal, hook);
    }
  }
  liveGroups.add(pid);
}

function untrackGroup(pid: number): void {
  liveGroups.delete(pid);
  if (liveGroups.size === 0) removeHooks();
}

/**
 * Whether a signalled group is gone within the drain grace: polled with
 * timers, so the event loop reaps the leader meanwhile (a zombie still
 * counts as a member).
 */
async function groupGone(pid: number | undefined): Promise<boolean> {
  if (!pid) return true;
  const deadline = Date.now() + DRAIN_GRACE_MS;
  while (groupAlive(pid)) {
    if (Date.now() >= deadline) return false;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  return true;
}

function killGroup(child: ChildProcess, signal: NodeJS.Signals): boolean {
  if (!child.pid) return false;
  try {
    process.kill(-child.pid, signal);
    return true;
  } catch (error) {
    if (!(error instanceof Error && "code" in error && error.code === "ESRCH"))
      throw error;
    return false;
  }
}

function appendOutput(
  chunks: Buffer[],
  chunk: Buffer,
  state: { bytes: number; truncated: boolean },
): void {
  if (state.bytes >= MAX_OUTPUT_BYTES) {
    state.truncated = true;
    return;
  }
  const remaining = MAX_OUTPUT_BYTES - state.bytes;
  const kept =
    chunk.byteLength > remaining ? chunk.subarray(0, remaining) : chunk;
  chunks.push(kept);
  state.bytes += kept.byteLength;
  if (kept.byteLength !== chunk.byteLength) state.truncated = true;
}

async function validateProposal(
  proposal: ApprovedCommandProposal,
): Promise<void> {
  if (proposal?.kind !== "command")
    throw new Error("Invalid approved command proposal");
  if (
    !Array.isArray(proposal.argv) ||
    proposal.argv.length === 0 ||
    proposal.argv.length > MAX_ARG_COUNT ||
    typeof proposal.argv[0] !== "string" ||
    proposal.argv[0].length === 0 ||
    proposal.argv.some(
      (argument) => typeof argument !== "string" || argument.includes("\0"),
    ) ||
    proposal.argv.reduce(
      (bytes, argument) => bytes + Buffer.byteLength(argument),
      0,
    ) > MAX_ARG_BYTES
  )
    throw new Error("Invalid approved command argv");
  if (
    typeof proposal.cwd !== "string" ||
    proposal.cwd.length === 0 ||
    proposal.cwd.includes("\0")
  )
    throw new Error("Invalid approved command cwd");
  if (
    !Number.isSafeInteger(proposal.timeoutMs) ||
    proposal.timeoutMs <= 0 ||
    proposal.timeoutMs > MAX_TIMEOUT_MS
  )
    throw new Error("Invalid approved command timeout");
  if (!Object.isFrozen(proposal) || !Object.isFrozen(proposal.argv))
    throw new Error("Approved command proposal must be immutable");
  const expectedIdentity = proposalCwdIdentity.get(proposal);
  const environment = proposalEnvironment.get(proposal);
  if (!expectedIdentity || !environment)
    throw new Error("Approved command proposal was not issued by this host");
  const canonicalCwd = await realpath(proposal.cwd);
  if (canonicalCwd !== proposal.cwd)
    throw new Error("Approved command cwd must be canonical");
  const info = await stat(canonicalCwd, { bigint: true });
  if (!info.isDirectory())
    throw new Error("Approved command cwd must be a directory");
  if (
    info.dev !== expectedIdentity.dev ||
    info.ino !== expectedIdentity.ino ||
    (expectedIdentity.birthtimeNs !== undefined &&
      info.birthtimeNs !== expectedIdentity.birthtimeNs)
  )
    throw new Error("Approved command cwd changed since approval");
}

/**
 * Executes a previously approved argv vector. No shell is involved unless the
 * approved argv explicitly names one, such as ["/bin/sh", "-c", "..."].
 * With `sandbox` it runs confined (sandbox.ts) or not at all: a sandbox that
 * is unavailable is an error, never a reason to run it as it is.
 */
export async function executeApprovedCommand(
  proposal: ApprovedCommandProposal,
  signal: AbortSignal,
  sandbox?: CommandSandbox,
): Promise<CommandResult> {
  if (typeof proposal !== "object" || proposal === null)
    throw new Error("Invalid approved command proposal");
  if (consumedProposals.has(proposal))
    throw new Error("Approved command proposal was already consumed");
  consumedProposals.add(proposal);
  await validateProposal(proposal);
  const environment = proposalEnvironment.get(proposal);
  if (!environment)
    throw new Error("Approved command proposal was not issued by this host");
  proposalCwdIdentity.delete(proposal);
  proposalEnvironment.delete(proposal);
  const unavailable = sandbox
    ? sandboxUnavailable(proposal.cwd, sandbox.home)
    : undefined;
  if (unavailable)
    throw new Error(`Command sandbox unavailable: ${unavailable}`);
  if (signal.aborted)
    return {
      output: "",
      exitCode: null,
      signal: null,
      timedOut: false,
      cancelled: true,
      truncated: false,
      cleanupIncomplete: false,
      ...(sandbox ? { sandboxed: true as const } : {}),
    };

  const confined = sandbox
    ? sandboxedCommand(proposal.argv, proposal.cwd, environment, sandbox)
    : undefined;
  const child = spawn(
    confined?.file ?? proposal.argv[0],
    confined?.args ?? proposal.argv.slice(1),
    {
      cwd: proposal.cwd,
      env: confined?.env ?? environment,
      shell: false,
      detached: true,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );

  if (child.pid) trackGroup(child.pid);
  const chunks: Buffer[] = [];
  const outputState = { bytes: 0, truncated: false };
  let timedOut = false;
  let cancelled = false;
  let terminationReason: "timeout" | "cancel" | undefined;
  let groupCleanup: Promise<boolean> | undefined;
  let timeoutTimer: ReturnType<typeof setTimeout> | undefined;
  let drainTimer: ReturnType<typeof setTimeout> | undefined;
  let terminationTimer: ReturnType<typeof setTimeout> | undefined;
  let forceFinish: (() => void) | undefined;

  /**
   * Stops the command's process group: SIGTERM, then SIGKILL for what
   * outlives the grace period. True when nothing of the group is left;
   * false when that cannot be confirmed (a signal was refused, or the group
   * outlived SIGKILL). A descendant that left the group (setsid, a daemon)
   * is not seen, as for any command that finishes.
   */
  const cleanupGroup = (): Promise<boolean> => {
    if (groupCleanup) return groupCleanup;
    groupCleanup = new Promise((resolve) => {
      const done = (gone: boolean) => {
        if (child.pid) untrackGroup(child.pid);
        resolve(gone);
      };
      let groupExists = false;
      try {
        groupExists = killGroup(child, "SIGTERM");
      } catch {
        try {
          child.kill("SIGTERM");
        } catch {
          // The process may have exited between group lookup and fallback.
        }
        // The group could not be signalled (EPERM): it may be left.
        done(false);
        return;
      }
      if (!groupExists) {
        done(true);
        return;
      }
      setTimeout(() => {
        // Only a group that outlived SIGTERM: a pid that is gone may be
        // reused.
        if (child.pid && groupAlive(child.pid))
          try {
            killGroup(child, "SIGKILL");
          } catch {
            try {
              child.kill("SIGKILL");
            } catch {
              // The process group may have disappeared during escalation.
            }
          }
        void groupGone(child.pid).then(done);
      }, KILL_GRACE_MS);
    });
    return groupCleanup;
  };

  const terminate = (reason: "timeout" | "cancel"): void => {
    if (terminationReason) return;
    terminationReason = reason;
    timedOut ||= reason === "timeout";
    cancelled ||= reason === "cancel";
    void cleanupGroup();
    terminationTimer = setTimeout(
      () => forceFinish?.(),
      TERMINATION_DEADLINE_MS,
    );
    terminationTimer.unref?.();
  };

  const onAbort = () => terminate("cancel");
  signal.addEventListener("abort", onAbort, { once: true });
  timeoutTimer = setTimeout(() => terminate("timeout"), proposal.timeoutMs);
  timeoutTimer.unref?.();

  const result = await new Promise<CommandResult>((resolve, reject) => {
    const onData = (chunk: Buffer | string) =>
      appendOutput(
        chunks,
        Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk),
        outputState,
      );
    child.stdout?.on("data", onData);
    child.stderr?.on("data", onData);
    const onError = (error: Error) => {
      void cleanupGroup().finally(() => reject(error));
    };
    child.once("exit", () => {
      void cleanupGroup();
    });
    child.once("error", onError);
    let leaderExitCode: number | null = null;
    let leaderSignal: NodeJS.Signals | null = null;
    let settled = false;
    const finish = async (cleanupIncomplete: boolean) => {
      if (settled) return;
      settled = true;
      if (drainTimer) clearTimeout(drainTimer);
      if (terminationTimer) clearTimeout(terminationTimer);
      if (cleanupIncomplete) {
        child.stdout?.destroy();
        child.stderr?.destroy();
        child.unref();
      }
      const gone = await cleanupGroup();
      resolve({
        output: Buffer.concat(chunks).toString("utf8"),
        exitCode: leaderExitCode,
        signal: leaderSignal,
        timedOut,
        cancelled,
        truncated: outputState.truncated,
        cleanupIncomplete: cleanupIncomplete || !gone,
        ...(sandbox ? { sandboxed: true as const } : {}),
      });
    };
    forceFinish = () => void finish(true);
    child.once("exit", (exitCode, childSignal) => {
      leaderExitCode = exitCode;
      leaderSignal = childSignal;
      drainTimer = setTimeout(() => void finish(true), DRAIN_GRACE_MS);
      drainTimer.unref?.();
    });
    child.once("close", () => {
      void finish(false);
    });
    child.once("close", () => {
      child.removeListener("error", onError);
      child.stdout?.removeListener("data", onData);
      child.stderr?.removeListener("data", onData);
    });
  }).finally(() => {
    signal.removeEventListener("abort", onAbort);
    if (timeoutTimer) clearTimeout(timeoutTimer);
    if (terminationTimer) clearTimeout(terminationTimer);
    if (drainTimer) clearTimeout(drainTimer);
  });

  return result;
}
