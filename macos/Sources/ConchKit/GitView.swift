import Foundation

// What the git view works from, apart from drawing it: a diff cut into
// hunks and the patch of the ones ticked (the TUI's own way: a hunk is
// known by its text, so one that changed underneath loses its tick rather
// than committing something else), comments on lines (review.comments.v1),
// a commit and a range from the graph (project.commit.v1), and a branch's
// overview: activity by day and who changed it.

// MARK: hunks

/// One hunk of a file's diff: its @@ line, its text as the patch takes it,
/// and its lines with their numbers.
public struct Hunk: Equatable, Sendable, Identifiable {
    public var index: Int
    public var header: String
    public var text: String
    public var lines: [DiffLine]
    public var id: Int { index }
}

/// A unified diff cut into what comes before its first hunk (the file's
/// header, which a patch needs) and its hunks, in order.
public func splitHunks(_ diff: String) -> (header: String, hunks: [Hunk]) {
    var header = ""
    var texts: [String] = []
    for raw in diff.split(separator: "\n", omittingEmptySubsequences: false) {
        let line = String(raw)
        if line.hasPrefix("@@") {
            texts.append(line + "\n")
        } else if texts.isEmpty {
            if !line.isEmpty || !header.isEmpty { header += line + "\n" }
        } else {
            texts[texts.count - 1] += line + "\n"
        }
    }
    // The diff's own last newline makes an empty line after the last hunk.
    if let last = texts.last, last.hasSuffix("\n\n") { texts[texts.count - 1] = String(last.dropLast()) }
    let hunks = texts.enumerated().map { i, t in
        Hunk(index: i, header: String(t.prefix(while: { $0 != "\n" })), text: t,
             lines: parseDiff(t).filter { $0.kind != .hunk && $0.kind != .meta })
    }
    return (header.trimmingCharacters(in: .newlines).isEmpty ? "" : header, hunks)
}

/// The patch of some of a file's hunks: its header, then those hunks in
/// the order they appear.
public func hunkPatch(header: String, hunks: [String]) -> String {
    guard !hunks.isEmpty else { return "" }
    return header + hunks.joined()
}

/// What is ticked for the next commit, per file: the whole file, or some of
/// its hunks by their text.
public struct StagePicks: Equatable, Sendable {
    public var whole: Set<String> = []
    public var hunks: [String: Set<String>] = [:]
    /// Each file's header, kept when its hunks are ticked, for the patch.
    public var headers: [String: String] = [:]

    public init() {}

    public var isEmpty: Bool { whole.isEmpty && hunks.values.allSatisfy(\.isEmpty) }
    public var hunkCount: Int { hunks.values.reduce(0) { $0 + $1.count } }

    /// A file's box: ticked whole, some hunks, or none.
    public func state(_ path: String, of all: [Hunk]?) -> Tick {
        if whole.contains(path) { return .on }
        let n = hunks[path]?.count ?? 0
        if n == 0 { return .off }
        if let all, n >= all.count { return .on }
        return .mixed
    }

    /// The file's box pressed: on to off, anything else to the whole file.
    public mutating func toggleFile(_ path: String) {
        if whole.contains(path) || (hunks[path]?.isEmpty == false) {
            whole.remove(path)
            hunks[path] = nil
        } else {
            whole.insert(path)
        }
    }

    /// The box over a list of files: on when every one is ticked whole or
    /// in every hunk, off when none is, a dash between.
    public func allState(_ paths: [String], hunks order: [String: [Hunk]] = [:]) -> Tick {
        guard !paths.isEmpty else { return .off }
        let states = paths.map { state($0, of: order[$0]) }
        if states.allSatisfy({ $0 == .on }) { return .on }
        if states.allSatisfy({ $0 == .off }) { return .off }
        return .mixed
    }

    /// Every one of paths ticked whole, or every tick on them cleared.
    public mutating func set(_ paths: [String], on: Bool) {
        for p in paths {
            hunks[p] = nil
            if on { whole.insert(p) } else { whole.remove(p) }
        }
    }

    /// A box shift-clicked: every file from the one last clicked (anchor)
    /// to this one takes the state the anchor has — Finder's range, for
    /// ticking a run of files at once. Without an anchor in the list it is
    /// an ordinary click.
    public mutating func extend(to path: String, from anchor: String?, in order: [String]) {
        guard let anchor, let a = order.firstIndex(of: anchor), let b = order.firstIndex(of: path) else {
            toggleFile(path)
            return
        }
        let on = state(anchor, of: nil) != .off
        set(Array(order[min(a, b)...max(a, b)]), on: on)
    }

