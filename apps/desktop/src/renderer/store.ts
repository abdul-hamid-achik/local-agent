import { reactive } from "vue";
import type { ProviderView } from "#desktop/shared/protocol";

export type ConnectionState = "connecting" | "ready" | "error";

export const store = reactive({
  connection: "connecting" as ConnectionState,
  connectionError: "",
  // An error no component handled (see main.ts); App shows it.
  uiError: "",
  repoRoot: "",
  workspace: "",
  model: "",
  intent: "build" as "plan" | "build",
  sessionDb: "",
  resumeSession: "",
  mcpConfig: "",
  mcpAttached: true,
  provider: undefined as ProviderView | undefined,
  providerSource: "",
});

export function workspaceRoot(): string {
  return store.workspace || store.repoRoot;
}

/**
 * Switches the workspace the views work on. A session handle belongs to the
 * workspace it was made in, so a real change drops it; picking the current
 * workspace again keeps it.
 */
export function changeWorkspace(
  target: { workspace: string; repoRoot: string; resumeSession: string },
  next: string,
): void {
  if (next === (target.workspace || target.repoRoot)) return;
  target.workspace = next;
  target.resumeSession = "";
}
