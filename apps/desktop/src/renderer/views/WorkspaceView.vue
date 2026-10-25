<template>
  <div class="view wide">
    <h1>Workspace</h1>
    <p class="subtitle">
      Bounded file review through the fd-pinned workspace: relative paths only, UTF-8
      text, 8 MB cap, no symlinks or hard links. Edits happen only via Chat approvals.
    </p>

    <div v-if="error" class="error-box">{{ error }}</div>

    <div class="workspace-layout">
      <div class="card browser">
        <div class="row crumbs">
          <button class="small ghost" @click="openDir('')">root</button>
          <span v-for="(part, index) in crumbs" :key="index" class="crumb">
            <span class="dim">/</span>
            <button class="small ghost" @click="openDir(crumbPath(index))">{{ part }}</button>
          </span>
        </div>
        <div v-if="entries.length === 0" class="empty">Empty directory.</div>
        <ul class="entries">
          <li v-for="entry in entries" :key="entry.name">
            <button
              class="entry"
              :class="{ active: entry.kind === 'file' && entryPath(entry.name) === openPath, dir: entry.kind === 'directory' }"
              :disabled="entry.kind === 'link' || entry.kind === 'other'"
              :title="unopenable[entry.kind]"
              @click="entry.kind === 'directory' ? openDir(entryPath(entry.name)) : openFile(entryPath(entry.name))"
            >
              <span class="entry-icon">{{ icons[entry.kind] }}</span>
              <span class="mono">{{ entry.name }}</span>
              <span v-if="entry.kind === 'file'" class="dim size">{{ formatBytes(entry.size) }}</span>
            </button>
          </li>
        </ul>
        <div v-if="truncated" class="dim foot">Listing truncated at 500 entries.</div>
      </div>
      <div class="card viewer">
        <div v-if="!openPath" class="empty">Select a file to read it.</div>
        <template v-else>
          <div class="row viewer-head">
            <span class="mono">{{ openPath }}</span>
            <span class="spacer" />
            <span class="dim">{{ formatBytes(openBytes) }}</span>
          </div>
          <pre class="file-content"><code>{{ preview.shown }}</code></pre>
          <div v-if="preview.clipped" class="row foot">
            <span class="dim">Showing the first {{ PREVIEW_CHARS.toLocaleString("en-US") }} characters.</span>
            <button class="small" @click="showAll = true">Show all</button>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { api, errorMessage } from "#desktop/renderer/api";
import { formatBytes, previewText } from "#desktop/renderer/format";
import { latest } from "#desktop/renderer/latest";
import { store, workspaceRoot } from "#desktop/renderer/store";
import type { WorkspaceEntry } from "#desktop/shared/protocol";

const dir = ref("");
const entries = ref<WorkspaceEntry[]>([]);
const truncated = ref(false);
const openPath = ref("");
const openText = ref("");
const openBytes = ref(0);
// A file may be 8 MB: laying out all of it in one <pre> stalls the window,
// so the start is shown until the reader asks for the rest.
const PREVIEW_CHARS = 200_000;
const showAll = ref(false);
const preview = computed(() =>
  showAll.value
    ? { shown: openText.value, clipped: false }
    : previewText(openText.value, PREVIEW_CHARS),
);
const error = ref("");
// A slow answer for an earlier click, or for the previous workspace, must not
// replace the current listing or file.
const listing = latest();
const reading = latest();

// A link is listed but never followed, as the model's tools do not follow it.
const icons: Record<WorkspaceEntry["kind"], string> = {
  directory: "▸",
  file: "·",
  link: "@",
  other: "·",
};
const unopenable: Partial<Record<WorkspaceEntry["kind"], string>> = {
  link: "A symbolic link: it is not followed",
  other: "Not a regular file",
};

const crumbs = computed(() => (dir.value ? dir.value.split("/") : []));
const crumbPath = (index: number) => crumbs.value.slice(0, index + 1).join("/");
const entryPath = (name: string) => (dir.value ? `${dir.value}/${name}` : name);

async function openDir(next: string): Promise<void> {
  error.value = "";
  const current = listing.begin();
  try {
    const result = await api.workspaceList(workspaceRoot(), next);
    if (!current()) return;
    dir.value = result.dir === "." ? "" : result.dir;
    entries.value = result.entries;
    truncated.value = result.truncated;
  } catch (error_) {
    if (current()) error.value = errorMessage(error_);
  }
}

async function openFile(path: string): Promise<void> {
  error.value = "";
  const current = reading.begin();
  try {
    const result = await api.workspaceRead(workspaceRoot(), path);
    if (!current()) return;
    openPath.value = path;
    openText.value = result.text;
    openBytes.value = result.bytes;
    showAll.value = false;
  } catch (error_) {
    if (current()) error.value = errorMessage(error_);
  }
}

onMounted(() => void openDir(""));
watch(
  () => workspaceRoot(),
  () => {
    reading.invalidate();
    openPath.value = "";
    openText.value = "";
    void openDir("");
  },
);
</script>

<style scoped>
.wide {
  max-width: none;
}

.workspace-layout {
  display: grid;
  grid-template-columns: 340px 1fr;
  gap: 14px;
  align-items: start;
}

@media (max-width: 900px) {
  .workspace-layout {
    grid-template-columns: 1fr;
  }
}

.crumbs {
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.crumb {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.entries {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 56vh;
  overflow-y: auto;
}

.entry {
  width: 100%;
  display: flex;
  gap: 8px;
  align-items: baseline;
  background: transparent;
  border: 1px solid transparent;
  text-align: left;
  padding: 5px 8px;
}

.entry:hover:not(:disabled) {
  background: var(--bg-raised);
}

.entry.active {
  background: var(--accent-soft);
  border-color: rgba(88, 166, 255, 0.3);
}

.entry-icon {
  color: var(--text-faint);
  width: 14px;
}

.entry.dir .entry-icon {
  color: var(--accent);
}

.size {
  margin-left: auto;
  font-size: 11.5px;
}

.dim {
  color: var(--text-faint);
}

.foot {
  font-size: 12px;
  margin-top: 8px;
}

.viewer-head {
  margin-bottom: 8px;
}

.viewer-head .spacer {
  flex: 1;
}

.file-content {
  max-height: 60vh;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
