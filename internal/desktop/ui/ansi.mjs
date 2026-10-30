// ANSI rows to styled runs. Copied from internal/phone/ui/lib.mjs for the
// window; one copy shared with the phone is still to do.

// The 16 colours a terminal names, as xterm draws them.
const dark16 = [
  "#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
  "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
]

// The same sixteen for a light background: white and the bright colours
// are what a dark terminal draws text in, and on white they vanish.
const light16 = [
  "#000000", "#c4262e", "#0f8a5f", "#8a6d00", "#1f5fbf", "#a1309e", "#0c7c99", "#5f5f5f",
  "#666666", "#d6333b", "#13995f", "#9a7b00", "#2d6fd6", "#b33bb0", "#0f8ba8", "#3a3a3a",
]
let base16 = dark16

// setPalette picks the sixteen colours for a light or a dark background.
export function setPalette(light) {
  base16 = light ? light16 : dark16
}

// color256 is one of xterm's 256 colours as a CSS colour, or null.
export function color256(n) {
  if (!Number.isInteger(n) || n < 0 || n > 255) return null
  if (n < 16) return base16[n]
  if (n >= 232) {
    const v = 8 + (n - 232) * 10
    return rgb(v, v, v)
  }
  n -= 16
  const level = (i) => (i === 0 ? 0 : 55 + i * 40)
  return rgb(level(Math.floor(n / 36)), level(Math.floor(n / 6) % 6), level(n % 6))
}

function rgb(r, g, b) {
  const ok = (v) => Number.isInteger(v) && v >= 0 && v <= 255
  if (!ok(r) || !ok(g) || !ok(b)) return null
  return "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")
}

// Escape sequences: CSI (the m ones are styles, the rest are dropped),
// OSC up to its terminator, and any other two-character escape.
const escape = /\x1b(?:\[([0-9;:<=>?]*)[ -/]*([@-~])|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[0-Z\\^-~])/g

const plain = () => ({ fg: null, bg: null, bold: false, dim: false, italic: false, underline: false, inverse: false })

// extended reads the colour after a 38 or 48: "5;n" or "2;r;g;b". It
// returns the colour and how many parameters it used.
function extended(params, i) {
  if (params[i + 1] === 5) return [color256(params[i + 2]), 2]
  if (params[i + 1] === 2) return [rgb(params[i + 2], params[i + 3], params[i + 4]), 4]
  return [null, params.length] // unknown: nothing after it can be trusted
}

function applySGR(style, text) {
  // "38:2::r:g:b" is the same colour written with colons; the empty
  // colour-space field goes.
  const params = text === "" ? [0] : text.split(";").flatMap((p) =>
    p.includes(":") ? p.split(":").filter((s) => s !== "").map(Number) : [p === "" ? 0 : Number(p)])
  for (let i = 0; i < params.length; i++) {
    const p = params[i]
    if (p === 0) Object.assign(style, plain())
    else if (p === 1) style.bold = true
    else if (p === 2) style.dim = true
    else if (p === 3) style.italic = true
    else if (p === 4) style.underline = true
    else if (p === 7) style.inverse = true
    else if (p === 22) style.bold = style.dim = false
    else if (p === 23) style.italic = false
    else if (p === 24) style.underline = false
    else if (p === 27) style.inverse = false
    else if (p >= 30 && p <= 37) style.fg = base16[p - 30]
    else if (p >= 90 && p <= 97) style.fg = base16[p - 90 + 8]
    else if (p === 39) style.fg = null
    else if (p >= 40 && p <= 47) style.bg = base16[p - 40]
    else if (p >= 100 && p <= 107) style.bg = base16[p - 100 + 8]
    else if (p === 49) style.bg = null
    else if (p === 38 || p === 48) {
      const [color, used] = extended(params, i)
      if (color) style[p === 38 ? "fg" : "bg"] = color
      i += used
    }
  }
}

// parseLine turns one row of a frame — text with ANSI styling — into runs
// of text that share a style. A frame's rows each start unstyled.
export function parseLine(line) {
  const runs = []
  const style = plain()
  let last = 0
  const push = (text) => {
    // What is left of a sequence cut off by the end of the row is not text.
    text = text.replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "")
    if (text !== "") runs.push({ text, ...style })
  }
  escape.lastIndex = 0
  for (let m; (m = escape.exec(line)); ) {
    push(line.slice(last, m.index))
    last = m.index + m[0].length
    if (m[2] === "m" && !/[<=>?]/.test(m[1])) applySGR(style, m[1])
  }
  push(line.slice(last))
  return runs
}

