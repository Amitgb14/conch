import Foundation

// The Tickets view (ticket.overview, ticket.overview.v1): every project's
// GitHub issues and Linear or Jira tickets, with what conch is doing about
// each. What it is made of, and the filtering and wording the view uses,
// are here so they can be tested.

public struct TicketDoing: Decodable, Equatable, Sendable {
    public var state: String?
    public var pane: String?
    public var branch: String?
    public var schedule: String?
    public var note: String?
    public var pr: PRInfo?
    public init(state: String? = nil, pane: String? = nil, branch: String? = nil, schedule: String? = nil, note: String? = nil, pr: PRInfo? = nil) {
        self.state = state
        self.pane = pane
        self.branch = branch
        self.schedule = schedule
        self.note = note
        self.pr = pr
    }
}

public struct TicketRow: Decodable, Equatable, Sendable, Identifiable {
    public var source: String
    public var key: String
    public var number: Int?
    public var title: String
    public var url: String?
    public var status: String?
    public var labels: [String]?
    public var mine: Bool?
    public var doing: TicketDoing?
    public var id: String { source + ":" + key }

    public init(source: String, key: String, number: Int? = nil, title: String, url: String? = nil, status: String? = nil,
                labels: [String]? = nil, mine: Bool? = nil, doing: TicketDoing? = nil) {
        self.source = source
        self.key = key
        self.number = number
        self.title = title
        self.url = url
        self.status = status
        self.labels = labels
        self.mine = mine
        self.doing = doing
    }

    /// How the key reads: #12 for an issue, ENG-142 for a ticket.
    public var shown: String { source == "github" ? "#" + key : key }
    /// What a task is started from: an issue's number or a ticket's key.
    public var issue: Int? { source == "github" ? number : nil }
    public var ticket: String? { source == "github" ? nil : key }
}

public struct TicketProject: Decodable, Equatable, Sendable, Identifiable {
    public var projectID: String?
    public var name: String
    public var sources: [String]?
    public var tickets: [TicketRow]
    public var errors: [String]?
    public var id: String { (projectID ?? "") + "|" + name }

    enum CodingKeys: String, CodingKey {
        case projectID = "project_id", name, sources, tickets, errors
    }

    public init(projectID: String?, name: String, sources: [String]? = nil, tickets: [TicketRow], errors: [String]? = nil) {
        self.projectID = projectID
        self.name = name
        self.sources = sources
        self.tickets = tickets
        self.errors = errors
    }
}

public struct TicketOverview: Decodable, Equatable, Sendable {
    public var projects: [TicketProject]
    public init(projects: [TicketProject]) { self.projects = projects }
}

/// The views down the side of the Tickets window.
public enum TicketFilter: Hashable, Sendable {
    case all, mine, ready, needsInfo, onIt
    case project(String)
    case source(String)
}

/// The labels that say a ticket is ready for conch, or needs somebody:
/// intake's own, and what triage gives.
public func ticketIsReady(_ t: TicketRow, readyLabels: Set<String>) -> Bool {
    (t.labels ?? []).contains { readyLabels.contains($0) }
}

public func ticketNeedsInfo(_ t: TicketRow) -> Bool {
    (t.labels ?? []).contains { $0 == "needs-info" || $0 == "too-big" }
}

public func ticketOnIt(_ t: TicketRow) -> Bool {
    let s = t.doing?.state ?? ""
    return !s.isEmpty && s != "merged"
}

/// Whether a ticket is in a view and matches what is typed: its key, title
/// or labels, without regard to case.
public func ticketMatches(_ t: TicketRow, _ f: TicketFilter, query: String, readyLabels: Set<String>, project: TicketProject) -> Bool {
    switch f {
    case .all: break
    case .mine: if t.mine != true { return false }
    case .ready: if !ticketIsReady(t, readyLabels: readyLabels) { return false }
    case .needsInfo: if !ticketNeedsInfo(t) { return false }
    case .onIt: if !ticketOnIt(t) { return false }
    case .project(let id): if project.id != id { return false }
    case .source(let s): if t.source != s { return false }
    }
    let q = query.trimmingCharacters(in: .whitespaces).lowercased()
    if q.isEmpty { return true }
    return t.shown.lowercased().contains(q) || t.title.lowercased().contains(q) || (t.labels ?? []).contains { $0.lowercased().contains(q) }
}

