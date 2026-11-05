import AppKit
import ConchKit
import SwiftUI

// A branch's git, in a tab or split of its own: three views on one control
// (GitTab.swift draws them). Overview is what a branch is when there is
// nothing to commit — how it stands against its upstream, its recent
// commits, a fortnight of activity, who changed it, the other branches
// against the base. Changes is its uncommitted work reviewed and committed
// hunk by hunk, with comments on lines (notes for its agent, or comments
// for its pull request) and a review of the pull request submitted from
// here. Graph is the project's commit graph, with a commit's details and
// a compare of two. Read from the branch's machine's server; the diff
// follows an agent's edits as the server announces them.

@MainActor
final class ChangesModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    let branch: String
    weak var store: Store?

    enum Mode: String, CaseIterable { case overview = "Overview", changes = "Changes", graph = "Graph" }
    /// The view chosen; nil until a choice is made, when it follows what
    /// there is: changes when there are some, the overview when not.
    @Published var chosen: Mode?

    @Published var changes: Changes?
    @Published var selected: String?
    /// Whether the selected file is one the branch committed (its diff is
    /// against the base) rather than uncommitted work.
    @Published var selectedCommitted = false
    @Published var diff: [DiffLine] = []
    /// The selected file's diff in hunks, for an uncommitted file.
    @Published var fileHunks: (header: String, hunks: [Hunk]) = ("", [])
    /// Every uncommitted file's hunks read so far, for the patch's order.
    var hunkOrder: [String: [Hunk]] = [:]
    @Published var error: String?
    @Published var busy: String?
    /// Files written in the last few seconds, marked as the TUI marks them.
    @Published var live: Set<String> = []
    /// What the next commit takes: whole files or some of their hunks;
    /// nothing ticked commits everything.
    @Published var picks = StagePicks()
    @Published var split = false
    @Published var message = ""
    @Published var credit = true
    @Published var writing = false

    // comments and the review
    @Published var comments: [ReviewComment] = []
    @Published var pending: [PendingComment] = []
    @Published var composingFile: String?
    @Published var composingLine: Int?
    /// The file box clicked last, where a shift-click's run starts.
    var tickAnchor: String?
    /// Notes already handed to an agent, kept on their lines while this tab
    /// is open: the server forgets a note once it is delivered.
    @Published var sent: [SentNote] = []
    @Published var event: ReviewEvent = .approve
    @Published var reviewBody = ""
    @Published var reviewed: ReviewEvent?

    // the overview
    @Published var recent: [LogCommit] = []

    var mode: Mode {
        if let c = chosen { return c }
        guard let ch = changes else { return .changes }
        return ch.files.isEmpty && (ch.committedFiles ?? []).isEmpty ? .overview : .changes
    }

    init(key: String, store: Store) {
        self.key = key
        let (m, p, b) = parseChangesKey(key) ?? ("local", "", "")
        machine = m
        project = p
        branch = b
        self.store = store
        Task { await load() }
    }

    struct Ref: Encodable {
        var project_id: String
        var branch: String
        var committed: Bool?
        var file: String?
    }

    private var reads = Latest(), diffs = Latest()

    var connection: Connection? { store?.connection(machine) }
    func can(_ cap: String) -> Bool { connection?.hello?.capabilities?.contains(cap) == true }

    func load() async {
        guard let c = connection else {
            error = "\(store?.label(machine) ?? machine) is not connected"
            return
        }
        let n = reads.next()
        do {
            let ch = try await c.call("project.changes", Ref(project_id: project, branch: branch, committed: true), as: Changes.self)
            guard reads.isLatest(n) else { return }
            changes = ch
            error = nil
            let all = ch.files.map(\.path) + (ch.committedFiles ?? []).map(\.path)
            for p in picks.whole where !ch.files.contains(where: { $0.path == p }) { picks.whole.remove(p) }
            if selected == nil || !all.contains(selected!) {
                if let f = ch.files.first {
                    select(f.path, committed: false)
                } else if let f = ch.committedFiles?.first {
                    select(f.path, committed: true)
                } else {
                    selected = nil
                    diff = []
                    fileHunks = ("", [])
                }
            } else {
                await loadDiff()
            }
        } catch {
            if reads.isLatest(n) { self.error = "\(error)" }
        }
        await loadComments()
        if mode == .overview { await loadRecent() }
    }

    func select(_ path: String, committed: Bool) {
        selected = path
        selectedCommitted = committed
        composingFile = nil
        Task { await loadDiff() }
    }

    func loadDiff() async {
        guard let c = connection, let path = selected else { return }
        let n = diffs.next()
        do {
            var text: String
            if selectedCommitted, let base = changes?.base {
                struct Git: Encodable {
                    var project_id: String
                    var branch: String
                    var commands: [[String]]
                }
                struct Out: Decodable { var output: String? }
                let out = try await c.call("branch.git", Git(project_id: project, branch: branch,
                                                              commands: [["diff", "\(base)...HEAD", "--", path]]), as: Out.self)
                // The output starts with the "$ git …" line it ran.
                text = (out.output ?? "").split(separator: "\n", omittingEmptySubsequences: false).drop { !$0.hasPrefix("diff ") }.joined(separator: "\n")
            } else {
                text = try await c.call("project.diff", Ref(project_id: project, branch: branch, file: path), as: DiffResult.self).diff
            }
            guard diffs.isLatest(n) else { return }
            diff = parseDiff(text)
            if !selectedCommitted {
                let h = splitHunks(text)
                fileHunks = h
                hunkOrder[path] = h.hunks
                picks.prune(path, header: h.header, hunks: h.hunks)
            } else {
                fileHunks = ("", [])
            }
        } catch {
            if diffs.isLatest(n) {
                diff = [DiffLine(id: 0, kind: .meta, text: "\(error)")]
                fileHunks = ("", [])
            }
        }
    }

    /// The server says files in this branch's worktree were written: read
    /// again, and mark them for a moment.
    func written(_ paths: [String], worktree: String) {
        guard changes?.worktree == nil || changes?.worktree == worktree else { return }
        let rel = paths.map { $0.hasPrefix(worktree + "/") ? String($0.dropFirst(worktree.count + 1)) : $0 }
        live.formUnion(rel)
        Task { await load() }
        DispatchQueue.main.asyncAfter(deadline: .now() + 4) { [weak self] in self?.live.subtract(rel) }
    }

    func written(worktree: String) { written([], worktree: worktree) }

    // MARK: committing

    /// The agent working on this branch, for the commit's credit and the
    /// notes' recipient.
    /// The agent this tab speaks of: the one handed work while it runs,
    /// else one at work on the branch, else the oldest there.
    var agent: PaneInfo? { ConchKit.branchAgent(branchAgents, handedTo: handedPane) }

    /// Work handed to an agent from here — a conflict, a failed run — said
    /// under the header until it is done and dismissed.
    struct Handed: Equatable {
        var what: String
        var pane: String?
        var at: Date
    }
    @Published var handed: Handed?

    /// The pane the handed work went to: named, or — sent through conch
    /// branch fix-ci, which picks or starts one itself — the branch's agent
    /// at work, else the newest one started since.
    var handedPane: String? {
        guard let h = handed else { return nil }
        if let p = h.pane { return p }
        let agents = branchAgents
        return (agents.first { $0.agent?.state == .working }
            ?? agents.filter { ($0.created ?? .distantPast) >= h.at.addingTimeInterval(-5) }.last
            ?? agents.first)?.id
    }

    var handedState: HandoffState? {
        guard let h = handed else { return nil }
        return handoffState(store?.snapshots[machine]?.panes.first { $0.id == handedPane }, handedAt: h.at)
    }

    /// What Commit takes, said: everything, or what is ticked.
    /// Whether this machine's server stages and commits the index alone.
    var canStage: Bool { can("branch.stage.v1") }

    var scope: CommitScope {
        commitScope(picked: !picks.isEmpty, files: changes?.files ?? [], canStage: canStage)
    }

    var commitWhat: String {
        guard changes != nil else { return "" }
        switch scope {
        case .everything(let n): return "every change (\(n) file\(n == 1 ? "" : "s"))"
        case .staged(let n): return "what is staged (\(n) file\(n == 1 ? "" : "s"))"
        case .picked: break
        }
        let s = picks.selection(order: hunkOrder)
        var parts: [String] = []
        if !s.files.isEmpty { parts.append("\(s.files.count) file\(s.files.count == 1 ? "" : "s")") }
        if s.hunks > 0 { parts.append("\(s.hunks) hunk\(s.hunks == 1 ? "" : "s")") }
        return parts.joined(separator: " and ")
    }

    func commit() {
        let text = credit ? withTrailer(message, agent: agent?.agent?.name) : message
        let s = picks.selection(order: hunkOrder)
        if !s.patch.isEmpty && !can("branch.hunks.v1") {
            error = "conch on \(store?.label(machine) ?? machine) predates committing single hunks: tick whole files instead"
            return
        }
        var staged = false
        if case .staged = scope { staged = true }
        run("Committing", done: "Committed on \(branch)") { c in
            struct Commit: Encodable {
                var project_id: String
                var branch: String
                var message: String
                var files: [String]?
                var patch: String?
                var staged: Bool?
            }
            try await c.call("branch.commit", Commit(project_id: self.project, branch: self.branch, message: text,
                                                       files: s.files.isEmpty ? nil : s.files, patch: s.patch.isEmpty ? nil : s.patch,
                                                       staged: staged ? true : nil))
            self.picks = StagePicks()
            self.message = ""
        }
    }

    /// git add: what is ticked put in the index, the ticks then cleared —
    /// the stage itself says what is staged now.
    func stage() {
        guard canStage else {
            error = "conch on \(store?.label(machine) ?? machine) is too old to stage: reload its server"
            return
        }
        let s = picks.selection(order: hunkOrder)
        guard !s.files.isEmpty || !s.patch.isEmpty else { return }
        let n = s.files.count + s.hunks
        run("Staging", done: "Staged \(n) \(n == 1 ? "change" : "changes")") { c in
            try await c.call("branch.stage", StageParams(project_id: self.project, branch: self.branch,
                                                         files: s.files.isEmpty ? nil : s.files, patch: s.patch.isEmpty ? nil : s.patch))
            self.picks = StagePicks()
        }
    }

    /// One file put in the index, or taken out of it, whole.
    func stage(file: String, unstage: Bool) {
        guard canStage else {
            error = "conch on \(store?.label(machine) ?? machine) is too old to stage: reload its server"
            return
        }
        run(unstage ? "Unstaging" : "Staging", done: (unstage ? "Unstaged " : "Staged ") + (file as NSString).lastPathComponent) { c in
            try await c.call("branch.stage", StageParams(project_id: self.project, branch: self.branch, files: [file], unstage: unstage ? true : nil))
        }
    }

    struct StageParams: Encodable {
        var project_id: String
        var branch: String
        var files: [String]?
        var patch: String?
        var unstage: Bool?
    }

    /// Writes the message from what is about to be committed, with the
    /// brain set in config.toml, by this computer's conch.
    func writeMessage() {
        guard let c = connection, let bin = store?.binary, !writing else { return }
        writing = true
        Task {
            defer { writing = false }
            do {
                let s = picks.selection(order: hunkOrder)
                var diff = s.patch
                let all = changes?.files ?? []
                let whole: [String]
                switch scope {
                case .picked: whole = s.files
                case .staged: whole = all.filter { $0.staged == true }.map(\.path)
                case .everything: whole = all.map(\.path)
                }
                for f in whole.prefix(30) {
                    if let d = try? await c.call("project.diff", Ref(project_id: project, branch: branch, file: f), as: DiffResult.self) {
                        diff += d.diff
                    }
                }
                var args = ["commit-message"]
                if let a = agent, let t = a.title, !t.isEmpty { args += ["-context", t] }
                let out = try await runConch(bin, args, input: Data(diff.utf8))
                message = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            } catch {
                self.error = "\(error)"
            }
        }
    }

    // MARK: comments and the review

    func loadComments() async {
        guard let c = connection, can("review.comments.v1") else { return }
        // Sent notes too, with their answers, where the server keeps them.
        struct R: Encodable { var project_id: String; var branch: String; var sent: Bool? }
        if let l = try? await c.call("review.list", R(project_id: project, branch: branch, sent: canThread ? true : nil), as: ReviewList.self) {
            comments = l.comments
        }
    }

    /// The files merging this branch into the base would conflict in, as
    /// the server tried without merging.
    var baseConflicts: [String] {
        store?.snapshots[machine]?.project(project)?.worktrees?.first { $0.branch == branch }?.conflicts ?? []
    }

    /// Hands a piece of work to the branch's agent — the one working here,
    /// or one started in the worktree with it — and goes to it.
    func handToAgent(_ text: String, what: String, doing: String) {
        guard let s = store else { return }
        if let a = agent {
            run(what, done: "Sent to \(a.name.isEmpty ? (a.agent?.name ?? "the agent") : a.name)") { c in
                struct P: Encodable { var id: String; var text: String }
                try await c.call("agent.prompt", P(id: a.id, text: text))
                self.handed = Handed(what: doing, pane: a.id, at: Date())
            }
            return
        }
        guard let dir = changes?.worktree else {
            error = "\(branch) is not checked out anywhere, so there is nowhere to start an agent on it"
            return
        }
        let name = s.startable(on: machine).first { $0.installed }?.name ?? "claude"
        run(what, done: "Started \(name) on \(branch)") { c in
            struct P: Encodable { var agent: String; var prompt: String; var cwd: String }
            let info = try await c.call("pane.create", P(agent: name, prompt: text, cwd: dir), as: PaneInfo.self)
            self.handed = Handed(what: doing, pane: info.id, at: Date())
            s.open(ConchKit.key(self.machine, info.id))
        }
    }

    /// The pull request's conflict with the base, given to the agent.
    func resolveConflicts() {
        guard let pr = branchInfo?.pr, let ch = changes else { return }
        handToAgent(conflictPrompt(number: pr.number, branch: branch, base: ch.base, files: baseConflicts),
                    what: "Asking for the conflicts to be resolved", doing: "resolving the conflict with \(ch.base)")
    }

    /// CI's failure sent to the agent, as the review queue's Fix does.
    func fixCI() {
        store?.fixCI(machine: machine, project: project, branch: branch)
        handed = Handed(what: "fixing the failed checks", pane: nil, at: Date())
    }

    var branchInfo: BranchInfo? {
        store?.snapshots[machine]?.project(project)?.branches?.first { $0.name == branch }
    }

    /// Whether the branch has an open pull request a comment can wait for.
    var prOpen: Bool {
        store?.snapshots[machine]?.project(project)?.branches?.first { $0.name == branch }?.pr?.state == "OPEN"
    }

    func compose(file: String, line: Int) {
        composingFile = file
        composingLine = line
    }

    /// A comment written on a line: a note for the agent (kept by conch),
    /// or one waiting in the review for the pull request.
    func addComment(_ note: String, file: String, line: Int, forPR: Bool) {
        let text = note.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        composingFile = nil
        if forPR {
            pending.append(PendingComment(file: file, line: line, note: text))
            return
        }
        guard let c = connection, can("review.comments.v1") else {
            error = "conch on \(store?.label(machine) ?? machine) is too old to keep notes on lines: reload its server"
            return
        }
        Task {
            do {
                let l = try await c.call("review.set", ReviewComment(projectID: project, branch: branch, file: file, line: line, note: text), as: ReviewList.self)
                comments = l.comments
            } catch { self.error = "\(error)" }
        }
    }

    /// A note resolved: taken away. One from GitHub is resolved there.
    func resolve(_ c: ReviewComment) {
        guard let id = c.id, !c.fromGitHub, let conn = connection else { return }
        let gone = ReviewComment(projectID: c.projectID, branch: c.branch, file: c.file, line: c.line, note: "", id: id)
        Task {
            if let l = try? await conn.call("review.set", gone, as: ReviewList.self) { comments = l.comments }
        }
    }

    func dropPending(_ p: PendingComment) { pending.removeAll { $0.id == p.id } }

    /// The notes waiting to be sent: not GitHub's, and not sent already.
    var notes: [ReviewComment] { comments.filter { !$0.fromGitHub && $0.sentAt == nil } }

    /// Whether this machine's server keeps sent notes for answers.
    var canThread: Bool { can("review.thread.v1") }

    /// Whether the agent a note went to has stopped working on it: done,
    /// idle, waiting on the person, or gone — so silence is said as such.
    func agentStopped(_ pane: String?) -> Bool {
        guard let pane, let p = store?.snapshots[machine]?.panes.first(where: { $0.id == pane }), p.running else { return true }
        return p.agent?.state != .working
    }

    /// The person answering a note: it waits to be sent again, its thread
    /// with it.
    func reply(_ c: ReviewComment, _ note: String) {
        let text = note.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let id = c.id, !text.isEmpty, let conn = connection else { return }
        struct P: Encodable { var project_id: String; var id: String; var note: String }
        Task {
            do {
                _ = try await conn.call("review.reply", P(project_id: project, id: id, note: text), as: ReviewComment.self)
                await loadComments()
            } catch { self.error = "\(error)" }
        }
    }

    /// The notes handed to the agent working on the branch.
    /// The running agents on this branch, oldest first: who the notes can go to.
    var branchAgents: [PaneInfo] {
        ConchKit.branchAgents(store?.snapshots[machine]?.panes ?? [], project: project, branch: branch)
    }

    /// Whether this machine's server takes the agent to send to.
    var canPick: Bool { can("review.send.to.v1") }

    /// The notes handed to an agent: the one picked (pane), a new one
    /// (start, an agent's name), or — with neither — the one the server
    /// finds on the branch. What was sent stays on its line, said as sent,
    /// since the server takes delivered notes away.
    func sendNotes(to pane: String? = nil, start: String? = nil) {
        if (pane != nil || start != nil) && !canPick {
            error = "conch on \(store?.label(machine) ?? machine) is too old to send to an agent you pick: reload its server"
            return
        }
        let given = notes
        guard !given.isEmpty else { return }
        run("Sending the notes") { c in
            struct S: Encodable {
                var project_id: String; var branch: String
                var pane: String?; var agent: String?; var new: Bool?
            }
            struct R: Decodable { var pane: PaneInfo; var started: Bool?; var sent: Int }
            let r = try await c.call("review.send", S(project_id: self.project, branch: self.branch, pane: pane, agent: start,
                                                      new: start != nil ? true : nil), as: R.self)
            let to = r.pane.name.isEmpty ? (r.pane.agent?.name ?? r.pane.id) : r.pane.name
            let at = Date()
            // A server that keeps sent notes lists them itself; for one from
            // before, the app remembers them while the tab is open.
            if !self.canThread {
                self.sent += given.map { SentNote(file: $0.file, line: $0.line, note: $0.note, to: to, pane: r.pane.id, at: at) }
            }
            self.store?.say((r.started == true ? "Started \(to) with " : "Sent ") + "\(r.sent) note\(r.sent == 1 ? "" : "s")" + (r.started == true ? "" : " to \(to)"))
        }
    }

    /// The review submitted to the pull request, with the comments waiting
    /// in it; requesting changes sends the notes to the agent as well.
    func submitReview() {
        guard event.canSubmit(body: reviewBody, comments: pending.count) else { return }
        guard can("pr.review.v1") else {
            error = "conch on \(store?.label(machine) ?? machine) is too old to review a pull request: reload its server"
            return
        }
        let ev = event
        let sub = ReviewSubmit(projectID: project, branch: branch, event: ev.rawValue, body: reviewBody, comments: pending)
        run("Submitting the review", done: ev == .approve ? "Approved the pull request" : ev == .requestChanges ? "Requested changes" : "Commented on the pull request") { c in
            try await c.call("branch.review", sub)
            self.pending = []
            self.reviewBody = ""
            self.reviewed = ev
            if ev == .requestChanges && !self.notes.isEmpty, self.agent != nil {
                struct S: Encodable { var project_id: String; var branch: String }
                _ = try? await c.call("review.send", S(project_id: self.project, branch: self.branch))
            }
        }
    }

    // MARK: the overview

    /// The branch's last month of commits, read in its checkout; for a
    /// branch with none, the commits it has against the base.
    func loadRecent() async {
        guard let c = connection else { return }
        struct Git: Encodable {
            var project_id: String
            var branch: String
            var commands: [[String]]
        }
        struct Out: Decodable { var output: String?; var exit_code: Int? }
        if let out = try? await c.call("branch.git", Git(project_id: project, branch: branch,
                                                          commands: [["log", "-n", "400", "--since=30.days", "--format=%H%x1f%s%x1f%an%x1f%ct"]]), as: Out.self),
           (out.exit_code ?? 0) == 0 {
            recent = parseLogLines(out.output ?? "")
            return
        }
        recent = (changes?.commits ?? []).map { ci in
            var c = LogCommit(hash: ci.hash, subject: ci.subject)
            c.author = ci.author
            c.time = ci.time
            return c
        }
    }

    // MARK: shipping

    func merge(squash: Bool) {
        let base = changes?.base ?? "the base"
        run("Merging into \(base)", done: "Merged \(branch) into \(base)") { c in
            struct Merge: Encodable { var project_id: String; var branch: String; var squash: Bool? }
            try await c.call("branch.merge", Merge(project_id: self.project, branch: self.branch, squash: squash ? true : nil))
        }
    }

    func push() {
        run("Pushing", done: "Pushed \(branch)") { c in
            try await c.call("branch.push", Ref(project_id: self.project, branch: self.branch))
        }
    }

    func openPR() {
        run("Opening a pull request") { c in
            struct PR: Decodable { var url: String }
            let pr = try await c.call("branch.pr", Ref(project_id: self.project, branch: self.branch), as: PR.self)
            if let u = URL(string: pr.url) { NSWorkspace.shared.open(u) }
        }
    }

    private func run(_ what: String, done: String? = nil, _ body: @escaping (Connection) async throws -> Void) {
        guard let c = connection, busy == nil else { return }
        busy = what
        Task {
            defer { busy = nil }
            do {
                try await body(c)
                if let done { store?.say(done) }
                await load()
            } catch {
                self.error = "\(error)"
                store?.say("\(what) failed: \(error)", .err)
            }
        }
    }
}

/// A note that went to an agent: where it was, what it said, and to whom.
struct SentNote: Identifiable, Equatable {
    let id = UUID()
    var file: String
    var line: Int?
    var note: String
    var to: String
    var pane: String
    var at: Date
}
