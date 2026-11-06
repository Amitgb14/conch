import Foundation

// conch's own skill — what lets an agent start, prompt and read another —
// per machine, as the TUI's Settings → Agents → conch's own skill shows it:
// agent.skill asked without apply is the plan, and nothing is written until
// it is asked again with apply.

public struct SkillChange: Decodable, Equatable, Sendable {
    public var path: String
    public var agents: [String]
    /// create, update, same, remove or skip (somebody's own skill of that name).
    public var action: String
    public var detail: String?
    public var error: String?

    public init(path: String, agents: [String], action: String, detail: String? = nil, error: String? = nil) {
        self.path = path
        self.agents = agents
        self.action = action
        self.detail = detail
        self.error = error
    }
}

public struct AgentSkillResult: Decodable, Equatable, Sendable {
    public var changes: [SkillChange]
    public var applied: Bool?

    public init(changes: [SkillChange], applied: Bool? = nil) {
        self.changes = changes
        self.applied = applied
    }
}

/// How many of a plan's paths have conch's skill, would be written, or are
/// somebody's own skill of that name (left alone).
public func skillCounts(_ r: AgentSkillResult) -> (have: Int, todo: Int, theirs: Int) {
    var have = 0, todo = 0, theirs = 0
    for c in r.changes {
        switch c.action {
        case "same": have += 1
        case "create", "update": todo += 1
        case "skip": theirs += 1
        default: break
        }
    }
    return (have, todo, theirs)
}

/// A machine's line: what a person wants to know before opening it.
public enum SkillStanding: Equatable, Sendable {
    case everyAgent
    case some(have: Int, not: Int)
    case notInstalled
    case noAgentReadsSkills

    public var text: String {
        switch self {
        case .everyAgent: return "✓ every agent has it"
        case .some(let h, let n): return "\(h) have it · \(n) not"
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
    case (_, 1...): return .notInstalled
    default: return .noAgentReadsSkills
    }
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
