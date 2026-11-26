import Foundation

// The inspector: a column at the window's right edge saying where the
// focused split's branch stands — what it changes, the notes on it, its
// pull request's checks — without opening its Changes tab. A conch-ui
// view (WebUI.swift): what it is sent is worked out here (InspectorView,
// one JSON object), and what it may ask back is checked here
// (InspectorIntent) before the app acts on it.

/// The branch the inspector shows.
public struct InspectorTarget: Equatable, Hashable, Sendable {
    public var machine: String
    public var project: String
    public var branch: String

    public init(machine: String, project: String, branch: String) {
        self.machine = machine
        self.project = project
        self.branch = branch
    }

    /// Its Changes tab's key, which also keys the model the tab reads.
    public var changes: String { changesKey(machine, project, branch) }
}

/// The branch of the focused split: a Changes tab's own, or the branch a
/// pane is on — named by the server, or else the deepest worktree holding
/// its folder. Nothing for a pane outside a project, or anything else (a
/// browser, a file).
public func inspectorTarget(focused: String?, pane: PaneInfo?, project: ProjectInfo?) -> InspectorTarget? {
    guard let focused else { return nil }
    if let c = parseChangesKey(focused) { return InspectorTarget(machine: c.machine, project: c.project, branch: c.branch) }
    guard let pane, let pid = pane.projectID, let project, project.id == pid else { return nil }
    let machine = unkey(focused).machine
    if let b = pane.branch, !b.isEmpty { return InspectorTarget(machine: machine, project: pid, branch: b) }
    guard let cwd = pane.cwd, !cwd.isEmpty else { return nil }
    let holding = (project.worktrees ?? []).filter { w in
        let root = w.path.hasSuffix("/") ? String(w.path.dropLast()) : w.path
        return w.branch?.isEmpty == false && (cwd == root || cwd.hasPrefix(root + "/"))
    }
    guard let w = holding.max(by: { $0.path.count < $1.path.count }), let b = w.branch else { return nil }
    return InspectorTarget(machine: machine, project: pid, branch: b)
}

/// How many files and notes the column lists before "and N more": it is
/// a glance, and the Changes tab holds the rest.
public let inspectorFileCap = 6
public let inspectorNoteCap = 5
public let inspectorCheckCap = 8

/// Everything the page draws, sent to it whole on every change.
public struct InspectorView: Encodable, Equatable, Sendable {
    public typealias Colors = WebColors
    public struct File: Encodable, Equatable, Sendable {
        public var path: String
        public var code: String
        public var added: Int
        public var deleted: Int
    }
    public struct Note: Encodable, Equatable, Sendable {
        public var id: String
        public var file: String
        public var line: Int?
        public var note: String
        public var by: String?
        public var github: Bool
        public var replies: Int
        public var sent: Bool
        /// conch's own notes are resolved here; GitHub's are resolved there.
        public var canResolve: Bool
    }
    public struct Check: Encodable, Equatable, Sendable {
        public var name: String
        public var state: String
        public var url: String?
    }
    public struct PR: Encodable, Equatable, Sendable {
        public var number: Int
        public var title: String?
        public var state: String?
        public var url: String?
        public var checks: String?
        public var passed: Int?
        public var total: Int?
        public var review: String?
        public var runs: [Check]
        public var moreRuns: Int
    }

    public var colors: Colors
    /// Set when there is no branch to show, saying why.
    public var empty: String?
    public var project = ""
    public var branch = ""
    public var base: String?
    /// The branch's commits not on its base.
    public var ahead: Int?
    public var loading = false
    public var error: String?
    public var added = 0
    public var deleted = 0
    public var files: [File] = []
    public var moreFiles = 0
    public var notes: [Note] = []
    public var moreNotes = 0
    public var pr: PR?
}

/// The column with nothing to show, and why.
public func inspectorEmpty(_ why: String, theme: ConchTheme) -> InspectorView {
    InspectorView(colors: .init(theme), empty: why)
}

/// The column for a branch, from what the Changes tab's model has read:
/// its changes (nil while the first read is out), its notes, and the
/// branch as the project lists it (its pull request).
public func inspectorView(_ t: InspectorTarget, project: String, branch: BranchInfo?, changes: Changes?,
                          comments: [ReviewComment], error: String?, theme: ConchTheme) -> InspectorView {
    var v = InspectorView(colors: .init(theme))
    v.project = project
    v.branch = t.branch
    v.base = changes?.base
    v.ahead = branch?.baseAhead
    v.error = error
    v.loading = changes == nil && error == nil
    if let ch = changes {
        (v.added, v.deleted) = ch.totals
        v.files = ch.files.prefix(inspectorFileCap).map { .init(path: $0.path, code: $0.code, added: $0.added, deleted: $0.deleted) }
        v.moreFiles = max(0, ch.files.count - inspectorFileCap)
    }
    let newest = comments.sorted { ($0.at ?? .distantPast) > ($1.at ?? .distantPast) }
    v.notes = newest.prefix(inspectorNoteCap).map { c in
        .init(id: c.id ?? "", file: c.file, line: c.line, note: c.note, by: c.by, github: c.fromGitHub,
              replies: c.replies?.count ?? 0, sent: c.sentTo != nil, canResolve: !c.fromGitHub && !(c.id ?? "").isEmpty)
    }
    v.moreNotes = max(0, comments.count - inspectorNoteCap)
    if let pr = branch?.pr {
        let runs = pr.runs ?? []
        v.pr = .init(number: pr.number, title: pr.title, state: pr.state, url: pr.url, checks: pr.checks,
                     passed: pr.passed, total: pr.total, review: pr.review,
                     runs: runs.prefix(inspectorCheckCap).map { .init(name: $0.name, state: $0.state, url: $0.url) },
                     moreRuns: max(0, runs.count - inspectorCheckCap))
    }
    return v
}

/// What the page asks of the app. Nothing else is acted on.
public enum InspectorIntent: Equatable, Sendable {
    /// The page has loaded and wants the view.
    case ready
    case openChanges
    case openFile(String)
    /// A conch note taken away.
    case resolve(String)
    /// A pull request or a check, in the browser: http and https only.
    case openURL(URL)
}

/// A message from the inspector, checked: the page is ours, but what it
/// draws — branch names, notes, check links — is not.
public func inspectorIntent(_ body: Any) -> InspectorIntent? {
    guard let m = webMessage(body), m.view == .inspector else { return nil }
    switch m.type {
    case "ready": return .ready
    case "openChanges": return .openChanges
    case "openFile": return m.text("path").map { .openFile($0) }
    case "resolve": return m.text("id").map { .resolve($0) }
    case "openURL": return m.url("url").map { .openURL($0) }
    default: return nil
    }
}
