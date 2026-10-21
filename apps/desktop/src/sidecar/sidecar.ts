/**
 * Bun sidecar: the only desktop process that imports @local-agent/runtime.
 * Speaks framed NDJSON JSON-RPC over stdio (see #desktop/shared/protocol).
 *
 * Run from the repository root: `bun apps/desktop/src/sidecar/sidecar.ts`.
 */
import { randomUUID } from "node:crypto";
import { readdir, stat } from "node:fs/promises";
import { join, relative, resolve } from "node:path";
import {
  type ApprovalRequest,
  admitOllamaModel,
  connectMcp,
  inspectRecovery,
  type McpConnection,
  McpServerConfigSchema,
  resolveProvider,
  runAgent,
  type SessionContext,
  SessionStore,
  Workspace,
} from "@local-agent/runtime";
import {
  type AdmissionView,
  type AgentStreamEvent,
  type ChatMessage,
  type ProjectCommandName,
  ProjectCommands,
  type ProviderView,
  type RpcRequest,
  type RpcResponse,
  SidecarMethods,
  type StreamEnvelope,
} from "#desktop/shared/protocol";

const repoRoot = (() => {
  const override = process.env.DESKTOP_REPO_ROOT;
  const raw =
    override ?? Bun.fileURLToPath(new URL("../../../../", import.meta.url));
  return raw.endsWith("/") && raw !== "/" ? raw.slice(0, -1) : raw;
})();

const MAX_LINE_BYTES = 8 * 1024 * 1024;
const MAX_OUTPUT_BYTES = 512 * 1024;

function writeFrame(frame: RpcResponse | StreamEnvelope): void {
  process.stdout.write(`${JSON.stringify(frame)}\n`);
}

