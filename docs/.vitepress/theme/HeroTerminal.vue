<script setup lang="ts">
// The hero terminal: frames of the released Tuimark client exactly as tuimark
// dumps them (hero.json, written by `bun run site:hero` in a release commit),
// in its dark theme. Text keeps its cells whatever monospace font the reader
// has; box and block glyphs are drawn as paths, so borders join like they do
// in a terminal. The frames play once, then rest on the last.
import { onMounted, ref } from 'vue'
import hero from './hero.json'

type Run = [text: string, fg: number, bg: number, flags: number]

/** A cell, in viewBox units: about the 0.6em by 1.2em of a monospace cell. */
const CW = 10
const CH = 20
const BASELINE = 14.6
/** The screen's own background: rows paint only where theirs differs. */
const SCREEN = hero.colors.indexOf('#0d1117')
const BOLD = 1
const DIM = 2
const ITALIC = 4
const UNDERLINE = 8
/** tuimark's spinner (SPEC v0.4b §12.6): ten glyphs, one every 100 ms. */
const SPIN = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏']

// Each glyph's strokes inside its cell: [from, to] pairs of points on a 3x3
// grid (0 the left or top edge, 1 the center, 2 the right or bottom edge),
// with r for a rounded corner. Heavy and double glyphs set their weight.
type Stroke = { d: (x: number, y: number) => string; weight: 'light' | 'heavy' }
const at = (x: number, y: number, gx: number, gy: number) =>
  `${x + (gx * CW) / 2} ${y + (gy * CH) / 2}`
const line = (pairs: [number, number, number, number][]) => (x: number, y: number) =>
  pairs.map(([ax, ay, bx, by]) => `M${at(x, y, ax, ay)}L${at(x, y, bx, by)}`).join('')
const R = 4
/** A rounded corner from edge (ex, 1) to edge (1, ey) through the center. */
const arc = (ex: number, ey: number) => (x: number, y: number) => {
  const cx = x + CW / 2
  const cy = y + CH / 2
  const hx = ex === 0 ? x : x + CW
  const vy = ey === 0 ? y : y + CH
  const sx = Math.sign(hx - cx)
  const sy = Math.sign(vy - cy)
  return `M${hx} ${cy}L${cx + sx * R} ${cy}Q${cx} ${cy} ${cx} ${cy + sy * R}L${cx} ${vy}`
}
/** Two parallel strokes, GAP apart, for the double-line glyphs. */
const GAP = 1.7
const double = (glyph: string) => (x: number, y: number) => {
  const l = x
  const r = x + CW
  const t = y
  const b = y + CH
  const cx = x + CW / 2
  const cy = y + CH / 2
  const g = GAP
  switch (glyph) {
    case '═':
      return `M${l} ${cy - g}H${r}M${l} ${cy + g}H${r}`
    case '║':
      return `M${cx - g} ${t}V${b}M${cx + g} ${t}V${b}`
    case '╔':
      return `M${r} ${cy - g}H${cx - g}V${b}M${r} ${cy + g}H${cx + g}V${b}`
    case '╗':
      return `M${l} ${cy - g}H${cx + g}V${b}M${l} ${cy + g}H${cx - g}V${b}`
    case '╚':
      return `M${r} ${cy + g}H${cx - g}V${t}M${r} ${cy - g}H${cx + g}V${t}`
    default:
      return `M${l} ${cy + g}H${cx + g}V${t}M${l} ${cy - g}H${cx - g}V${t}`
  }
}
const BOX: Record<string, Stroke> = {
  '─': { d: line([[0, 1, 2, 1]]), weight: 'light' },
  '│': { d: line([[1, 0, 1, 2]]), weight: 'light' },
  '┌': { d: line([[2, 1, 1, 1], [1, 1, 1, 2]]), weight: 'light' },
  '┐': { d: line([[0, 1, 1, 1], [1, 1, 1, 2]]), weight: 'light' },
  '└': { d: line([[2, 1, 1, 1], [1, 1, 1, 0]]), weight: 'light' },
  '┘': { d: line([[0, 1, 1, 1], [1, 1, 1, 0]]), weight: 'light' },
  '├': { d: line([[1, 0, 1, 2], [1, 1, 2, 1]]), weight: 'light' },
  '┤': { d: line([[1, 0, 1, 2], [1, 1, 0, 1]]), weight: 'light' },
  '╭': { d: arc(2, 2), weight: 'light' },
  '╮': { d: arc(0, 2), weight: 'light' },
  '╰': { d: arc(2, 0), weight: 'light' },
  '╯': { d: arc(0, 0), weight: 'light' },
  '━': { d: line([[0, 1, 2, 1]]), weight: 'heavy' },
  '┃': { d: line([[1, 0, 1, 2]]), weight: 'heavy' },
  ...Object.fromEntries(
    [...'═║╔╗╚╝'].map((glyph) => [glyph, { d: double(glyph), weight: 'light' } as Stroke]),
  ),
}
/** Block elements: a left part of the cell (eighths), or a shade of it. */
const BLOCK: Record<string, { w: number; alpha: number }> = {
  '█': { w: 1, alpha: 1 },
  '▉': { w: 7 / 8, alpha: 1 },
  '▊': { w: 6 / 8, alpha: 1 },
  '▋': { w: 5 / 8, alpha: 1 },
  '▌': { w: 4 / 8, alpha: 1 },
  '▍': { w: 3 / 8, alpha: 1 },
  '▎': { w: 2 / 8, alpha: 1 },
  '▏': { w: 1 / 8, alpha: 1 },
  '▓': { w: 1, alpha: 0.75 },
  '▒': { w: 1, alpha: 0.5 },
  '░': { w: 1, alpha: 0.25 },
}

