import Foundation

// The parts of conch's wire protocol (internal/proto) the Mac app reads.
// Fields are optional wherever the server marks them omitempty, so an
// older or newer server still decodes; anything unknown is ignored.

public enum AgentState: String, Decodable, Sendable {
    case idle, working, blocked, done
    case unknown

    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = AgentState(rawValue: raw) ?? .unknown
    }
}

public struct AgentStatus: Decodable, Equatable, Sendable {
    public var name: String
    public var state: AgentState
    public var message: String?
    public var failed: Bool?
    public var since: Date?

    public init(name: String, state: AgentState, message: String? = nil, failed: Bool? = nil, since: Date? = nil) {
        self.name = name
        self.state = state
        self.message = message
        self.failed = failed
        self.since = since
    }

    public static func == (a: AgentStatus, b: AgentStatus) -> Bool {
        a.name == b.name && a.state == b.state && a.message == b.message && a.failed == b.failed
    }
}

public struct PaneInfo: Decodable, Identifiable, Equatable, Sendable {
    public var id: String
    public var name: String
    public var cwd: String?
    public var cols: Int?
    public var rows: Int?
    public var state: String
    public var exitCode: Int?
    public var agent: AgentStatus?
    public var title: String?
    public var customName: Bool?
    public var projectID: String?
    public var branch: String?

    enum CodingKeys: String, CodingKey {
        case id, name, cwd, cols, rows, state, agent, title, branch
        case exitCode = "exit_code"
        case customName = "custom_name"
        case projectID = "project_id"
    }

    public init(id: String, name: String, state: String = "running", agent: AgentStatus? = nil,
                title: String? = nil, projectID: String? = nil, branch: String? = nil, cwd: String? = nil) {
        self.id = id
        self.name = name
        self.state = state
        self.agent = agent
        self.title = title
        self.projectID = projectID
        self.branch = branch
        self.cwd = cwd
    }

    /// What the tree calls a pane: an agent's own title for its work, unless
    /// the person renamed it (proto.PaneInfo.DisplayName).
    public var displayName: String {
        if customName != true, agent != nil, let t = title, !t.isEmpty { return t }
        return name
    }

    public var running: Bool { state == "running" }
}

public struct GitStatus: Decodable, Equatable, Sendable {
    public var files: Int?
    public var added: Int?
    public var deleted: Int?
}

public struct WorktreeInfo: Decodable, Equatable, Sendable {
    public var path: String
    public var branch: String?
    public var main: Bool?
    public var status: GitStatus?

    public init(path: String, branch: String?, main: Bool? = nil) {
        self.path = path
        self.branch = branch
        self.main = main
    }
}

public struct PRInfo: Decodable, Equatable, Sendable {
    public var number: Int
    public var state: String?
    public var checks: String?
}

public struct BranchInfo: Decodable, Equatable, Sendable {
    public var name: String
    public var baseAhead: Int?
    public var baseBehind: Int?
    public var worktree: String?
    public var pr: PRInfo?

    enum CodingKeys: String, CodingKey {
        case name, worktree, pr
        case baseAhead = "base_ahead"
        case baseBehind = "base_behind"
    }

    public init(name: String, baseAhead: Int? = nil, worktree: String? = nil) {
        self.name = name
        self.baseAhead = baseAhead
        self.worktree = worktree
    }
}

public struct ProjectInfo: Decodable, Identifiable, Equatable, Sendable {
    public var id: String
    public var name: String
    public var path: String
    public var git: Bool?
    public var base: String?
    public var worktrees: [WorktreeInfo]?
    public var branches: [BranchInfo]?

    public init(id: String, name: String, path: String, worktrees: [WorktreeInfo]? = nil, branches: [BranchInfo]? = nil) {
        self.id = id
        self.name = name
        self.path = path
        self.worktrees = worktrees
        self.branches = branches
    }
}

public struct PaneList: Decodable { public var panes: [PaneInfo] }
public struct ProjectList: Decodable { public var projects: [ProjectInfo] }

public struct HelloResult: Decodable, Sendable {
    public var version: String?
    public var build: String?
    public var capabilities: [String]?
    public var home: String?
}

/// A choice of a waiting agent's menu, as `conch agent question` reads it.
public struct Choice: Decodable, Equatable, Sendable {
    public var choice: String
    public var label: String
    public var `default`: Bool?
}

/// What a waiting agent asks (internal/phone.Question).
public struct Question: Decodable, Equatable, Sendable {
    public var id: String
    public var text: String
    public var choices: [Choice]
}

/// The server's times are Go's RFC 3339, with nanoseconds when it has them.
public func conchDecoder() -> JSONDecoder {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .custom { decoder in
        let frac = ISO8601DateFormatter()
        frac.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        let s = try decoder.singleValueContainer().decode(String.self)
        // Foundation takes at most milliseconds; Go writes nanoseconds.
        var trimmed = s
        if let dot = s.firstIndex(of: "."), let end = s[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let digits = s[s.index(after: dot)..<end]
            trimmed = String(s[..<dot]) + "." + String(digits.prefix(3)) + String(s[end...])
        }
        if let date = frac.date(from: trimmed) ?? plain.date(from: s) { return date }
        return Date(timeIntervalSince1970: 0)
    }
    return d
}
