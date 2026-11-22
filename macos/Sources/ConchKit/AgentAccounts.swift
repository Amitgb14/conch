import Foundation

// An agent's accounts (agent.accounts.v1): a work and a home subscription
// of Claude Code or Codex, each a folder of its own the agent signs in to
// once. The server keeps them per machine; Settings → Agents lists them.

/// The server's capability for agent accounts.
public let accountsCapability = "agent.accounts.v1"

/// One account (proto.AccountInfo). The agent's own is `isDefault`, with
/// the name "default" and no folder of conch's.
public struct AgentAccount: Decodable, Equatable, Sendable, Identifiable {
    public var agent: String
    public var name: String
    public var dir: String?
    public var isDefault: Bool?
    public var limits: PlanLimits?
    /// How many agents run on it now.
    public var agents: Int?
    /// Whether it has been signed in to; nil when the server cannot tell
    /// or is older.
    public var signedIn: Bool?
    public var id: String { agent + "|" + (isDefault == true ? "default" : name) }
    enum CodingKeys: String, CodingKey {
        case agent, name, dir, limits, agents
        case isDefault = "default"
        case signedIn = "signed_in"
    }
    public init(agent: String, name: String, dir: String? = nil, isDefault: Bool? = nil, limits: PlanLimits? = nil, agents: Int? = nil, signedIn: Bool? = nil) {
        self.signedIn = signedIn
        self.agent = agent
        self.name = name
        self.dir = dir
        self.isDefault = isDefault
        self.limits = limits
        self.agents = agents
    }
}

/// A folder whose agents start on an account (proto.AccountPin).
public struct AgentAccountPin: Decodable, Equatable, Sendable, Identifiable {
    public var path: String
    public var account: String
    public var id: String { path }
    public init(path: String, account: String) {
        self.path = path
        self.account = account
    }
}

/// account.list's answer (proto.AccountList).
public struct AgentAccountList: Decodable, Equatable, Sendable {
    public var accounts: [AgentAccount]?
    public var pins: [AgentAccountPin]?
    /// The percentage a new agent's account must be under, 0 for off.
    public var failoverAt: Int?
    /// The agents that can have more than one account.
    public var agents: [String]?
    /// Each agent's own failover line, where one has one
    /// (account.failover.agent.v1); the rest follow failoverAt.
    public var failoverFor: [String: Int]?
    enum CodingKeys: String, CodingKey {
        case accounts, pins, agents
        case failoverAt = "failover_at"
        case failoverFor = "failover_for"
    }
    public init(accounts: [AgentAccount]? = nil, pins: [AgentAccountPin]? = nil, failoverAt: Int? = nil, agents: [String]? = nil, failoverFor: [String: Int]? = nil) {
        self.failoverFor = failoverFor
        self.accounts = accounts
        self.pins = pins
        self.failoverAt = failoverAt
        self.agents = agents
    }
}

public struct AccountAddParams: Encodable, Sendable {
    public var agent: String
    public var name: String
    public var dir: String?
    public init(agent: String, name: String, dir: String? = nil) {
        self.agent = agent
        self.name = name
        self.dir = dir
    }
}

public struct AccountRefParams: Encodable, Sendable {
    public var agent: String
    public var name: String
    public init(agent: String, name: String) {
        self.agent = agent
        self.name = name
    }
}

/// account.pin: an empty account unpins the path.
public struct AccountPinParams: Encodable, Sendable {
    public var path: String
    public var account: String?
    public init(path: String, account: String? = nil) {
        self.path = path
        self.account = account
    }
}

public struct AccountFailoverParams: Encodable, Sendable {
    public var at: Int
    /// One agent's own line (account.failover.agent.v1); with shared, that
    /// agent goes back to every agent's line.
    public var agent: String?
    public var shared: Bool?
    public init(at: Int, agent: String? = nil, shared: Bool? = nil) {
        self.at = at
        self.agent = agent
        self.shared = shared
    }
}

/// The server's capability for a failover line per agent.
public let accountFailoverAgentCapability = "account.failover.agent.v1"
/// The server's capability for usage.history by account, with buckets.
public let usageAccountsCapability = "usage.history.accounts.v1"

/// One agent's accounts, as the page groups them: its own first, then the
/// ones added, in the order the server gave.
public struct AgentAccountGroup: Equatable, Sendable, Identifiable {
    public var agent: String
    public var accounts: [AgentAccount]
    public var id: String { agent }
}

