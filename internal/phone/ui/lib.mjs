// The parts of the phone app that are plain functions: no page, no
// network. They are tested with node (uitest/lib.test.mjs).

// The 16 colours a terminal names, as xterm draws them.
const base16 = [
  "#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
  "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
]

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

// frameRows is a frame's rows as runs, without the empty rows below the
// last one with anything on it: a terminal is mostly empty under a short
// conversation, and on a phone that is a screenful of nothing. A row that
// is only the cursor counts as something.
export function frameRows(lines) {
  const rows = (lines || []).map(parseLine)
  const blank = (row) => row.every((r) => r.text.trim() === "" && !r.bg && !r.inverse)
  let end = rows.length
  while (end > 1 && blank(rows[end - 1])) end--
  return rows.slice(0, end)
}

// What needs you comes first, as in the TUI's review queue.
const stateOrder = { waiting: 0, done: 1, working: 2, idle: 3 }

// sortAgents orders the list as the gateway does: waiting first, and the
// longest wait first.
export function sortAgents(agents) {
  return [...agents].sort((a, b) =>
    (stateOrder[a.state] ?? 9) - (stateOrder[b.state] ?? 9) || String(a.since).localeCompare(String(b.since)))
}

// upsertAgent is the list with one agent added or replaced.
export function upsertAgent(agents, agent) {
  return sortAgents([...agents.filter((a) => a.pane !== agent.pane), agent])
}

// removeAgent is the list without a pane's agent.
export function removeAgent(agents, pane) {
  return agents.filter((a) => a.pane !== pane)
}

// groupAgents puts the list under its projects, keeping the list's order
// within each and ordering the groups by their first agent — so a project
// with someone waiting is on top.
export function groupAgents(agents) {
  const groups = []
  const byKey = new Map()
  for (const a of agents) {
    const key = a.project ? a.project.id : ""
    if (!byKey.has(key)) {
      const g = { key, name: a.project ? a.project.name || a.project.id : "No project", agents: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    byKey.get(key).agents.push(a)
  }
  return groups
}

// agentLabel is what to call an agent: its task's title, else its name.
export function agentLabel(a) {
  return a.title || a.name || a.pane
}

// ago says how long since a time, briefly: "now", "5m", "2h", "3d".
export function ago(since, now = Date.now()) {
  const t = Date.parse(since)
  if (Number.isNaN(t)) return ""
  const s = Math.max(0, Math.floor((now - t) / 1000))
  if (s < 60) return "now"
  if (s < 3600) return Math.floor(s / 60) + "m"
  if (s < 86400) return Math.floor(s / 3600) + "h"
  return Math.floor(s / 86400) + "d"
}

// route reads the page's path: the list, one agent, its terminal, or a
// new task. Anything else is the list.
export function route(path) {
  const t = /^\/agent\/(p[0-9]+)\/terminal\/?$/.exec(path)
  if (t) return { view: "terminal", pane: t[1] }
  const m = /^\/agent\/(p[0-9]+)\/?$/.exec(path)
  if (m) return { view: "agent", pane: m[1] }
  if (/^\/new\/?$/.test(path)) return { view: "new" }
  if (/^\/settings\/?$/.test(path)) return { view: "settings" }
  return { view: "list" }
}

// can says whether a device with permission `have` may do what needs `need`.
export function can(have, need) {
  const rank = { view: 1, reply: 2, full: 3 }
  return (rank[have] || 0) >= (rank[need] || 99)
}

// backoff is how long to wait before the nth try at reconnecting: quick
// at first, never more than half a minute.
export function backoff(n) {
  return Math.min(30000, 500 * 2 ** Math.max(0, Math.min(n, 10)))
}

// codeFromHash takes a pairing code out of a link's fragment ("#code=438-219"),
// which is what a QR code will hold.
export function codeFromHash(hash) {
  const m = /^#code=([0-9][0-9 -]{4,10}[0-9])$/.exec(hash || "")
  return m ? m[1] : ""
}

// fontSizeFor is the text size at which a frame `cols` wide fits `width`
// pixels, within what is readable; below that the frame scrolls sideways.
export function fontSizeFor(cols, width, min = 7, max = 14) {
  if (!(cols > 0) || !(width > 0)) return max
  // A monospace character is about 0.6 of its size wide.
  return Math.max(min, Math.min(max, Math.floor((width / cols / 0.6) * 10) / 10))
}

// The terminal view's key bar: the keys a phone's keyboard doesn't have,
// named as the gateway's `keys` message takes them (`conch send -keys`).
// ctrl+c asks for a second tap: one stray touch shouldn't stop an agent.
export const KEYBAR = [
  { label: "esc", key: "esc" },
  { label: "tab", key: "tab" },
  { label: "⇧tab", key: "shift+tab" },
  { label: "↑", key: "up" },
  { label: "↓", key: "down" },
  { label: "←", key: "left" },
  { label: "→", key: "right" },
  { label: "⏎", key: "enter" },
  { label: "⌫", key: "backspace" },
  { label: "^C", key: "ctrl+c", confirm: true },
]

// textKeys is text as key names, one per character: what the terminal
// view types when there is no way to send text as such. A space, a new
// line and a tab have names of their own; anything else is itself.
export function textKeys(text) {
  const named = { " ": "space", "\n": "enter", "\r": "enter", "\t": "tab" }
  const keys = []
  for (const ch of String(text ?? "").replace(/\r\n/g, "\n")) {
    if (named[ch]) keys.push(named[ch])
    else if (ch >= " " && ch !== "\x7f") keys.push(ch)
  }
  return keys
}

// chunks splits keys into messages the gateway takes: at most 64 each.
export function chunks(keys, size = 64) {
  const out = []
  for (let i = 0; i < keys.length; i += size) out.push(keys.slice(i, i + size))
  return out
}

// keyBytes is a base64url key as pushManager.subscribe wants it.
export function keyBytes(b64url) {
  const s = String(b64url || "").replace(/-/g, "+").replace(/_/g, "/")
  const bin = atob(s + "=".repeat((4 - (s.length % 4)) % 4))
  return Uint8Array.from(bin, (c) => c.charCodeAt(0))
}

// appPath is an address the app may go to when a notification is
// tapped: one of its own views, never anywhere else.
export function appPath(url) {
  return typeof url === "string" && (url === "/" || route(url).view !== "list") ? url : "/"
}
