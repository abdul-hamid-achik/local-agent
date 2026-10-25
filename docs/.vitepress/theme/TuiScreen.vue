<script setup lang="ts">
// A terminal screen of the home page: frames of the released Tuimark client
// exactly as tuimark dumps them (screens.json, written by `bun run
// site:screens` in a release commit), in its dark theme, drawn by tui-draw.ts
// and TuiSvg.vue. A scene of several frames plays once (when the page opens,
// or when it scrolls into view), then rests on its last; without script, or
// with reduced motion, the last frame is all there is.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import data from './screens.json'
import { draw, type Run } from './tui-draw'
import TuiSvg from './TuiSvg.vue'

const props = withDefaults(
  defineProps<{
    /** A scene of screens.json. */
    scene: keyof typeof data.scenes
    /** When it plays: as the page opens, or once half of it is in view. */
    play?: 'load' | 'view'
    /** Seconds before the first cut, and between cuts. */
    start?: number
    step?: number
  }>(),
  { play: 'view', start: 0.7, step: 1.6 },
)

/** The screen's own background: rows paint only where theirs differs. */
const SCREEN = data.colors.indexOf('#0d1117')

const screens = data.scenes[props.scene].screens.map((screen, index, all) => ({
  cols: screen.cols,
  rows: screen.rows,
  // The widest screen, and the narrower one the page falls back to.
  size: all.length === 1 ? 'only' : index === 0 ? 'wide' : 'narrow',
  frames: screen.frames.map((frame) => draw(frame.lines as unknown as Run[][], data.colors, SCREEN)),
}))
const frames = screens[0]?.frames.length ?? 0

// One frame has nothing to play. Several play once, unless the reader asked
// for less motion; a scene that plays in view waits for half of it to show.
const root = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | undefined
onMounted(() => {
  const el = root.value
  if (!el || frames < 2 || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  if (props.play === 'load') {
    el.classList.add('is-armed')
    requestAnimationFrame(() => el.classList.add('is-playing'))
    return
  }
  if (typeof IntersectionObserver === 'undefined') return
  el.classList.add('is-armed')
  observer = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return
      el.classList.add('is-playing')
      observer?.disconnect()
    },
    { threshold: 0.5 },
  )
  observer.observe(el)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div
    ref="root"
    class="la-tui-screens"
    :class="`la-tui-${scene}`"
    :style="{ '--start': `${start}s`, '--step': `${step}s` }"
  >
    <div
      v-for="screen in screens"
      :key="screen.cols"
      class="la-tui-screen"
      :class="`la-tui-screen-${screen.size}`"
    >
      <TuiSvg
        v-for="(frame, index) in screen.frames"
        :key="index"
        class="la-tui-frame"
        :style="{ '--frame': index }"
        :frame="frame"
        :cols="screen.cols"
        :rows="screen.rows"
      />
    </div>
  </div>
</template>
