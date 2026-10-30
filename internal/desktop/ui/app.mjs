// The window's page. Everything goes through one socket to the host, which
// passes calls to the machine they name; this file draws what comes back.
// What can be a plain function lives in lib.mjs, where node tests it.

import { Term, theme as termTheme } from "./term.mjs"
import { setPalette } from "./ansi.mjs"
import { agentIcon, icon, escapeAttr as esc } from "./icons.mjs"
import {
  keyFromEvent, splitRef, applyEvent, Calls, cellAt, wheelGoesTo, clicksGoTo, mouseButton, wheelSteps,
  scrollOffset, ordered, selectionText, groupTasks, waitingCount, tabsFor, readMenu, choiceKeys, plainRows,
  changeSummary, fitSize, askReason, questionAbove, parseDiff, lineOf, commentsAt, plural, branchChecks,
} from "./lib.mjs"

const $ = (id) => document.getElementById(id)
const app = $("app"), canvas = $("term"), ime = $("ime"), box = $("box")

// The address may also say where to start — #view=home, theme=light,
// rail=1 — which is how the window's pictures in the docs are taken.
const start = new URLSearchParams(location.hash.slice(1))
const startOverride = {}
if (["dark", "light", "system"].includes(start.get("theme"))) startOverride.theme = start.get("theme")
if (start.get("rail")) startOverride.rail = start.get("rail") === "1"

// ---- state ----
const calls = new Calls()
let ws = null
let machines = []
const panes = {} // machine → [PaneInfo]
const projects = {} // machine → [ProjectInfo]
let groups = { projects: [], terminals: [] }
let current = null // the pane shown, machine:pane
let view = "session"
let inspectorTab = "changes"
let changes = null // the shown branch's, from project.changes
let pilotState = {} // machine → autopilot.get, for the Checks tab
let agentsAvailable = []
let conchPath = "" // the conch the host runs in a pane (hello)
let autopilot = null
const prefs = { theme: "system", accent: "violet", density: "comfortable", icons: true, rail: false, inspector: true, notify: true, sound: "Glass" }
// override is what the address asked for (#theme=light, rail=1): shown,
// never saved, and dropped for a setting the person then changes.
const override = { ...startOverride }

const term = new Term(canvas, { family: getComputedStyle(document.documentElement).getPropertyValue("--mono") || "Menlo, monospace" })

function call(machine, method, params) {
  const { message, done } = calls.start(machine, method, params)
  ws.send(JSON.stringify(message))
  return done
}

// ---- connecting ----
async function config() {
  const fromHash = start.get("token")
  if (fromHash) {
    history.replaceState(null, "", location.pathname) // out of the address bar
    sessionStorage.setItem("conch-token", fromHash)
    return { token: fromHash, socket: `ws://${location.host}/socket` }
  }
  const kept = sessionStorage.getItem("conch-token")
  if (kept) return { token: kept, socket: `ws://${location.host}/socket` }
  return (await fetch("config.json", { cache: "no-store" })).json() // the window's own scheme
}

async function connect() {
  const cfg = await config()
  ws = new WebSocket(`${cfg.socket}?token=${encodeURIComponent(cfg.token)}`)
  ws.onmessage = (ev) => receive(JSON.parse(ev.data))
  ws.onclose = () => {
    calls.failAll("the connection to conch closed")
    $("termhead").innerHTML = `<strong>Disconnected</strong><span>reopen the window to reconnect</span>`
  }
}

function receive(m) {
  switch (m.t) {
    case "hello":
      conchPath = m.conch || ""
      Object.assign(prefs, (m.state && m.state.prefs) || {})
      machines = m.machines || []
      applyPrefs()
      for (const x of machines) if (x.state === "online") refreshMachine(x.id)
      show(["home", "settings"].includes(start.get("view")) ? start.get("view") : "session")
      wantReview = start.get("view") === "review"
      return
    case "machines":
      machines = m.machines || []
      for (const x of machines) if (x.state === "online") refreshMachine(x.id)
      draw()
      return
    case "result":
      calls.settle(m)
      return
    case "event":
      onEvent(m)
      return
    case "frame":
      if (m.pane === current) paint(m.frame)
      return
  }
}

async function refreshMachine(machine) {
  try {
    const [pl, pr] = await Promise.all([call(machine, "pane.list"), call(machine, "project.list").catch(() => null)])
    panes[machine] = (pl && pl.panes) || []
    if (pr) projects[machine] = pr.projects || []
    draw()
  } catch (e) {
    console.warn(machine, e.message)
  }
}

function onEvent(m) {
  if (m.event.startsWith("pane.")) {
    panes[m.machine] = applyEvent(panes[m.machine], m.event, m.data)
    if (m.event === "pane.closed" && current === `${m.machine}:${m.data.id}`) select(null)
    draw()
  } else if (m.event === "project.updated" || m.event === "project.removed") {
    refreshMachine(m.machine)
  } else if (m.event === "worktree.changed") {
    loadChanges()
    if (view === "review") loadReviewFile()
  } else if (m.event === "review.changed" && view === "review") {
    loadComments()
  }
}

// ---- preferences ----
let saveTimer = 0
function shown() {
  return { ...prefs, ...override }
}
function applyPrefs() {
  const root = document.documentElement
  const p = shown()
  root.dataset.theme = p.theme
  root.dataset.accent = p.accent
  root.dataset.density = p.density
  app.classList.toggle("rail", !!p.rail)
  $("inspector").hidden = !p.inspector
  // The terminal takes its colours from the theme, the sixteen included.
  const css = getComputedStyle(root)
  setPalette(css.getPropertyValue("color-scheme").trim() === "light")
  termTheme.fg = css.getPropertyValue("--text").trim() || termTheme.fg
  termTheme.bg = css.getPropertyValue("--panel").trim() || termTheme.bg
  termTheme.selection = "color-mix(in srgb, " + (css.getPropertyValue("--accent").trim() || "#8B7CF6") + " 35%, transparent)"
  if (term.prev) term.select(selection ? ordered(selection) : null)
}
function setPref(name, value) {
  prefs[name] = value
  delete override[name]
  savePrefs()
}
function savePrefs() {
  applyPrefs()
  clearTimeout(saveTimer)
  saveTimer = setTimeout(() => ws && ws.send(JSON.stringify({ t: "state", state: { v: 1, prefs } })), 300)
}
matchMedia("(prefers-color-scheme: light)").addEventListener("change", applyPrefs)