function fail(id: number, error: unknown): void {
  writeFrame({
    id,
    ok: false,
    error: error instanceof Error ? error.message : "Sidecar request failed",
  });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function requiredString(params: unknown, key: string): string {
  if (!isRecord(params) || typeof params[key] !== "string" || !params[key])
    throw new Error(`Missing string param: ${key}`);
  return params[key] as string;
}

function optionalString(params: unknown, key: string): string | undefined {
  if (!isRecord(params) || params[key] === undefined) return undefined;
  if (typeof params[key] !== "string") throw new Error(`Invalid param: ${key}`);
  return params[key];
}

function resolveWorkspaceRoot(input: string): string {
  const root = resolve(input);
  if (!root.startsWith(`${repoRoot}/`) && root !== repoRoot)
    throw new Error("Workspace must stay inside the repository checkout");
  return root;
}

async function resolveProviderView(model?: string): Promise<ProviderView> {
  const provider = resolveProvider(process.env, model);
  return {
    type: provider.type,
    base_url: provider.base_url,
    model: provider.model,
    context_size: provider.context_size,
    source:
      process.env.LOCAL_AGENT_PROVIDER === "chalupa" ? "chalupa" : "local",
  };
}

/* ------------------------------------------------------------------ */
/* Agent runs with approval round-trips                                 */
/* ------------------------------------------------------------------ */

type PendingDecision = {
  token: string;
  resolve: (allowed: boolean) => void;
};

type ActiveRun = {
  controller: AbortController;
  pending: PendingDecision | undefined;
};

const activeRuns = new Map<string, ActiveRun>();

function emitStream(stream: string, event: AgentStreamEvent): void {
  writeFrame({ stream, event });
}

async function handleAgentStart(id: number, params: unknown): Promise<void> {
  if (!isRecord(params)) throw new Error("Invalid agent.start params");
  const runId =
    typeof params.runId === "string" && params.runId
      ? params.runId
      : randomUUID();
  if (activeRuns.has(runId)) throw new Error("Duplicate run id");
  if (activeRuns.size >= 4) throw new Error("Too many concurrent agent runs");
  const messages = params.messages as ChatMessage[];
  if (
    !Array.isArray(messages) ||
    messages.length === 0 ||
    messages.length > 1000
  )
    throw new Error("messages must be a non-empty array");
  const workspaceRoot = resolveWorkspaceRoot(
    requiredString(params, "workspace"),
  );
  const intent = params.intent;
  if (intent !== "plan" && intent !== "build")
    throw new Error("Unknown intent");
  const model = optionalString(params, "model") || undefined;
  const sessionDb = optionalString(params, "sessionDb") || undefined;
  const resume = optionalString(params, "resume") || undefined;
  const mcpConfigPath = optionalString(params, "mcpConfig") || undefined;

  const controller = new AbortController();
  const run: ActiveRun = { controller, pending: undefined };
  activeRuns.set(runId, run);

  const provider = resolveProvider(process.env, model);
  const workspace = await Workspace.create(workspaceRoot);
  let store: SessionStore | undefined;
  const mcp: McpConnection[] = [];
  try {
    store = sessionDb ? await SessionStore.open(sessionDb) : undefined;
    if (mcpConfigPath) {
      const file = Bun.file(mcpConfigPath);
      if (file.size > 1_000_000) throw new Error("MCP config exceeds 1 MB");
      const configs = McpServerConfigSchema.array()
        .max(16)
        .parse(await file.json());
      if (new Set(configs.map((config) => config.name)).size !== configs.length)
        throw new Error("Duplicate MCP server names");
      for (const config of configs) {
        controller.signal.throwIfAborted();
        mcp.push(await connectMcp(config, controller.signal));
      }
    }
    let session: SessionContext | undefined;
    if (store) {
      session = {
        store,
        state: resume
          ? await store.loadSession(resume, workspace.root)
          : await store.createSession(workspace.root),
      };
    }
    writeFrame({
      id,
      ok: true,
      result: { runId, sessionId: session?.state.publicId },
    });

    const approve = (proposal: ApprovalRequest): Promise<boolean> =>
      new Promise((resolveDecision) => {
        const token = randomUUID();
        run.pending = { token, resolve: resolveDecision };
        controller.signal.addEventListener(
          "abort",
          () => resolveDecision(false),
          {
            once: true,
          },
        );
        if (proposal.kind === "edit") {
          emitStream(runId, {
            type: "approval_requested",
            token,
            proposal: {
              kind: "edit",
              path: proposal.path,
              before: proposal.before,
              after: proposal.after,
              diff: proposal.diff,
            },
          });
        } else if (proposal.kind === "write") {
          emitStream(runId, {
            type: "approval_requested",
            token,
            proposal: {
              kind: "write",
              path: proposal.path,
              created: proposal.created,
              before: proposal.before,
              after: proposal.after,
              diff: proposal.diff,
            },
          });
        } else if (proposal.kind === "command") {
          emitStream(runId, {
            type: "approval_requested",
            token,
            proposal: {
              kind: "command",
              argv: [...proposal.argv],
              timeout_ms: proposal.timeoutMs,
              cwd: proposal.cwd,
            },
          });
        } else {
          emitStream(runId, {
            type: "approval_requested",
            token,
            proposal: {
              kind: "mcp",
              name: proposal.name,
              argumentsJson: proposal.argumentsJson,
            },
          });
        }
      });

    for await (const event of runAgent(provider, messages, {
      workspace,
      intent,
      approve: (proposal, signal) => {
        signal.throwIfAborted();
        return approve(proposal);
      },
      signal: controller.signal,
      session,
      mcp,
    })) {
      emitStream(runId, event as AgentStreamEvent);
    }
    emitStream(runId, { type: "run_done" });
  } catch (error) {
    if (controller.signal.aborted) {
      emitStream(runId, { type: "run_error", message: "Run cancelled" });
    } else {
      emitStream(runId, {
        type: "run_error",
        message: error instanceof Error ? error.message : "Agent run failed",
      });
    }
    emitStream(runId, { type: "run_done" });
  } finally {
    run.pending?.resolve(false);
    activeRuns.delete(runId);
    await Promise.allSettled(mcp.map((connection) => connection.close()));
    store?.close();
    workspace.close();
  }
}

function handleAgentDecide(params: unknown): { decided: boolean } {
  if (!isRecord(params)) throw new Error("Invalid agent.decide params");
  const run = activeRuns.get(requiredString(params, "runId"));
  const token = requiredString(params, "token");
  if (typeof params.allowed !== "boolean")
    throw new Error("Missing allowed flag");
  if (run?.pending?.token === token) {
    const pending = run.pending;
    run.pending = undefined;
    pending.resolve(params.allowed);
    return { decided: true };
  }
  return { decided: false };
}

function handleAgentCancel(params: unknown): { cancelled: boolean } {
  if (!isRecord(params)) throw new Error("Invalid agent.cancel params");
  const run = activeRuns.get(requiredString(params, "runId"));
  if (!run) return { cancelled: false };
  run.controller.abort();
  run.pending?.resolve(false);
  run.pending = undefined;
  return { cancelled: true };
}

/* ------------------------------------------------------------------ */
/* Workspace, sessions, recovery, MCP                                  */
/* ------------------------------------------------------------------ */

async function handleWorkspaceRead(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid workspace.read params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const path = requiredString(params, "path");
  using workspace = await Workspace.create(root);
  const text = await workspace.read(path);
  return { text, bytes: Buffer.byteLength(text) };
}

async function handleWorkspaceList(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid workspace.list params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const dir = optionalString(params, "dir") ?? "";
  if (dir.includes("\0") || dir.startsWith("/") || dir.includes(".."))
    throw new Error("Invalid directory");
  const absolute = resolve(root, dir);
  const rel = relative(root, absolute);
  if (rel.startsWith("..")) throw new Error("Directory escapes workspace");
  const names = await readdir(absolute);
  const entries = [];
  for (const name of names.slice(0, 500)) {
    const info = await stat(join(absolute, name));
    entries.push({
      name,
      kind: info.isDirectory() ? "directory" : "file",
      size: info.isFile() ? info.size : 0,
    });
  }
  entries.sort((a, b) =>
    a.kind === b.kind
      ? a.name.localeCompare(b.name)
      : a.kind === "directory"
        ? -1
        : 1,
  );
  return { dir: rel, entries, truncated: names.length > 500 };
}

async function handleSessionList(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid session.list params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const store = await SessionStore.open(requiredString(params, "db"));
  try {
    const sessions = await store.listSessions(root);
    return {
      sessions: sessions.map((session) => ({
        id: session.publicId,
        revision: session.revision,
        messages: session.messages.length,
      })),
    };
  } finally {
    store.close();
  }
}

async function handleSessionCreate(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid session.create params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const store = await SessionStore.open(requiredString(params, "db"));
  try {
    const session = await store.createSession(root);
    return { id: session.publicId, revision: session.revision };
  } finally {
    store.close();
  }
}

async function handleSessionInfo(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid session.info params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const store = await SessionStore.open(requiredString(params, "db"));
  try {
    const state = await store.loadSession(requiredString(params, "id"), root);
    const release = await store.acquireLease(state.id, root);
    try {
      const current = await store.loadSession(state.publicId, root);
      return {
        session: {
          id: current.publicId,
          revision: current.revision,
          messages: current.messages,
        },
        hazards: store
          .listHazards(current.id, current.workspaceId)
          .map((hazard) => ({
            id: hazard.id,
            execution_id: hazard.execution_id,
            turn_id: hazard.turn_id,
            tool_name: hazard.tool_name,
            event_type: hazard.event_type,
            effect_class: hazard.effect_class,
          })),
      };
    } finally {
      await release();
    }
  } finally {
    store.close();
  }
}

async function handleRecoveryInfo(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid recovery.info params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const store = await SessionStore.open(requiredString(params, "db"));
  try {
    return await inspectRecovery(store, requiredString(params, "id"), root);
  } finally {
    store.close();
  }
}

async function handleMcpStatus(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid mcp.status params");
  const configPath = requiredString(params, "configPath");
  const file = Bun.file(configPath);
  if (file.size > 1_000_000) throw new Error("MCP config exceeds 1 MB");
  const configs = McpServerConfigSchema.array()
    .max(16)
    .parse(await file.json());
  if (new Set(configs.map((config) => config.name)).size !== configs.length)
    throw new Error("Duplicate MCP server names");
  const controller = new AbortController();
  const servers = [];
  for (const config of configs) {
    try {
      const connection = await connectMcp(config, controller.signal);
      const tools = connection.tools.map((tool) => ({
        name: tool.name,
        description: tool.description,
        readOnly: tool.readOnly,
      }));
      await connection.close();
      servers.push({
        name: config.name,
        transport: config.transport.kind,
        status: "connected",
        tools,
      });
    } catch {
      servers.push({
        name: config.name,
        transport: config.transport.kind,
        status: "unavailable",
        tools: [],
      });
    }
  }
  return { servers };
}

/* ------------------------------------------------------------------ */
/* Providers                                                           */
/* ------------------------------------------------------------------ */

async function handleProviderAdmit(params: unknown): Promise<AdmissionView> {
  const model = isRecord(params) ? optionalString(params, "model") : undefined;
  const provider = resolveProvider(process.env, model);
  const admission = await admitOllamaModel(
    provider,
    AbortSignal.timeout(30_000),
  );
  return admission;
}

async function handleOllamaModels(params: unknown) {
  const model = isRecord(params) ? optionalString(params, "model") : undefined;
  const provider = resolveProvider(process.env, model);
  const response = await fetch(`${provider.base_url}/api/tags`, {
    signal: AbortSignal.timeout(10_000),
    redirect: "error",
  });
  if (!response.ok)
    throw new Error(`Ollama inventory failed (${response.status})`);
  const raw = (await response.json()) as {
    models?: Array<{ name?: unknown; size?: unknown }>;
  };
  return {
    endpoint: provider.base_url,
    models: (raw.models ?? []).slice(0, 200).map((entry) => ({
      name: typeof entry.name === "string" ? entry.name : "unknown",
      size: typeof entry.size === "number" ? entry.size : 0,
    })),
  };
}

/* ------------------------------------------------------------------ */
/* Project commands (allowlisted) and repo inventory                   */
/* ------------------------------------------------------------------ */

type ProjectRun = { process: ReturnType<typeof Bun.spawn>; killed: boolean };
const projectRuns = new Map<string, ProjectRun>();

async function handleProjectRun(id: number, params: unknown): Promise<void> {
  if (!isRecord(params)) throw new Error("Invalid project.run params");
  const name = requiredString(params, "name") as ProjectCommandName;
  const argv = ProjectCommands[name];
  if (!argv) throw new Error("Unknown project command");
  const runId =
    typeof params.runId === "string" && params.runId
      ? params.runId
      : randomUUID();
  if (projectRuns.has(runId)) throw new Error("Duplicate project run id");
  const binary = name.startsWith("git:") ? "git" : (Bun.which("bun") ?? "bun");
  const child = Bun.spawn([binary, ...argv.slice(1)], {
    cwd: repoRoot,
    stdout: "pipe",
    stderr: "pipe",
  });
  projectRuns.set(runId, { process: child, killed: false });
  writeFrame({ id, ok: true, result: { runId } });

  let emitted = 0;
  const pump = async (
    stream: ReadableStream<Uint8Array> | null,
    type: "stdout" | "stderr",
  ) => {
    if (!stream) return;
    const reader = stream.getReader();
    const decoder = new TextDecoder();
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      if (emitted >= MAX_OUTPUT_BYTES) continue;
      const text = decoder.decode(chunk.value, { stream: true });
      emitted += chunk.value.byteLength;
      const clipped =
        emitted > MAX_OUTPUT_BYTES
          ? `${text.slice(0, Math.max(0, text.length - (emitted - MAX_OUTPUT_BYTES)))}…[truncated]`
          : text;
      writeFrame({ stream: runId, event: { type, text: clipped } });
    }
  };
  let code: number;
  try {
    await Promise.all([
      pump(child.stdout, "stdout"),
      pump(child.stderr, "stderr"),
    ]);
    code = await child.exited;
  } catch {
    code = 1;
  }
  projectRuns.delete(runId);
  writeFrame({ stream: runId, event: { type: "exit", code } });
}

