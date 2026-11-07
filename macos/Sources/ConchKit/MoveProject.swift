import Foundation

// Moving a whole project to another machine, as `conch project move` does
// it (internal/relocate): Conch.app runs that command — -plan to show what
// would move, then -y to move it — and reads its JSON, a line a step. Here
// is what those lines say, and the few decisions the sheet makes itself.

/// What one worktree takes along (proto.WorktreeMoveInfo).
public struct MoveInfo: Decodable, Equatable, Sendable {
    public var staged: Int?
    public var unstaged: Int?
    public var other: Int?
    public var history: [String]?
}

public struct MoveWorktree: Decodable, Equatable, Sendable {
    public var branch: String
    public var path: String
    public var info: MoveInfo
    public var agents: [PaneInfo]?
    /// The worktree there on this branch already, an earlier move's:
    /// nothing is copied, its agents continue in it.
    public var there: String?
}

/// What a move would do (relocate.Plan).
public struct MovePlan: Decodable, Equatable, Sendable {
    public var projectID: String
    public var name: String
    public var path: String
    public var remote: String?
    public var from: String
    public var to: String
    public var target: String?
    public var targetName: String?
    public var targetPath: String?
    public var cloneTo: String?
    public var worktrees: [MoveWorktree]?
    public var branches: [BranchInfo]?
    public var mainAgents: [PaneInfo]?
    public var skipped: [String]?
    /// Branches there already, with nothing left to follow them.
    public var already: [String]?
    public var mainDirty: Int?
    public var terminals: Int?

    enum CodingKeys: String, CodingKey {
        case name, path, remote, from, to, target, worktrees, branches, skipped, terminals, already
        case projectID = "project_id"
        case targetName = "target_name"
        case targetPath = "target_path"
        case cloneTo = "clone_to"
        case mainAgents = "main_agents"
        case mainDirty = "main_dirty"
    }

    /// Every agent that continues there.
    public var agents: [PaneInfo] { (worktrees ?? []).flatMap { $0.agents ?? [] } + (mainAgents ?? []) }

    /// Where it goes there: the clone it makes, or the project it joins.
    public var destination: String { cloneTo ?? targetPath ?? "" }
}

/// A step of a move (relocate.Event).
public struct MoveEvent: Decodable, Equatable, Sendable {
    public var kind: String   // clone, branch, worktree, agents
    public var name: String
    public var state: String  // start, ok, fail
    public var detail: String?
    public var step: Int
    public var steps: Int
}

/// What a move did (relocate.Result).
public struct MoveResult: Decodable, Equatable, Sendable {
    public var target: String
    public var targetPath: String
    public var moved: [String]?
    public var started: [PaneInfo]?
    public var failed: [String]?

    enum CodingKeys: String, CodingKey {
        case target, moved, started, failed
        case targetPath = "target_path"
    }
}

/// One line of `conch project move -json`.
public enum MoveLine: Equatable, Sendable {
    case plan(MovePlan, steps: Int)
    case event(MoveEvent)
    case result(MoveResult)
    case error(String)
}

/// A line of `conch project move -json`, or nil for one it doesn't know.
public func parseMoveLine(_ line: String) -> MoveLine? {
    struct Raw: Decodable {
        var plan: MovePlan?
        var steps: Int?
        var event: MoveEvent?
        var result: MoveResult?
        var error: String?
    }
    guard let data = line.data(using: .utf8), let r = try? conchDecoder().decode(Raw.self, from: data) else { return nil } // Go writes dates RFC 3339
    if let p = r.plan { return .plan(p, steps: r.steps ?? 0) }
    if let e = r.event { return .event(e) }
    if let x = r.result { return .result(x) }
    if let e = r.error { return .error(e) }
    return nil
}

/// The command that moves project from machine to machine: -plan only to
/// see what would move; into, when given, is where it goes there.
public func moveArgs(project: String, from: String, to: String, into: String?, plan: Bool, cols: Int = 120, rows: Int = 36) -> [String] {
    var args = ["project", "move", project, "-to", to, "-json"]
    if let into, !into.trimmingCharacters(in: .whitespaces).isEmpty {
        args += ["-into", into.trimmingCharacters(in: .whitespaces)]
    }
    args += plan ? ["-plan"] : ["-y", "-cols", String(cols), "-rows", String(rows)]
    return onMachine(from, args)
}

/// A remote URL as host/path without .git, however it is written
/// (relocate.NormalRemote): git@host:a/b.git, ssh://git@host/a/b, https://host/a/b.
public func normalRemote(_ u: String) -> String {
    var s = u.trimmingCharacters(in: .whitespaces)
    if s.isEmpty { return "" }
    if !s.contains("://"), let c = s.firstIndex(of: ":"), c > s.startIndex, !s.hasPrefix("/"), !s[..<c].contains("/") {
        s = "ssh://" + s[..<c] + "/" + s[s.index(after: c)...].drop(while: { $0 == "/" })
    }
    if let u = URL(string: s), let host = u.host, !host.isEmpty {
        s = host.lowercased() + "/" + u.path.drop(while: { $0 == "/" })
    }
    while s.hasSuffix("/") { s.removeLast() }
    if s.hasSuffix(".git") { s.removeLast(4) }
    while s.hasSuffix("/") { s.removeLast() }
    return s
}

/// Whether two remotes name the same repository.
public func sameRemote(_ a: String?, _ b: String?) -> Bool {
    let na = normalRemote(a ?? ""), nb = normalRemote(b ?? "")
    return !na.isEmpty && na.lowercased() == nb.lowercased()
}

/// The project on a machine cloned from the same origin as remote, if any:
/// a move there joins it rather than cloning again.
public func cloneOf(_ remote: String?, in projects: [ProjectInfo]) -> ProjectInfo? {
    projects.first { $0.git == true && sameRemote($0.remote, remote) }
}

/// What a worktree takes along, in a few words: 2 staged · 1 unstaged.
public func moveTakes(_ i: MoveInfo) -> [String] {
    var out: [String] = []
    if let n = i.staged, n > 0 { out.append("\(n) staged") }
    if let n = i.unstaged, n > 0 { out.append("\(n) unstaged") }
    if let n = i.other, n > 0 { out.append("\(n) untracked") }
    return out.isEmpty ? ["committed"] : out
}

/// Why a branch with no worktree moves.
public func branchWhy(_ b: BranchInfo) -> String {
    if b.gone == true { return "its upstream is gone" }
    if let n = b.ahead, n > 0 { return "\(n) commit\(n == 1 ? "" : "s") not on \(b.upstream ?? "origin")" }
    return "only here"
}
