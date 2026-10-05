// conch on a phone, after the Claude app: an inbox of what needs you, a
// drawer of every agent and terminal, each agent as a conversation with a
// composer, and its terminal to type into directly. It talks to the
// gateway that served it (docs/plans/phone-api.md) and to nothing else.
//
// Everything the gateway sends is shown with textContent: an agent's
// title, its question and its screen are text, never markup.
import {
  parseLine, frameRows, sortPanes, upsertPane, removeAgent, groupAgents, agentLabel,
  ago, route, can, backoff, codeFromHash, fontSizeFor, fitFontSize, fitPane, worthResizing, READABLE, chunks, keyBytes, appPath,
  keyFromEvent, withMods, tapModifier, usedModifier, TERMINAL_KEYS, kids, scrollback, olderOffset, wheelSteps, ttyInput,
} from "/lib.mjs"

const API_VERSION = 2
// The agents conch can start. There is no route that lists the ones
// installed, so a new agent names one of these or leaves it to conch.
const AGENTS = ["claude", "codex", "gemini", "opencode", "devin"]

const state = {
  hello: null, // who this device is, once paired
  panes: [], // every pane: agents first, then terminals
  machines: [], // every machine the gateway reaches, this computer first
  listed: false, // the list has come from the gateway at least once
  online: false,
  reached: 0, // when the gateway last answered
  ws: null,
  tries: 0,
  heard: 0, // when the socket last said anything
  view: null, // the view on screen: { update(), leave(), frame(), … }
}

const $ = (id) => document.getElementById(id)

// h builds an element. Children are nodes or strings; strings become text.
function h(tag, props = {}, ...children) {
  const el = document.createElement(tag)
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") el.className = v
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v)
    else if (v === true) el.setAttribute(k, "")
    else if (v !== false && v != null) el.setAttribute(k, v)
  }
  el.append(...kids(children))
  return el
}

// put replaces an element's content with children as h takes them.
const put = (el, ...children) => el.replaceChildren(...kids(children))

const paneOf = (id) => state.panes.find((p) => p.pane === id)
const mayReply = () => can(state.hello?.permission, "reply")
const mayType = () => can(state.hello?.permission, "full")

// ---- the gateway ----

class APIError extends Error {
  constructor(code, message, status) {
    super(message)
    this.code = code
    this.status = status
  }
}

async function api(method, path, body) {
  const headers = {}
  if (body !== undefined) headers["Content-Type"] = "application/json"
  if (method !== "GET" && state.hello) headers["X-CSRF-Token"] = state.hello.csrf_token
  let res
  try {
    res = await fetch(path, { method, headers, credentials: "same-origin", body: body === undefined ? undefined : JSON.stringify(body) })
  } catch {
    setOnline(false)
    throw new APIError("offline", "Your laptop can't be reached.", 0)
  }
  reachedNow()
  const data = res.status === 204 ? null : await res.json().catch(() => null)
  if (res.ok) return data
  const err = new APIError(data?.error?.code || "error", data?.error?.message || res.statusText, res.status)
  if (err.code === "unauthorized") unpaired()
  throw err
}

function reachedNow() {
  state.reached = Date.now()
  try { localStorage.setItem("conch.reached", String(state.reached)) } catch { /* private mode */ }
}

function setOnline(online) {
  if (state.online === online) return
  state.online = online
  showBanner()
  showLive()
  state.view?.update?.()
}

function showBanner() {
  const banner = $("banner")
  banner.hidden = state.online || !state.hello
  if (banner.hidden) return
  const at = state.reached ? new Date(state.reached).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : ""
  banner.textContent = at
    ? `Your laptop can't be reached — asleep or off the tailnet. Last reached ${at}; this is what it showed then.`
    : "Your laptop can't be reached — asleep or off the tailnet."
}

function showLive() {
  const live = $("live")
  live.hidden = !state.hello
  live.classList.toggle("on", state.online)
  live.title = state.online ? "Connected to your laptop" : "Not connected"
}

// The list is kept between visits so there is something to show while the
// laptop sleeps. Questions are left out: they can quote a command.
function remember() {
  try {
    localStorage.setItem("conch.panes", JSON.stringify(state.panes.map(({ question, ...rest }) => rest)))
  } catch { /* private mode */ }
}

function recall() {
  try {
    const panes = JSON.parse(localStorage.getItem("conch.panes") || "[]")
    if (Array.isArray(panes)) state.panes = sortPanes(panes.filter((p) => p && typeof p.pane === "string"))
    state.reached = Number(localStorage.getItem("conch.reached")) || 0
  } catch { /* nothing kept */ }
}

function forget() {
  try {
    for (const k of ["conch.panes", "conch.agents", "conch.reached"]) localStorage.removeItem(k)
  } catch { /* private mode */ }
}

function setPanes(panes) {
  state.panes = panes
  state.listed = true
  remember()
  state.view?.update?.()
  drawer.update()
}

// ---- the socket ----