    /// A hunk's box pressed. A file goes whole or by hunks, never both.
    public mutating func toggleHunk(_ path: String, header: String, hunk: String, all: [Hunk]) {
        var set = hunks[path] ?? (whole.contains(path) ? Set(all.map(\.text)) : [])
        whole.remove(path)
        if set.contains(hunk) { set.remove(hunk) } else { set.insert(hunk) }
        hunks[path] = set.isEmpty ? nil : set
        headers[path] = header
    }

    /// Drops ticks on hunks no longer in the file's diff, after it changed.
    public mutating func prune(_ path: String, header: String, hunks now: [Hunk]) {
        guard var set = hunks[path] else { return }
        let texts = Set(now.map(\.text))
        set.formIntersection(texts)
        hunks[path] = set.isEmpty ? nil : set
        headers[path] = header
    }

    /// The commit this makes: whole files by path, and the ticked hunks as
    /// one patch, each file's in the order its diff has them.
    public func selection(order: [String: [Hunk]]) -> (files: [String], patch: String, hunks: Int) {
        var patch = ""
        var n = 0
        for path in hunks.keys.sorted() {
            guard let set = hunks[path], !set.isEmpty else { continue }
            let ordered = (order[path] ?? []).map(\.text).filter { set.contains($0) }
            let texts = ordered.isEmpty ? set.sorted() : ordered
            patch += hunkPatch(header: headers[path] ?? "", hunks: texts)
            n += texts.count
        }
        return (whole.sorted(), patch, n)
    }
}

public enum Tick: Equatable, Sendable { case on, off, mixed }

/// The message with the agent credited, as git takes a trailer: once.
public func withTrailer(_ message: String, agent: String?) -> String {
    guard let agent, !agent.isEmpty else { return message }
    let name = agentTrailerName(agent)
    let trailer = "Co-Authored-By: \(name)"
    if message.contains(trailer) { return message }
    return message.trimmingCharacters(in: .whitespacesAndNewlines) + "\n\n" + trailer
}

/// The name an agent is credited under.
public func agentTrailerName(_ agent: String) -> String {
    switch agent {
    case "claude": return "Claude <noreply@anthropic.com>"
    case "codex": return "Codex <noreply@openai.com>"
    case "gemini": return "Gemini <noreply@google.com>"
    default: return agent.prefix(1).uppercased() + agent.dropFirst()
    }
}

// MARK: comments on lines

public struct ReviewReply: Decodable, Equatable, Sendable {
    public var by: String?
    public var note: String
    public var at: Date?
}

/// A comment on a line of a branch's diff: one of conch's notes for the
/// agent, or one read from the pull request (source "github").
public struct ReviewComment: Codable, Equatable, Sendable, Identifiable {
    public var id: String?
    public var projectID: String
    public var branch: String
    public var file: String
    public var hunk: String?
    public var line: Int?
    public var note: String
    public var by: String?
    public var at: Date?
    public var source: String?
    public var url: String?
    public var outdated: Bool?
    public var replies: [ReviewReply]?
    /// Whom a conch note was sent to, that pane, and when: nil while it
    /// waits to be sent (review.thread.v1).
    public var sentTo: String?
    public var sentPane: String?
    public var sentAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, branch, file, hunk, line, note, by, at, source, url, outdated, replies
        case projectID = "project_id"
        case sentTo = "sent_to", sentPane = "sent_pane", sentAt = "sent_at"
    }

    public init(projectID: String, branch: String, file: String, line: Int?, note: String, id: String? = nil) {
        self.id = id
        self.projectID = projectID
        self.branch = branch
        self.file = file
        self.line = line
        self.note = note
    }

    public func encode(to e: Encoder) throws {
        var c = e.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(id, forKey: .id)
        try c.encode(projectID, forKey: .projectID)
        try c.encode(branch, forKey: .branch)
        try c.encode(file, forKey: .file)
        try c.encodeIfPresent(hunk, forKey: .hunk)
        try c.encodeIfPresent(line, forKey: .line)
        try c.encode(note, forKey: .note)
    }

    /// From the pull request on GitHub: read there, changed there.
    public var fromGitHub: Bool { source == "github" }
}

