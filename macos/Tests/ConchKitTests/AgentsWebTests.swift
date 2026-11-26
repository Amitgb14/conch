import Foundation
import Testing
import WebKit
@testable import ConchKit

// The Agents overview (⌃⌘A): its cards, limits and activity from what the
// app reads, the key of its tab, what it may ask, and the view clicked
// through in a real web view.

private let now = Date(timeIntervalSince1970: 100_000)

private func cards() -> [RosterCard] {
    roster([
        RosterCard(key: "local|p1", title: "Fix overdraft", place: "ledger · fix/overdraft", agent: "claude", state: .working, since: now.addingTimeInterval(-120)),
        RosterCard(key: "local|p2", title: "Add health check", place: "ledger · main", agent: "claude", state: .blocked, since: now.addingTimeInterval(-30)),
        RosterCard(key: "busybox|p3", title: "Review", place: "ledger · busybox", agent: "codex", state: .done, since: now.addingTimeInterval(-600)),
        RosterCard(key: "local|p4", title: "dev-server", place: "ledger"),
        RosterCard(key: "local|p5", title: "Old", agent: "gemini", state: .blocked, since: now.addingTimeInterval(-9000), running: false),
    ])
}

private let question = Question(id: "q1", text: "Allow command? go test", choices: [
    Choice(choice: "1", label: "Allow", default: true), Choice(choice: "2", label: "Always", default: nil), Choice(choice: "3", label: "Deny…", default: false),
])