type Text = { x: number; y: number; cells: number; text: string; fill: string; flags: number }
type Rect = { x: number; y: number; w: number; h: number; fill: string; alpha: number }
type Frame = {
  bg: Rect[]
  blocks: Rect[]
  strokes: { d: string; stroke: string; weight: 'light' | 'heavy' }[]
  texts: Text[]
  spins: { x: number; y: number; fill: string }[]
}

function draw(lines: Run[][]): Frame {
  const frame: Frame = { bg: [], blocks: [], strokes: [], texts: [], spins: [] }
  const strokes = new Map<string, string>()
  lines.forEach((runs, row) => {
    const y = row * CH
    let cell = 0
    for (const [text, fg, bg, flags] of runs) {
      const cells = Array.from(text)
      const fill = hero.colors[fg] ?? '#e6edf3'
      if (bg !== SCREEN)
        frame.bg.push({ x: cell * CW, y, w: cells.length * CW, h: CH, fill: hero.colors[bg] ?? '', alpha: 1 })
      // A run's text, cut where a glyph is drawn instead: each piece keeps
      // its first cell and its width, and loses only its outer spaces.
      let piece: string[] = []
      let start = cell
      const flush = () => {
        const joined = piece.join('')
        const lead = joined.length - joined.trimStart().length
        const trimmed = joined.trim()
        if (trimmed)
          frame.texts.push({
            x: (start + lead) * CW,
            y: y + BASELINE,
            cells: Array.from(trimmed).length,
            text: trimmed,
            fill,
            flags,
          })
        piece = []
      }
      cells.forEach((ch, i) => {
        const x = (cell + i) * CW
        const box = BOX[ch]
        const block = BLOCK[ch]
        if (box || block || ch === SPIN[0]) {
          flush()
          start = cell + i + 1
          if (box) {
            const key = `${fill}|${box.weight}`
            strokes.set(key, (strokes.get(key) ?? '') + box.d(x, y))
          } else if (block) {
            const alpha = block.alpha * (flags & DIM ? 0.6 : 1)
            frame.blocks.push({ x, y, w: block.w * CW, h: CH, fill, alpha })
          } else frame.spins.push({ x, y: y + BASELINE, fill })
          return
        }
        piece.push(ch)
      })
      flush()
      cell += cells.length
    }
  })
  for (const [key, d] of strokes) {
    const [stroke = '', weight] = key.split('|')
    frame.strokes.push({ d, stroke, weight: weight === 'heavy' ? 'heavy' : 'light' })
  }
  return frame
}

const screens = hero.screens.map((screen) => ({
  cols: screen.cols,
  rows: screen.rows,
  frames: screen.frames.map((frame) => draw(frame.lines as unknown as Run[][])),
}))

const textStyle = (flags: number) => ({
  fontWeight: flags & BOLD ? 700 : undefined,
  fontStyle: flags & ITALIC ? 'italic' : undefined,
  textDecoration: flags & UNDERLINE ? 'underline' : undefined,
  opacity: flags & DIM ? 0.6 : undefined,
})

// The frames play when the page opens, unless the reader asked for less
// motion; without script, or with reduced motion, the last frame shows.
const root = ref<HTMLElement | null>(null)
onMounted(() => {
  const el = root.value
  if (!el || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  el.classList.add('is-armed')
  requestAnimationFrame(() => el.classList.add('is-playing'))
})
</script>

<template>
  <div ref="root" class="la-hero-screens">
    <div
      v-for="screen in screens"
      :key="screen.cols"
      class="la-hero-screen"
      :class="`la-hero-screen-${screen.cols}`"
    >
      <svg
        v-for="(frame, index) in screen.frames"
        :key="index"
        class="la-hero-frame"
        :viewBox="`0 0 ${screen.cols * CW} ${screen.rows * CH}`"
        aria-hidden="true"
        focusable="false"
      >
        <rect class="la-hero-bg" :width="screen.cols * CW" :height="screen.rows * CH" />
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
          :class="`la-hero-${s.weight}`"
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
        <g v-for="(s, i) in frame.spins" :key="`p${i}`" class="la-hero-spin" :fill="s.fill">
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
    </div>
  </div>
</template>
