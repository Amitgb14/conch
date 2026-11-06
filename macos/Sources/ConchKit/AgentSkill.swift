import Foundation

// conch's skills — conch (start, prompt and read another agent),
// conch-team, conch-review, conch-ship and conch-factory — per machine, as
// the TUI's Settings → Agents → conch's skills shows them: agent.skill
// asked without apply is the plan, and nothing is written until it is
// asked again with apply. A server from before there was more than one
// answers a change per folder for conch's own, with no skill named.

public struct SkillChange: Decodable, Equatable, Sendable {
    /// Which of conch's skills; nil from an older server, meaning conch.
    public var skill: String?
    public var path: String
    public var agents: [String]
    /// create, update, same, remove or skip (somebody's own skill of that name).
    public var action: String
    public var detail: String?
    public var error: String?

    public init(skill: String? = nil, path: String, agents: [String], action: String, detail: String? = nil, error: String? = nil) {
        self.skill = skill
        self.path = path
        self.agents = agents
        self.action = action
        self.detail = detail
        self.error = error
    }

    /// The agents' skills folder this SKILL.md is in.
    public var folder: String {
        ((path as NSString).deletingLastPathComponent as NSString).deletingLastPathComponent
    }

    public var skillName: String { skill ?? "conch" }
}

public struct AgentSkillResult: Decodable, Equatable, Sendable {
    public var changes: [SkillChange]
    public var applied: Bool?

    public init(changes: [SkillChange], applied: Bool? = nil) {
        self.changes = changes
        self.applied = applied
    }
}

/// How many of the agents' skills folders have every one of conch's
/// skills, how many lack one or hold an older copy, and how many skills are
/// somebody's own of that name (left alone).
public func skillCounts(_ r: AgentSkillResult) -> (have: Int, todo: Int, theirs: Int) {
    var have = 0, todo = 0, theirs = 0
    for f in skillFolders(r) {
        if f.changes.contains(where: { ["create", "update"].contains($0.action) }) {
            todo += 1
        } else if f.changes.contains(where: { $0.action == "same" }) {
            have += 1
        }
        theirs += f.changes.filter { $0.action == "skip" }.count
    }
    return (have, todo, theirs)
}

/// One agents' skills folder and conch's skills in it.
public struct SkillFolder: Equatable, Sendable, Identifiable {
    public var path: String
    public var agents: [String]
    public var changes: [SkillChange]
    public var id: String { path }
}

/// A plan's changes by folder, in the order the server gave them.
public func skillFolders(_ r: AgentSkillResult) -> [SkillFolder] {
    var out: [SkillFolder] = []
    for c in r.changes {
        if let i = out.firstIndex(where: { $0.path == c.folder }) {
            out[i].changes.append(c)
            for a in c.agents where !out[i].agents.contains(a) { out[i].agents.append(a) }
        } else {
            out.append(SkillFolder(path: c.folder, agents: c.agents, changes: [c]))
        }
    }
    return out
}

/// A machine's line: what a person wants to know before opening it.
public enum SkillStanding: Equatable, Sendable {
    case everyAgent
    case some(have: Int, not: Int)
    /// conch's own is there, from before the rest, and no folder has all.
    case missingSome(Int)
    case notInstalled
    case noAgentReadsSkills

    public var text: String {
        switch self {
        case .everyAgent: return "✓ every agent has them"
        case .some(let h, let n): return "\(h) have them · \(n) not"
        case .missingSome(let n): return "\(n) missing some · an older install"
        case .notInstalled: return "not installed"
        case .noAgentReadsSkills: return "no agent here reads skills"
        }
    }
}

public func skillStanding(_ r: AgentSkillResult) -> SkillStanding {
    let (have, todo, _) = skillCounts(r)
    switch (have, todo) {
    case (1..., 0): return .everyAgent
    case (1..., _): return .some(have: have, not: todo)
    case (_, 1...): return skillAnyInstalled(r) ? .missingSome(todo) : .notInstalled
    default: return .noAgentReadsSkills
    }
}

/// Whether any of conch's skills is there to take away.
public func skillAnyInstalled(_ r: AgentSkillResult) -> Bool {
    r.changes.contains { $0.action == "same" }
}

/// One path's state, as the TUI says it.
public func skillLine(_ c: SkillChange) -> String {
    if let e = c.error, !e.isEmpty { return "failed: " + e }
    switch c.action {
    case "same": return "✓ has it"
    case "create": return "not installed"
    case "update": return "an older copy"
    case "remove": return "conch's copy"
    case "skip": return (c.detail ?? "").isEmpty ? "yours, left alone" : c.detail!
    default: return c.action
    }
}

/// What a write would touch, for the question asked before it: the paths
/// that would change, and nothing that is already right or is yours.
public func skillWrites(_ r: AgentSkillResult) -> [SkillChange] {
    r.changes.filter { ["create", "update", "remove"].contains($0.action) }
}

/// One of conch's skills across a machine's agents' folders: in how many
/// it is, and in how many it could be (a folder holding somebody's own
/// skill of that name is not counted either way).
public struct SkillCoverage: Equatable, Sendable, Identifiable {
    public var name: String
    public var have: Int
    public var of: Int
    public var id: String { name }
    public var everywhere: Bool { of > 0 && have == of }
}

/// Each skill a plan names, in the order it names them.
public func skillCoverage(_ r: AgentSkillResult) -> [SkillCoverage] {
    var out: [SkillCoverage] = []
    for c in r.changes where c.action != "skip" {
        let i = out.firstIndex { $0.name == c.skillName } ?? {
            out.append(SkillCoverage(name: c.skillName, have: 0, of: 0))
            return out.count - 1
        }()
        out[i].of += 1
        if c.action == "same" { out[i].have += 1 }
    }
    return out
}

/// What each of conch's skills is for, in a line.
public let conchSkillBlurbs: [String: String] = [
    "conch": "start a helper agent, prompt it, read its answer",
    "conch-team": "several agents on one task: a race, a split, a chain",
    "conch-review": "another agent reviews, the author fixes, review again",
    "conch-ship": "tests, pull request, CI, merge when asked",
    "conch-factory": "a ticket all the way to a merged pull request",
]
