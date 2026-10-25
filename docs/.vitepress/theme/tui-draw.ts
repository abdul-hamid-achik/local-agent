// The drawing of a tuimark dump for the site's terminal screens: a frame's
// rows of [text, fg, bg, flags] runs become SVG shapes. Text keeps its cells
// whatever monospace font the reader has; box and block glyphs are drawn as
// paths, so borders join like they do in a terminal. TuiScreen (the home
// page's scenes) and ThemeGallery (the configuration page) share it.

export type Run = [text: string, fg: number, bg: number, flags: number]

/** A cell, in viewBox units: about the 0.6em by 1.2em of a monospace cell. */
export const CW = 10
export const CH = 20
export const BASELINE = 14.6
export const BOLD = 1
export const DIM = 2
export const ITALIC = 4
export const UNDERLINE = 8
/** tuimark's spinner (SPEC v0.4b §12.6): ten glyphs, one every 100 ms. */
export const SPIN = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏']

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
export type Frame = {
  bg: Rect[]
  blocks: Rect[]
  strokes: { d: string; stroke: string; weight: 'light' | 'heavy' }[]
  texts: Text[]
  spins: { x: number; y: number; fill: string }[]
}

/**
 * One frame's shapes from its rows of runs: `colors` is the dump's color
 * table, and `screen` the index of the screen's own background, which rows
 * paint only where theirs differs.
 */
export function draw(lines: Run[][], colors: readonly string[], screen: number): Frame {
  const frame: Frame = { bg: [], blocks: [], strokes: [], texts: [], spins: [] }
  const strokes = new Map<string, string>()
  lines.forEach((runs, row) => {
    const y = row * CH
    let cell = 0
    for (const [text, fg, bg, flags] of runs) {
      const cells = Array.from(text)
      const fill = colors[fg] ?? '#e6edf3'
      if (bg !== screen)
        frame.bg.push({ x: cell * CW, y, w: cells.length * CW, h: CH, fill: colors[bg] ?? '', alpha: 1 })
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

export const textStyle = (flags: number) => ({
  fontWeight: flags & BOLD ? 700 : undefined,
  fontStyle: flags & ITALIC ? 'italic' : undefined,
  textDecoration: flags & UNDERLINE ? 'underline' : undefined,
  opacity: flags & DIM ? 0.6 : undefined,
})
