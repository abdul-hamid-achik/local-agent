<template>
  <div class="view">
    <h1>Providers</h1>
    <p class="subtitle">
      Model admission before any prompt data is sent: inventory, identity, weight, and
      native-context evidence. Local Ollama stays on loopback; Chalupa arrives as a
      process-scoped profile.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>
    <div v-if="notice" class="notice-box">{{ notice }}</div>

    <div class="card">
      <h2>Resolved profile</h2>
      <div class="row wrap">
        <input v-model="store.model" type="text" placeholder="model (must exist)" spellcheck="false" style="max-width: 260px" />
        <button class="small primary" @click="resolve">Resolve</button>
        <button class="small" :disabled="!store.provider" @click="admit">Run admission</button>
        <button class="small ghost" :disabled="!store.provider" @click="inventory">List models</button>
      </div>
      <table v-if="store.provider" class="data" style="margin-top: 12px">
        <tbody>
          <tr><th>Type</th><td class="mono">{{ store.provider.type }}</td></tr>
          <tr><th>Endpoint</th><td class="mono">{{ store.provider.base_url }}</td></tr>
          <tr><th>Model</th><td class="mono">{{ store.provider.model }}</td></tr>
          <tr><th>Context</th><td class="mono">{{ store.provider.context_size }}</td></tr>
          <tr><th>Source</th><td><span class="badge blue">{{ store.provider.source }}</span></td></tr>
        </tbody>
      </table>
      <div v-else class="empty">No profile resolved yet.</div>
    </div>

    <div v-if="admission" class="card">
      <h2>Admission result</h2>
      <div class="row">
        <span class="badge" :class="admission.status === 'validated' ? 'green' : admission.status === 'rejected' ? 'red' : 'amber'">
          {{ admission.status }}
        </span>
      </div>
      <table v-if="admission.status === 'validated'" class="data" style="margin-top: 10px">
        <tbody>
          <tr><th>Model</th><td class="mono">{{ admission.model }}</td></tr>
          <tr><th>Weight</th><td class="mono">{{ formatBytes(admission.sizeBytes) }}</td></tr>
          <tr><th>Native context</th><td class="mono">{{ admission.nativeContext ?? "unknown" }}</td></tr>
          <tr><th>Capabilities</th><td class="mono">{{ admission.capabilities.join(", ") || "—" }}</td></tr>
        </tbody>
      </table>
      <div v-else class="error-box" style="margin-top: 10px">{{ admission.message }}</div>
      <div class="help">
        Reachability alone returns <code>unverified</code>; remote profiles fail closed
        when tags/show omit identity, weight, capability, or native-context evidence.
      </div>
    </div>

    <div v-if="models" class="card">
      <h2>Inventory — <span class="mono">{{ models.endpoint }}</span></h2>
      <table class="data">
        <thead><tr><th>Name</th><th>Size</th><th /></tr></thead>
        <tbody>
          <tr v-for="model in models.models" :key="model.name">
            <td class="mono">{{ model.name }}</td>
            <td class="mono">{{ formatBytes(model.size) }}</td>
            <td><button class="small ghost" @click="useModel(model.name)">Use</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <h2>Process profile (Chalupa)</h2>
      <div class="help" style="margin-bottom: 10px">
        The client consumes <code>LOCAL_AGENT_PROVIDER=chalupa</code>,
        <code>LOCAL_AGENT_PROVIDER_CONFIG</code>, and
        <code>LOCAL_AGENT_LOCAL_ONLY=false</code> from the sidecar environment. Saved
        here, applied by restarting the sidecar. Chalupa owns placement and tunnels.
      </div>
      <div v-for="(row, index) in envRows" :key="index" class="row env-row">
        <input v-model="row.key" type="text" placeholder="NAME" spellcheck="false" class="mono" />
        <input v-model="row.value" type="text" placeholder="value" spellcheck="false" class="mono" />
        <button class="small ghost" @click="envRows.splice(index, 1)">Remove</button>
      </div>
      <div class="row" style="margin-top: 10px">
        <button class="small" @click="envRows.push({ key: '', value: '' })">Add variable</button>
        <button class="small primary" @click="saveEnv">Save &amp; restart sidecar</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { store } from "#desktop/renderer/store";
import type { AdmissionView } from "#desktop/shared/protocol";

const admission = ref<AdmissionView | undefined>();
const models = ref<{ endpoint: string; models: { name: string; size: number }[] } | undefined>();
const envRows = ref<{ key: string; value: string }[]>([]);
const error = ref("");
const notice = ref("");

function formatBytes(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(0, Math.round(bytes / 1024))} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

async function resolve(): Promise<void> {
  error.value = "";
  admission.value = undefined;
  try {
    store.provider = await api.providerResolve(store.model.trim() || undefined);
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

async function admit(): Promise<void> {
  error.value = "";
  try {
    admission.value = await api.providerAdmit(store.model.trim() || undefined);
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

async function inventory(): Promise<void> {
  error.value = "";
  try {
    models.value = await api.ollamaModels(store.model.trim() || undefined);
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

function useModel(name: string): void {
  store.model = name;
  notice.value = `Model set to ${name}. Resolve again to refresh the profile.`;
}

async function saveEnv(): Promise<void> {
  error.value = "";
  notice.value = "";
  const env: Record<string, string> = {};
  for (const row of envRows.value) {
    const key = row.key.trim();
    if (!key) continue;
    if (!/^[A-Z_][A-Z0-9_]*$/.test(key)) {
      error.value = `Invalid variable name: ${key}`;
      return;
    }
    env[key] = row.value;
  }
  try {
    await api.setProviderEnv(env);
    notice.value = "Profile saved. Sidecar restarted — resolve again to verify.";
    store.provider = undefined;
  } catch (error_) {
    error.value = errorMessage(error_);
  }
}

onMounted(async () => {
  try {
    const env = await api.getProviderEnv();
    envRows.value = Object.entries(env).map(([key, value]) => ({ key, value }));
  } catch {
    envRows.value = [];
  }
});
</script>

<style scoped>
.env-row {
  margin-bottom: 8px;
}

.help {
  font-size: 12px;
  color: var(--text-faint);
  margin-top: 8px;
}
</style>
