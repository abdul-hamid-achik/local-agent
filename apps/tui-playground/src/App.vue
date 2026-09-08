<script setup lang="ts">
import {
  Approval,
  Box,
  Composer,
  PanelLayout,
  Text,
  onKeyDown,
  useExit,
  useTerminalSize,
} from "@local-agent/vue-tui";
import { ref } from "vue";

const draft = ref("");
const approval = ref(false);
const lastDecision = ref("");
const exit = useExit();
const { width, height } = useTerminalSize();

onKeyDown((key) => {
  if (key.ctrl && key.name === "c") {
    key.preventDefault();
    exit();
  }
});
</script>

<template>
  <PanelLayout :sidebar="true">
    <template #default>
      <Text bold>Vue TUI playground</Text>
      <Text>Resize: {{ width }}×{{ height }}</Text>
      <Text v-if="lastDecision">Decision: {{ lastDecision }}</Text>
      <Box :flexGrow="1" />
      <Approval
        v-if="approval"
        title="Approve this draft? (Enter denies)"
        :preview="draft"
        @decision="(approved) => { lastDecision = approved ? 'approved' : 'denied'; approval = false }"
      />
      <Composer
        v-else
        v-model="draft"
        :busy="false"
        @submit="approval = true"
        @cancel="draft = ''"
      />
    </template>
    <template #sidebar>
      <Text>Reusable PanelLayout

Edit App.vue to experiment with declarative terminal components.</Text>
    </template>
  </PanelLayout>
</template>
