// The service worker run as a browser would run it: its handlers given
// events, with a fake registration and fake windows.
import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import vm from "node:vm"

function load({ windows = [] } = {}) {
  const handlers = {}
  const shown = [], opened = [], posted = [], focused = []
  const self = {
    location: { origin: "https://laptop.ts.net" },
    addEventListener: (type, fn) => { handlers[type] = fn },
    registration: { showNotification: async (title, options) => { shown.push({ title, ...options }) } },
    clients: {
      matchAll: async () => windows.map((url) => ({
        url,
        postMessage: (m) => posted.push({ window: url, ...m }),
        focus: async () => { focused.push(url) },
      })),
      openWindow: async (url) => { opened.push(url) },
      claim: async () => {},
    },
    skipWaiting: async () => {},
  }
  const code = readFileSync(new URL("../ui/sw.js", import.meta.url), "utf8")
  vm.runInNewContext(code, { self, caches: {}, fetch: async () => {}, URL, Response, console })
  const fire = async (type, event) => {
    let wait
    handlers[type]({ ...event, waitUntil: (p) => { wait = p } })
    await wait
  }
  return { fire, shown, opened, posted, focused }
}

const push = (m) => ({ data: { json: () => m } })

test("a waiting push is a notice naming the agent and its project", async () => {
  const sw = load()
  await sw.fire("push", push({ type: "waiting", pane: "p3", name: "reviewer", project: "api", url: "/agent/p3" }))
  assert.equal(sw.shown.length, 1)
  const n = sw.shown[0]
  assert.equal(n.title, "reviewer is waiting for you")
  assert.equal(n.body, "in api")
  assert.equal(n.tag, "conch-p3")
  assert.equal(n.renotify, true)
  assert.equal(n.data.url, "/agent/p3") // an object from the worker's realm: compared by field
})

test("a done push, and pushes with little in them", async () => {
  const sw = load()
  await sw.fire("push", push({ type: "done", pane: "p4", name: "", url: "/agent/p4" }))
  await sw.fire("push", push({}))
  await sw.fire("push", { data: { json: () => { throw new SyntaxError("not JSON") } } })
  await sw.fire("push", {})
  assert.deepEqual(sw.shown.map((n) => [n.title, n.body, n.tag, n.data.url]), [
    ["An agent is done", "", "conch-p4", "/agent/p4"],
    ["An agent is waiting for you", "", "conch", "/"],
    ["An agent is waiting for you", "", "conch", "/"],
    ["An agent is waiting for you", "", "conch", "/"],
  ])
})

test("a notice about an agent on another machine opens that agent", async () => {
  const sw = load()
  await sw.fire("push", push({ type: "waiting", pane: "busybox:p1", name: "reviewer", project: "api", url: "/agent/busybox:p1" }))
  const n = sw.shown.at(-1)
  // The pane carries its machine, so the tap lands on that agent's page
  // rather than on the list — which is what a bare `p[0-9]+` left.
  assert.equal(n.data.url, "/agent/busybox:p1")
  // The tag only groups notices in the browser, so the machine's colon
  // is no trouble there; the push Topic header, which must be URL-safe,
  // is sanitised on the Go side (TestPushOnTransitions).
  assert.equal(n.tag, "conch-busybox:p1")
})

test("a push can't aim a tap anywhere but its agent", async () => {
  const sw = load()
  for (const url of ["https://evil.example/", "//evil.example/x", "/agent/p9", "/settings"]) {
    await sw.fire("push", push({ type: "waiting", pane: "p3", name: "x", url }))
  }
  await sw.fire("push", push({ type: "waiting", pane: "../x", name: "x", url: "/agent/../x" }))
  assert.deepEqual(sw.shown.map((n) => n.data.url), ["/", "/", "/", "/", "/"])
  assert.equal(sw.shown[4].tag, "conch")
})

test("a tap opens the agent: in the open app, or a new window", async () => {
  const open = load({ windows: ["https://elsewhere.example/", "https://laptop.ts.net/"] })
  const closed = []
  await open.fire("notificationclick", { notification: { data: { url: "/agent/p3" }, close: () => closed.push(1) } })
  // Only this site's window is told, and told where to go.
  assert.equal(open.posted.length, 1)
  assert.equal(open.posted[0].type, "open")
  assert.equal(open.posted[0].url, "/agent/p3")
  assert.deepEqual(open.focused, ["https://laptop.ts.net/"])
  assert.deepEqual(open.opened, [])
  assert.equal(closed.length, 1)

  const none = load()
  await none.fire("notificationclick", { notification: { data: { url: "/agent/p3" }, close: () => {} } })
  await none.fire("notificationclick", { notification: { close: () => {} } })
  assert.deepEqual(none.opened, ["/agent/p3", "/"])
})
