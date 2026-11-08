// The phone's end of the relay (internal/relay): a Noise session with the
// gateway, made through a relay that copies it and cannot read it (though
// it serves this file, so it is trusted to serve it unchanged), and the
// app's calls and its socket carried inside.
//
// Noise_NK_25519_AESGCM_SHA256, every part of it in WebCrypto: the phone
// knows the gateway's key already — the pairing QR code carried it, past
// the relay — so a relay answering in the gateway's place cannot finish
// the handshake. Inside, each direction is a stream of frames (four bytes
// of length, then JSON), the gateway's deflated (deflate-raw, flushed at
// every frame). internal/relay/tunnel.go is the other side; a test runs
// this against it.

const NAME = "Noise_NK_25519_AESGCM_SHA256"
export const PROLOGUE = "conch-relay-v1"
export const VERSION = 1
// The relay's close codes (internal/relay/relay.go).
export const NO_GATEWAY = 4404
export const BUSY = 4429
const MAX_PLAIN = 65535 - 16
// CALL_WAIT is how long a call waits for its answer: the gateway's own
// longest (starting a task makes a worktree first) and some. An answer
// lost on the way would otherwise leave the app waiting for ever.
export const CALL_WAIT = 75_000

const enc = new TextEncoder()
const dec = new TextDecoder()
const subtle = globalThis.crypto.subtle

function concat(...parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let i = 0
  for (const p of parts) { out.set(p, i); i += p.length }
  return out
}

async function sha256(b) { return new Uint8Array(await subtle.digest("SHA-256", b)) }

async function hmac(key, data) {
  const k = await subtle.importKey("raw", key, { name: "HMAC", hash: "SHA-256" }, false, ["sign"])
  return new Uint8Array(await subtle.sign("HMAC", k, data))
}

// hkdf is Noise's: two outputs from a chaining key and input.
async function hkdf(ck, ikm) {
  const temp = await hmac(ck, ikm)
  const o1 = await hmac(temp, new Uint8Array([1]))
  const o2 = await hmac(temp, concat(o1, new Uint8Array([2])))
  return [o1, o2]
}

function nonce(n) {
  const iv = new Uint8Array(12)
  new DataView(iv.buffer).setBigUint64(4, BigInt(n), false)
  return iv
}

// cipher is a CipherState: a key and a counter.
class Cipher {
  constructor(key) { this.key = key; this.n = 0; this.k = null }
  async ck() {
    if (!this.k) this.k = await subtle.importKey("raw", this.key, "AES-GCM", false, ["encrypt", "decrypt"])
    return this.k
  }
  async encrypt(ad, pt) {
    const iv = nonce(this.n++)
    return new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv, additionalData: ad }, await this.ck(), pt))
  }
  async decrypt(ad, ct) {
    const iv = nonce(this.n++)
    return new Uint8Array(await subtle.decrypt({ name: "AES-GCM", iv, additionalData: ad }, await this.ck(), ct))
  }
}

async function x25519() {
  const pair = await subtle.generateKey({ name: "X25519" }, true, ["deriveBits"])
  return { priv: pair.privateKey, pub: new Uint8Array(await subtle.exportKey("raw", pair.publicKey)) }
}

async function dh(priv, pub) {
  const k = await subtle.importKey("raw", pub, { name: "X25519" }, false, [])
  return new Uint8Array(await subtle.deriveBits({ name: "X25519", public: k }, priv, 256))
}

// Symmetric is Noise's SymmetricState, for the one pattern used here.
class Symmetric {
  async init() {
    const name = enc.encode(NAME)
    this.h = name.length <= 32 ? concat(name, new Uint8Array(32 - name.length)) : await sha256(name)
    this.ck = this.h
    this.c = null
  }
  async mixHash(b) { this.h = await sha256(concat(this.h, b)) }
  async mixKey(ikm) {
    const [ck, k] = await hkdf(this.ck, ikm)
    this.ck = ck
    this.c = new Cipher(k)
  }
  async encryptAndHash(pt) {
    const ct = this.c ? await this.c.encrypt(this.h, pt) : pt
    await this.mixHash(ct)
    return ct
  }
  async decryptAndHash(ct) {
    const pt = this.c ? await this.c.decrypt(this.h, ct) : ct
    await this.mixHash(ct)
    return pt
  }
  async split() {
    const [a, b] = await hkdf(this.ck, new Uint8Array(0))
    return [new Cipher(a), new Cipher(b)]
  }
}

