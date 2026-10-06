import test from "node:test"
import assert from "node:assert/strict"
import {
  parseLine, frameRows, color256, sortAgents, upsertAgent, removeAgent, groupAgents, groupByMachine, agentLabel,
  ago, route, can, backoff, codeFromHash, fontSizeFor, fitFontSize, fitPane, worthResizing, chunks, keyBytes, appPath,
} from "../ui/lib.mjs"

const text = (runs) => runs.map((r) => r.text).join("")

test("plain text is one unstyled run", () => {
  assert.deepEqual(parseLine("hello"), [
    { text: "hello", fg: null, bg: null, bold: false, dim: false, italic: false, underline: false, inverse: false },
  ])
  assert.deepEqual(parseLine(""), [])
})

test("styles start, stack and reset", () => {
  const runs = parseLine("\x1b[1m✻ Effecting…\x1b[0m (32s) \x1b[31;4mred\x1b[39m under\x1b[24m.")
  assert.equal(text(runs), "✻ Effecting… (32s) red under.")
  assert.equal(runs[0].bold, true)
  assert.equal(runs[1].bold, false)
  assert.equal(runs[2].fg, "#cd3131")
  assert.equal(runs[2].underline, true)
  assert.equal(runs[3].fg, null)
  assert.equal(runs[3].underline, true)
  assert.equal(runs[4].underline, false)
})

test("an empty SGR is a reset, and 22 ends bold and dim", () => {
  const runs = parseLine("\x1b[1;2ma\x1b[22mb\x1b[7mc\x1b[md")
  assert.deepEqual(runs.map((r) => [r.bold, r.dim, r.inverse]), [
    [true, true, false], [false, false, false], [false, false, true], [false, false, false],
  ])
})

test("bright, 256 and true colours, with semicolons or colons", () => {
  assert.equal(parseLine("\x1b[92mx")[0].fg, "#23d18b")
  assert.equal(parseLine("\x1b[103mx")[0].bg, "#f5f543")
  assert.equal(parseLine("\x1b[38;5;196mx")[0].fg, "#ff0000")
  assert.equal(parseLine("\x1b[48;5;232mx")[0].bg, "#080808")
  assert.equal(parseLine("\x1b[38;2;1;2;255mx")[0].fg, "#0102ff")
  assert.equal(parseLine("\x1b[38:2::10:20:30mx")[0].fg, "#0a141e")
  assert.equal(parseLine("\x1b[38:5:21mx")[0].fg, "#0000ff")
  // The colour's own numbers are not read as styles: 1 is not bold here.
  const run = parseLine("\x1b[38;5;1;4mx")[0]
  assert.equal(run.bold, false)
  assert.equal(run.underline, true)
})

test("colours out of range, or cut short, change nothing", () => {
  for (const bad of ["\x1b[38;5;999mx", "\x1b[38;2;300;0;0mx", "\x1b[38;5mx", "\x1b[38mx", "\x1b[38;9;1mx", "\x1b[48;2;1;2mx"]) {
    const run = parseLine(bad)[0]
    assert.equal(run.fg, null, JSON.stringify(bad))
    assert.equal(run.bg, null, JSON.stringify(bad))
    assert.equal(run.text, "x")
  }
  assert.equal(color256(-1), null)
  assert.equal(color256(256), null)
  assert.equal(color256(1.5), null)
  assert.equal(color256(16), "#000000")
  assert.equal(color256(231), "#ffffff")
  assert.equal(color256(255), "#eeeeee")
})

test("other escapes are dropped, never shown", () => {
  assert.equal(text(parseLine("a\x1b[2Kb\x1b[10;5Hc\x1b[?25ld")), "abcd")
  assert.equal(text(parseLine("a\x1b]0;title\x07b\x1b]8;;http://x\x1b\\c")), "abc")
  assert.equal(text(parseLine("a\x1b7b\x1b=c")), "abc")
  // A sequence cut off at the end of the row.
  assert.equal(text(parseLine("a\x1b[")), "a[")
  assert.equal(text(parseLine("a\x07b\tc")), "ab\tc")
  // A private sequence that ends in m is not a style.
  assert.equal(parseLine("\x1b[>4;2mx")[0].underline, false)
  assert.equal(parseLine("\x1b[31m").length, 0)
})

