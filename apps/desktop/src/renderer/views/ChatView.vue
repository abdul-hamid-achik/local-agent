<template>
  <div class="view chat">
    <h1>Chat</h1>
    <p class="subtitle">
      Agent turns through the real runtime loop — streaming text, tool calls, and
      host approvals. Applies to the next turn only.
    </p>

    <div v-if="notice" class="notice-box">{{ notice }}</div>
    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="card session-strip">
      <div class="row wrap">
        <span class="badge blue">{{ store.intent }}</span>
        <span v-if="activeSessionId" class="badge green">session {{ shortId(activeSessionId) }}</span>
        <span v-else class="badge">ephemeral (no session db)</span>
        <span v-if="store.mcpAttached && store.mcpConfig" class="badge violet">mcp attached</span>
        <span class="spacer" />
        <button v-if="running" class="danger small" @click="stop">Stop</button>
      </div>
    </div>

    <div ref="transcriptEl" class="card transcript">
      <div v-if="messages.length === 0 && !liveText" class="empty">
        Send a prompt to start. Try <span class="kbd">/help</span> for chat commands.
      </div>
      <div v-for="(message, index) in messages" :key="index" class="message" :class="message.role">
        <div class="message-head">
          <span class="badge" :class="message.role === 'user' ? 'blue' : 'green'">
            {{ message.role }}
          </span>
          <span v-if="message.tool_name" class="mono dim">{{ message.tool_name }}</span>
        </div>
        <div
          v-if="message.role === 'assistant' || message.role === 'user'"
          class="markdown-body"
          v-html="renderMarkdown(message.content)"
        />
        <pre v-else>{{ message.content }}</pre>
        <details v-if="message.thinking" class="thinking">
          <summary>thinking</summary>
          <pre>{{ message.thinking }}</pre>
        </details>
        <div v-if="message.tool_calls?.length" class="tool-calls">
          <div v-for="call in message.tool_calls" :key="call.id ?? call.function.name" class="tool-call">
            <span class="badge violet mono">{{ call.function.name }}</span>
          </div>
        </div>
      </div>
      <div v-if="liveText" class="message assistant">
        <div class="message-head"><span class="badge green">assistant</span></div>
        <div class="markdown-body" v-html="renderMarkdown(liveText)" />
      </div>
      <div v-if="liveThinking" class="thinking-live">
        <span class="badge">thinking…</span>
        <pre>{{ truncatedThinking }}</pre>
      </div>
      <div v-for="receipt in receipts" :key="receipt.id" class="receipt">
        <span class="badge" :class="receiptClass(receipt.status)">{{ receipt.status }}</span>
        <span class="mono">{{ receipt.name }}</span>
        <pre v-if="receipt.output">{{ receipt.output }}</pre>
      </div>
      <ApprovalCard
        v-for="approval in approvals"
        :key="approval.token"
        :proposal="approval.proposal"
        :decided="approval.decided"
        :allowed="approval.allowed"
        @decide="(allowed) => decide(approval, allowed)"
      />
      <div v-if="usage" class="usage mono dim">
        input {{ usage.inputTokens }} · output {{ usage.outputTokens }} tokens
      </div>
    </div>

    <div class="card composer">
      <div class="row">
        <textarea
          v-model="draft"
          rows="3"
          placeholder="Ask anything, or /help · /mode plan|build · /mcp"
          @keydown="onKey"
        />
      </div>
      <div class="row" style="margin-top: 8px">
        <button class="primary" :disabled="running || !draft.trim()" @click="submit">
          Send
        </button>
        <button class="ghost" :disabled="running" @click="clear">Clear</button>
        <span class="spacer" />
        <span class="dim help-text"><span class="kbd">Enter</span> send · <span class="kbd">Shift+Enter</span> newline</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import ApprovalCard from "#desktop/renderer/components/ApprovalCard.vue";