function connect() {
  if (state.ws || !state.hello || state.hello.offline) return
  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/socket`)
  state.ws = ws
  ws.onopen = () => {
    state.tries = 0
    state.heard = Date.now()
    reachedNow()
    setOnline(true)
    send({ type: "panes.watch" })
    state.view?.connected?.()
    api("GET", "/api/hello").then((hello) => {
      if (!hello) return
      if (hello.machines) state.machines = hello.machines
      if (state.hello && hello.permission !== state.hello.permission) {
        state.hello = hello
        render() // a permission changed on the laptop: new controls, or fewer
      }
    }).catch(() => {})
  }
  ws.onmessage = (ev) => {
    state.heard = Date.now()
    let m
    try { m = JSON.parse(ev.data) } catch { return }
    onMessage(m)
  }
  ws.onclose = () => {
    if (state.ws !== ws) return
    state.ws = null
    setOnline(false)
    if (state.hello) setTimeout(connect, backoff(state.tries++))
  }
}

function send(m) {
  if (state.ws?.readyState === WebSocket.OPEN) state.ws.send(JSON.stringify(m))
}

function onMessage(m) {
  switch (m.type) {
    case "panes":
      setPanes(sortPanes(m.panes || []))
      break
    case "machines":
      // A machine coming up or going says so on its own; the rows it
      // contributes arrive as panes, so only the strip changes here.
      state.machines = m.machines || []
      state.view?.update?.()
      break
    case "pane.changed":
      if (m.info) setPanes(upsertPane(state.panes, m.info))
      break
    case "pane.gone":
      setPanes(removeAgent(state.panes, m.pane))
      break
    case "frame":
      if (m.frame) state.view?.frame?.(m.frame)
      break
    case "error":
      state.view?.socketError?.(m.error)
      break
    case "bye":
      if (m.reason === "revoked") unpaired()
      break
  }
}

// A socket on a phone can die without saying so — the screen locks, the
// network changes. Ping, and start again when nothing has come back.
setInterval(() => {
  if (!state.ws) return
  if (Date.now() - state.heard > 60000) state.ws.close()
  else send({ type: "ping" })
}, 25000)

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && state.hello && !state.ws) {
    state.tries = 0
    connect()
  }
})

// ---- pieces ----

// dot is a pane's state as a small coloured mark; pill the same with its word.
const dot = (p) => h("span", { class: `dot ${p.kind === "terminal" ? "terminal" : p.state}`, title: p.kind === "terminal" ? "terminal" : p.state })
const pill = (s) => h("span", { class: `pill ${s}` }, s)

function subtitle(p) {
  return [p.kind === "terminal" ? "terminal" : p.agent, p.project?.name, p.branch].filter(Boolean).join(" · ")
}

// machineName is a machine's label, or its id until the list arrives.
function machineName(id) {
  const m = state.machines.find((x) => x.id === id)
  return m ? m.label : id
}

// elsewhere says a pane is on another machine. On a phone that reaches only
// this computer — the usual case — there is nothing to say, so nothing is
// drawn: the machine is only ever news when there is more than one.
function elsewhere(p) {
  if (!p.machine || p.machine === "local" || state.machines.length < 2) return null
  return h("span", { class: "machine-tag" }, machineName(p.machine))
}

// row is one pane in a list: what it is, where, and how long it has been so.
function row(p, extra) {
  return h("a", { href: `/agent/${p.pane}`, "data-nav": true, class: `row ${p.state}` },
    dot(p),
    h("span", { class: "body" },
      h("span", { class: "name" }, agentLabel(p), elsewhere(p)),
      h("span", { class: "sub" }, subtitle(p) || p.cwd || "")),
    h("span", { class: "when" }, ago(p.since)),
    extra)
}

// machineStrip says what is wrong with a machine, and nothing at all when
// every machine answers: an empty list is then an empty list, not a phone
// that has quietly lost half of them.
function machineStrip() {
  const trouble = state.machines.filter((m) => m.state !== "online")
  if (!trouble.length) return null
  return h("div", { class: "machines" }, trouble.map((m) =>
    h("div", { class: `machine ${m.state}` },
      h("span", { class: "machine-dot" }),
      h("span", { class: "machine-name" }, m.label),
      h("span", { class: "machine-what" },
        m.state === "connecting" ? "connecting…" : m.detail || "not answering"))))
}

// choiceButtons answers a question with a tap. The question's id goes
// with the choice, so one made after the question changed is refused.
function choiceButtons(p, q, onError) {
  const buttons = q.choices.map((c) => h("button", { class: "choice" },
    h("span", { class: "num" }, c.choice), h("span", { class: "text" }, c.label)))
  buttons.forEach((b, i) => b.addEventListener("click", async (ev) => {
    ev.preventDefault()
    buttons.forEach((x) => { x.disabled = true; x.classList.toggle("chosen", x === b) })
    try {
      await api("POST", "/api/answer", { pane: p.pane, question_id: q.id, choice: q.choices[i].choice })
    } catch (err) {
      onError(err.code === "question_changed" ? "It is asking something else now; nothing was sent."
        : err.code === "not_waiting" ? "It is no longer waiting; nothing was sent." : err.message)
      buttons.forEach((x) => { x.disabled = false; x.classList.remove("chosen") })
    }
  }))
  return h("div", { class: "choices" }, buttons)
}

// questionCard is what a waiting agent asks, answerable in place.
function questionCard(p, onError) {
  const q = p.question
  const card = h("div", { class: "question" },
    h("div", { class: "kicker" }, "Waiting for you"),
    h("p", { class: "ask" }, q?.text || "The agent is waiting for an answer."))
  if (q?.choices?.length && mayReply()) card.append(choiceButtons(p, q, onError))
  else if (mayType()) card.append(h("a", { href: `/agent/${p.pane}/terminal`, "data-nav": true, class: "button" }, "Answer in the terminal"))
  else card.append(h("p", { class: "hint" }, mayReply() ? "Its choices couldn't be read; answer it at your laptop." : "This device can look but not answer."))
  return card
}

// ---- the sheet: a panel from the bottom for choices and forms ----

const sheet = {
  open(title, ...content) {
    const box = $("sheet")
    put(box, 
      h("div", { class: "scrim", onclick: () => sheet.close() }),
      h("div", { class: "panel", role: "dialog", "aria-label": title },
        h("div", { class: "grip" }),
        h("div", { class: "sheet-head" }, h("h3", {}, title), h("button", { class: "icon", "aria-label": "Close", onclick: () => sheet.close() }, "×")),
        content))
    box.hidden = false
  },
  close() {
    $("sheet").hidden = true
    $("sheet").replaceChildren()
  },
}

// ---- the drawer: every pane by project, as Claude lists its chats ----

const drawer = {
  open() {
    this.update()
    $("drawer").hidden = false
  },
  close() { $("drawer").hidden = true },
  update() {
    const nav = $("drawer-list")
    if (!nav || !state.hello) return
    const groups = groupAgents(state.panes)
    put(nav, 
      mayType() ? h("button", { class: "new wide", onclick: () => { drawer.close(); openNew() } }, "＋ New") : null,
      h("a", { href: "/", "data-nav": true, class: "nav-item" }, "Inbox"),
      groups.length === 0 ? h("p", { class: "hint" }, state.listed ? "Nothing running." : "") : null,
      groups.map((g) => [h("h4", {}, g.name), g.agents.map((p) => row(p))]),
      h("a", { href: "/settings", "data-nav": true, class: "nav-item foot" }, "⚙  Settings"))
  },
}

// ---- views ----

// show puts a view on screen under a header: its title, what it is about,
// a way back, and its own actions.
function show(view, { title = "conch", sub = "", back = "", more = null } = {}) {
  state.view?.leave?.()
  state.view = view
  sheet.close()
  drawer.close()
  // After the old view has closed its frames: going from an agent to its
  // terminal closes and opens the same pane, in that order.
  view.connected?.()
  $("title").textContent = title
  $("sub").textContent = sub
  $("back").hidden = !back
  if (back) $("back").setAttribute("href", back)
  $("menu").hidden = !!back || !state.hello
  $("more").hidden = !more
  state.more = more
  document.body.dataset.view = view.name || ""
  $("view").replaceChildren(view.el)
  view.update?.()
  window.scrollTo(0, 0)
}

function navigate(path) {
  if (path !== location.pathname) history.pushState(null, "", path)
  render()
}

function render() {
  showLive()
  if (!state.hello) return show(pairView(), { title: "conch" })
  const r = route(location.pathname)
  if (r.view === "agent" || r.view === "terminal") {
    const p = paneOf(r.pane)
    // A terminal has no conversation: it opens on its screen.
    const term = r.view === "terminal" || p?.kind === "terminal"
    return show(term ? terminalView(r.pane) : chatView(r.pane), {
      title: p ? agentLabel(p) : r.pane, sub: p ? subtitle(p) : "", back: "/", more: () => paneMenu(r.pane),
    })
  }
  if (r.view === "settings") return show(settingsView(), { title: "Settings", back: "/" })
  if (r.view === "new" && mayType()) { navigate("/"); return openNew() }
  show(inboxView(), { title: "conch" })
}

document.addEventListener("click", (ev) => {
  const a = ev.target.closest?.("a[data-nav]")
  if (!a || ev.metaKey || ev.ctrlKey) return
  ev.preventDefault()
  drawer.close()
  navigate(a.getAttribute("href"))
})
window.addEventListener("popstate", render)

function unpaired() {
  if (!state.hello) return
  state.hello = null
  state.panes = []
  forget()
  state.ws?.close()
  state.ws = null
  showBanner()
  render()
}

function deviceName() {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return "iPhone"
  if (/iPad/.test(ua)) return "iPad"
  if (/Android/.test(ua)) return "Android phone"
  return "Browser"
}

function pairView() {
  const status = h("p", { class: "error", role: "alert" })
  const code = h("input", { class: "code", inputmode: "numeric", autocomplete: "one-time-code", placeholder: "123-456", required: true, value: codeFromHash(location.hash), "aria-label": "Pairing code" })
  const name = h("input", { maxlength: "64", value: deviceName(), "aria-label": "This device's name" })
  const button = h("button", { class: "primary wide" }, "Pair")
  const form = h("form", {
    class: "card",
    onsubmit: async (ev) => {
      ev.preventDefault()
      button.disabled = true
      status.textContent = ""
      try {
        await api("POST", "/pair", { code: code.value, device_name: name.value })
        history.replaceState(null, "", location.pathname) // the code is spent; drop it from the address
        state.justPaired = true
        await start()
      } catch (err) {
        status.textContent = err.message
      } finally {
        button.disabled = false
      }
    },
  },
  h("label", {}, "Pairing code", code),
  h("label", {}, "This device's name", name),
  button, status)
  if (state.justPaired) {
    // Paired, and still not in: the cookie didn't stick — blocked
    // cookies, or a window that keeps none.
    state.justPaired = false
    status.textContent = "Paired, but this browser didn't keep the device's key (are cookies blocked for this site?). " +
      "Allow them and pair again with a new code."
  }
  // Over plain http (other than on this computer) the browser won't keep
  // the device's key, and the gateway refuses to pair: say so first.
  if (!window.isSecureContext) {
    button.disabled = true
    status.textContent = "This page isn't on HTTPS, so this browser can't keep the device's key. " +
      "Open conch at its HTTPS address (the one tailscale serve gives, passed to conch web as -url) and pair there."
  }
  return {
    name: "pair",
    el: h("section", { class: "pair" },
      h("img", { src: "/icon.svg", alt: "", class: "logo" }),
      h("h1", {}, "Your agents, in your pocket"),
      h("p", { class: "quiet" }, "On your laptop, press ", h("kbd", {}, "P"), " in conch or run ", h("code", {}, "conch web pair"),
        ", then scan its QR code with your camera — or type the code here."),
      form),
  }
}

// inboxView is the home screen: what needs you first, answerable in
// place, then everything else.
function inboxView() {
  const el = h("section", { class: "inbox" })
  const note = h("p", { class: "error", role: "alert" })
  const update = () => {
    const waiting = state.panes.filter((p) => p.kind !== "terminal" && p.state === "waiting")
    const done = state.panes.filter((p) => p.kind !== "terminal" && p.state === "done")
    const working = state.panes.filter((p) => p.kind !== "terminal" && p.state === "working")
    const idle = state.panes.filter((p) => p.kind !== "terminal" && p.state === "idle")
    const terminals = state.panes.filter((p) => p.kind === "terminal")
    const section = (title, items, render = (p) => row(p)) => items.length
      ? [h("h2", {}, title, h("span", { class: "count" }, String(items.length))), h("div", { class: "list" }, items.map(render))]
      : []
    const greeting = waiting.length
      ? `${waiting.length === 1 ? "One agent needs" : `${waiting.length} agents need`} you`
      : state.panes.length ? "Nothing needs you" : state.listed ? "No agents running" : state.online ? "Loading…" : "Nothing to show yet"
    put(el, 
      h("h1", { class: "greeting" }, greeting),
      state.listed && !state.panes.length && mayType()
        ? h("button", { class: "primary", onclick: () => openNew() }, "Start something") : null,
      note,
      machineStrip(),
      waiting.length ? h("div", { class: "needs" }, waiting.map((p) =>
        h("div", { class: "card waiting-card" }, row(p), questionCard(p, (m) => { note.textContent = m })))) : null,
      section("Finished", done), section("Working", working), section("Idle", idle), section("Terminals", terminals))
  }
  const tick = setInterval(update, 30000) // the ages move on
  return { name: "inbox", el, update, leave: () => clearInterval(tick) }
}

// chatView is an agent as a conversation: what it asks, what it said
// last, and a composer to reply — or stop it while it works.
function chatView(pane) {
  const status = h("div", { class: "status" })
  const ask = h("div", {})
  const scr = paneScreen(pane, "tail", "What the agent said: scroll up for earlier output")
  const tail = scr.el
  // The conversation draws the pane's own rows, so it is as wide as the
  // pane and no wider: a window with room to spare asks for more columns,
  // exactly as the terminal view does.
  const askWide = widener(pane, tail)
  const note = h("p", { class: "error", role: "alert" })
  const text = h("textarea", { placeholder: "Reply…", rows: "1", "aria-label": "Reply" })
  const action = h("button", { class: "send", "aria-label": "Send" }, "↑")
  const hint = h("p", { class: "hint composer-hint" })
  text.addEventListener("input", () => { // grows with what is typed
    text.style.height = "auto"
    text.style.height = Math.min(text.scrollHeight, 160) + "px"
    update()
  })
  let stopping = false
  const composer = h("form", {
    class: "composer",
    hidden: !mayReply(),
    onsubmit: async (ev) => {
      ev.preventDefault()
      const p = paneOf(pane)
      if (!text.value.trim()) {
        // Nothing typed and it's working: the button stops it, as Esc does
        // at the laptop.
        if (p?.state === "working" && mayType()) {
          stopping = true
          send({ type: "keys", id: "stop", pane, keys: ["esc"] })
          setTimeout(() => { stopping = false; update() }, 1500)
          update()
        }
        return
      }
      action.disabled = true
      note.textContent = ""
      try {
        await api("POST", "/api/reply", { pane, text: text.value })
        text.value = ""
        text.style.height = "auto"
      } catch (err) {
        note.textContent = err.code === "agent_blocked" ? "It is waiting for an answer, so nothing was sent. Answer its question first." : err.message
      } finally {
        update()
      }
    },
  }, hint, h("div", { class: "field" },
    mayType() ? h("a", { href: `/agent/${pane}/terminal`, "data-nav": true, class: "icon", "aria-label": "Terminal" }, "⌨") : null,
    text, action))
  const el = h("section", { class: "chat" }, status, ask, note,
    h("div", { class: "section-head" }, h("h2", {}, "Output"),
      h("a", { href: `/agent/${pane}/terminal`, "data-nav": true, class: "link" }, "Terminal ›")),
    tail, composer)

  let shown = null
  const update = () => {
    const p = paneOf(pane)
    if (!p) {
      put(status, h("p", { class: "quiet" }, state.listed ? "This agent has gone: its pane closed, or the agent left it." : "Loading…"))
      ask.replaceChildren()
      composer.hidden = true
      return
    }
    $("title").textContent = agentLabel(p)
    $("sub").textContent = subtitle(p)
    const since = ago(p.since)
    put(status, pill(p.state), h("span", { class: "quiet" }, !since || since === "now" ? " just now" : ` for ${since}`),
      p.cost_usd ? h("span", { class: "quiet" }, ` · $${p.cost_usd.toFixed(2)}`) : null,
      p.failed ? h("span", { class: "error" }, " · its last request failed") : null)
    const qid = p.state === "waiting" ? p.question?.id || "?" : ""
    if (qid !== shown) { // the same question keeps its buttons as they are
      shown = qid
      ask.replaceChildren(qid ? questionCard(p, (m) => { note.textContent = m }) : "")
    }
    composer.hidden = !mayReply()
    const waiting = p.state === "waiting"
    const stop = p.state === "working" && !text.value.trim() && mayType()
    action.textContent = stop ? "■" : "↑"
    action.setAttribute("aria-label", stop ? "Stop" : "Send")
    action.classList.toggle("stop", stop)
    action.disabled = waiting || !state.online || stopping || (!stop && !text.value.trim())
    text.disabled = waiting || !state.online
    hint.textContent = waiting ? "Answer its question first — a reply now would be typed onto it."
      : stopping ? "Stopping…" : p.state === "working" ? "Working. A reply goes in when this work ends." : ""
  }
  return {
    name: "chat", el, update,
    frame: (f) => { if (f.pane === pane) { scr.frame(f); askWide(f) } },
    connected: () => send({ type: "frame.open", pane }),
    socketError: (e) => { if (e?.code !== "not_found") note.textContent = e?.message || "" },
    leave: () => send({ type: "frame.close", pane }),
  }
}

// widener asks for a pane to be made as wide as the window could show.
// Growing the type fills a window only as far as the pane's columns go, so
// a window with room to spare asks for the pane itself instead. It only
// ever asks to grow — a phone must not shrink the pane the laptop is
// working in — only when the gap is worth a resize, and once per size, so
// a frame arriving cannot start a conversation that never ends.
function widener(pane, el) {
  let asked = null
  return (f) => {
    if (!f || f.pane !== pane || !el?.clientWidth) return
    // At the size it is actually drawn at: somebody who chose 10px wants
    // the columns that fit at 10px, not at a size conch prefers.
    const chosen = termSize()
    const at = chosen === "fit" ? READABLE : Number(chosen)
    const want = fitPane(el.clientWidth - 16, el.clientHeight, undefined, undefined, undefined, undefined, at)
    if (!want || !worthResizing({ cols: f.cols, rows: f.rows }, want)) return
    const key = want.cols + "x" + want.rows
    if (asked === key) return // asked once for this size; the laptop may say no
    asked = key
    send({ type: "resize", pane, cols: want.cols, rows: want.rows })
  }
}

// rowNode is one row of styled runs as an element; its text is text.
function rowNode(runs) {
  const line = document.createElement("div")
  for (const run of runs) {
    const span = document.createElement("span")
    span.textContent = run.text
    let fg = run.fg, bg = run.bg
    if (run.inverse) [fg, bg] = [bg || "#1a1918", fg || "#e8e6df"]
    if (fg) span.style.color = fg
    if (bg) span.style.backgroundColor = bg
    if (run.bold) span.style.fontWeight = "700"
    if (run.dim) span.style.opacity = "0.6"
    if (run.italic) span.style.fontStyle = "italic"
    if (run.underline) span.style.textDecoration = "underline"
    line.append(span)
  }
  return line
}

// How many rows of history a screen keeps on the page; older ones go.
const HISTORY_ROWS = 4000

// paneScreen is a pane's screen with its history above it: the live rows
// redrawn with every frame, and older output loaded a page at a time when
// you scroll to the top (or tap the line there), stitched on above. It
// follows the newest line unless you have scrolled up to read.
function paneScreen(pane, cls, label) {
  const older = h("button", { class: "older", hidden: true }, "Earlier output")
  const hist = h("div", { class: "hist" })
  const live = h("div", { class: "live" })
  const el = h("pre", { class: `screen ${cls}`, "aria-label": label, tabindex: "0" }, older, hist, live)
  let sb = null, loading = null, stick = true, last = null
  // A program that took the mouse — an agent's full-screen view — keeps
  // its history itself: past the edge of the box, a swipe or the wheel is
  // turned into wheel steps for it, as the laptop does.
  let mouse = false, acc = 0, lastY = null

  const stop = () => { clearTimeout(loading); loading = null }
  const atTop = () => el.scrollTop <= 0
  const atBottom = () => el.scrollTop + el.clientHeight >= el.scrollHeight - 1
  const wheel = (dy, step) => {
    const r = wheelSteps(acc, dy, step)
    acc = r.acc
    if (r.steps) send({ type: "wheel", pane, direction: r.steps > 0 ? "up" : "down", count: Math.min(10, Math.abs(r.steps)) })
  }
  // Only at the box's own edge: inside it, it scrolls as any box does.
  const pastEdge = (dy) => (dy > 0 && atTop()) || (dy < 0 && atBottom())
  el.addEventListener("touchstart", (ev) => { lastY = ev.touches[0].clientY; acc = 0 }, { passive: true })
  el.addEventListener("touchmove", (ev) => {
    if (!mouse || !mayReply() || lastY == null) return
    const y = ev.touches[0].clientY, dy = y - lastY
    lastY = y
    if (pastEdge(dy)) { ev.preventDefault(); wheel(dy, 26) }
  }, { passive: false })
  el.addEventListener("wheel", (ev) => {
    if (!mouse || !mayReply()) return
    const dy = -ev.deltaY * (ev.deltaMode === 1 ? 16 : 1)
    if (pastEdge(dy)) { ev.preventDefault(); wheel(dy, 40) }
  }, { passive: false })

  const more = () => {
    if (mouse) return // the program keeps its own history
    const offset = olderOffset(sb)
    if (!offset || loading) return
    older.textContent = "Loading earlier output…"
    send({ type: "scroll", pane, offset })
    // If no page comes back, go back to following the pane.
    loading = setTimeout(() => { loading = null; send({ type: "scroll", pane, offset: 0 }); label2() }, 4000)
  }
  const label2 = () => {
    if (mouse) {
      older.hidden = !mayReply()
      older.textContent = "↑ Pull down here, or tap, to scroll back"
      return
    }
    older.hidden = !sb || sb.top <= 0
    older.textContent = "↑ Earlier output"
  }
  older.addEventListener("click", () => {
    if (mouse) send({ type: "wheel", pane, direction: "up", count: 5 })
    else more()
  })
  el.addEventListener("scroll", () => {
    stick = el.scrollTop + el.clientHeight >= el.scrollHeight - 24
    if (el.scrollTop < 80) more()
  })
  const toBottom = () => { if (stick) el.scrollTop = el.scrollHeight }

  return {
    el,
    get last() { return last },
    toBottom,
    follow: () => { stick = true; toBottom() },
    frame(f) {
      const r = scrollback(sb, f)
      sb = r.state
      const out = r.out
      if ((f.offset || 0) > 0) {
        // A page of older output: above what is shown, without moving it.
        if (out.prepend?.length) {
          const before = el.scrollHeight
          hist.prepend(...out.prepend.map((l) => rowNode(parseLine(l))))
          el.scrollTop += el.scrollHeight - before
        }
        stop()
        send({ type: "scroll", pane, offset: 0 }) // and back to following it
        label2()
        return
      }
      last = f
      mouse = !!f.mouse
      if (out.reset) hist.replaceChildren()
      if (out.append?.length) hist.append(...out.append.map((l) => rowNode(parseLine(l))))
      while (hist.childElementCount > HISTORY_ROWS) hist.firstChild.remove()
      live.replaceChildren(...frameRows(out.live || []).map(rowNode))
      if (!loading) label2()
      toBottom()
    },
  }
}

// The terminal's text size, kept per device: a number is that size and a
// wide screen scrolls sideways; "fit" shrinks the whole width onto the
// phone, which for a wide terminal is too small to read.
const TERM_SIZES = ["10", "12", "8", "fit"]
function termSize() {
  let size = ""
  try { size = localStorage.getItem("conch.termSize") || "" } catch { /* private mode */ }
  return TERM_SIZES.includes(size) ? size : TERM_SIZES[0]
}

// The textarea the phone's keyboard types into holds this and nothing
// else between keystrokes: a keystroke that shortens it is a backspace,
// which an empty field would not report.
const SENTINEL = "\u200b"

// terminalView is a pane's whole screen, typed into directly: tap it and
// the phone's keyboard goes to the pane, key by key, with the keys a
// phone lacks in a row above it.
function terminalView(pane) {
  const scr = paneScreen(pane, "terminal", "The terminal. Tap to type; scroll up for earlier output.")
  const screen = scr.el
  const note = h("p", { class: "error", role: "alert" })
  const typing = mayType()
  const tty = h("textarea", {
    class: "tty", autocapitalize: "off", autocomplete: "off", autocorrect: "off", spellcheck: "false",
    enterkeyhint: "enter", "aria-label": "Type into the terminal",
  })
  const mods = { ctrl: undefined, alt: undefined }
  let sent = 0

  const held = () => ({ ctrl: mods.ctrl?.state && mods.ctrl.state !== "off", alt: mods.alt?.state && mods.alt.state !== "off" })
  const spend = () => {
    mods.ctrl = usedModifier(mods.ctrl)
    mods.alt = usedModifier(mods.alt)
    showMods()
  }
  const sendKeys = (keys) => {
    note.textContent = ""
    for (const part of chunks(keys)) send({ type: "keys", id: `k${++sent}`, pane, keys: part })
  }
  const sendText = (t) => {
    note.textContent = ""
    send({ type: "text", id: `t${++sent}`, pane, text: t })
  }

  // What the keyboard put in the field goes to the pane, and the field
  // goes back to holding only the sentinel.
  let composing = false
  // Back to holding only the sentinel, with the cursor after it: Safari
  // on iOS puts it in front when the value is set, and every letter then
  // landed ahead of the sentinel and was taken for a backspace.
  const reset = () => {
    tty.value = SENTINEL
    tty.setSelectionRange(SENTINEL.length, SENTINEL.length)
  }
  const flush = () => {
    if (composing) return
    const { backspace, text: t } = ttyInput(tty.value, SENTINEL)
    reset()
    if (backspace) sendKeys([withMods("backspace", held())])
    if (!t) {
      if (backspace) spend()
      return
    }
    if (t === "\n") sendKeys([withMods("enter", held())])
    else if ([...t].length === 1 && (held().ctrl || held().alt)) sendKeys([withMods(t === " " ? "space" : t.toLowerCase(), held())])
    else sendText(t.replace(/\n/g, "\r"))
    spend()
  }
  tty.value = SENTINEL
  tty.addEventListener("input", flush)
  tty.addEventListener("compositionstart", () => { composing = true })
  tty.addEventListener("compositionend", () => { composing = false; flush() })
  tty.addEventListener("keydown", (ev) => {
    const k = keyFromEvent(ev, held())
    if (!k) return
    ev.preventDefault()
    sendKeys([k])
    spend()
  })
  screen.addEventListener("click", () => { if (typing) tty.focus({ preventScroll: true }) })

  const modButtons = {}
  const showMods = () => {
    for (const [m, b] of Object.entries(modButtons)) {
      b.classList.toggle("once", mods[m]?.state === "once")
      b.classList.toggle("locked", mods[m]?.state === "locked")
    }
  }
  const bar = h("div", { class: "keybar", role: "toolbar", "aria-label": "Keys" }, TERMINAL_KEYS.map((k) => {
    const b = h("button", { class: "key", "aria-label": k.mod || k.key }, k.label)
    // Tapping a key keeps the keyboard up: the field keeps the focus.
    b.addEventListener("mousedown", (ev) => ev.preventDefault())
    if (k.mod) {
      modButtons[k.mod] = b
      b.addEventListener("click", () => { mods[k.mod] = tapModifier(mods[k.mod], Date.now()); showMods() })
      return b
    }
    let armed = null
    b.addEventListener("click", () => {
      if (k.confirm && !armed) {
        // One tap arms it; a second within two seconds sends it.
        b.textContent = "again"
        b.classList.add("armed")
        armed = setTimeout(() => { armed = null; b.textContent = k.label; b.classList.remove("armed") }, 2000)
        return
      }
      if (armed) {
        clearTimeout(armed)
        armed = null
        b.textContent = k.label
        b.classList.remove("armed")
      }
      sendKeys([withMods(k.key, held())])
      spend()
    })
    return b
  }))
  const size = h("button", { class: "chip", "aria-label": "Text size" })
  const showSize = () => { size.textContent = termSize() === "fit" ? "Aa fit" : `Aa ${termSize()}` }
  size.addEventListener("click", () => {
    const next = TERM_SIZES[(TERM_SIZES.indexOf(termSize()) + 1) % TERM_SIZES.length]
    try { localStorage.setItem("conch.termSize", next) } catch { /* private mode */ }
    showSize()
    draw()
  })
  showSize()
  const draw = () => {
    const f = scr.last
    if (!f) return
    const s = termSize()
    screen.style.fontSize =
      (s === "fit" ? fitFontSize(f.cols, screen.clientWidth - 16, f.rows, screen.clientHeight) : Number(s)) + "px"
    scr.toBottom()
  }

  const askWider = widener(pane, screen)

  const dock = typing
    ? h("div", { class: "dock" }, bar)
    : h("p", { class: "hint dock" }, "Typing needs the full permission. On your laptop, run ",
      h("code", {}, `conch web permission ${state.hello?.device_id || "ID"} full`), ", then reload this page.")
  const p0 = paneOf(pane)
  const el = h("section", { class: "term" },
    h("div", { class: "term-bar" },
      p0?.kind !== "terminal" ? h("a", { href: `/agent/${pane}`, "data-nav": true, class: "link" }, "‹ Chat") : h("span"),
      typing ? h("span", { class: "hint" }, "Tap the screen to type") : h("span"),
      size),
    screen, typing ? tty : null, note, dock)

  // The terminal takes exactly what is visible under the header: when
  // the phone's keyboard opens, the visible part shrinks, the screen with
  // it, and the key row stays just above the keyboard with the prompt in
  // view — rather than the keyboard covering both.
  const vv = window.visualViewport
  const layout = () => {
    const top = Math.max($("banner").hidden ? document.querySelector("header").getBoundingClientRect().bottom
      : $("banner").getBoundingClientRect().bottom, vv ? vv.offsetTop : 0)
    const bottom = vv ? vv.offsetTop + vv.height : window.innerHeight
    el.style.top = top + "px"
    el.style.height = Math.max(120, bottom - top) + "px"
    scr.toBottom()
  }
  vv?.addEventListener("resize", layout)
  vv?.addEventListener("scroll", layout)
  window.addEventListener("resize", layout)
  tty.addEventListener("focus", () => { reset(); scr.follow(); setTimeout(layout, 50); setTimeout(layout, 350) })
  requestAnimationFrame(layout)

  return {
    name: "terminal", el,
    update: () => {
      const p = paneOf(pane)
      if (p) { $("title").textContent = agentLabel(p); $("sub").textContent = p.kind === "terminal" ? subtitle(p) : subtitle(p) + " · terminal" }
      for (const b of bar.querySelectorAll("button")) b.disabled = !state.online
    },
    frame: (f) => { if (f.pane === pane) { scr.frame(f); if (!(f.offset > 0)) draw(); askWider(f) } },
    connected: () => send({ type: "frame.open", pane }),
    socketError: (e) => { note.textContent = e?.message || "" },
    leave: () => {
      send({ type: "frame.close", pane })
      vv?.removeEventListener("resize", layout)
      vv?.removeEventListener("scroll", layout)
      window.removeEventListener("resize", layout)
    },
  }
}

// paneMenu is the ⋯ in a pane's header: its terminal or conversation,
// renaming and closing it.
function paneMenu(pane) {
  const p = paneOf(pane)
  const err = h("p", { class: "error", role: "alert" })
  const inTerm = route(location.pathname).view === "terminal"
  const items = []
  if (p?.kind !== "terminal") {
    items.push(h("a", { href: inTerm ? `/agent/${pane}` : `/agent/${pane}/terminal`, "data-nav": true, class: "item", onclick: () => sheet.close() },
      inTerm ? "Conversation" : "Terminal"))
  }
  if (mayType()) {
    items.push(h("button", {
      class: "item", onclick: () => {
        const name = h("input", { value: p?.name || "", maxlength: "64", "aria-label": "Name" })
        sheet.open("Rename", h("form", {
          onsubmit: async (ev) => {
            ev.preventDefault()
            try {
              await api("POST", "/api/rename", { pane, name: name.value })
              sheet.close()
            } catch (e) { err.textContent = e.message }
          },
        }, h("label", {}, "Name", name), h("button", { class: "primary wide" }, "Rename"), err))
        name.focus()
      },
    }, "Rename"))
    const close = h("button", { class: "item danger" }, "Close")
    let armed = false
    close.addEventListener("click", async () => {
      if (!armed) { // it ends the pane's program: ask once more
        armed = true
        close.textContent = "Close — tap again to end it"
        return
      }
      try {
        await api("POST", "/api/close", { pane })
        sheet.close()
        navigate("/")
      } catch (e) { err.textContent = e.message }
    })
    items.push(close)
  }
  sheet.open(p ? agentLabel(p) : pane, h("div", { class: "menu" }, items), err)
}

// openNew is ＋: a task (branch, worktree and agent), an agent here, or a
// terminal.
function openNew(kind = "task") {
  const err = h("p", { class: "error", role: "alert" })
  const tabs = [["task", "Task"], ["agent", "Agent"], ["terminal", "Terminal"]]
  const project = h("select", { "aria-label": "Project" }, h("option", { value: "" }, "Loading…"))
  // Which machine to start it on. Only machines that answer can take one,
  // and with a single machine there is nothing to choose: the row is left
  // out rather than offering one option.
  const reachable = state.machines.filter((m) => m.state === "online")
  const machine = h("select", { "aria-label": "Machine" },
    reachable.map((m) => h("option", { value: m.id }, m.label)))
  const agent = h("select", { "aria-label": "Agent" }, h("option", { value: "" }, "conch's default"), AGENTS.map((a) => h("option", { value: a }, a)))
  const name = h("input", { maxlength: "64", placeholder: "optional", "aria-label": "Name" })
  const prompt = h("textarea", { rows: "4", placeholder: kind === "task" ? "What should it do?" : "Its first message (optional)", "aria-label": "Prompt" })
  const go = h("button", { class: "primary wide" }, kind === "task" ? "Start task" : kind === "agent" ? "Start agent" : "Open terminal")
  // A project belongs to a machine, so changing the machine loads its own.
  const loadProjects = () => {
    project.replaceChildren(h("option", { value: "" }, "Loading…"))
    const where = machine.value && machine.value !== "local" ? `?machine=${encodeURIComponent(machine.value)}` : ""
    api("GET", "/api/projects" + where).then(({ projects }) => {
      put(project,
        kind !== "task" ? h("option", { value: "" }, "No project (home folder)") : null,
        projects.map((p) => h("option", { value: p.id }, p.name || p.path)))
      if (kind === "task" && !projects.length) project.replaceChildren(h("option", { value: "" }, "No projects: add one in conch first"))
    }).catch((e) => { err.textContent = e.message })
  }
  machine.addEventListener("change", loadProjects)
  loadProjects()
  const form = h("form", {
    onsubmit: async (ev) => {
      ev.preventDefault()
      go.disabled = true
      err.textContent = ""
      try {
        let res
        if (kind === "task") {
          const body = { project: project.value, prompt: prompt.value }
          if (machine.value && machine.value !== "local") body.machine = machine.value
          if (agent.value) body.agent = agent.value
          if (name.value.trim()) body.name = name.value.trim()
          res = await api("POST", "/api/task", body)
        } else {
          const body = { kind }
          if (machine.value && machine.value !== "local") body.machine = machine.value
          if (project.value) body.project = project.value
          if (name.value.trim()) body.name = name.value.trim()
          if (kind === "agent") {
            body.agent = agent.value || AGENTS[0]
            if (prompt.value.trim()) body.prompt = prompt.value
          }
          res = await api("POST", "/api/panes", body)
        }
        sheet.close()
        navigate(`/agent/${res.pane}`)
      } catch (e) {
        err.textContent = e.message
        go.disabled = false
      }
    },
  },
  reachable.length > 1 ? h("label", {}, "Machine", machine) : null,
  h("label", {}, "Project", project),
  kind !== "terminal" ? h("label", {}, "Agent", agent) : null,
  kind !== "terminal" ? h("label", {}, kind === "task" ? "Task" : "First message", prompt) : null,
  h("label", {}, "Name", name),
  kind === "task" ? h("p", { class: "hint" }, "conch makes a branch and a worktree for it, and starts the agent there.") : null,
  go, err)
  sheet.open("New", h("div", { class: "segmented" }, tabs.map(([k, label]) =>
    h("button", { class: k === kind ? "on" : "", onclick: () => openNew(k) }, label))), form)
}

// A tapped notification, when the app is already open, arrives from the
// service worker as a message rather than a new window.
navigator.serviceWorker?.addEventListener("message", (ev) => {
  if (ev.data?.type === "open") navigate(appPath(ev.data.url))
})

// settingsView turns notifications on and off for this device.
function settingsView() {
  const status = h("p", { role: "status" })
  const error = h("p", { class: "error", role: "alert" })
  const done = h("input", { type: "checkbox" })
  const on = h("button", { class: "primary wide" }, "Turn on notifications")
  const off = h("button", { class: "wide" }, "Turn off")
  const supported = "serviceWorker" in navigator && "PushManager" in window && "Notification" in window

  const registration = () => navigator.serviceWorker.ready
  const current = async () => (await registration()).pushManager.getSubscription()
  const events = () => (done.checked ? ["waiting", "done"] : ["waiting"])
  const save = async (sub) => {
    const { endpoint, keys } = sub.toJSON()
    await api("POST", "/api/push/subscribe", { endpoint, keys, on: events() })
    try { localStorage.setItem("conch.pushDone", done.checked ? "1" : "") } catch { /* private mode */ }
  }
  const refresh = async () => {
    error.textContent = ""
    if (!supported) {
      status.textContent = /iPhone|iPad/.test(navigator.userAgent)
        ? "To get notifications on an iPhone or iPad, add conch to your Home Screen (Share → Add to Home Screen, iOS 16.4 or later) and open it from there."
        : "This browser can't receive notifications from conch."
      on.hidden = off.hidden = true
      return
    }
    if (Notification.permission === "denied") {
      status.textContent = "Notifications are blocked for this site. Allow them in the browser's settings, then come back."
      on.hidden = off.hidden = true
      return
    }
    const sub = await current()
    status.textContent = sub
      ? "On: you are told when an agent is waiting for you" + (done.checked ? ", and when one finishes." : ".")
      : "Off."
    on.hidden = !!sub
    off.hidden = !sub
  }
  on.addEventListener("click", async () => {
    on.disabled = true
    try {
      if (await Notification.requestPermission() !== "granted") return
      const { vapid_public_key } = await api("GET", "/api/push/key")
      const reg = await registration()
      let sub = await reg.pushManager.getSubscription()
      if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(vapid_public_key) })
      await save(sub)
    } catch (err) {
      error.textContent = err.message
    } finally {
      on.disabled = false
      refresh().catch(() => {})
    }
  })
  off.addEventListener("click", async () => {
    off.disabled = true
    try {
      const sub = await current()
      if (sub) {
        await api("DELETE", "/api/push/subscribe", { endpoint: sub.endpoint }).catch(() => {})
        await sub.unsubscribe()
      }
    } catch (err) {
      error.textContent = err.message
    } finally {
      off.disabled = false
      refresh().catch(() => {})
    }
  })
  done.addEventListener("change", async () => {
    try {
      const sub = supported && await current()
      if (sub) await save(sub)
    } catch (err) {
      error.textContent = err.message
    }
    refresh().catch(() => {})
  })
  try { done.checked = localStorage.getItem("conch.pushDone") === "1" } catch { /* private mode */ }

  const me = state.hello || {}
  const kv = (k, v) => h("div", { class: "kv" }, h("span", {}, k), h("span", { class: "quiet" }, v))
  const el = h("section", { class: "settings" },
    h("h2", {}, "Notifications"),
    h("div", { class: "card" }, status, h("label", { class: "check" }, done, " Also when an agent finishes"), on, off, error),
    h("p", { class: "hint" }, "A notification says which agent and which project, never what it asks or shows: it passes through Apple's or Google's servers."),
    h("h2", {}, "This device"),
    h("div", { class: "card" }, kv("Device", me.device_id || ""), kv("Permission", me.permission || ""), kv("conch", me.conch_version || "")),
    h("p", { class: "hint" }, "To unpair it, run ", h("code", {}, `conch web revoke ${me.device_id || "ID"}`), " on your laptop."))
  refresh().catch((err) => { error.textContent = err.message })
  return { name: "settings", el }
}

// ---- start ----

$("menu").addEventListener("click", () => drawer.open())
$("drawer-scrim").addEventListener("click", () => drawer.close())
$("more").addEventListener("click", () => state.more?.())
$("new").addEventListener("click", () => openNew())

async function start() {
  try {
    state.hello = await api("GET", "/api/hello")
    // The machines come with hello, so a machine still being reached is
    // drawn before anything else is asked for.
    state.machines = state.hello.machines || []
  } catch (err) {
    if (err.code === "offline" && state.panes.length) {
      // Paired before, and the laptop is away: show what was last seen.
      state.hello = { permission: "view", offline: true }
      showBanner()
      render()
      setTimeout(start, backoff(state.tries++))
      return
    }
    state.hello = null
    if (err.code === "unauthorized") {
      state.panes = []
      forget() // what another pairing left behind is not this one's to see
    }
    render()
    if (err.code !== "unauthorized") $("view").prepend(h("p", { class: "error" }, err.message))
    return
  }
  if (state.hello.api_version !== API_VERSION) {
    // This page is older or newer than the gateway: take the gateway's.
    const regs = await navigator.serviceWorker?.getRegistrations?.().catch(() => []) || []
    await Promise.all(regs.map((r) => r.unregister()))
    if (!sessionStorage.getItem("conch.reloaded")) {
      sessionStorage.setItem("conch.reloaded", "1")
      location.reload()
      return
    }
  }
  state.tries = 0
  state.justPaired = false
  $("new").hidden = !mayType()
  render()
  connect()
}

recall()
navigator.serviceWorker?.register("/sw.js").catch(() => {})
start()
