// node --test uitest/ — run by TestUILogic.
import { test } from "node:test"
import assert from "node:assert/strict"
import { keyFromEvent, paneRef, splitRef, applyEvent, paneLabel, sidebar, Calls } from "../ui/lib.mjs"

test("keyFromEvent", () => {
  assert.equal(keyFromEvent({ key: "Enter" }), "enter")
  assert.equal(keyFromEvent({ key: "Tab", shiftKey: true }), "shift+tab")
  assert.equal(keyFromEvent({ key: "c", ctrlKey: true }), "ctrl+c")
  assert.equal(keyFromEvent({ key: "x", altKey: true, ctrlKey: true }), "ctrl+alt+x")
  assert.equal(keyFromEvent({ key: " ", ctrlKey: true }), "ctrl+space")
  assert.equal(keyFromEvent({ key: "ArrowUp", ctrlKey: true }), "ctrl+up")
  assert.equal(keyFromEvent({ key: "a" }), null) // text comes as input
  assert.equal(keyFromEvent({ key: "é" }), null)
  assert.equal(keyFromEvent({ key: "c", metaKey: true }), null) // the window's
  assert.equal(keyFromEvent({ key: "Enter", isComposing: true }), null)
  assert.equal(keyFromEvent({ key: "Shift", ctrlKey: true }), null)
  assert.equal(keyFromEvent(null), null)
})

test("pane refs", () => {
  assert.equal(paneRef("box", "p3"), "box:p3")
  assert.deepEqual(splitRef("box:p3"), { machine: "box", id: "p3" })
  assert.deepEqual(splitRef("p3"), { machine: "local", id: "p3" })
})

test("applyEvent", () => {
  const a = { id: "p1", name: "one", state: "running" }
  let panes = applyEvent([], "pane.created", a)
  assert.deepEqual(panes, [a])
  assert.equal(applyEvent(panes, "pane.created", a).length, 1) // twice: once
  panes = applyEvent(panes, "pane.updated", { id: "p1", title: "renamed" })
  assert.equal(panes[0].title, "renamed")
  assert.equal(panes[0].name, "one") // the rest kept
  assert.equal(applyEvent(panes, "pane.updated", { id: "p9" }), panes) // unknown: unchanged
  panes = applyEvent(panes, "pane.exited", { id: "p1", state: "exited", exit_code: 2 })
  assert.equal(panes[0].state, "exited")
  assert.deepEqual(applyEvent(panes, "pane.closed", { id: "p1" }), [])
  assert.equal(applyEvent(panes, "pane.closed", { id: "p9" }), panes)
  assert.equal(applyEvent(panes, "project.updated", {}), panes)
  assert.equal(applyEvent(panes, "pane.created", null), panes)
  assert.deepEqual(applyEvent(undefined, "pane.created", a), [a])
})

test("paneLabel", () => {
  assert.equal(paneLabel({ id: "p1", title: "t", name: "n" }), "t")
  assert.equal(paneLabel({ id: "p1", name: "n" }), "n")
  assert.equal(paneLabel({ id: "p1", agent: { name: "claude" } }), "claude")
  assert.equal(paneLabel({ id: "p1", command: ["/bin/zsh"] }), "/bin/zsh")
  assert.equal(paneLabel({ id: "p1" }), "p1")
})

test("sidebar", () => {
  const machines = [
    { id: "local", label: "this computer", state: "online" },
    { id: "box", label: "box", state: "offline", detail: "ssh: refused" },
  ]
  const rows = sidebar(machines, {
    local: [{ id: "p1", name: "shell", state: "running" }, { id: "p2", agent: { name: "claude", state: "waiting" } }],
    box: [{ id: "p1", name: "ignored: box is offline" }],
  })
  assert.deepEqual(rows.map((r) => [r.kind, r.ref || r.machine, r.label, r.state]), [
    ["machine", "local", "this computer", "online"],
    ["pane", "local:p1", "shell", "running"],
    ["pane", "local:p2", "claude", "waiting"],
    ["machine", "box", "box", "offline"],
  ])
  assert.equal(rows[3].detail, "ssh: refused")
  assert.deepEqual(sidebar([], {}), [])
  assert.deepEqual(sidebar(undefined, {}), [])
})

