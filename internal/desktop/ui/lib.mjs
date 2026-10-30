// The parts of the window that are plain functions: no page, no socket.
// Tested with node (uitest/).

import { parseLine } from "./ansi.mjs"
import { layoutRow } from "./term.mjs"

// The keys a terminal is sent by name (pane.send_keys); everything else
// typed arrives as text.
const specialKeys = {
  Enter: "enter", Backspace: "backspace", Tab: "tab", Escape: "esc", Delete: "delete",
  ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right",
  Home: "home", End: "end", PageUp: "pgup", PageDown: "pgdown", Insert: "insert",
  F1: "f1", F2: "f2", F3: "f3", F4: "f4", F5: "f5", F6: "f6",
  F7: "f7", F8: "f8", F9: "f9", F10: "f10", F11: "f11", F12: "f12",
}

// keyFromEvent is the key name for a keydown the pane should get as a
// key: a special key, or a letter with ctrl or alt held. Plain text is
// null — the input event carries it, which keeps accents and input
// methods working. A key with cmd held belongs to the window.
export function keyFromEvent(ev) {
  if (!ev || ev.metaKey || ev.isComposing) return null
  let name = specialKeys[ev.key]
  if (name === "tab" && ev.shiftKey) return "shift+tab"
  if (!name) {
    if (!(ev.ctrlKey || ev.altKey) || typeof ev.key !== "string" || [...ev.key].length !== 1) return null
    name = ev.key === " " ? "space" : ev.key.toLowerCase()
  }
  return (ev.ctrlKey ? "ctrl+" : "") + (ev.altKey ? "alt+" : "") + name
}

// paneRef is a pane's ID outside its machine, as the host keys it.
export const paneRef = (machine, id) => `${machine}:${id}`

// splitRef reads one back; a bare ID is this computer's.
export function splitRef(ref) {
  const i = ref.indexOf(":")
  return i < 0 ? { machine: "local", id: ref } : { machine: ref.slice(0, i), id: ref.slice(i + 1) }
}

// applyEvent is a machine's pane list after one of its server's events.
// Events about anything else leave it as it was (the same array).
export function applyEvent(panes, event, data) {
  panes = panes || []
  switch (event) {
    case "pane.created":
    case "pane.updated":
    case "pane.exited": {
      if (!data || !data.id) return panes
      const i = panes.findIndex((p) => p.id === data.id)
      if (i < 0) return event === "pane.created" ? [...panes, data] : panes
      const next = [...panes]
      next[i] = { ...panes[i], ...data }
      return next
    }
    case "pane.closed":
      if (!data || !data.id) return panes
      return panes.some((p) => p.id === data.id) ? panes.filter((p) => p.id !== data.id) : panes
  }
  return panes
}

// paneLabel is what the sidebar calls a pane: its name, or its agent, or
// its command.
export function paneLabel(p) {
  return p.title || p.name || (p.agent && p.agent.name) || (p.command && p.command[0]) || p.id
}

// sidebar is the rows to draw: each machine, then its panes, a machine
// that is not up saying why instead.
export function sidebar(machines, panesByMachine) {
  const rows = []
  for (const m of machines || []) {
    rows.push({ kind: "machine", machine: m.id, label: m.label, state: m.state, detail: m.detail || "" })
    if (m.state !== "online") continue
    for (const p of panesByMachine[m.id] || []) {
      rows.push({ kind: "pane", machine: m.id, ref: paneRef(m.id, p.id), label: paneLabel(p),
        agent: (p.agent && p.agent.name) || "", state: (p.agent && p.agent.state) || p.state || "" })
    }
  }
  return rows
}

// Calls is the page's side of the host's call messages: an ID for each,
// and the promise it settles.
export class Calls {
  constructor() {
    this.n = 0
    this.waiting = new Map()
  }
  // start returns the message to send and the promise of its result.
  start(machine, method, params) {
    const id = `c${++this.n}`
    const done = new Promise((resolve, reject) => this.waiting.set(id, { resolve, reject }))
    return { message: { t: "call", id, machine, method, params: params ?? {} }, done }
  }
  // settle hands a result message to its caller; false if nobody asked.
  settle(m) {
    const w = this.waiting.get(m.id)
    if (!w) return false
    this.waiting.delete(m.id)
    if (m.error) w.reject(Object.assign(new Error(m.error.message), { code: m.error.code }))
    else w.resolve(m.result ?? null)
    return true
  }
  // failAll rejects every call still waiting, as when the socket closes.
  failAll(reason) {
    for (const w of this.waiting.values()) w.reject(new Error(reason))
    this.waiting.clear()
  }
}

