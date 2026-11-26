// The desktop app's parts that need no window: run with `node --test`.
import assert from "node:assert/strict"
import { createRequire } from "node:module"
import { PassThrough } from "node:stream"
import { test } from "node:test"
import { agentsState, ago, buildTree, bytes, paneLabel, paneState, waiting } from "../renderer/lib/model.mjs"

const require = createRequire(import.meta.url)
const { bridgeCommand, findConch } = require("../src/command.cjs")
const { Connection, allowed } = require("../src/connection.cjs")

// --- reaching the server

test("on Linux the bridge is conch's own, found where it is installed", () => {
  const c = bridgeCommand({ platform: "linux", env: {}, home: "/home/a", exists: (p) => p === "/home/a/.local/bin/conch" })
  assert.deepEqual(c, { file: "/home/a/.local/bin/conch", args: ["bridge"], where: "this computer" })
  assert.equal(findConch({}, "/home/a", () => false), "conch", "else PATH finds it")
  assert.equal(findConch({ CONCH_BIN: "/x/conch" }, "/home/a", () => true), "/x/conch", "CONCH_BIN first")
})

test("on Windows the bridge runs inside WSL, the program never read as shell", () => {
  const c = bridgeCommand({ platform: "win32", env: {}, home: "C:\\Users\\a", exists: () => false })
  assert.equal(c.file, "wsl.exe")
  assert.deepEqual(c.args, ["-e", "sh", "-lc", 'exec "$0" bridge', "conch"])
  const d = bridgeCommand({ platform: "win32", env: { CONCH_WSL_DISTRO: "Ubuntu", CONCH_BIN: "/opt/conch; rm -rf ~" }, home: "", exists: () => false })
  assert.deepEqual(d.args, ["-d", "Ubuntu", "-e", "sh", "-lc", 'exec "$0" bridge', "/opt/conch; rm -rf ~"])
  assert.equal(d.where, "WSL (Ubuntu)")
})

function pair() {
  const toServer = new PassThrough(), fromServer = new PassThrough()
  const c = new Connection(toServer, fromServer)
  const sent = []
  let buf = ""
  toServer.on("data", (b) => { buf += b; let i; while ((i = buf.indexOf("\n")) >= 0) { sent.push(JSON.parse(buf.slice(0, i))); buf = buf.slice(i + 1) } })
  return { c, sent, say: (s) => fromServer.write(s), end: () => fromServer.end() }
}
const tick = () => new Promise((r) => setImmediate(r))

test("a call is answered by its id, whatever the order", async () => {
  const { c, sent, say } = pair()
  const a = c.call("pane.list")
  const b = c.call("project.list", { x: 1 })
  await tick()
  assert.deepEqual(sent, [{ id: "1", method: "pane.list" }, { id: "2", method: "project.list", params: { x: 1 } }])
  say('{"id":"2","result":{"projects":[]}}\n{"id":"1","result":{"panes":[1]}}\n')
  assert.deepEqual(await a, { panes: [1] })
  assert.deepEqual(await b, { projects: [] })
})

test("messages cut anywhere are put back together; junk lines are dropped", async () => {
  const { c, say } = pair()
  const events = []
  c.on("event", (e) => events.push(e))
  const r = c.call("hello")
  await tick()
  say('{"event":"pane.upd')
  say('ated","data":{"id":"p1"}}\nnot json\n\n{"id":"1","res')
  say('ult":{"ok":true}}\n')
  assert.deepEqual(await r, { ok: true })
  assert.deepEqual(events, [{ event: "pane.updated", data: { id: "p1" } }])
})

test("an error answer fails the call with its code", async () => {
  const { c, say } = pair()
  const r = c.call("pane.close", { id: "p9" })
  await tick()
  say('{"id":"1","error":{"code":"not_found","message":"no pane p9"}}\n')
  await assert.rejects(r, (e) => e.code === "not_found" && e.message === "no pane p9")
})

test("the bridge ending fails what waits and every call after", async () => {
  const { c, end } = pair()
  let why = ""
  c.on("close", (w) => { why = w })
  const r = c.call("pane.list")
  end()
  await assert.rejects(r, /bridge ended/)
  assert.equal(why, "the bridge ended")
  await assert.rejects(c.call("pane.list"), /not connected/)
})

