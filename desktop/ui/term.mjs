// Paints conch frames onto a canvas. A frame is the server's rendered
// screen — one ANSI-styled string per row — so there is no emulator here:
// rows are parsed into runs, laid out on the cell grid, and only the rows
// whose text changed since the last frame are drawn again.

import { parseLine } from "./ansi.mjs"

// Ranges two cells wide: East Asian wide and full-width, and emoji. Rough
// for the spike; phase 2 takes its widths from the same tables the server
// uses, so a row lines up the same in both.
const wide = [
  [0x1100, 0x115f], [0x231a, 0x231b], [0x2329, 0x232a], [0x23e9, 0x23ec], [0x23f0, 0x23f0],
  [0x23f3, 0x23f3], [0x25fd, 0x25fe], [0x2614, 0x2615], [0x2648, 0x2653], [0x267f, 0x267f],
  [0x2693, 0x2693], [0x26a1, 0x26a1], [0x26aa, 0x26ab], [0x26bd, 0x26be], [0x26c4, 0x26c5],
  [0x26ce, 0x26ce], [0x26d4, 0x26d4], [0x26ea, 0x26ea], [0x26f2, 0x26f3], [0x26f5, 0x26f5],
  [0x26fa, 0x26fa], [0x26fd, 0x26fd], [0x2705, 0x2705], [0x270a, 0x270b], [0x2728, 0x2728],
  [0x274c, 0x274c], [0x274e, 0x274e], [0x2753, 0x2755], [0x2757, 0x2757], [0x2795, 0x2797],
  [0x27b0, 0x27b0], [0x27bf, 0x27bf], [0x2b1b, 0x2b1c], [0x2b50, 0x2b50], [0x2b55, 0x2b55],
  [0x2e80, 0x303e], [0x3041, 0x33ff], [0x3400, 0x4dbf], [0x4e00, 0x9fff], [0xa000, 0xa4cf],
  [0xac00, 0xd7a3], [0xf900, 0xfaff], [0xfe30, 0xfe4f], [0xff00, 0xff60], [0xffe0, 0xffe6],
  [0x1f300, 0x1f64f], [0x1f680, 0x1f6ff], [0x1f900, 0x1f9ff], [0x1fa70, 0x1faff],
  [0x20000, 0x2fffd], [0x30000, 0x3fffd],
]

// charWidth is how many cells a code point takes: 0 for a combining mark
// or zero-width joiner, 2 for a wide one, 1 otherwise.
export function charWidth(cp) {
  if (cp === 0x200d || (cp >= 0x0300 && cp <= 0x036f) || (cp >= 0xfe00 && cp <= 0xfe0f) ||
      (cp >= 0x20d0 && cp <= 0x20ff)) return 0
  if (cp < 0x1100) return 1
  let lo = 0, hi = wide.length - 1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (cp < wide[mid][0]) hi = mid - 1
    else if (cp > wide[mid][1]) lo = mid + 1
    else return 2
  }
  return 1
}

// layoutRow places a row's runs on the grid. ASCII stays in runs, since a
// monospace font lines those up by itself and one fillText per run is far
// cheaper than one per character; anything else is placed on its own at
// its column, because a font's CJK or emoji glyph is rarely exactly two
// cells. Each piece is {col, cells, text, style}.
export function layoutRow(runs) {
  const out = []
  let col = 0
  for (const run of runs) {
    let ascii = "", start = col
    const flush = () => {
      if (ascii) out.push({ col: start, cells: ascii.length, text: ascii, style: run })
      ascii = ""
    }
    for (const ch of run.text) {
      const cp = ch.codePointAt(0)
      if (cp < 0x80) {
        if (!ascii) start = col
        ascii += ch
        col++
        continue
      }
      flush()
      const w = charWidth(cp)
      if (w === 0 && out.length) {
        out[out.length - 1].text += ch // joins the character before it
        continue
      }
      out.push({ col, cells: w, text: ch, style: run })
      col += w
    }
    flush()
  }
  return out
}