// cellAt is the cell under a point, given in pixels from the canvas's top
// left, held inside the grid: a drag that leaves the pane keeps selecting
// up to its edge.
export function cellAt(x, y, cellW, cellH, cols, rows) {
  const clamp = (v, hi) => Math.max(0, Math.min(hi - 1, v))
  return { col: clamp(Math.floor(x / cellW), cols), row: clamp(Math.floor(y / cellH), rows) }
}

// wheelGoesTo says who the wheel is for: "history" scrolls conch's own
// copy of what scrolled away, "program" sends it as mouse reports. On the
// main screen the wheel is conch's while there is history to show — a
// program that took the mouse there wants clicks, and printed the reports
// as text when given the wheel. On the alternate screen it is the
// program's, which keeps its own scrollback, if it asked for the mouse.
// Alt or ctrl hands it to the other side. The TUI keeps the same rule.
export function wheelGoesTo(frame, mods = {}) {
  if (!frame) return "none"
  let to
  if (frame.alt_screen) to = frame.mouse ? "program" : "history"
  else to = frame.history > 0 || !frame.mouse ? "history" : "program"
  if (mods.altKey || mods.ctrlKey) to = to === "history" ? (frame.mouse ? "program" : "history") : "history"
  if (to === "history" && !(frame.history > 0)) return "none"
  return to
}

// clicksGoTo says whether a click is the program's (it asked for the
// mouse) or the window's, for selecting text. Shift always selects, as in
// every terminal, so text can be copied out of a program that took the
// mouse.
export function clicksGoTo(frame, mods = {}) {
  return frame && frame.mouse && !mods.shiftKey ? "program" : "select"
}

// mouseButton is a DOM button as pane.send_mouse names it.
export function mouseButton(button) {
  return ["left", "middle", "right"][button] || "none"
}

// wheelSteps adds a wheel's movement to what was left over and says how
// many whole steps that makes: a trackpad sends many small movements, a
// wheel a few big ones.
export function wheelSteps(acc, dy, step) {
  const total = (Number(acc) || 0) + (Number(dy) || 0)
  const steps = Math.trunc(total / step) || 0 // not -0
  return { acc: total - steps * step, steps }
}

// scrollOffset is where the view goes after steps of the wheel: up (a
// negative step) is further back into history. 0 is the live screen.
export function scrollOffset(offset, history, steps, linesPerStep = 3) {
  return Math.max(0, Math.min(history || 0, (offset || 0) - steps * linesPerStep))
}

// rowCells is a frame row as one character per cell, with "" in the cell
// a wide character's second half takes.
export function rowCells(line, cols) {
  const cells = new Array(cols).fill(" ")
  for (const p of layoutRow(parseLine(line || ""))) {
    if (p.cells === p.text.length && /^[\x00-\x7f]*$/.test(p.text)) {
      for (let i = 0; i < p.text.length && p.col + i < cols; i++) cells[p.col + i] = p.text[i]
      continue
    }
    if (p.col >= cols) continue
    cells[p.col] = p.text
    for (let i = 1; i < p.cells && p.col + i < cols; i++) cells[p.col + i] = ""
  }
  return cells
}

// ordered is a selection with its start first.
export function ordered(sel) {
  const { a, b } = sel
  return a.row < b.row || (a.row === b.row && a.col <= b.col) ? { a, b } : { a: b, b: a }
}

// selected says whether a cell is inside a selection: from the start cell
// to the end cell, reading order, as a terminal selects.
export function selected(sel, row, col) {
  if (!sel) return false
  const { a, b } = ordered(sel)
  if (row < a.row || row > b.row) return false
  if (row === a.row && col < a.col) return false
  if (row === b.row && col > b.col) return false
  return true
}