test("the window may ask for what it draws, nothing else", () => {
  for (const m of ["pane.list", "project.list", "pane.raw", "pane.send_text", "pane.resize", "pane.create", "pane.close"]) assert.ok(allowed.has(m), m)
  for (const m of ["server.stop", "server.reload", "project.remove", "agent.setup.sync", "token.create", "hello", "auth.token"]) assert.ok(!allowed.has(m), m)
})

// --- what the window draws

const projects = [{ id: "j1", name: "ledger" }, { id: "j2", name: "api" }, { id: "j3", name: "empty" }]
const panes = [
  { id: "p1", name: "claude", title: "Fix overdraft", project_id: "j1", branch: "fix/overdraft", state: "running", agent: { name: "claude", state: "working", since: "2026-11-25T10:00:00Z" } },
  { id: "p2", name: "claude", title: "Add health check", project_id: "j1", state: "running", agent: { name: "claude", state: "blocked", message: "Allow go test?", since: "2026-11-25T10:09:00Z" } },
  { id: "p3", name: "zsh", project_id: "j2", state: "running" },
  { id: "p4", name: "dev", custom_name: true, title: "npm run dev", state: "running" },
  { id: "p5", name: "codex", project_id: "j1", state: "exited", agent: { name: "codex", state: "done" } },
  { id: "p6", name: "sh", project_id: "gone", state: "running" },
]

test("panes are named and stated as the sidebar says them", () => {
  assert.equal(paneLabel(panes[0]), "Fix overdraft")
  assert.equal(paneLabel(panes[3]), "dev", "a name given it wins")
  assert.equal(paneLabel({ id: "p9" }), "p9")
  assert.deepEqual(panes.map(paneState), ["working", "waiting", "terminal", "terminal", "exited", "terminal"])
})

test("the tree: projects by name with panes, waiting first; the rest loose", () => {
  const t = buildTree(panes, projects)
  assert.deepEqual(t.groups.map((g) => g.project.name), ["api", "ledger"], "a project with no pane is left out")
  assert.deepEqual(t.groups[1].panes.map((p) => p.id), ["p2", "p1", "p5"])
  assert.deepEqual(t.loose.map((p) => p.id), ["p4", "p6"], "no project, or one gone")
  assert.deepEqual(buildTree([], []), { groups: [], loose: [] })
  assert.deepEqual(buildTree(undefined, undefined), { groups: [], loose: [] })
})

test("waiting is longest first", () => {
  const more = [...panes, { id: "p7", state: "running", agent: { name: "codex", state: "blocked", since: "2026-11-25T09:00:00Z" } }]
  assert.deepEqual(waiting(more).map((p) => p.id), ["p7", "p2"])
})

test("ago", () => {
  const now = Date.parse("2026-11-25T10:10:00Z")
  assert.equal(ago("2026-11-25T10:09:50Z", now), "now")
  assert.equal(ago("2026-11-25T10:00:00Z", now), "10m")
  assert.equal(ago("2026-11-25T07:00:00Z", now), "3h")
  assert.equal(ago("2026-11-20T10:00:00Z", now), "5d")
  assert.equal(ago("", now), undefined)
  assert.equal(ago(undefined, now), undefined)
  assert.equal(ago("0001-01-01T00:00:00Z", now), undefined, "Go's zero time is no time")
})

test("the Agents overview's state for conch-ui", () => {
  const s = agentsState(panes, projects, Date.parse("2026-11-25T10:10:00Z"), "p1")
  assert.deepEqual(s.agents.map((a) => a.key), ["p2", "p1", "p5"], "agents only, waiting first, exited last")
  assert.equal(s.agents[0].question.text, "Allow go test?")
  assert.equal(s.agents[1].question, undefined)
  assert.equal(s.agents[1].place, "ledger · fix/overdraft")
  assert.equal(s.agents[1].focused, true)
  assert.equal(s.agents[2].running, false)
  assert.deepEqual(s.activity.map((e) => e.text), ["Add health check is waiting on you", "Fix overdraft started working"])
  assert.ok(s.colors.bg.startsWith("#"))
  assert.deepEqual(agentsState([], [], 0).agents, [])
})

test("bytes from base64", () => {
  assert.deepEqual([...bytes(Buffer.from("hi\x1b[0m").toString("base64"))], [104, 105, 27, 91, 48, 109])
  assert.equal(bytes("").length, 0)
  assert.equal(bytes(undefined).length, 0)
})