// initiate is the phone's half of Noise NK. write sends a message; read
// waits for one. It returns the two directions' ciphers and the gateway's
// handshake payload.
export async function initiate(gatewayKey, payload, write, read) {
  const s = new Symmetric()
  await s.init()
  await s.mixHash(enc.encode(PROLOGUE))
  await s.mixHash(gatewayKey)
  // -> e, es
  const e = await x25519()
  await s.mixHash(e.pub)
  await s.mixKey(await dh(e.priv, gatewayKey))
  const ct = await s.encryptAndHash(payload)
  write(concat(e.pub, ct))
  // <- e, ee
  const msg = await read()
  if (msg.length < 32 + 16) throw new Error("the answer is too short to be a handshake")
  const re = msg.slice(0, 32)
  await s.mixHash(re)
  await s.mixKey(await dh(e.priv, re))
  let reply
  try {
    reply = await s.decryptAndHash(msg.slice(32))
  } catch {
    throw new Error("the answer isn't from the computer this phone paired with")
  }
  const [send, recv] = await s.split()
  return { send, recv, payload: reply }
}

// frame is a frame as the stream carries it.
export function frame(obj) {
  const body = enc.encode(JSON.stringify(obj))
  const out = new Uint8Array(4 + body.length)
  new DataView(out.buffer).setUint32(0, body.length, false)
  out.set(body, 4)
  return out
}

// Frames takes a stream of bytes in pieces and gives back the whole frames
// in it.
export class Frames {
  constructor() { this.buf = new Uint8Array(0) }
  push(bytes) {
    this.buf = this.buf.length ? concat(this.buf, bytes) : bytes
    const out = []
    for (;;) {
      if (this.buf.length < 4) break
      const n = new DataView(this.buf.buffer, this.buf.byteOffset, 4).getUint32(0, false)
      if (this.buf.length < 4 + n) break
      out.push(JSON.parse(dec.decode(this.buf.subarray(4, 4 + n))))
      this.buf = this.buf.slice(4 + n)
    }
    return out
  }
}

// cookieFrom takes the device's token out of a Set-Cookie the gateway
// sent: "" for one that clears it, null when it says nothing of it.
export function cookieFrom(setCookie, name) {
  if (!setCookie) return null
  for (const line of setCookie.split("\n")) {
    const [pair, ...attrs] = line.split(";")
    const eq = pair.indexOf("=")
    if (eq < 0 || pair.slice(0, eq).trim() !== name) continue
    const value = pair.slice(eq + 1).trim()
    const gone = attrs.some((a) => /^\s*max-age\s*=\s*(0|-\d+)\s*$/i.test(a))
    return gone ? "" : value
  }
  return null
}

// relayLink is where the relay is and what it needs, from the pairing
// link's fragment: room and key, both base64url. Anything else is null.
export function relayLink(hash) {
  const p = new URLSearchParams(String(hash || "").replace(/^#/, ""))
  const room = p.get("room") || ""
  const key = p.get("key") || ""
  if (!/^[A-Za-z0-9_-]{22}$/.test(room) || !/^[A-Za-z0-9_-]{43}$/.test(key)) return null
  return { room, key }
}

export function fromB64url(s) {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4))
  return Uint8Array.from(b, (c) => c.charCodeAt(0))
}

// WORDS is internal/relay/words.go's list; a test says they agree.
export const WORDS = [
  "acorn", "amber", "anchor", "apple", "arrow", "aspen", "atlas", "badge",
  "bamboo", "banjo", "basil", "beacon", "bean", "bear", "beetle", "bell",
  "berry", "birch", "bison", "blaze", "bloom", "boat", "bolt", "bone",
  "brick", "brook", "brush", "bucket", "cabin", "cactus", "camel", "candle",
  "canoe", "canyon", "cargo", "carrot", "castle", "cedar", "chalk", "cherry",
  "cider", "cinder", "clam", "cliff", "clock", "cloud", "clover", "cobalt",
  "comet", "coral", "cotton", "crane", "creek", "crow", "crystal", "cube",
  "daisy", "delta", "denim", "desert", "dingo", "dolphin", "dove", "dragon",
  "drum", "dune", "eagle", "ember", "falcon", "fern", "ferry", "fig",
  "finch", "flame", "flint", "flute", "fog", "forest", "fossil", "fox",
  "frost", "gale", "garnet", "gecko", "ginger", "glacier", "globe", "goose",
  "granite", "grape", "gravel", "gull", "harbor", "hazel", "heron", "hill",
  "honey", "horizon", "husky", "iris", "island", "ivory", "jade", "jaguar",
  "jasper", "jelly", "jungle", "kayak", "kelp", "kettle", "kiwi", "koala",
  "lagoon", "lake", "lantern", "larch", "lava", "lemon", "lily", "lime",
  "linen", "lion", "lotus", "lynx", "magnet", "mango", "maple", "marble",
  "meadow", "melon", "mesa", "mint", "moose", "moss", "moth", "nectar",
  "nest", "nickel", "noodle", "nutmeg", "oak", "oasis", "ocean", "olive",
  "onyx", "opal", "orbit", "orchid", "otter", "owl", "oyster", "paddle",
  "palm", "panda", "papaya", "parrot", "peach", "pearl", "pebble", "pecan",
  "pepper", "piano", "pine", "planet", "plum", "polar", "pony", "poppy",
  "prairie", "puffin", "pumpkin", "quail", "quartz", "quill", "rabbit", "radish",
  "rain", "raven", "reed", "reef", "ribbon", "ridge", "river", "robin",
  "rocket", "rook", "rose", "ruby", "saddle", "saffron", "sage", "salmon",
  "sand", "satin", "scarf", "seal", "sequoia", "shadow", "shell", "silver",
  "sky", "slate", "sloth", "snow", "sparrow", "spruce", "squid", "star",
  "stone", "storm", "sugar", "summit", "sun", "swan", "tango", "thistle",
  "thunder", "tide", "tiger", "timber", "toast", "topaz", "torch", "trout",
  "tulip", "tundra", "turtle", "valley", "velvet", "violet", "volcano", "walnut",
  "walrus", "wave", "willow", "wind", "wolf", "wren", "yak", "zebra",
  "alder", "bluebell", "cocoa", "dahlia", "egret", "fjord", "grove", "hawk",
  "igloo", "juniper", "krill", "lemur", "marsh", "nova", "pixel", "quasar",
]

