<template>
  <div class="view chat">
    <h1>Chat</h1>
    <p class="subtitle">
      Agent turns through the real runtime loop — streaming text, tool calls, and
      host approvals. Applies to the next turn only.
    </p>

    <div v-if="state.notice" class="notice-box">{{ state.notice }}</div>
    <div v-if="state.error" class="error-box">{{ state.error }}</div>

    <div class="card session-strip">
      <div class="row wrap">
        <span class="badge blue">{{ store.intent }}</span>
        <span v-if="state.sessionId" class="badge green">session {{ shortId(state.sessionId) }}</span>
        <span v-else class="badge">ephemeral (no session db)</span>
        <span v-if="store.mcpAttached && store.mcpConfig" class="badge violet">mcp attached</span>
        <span class="spacer" />
        <button v-if="state.running" class="danger small" @click="chat.stop()">Stop</button>
      </div>
    </div>

    <div ref="transcriptEl" class="card transcript">
      <div v-if="state.messages.length === 0 && !state.liveText" class="empty">
        Send a prompt to start. Try <span class="kbd">/help</span> for chat commands.
      </div>
      <div v-for="(message, index) in state.messages" :key="index" class="message" :class="message.role">
        <div class="message-head">
          <span class="badge" :class="message.role === 'user' ? 'blue' : 'green'">
            {{ message.role }}
          </span>
          <span v-if="message.tool_name" class="mono dim">{{ message.tool_name }}</span>
        </div>
        <div
          v-if="message.role === 'assistant' || message.role === 'user'"
          class="markdown-body"
          v-html="rendered[index]"
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
      <div v-if="state.liveText" class="message assistant">
        <div class="message-head"><span class="badge green">assistant</span></div>
        <div class="markdown-body" v-html="liveHtml" />
      </div>
      <div v-if="state.liveThinking" class="thinking-live">
        <span class="badge">thinking…</span>
        <pre>{{ state.shownThinking }}</pre>
      </div>
      <div v-for="receipt in state.receipts" :key="receipt.id" class="receipt">
        <span class="badge" :class="receiptClass(receipt.status)">{{ receipt.status }}</span>
        <span class="mono">{{ receipt.name }}</span>
        <pre v-if="receipt.output">{{ receipt.output }}</pre>
      </div>
      <ApprovalCard
        v-for="approval in state.approvals"
        :key="approval.token"
        :proposal="approval.proposal"
        :status="approval.status"
        @decide="(allowed) => chat.decide(approval, allowed)"
      />
      <div v-if="state.usage" class="usage mono dim">
        input {{ state.usage.inputTokens }} · output {{ state.usage.outputTokens }} tokens
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
        <button class="primary" :disabled="state.running || !draft.trim()" @click="submit">
          Send
        </button>
        <button class="ghost" :disabled="state.running" @click="clearConversation(chat, store)">Clear</button>
        <span class="spacer" />
        <span class="dim help-text"><span class="kbd">Enter</span> send · <span class="kbd">Shift+Enter</span> newline</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from "vue";
import ApprovalCard from "#desktop/renderer/components/ApprovalCard.vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { clearConversation, useChat } from "#desktop/renderer/chat-run";
import { isSendKey } from "#desktop/renderer/composer";
import { liveMarkdown } from "#desktop/renderer/live-markdown";
import { renderMarkdown } from "#desktop/renderer/markdown";
import { atEnd } from "#desktop/renderer/scroll";
import { parseSlashCommand } from "#desktop/renderer/slash";
import { store, workspaceRoot } from "#desktop/renderer/store";

// KeepAlive (App.vue) matches on this name, so the draft and scroll survive
// switching views. The run itself lives in useChat and survives either way.
defineOptions({ name: "ChatView" });

const chat = useChat();
const state = chat.state;
const draft = ref("");
// A finished message renders once, not again on every delta of the live
// reply; the live reply renders at most once a frame.
const rendered = computed(() =>
  state.messages.map((message) =>
    message.role === "assistant" || message.role === "user"
      ? renderMarkdown(message.content)
      : "",
  ),
);
const { html: liveHtml, stop: stopLive } = liveMarkdown(() => state.liveText, {
  request: (callback) => requestAnimationFrame(callback),
  cancel: (id) => cancelAnimationFrame(id),
});
const transcriptEl = ref<HTMLElement | undefined>();

const shortId = (id: string) => (id.length > 12 ? `${id.slice(0, 8)}…` : id);

function receiptClass(status: string): string {
  if (status === "succeeded") return "green";
  if (status === "denied") return "amber";
  return "red";
}

// Follows the transcript to its end, unless the reader scrolled up: that is
// measured now, before the event's content is rendered. Sending always follows.
function scrollDown(force = false): void {
  const scroller = transcriptEl.value;
  if (!force && scroller && !atEnd(scroller)) return;
  void nextTick(() => {
    transcriptEl.value?.scrollTo({ top: transcriptEl.value.scrollHeight });
  });
}
const followEvent = () => scrollDown();
chat.onEvent = followEvent;

function onKey(event: KeyboardEvent): void {
  if (isSendKey(event)) {
    event.preventDefault();
    void submit();
  }
}

async function runSlash(command: string): Promise<boolean> {
  const parsed = parseSlashCommand(command);
  if (parsed.kind === "mode") {
    const requested = parsed.requested;
    if (!requested) {
      state.notice = `Current intent: ${store.intent}. Use /mode plan or /mode build.`;
    } else if (requested !== "plan" && requested !== "build") {
      state.notice = `Unknown mode: ${requested}. Use /mode plan or /mode build.`;
    } else {
      store.intent = requested;
      state.notice = `Intent changed to ${requested}. Applies to the next turn.`;
    }
    return true;
  }
  if (parsed.kind === "help") {
    state.notice = "/mode plan|build · /mcp · /help — same surface as the terminal client.";
    return true;
  }
  if (parsed.kind === "mcp") {
    try {
      if (!store.mcpAttached || !store.mcpConfig) {
        state.notice = "MCP servers: 0 · set an MCP config in the MCP view to attach servers.";
      } else {
        const status = await api.mcpStatus(store.mcpConfig);
        const tools = status.servers.reduce((total, server) => total + server.tools.length, 0);
        state.notice = `MCP servers: ${status.servers.length} · ${tools} tools (config attached to next turn).`;
      }
    } catch (error_) {
      state.error = errorMessage(error_);
    }
    return true;
  }
  if (parsed.kind === "unknown") {
    state.notice = `Unknown command: ${parsed.command}. Use /help.`;
    return true;
  }
  return false;
}

async function submit(): Promise<void> {
  if (state.running || !draft.value.trim()) return;
  const prompt = draft.value;
  // A prompt needs a model: refused before it leaves the composer, so it is
  // not lost. A slash command needs none.
  if (parseSlashCommand(prompt).kind === "not-slash" && !store.model.trim()) {
    state.notice = "";
    state.error = "Set a model in the top bar first (it must already exist in Ollama).";
    return;
  }
  draft.value = "";
  state.notice = "";
  state.error = "";
  if (await runSlash(prompt.trim())) return;
  const started = chat.send(prompt, {
    workspace: workspaceRoot(),
    intent: store.intent,
    model: store.model.trim(),
    sessionDb: store.sessionDb || undefined,
    resume: store.resumeSession || undefined,
    mcpConfig: store.mcpAttached ? store.mcpConfig || undefined : undefined,
  });
  scrollDown(true);
  const sessionId = await started;
  if (sessionId) store.resumeSession = sessionId;
}

onUnmounted(() => {
  if (chat.onEvent === followEvent) chat.onEvent = () => undefined;
  stopLive();
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
