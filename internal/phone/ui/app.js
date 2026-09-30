// The stub page: pair this device, then list the agents. Text from the
// server goes in with textContent only — an agent's title is not markup.
const $ = (id) => document.getElementById(id)

async function api(path, options) {
  const res = await fetch(path, { credentials: "same-origin", ...options })
  const body = res.status === 204 ? null : await res.json().catch(() => null)
  if (!res.ok) throw Object.assign(new Error(body?.error?.message || res.statusText), { code: body?.error?.code })
  return body
}

async function show() {
  let hello
  try {
    hello = await api("/api/hello")
  } catch (err) {
    $("agents").hidden = true
    $("pair").hidden = false
    $("status").textContent = err.code === "unauthorized" ? "This device isn't paired." : err.message
    return
  }
  $("pair").hidden = true
  $("agents").hidden = false
  $("status").textContent = `Paired as ${hello.device_id} (${hello.permission}) · conch ${hello.conch_version}`
  const { agents } = await api("/api/agents")
  const list = $("list")
  list.replaceChildren()
  if (agents.length === 0) list.append(Object.assign(document.createElement("li"), { textContent: "No agents running." }))
  for (const a of agents) {
    const li = document.createElement("li")
    const state = Object.assign(document.createElement("span"), { className: "state", textContent: a.state })
    li.append(state, ` · ${a.agent} in ${a.pane} · ${a.title || a.name}`)
    if (a.question) li.append(document.createElement("br"), a.question.text)
    list.append(li)
  }
}

$("pair").addEventListener("submit", async (ev) => {
  ev.preventDefault()
  try {
    await api("/pair", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: $("code").value, device_name: $("name").value }),
    })
    await show()
  } catch (err) {
    $("status").textContent = err.message
  }
})

show().catch((err) => { $("status").textContent = err.message })