test("Calls", async () => {
  const calls = new Calls()
  const a = calls.start("local", "pane.list")
  const b = calls.start("box", "pane.close", { id: "p1" })
  assert.deepEqual(a.message, { t: "call", id: "c1", machine: "local", method: "pane.list", params: {} })
  assert.equal(b.message.id, "c2")
  assert.equal(calls.settle({ t: "result", id: "c1", result: { panes: [] } }), true)
  assert.deepEqual(await a.done, { panes: [] })
  assert.equal(calls.settle({ t: "result", id: "c1" }), false) // settled already
  calls.settle({ t: "result", id: "c2", error: { code: "not_found", message: "no pane" } })
  await assert.rejects(b.done, (e) => e.code === "not_found" && e.message === "no pane")
  const c = calls.start("local", "pane.list")
  calls.failAll("socket closed")
  await assert.rejects(c.done, /socket closed/)
  assert.equal(calls.waiting.size, 0)
  const d = calls.start("local", "x")
  calls.settle({ t: "result", id: d.message.id }) // no result: null
  assert.equal(await d.done, null)
})

import { cellAt, wheelGoesTo, clicksGoTo, mouseButton, wheelSteps, scrollOffset, rowCells, ordered, selected, selectionText } from "../ui/lib.mjs"

test("function keys", () => {
  assert.equal(keyFromEvent({ key: "F1" }), "f1")
  assert.equal(keyFromEvent({ key: "F12", ctrlKey: true }), "ctrl+f12")
})

test("cellAt", () => {
  assert.deepEqual(cellAt(0, 0, 8, 16, 80, 24), { col: 0, row: 0 })
  assert.deepEqual(cellAt(15.9, 16, 8, 16, 80, 24), { col: 1, row: 1 })
  assert.deepEqual(cellAt(-5, -5, 8, 16, 80, 24), { col: 0, row: 0 }) // dragged out the top left
  assert.deepEqual(cellAt(9999, 9999, 8, 16, 80, 24), { col: 79, row: 23 }) // and the bottom right
  assert.deepEqual(cellAt(3, 3, 8, 16, 1, 1), { col: 0, row: 0 })
})

test("wheelGoesTo", () => {
  // Main screen: conch's history while there is any.
  assert.equal(wheelGoesTo({ history: 50 }), "history")
  assert.equal(wheelGoesTo({ history: 50, mouse: true }), "history") // the agent wants clicks, not the wheel
  assert.equal(wheelGoesTo({ history: 50, mouse: true }, { altKey: true }), "program")
  assert.equal(wheelGoesTo({ history: 0, mouse: true }), "program")
  assert.equal(wheelGoesTo({ history: 0 }), "none")
  assert.equal(wheelGoesTo({ history: 50 }, { ctrlKey: true }), "history") // nobody else to give it to
  // Alternate screen: the program's, if it asked for the mouse.
  assert.equal(wheelGoesTo({ alt_screen: true, mouse: true, history: 10 }), "program")
  assert.equal(wheelGoesTo({ alt_screen: true, mouse: true, history: 10 }, { ctrlKey: true }), "history")
  assert.equal(wheelGoesTo({ alt_screen: true, mouse: true }, { altKey: true }), "none") // no history kept
  assert.equal(wheelGoesTo({ alt_screen: true, history: 10 }), "history")
  assert.equal(wheelGoesTo(null), "none")
})

test("clicksGoTo and mouseButton", () => {
  assert.equal(clicksGoTo({ mouse: true }), "program")
  assert.equal(clicksGoTo({ mouse: true }, { shiftKey: true }), "select")
  assert.equal(clicksGoTo({}), "select")
  assert.equal(clicksGoTo(null), "select")
  assert.deepEqual([0, 1, 2, 3, -1].map(mouseButton), ["left", "middle", "right", "none", "none"])
})

