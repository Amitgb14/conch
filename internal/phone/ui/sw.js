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
