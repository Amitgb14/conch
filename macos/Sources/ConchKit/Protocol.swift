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

/// What an agent has used, as the server reports it (proto.Tokens).
public struct Tokens: Decodable, Equatable, Sendable {
    public var input = 0, cacheWrite = 0, cacheRead = 0, output = 0
    public var costUSD: Double?
    /// The latest request's context, and the model's window when the
    /// agent reports it.
    public var context = 0, contextSize = 0

    enum CodingKeys: String, CodingKey {
        case input, output, context
        case contextSize = "context_size"
        case cacheWrite = "cache_write"
        case cacheRead = "cache_read"
        case costUSD = "cost_usd"
    }

    public init(input: Int = 0, cacheWrite: Int = 0, cacheRead: Int = 0, output: Int = 0, costUSD: Double? = nil) {
        self.input = input
        self.cacheWrite = cacheWrite
        self.cacheRead = cacheRead
        self.output = output
        self.costUSD = costUSD
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        input = try c.decodeIfPresent(Int.self, forKey: .input) ?? 0
        cacheWrite = try c.decodeIfPresent(Int.self, forKey: .cacheWrite) ?? 0
        cacheRead = try c.decodeIfPresent(Int.self, forKey: .cacheRead) ?? 0
        output = try c.decodeIfPresent(Int.self, forKey: .output) ?? 0
        costUSD = try c.decodeIfPresent(Double.self, forKey: .costUSD)
        context = try c.decodeIfPresent(Int.self, forKey: .context) ?? 0
        contextSize = try c.decodeIfPresent(Int.self, forKey: .contextSize) ?? 0
    }
}

/// An agent running inside another agent's process, known from the hooks
/// that say it started and stopped.
public struct Subagent: Decodable, Equatable, Sendable {
    public var id: String
    public var type: String?
    /// The task it was given, when known.
    public var description: String?
    public var since: Date?

    public init(id: String, type: String? = nil, description: String? = nil) {
        self.id = id
        self.type = type
        self.description = description
    }

    /// What its row says: the task, else its kind, else its id.
    public var title: String {
        if let d = description, !d.isEmpty { return d }
        if let t = type, !t.isEmpty { return t }
        return id
    }
}

public struct AgentStatus: Decodable, Equatable, Sendable {
    public var name: String
    public var state: AgentState
    public var message: String?
    public var failed: Bool?
    public var since: Date?
    public var tokens: Tokens?
    /// The agents this one runs inside its own process (Claude Code's
    /// Agent tool), which have no pane of their own; none from an older
    /// server.
    public var subagents: [Subagent]?

    public init(name: String, state: AgentState, message: String? = nil, failed: Bool? = nil, since: Date? = nil,
                tokens: Tokens? = nil) {
        self.name = name
        self.state = state
        self.message = message
        self.failed = failed
        self.since = since
        self.tokens = tokens
    }