test("wheelSteps and scrollOffset", () => {
  assert.deepEqual(wheelSteps(0, 120, 40), { acc: 0, steps: 3 })
  assert.deepEqual(wheelSteps(0, 10, 40), { acc: 10, steps: 0 }) // a trackpad's nudge
  assert.deepEqual(wheelSteps(30, 15, 40), { acc: 5, steps: 1 })
  assert.deepEqual(wheelSteps(0, -50, 40), { acc: -10, steps: -1 })
  assert.equal(Object.is(wheelSteps(0, -10, 40).steps, -0), false)
  assert.deepEqual(wheelSteps(undefined, NaN, 40), { acc: 0, steps: 0 })
  assert.equal(scrollOffset(0, 100, -2), 6) // up two steps: back six lines
  assert.equal(scrollOffset(6, 100, 1), 3)
  assert.equal(scrollOffset(2, 100, 1), 0) // not past the live screen
  assert.equal(scrollOffset(98, 100, -5), 100) // not past the oldest line
  assert.equal(scrollOffset(0, 0, -5), 0)
})

test("rowCells", () => {
  assert.deepEqual(rowCells("ab", 4), ["a", "b", " ", " "])
  assert.deepEqual(rowCells("a変b", 5), ["a", "変", "", "b", " "])
  assert.deepEqual(rowCells("\x1b[31mred\x1b[0m", 3), ["r", "e", "d"])
  assert.deepEqual(rowCells("toolong", 3), ["t", "o", "o"])
  assert.deepEqual(rowCells("ab変", 3), ["a", "b", "変"]) // a wide character cut at the edge
  assert.deepEqual(rowCells("", 2), [" ", " "])
  assert.deepEqual(rowCells(undefined, 1), [" "])
})

test("selection", () => {
  const down = { a: { row: 0, col: 2 }, b: { row: 1, col: 1 } }
  const up = { a: { row: 1, col: 1 }, b: { row: 0, col: 2 } }
  assert.deepEqual(ordered(up), ordered(down))
  assert.equal(selected(up, 0, 1), false)
  assert.equal(selected(up, 0, 2), true)
  assert.equal(selected(up, 0, 70), true) // the rest of the first row
  assert.equal(selected(up, 1, 0), true)
  assert.equal(selected(up, 1, 2), false)
  assert.equal(selected(up, 2, 0), false)
  assert.equal(selected(null, 0, 0), false)
  const lines = ["hello world   ", "second line", "third"]
  assert.equal(selectionText(lines, 14, { a: { row: 0, col: 6 }, b: { row: 1, col: 5 } }), "world\nsecond")
  assert.equal(selectionText(lines, 14, { a: { row: 2, col: 4 }, b: { row: 2, col: 0 } }), "third") // dragged backwards
  assert.equal(selectionText(lines, 14, { a: { row: 0, col: 0 }, b: { row: 0, col: 0 } }), "h")
  assert.equal(selectionText(lines, 14, null), "")
  assert.equal(selectionText(["a変b"], 4, { a: { row: 0, col: 0 }, b: { row: 0, col: 3 } }), "a変b")
})

import { agentState, taskTitle, groupTasks, waitingCount, tabsFor, readMenu, choiceKeys, plainRows, changeSummary, ago } from "../ui/lib.mjs"

test("agentState and taskTitle", () => {
  assert.equal(agentState({ agent: { state: "blocked" } }), "waiting")
  assert.equal(agentState({ agent: { state: "working" } }), "working")
  assert.equal(agentState({ agent: { state: "done" } }), "done")
  assert.equal(agentState({ agent: { state: "something new" } }), "idle")
  assert.equal(agentState({ state: "exited", agent: { state: "working" } }), "exited")
  assert.equal(agentState({ state: "running" }), "")
  assert.equal(agentState(null), "")
  assert.equal(taskTitle({ id: "p1", name: "fix-tests", title: "t" }), "fix-tests")
  assert.equal(taskTitle({ id: "p1", title: "t" }), "t")
  assert.equal(taskTitle({ id: "p1", agent: { name: "claude" } }), "claude")
  assert.equal(taskTitle({ id: "p1" }), "p1")
})