@Test func agentsWebShowsAgentsNotTerminals() {
    let v = agentsWebView(cards: cards(), questions: ["local|p2": question], answering: [], focused: "local|p1", plans: [], theme: .conch, now: now)
    #expect(v.agents.map(\.key) == ["local|p2", "local|p1", "busybox|p3", "local|p5"], "waiting first, as the roster orders; no terminal")
    let wait = v.agents[0]
    #expect(wait.state == "blocked" && wait.question?.text == "Allow command? go test")
    #expect(wait.question?.choices.map(\.label) == ["Allow", "Always", "Deny…"])
    #expect(wait.question?.choices.map(\.index) == [0, 1, 2] && wait.question?.choices.map(\.default) == [true, false, false])
    #expect(wait.ago == "30s" || wait.ago == "now", "\(wait.ago ?? "")")
    #expect(v.agents[1].focused && !wait.focused)
    #expect(v.agents[1].question == nil, "a working agent asks nothing")
    // An exited agent is not waiting, whatever it last said.
    #expect(v.agents[3].question == nil && !v.agents[3].running)
    for a in v.agents { #expect(a.color.hasPrefix("#") && a.color.count == 7) }
}

@Test func agentsWebQuestionWithoutChoices() {
    // Nothing read off the screen yet: the agent's message, kind and all.
    let v = agentsWebView(cards: cards(), questions: [:], answering: ["local|p2"], focused: nil, plans: [],
                          messages: ["local|p2": "Claude needs your permission to use Bash"], theme: .conch, now: now)
    let wait = v.agents[0]
    #expect(wait.question?.choices.isEmpty == true && wait.question?.kind == "permission")
    #expect(wait.question?.text == "Claude needs your permission to use Bash")
    #expect(wait.answering)
}

@Test func agentsWebLimitsAndActivity() {
    let plans = [
        PlanLimits(agent: "codex", fiveHour: .init(usedPct: 12)),
        PlanLimits(agent: "claude", account: "work", fiveHour: .init(usedPct: 41), week: .init(usedPct: 63)),
        PlanLimits(agent: "gemini"), // reports nothing
    ]
    let v = agentsWebView(cards: cards(), questions: [:], answering: [], focused: nil, plans: plans, theme: .conch, now: now)
    #expect(v.limits.map(\.label) == ["claude (work) · 5 hours", "claude (work) · 7 days", "codex · 5 hours"])
    #expect(v.limits.map(\.pct) == [41, 63, 12])
    #expect(v.activity.first?.key == "local|p2" && v.activity.first?.text == "Add health check is waiting on you")
    #expect(v.activity.count == 4, "agents only, each once")
}

@Test func agentsWebWithNothing() {
    let v = agentsWebView(cards: [], questions: [:], answering: [], focused: nil, plans: [], theme: .conch, now: now)
    #expect(v.agents.isEmpty && v.limits.isEmpty && v.activity.isEmpty)
}

@Test func agentsKeyIsNotAPane() {
    #expect(isAgentsKey(agentsKey) && !isAgentsKey("local|p1") && !isAgentsKey("agents|x"))
    #expect(!isPaneKey(agentsKey) && !isTerminalKey(agentsKey))
    #expect(machine(ofKey: agentsKey) == "local")
    #expect(treeRow(forTab: agentsKey) == nil)
}

@Test func agentsHasAMenuCommand() {
    #expect(paletteCommands.first { $0.id == "agentsOverview" }?.keys == "⌃⌘A")
    // Not a key something else has.
    #expect(paletteCommands.filter { $0.keys == "⌃⌘A" }.count == 1)
}

private func ask(_ m: [String: Any]) -> AgentsIntent? { agentsIntent(m.merging(["view": "agents"]) { a, _ in a }) }

@Test func agentsIntentsParse() {
    #expect(ask(["type": "ready"]) == .ready)
    #expect(ask(["type": "open", "key": "local|p1"]) == .open("local|p1"))
    #expect(ask(["type": "answer", "key": "local|p2", "choice": 1]) == .answer(key: "local|p2", choice: 1))
    #expect(ask(["type": "newTask"]) == .newTask)
    #expect(ask(["type": "answer", "key": "local|p2", "choice": -1]) == nil)
    #expect(ask(["type": "answer", "key": "local|p2", "choice": "1"]) == nil)
    #expect(ask(["type": "answer", "choice": 0]) == nil)
    #expect(ask(["type": "open"]) == nil)
    #expect(ask(["type": "kill", "key": "local|p1"]) == nil)
    #expect(agentsIntent(["view": "changes", "type": "newTask"]) == nil)
}

@MainActor @Test func agentsViewDrawsAndClicksThrough() async throws {
    let page = ConchUIPage(.agents)
    try await page.await(1)
    let plans = [PlanLimits(agent: "claude", fiveHour: .init(usedPct: 41))]
    try await page.show(agentsWebView(cards: cards(), questions: ["local|p2": question], answering: [], focused: nil, plans: plans, theme: .conch, now: now))
    let text = try await page.text()
    for want in ["4 agents", "Fix overdraft", "Add health check", "Allow command? go test", "Allow", "Always", "Review", "Plan limits",
                 "claude · 5 hours", "41%", "Activity", "Add health check is waiting on you", "Needs you · 1", "Working · 1", "Done · 1"] {
        #expect(text.contains(want), "missing \(want)")
    }
    #expect(!text.contains("dev-server"), "a terminal is not an agent")
    let n = page.said.count
    try await page.click("Always")
    try await page.click("New Task")
    try await page.await(n + 2)
    #expect(page.said.dropFirst(n).compactMap(agentsIntent) == [.answer(key: "local|p2", choice: 1), .newTask])

    // The filters are the page's own: no message, fewer cards.
    try await page.click("Done · 1")
    // The cards, not the activity beside them, which still names them all.
    let done = try #require(try await page.run("document.querySelector('.grid').innerText") as? String)
    #expect(done.contains("Review") && !done.contains("Fix overdraft"))
    try await page.click("Open")
    try await page.await(n + 3)
    #expect(agentsIntent(page.said[n + 2]) == .open("busybox|p3"))
}

@MainActor @Test func agentsViewWhileAnsweringAndEmpty() async throws {
    let page = ConchUIPage(.agents)
    try await page.await(1)
    try await page.show(agentsWebView(cards: cards(), questions: ["local|p2": question], answering: ["local|p2"], focused: nil, plans: [], theme: .conch, now: now))
    let t = try await page.text()
    #expect(t.contains("Sending your answer") && !t.contains("Always"))
    #expect(t.contains("No agent has reported its plan yet"))
    try await page.show(agentsWebView(cards: [], questions: [:], answering: [], focused: nil, plans: [], theme: .conch, now: now))
    #expect(try await page.text().contains("No agents yet"))
}
