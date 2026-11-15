import Foundation

// Agent teams: agents grouped by what they work on — backend, UI, a
// report — across projects and branches. A team is put on a pane by hand
// (pane.team, PaneInfo.team) or by config.toml's [[teams]] rules; both
// sides read the rules alike (internal/config/teams.go), so the app, the
// TUI's CLI and a phone say the same team for an agent.

/// One [[teams]] entry.
public struct AgentTeam: Equatable, Sendable {
    public var name: String
    /// A colour name (AgentTeam.colors), "#rrggbb", or "" for one by place.
    public var color: String
    public var projects: [String]
    public var branches: [String]

    public init(name: String, color: String = "", projects: [String] = [], branches: [String] = []) {
        self.name = name
        self.color = color
        self.projects = projects
        self.branches = branches
    }

    /// The colours a team may name, as config.TeamColors lists them, in the
    /// order a team without one takes them by its place.
    public static let colors: [(name: String, rgb: RGB)] = [
        ("blue", RGB("#3E7BD6")), ("orange", RGB("#E07B2A")), ("purple", RGB("#8E5CD9")),
        ("teal", RGB("#2E9D9A")), ("green", RGB("#3C9D4E")), ("red", RGB("#D9534F")),
        ("pink", RGB("#D6589B")), ("yellow", RGB("#C9A227")), ("gray", RGB("#8A9399")),
    ]

    /// Set on a pane by hand: kept out of every team, whatever the rules say.
    public static let none = "-"
}

/// config.toml's [[teams]], in order; a team without a name is left out,
/// as Go leaves it out of matching.
public func parseTeams(toml: String) -> [AgentTeam] {
    var out: [AgentTeam] = []
    var cur: AgentTeam? = nil
    func flush() {
        if let c = cur, !c.name.isEmpty { out.append(c) }
        cur = nil
    }
    for raw in toml.components(separatedBy: "\n") {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            flush()
            if line.replacingOccurrences(of: " ", with: "").hasPrefix("[[teams]]") { cur = AgentTeam(name: "") }
            continue
        }
        guard cur != nil, !line.hasPrefix("#"), let eq = line.firstIndex(of: "=") else { continue }
        let k = line[..<eq].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        let v = String(line[line.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
        switch k {
        case "name": cur?.name = (tomlString(v) ?? "").trimmingCharacters(in: .whitespaces)
        case "color": cur?.color = (tomlString(v) ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        case "projects": cur?.projects = tomlStrings(v)
        case "branches": cur?.branches = tomlStrings(v)
        default: break
        }
    }
    flush()
    return out
}

/// The team an agent is in: the one it was put in by hand (set), else the
/// first team whose rule matches its project (by name, any case) or its
/// branch. nil is none. As config.TeamFor.
public func teamFor(_ teams: [AgentTeam], set: String?, project: String?, branch: String?) -> String? {
    switch set ?? "" {
    case AgentTeam.none: return nil
    case "": break
    case let s: return s
    }
    for t in teams where !t.name.isEmpty {
        if let p = project, !p.isEmpty, t.projects.contains(where: { $0.caseInsensitiveCompare(p) == .orderedSame }) { return t.name }
        if let b = branch, !b.isEmpty, t.branches.contains(where: { globMatch($0, b) }) { return t.name }
    }
    return nil
}

/// A branch pattern: * matches any run of characters, slashes included.
public func globMatch(_ pattern: String, _ s: String) -> Bool {
    guard pattern.contains("*") else { return pattern == s }
    let parts = pattern.components(separatedBy: "*")
    guard s.hasPrefix(parts[0]) else { return false }
    var rest = Substring(s.dropFirst(parts[0].count))
    let last = parts[parts.count - 1]
    for p in parts.dropFirst().dropLast() where !p.isEmpty {
        guard let r = rest.range(of: p) else { return false }
        rest = rest[r.upperBound...]
    }
    return rest.count >= last.count && rest.hasSuffix(last)
}

/// A team's colour: what its entry names, else by its place among the
/// teams; a team put on a pane by hand that config.toml does not list
/// takes one from its name, the same every time.
public func teamColor(_ name: String, in teams: [AgentTeam]) -> RGB {
    let palette = AgentTeam.colors
    if let i = teams.firstIndex(where: { $0.name == name }) {
        let c = teams[i].color
        if let named = palette.first(where: { $0.name == c }) { return named.rgb }
        if c.range(of: #"^#[0-9a-fA-F]{6}$"#, options: .regularExpression) != nil { return RGB(c) }
        return palette[i % palette.count].rgb
    }
    var h: UInt32 = 2166136261
    for b in name.utf8 { h = (h ^ UInt32(b)) &* 16777619 }
    return palette[Int(h % UInt32(palette.count))].rgb
}

/// The team names to offer for a pane: config.toml's, then any put on a
/// pane by hand that it does not list, in the order first seen.
public func teamNames(_ teams: [AgentTeam], panes: [PaneInfo]) -> [String] {
    var out = teams.map(\.name)
    for p in panes {
        if let t = p.team, !t.isEmpty, t != AgentTeam.none, !out.contains(t) { out.append(t) }
    }
    return out
}
