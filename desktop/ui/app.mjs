// The phase 0 page: paints frames from the host and measures how long that
// takes (bench), or shows one live pane and measures keystroke to echo
// (live). Results go back to the host as a report.

import { Term } from "./term.mjs"

const status = document.getElementById("status")
const canvas = document.getElementById("term")
const box = document.getElementById("box")
const ime = document.getElementById("ime")
const say = (s) => { status.textContent = s }

export function stats(xs) {
  if (!xs.length) return { n: 0 }
  const s = [...xs].sort((a, b) => a - b)
  const at = (q) => s[Math.min(s.length - 1, Math.floor(q * s.length))]
  const r = (v) => Math.round(v * 100) / 100
  return { n: s.length, p50: r(at(0.5)), p95: r(at(0.95)), max: r(s[s.length - 1]) }
}

const nextPaint = () => new Promise((ok) => requestAnimationFrame(() => ok(performance.now())))

async function main() {
  const cfg = await (await fetch("config.json", { cache: "no-store" })).json()
  const token = cfg.token || new URLSearchParams(location.hash.slice(1)).get("token") || ""
  const term = new Term(canvas)
  const ws = new WebSocket(`${cfg.socket}?token=${encodeURIComponent(token)}`)
  const report = (body) => fetch(cfg.report, {
    method: "POST",
    headers: { "X-Conch-Token": token, "Content-Type": "application/json" },
    body: JSON.stringify({ ...body, ua: navigator.userAgent, dpr: devicePixelRatio, cell: [term.cellW, term.cellH] }),
  })
  ws.onerror = () => say("socket failed")
  ws.onclose = () => say("socket closed")
  const box0 = () => box.getBoundingClientRect()
  const grid = () => { const b = box0(); return term.fit(b.width - 12, b.height - 12) }
  if (cfg.mode === "bench") bench(ws, term, report)
  else live(ws, term, report, grid, cfg.autotest)
}

// bench paints every frame as it arrives and times the drawing, and the
// time from arrival to the next paint the browser makes. In the flood run
// it paints only the newest frame at each animation frame, as the real
// window would, and counts how many it kept up with.
function bench(ws, term, report) {
  let run = null
  const results = []
  let latest = null, scheduled = false
  ws.onopen = () => ws.send(JSON.stringify({ t: "start" }))
  ws.onmessage = async (ev) => {
    const m = JSON.parse(ev.data)
    if (m.t === "run") {
      run = { pace: m.pace, count: m.count, draw: [], present: [], rows: [], painted: 0, t0: performance.now() }
      say(`bench: ${m.count} frames, ${m.pace ? m.pace + " ms apart" : "flood"}`)
      return
    }
    if (m.t === "frame") {
      if (term.prev === null || term.prev.cols !== m.frame.cols) term.fit(m.frame.cols * term.cellW, m.frame.rows * term.cellH)
      if (run.pace) {
        const t0 = performance.now()
        run.rows.push(term.paint(m.frame))
        const t1 = performance.now()
        run.draw.push(t1 - t0)
        run.present.push((await nextPaint()) - t0)
        run.painted++
      } else {
        latest = m.frame
        if (!scheduled) {
          scheduled = true
          requestAnimationFrame(() => {
            scheduled = false
            const t0 = performance.now()
            run.rows.push(term.paint(latest))
            run.draw.push(performance.now() - t0)
            run.painted++
          })
        }
      }
      return
    }
    if (m.t === "end") {
      await nextPaint()
      const ms = performance.now() - run.t0
      results.push({ pace: run.pace, frames: run.count, painted: run.painted, ms: Math.round(ms),
        draw: stats(run.draw), present: stats(run.present), rowsPerPaint: stats(run.rows) })
      ws.send(JSON.stringify({ t: "ack" }))
      if (results.length === 2) {
        say(`done: draw p95 ${results[0].draw.p95} ms, present p95 ${results[0].present.p95} ms`)
        await report({ mode: "bench", results })
      }
    }
  }
}

// live sizes the pane to the window, types what is typed, and with
// autotest types forty letters itself, timing each from the send to the
// paint of the first frame that shows it.
function live(ws, term, report, grid, autotest) {
  let typed = "", pending = null
  const echo = []
  const send = (text) => ws.send(JSON.stringify({ t: "text", text }))
  const resize = () => { const g = grid(); ws.send(JSON.stringify({ t: "resize", cols: g.cols, rows: g.rows })) }
  ws.onopen = () => {
    resize()
    new ResizeObserver(() => resize()).observe(box)
    canvas.focus()
    if (autotest) setTimeout(autoType, 800)
  }
  ws.onmessage = async (ev) => {
    const m = JSON.parse(ev.data)
    if (m.t === "error") { say("error: " + m.error); return }
    if (m.t !== "frame") return
    term.paint(m.frame)
    say(`live: pane ${m.frame.id} ${m.frame.cols}×${m.frame.rows}`)
    if (pending && m.frame.lines.join("\n").includes(pending.text)) {
      const p = pending
      pending = null
      echo.push((await nextPaint()) - p.t0)
      p.done()
    }
  }
  // Typing: keys from the canvas, text (and IME composition) through the
  // hidden textarea. Enough for the spike; phase 2 maps every key.
  canvas.addEventListener("click", () => ime.focus())
  canvas.addEventListener("focus", () => ime.focus())
  ime.addEventListener("keydown", (e) => {
    if (e.isComposing) return
    const keys = { Enter: "\r", Backspace: "\x7f", Tab: "\t", Escape: "\x1b" }
    if (keys[e.key]) { send(keys[e.key]); e.preventDefault() }
    else if (e.ctrlKey && e.key.length === 1) { send(String.fromCharCode(e.key.toUpperCase().charCodeAt(0) - 64)); e.preventDefault() }
  })
  ime.addEventListener("input", (e) => {
    if (e.isComposing) return
    if (ime.value) { send(ime.value); ime.value = "" }
  })
  ime.addEventListener("compositionend", () => { if (ime.value) { send(ime.value); ime.value = "" } })

  async function autoType() {
    const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN"
    for (const ch of letters) {
      typed += ch
      await new Promise((done) => {
        pending = { text: typed, t0: performance.now(), done }
        send(ch)
        setTimeout(() => { if (pending) { pending = null; done() } }, 2000)
      })
      await new Promise((ok) => setTimeout(ok, 120))
    }
    say(`done: echo p95 ${stats(echo).p95} ms`)
    await report({ mode: "live", echo: stats(echo), missed: letters.length - echo.length })
  }
}

main().catch((e) => say("failed: " + e.message))
