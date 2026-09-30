import test from "node:test"
import assert from "node:assert/strict"
import {
  parseLine, frameRows, color256, sortAgents, upsertAgent, removeAgent, groupAgents, agentLabel,
  ago, route, can, backoff, codeFromHash, fontSizeFor, KEYBAR, textKeys, chunks, keyBytes, appPath,
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

test("text as keys", () => {
  assert.deepEqual(textKeys("git st"), ["g", "i", "t", "space", "s", "t"])
  assert.deepEqual(textKeys("a\tb\nc\r\nd\re"), ["a", "tab", "b", "enter", "c", "enter", "d", "enter", "e"])
  assert.deepEqual(textKeys("é+ñ😀"), ["é", "+", "ñ", "😀"])
  // Control characters are not typed: a paste can't smuggle an escape in.
  assert.deepEqual(textKeys("a\x1b[2Jb\x03\x7f"), ["a", "[", "2", "J", "b"])
  assert.deepEqual(textKeys(""), [])
  assert.deepEqual(textKeys(undefined), [])
  assert.deepEqual(textKeys(null), [])
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

test("the key bar", () => {
  const keys = KEYBAR.map((k) => k.key)
  assert.equal(new Set(keys).size, keys.length)
  for (const k of ["esc", "tab", "up", "down", "left", "right", "enter", "ctrl+c"]) assert.ok(keys.includes(k), k)
  assert.deepEqual(KEYBAR.filter((k) => k.confirm).map((k) => k.key), ["ctrl+c"])
  for (const k of KEYBAR) assert.ok(k.label && k.label.length <= 4, k.label)
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
