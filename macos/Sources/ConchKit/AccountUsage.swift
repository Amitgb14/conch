import Foundation

// The status bar's usage and its panel, decided without the views: which
// accounts there are and in what order, which one the chip shows, the
// buckets a span charts, the models a chart stacks, and how near a window
// is to its failover line.

/// One account, everything known about it: its plan (agent.limits), what it
/// used (usage.history by account), and what account.list says.
public struct UsageAccount: Equatable, Sendable, Identifiable {
    public var agent: String
    /// "" for the agent's own.
    public var name: String
    public var limits: PlanLimits?
    public var usage: AgentUsage?
    public var info: AgentAccount?
    public var id: String { agent + "|" + name }
    public var isOwn: Bool { name.isEmpty }

    public init(agent: String, name: String, limits: PlanLimits? = nil, usage: AgentUsage? = nil, info: AgentAccount? = nil) {
        self.agent = agent
        self.name = name
        self.limits = limits
        self.usage = usage
        self.info = info
    }

    /// How a tab names it: "default" for Claude's own, "Codex" for
    /// Codex's, "Codex review" for another of Codex's.
    public var tabLabel: String {
        if agent == "claude" { return isOwn ? "default" : name }
        let title = agent.prefix(1).uppercased() + agent.dropFirst()
        return isOwn ? title : "\(title) \(name)"
    }

    /// Whether its plan has been reported, so the chip can show it.
    public var reportsPlan: Bool { limits?.reportsUse == true }
}

/// The agents conch keeps accounts for, in the order they are shown.
public let accountAgentOrder = ["claude", "codex"]

/// Every account to show: each agent's own and the ones added (from
/// account.list), and any that reported a plan or saved usage without being
/// listed by an older server. Once the list is known, an account of an
/// agent it covers that is not in it was removed in Settings and is left
/// out, though an agent still running on it goes on reporting. Each agent's
/// own first, then its others in the order they were added; agents in a
/// fixed order.
public func usageAccounts(limits: [PlanLimits], history: UsageHistory?, list: AgentAccountList?) -> [UsageAccount] {
    var out: [UsageAccount] = []
    func index(_ agent: String, _ name: String) -> Int {
        if let i = out.firstIndex(where: { $0.agent == agent && $0.name == name }) { return i }
        out.append(UsageAccount(agent: agent, name: name))
        return out.count - 1
    }
    for a in list?.accounts ?? [] {
        out[index(a.agent, a.isDefault == true ? "" : a.name)].info = a
    }
    let listed = Set((list?.accounts ?? []).map { "\($0.agent)|\($0.isDefault == true ? "" : $0.name)" })
    let covered = Set(list?.agents ?? [])
    func removed(_ agent: String, _ name: String) -> Bool {
        !name.isEmpty && covered.contains(agent) && !listed.contains("\(agent)|\(name)")
    }
    for l in limits where l.reportsUse && !removed(l.agent, l.account ?? "") {
        out[index(l.agent, l.account ?? "")].limits = l
    }
    for u in history?.agents ?? [] where !removed(u.agent, u.account ?? "") {
        out[index(u.agent, u.account ?? "")].usage = u
    }
    let added = (list?.accounts ?? []).map { "\($0.agent)|\($0.isDefault == true ? "" : $0.name)" }
    func rank(_ a: UsageAccount) -> (Int, Int, Int) {
        let agent = accountAgentOrder.firstIndex(of: a.agent) ?? accountAgentOrder.count
        let at = added.firstIndex(of: a.id) ?? Int.max
        return (agent, a.isOwn ? 0 : 1, at)
    }
    return out.sorted { rank($0) < rank($1) || (rank($0) == rank($1) && $0.id < $1.id) }
}

/// The accounts the chip marks: one each that has reported its plan.
public func chipMarks(_ accounts: [UsageAccount]) -> [UsageAccount] { accounts.filter(\.reportsPlan) }

/// The account the chip shows: the one the agent in view runs on, when it
/// has reported its plan; else Claude's own; else the first that has.
public func chipAccount(_ accounts: [UsageAccount], agent: String?, account: String?) -> UsageAccount? {
    let reported = accounts.filter(\.reportsPlan)
    if let agent, let a = reported.first(where: { $0.agent == agent && $0.name == (account ?? "") }) { return a }
    return reported.first { $0.agent == "claude" && $0.isOwn } ?? reported.first
}