test("groupTasks", () => {
  const machines = [{ id: "local", label: "this computer", state: "online" }, { id: "box", label: "box", state: "online" }, { id: "off", label: "off", state: "offline" }]
  const panes = {
    local: [
      { id: "p1", name: "b-task", project_id: "pr1", branch: "b", agent: { name: "claude", state: "working" } },
      { id: "p2", name: "a-task", project_id: "pr1", branch: "a", agent: { name: "codex", state: "blocked" } },
      { id: "p3", name: "zsh", project_id: "pr1", branch: "a" },
      { id: "p4", name: "loose", agent: { name: "gemini", state: "done" } },
    ],
    box: [{ id: "p1", name: "remote", project_id: "rp", agent: { name: "devin", state: "idle" } }],
    off: [{ id: "p9", agent: { name: "x", state: "blocked" } }],
  }
  const projects = { local: [{ id: "pr1", name: "conch" }], box: [{ id: "rp", name: "infra" }] }
  const g = groupTasks(machines, panes, projects)
  assert.deepEqual(g.projects.map((p) => p.name), ["conch", "infra · box", "Other agents"])
  const conch = g.projects[0]
  assert.deepEqual(conch.tasks.map((t) => [t.title, t.state, t.agent]), [["a-task", "waiting", "codex"], ["b-task", "working", "claude"]])
  assert.equal(conch.tasks[0].ref, "local:p2")
  assert.deepEqual(g.terminals.map((t) => t.ref), ["local:p3"])
  assert.equal(waitingCount(g), 1) // the offline machine's agent is not counted
  assert.deepEqual(groupTasks([], {}, {}), { projects: [], terminals: [] })
})

test("tabsFor", () => {
  const item = { machine: "local", project: "pr1", branch: "a", pane: { id: "p2", agent: { name: "codex", state: "blocked" } } }
  const panes = [item.pane, { id: "p3", name: "zsh", project_id: "pr1", branch: "a" }, { id: "p1", project_id: "pr1", branch: "b", agent: { name: "claude" } }]
  assert.deepEqual(tabsFor(item, panes).map((t) => [t.ref, t.title, t.agent, t.state]), [
    ["local:p2", "codex", "codex", "waiting"], ["local:p3", "zsh", "shell", ""],
  ])
  assert.deepEqual(tabsFor({ ...item, project: "" }, panes).map((t) => t.ref), ["local:p2"]) // no project: alone
  assert.deepEqual(tabsFor(null, panes), [])
})

test("readMenu and choiceKeys", () => {
  const screen = [
    "Do you want to make this edit to cursor_test.go?",
    "│ ❯ 1. Yes                                    │",
    "│   2. Yes, and don't ask again this session  │",
    "│   3. No, and tell Claude what to do         │",
  ]
  const m = readMenu(screen)
  assert.deepEqual(m, { labels: ["Yes", "Yes, and don't ask again this session", "No, and tell Claude what to do"], cursor: 0, top: 1 })
  assert.deepEqual(choiceKeys(m, 0), ["enter"])
  assert.deepEqual(choiceKeys(m, 2), ["down", "down", "enter"])
  assert.deepEqual(choiceKeys({ labels: ["a", "b", "c"], cursor: 2 }, 0), ["up", "up", "enter"])
  assert.deepEqual(choiceKeys(m, 3), [])
  assert.deepEqual(choiceKeys(null, 0), [])
  // A numbered list in an answer has no cursor: not a menu.
  assert.equal(readMenu(["1. first", "2. second"]), null)
  // Two cursors is no menu either; nor a list that skips a number.
  assert.equal(readMenu(["❯ 1. a", "› 2. b"]), null)
  assert.equal(readMenu(["❯ 1. a", "3. c"]), null)
  // The nearest the bottom wins.
  assert.deepEqual(readMenu(["❯ 1. old", "2. old", "", "1. new", "❯ 2. new"]), { labels: ["new", "new"], cursor: 1, top: 3 })
  assert.equal(readMenu([]), null)
  assert.equal(readMenu(undefined), null)
})

test("plainRows, changeSummary and ago", () => {
  assert.deepEqual(plainRows({ lines: ["\x1b[1;31m❯ 1. Yes\x1b[0m", "\x1b]8;;http://x\x07link\x1b]8;;\x07"] }), ["❯ 1. Yes", "link"])
  assert.deepEqual(plainRows(null), [])
  const s = changeSummary({ files: [{ path: "internal/pane/a.go", code: "M", added: 3, deleted: 1 }, { path: "README.md", code: "A", added: 10, deleted: 0 }], commits: [{}, {}] })
  assert.deepEqual([s.added, s.deleted, s.commits], [13, 1, 2])
  assert.deepEqual([s.files[0].name, s.files[0].dir, s.files[1].dir], ["a.go", "internal/pane", ""])
  assert.deepEqual(changeSummary(null), { files: [], added: 0, deleted: 0, commits: 0 })
  const now = Date.parse("2026-10-29T12:00:00Z")
  assert.equal(ago("2026-10-29T11:59:30Z", now), "now")
  assert.equal(ago("2026-10-29T11:56:00Z", now), "4m")
  assert.equal(ago("2026-10-29T10:00:00Z", now), "2h")
  assert.equal(ago("2026-10-26T12:00:00Z", now), "3d")
  assert.equal(ago("nonsense", now), "")
  assert.equal(ago("2026-10-29T12:05:00Z", now), "now") // a clock ahead: not negative
})

