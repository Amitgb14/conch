// node --test ui/term.test.mjs — run by TestUILogic.
import { test } from "node:test"
import assert from "node:assert/strict"
import { charWidth, layoutRow, gridFor, changedRows } from "./term.mjs"
import { parseLine } from "./ansi.mjs"

test("charWidth", () => {
  assert.equal(charWidth("a".codePointAt(0)), 1)
  assert.equal(charWidth("é".codePointAt(0)), 1)
  assert.equal(charWidth("─".codePointAt(0)), 1)
  assert.equal(charWidth("変".codePointAt(0)), 2)
  assert.equal(charWidth("🚀".codePointAt(0)), 2)
  assert.equal(charWidth("✅".codePointAt(0)), 2)
  assert.equal(charWidth(0x0301), 0) // combining acute
  assert.equal(charWidth(0x200d), 0)
  assert.equal(charWidth(0x1100), 2) // first of the first range
  assert.equal(charWidth(0x10ff), 1) // just before it
  assert.equal(charWidth(0x3fffd), 2) // last of the last
})

test("layoutRow keeps ASCII in runs and places the rest by column", () => {
  const pieces = layoutRow(parseLine("ab変c\x1b[31m🚀d"))
  assert.deepEqual(pieces.map((p) => [p.col, p.cells, p.text]), [
    [0, 2, "ab"], [2, 2, "変"], [4, 1, "c"], [5, 2, "🚀"], [7, 1, "d"],
  ])
  assert.equal(pieces[3].style.fg, "#cd3131")
})

test("layoutRow joins a combining mark to the character before it", () => {
  // After ASCII: the mark joins that run.
  assert.deepEqual(layoutRow(parseLine("éx")).map((p) => [p.col, p.text]), [[0, "é"], [1, "x"]])
  // After a wide character: it joins that one.
  assert.deepEqual(layoutRow(parseLine("変́x")).map((p) => [p.col, p.text]), [[0, "変́"], [2, "x"]])
  // At the start of a row there is nothing to join; it takes no cells.
  assert.deepEqual(layoutRow(parseLine("́x")).map((p) => [p.col, p.text]), [[0, "́"], [0, "x"]])
})

test("layoutRow of an empty row, and of a row that is only escapes", () => {
  assert.deepEqual(layoutRow(parseLine("")), [])
  assert.deepEqual(layoutRow(parseLine("\x1b[0m\x1b[31m")), [])
})

test("gridFor", () => {
  assert.deepEqual(gridFor(800, 600, 8, 16), { cols: 100, rows: 37 })
  assert.deepEqual(gridFor(1, 1, 8, 16), { cols: 1, rows: 1 })
  assert.deepEqual(gridFor(0, 0, 8, 16), { cols: 1, rows: 1 })
  assert.deepEqual(gridFor(800, 600, 0, 16), { cols: 1, rows: 1 })
  assert.deepEqual(gridFor(800, 600, NaN, 16), { cols: 1, rows: 1 })
})

test("changedRows", () => {
  const a = { cols: 3, lines: ["a", "b", "c"] }
  assert.deepEqual(changedRows(null, a), [0, 1, 2])
  assert.deepEqual(changedRows(a, { cols: 3, lines: ["a", "x", "c"] }), [1])
  assert.deepEqual(changedRows(a, a), [])
  assert.deepEqual(changedRows(a, { cols: 4, lines: ["a", "b", "c"] }), [0, 1, 2])
  assert.deepEqual(changedRows(a, { cols: 3, lines: ["a", "b"] }), [0, 1])
})