// ---- drawing ----
function item(ref) {
  for (const g of groups.projects) for (const t of g.tasks) if (t.ref === ref) return t
  return groups.terminals.find((t) => t.ref === ref) || null
}

// first is the task to open on: the one waiting longest on you, else the
// first agent, else nothing — never an empty window when there is work.
let opened = false
function first() {
  const all = groups.projects.flatMap((g) => g.tasks)
  return (all.find((t) => t.state === "waiting") || all[0] || {}).ref || null
}

// ---- the app around the page ----
// In the app the webview offers its bindings (window.go) and events
// (window.runtime); in a browser neither is there and none of this runs.
const native = () => window.go && window.go.main && window.go.main.App
let lastAttention = ""
function reportAttention() {
  const app = native()
  if (!app) return
  const p = shown()
  const waiting = groups.projects.flatMap((g) => g.tasks).filter((t) => t.state === "waiting").map((t) => ({
    ref: t.ref, title: t.title, agent: t.agent,
    question: (t.pane.agent && t.pane.agent.message) || "",
  }))
  const at = { waiting, focused: document.hasFocus(), notify: !!p.notify, sound: p.sound || "" }
  const key = JSON.stringify(at)
  if (key === lastAttention) return
  lastAttention = key
  app.Attention(at).catch(() => {})
}
window.addEventListener("focus", reportAttention)
window.addEventListener("blur", reportAttention)
function onNative() {
  const rt = window.runtime
  if (!rt || !rt.EventsOn) return
  rt.EventsOn("open", (ref) => select(ref))
  rt.EventsOn("menu", (action) => {
    if (action === "new") show("home")
    else if (action === "settings") show("settings")
    else if (action === "rail") setPref("rail", !shown().rail)
    else if (action === "inspector") { setPref("inspector", !shown().inspector); drawSession() }
    else if (action === "review") openReview()
    else if (action === "waiting") $("navWaiting").click()
  })
}

function draw() {
  groups = groupTasks(machines, panes, projects)
  reportAttention()
  if (!opened && !current && view === "session") {
    const ref = first()
    if (ref) { opened = true; select(ref); return }
  }
  drawSide()
  if (view === "session") drawSession()
  if (view === "home") drawHome()
  if (view === "settings") drawSettings()
}

function drawSide() {
  const prefs = shown()
  $("brand").innerHTML = `${icon("conch", 18)}<span>conch</span>`
  $("newTask").innerHTML = `${icon("plus", 16, 2.2)}<span class="text">New task</span><span class="key">⌘N</span>`
  $("newTask").setAttribute("aria-current", String(view === "home"))
  const waiting = waitingCount(groups)
  $("navWaiting").innerHTML = `${icon("inbox")}<span class="text grow">Needs you</span><span class="count ${waiting ? "wait" : ""}">${waiting}</span>`
  $("navWaiting").setAttribute("aria-label", `Needs you, ${waiting}`)
  $("navSettings").innerHTML = `${icon("gear")}<span class="text grow">Settings</span><span class="count">⌘,</span>`
  $("navSettings").setAttribute("aria-current", String(view === "settings"))

  const row = (t) => `<button class="task" type="button" data-ref="${esc(t.ref)}" aria-current="${t.ref === current}" title="${esc(t.title)}">
      <span class="mark">${prefs.icons ? agentIcon(t.agent, 22) : ""}<span class="dot ${t.state || "blank"}"></span></span>
      <span class="label"><span>${esc(t.title)}</span>${t.branch ? `<span class="sub">${esc(t.branch)}</span>` : ""}</span>
    </button>`
  let html = ""
  for (const g of groups.projects) html += `<div class="group"><h3>${esc(g.name)}</h3>${g.tasks.map(row).join("")}</div>`
  if (groups.terminals.length) html += `<div class="group terminals"><h3>Terminals</h3>${groups.terminals.map(row).join("")}</div>`
  if (!html) html = `<p class="note" style="padding: 0 10px">No agents yet. Start one with New task.</p>`
  $("tasks").innerHTML = html

  $("machines").innerHTML = machines.map((m) => `<button class="machine ${m.state}" type="button" title="${esc(m.label)} — ${esc(m.state)}">
      <span class="dot ${m.state}"></span><span class="name">${esc(m.id === "local" ? "This Mac" : m.label)}</span>
      <span class="detail">${esc(m.state === "online" ? (m.version ? "conch " + m.version : "") : m.detail || m.state)}</span>
    </button>`).join("")
}

function drawSession() {
  const prefs = shown()
  const it = item(current)
  $("title").textContent = it ? it.title : "conch"
  $("who").innerHTML = it ? `${prefs.icons ? agentIcon(it.agent, 18) : ""}${esc(it.agent)}` : ""
  $("branch").textContent = it ? it.branch : ""
  $("railToggle").innerHTML = icon("rail")
  $("send").innerHTML = icon("send", 15, 2.2)
  $("inspectorToggle").innerHTML = icon("review")
  $("empty").hidden = !!it
  canvas.hidden = !it
  $("composer").hidden = !it
  $("reply").placeholder = it ? (it.pane.agent ? `Reply to ${it.agent}` : "Type a command") : "Reply"

  const tabs = it ? tabsFor(it, panes[it.machine]) : []
  $("tabs").innerHTML = tabs.length > 1 ? tabs.map((t) => `<button class="tab" role="tab" type="button" data-ref="${esc(t.ref)}" aria-selected="${t.ref === current}">
      ${prefs.icons ? agentIcon(t.agent, 20) : ""}${esc(t.title)}<span class="dot ${t.state || "blank"}"></span></button>`).join("") : ""
  $("tabs").hidden = tabs.length <= 1

  const f = term.prev
  $("termhead").innerHTML = it
    ? `${prefs.icons ? agentIcon(it.agent, 20) : ""}<strong>${esc(it.agent)}</strong><span>${esc(stateWords(it))}</span><span class="grow"></span><span class="size">${f ? `${f.cols}×${f.rows}` : ""}</span>`
    : `<strong>conch</strong>`
  drawAsk(it)
  drawInspector(it)
}

