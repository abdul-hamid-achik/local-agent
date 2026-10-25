<script setup lang="ts">
// The configuration page's theme gallery: one screen of the released Tuimark
// client in each theme it ships (themes.json, written by `bun run
// site:screens` in a release commit), drawn by tui-draw.ts and TuiSvg.vue.
// A theme is a radio, so the picker is one tab stop that arrows walk; without
// script the dark screen is all there is.
import { computed, ref } from 'vue'
import data from './themes.json'
import { draw, type Run } from './tui-draw'
import TuiSvg from './TuiSvg.vue'

const choices = data.themes.map((theme) => ({ name: theme.name, swatch: data.colors[theme.screen] }))
const chosen = ref('dark')
const theme = computed(() => data.themes.find((each) => each.name === chosen.value) ?? data.themes[0]!)
const frame = computed(() => draw(theme.value.lines as unknown as Run[][], data.colors, theme.value.screen))
const screen = computed(() => data.colors[theme.value.screen])
</script>

<template>
  <figure class="la-theme-gallery">
    <fieldset class="la-theme-picker">
      <legend class="la-sr-only">Theme</legend>
      <label v-for="choice in choices" :key="choice.name" class="la-theme-choice">
        <input v-model="chosen" class="la-sr-only" type="radio" name="la-theme" :value="choice.name" />
        <span class="la-theme-swatch" aria-hidden="true" :style="{ background: choice.swatch }" />
        {{ choice.name }}
      </label>
    </fieldset>
    <div class="la-tui-window la-theme-window" :style="{ background: screen }">
      <TuiSvg
        class="la-tui-frame"
        :frame="frame"
        :cols="data.cols"
        :rows="data.rows"
        :bg="screen"
        :label="`The local-agent client in the ${chosen} theme: an applied edit and the reply that follows it`"
      />
    </div>
    <figcaption><code>local-agent --theme {{ chosen }}</code></figcaption>
  </figure>
</template>