// fingerprint is a key as four words, as relay.Fingerprint says it.
export async function fingerprint(pub) {
  const sum = await sha256(pub)
  const out = []
  for (let i = 0; i < 4; i++) out.push(WORDS[((sum[2 * i] << 8) | sum[2 * i + 1]) % WORDS.length])
  return out.join(" ")
}

// Tunnel is a session with the gateway through a relay. Calls and sockets
// made before it is open wait for it; when it closes, every call fails and
// every socket closes, and the next use opens it again.
export class Tunnel {
  constructor({ relay, room, key, WebSocketImpl = globalThis.WebSocket }) {
    this.url = `${relay.replace(/^http/, "ws").replace(/\/$/, "")}/relay/phone?room=${encodeURIComponent(room)}`
    this.key = typeof key === "string" ? fromB64url(key) : key
    this.WS = WebSocketImpl
    this.opening = null
    this.next = 0
    this.calls = new Map()
    this.sockets = new Map()
    this.onclose = null // (code, reason): the session ended
    this.callWait = CALL_WAIT
  }

  open() {
    if (!this.opening) this.opening = this.#open().catch((err) => { this.opening = null; throw err })
    return this.opening
  }

  async #open() {
    const ws = new this.WS(this.url)
    ws.binaryType = "arraybuffer"
    this.ws = ws
    const inbox = []
    let waiting = null
    let closed = null
    const fail = (code, reason) => {
      const err = new Error(reason || (code === NO_GATEWAY ? "Your computer isn't connected to the relay." : "The relay closed the connection."))
      err.code = code
      return err
    }
    ws.onmessage = (ev) => {
      const b = new Uint8Array(ev.data)
      if (waiting) { const w = waiting; waiting = null; w.resolve(b) } else inbox.push(b)
    }
    ws.onclose = (ev) => {
      closed = fail(ev.code, ev.reason)
      if (waiting) { const w = waiting; waiting = null; w.reject(closed) }
      if (this.ws === ws) this.#ended(ev.code, ev.reason)
    }
    await new Promise((resolve, reject) => {
      ws.onopen = resolve
      ws.onerror = () => reject(closed || fail(0, "The relay can't be reached."))
      const prev = ws.onclose
      ws.onclose = (ev) => { prev(ev); reject(closed) }
    })
    const read = () => inbox.length ? Promise.resolve(inbox.shift()) :
      closed ? Promise.reject(closed) : new Promise((resolve, reject) => { waiting = { resolve, reject } })
    let send, recv, hello
    try {
      const done = await initiate(this.key, enc.encode(JSON.stringify({ v: VERSION })), (b) => ws.send(b), read)
      ;({ send, recv } = done)
      hello = JSON.parse(dec.decode(done.payload))
      if (hello.v !== VERSION) throw new Error(`The computer speaks tunnel version ${hello.v}; this app ${VERSION}. Reload the app.`)
    } catch (err) {
      this.ws = null
      ws.close(1000)
      throw err
    }
    this.send = send
    this.recv = recv
    // Outgoing messages are sealed in order: each waits for the last.
    this.sealing = Promise.resolve()
    const frames = new Frames()
    let take
    // A frame that doesn't parse ends the session: carrying on would read
    // the rest of the stream from the wrong place.
    const bad = () => { if (ws.readyState <= 1) ws.close(4000, "bad frame") }
    if (hello.z === "deflate-raw") {
      const ds = new DecompressionStream("deflate-raw")
      const writer = ds.writable.getWriter()
      const reader = ds.readable.getReader()
      ;(async () => {
        for (;;) {
          const { value, done } = await reader.read().catch(() => ({ done: true }))
          if (done) return
          try {
            for (const f of frames.push(value)) this.#take(f)
          } catch {
            bad()
            return
          }
        }
      })()
      take = (b) => writer.write(b)
    } else {
      take = (b) => { try { for (const f of frames.push(b)) this.#take(f) } catch { bad() } }
    }
    // And incoming ones opened in order.
    let opening = Promise.resolve()
    const onData = (b) => {
      opening = opening.then(() => recv.decrypt(new Uint8Array(0), b)).then(take).catch(() => ws.close(4000, "bad message"))
    }
    for (const b of inbox.splice(0)) onData(b)
    ws.onmessage = (ev) => onData(new Uint8Array(ev.data))
  }

  #ended(code, reason) {
    this.opening = null
    this.ws = null
    const err = new Error(code === NO_GATEWAY ? "Your computer isn't connected to the relay." : "The relay connection closed.")
    err.code = code
    for (const [, c] of this.calls) c.reject(err)
    this.calls.clear()
    for (const [, s] of this.sockets) s._closed(code === NO_GATEWAY ? code : 1006)
    this.sockets.clear()
    this.onclose?.(code, reason)
  }

