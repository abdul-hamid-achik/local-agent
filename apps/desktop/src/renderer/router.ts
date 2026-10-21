import { ref } from "vue";

export type RouteName =
  | "chat"
  | "sessions"
  | "recovery"
  | "workspace"
  | "mcp"
  | "providers"
  | "project"
  | "reference";

export const routes: { name: RouteName; label: string; hint: string }[] = [
  { name: "chat", label: "Chat", hint: "Agent turns, tools, approvals" },
  { name: "sessions", label: "Sessions", hint: "Durable SQLite sessions" },
  { name: "recovery", label: "Recovery", hint: "Uncertain-outcome review" },
  { name: "workspace", label: "Workspace", hint: "Bounded file review" },
  { name: "mcp", label: "MCP", hint: "Servers, tools, trust" },
  { name: "providers", label: "Providers", hint: "Ollama admission, Chalupa" },
  { name: "project", label: "Project", hint: "Checks, contracts, git" },
  { name: "reference", label: "Reference", hint: "Modes, commands, docs" },
];

const valid = new Set(routes.map((route) => route.name));

function fromHash(): RouteName {
  const name = window.location.hash.replace(/^#\/?/, "");
  return (valid.has(name as RouteName) ? name : "chat") as RouteName;
}

export const currentRoute = ref<RouteName>(fromHash());

window.addEventListener("hashchange", () => {
  currentRoute.value = fromHash();
});

export function navigate(name: RouteName): void {
  window.location.hash = `#/${name}`;
}