import { fitSize, askReason } from "../ui/lib.mjs"

test("fitSize", () => {
  const f = { cols: 120, rows: 40 }
  assert.deepEqual(fitSize(f, 800, 640, 8, 16), { cols: 100, rows: 40 })
  assert.equal(fitSize(f, 960, 640, 8, 16), null) // already that size
  assert.equal(fitSize(f, 100, 640, 8, 16), null) // hidden or mid-layout: too narrow to mean it
  assert.equal(fitSize(f, 800, 40, 8, 16), null)
  assert.equal(fitSize(null, 800, 640, 8, 16), null)
  assert.equal(fitSize(f, 800, 640, 0, 16), null)
  assert.deepEqual(fitSize(f, 100000, 100000, 8, 16), { cols: 500, rows: 200 }) // capped
  assert.deepEqual(fitSize(f, 160, 80, 8, 16), { cols: 20, rows: 5 }) // exactly the minimum
})

test("askReason", () => {
  assert.equal(askReason({ message: " Claude needs your permission to use Edit " }), "Claude needs your permission to use Edit")
  assert.equal(askReason({ reason: "rule:permission_prompt" }), "It is waiting for your answer.")
  assert.equal(askReason(null), "It is waiting for your answer.")
})

import { questionAbove } from "../ui/lib.mjs"

test("questionAbove", () => {
  const rows = ["earlier output", "", "╭────────────╮", "│ Do you want to make", "│ this edit?  │", "│            │", "│ ❯ 1. Yes   │"]
  assert.equal(questionAbove(rows, 6), "Do you want to make this edit?")
  assert.equal(questionAbove(["Proceed?", "", "❯ 1. Yes"], 2), "Proceed?")
  assert.equal(questionAbove(["❯ 1. Yes"], 0), "")
  assert.equal(questionAbove([], 0), "")
  assert.equal(questionAbove(["x".repeat(400), "❯ 1. a"], 1).length, 300)
  // What was typed above the question is not part of it.
  assert.equal(questionAbove(["> printf 'Edit it?'", "Edit it?", "", "❯ 1. Yes"], 3), "Edit it?")
  // Two lines at most: a question, not the screen.
  assert.equal(questionAbove(["a", "b", "c", "❯ 1. Yes"], 3), "b c")
})

import { parseDiff, lineOf, commentsAt } from "../ui/lib.mjs"

const sample = `diff --git a/x.go b/x.go
index 1..2 100644
--- a/x.go
+++ b/x.go
@@ -1,4 +1,5 @@ package x
 package x
-var a = 1
+var a = 2
+var b = 3
 
 func f() {}
@@ -10,2 +11,2 @@ func g() {
-	return 1
+	return 2
\\ No newline at end of file
`

test("parseDiff", () => {
  const rows = parseDiff(sample)
  assert.deepEqual(rows.map((r) => [r.kind, r.oldN ?? "", r.newN ?? "", r.text]), [
    ["hunk", "", "", "package x"],
    ["ctx", 1, 1, "package x"],
    ["del", 2, "", "var a = 1"],
    ["add", "", 2, "var a = 2"],
    ["add", "", 3, "var b = 3"],
    ["ctx", 3, 4, ""],
    ["ctx", 4, 5, "func f() {}"],
    ["hunk", "", "", "func g() {"],
    ["del", 10, "", "\treturn 1"],
    ["add", "", 11, "\treturn 2"],
  ])
  assert.deepEqual(parseDiff(""), [])
  assert.deepEqual(parseDiff(undefined), [])
  assert.deepEqual(parseDiff("Binary files a/x and b/x differ\n"), []) // no hunks
  assert.equal(lineOf(rows[2]), 2) // a deleted line: its old number
  assert.equal(lineOf(rows[3]), 2)
})

