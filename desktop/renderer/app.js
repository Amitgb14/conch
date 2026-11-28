// The window: the tree of projects and their agents and terminals, a tab
// for each pane opened, each drawn by xterm.js from the pane's own output
// (pane.raw, as Conch.app's Ghostty panes are), and the Agents overview
// drawn by conch-ui. Everything goes to the server through the preload
// (window.conchDesktop), which allows only what this window draws.
import { Terminal } from "./vendor/xterm.mjs"
import { FitAddon } from "./vendor/addon-fit.mjs"
import { agentsState, buildTree, bytes, colors, paneLabel, paneState, waiting } from "./lib/model.mjs"

const conch = window.conchDesktop
const $ = (id) => document.getElementById(id)

let panes = []
let projects = []
let status = { state: "connecting" }
/** Open tabs, in order: a pane's id, or "agents". */
let tabs = []
let current = null
/** Each open pane's terminal. */
const terms = new Map()

// --- talking to the server

async function refresh() {
  if (status.state !== "connected") return
  try {
    const [p, j] = await Promise.all([conch.call("pane.list"), conch.call("project.list")])
    panes = p?.panes ?? []
    projects = j?.projects ?? []
  } catch (e) {
    say(`could not read the panes: ${e.message}`)
  }
  // A tab whose pane is gone closes.
  for (const t of [...tabs]) if (t !== "agents" && !panes.some((p) => p.id === t)) closeTab(t, false)
  draw()
}

let soon = null
function refreshSoon() {
  clearTimeout(soon)
  soon = setTimeout(refresh, 150)
}

conch.onEvent((e) => {
  if (e.event === "pane.output") return output(e.data)
  if (e.event === "pane.exited" || e.event === "pane.closed") ended(e.data, e.event)
  if (e.event.startsWith("pane.") || e.event.startsWith("project.")) refreshSoon()
})

conch.onStatus((s) => {
  const was = status.state
  status = s
  if (s.state === "connected" && was !== "connected") {
    // A new connection holds no pane's output: open terminals start again.
    for (const t of terms.values()) t.subscribed = false
    refresh().then(() => { if (current && current !== "agents") show(current) })
  }
  drawStatus()
  if (s.state !== "connected") drawEmpty()
})

// --- drawing

function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "on") for (const [ev, f] of Object.entries(v)) e.addEventListener(ev, f)
    else if (k === "class") e.className = v
    else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v === true ? "" : v)
  }
  for (const k of kids.flat()) if (k !== null && k !== undefined && k !== false) e.append(k instanceof Node ? k : String(k))
  return e
}

const agentColor = (p) => agentsState([p], [], Date.now()).agents[0]?.color ?? colors.muted

function paneRow(p) {
  const st = paneState(p)
  return el("button", { class: `row-pane ${current === p.id ? "on" : ""}`, title: paneLabel(p), on: { click: () => open(p.id) } },
    el("span", { class: "dotm", style: `background:${p.agent ? agentColor(p) : "transparent"};${p.agent ? "" : "border:1px solid var(--muted)"}` }),
    el("span", { class: "label" }, paneLabel(p)),
    st === "terminal" ? null : el("span", { class: `st ${st}` }, st))
}

function draw() {
  const tree = buildTree(panes, projects)
  $("tree").replaceChildren(
    ...tree.groups.flatMap((g) => [el("div", { class: "group-head" }, g.project.name), ...g.panes.map(paneRow)]),
    ...(tree.loose.length ? [el("div", { class: "group-head" }, "CLI"), ...tree.loose.map(paneRow)] : []),
  )
  $("needs").replaceChildren(...waiting(panes).map((p) =>
    el("div", { class: "need" },
      el("div", { class: "buttons" }, el("strong", { class: "grow" }, paneLabel(p)), el("span", { class: "st waiting" }, "waiting")),
      p.agent?.message ? el("p", {}, p.agent.message) : null,
      el("button", { class: "primary small", on: { click: () => open(p.id) } }, "Open to answer"))))
  drawTabs()
  if (current === "agents") updateAgents()
  if (!current) drawEmpty()
  drawStatus()
}