    public static func == (a: AgentStatus, b: AgentStatus) -> Bool {
        a.name == b.name && a.state == b.state && a.message == b.message && a.failed == b.failed && a.tokens == b.tokens
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
    /// The program's process on its machine.
    public var pid: Int?
    /// The pane this one was started from: an agent's helper.
    public var createdBy: String?
    /// The program and its arguments.
    public var command: [String]?
    /// When it was started.
    public var created: Date?

    /// An ssh session, as the TUI tells one (sshPane): no agent, and the
    /// program is ssh.
    public var isSSH: Bool {
        agent == nil && (command?.first.map { ($0 as NSString).lastPathComponent == "ssh" } ?? false)
    }
    /// What happened since someone last looked: "activity" or "silence".
    public var alert: String?
    /// How it is watched for those (pane.monitor.v1).
    public var monitor: PaneMonitor?
    /// The TCP ports its programs listen on, lowest first (pane.listening.v1).
    public var listening: [Int]?

    enum CodingKeys: String, CodingKey {
        case id, name, cwd, cols, rows, state, agent, title, branch, alert, pid, command, created, monitor, listening
        case exitCode = "exit_code"
        case customName = "custom_name"
        case projectID = "project_id"
        case createdBy = "created_by"
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
    public var conflicts: Int?

    public init(files: Int? = nil, added: Int? = nil, deleted: Int? = nil, conflicts: Int? = nil) {
        self.files = files
        self.added = added
        self.deleted = deleted
        self.conflicts = conflicts
    }
}

public struct WorktreeInfo: Decodable, Equatable, Sendable {
    public var path: String
    public var branch: String?
    public var main: Bool?
    public var status: GitStatus?
    /// The first port the checkout is given, as CONCH_PORT in its panes.
    public var port: Int?
    /// The files merging the branch into the base would conflict in,
    /// tried without merging (git 2.38 and later).
    public var conflicts: [String]?

    public init(path: String, branch: String?, main: Bool? = nil, port: Int? = nil) {
        self.path = path
        self.branch = branch
        self.main = main
        self.port = port
    }
}

/// One check of a pull request: state is pass, fail or pending.
/// Not Identifiable by name: a name can repeat across workflows.
public struct PRCheck: Decodable, Equatable, Sendable {
    public var name: String
    public var state: String
    public var url: String?
}

public struct PRInfo: Decodable, Equatable, Sendable {
    public var number: Int
    public var state: String?
    public var checks: String?
    public var draft: Bool?
    public var url: String?
    public var title: String?
    /// APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED.
    public var review: String?
    /// Checks finished passing, of all of them.
    public var passed: Int?
    public var total: Int?
    public var head: String?
    /// GitHub says it cannot merge into its base as it stands.
    public var conflicting: Bool?
    /// Its checks one by one, from a server that names them.
    public var runs: [PRCheck]?

    public init(number: Int, state: String? = nil, checks: String? = nil, draft: Bool? = nil, url: String? = nil) {
        self.number = number
        self.state = state
        self.checks = checks
        self.draft = draft
        self.url = url
    }
}

public struct BranchInfo: Decodable, Equatable, Sendable {
    public var name: String
    public var baseAhead: Int?
    public var baseBehind: Int?
    public var worktree: String?
    public var pr: PRInfo?
    /// Its upstream, and how far ahead of it; when it was last committed to.
    public var upstream: String?
    public var ahead: Int?
    public var behind: Int?
    public var committed: Date?
    /// Its upstream is gone (deleted on the remote, say after a merge).
    public var gone: Bool?

    enum CodingKeys: String, CodingKey {
        case name, worktree, pr, upstream, ahead, behind, committed, gone
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
    /// Its origin's URL, when it has one: a move to another machine joins
    /// the project there cloned from the same.
    public var remote: String?

    public init(id: String, name: String, path: String, worktrees: [WorktreeInfo]? = nil, branches: [BranchInfo]? = nil) {
        self.id = id
        self.name = name
        self.path = path
        self.worktrees = worktrees
        self.branches = branches
    }
}

/// A project group (project.groups.v1): projects kept together because
/// they change together, each still a project of its own, in the order
/// the person gave them.
public struct GroupInfo: Decodable, Equatable, Sendable {
    public var name: String
    public var projects: [String]
    public var notes: String?
    /// Tasks across its repos (group.task.v1), the newest last.
    public var tasks: [GroupTask]?

    public init(name: String, projects: [String], notes: String? = nil, tasks: [GroupTask]? = nil) {
        self.name = name
        self.projects = projects
        self.notes = notes
        self.tasks = tasks
    }
}

/// A task across a group's repos: the branch in each of its projects, the
/// folder linking their worktrees, and the panes it started.
public struct GroupTask: Decodable, Equatable, Sendable {
    public var branch: String
    public var folder: String
    public var projects: [String]
    public var panes: [String]?
    public var each: Bool?

    public init(branch: String, folder: String, projects: [String], panes: [String]? = nil, each: Bool? = nil) {
        self.branch = branch
        self.folder = folder
        self.projects = projects
        self.panes = panes
        self.each = each
    }
}

public struct GroupList: Decodable { public var groups: [GroupInfo]? }

public struct PaneList: Decodable { public var panes: [PaneInfo] }
public struct ProjectList: Decodable { public var projects: [ProjectInfo] }

public struct HelloResult: Decodable, Sendable {
    public var version: String?
    /// The server's process, for what conch uses on this computer.
    public var pid: Int?
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

/// How much of an agent account's plan is used (proto.PlanLimits).
public struct PlanLimits: Decodable, Equatable, Sendable {
    public struct Window: Decodable, Equatable, Sendable {
        public var usedPct: Double
        public var resetsAt: Date?
        enum CodingKeys: String, CodingKey {
            case usedPct = "used_pct"
            case resetsAt = "resets_at"
        }
        public init(usedPct: Double, resetsAt: Date? = nil) {
            self.usedPct = usedPct
            self.resetsAt = resetsAt
        }
    }
    /// One window under the name its agent gave it (five_hour, seven_day,
    /// seven_day_opus, spend_limit…).
    public struct Named: Decodable, Equatable, Sendable {
        public var key: String
        public var usedPct: Double
        public var resetsAt: Date?
        enum CodingKeys: String, CodingKey {
            case key
            case usedPct = "used_pct"
            case resetsAt = "resets_at"
        }
        public init(key: String, usedPct: Double, resetsAt: Date? = nil) {
            self.key = key
            self.usedPct = usedPct
            self.resetsAt = resetsAt
        }
    }
    public var agent: String
    public var fiveHour: Window?
    public var week: Window?
    public var spend: Window?
    public var windows: [Named]?
    /// When the agent reported it.
    public var at: Date?
    enum CodingKeys: String, CodingKey {
        case agent, week, spend, windows, at
        case fiveHour = "five_hour"
    }
    public init(agent: String, fiveHour: Window? = nil, week: Window? = nil, spend: Window? = nil, windows: [Named]? = nil, at: Date? = nil) {
        self.agent = agent
        self.fiveHour = fiveHour
        self.week = week
        self.spend = spend
        self.windows = windows
        self.at = at
    }

    /// Every window, as the limits view lists them: the agent's own list
    /// when it gave one, else the three every server sends.
    public var allWindows: [Named] {
        if let w = windows, !w.isEmpty { return w }
        var out: [Named] = []
        if let w = fiveHour { out.append(Named(key: "five_hour", usedPct: w.usedPct, resetsAt: w.resetsAt)) }
        if let w = week { out.append(Named(key: "seven_day", usedPct: w.usedPct, resetsAt: w.resetsAt)) }
        if let w = spend { out.append(Named(key: "spend_limit", usedPct: w.usedPct, resetsAt: w.resetsAt)) }
        return out
    }

    /// What a window is called: "5 hours", "7 days", "7 days · opus", "Spend".
    public static func label(_ key: String) -> String {
        switch key {
        case "five_hour": return "5 hours"
        case "seven_day": return "7 days"
        case "spend_limit", "spend": return "Spend"
        default:
            if key.hasPrefix("seven_day_") { return "7 days · " + key.dropFirst("seven_day_".count).replacingOccurrences(of: "_", with: " ") }
            return key.replacingOccurrences(of: "_", with: " ")
        }
    }
}

public struct AgentLimitsResult: Decodable { public var limits: [PlanLimits]? }

/// A session list, counted rather than read: the sidebar says how many.
public struct SessionCount: Decodable {
    struct Any_: Decodable {}
    var sessions: [Any_]?
    public var count: Int { sessions?.count ?? 0 }
}

/// What fs.find found in a checkout: paths relative to it.
public struct FindResult: Decodable, Sendable {
    public var root: String
    public var paths: [String]?
    public var truncated: Bool?
}

/// A pane watched as tmux's monitor-activity and monitor-silence: an alert
/// when it prints while nobody looks (activity), or goes quiet for silence
/// seconds after printing — a build or a test run ending.
public struct PaneMonitor: Codable, Equatable, Sendable {
    public var activity: Bool?
    public var silence: Int?

    public init(activity: Bool? = nil, silence: Int? = nil) {
        self.activity = activity
        self.silence = silence
    }

    /// The same, one kind turned the other way: silence (after seconds)
    /// when quiet is true, else activity.
    public func toggled(quiet: Bool, seconds: Int) -> PaneMonitor {
        var m = self
        if quiet {
            m.silence = (silence ?? 0) > 0 ? nil : seconds
        } else {
            m.activity = activity == true ? nil : true
        }
        return m
    }
}

/// How long a pane must be quiet to raise its alert: [notify] silence, as
/// the TUI reads it (config.NotifyCfg.SilenceAfter), 30 when unset or not a
/// whole number above zero.
public func silenceAfter(toml: String) -> Int {
    guard let v = tomlSetting("notify", "silence", in: toml), let n = Int(v), n >= 1 else { return 30 }
    return n
}