function handleProjectCancel(params: unknown): { cancelled: boolean } {
  if (!isRecord(params)) throw new Error("Invalid project.cancel params");
  const run = projectRuns.get(requiredString(params, "runId"));
  if (!run) return { cancelled: false };
  run.killed = true;
  run.process.kill();
  return { cancelled: true };
}

async function handleSpecsList() {
  const dir = join(repoRoot, "specs", "bun");
  const names = (await readdir(dir))
    .filter((name) => name.endsWith(".yml"))
    .sort();
  const specs = [];
  for (const name of names) {
    const file = Bun.file(join(dir, name));
    const head = (await file.text()).split("\n").slice(0, 12).join("\n");
    specs.push({ file: `specs/bun/${name}`, bytes: file.size, head });
  }
  return { specs };
}

async function handleDocsList() {
  const dir = join(repoRoot, "docs");
  const names = (await readdir(dir))
    .filter((name) => name.endsWith(".md"))
    .sort();
  const docs = [];
  for (const name of names) {
    const text = await Bun.file(join(dir, name)).text();
    const title = text.match(/^#\s+(.+)$/m)?.[1] ?? name;
    docs.push({ file: `docs/${name}`, title });
  }
  return { docs };
}

/* ------------------------------------------------------------------ */
/* Dispatch + stdio loop                                               */
/* ------------------------------------------------------------------ */

async function dispatch(request: RpcRequest): Promise<void> {
  const { id, method, params } = request;
  // Streaming methods own their response frame.
  if (method === SidecarMethods.agentStart) {
    await handleAgentStart(id, params);
    return;
  }
  if (method === SidecarMethods.projectRun) {
    await handleProjectRun(id, params);
    return;
  }
  let result: unknown;
  switch (method) {
    case SidecarMethods.hello:
      result = { version: "0.0.0", runtime: "bun", repoRoot };
      break;
    case SidecarMethods.providerResolve:
      result = await resolveProviderView(
        isRecord(params)
          ? optionalString(params, "model") || undefined
          : undefined,
      );
      break;
    case SidecarMethods.providerAdmit:
      result = await handleProviderAdmit(params);
      break;
    case SidecarMethods.ollamaModels:
      result = await handleOllamaModels(params);
      break;
    case SidecarMethods.agentDecide:
      result = handleAgentDecide(params);
      break;
    case SidecarMethods.agentCancel:
      result = handleAgentCancel(params);
      break;
    case SidecarMethods.workspaceRead:
      result = await handleWorkspaceRead(params);
      break;
    case SidecarMethods.workspaceList:
      result = await handleWorkspaceList(params);
      break;
    case SidecarMethods.sessionList:
      result = await handleSessionList(params);
      break;
    case SidecarMethods.sessionCreate:
      result = await handleSessionCreate(params);
      break;
    case SidecarMethods.sessionInfo:
      result = await handleSessionInfo(params);
      break;
    case SidecarMethods.recoveryInfo:
      result = await handleRecoveryInfo(params);
      break;
    case SidecarMethods.mcpStatus:
      result = await handleMcpStatus(params);
      break;
    case SidecarMethods.projectCancel:
      result = handleProjectCancel(params);
      break;
    case SidecarMethods.specsList:
      result = await handleSpecsList();
      break;
    case SidecarMethods.docsList:
      result = await handleDocsList();
      break;
    default:
      throw new Error(`Unknown method: ${method}`);
  }
  writeFrame({ id, ok: true, result });
}

async function main(): Promise<void> {
  const reader = Bun.stdin.stream().getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) break;
    buffer += decoder.decode(chunk.value, { stream: true });
    if (buffer.length > MAX_LINE_BYTES * 4)
      throw new Error("Sidecar input exceeds buffer limit");
    let newline = buffer.indexOf("\n");
    while (newline >= 0) {
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      newline = buffer.indexOf("\n");
      if (!line) continue;
      let request: RpcRequest;
      try {
        request = JSON.parse(line) as RpcRequest;
        if (
          typeof request.id !== "number" ||
          typeof request.method !== "string"
        )
          throw new Error("Malformed request");
      } catch {
        continue;
      }
      if (
        request.method === SidecarMethods.agentStart ||
        request.method === SidecarMethods.projectRun
      ) {
        // Streaming handlers run concurrently: decide/cancel must be served
        // while a run is in flight. They own their response frame.
        dispatch(request).catch((error: unknown) => fail(request.id, error));
        continue;
      }
      try {
        await dispatch(request);
      } catch (error) {
        fail(request.id, error);
      }
    }
  }
}

await main();