  #write(obj) {
    const ws = this.ws
    const bytes = frame(obj)
    this.sealing = this.sealing.then(async () => {
      for (let i = 0; i < bytes.length; i += MAX_PLAIN) {
        const ct = await this.send.encrypt(new Uint8Array(0), bytes.subarray(i, i + MAX_PLAIN))
        if (ws.readyState !== 1) throw new Error("closed")
        ws.send(ct)
      }
    })
    return this.sealing
  }

  #take(f) {
    if (f.t === "res") {
      const c = this.calls.get(f.id)
      if (!c) return
      this.calls.delete(f.id)
      c.resolve({ status: f.s, headers: f.h || {}, body: f.b || "" })
      return
    }
    const s = this.sockets.get(f.id)
    if (!s) return
    if (f.t === "wo") s._opened()
    else if (f.t === "wm") s._message(f.d || "")
    else if (f.t === "wc") { this.sockets.delete(f.id); s._closed(f.c || 1000, f.r || "") }
  }

  // request makes a call on the gateway: {status, headers, body}.
  async request(method, path, headers = {}, body = undefined) {
    await this.open()
    const id = ++this.next
    let timer
    const p = new Promise((resolve, reject) => {
      const done = (fn) => (v) => { clearTimeout(timer); this.calls.delete(id); fn(v) }
      this.calls.set(id, { resolve: done(resolve), reject: done(reject) })
      timer = setTimeout(() => {
        const err = new Error("The computer didn't answer.")
        err.code = "timeout"
        this.calls.get(id)?.reject(err)
      }, this.callWait)
    })
    try {
      await this.#write({ t: "req", id, m: method, p: path, h: headers, b: body ?? "" })
    } catch (err) {
      this.calls.get(id)?.reject(err)
    }
    return p
  }

  // socket opens a WebSocket on the gateway; what comes back acts as one
  // (onopen, onmessage, onclose, send, close, readyState).
  socket(path, headers = {}) {
    const s = new TunnelSocket(this, ++this.next)
    this.sockets.set(s.id, s)
    this.open().then(() => this.#write({ t: "wo", id: s.id, p: path, h: headers }))
      .catch((err) => { this.sockets.delete(s.id); s._closed(err.code || 1006, err.message) })
    return s
  }

  _send(id, d) { return this.#write({ t: "wm", id, d }).catch(() => {}) }
  _close(id) { this.sockets.delete(id); return this.#write({ t: "wc", id, c: 1000 }).catch(() => {}) }

  close() { this.ws?.close(1000) }
}

export class TunnelSocket {
  constructor(tunnel, id) {
    this.tunnel = tunnel
    this.id = id
    this.readyState = 0
    this.onopen = this.onmessage = this.onclose = this.onerror = null
  }
  _opened() { this.readyState = 1; this.onopen?.({}) }
  _message(d) { this.onmessage?.({ data: d }) }
  _closed(code, reason = "") {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.({ code, reason })
  }
  send(text) { if (this.readyState === 1) this.tunnel._send(this.id, String(text)) }
  close() {
    if (this.readyState >= 2) return
    this.tunnel._close(this.id)
    this._closed(1000)
  }
}
