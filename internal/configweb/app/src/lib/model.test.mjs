import assert from "node:assert/strict"
import test from "node:test"
import * as m from "./model.js"

test("tokenFrom reads t= from the fragment and nothing else", () => {
  assert.equal(m.tokenFrom("#t=abc123"), "abc123")
  assert.equal(m.tokenFrom("#x=1&t=ff"), "ff")
  assert.equal(m.tokenFrom(""), "")
  assert.equal(m.tokenFrom(undefined), "")
  assert.equal(m.tokenFrom("#t="), "")
  assert.equal(m.tokenFrom("#t=<script>"), "")
  assert.equal(m.tokenFrom("#at=ff"), "")
})

test("setValue copies, and never changes what it was given", () => {
  const cfg = { ui: { theme: "conch", mouse: true } }
  const next = m.setValue(cfg, "ui", "theme", "nord")
  assert.equal(next.ui.theme, "nord")
  assert.equal(cfg.ui.theme, "conch")
  assert.equal(m.getValue(next, "ui", "mouse"), true)
})

test("setValue with undefined removes a key, so it goes back to its default", () => {
  const next = m.setValue({ ui: { a: 1, b: 2 } }, "ui", "a", undefined)
  assert.deepEqual(next, { ui: { b: 2 } })
})

test("setValue makes a table that was missing, and works at the top level", () => {
  assert.deepEqual(m.setValue({}, "web", "port", 9000), { web: { port: 9000 } })
  assert.deepEqual(m.setValue({}, "", "actions", [{ name: "x" }]), { actions: [{ name: "x" }] })
  assert.deepEqual(m.setValue({ actions: [] }, "", "actions", undefined), {})
  assert.equal(m.getValue({}, "web", "port"), undefined)
  assert.equal(m.getValue(null, "", "x"), undefined)
})

test("setValue with an empty key replaces a keyed table whole", () => {
  const next = m.setValue({ sandbox: { daytona: {} } }, "sandbox", "", { e2b: { target: "us" } })
  assert.deepEqual(next.sandbox, { e2b: { target: "us" } })
  assert.deepEqual(m.setValue({ sandbox: { a: {} } }, "sandbox", "", undefined).sandbox, {})
})

test("same ignores key order and undefined keys", () => {
  assert.ok(m.same({ a: 1, b: [1, { c: 2 }] }, { b: [1, { c: 2 }], a: 1 }))
  assert.ok(m.same({ a: 1, b: undefined }, { a: 1 }))
  assert.ok(!m.same([1, 2], [2, 1]))
  assert.ok(m.same(undefined, null))
})

test("changedSections names only the sections that differ", () => {
  const schema = [
    { id: "ui", key: "ui", fields: [] },
    { id: "actions", key: "", fields: [{ key: "actions" }] },
    { id: "perm", key: "", fields: [{ key: "permissions" }] },
  ]
  const saved = { ui: { theme: "conch" }, actions: [] }
  assert.deepEqual(m.changedSections(schema, saved, saved), [])
  assert.deepEqual(m.changedSections(schema, saved, { ...saved, ui: { theme: "nord" } }), ["ui"])
  assert.deepEqual(m.changedSections(schema, saved, { ...saved, actions: [{ name: "x" }] }), ["actions"])
  assert.deepEqual(m.changedSections(undefined, saved, saved), [])
})

test("parseList splits on commas and lines and drops blanks", () => {
  assert.deepEqual(m.parseList("A, B\n\nC ,"), ["A", "B", "C"])
  assert.deepEqual(m.parseList(""), [])
  assert.deepEqual(m.parseList(undefined), [])
})

test("parseInts says what isn't a number rather than dropping it", () => {
  assert.deepEqual(m.parseInts("80, 95"), { value: [80, 95] })
  assert.deepEqual(m.parseInts(""), { value: [] })
  assert.match(m.parseInts("80, x, 9.5").error, /x, 9.5/)
})

