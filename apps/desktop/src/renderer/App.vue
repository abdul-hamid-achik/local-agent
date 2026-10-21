<template>
  <div class="shell">
    <aside class="sidenav">
      <div class="brand">
        <svg class="brand-mark" width="30" height="30" viewBox="0 0 48 48" aria-hidden="true">
          <rect x="1" y="1" width="46" height="46" rx="12" fill="#151916" stroke="#B85F3D" stroke-width="2" />
          <path d="M13 14.5 22 24l-9 9.5" fill="none" stroke="#D6E3A5" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M27 33.5h9" fill="none" stroke="#D6E3A5" stroke-width="3" stroke-linecap="round" />
          <circle cx="35" cy="14" r="3" fill="#B85F3D" />
        </svg>
        <div>
          <div class="brand-title">Local Agent</div>
          <div class="brand-sub">Workbench</div>
        </div>
      </div>
      <nav>
        <button
          v-for="route in routes"
          :key="route.name"
          class="nav-item"
          :class="{ active: currentRoute === route.name }"
          :title="route.hint"
          @click="navigate(route.name)"
        >
          <span class="nav-label">{{ route.label }}</span>
          <span class="nav-hint">{{ route.hint }}</span>
        </button>
      </nav>
      <div class="sidenav-foot">
        <span class="badge" :class="connectionBadge">{{ connectionText }}</span>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <div class="topbar-group">
          <label class="topbar-field">
            <span>Workspace</span>
            <span class="mono topbar-value" :title="store.workspace || store.repoRoot">
              {{ shortPath(store.workspace || store.repoRoot) }}
            </span>
          </label>
          <button class="small ghost" @click="pickWorkspace">Change</button>
        </div>
        <div class="topbar-group">
          <label class="topbar-field">
            <span>Model</span>
            <input
              v-model="store.model"
              type="text"
              placeholder="qwen3.5:2b"
              class="topbar-input"
              spellcheck="false"
            />
          </label>
          <label class="topbar-field">
            <span>Intent</span>
            <select v-model="store.intent" class="topbar-input">
              <option value="build">build</option>
              <option value="plan">plan</option>
            </select>
          </label>
        </div>
      </header>
      <main class="content">
        <div v-if="store.connection === 'error'" class="view">
          <div class="error-box">
            {{ store.connectionError }}
            <div class="row" style="margin-top: 10px">
              <button class="small" @click="reconnect">Restart sidecar</button>
            </div>
          </div>
        </div>
        <component :is="activeView" v-else />
      </main>
      <footer class="statusbar">
        <span class="mono">{{ shortPath(store.repoRoot) || "…" }}</span>
        <span class="status-sep">·</span>
        <span v-if="store.provider">
          {{ store.provider.type }} · {{ store.provider.model }} ·
          ctx {{ store.provider.context_size }}
        </span>
        <span v-else class="dim">provider unresolved</span>
        <span class="status-sep">·</span>
        <span>intent {{ store.intent }}</span>
        <span class="spacer" />
        <span v-if="store.sessionDb" class="mono dim">
          db {{ shortPath(store.sessionDb) }}
        </span>
        <span v-else class="dim">no session db</span>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { currentRoute, navigate, routes } from "#desktop/renderer/router";
import { store } from "#desktop/renderer/store";

const views = {
  chat: defineAsyncComponent(() => import("#desktop/renderer/views/ChatView.vue")),
  sessions: defineAsyncComponent(() => import("#desktop/renderer/views/SessionsView.vue")),
  recovery: defineAsyncComponent(() => import("#desktop/renderer/views/RecoveryView.vue")),
  workspace: defineAsyncComponent(
    () => import("#desktop/renderer/views/WorkspaceView.vue"),
  ),
  mcp: defineAsyncComponent(() => import("#desktop/renderer/views/McpView.vue")),
  providers: defineAsyncComponent(
    () => import("#desktop/renderer/views/ProvidersView.vue"),
  ),
  project: defineAsyncComponent(() => import("#desktop/renderer/views/ProjectView.vue")),
  reference: defineAsyncComponent(
    () => import("#desktop/renderer/views/ReferenceView.vue"),
  ),
} as const;

