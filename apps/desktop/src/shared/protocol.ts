/** Framed JSON-RPC between Electron main and the Bun sidecar (NDJSON stdio). */

export type RpcRequest = {
  id: number;
  method: string;
  params?: unknown;
};

export type RpcResponse =
  | { id: number; ok: true; result: unknown }
  | { id: number; ok: false; error: string };

export type StreamEnvelope = {
  stream: string;
  event: unknown;
};

export function isRpcResponse(value: unknown): value is RpcResponse {
  if (typeof value !== "object" || value === null) return false;
  const record = value as Record<string, unknown>;
  return typeof record.id === "number" && typeof record.ok === "boolean";
}

export function isStreamEnvelope(value: unknown): value is StreamEnvelope {
  if (typeof value !== "object" || value === null) return false;
  const record = value as Record<string, unknown>;
  return typeof record.stream === "string" && "event" in record;
}

/** Methods served by the sidecar. Keep in sync with sidecar dispatch. */
export const SidecarMethods = {
  hello: "hello",
  providerResolve: "provider.resolve",
  providerAdmit: "provider.admit",
  ollamaModels: "ollama.models",
  agentStart: "agent.start",
  agentDecide: "agent.decide",
  agentCancel: "agent.cancel",
  workspaceRead: "workspace.read",
  workspaceList: "workspace.list",
  sessionList: "session.list",
  sessionCreate: "session.create",
  sessionInfo: "session.info",
  recoveryInfo: "recovery.info",
  mcpStatus: "mcp.status",
  projectRun: "project.run",
  projectCancel: "project.cancel",
  specsList: "specs.list",
  docsList: "docs.list",
} as const;

export type ProviderView = {
  type: "ollama" | "ollama_remote";
  base_url: string;
  model: string;
  context_size: number;
  source: "local" | "chalupa";
};

export type AdmissionView =
  | { status: "unverified"; kind: "transport"; message: string }
  | { status: "rejected"; kind: "metadata" | "policy"; message: string }
  | {
      status: "validated";
      model: string;
      sizeBytes: number;
      nativeContext: number | undefined;
      capabilities: string[];
    };

export type ChatMessage = {
  role: "system" | "user" | "assistant" | "tool";
  content: string;
  thinking?: string;
  tool_calls?: Array<{
    id?: string;
    function: { name: string; arguments: Record<string, unknown> };
  }>;
  tool_name?: string;
  tool_call_id?: string;
};

export type AgentStreamEvent =
  | { type: "text"; text: string }
  | { type: "thinking"; text: string }
  | {
      type: "tool_call";
      call: {
        id?: string;
        function: { name: string; arguments: Record<string, unknown> };
      };
    }
  | { type: "done"; inputTokens: number; outputTokens: number }
  | { type: "message"; message: ChatMessage }
  | { type: "tool_requested"; id: string; name: string }
  | { type: "tool_start"; id: string; name: string }
  | {
      type: "tool_result";
      id: string;
      name: string;
      status:
        | "succeeded"
        | "denied"
        | "failed"
        | "outcome_unknown"
        | "unverified";
      output: string;
    }
  | { type: "turn_complete"; messages: ChatMessage[] }
  | {
      type: "approval_requested";
      token: string;
      proposal:
        | {
            kind: "edit";
            path: string;
            before: string;
            after: string;
            diff: string;
          }
        | {
            kind: "write";
            path: string;
            created: boolean;
            before: string;
            after: string;
            diff: string;
          }
        | { kind: "command"; argv: string[]; timeout_ms: number; cwd: string }
        | { kind: "mcp"; name: string; argumentsJson: string };
    }
  | { type: "run_error"; message: string }
  | { type: "run_done" };

export type SessionView = {
  id: string;
  revision: number;
  messages: ChatMessage[];
};

export type HazardView = {
  id: number;
  execution_id: string;
  turn_id: string;
  tool_name: string;
  event_type: string;
  effect_class: string;
};

export type RecoveryView = {
  session: string;
  workspace: string;
  revision: number;
  hazards: Array<{
    eventId: number;
    executionId: string;
    turnId: string;
    toolName: string;
    eventType: string;
    effectClass: string;
    argumentsSha256: string;
  }>;
};

export type WorkspaceEntry = {
  name: string;
  kind: "file" | "directory";
  size: number;
};

export type McpStatusView = {
  servers: Array<{
    name: string;
    transport: string;
    status: "connected" | "unavailable";
    tools: Array<{ name: string; description?: string; readOnly: boolean }>;
  }>;
};

export type ProjectStreamEvent =
  | { type: "stdout"; text: string }
  | { type: "stderr"; text: string }
  | { type: "exit"; code: number };

/** Allowlisted repo commands the Project view may run. Enforced in the sidecar. */
export const ProjectCommands = {
  check: ["bun", "run", "check"],
  test: ["bun", "run", "test"],
  typecheck: ["bun", "run", "typecheck"],
  lint: ["bun", "run", "lint"],
  build: ["bun", "run", "build"],
  "site:build": ["bun", "run", "site:build"],
  "test:integration": ["bun", "run", "test:integration"],
  "git:status": ["git", "status", "--short", "--branch"],
  "git:log": ["git", "log", "--oneline", "-20"],
} as const;

export type ProjectCommandName = keyof typeof ProjectCommands;
