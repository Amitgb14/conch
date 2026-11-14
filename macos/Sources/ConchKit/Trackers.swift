import Foundation

// How each tracker stands (ticket.check, ticket.check.v1): what Settings →
// Trackers shows beside the key, so a key that does not work is found
// there rather than in the middle of New Task.

/// A Linear team or a Jira project the key can see.
public struct TrackerArea: Codable, Equatable, Sendable {
    public var key: String
    public var name: String?
    public init(key: String, name: String? = nil) {
        self.key = key
        self.name = name
    }
}

/// One tracker: github, linear or jira.
public struct TrackerState: Codable, Equatable, Sendable {
    public var tracker: String
    public var state: String
    public var who: String?
    public var email: String?
    public var workspace: String?
    public var areas: [TrackerArea]?
    public var area: String?
    public var open: Int?
    public var mine: Int?
    public var more: Bool?
    public var keyFrom: String?
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case tracker, state, who, email, workspace, areas, area, open, mine, more, error
        case keyFrom = "key_from"
    }

    public init(tracker: String, state: String, who: String? = nil, email: String? = nil, workspace: String? = nil,
                areas: [TrackerArea]? = nil, area: String? = nil, open: Int? = nil, mine: Int? = nil, more: Bool? = nil,
                keyFrom: String? = nil, error: String? = nil) {
        self.tracker = tracker
        self.state = state
        self.who = who
        self.email = email
        self.workspace = workspace
        self.areas = areas
        self.area = area
        self.open = open
        self.mine = mine
        self.more = more
        self.keyFrom = keyFrom
        self.error = error
    }
}

/// One [[trackers.projects]] entry and how many are open there.
public struct TrackerProjectState: Codable, Equatable, Sendable {
    public var project: String
    public var tracker: String
    public var area: String?
    public var open: Int?
    public var more: Bool?
    public var error: String?
}

public struct TicketCheckResult: Codable, Equatable, Sendable {
    public var trackers: [TrackerState]
    public var projects: [TrackerProjectState]?
}

/// The badge a tracker's card wears.
public enum TrackerTone: Equatable, Sendable { case ok, off, err }

public func trackerBadge(_ t: TrackerState?) -> (text: String, tone: TrackerTone) {
    switch t?.state {
    case "connected": return ("Connected", .ok)
    case "error":
        // A refused key is a sign-in; anything else (a site that did not
        // answer, a half-filled card) is not reaching it.
        let e = (t?.error ?? "").lowercased()
        return (e.contains("refused the key") || e.contains("not signed in") ? "Couldn't sign in" : "Not reachable", .err)
    case "off": return ("Off", .off)
    default: return ("Not checked", .off)
    }
}

/// Who the key is, in a line: "Amit Ghadge · amit@acme.dev · Acme".
public func trackerWho(_ t: TrackerState) -> String {
    [t.who, t.email, t.workspace].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
}

/// A count as said: 23, or 100+ when there were at least that many.
public func trackerCount(_ n: Int?, more: Bool?) -> String {
    "\(n ?? 0)" + (more == true ? "+" : "")
}

/// The facts under a connected card: open tickets where they are listed,
/// those assigned to you, and the teams or projects the key can see.
public func trackerFacts(_ t: TrackerState) -> [(value: String, label: String)] {
    guard t.state == "connected" else { return [] }
    var out: [(String, String)] = []
    let area = t.area ?? ""
    if t.open != nil || !area.isEmpty {
        out.append((trackerCount(t.open, more: t.more), area.isEmpty ? "open" : "open in \(area)"))
        out.append(("\(t.mine ?? 0)", "assigned to you"))
    }
    if let a = t.areas, !a.isEmpty {
        out.append(("\(a.count)", t.tracker == "jira" ? (a.count == 1 ? "project" : "projects") : (a.count == 1 ? "team" : "teams")))
    }
    return out
}

/// Where the key is read from, as the card says it: never the key.
public func trackerKeyNote(_ t: TrackerState) -> String? {
    guard let from = t.keyFrom, !from.isEmpty else { return nil }
    switch from {
    case "gh": return "Signed in through gh (gh auth login); conch keeps no key for GitHub."
    case "config.toml": return "The key is kept in config.toml as it is."
    default:
        if from.hasPrefix("$") { return "The key is read from \(from)." }
        return "The key is read from the \(from)."
    }
}

/// A project entry's state in a few words: "23 open", or what is wrong.
public func trackerProjectNote(_ p: TrackerProjectState) -> (text: String, ok: Bool) {
    if let e = p.error, !e.isEmpty { return (e, false) }
    return (trackerCount(p.open, more: p.more) + " open", true)
}
