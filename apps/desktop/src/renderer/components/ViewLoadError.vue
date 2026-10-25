<template>
  <div class="view">
    <div class="error-box">
      This view could not load: {{ errorMessage(error) }}
      <div class="row" style="margin-top: 10px">
        <button class="small" @click="reload">Reload</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { errorMessage } from "#desktop/renderer/api";

// Vue gives an async view's error component the reason its import failed.
defineProps<{ error?: unknown }>();

function reload(): void {
  window.location.reload();
}
</script>