/// The accounts grouped by agent, in the order the server names the
/// agents (then any it did not), each with its own account first.
public func agentAccountGroups(_ list: AgentAccountList) -> [AgentAccountGroup] {
    let all = list.accounts ?? []
    var order = list.agents ?? []
    for a in all where !order.contains(a.agent) { order.append(a.agent) }
    return order.map { agent in
        let mine = all.filter { $0.agent == agent }
        return AgentAccountGroup(agent: agent, accounts: mine.filter { $0.isDefault == true } + mine.filter { $0.isDefault != true })
    }
}

/// An account's name as people see it: "default (its own)" for the agent's.
public func agentAccountLabel(_ a: AgentAccount) -> String {
    a.isDefault == true ? "default (its own)" : a.name
}

/// What an account has used, as `conch account list` says it: "5h 95% ·
/// 7d 18%", leaving out a window that has turned over; empty when it has
/// reported nothing yet.
public func agentAccountUsage(_ limits: PlanLimits?, now: Date = Date()) -> String {
    guard let l = limits else { return "" }
    var parts: [String] = []
    func add(_ label: String, _ w: PlanLimits.Window?) {
        guard let w else { return }
        guard windowLive(w.resetsAt, now: now) else { return }
        parts.append("\(label) \(Int(w.usedPct.rounded()))%")
    }
    add("5h", l.fiveHour)
    add("7d", l.week)
    add("spend", l.spend)
    return parts.joined(separator: " · ")
}

/// The fullest live window, in percent: what failover compares with its
/// line. Nil when the account has reported nothing.
public func agentAccountUsed(_ limits: PlanLimits?, now: Date = Date()) -> Double? {
    guard let l = limits else { return nil }
    let live = l.allWindows.filter { windowLive($0.resetsAt, now: now) }
    return live.map(\.usedPct).max()
}

/// Why a new account's name will not do, or nil when it will. The server
/// says the same; asking first keeps the sheet open with the reason.
public func agentAccountNameProblem(_ name: String, agent: String, existing: [AgentAccount]) -> String? {
    let n = name.trimmingCharacters(in: .whitespaces)
    if n.isEmpty { return "Give it a name, like work or home." }
    if n.lowercased() == "default" { return "default is the agent's own account." }
    // The server's rule (accounts.ValidName): a letter or digit, then up
    // to 31 of letters, digits, '.', '_' and '-'.
    if n.range(of: "^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$", options: .regularExpression) == nil {
        return "A name is one word of up to 32 letters, digits, '.', '_' or '-'."
    }
    if existing.contains(where: { $0.agent == agent && $0.isDefault != true && $0.name.lowercased() == n.lowercased() }) {
        return "\(agent) already has an account called \(n)."
    }
    return nil
}

/// The failover lines Settings offers, 0 meaning off.
public let agentAccountFailoverChoices = [0, 80, 90, 95]

/// How the failover line reads.
public func agentAccountFailoverLabel(_ at: Int) -> String {
    at <= 0 ? "Off" : "At \(at)%"
}

/// The pins that name an account of this name (any agent's: a pin is by
/// name), so removing it can say which folders lose theirs.
public func agentAccountPins(named name: String, in list: AgentAccountList) -> [AgentAccountPin] {
    (list.pins ?? []).filter { $0.account == name }
}

/// What an account row offers for signing in: nothing for the agent's
/// own (it is signed in as it always was), a plain "Signed in" once it
/// is, and Sign In when it is not or the server cannot say.
public enum AgentAccountSignIn: Equatable, Sendable {
    case own, signedIn, signIn
}

public func agentAccountSignIn(_ a: AgentAccount) -> AgentAccountSignIn {
    if a.isDefault == true { return .own }
    return a.signedIn == true ? .signedIn : .signIn
}

/// A failover line typed in: a whole number from 1 to 100 (a trailing %
/// allowed), else nil. 0 is Off, which the menu has.
public func agentAccountFailoverValue(_ s: String) -> Int? {
    var t = s.trimmingCharacters(in: .whitespaces)
    if t.hasSuffix("%") { t.removeLast() }
    guard let v = Int(t.trimmingCharacters(in: .whitespaces)), (1...100).contains(v) else { return nil }
    return v
}