/// The tab the panel opens on: the account the chip shows, else the first.
public func panelTab(_ accounts: [UsageAccount], chip: UsageAccount?) -> String? {
    chip?.id ?? accounts.first?.id
}

/// What a span charts: the last 24 hours by the hour, the last 7 days or
/// the last 30 days by the day.
public enum UsageSpan: String, CaseIterable, Sendable {
    case day, week, month

    public var title: String {
        switch self {
        case .day: "Day"
        case .week: "Week"
        case .month: "Month"
        }
    }

    /// The period the server totals it in, in hours.
    public var hours: Int {
        switch self {
        case .day: 24
        case .week: 7 * 24
        case .month: 30 * 24
        }
    }

    /// "today", "this week", "the last 30 days", for a sentence.
    public var phrase: String {
        switch self {
        case .day: "in the last 24 hours"
        case .week: "in the last 7 days"
        case .month: "in the last 30 days"
        }
    }
}

/// The buckets a span charts, oldest first.
public func spanBuckets(_ u: AgentUsage?, _ span: UsageSpan) -> [UsageBucket] {
    guard let u else { return [] }
    switch span {
    case .day: return u.hours ?? []
    case .week: return Array((u.days ?? []).suffix(7))
    case .month: return u.days ?? []
    }
}

/// A span's totals, by model, from the server's period.
public func spanTotal(_ u: AgentUsage?, _ span: UsageSpan) -> UsagePeriod? {
    u?.period(span.hours)
}

/// The models a chart stacks, most used first, at most `limit`; the rest are
/// added up as "Other", so a colour is never made up for a ninth model.
public func chartModels(_ buckets: [UsageBucket], limit: Int = 3) -> [String] {
    var totals: [String: Int] = [:]
    for b in buckets { for m in b.models { totals[m.model, default: 0] += m.tokens.total } }
    let sorted = totals.filter { $0.value > 0 }.sorted { $0.value != $1.value ? $0.value > $1.value : $0.key < $1.key }.map(\.key)
    return sorted.count > limit ? Array(sorted.prefix(limit)) + [otherModel] : sorted
}

/// The name the chart gives every model past its limit.
public let otherModel = "Other"

/// A bucket's tokens for one of chartModels' names.
public func bucketTokens(_ b: UsageBucket, model: String, shown: [String]) -> Int {
    if model == otherModel {
        return b.models.filter { !shown.contains($0.model) }.reduce(0) { $0 + $1.tokens.total }
    }
    return b.models.first { $0.model == model }?.tokens.total ?? 0
}

/// A round top for an axis: 1, 1.5, 2, 3, 5, 7.5 or 10 times a power of ten.
public func niceCeiling(_ v: Double) -> Double {
    guard v > 0 else { return 1 }
    let p = pow(10, floor(log10(v)))
    for m in [1, 1.5, 2, 3, 5, 7.5, 10] where m * p >= v { return m * p }
    return 10 * p
}

/// The failover line an agent's new agents are held to: its own where it
/// has one, else every agent's; 0 is off.
public func failoverLine(_ list: AgentAccountList?, agent: String) -> Int {
    list?.failoverFor?[agent] ?? list?.failoverAt ?? 0
}

/// How near a window is to its limit, said in words as well as colour.
public enum UsageLevel: Equatable, Sendable {
    case room, close, past, limit

    public var words: String {
        switch self {
        case .room: "Room left"
        case .close: "Getting close"
        case .past: "Past failover"
        case .limit: "At the limit"
        }
    }
}

/// A window's level: at the limit from 100%, past failover from the line
/// (when failover is on), getting close from 10 points under it (or 80%
/// with failover off).
public func usageLevel(_ pct: Double, line: Int) -> UsageLevel {
    if pct >= 100 { return .limit }
    if line > 0 && pct >= Double(line) { return .past }
    let close = line > 0 ? Double(line - 10) : 80
    return pct >= close ? .close : .room
}