function stateWords(it) {
  const where = it.branch ? ` in ${it.branch}` : ""
  return { waiting: "waiting on you", working: `working${where}`, done: "done — not looked at", idle: "idle", exited: "exited", "": where.trim() }[it.state] ?? ""
}

// A waiting agent's question, with its choices as buttons: read off the
// screen, answered with arrows and enter from where its cursor is.
let menu = null
function drawAsk(it) {
  const ask = $("ask")
  const rows = plainRows(term.prev)
  menu = it && it.state === "waiting" ? readMenu(rows) : null
  if (!it || it.state !== "waiting") { ask.hidden = true; return }
  ask.hidden = false
  const question = (menu && questionAbove(rows, menu.top)) || askReason(it.pane.agent)
  const buttons = menu
    ? menu.labels.map((l, i) => `<button class="btn ${i === menu.cursor ? "primary" : ""}" type="button" data-choice="${i}" title="${esc(l)}"><span class="k">${i + 1}</span>${esc(shortLabel(l))}</button>`).join("")
    : `<button class="btn" type="button" data-focus-term>Answer in the terminal</button>`
  ask.innerHTML = `<div class="qrow"><span class="shield">${icon("shield", 15, 2)}</span>
    <span class="q">${esc(question)}<small>${esc(it.agent)} is waiting on you</small></span></div>
    <div class="choices">${buttons}</div>`
}
function shortLabel(l) {
  return l.length > 44 ? l.slice(0, 42) + "…" : l
}

let wantReview = false // the address asked to open on the review
async function loadChanges() {
  const it = item(current)
  if (!it || !it.project || !it.branch) { changes = null; drawInspector(it); return }
  try {
    changes = await call(it.machine, "project.changes", { project_id: it.project, branch: it.branch })
  } catch {
    changes = null
  }
  const m = machines.find((x) => x.id === it.machine)
  if (m && (m.capabilities || []).includes("autopilot.v1")) {
    call(it.machine, "autopilot.get").then((st) => { pilotState[it.machine] = st; drawInspector(item(current)) }).catch(() => {})
  }
  drawInspector(it)
  if (wantReview) { wantReview = false; openReview() }
}

function drawInspector(it) {
  const prefs = shown()
  const el = $("inspector")
  if (!prefs.inspector) return
  const s = changeSummary(changes)
  const tabs = `<div class="itabs" role="tablist" aria-label="Inspector">
      <button class="itab" role="tab" type="button" data-itab="changes" aria-selected="${inspectorTab === "changes"}">Changes <span class="n">${s.files.length || ""}</span></button>
      <button class="itab" role="tab" type="button" data-itab="commits" aria-selected="${inspectorTab === "commits"}">Commits <span class="n">${s.commits || ""}</span></button>
      <button class="itab" role="tab" type="button" data-itab="checks" aria-selected="${inspectorTab === "checks"}">Checks</button>
    </div>`
  let body
  if (!it || !it.project) body = `<p class="note">Changes show here for a task on a branch of a project.</p>`
  else if (inspectorTab === "changes") {
    body = s.files.length
      ? `<div class="files">${s.files.map((f) => `<button class="file" type="button" title="${esc(f.path)}" data-path="${esc(f.path)}">
          <span class="p">${esc(f.name)} <span>${esc(f.dir)}</span></span>
          <span class="num add">${f.binary ? "bin" : "+" + f.added}</span><span class="num del">${f.deleted ? "−" + f.deleted : ""}</span></button>`).join("")}</div>
        <p class="note">${plural(s.files.length, "file")} · <span class="num add">+${s.added}</span>${s.deleted ? ` <span class="num del">−${s.deleted}</span>` : ""} against ${esc((changes && changes.base) || "the base")}</p>`
      : `<p class="note">No changes on ${esc(it.branch)} yet.</p>`
  } else if (inspectorTab === "checks") {
    const project = (projects[it.machine] || []).find((p) => p.id === it.project)
    const rows = branchChecks(project, it.branch, pilotState[it.machine])
    const mark = { passed: "✓", failed: "✕", running: "•", waiting: "…", none: "–" }
    body = rows.length
      ? `<div class="checks">${rows.map((r) => `<div class="check ${r.state}"><span class="ci">${mark[r.state] || "–"}</span>
          <span class="grow">${r.url ? `<a href="${esc(r.url)}" data-external>${esc(r.name)}</a>` : esc(r.name)}</span><span class="note">${esc(r.detail)}</span></div>`).join("")}</div>`
      : `<p class="note">No pull request or checks for ${esc(it.branch)} yet.</p>`
  } else {
    const cs = (changes && changes.commits) || []
    body = cs.length ? `<div class="commits">${cs.map((c) => `<div><code>${esc((c.hash || "").slice(0, 7))}</code>${esc(c.subject || "")}</div>`).join("")}</div>`
      : `<p class="note">No commits ahead of ${esc((changes && changes.base) || "the base")}.</p>`
  }
  el.innerHTML = tabs + body + (it && it.project ? `<div class="iactions"><button class="btn primary" type="button" data-act="review">Review changes</button></div>` : "")
}

function paint(frame) {
  const resized = !term.prev || term.prev.cols !== frame.cols || term.prev.lines.length !== frame.rows
  if (resized) term.fit(frame.cols * term.cellW, frame.rows * term.cellH)
  term.paint(frame)
  placeIme(frame)
  if (resized) fit()
  const it = item(current)
  if (resized || (it && it.state === "waiting")) drawSession()
}

// ---- fitting the pane to its panel ----
// The panel's size decides the pane's, as a terminal window's does. Asked
// again only when the pane's real size differs (fitSize), and not while a
// resize is settling.
let fitTimer = 0
function fit() {
  clearTimeout(fitTimer)
  fitTimer = setTimeout(() => {
    if (!current || view !== "session") return
    const cs = getComputedStyle(box)
    const w = box.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight)
    const h = box.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom)
    const want = fitSize(term.prev, w, h, term.cellW, term.cellH)
    if (!want) return
    const { machine, id } = splitRef(current)
    call(machine, "pane.resize", { id, ...want }).catch(() => {})
  }, 120)
}
new ResizeObserver(fit).observe(box)

