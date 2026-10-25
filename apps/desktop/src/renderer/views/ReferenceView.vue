<template>
  <div class="view">
    <h1>Reference</h1>
    <p class="subtitle">
      The Bun client's feature surface in one place: presentations, commands, tools,
      limits, and public docs.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="card">
      <h2>Feature map — every capability and where it lives in this app</h2>
      <table class="data">
        <thead><tr><th>Feature</th><th>Status</th><th>Workbench view</th></tr></thead>
        <tbody>
          <tr v-for="feature in features" :key="feature.name">
            <td><strong>{{ feature.name }}</strong><br /><span class="dim">{{ feature.detail }}</span></td>
            <td><span class="badge" :class="feature.done ? 'green' : 'amber'">{{ feature.done ? "live" : "reference" }}</span></td>
            <td><button class="small ghost" @click="navigate(feature.route)">{{ feature.route }}</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="grid-2">
      <div class="card">
        <h2>Presentations</h2>
        <table class="data">
          <tbody>
            <tr><td class="mono">fullscreen</td><td>Alternate-screen TUI, Markdown roles</td></tr>
            <tr><td class="mono">inline</td><td>Native scrollback, split footer</td></tr>
            <tr><td class="mono">headless</td><td>JSON events or plain text + receipts</td></tr>
          </tbody>
        </table>
        <div class="help">
          Chat here mirrors headless semantics (stream + approvals). Run the terminal
          client for pixel-level TUI behavior:
          <code>bun run dev --model MODEL [--mode inline]</code>
        </div>
      </div>
      <div class="card">
        <h2>Slash commands</h2>
        <table class="data">
          <tbody>
            <tr><td class="mono">/help</td><td>Command summary</td></tr>
            <tr><td class="mono">/mode plan|build</td><td>Intent for the next turn</td></tr>
            <tr><td class="mono">/mcp</td><td>Attached server/tool counts</td></tr>
          </tbody>
        </table>
        <div class="help">All three work in the Chat composer, same as the terminal.</div>
      </div>
    </div>

    <div class="card">
      <h2>Tools — what each intent offers, from the runtime</h2>
      <table class="data">
        <thead><tr><th>Tool</th><th>What it does</th><th>plan</th><th>build</th></tr></thead>
        <tbody>
          <tr v-for="tool in reference?.tools ?? []" :key="tool.name">
            <td class="mono">{{ tool.name }}</td>
            <td>{{ tool.summary }}</td>
            <td>{{ tool.readOnly ? "allow" : "deny" }}</td>
            <td>{{ tool.readOnly ? "allow" : "ask" }}</td>
          </tr>
          <tr>
            <td class="mono">SERVER__TOOL</td>
            <td>A tool of a configured MCP server.</td>
            <td>deny</td>
            <td>ask</td>
          </tr>
        </tbody>
      </table>
      <div class="help">
        Serial loop: a complete provider response precedes every host action.
        A build ask is answered by a rule in the workspace's permission file
        when one matches, otherwise by the approval card here (or, in the CLI's
        headless mode, by <code>--approve-edits</code>,
        <code>--approve-commands</code> and <code>--approve-mcp</code>).
      </div>
    </div>

    <div class="card">
      <h2>Bounds &amp; limits</h2>
      <table class="data">
        <tbody>
          <tr v-for="[name, bound] in limits" :key="name">
            <td class="mono">{{ name }}</td>
            <td>{{ bound }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <h2>Public docs ({{ docs.length }})</h2>
      <div class="help">Historical-release scope, served from <code>docs/</code> → local-agent.dev.</div>
      <table class="data">
        <tbody>
          <tr v-for="doc in docs" :key="doc.file">
            <td>{{ doc.title }}</td>
            <td class="mono dim">{{ doc.file }}</td>
            <td>
              <button class="small ghost" @click="openPublished(doc.file)">site ↗</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { limitRows } from "#desktop/renderer/reference";
import { navigate, type RouteName } from "#desktop/renderer/router";
import type { ReferenceView } from "#desktop/shared/protocol";

const features: { name: string; detail: string; done: boolean; route: RouteName }[] = [
  { name: "Agent chat", detail: "streaming turns, thinking, receipts", done: true, route: "chat" },
  { name: "Approvals", detail: "edit diff, argv, MCP args — approve/deny inline", done: true, route: "chat" },
  { name: "Plan / build intents", detail: "read-only plan vs gated build", done: true, route: "chat" },
  { name: "Sessions", detail: "SQLite create/list/resume/inspect", done: true, route: "sessions" },
  { name: "Recovery", detail: "hazard review tokens, never auto-replay", done: true, route: "recovery" },
  { name: "Workspace", detail: "fd-pinned bounded reads", done: true, route: "workspace" },
  { name: "MCP", detail: "stdio/HTTP trust, status, tool catalog", done: true, route: "mcp" },
  { name: "Provider admission", detail: "inventory, weight, context evidence", done: true, route: "providers" },
  { name: "Chalupa profile", detail: "process-scoped remote config", done: true, route: "providers" },
  { name: "Project checks", detail: "check, test, builds, git", done: true, route: "project" },
  { name: "Contracts", detail: "Glyphrun loopback specs inventory", done: true, route: "project" },
  { name: "Fullscreen TUI", detail: "terminal-only; command provided", done: false, route: "reference" },
  { name: "Inline TUI", detail: "terminal-only; command provided", done: false, route: "reference" },
];

const docs = ref<{ file: string; title: string }[]>([]);
const reference = ref<ReferenceView>();
const limits = computed(() =>
  reference.value ? limitRows(reference.value.limits) : [],
);
const error = ref("");

function openPublished(file: string): void {
  const slug = file.replace(/^docs\//, "").replace(/\.md$/, "");
  const page = slug === "index" ? "" : `/${slug}`;
  void api.openExternal(`https://local-agent.dev${page}`).catch((error_: unknown) => {
    error.value = errorMessage(error_);
  });
}

// Each table loads on its own: one failing leaves the other.
onMounted(() => {
  const failed = (error_: unknown) => {
    error.value = errorMessage(error_);
  };
  api.docsList().then((list) => {
    docs.value = list.docs;
  }, failed);
  api.reference().then((value) => {
    reference.value = value;
  }, failed);
});
</script>

<style scoped>
.dim {
  color: var(--text-faint);
  font-size: 12px;
}

.help {
  font-size: 12px;
  color: var(--text-faint);
  margin-top: 8px;
}
</style>