test("parseNumber: empty is unset, and limits are inclusive", () => {
  assert.deepEqual(m.parseNumber(""), { value: undefined })
  assert.deepEqual(m.parseNumber(" 42 ", { integer: true }), { value: 42 })
  assert.deepEqual(m.parseNumber("0", { integer: true, min: 0 }), { value: 0 })
  assert.deepEqual(m.parseNumber("65535", { integer: true, max: 65535 }), { value: 65535 })
  assert.match(m.parseNumber("65536", { max: 65535 }).error, /at most/)
  assert.match(m.parseNumber("-1", { min: 0 }).error, /at least/)
  assert.match(m.parseNumber("1.5", { integer: true }).error, /whole/)
  assert.deepEqual(m.parseNumber("0.25"), { value: 0.25 })
  assert.match(m.parseNumber("abc").error, /isn't a number/)
  assert.match(m.parseNumber("Infinity").error, /isn't a number/)
})

test("validClock takes HH:MM in range, and empty", () => {
  for (const ok of ["", "0:00", "22:00", "23:59", "08:30"]) assert.ok(m.validClock(ok), ok)
  for (const bad of ["24:00", "12:60", "noon", "12", "12:5"]) assert.ok(!m.validClock(bad), bad)
})

test("freshName does not take a name already there", () => {
  assert.equal(m.freshName({}, "new"), "new")
  assert.equal(m.freshName(undefined, "new"), "new")
  assert.equal(m.freshName({ new: {}, "new-2": {} }, "new"), "new-3")
})

test("renameKey keeps the order, and refuses a clash or an empty name", () => {
  const obj = { a: 1, b: 2, c: 3 }
  assert.deepEqual(Object.keys(m.renameKey(obj, "b", "z")), ["a", "z", "c"])
  assert.equal(m.renameKey(obj, "b", "c"), obj)
  assert.equal(m.renameKey(obj, "b", ""), obj)
  assert.equal(m.renameKey(obj, "b", "b"), obj)
})

test("move clamps to the ends and leaves out-of-range alone", () => {
  assert.deepEqual(m.move([1, 2, 3], 0, 1), [2, 1, 3])
  assert.deepEqual(m.move([1, 2, 3], 2, 1), [1, 2, 3])
  assert.deepEqual(m.move([1, 2, 3], 0, -1), [1, 2, 3])
  assert.deepEqual(m.move([1], 0, 1), [1])
  assert.deepEqual(m.move([], 0, 1), [])
  assert.deepEqual(m.move([1, 2], 5, -1), [1, 2])
})

test("searchSections matches titles, labels, keys, help and nested fields", () => {
  const schema = [
    { id: "ui", key: "ui", title: "Appearance", fields: [{ key: "theme", label: "Theme" }] },
    { id: "actions", key: "", title: "Actions", fields: [{ key: "actions", label: "Actions", fields: [{ key: "run", label: "Command" }] }] },
  ]
  assert.deepEqual(m.searchSections(schema, ""), ["ui", "actions"])
  assert.deepEqual(m.searchSections(schema, "THEME"), ["ui"])
  assert.deepEqual(m.searchSections(schema, "command"), ["actions"])
  assert.deepEqual(m.searchSections(schema, "zzz"), [])
  assert.deepEqual(m.searchSections(undefined, "x"), [])
})

test("resolveTheme: a choice wins, system follows the system", () => {
  assert.equal(m.resolveTheme("light", true), "light")
  assert.equal(m.resolveTheme("dark", false), "dark")
  assert.equal(m.resolveTheme("system", true), "dark")
  assert.equal(m.resolveTheme("system", false), "light")
  assert.equal(m.resolveTheme(undefined, true), "dark")
  assert.equal(m.resolveTheme("purple", false), "light")
})

test("themePref reads back only the three choices", () => {
  assert.equal(m.themePref("dark"), "dark")
  assert.equal(m.themePref("light"), "light")
  for (const v of [null, undefined, "", "system", "DARK", "x"]) assert.equal(m.themePref(v), "system")
})