// ---- choosing a task ----
function select(ref) {
  if (ref === current) return
  if (current && ws) ws.send(JSON.stringify({ t: "unsubscribe", pane: current }))
  current = ref
  term.prev = null
  setSelection(null)
  changes = null
  if (ref) {
    ws.send(JSON.stringify({ t: "subscribe", pane: ref }))
    loadChanges()
  }
  show("session")
  if (ref) ime.focus()
}

function show(v) {
  view = v
  $("viewSession").hidden = v !== "session"
  $("viewHome").hidden = v !== "home"
  $("viewSettings").hidden = v !== "settings"
  $("viewReview").hidden = v !== "review"
  if (v === "home") drawHome()
  if (v === "review") drawReview()
  if (v === "settings") drawSettings()
  draw()
}

document.addEventListener("click", (e) => {
  const t = e.target.closest("[data-ref]")
  if (t && (t.classList.contains("task") || t.classList.contains("tab") || t.classList.contains("card"))) { select(t.dataset.ref); return }
  const c = e.target.closest("[data-choice]")
  if (c && menu && current) {
    const { machine, id } = splitRef(current)
    call(machine, "pane.send_keys", { id, keys: choiceKeys(menu, Number(c.dataset.choice)) }).catch(() => {})
    return
  }
  if (e.target.closest("[data-focus-term]")) { ime.focus(); return }
  const ext = e.target.closest("a[data-external]")
  if (ext) {
    e.preventDefault()
    if (window.runtime && window.runtime.BrowserOpenURL) window.runtime.BrowserOpenURL(ext.href)
    else window.open(ext.href, "_blank", "noopener")
    return
  }
  if (e.target.closest("[data-act=review]")) { openReview(); return }
  const fr = e.target.closest(".inspector .file[data-path]")
  if (fr) { openReview(fr.dataset.path); return }
  const it = e.target.closest("[data-itab]")
  if (it) { inspectorTab = it.dataset.itab; drawInspector(item(current)); return }
})
$("newTask").onclick = () => show("home")
$("navSettings").onclick = () => show("settings")
$("navWaiting").onclick = () => {
  const w = groups.projects.flatMap((g) => g.tasks).find((t) => t.state === "waiting")
  if (w) select(w.ref)
}
$("railToggle").onclick = () => setPref("rail", !shown().rail)
$("inspectorToggle").onclick = () => { setPref("inspector", !shown().inspector); drawSession() }
// In a browser the page takes its own shortcuts; in the app they are the
// menu's (onNative), and taking them here too would do each twice.
window.addEventListener("keydown", (e) => {
  if (native() || (!e.metaKey && !e.ctrlKey)) return
  if (e.key === "n" && e.metaKey) { e.preventDefault(); show("home") }
  else if (e.key === "," && e.metaKey) { e.preventDefault(); show("settings") }
  else if (e.key === "b" && e.metaKey) { e.preventDefault(); setPref("rail", !shown().rail) }
})

// ---- replying ----
$("reply").addEventListener("input", () => {
  const r = $("reply")
  r.style.height = "auto"
  r.style.height = Math.min(r.scrollHeight, 160) + "px"
})
$("reply").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); $("composer").requestSubmit() }
})
$("composer").addEventListener("submit", async (e) => {
  e.preventDefault()
  const text = $("reply").value.trim()
  const it = item(current)
  if (!text || !it) return
  const { machine, id } = splitRef(current)
  try {
    if (it.pane.agent) await call(machine, "agent.prompt", { id, text })
    else {
      await call(machine, "pane.send_text", { id, text })
      await call(machine, "pane.send_keys", { id, keys: ["enter"] })
    }
    $("reply").value = ""
    $("reply").style.height = "auto"
  } catch (err) {
    $("termhead").querySelector("span") && ($("termhead").querySelector("span").textContent = err.message)
  }
})

// ---- typing into the terminal (keys by name, the rest as text) ----
function send(method, params) {
  if (!current) return
  const { machine, id } = splitRef(current)
  if (method !== "pane.scroll" && method !== "pane.send_mouse") {
    if (term.prev && term.prev.offset > 0) call(machine, "pane.scroll", { id, offset: 0 }).catch(() => {})
    setSelection(null)
  }
  call(machine, method, { id, ...params }).catch(() => {})
}
ime.addEventListener("keydown", (e) => {
  const key = keyFromEvent(e)
  if (!key) return
  e.preventDefault()
  send("pane.send_keys", { keys: [key] })
})
ime.addEventListener("input", (e) => {
  if (e.isComposing || !ime.value) return
  send("pane.send_text", { text: ime.value })
  ime.value = ""
})
ime.addEventListener("compositionend", () => {
  if (!ime.value) return
  send("pane.send_text", { text: ime.value })
  ime.value = ""
})
ime.addEventListener("paste", (e) => {
  const text = e.clipboardData && e.clipboardData.getData("text/plain")
  e.preventDefault()
  if (text) send("pane.send_text", { text, paste: true })
})
function placeIme(frame) {
  const c = frame.cursor || { row: frame.rows - 1, col: 0 }
  ime.style.left = `${canvas.offsetLeft + c.col * term.cellW}px`
  ime.style.top = `${canvas.offsetTop + c.row * term.cellH}px`
  ime.style.height = `${term.cellH}px`
}

// ---- selection and copy ----
let selection = null
function setSelection(sel) {
  selection = sel
  term.select(sel ? ordered(sel) : null)
}
document.addEventListener("copy", (e) => {
  if (!selection || !term.prev || document.activeElement === $("reply")) return
  e.clipboardData.setData("text/plain", selectionText(term.prev.lines, term.prev.cols, selection))
  e.preventDefault()
})