public struct ReviewList: Decodable, Equatable, Sendable {
    public var comments: [ReviewComment]
    public init(comments: [ReviewComment] = []) { self.comments = comments }
    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        comments = try c.decodeIfPresent([ReviewComment].self, forKey: .comments) ?? []
    }
    enum CodingKeys: String, CodingKey { case comments }
}

/// The comments on one line of one file, the oldest first.
public func comments(_ all: [ReviewComment], file: String, line: Int) -> [ReviewComment] {
    all.filter { $0.file == file && $0.line == line }.sorted { ($0.at ?? .distantPast) < ($1.at ?? .distantPast) }
}

/// The line a comment on this diff line is on: its number in the new file
/// (proto.ReviewComment.Line). A removed line is not in the new file, so it
/// takes none — keyed by its old number too, it and the new line with the
/// same number both opened the composer.
public func commentLine(_ l: DiffLine) -> Int? { l.new }

/// Open comments on a file, for its row's count.
public func openComments(_ all: [ReviewComment], file: String) -> Int { all.filter { $0.file == file }.count }

/// A pending comment for the pull request: kept in the app until the
/// review is submitted, as GitHub's own "Start a review" keeps them.
public struct PendingComment: Equatable, Sendable, Identifiable, Encodable {
    public var id = UUID()
    public var file: String
    public var line: Int
    public var note: String

    public init(file: String, line: Int, note: String) {
        self.file = file
        self.line = line
        self.note = note
    }

    enum CodingKeys: String, CodingKey { case file, line, note }
}

/// A review to submit (branch.review).
public struct ReviewSubmit: Encodable, Sendable {
    public var projectID: String
    public var branch: String
    public var event: String
    public var body: String
    public var comments: [PendingComment]

    public init(projectID: String, branch: String, event: String, body: String, comments: [PendingComment]) {
        self.projectID = projectID
        self.branch = branch
        self.event = event
        self.body = body
        self.comments = comments
    }

    enum CodingKeys: String, CodingKey {
        case branch, event, body, comments
        case projectID = "project_id"
    }
}

public enum ReviewEvent: String, CaseIterable, Sendable {
    case comment, approve, requestChanges = "request_changes"
    public var title: String {
        switch self {
        case .comment: return "Comment"
        case .approve: return "Approve"
        case .requestChanges: return "Request changes"
        }
    }
    public var help: String {
        switch self {
        case .comment: return "Feedback without a decision"
        case .approve: return "Ready to merge; autopilot merges it once checks pass"
        case .requestChanges: return "Blocks the merge; your notes go to the agent to fix"
        }
    }
    /// Approving needs nothing said; the others need a summary or a comment.
    public func canSubmit(body: String, comments: Int) -> Bool {
        self == .approve || !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || comments > 0
    }
}

// MARK: a commit and a range

public struct CommitDetail: Decodable, Equatable, Sendable {
    public var hash: String
    public var parents: [String]?
    public var author: String?
    public var email: String?
    public var time: Date?
    public var subject: String?
    public var body: String?
    public var files: [FileChange]?
    public var commits: Int?
    public var diff: String?
}

public struct CommitParams: Encodable, Sendable {
    public var projectID: String
    public var hash: String
    public var from: String?
    public var file: String?

    public init(projectID: String, hash: String, from: String? = nil, file: String? = nil) {
        self.projectID = projectID
        self.hash = hash
        self.from = from
        self.file = file
    }

    enum CodingKeys: String, CodingKey {
        case hash, from, file
        case projectID = "project_id"
    }
}

/// The older and newer of two commits in a graph's order (newest first):
/// the range compared runs from the older to the newer.
public func compareRange(_ a: String, _ b: String, order: [String]) -> (from: String, to: String)? {
    guard let ia = order.firstIndex(of: a), let ib = order.firstIndex(of: b), ia != ib else { return nil }
    return ia > ib ? (a, b) : (b, a)
}

/// Whether a commit matches a graph's search: its subject, hash or author.
public func commitMatches(_ c: LogCommit, _ query: String) -> Bool {
    let q = query.trimmingCharacters(in: .whitespaces).lowercased()
    if q.isEmpty { return true }
    return c.subject.lowercased().contains(q) || c.hash.hasPrefix(q) || (c.author?.lowercased().contains(q) ?? false)
}

// MARK: overview