// selectionText is what a selection copies: each row's selected cells,
// without the spaces a terminal pads a line out with.
export function selectionText(lines, cols, sel) {
  if (!sel) return ""
  const { a, b } = ordered(sel)
  const out = []
  for (let row = a.row; row <= b.row; row++) {
    const cells = rowCells(lines[row], cols)
    const from = row === a.row ? a.col : 0
    const to = row === b.row ? b.col + 1 : cols
    out.push(cells.slice(from, to).join("").replace(/ +$/, ""))
  }
  return out.join("\n")
}

// agentState is what the sidebar says about a pane: waiting (it asks you
// something), working, done (finished, not looked at), idle, exited — or
// "" for a pane with no agent.
export function agentState(p) {
  if (!p) return ""
  if (p.state === "exited") return "exited"
  if (!p.agent) return ""
  return { blocked: "waiting", working: "working", done: "done", idle: "idle" }[p.agent.state] || "idle"
}

// taskTitle is what a task is called: the name it was given, else its
// window title, else the agent's name.
export function taskTitle(p) {
  return p.name || p.title || (p.agent && p.agent.name) || p.id
}

const stateRank = { waiting: 0, working: 1, done: 2, idle: 3, exited: 4, "": 5 }

// groupTasks is the sidebar's projects, each with its agents as tasks
// (waiting first), and the panes that are no agent's as terminals.
export function groupTasks(machines, panesByMachine, projectsByMachine) {
  const groups = new Map()
  const terminals = []
  for (const m of machines || []) {
    if (m.state !== "online") continue
    const names = new Map((projectsByMachine[m.id] || []).map((pr) => [pr.id, pr.name]))
    for (const p of panesByMachine[m.id] || []) {
      const item = {
        ref: paneRef(m.id, p.id), machine: m.id, pane: p, title: taskTitle(p),
        agent: (p.agent && p.agent.name) || "shell", state: agentState(p),
        project: p.project_id || "", branch: p.branch || "",
      }
      if (!p.agent) { terminals.push(item); continue }
      const key = `${m.id}/${p.project_id || ""}`
      if (!groups.has(key)) {
        const name = p.project_id ? names.get(p.project_id) || p.project_id : "Other agents"
        groups.set(key, { key, name: m.id === "local" ? name : `${name} · ${m.label}`, loose: !p.project_id, tasks: [] })
      }
      groups.get(key).tasks.push(item)
    }
  }
  const out = [...groups.values()]
  for (const g of out) g.tasks.sort((a, b) => stateRank[a.state] - stateRank[b.state] || a.title.localeCompare(b.title))
  // Projects by name, then the agents that belong to none.
  out.sort((a, b) => (a.loose - b.loose) || a.name.localeCompare(b.name))
  return { projects: out, terminals }
}

// waitingCount is how many agents ask something of you.
export function waitingCount(groups) {
  return groups.projects.reduce((n, g) => n + g.tasks.filter((t) => t.state === "waiting").length, 0)
}

// tabsFor is the tabs beside a pane: every pane on its machine in the same
// project and branch (a shell started there included), the pane first.
export function tabsFor(item, panes) {
  if (!item) return []
  const same = (panes || []).filter((p) => p.id !== item.pane.id && item.project &&
    p.project_id === item.project && (p.branch || "") === item.branch)
  return [item.pane, ...same].map((p) => ({ ref: paneRef(item.machine, p.id), title: p.agent ? p.agent.name : taskTitle(p),
    agent: (p.agent && p.agent.name) || "shell", state: agentState(p) }))
}

// choiceLine is one row of a numbered menu an agent draws: "❯ 1. Yes",
// "  2. No", with or without the box round it. As the phone reads it.
const choiceLine = /^[\s│┃|]*([❯›>])?\s*(\d{1,2})\.\s+(\S.*?)[\s│┃|]*$/u

// readMenu finds the numbered menu nearest the bottom of a screen (plain
// rows): numbered from 1 without a gap, exactly one row under the cursor.
// Anything else — a numbered list in an answer — is not a menu to press
// keys into. Returns {labels, cursor, top} — top is the first choice's
// row — or null.
export function readMenu(rows) {
  let best = null, cur = { labels: [], cursor: 0, top: 0 }, cursors = 0
  const flush = () => {
    if (cur.labels.length >= 2 && cursors === 1) best = cur
    cur = { labels: [], cursor: 0, top: 0 }
    cursors = 0
  }
  for (const [i, row] of (rows || []).entries()) {
    const m = choiceLine.exec(row)
    if (!m) { flush(); continue }
    const n = Number(m[2])
    if (n !== cur.labels.length + 1) {
      flush()
      if (n !== 1) continue
    }
    if (n === 1) cur.top = i
    if (m[1]) { cur.cursor = n - 1; cursors++ }
    cur.labels.push(m[3])
  }
  flush()
  return best
}