// ---- the mouse: the program's if it asked (shift selects anyway) ----
let drag = null
function cellOf(e) {
  const r = canvas.getBoundingClientRect()
  const f = term.prev
  return cellAt(e.clientX - r.left, e.clientY - r.top, term.cellW, term.cellH, f.cols, f.rows)
}
function mouseEvent(e, cell, button, action) {
  send("pane.send_mouse", { x: cell.col, y: cell.row, button, action, shift: e.shiftKey, alt: e.altKey, ctrl: e.ctrlKey })
}
canvas.addEventListener("mousedown", (e) => {
  e.preventDefault()
  ime.focus()
  if (!term.prev) return
  const cell = cellOf(e)
  const to = clicksGoTo(term.prev, e)
  drag = { to, button: mouseButton(e.button), moved: false }
  if (to === "program") mouseEvent(e, cell, drag.button, "press")
  else if (e.button === 0) setSelection({ a: cell, b: cell })
})
window.addEventListener("mousemove", (e) => {
  if (!drag || !term.prev) return
  const cell = cellOf(e)
  if (drag.to === "program") { mouseEvent(e, cell, drag.button, "motion"); return }
  if (selection && (cell.row !== selection.b.row || cell.col !== selection.b.col)) {
    drag.moved = true
    setSelection({ a: selection.a, b: cell })
  }
})
window.addEventListener("mouseup", (e) => {
  if (!drag) return
  const d = drag
  drag = null
  if (!term.prev) return
  if (d.to === "program") mouseEvent(e, cellOf(e), d.button, "release")
  else if (!d.moved) setSelection(null)
})
let wheelAcc = 0
canvas.addEventListener("wheel", (e) => {
  const f = term.prev
  if (!f || !current) return
  e.preventDefault()
  const to = wheelGoesTo(f, e)
  const dy = e.deltaMode === 1 ? e.deltaY * 40 : e.deltaY
  const { acc, steps } = wheelSteps(wheelAcc, dy, 40)
  wheelAcc = acc
  if (!steps || to === "none") return
  if (to === "history") { send("pane.scroll", { offset: scrollOffset(f.offset, f.history, steps) }); return }
  const cell = cellOf(e)
  for (let i = 0; i < Math.min(Math.abs(steps), 10); i++) mouseEvent(e, cell, steps < 0 ? "wheel_up" : "wheel_down", "wheel")
}, { passive: false })

// ---- New task ----
let homeErr = ""
async function drawHome() {
  const online = machines.filter((m) => m.state === "online")
  const recent = groups.projects.flatMap((g) => g.tasks).slice(0, 6)
  const machineOpts = online.map((m) => `<option value="${esc(m.id)}">${esc(m.id === "local" ? "This Mac" : m.label)}</option>`).join("")
  const form = $("home").querySelector("form")
  const keep = form ? { prompt: form.prompt.value, machine: form.machine.value, project: form.project.value, agent: form.agent.value } : null
  const mach = (keep && online.some((m) => m.id === keep.machine) && keep.machine) || (online[0] && online[0].id) || "local"
  const projOpts = (projects[mach] || []).map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")
  const agents = (agentsAvailable.length ? agentsAvailable.filter((a) => a.installed) : [{ name: "claude" }]).map((a) => `<option value="${esc(a.name)}">${esc(a.name)}</option>`).join("")
  const waiting = waitingCount(groups)
  const running = groups.projects.reduce((n, g) => n + g.tasks.filter((t) => t.state === "working").length, 0)
  $("home").innerHTML = `
    <div class="hero"><span class="logo">${icon("conch", 22)}</span>
      <h1>What should an agent work on?</h1>
      <p>${running} working · ${waiting} waiting on you</p></div>
    <form class="taskform" id="taskform">
      <label class="sr" for="prompt">Task</label>
      <textarea id="prompt" name="prompt" rows="3" placeholder="Describe the task. It runs on a branch of its own."></textarea>
      ${homeErr ? `<p class="err">${esc(homeErr)}</p>` : ""}
      <div class="row">
        <label class="sr" for="project">Project</label><select class="pill" id="project" name="project">${projOpts || `<option value="">No projects — add one with conch project add</option>`}</select>
        <label class="sr" for="machine">Machine</label><select class="pill" id="machine" name="machine">${machineOpts}</select>
        <span class="grow"></span>
        <label class="sr" for="agent">Agent</label><select class="pill bare" id="agent" name="agent">${agents}</select>
        <button class="send" type="submit" aria-label="Start task">${icon("send", 15, 2.2)}</button>
      </div>
    </form>
    ${recent.length ? `<h2 class="section-title">Recent</h2><div class="recent">${recent.map((t) => `<button class="card" type="button" data-ref="${esc(t.ref)}">
        <span class="top">${agentIcon(t.agent, 24)}<span class="grow">${esc(t.agent)}${t.branch ? " · " + esc(t.branch) : ""}</span><span class="dot ${t.state || "blank"}"></span></span>
        <span class="t">${esc(t.title)}</span><span class="s ${t.state}">${esc({ waiting: "Waiting on you", working: "Working", done: "Done — ready to review", idle: "Idle", exited: "Exited" }[t.state] || "")}</span>
      </button>`).join("")}</div>` : ""}`
  const f = $("taskform")
  // What was being typed survives a redraw; a choice that is no longer
  // offered (a machine that went away, or none known yet) falls back to
  // the first that is.
  const restore = (sel, v) => { if (v && [...sel.options].some((o) => o.value === v)) sel.value = v }
  if (keep) { f.prompt.value = keep.prompt; restore(f.machine, keep.machine); restore(f.project, keep.project); restore(f.agent, keep.agent) }
  f.machine.onchange = () => drawHome()
  f.prompt.onkeydown = (e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); f.requestSubmit() } }
  f.onsubmit = async (e) => {
    e.preventDefault()
    const prompt = f.prompt.value.trim()
    if (!prompt) { f.prompt.focus(); return }
    if (!f.project.value) { homeErr = "Add a project first: conch project add PATH"; drawHome(); return }
    homeErr = ""
    try {
      const info = await call(f.machine.value, "task.create", { project_id: f.project.value, prompt, agent: f.agent.value })
      await refreshMachine(f.machine.value)
      select(`${f.machine.value}:${info.id}`)
    } catch (err) {
      homeErr = err.message
      drawHome()
    }
  }
  if (!agentsAvailable.length && online[0]) {
    call(online[0].id, "agent.status").then((r) => { agentsAvailable = (r && r.agents) || []; if (view === "home") drawHome() }).catch(() => {})
  }
  if (view === "home" && document.activeElement === document.body) f.prompt.focus()
}

