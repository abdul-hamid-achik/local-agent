<template>
  <div class="view">
    <h1>MCP</h1>
    <p class="subtitle">
      Explicitly trusted servers only — stdio lifecycle or loopback HTTP. Status checks
      connect, list tools, then close. Transport success is never domain success.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>
    <div v-if="notice" class="notice-box">{{ notice }}</div>

    <div class="card">
      <div class="field">
        <label>Trusted config (JSON array, max 16 servers, 1 MB)</label>
        <div class="row">
          <input v-model="store.mcpConfig" type="text" placeholder="/path/to/mcp.json" spellcheck="false" />
          <button class="small" @click="pickConfig">Browse</button>
          <button class="small" @click="loadFile">Load</button>
          <button class="small primary" :disabled="!store.mcpConfig.trim()" @click="check">
            Check status
          </button>
        </div>
      </div>
      <div class="field">
        <label>Config editor</label>
        <textarea v-model="editor" rows="8" class="mono" spellcheck="false" />
        <div class="row" style="margin-top: 8px">
          <button class="small" :disabled="!store.mcpConfig.trim()" @click="saveFile">Save</button>
          <button class="small ghost" @click="insertExample">Insert example</button>
          <span class="spacer" />
          <label class="row attach">
            <input v-model="store.mcpAttached" type="checkbox" @change="onAttachChange" />
            Attach to Chat turns
          </label>
        </div>
        <div class="help">
          Saving writes the file above. stdio entries start their commands on check —
          use absolute command paths (inherited env, including PATH, is cleared).
          Remote HTTP requires <code>allowRemote: true</code>. Plan intent denies MCP
          calls, including read-only ones.
        </div>
      </div>
    </div>

    <div v-if="status" class="card">
      <h2>Servers ({{ status.servers.length }})</h2>
      <div v-for="server in status.servers" :key="server.name" class="server">
        <div class="row">
          <span class="badge" :class="server.status === 'connected' ? 'green' : 'red'">
            {{ server.status }}
          </span>
          <strong>{{ server.name }}</strong>
          <span class="badge">{{ server.transport }}</span>
          <span class="dim">{{ server.tools.length }} tools</span>
        </div>
        <table v-if="server.tools.length" class="data">
          <thead>
            <tr>
              <th>Tool</th>
              <th>Description</th>
              <th>Read-only</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="tool in server.tools" :key="tool.name">
              <td class="mono">{{ tool.name }}</td>
              <td>{{ tool.description ?? "—" }}</td>
              <td>{{ tool.readOnly ? "yes" : "no" }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { store } from "#desktop/renderer/store";
import type { McpStatusView } from "#desktop/shared/protocol";

const editor = ref("");
const status = ref<McpStatusView | undefined>();
const error = ref("");
const notice = ref("");

function onAttachChange(): void {
  notice.value = store.mcpAttached
    ? "Config will attach to the next Chat turn."
    : "Config detached — Chat turns will run without MCP servers.";
}

async function pickConfig(): Promise<void> {
  const picked = await api.pick({
    kind: "file",
    filters: [{ name: "JSON", extensions: ["json"] }],
  });
  if (picked) {
    store.mcpConfig = picked;
    await loadFile();
  }
}

async function loadFile(): Promise<void> {
  error.value = "";
  notice.value = "";
  try {
    editor.value = await api.readTextFile(store.mcpConfig.trim());
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

async function saveFile(): Promise<void> {
  error.value = "";
  notice.value = "";
  try {
    JSON.parse(editor.value);
    await api.writeTextFile(store.mcpConfig.trim(), editor.value);
    notice.value = `Saved ${store.mcpConfig.trim()}.`;
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

function insertExample(): void {
  editor.value = JSON.stringify(
    [
      { name: "local", transport: { kind: "http", url: "http://127.0.0.1:3000/mcp" } },
    ],
    null,
    2,
  );
}

async function check(): Promise<void> {
  error.value = "";
  notice.value = "";
  status.value = undefined;
  try {
    const configPath = store.mcpConfig.trim();
    status.value = await api.mcpStatus(configPath);
    if (status.value.servers.some((server) => server.status === "unavailable")) {
      notice.value = "At least one server is unavailable — see details above.";
    }
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}
</script>

<style scoped>
.server {
  border-top: 1px solid var(--border-soft);
  padding: 12px 0 4px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.server:first-of-type {
  border-top: none;
  padding-top: 0;
}

.dim {
  color: var(--text-faint);
  font-size: 12.5px;
}

.attach {
  gap: 6px;
  font-size: 13px;
  color: var(--text-dim);
}

.attach input {
  width: auto;
}

.spacer {
  flex: 1;
}

.help {
  font-size: 12px;
  color: var(--text-faint);
  margin-top: 8px;
}
</style>