// choiceKeys are the keys that pick choice i of a menu: arrows from where
// the cursor is, then enter — so a menu that numbers differently from its
// order, or takes no digits, is still answered right.
export function choiceKeys(menu, i) {
  if (!menu || i < 0 || i >= menu.labels.length) return []
  const d = i - menu.cursor
  return [...Array(Math.abs(d)).fill(d < 0 ? "up" : "down"), "enter"]
}

// plainRows is a frame's rows as text, for reading a menu off it.
export function plainRows(frame) {
  return ((frame && frame.lines) || []).map((l) => l.replace(/\x1b\[[0-9;:<=>?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?/g, ""))
}

// changeSummary is a branch's changes as the inspector lists them.
export function changeSummary(ch) {
  const files = ((ch && ch.files) || []).map((f) => {
    const i = f.path.lastIndexOf("/")
    return { path: f.path, name: f.path.slice(i + 1), dir: i < 0 ? "" : f.path.slice(0, i),
      code: f.code, added: f.added || 0, deleted: f.deleted || 0, binary: !!f.binary }
  })
  return {
    files,
    added: files.reduce((n, f) => n + f.added, 0),
    deleted: files.reduce((n, f) => n + f.deleted, 0),
    commits: ((ch && ch.commits) || []).length,
  }
}

// ago is a short time since an ISO date: "now", "4m", "2h", "3d".
export function ago(iso, now = Date.now()) {
  const t = Date.parse(iso)
  if (!Number.isFinite(t)) return ""
  const s = Math.max(0, (now - t) / 1000)
  if (s < 60) return "now"
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

// fitSize is the size to ask a pane for so it fills a box of the given
// pixels, or null when it already has that size or the box is too small to
// mean anything (hidden, or mid-layout): a pane shrunk to 1×1 because its
// panel was momentarily hidden would reflow every line it holds. It is
// compared with the size the pane has — the frame — not with what was
// last asked, because another client may have resized it since.
export function fitSize(frame, width, height, cellW, cellH, min = { cols: 20, rows: 5 }) {
  if (!frame || !(cellW > 0) || !(cellH > 0)) return null
  const cols = Math.floor(width / cellW), rows = Math.floor(height / cellH)
  if (cols < min.cols || rows < min.rows) return null
  if (cols === frame.cols && rows === frame.rows) return null
  return { cols: Math.min(cols, 500), rows: Math.min(rows, 200) }
}

// askReason is what a waiting agent's card says under its name: its own
// message, never a detection rule's code.
export function askReason(agent) {
  const m = agent && agent.message
  if (m && m.trim()) return m.trim()
  return "It is waiting for your answer."
}

// questionAbove is what a menu asks: the paragraph just above its first
// choice, without the box an agent draws round it.
export function questionAbove(rows, top) {
  const out = []
  for (let i = top - 1; i >= 0 && out.length < 2; i--) {
    const text = (rows[i] || "").replace(/^[\s│┃|╭╮╰╯─━┌┐└┘]+|[\s│┃|╭╮╰╯─━┌┐└┘]+$/gu, "")
    if (!text) { if (out.length) break; continue }
    // A prompt line is what was typed, not what is asked.
    if (/^[>$%#❯›] /u.test(text)) break
    out.unshift(text)
  }
  return out.join(" ").slice(0, 300)
}

// parseDiff turns a unified diff into the rows the review draws: hunk
// headers, and lines with their old and new numbers. A comment is pinned
// to a line by its new number (or old, for a deleted one) and its hunk.
export function parseDiff(text) {
  const rows = []
  let oldN = 0, newN = 0, hunk = ""
  for (const line of (text || "").split("\n")) {
    const h = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/.exec(line)
    if (h) {
      oldN = Number(h[1]); newN = Number(h[2]); hunk = line
      rows.push({ kind: "hunk", text: (h[3] || "").trim() || line, hunk })
      continue
    }
    if (!hunk) continue // the file header before the first hunk
    if (line.startsWith("+")) rows.push({ kind: "add", text: line.slice(1), newN: newN++, hunk })
    else if (line.startsWith("-")) rows.push({ kind: "del", text: line.slice(1), oldN: oldN++, hunk })
    else if (line.startsWith("\\")) continue // "\ No newline at end of file"
    else if (line !== "" || newN) rows.push({ kind: "ctx", text: line.slice(1), oldN: oldN++, newN: newN++, hunk })
  }
  // A diff ends with a newline, which split leaves as one empty context
  // row too many.
  if (rows.length && rows[rows.length - 1].kind === "ctx" && rows[rows.length - 1].text === "" && (text || "").endsWith("\n")) rows.pop()
  return rows
}

// lineOf is the line a comment on a diff row names: its new number, or the
// old one for a line that was deleted.
export function lineOf(row) {
  return row.kind === "del" ? row.oldN : row.newN
}

// commentsAt places a branch's comments for one file under their rows:
// a map from row index to its comments. A comment whose line is no longer
// in the diff goes under the hunk it was written on, or the top.
export function commentsAt(rows, comments, file) {
  const at = new Map()
  const put = (i, c) => { if (!at.has(i)) at.set(i, []); at.get(i).push(c) }
  for (const c of comments || []) {
    if (c.file !== file) continue
    let i = rows.findIndex((r) => r.kind !== "hunk" && c.line && lineOf(r) === c.line && (!c.hunk || r.hunk === c.hunk))
    if (i < 0) i = rows.findIndex((r) => r.kind !== "hunk" && c.line && lineOf(r) === c.line)
    if (i < 0) i = Math.max(0, rows.findIndex((r) => r.kind === "hunk" && r.hunk === c.hunk))
    put(i, c)
  }
  return at
}

// plural is a count with its noun: "1 file", "3 files".
export function plural(n, noun) {
  return `${n} ${noun}${n === 1 ? "" : "s"}`
}

// branchChecks is what the inspector's Checks tab lists for a branch: its
// pull request, the pull request's checks, the project's own check and
// what the autopilot has sent back — each {name, state, detail, url},
// state one of passed, failed, running, waiting, none.
export function branchChecks(project, branch, pilot) {
  const rows = []
  const b = ((project && project.branches) || []).find((x) => x.name === branch)
  const pr = b && b.pr
  if (pr) {
    const state = pr.state === "MERGED" ? "passed" : pr.state === "CLOSED" ? "none" : "waiting"
    const review = { APPROVED: "approved", CHANGES_REQUESTED: "changes requested", REVIEW_REQUIRED: "review required" }[pr.review] || ""
    rows.push({ name: `Pull request #${pr.number}`, state, url: pr.url || "",
      detail: [pr.state === "OPEN" ? (pr.draft ? "draft" : "open") : (pr.state || "").toLowerCase(), review].filter(Boolean).join(" · ") })
    if (pr.checks) {
      const s = { pass: "passed", fail: "failed", pending: "running" }[pr.checks] || "none"
      rows.push({ name: "CI", state: s, url: pr.url || "",
        detail: pr.total ? `${pr.passed || 0} of ${pr.total} passed` : { passed: "passed", failed: "failed", running: "running" }[s] || "" })
    }
  }
  const ap = ((pilot && pilot.branches) || []).find((x) => x.branch === branch && (!project || x.project_id === project.id))
  if (ap) {
    if (ap.checking) rows.push({ name: "Project check", state: "running", detail: "running now", url: "" })
    else if (ap.check_failed) rows.push({ name: "Project check", state: "failed", detail: "failing at this commit", url: "" })
    if (ap.fixes) rows.push({ name: "Autopilot", state: "waiting", detail: `sent ${ap.fixes} failure${ap.fixes === 1 ? "" : "s"} back to the agent`, url: "" })
    if (ap.waiting) rows.push({ name: "Merge", state: "waiting", detail: ap.held ? `held: ${ap.held}` : "waiting to merge", url: "" })
  }
  return rows
}
