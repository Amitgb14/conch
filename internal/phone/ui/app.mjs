// conch on a phone: the agents, one agent with its screen, a reply, an
// answer to what it asks, and a new task. It talks to the gateway that
// served it (docs/plans/phone-api.md) and to nothing else.
//
// Everything the gateway sends is shown with textContent: an agent's
// title, its question and its screen are text, never markup.
import {
  frameRows, sortAgents, upsertAgent, removeAgent, groupAgents, agentLabel,
  ago, route, can, backoff, codeFromHash, fontSizeFor,
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

function show(view, where = "") {
  state.view?.leave?.()
  state.view = view
  $("where").textContent = where
  $("new").hidden = !state.hello || !can(state.hello.permission, "full")
  $("view").replaceChildren(view.el)
  view.update?.()
  window.scrollTo(0, 0)
}

function navigate(path) {
  if (path !== location.pathname) history.pushState(null, "", path)
  render()
}

function render() {
  if (!state.hello) return show(pairView())
  const r = route(location.pathname)
  if (r.view === "agent") return show(agentView(r.pane))
  if (r.view === "new" && can(state.hello.permission, "full")) return show(newTaskView(), "New task")
  show(listView())
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

function pairView() {
  const status = h("p", { class: "error", role: "alert" })
  const code = h("input", { inputmode: "numeric", autocomplete: "one-time-code", placeholder: "123-456", required: true, value: codeFromHash(location.hash) })
  const name = h("input", { maxlength: "64", value: deviceName() })
  const button = h("button", { class: "primary" }, "Pair")
  const form = h("form", {
    onsubmit: async (ev) => {
      ev.preventDefault()
      button.disabled = true
      status.textContent = ""
      try {
        await api("POST", "/pair", { code: code.value, device_name: name.value })
        history.replaceState(null, "", location.pathname) // the code is spent; drop it from the address
        await start()
      } catch (err) {
        status.textContent = err.message
      } finally {
        button.disabled = false
      }
    },
  },
  h("p", {}, "Run ", h("code", {}, "conch web pair"), " on your laptop and enter the code it shows."),
  h("label", {}, "Pairing code", code),
  h("label", {}, "This device's name", name),
  button, status)
  return { el: h("section", {}, h("h2", {}, "Pair this device"), form) }
}

function listView() {
  const el = h("section")
  const update = () => {
    if (state.agents.length === 0) {
      el.replaceChildren(h("p", { class: "quiet" }, state.listed ? "No agents are running." : state.online ? "Loading…" : "Nothing to show yet."))
      return
    }
    el.replaceChildren(...groupAgents(state.agents).flatMap((g) => [
      h("h2", {}, g.name),
      h("ul", { class: "agents" }, ...g.agents.map((a) =>
        h("li", {}, h("a", { href: `/agent/${a.pane}`, "data-nav": true },
          h("span", { class: `dot ${a.state}`, title: a.state }),
          h("span", { class: "label" }, agentLabel(a)),
          h("span", { class: "age" }, ago(a.since)),
          h("span", { class: "meta" }, [a.state, a.agent, a.branch, a.cost_usd ? `$${a.cost_usd.toFixed(2)}` : "", a.failed ? "last request failed" : ""].filter(Boolean).join(" · ")),
          a.question ? h("span", { class: "asks" }, a.question.text || "Waiting for you") : null)))),
    ]))
  }
  const tick = setInterval(update, 30000) // the ages move on
  return { el, update, leave: () => clearInterval(tick) }
}

function agentView(pane) {
  const head = h("p", {})
  const ask = h("div", {})
  const screen = h("pre", { class: "screen", "aria-label": "The agent's screen" })
  const note = h("p", { class: "error", role: "alert" })
  const text = h("textarea", { placeholder: "Reply to the agent…", rows: "2", "aria-label": "Reply" })
  const sendButton = h("button", { class: "primary" }, "Send")
  const replyNote = h("p", { class: "quiet" })
  const mayReply = can(state.hello.permission, "reply")
  const reply = h("form", {
    hidden: !mayReply,
    onsubmit: async (ev) => {
      ev.preventDefault()
      if (!text.value.trim()) return
      sendButton.disabled = true
      note.textContent = ""
      try {
        await api("POST", "/api/reply", { pane, text: text.value })
        text.value = ""
      } catch (err) {
        note.textContent = err.code === "agent_blocked" ? "It is waiting for an answer, so nothing was sent. Answer its question first." : err.message
      } finally {
        update()
      }
    },
  }, h("div", { class: "row" }, text, sendButton), replyNote)
  const el = h("section", {}, head, ask, note, screen, reply)

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
    const box = h("div", { class: "question" }, h("p", {}, q.text || "The agent is waiting for an answer."))
    if (q.choices?.length && mayReply) {
      const buttons = q.choices.map((c) => h("button", { type: "button" }, c.label + (c.default ? "  ·  selected" : "")))
      buttons.forEach((b, i) => b.addEventListener("click", () => answer(q, q.choices[i], buttons)))
      box.append(h("div", { class: "choices" }, ...buttons))
    } else if (mayReply) {
      box.append(h("p", { class: "quiet" }, "conch couldn't read the choices off its screen. Answer this one at your laptop."))
    } else {
      box.append(h("p", { class: "quiet" }, "This device can look but not answer."))
    }
    ask.replaceChildren(box)
  }

  const update = () => {
    const a = state.agents.find((x) => x.pane === pane)
    $("where").textContent = a ? agentLabel(a) : pane
    if (!a) {
      head.replaceChildren(state.listed ? "This agent has gone: its pane closed, or the agent left it." : "Loading…")
      showQuestion(null)
      reply.hidden = true
      return
    }
    const since = ago(a.since)
    head.replaceChildren(...[
      h("span", { class: `state ${a.state}` }, a.state), since === "now" || !since ? " just now" : ` for ${since}`,
      h("span", { class: "quiet" }, " · " + [a.agent, a.project?.name, a.branch].filter(Boolean).join(" · ")),
      a.failed && h("span", { class: "error" }, " · its last request failed"),
    ].filter(Boolean))
    showQuestion(a.state === "waiting" ? a.question : null)
    reply.hidden = !mayReply
    const waiting = a.state === "waiting"
    text.disabled = waiting || !state.online
    sendButton.disabled = waiting || !state.online
    replyNote.textContent = waiting ? "It is waiting for an answer; a reply would be typed onto the question."
      : a.state === "working" ? "It is working; it gets your reply when this work ends." : ""
  }

  const frame = (f) => {
    if (f.pane !== pane) return
    screen.style.fontSize = fontSizeFor(f.cols, screen.clientWidth - 16) + "px"
    screen.replaceChildren(...frameRows(f.lines).map((runs) => {
      const row = document.createElement("div")
      for (const run of runs) {
        const span = document.createElement("span")
        span.textContent = run.text
        let fg = run.fg, bg = run.bg
        if (run.inverse) [fg, bg] = [bg || "#101114", fg || "#d6d6d2"]
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

  const connected = () => send({ type: "frame.open", pane })
  connected()
  return {
    el, update, frame, connected,
    socketError: (e) => { if (e?.code !== "not_found") note.textContent = e?.message || "" },
    leave: () => send({ type: "frame.close", pane }),
  }
}

function newTaskView() {
  const status = h("p", { class: "error", role: "alert" })
  const project = h("select", { required: true }, h("option", { value: "" }, "Loading…"))
  const agent = h("select", {}, h("option", { value: "" }, "conch's default"), ...AGENTS.map((a) => h("option", { value: a }, a)))
  const name = h("input", { maxlength: "64", placeholder: "optional, e.g. reviewer" })
  const prompt = h("textarea", { required: true, rows: "5", placeholder: "What should it do?" })
  const button = h("button", { class: "primary" }, "Start")
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
  h("label", {}, "Project", project),
  h("label", {}, "Agent", agent),
  h("label", {}, "Name", name),
  h("label", {}, "Task", prompt),
  button, status)
  return { el: h("section", {}, h("p", { class: "quiet" }, "A new branch and worktree, and an agent started there."), form) }
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
  render()
  connect()
}

recall()
navigator.serviceWorker?.register("/sw.js").catch(() => {})
start()
