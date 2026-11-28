import Foundation

// A branch's Changes drawn by conch-ui (ui/src/views/Changes.tsx): the
// files, the selected file's diff with comments on its lines, and the
// commit, checks and review. Everything it does is ChangesModel's, as it
// was for the SwiftUI view; the page is sent the model as ChangesWebView
// and says what was clicked as a ChangesIntent, checked here.

/// A note already handed to an agent, as the tab keeps it on its line.
public struct WebSentNote: Equatable, Sendable {
    public var id: String
    public var file: String
    public var line: Int?
    public var note: String
    public var to: String
    public var pane: String
    public var at: Date

    public init(id: String, file: String, line: Int?, note: String, to: String, pane: String, at: Date) {
        self.id = id
        self.file = file
        self.line = line
        self.note = note
        self.to = to
        self.pane = pane
        self.at = at
    }
}

/// Everything the view is drawn from: ChangesModel's state, gathered by
/// the app (ChangesWeb.swift there) so this stays a function of values.
public struct ChangesWebInput {
    public var changes: Changes?
    public var error: String?
    public var busy: String?
    public var selected: String?
    public var selectedCommitted = false
    public var split = false
    /// The selected file's diff (a committed file's), and its hunks (an
    /// uncommitted file's).
    public var diff: [DiffLine] = []
    public var hunks: [Hunk] = []
    public var hunkOrder: [String: [Hunk]] = [:]
    public var picks = StagePicks()
    public var commitWhat = ""
    public var live: Set<String> = []
    public var comments: [ReviewComment] = []
    public var pending: [PendingComment] = []
    public var sent: [WebSentNote] = []
    public var composingFile: String?
    public var composingLine: Int?
    public var message = ""
    public var writing = false
    public var credit = true
    public var canStage = false
    public var canThread = false
    /// The agent on the branch, credited in a commit.
    public var agentName: String?
    /// Whether the agent a note went to has stopped (by its pane).
    public var agentStopped: (String?) -> Bool = { _ in true }
    // the review
    public var notes = 0
    public var pr: PRInfo?
    public var reviewed: ReviewEvent?
    public var event: ReviewEvent = .approve
    public var reviewBody = ""
    // where notes go
    public var canPick = false
    public var branchAgents: [PaneInfo] = []
    public var startable: [(name: String, title: String)] = []
    public var hasAgent = false
    // checks
    public var conflicts: [String] = []
    public var baseUnpushed = 0
    public var ciCard: CICard?
    public var canInstallCI = false
    public var canRerun = false
    public var fixLabel = "Start an agent on the failure"
    public var now = Date()

    public init() {}
}

public struct ChangesWebView: Encodable, Equatable, Sendable {
    public struct Line: Encodable, Equatable, Sendable {
        public var kind: String
        public var text: String
        public var old: Int?
        public var new: Int?
        /// The line a comment on it is kept on.
        public var cl: Int?
    }
    public struct File: Encodable, Equatable, Sendable {
        public var path: String
        public var code: String
        public var added: Int
        public var deleted: Int
        public var binary: Bool
        public var stage: String
        /// Its tick box, where it has one: on, mixed or off.
        public var tick: String?
        public var notes: Int
        public var live: Bool
    }
    public struct Selected: Encodable, Equatable, Sendable {
        public var path: String
        public var committed: Bool
        public var live: Bool
    }
    public struct HunkView: Encodable, Equatable, Sendable {
        public var index: Int
        public var header: String
        public var ticked: Bool
        public var lines: [Line]
    }
    public struct Chip: Encodable, Equatable, Sendable {
        public var text: String
        public var tone: String
    }
    public struct Reply: Encodable, Equatable, Sendable {
        public var by: String
        public var note: String
        public var ago: String?
        public var agent: Bool
    }
    public struct Comment: Encodable, Equatable, Sendable {
        public var id: String
        public var line: Int
        public var by: String
        public var note: String
        public var ago: String?
        public var chip: Chip?
        public var replies: [Reply]
        public var github: Bool
        public var url: String?
        public var outdated: Bool
        public var waiting: Bool
        public var sentPane: String?
        public var sentTo: String?
        public var noAnswer: Bool
        public var canReply: Bool
        public var canResolve: Bool
    }
    public struct Pending: Encodable, Equatable, Sendable {
        public var id: String
        public var line: Int
        public var note: String
    }
    public struct Sent: Encodable, Equatable, Sendable {
        public var id: String
        public var line: Int?
        public var note: String
        public var to: String
        public var pane: String
        public var ago: String
    }
    public struct Commit: Encodable, Equatable, Sendable {
        public var files: Int
        public var message: String
        public var writing: Bool
        public var canStage: Bool
        public var canStageNow: Bool
        public var canCommit: Bool
        public var agent: String?
        public var credit: Bool
    }
    public struct Event: Encodable, Equatable, Sendable {
        public var id: String
        public var title: String
        public var help: String
    }
    public struct Review: Encodable, Equatable, Sendable {
        public var notes: Int
        public var prOpen: Bool
        public var prNumber: Int?
        public var pending: Int
        public var reviewed: String?
        public var event: String
        public var body: String
        public var canSubmit: Bool
        public var events: [Event]
    }
    public struct SendAgent: Encodable, Equatable, Sendable {
        public var id: String
        public var label: String
        public var disabled: Bool
    }
    public struct Startable: Encodable, Equatable, Sendable {
        public var name: String
        public var title: String
    }
    public struct Send: Encodable, Equatable, Sendable {
        public var canPick: Bool
        public var agents: [SendAgent]
        public var startable: [Startable]
        public var enabled: Bool
        public var branch: String
    }
    public struct Run: Encodable, Equatable, Sendable {
        public var name: String
        public var state: String
        public var url: String?
        public var `where`: String
    }
    public struct Card: Encodable, Equatable, Sendable {
        public var text: String
        public var install: String?
        public var open: String?
    }
    public struct Checks: Encodable, Equatable, Sendable {
        public var number: Int
        public var text: String
        public var status: String
        public var progress: Double?
        public var runs: [Run]
        public var failing: Bool
        public var fixLabel: String
        public var canRerun: Bool
        public var url: String?
        public var card: Card?
    }

