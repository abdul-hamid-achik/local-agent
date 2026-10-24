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
};

const STDERR_TAIL_CHARS = 2000;

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
    child.stdout?.on("data", (chunk: Buffer) => {
      for (const line of this.lines.push(chunk)) this.handleLine(line);
    });
    child.stderr?.on("data", (chunk: Buffer) => {
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

  stop(): void {
    const child = this.child;
    this.child = undefined;
    this.failure = new Error("Sidecar was stopped");
    this.rejectPending(this.failure);
    child?.kill();
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
        this.child?.stdin?.write(`${JSON.stringify({ id, method, params })}\n`);
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

  private handleLine(raw: string): void {
    const line = raw.trim();
    if (!line) return;
    let frame: unknown;
    try {
      frame = JSON.parse(line) as unknown;
    } catch {
      return;
    }
    if (isRpcResponse(frame)) this.handleResponse(frame);
    else if (isStreamEnvelope(frame)) this.onStream(frame);
  }

  private handleResponse(frame: RpcResponse): void {
    const entry = this.pending.get(frame.id);
    if (!entry) return; // Duplicate/terminal frame from a streaming method.
    this.pending.delete(frame.id);
    if (frame.ok) entry.resolve(frame.result);
    else entry.reject(new Error(frame.error));
  }
}
