<template>
  <div class="view">
    <h1>Sessions</h1>
    <p class="subtitle">
      Experimental durable sessions in a separate SQLite database. Same workspace +
      database lists, resumes, and inspects saved conversations.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="card">
      <div class="field">
        <label>Session database</label>
        <div class="row">
          <input v-model="store.sessionDb" type="text" placeholder="/path/to/sessions.db" spellcheck="false" />
          <button class="small" @click="pickDb">Browse</button>
          <button class="small primary" :disabled="!store.sessionDb" @click="refresh">List</button>
          <button class="small" :disabled="!store.sessionDb" @click="create">New session</button>
        </div>
        <div class="help">
          Never point this at the released Go client's database. Listing never contacts a provider.
        </div>
      </div>
    </div>

    <div class="card">
      <h2>Handles ({{ sessions.length }})</h2>
      <div v-if="sessions.length === 0" class="empty">
        No sessions listed yet — choose a database and press List.
      </div>
      <table v-else class="data">
        <thead>
          <tr>
            <th>Handle</th>
            <th>Revision</th>
            <th>Messages</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="session in sessions" :key="session.id">
            <td class="mono">{{ session.id }}</td>
            <td>{{ session.revision }}</td>
            <td>{{ session.messages }}</td>
            <td>
              <div class="row">
                <button class="small" @click="inspect(session.id)">Inspect</button>
                <button class="small ghost" @click="resume(session.id)">Resume in Chat</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="detail" class="card">
      <h2>Session <span class="mono">{{ detail.session.id }}</span></h2>
      <div class="row wrap" style="margin-bottom: 10px">
        <span class="badge blue">revision {{ detail.session.revision }}</span>
        <span class="badge" :class="detail.hazards.length ? 'red' : 'green'">
          {{ detail.hazards.length }} hazards
        </span>
      </div>
      <h3>Uncertain outcomes</h3>
      <div v-if="detail.hazards.length === 0" class="empty">
        No hazards — every recorded tool outcome is settled.
      </div>
      <table v-else class="data">
        <thead>
          <tr>
            <th>Event</th>
            <th>Tool</th>
            <th>Type</th>
            <th>Effect</th>
            <th>Turn</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="hazard in detail.hazards" :key="hazard.id">
            <td class="mono">{{ hazard.id }}</td>
            <td class="mono">{{ hazard.tool_name }}</td>
            <td>{{ hazard.event_type }}</td>
            <td>{{ hazard.effect_class }}</td>
            <td class="mono">{{ hazard.turn_id }}</td>
          </tr>
        </tbody>
      </table>
      <h3>Messages ({{ detail.session.messages.length }})</h3>
      <div class="messages">
        <div
          v-for="(message, index) in detail.session.messages"
          :key="index"
          class="message"
          :class="message.role"
        >
          <span class="badge">{{ message.role }}</span>
          <div class="markdown-body" v-html="renderMarkdown(message.content.slice(0, 2000))" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { renderMarkdown } from "#desktop/renderer/markdown";
import { navigate } from "#desktop/renderer/router";
import { store, workspaceRoot } from "#desktop/renderer/store";
import type { ChatMessage } from "#desktop/shared/protocol";

type Detail = {
  session: { id: string; revision: number; messages: ChatMessage[] };
  hazards: {
    id: number;
    execution_id: string;
    turn_id: string;
    tool_name: string;
    event_type: string;
    effect_class: string;
  }[];
};

const sessions = ref<{ id: string; revision: number; messages: number }[]>([]);
const detail = ref<Detail | undefined>();
const error = ref("");

async function pickDb(): Promise<void> {
  const picked = await api.pick({
    kind: "file",
    filters: [{ name: "SQLite", extensions: ["db", "sqlite", "sqlite3"] }],
  });
  if (picked) {
    store.sessionDb = picked;
    await refresh();
  }
}

async function refresh(): Promise<void> {
  error.value = "";
  detail.value = undefined;
  try {
    const result = await api.sessionList(store.sessionDb, workspaceRoot());
    sessions.value = result.sessions;
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

async function create(): Promise<void> {
  error.value = "";
  try {
    const created = await api.sessionCreate(store.sessionDb, workspaceRoot());
    store.resumeSession = created.id;
    await refresh();
    await inspect(created.id);
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

async function inspect(id: string): Promise<void> {
  error.value = "";
  try {
    detail.value = await api.sessionInfo(store.sessionDb, workspaceRoot(), id);
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

function resume(id: string): void {
  store.resumeSession = id;
  navigate("chat");
}
</script>

<style scoped>
.messages {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 420px;
  overflow-y: auto;
}

.message {
  border-left: 3px solid var(--border);
  padding-left: 10px;
}
</style>
