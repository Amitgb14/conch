// conch on a phone: the agents, one agent with its screen, a reply, an
// answer to what it asks, and a new task. It talks to the gateway that
// served it (docs/plans/phone-api.md) and to nothing else.
//
// Everything the gateway sends is shown with textContent: an agent's
// title, its question and its screen are text, never markup.
import {
  frameRows, tailRows, sortAgents, upsertAgent, removeAgent, groupAgents, agentLabel,
  ago, route, can, backoff, codeFromHash, fontSizeFor, KEYBAR, textKeys, chunks, keyBytes, appPath,
} from "/lib.mjs"

const API_VERSION = 1
// The agents conch can start. There is no route that lists the ones
// installed, so a task names one of these or leaves it to conch.
const AGENTS = ["claude", "codex", "gemini", "opencode", "devin"]

const state = {
  hello: null, // who this device is, once paired
  agents: [],
  listed: false, // the list has come from the gateway at least once
  online: false,
  reached: 0, // when the gateway last answered
  ws: null,
  tries: 0,
  heard: 0, // when the socket last said anything
  view: null, // the view on screen: { update(), leave() }
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
  el.append(...children.filter((c) => c != null && c !== false))
  return el
}

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
    ? `Your laptop can't be reached — asleep or off the tailnet. Last reached ${at}; what is shown is from then.`
    : "Your laptop can't be reached — asleep or off the tailnet."
}

// The list is kept between visits so there is something to show while the
// laptop sleeps. Questions are left out: they can quote a command.
function remember() {
  try {
    const agents = state.agents.map(({ question, ...rest }) => rest)
    localStorage.setItem("conch.agents", JSON.stringify(agents))
  } catch { /* private mode */ }
}

function recall() {
  try {
    const agents = JSON.parse(localStorage.getItem("conch.agents") || "[]")
    if (Array.isArray(agents)) state.agents = sortAgents(agents.filter((a) => a && typeof a.pane === "string"))
    state.reached = Number(localStorage.getItem("conch.reached")) || 0
  } catch { /* nothing kept */ }
}

function forget() {
  try {
    localStorage.removeItem("conch.agents")
    localStorage.removeItem("conch.reached")
  } catch { /* private mode */ }
}

function setAgents(agents) {
  state.agents = agents
  state.listed = true
  remember()
  state.view?.update?.()
}

// ---- the socket ----

