import Foundation

// What each agent loads in a checkout, as the TUI's setup view (i) shows
// it (agent.setup, internal/agentsetup): its instructions, skills, MCP
// servers and the rest, each with where it comes from, and the local files
// a worktree has or lacks against the main checkout. Read only here.

public struct SetupItem: Decodable, Equatable, Sendable {
    public var name: String
    public var scope: String
    public var path: String?
    public var detail: String?
    public var missing: Bool?
}

public struct SetupGroup: Decodable, Equatable, Sendable {
    public var title: String
    public var items: [SetupItem]?
}

public struct AgentSetup: Decodable, Equatable, Sendable, Identifiable {
    public var agent: String
    public var label: String
    public var groups: [SetupGroup]?
    public var notes: [String]?
    public var id: String { agent }

    /// How many things it loads, missing ones apart.
    public var loaded: Int { (groups ?? []).reduce(0) { $0 + ($1.items ?? []).filter { $0.missing != true }.count } }
}

public struct LocalFile: Decodable, Equatable, Sendable {
    public var path: String
    public var state: String // missing, differs, same, not_ignored
}

public struct AgentSetupResult: Decodable, Equatable, Sendable {
    public var dir: String
    public var worktree: String?
    public var main: String?
    public var localFiles: [LocalFile]?
    public var agents: [AgentSetup]?

    enum CodingKeys: String, CodingKey {
        case dir, worktree, main, agents
        case localFiles = "local_files"
    }
}

/// What a local file's state says, in words.
public func localFileNote(_ state: String) -> String {
    switch state {
    case "missing": return "not copied here"
    case "differs": return "differs from the main checkout"
    case "same": return "the same as the main checkout"
    case "not_ignored": return "not ignored by git, so never copied"
    default: return state
    }
}

/// A checkout's agent setup tab's key: "setup|machine|project|dir".
public func setupKey(_ machine: String, _ project: String, dir: String) -> String { "setup|\(machine)|\(project)|\(dir)" }

public func parseSetupKey(_ k: String) -> (machine: String, project: String, dir: String)? {
    let parts = k.split(separator: "|", maxSplits: 3, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 4, parts[0] == "setup", !parts[2].isEmpty, !parts[3].isEmpty else { return nil }
    return (parts[1], parts[2], parts[3])
}
