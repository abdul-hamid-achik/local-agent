import { reactive } from "vue";
import type { ProviderView } from "#desktop/shared/protocol";

export type ConnectionState = "connecting" | "ready" | "error";

export const store = reactive({
  connection: "connecting" as ConnectionState,
  connectionError: "",
  repoRoot: "",
  workspace: "",
  model: "",
  intent: "build" as "plan" | "build",
  sessionDb: "",
  resumeSession: "",
  mcpConfig: "",
  mcpAttached: true,
  provider: undefined as ProviderView | undefined,
  providerSource: "" as string,
});

export function workspaceRoot(): string {
  return store.workspace || store.repoRoot;
}