// gridFor is how many cells fit in a box of the given CSS pixels.
export function gridFor(width, height, cellW, cellH) {
  if (!(cellW > 0) || !(cellH > 0)) return { cols: 1, rows: 1 }
  return {
    cols: Math.max(1, Math.floor(width / cellW)),
    rows: Math.max(1, Math.floor(height / cellH)),
  }
}

// changedRows lists the rows of next that differ from prev, or every row
// when the size changed.
export function changedRows(prev, next) {
  const rows = []
  const sameSize = prev && prev.cols === next.cols && prev.lines.length === next.lines.length
  for (let i = 0; i < next.lines.length; i++) {
    if (!sameSize || prev.lines[i] !== next.lines[i]) rows.push(i)
  }
  return rows
}

export const theme = { fg: "#d4d4d4", bg: "#1e1e1e" }

export class Term {
  constructor(canvas, { family = "Menlo, 'DejaVu Sans Mono', monospace", size = 13 } = {}) {
    this.canvas = canvas
    this.ctx = canvas.getContext("2d", { alpha: false })
    this.family = family
    this.size = size
    this.prev = null
    this.fonts = new Map()
    this.measure()
  }

  font(bold, italic) {
    const key = (bold ? 1 : 0) | (italic ? 2 : 0)
    let f = this.fonts.get(key)
    if (!f) {
      f = `${italic ? "italic " : ""}${bold ? "bold " : ""}${this.size}px ${this.family}`
      this.fonts.set(key, f)
    }
    return f
  }

  measure() {
    this.ctx.font = this.font(false, false)
    const m = this.ctx.measureText("MMMMMMMMMM")
    this.cellW = m.width / 10
    this.cellH = Math.ceil(this.size * 1.25)
    this.baseline = Math.round(this.size * 0.95)
  }

  // fit sizes the canvas to its box and says how many cells that is.
  fit(width, height) {
    const dpr = globalThis.devicePixelRatio || 1
    const grid = gridFor(width, height, this.cellW, this.cellH)
    this.canvas.style.width = `${grid.cols * this.cellW}px`
    this.canvas.style.height = `${grid.rows * this.cellH}px`
    this.canvas.width = Math.ceil(grid.cols * this.cellW * dpr)
    this.canvas.height = Math.ceil(grid.rows * this.cellH * dpr)
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    this.prev = null // the canvas was cleared
    return grid
  }

  // paint draws a frame, only the rows that changed.
  paint(frame) {
    const ctx = this.ctx
    const rows = changedRows(this.prev, frame)
    let font = ""
    ctx.textBaseline = "alphabetic"
    for (const r of rows) {
      const y = r * this.cellH
      ctx.fillStyle = theme.bg
      ctx.fillRect(0, y, frame.cols * this.cellW, this.cellH)
      const pieces = layoutRow(parseLine(frame.lines[r] || ""))
      for (const p of pieces) {
        const s = p.style
        const fg = (s.inverse ? s.bg : s.fg) || (s.inverse ? theme.bg : theme.fg)
        const bg = s.inverse ? (s.fg || theme.fg) : s.bg
        if (bg) {
          ctx.fillStyle = bg
          ctx.fillRect(p.col * this.cellW, y, p.cells * this.cellW, this.cellH)
        }
        if (p.text.trim() === "") continue
        const f = this.font(s.bold, s.italic)
        if (f !== font) ctx.font = font = f
        ctx.fillStyle = fg
        ctx.globalAlpha = s.dim ? 0.6 : 1
        ctx.fillText(p.text, p.col * this.cellW, y + this.baseline)
        if (s.underline) ctx.fillRect(p.col * this.cellW, y + this.baseline + 2, p.cells * this.cellW, 1)
      }
      ctx.globalAlpha = 1
    }
    this.prev = frame
    return rows.length
  }
}