function connect() {
  if (state.ws || !state.hello) return
  const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/socket`)
  state.ws = ws
  ws.onopen = () => {
    state.tries = 0
    state.heard = Date.now()
    reachedNow()
    setOnline(true)
    send({ type: "agents.watch" })
    state.view?.connected?.()
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
    case "agents":
      setAgents(sortAgents(m.agents || []))
      break
    case "agent":
      if (m.agent) setAgents(upsertAgent(state.agents, m.agent))
      break
    case "agent.gone":
      setAgents(removeAgent(state.agents, m.pane))
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

// ---- views ----

// show puts a view on screen under a header: its title, what it is about,
// and a way back when it isn't the list.
function show(view, { title = "conch", sub = "", back = "" } = {}) {
  state.view?.leave?.()
  state.view = view
  // After the old view has closed its frames: going from an agent to its
  // terminal closes and opens the same pane, in that order.
  view.connected?.()
  setHeader(title, sub, back)
  $("new").hidden = !state.hello || !can(state.hello.permission, "full")
  $("settings").hidden = !state.hello
  document.body.dataset.view = view.name || ""
  $("view").replaceChildren(view.el)
  view.update?.()
  window.scrollTo(0, 0)
}

function setHeader(title, sub, back) {
  $("home").textContent = title
  $("where").textContent = sub
  $("back").hidden = !back
  if (back) $("back").setAttribute("href", back)
}

// The dot in the header: live while the socket is open.
function showLive() {
  const live = $("live")
  live.hidden = !state.hello
  live.classList.toggle("on", state.online)
  live.title = state.online ? "Connected to your laptop" : "Not connected"
}

function navigate(path) {
  if (path !== location.pathname) history.pushState(null, "", path)
  render()
}

function render() {
  showLive()
  if (!state.hello) return show(pairView(), { title: "Pair this device" })
  const r = route(location.pathname)
  if (r.view === "agent") return show(agentView(r.pane), { back: "/" })
  if (r.view === "terminal") return show(terminalView(r.pane), { back: `/agent/${r.pane}` })
  if (r.view === "settings") return show(settingsView(), { title: "Settings", back: "/" })
  if (r.view === "new" && can(state.hello.permission, "full")) return show(newTaskView(), { title: "New task", back: "/" })
  show(listView(), { title: "Agents" })
}

document.addEventListener("click", (ev) => {
  const a = ev.target.closest?.("a[data-nav]")
  if (!a || ev.metaKey || ev.ctrlKey) return
  ev.preventDefault()
  navigate(a.getAttribute("href"))
})
window.addEventListener("popstate", render)

function unpaired() {
  if (!state.hello) return
  state.hello = null
  state.agents = []
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

// pill is an agent's state as a coloured label.
const pill = (s) => h("span", { class: `pill ${s}` }, s)

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
      h("p", { class: "lead" }, "Your coding agents, from your phone."),
      h("p", { class: "quiet" }, "On your laptop, press ", h("kbd", {}, "P"), " in conch or run ", h("code", {}, "conch web pair"),
        ", then scan the QR code — or type the code here."),
      form),
  }
}

function listView() {
  const el = h("section", { class: "list" })
  const update = () => {
    if (state.agents.length === 0) {
      el.replaceChildren(h("div", { class: "empty" },
        h("p", { class: "lead" }, state.listed ? "No agents running" : state.online ? "Loading…" : "Nothing to show yet"),
        state.listed && can(state.hello.permission, "full")
          ? h("p", { class: "quiet" }, "Start one with ", h("a", { href: "/new", "data-nav": true }, "a new task"), ".")
          : null))
      return
    }
    const counts = {}
    for (const a of state.agents) counts[a.state] = (counts[a.state] || 0) + 1
    const summary = h("div", { class: "summary" }, ...["waiting", "done", "working", "idle"]
      .filter((s) => counts[s]).map((s) => h("span", { class: `pill ${s}` }, `${counts[s]} ${s}`)))
    el.replaceChildren(summary, ...groupAgents(state.agents).flatMap((g) => [
      h("h2", {}, g.name, h("span", { class: "count" }, String(g.agents.length))),
      h("ul", { class: "cards" }, ...g.agents.map((a) =>
        h("li", {}, h("a", { href: `/agent/${a.pane}`, "data-nav": true, class: `card agent ${a.state}` },
          h("div", { class: "top" }, pill(a.state), h("span", { class: "label" }, agentLabel(a)), h("span", { class: "age" }, ago(a.since))),
          h("div", { class: "meta" }, [a.agent, a.branch, a.cost_usd ? `$${a.cost_usd.toFixed(2)}` : ""].filter(Boolean).join(" · "),
            a.failed ? h("span", { class: "error" }, " · last request failed") : null),
          a.question ? h("div", { class: "asks" }, a.question.text || "Waiting for you") : null)))),
    ]))
  }
  const tick = setInterval(update, 30000) // the ages move on
  return { name: "list", el, update, leave: () => clearInterval(tick) }
}

function agentView(pane) {
  const head = h("div", { class: "hero" })
  const ask = h("div", {})
  const tail = h("pre", { class: "screen tail", "aria-label": "The agent's latest output" })
  const note = h("p", { class: "error", role: "alert" })
  const text = h("textarea", { placeholder: "Reply…", rows: "1", "aria-label": "Reply" })
  const sendButton = h("button", { class: "send", "aria-label": "Send" }, "↑")
  const replyNote = h("p", { class: "hint" })
  const mayReply = can(state.hello.permission, "reply")
  // The box grows with what is typed, up to a few lines.
  text.addEventListener("input", () => {
    text.style.height = "auto"
    text.style.height = Math.min(text.scrollHeight, 140) + "px"
  })
  const reply = h("form", {
    class: "composer",
    hidden: !mayReply,
    onsubmit: async (ev) => {
      ev.preventDefault()
      if (!text.value.trim()) return
      sendButton.disabled = true
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
  }, replyNote, h("div", { class: "row" }, text, sendButton))
  const output = h("div", { class: "section-head" }, h("h2", {}, "Latest output"),
    h("a", { href: `/agent/${pane}/terminal`, "data-nav": true, class: "link" }, "Terminal ›"))
  const el = h("section", { class: "agent-view" }, head, ask, note, output, tail, reply)

  let shownQuestion = null
  const answer = async (q, choice, buttons) => {
    buttons.forEach((b) => (b.disabled = true))
    note.textContent = ""
    try {
      await api("POST", "/api/answer", { pane, question_id: q.id, choice: choice.choice })
    } catch (err) {
      note.textContent = err.code === "question_changed" ? "It is asking something else now, so nothing was sent."
        : err.code === "not_waiting" ? "It is no longer waiting, so nothing was sent." : err.message
      buttons.forEach((b) => (b.disabled = false))
    }
  }
  const showQuestion = (q) => {
    const key = q ? q.id : ""
    if (key === shownQuestion) return // the same question: leave its buttons as they are
    shownQuestion = key
    if (!q) return ask.replaceChildren()
    const box = h("div", { class: "question" }, h("div", { class: "kicker" }, "Waiting for you"),
      h("p", {}, q.text || "The agent is waiting for an answer."))
    if (q.choices?.length && mayReply) {
      const buttons = q.choices.map((c) => h("button", { class: "choice" },
        h("span", { class: "num" }, c.choice), h("span", { class: "text" }, c.label), c.default ? h("span", { class: "current" }, "selected") : null))
      buttons.forEach((b, i) => b.addEventListener("click", () => answer(q, q.choices[i], buttons)))
      box.append(h("div", { class: "choices" }, ...buttons))
    } else if (can(state.hello.permission, "full")) {
      box.append(h("p", { class: "quiet" }, "conch couldn't read the choices off its screen. "),
        h("a", { href: `/agent/${pane}/terminal`, "data-nav": true, class: "button wide" }, "Answer it in the terminal"))
    } else if (mayReply) {
      box.append(h("p", { class: "quiet" }, "conch couldn't read the choices off its screen. Answer this one at your laptop."))
    } else {
      box.append(h("p", { class: "quiet" }, "This device can look but not answer."))
    }
    ask.replaceChildren(box)
  }

  const update = () => {
    const a = state.agents.find((x) => x.pane === pane)
    $("home").textContent = a ? agentLabel(a) : pane
    $("where").textContent = a ? [a.agent, a.project?.name].filter(Boolean).join(" · ") : ""
    if (!a) {
      head.replaceChildren(h("p", { class: "quiet" }, state.listed ? "This agent has gone: its pane closed, or the agent left it." : "Loading…"))
      showQuestion(null)
      reply.hidden = true
      return
    }
    const since = ago(a.since)
    head.replaceChildren(pill(a.state), h("span", { class: "since" }, !since || since === "now" ? "just now" : `for ${since}`),
      h("span", { class: "meta" }, [a.branch, a.cost_usd ? `$${a.cost_usd.toFixed(2)}` : ""].filter(Boolean).join(" · ")),
      ...(a.failed ? [h("p", { class: "error" }, "Its last request failed.")] : []))
    showQuestion(a.state === "waiting" ? a.question : null)
    reply.hidden = !mayReply
    const waiting = a.state === "waiting"
    text.disabled = waiting || !state.online
    sendButton.disabled = waiting || !state.online
    replyNote.textContent = waiting ? "Answer its question first: a reply now would be typed onto it."
      : a.state === "working" ? "It's working — it gets your reply when this work ends." : ""
    replyNote.hidden = !replyNote.textContent
  }

  const frame = (f) => {
    if (f.pane === pane) drawRows(tail, tailRows(frameRows(f.lines), 18))
  }

  const connected = () => send({ type: "frame.open", pane })
  return {
    name: "agent", el, update, frame, connected,
    socketError: (e) => { if (e?.code !== "not_found") note.textContent = e?.message || "" },
    leave: () => send({ type: "frame.close", pane }),
  }
}

// drawRows puts rows of styled runs on screen as text.
function drawRows(screen, rows) {
  screen.replaceChildren(...rows.map((runs) => {
    const row = document.createElement("div")
    for (const run of runs) {
      const span = document.createElement("span")
      span.textContent = run.text
      let fg = run.fg, bg = run.bg
      if (run.inverse) [fg, bg] = [bg || "#0b0d10", fg || "#d8dade"]
      if (fg) span.style.color = fg
      if (bg) span.style.backgroundColor = bg
      if (run.bold) span.style.fontWeight = "700"
      if (run.dim) span.style.opacity = "0.6"
      if (run.italic) span.style.fontStyle = "italic"
      if (run.underline) span.style.textDecoration = "underline"
      row.append(span)
    }
    return row
  }))
}

// The terminal's text size: a number is that size, and a wide screen
// scrolls sideways; "fit" shrinks the whole width onto the phone, which
// for a wide terminal is too small to read, so it isn't the default.
// Kept per device.
const TERM_SIZES = ["10", "12", "8", "fit"]
function termSize() {
  let size = ""
  try { size = localStorage.getItem("conch.termSize") || "" } catch { /* private mode */ }
  return TERM_SIZES.includes(size) ? size : TERM_SIZES[0]
}

// terminalView is a pane's whole screen, every frame, and for a device
// with full the keys a phone's keyboard lacks and a line to type from.
function terminalView(pane) {
  const screen = h("pre", { class: "screen terminal", "aria-label": "The terminal" })
  const note = h("p", { class: "error", role: "alert" })
  const mayType = can(state.hello.permission, "full")
  let sent = 0, last = null
  const sendKeys = (keys) => {
    note.textContent = ""
    for (const part of chunks(keys)) send({ type: "keys", id: `k${++sent}`, pane, keys: part })
  }
  const draw = () => {
    if (!last) return
    const size = termSize()
    screen.style.fontSize = (size === "fit" ? fontSizeFor(last.cols, screen.clientWidth - 16) : Number(size)) + "px"
    drawRows(screen, frameRows(last.lines)) // not the empty rows under the last line
  }
  const sizeButton = h("button", { class: "chip", "aria-label": "Text size" })
  const showSize = () => { sizeButton.textContent = termSize() === "fit" ? "Aa fit width" : `Aa ${termSize()}px` }
  sizeButton.addEventListener("click", () => {
    const next = TERM_SIZES[(TERM_SIZES.indexOf(termSize()) + 1) % TERM_SIZES.length]
    try { localStorage.setItem("conch.termSize", next) } catch { /* private mode */ }
    showSize()
    draw()
  })
  showSize()

  const bar = h("div", { class: "keybar", role: "toolbar", "aria-label": "Keys" }, ...KEYBAR.map((k) => {
    const b = h("button", { class: "key", "aria-label": k.key }, k.label)
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
      sendKeys([k.key])
    })
    return b
  }))
  const line = h("input", { placeholder: "Type into the terminal…", autocapitalize: "off", autocorrect: "off", spellcheck: "false", enterkeyhint: "send", "aria-label": "Text to type" })
  const typing = h("form", {
    class: "row",
    onsubmit: (ev) => {
      ev.preventDefault()
      const keys = textKeys(line.value)
      if (keys.length === 0) return
      sendKeys(keys) // typed, not submitted: ⏎ is its own key
      line.value = ""
    },
  }, line, h("button", { class: "send", "aria-label": "Type" }, "↑"))
  const keys = mayType
    ? h("div", { class: "composer keys" }, bar, typing)
    : h("p", { class: "hint" }, "This device can look but not type: that needs the full permission.")
  const el = h("section", { class: "terminal-view" },
    h("div", { class: "toolbar" }, sizeButton), screen, note, keys)

  const update = () => {
    const a = state.agents.find((x) => x.pane === pane)
    $("home").textContent = a ? agentLabel(a) : pane
    $("where").textContent = "terminal"
    const off = !state.online
    for (const b of el.querySelectorAll(".composer button")) b.disabled = off
    line.disabled = off
  }
  return {
    name: "terminal", el, update,
    frame: (f) => { if (f.pane === pane) { last = f; draw() } },
    connected: () => send({ type: "frame.open", pane }),
    socketError: (e) => { note.textContent = e?.message || "" },
    leave: () => send({ type: "frame.close", pane }),
  }
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
  const row = (k, v) => h("div", { class: "kv" }, h("span", {}, k), h("span", { class: "quiet" }, v))
  const el = h("section", {},
    h("h2", {}, "Notifications"),
    h("div", { class: "card" }, status,
      h("label", { class: "check" }, done, " Also when an agent finishes"),
      on, off, error),
    h("p", { class: "hint" }, "A notification says which agent and which project, never what it asks or shows: it passes through Apple's or Google's servers."),
    h("h2", {}, "This device"),
    h("div", { class: "card" }, row("Device", me.device_id || ""), row("Permission", me.permission || ""), row("conch", me.conch_version || "")),
    h("p", { class: "hint" }, "To unpair it, run ", h("code", {}, `conch web revoke ${me.device_id || "ID"}`), " on your laptop."))
  refresh().catch((err) => { error.textContent = err.message })
  return { name: "settings", el }
}

function newTaskView() {
  const status = h("p", { class: "error", role: "alert" })
  const project = h("select", { required: true }, h("option", { value: "" }, "Loading…"))
  const agent = h("select", {}, h("option", { value: "" }, "conch's default"), ...AGENTS.map((a) => h("option", { value: a }, a)))
  const name = h("input", { maxlength: "64", placeholder: "optional, e.g. reviewer" })
  const prompt = h("textarea", { required: true, rows: "6", placeholder: "What should it do?" })
  const button = h("button", { class: "primary wide" }, "Start")
  api("GET", "/api/projects").then(({ projects }) => {
    project.replaceChildren(...(projects.length
      ? projects.map((p) => h("option", { value: p.id }, p.name || p.path))
      : [h("option", { value: "" }, "No projects: add one in conch first")]))
  }).catch((err) => { status.textContent = err.message })
  const form = h("form", {
    onsubmit: async (ev) => {
      ev.preventDefault()
      button.disabled = true
      status.textContent = ""
      try {
        const body = { project: project.value, prompt: prompt.value }
        if (agent.value) body.agent = agent.value
        if (name.value.trim()) body.name = name.value.trim()
        const res = await api("POST", "/api/task", body)
        navigate(`/agent/${res.pane}`)
      } catch (err) {
        status.textContent = err.message
        button.disabled = false
      }
    },
  },
  h("div", { class: "card" },
    h("label", {}, "Project", project),
    h("label", {}, "Agent", agent),
    h("label", {}, "Name", name)),
  h("div", { class: "card" }, h("label", {}, "Task", prompt)),
  h("p", { class: "hint" }, "conch makes a branch and a worktree for it, and starts the agent there."),
  button, status)
  return { name: "new", el: h("section", {}, form) }
}

// ---- start ----

async function start() {
  try {
    state.hello = await api("GET", "/api/hello")
  } catch (err) {
    if (err.code === "offline" && state.agents.length) {
      // Paired before, and the laptop is away: show what was last seen.
      state.hello = { permission: "view", offline: true }
      showBanner()
      render()
      setTimeout(start, backoff(state.tries++))
      return
    }
    state.hello = null
    if (err.code === "unauthorized") {
      state.agents = []
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
  render()
  connect()
}

recall()
navigator.serviceWorker?.register("/sw.js").catch(() => {})
start()