function drawTabs() {
  $("tabs").replaceChildren(...tabs.map((t) => {
    const p = panes.find((x) => x.id === t)
    const name = t === "agents" ? "Agents" : p ? paneLabel(p) : t
    return el("div", { class: `tab ${current === t ? "on" : ""}`, role: "tab", "aria-selected": current === t ? "true" : "false" },
      el("button", { class: "tab-name", style: "background:none;padding:0", on: { click: () => show(t) } }, name),
      el("button", { class: "x", "aria-label": `Close ${name}`, title: "Close the tab (the pane keeps running)", on: { click: () => closeTab(t, true) } }, "×"))
  }))
}

function drawStatus() {
  const s = status
  $("status").replaceChildren(
    el("span", { class: s.state === "connected" ? "on" : "off" }, s.state === "connected" ? "● connected" : s.state === "connecting" ? "○ connecting…" : "● not connected"),
    s.where ? el("span", {}, s.where) : null,
    s.server ? el("span", {}, `conch ${s.server}`) : null,
    el("span", { class: "grow" }),
    el("span", {}, `${panes.filter((p) => p.agent).length} agents · ${waiting(panes).length} waiting`))
}

function drawEmpty() {
  const box = $("empty")
  box.hidden = !!current && status.state === "connected"
  if (status.state !== "connected") {
    box.replaceChildren(
      el("strong", {}, status.state === "connecting" ? "Reaching conch…" : "Not connected to conch"),
      status.error ? el("code", {}, status.error) : null,
      conch.platform === "win32"
        ? el("p", {}, "conch runs inside WSL: install it there (", el("code", {}, "curl -fsSL https://conch.sh/install | sh"), "), or set CONCH_WSL_DISTRO.")
        : el("p", {}, "Install conch, or set CONCH_BIN to where it is."),
      el("button", { on: { click: () => conch.reconnect() } }, "Try again"))
  } else {
    box.replaceChildren(el("strong", {}, "Open an agent or a terminal from the left"), el("p", {}, "Or the Agents overview, for every agent at once."))
  }
}

function say(text) {
  const s = $("status")
  const note = el("span", { class: "off" }, text)
  s.append(note)
  setTimeout(() => note.remove(), 6000)
}

// --- tabs

function open(id) {
  if (!tabs.includes(id)) tabs.push(id)
  show(id)
}

function show(id) {
  current = id
  for (const [k, t] of terms) t.box.hidden = k !== id
  $("agents-view").hidden = id !== "agents"
  $("empty").hidden = true
  if (id === "agents") updateAgents()
  else attach(id)
  draw()
}

function closeTab(id, pick) {
  tabs = tabs.filter((t) => t !== id)
  const t = terms.get(id)
  if (t) {
    if (t.subscribed && status.state === "connected") conch.call("pane.unsubscribe", { id }).catch(() => {})
    t.term.dispose()
    t.box.remove()
    terms.delete(id)
  }
  if (current === id) {
    current = null
    if (pick && tabs.length) return show(tabs[tabs.length - 1])
    $("agents-view").hidden = true
  }
  draw()
}

// --- a pane's terminal, from its raw output

