// conch's protocol over a stream: newline-delimited JSON, a request with
// an id answered by a message with the same id, and events pushed with no
// id (internal/proto). One Connection per bridge.
"use strict"
const { EventEmitter } = require("node:events")

class Connection extends EventEmitter {
  /** input: where messages to the server are written (the bridge's
   *  stdin); output: where its messages arrive (its stdout). */
  constructor(input, output) {
    super()
    this.input = input
    this.next = 1
    this.waiting = new Map()
    this.closed = false
    let held = Buffer.alloc(0)
    output.on("data", (chunk) => {
      held = held.length ? Buffer.concat([held, chunk]) : chunk
      let nl
      while ((nl = held.indexOf(10)) >= 0) {
        const line = held.subarray(0, nl).toString("utf8")
        held = held.subarray(nl + 1)
        if (line.trim()) this.receive(line)
      }
    })
    output.on("end", () => this.close("the bridge ended"))
    output.on("error", (e) => this.close(String(e)))
  }

  receive(line) {
    let m
    try { m = JSON.parse(line) } catch { return } // a stray line is not a message
    if (m.id && this.waiting.has(m.id)) {
      const w = this.waiting.get(m.id)
      this.waiting.delete(m.id)
      if (m.error) w.reject(Object.assign(new Error(m.error.message || m.error.code), { code: m.error.code }))
      else w.resolve(m.result ?? null)
    } else if (m.event) {
      this.emit("event", { event: m.event, data: m.data ?? null })
    }
  }

  /** A request, answered with its result or failed with its error. */
  call(method, params) {
    if (this.closed) return Promise.reject(new Error("not connected"))
    const id = String(this.next++)
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject })
      this.input.write(JSON.stringify(params === undefined ? { id, method } : { id, method, params }) + "\n")
    })
  }

  close(why) {
    if (this.closed) return
    this.closed = true
    for (const w of this.waiting.values()) w.reject(new Error(why))
    this.waiting.clear()
    this.emit("close", why)
  }
}

/** The methods the window may ask for: what it draws and the panes it
 *  shows. Anything else — reloading the server, closing projects, running
 *  agents' setups — stays out of a page's reach. */
const allowed = new Set([
  "pane.list", "project.list", "pane.raw", "pane.unsubscribe", "pane.resize", "pane.send_text", "pane.create", "pane.close", "agent.list",
])

module.exports = { Connection, allowed }
