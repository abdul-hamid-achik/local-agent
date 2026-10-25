/** Spawns the Bun sidecar and multiplexes NDJSON RPC + stream events. */
import { type ChildProcess, spawn } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";
import {
  type BunResolution,
  bunNotFoundMessage,
  resolveBun,
} from "#desktop/main/bun-path";
import { LineDecoder } from "#desktop/main/ndjson";
import {
  isRpcResponse,
  isStreamEnvelope,
  type RpcResponse,
  type StreamEnvelope,
} from "#desktop/shared/protocol";

export type StreamHandler = (envelope: StreamEnvelope) => void;
/** `message` says why the sidecar went away. */
export type ExitHandler = (code: number | null, message?: string) => void;

export type SidecarOptions = {
  resolveBun?: () => BunResolution;
  script?: string;
  /** How long stop() waits after SIGTERM before SIGKILL. */
  stopGraceMs?: number;
};

const STDERR_TAIL_CHARS = 2000;
// Longer than the sidecar's own shutdown (it cancels its runs, waits two
// seconds for them, then has their command groups killed), so a SIGKILL
// never cuts that short and leaves the groups behind.
const STOP_GRACE_MS = 5000;
// A frame carries at most an 8 MB file, which JSON can double.
const MAX_LINE_CHARS = 32 * 1024 * 1024;
// The sidecar ends on 32 MiB of unread input (MAX_LINE_BYTES * 4 there),
// every run with it, so a larger request is refused here, with room left for
// what shares its read.
const MAX_REQUEST_CHARS = MAX_LINE_CHARS - 1024 * 1024;
const PREVIEW_CHARS = 80;

/** A line for a message: one line, no escape sequences, cut short. */
function preview(line: string): string {
  const flat = line
    // biome-ignore lint/suspicious/noControlCharactersInRegex: remove escape sequences from what the sidecar wrote
    .replace(/\u001b\[[0-?]*[ -/]*[@-~]/gu, "")
    // biome-ignore lint/suspicious/noControlCharactersInRegex: and control characters
    .replace(/[\s\u0000-\u001f\u007f-\u009f]+/gu, " ")
    .trim();
  // By code point: a cut never leaves half of a surrogate pair.
  const chars = Array.from(flat);
  return chars.length > PREVIEW_CHARS
    ? `${chars.slice(0, PREVIEW_CHARS).join("")}…`
    : flat;
}

/** SIGTERM, then SIGKILL once the grace is over; settles when it is gone. */
function terminate(child: ChildProcess, graceMs: number): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null)
    return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(() => child.kill("SIGKILL"), graceMs);
    const done = () => {
      clearTimeout(timer);
      resolve();
    };
    child.once("exit", done);
    child.once("error", done);
    child.kill("SIGTERM");
  });
}

export class SidecarClient {
  private child: ChildProcess | undefined;
  private nextId = 1;
  private pending = new Map<
    number,
    { resolve: (value: unknown) => void; reject: (error: Error) => void }
  >();
  private lines = new LineDecoder();
  private stderrTail = "";
  // Why the sidecar is down; every invoke rejects with it until start().
  private failure: Error | undefined;

  onStream: StreamHandler = () => undefined;
  onExit: ExitHandler = () => undefined;

  constructor(
    private readonly repoRoot: string,
    private readonly extraEnv: Record<string, string> = {},
    private readonly options: SidecarOptions = {},
  ) {}

  get running(): boolean {
    return this.child !== undefined && this.child.exitCode === null;
  }

  start(): void {
    if (this.running) return;
    this.failure = undefined;
    const resolution = (
      this.options.resolveBun ??
      (() => resolveBun({ env: process.env, home: homedir() }))
    )();
    if (!resolution.path) {
      this.child = undefined;
      this.fail(undefined, null, bunNotFoundMessage(resolution.tried));
      return;
    }
    const bun = resolution.path;
    const child = spawn(
      bun,
      [
        this.options.script ??
          join(
            this.repoRoot,
            "apps",
            "desktop",
            "src",
            "sidecar",
            "sidecar.ts",
          ),
      ],
      {
        cwd: this.repoRoot,
        env: {
          ...process.env,
          DESKTOP_REPO_ROOT: this.repoRoot,
          ...this.extraEnv,
        },
        stdio: ["pipe", "pipe", "pipe"],
      },
    );
    this.child = child;
    this.lines = new LineDecoder();
    this.stderrTail = "";
    // A stopped or replaced child's last output is dropped: it would land in
    // the new child's decoder and stream.
    child.stdout?.on("data", (chunk: Buffer) => {
      if (child !== this.child) return;
      for (const line of this.lines.push(chunk)) {
        // A line before this one may have ended the child.
        if (child !== this.child) return;
        this.handleLine(child, line);
      }
      if (child === this.child && this.lines.pending > MAX_LINE_CHARS)
        this.broken(child, `a line over ${MAX_LINE_CHARS} characters`);
    });
    child.stderr?.on("data", (chunk: Buffer) => {
      if (child !== this.child) return;
      const text = chunk.toString("utf8");
      console.error(`[sidecar] ${text.trimEnd()}`);
      this.stderrTail = (this.stderrTail + text).slice(-STDERR_TAIL_CHARS);
    });
    // A broken pipe on a dead child must not become an uncaught exception.
    child.stdin?.on("error", () => undefined);
    child.on("exit", (code) => {
      const detail = this.stderrTail.trim();
      this.fail(
        child,
        code,
        `Sidecar exited${code === null ? "" : ` with code ${code}`}${detail ? `: ${detail}` : "."}`,
      );
    });
    child.on("error", (error) => {
      this.fail(
        child,
        null,
        `Could not start the sidecar with Bun at ${bun}: ${error.message}. Check that the file is a working Bun executable, then restart the sidecar.`,
      );
    });
  }

