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

// tailRows is the last n rows of a frame that has lost its empty rows at
// the bottom: what an agent said last, without the screen above it.
export function tailRows(rows, n) {
  return n > 0 ? rows.slice(-n) : []
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

// groupByMachine puts the drawer's panes under the machine they are on,
// and each machine's under its projects — the tree's own order, machines
// then projects. Every machine the gateway offers is a section, with
// nothing in it when nothing runs there: a machine that is reachable and
// empty is the one thing a list built from panes alone cannot show, and
// not showing it reads as the phone having lost it.
//
// One machine is the usual case and needs no headings, so it is given
// none: the sections are the projects, as they always were.
export function groupByMachine(panes, machines = []) {
  if (machines.length < 2) return { machines: [], groups: groupAgents(panes) }
  const sections = []
  const byID = new Map()
  for (const m of machines) {
    const s = { id: m.id, label: m.label || m.id, state: m.state || "", detail: m.detail || "", groups: [] }
    byID.set(s.id, s)
    sections.push(s)
  }
  const mine = new Map()
  for (const p of panes) {
    const id = p.machine || "local"
    if (!mine.has(id)) mine.set(id, [])
    mine.get(id).push(p)
  }
  for (const [id, list] of mine) {
    // A pane on a machine the list does not have is still the person's
    // pane: it gets a section of its own rather than being dropped.
    if (!byID.has(id)) {
      const s = { id, label: id, state: "", detail: "", groups: [] }
      byID.set(id, s)
      sections.push(s)
    }
    byID.get(id).groups = groupAgents(list)
  }
  return { machines: sections, groups: [] }
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
  // A pane on another machine is `machine:pane`, and that is what its page
  // is called: /agent/busybox:p1. Matching `p3` alone sent every remote
  // agent's page to the list instead — the row was there, the tap did
  // nothing. The machine is the catalog's shape (lower case, digits,
  // dashes), so nothing else can slip in through the path.
  const t = /^\/agent\/((?:[a-z0-9-]+:)?p[0-9]+)\/terminal\/?$/.exec(path)
  if (t) return { view: "terminal", pane: t[1] }
  const m = /^\/agent\/((?:[a-z0-9-]+:)?p[0-9]+)\/?$/.exec(path)
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
export function fontSizeFor(cols, width, min = 7, max = 14, rows = 0, height = 0) {
  if (!(cols > 0) || !(width > 0)) return max
  // A monospace character is about 0.6 of its size wide. On a phone the
  // comfortable ceiling is the default max; a desktop browser window is
  // several times as wide, and holding to a phone's ceiling there leaves
  // half the window empty — so the text may grow to fill it, as far as the
  // rows still fit the height.
  let size = width / cols / 0.6
  if (rows > 0 && height > 0) size = Math.min(size, height / rows / LINE_HEIGHT)
  return Math.max(min, Math.min(max, Math.floor(size * 10) / 10))
}

// LINE_HEIGHT is the line box a frame's rows are drawn in, as a multiple of
// the font size; app.css sets it.
export const LINE_HEIGHT = 1.25

// fitFontSize is fontSizeFor with the ceiling a window of this width
// deserves: a phone keeps the comfortable 14, a wide window may go bigger
// rather than leaving the half of it empty.
export function fitFontSize(cols, width, rows = 0, height = 0) {
  const max = width >= 900 ? 22 : 14
  return fontSizeFor(cols, width, 7, max, rows, height)
}

// fitPane is the pane size a window of this width and height would show
// comfortably: as many columns as fit at a readable size, within what the
// gateway accepts. Growing the type fills a window only as far as the
// pane's own columns go — a 120-column pane in a wide window runs out of
// columns long before it runs out of room — so a window with room to spare
// asks for the pane to be made bigger instead.
export function fitPane(width, height, min = 20, max = 500, minRows = 5, maxRows = 200, at = READABLE) {
  if (!(width > 0) || !(height > 0) || !(at > 0)) return null
  // Counted at the size the rows are actually drawn at: somebody who chose
  // 10px wants the columns that fit at 10px, not at a size conch prefers.
  const cols = Math.floor(width / (at * 0.6))
  const rows = Math.floor(height / (at * LINE_HEIGHT))
  if (!(cols >= min) || !(rows >= minRows)) return null
  return { cols: Math.min(cols, max), rows: Math.min(rows, maxRows) }
}

// READABLE is the type size a desktop window's columns are counted at.
export const READABLE = 13

// worthResizing says whether a pane is far enough from what the window
// could show to be worth asking to change it: a column or two either way
// is not, and a pane that is already wider than the window is left alone —
// the laptop is looking at it too, and it is theirs.
export function worthResizing(have, want) {
  if (!have || !want) return false
  return want.cols - have.cols >= 8 || want.rows - have.rows >= 4
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

// sortPanes orders every pane: agents as sortAgents does, then terminals,
// oldest first.
export function sortPanes(panes) {
  const agents = sortAgents(panes.filter((p) => p.kind !== "terminal"))
  const terminals = panes.filter((p) => p.kind === "terminal")
    .sort((a, b) => String(a.since).localeCompare(String(b.since)))
  return [...agents, ...terminals]
}

// upsertPane is the list with one pane added or replaced.
export function upsertPane(panes, pane) {
  return sortPanes([...panes.filter((p) => p.pane !== pane.pane), pane])
}

// The keys a hardware keyboard sends that aren't text: what they are
// called in the gateway's `keys` message.
const specialKeys = {
  Enter: "enter", Backspace: "backspace", Tab: "tab", Escape: "esc", Delete: "delete",
  ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right",
  Home: "home", End: "end", PageUp: "pgup", PageDown: "pgdown",
}

// keyFromEvent is the key name for a keydown the terminal should send as
// a key: a special key, or a letter with ctrl or alt held (on top of the
// sticky ones, mods). Plain text returns null — the input event carries
// it, which is what keeps accents and phone keyboards working. A key with
// cmd/meta belongs to the browser.
export function keyFromEvent(ev, mods = {}) {
  if (!ev || ev.metaKey || ev.isComposing) return null
  const ctrl = ev.ctrlKey || mods.ctrl, alt = ev.altKey || mods.alt
  let name = specialKeys[ev.key]
  if (name === "tab" && ev.shiftKey) name = "shift+tab"
  if (!name) {
    if (!(ctrl || alt) || typeof ev.key !== "string" || [...ev.key].length !== 1) return null
    name = ev.key === " " ? "space" : ev.key.toLowerCase()
  }
  return withMods(name, { ctrl, alt })
}

// withMods puts modifiers in front of a key name: "ctrl+alt+x".
export function withMods(key, mods = {}) {
  if (key.includes("+") && key !== "+") {
    const [first] = key.split("+")
    if (["ctrl", "alt", "shift"].includes(first)) return key // already modified
  }
  return (mods.ctrl ? "ctrl+" : "") + (mods.alt ? "alt+" : "") + key
}

// A sticky modifier on the key bar: one tap holds it for the next key,
// a second tap soon after locks it, a tap on a locked one lets it go.
export function tapModifier(mod, now) {
  const { state = "off", at = 0 } = mod || {}
  if (state === "off") return { state: "once", at: now }
  if (state === "once" && now - at < 400) return { state: "locked", at: now }
  return { state: "off", at: now }
}

// usedModifier is a modifier after a key went out with it: a one-off is
// spent, a lock stays.
export function usedModifier(mod) {
  return mod && mod.state === "once" ? { state: "off", at: mod.at } : mod
}

// The key bar under a terminal, after Blink and Termius: the keys a phone
// keyboard lacks. ctrl and alt are sticky modifiers; ^C asks twice.
export const TERMINAL_KEYS = [
  { label: "esc", key: "esc" },
  { label: "tab", key: "tab" },
  { label: "ctrl", mod: "ctrl" },
  { label: "alt", mod: "alt" },
  { label: "←", key: "left" },
  { label: "↑", key: "up" },
  { label: "↓", key: "down" },
  { label: "→", key: "right" },
  { label: "⇧tab", key: "shift+tab" },
  { label: "|", key: "|" },
  { label: "~", key: "~" },
  { label: "/", key: "/" },
  { label: "-", key: "-" },
  { label: "^C", key: "ctrl+c", confirm: true },
]

// kids is what a view may give as an element's children — nodes, strings,
// lists of them nested any deep, and null or false for nothing — as the
// nodes and strings to put in. Given straight to the DOM, a list or a
// null would be written out as text.
export function kids(children) {
  return children.flat(Infinity).filter((c) => c != null && c !== false)
}

// Scrollback, stitched from frames. A frame shows rows of a pane from
// line `history - offset` (counting from its oldest kept line): the live
// screen at offset 0, an older page above it otherwise. `scrollback`
// takes what is known and a frame, and says what to do with the screen:
//   append  — rows that scrolled off the live screen, to put under history
//   prepend — older rows a requested page brought, to put above it
//   live    — the live rows now (absent for a page)
//   reset   — throw the history shown away: it no longer joins up
// and returns the new state {history, top, live}, where top is the oldest
// line shown. Lines are the frame's strings, untouched.
export function scrollback(state, f) {
  const lines = f.lines || [], history = f.history || 0, offset = f.offset || 0
  if (offset > 0) {
    if (!state) return { state, out: {} } // a page nobody asked for yet
    const pageTop = Math.max(0, history - offset)
    if (pageTop >= state.top) return { state, out: {} }
    const prepend = lines.slice(0, Math.min(state.top - pageTop, lines.length))
    return { state: { ...state, top: pageTop }, out: { prepend } }
  }
  if (!state) return { state: { history, top: history, live: lines }, out: { live: lines, reset: true } }
  const moved = history - state.history
  if (moved < 0 || moved > state.live.length) {
    // The history was cleared, or more went by than the screen held.
    return { state: { history, top: history, live: lines }, out: { live: lines, reset: true } }
  }
  return { state: { history, top: state.top, live: lines }, out: { live: lines, append: state.live.slice(0, moved) } }
}

// olderOffset is the offset to ask for to get the page above what is
// shown, or 0 when the oldest line is already there.
export function olderOffset(state) {
  if (!state || state.top <= 0) return 0
  return state.history - state.top + Math.max(1, state.live.length)
}

// wheelSteps turns a swipe or a wheel into wheel steps for a program that
// takes the mouse: dy is how far the content was pulled (down positive:
// toward older lines), step how far one wheel step is. It returns what is
// left over and the steps, positive for up (older), negative for down.
export function wheelSteps(acc, dy, step) {
  const total = (Number(acc) || 0) + (Number(dy) || 0)
  const steps = Math.trunc(total / step) || 0 // not -0
  return { acc: total - steps * step, steps }
}

// ttyInput reads what a keystroke did to the hidden field the terminal is
// typed through. Between keystrokes the field holds only the sentinel, so
// that deleting it is a backspace even when nothing else is there. What
// was typed is what is in the field besides it — before or after it: iOS
// can leave the cursor ahead of the sentinel, and then a letter lands in
// front. A field without the sentinel had it deleted: one backspace, and
// whatever was typed in its place.
export function ttyInput(value, sentinel) {
  const v = String(value ?? "")
  const at = v.indexOf(sentinel)
  if (at < 0) return { backspace: true, text: v }
  return { backspace: false, text: v.slice(0, at) + v.slice(at + sentinel.length) }
}