test("markup in a row stays text", () => {
  const runs = parseLine("<img src=x onerror=alert(1)>\x1b[1m&amp;")
  assert.equal(runs[0].text, "<img src=x onerror=alert(1)>")
  assert.equal(runs[1].text, "&amp;")
})

const agent = (pane, state, since, project) => ({ pane, state, since, project })

test("waiting first, longest wait first", () => {
  const list = [
    agent("p1", "idle", "2026-09-30T09:00:00Z"),
    agent("p2", "waiting", "2026-09-30T09:10:00Z"),
    agent("p3", "working", "2026-09-30T09:05:00Z"),
    agent("p4", "waiting", "2026-09-30T09:02:00Z"),
    agent("p5", "done", "2026-09-30T09:20:00Z"),
    agent("p6", "strange", "2026-09-30T08:00:00Z"),
  ]
  const before = JSON.stringify(list)
  assert.deepEqual(sortAgents(list).map((a) => a.pane), ["p4", "p2", "p5", "p3", "p1", "p6"])
  assert.equal(JSON.stringify(list), before) // the list given is left alone
  assert.deepEqual(sortAgents([]), [])
})

test("an update replaces its agent and moves it; gone removes it", () => {
  let list = sortAgents([agent("p1", "working", "2026-09-30T09:00:00Z"), agent("p2", "idle", "2026-09-30T09:00:00Z")])
  list = upsertAgent(list, agent("p2", "waiting", "2026-09-30T09:30:00Z"))
  assert.deepEqual(list.map((a) => [a.pane, a.state]), [["p2", "waiting"], ["p1", "working"]])
  list = upsertAgent(list, agent("p3", "idle", "2026-09-30T09:31:00Z"))
  assert.equal(list.length, 3)
  list = removeAgent(list, "p2")
  assert.deepEqual(list.map((a) => a.pane), ["p1", "p3"])
  assert.equal(removeAgent(list, "p404").length, 2)
  assert.deepEqual(upsertAgent([], agent("p1", "idle", "x")).map((a) => a.pane), ["p1"])
})

test("groups follow their first agent", () => {
  const api = { id: "r1", name: "api" }
  const web = { id: "r2", name: "" }
  const groups = groupAgents(sortAgents([
    agent("p1", "idle", "1", api), agent("p2", "waiting", "2", web), agent("p3", "working", "3"), agent("p4", "done", "4", api),
  ]))
  assert.deepEqual(groups.map((g) => [g.name, g.agents.map((a) => a.pane)]), [
    ["r2", ["p2"]], ["api", ["p4", "p1"]], ["No project", ["p3"]],
  ])
  assert.deepEqual(groupAgents([]), [])
})

test("labels and ages", () => {
  assert.equal(agentLabel({ title: "Reviewing", name: "reviewer", pane: "p3" }), "Reviewing")
  assert.equal(agentLabel({ name: "reviewer", pane: "p3" }), "reviewer")
  assert.equal(agentLabel({ pane: "p3" }), "p3")
  const now = Date.parse("2026-09-30T12:00:00Z")
  assert.equal(ago("2026-09-30T11:59:30Z", now), "now")
  assert.equal(ago("2026-09-30T11:59:00Z", now), "1m")
  assert.equal(ago("2026-09-30T11:00:01Z", now), "59m")
  assert.equal(ago("2026-09-30T11:00:00Z", now), "1h")
  assert.equal(ago("2026-09-29T12:00:00Z", now), "1d")
  assert.equal(ago("2026-09-30T12:05:00Z", now), "now") // a clock ahead of ours
  assert.equal(ago("nonsense", now), "")
  assert.equal(ago(undefined, now), "")
})

test("routes", () => {
  assert.deepEqual(route("/"), { view: "list" })
  assert.deepEqual(route(""), { view: "list" })
  assert.deepEqual(route("/agent/p3"), { view: "agent", pane: "p3" })
  assert.deepEqual(route("/agent/p12/"), { view: "agent", pane: "p12" })
  assert.deepEqual(route("/new"), { view: "new" })
  assert.deepEqual(route("/agent/p3/terminal"), { view: "terminal", pane: "p3" })
  assert.deepEqual(route("/settings"), { view: "settings" })
  assert.deepEqual(route("/agent/p3/terminal/"), { view: "terminal", pane: "p3" })
  for (const other of ["/agent/", "/agent/reviewer", "/agent/p3/x", "/agent/p", "/newer", "/api/agents", "/agent/p3?x", "/agent/terminal", "/agent/p3/terminals", "/agent/x/terminal"]) {
    assert.deepEqual(route(other), { view: "list" }, other)
  }
})