// ---- Review ----
// A task's branch, file by file: its diff, the comments on it, and the
// way out — comments back to the agent, or commit, push and a pull request.
const rv = { it: null, file: "", rows: [], comments: [], draft: null, msg: "", err: false, busy: false }

async function openReview(file) {
  const it = item(current)
  if (!it || !it.project || !it.branch) return
  rv.it = it
  rv.msg = ""
  rv.draft = null
  if (!changes) await loadChanges()
  const files = changeSummary(changes).files
  rv.file = file || (files[0] && files[0].path) || ""
  show("review")
  await Promise.all([loadReviewFile(), loadComments()])
}

async function loadReviewFile() {
  const it = rv.it
  rv.rows = []
  if (it && rv.file) {
    try {
      const d = await call(it.machine, "project.diff", { project_id: it.project, branch: it.branch, file: rv.file })
      rv.rows = parseDiff(d && d.diff)
    } catch (e) {
      rv.msg = e.message; rv.err = true
    }
  }
  drawReview()
}

async function loadComments() {
  const it = rv.it
  if (!it) return
  try {
    const r = await call(it.machine, "review.list", { project_id: it.project, branch: it.branch })
    rv.comments = (r && r.comments) || []
  } catch {
    rv.comments = []
  }
  drawReview()
}

function drawReview() {
  const it = rv.it
  if (!it) return
  const s = changeSummary(changes)
  const mine = rv.comments.filter((c) => !c.source)
  $("reviewHead").innerHTML = `<button class="iconbtn" type="button" data-back aria-label="Back to the session">${icon("close")}</button>
    <h1>${esc(it.title)}</h1><span class="who">${agentIcon(it.agent, 18)}${esc(it.agent)}</span>
    <span class="branch">${esc(it.branch)} → ${esc((changes && changes.base) || "base")}</span><span class="grow"></span>
    <span class="note">${plural(s.files.length, "file")} · <span class="num add">+${s.added}</span>${s.deleted ? ` <span class="num del">−${s.deleted}</span>` : ""}</span>`
  const count = (path) => rv.comments.filter((c) => c.file === path).length
  const files = s.files.map((f) => `<button class="file" type="button" data-rfile="${esc(f.path)}" aria-current="${f.path === rv.file}" title="${esc(f.path)}">
      <span class="p">${esc(f.name)} <span>${esc(f.dir)}</span></span>${count(f.path) ? `<span class="c">${count(f.path)}</span>` : ""}</button>`).join("")
  const at = commentsAt(rv.rows, rv.comments, rv.file)
  const thread = (c) => `<div class="thread"><span class="by">${esc(c.by || "You")}${c.source === "github" ? " · on GitHub" : ` · for ${esc(it.agent)}`}</span>${esc(c.note)}</div>`
  const draft = (i) => rv.draft && rv.draft.i === i ? `<form class="thread" data-draft><span class="by">Your comment · for ${esc(it.agent)}</span>
      <label class="sr" for="draftNote">Comment</label><textarea id="draftNote" placeholder="What should ${esc(it.agent)} change here?"></textarea>
      <span class="acts"><button class="btn quiet" type="button" data-cancel>Cancel</button><button class="btn primary" type="submit">Comment</button></span></form>` : ""
  let diff = ""
  rv.rows.forEach((r, i) => {
    if (r.kind === "hunk") diff += `<div class="hunk">${esc(r.text)}</div>`
    else diff += `<div class="r ${r.kind}"><button class="n" type="button" data-line="${i}" aria-label="Comment on line ${lineOf(r)}">${lineOf(r)}</button><span class="t">${esc(r.text)}</span></div>`
    for (const c of at.get(i) || []) diff += thread(c)
    diff += draft(i)
  })
  if (!rv.rows.length) diff = `<p class="empty-diff">${rv.file ? "No changes to show in this file." : "No changes on this branch yet."}</p>`
  $("review").innerHTML = `<nav class="rfiles" aria-label="Changed files">${files}</nav>
    <section class="diff" aria-label="${esc(rv.file)}">${diff}</section>
    <aside class="land" aria-label="Land this branch">
      <div class="box2"><button class="btn ${mine.length ? "primary" : ""}" type="button" data-send ${mine.length ? "" : "disabled"}>${mine.length ? `Send ${plural(mine.length, "comment")} to ${esc(it.agent)}` : "No comments yet"}</button>
        <p class="note">They go to the agent working on this branch, with the file and line, as one message.</p></div>
      <div class="box2"><label class="sr" for="commitMsg">Commit message</label><input id="commitMsg" placeholder="Commit message">
        <button class="btn" type="button" data-commit>Commit all</button>
        <button class="btn" type="button" data-push>Push</button>
        <button class="btn primary" type="button" data-pr>Open pull request</button></div>
      ${rv.msg ? `<p class="msg ${rv.err ? "err" : ""}">${esc(rv.msg)}</p>` : ""}
    </aside>`
  const ta = document.getElementById("draftNote")
  if (ta) ta.focus()
}

async function reviewDo(label, fn) {
  if (rv.busy) return
  rv.busy = true
  rv.msg = label + "…"; rv.err = false
  drawReview()
  try {
    rv.msg = await fn()
  } catch (e) {
    rv.msg = e.message; rv.err = true
  }
  rv.busy = false
  drawReview()
}

