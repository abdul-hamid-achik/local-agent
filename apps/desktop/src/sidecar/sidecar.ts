/**
 * Bun sidecar: the only desktop process that imports @local-agent/runtime.
 * Speaks framed NDJSON JSON-RPC over stdio (see #desktop/shared/protocol).
 *
 * Run from the repository root: `bun apps/desktop/src/sidecar/sidecar.ts`.
 */
import "#desktop/sidecar/frames-only";
import { randomUUID } from "node:crypto";
import { readdir } from "node:fs/promises";
import { join, relative, resolve } from "node:path";
import {
  type ApprovalRequest,
  admitOllamaModel,
  commandLine,
  connectMcpServers,
  inspectRecovery,
  LIMITS,
  type McpConnection,
  type Provider,
  planContext,
  readMcpConfig,
  resolveProvider,
  runAgent,
  type SessionContext,
  SessionStore,
  secretPathNotes,
  shellWord,
  toolReference,
  Workspace,
  workspaceRules,
} from "@local-agent/runtime";
import {
  type AdmissionView,
  type AgentStreamEvent,
  type ChatMessage,
  type McpStatusView,
  type ProjectCommandName,
  ProjectCommands,
  type ProviderView,
  type ReferenceView,
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

// Set once the desktop stops reading: a write would only fail again.
let stdoutClosed = false;

function writeFrame(frame: RpcResponse | StreamEnvelope): void {
  if (stdoutClosed) return;
  process.stdout.write(`${JSON.stringify(frame)}\n`);
}

function fail(id: number, error: unknown): void {
  writeFrame({
    id,
    ok: false,
    error: error instanceof Error ? error.message : "Sidecar request failed",
  });
}

/**
 * A command's secretPaths as the card's fields (see secretPathNotes), each
 * path as a shell word, as the command line shows it.
 */
function secretFields(secretPaths: readonly string[] | undefined): {
  secret_paths?: string[];
  unchecked_path_words?: number;
} {
  const { paths, unchecked } = secretPathNotes(secretPaths);
  return {
    ...(paths.length ? { secret_paths: paths.map(shellWord) } : {}),
    ...(unchecked === undefined ? {} : { unchecked_path_words: unchecked }),
  };
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

/** The last plan per endpoint and model: a run whose look at the model fails keeps it. */
const plans = new Map<string, Provider>();

/**
 * The provider with the CLI's planned context size (see planContext). A
 * plan is kept per endpoint and model, so a run whose look at the model
 * fails reuses the last one instead of falling back to 16384 for one run.
 */
async function plannedProvider(
  model: string | undefined,
  signal: AbortSignal,
): Promise<Provider> {
  const resolved = resolveProvider(process.env, model);
  const key = `${resolved.base_url}\n${resolved.model}`;
  const provider = await planContext(resolved, signal);
  if (provider.context_plan) plans.set(key, provider);
  else if (
    provider === resolved &&
    resolved.type === "ollama" &&
    !resolved.context_explicit
  ) {
    const last = plans.get(key);
    if (last)
      return {
        ...resolved,
        context_size: last.context_size,
        context_plan: last.context_plan,
      };
  }
  return provider;
}

async function resolveProviderView(model?: string): Promise<ProviderView> {
  const provider = await plannedProvider(model, AbortSignal.timeout(15_000));
  return {
    type: provider.type,
    base_url: provider.base_url,
    model: provider.model,
    context_size: provider.context_size,
    source:
      process.env.LOCAL_AGENT_PROVIDER === "chalupa"
        ? "chalupa"
        : process.env.LOCAL_AGENT_PROVIDER === "openai_local"
          ? "openai_local"
          : "local",
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

  let workspace: Workspace | undefined;
  let store: SessionStore | undefined;
  const mcp: McpConnection[] = [];
  // Until agent.start is answered, a failure is its answer (the caller's
  // dispatch writes it); after that, the run's stream reports it.
  let answered = false;
  try {
    const provider = await plannedProvider(model, controller.signal);
    workspace = await Workspace.create(workspaceRoot);
    store = sessionDb ? await SessionStore.open(sessionDb) : undefined;
    if (mcpConfigPath) {
      // The servers connect at once, as in the CLI; the user named this
      // file for the run, so one that does not start is the run's failure.
      const startup = await connectMcpServers(
        await readMcpConfig(mcpConfigPath),
        controller.signal,
      );
      mcp.push(...startup.connected.map(({ connection }) => connection));
      if (startup.failed.length)
        throw new Error(
          startup.failed
            .map((server) => `MCP server ${server.name}: ${server.error}`)
            .join("; "),
        );
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
    answered = true;

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
              command_line: commandLine(proposal.argv),
              timeout_ms: proposal.timeoutMs,
              cwd: proposal.cwd,
              ...secretFields(proposal.secretPaths),
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
              ...(proposal.inner && proposal.gateway
                ? { inner: proposal.inner, gateway: proposal.gateway }
                : {}),
            },
          });
        }
      });

    // The workspace's permission file applies here as in every client:
    // deny refuses before any card, a trusted allow skips it.
    const fileRules = workspaceRules(workspace.root);
    for await (const event of runAgent(provider, messages, {
      workspace,
      intent,
      approve: async (proposal, signal) => {
        signal.throwIfAborted();
        const verdict = await fileRules.verdict(proposal);
        signal.throwIfAborted();
        if (verdict.effect !== "ask") return verdict.effect === "allow";
        return approve(proposal);
      },
      deniedRead: fileRules.deniedRead,
      signal: controller.signal,
      session,
      mcp,
    })) {
      emitStream(runId, event as AgentStreamEvent);
    }
    emitStream(runId, { type: "run_done" });
  } catch (error) {
    if (!answered)
      throw controller.signal.aborted ? new Error("Run cancelled") : error;
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
    await Promise.allSettled(mcp.map((connection) => connection.close()));
    store?.close();
    workspace?.close();
    // Last, so a shutdown that waits for the runs waits for this cleanup.
    activeRuns.delete(runId);
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

function cancelRun(run: ActiveRun): void {
  run.controller.abort();
  run.pending?.resolve(false);
  run.pending = undefined;
}

function handleAgentCancel(params: unknown): { cancelled: boolean } {
  if (!isRecord(params)) throw new Error("Invalid agent.cancel params");
  const run = activeRuns.get(requiredString(params, "runId"));
  if (!run) return { cancelled: false };
  cancelRun(run);
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
  // What the model's list shows: no secrets, and a link (a broken one
  // too) is an entry of its own, never followed.
  using workspace = await Workspace.create(root);
  const { entries, truncated } = await workspace.listEntries(dir);
  return { dir: relative(root, resolve(root, dir)), entries, truncated };
}

async function handleSessionList(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid session.list params");
  const root = resolveWorkspaceRoot(requiredString(params, "workspace"));
  const store = await SessionStore.open(requiredString(params, "db"));
  try {
    const sessions = await store.listSessionSummaries(root);
    return {
      sessions: sessions.map((session) => ({
        id: session.publicId,
        revision: session.revision,
        messages: session.messageCount,
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

/** Status checks in flight: a shutdown cancels them and waits for them. */
let statusChecks = 0;

async function handleMcpStatus(params: unknown) {
  if (!isRecord(params)) throw new Error("Invalid mcp.status params");
  const configs = await readMcpConfig(requiredString(params, "configPath"));
  statusChecks++;
  let startup: Awaited<ReturnType<typeof connectMcpServers>>;
  try {
    // Cancelled by a shutdown, which then closes what had connected.
    startup = await connectMcpServers(configs, stopping.signal);
  } finally {
    statusChecks--;
  }
  await Promise.allSettled(
    startup.connected.map(({ connection }) => connection.close()),
  );
  const servers: McpStatusView["servers"] = configs.map((config) => {
    const connected = startup.connected.find(
      (server) => server.name === config.name,
    )?.connection;
    const failed = startup.failed.find((server) => server.name === config.name);
    return {
      name: config.name,
      transport: config.transport.kind,
      status: connected ? "connected" : "unavailable",
      tools:
        connected?.tools.map((tool) => ({
          name: tool.name,
          description: tool.description,
          readOnly: tool.readOnly,
        })) ?? [],
      ...(failed ? { error: failed.error } : {}),
    };
  });
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
/** How long a stopped project command's group has before SIGKILL. */
const PROJECT_KILL_GRACE_MS = 2_000;

/** Signals a process group; false when no such group is left. */
function signalGroup(pid: number, signal: NodeJS.Signals | 0): boolean {
  try {
    process.kill(-pid, signal);
    return true;
  } catch (error) {
    // EPERM still means the group exists.
    return error instanceof Error && "code" in error && error.code === "EPERM";
  }
}

/**
 * Stops a project command with what it started (`bun run test` runs its
 * workers in the same group): SIGTERM, then SIGKILL for a group that is
 * still there after the grace.
 */
function stopProjectRun(run: ProjectRun): void {
  run.killed = true;
  const pid = run.process.pid;
  if (!signalGroup(pid, "SIGTERM")) return;
  setTimeout(() => {
    if (signalGroup(pid, 0)) signalGroup(pid, "SIGKILL");
  }, PROJECT_KILL_GRACE_MS).unref();
}

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
    // Its own group, so a cancel reaches what it started.
    detached: true,
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
  stopProjectRun(run);
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

/**
 * The Reference view's tools and bounds, from the runtime. This host's runs
 * give runAgent neither ask nor goal, so ask_user and goal_status are left out.
 */
function handleReference(): ReferenceView {
  return { tools: toolReference({ ask: false, goal: false }), limits: LIMITS };
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
  // A request read before the shutdown but served after it starts nothing
  // the shutdown would not stop. A run's controls still answer: the runs
  // are being cancelled anyway.
  if (shuttingDown && !CONTROLS.has(method))
    throw new Error("The sidecar is shutting down");
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
    case SidecarMethods.reference:
      result = handleReference();
      break;
    default:
      throw new Error(`Unknown method: ${method}`);
  }
  writeFrame({ id, ok: true, result });
}

/** A run's controls: answered at once, never behind a slow request. */
const CONTROLS = new Set<string>([
  SidecarMethods.agentDecide,
  SidecarMethods.agentCancel,
  SidecarMethods.projectCancel,
]);

/** Requests waiting their turn; past it a request is refused at once. */
const MAX_QUEUED = 64;

/** How long a shutdown waits for cancelled runs to close what they opened. */
const SHUTDOWN_GRACE_MS = 2_000;
let shuttingDown = false;
const stopping = new AbortController();

/**
 * Ends the sidecar: on SIGTERM, SIGINT or SIGHUP (the desktop's stop),
 * when stdin ends or stdout breaks (the desktop went away). Runs are
 * cancelled as agent.cancel does, project commands get SIGTERM with their
 * groups, and the exit comes once the runs have closed their MCP servers
 * and stores and the commands have ended, or after the grace, when what is
 * left of those groups gets SIGKILL. At exit the runtime kills any command
 * group of a run still live.
 */
function shutdown(): void {
  if (shuttingDown) return;
  shuttingDown = true;
  stopping.abort(new Error("The sidecar is shutting down"));
  for (const run of activeRuns.values()) cancelRun(run);
  const groups = [...projectRuns.values()];
  for (const run of groups) stopProjectRun(run);
  const deadline = Date.now() + SHUTDOWN_GRACE_MS;
  const poll = setInterval(() => {
    const running =
      activeRuns.size > 0 || projectRuns.size > 0 || statusChecks > 0;
    if (running && Date.now() < deadline) return;
    clearInterval(poll);
    for (const run of groups)
      if (signalGroup(run.process.pid, 0))
        signalGroup(run.process.pid, "SIGKILL");
    process.exit();
  }, 25);
}

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"] as const)
  process.on(signal, shutdown);
process.stdout.on("error", () => {
  stdoutClosed = true;
  shutdown();
});

async function main(): Promise<void> {
  const reader = Bun.stdin.stream().getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  // Requests take effect in the order they came, each after the one before
  // it. A run starts in its turn but does not hold the line (its stream
  // owns its answer), and a run's controls are answered at once: a decide
  // or a cancel is served while a run, or a slow request (mcp.status
  // connects every server), is in flight.
  let ordered = Promise.resolve();
  let queued = 0;
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
      } catch {
        continue;
      }
      if (typeof request?.method !== "string") {
        // An id gets its answer, so the caller does not wait for one.
        if (typeof request?.id === "number")
          fail(request.id, new Error("Malformed request"));
        continue;
      }
      if (typeof request.id !== "number") continue;
      const answer = () =>
        dispatch(request).catch((error: unknown) => fail(request.id, error));
      if (CONTROLS.has(request.method)) {
        void answer();
        continue;
      }
      // Bounded without pausing the reader, so a control behind a full
      // queue is still read and answered.
      if (queued >= MAX_QUEUED) {
        fail(request.id, new Error("Too many requests waiting"));
        continue;
      }
      const streaming =
        request.method === SidecarMethods.agentStart ||
        request.method === SidecarMethods.projectRun;
      queued++;
      ordered = ordered.then(() => {
        queued--;
        if (!streaming) return answer();
        void answer();
      });
    }
  }
}

try {
  await main();
} catch (error) {
  console.error(error);
  process.exitCode = 1;
}
// stdin ended (or could not be read): no one is left to read an answer.
shutdown();