test("permissions", () => {
  assert.equal(can("view", "view"), true)
  assert.equal(can("view", "reply"), false)
  assert.equal(can("reply", "reply"), true)
  assert.equal(can("reply", "full"), false)
  assert.equal(can("full", "reply"), true)
  assert.equal(can("", "view"), false)
  assert.equal(can(undefined, "view"), false)
  assert.equal(can("full", "admin"), false)
})

test("reconnecting backs off to half a minute", () => {
  assert.equal(backoff(0), 500)
  assert.equal(backoff(1), 1000)
  assert.equal(backoff(5), 16000)
  assert.equal(backoff(6), 30000)
  assert.equal(backoff(1000), 30000)
  assert.equal(backoff(-3), 500)
})

test("a pairing code in the link", () => {
  assert.equal(codeFromHash("#code=438-219"), "438-219")
  assert.equal(codeFromHash("#code=438219"), "438219")
  for (const bad of ["", undefined, "#", "#code=", "#code=abc-def", "#code=438-219<script>", "#other=438-219", "#code=1"]) {
    assert.equal(codeFromHash(bad), "", String(bad))
  }
})

test("a frame's text size", () => {
  assert.equal(fontSizeFor(40, 390), 14) // narrow frame: as big as is comfortable
  assert.equal(fontSizeFor(80, 390), 8.1)
  assert.equal(fontSizeFor(200, 390), 7) // too wide to fit: smallest, and it scrolls
  assert.equal(fontSizeFor(0, 390), 14)
  assert.equal(fontSizeFor(80, 0), 14)
  assert.equal(fontSizeFor(undefined, undefined), 14)
  // The rows have to fit the height as well, when it is given.
  assert.equal(fontSizeFor(80, 390, 7, 14, 40, 200), 7) // 40 rows in 200px: smallest
  assert.equal(fontSizeFor(40, 900, 7, 14, 10, 1000), 14) // room either way: the ceiling
})

test("a frame fills a window wider than a phone", () => {
  // A phone keeps the comfortable ceiling.
  assert.equal(fitFontSize(80, 390, 40, 800), 8.1)
  assert.equal(fitFontSize(40, 390, 40, 800), 14)
  // A desktop window: the text grows rather than leaving half of it empty,
  // which is what a 14px ceiling did — 120 columns took 1000px of 1900.
  assert.ok(fitFontSize(120, 1900, 40, 1200) > 14, "it stayed at the phone's ceiling")
  assert.equal(fitFontSize(120, 1900, 40, 1200), 22)
  // Unless the rows would not fit: then the height decides.
  assert.equal(fitFontSize(120, 1900, 40, 600), 12)
  // And never beyond the ceiling, however big the window.
  assert.equal(fitFontSize(40, 4000, 10, 4000), 22)
})

test("a frame loses the empty rows under its last line, and keeps the cursor's", () => {
  const rows = frameRows(["one", "", "\x1b[1mtwo\x1b[0m", "   ", "", "\x1b[0m"])
  assert.deepEqual(rows.map(text), ["one", "", "two"])
  assert.deepEqual(frameRows(["a", "\x1b[7m \x1b[27m", ""]).map(text), ["a", " "])
  assert.deepEqual(frameRows(["a", "\x1b[44m  \x1b[0m", ""]).length, 2)
  // An empty screen is still one row tall.
  assert.equal(frameRows(["", "", ""]).length, 1)
  assert.deepEqual(frameRows([]), [])
  assert.deepEqual(frameRows(undefined), [])
})


test("keys go in messages of 64 at most", () => {
  assert.deepEqual(chunks([]), [])
  assert.deepEqual(chunks(["a"]), [["a"]])
  const keys = Array.from({ length: 129 }, (_, i) => String(i % 10))
  const parts = chunks(keys)
  assert.deepEqual(parts.map((p) => p.length), [64, 64, 1])
  assert.deepEqual(parts.flat(), keys)
  assert.equal(chunks(Array(64).fill("x")).length, 1)
})


