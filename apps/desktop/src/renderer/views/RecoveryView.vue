<template>
  <div class="view">
    <h1>Recovery</h1>
    <p class="subtitle">
      Review tokens for interrupted or uncertain tool effects. Interrupted effects
      are never replayed automatically — inspect here before continuing a session.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="card">
      <div class="grid-2">
        <div class="field">
          <label for="recovery-db">Session database</label>
          <div class="row">
            <input id="recovery-db" v-model="store.sessionDb" type="text" placeholder="/path/to/sessions.db" spellcheck="false" />
            <button class="small" @click="pickDb">Browse</button>
          </div>
        </div>
        <div class="field">
          <label for="recovery-handle">Session handle</label>
          <div class="row">
            <input id="recovery-handle" v-model="handle" type="text" placeholder="session handle" spellcheck="false" />
            <button class="small primary" :disabled="!store.sessionDb || !handle" @click="inspect">
              Inspect
            </button>
          </div>
        </div>
      </div>
      <div class="help">
        Tokens deliberately exclude conversation, arguments, and raw receipts — only
        identifiers and argument hashes for manual review.
      </div>
    </div>

    <div v-if="report" class="card">
      <h2>Recovery report</h2>
      <div class="row wrap" style="margin-bottom: 10px">
        <span class="badge blue mono">{{ report.session }}</span>
        <span class="badge">revision {{ report.revision }}</span>
        <span class="badge" :class="report.hazards.length ? 'red' : 'green'">
          {{ report.hazards.length }} hazards
        </span>
      </div>
      <div class="mono dim">{{ report.workspace }}</div>
      <div v-if="report.hazards.length === 0" class="empty">
        No hazards — nothing needs manual review.
      </div>
      <table v-else class="data">
        <thead>
          <tr>
            <th>Event</th>
            <th>Execution</th>
            <th>Turn</th>
            <th>Tool</th>
            <th>Type</th>
            <th>Effect</th>
            <th>Args SHA-256</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="hazard in report.hazards" :key="hazard.eventId">
            <td class="mono">{{ hazard.eventId }}</td>
            <td class="mono">{{ hazard.executionId }}</td>
            <td class="mono">{{ hazard.turnId }}</td>
            <td class="mono">{{ hazard.toolName }}</td>
            <td>{{ hazard.eventType }}</td>
            <td>{{ hazard.effectClass }}</td>
            <td class="mono">{{ hazard.argumentsSha256 }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { latest } from "#desktop/renderer/latest";
import { store, workspaceRoot } from "#desktop/renderer/store";
import type { RecoveryView } from "#desktop/shared/protocol";

const handle = ref(store.resumeSession);
const report = ref<RecoveryView | undefined>();
const error = ref("");
// A report answers the session it was asked about: a slower, older answer
// must not replace a newer one, nor outlive a change of session or database.
const inspections = latest();

watch(
  () => [handle.value.trim(), store.sessionDb],
  () => {
    inspections.invalidate();
    report.value = undefined;
  },
);

async function pickDb(): Promise<void> {
  const picked = await api.pickSessionDb();
  if (picked) store.sessionDb = picked;
}

async function inspect(): Promise<void> {
  error.value = "";
  report.value = undefined;
  const current = inspections.begin();
  try {
    const found = await api.recoveryInfo(store.sessionDb, workspaceRoot(), handle.value.trim());
    if (current()) report.value = found;
  } catch (error_) {
    if (current()) error.value = errorMessage(error_);
  }
}
</script>

<style scoped>
.dim {
  color: var(--text-faint);
  font-size: 12px;
  margin-bottom: 8px;
}
</style>