const activeView = computed(() => views[currentRoute.value]);

const connectionText = computed(() =>
  store.connection === "ready"
    ? "sidecar ready"
    : store.connection === "connecting"
      ? "connecting…"
      : "sidecar error",
);

const connectionBadge = computed(() =>
  store.connection === "ready" ? "green" : store.connection === "connecting" ? "blue" : "red",
);

function shortPath(path: string): string {
  if (!path) return "";
  const home = "/Users/";
  if (path.startsWith(home)) {
    const rest = path.slice(home.length).split("/");
    if (rest.length > 2) return `~/${rest.slice(1).join("/")}`;
  }
  return path.length > 48 ? `…${path.slice(-47)}` : path;
}

async function pickWorkspace(): Promise<void> {
  const picked = await api.pick({ kind: "directory" });
  if (picked) store.workspace = picked;
}

async function connect(): Promise<void> {
  store.connection = "connecting";
  store.connectionError = "";
  try {
    const hello = await api.hello();
    store.repoRoot = hello.repoRoot;
    if (!store.workspace) store.workspace = hello.repoRoot;
    store.connection = "ready";
  } catch (error) {
    store.connection = "error";
    store.connectionError = errorMessage(error);
  }
}

async function reconnect(): Promise<void> {
  try {
    await api.restartSidecar();
  } catch {
    // Restart races a dead sidecar; reconnect anyway.
  }
  await connect();
}

onMounted(() => {
  void connect();
  api.onSidecarExit(() => {
    store.connection = "error";
    store.connectionError = "Sidecar exited. Restart it to continue.";
  });
});
</script>

<style scoped>
.shell {
  display: flex;
  height: 100%;
}

.sidenav {
  width: 228px;
  flex-shrink: 0;
  background: var(--bg-soft);
  border-right: 1px solid var(--border-soft);
  display: flex;
  flex-direction: column;
  padding: 16px 12px;
}

.brand {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 2px 8px 16px;
}

.brand-mark {
  flex-shrink: 0;
  border-radius: 7px;
}

.brand-title {
  font-weight: 700;
  font-size: 14.5px;
  letter-spacing: -0.01em;
}

.brand-sub {
  font-size: 11.5px;
  color: var(--text-faint);
  text-transform: uppercase;
  letter-spacing: 0.12em;
}

nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
}

.nav-item {
  background: transparent;
  border: 1px solid transparent;
  text-align: left;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.nav-item:hover {
  background: var(--bg-raised);
}

.nav-item.active {
  background: var(--accent-soft);
  border-color: rgba(88, 166, 255, 0.3);
}

.nav-label {
  font-weight: 600;
  font-size: 13.5px;
}

.nav-item.active .nav-label {
  color: var(--accent);
}

.nav-hint {
  font-size: 11px;
  color: var(--text-faint);
}

.sidenav-foot {
  padding: 12px 8px 0;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.topbar {
  display: flex;
  gap: 18px;
  align-items: center;
  justify-content: space-between;
  padding: 10px 22px;
  border-bottom: 1px solid var(--border-soft);
  background: var(--bg-soft);
  flex-wrap: wrap;
}

.topbar-group {
  display: flex;
  gap: 10px;
  align-items: center;
}

.topbar-field {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 12px;
  color: var(--text-faint);
  font-weight: 600;
}

.topbar-value {
  color: var(--text-dim);
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.topbar-input {
  width: auto;
  padding: 4px 8px;
  font-size: 12.5px;
}

input.topbar-input {
  width: 170px;
}

.content {
  flex: 1;
  overflow-y: auto;
}

.statusbar {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 6px 22px;
  border-top: 1px solid var(--border-soft);
  font-size: 12px;
  color: var(--text-dim);
  background: var(--bg-soft);
}

.statusbar .dim {
  color: var(--text-faint);
}

.status-sep {
  color: var(--text-faint);
}

.spacer {
  flex: 1;
}
</style>