document.addEventListener("click", (e) => {
  if (view !== "review" || !rv.it) return
  const it = rv.it
  const where = { project_id: it.project, branch: it.branch }
  if (e.target.closest("[data-back]")) { show("session"); return }
  const f = e.target.closest("[data-rfile]")
  if (f) { rv.file = f.dataset.rfile; rv.draft = null; loadReviewFile(); return }
  const ln = e.target.closest("[data-line]")
  if (ln) { rv.draft = { i: Number(ln.dataset.line) }; drawReview(); return }
  if (e.target.closest("[data-cancel]")) { rv.draft = null; drawReview(); return }
  if (e.target.closest("[data-send]")) {
    reviewDo("Sending", async () => {
      const r = await call(it.machine, "review.send", { ...where, agent: it.agent })
      await loadComments()
      return `Sent ${r.sent} to ${it.agent}.`
    })
    return
  }
  if (e.target.closest("[data-commit]")) {
    const message = $("commitMsg").value.trim()
    if (!message) { $("commitMsg").focus(); return }
    reviewDo("Committing", async () => {
      const r = await call(it.machine, "branch.commit", { ...where, message })
      await loadChanges()
      return `Committed ${(r.hash || "").slice(0, 7)}.`
    })
    return
  }
  if (e.target.closest("[data-push]")) { reviewDo("Pushing", async () => { await call(it.machine, "branch.push", where); return "Pushed." }); return }
  if (e.target.closest("[data-pr]")) {
    reviewDo("Opening a pull request", async () => {
      const r = await call(it.machine, "branch.pr", { ...where, title: it.title })
      return r.url ? `Pull request: ${r.url}` : "Pull request opened."
    })
  }
})
document.addEventListener("submit", async (e) => {
  if (!e.target.matches("[data-draft]")) return
  e.preventDefault()
  const note = document.getElementById("draftNote").value.trim()
  const r = rv.draft && rv.rows[rv.draft.i]
  if (!note || !r) return
  const it = rv.it
  try {
    await call(it.machine, "review.set", { project_id: it.project, branch: it.branch, file: rv.file, hunk: r.hunk, line: lineOf(r), note })
    rv.draft = null
    await loadComments()
  } catch (err) {
    rv.msg = err.message; rv.err = true
    drawReview()
  }
})

// ---- machines, through the CLI in a pane ----
// Adding or upgrading a machine talks to the person — ssh may ask about a
// host key or a password, an older server may need restarting — so it
// runs as conch machine does, in a terminal here, and the window shows it.
async function runConch(name, args) {
  if (!conchPath) return
  try {
    const info = await call("local", "pane.create", { name, command: [conchPath, ...args], no_project: true })
    await refreshMachine("local")
    select(`local:${info.id}`)
  } catch (e) {
    console.warn(e.message)
  }
}
document.addEventListener("submit", (e) => {
  if (!e.target.matches("[data-addmachine]")) return
  e.preventDefault()
  const f = e.target
  const target = f.target.value.trim()
  if (!target || /\s/.test(target)) { f.target.focus(); return }
  const label = f.label.value.trim()
  // "--" so a target starting with "-" is a target, not a flag.
  runConch(`add ${target}`, ["machine", "add", ...(label ? ["-label", label] : []), "--", target])
})
document.addEventListener("click", (e) => {
  if (e.target.closest("[data-update]")) runConch("update conch", ["update"])
  const u = e.target.closest("[data-upgrade]")
  if (u) runConch(`upgrade ${u.dataset.upgrade}`, ["machine", "upgrade", u.dataset.upgrade])
  const m = e.target.closest(".side .machine")
  if (m) { show("settings"); toSection("machines") }
})

// toSection scrolls Settings' own column to a section.
function toSection(id) {
  const page = document.querySelector(".settings .page")
  const el = document.getElementById(id)
  if (page && el) page.scrollTop = el.offsetTop - page.offsetTop - 20
}
document.addEventListener("click", (e) => {
  const a = e.target.closest(".settings nav a[href^='#']")
  if (!a) return
  e.preventDefault()
  toSection(a.getAttribute("href").slice(1))
})

