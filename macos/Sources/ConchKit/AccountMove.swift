import Foundation

// Moving a running agent to another of its accounts (agent.move.v1): its
// conversation carried to that account's folder and resumed there, in a
// pane that takes the old one's place. By hand from an agent's menu; by
// itself when an agent's plan runs out, after a few seconds anyone can
// cancel; and back again once the account it came off has room.

/// The server's capability for moving agents between accounts.
public let agentMoveCapability = "agent.move.v1"

/// A move the server has scheduled (agent.moving).
public struct AgentMoving: Decodable, Equatable, Sendable {
    public var pane: String
    public var agent: String
    public var from: String
    public var to: String
    public var at: Date
    public var reason: String
    /// The background commands that stop with it, when the server waited
    /// for them and they went on.
    public var stops: [String]?
    public init(pane: String, agent: String, from: String, to: String, at: Date, reason: String, stops: [String]? = nil) {
        self.stops = stops
        self.pane = pane
        self.agent = agent
        self.from = from
        self.to = to
        self.at = at
        self.reason = reason
    }
}

/// A move done (agent.moved).
public struct AgentMoved: Decodable, Equatable, Sendable {
    public var pane: String
    public var replaces: String
    public var agent: String
    public var from: String
    public var to: String
    public var reason: String
}

/// One account as the server weighs it for a move (account.suggest).
public struct AccountRank: Decodable, Equatable, Sendable, Identifiable {
    /// "" for the agent's own.
    public var account: String
    public var room: Double
    public var reported: Bool?
    public var signedIn: Bool?
    public var agents: Int?
    public var resetsAt: Date?
    public var usable: Bool
    public var why: String?
    public var id: String { account }
    enum CodingKeys: String, CodingKey {
        case account, room, reported, agents, usable, why
        case signedIn = "signed_in"
        case resetsAt = "resets_at"
    }
    public init(account: String, room: Double, reported: Bool? = nil, signedIn: Bool? = nil, agents: Int? = nil, resetsAt: Date? = nil, usable: Bool, why: String? = nil) {
        self.account = account
        self.room = room
        self.reported = reported
        self.signedIn = signedIn
        self.agents = agents
        self.resetsAt = resetsAt
        self.usable = usable
        self.why = why
    }

    /// Its name as people see it: "default" for the agent's own.
    public var label: String { account.isEmpty ? "default" : account }
}

public struct AccountSuggestion: Decodable, Equatable, Sendable {
    public var agent: String
    public var accounts: [AccountRank]
}

public struct AgentMoveParams: Encodable, Sendable {
    public var id: String
    public var account: String?
    public var `continue`: Bool?
    public var reason: String?
    /// Moves it even with background commands running, which stop.
    public var force: Bool?
    public init(id: String, account: String? = nil, continue: Bool? = nil, reason: String? = nil, force: Bool? = nil) {
        self.force = force
        self.id = id
        self.account = account
        self.continue = `continue`
        self.reason = reason
    }
}

public struct AgentMoveCancelParams: Encodable, Sendable {
    public var id: String
    public init(id: String) { self.id = id }
}

public struct AccountSuggestParams: Encodable, Sendable {
    public var agent: String
    public var exclude: String?
    public init(agent: String, exclude: String? = nil) {
        self.agent = agent
        self.exclude = exclude
    }
}

/// An account name as account.suggest and agent.move take it.
public func accountLabel(_ name: String?) -> String {
    (name ?? "").isEmpty ? "default" : name!
}

/// An agent's Switch Account ▸ menu: every other account, the one conch
/// would pick marked and first, those it cannot go to said why and doing
/// nothing; then Move Back when the account it came off has room; then
/// letting conch choose.
public func switchAccountMenu(pane key: String, on account: String?, ranks: [AccountRank], movedFrom: String?) -> RowMenuItem {
    let here = accountLabel(account)
    var kids: [RowMenuItem] = []
    let others = ranks.filter { $0.label != here }
    let pick = others.first { $0.usable }
    for r in others {
        var title = r.label + "  ·  " + (r.why ?? (r.usable ? "room" : "unavailable"))
        if r.id == pick?.id { title += "  ·  conch's pick" }
        kids.append(RowMenuItem(title, r.usable ? .moveAgent(pane: key, account: r.label) : .divider))
    }
    if let back = moveBack(movedFrom: movedFrom, ranks: ranks) {
        kids.append(.divider)
        kids.append(RowMenuItem("Move Back to \(back)", .moveAgent(pane: key, account: back)))
    }
    if pick != nil {
        kids.append(.divider)
        kids.append(RowMenuItem("Let conch Choose", .moveAgent(pane: key, account: nil)))
    }
    if kids.isEmpty {
        kids.append(RowMenuItem("No Other Account", .divider))
    }
    return RowMenuItem("Switch Account", .divider, children: kids)
}

/// The account an agent can move back to: the one it came off, once that
/// has room again; nil while it has none, or the agent never moved.
public func moveBack(movedFrom: String?, ranks: [AccountRank]) -> String? {
    guard let from = movedFrom, !from.isEmpty, let r = ranks.first(where: { $0.label == from }), r.usable,
          r.reported != true || r.room > 0 else { return nil }
    return from
}

/// Whole seconds until a scheduled move, never below zero.
public func secondsUntil(_ m: AgentMoving, now: Date = Date()) -> Int {
    max(Int(m.at.timeIntervalSince(now).rounded(.up)), 0)
}

/// What a move asked for by hand needs to say before it goes.
public struct MoveAsk: Identifiable, Equatable, Sendable {
    /// The pane, keyed as the app keys panes.
    public var pane: String
    public var agent: String
    public var from: String
    /// The account chosen; nil lets conch choose.
    public var to: String?
    public var id: String { pane + "→" + (to ?? "") }
    public init(pane: String, agent: String, from: String, to: String?) {
        self.pane = pane
        self.agent = agent
        self.from = from
        self.to = to
    }
}
