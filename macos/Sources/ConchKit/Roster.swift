import Foundation

// The Cel style's roster (AppStyle.cel): every agent and terminal the
// workspace shows as a card across the top of the window, and a short
// log of what they last did. Built only from what the app already reads
// — panes, their agents' states and since when.

/// One card of the roster.
public struct RosterCard: Equatable, Sendable {
    /// The pane's key ("machine|id").
    public var key: String
    public var title: String
    /// Where it is: project and branch, or a machine's name.
    public var place: String
    /// The agent in it; nil for a terminal.
    public var agent: String?
    public var state: AgentState?
    public var since: Date?
    public var running: Bool

    public init(key: String, title: String, place: String = "", agent: String? = nil, state: AgentState? = nil,
                since: Date? = nil, running: Bool = true) {
        self.key = key
        self.title = title
        self.place = place
        self.agent = agent
        self.state = state
        self.since = since
        self.running = running
    }

    public init(key: String, pane p: PaneInfo, place: String) {
        self.init(key: key, title: p.displayName, place: place, agent: p.agent?.name, state: p.agent?.state,
                  since: p.agent?.since, running: p.running)
    }

    /// Where it goes in the roster: what needs you first, then what is at
    /// work, what finished, agents at rest, terminals, and what has exited.
    var rank: Int {
        if !running { return 5 }
        guard agent != nil else { return 4 }
        switch state {
        case .blocked: return 0
        case .working: return 1
        case .done: return 2
        default: return 3
        }
    }

    /// The card's bar: how full, and the word under it.
    public var bar: (fill: Double, word: String) {
        if !running { return (0, "exited") }
        guard agent != nil else { return (1, "terminal") }
        switch state {
        case .blocked: return (1, "waiting on you")
        case .working: return (0.6, "working")
        case .done: return (1, "done")
        default: return (0.12, "idle")
        }
    }
}

/// The roster in its order (RosterCard.rank), cards of one rank by name,
/// so a card moves only when its state does.
public func roster(_ cards: [RosterCard]) -> [RosterCard] {
    cards.sorted { ($0.rank, $0.title.lowercased(), $0.key) < ($1.rank, $1.title.lowercased(), $1.key) }
}

/// A line of the roster's log: an agent and what it last did, newest first.
public struct ActivityLine: Equatable, Sendable {
    public var key: String
    public var agent: String
    public var text: String
    public var since: Date
}

/// What the agents last did, the newest `limit`: each one's state and
/// since when. Terminals and agents with no time say nothing.
public func activity(_ cards: [RosterCard], limit: Int = 6) -> [ActivityLine] {
    cards.compactMap { c -> ActivityLine? in
        guard let a = c.agent, let t = c.since else { return nil }
        let what: String
        if !c.running {
            what = "exited"
        } else {
            switch c.state {
            case .blocked: what = "is waiting on you"
            case .working: what = "started working"
            case .done: what = "is done"
            default: what = "went idle"
            }
        }
        return ActivityLine(key: c.key, agent: a, text: "\(c.title) \(what)", since: t)
    }
    .sorted { ($0.since, $0.key) > ($1.since, $1.key) }
    .prefix(max(limit, 0)).map { $0 }
}

/// How long ago, as the log says it: now, 4m, 3h, 2d.
public func shortAgo(_ since: Date, _ now: Date) -> String {
    let s = max(now.timeIntervalSince(since), 0)
    if s < 60 { return "now" }
    if s < 3600 { return "\(Int(s / 60))m" }
    if s < 86400 { return "\(Int(s / 3600))h" }
    return "\(Int(s / 86400))d"
}

/// A project's two letters for the rail: its name's first letter and the
/// next after a separator, or its first two ("api" → "ap", "my-app" →
/// "ma"); "?" for none.
public func railLetters(_ name: String) -> String {
    let words = name.split(whereSeparator: { "-_ ./".contains($0) }).filter { !$0.isEmpty }
    if words.count >= 2, let a = words[0].first, let b = words[1].first { return String([a, b]).lowercased() }
    let n = name.filter { !"-_ ./".contains($0) }
    return n.isEmpty ? "?" : String(n.prefix(2)).lowercased()
}
