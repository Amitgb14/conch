import Foundation

/// A project's saved agent conversations, as the server lists them
/// (session.list): what the Sessions row opens, resumed with
/// session.resume and deleted with session.delete — the TUI's Sessions
/// view, in a tab of the app's own.
public struct SessionInfo: Decodable, Equatable, Identifiable, Sendable {
    public var agent: String
    /// The agent's own id for it; "" for an interrupted run whose
    /// conversation was not found.
    public var sessionID: String?
    public var dir: String
    public var branch: String?
    public var title: String
    public var started: Date?
    public var updated: Date?
    /// The pane it is open in, when it is.
    public var paneID: String?
    /// It was running when conch's server stopped and hasn't been resumed.
    public var interrupted: Bool?
    public var costUSD: Double?
    /// Kept to come back to (session.star.v1): conch's mark, not the agent's.
    public var starred: Bool?
    /// The words around a match inside the conversation, in a search's results.
    public var snippet: String?

    enum CodingKeys: String, CodingKey {
        case agent, dir, branch, title, started, updated, interrupted, starred, snippet
        case sessionID = "id"
        case paneID = "pane_id"
        case costUSD = "cost_usd"
    }

    public init(agent: String, sessionID: String? = nil, dir: String, branch: String? = nil, title: String,
                updated: Date? = nil, paneID: String? = nil, interrupted: Bool? = nil) {
        self.agent = agent
        self.sessionID = sessionID
        self.dir = dir
        self.branch = branch
        self.title = title
        self.updated = updated
        self.paneID = paneID
        self.interrupted = interrupted
    }

    public var id: String { agent + "\u{0}" + (sessionID ?? "") + "\u{0}" + dir }

    /// What it is called in the list: its title, or the agent's name.
    public var name: String { title.trimmingCharacters(in: .whitespaces).isEmpty ? agent : title }
}

public struct SessionListResult: Decodable, Sendable {
    public var sessions: [SessionInfo]?
    /// An agent whose sessions come from another program had not answered:
    /// the list is missing some, and asking again in a moment finds them.
    public var partial: Bool?
}

/// A sessions tab's key in the layout: "sessions|machine|project".
public func sessionsKey(_ machine: String, _ project: String) -> String { "sessions|\(machine)|\(project)" }

public func parseSessionsKey(_ k: String) -> (machine: String, project: String)? {
    let parts = k.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 3, parts[0] == "sessions", !parts[2].isEmpty else { return nil }
    return (parts[1], parts[2])
}

/// The sessions a search keeps: every word in the title, branch or agent,
/// in any case. Interrupted ones first, then the newest.
public func filterSessions(_ list: [SessionInfo], _ query: String) -> [SessionInfo] {
    let words = query.lowercased().split(separator: " ").map(String.init)
    let kept = list.filter { s in
        let hay = (s.title + " " + (s.branch ?? "") + " " + s.agent).lowercased()
        return words.allSatisfy { hay.contains($0) }
    }
    return orderSessions(kept)
}

/// Interrupted first, then starred, then newest — what is owed a look
/// above what was kept, above the rest.
public func orderSessions(_ list: [SessionInfo]) -> [SessionInfo] {
    list.sorted {
        if ($0.interrupted ?? false) != ($1.interrupted ?? false) { return $0.interrupted ?? false }
        if ($0.starred ?? false) != ($1.starred ?? false) { return $0.starred ?? false }
        return ($0.updated ?? .distantPast) > ($1.updated ?? .distantPast)
    }
}

/// The interrupted conversations a "resume all" brings back: not open in a
/// pane, and with a conversation to resume.
public func resumable(_ list: [SessionInfo]) -> [SessionInfo] {
    list.filter { $0.interrupted == true && ($0.paneID ?? "").isEmpty }
}

/// Whether a query is worth asking the server to look inside the
/// conversations for: three letters or more.
public func searchesContents(_ query: String) -> Bool {
    query.trimmingCharacters(in: .whitespaces).count >= 3
}

/// How long ago, as a list says it: "now", "5m ago", "3h ago", "2d ago",
/// then the date.
public func agoText(_ date: Date?, now: Date = Date()) -> String {
    guard let date, date.timeIntervalSince1970 > 0 else { return "" }
    let s = max(Int(now.timeIntervalSince(date)), 0)
    switch s {
    case ..<60: return "now"
    case ..<3600: return "\(s / 60)m ago"
    case ..<86400: return "\(s / 3600)h ago"
    case ..<(86400 * 14): return "\(s / 86400)d ago"
    default:
        let f = DateFormatter()
        f.dateFormat = "d MMM yyyy"
        return f.string(from: date)
    }
}