    public var colors: WebColors
    public var branch = ""
    public var base = ""
    public var worktree = false
    public var loading = false
    public var busy: String?
    public var files: [File] = []
    public var committed: [File] = []
    public var commits: [CommitView] = []
    public var allTick: String?
    public var commitWhat = ""
    public var selected: Selected?
    public var split = false
    public var hunks: [HunkView] = []
    public var diff: [Line] = []
    public var comments: [Comment] = []
    public var pending: [Pending] = []
    public var sent: [Sent] = []
    public var composing: Int?
    public var commit: Commit
    public var review: Review
    public var send: Send
    public var checks: Checks?

    public struct CommitView: Encodable, Equatable, Sendable {
        public var hash: String
        public var subject: String
    }
}

extension Tick {
    /// How a view names it.
    public var word: String {
        switch self {
        case .on: return "on"
        case .mixed: return "mixed"
        case .off: return "off"
        }
    }
}

extension DiffLine.Kind {
    public var word: String {
        switch self {
        case .meta: return "meta"
        case .hunk: return "hunk"
        case .context: return "context"
        case .added: return "added"
        case .removed: return "removed"
        }
    }
}

/// The view's commits are capped as the native list was.
public let changesWebCommitCap = 20

/// The view for a branch's Changes, from the model's state.
public func changesWebView(_ i: ChangesWebInput, theme: ConchTheme) -> ChangesWebView {
    func line(_ l: DiffLine) -> ChangesWebView.Line {
        .init(kind: l.kind.word, text: l.text, old: l.old, new: l.new, cl: l.kind == .context || l.kind == .added || l.kind == .removed ? commentLine(l) : nil)
    }
    let ch = i.changes
    let worktree = ch?.worktree != nil
    let path = i.selected ?? ""
    func file(_ f: FileChange, committed: Bool) -> ChangesWebView.File {
        .init(path: f.path, code: f.code, added: f.added, deleted: f.deleted, binary: f.binary == true,
              stage: committed ? "off" : f.stage.word,
              tick: !committed && worktree ? i.picks.state(f.path, of: i.hunkOrder[f.path]).word : nil,
              notes: openComments(i.comments, file: f.path) + i.pending.filter { $0.file == f.path }.count,
              live: i.live.contains(f.path))
    }
    var v = ChangesWebView(
        colors: WebColors(theme),
        commit: .init(files: 0, message: i.message, writing: i.writing, canStage: i.canStage, canStageNow: false,
                      canCommit: false, agent: i.agentName, credit: i.credit),
        review: .init(notes: i.notes, prOpen: i.pr?.state == "OPEN", prNumber: i.pr?.number, pending: i.pending.count,
                      reviewed: i.reviewed?.rawValue, event: i.event.rawValue, body: i.reviewBody,
                      canSubmit: i.event.canSubmit(body: i.reviewBody, comments: i.pending.count) && i.busy == nil,
                      events: ReviewEvent.allCases.map { .init(id: $0.rawValue, title: $0.title, help: $0.help) }),
        send: .init(canPick: i.canPick,
                    agents: i.branchAgents.map { a in
                        let name = a.name.isEmpty ? (a.agent?.name ?? a.id) : a.name
                        let state = a.agent?.state == .blocked ? " — waiting on you" : a.agent?.state == .working ? " — working" : ""
                        return .init(id: a.id, label: name + state, disabled: a.agent?.state == .blocked)
                    },
                    startable: i.startable.map { .init(name: $0.name, title: $0.title) },
                    enabled: i.notes > 0 && i.busy == nil && (i.canPick || i.hasAgent),
                    branch: ch?.branch ?? ""))
    v.loading = ch == nil && i.error == nil
    v.busy = i.busy
    guard let ch else { return v }
    v.branch = ch.branch
    v.base = ch.base
    v.worktree = worktree
    if worktree {
        v.files = ch.files.map { file($0, committed: false) }
        v.committed = (ch.committedFiles ?? []).map { file($0, committed: true) }
        if !ch.files.isEmpty { v.allTick = i.picks.allState(ch.files.map(\.path), hunks: i.hunkOrder).word }
    } else {
        v.files = ch.files.map { file($0, committed: true) }
    }
    v.commits = (ch.commits ?? []).prefix(changesWebCommitCap).map { .init(hash: $0.hash, subject: $0.subject) }
    v.commitWhat = i.commitWhat
    v.split = i.split
    if let p = i.selected {
        v.selected = .init(path: p, committed: i.selectedCommitted, live: i.live.contains(p))
        if i.hunks.isEmpty {
            v.diff = i.diff.filter { $0.kind != .meta }.map(line)
        } else {
            v.hunks = i.hunks.map { h in
                .init(index: h.index, header: h.header,
                      ticked: i.picks.whole.contains(p) || (i.picks.hunks[p]?.contains(h.text) ?? false),
                      lines: h.lines.map(line))
            }
        }
    }
    // The selected file's threads: comments on a line, the pending review
    // comments, and notes sent from here.
    v.comments = i.comments.filter { $0.file == path && $0.line != nil }.map { c in
        let state = noteState(c, agentStopped: i.agentStopped(c.sentPane))
        var chip: ChangesWebView.Chip
        var waiting = false, noAnswer = false, threaded = false
        switch state {
        case .github: chip = .init(text: "on the pull request", tone: "text")
        case .waiting:
            waiting = true
            chip = .init(text: (c.replies ?? []).isEmpty ? "note for the agent" : "answered back · waiting to be sent", tone: "agent")
        case .sent(let to, let stopped):
            threaded = true
            noAnswer = stopped
            chip = .init(text: stopped ? "sent to \(to) · no answer" : "sent to \(to) · working", tone: stopped ? "muted" : "work")
        case .replied(let by):
            threaded = true
            chip = .init(text: "answered by \(by)", tone: "ok")
        }
        return .init(id: c.id ?? "", line: c.line ?? 0, by: c.by ?? "you", note: c.note, ago: c.at.map { agoText($0, now: i.now) },
                     chip: chip,
                     replies: (c.replies ?? []).map { r in
                         let agent = !(r.by ?? "").isEmpty
                         return .init(by: agent ? r.by! : "you", note: r.note, ago: r.at.map { agoText($0, now: i.now) }, agent: agent)
                     },
                     github: c.fromGitHub, url: c.url, outdated: c.outdated == true, waiting: waiting,
                     sentPane: threaded ? c.sentPane : nil, sentTo: threaded ? c.sentTo : nil, noAnswer: noAnswer,
                     canReply: threaded && i.canThread, canResolve: !c.fromGitHub && !(c.id ?? "").isEmpty)
    }
    v.pending = i.pending.filter { $0.file == path }.map { .init(id: $0.id.uuidString, line: $0.line, note: $0.note) }
    v.sent = i.sent.filter { $0.file == path }.map { .init(id: $0.id, line: $0.line, note: $0.note, to: $0.to, pane: $0.pane, ago: agoText($0.at, now: i.now)) }
    if i.composingFile == path { v.composing = i.composingLine }
    let files = ch.files.count
    let blank = i.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    v.commit.files = files
    v.commit.canStageNow = i.canStage && !i.picks.isEmpty && i.busy == nil
    v.commit.canCommit = files > 0 && !blank && i.busy == nil
    if let pr = i.pr {
        let st = prStatus(pr, base: ch.base, conflicts: i.conflicts, baseUnpushed: i.baseUnpushed)
        v.checks = .init(number: pr.number, text: st.ci.text, status: pr.checks ?? "none",
                         progress: pr.checks == "pending" || pr.checks == "fail" ? st.progress : nil,
                         runs: (pr.runs ?? []).prefix(10).map { .init(name: $0.name, state: $0.state, url: $0.url, where: checkWhere($0)) },
                         failing: st.failing, fixLabel: i.fixLabel, canRerun: i.canRerun, url: pr.url,
                         card: i.ciCard.map { card in
                             .init(text: card.text(agent: i.agentName), install: i.canInstallCI ? card.install : nil,
                                   open: card.check.url == nil ? nil : card.open)
                         })
    }
    return v
}