/// The projects with only the tickets a view shows; a project left with
/// none is kept only when it has something to say (an error), or when the
/// view is that project.
public func filterTickets(_ ov: TicketOverview, _ f: TicketFilter, query: String, readyLabels: Set<String>) -> [TicketProject] {
    ov.projects.compactMap { p in
        var q = p
        q.tickets = p.tickets.filter { ticketMatches($0, f, query: query, readyLabels: readyLabels, project: p) }
        if !q.tickets.isEmpty { return q }
        if case .project(let id) = f, id == p.id { return q }
        if !(p.errors ?? []).isEmpty, query.isEmpty, f == .all { return q }
        return nil
    }
}

/// How many tickets a view holds, for its count in the side.
public func ticketCount(_ ov: TicketOverview, _ f: TicketFilter, readyLabels: Set<String>) -> Int {
    filterTickets(ov, f, query: "", readyLabels: readyLabels).reduce(0) { $0 + $1.tickets.count }
}

/// The tone a ticket's state is drawn in.
public enum DoingTone: Equatable, Sendable { case work, wait, pr, queued, done, none }

/// What conch is doing about a ticket, in a few words, and its tone.
public func ticketDoingText(_ d: TicketDoing?) -> (text: String, tone: DoingTone) {
    guard let d, let s = d.state, !s.isEmpty else {
        if let b = d?.branch, !b.isEmpty { return ("a branch: \(b)", .none) }
        return ("", .none)
    }
    switch s {
    case "waiting": return ("waiting on you", .wait)
    case "working": return ("agent working", .work)
    case "pr":
        var t = "PR #\(d.pr?.number ?? 0)"
        switch d.pr?.checks {
        case "pass": t += " · CI green"
        case "fail": t += " · CI failed"
        case "pending": t += " · CI running"
        default: break
        }
        return (t, .pr)
    case "queued":
        if let n = d.note, !n.isEmpty { return ("queued · \(n)", .queued) }
        return ("queued", .queued)
    case "idle": return ("agent idle", .none)
    case "merged": return ("merged" + (d.pr.map { " · PR #\($0.number)" } ?? ""), .done)
    default: return (s, .none)
    }
}

/// The values of a table of strings in config.toml — [verify.intake], or
/// `intake = { r1 = "ready" }` inline under [verify] — for the labels that
/// mean ready. Only quoted values; anything else is not a label.
public func tomlTableValues(_ section: String, _ key: String, in toml: String?) -> [String] {
    guard let toml else { return [] }
    var out: [String] = []
    var current = ""
    func quoted(_ s: Substring) -> [String] {
        var found: [String] = []
        var rest = s
        while let open = rest.firstIndex(of: "\""), let close = rest[rest.index(after: open)...].firstIndex(of: "\"") {
            found.append(String(rest[rest.index(after: open)..<close]))
            rest = rest[rest.index(after: close)...]
        }
        return found
    }
    for raw in toml.split(whereSeparator: \.isNewline) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("#") || line.isEmpty { continue }
        if line.hasPrefix("[") {
            current = String(line.dropFirst().prefix { $0 != "]" }).trimmingCharacters(in: .whitespaces)
            continue
        }
        guard let eq = line.firstIndex(of: "=") else { continue }
        let name = line[..<eq].trimmingCharacters(in: .whitespaces)
        let value = line[line.index(after: eq)...]
        if current == section + "." + key {
            // r1a2b3c = "ready": the value, not the key.
            if let v = quoted(value).first { out.append(v) }
        } else if current == section, name == key, value.contains("{") {
            // intake = { r1 = "ready", r2 = "go" }: every second quoted
            // string is a value only when keys are quoted; take each
            // value after an =.
            for part in value.split(separator: ",") {
                if let e = part.firstIndex(of: "="), let v = quoted(part[part.index(after: e)...]).first { out.append(v) }
            }
        }
    }
    return out.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
}