test("commentsAt", () => {
  const rows = parseDiff(sample)
  const at = commentsAt(rows, [
    { file: "x.go", line: 3, hunk: rows[0].hunk, note: "why b?" },
    { file: "x.go", line: 11, note: "no hunk given" },
    { file: "x.go", line: 99, hunk: rows[7].hunk, note: "line gone: under its hunk" },
    { file: "x.go", note: "about the file" },
    { file: "other.go", line: 1, note: "another file" },
  ], "x.go")
  assert.deepEqual([...at.keys()].sort((a, b) => a - b), [0, 4, 7, 9])
  assert.equal(at.get(4)[0].note, "why b?")
  assert.equal(at.get(9)[0].note, "no hunk given")
  assert.equal(at.get(7)[0].note, "line gone: under its hunk")
  assert.equal(at.get(0)[0].note, "about the file")
  assert.equal(commentsAt(rows, null, "x.go").size, 0)
})

import { plural } from "../ui/lib.mjs"

test("plural", () => {
  assert.equal(plural(1, "file"), "1 file")
  assert.equal(plural(0, "file"), "0 files")
  assert.equal(plural(3, "comment"), "3 comments")
})

import { branchChecks } from "../ui/lib.mjs"

test("branchChecks", () => {
  const project = { id: "pr1", branches: [
    { name: "fix", pr: { number: 12, state: "OPEN", review: "APPROVED", checks: "fail", passed: 3, total: 5, url: "https://github.com/x/y/pull/12" } },
    { name: "draft", pr: { number: 13, state: "OPEN", draft: true, checks: "pending" } },
    { name: "done", pr: { number: 9, state: "MERGED" } },
    { name: "bare" },
  ] }
  assert.deepEqual(branchChecks(project, "fix", null).map((r) => [r.name, r.state, r.detail]), [
    ["Pull request #12", "waiting", "open · approved"],
    ["CI", "failed", "3 of 5 passed"],
  ])
  assert.deepEqual(branchChecks(project, "draft", null).map((r) => [r.name, r.state, r.detail]), [
    ["Pull request #13", "waiting", "draft"], ["CI", "running", "running"],
  ])
  assert.deepEqual(branchChecks(project, "done", null).map((r) => [r.state, r.detail]), [["passed", "merged"]])
  assert.deepEqual(branchChecks(project, "bare", null), [])
  assert.deepEqual(branchChecks(project, "missing", null), [])
  assert.deepEqual(branchChecks(null, "x", null), [])
  const pilot = { branches: [
    { project_id: "pr1", branch: "bare", check_failed: true, fixes: 2, waiting: true, held: "behind its base" },
    { project_id: "other", branch: "bare", checking: "p9" },
  ] }
  assert.deepEqual(branchChecks(project, "bare", pilot).map((r) => [r.name, r.state, r.detail]), [
    ["Project check", "failed", "failing at this commit"],
    ["Autopilot", "waiting", "sent 2 failures back to the agent"],
    ["Merge", "waiting", "held: behind its base"],
  ])
})

import { matchesSearch, sortTasks } from "../ui/lib.mjs"

test("matchesSearch", () => {
  assert.equal(matchesSearch("Badge the Dock icon", "dock badge"), true)
  assert.equal(matchesSearch("Badge the Dock icon", "DOCK"), true)
  assert.equal(matchesSearch("Badge the Dock icon", "dock sound"), false)
  assert.equal(matchesSearch("anything", ""), true) // no search: everything
  assert.equal(matchesSearch("anything", "   "), true)
  assert.equal(matchesSearch("", "x"), false)
  assert.equal(matchesSearch(null, undefined), true)
})

test("sortTasks", () => {
  const ts = [{ title: "b", state: "idle" }, { title: "a", state: "working" }, { title: "c", state: "waiting" }, { title: "a2", state: "waiting" }]
  assert.deepEqual(sortTasks(ts).map((t) => t.title), ["a2", "c", "a", "b"])
  assert.deepEqual(sortTasks(ts, "name").map((t) => t.title), ["a", "a2", "b", "c"])
  assert.deepEqual(ts.map((t) => t.title), ["b", "a", "c", "a2"]) // not sorted in place
  assert.deepEqual(sortTasks([]), [])
})
