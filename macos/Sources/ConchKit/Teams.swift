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
    // An array that runs over several lines is gathered here until it
    // closes; its continuation lines are never read as headers or keys.
    var pending: (key: String, text: String)? = nil
    func setArray(_ k: String, _ v: [String]) {
        if k == "projects" { cur?.projects = v } else { cur?.branches = v }
    }
    for raw in toml.components(separatedBy: "\n") {
        if let p = pending {
            let text = p.text + "\n" + raw
            let a = scanTOMLArray(text)
            if a.closed { setArray(p.key, a.strings); pending = nil } else { pending = (p.key, text) }
            continue
        }
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            flush()
            if line.filter({ $0 != " " && $0 != "\t" }).hasPrefix("[[teams]]") { cur = AgentTeam(name: "") }
            continue
        }
        guard cur != nil, !line.hasPrefix("#"), let eq = line.firstIndex(of: "=") else { continue }
        let k = line[..<eq].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        let v = String(line[line.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
        switch k {
        case "name": cur?.name = (tomlString(v) ?? "").trimmingCharacters(in: .whitespaces)
        case "color": cur?.color = (tomlString(v) ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        case "projects", "branches":
            guard v.hasPrefix("[") else { setArray(k, []); continue }
            let a = scanTOMLArray(v)
            if a.closed { setArray(k, a.strings) } else { pending = (k, v) }
        default: break
        }
    }
    // An array never closed: what it held so far, as a reader that gives up
    // at the end of the file would have nothing better.
    if let p = pending { setArray(p.key, scanTOMLArray(p.text).strings) }
    flush()
    return out
}

/// A TOML array of strings from its opening "[", over as many lines as it
/// takes: its strings, and whether it closed. Comments (# outside a
/// string) run to the end of their line, so a quote or bracket in one is
/// not read as the array's.
func scanTOMLArray(_ v: String) -> (strings: [String], closed: Bool) {
    var out: [String] = [], cur = ""
    var quote: Character? = nil, escaped = false, comment = false, depth = 0
    for ch in v {
        if comment {
            if ch == "\n" { comment = false }
            continue
        }
        if let q = quote {
            if q == "\"" && escaped { cur.append(ch); escaped = false; continue }
            if q == "\"" && ch == "\\" { cur.append(ch); escaped = true; continue }
            if ch == q {
                out.append(q == "\"" ? unescapeTOML(cur) : cur)
                cur = ""
                quote = nil
            } else {
                cur.append(ch)
            }
            continue
        }
        switch ch {
        case "#": comment = true
        case "\"", "'": quote = ch
        case "[": depth += 1
        case "]":
            depth -= 1
            if depth == 0 { return (out, true) }
        default: break
        }
    }
    return (out, false)
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

// MARK: renaming and deleting a team

/// The [[teams]] entries of config.toml, as line ranges: where each starts
/// (its header) and ends (the next header, or the file's end), and the
/// line holding its name. A header inside an array that runs over several
/// lines is not one, as parseTeams reads them.
private func teamBlocks(_ lines: [String]) -> [(start: Int, end: Int, nameLine: Int?, name: String)] {
    var out: [(start: Int, end: Int, nameLine: Int?, name: String)] = []
    var cur: (start: Int, nameLine: Int?, name: String)? = nil
    var pending: String? = nil
    for (i, raw) in lines.enumerated() {
        if let p = pending {
            let text = p + "\n" + raw
            pending = scanTOMLArray(text).closed ? nil : text
            continue
        }
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            if let c = cur { out.append((c.start, i, c.nameLine, c.name)) }
            cur = line.filter({ $0 != " " && $0 != "\t" }).hasPrefix("[[teams]]") ? (i, nil, "") : nil
            continue
        }
        guard !line.hasPrefix("#"), let eq = line.firstIndex(of: "=") else { continue }
        let k = line[..<eq].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        let v = String(line[line.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
        if v.hasPrefix("["), !scanTOMLArray(v).closed { pending = v }
        if k == "name", cur != nil, cur?.nameLine == nil {
            cur?.nameLine = i
            cur?.name = (tomlString(v) ?? "").trimmingCharacters(in: .whitespaces)
        }
    }
    if let c = cur { out.append((c.start, lines.count, c.nameLine, c.name)) }
    return out
}

/// A TOML basic string.
func tomlQuoted(_ s: String) -> String {
    "\"" + s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\""
}

/// config.toml with every [[teams]] entry named from renamed to, its
/// colour and rules kept; every other line as it was.
public func renameTeamTOML(_ toml: String, from: String, to: String) -> String {
    var lines = toml.components(separatedBy: "\n")
    for b in teamBlocks(lines) where b.name == from {
        guard let at = b.nameLine else { continue }
        let indent = String(lines[at].prefix { $0 == " " || $0 == "\t" })
        lines[at] = indent + "name = " + tomlQuoted(to)
    }
    return lines.joined(separator: "\n")
}

/// config.toml with a [[teams]] entry of this name at its end, so a team
/// made from an agent's menu outlives its agents: one on the pane alone
/// went when they closed. Unchanged when it has one already, or for no
/// name or "-" (no team).
public func addTeamTOML(_ toml: String, name: String) -> String {
    let n = name.trimmingCharacters(in: .whitespaces)
    guard !n.isEmpty, n != AgentTeam.none, !parseTeams(toml: toml).contains(where: { $0.name == n }) else { return toml }
    var out = toml
    if !out.isEmpty && !out.hasSuffix("\n") { out += "\n" }
    if !out.isEmpty && !out.hasSuffix("\n\n") { out += "\n" }
    return out + "[[teams]]\nname = " + tomlQuoted(n) + "\n"
}

/// config.toml without the [[teams]] entries named name — each from its
/// header to the next one; every other line as it was.
public func removeTeamTOML(_ toml: String, name: String) -> String {
    var lines = toml.components(separatedBy: "\n")
    for b in teamBlocks(lines).reversed() where b.name == name {
        // The blank lines after it go too: the one before it already parts
        // what was above from what follows.
        lines.removeSubrange(b.start..<b.end)
    }
    return lines.joined(separator: "\n")
}

/// A pane somewhere in the window, with the name of its project there.
public struct TeamMemberCandidate: Equatable, Sendable {
    public var key: String
    public var pane: PaneInfo
    public var project: String?
    public init(key: String, pane: PaneInfo, project: String?) {
        self.key = key
        self.pane = pane
        self.project = project
    }
}

/// The agents in a team, on every machine and in every project: put in it
/// by hand or by its rules. Only agents: a terminal is in no team.
public func teamMembers(_ name: String, teams: [AgentTeam], panes: [TeamMemberCandidate]) -> [String] {
    guard !name.isEmpty else { return [] }
    return panes.filter { c in
        c.pane.agent != nil && teamFor(teams, set: c.pane.team, project: c.project, branch: c.pane.branch) == name
    }.map(\.key)
}

/// Why a team cannot take this name, or nil when it can: none, "No team"
/// (or "-", which means it), the same name, or another team's.
public func teamRenameProblem(_ to: String, from: String, existing: [String]) -> String? {
    let t = to.trimmingCharacters(in: .whitespaces)
    if t.isEmpty { return "A team needs a name." }
    if t == AgentTeam.none || t.caseInsensitiveCompare("No team") == .orderedSame { return "“\(t)” means no team: choose another name." }
    if t == from { return nil }
    if existing.contains(where: { $0 == t }) { return "There is already a team called “\(t)”." }
    return nil
}

/// The teams agents were put in by hand that config.toml does not list:
/// a team made from an agent's menu lives on the agent alone, and
/// Settings → Teams lists these after its own so it shows every team in
/// use. In the order first seen; "-" (no team) is none.
public func teamsOnlyOnAgents(listed: [String], panes: [PaneInfo]) -> [String] {
    var out: [String] = []
    for p in panes where p.agent != nil {
        guard let t = p.team?.trimmingCharacters(in: .whitespaces), !t.isEmpty, t != AgentTeam.none,
              !listed.contains(t), !out.contains(t) else { continue }
        out.append(t)
    }
    return out
}

/// A team's agents summed by kind, for Settings → Teams: one mark and a
/// count each rather than a mark per agent, and how many of them are at
/// work, which is what turns the spinner. In the order first seen; a pane
/// with no agent is left out.
public struct TeamAgentCount: Equatable, Sendable {
    public var agent: String
    public var count: Int
    public var working: Int
    public init(agent: String, count: Int, working: Int) {
        self.agent = agent
        self.count = count
        self.working = working
    }
}

public func teamAgentCounts(_ panes: [PaneInfo]) -> [TeamAgentCount] {
    var out: [TeamAgentCount] = []
    for p in panes {
        guard let a = p.agent else { continue }
        let w = a.state == .working ? 1 : 0
        if let i = out.firstIndex(where: { $0.agent == a.name }) {
            out[i].count += 1
            out[i].working += w
        } else {
            out.append(TeamAgentCount(agent: a.name, count: 1, working: w))
        }
    }
    return out
}

/// What a team's colour control shows for a value: "" is Auto (by its
/// place), a palette name is that dot, #rrggbb is a colour of your own,
/// and anything else — a name some later conch knows — is left as Auto
/// rather than lighting a dot it is not.
public enum TeamColorChoice: Equatable, Sendable {
    case auto
    case named(String)
    case custom(String)
}

public func teamColorChoice(_ value: String) -> TeamColorChoice {
    let v = value.trimmingCharacters(in: .whitespaces)
    if v.isEmpty { return .auto }
    if AgentTeam.colors.contains(where: { $0.name == v.lowercased() }) { return .named(v.lowercased()) }
    if v.range(of: #"^#[0-9a-fA-F]{6}$"#, options: .regularExpression) != nil { return .custom(v.uppercased()) }
    return .auto
}

/// Whether a colour is light enough that white on it is hard to read: by
/// its relative luminance, as WCAG weighs the channels.
public func teamColorIsLight(_ c: RGB) -> Bool {
    func lin(_ x: Double) -> Double { x <= 0.03928 ? x / 12.92 : pow((x + 0.055) / 1.055, 2.4) }
    return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b) > 0.4
}