  /**
   * Stops the sidecar without reporting an exit. It settles once the child
   * is gone: SIGTERM lets it cancel its runs and exit, and SIGKILL follows
   * if it has not after the grace.
   */
  stop(): Promise<void> {
    const child = this.child;
    this.child = undefined;
    this.failure = new Error("Sidecar was stopped");
    this.rejectPending(this.failure);
    return child
      ? terminate(child, this.options.stopGraceMs ?? STOP_GRACE_MS)
      : Promise.resolve();
  }

  invoke(method: string, params?: unknown): Promise<unknown> {
    if (!this.running)
      return Promise.reject(
        this.failure ?? new Error("Sidecar is not running"),
      );
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try {
        const line = `${JSON.stringify({ id, method, params })}\n`;
        if (line.length > MAX_REQUEST_CHARS)
          throw new Error(`The ${method} request is too large for the sidecar`);
        this.child?.stdin?.write(line);
      } catch (error) {
        this.pending.delete(id);
        reject(
          error instanceof Error ? error : new Error("Sidecar write failed"),
        );
      }
    });
  }

  /** Ends the sidecar's life once: a stopped or replaced child reports nothing. */
  private fail(
    child: ChildProcess | undefined,
    code: number | null,
    message: string,
  ): void {
    if (child !== this.child) return;
    this.child = undefined;
    this.failure = new Error(message);
    this.rejectPending(this.failure);
    this.onExit(code, message);
  }

  private rejectPending(error: Error): void {
    for (const [, entry] of this.pending) entry.reject(error);
    this.pending.clear();
  }

  /**
   * The sidecar's stdout carries frames only (its console goes to stderr),
   * so any other line means the protocol broke: the answers it was meant to
   * carry cannot be trusted to come. The sidecar is ended and every waiting
   * invoke rejects, instead of waiting forever.
   */
  private broken(child: ChildProcess, what: string): void {
    this.fail(
      child,
      null,
      `The sidecar wrote ${what}, not a frame, and was stopped. Restart the sidecar.`,
    );
    void terminate(child, this.options.stopGraceMs ?? STOP_GRACE_MS);
  }

  private handleLine(child: ChildProcess, raw: string): void {
    const line = raw.trim();
    if (!line) return;
    let frame: unknown;
    try {
      frame = JSON.parse(line) as unknown;
    } catch {
      this.broken(child, `"${preview(line)}"`);
      return;
    }
    if (isRpcResponse(frame)) this.handleResponse(frame);
    else if (isStreamEnvelope(frame)) this.onStream(frame);
    else if (this.rejectMalformed(frame)) return;
    else this.broken(child, `"${preview(line)}"`);
  }

  /** An answer to a waiting invoke in the wrong shape fails that invoke. */
  private rejectMalformed(frame: unknown): boolean {
    if (typeof frame !== "object" || frame === null) return false;
    const id = (frame as Record<string, unknown>).id;
    const entry = typeof id === "number" ? this.pending.get(id) : undefined;
    if (!entry || typeof id !== "number") return false;
    this.pending.delete(id);
    entry.reject(new Error("The sidecar answered with a malformed frame"));
    return true;
  }

  private handleResponse(frame: RpcResponse): void {
    const entry = this.pending.get(frame.id);
    if (!entry) {
      // The sidecar answers each request once, and every waiting invoke is
      // rejected (and forgotten) only when the child is gone, so an answer
      // nobody waits for is a correlation bug: said, not swallowed.
      console.error(`[main] ignored an answer to unknown request ${frame.id}`);
      return;
    }
    this.pending.delete(frame.id);
    if (frame.ok) entry.resolve(frame.result);
    else
      entry.reject(
        new Error(
          typeof frame.error === "string" && frame.error
            ? frame.error
            : "Sidecar request failed",
        ),
      );
  }
}