import { api, errorMessage, randomRunId, rpc, subscribeStream } from "#desktop/renderer/api";
import { renderMarkdown } from "#desktop/renderer/markdown";
import { parseSlashCommand } from "#desktop/renderer/slash";
import { store, workspaceRoot } from "#desktop/renderer/store";
import type { AgentStreamEvent, ChatMessage } from "#desktop/shared/protocol";
import { SidecarMethods } from "#desktop/shared/protocol";

// KeepAlive (App.vue) matches on this name: leaving Chat must not end its run.
defineOptions({ name: "ChatView" });

type Approval = Extract<AgentStreamEvent, { type: "approval_requested" }> & {
  decided: boolean;
  allowed?: boolean;
};

type Receipt = {
  id: string;
  name: string;
  status: string;
  output: string;
};

const messages = ref<ChatMessage[]>([]);
const draft = ref("");
const liveText = ref("");
const liveThinking = ref("");
const receipts = ref<Receipt[]>([]);
const approvals = ref<Approval[]>([]);
const usage = ref<{ inputTokens: number; outputTokens: number } | undefined>();
const running = ref(false);
const notice = ref("");
const error = ref("");
const activeSessionId = ref("");
const transcriptEl = ref<HTMLElement | undefined>();
let runId = "";
let unsubscribe: (() => void) | undefined;
let unsubscribeExit: (() => void) | undefined;
const deciding = new Set<string>();

const truncatedThinking = ref("");
const shortId = (id: string) => (id.length > 12 ? `${id.slice(0, 8)}…` : id);

function receiptClass(status: string): string {
  if (status === "succeeded") return "green";
  if (status === "denied") return "amber";
  return "red";
}

function scrollDown(): void {
  void nextTick(() => {
    transcriptEl.value?.scrollTo({ top: transcriptEl.value.scrollHeight });
  });
}

function onKey(event: KeyboardEvent): void {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    void submit();
  }
}

function clear(): void {
  messages.value = [];
  receipts.value = [];
  approvals.value = [];
  liveText.value = "";
  liveThinking.value = "";
  usage.value = undefined;
  notice.value = "";
  error.value = "";
  activeSessionId.value = "";
}

async function stop(): Promise<void> {
  if (runId) await api.agentCancel(runId).catch(() => undefined);
}

function handleEvent(event: AgentStreamEvent): void {
  switch (event.type) {
    case "text":
      liveText.value += event.text;
      break;
    case "thinking":
      liveThinking.value += event.text;
      truncatedThinking.value =
        liveThinking.value.length > 2000
          ? `${liveThinking.value.slice(-2000)}…`
          : liveThinking.value;
      break;
    case "message":
      messages.value.push(event.message);
      liveText.value = "";
      liveThinking.value = "";
      truncatedThinking.value = "";
      break;
    case "tool_result":
      receipts.value.push({
        id: event.id,
        name: event.name,
        status: event.status,
        output: event.output.slice(0, 4000),
      });
      break;
    case "approval_requested":
      approvals.value.push({ ...event, decided: false });
      break;
    case "done":
      usage.value = { inputTokens: event.inputTokens, outputTokens: event.outputTokens };
      break;
    case "run_error":
      error.value = event.message;
      finish();
      break;
    case "run_done":
      finish();
      break;
    default:
      break;
  }
  scrollDown();
}

function finish(): void {
  running.value = false;
  unsubscribe?.();
  unsubscribe = undefined;
  runId = "";
}

async function decide(approval: Approval, allowed: boolean): Promise<void> {
  if (approval.decided || deciding.has(approval.token)) return;
  deciding.add(approval.token);
  try {
    await api.agentDecide(runId, approval.token, allowed);
    approval.decided = true;
    approval.allowed = allowed;
  } catch (error_) {
    // The card stays open so the decision can be retried.
    error.value = errorMessage(error_);
  } finally {
    deciding.delete(approval.token);
  }
}

