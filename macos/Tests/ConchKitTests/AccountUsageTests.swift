import ConchKit
import Foundation
import Testing

// The status bar's usage chip and panel: which accounts, in what order,
// which one the chip shows, and what a span charts.

private func lim(_ agent: String, _ account: String? = nil, five: Double = 10, week: Double = 20) -> PlanLimits {
    PlanLimits(agent: agent, account: account, fiveHour: .init(usedPct: five), week: .init(usedPct: week))
}

private func bucket(_ hour: Int, _ models: [(String, Int)]) -> UsageBucket {
    let ms = models.map { ModelUsage(model: $0.0, tokens: UsageTokens(output: $0.1)) }
    return UsageBucket(start: Date(timeIntervalSince1970: Double(hour * 3600)), tokens: UsageTokens(output: models.reduce(0) { $0 + $1.1 }), models: ms)
}

@Test func accountsAreEachAgentsOwnThenTheAddedOnes() {
    let list = AgentAccountList(accounts: [
        AgentAccount(agent: "codex", name: "default", isDefault: true),
        AgentAccount(agent: "codex", name: "review", dir: "/r"),
        AgentAccount(agent: "claude", name: "default", isDefault: true),
        AgentAccount(agent: "claude", name: "zeta", dir: "/z"),
        AgentAccount(agent: "claude", name: "alpha", dir: "/a"),
    ])
    let got = usageAccounts(limits: [lim("claude", "alpha"), lim("claude")], history: nil, list: list)
    // Claude first, its own first, then in the order added (not by name).
    #expect(got.map(\.id) == ["claude|", "claude|zeta", "claude|alpha", "codex|", "codex|review"])
    #expect(got[0].limits != nil && got[1].limits == nil && got[2].limits != nil)
    #expect(got.map(\.tabLabel) == ["default", "zeta", "alpha", "Codex", "Codex review"])
}

@Test func anAccountNotListedStillShows() {
    // An older server, or an account removed since it reported.
    let h = UsageHistory(agents: [AgentUsage(agent: "claude", periods: [], account: "gone")])
    let got = usageAccounts(limits: [lim("codex")], history: h, list: nil)
    #expect(got.map(\.id) == ["claude|gone", "codex|"])
    #expect(usageAccounts(limits: [], history: nil, list: nil).isEmpty)
    // A report with no window is no plan.
    #expect(usageAccounts(limits: [PlanLimits(agent: "claude")], history: nil, list: nil).isEmpty)
}

@Test func theChipFollowsTheAgentInView() {
    let accounts = usageAccounts(limits: [lim("claude"), lim("claude", "backup"), lim("codex")], history: nil,
                                 list: AgentAccountList(accounts: [AgentAccount(agent: "codex", name: "review", dir: "/r")]))
    #expect(chipMarks(accounts).map(\.id) == ["claude|", "claude|backup", "codex|"])
    #expect(chipAccount(accounts, agent: "claude", account: "backup")?.id == "claude|backup")
    #expect(chipAccount(accounts, agent: "claude", account: nil)?.id == "claude|")
    #expect(chipAccount(accounts, agent: "codex", account: "")?.id == "codex|")
    // An account with nothing reported, or no agent in view: Claude's own.
    #expect(chipAccount(accounts, agent: "codex", account: "review")?.id == "claude|")
    #expect(chipAccount(accounts, agent: nil, account: nil)?.id == "claude|")
    // No Claude at all: the first that reported.
    let codexOnly = usageAccounts(limits: [lim("codex")], history: nil, list: nil)
    #expect(chipAccount(codexOnly, agent: nil, account: nil)?.id == "codex|")
    #expect(chipAccount([], agent: "claude", account: nil) == nil)
    #expect(panelTab(accounts, chip: nil) == "claude|")
    #expect(panelTab([], chip: nil) == nil)
}

@Test func aSpanChartsItsBuckets() {
    let days = (0..<30).map { bucket($0 * 24, [("m", $0)]) }
    let hours = (0..<24).map { bucket($0, [("m", 1)]) }
    let u = AgentUsage(agent: "claude", periods: [UsagePeriod(hours: 168, tokens: UsageTokens(output: 9))], hours: hours, days: days)
    #expect(spanBuckets(u, .day).count == 24)
    #expect(spanBuckets(u, .week).map(\.tokens.output) == [23, 24, 25, 26, 27, 28, 29])
    #expect(spanBuckets(u, .month).count == 30)
    #expect(spanTotal(u, .week)?.tokens.output == 9)
    #expect(spanTotal(u, .day) == nil)
    // An older server sends no buckets.
    let old = AgentUsage(agent: "claude", periods: [])
    #expect(spanBuckets(old, .week).isEmpty && spanBuckets(nil, .day).isEmpty)
}