/// What the Changes view asks of the app.
public enum ChangesIntent: Equatable, Sendable {
    case ready
    case select(path: String, committed: Bool)
    case tickAll
    case tickFile(path: String, shift: Bool)
    case tickHunk(Int)
    case stageFile(path: String, unstage: Bool)
    case copyPath(String)
    case split(Bool)
    case compose(line: Int)
    case cancelCompose
    case addComment(line: Int, note: String, forPR: Bool)
    case reply(id: String, note: String)
    case resolve(String)
    case dropPending(String)
    case dismissSent(String)
    case openPane(String)
    case openURL(URL)
    /// To an agent on the branch (its pane), a new one (its name), or the
    /// server's choice.
    case sendNotes(to: String?, start: String?)
    case message(String)
    case credit(Bool)
    case writeMessage, stage, commit
    case event(ReviewEvent)
    case reviewBody(String)
    case submitReview, reviewAgain, openPR, fixCI, rerunCI, installCI, openCIService
}

/// The longest text the view may hand over: a note, a message, a review.
public let changesWebTextCap = 100_000

/// A message from the Changes view, checked.
public func changesIntent(_ body: Any) -> ChangesIntent? {
    guard let m = webMessage(body), m.view == .changes else { return nil }
    func text(_ k: String) -> String? { m.string(k).flatMap { $0.count <= changesWebTextCap ? $0 : nil } }
    switch m.type {
    case "ready": return .ready
    case "select":
        guard let p = m.text("path"), let c = m.bool("committed") else { return nil }
        return .select(path: p, committed: c)
    case "tickAll": return .tickAll
    case "tickFile":
        guard let p = m.text("path") else { return nil }
        return .tickFile(path: p, shift: m.bool("shift") ?? false)
    case "tickHunk": return m.int("index").flatMap { $0 >= 0 ? .tickHunk($0) : nil }
    case "stageFile":
        guard let p = m.text("path"), let u = m.bool("unstage") else { return nil }
        return .stageFile(path: p, unstage: u)
    case "copyPath": return m.text("path").map { .copyPath($0) }
    case "split": return m.bool("on").map { .split($0) }
    case "compose": return m.int("line").flatMap { $0 > 0 ? .compose(line: $0) : nil }
    case "cancelCompose": return .cancelCompose
    case "addComment":
        guard let l = m.int("line"), l > 0, let n = text("note"), !n.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              let pr = m.bool("pr") else { return nil }
        return .addComment(line: l, note: n, forPR: pr)
    case "reply":
        guard let id = m.text("id"), let n = text("note"), !n.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return .reply(id: id, note: n)
    case "resolve": return m.text("id").map { .resolve($0) }
    case "dropPending": return m.text("id").map { .dropPending($0) }
    case "dismissSent": return m.text("id").map { .dismissSent($0) }
    case "openPane": return m.text("pane").map { .openPane($0) }
    case "openURL": return m.url("url").map { .openURL($0) }
    case "sendNotes":
        let to = m.text("to"), start = m.text("start")
        if to != nil && start != nil { return nil }
        return .sendNotes(to: to, start: start)
    case "message": return text("text").map { .message($0) }
    case "credit": return m.bool("on").map { .credit($0) }
    case "writeMessage": return .writeMessage
    case "stage": return .stage
    case "commit": return .commit
    case "event": return m.text("id").flatMap(ReviewEvent.init(rawValue:)).map { .event($0) }
    case "reviewBody": return text("text").map { .reviewBody($0) }
    case "submitReview": return .submitReview
    case "reviewAgain": return .reviewAgain
    case "openPR": return .openPR
    case "fixCI": return .fixCI
    case "rerunCI": return .rerunCI
    case "installCI": return .installCI
    case "openCIService": return .openCIService
    default: return nil
    }
}
