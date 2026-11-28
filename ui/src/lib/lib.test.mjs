// conch-ui's logic, apart from React: run with `node --test` (pnpm test).
import assert from "node:assert/strict"
import { test } from "node:test"
import { agentCounts, filterAgents, language, percent, splitRows, tokens } from "./lib.mjs"

const l = (kind, text, old, nw) => ({ kind, text, old, new: nw })

test("split rows pair removed with added in order, context on both sides", () => {
  const rows = splitRows([
    l("context", "a", 1, 1),
    l("removed", "b", 2),
    l("removed", "c", 3),
    l("added", "B", undefined, 2),
    l("context", "d", 4, 3),
    l("added", "e", undefined, 4),
  ])
  assert.deepEqual(rows.map((r) => [r.left?.text ?? null, r.right?.text ?? null]), [
    ["a", "a"],
    ["b", "B"],
    ["c", null],
    ["d", "d"],
    [null, "e"],
  ])
})

test("split rows of nothing are nothing", () => {
  assert.deepEqual(splitRows([]), [])
  assert.deepEqual(splitRows(undefined), [])
})

test("a file's language comes from its name", () => {
  assert.equal(language("internal/post.go"), "go")
  assert.equal(language("ui/src/App.tsx"), "js")
  assert.equal(language("a/b.PY"), "py")
  assert.equal(language("Makefile"), "shell")
  assert.equal(language("x.sh"), "shell")
  assert.equal(language("README"), "plain")
  assert.equal(language(""), "plain")
  assert.equal(language(undefined), "plain")
})

test("tokens cover the whole line, in order", () => {
  for (const [line, lang] of [
    ['func Post(n int64) error { return "x // y" } // done', "go"],
    ["const a = 'it\\'s' + 42 /* c */", "js"],
    ["def f(x):  # comment", "py"],
    ["echo \"$HOME\" # home", "shell"],
    ["", "go"],
    ["unterminated \"string", "go"],
    ["anything at all <b>", "plain"],
  ]) {
    assert.equal(tokens(line, lang).map((t) => t.s).join(""), line, line)
  }
})

test("tokens find keywords, strings, comments and numbers", () => {
  const kinds = (line, lang) => tokens(line, lang).filter((t) => t.k !== "plain").map((t) => `${t.k}:${t.s}`)
  assert.deepEqual(kinds('if n < 0 { return "no" } // why', "go"), ["kw:if", "num:0", "kw:return", 'str:"no"', "com:// why"])
  assert.deepEqual(kinds("const x = `t` # plain words here", "js"), ["kw:const", "str:`t`"])
  assert.deepEqual(kinds("return x  # yes", "py"), ["kw:return", "com:# yes"])
  // A keyword inside a word is not one.
  assert.deepEqual(kinds("ifx returned", "go"), [])
  // Plain text is never coloured.
  assert.deepEqual(kinds('if "x"', "plain"), [])
})

test("a very long line is not lexed", () => {
  const long = "x".repeat(5000)
  assert.deepEqual(tokens(long, "go"), [{ k: "plain", s: long }])
})

test("percent is whole and kept between 0 and 100", () => {
  assert.equal(percent(1, 3), 33)
  assert.equal(percent(3, 3), 100)
  assert.equal(percent(5, 3), 100)
  assert.equal(percent(0, 0), 0)
  assert.equal(percent(undefined, 4), 0)
  assert.equal(percent(-1, 4), 0)
})

const agents = [
  { key: "a", state: "blocked", running: true },
  { key: "b", state: "working", running: true },
  { key: "c", state: "working", running: true },
  { key: "d", state: "done", running: true },
  { key: "e", state: "idle", running: true },
  { key: "f", state: "working", running: false },
]

test("agents are counted by what they are doing; an exited one is none of those", () => {
  assert.deepEqual(agentCounts(agents), { all: 6, waiting: 1, working: 2, done: 1 })
  assert.deepEqual(agentCounts([]), { all: 0, waiting: 0, working: 0, done: 0 })
})

test("agents are filtered the same way", () => {
  assert.deepEqual(filterAgents(agents, "all").map((a) => a.key), ["a", "b", "c", "d", "e", "f"])
  assert.deepEqual(filterAgents(agents, "waiting").map((a) => a.key), ["a"])
  assert.deepEqual(filterAgents(agents, "working").map((a) => a.key), ["b", "c"])
  assert.deepEqual(filterAgents(agents, "done").map((a) => a.key), ["d"])
  assert.deepEqual(filterAgents(agents, "nonsense").map((a) => a.key), ["a", "b", "c", "d", "e", "f"])
})