@Test func aChartStacksTheBusiestModelsAndAddsUpTheRest() {
    let bs = [bucket(0, [("a", 50), ("b", 30), ("c", 10), ("d", 5)]), bucket(1, [("b", 30), ("e", 2), ("z", 0)])]
    let shown = chartModels(bs)
    #expect(shown == ["b", "a", "c", otherModel])
    #expect(bucketTokens(bs[0], model: otherModel, shown: shown) == 5)
    #expect(bucketTokens(bs[1], model: otherModel, shown: shown) == 2)
    #expect(bucketTokens(bs[1], model: "a", shown: shown) == 0)
    // Few enough: no Other; nothing used: no series; ties by name.
    #expect(chartModels([bucket(0, [("y", 1), ("x", 1)])]) == ["x", "y"])
    #expect(chartModels([bucket(0, [("x", 0)])]).isEmpty)
    #expect(chartModels([]).isEmpty)
}

@Test func anAxisEndsOnARoundNumber() {
    #expect(niceCeiling(0) == 1)
    #expect(niceCeiling(-3) == 1)
    #expect(niceCeiling(1) == 1)
    #expect(niceCeiling(1.2) == 1.5)
    #expect(niceCeiling(41) == 50)
    #expect(niceCeiling(76_000) == 100_000)
    #expect(niceCeiling(7_400_000) == 7_500_000)
}

@Test func failoverLinesAndLevels() {
    let l = AgentAccountList(failoverAt: 80, failoverFor: ["codex": 90, "claude": 0])
    #expect(failoverLine(l, agent: "codex") == 90)
    #expect(failoverLine(l, agent: "claude") == 0)
    #expect(failoverLine(AgentAccountList(failoverAt: 80), agent: "claude") == 80)
    #expect(failoverLine(nil, agent: "claude") == 0)
    #expect(usageLevel(6, line: 80) == .room)
    #expect(usageLevel(70, line: 80) == .close)
    #expect(usageLevel(80, line: 80) == .past)
    #expect(usageLevel(100, line: 80) == .limit)
    // Failover off: close from 80%, never "past".
    #expect(usageLevel(79, line: 0) == .room)
    #expect(usageLevel(95, line: 0) == .close)
    #expect(usageLevel(120, line: 0) == .limit)
    #expect(UsageLevel.past.words == "Past failover")
}

@Test func whatTheServerSendsDecodes() throws {
    let json = #"""
    {"at":"2026-11-19T10:00:00Z","agents":[{"agent":"claude","account":"backup","periods":[{"hours":24,"output":3}],
      "hours":[{"start":"2026-11-19T09:00:00Z","output":3,"models":[{"model":"claude-opus-5-5","output":3}]}],
      "days":[{"start":"2026-11-19T00:00:00-08:00","input":1}]}]}
    """#
    let h = try conchDecoder().decode(UsageHistory.self, from: Data(json.utf8))
    let a = try #require(h.agents.first)
    #expect(a.account == "backup" && a.hours?.count == 1 && a.days?.count == 1)
    #expect(a.hours?[0].models.first?.tokens.output == 3 && a.days?[0].tokens.input == 1)
    // An older server's answer: no account, no buckets.
    let old = try conchDecoder().decode(UsageHistory.self, from: Data(#"{"agents":[{"agent":"codex","periods":[]}]}"#.utf8))
    #expect(old.agents[0].account == nil && old.agents[0].hours == nil)
    // A pane's account, and a failover with an agent.
    let p = try conchDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p1","name":"x","state":"running","account":"backup"}"#.utf8))
    #expect(p.account == "backup")
    let e = JSONEncoder()
    e.outputFormatting = .sortedKeys
    #expect(String(decoding: try e.encode(AccountFailoverParams(at: 90, agent: "codex")), as: UTF8.self) == #"{"agent":"codex","at":90}"#)
    #expect(String(decoding: try e.encode(AccountFailoverParams(at: 0, agent: "codex", shared: true)), as: UTF8.self) == #"{"agent":"codex","at":0,"shared":true}"#)
}
