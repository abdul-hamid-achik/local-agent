<template>
  <div class="view wide">
    <h1>Project</h1>
    <p class="subtitle">
      Work this checkout: allowlisted repo commands with streamed output, Glyphrun
      contracts, and git state. Commands run at the repository root.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="card">
      <h2>Commands</h2>
      <div class="row wrap">
        <button
          v-for="command in commandNames"
          :key="command"
          class="small"
          :disabled="running !== undefined"
          :title="argvText(command)"
          @click="run(command)"
        >
          {{ command }}
        </button>
        <span class="spacer" />
        <button v-if="running" class="small danger" @click="cancel">Cancel</button>
      </div>
      <div v-if="lastCommand" class="mono dim run-head">
        $ {{ lastCommand }} <span v-if="exitCode !== undefined">→ exit {{ exitCode }}</span>
        <span v-else>→ running…</span>
      </div>
      <pre ref="outputEl" class="output">{{ output || "No output yet." }}</pre>
    </div>

    <div class="grid-2">
      <div class="card">
        <h2>Glyphrun contracts ({{ specs.length }})</h2>
        <div class="help">Deterministic loopback fixtures — no models, no GPUs.</div>
        <div v-for="spec in specs" :key="spec.file" class="spec">
          <div class="mono spec-file">{{ spec.file }}</div>
          <pre>{{ spec.head }}</pre>
        </div>
      </div>
      <div class="card">
        <h2>Checks cheat sheet</h2>
        <table class="data">
          <tbody>
            <tr><td class="mono">check</td><td>lint + TS 7 + tests + CLI build</td></tr>
            <tr><td class="mono">test</td><td>Vitest through Bun</td></tr>
            <tr><td class="mono">typecheck</td><td>TS 7 + playground templates</td></tr>
            <tr><td class="mono">site:build</td><td>VitePress production build</td></tr>
            <tr><td class="mono">test:integration</td><td>glyph run specs/bun/* (needs glyph)</td></tr>
          </tbody>
        </table>
        <h3 style="margin-top: 16px">Monorepo</h3>
        <table class="data">
          <tbody>
            <tr><td class="mono">apps/cli</td><td>composition + 3 presentations</td></tr>
            <tr><td class="mono">apps/tui-playground</td><td>model-free .vue demo</td></tr>
            <tr><td class="mono">apps/desktop</td><td>this workbench</td></tr>
            <tr><td class="mono">packages/runtime</td><td>inference, tools, sessions</td></tr>
            <tr><td class="mono">packages/vue-tui</td><td>terminal components</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { api, errorMessage, randomRunId, subscribeStream } from "#desktop/renderer/api";
import { ProjectCommands, type ProjectCommandName, type ProjectStreamEvent } from "#desktop/shared/protocol";

const commandNames = Object.keys(ProjectCommands) as ProjectCommandName[];
const output = ref("");
const lastCommand = ref("");
const exitCode = ref<number | undefined>();
const running = ref<string | undefined>();
const specs = ref<{ file: string; bytes: number; head: string }[]>([]);
const error = ref("");
const outputEl = ref<HTMLElement | undefined>();
let unsubscribe: (() => void) | undefined;

const argvText = (name: ProjectCommandName) => ProjectCommands[name].join(" ");

function scrollDown(): void {
  void nextTick(() => {
    outputEl.value?.scrollTo({ top: outputEl.value.scrollHeight });
  });
}

async function run(name: ProjectCommandName): Promise<void> {
  if (running.value) return;
  error.value = "";
  output.value = "";
  exitCode.value = undefined;
  lastCommand.value = argvText(name);
  const runId = randomRunId();
  running.value = runId;
  unsubscribe = subscribeStream(runId, (event) => {
    const typed = event as ProjectStreamEvent;
    if (typed.type === "stdout" || typed.type === "stderr") {
      output.value += typed.text;
      scrollDown();
    } else if (typed.type === "exit") {
      exitCode.value = typed.code;
      running.value = undefined;
      unsubscribe?.();
      unsubscribe = undefined;
    }
  });
  try {
    await api.projectRun(name, runId);
  } catch (error_) {
    error.value = errorMessage(error_);
    running.value = undefined;
    unsubscribe?.();
    unsubscribe = undefined;
  }
}

async function cancel(): Promise<void> {
  if (running.value) await api.projectCancel(running.value).catch(() => undefined);
}

onMounted(async () => {
  try {
    specs.value = (await api.specsList()).specs;
  } catch (error_) {
    error.value = errorMessage(error_);
  }
});

onUnmounted(() => {
  if (running.value) void api.projectCancel(running.value).catch(() => undefined);
  unsubscribe?.();
});
</script>

<style scoped>
.wide {
  max-width: none;
}

.spacer {
  flex: 1;
}

.run-head {
  margin-top: 10px;
  font-size: 12px;
}

.dim {
  color: var(--text-faint);
}

.output {
  max-height: 320px;
  white-space: pre-wrap;
  word-break: break-word;
}

.spec {
  margin-top: 10px;
}

.spec-file {
  font-size: 12.5px;
  color: var(--accent);
  margin-bottom: 4px;
}

.spec pre {
  margin: 0;
  max-height: 150px;
  font-size: 11.5px;
}

.help {
  font-size: 12px;
  color: var(--text-faint);
}
</style>
