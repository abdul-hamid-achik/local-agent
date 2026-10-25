<script setup lang="ts">
// One drawn frame (tui-draw.ts) as an SVG at cols x rows cells. Decorative by
// default, beside a caption or a scene that says what it shows; with `label`
// it is an image of its own.
import { CH, CW, type Frame, SPIN, textStyle } from './tui-draw'

defineProps<{
  frame: Frame
  cols: number
  rows: number
  /** The screen's background, when it is not the dark theme's (style.css). */
  bg?: string
  /** What the screen shows, for a frame that stands alone. */
  label?: string
}>()
</script>

<template>
  <svg
    :viewBox="`0 0 ${cols * CW} ${rows * CH}`"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
    focusable="false"
  >
    <rect class="la-tui-bg" :width="cols * CW" :height="rows * CH" :style="bg ? { fill: bg } : undefined" />
    <rect
      v-for="(r, i) in frame.bg"
      :key="`b${i}`"
      :x="r.x"
      :y="r.y"
      :width="r.w"
      :height="r.h"
      :fill="r.fill"
    />
    <rect
      v-for="(r, i) in frame.blocks"
      :key="`k${i}`"
      :x="r.x"
      :y="r.y"
      :width="r.w"
      :height="r.h"
      :fill="r.fill"
      :fill-opacity="r.alpha"
    />
    <path
      v-for="(s, i) in frame.strokes"
      :key="`s${i}`"
      :d="s.d"
      :stroke="s.stroke"
      :class="`la-tui-${s.weight}`"
    />
    <text
      v-for="(t, i) in frame.texts"
      :key="`t${i}`"
      :x="t.x"
      :y="t.y"
      :fill="t.fill"
      :textLength="t.cells * CW"
      lengthAdjust="spacing"
      :style="textStyle(t.flags)"
    >{{ t.text }}</text>
    <g v-for="(s, i) in frame.spins" :key="`p${i}`" class="la-tui-spin" :fill="s.fill">
      <text
        v-for="(glyph, phase) in SPIN"
        :key="glyph"
        :x="s.x"
        :y="s.y"
        :textLength="CW"
        lengthAdjust="spacingAndGlyphs"
        :style="{ '--phase': phase }"
      >{{ glyph }}</text>
    </g>
  </svg>
</template>
