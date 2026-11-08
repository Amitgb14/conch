// Run by TestTunnelJSAgainstGo: this app's end of the relay against the
// Go gateway's, through a real relay. Prints "ok" or throws.
import assert from "node:assert/strict"
import { Tunnel, fingerprint, fromB64url } from "../ui/tunnel.mjs"

const { RELAY, ROOM, KEY, FP, WRONG } = process.env

if (WRONG) {
  // Another computer's key: the handshake fails, and says so.
  const t = new Tunnel({ relay: RELAY, room: ROOM, key: WRONG })
  await assert.rejects(t.request("GET", "/api/echo"), /isn't from the computer this phone paired with|closed/)
  console.log("ok")
  process.exit(0)
}

assert.equal(await fingerprint(fromB64url(KEY)), FP)
const t = new Tunnel({ relay: RELAY, room: ROOM, key: KEY })
const res = await t.request("POST", "/api/echo", { "Content-Type": "application/json", Cookie: "conch_device=tok" }, '{"a":1}')
assert.equal(res.status, 200)
const got = JSON.parse(res.body)
assert.equal(got.body, '{"a":1}')
assert.equal(got.cookie, "conch_device=tok")
assert.match(res.headers["Set-Cookie"], /conch_device=tok/)

// Calls at once come back to their own callers.
const many = await Promise.all(Array.from({ length: 20 }, (_, i) => t.request("POST", "/api/echo", {}, String(i))))
many.forEach((r, i) => assert.equal(JSON.parse(r.body).body, String(i)))

// A big answer: many Noise messages, deflated.
const big = await t.request("GET", "/api/big")
assert.equal(big.body.length, Number(process.env.BIG))

// A call that is never answered gives up, and the tunnel goes on.
t.callWait = 300
await assert.rejects(t.request("GET", "/api/hang"), (err) => err.code === "timeout")
t.callWait = 30_000
assert.equal((await t.request("POST", "/api/echo", {}, "after")).status, 200)
assert.equal(t.calls.size, 0)

// An answer too large for the tunnel comes back as an error, not silence.
const tooBig = await t.request("GET", "/api/escapes")
assert.equal(tooBig.status, 502)
assert.match(tooBig.body, /too large/)

// A socket, in order, with a message over 64 KB each way.
const s = t.socket("/api/socket")
const got2 = []
const closed = new Promise((resolve) => { s.onclose = (ev) => resolve(ev.code) })
await new Promise((resolve) => { s.onopen = resolve })
const long = "x".repeat(150000)
const want = ["a", "b", long, "c"]
const all = new Promise((resolve) => {
  s.onmessage = (ev) => { got2.push(ev.data); if (got2.length === want.length) resolve() }
})
for (const m of want) s.send(m)
await all
assert.deepEqual(got2, want.map((m) => "echo:" + m))
s.send("bye")
assert.equal(await closed, 1000)
t.close()
console.log("ok")
