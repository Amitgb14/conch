import Foundation

// Another agent at a plan limit (agent.switch.limit.v1, #352 phase 3):
// when an agent is out of plan on every account it has, its server offers
// another agent (agent.switch.offered) and the app puts the offer on a
// card at the foot of the pane — Continue in Codex, Wait for the reset,
// Choose… — or, with [agents] switch_at_limit, counts down to the switch
// the server makes itself. Account moves come first: the server offers
// this only when no account of the agent has room.

/// The server's capability for offering another agent at a plan limit.
public let agentSwitchLimitCapability = "agent.switch.limit.v1"

/// A switch offered because an agent is out of plan on every account.
public struct AgentSwitchOffer: Decodable, Equatable, Sendable {
    public var pane: String
    public var from: String
    /// The agent offered; nil when none here can carry it on (why says).
    public var to: String?
    public var crossed: String?
    public var accounts: [AccountRank]?
    public var resetsAt: Date?
    /// The server switches by itself at `at` unless called off.
    public var auto: Bool?
    public var at: Date?
    public var setting: String?
    public var why: String?
    /// The offer is withdrawn.
    public var gone: Bool?
    enum CodingKeys: String, CodingKey {
        case pane, from, to, crossed, accounts, auto, at, setting, why, gone
        case resetsAt = "resets_at"
    }
    public init(pane: String, from: String, to: String? = nil, crossed: String? = nil, accounts: [AccountRank]? = nil, resetsAt: Date? = nil,
                auto: Bool? = nil, at: Date? = nil, setting: String? = nil, why: String? = nil, gone: Bool? = nil) {
        self.pane = pane
        self.from = from
        self.to = to
        self.crossed = crossed
        self.accounts = accounts
        self.resetsAt = resetsAt
        self.auto = auto
        self.at = at
        self.setting = setting
        self.why = why
        self.gone = gone
    }
}

/// An offer on its card: the pane keyed as the app keys panes, and what
/// would cross once the preview has said.
public struct LimitOffer: Equatable, Sendable {
    public var pane: String
    public var offer: AgentSwitchOffer
    public var preview: AgentSwitchResult?
    public init(pane: String, offer: AgentSwitchOffer, preview: AgentSwitchResult? = nil) {
        self.pane = pane
        self.offer = offer
        self.preview = preview
    }

    /// Seconds left before the server switches by itself; nil for an
    /// offer that waits to be taken.
    public func secondsLeft(now: Date = Date()) -> Int? {
        guard offer.auto == true, let at = offer.at else { return nil }
        return max(Int(at.timeIntervalSince(now).rounded(.up)), 0)
    }
}

/// "in 2h 10m", "in 6m", "in a minute".
public func resetsIn(_ d: TimeInterval) -> String {
    if d < 60 { return "in a minute" }
    if d < 3600 { return "in \(Int(d / 60))m" }
    return "in \(Int(d / 3600))h \(Int(d / 60) % 60)m"
}

/// How full each account is and when the first turns over:
/// "work 100% · default 97% · resets in 2h 10m".
public func limitFullness(_ o: AgentSwitchOffer, now: Date = Date()) -> String {
    var parts = (o.accounts ?? []).filter { $0.reported == true }.map { "\($0.label) \(Int((100 - $0.room).rounded()))%" }
    if let r = o.resetsAt, r > now { parts.append("resets " + resetsIn(r.timeIntervalSince(now))) }
    return parts.joined(separator: " · ")
}

/// The card's heading.
public func limitHeading(_ o: AgentSwitchOffer, agents: [AgentAvailability] = []) -> String {
    "\(agentTitle(o.from, among: agents)) is out of plan on every account"
}

/// The card's sentences under its heading, in order.
public func limitLines(_ c: LimitOffer, agents: [AgentAvailability] = [], now: Date = Date()) -> [String] {
    let o = c.offer
    let from = agentTitle(o.from, among: agents), to = agentTitle(o.to ?? "", among: agents)
    let how = o.crossed == SwitchWay.handoff
        ? "It is handed the conversation as a document for its first message."
        : "It takes the whole conversation and your last message, and carries on."
    var out: [String] = []
    if let left = c.secondsLeft(now: now) {
        out.append("Continuing in \(to) \(left > 0 ? "in \(left)s" : "now"). \(how)")
    } else {
        out.append("Continue in \(to)? \(how)")
    }
    if let cr = c.preview?.crossing {
        var carries = [counted(cr.messages, "message"), counted(cr.commands, "command"), counted(cr.notes, "note")]
        if cr.plan == true { carries.append("the plan") }
        var s = "Carries " + carries.joined(separator: " · ")
        if !cr.fits || (cr.shortened ?? 0) > 0 { s += " · \(counted(cr.shortened ?? 0, "step")) shortened to fit" }
        out.append(s)
    }
    out.append("\(from)'s session stays as it is. When \(from) has room again, Switch Back is offered.")
    if let why = o.why, !why.isEmpty { out.append("Note: \(why).") }
    return out
}

/// The card's buttons, the first the default.
public func limitButtons(_ o: AgentSwitchOffer, agents: [AgentAvailability] = []) -> [String] {
    if o.auto == true { return ["Continue Now", "Cancel", "Choose…"] }
    return ["Continue in \(agentTitle(o.to ?? "", among: agents))", "Wait for the Reset", "Choose…"]
}

/// What a switch the server made at a plan limit is said as.
public func limitSwitchedText(_ r: AgentSwitchResult, agents: [AgentAvailability] = []) -> String {
    let from = agentTitle(r.from, among: agents), to = agentTitle(r.to, among: agents)
    if let err = r.error { return "The switch to \(to) at the plan limit failed: \(err)" }
    let how = r.crossed == SwitchWay.handoff ? "with the handoff document" : "with the whole conversation"
    return "\(from) was out of plan on every account: switched to \(to), \(how). Switch Back is above it."
}