/// Commits a day for the last n days, oldest first, the last being today.
public func commitsByDay(_ commits: [LogCommit], now: Date, days: Int = 14, calendar: Calendar = .current) -> [Int] {
    let today = calendar.startOfDay(for: now)
    var out = Array(repeating: 0, count: days)
    for c in commits {
        guard let t = c.time else { continue }
        let d = calendar.dateComponents([.day], from: calendar.startOfDay(for: t), to: today).day ?? -1
        if d >= 0 && d < days { out[days - 1 - d] += 1 }
    }
    return out
}

/// Who committed in the last days, most first, with each one's share.
public func authorsSince(_ commits: [LogCommit], now: Date, days: Int = 7) -> [(author: String, count: Int, share: Double)] {
    let since = now.addingTimeInterval(-Double(days) * 86_400)
    var counts: [String: Int] = [:]
    for c in commits where (c.time ?? .distantPast) >= since {
        counts[c.author ?? "someone", default: 0] += 1
    }
    let total = counts.values.reduce(0, +)
    return counts.sorted { $0.value != $1.value ? $0.value > $1.value : $0.key < $1.key }
        .map { ($0.key, $0.value, total > 0 ? Double($0.value) / Double(total) : 0) }
}

/// What a clean branch says it is: nothing uncommitted, and how it stands
/// against its upstream.
public func cleanLine(branch: String, ahead: Int, behind: Int, upstream: String?) -> String {
    guard let up = upstream, !up.isEmpty else { return "\(branch) has nothing uncommitted and no upstream." }
    switch (ahead, behind) {
    case (0, 0): return "\(branch) has nothing uncommitted and is up to date with \(up)."
    case (_, 0): return "\(branch) has nothing uncommitted, \(ahead) commit\(ahead == 1 ? "" : "s") ahead of \(up)."
    case (0, _): return "\(branch) has nothing uncommitted, \(behind) commit\(behind == 1 ? "" : "s") behind \(up)."
    default: return "\(branch) has nothing uncommitted; \(ahead) ahead of \(up) and \(behind) behind."
    }
}

/// Commits from `git log --format=%H%x1f%s%x1f%an%x1f%ct`, as branch.git
/// prints them under the "$ git …" line it ran; anything else is skipped.
public func parseLogLines(_ output: String) -> [LogCommit] {
    output.split(separator: "\n").compactMap { line in
        let f = line.split(separator: "\u{1f}", omittingEmptySubsequences: false).map(String.init)
        // A line whose time is not a number is not one of ours: a subject
        // holding the separator shifts every field after it.
        guard f.count == 4, f[0].count >= 7, f[0].allSatisfy(\.isHexDigit), let t = Double(f[3]) else { return nil }
        var c = LogCommit(hash: f[0], subject: f[1])
        c.author = f[2]
        c.time = Date(timeIntervalSince1970: t)
        return c
    }
}

/// The agents notes on a branch can go to: running agents of the project
/// on that branch, oldest first, as the send menu lists them.
public func branchAgents(_ panes: [PaneInfo], project: String, branch: String) -> [PaneInfo] {
    panes.filter { $0.agent != nil && $0.projectID == project && $0.branch == branch && $0.running }
        .sorted { ($0.created ?? .distantPast) < ($1.created ?? .distantPast) }
}

/// What a commit takes: what is ticked; else, where the server can stage,
/// what is already staged, as git commit would; else every change.
public enum CommitScope: Equatable, Sendable {
    case picked
    case staged(Int)
    case everything(Int)
}

public func commitScope(picked: Bool, files: [FileChange], canStage: Bool) -> CommitScope {
    if picked { return .picked }
    let staged = files.filter { $0.staged == true }.count
    return canStage && staged > 0 ? .staged(staged) : .everything(files.count)
}

/// Where a conch note stands: waiting to be sent, sent and not answered
/// (and whether the agent it went to has stopped working), or answered by
/// that agent. A note the person answered back waits to be sent again.
public enum NoteState: Equatable, Sendable {
    case waiting
    case sent(to: String, agentStopped: Bool)
    case replied(by: String)
    case github
}

/// The state of c; agentStopped says whether the agent it was sent to is
/// no longer working (done, idle, waiting on the person, or gone).
public func noteState(_ c: ReviewComment, agentStopped: Bool) -> NoteState {
    if c.fromGitHub { return .github }
    guard c.sentAt != nil else { return .waiting }
    if let last = c.replies?.last, let by = last.by, !by.isEmpty,
       last.at.map({ $0 >= c.sentAt! }) ?? true {
        return .replied(by: by)
    }
    return .sent(to: c.sentTo ?? "the agent", agentStopped: agentStopped)
}
