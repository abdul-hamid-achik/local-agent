<template>
  <div class="approval">
    <div class="row">
      <span class="badge amber">approval requested</span>
      <span class="badge violet">{{ proposal.kind }}</span>
      <span v-if="proposal.kind === 'edit' || proposal.kind === 'write'" class="mono">{{ proposal.path }}</span>
      <span v-if="proposal.kind === 'write'" class="dim">{{ proposal.created ? "new file" : "replace" }}</span>
      <span v-if="proposal.kind === 'mcp'" class="mono">{{ proposal.inner ?? proposal.name }}</span>
      <span v-if="proposal.kind === 'mcp' && proposal.gateway" class="dim">via {{ proposal.gateway }}</span>
    </div>
    <pre v-if="proposal.kind === 'edit' || proposal.kind === 'write'" class="diff">{{ proposal.diff }}</pre>
    <pre v-if="proposal.kind === 'command'">[{{ proposal.cwd }}]$ {{ argvText }}</pre>
    <div v-if="proposal.kind === 'command' && proposal.secret_paths?.length" class="dim">
      names a path the workspace treats as secret: {{ proposal.secret_paths.join(", ") }}
    </div>
    <pre v-if="proposal.kind === 'mcp'">{{ proposal.argumentsJson }}</pre>
    <div class="row">
      <button class="primary small" :disabled="status !== 'pending'" @click="$emit('decide', true)">
        Approve
      </button>
      <button class="danger small" :disabled="status !== 'pending'" @click="$emit('decide', false)">
        Deny
      </button>
      <span v-if="status !== 'pending'" class="dim">{{ statusText }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { ApprovalStatus } from "#desktop/renderer/chat-run";
import type { AgentStreamEvent } from "#desktop/shared/protocol";

type Proposal = Extract<AgentStreamEvent, { type: "approval_requested" }>["proposal"];

const props = defineProps<{ proposal: Proposal; status: ApprovalStatus }>();
defineEmits<{ decide: [allowed: boolean] }>();

const argvText = computed(() =>
  props.proposal.kind === "command"
    ? props.proposal.argv.map((arg) => (arg.includes(" ") ? JSON.stringify(arg) : arg)).join(" ")
    : "",
);

const statusText = computed(() =>
  props.status === "ended" ? "not decided: the run is no longer waiting" : props.status,
);
</script>

<style scoped>
.approval {
  border: 1px solid rgba(210, 153, 34, 0.5);
  background: var(--amber-soft);
  border-radius: var(--radius);
  padding: 12px;
  margin: 10px 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.diff {
  max-height: 260px;
}

.dim {
  color: var(--text-faint);
  font-size: 12.5px;
}
</style>