async function runSlash(command: string): Promise<boolean> {
  const parsed = parseSlashCommand(command);
  if (parsed.kind === "mode") {
    const requested = parsed.requested;
    if (!requested) {
      notice.value = `Current intent: ${store.intent}. Use /mode plan or /mode build.`;
    } else if (requested !== "plan" && requested !== "build") {
      notice.value = `Unknown mode: ${requested}. Use /mode plan or /mode build.`;
    } else {
      store.intent = requested;
      notice.value = `Intent changed to ${requested}. Applies to the next turn.`;
    }
    return true;
  }
  if (parsed.kind === "help") {
    notice.value = "/mode plan|build · /mcp · /help — same surface as the terminal client.";
    return true;
  }
  if (parsed.kind === "mcp") {
    try {
      if (!store.mcpAttached || !store.mcpConfig) {
        notice.value = "MCP servers: 0 · set an MCP config in the MCP view to attach servers.";
      } else {
        const status = await api.mcpStatus(store.mcpConfig);
        const tools = status.servers.reduce((total, server) => total + server.tools.length, 0);
        notice.value = `MCP servers: ${status.servers.length} · ${tools} tools (config attached to next turn).`;
      }
    } catch (error_) {
      error.value = errorMessage(error_);
    }
    return true;
  }
  if (parsed.kind === "unknown") {
    notice.value = `Unknown command: ${parsed.command}. Use /help.`;
    return true;
  }
  return false;
}

async function submit(): Promise<void> {
  if (running.value || !draft.value.trim()) return;
  const prompt = draft.value;
  draft.value = "";
  notice.value = "";
  error.value = "";
  if (await runSlash(prompt.trim())) return;
  if (!store.model.trim()) {
    error.value = "Set a model in the top bar first (it must already exist in Ollama).";
    return;
  }
  running.value = true;
  receipts.value = [];
  usage.value = undefined;
  messages.value.push({ role: "user", content: prompt });
  runId = randomRunId();
  unsubscribe = subscribeStream(runId, (event) =>
    handleEvent(event as AgentStreamEvent),
  );
  scrollDown();
  try {
    const started = await rpc<{ runId: string; sessionId?: string }>(
      SidecarMethods.agentStart,
      {
        runId,
        messages: messages.value,
        workspace: workspaceRoot(),
        intent: store.intent,
        model: store.model.trim(),
        sessionDb: store.sessionDb || undefined,
        resume: store.resumeSession || undefined,
        mcpConfig: store.mcpAttached ? store.mcpConfig || undefined : undefined,
      },
    );
    if (started.sessionId) {
      activeSessionId.value = started.sessionId;
      store.resumeSession = started.sessionId;
    }
  } catch (error_) {
    error.value = errorMessage(error_);
    finish();
  }
}

onMounted(() => {
  unsubscribeExit = api.onSidecarExit((_code, message) => {
    if (!running.value) return;
    error.value = message ?? "Sidecar exited during the run.";
    finish();
  });
});

// The view is kept alive across navigation, so this runs only at teardown. A
// run ends by Stop or when the app quits (main stops the sidecar).
onUnmounted(() => {
  unsubscribe?.();
  unsubscribeExit?.();
});
</script>

<style scoped>
.transcript {
  max-height: 52vh;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.message {
  border-left: 3px solid var(--border);
  padding: 2px 0 2px 12px;
}

.message.user {
  border-color: var(--accent);
}

.message.assistant {
  border-color: var(--green);
}

.message.tool {
  border-color: var(--violet);
}

.message-head {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 4px;
}

.dim {
  color: var(--text-faint);
  font-size: 12px;
}

.thinking summary {
  cursor: pointer;
  color: var(--text-faint);
  font-size: 12.5px;
}

.thinking-live pre {
  max-height: 140px;
}

.tool-calls {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 6px;
}

.receipt {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: flex-start;
  background: var(--bg);
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-sm);
  padding: 8px 10px;
}

.receipt pre {
  width: 100%;
  max-height: 180px;
  margin: 0;
}

.usage {
  text-align: right;
}

.session-strip .spacer,
.composer .spacer {
  flex: 1;
}

.help-text {
  font-size: 12px;
  color: var(--text-faint);
}

.composer textarea {
  resize: vertical;
  min-height: 64px;
}
</style>
