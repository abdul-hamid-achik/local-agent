/** Spawns the Bun sidecar and multiplexes NDJSON RPC + stream events. */
import { type ChildProcess, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import {
  isRpcResponse,
  isStreamEnvelope,
  type RpcResponse,
  type StreamEnvelope,
} from "#desktop/shared/protocol";

export type StreamHandler = (envelope: StreamEnvelope) => void;
export type ExitHandler = (code: number | null) => void;

function resolveBun(): string {
  const candidates = [
    "bun",
    "/opt/homebrew/bin/bun",
    join(homedir(), ".bun", "bin", "bun"),
  ];
  for (const candidate of candidates) {
    if (candidate === "bun" || existsSync(candidate)) return candidate;
  }
  return "bun";
}

export class SidecarClient {
  private child: ChildProcess | undefined;
  private nextId = 1;
  private pending = new Map<
    number,
    { resolve: (value: unknown) => void; reject: (error: Error) => void }
  >();
  private buffer = "";

  onStream: StreamHandler = () => undefined;
  onExit: ExitHandler = () => undefined;

  constructor(
    private readonly repoRoot: string,
    private readonly extraEnv: Record<string, string> = {},
  ) {}

  get running(): boolean {
    return this.child !== undefined && this.child.exitCode === null;
  }

  start(): void {
    if (this.running) return;
    const child = spawn(
      resolveBun(),
      [join(this.repoRoot, "apps", "desktop", "src", "sidecar", "sidecar.ts")],
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
    this.buffer = "";
    child.stdout?.on("data", (chunk: Buffer) =>
      this.handleData(chunk.toString("utf8")),
    );
    child.stderr?.on("data", (chunk: Buffer) => {
      console.error(`[sidecar] ${chunk.toString("utf8").trimEnd()}`);
    });
    child.on("exit", (code) => {
      this.child = undefined;
      for (const [, entry] of this.pending)
        entry.reject(new Error("Sidecar exited"));
      this.pending.clear();
      this.onExit(code);
    });
    child.on("error", (error) => {
      console.error(`[sidecar] spawn failed: ${error.message}`);
    });
  }

  stop(): void {
    this.child?.kill();
    this.child = undefined;
  }

  invoke(method: string, params?: unknown): Promise<unknown> {
    if (!this.running)
      return Promise.reject(new Error("Sidecar is not running"));
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

  private handleData(text: string): void {
    this.buffer += text;
    let newline = this.buffer.indexOf("\n");
    while (newline >= 0) {
      const line = this.buffer.slice(0, newline).trim();
      this.buffer = this.buffer.slice(newline + 1);
      newline = this.buffer.indexOf("\n");
      if (!line) continue;
      let frame: unknown;
      try {
        frame = JSON.parse(line) as unknown;
      } catch {
        continue;
      }
      if (isRpcResponse(frame)) this.handleResponse(frame);
      else if (isStreamEnvelope(frame)) this.onStream(frame);
    }
  }

  private handleResponse(frame: RpcResponse): void {
    const entry = this.pending.get(frame.id);
    if (!entry) return; // Duplicate/terminal frame from a streaming method.
    this.pending.delete(frame.id);
    if (frame.ok) entry.resolve(frame.result);
    else entry.reject(new Error(frame.error));
  }
}