// ---- Settings ----
async function drawSettings() {
  const prefs = shown()
  const local = machines.find((m) => m.id === "local")
  const canPilot = local && (local.capabilities || []).includes("autopilot.v1")
  if (canPilot && !autopilot) {
    call("local", "autopilot.get").then((s) => { autopilot = (s && s.policy) || {}; if (view === "settings") drawSettings() }).catch(() => {})
  }
  const seg = (name, opts) => `<div class="seg" role="radiogroup">${opts.map(([v, l]) =>
    `<button type="button" role="radio" data-pref="${name}" data-val="${v}" aria-checked="${prefs[name] === v}">${l}</button>`).join("")}</div>`
  const sw = (name, label, on) => `<button class="switch" type="button" role="switch" aria-label="${esc(label)}" data-toggle="${name}" aria-checked="${!!on}"></button>`
  const themes = [["dark", "Dark", "#0A0A0B", "#141416", "#2A2A30"], ["light", "Light", "#FFFFFF", "#F1F1F3", "#E1E1E5"], ["system", "Follow system", "#5A5A63", "#3A3A40", "#7A7A84"]]
  const accents = [["violet", "#8B7CF6"], ["blue", "#6EA8FE"], ["green", "#5CC48D"], ["amber", "#F5B544"], ["rose", "#F27FA5"]]
  const fix = autopilot ? autopilot.auto_fix || 0 : 0
  const resolve = autopilot ? autopilot.auto_resolve || 0 : 0
  const pick = (field, value, options) => `<select class="select" data-pilot="${field}" aria-label="${field}">${options.map(([v, l]) =>
    `<option value="${v}" ${Number(v) === value ? "selected" : ""}>${l}</option>`).join("")}</select>`
  $("settings").innerHTML = `
    <nav aria-label="Settings"><h1>Settings</h1>
      <a class="nav" href="#appearance">Appearance</a>${native() ? `<a class="nav" href="#notifications">Notifications</a>` : ""}<a class="nav" href="#autopilot">Autopilot</a><a class="nav" href="#machines">Machines</a></nav>
    <div class="page"><div>
      <section id="appearance"><h2>Appearance</h2><div class="panel">
        <div class="setrow"><div class="what"><div>Theme</div><div>Follow the system, or choose one.</div></div>
          <div class="themes" role="radiogroup">${themes.map(([v, l, bg, side, line]) => `<button class="theme" type="button" role="radio" data-pref="theme" data-val="${v}" aria-checked="${prefs.theme === v}">
            <span class="pv" style="background:${bg}"><i style="background:${side}"></i><b><s style="width:60%;background:${line}"></s><s style="width:85%;background:${line}"></s><u></u></b></span>${l}</button>`).join("")}</div></div>
        <div class="setrow"><div class="what"><div>Accent</div><div>The colour of the main action on every screen.</div></div>
          <div class="swatches" role="radiogroup">${accents.map(([v, c]) => `<button class="swatch" type="button" role="radio" aria-label="${v}" data-pref="accent" data-val="${v}" aria-checked="${prefs.accent === v}" style="background:${c};color:${c}"></button>`).join("")}</div></div>
        <div class="setrow"><div class="what"><div>Density</div><div>How much fits in the sidebar and lists.</div></div>${seg("density", [["comfortable", "Comfortable"], ["compact", "Compact"]])}</div>
        <div class="setrow"><div class="what"><div>Agent icons</div><div>Each agent's mark beside its tasks and tabs.</div></div>
          <span style="display:flex;gap:4px">${["claude", "codex", "gemini", "opencode", "devin"].map((a) => agentIcon(a, 22)).join("")}</span>${sw("icons", "Agent icons", prefs.icons)}</div>
        <div class="setrow"><div class="what"><div>Fold the sidebar</div><div>Icons only, for more room. ⌘B</div></div>${sw("rail", "Fold the sidebar", prefs.rail)}</div>
      </div></section>
      ${native() ? `<section id="notifications"><h2>When an agent waits on you</h2><div class="panel">
        <div class="setrow"><div class="what"><div>Notify me</div><div>A notification names the agent and what it asks; clicking it opens the task. Not while conch is in front.</div></div>${sw("notify", "Notify me", prefs.notify)}</div>
        <div class="setrow"><div class="what"><div>Sound</div><div>Played once, not repeated while it waits.</div></div>
          <select class="select" data-sound aria-label="Sound">${["Glass", "Ping", "Pop", "Tink", ""].map((n) => `<option value="${n}" ${prefs.sound === n ? "selected" : ""}>${n || "None"}</option>`).join("")}</select></div>
        <div class="setrow"><div class="what"><div>Badge the Dock icon</div><div>With how many agents are waiting. Always on.</div></div></div>
      </div></section>` : ""}
      <section id="autopilot"><h2>Autopilot</h2><div class="panel">${canPilot ? `
        <div class="setrow"><div class="what"><div>Send failures back to the agent</div><div>A failed CI run or project check goes to the branch's agent with its log, this many times in a row.</div></div>
          ${pick("auto_fix", fix, [["0", "Never"], ["1", "1 time"], ["3", "3 times"], ["5", "5 times"]])}</div>
        <div class="setrow"><div class="what"><div>Open a pull request when an agent finishes</div><div>Pushed once its check passes; new commits go to the open one.</div></div>${sw("auto_pr", "Open a pull request", autopilot && autopilot.auto_pr)}</div>
        <div class="setrow"><div class="what"><div>Send merge conflicts back to resolve</div><div>After conch's own rebase stops on a conflict.</div></div>
          ${pick("auto_resolve", resolve, [["0", "Never"], ["1", "1 time"], ["2", "2 times"]])}</div>
        <div class="setrow"><div class="what"><div>Merge pull requests that turn green</div><div>When checks pass and nothing holds the pull request back.</div></div>${sw("auto_merge", "Merge green pull requests", autopilot && autopilot.auto_merge)}</div>`
        : `<div class="setrow"><div class="what"><div>Not available</div><div>This computer's conch server is older than the window: reload it with conch server reload.</div></div></div>`}
      </div><p class="note">A TUI that is open hands the server its own autopilot settings when it starts.</p></section>
      <section id="machines"><h2>Machines</h2><div class="panel">${machines.map((m) => `<div class="setrow"><span class="dot ${m.state}"></span><div class="what"><div>${esc(m.id === "local" ? "This Mac" : m.label)}</div><div>${esc(m.state === "online" ? "conch " + (m.version || "") : m.detail || m.state)}</div></div>
          ${m.id !== "local" && conchPath ? `<button class="btn" type="button" data-upgrade="${esc(m.id)}">Upgrade conch there</button>` : ""}</div>`).join("")}
        ${conchPath ? `<div class="setrow"><div class="what"><div>Update conch</div><div>The newest release, installed and the server reloaded onto it; agents keep running. It runs in a terminal, so you can watch it.</div></div>
          <button class="btn" type="button" data-update>Check for updates</button></div>` : ""}
        ${conchPath ? `<form class="setrow" data-addmachine><div class="what"><div>Add a machine</div><div>Over ssh: conch installs itself there if it needs to. It runs in a terminal, so ssh can ask what it needs to.</div></div>
          <label class="sr" for="mTarget">ssh target</label><input class="select" id="mTarget" name="target" placeholder="me@host" required>
          <label class="sr" for="mLabel">Name</label><input class="select" id="mLabel" name="label" placeholder="Name (optional)">
          <button class="btn primary" type="submit">Add</button></form>` : ""}
      </div></section>
    </div></div>`
}
document.addEventListener("click", (e) => {
  const p = e.target.closest("[data-pref]")
  if (p) { setPref(p.dataset.pref, p.dataset.val); drawSettings(); return }
  const t = e.target.closest("[data-toggle]")
  if (!t) return
  const name = t.dataset.toggle
  if (name.startsWith("auto_")) {
    autopilot = { ...(autopilot || {}), [name]: !(autopilot && autopilot[name]) }
    call("local", "autopilot.set", autopilot).catch(() => {})
  } else {
    setPref(name, !shown()[name])
    draw()
  }
  drawSettings()
})
document.addEventListener("change", (e) => {
  if (e.target.matches("[data-sound]")) { setPref("sound", e.target.value); return }
  const s = e.target.closest("[data-pilot]")
  if (!s) return
  autopilot = { ...(autopilot || {}), [s.dataset.pilot]: Number(s.value) }
  call("local", "autopilot.set", autopilot).catch(() => {})
})

// The terminal's cells are measured in its font, which loads after the
// page starts: measure again once it has, or every column is off.
document.fonts.ready.then(() => {
  term.measure()
  const f = term.prev
  if (f) { term.prev = null; paint(f) }
})

applyPrefs()
onNative()
show("session")
connect().catch((e) => { $("termhead").innerHTML = `<strong>Could not connect</strong><span>${esc(e.message)}</span>` })