function attach(id) {
  let t = terms.get(id)
  if (!t) {
    const box = el("div", { class: "term focus" })
    $("views").append(box)
    const term = new Terminal({
      fontFamily: '"JetBrains Mono", "Cascadia Mono", "DejaVu Sans Mono", Menlo, monospace', fontSize: 13, cursorBlink: true,
      allowProposedApi: false, scrollback: 5000,
      theme: { background: "#0f1215", foreground: colors.fg, cursor: colors.accent, selectionBackground: "#2aa5b555" },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(box)
    // Typing goes to the pane; the terminal's own answers to the program
    // are said to be replies, so they take its size for nobody.
    term.onData((d) => conch.call("pane.send_text", { id, text: d }).catch(() => {}))
    term.onBinary((d) => conch.call("pane.send_text", { id, text: d, reply: true }).catch(() => {}))
    // Copy and paste with ctrl+shift+c / v, as Linux terminals do.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown" || !e.ctrlKey || !e.shiftKey) return true
      if (e.code === "KeyC") { navigator.clipboard.writeText(term.getSelection()).catch(() => {}); return false }
      if (e.code === "KeyV") { navigator.clipboard.readText().then((s) => conch.call("pane.send_text", { id, text: s, paste: true })).catch(() => {}); return false }
      return true
    })
    t = { term, fit, box, subscribed: false, cols: 0, rows: 0 }
    terms.set(id, t)
    new ResizeObserver(() => sized(id)).observe(box)
  }
  t.box.hidden = false
  sized(id)
  t.term.focus()
  if (!t.subscribed && status.state === "connected") subscribe(id, t)
}

/** The pane takes the size there is room for, as it does for any client
 *  drawing it; the raw stream only makes sense at the pane's own size. */
function sized(id) {
  const t = terms.get(id)
  if (!t || t.box.hidden) return
  const d = t.fit.proposeDimensions()
  if (!d || !d.cols || !d.rows) return
  if (d.cols === t.cols && d.rows === t.rows) return
  t.cols = d.cols
  t.rows = d.rows
  t.term.resize(d.cols, d.rows)
  if (status.state === "connected") conch.call("pane.resize", { id, cols: d.cols, rows: d.rows }).catch(() => {})
}

async function subscribe(id, t) {
  t.subscribed = true
  try {
    const start = await conch.call("pane.raw", { id })
    t.term.reset()
    if (start?.cols && start?.rows) t.term.resize(start.cols, start.rows)
    t.term.write(bytes(start?.replay))
  } catch (e) {
    t.subscribed = false
    t.term.write(`\r\n\x1b[31mconch: ${e.message}\x1b[0m\r\n`)
  }
}

function output(o) {
  const t = terms.get(o?.id)
  if (!t) return
  if (o.reset) {
    // A fresh replay: the connection fell behind, or the pane changed size.
    t.term.reset()
    if (o.cols && o.rows) t.term.resize(o.cols, o.rows)
  }
  t.term.write(bytes(o.data))
}

function ended(info, event) {
  const t = terms.get(info?.id)
  if (!t) return
  t.box.append(el("div", { class: "ended" }, event === "pane.exited" ? `ended (exit ${info.exit_code ?? 0})` : "closed"))
}

// --- the Agents overview, conch-ui's view

let mounted = false
function updateAgents() {
  if (!window.conchUI) return
  if (!mounted) {
    window.conchUI.mount("agents")
    mounted = true
  }
  window.conchUI.update(agentsState(panes, projects, Date.now(), null))
}

// conch-ui's views speak to their host through window.conchHost; here the
// host is this page.
window.conchHost = {
  post(m) {
    if (m?.view !== "agents") return
    if (m.type === "open" && typeof m.key === "string" && panes.some((p) => p.id === m.key)) open(m.key)
    if (m.type === "newTask") say("New tasks: run `conch task` in a terminal for now.")
  },
}

// --- the rest

$("agents-button").addEventListener("click", () => open("agents"))
$("new-shell").addEventListener("click", async () => {
  try {
    const p = await conch.call("pane.create", { no_project: true })
    await refresh()
    if (p?.id) open(p.id)
  } catch (e) { say(`could not start a terminal: ${e.message}`) }
})
// The overview's times move on with no event to say so.
setInterval(() => { if (current === "agents") updateAgents() }, 15000)

conch.status().then((s) => {
  status = s
  drawStatus()
  if (s.state === "connected") refresh()
  else drawEmpty()
})
