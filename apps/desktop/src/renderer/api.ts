import type { DesktopBridge } from "#desktop/main/preload";
import type {
  AdmissionView,
  AgentStreamEvent,
  ChatMessage,
  McpStatusView,
  ProjectCommandName,
  ProjectStreamEvent,
  ProviderView,
  RecoveryView,
  ReferenceView,
  SessionView,
  StreamEnvelope,
  WorkspaceEntry,
} from "#desktop/shared/protocol";
import { SidecarMethods } from "#desktop/shared/protocol";

function bridge(): DesktopBridge {
  const exposed = (window as unknown as { desktop?: DesktopBridge }).desktop;
  if (!exposed) throw new Error("Desktop bridge is unavailable");
  return exposed;
}

/**
 * Calls the sidecar. The params reach it as one JSON line, so they are sent
 * as that JSON data: Electron's IPC cannot clone a Proxy, and Vue state
 * (the chat transcript) is one.
 */
export async function rpc<T>(method: string, params?: unknown): Promise<T> {
  const data =
    params === undefined ? undefined : JSON.parse(JSON.stringify(params));
  return (await bridge().invoke(method, data)) as T;
}

export type StreamCallback = (
  event: AgentStreamEvent | ProjectStreamEvent,
) => void;

const streamSubs = new Map<string, Set<StreamCallback>>();
let streamWired = false;

function ensureStreamWired(): void {
  if (streamWired) return;
  // Only once the bridge took it: a missing bridge must not leave the
  // stream "wired" for a later subscriber.
  bridge().onStream((envelope: StreamEnvelope) => {
    const subs = streamSubs.get(envelope.stream);
    if (!subs) return;
    for (const callback of subs) {
      callback(envelope.event as AgentStreamEvent | ProjectStreamEvent);
    }
  });
  streamWired = true;
}

export function subscribeStream(
  stream: string,
  callback: StreamCallback,
): () => void {
  ensureStreamWired();
  let subs = streamSubs.get(stream);
  if (!subs) {
    subs = new Set();
    streamSubs.set(stream, subs);
  }
  subs.add(callback);
  let subscribed = true;
  return () => {
    // A second call must not reach a Set that a later subscriber now owns.
    if (!subscribed) return;
    subscribed = false;
    subs.delete(callback);
    if (subs.size === 0 && streamSubs.get(stream) === subs)
      streamSubs.delete(stream);
  };
}

export const api = {
  hello: () =>
    rpc<{ version: string; runtime: string; repoRoot: string }>(
      SidecarMethods.hello,
    ),
  providerResolve: (model?: string) =>
    rpc<ProviderView>(SidecarMethods.providerResolve, { model }),
  providerAdmit: (model?: string) =>
    rpc<AdmissionView>(SidecarMethods.providerAdmit, { model }),
  ollamaModels: (model?: string) =>
    rpc<{ endpoint: string; models: { name: string; size: number }[] }>(
      SidecarMethods.ollamaModels,
      { model },
    ),
  agentStart: (params: {
    runId: string;
    messages: ChatMessage[];
    workspace: string;
    intent: "plan" | "build";
    model?: string;
    sessionDb?: string;
    resume?: string;
    mcpConfig?: string;
  }) =>
    rpc<{ runId: string; sessionId?: string }>(
      SidecarMethods.agentStart,
      params,
    ),
  agentDecide: (runId: string, token: string, allowed: boolean) =>
    rpc<{ decided: boolean }>(SidecarMethods.agentDecide, {
      runId,
      token,
      allowed,
    }),
  agentCancel: (runId: string) =>
    rpc<{ cancelled: boolean }>(SidecarMethods.agentCancel, { runId }),
  workspaceRead: (workspace: string, path: string) =>
    rpc<{ text: string; bytes: number }>(SidecarMethods.workspaceRead, {
      workspace,
      path,
    }),
  workspaceList: (workspace: string, dir?: string) =>
    rpc<{ dir: string; entries: WorkspaceEntry[]; truncated: boolean }>(
      SidecarMethods.workspaceList,
      { workspace, dir },
    ),
  sessionList: (db: string, workspace: string) =>
    rpc<{ sessions: { id: string; revision: number; messages: number }[] }>(
      SidecarMethods.sessionList,
      { db, workspace },
    ),
  sessionCreate: (db: string, workspace: string) =>
    rpc<{ id: string; revision: number }>(SidecarMethods.sessionCreate, {
      db,
      workspace,
    }),
  sessionInfo: (db: string, workspace: string, id: string) =>
    rpc<{
      session: SessionView;
      hazards: {
        id: number;
        execution_id: string;
        turn_id: string;
        tool_name: string;
        event_type: string;
        effect_class: string;
      }[];
    }>(SidecarMethods.sessionInfo, { db, workspace, id }),
  recoveryInfo: (db: string, workspace: string, id: string) =>
    rpc<RecoveryView>(SidecarMethods.recoveryInfo, { db, workspace, id }),
  mcpStatus: (configPath: string) =>
    rpc<McpStatusView>(SidecarMethods.mcpStatus, { configPath }),
  projectRun: (name: ProjectCommandName, runId: string) =>
    rpc<{ runId: string }>(SidecarMethods.projectRun, { name, runId }),
  projectCancel: (runId: string) =>
    rpc<{ cancelled: boolean }>(SidecarMethods.projectCancel, { runId }),
  specsList: () =>
    rpc<{ specs: { file: string; bytes: number; head: string }[] }>(
      SidecarMethods.specsList,
    ),
  docsList: () =>
    rpc<{ docs: { file: string; title: string }[] }>(SidecarMethods.docsList),
  reference: () => rpc<ReferenceView>(SidecarMethods.reference),

  // Async so a missing bridge rejects, as rpc() does, instead of throwing
  // before a promise exists.
  repoRoot: async () => bridge().repoRoot(),
  restartSidecar: async () => bridge().restartSidecar(),
  setProviderEnv: async (env: Record<string, string>) =>
    bridge().setProviderEnv(env),
  getProviderEnv: async () => bridge().getProviderEnv(),
  pick: async (options: {
    kind: "directory" | "file";
    filters?: { name: string; extensions: string[] }[];
  }) => bridge().pick(options),
  // The session database picker of the Sessions and Recovery views.
  pickSessionDb: async () =>
    bridge().pick({
      kind: "file",
      filters: [{ name: "SQLite", extensions: ["db", "sqlite", "sqlite3"] }],
    }),
  readTextFile: async (path: string) => bridge().readTextFile(path),
  writeTextFile: async (path: string, text: string) =>
    bridge().writeTextFile(path, text),
  openExternal: async (url: string) => bridge().openExternal(url),
  // These two return an unsubscribe, not a promise, so they stay synchronous.
  onSidecarExit: (callback: (code: number | null, message?: string) => void) =>
    bridge().onSidecarExit(callback),
  onSidecarRestart: (callback: () => void) =>
    bridge().onSidecarRestart(callback),
};

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  // A rejection with a reason of its own keeps it: a string, or an object
  // carrying a message. Nothing else is shown.
  if (typeof error === "string" && error) return error;
  if (typeof error === "object" && error !== null && "message" in error) {
    const { message } = error;
    if (typeof message === "string" && message) return message;
  }
  return "Request failed";
}

export function randomRunId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto)
    return crypto.randomUUID();
  return `run-${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
}