test("a push key as bytes", () => {
  // The RFC 8291 example's receiver key: 65 bytes, uncompressed (0x04).
  const k = keyBytes("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
  assert.equal(k.length, 65)
  assert.equal(k[0], 4)
  assert.deepEqual([...keyBytes("_-8")], [0xff, 0xef])
  assert.deepEqual([...keyBytes("")], [])
})

test("a tapped notification goes only to the app's own views", () => {
  for (const ok of ["/", "/agent/p3", "/agent/p3/terminal", "/new", "/settings"]) assert.equal(appPath(ok), ok)
  for (const bad of ["https://evil.example/", "//evil.example", "/agent/x", "javascript:alert(1)", "", undefined, null, 3, {}]) {
    assert.equal(appPath(bad), "/", String(bad))
  }
})

test("the tail of a frame", async () => {
  const { tailRows } = await import("../ui/lib.mjs")
  const rows = frameRows(["a", "b", "c", "", ""])
  assert.deepEqual(tailRows(rows, 2).map(text), ["b", "c"])
  assert.deepEqual(tailRows(rows, 10).map(text), ["a", "b", "c"])
  assert.deepEqual(tailRows(rows, 0), [])
  assert.deepEqual(tailRows([], 5), [])
})

test("every pane in order: agents, then terminals", async () => {
  const { sortPanes, upsertPane } = await import("../ui/lib.mjs")
  const t = (pane, since) => ({ pane, kind: "terminal", state: "idle", since })
  const a = (pane, state, since) => ({ pane, kind: "agent", state, since })
  const list = sortPanes([t("p1", "2"), a("p2", "idle", "1"), t("p3", "1"), a("p4", "waiting", "3")])
  assert.deepEqual(list.map((p) => p.pane), ["p4", "p2", "p3", "p1"])
  assert.deepEqual(upsertPane(list, a("p1", "working", "4")).map((p) => p.pane), ["p4", "p1", "p2", "p3"])
  assert.deepEqual(sortPanes([]), [])
})

test("hardware keys as key names", async () => {
  const { keyFromEvent } = await import("../ui/lib.mjs")
  const k = (key, extra = {}, mods) => keyFromEvent({ key, ...extra }, mods)
  assert.equal(k("Enter"), "enter")
  assert.equal(k("Backspace"), "backspace")
  assert.equal(k("ArrowUp"), "up")
  assert.equal(k("Escape"), "esc")
  assert.equal(k("Tab", { shiftKey: true }), "shift+tab")
  assert.equal(k("c", { ctrlKey: true }), "ctrl+c")
  assert.equal(k("C", { ctrlKey: true, shiftKey: true }), "ctrl+c")
  assert.equal(k("x", { altKey: true }), "alt+x")
  assert.equal(k(" ", { ctrlKey: true }), "ctrl+space")
  assert.equal(k("ArrowLeft", { ctrlKey: true }), "ctrl+left")
  // Sticky modifiers from the key bar count as held.
  assert.equal(k("d", {}, { ctrl: true }), "ctrl+d")
  assert.equal(k("Enter", {}, { alt: true }), "alt+enter")
  // Text is the input event's; the browser keeps cmd; IME is mid-word.
  for (const ev of [{ key: "a" }, { key: "é" }, { key: "A", shiftKey: true }, { key: "c", metaKey: true }, { key: "Enter", isComposing: true },
    { key: "Shift", shiftKey: true }, { key: "Control", ctrlKey: true }, { key: "Dead", altKey: true }]) {
    assert.equal(keyFromEvent(ev), null, JSON.stringify(ev))
  }
  assert.equal(keyFromEvent(null), null)
})

test("modifiers in front of a key", async () => {
  const { withMods } = await import("../ui/lib.mjs")
  assert.equal(withMods("x", { ctrl: true }), "ctrl+x")
  assert.equal(withMods("x", { ctrl: true, alt: true }), "ctrl+alt+x")
  assert.equal(withMods("up", {}), "up")
  assert.equal(withMods("ctrl+c", { ctrl: true }), "ctrl+c")
  assert.equal(withMods("+", { ctrl: true }), "ctrl++")
})

test("a sticky modifier: once, locked, off", async () => {
  const { tapModifier, usedModifier } = await import("../ui/lib.mjs")
  let m = tapModifier(undefined, 1000)
  assert.equal(m.state, "once")
  assert.equal(usedModifier(m).state, "off") // spent by the next key
  m = tapModifier(m, 1200)
  assert.equal(m.state, "locked") // a second tap soon after
  assert.equal(usedModifier(m).state, "locked")
  assert.equal(tapModifier(m, 5000).state, "off")
  // A second tap long after is a change of mind, not a lock.
  assert.equal(tapModifier(tapModifier(undefined, 0), 1000).state, "off")
  assert.equal(usedModifier(undefined), undefined)
})

test("the terminal's key bar", async () => {
  const { TERMINAL_KEYS } = await import("../ui/lib.mjs")
  const keys = TERMINAL_KEYS.filter((k) => k.key).map((k) => k.key)
  for (const k of ["esc", "tab", "up", "down", "left", "right", "ctrl+c"]) assert.ok(keys.includes(k), k)
  assert.deepEqual(TERMINAL_KEYS.filter((k) => k.mod).map((k) => k.mod), ["ctrl", "alt"])
  assert.deepEqual(TERMINAL_KEYS.filter((k) => k.confirm).map((k) => k.key), ["ctrl+c"])
  assert.equal(new Set(TERMINAL_KEYS.map((k) => k.label)).size, TERMINAL_KEYS.length)
})

test("children nested any deep, with nothing in them", async () => {
  const { kids } = await import("../ui/lib.mjs")
  assert.deepEqual(kids(["a", null, ["b", [false, ["c", undefined]], []], 0, ""]), ["a", "b", "c", 0, ""])
  assert.deepEqual(kids([]), [])
  assert.deepEqual(kids([[[null]]]), [])
})

test("scrollback: the live screen, lines scrolling off it, and older pages", async () => {
  const { scrollback, olderOffset } = await import("../ui/lib.mjs")
  const frame = (history, offset, first, n = 4) => ({ history, offset, lines: Array.from({ length: n }, (_, i) => `L${first + i}`) })
  // The live screen: lines 100..103 of a pane with 100 lines of history.
  let { state, out } = scrollback(null, frame(100, 0, 100))
  assert.deepEqual(out, { live: ["L100", "L101", "L102", "L103"], reset: true })
  assert.equal(state.top, 100)
  assert.equal(olderOffset(state), 4) // one screen up
  // Two lines scroll off: they join the history shown.
  ;({ state, out } = scrollback(state, frame(102, 0, 102)))
  assert.deepEqual(out.append, ["L100", "L101"])
  assert.deepEqual(out.live, ["L102", "L103", "L104", "L105"])
  assert.equal(state.top, 100)
  // The same frame again: nothing moves.
  ;({ state, out } = scrollback(state, frame(102, 0, 102)))
  assert.deepEqual(out.append, [])
  // The page above, asked for with olderOffset: lines 96..99.
  assert.equal(olderOffset(state), 6)
  ;({ state, out } = scrollback(state, frame(102, 6, 96)))
  assert.deepEqual(out, { prepend: ["L96", "L97", "L98", "L99"] })
  assert.equal(state.top, 96)
  // A page that overlaps what is shown gives only what is new; one that
  // is wholly shown gives nothing.
  ;({ state, out } = scrollback(state, frame(102, 8, 94)))
  assert.deepEqual(out.prepend, ["L94", "L95"])
  ;({ state, out } = scrollback(state, frame(102, 8, 94)))
  assert.deepEqual(out, {})
  // Near the oldest line the server stops at it.
  ;({ state, out } = scrollback({ history: 3, top: 3, live: ["a", "b", "c", "d"] }, { history: 3, offset: 3, lines: ["L0", "L1", "L2", "a"] }))
  assert.deepEqual(out.prepend, ["L0", "L1", "L2"])
  assert.equal(state.top, 0)
  assert.equal(olderOffset(state), 0) // nothing older
})

test("scrollback starts over when it no longer joins up", async () => {
  const { scrollback, olderOffset } = await import("../ui/lib.mjs")
  const live = (history, lines) => ({ history, offset: 0, lines })
  let { state } = scrollback(null, live(50, ["a", "b", "c"]))
  let out
  // More went by than the screen held.
  ;({ state, out } = scrollback(state, live(60, ["x", "y", "z"])))
  assert.equal(out.reset, true)
  assert.equal(state.top, 60)
  // The history was cleared (a full-screen program left).
  ;({ state, out } = scrollback(state, live(0, ["p"])))
  assert.equal(out.reset, true)
  assert.equal(state.top, 0)
  // A page before any live frame, and frames with nothing in them.
  assert.deepEqual(scrollback(null, { history: 9, offset: 3, lines: ["q"] }), { state: null, out: {} })
  ;({ state, out } = scrollback(null, {}))
  assert.deepEqual(out.live, [])
  assert.equal(olderOffset(null), 0)
  assert.equal(olderOffset({ history: 5, top: 5, live: [] }), 1)
})

test("a swipe as wheel steps", async () => {
  const { wheelSteps } = await import("../ui/lib.mjs")
  assert.deepEqual(wheelSteps(0, 30, 26), { acc: 4, steps: 1 })
  assert.deepEqual(wheelSteps(4, 22, 26), { acc: 0, steps: 1 }) // what was left over counts
  assert.deepEqual(wheelSteps(0, -60, 26), { acc: -8, steps: -2 })
  assert.deepEqual(wheelSteps(0, 10, 26), { acc: 10, steps: 0 })
  // Turning back cancels what had built up rather than stepping both ways.
  assert.deepEqual(wheelSteps(20, -30, 26), { acc: -10, steps: 0 })
  assert.deepEqual(wheelSteps(undefined, NaN, 26), { acc: 0, steps: 0 })
})

test("what a keystroke did to the terminal's hidden field", async () => {
  const { ttyInput } = await import("../ui/lib.mjs")
  const S = "​"
  assert.deepEqual(ttyInput(S + "a", S), { backspace: false, text: "a" })
  // iOS: the cursor was ahead of the sentinel, so the letter landed in front.
  assert.deepEqual(ttyInput("a" + S, S), { backspace: false, text: "a" })
  assert.deepEqual(ttyInput("he" + S + "llo", S), { backspace: false, text: "hello" })
  assert.deepEqual(ttyInput(S, S), { backspace: false, text: "" })
  assert.deepEqual(ttyInput(S + "\n", S), { backspace: false, text: "\n" })
  // The sentinel deleted: a backspace, and anything typed in its place.
  assert.deepEqual(ttyInput("", S), { backspace: true, text: "" })
  assert.deepEqual(ttyInput("x", S), { backspace: true, text: "x" })
  assert.deepEqual(ttyInput(undefined, S), { backspace: true, text: "" })
})

test("the pane a window could show, and when it is worth asking", () => {
  // A desktop window: more columns than any pane the laptop would give it.
  assert.deepEqual(fitPane(1884, 860), { cols: 241, rows: 52 })
  // A phone: a pane it could show, which is small.
  assert.deepEqual(fitPane(374, 700), { cols: 47, rows: 43 })
  // Nothing to go on, and a window too small for a pane at all.
  assert.equal(fitPane(0, 0), null)
  assert.equal(fitPane(100, 60), null) // under the gateway's smallest
  // Counted at the size the rows are drawn at, not a size conch prefers:
  // somebody who chose 12px on a 34-inch monitor wants the columns that
  // fit at 12px.
  assert.deepEqual(fitPane(1974, 700, undefined, undefined, undefined, undefined, 12), { cols: 274, rows: 46 })
  assert.deepEqual(fitPane(1974, 700, undefined, undefined, undefined, undefined, 8), { cols: 411, rows: 70 })
  assert.equal(fitPane(1974, 700, undefined, undefined, undefined, undefined, 0), null)

  // Never past the gateway's bounds, however big the window.
  const huge = fitPane(100000, 100000)
  assert.equal(huge.cols, 500)
  assert.equal(huge.rows, 200)

  // Worth asking: the window has room the pane's columns cannot fill.
  assert.equal(worthResizing({ cols: 120, rows: 40 }, fitPane(1884, 860)), true)
  // Not worth asking: the pane is already as wide, or wider — a phone must
  // never shrink the pane the laptop is working in.
  assert.equal(worthResizing({ cols: 268, rows: 50 }, fitPane(1884, 860)), false)
  assert.equal(worthResizing({ cols: 241, rows: 52 }, fitPane(1884, 860)), false)
  assert.equal(worthResizing({ cols: 300, rows: 80 }, fitPane(374, 700)), false)
  // A column or two is not worth a resize.
  assert.equal(worthResizing({ cols: 238, rows: 50 }, { cols: 241, rows: 52 }), false)
  assert.equal(worthResizing({ cols: 230, rows: 50 }, { cols: 241, rows: 52 }), true)
  // Nothing to compare.
  assert.equal(worthResizing(null, { cols: 100, rows: 40 }), false)
  assert.equal(worthResizing({ cols: 100, rows: 40 }, null), false)
})

// The drawer lists machines as the tree does: this computer, then the
// others, each with its own projects under it. A phone that reaches one
// machine — the usual case — is given no machine headings at all, and the
// machine that matters most to show is the one with nothing running: a
// list built from panes alone cannot show it, and its absence reads as
// the phone having lost it.
test("the drawer groups by machine", () => {
  const pane = (id, machine, project) => ({
    pane: id, machine, state: "idle", name: "claude", kind: "agent", since: "2026-10-06T00:00:00Z",
    ...(project ? { project } : {}),
  })
  const api = { id: "r1", name: "api" }

  // One machine: no sections, the projects as they always were.
  const one = groupByMachine([pane("local:p1", "local", api)], [{ id: "local", label: "this computer", state: "online" }])
  assert.deepEqual(one.machines, [])
  assert.deepEqual(one.groups.map((g) => g.name), ["api"])
  // And with no machine list at all, as an app that has not heard yet.
  assert.deepEqual(groupByMachine([pane("p1", "local")], []).groups.map((g) => g.name), ["No project"])

  // Two machines: a section each, in the order given (this computer
  // first, as the gateway sends them), with the projects inside.
  const machines = [
    { id: "local", label: "this computer", state: "online" },
    { id: "busybox", label: "busybox", state: "online" },
    { id: "gpu-1", label: "gpu-1", state: "offline", detail: "ssh: connect to host gpu-1 port 22: no route to host" },
  ]
  const got = groupByMachine([
    pane("local:p1", "local", api), pane("busybox:p1", "busybox"), pane("busybox:p2", "busybox", api),
  ], machines)
  assert.deepEqual(got.groups, [])
  assert.deepEqual(got.machines.map((m) => [m.id, m.label, m.state, m.groups.map((g) => [g.name, g.agents.map((p) => p.pane)])]), [
    ["local", "this computer", "online", [["api", ["local:p1"]]]],
    ["busybox", "busybox", "online", [["No project", ["busybox:p1"]], ["api", ["busybox:p2"]]]],
    // The machine with nothing on it is a section all the same, and
    // carries why it is not answering.
    ["gpu-1", "gpu-1", "offline", []],
  ])
  assert.equal(got.machines[2].detail.startsWith("ssh: connect"), true)

  // A machine with nothing running is still listed: this is what the
  // whole grouping is for.
  const empty = groupByMachine([], machines)
  assert.deepEqual(empty.machines.map((m) => m.id), ["local", "busybox", "gpu-1"])
  assert.deepEqual(empty.machines.map((m) => m.groups.length), [0, 0, 0])

  // A pane on a machine the list does not have is still the person's
  // pane: it gets a section rather than being dropped.
  const stray = groupByMachine([pane("vm9:p1", "vm9")], machines)
  assert.deepEqual(stray.machines.map((m) => m.id), ["local", "busybox", "gpu-1", "vm9"])
  assert.deepEqual(stray.machines[3].groups.map((g) => g.agents.map((p) => p.pane)), [["vm9:p1"]])
  // A pane with no machine at all belongs to this computer, as every
  // other part of the app reads it.
  const bare = groupByMachine([{ pane: "p1", state: "idle", kind: "agent" }], machines)
  assert.deepEqual(bare.machines[0].groups.map((g) => g.agents.map((p) => p.pane)), [["p1"]])
})
