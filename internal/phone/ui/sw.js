// The service worker keeps the app's own files, so it opens when the
// laptop can't be reached and says so, rather than showing the browser's
// error page. It asks the network first: a new conch build is picked up
// the next time the laptop answers. Nothing from /api or /pair is ever
// kept — an agent's screen does not belong in a cache.
const CACHE = "conch-shell-1"
const SHELL = ["/", "/app.css", "/app.mjs", "/lib.mjs", "/manifest.webmanifest", "/icon.svg", "/icon-180.png"]

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()))
})

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()))
})

self.addEventListener("fetch", (event) => {
  const req = event.request
  const url = new URL(req.url)
  if (req.method !== "GET" || url.origin !== self.location.origin) return
  if (url.pathname.startsWith("/api/") || url.pathname === "/pair") return
  // Every page of the app is the same document.
  const key = req.mode === "navigate" ? "/" : url.pathname
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (res.ok && SHELL.includes(key)) {
          const copy = res.clone()
          caches.open(CACHE).then((c) => c.put(key, copy))
        }
        return res
      })
      .catch(() => caches.match(key).then((hit) => hit || Response.error())))
})

// notice is what a push says on the lock screen: who is waiting or done,
// and where. The message carries no question and no screen text, and
// nothing in it is taken for an address but an agent's own page.
function notice(m) {
  const who = typeof m.name === "string" && m.name ? m.name : "An agent"
  const pane = typeof m.pane === "string" && /^p[0-9]+$/.test(m.pane) ? m.pane : ""
  return {
    title: m.type === "done" ? `${who} is done` : `${who} is waiting for you`,
    body: typeof m.project === "string" && m.project ? `in ${m.project}` : "",
    tag: pane ? `conch-${pane}` : "conch",
    url: pane && m.url === `/agent/${pane}` ? m.url : "/",
  }
}

self.addEventListener("push", (event) => {
  let m = {}
  try { m = event.data ? event.data.json() : {} } catch { /* shown as a plain notice */ }
  const n = notice(m || {})
  // Every push shows something: browsers take back the permission of a
  // site whose pushes don't.
  event.waitUntil(self.registration.showNotification(n.title, {
    body: n.body, tag: n.tag, renotify: true, icon: "/icon-180.png", badge: "/icon-180.png", data: { url: n.url },
  }))
})

// A tap opens the agent: in the app if it is open, else a new window.
self.addEventListener("notificationclick", (event) => {
  event.notification.close()
  const url = (event.notification.data && event.notification.data.url) || "/"
  event.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((windows) => {
    for (const w of windows) {
      if (new URL(w.url).origin !== self.location.origin) continue
      w.postMessage({ type: "open", url })
      return w.focus()
    }
    return self.clients.openWindow(url)
  }))
})
