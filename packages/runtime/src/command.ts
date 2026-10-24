import { type ChildProcess, spawn } from "node:child_process";
import { realpath, stat } from "node:fs/promises";
import {
  type CommandSandbox,
  sandboxedCommand,
  sandboxUnavailable,
} from "#runtime/sandbox";
import { secretPathIn } from "#runtime/workspace";

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

  const secretPaths = [
    ...new Set(argv.flatMap((argument) => secretPathIn(argument) ?? [])),
  ].slice(0, 8);
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
  let groupCleanup: Promise<void> | undefined;
  let timeoutTimer: ReturnType<typeof setTimeout> | undefined;
  let drainTimer: ReturnType<typeof setTimeout> | undefined;
  let terminationTimer: ReturnType<typeof setTimeout> | undefined;
  let forceFinish: (() => void) | undefined;

  const cleanupGroup = (): Promise<void> => {
    if (groupCleanup) return groupCleanup;
    groupCleanup = new Promise((resolve) => {
      let groupExists = false;
      try {
        groupExists = killGroup(child, "SIGTERM");
      } catch {
        try {
          child.kill("SIGTERM");
        } catch {
          // The process may have exited between group lookup and fallback.
        }
      }
      if (!groupExists) {
        if (child.pid) untrackGroup(child.pid);
        resolve();
        return;
      }
      setTimeout(() => {
        try {
          killGroup(child, "SIGKILL");
        } catch {
          try {
            child.kill("SIGKILL");
          } catch {
            // The process group may have disappeared during escalation.
          }
        }
        if (child.pid) untrackGroup(child.pid);
        resolve();
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
      await cleanupGroup();
      resolve({
        output: Buffer.concat(chunks).toString("utf8"),
        exitCode: leaderExitCode,
        signal: leaderSignal,
        timedOut,
        cancelled,
        truncated: outputState.truncated,
        cleanupIncomplete,
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
