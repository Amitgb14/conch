import AppKit
import ConchKit
import SwiftUI

// A project's commit graph, in a tab of its own: every branch and tag of
// the repository, newest first, each commit a dot on its branch's lane
// with the lines git log --graph draws, its refs as chips, its subject,
// author and age. Opened from a project's or a branch's menu; a branch
// opens it at that branch's newest commit.

@MainActor
final class CommitsModel: ObservableObject {
    let key: String
    let machine: String
    let project: String

    /// Open pull requests by the commit their checks run on (their head),
    /// for a badge on that row.
    var pullRequests: [String: PRInfo] {
        var out: [String: PRInfo] = [:]
        for b in store.snapshots[machine]?.project(project)?.branches ?? [] {
            if let pr = b.pr, (pr.state ?? "OPEN") == "OPEN", let h = pr.head, !h.isEmpty { out[h] = pr }
        }
        return out
    }
    unowned let store: Store
    @Published var rows: [GraphRow] = []
    @Published var more = false
    @Published var loading = false
    @Published var error = ""
    /// The commit picked, and a branch to bring into view once read.
    @Published var selected: String?
    @Published var focusBranch: String?
    /// Counts the times the picked commit is to be brought into view: a
    /// branch's tip already picked is shown again, which a change of the
    /// pick alone never said.
    @Published var reveal = 0
    /// The repository's remotes, as the server names them (nil: an older
    /// server, and origin and upstream are taken).
    @Published var remotes: Set<String>?
    /// How the repository stores itself (nil from an older server).
    @Published var format: RepoFormat?
    /// Which agent branch each commit is on, by hash.
    @Published var owners: [String: String] = [:]
    /// A branch picked in the side's list: the rest of the graph dims.
    @Published var focusOn: String?
    /// The CI panel or the autopilot's in place of the commit's — one at a
    /// time — and what each shows.
    @Published var showCI = false { didSet { if showCI { showAutopilot = false } } }
    @Published var showAutopilot = false { didSet { if showAutopilot { showCI = false } } }
    var sidePanel: Bool { showCI || showAutopilot }
    @Published var activity: [AutopilotNote] = []
    @Published var autopilot: AutopilotState?
    @Published var activityError = ""
    private var activityReads = Latest()
    @Published var ci: CIStats?
    @Published var ciDays = 14
    @Published var ciError = ""
    @Published var ciLoading = false
    /// Runs asked to start again, by run id, with the attempt that failed:
    /// the button says so until a later attempt shows up.
    @Published var rerunning: [Int64: Int] = [:]
    private var ciReads = Latest()
    /// Branches rebased and not pushed (branch.rebased), by name.
    @Published var rebases: [String: BranchRebase] = [:]
    /// The rebase being reviewed in a sheet, with git range-diff's account.
    @Published var reviewing: BranchRebase?
    @Published var reviewError = ""
    /// Branches whose rewrite is going up, or being undone.
    @Published var pushing: Set<String> = []
    private var rebaseReads = Latest()
    /// The graph's search: commits that do not match are dimmed, never
    /// taken out, so the lanes stay whole.
    @Published var query = ""
    /// A second commit picked with shift: the range compared runs between
    /// it and the one picked.
    @Published var compareWith: String?
    /// What the side panel shows: the commit picked, or the range.
    @Published var detail: CommitDetail?
    @Published var detailError = ""
    /// One file's diff opened from the side panel.
    @Published var peekFile: String?
    @Published var peek: [DiffLine] = []
    private var details = Latest()
    private var limit = proto_LogLimit
    /// Reads in flight: only the newest one's reply is taken, or a read for
    /// 300 landing after one for 600 put the shorter history back.
    private var reads = Latest()

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseCommitsKey(key) ?? ("local", "")
        load()
        store.refreshProject(machine: machine, project: project)
        loadAutopilot()
    }

    /// ↻: the history again, and the server asked to look at GitHub now.
    func refresh() {
        load()
        store.refreshProject(machine: machine, project: project)
    }

    var count: Int { rows.count }

    var projectInfo: ProjectInfo? { store.snapshots[machine]?.project(project) }

    /// The agents' branches, most urgent first.
    var branches: [GraphBranch] {
        guard let p = projectInfo else { return [] }
        return graphBranches(p, panes: store.snapshots[machine]?.panes ?? [], rebased: rebases)
    }

    /// The branch a commit is on and its entry in the side's list, if it
    /// is one of the agents'.
    func branch(of hash: String) -> GraphBranch? {
        guard let name = owners[hash] else { return nil }
        return branches.first { $0.name == name }
    }

    /// Asks an agent to resolve a branch's conflict: its own, or a new one
    /// of the agent named.
    func askToResolve(_ b: GraphBranch, start: String? = nil) {
        store.askToResolve(machine: machine, project: project, branch: b, start: start)
    }

    /// Brings the agent resolving b's conflict forward.
    func showResolver(_ b: GraphBranch) {
        guard let p = b.resolver, p.running else { return }
        store.open(ConchKit.key(machine, p.id))
    }

    var canAutopilot: Bool { store.connection(machine)?.hello?.capabilities?.contains("autopilot.v1") == true }
    var canActivity: Bool { store.connection(machine)?.hello?.capabilities?.contains("autopilot.activity.v1") == true }

    /// The autopilot's policy and the branches it minds, and what it did
    /// in this project, newest first.
    func loadAutopilot() {
        struct NoParams: Encodable {}
        guard let c = store.connection(machine), canAutopilot else { return }
        let n = activityReads.next()
        Task {
            if let st = try? await c.call("autopilot.get", NoParams(), as: AutopilotState.self), activityReads.isLatest(n) {
                autopilot = st
            }
            guard canActivity else {
                activityError = "conch on \(store.label(machine)) keeps no record of what the autopilot did: reload its server."
                return
            }
            struct P: Encodable { var project_id: String; var limit: Int }
            do {
                let a = try await c.call("autopilot.activity", P(project_id: project, limit: 200), as: AutopilotActivity.self)
                guard activityReads.isLatest(n) else { return }
                activity = a.notes
                activityError = ""
            } catch {
                guard activityReads.isLatest(n) else { return }
                activityError = "\(error)"
            }
        }
    }

    /// Brings a branch into view: its commits stand out, its newest picked.
    func focus(branch: String) {
        focusOn = branch
        if let tip = branchTips(rows.map(\.commit), remotes: remotes ?? ["origin", "upstream"])[branch] {
            pick(tip, shift: false)
            reveal += 1
        }
    }

    var canCI: Bool { store.connection(machine)?.hello?.capabilities?.contains("ci.stats.v1") == true }
    var canRerun: Bool { store.connection(machine)?.hello?.capabilities?.contains("ci.rerun.v1") == true }

    /// Whether a failing branch's CI can be run again: a server that says
    /// which checks can (pr.check.rerun.v1) is taken at its word, so a CI
    /// whose integration has no re-run offers none.
    func canRerun(branch: String) -> Bool {
        guard canRerun else { return false }
        guard store.connection(machine)?.hello?.capabilities?.contains("pr.check.rerun.v1") == true else { return true }
        return ciCanRerun(projectInfo?.branches?.first { $0.name == branch }?.pr, checksSay: true)
    }

    /// The project's CI over ciDays. A busy repository's job times arrive
    /// a few dozen runs at a time, so while the server says some are still
    /// to read it is asked again, a few times.
    func loadCI(again: Int = 0) {
        guard let c = store.connection(machine) else { return }
        guard canCI else {
            ciError = "conch on \(store.label(machine)) is older than this app and can't add up CI: reload its server."
            return
        }
        struct P: Encodable { var project_id: String; var days: Int }
        ciLoading = true
        let n = ciReads.next()
        let days = ciDays
        Task {
            do {
                let s = try await c.call("ci.stats", P(project_id: project, days: days), as: CIStats.self)
                guard ciReads.isLatest(n) else { return }
                ci = s
                ciError = ""
                ciLoading = false
                if s.jobsPending > 0 && again < 4 {
                    try? await Task.sleep(for: .milliseconds(500))
                    if ciReads.isLatest(n) && showCI { loadCI(again: again + 1) }
                }
            } catch {
                guard ciReads.isLatest(n) else { return }
                ciLoading = false
                ciError = "\(error)"
            }
        }
    }

    /// Runs a failed run's failed jobs again — the run named, or a branch's
    /// newest failed one — and reads CI again once GitHub has it.
    func rerun(run: Int64? = nil, attempt: Int = 1, branch: String? = nil) {
        guard let c = store.connection(machine) else { return }
        guard canRerun else {
            store.error = "conch on \(store.label(machine)) is too old to re-run CI: reload its server"
            return
        }
        struct P: Encodable { var project_id: String; var run_id: Int64?; var branch: String? }
        struct R: Decodable { var run_id: Int64 }
        if let run { rerunning[run] = attempt }
        Task {
            do {
                let r = try await c.call("ci.rerun", P(project_id: project, run_id: run, branch: branch), as: R.self)
                if run == nil { rerunning[r.run_id] = attempt }
                store.say("Running the failed jobs again" + (branch.map { " on \($0)" } ?? ""))
                store.refreshProject(machine: machine, project: project)
                try? await Task.sleep(for: .seconds(4))
                if showCI { loadCI() }
            } catch {
                if let run { rerunning[run] = nil }
                store.error = "Could not re-run CI: \(error)"
            }
        }
    }

    var canRebased: Bool { store.connection(machine)?.hello?.capabilities?.contains("branch.rebased.v1") == true }

    /// The agents' branches rebased and not pushed. An older server has no
    /// such list, and the graph goes on without it.
    func loadRebases() {
        guard let c = store.connection(machine), canRebased else { return }
        struct P: Encodable { var project_id: String }
        let n = rebaseReads.next()
        Task {
            guard let r = try? await c.call("branch.rebased", P(project_id: project), as: BranchRebased.self),
                  rebaseReads.isLatest(n) else { return }
            rebases = Dictionary((r.rebases ?? []).map { ($0.branch, $0) }, uniquingKeysWith: { a, _ in a })
            // A sheet open on a rebase that was pushed or undone elsewhere.
            if let open = reviewing, rebases[open.branch]?.after != open.after { reviewing = nil }
        }
    }

    /// Opens the review of a branch's rebase: what resolving changed.
    func review(_ r: BranchRebase) {
        guard let c = store.connection(machine) else { return }
        struct P: Encodable { var project_id: String; var branch: String; var diff: Bool }
        reviewing = r
        reviewError = ""
        Task {
            do {
                let got = try await c.call("branch.rebased", P(project_id: project, branch: r.branch, diff: true), as: BranchRebased.self)
                guard reviewing?.branch == r.branch else { return }
                if let fresh = got.rebases?.first {
                    reviewing = fresh
                } else {
                    reviewError = "\(r.branch) is no longer a rebase waiting to be pushed: it was pushed, undone or committed to since."
                }
            } catch {
                guard reviewing?.branch == r.branch else { return }
                reviewError = "Could not read the rebase: \(error)"
            }
        }
    }

    /// Pushes a rebased branch: over the old one the remote still has, and
    /// only that, or a first push.
    func pushRebase(_ r: BranchRebase) {
        guard let c = store.connection(machine) else { return }
        struct P: Encodable { var project_id: String; var branch: String; var over: String? }
        var over: String?
        switch r.push {
        case .fresh: over = nil
        case .over(let h): over = h
        case .moved(let h):
            store.error = "Somebody pushed \(String(h.prefix(8))) to \(r.branch) since it was rebased; pushing would throw that away. Bring it in first."
            return
        }
        pushing.insert(r.branch)
        Task {
            defer { pushing.remove(r.branch) }
            do {
                try await c.call("branch.push", P(project_id: project, branch: r.branch, over: over))
                store.say("Pushed \(r.branch), rebased onto \(projectInfo?.base ?? "the base")")
                if reviewing?.branch == r.branch { reviewing = nil }
                rebases[r.branch] = nil
                store.refreshProject(machine: machine, project: project)
                loadRebases()
            } catch {
                store.error = "Could not push \(r.branch): \(error)"
                loadRebases()
            }
        }
    }

    /// Puts a rebased branch back where it was, after asking: the rewrite
    /// stays in git's reflog, but the agent's resolution leaves the branch.
    func undoRebase(_ r: BranchRebase) {
        guard let c = store.connection(machine) else { return }
        let a = NSAlert()
        a.messageText = "Undo the rebase of \(r.branch)?"
        let base = projectInfo?.base ?? "the base"
        a.informativeText = "The branch goes back to \(String(r.before.prefix(8))), where it was before"
            + (r.changed > 0 ? ", and its conflict with \(base) is back. " : ", behind \(base) again. ")
            + "The rebased commits stay in git's reflog (\(String(r.after.prefix(8)))). A worktree with uncommitted changes is not touched."
        a.addButton(withTitle: "Undo the Rebase")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        struct P: Encodable { var project_id: String; var branch: String; var before: String; var after: String }
        pushing.insert(r.branch)
        Task {
            defer { pushing.remove(r.branch) }
            do {
                try await c.call("branch.rebase_undo", P(project_id: project, branch: r.branch, before: r.before, after: r.after))
                store.say("Put \(r.branch) back where it was before its rebase", .info)
                if reviewing?.branch == r.branch { reviewing = nil }
                rebases[r.branch] = nil
                store.refreshProject(machine: machine, project: project)
                // The commit picked was the rewrite, gone from the graph now:
                // the branch's own tip, where it is back to, instead.
                focusBranch = r.branch
                load()
            } catch {
                store.error = "Could not undo the rebase: \(error)"
            }
        }
    }

    func handToAgent(_ c: LogCommit) {
        store.handCommit(machine: machine, project: project, hash: c.hash, subject: c.subject)
    }

    /// Lanes the graph column is wide, the same for every row so hashes
    /// and subjects line up; a history wider than 16 lanes is clipped
    /// rather than pushing the text off the row.
    var lanes: Int { min(max(rows.map(\.width).max() ?? 1, 1), 16) }

    func load() {
        guard let c = store.connection(machine) else {
            error = "\(store.label(machine)) is not connected"
            return
        }
        guard c.hello?.capabilities?.contains("project.log.v1") == true else {
            error = "conch on \(store.label(machine)) is older than this app and can't read a commit graph: reload its server."
            return
        }
        struct Params: Encodable { var project_id: String; var limit: Int }
        loading = true
        loadRebases()
        let n = reads.next()
        Task {
            do {
                let r = try await c.call("project.log", Params(project_id: project, limit: limit), as: LogResult.self)
                guard reads.isLatest(n) else { return }
                loading = false
                rows = commitGraph(r.commits ?? [])
                more = r.more ?? false
                remotes = r.remotes.map(Set.init)
                format = r.format
                owners = commitOwners(r.commits ?? [], base: projectInfo?.base ?? "", remotes: remotes ?? ["origin", "upstream"])
                error = ""
                // The branch asked for, picked and brought into view if it is
                // among those read; either way, asked once.
                if let b = focusBranch {
                    focusBranch = nil
                    if let hit = rows.first(where: { CommitRef.parse($0.commit.refs, remotes: remotes ?? ["origin", "upstream"]).contains { $0.name == b } }) {
                        // Picked as a click would, so its panel reads it too.
                        pick(hit.commit.hash, shift: false)
                        reveal += 1
                    }
                }
            } catch {
                guard reads.isLatest(n) else { return }
                loading = false
                self.error = "Could not read the history: \(error)"
            }
        }
    }

    /// Picks a commit, or with shift a second one to compare with it; the
    /// picked one clicked again closes its panel.
    func pick(_ hash: String, shift: Bool) {
        if pickCloses(hash, selected: selected, shift: shift, comparing: compareWith != nil) {
            closeCommit()
            return
        }
        if shift, let s = selected, s != hash {
            compareWith = hash
        } else {
            selected = hash
            compareWith = nil
        }
        loadDetail()
    }

    /// Closes the commit's panel: the graph is whole again.
    func closeCommit() {
        selected = nil
        compareWith = nil
        detail = nil
        detailError = ""
        peekFile = nil
        peek = []
    }

    /// One step back (Escape): the newest thing opened closes first.
    func back() {
        switch graphBack(showCI: showCI, showAutopilot: showAutopilot, comparing: compareWith != nil,
                         selected: selected != nil, focused: focusOn != nil) {
        case .closeCI: showCI = false
        case .closeAutopilot: showAutopilot = false
        case .endCompare: compareWith = nil; loadDetail()
        case .closeCommit: closeCommit()
        case .clearFocus: focusOn = nil
        case .nothing: break
        }
    }

    /// The range being compared, the older commit first, by the graph's order.
    var range: (from: String, to: String)? {
        guard let a = selected, let b = compareWith else { return nil }
        return compareRange(a, b, order: rows.map(\.commit.hash))
    }

    /// Hashes inside the compared range, for dimming the rest.
    var inRange: Set<String>? {
        guard let r = range, let lo = rows.firstIndex(where: { $0.commit.hash == r.to }),
              let hi = rows.firstIndex(where: { $0.commit.hash == r.from }) else { return nil }
        return Set(rows[lo...hi].map(\.commit.hash))
    }

    var canShowDetail: Bool { store.connection(machine)?.hello?.capabilities?.contains("project.commit.v1") == true }

    func loadDetail() {
        peekFile = nil
        peek = []
        guard let c = store.connection(machine), let sel = selected else { detail = nil; return }
        guard canShowDetail else {
            detail = nil
            detailError = "conch on \(store.label(machine)) is too old to open a commit: reload its server"
            return
        }
        let params = range.map { CommitParams(projectID: project, hash: $0.to, from: $0.from) } ?? CommitParams(projectID: project, hash: sel)
        let n = details.next()
        Task {
            do {
                let d = try await c.call("project.commit", params, as: CommitDetail.self)
                guard details.isLatest(n) else { return }
                detail = d
                detailError = ""
            } catch {
                guard details.isLatest(n) else { return }
                detail = nil
                detailError = "\(error)"
            }
        }
    }

    /// One file's diff in the commit, or across the range.
    func openPeek(_ file: String) {
        guard let c = store.connection(machine), let sel = selected else { return }
        if peekFile == file { peekFile = nil; peek = []; return }
        peekFile = file
        let params = range.map { CommitParams(projectID: project, hash: $0.to, from: $0.from, file: file) } ?? CommitParams(projectID: project, hash: sel, file: file)
        Task {
            if let d = try? await c.call("project.commit", params, as: CommitDetail.self), peekFile == file {
                peek = parseDiff(d.diff ?? "").filter { $0.kind != .meta }
            }
        }
    }

    /// Whether older commits can still be read: there are more, and fewer
    /// than the server's 2000 have been asked for.
    var canReadMore: Bool { more && limit < 2000 }

    /// Older commits, 300 at a time, up to the server's 2000.
    func loadMore() {
        guard canReadMore, !loading else { return }
        limit = min(limit + proto_LogLimit, 2000)
        load()
    }
}

/// project.log's own page size (proto.LogLimit).
let proto_LogLimit = 300

struct CommitsView: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store
    /// Inside a branch's git tab: no frame of its own, which the tab has.
    var embedded = false
    /// The agents' branches beside the graph, which a narrow window can do without.
    @AppStorage("commits.branches") private var showBranches = true

    var body: some View {
        HStack(spacing: 0) {
            // The CI panel takes the room the branch list had.
            if showBranches && !model.sidePanel {
                BranchSide(model: model, store: store).frame(minWidth: 180, idealWidth: 228, maxWidth: 228)
                Rectangle().fill(Theme.line).frame(width: 1)
            }
            graph.frame(minWidth: 320)
            if model.showCI {
                Rectangle().fill(Theme.line).frame(width: 1)
                CIPanel(model: model).frame(minWidth: 300, idealWidth: 380, maxWidth: 380)
            } else if model.showAutopilot {
                Rectangle().fill(Theme.line).frame(width: 1)
                AutopilotPanel(model: model, store: store).frame(minWidth: 300, idealWidth: 380, maxWidth: 380)
            } else if model.selected != nil {
                Rectangle().fill(Theme.line).frame(width: 1)
                CommitDetailPanel(model: model, store: store).frame(minWidth: 260, idealWidth: 340, maxWidth: 340)
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: embedded ? 0 : 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(embedded ? .clear : Theme.line))
        .sheet(isPresented: Binding(get: { model.reviewing != nil }, set: { if !$0 { model.reviewing = nil } })) {
            RebaseReviewSheet(model: model)
        }
        // Only while the graph has the keyboard: Escape belongs to the
        // terminals and agents in the other splits.
        .onExitCommand { model.back() }
    }

    var graph: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            ConflictBanner(model: model)
            RebasedBanner(model: model)
            if model.range != nil {
                HStack(spacing: 8) {
                    Circle().fill(Theme.merged).frame(width: 7, height: 7)
                    Text("Comparing \(String(model.range!.from.prefix(7))) … \(String(model.range!.to.prefix(7)))").font(.ui(size: 12))
                    Spacer()
                    Button("Done") { model.compareWith = nil; model.loadDetail() }.controlSize(.small)
                }
                .padding(.horizontal, 12).frame(height: 30).background(Theme.merged.opacity(0.14))
            }
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(16).frame(maxWidth: .infinity, alignment: .leading)
            }
            if model.rows.isEmpty && !model.loading && model.error.isEmpty {
                VStack { Spacer(); Text("No commits yet.").foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
            } else {
                ScrollViewReader { reader in
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            let byName = Dictionary(model.branches.map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
                            ForEach(model.rows, id: \.commit.hash) { r in
                                CommitRow(model: model, store: store, row: r, owner: model.owners[r.commit.hash].flatMap { byName[$0] })
                                    .id(r.commit.hash)
                            }
                            if model.canReadMore {
                                Button("Show Older Commits") { model.loadMore() }
                                    .buttonStyle(.link).padding(12).disabled(model.loading)
                            } else if model.more {
                                Text("The newest \(model.count), as many as are read").font(.ui(size: 12))
                                    .foregroundStyle(Theme.muted).padding(12)
                            }
                        }
                        .padding(.vertical, 6)
                    }
                    .onChange(of: model.reveal) { _, _ in
                        if let h = model.selected { withAnimation { reader.scrollTo(h, anchor: .center) } }
                    }
                }
            }
        }
    }

    var header: some View {
        let name = store.snapshots[model.machine]?.project(model.project)?.name ?? model.project
        return HStack(spacing: 10) {
            if !model.sidePanel {
                Button { showBranches.toggle() } label: {
                    LucideIcon("panel-left", color: showBranches ? Theme.accent : Theme.muted, size: 13).padding(3).contentShape(Rectangle())
                }
                .buttonStyle(.plain).help(showBranches ? "Hide the agents' branches" : "Show the agents' branches")
                .accessibilityLabel(showBranches ? "Hide the agents' branches" : "Show the agents' branches")
            }
            LucideIcon("git-commit-horizontal", color: Theme.accent, size: 14)
            Text("Commits").font(.ui(size: 13, weight: .semibold)).fixedSize()
            Text(name).font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(1).layoutPriority(-1)
            Text(model.more ? "newest \(model.count)" : "\(model.count)").font(.ui(size: 11)).foregroundStyle(Theme.muted).fixedSize()
            // Left out while a panel takes the right side: the graph is
            // narrow then, and the search, panels and refresh need the room.
            if let f = model.format, !f.label.isEmpty, model.selected == nil, !model.sidePanel {
                Text(f.git3 ? "Git 3 · \(f.label)" : f.label)
                    .font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(f.git3 ? Theme.accent : Theme.muted)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(Capsule().fill((f.git3 ? Theme.accent : Theme.muted).opacity(0.14)))
                    .lineLimit(1)
                    .help(f.git3 ? "This repository is stored the way git 3 makes new ones: SHA-256 object names and reftable refs" : "Object names and refs storage")
            }
            if model.loading { ProgressView().controlSize(.small) }
            Spacer(minLength: 8)
            HStack(spacing: 6) {
                LucideIcon("search", color: Theme.muted, size: 11)
                TextField("Search commits, hashes, authors", text: $model.query).textFieldStyle(.plain).font(.ui(size: 12))
                    .frame(minWidth: 60, maxWidth: 220)
            }
            .padding(.horizontal, 8).frame(height: 24)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.window))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
            // Said until a commit is picked: with its panel open the graph
            // is narrow, and the search and refresh need the room.
            if model.selected == nil && !model.sidePanel {
                Text("shift-click to compare").font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).layoutPriority(-2)
            }
            if model.canAutopilot {
                let on = model.autopilot?.policy.anyOn == true
                Button { model.showAutopilot.toggle(); if model.showAutopilot { model.loadAutopilot() } } label: {
                    HStack(spacing: 5) {
                        Circle().fill(on ? Theme.ok : Theme.muted).frame(width: 6, height: 6)
                        Text("Autopilot").font(.ui(size: 11.5, weight: .semibold))
                            .foregroundStyle(model.showAutopilot ? Theme.accent : Theme.text2)
                    }
                    .padding(.horizontal, 7).frame(height: 22)
                    .background(RoundedRectangle(cornerRadius: 6).fill(model.showAutopilot ? Theme.accent.opacity(0.16) : .clear))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(model.showAutopilot ? Theme.accent.opacity(0.5) : Theme.line))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain).fixedSize()
                .help(model.showAutopilot ? "Back to the commit" : "What the autopilot does on its own here, and what it did")
                .accessibilityLabel(model.showAutopilot ? "Hide the autopilot" : "Show the autopilot, \(on ? "on" : "off")")
            }
            if model.canCI {
                Button {
                    model.showCI.toggle()
                    if model.showCI { model.loadCI() }
                } label: {
                    HStack(spacing: 4) {
                        LucideIcon("zap", color: model.showCI ? Theme.accent : Theme.muted, size: 11)
                        Text("CI").font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(model.showCI ? Theme.accent : Theme.text2)
                    }
                    .padding(.horizontal, 7).frame(height: 22)
                    .background(RoundedRectangle(cornerRadius: 6).fill(model.showCI ? Theme.accent.opacity(0.16) : .clear))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(model.showCI ? Theme.accent.opacity(0.5) : Theme.line))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain).fixedSize()
                .help(model.showCI ? "Back to the commit" : "The project's CI: pass rate, run times, slow and flaky jobs, failures to run again")
                .accessibilityLabel(model.showCI ? "Hide CI" : "Show CI")
            }
            Button { model.refresh() } label: { LucideIcon("refresh-cw", size: 12).padding(4).contentShape(Rectangle()) }.buttonStyle(.plain).fixedSize().help("Read the history again, and its pull requests from GitHub")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }
}

/// The colours lanes take in turn: the theme's accent for the first (the
/// newest branch, usually the one being worked on), then its other
/// signal colours, so neighbouring branches differ.
@MainActor
func laneColor(_ i: Int) -> Color {
    let palette = lanePalette(Theme.current)
    return Theme.color(palette[((i % palette.count) + palette.count) % palette.count])
}

struct CommitRow: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store
    let row: GraphRow
    /// The agent branch the commit is on, if any.
    let owner: GraphBranch?
    @State private var hovering = false

    static let height: CGFloat = 26
    static let laneWidth: CGFloat = 14

    var body: some View {
        let c = row.commit
        let refs = CommitRef.parse(c.refs, remotes: model.remotes ?? ["origin", "upstream"])
        let on = model.selected == c.hash
        let cmp = model.compareWith == c.hash
        let tip = owner != nil && refs.contains { $0.name == owner?.name }
        let dim = !commitMatches(c, model.query) || (model.inRange.map { !$0.contains(c.hash) } ?? false)
            || (model.focusOn != nil && model.owners[c.hash] != model.focusOn)
        HStack(spacing: 8) {
            GraphCell(row: row).frame(width: CGFloat(model.lanes) * Self.laneWidth + 6, height: Self.height).clipped()
            Text(c.short).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted).fixedSize()
            // Chips and the subject share what is left, clipped rather than
            // pushing the hash and the agent out of the row.
            HStack(spacing: 8) {
            ForEach(Array(refs.enumerated()), id: \.offset) { _, r in RefChip(ref: r) }
            // The commit a pull request's checks run on: its CI, here too.
            if let pr = model.pullRequests[c.hash] {
                CIChip(pr: pr, number: true)
                CIMarks(marks: ciMarks(pr))
            }
            if tip, let o = owner { StateTag(state: o.state) }
            Text(c.subject).font(.ui(size: 12.5)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.tail)
                .layoutPriority(-1)
            }
            .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading).clipped()
            AgentCell(agent: owner?.agent, author: c.author ?? "").frame(width: 96, alignment: .leading)
            Text(agoText(c.time)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).frame(minWidth: 52, alignment: .trailing)
        }
        .padding(.horizontal, 10).frame(height: Self.height)
        .opacity(dim ? 0.3 : 1)
        .background(cmp ? Theme.merged.opacity(0.22) : on ? Theme.accentDeep.opacity(0.3) : hovering ? Theme.raise : .clear)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { model.pick(c.hash, shift: NSEvent.modifierFlags.contains(.shift)) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { model.pick(c.hash, shift: false) }
        .help(c.hash)
        .contextMenu {
            Button("Copy Hash") { copy(c.hash) }
            Button("Copy Subject") { copy(c.subject) }
            Button("Hand to an Agent…") { model.handToAgent(c) }
            if let o = owner, o.state == .ciFailing, tip, model.canRerun(branch: o.name) {
                Button("Re-run Failed Jobs") { model.rerun(branch: o.name) }
            }
            if let o = owner, o.state.conflicts {
                Button("Ask \(o.agent?.displayName ?? "an Agent") to Resolve") { model.askToResolve(o) }
            }
            let branches = refs.filter { $0.kind == .head || $0.kind == .branch }.map(\.name).filter { $0 != "HEAD" }
            if !branches.isEmpty { Divider() }
            ForEach(branches, id: \.self) { b in
                Button("View Changes on \(b)") { store.openChanges(project: key(model.machine, model.project), branch: b) }
            }
        }
    }

    func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}

/// A ref as a chip: HEAD's branch in the accent, a branch in the working
/// colour, a remote quieter, a tag outlined.
struct RefChip: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let ref: CommitRef

    var body: some View {
        let _ = look
        let color: Color = switch ref.kind {
        case .head: Theme.accent
        case .branch: Theme.work
        case .remote: Theme.muted
        case .tag: Theme.warn
        }
        HStack(spacing: 3) {
            if ref.kind == .tag { LucideIcon("tag", color: color, size: 9) }
            Text(ref.kind == .head && ref.name != "HEAD" ? "HEAD → \(ref.name)" : ref.name)
        }
        .font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(color).lineLimit(1)
        .padding(.horizontal, 6).padding(.vertical, 1.5)
        .background(RoundedRectangle(cornerRadius: 4).fill(ref.kind == .tag ? .clear : color.opacity(0.16)))
        .overlay(RoundedRectangle(cornerRadius: 4).stroke(ref.kind == .tag ? color.opacity(0.6) : .clear))
        .fixedSize()
    }
}

/// One row's share of the graph: the lines through it and the commit's dot.
struct GraphCell: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let row: GraphRow

    var body: some View {
        let _ = look
        // Colours are read here, on the main actor; the canvas only draws.
        let lanes = Set(row.colors.keys).union([row.lane])
        let color = Dictionary(uniqueKeysWithValues: lanes.map { ($0, laneColor(row.colors[$0] ?? $0)) })
        let background = Theme.window
        let head = CommitRef.parse(row.commit.refs).contains { $0.kind == .head }
        return Canvas { ctx, size in
            let h = size.height, mid = h / 2, w = CommitRow.laneWidth
            func x(_ i: Int) -> CGFloat { 3 + w / 2 + CGFloat(i) * w }
            func stroke(_ p: Path, _ lane: Int) {
                ctx.stroke(p, with: .color(color[lane] ?? .gray), style: StrokeStyle(lineWidth: 1.6, lineCap: .round))
            }
            for i in row.through {
                stroke(Path { $0.move(to: CGPoint(x: x(i), y: 0)); $0.addLine(to: CGPoint(x: x(i), y: h)) }, i)
            }
            // Children arrive from the top; a lane bending in curves to
            // the dot, as git's / and \ do.
            for i in row.into {
                stroke(Path { p in
                    p.move(to: CGPoint(x: x(i), y: 0))
                    if i == row.lane {
                        p.addLine(to: CGPoint(x: x(i), y: mid))
                    } else {
                        p.addCurve(to: CGPoint(x: x(row.lane), y: mid), control1: CGPoint(x: x(i), y: mid * 0.7),
                                   control2: CGPoint(x: x(row.lane), y: mid * 0.3))
                    }
                }, i)
            }
            for k in row.outOf {
                stroke(Path { p in
                    p.move(to: CGPoint(x: x(row.lane), y: mid))
                    if k == row.lane {
                        p.addLine(to: CGPoint(x: x(k), y: h))
                    } else {
                        p.addCurve(to: CGPoint(x: x(k), y: h), control1: CGPoint(x: x(row.lane), y: mid + mid * 0.7),
                                   control2: CGPoint(x: x(k), y: mid + mid * 0.3))
                    }
                }, k)
            }
            let dotColor = color[row.lane] ?? .gray
            let r: CGFloat = head ? 4.5 : row.merge ? 3 : 3.8
            let dot = Path(ellipseIn: CGRect(x: x(row.lane) - r, y: mid - r, width: 2 * r, height: 2 * r))
            if head || row.merge {
                ctx.fill(dot, with: .color(dotColor))
            } else {
                ctx.fill(dot, with: .color(background))
                ctx.stroke(dot, with: .color(dotColor), lineWidth: 1.6)
            }
        }
    }
}

/// The side of the graph: the commit picked — who, when, its message whole,
/// the files it changed with a click to see one's diff — or, with a second
/// commit shift-clicked, every file changed across the range.
struct CommitDetailPanel: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                // Still reading, or it failed: the × has no header to sit in.
                if model.detail == nil {
                    HStack { Spacer(); CloseCommitButton(model: model) }
                }
                if !model.detailError.isEmpty {
                    Text(model.detailError).font(.ui(size: 12)).foregroundStyle(Theme.err)
                }
                if let d = model.detail {
                    if let r = model.range {
                        HStack {
                            Text("Compare").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.merged)
                            Spacer()
                            CloseCommitButton(model: model)
                        }
                        Text("\(d.commits ?? 0) commit\((d.commits ?? 0) == 1 ? "" : "s"), \(d.files?.count ?? 0) file\((d.files?.count ?? 0) == 1 ? "" : "s")")
                            .font(.ui(size: 14, weight: .semibold))
                        Text("\(String(r.from.prefix(7))) → \(String(r.to.prefix(7)))").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
                    } else {
                        HStack {
                            Text(String(d.hash.prefix(10))).font(.ui(size: 12, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.work)
                            if (d.parents?.count ?? 0) > 1 { Text("merge").font(.ui(size: 10.5)).foregroundStyle(Theme.muted) }
                            Spacer()
                            Button("Copy") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(d.hash, forType: .string)
                            }.controlSize(.small)
                            CloseCommitButton(model: model)
                        }
                        Text(d.subject ?? "").font(.ui(size: 14, weight: .semibold)).fixedSize(horizontal: false, vertical: true)
                        HStack(spacing: 6) {
                            Text(d.author ?? "").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                            if let t = d.time { Text("· \(agoText(t))").font(.ui(size: 12)).foregroundStyle(Theme.muted) }
                        }
                        if let b = d.body, !b.isEmpty {
                            Text(b).font(.ui(size: 12)).foregroundStyle(Theme.text2).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                        }
                        if let ps = d.parents, !ps.isEmpty {
                            HStack(spacing: 6) {
                                Text("Parents").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                                ForEach(ps, id: \.self) { p in
                                    Button(String(p.prefix(7))) { model.pick(p, shift: false); model.reveal += 1 }
                                        .buttonStyle(.link).font(.ui(size: 11.5, design: .monospaced))
                                }
                            }
                        }
                    }
                    if model.range == nil {
                        if let b = model.branch(of: d.hash), b.state.conflicts { ConflictCard(model: model, branch: b) }
                        if let b = model.branch(of: d.hash), let r = b.rebase { RebasedCard(model: model, branch: b, rebase: r) }
                        HStack(spacing: 8) {
                            if let c = model.rows.first(where: { $0.commit.hash == d.hash })?.commit {
                                Button("Hand to an Agent…") { model.handToAgent(c) }
                                    .controlSize(.small)
                                    .help("A new branch starting at this commit, with an agent of your choice on it")
                            }
                        }
                    }
                    Rectangle().fill(Theme.line).frame(height: 1)
                    Text("FILES · \(d.files?.count ?? 0)").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    ForEach(d.files ?? []) { f in
                        VStack(alignment: .leading, spacing: 0) {
                            HStack(spacing: 8) {
                                Text(f.code).font(.ui(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(fileCodeColor(f.code)).frame(width: 12)
                                Text(f.path).font(.ui(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.head)
                                Spacer(minLength: 4)
                                Text("+\(f.added)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.ok)
                                Text("−\(f.deleted)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.err)
                            }
                            .padding(.vertical, 3).contentShape(Rectangle())
                            .onTapGesture { model.openPeek(f.path) }
                            .accessibilityElement(children: .combine)
                            .accessibilityAddTraits(.isButton)
                            .accessibilityAction { model.openPeek(f.path) }
                            if model.peekFile == f.path {
                                VStack(alignment: .leading, spacing: 0) {
                                    ForEach(model.peek.prefix(120)) { l in
                                        Text((l.kind == .added ? "+ " : l.kind == .removed ? "− " : l.kind == .hunk ? "" : "  ") + l.text)
                                            .font(.ui(size: 11, design: .monospaced)).lineLimit(1)
                                            .foregroundStyle(l.kind == .added ? Theme.ok : l.kind == .removed ? Theme.err : l.kind == .hunk ? Theme.work : Theme.text2)
                                            .frame(maxWidth: .infinity, alignment: .leading)
                                            .background(l.kind == .added ? Theme.ok.opacity(0.1) : l.kind == .removed ? Theme.err.opacity(0.1) : .clear)
                                    }
                                }
                                .padding(6).background(RoundedRectangle(cornerRadius: 6).fill(Theme.side))
                            }
                        }
                    }
                } else if model.detailError.isEmpty {
                    ProgressView().controlSize(.small)
                }
            }
            .padding(14)
        }
        .background(Theme.side)
    }
}

/// The colour of a file's status letter.
@MainActor
func fileCodeColor(_ code: String) -> Color {
    switch code {
    case "A", "?": return Theme.ok
    case "D": return Theme.err
    case "U": return Theme.warn
    case "R", "C": return Theme.merged
    default: return Theme.warn
    }
}

/// Who made a commit: the agent of the branch it is on, with its mark's
/// colour, or the author git has for it.
struct AgentCell: View {
    @Environment(\.conchTheme) private var look
    let agent: PaneInfo?
    let author: String

    var body: some View {
        let _ = look
        HStack(spacing: 5) {
            if let a = agent {
                Circle().fill(agentColor(a.agent?.name).map(Theme.color) ?? Theme.muted).frame(width: 7, height: 7)
                Text(a.agent?.name.capitalized ?? a.displayName).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1)
            } else {
                Text(author).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
        }
        .help(agent.map { "\($0.displayName) — \(author)" } ?? author)
    }
}

/// A pull request's checks as small squares on its commit: green passed,
/// red failed, an outline still running.
struct CIMarks: View {
    @Environment(\.conchTheme) private var look
    let marks: [CIMark]

    var body: some View {
        let _ = look
        HStack(spacing: 2) {
            ForEach(Array(marks.enumerated()), id: \.offset) { _, m in
                let tone = m.state == "pass" ? Theme.ok : m.state == "fail" ? Theme.err : Theme.warn
                RoundedRectangle(cornerRadius: 2)
                    .fill(m.state == "pending" ? .clear : tone)
                    .overlay(RoundedRectangle(cornerRadius: 2).stroke(tone, lineWidth: 1.2))
                    .frame(width: 8, height: 8)
                    .help("\(m.name): \(m.state == "pass" ? "passed" : m.state == "fail" ? "failed" : "running")")
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(marks.map { "\($0.name) \($0.state)" }.joined(separator: ", "))
    }
}

/// What an agent branch needs, on its newest commit.
struct StateTag: View {
    @Environment(\.conchTheme) private var look
    let state: GraphBranchState

    var body: some View {
        let _ = look
        if let (text, tone) = words {
            Text(text).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(tone).lineLimit(1)
                .padding(.horizontal, 6).padding(.vertical, 1.5)
                .background(Capsule().fill(tone.opacity(0.16)))
                .fixedSize()
        }
    }

    var words: (String, Color)? {
        switch state {
        case .conflicted(let n): ("✕ conflict · \(n) file\(n == 1 ? "" : "s")", Theme.err)
        case .willConflict: ("⚠ will conflict", Theme.warn)
        case .ciFailing: ("✕ CI failed", Theme.err)
        case .waiting: ("waiting for you", Theme.warn)
        case .rebased: ("✓ rebased", Theme.ok)
        case .resolving: ("resolving", Theme.work)
        case .resolveWaiting: ("resolving · needs you", Theme.warn)
        case .resolveStopped: ("✕ resolve stopped", Theme.err)
        default: nil
        }
    }
}

/// The agents' branches beside the graph: each with its agent, what it
/// needs, and how far it is from the base. Picking one dims the rest of
/// the graph; picking it again brings it all back.
struct BranchSide: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store

    var body: some View {
        let bs = model.branches
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text("AGENTS ON BRANCHES").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
                Spacer()
                Text("\(bs.count)").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
            }
            .padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 6)
            if bs.isEmpty {
                Text("No agent is working on a branch of this project. Start a task and its branch shows here.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).padding(.horizontal, 10)
                    .fixedSize(horizontal: false, vertical: true)
            }
            ScrollView {
                VStack(spacing: 2) {
                    ForEach(bs, id: \.name) { b in BranchSideRow(model: model, branch: b) }
                }
                .padding(.horizontal, 6)
            }
            Spacer(minLength: 0)
            if let base = model.projectInfo?.base {
                HStack(spacing: 6) {
                    Capsule().fill(laneColor(0)).frame(width: 3, height: 12)
                    Text(base).font(.ui(size: 12))
                    Spacer()
                    Text("base").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                }
                .padding(.horizontal, 12).padding(.vertical, 10)
            }
        }
        .background(Theme.side)
    }
}

struct BranchSideRow: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let branch: GraphBranch
    @State private var hovering = false

    var body: some View {
        let _ = look
        let on = model.focusOn == branch.name
        let (state, tone) = describe(branch)
        HStack(spacing: 8) {
            Circle().fill(agentColor(branch.agent?.agent?.name).map(Theme.color) ?? Theme.muted).frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                Text(branch.name).font(.ui(size: 12, weight: .medium)).lineLimit(1).truncationMode(.middle)
                HStack(spacing: 4) {
                    Text(branch.agent.map { $0.agent?.name.capitalized ?? $0.displayName } ?? "no agent").foregroundStyle(Theme.muted)
                    Text("·").foregroundStyle(Theme.muted)
                    Text(state).foregroundStyle(tone)
                }
                .font(.ui(size: 10.5)).lineLimit(1)
            }
            Spacer(minLength: 4)
            Text("↑\(branch.ahead) ↓\(branch.behind)").font(.ui(size: 10.5, design: .monospaced))
                .foregroundStyle(branch.behind > 0 && branch.state.conflicts ? tone : Theme.muted)
        }
        .padding(.horizontal, 8).padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accentDeep.opacity(0.3) : hovering ? Theme.raise : .clear))
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { toggle(on) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { toggle(on) }
        .contextMenu {
            if branch.state == .resolving || branch.state == .resolveWaiting, let r = branch.resolver {
                Button("Show \(resolverName(branch) ?? r.displayName)") { model.showResolver(branch) }
            } else if branch.state.conflicts {
                let again = { if case .resolveStopped = branch.state { return " Again" } else { return "" } }()
                Button("Ask \(branch.agent?.displayName ?? "an Agent") to Resolve" + again) { model.askToResolve(branch) }
            }
            if let r = branch.rebase {
                Button("Review the Resolution…") { model.review(r) }
                Button(r.push == .fresh ? "Push" : "Push (with Lease)") { model.pushRebase(r) }
                    .disabled(model.pushing.contains(r.branch) || { if case .moved = r.push { return true } else { return false } }())
                Button("Undo the Rebase…") { model.undoRebase(r) }
            }
            if branch.state == .ciFailing, model.canRerun(branch: branch.name) {
                Button("Re-run Failed Jobs") { model.rerun(branch: branch.name) }
            }
            Button("View Changes") { model.store.openChanges(project: key(model.machine, model.project), branch: branch.name) }
        }
    }

    /// Focuses the branch, or lets go of it.
    func toggle(_ on: Bool) {
        model.focusOn = on ? nil : branch.name
        // The branch's newest commit, picked and brought into view.
        if !on, let tip = branchTips(model.rows.map(\.commit), remotes: model.remotes ?? ["origin", "upstream"])[branch.name] {
            model.pick(tip, shift: false)
            model.reveal += 1
        }
    }

    func describe(_ b: GraphBranch) -> (String, Color) {
        switch b.state {
        case .conflicted(let n): ("conflict · \(n) file\(n == 1 ? "" : "s")", Theme.err)
        case .rebased: ("rebased", Theme.ok)
        case .waiting: ("waiting for you", Theme.warn)
        case .ciFailing: ("CI failed", Theme.err)
        case .willConflict: ("will conflict", Theme.warn)
        case .resolving: ("resolving", Theme.work)
        case .resolveWaiting: ("needs you", Theme.warn)
        case .resolveStopped: ("stopped", Theme.err)
        case .working: ("working", Theme.work)
        case .done: ("done", Theme.ok)
        case .idle: ("idle", Theme.muted)
        }
    }
}

/// Above the graph when a branch's rebase or merge stopped on conflicts:
/// what stopped, and the agent to ask — the branch's own, or another one
/// started in its worktree. Once one is asked, how that is going: at it,
/// waiting on the person, or stopped with the conflict still there.
struct ConflictBanner: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel

    var body: some View {
        let _ = look
        if let b = conflictBannerBranch(model.branches) {
            let base = model.projectInfo?.base ?? "the base"
            let tone = resolveTone(b.state)
            HStack(spacing: 10) {
                if b.state == .resolving {
                    ProgressView().controlSize(.small).frame(width: 16)
                } else {
                    Image(systemName: b.state == .resolveWaiting ? "hand.raised.fill" : "exclamationmark.triangle.fill").foregroundStyle(tone)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text(b.state.resolution ? resolveHeadline(b, base: base) : "\(b.name) stopped on a conflict with \(base)")
                        .font(.ui(size: 12.5, weight: .semibold)).lineLimit(2)
                    Text(b.state.resolution ? resolveDetail(b) + "." : stoppedDetail(b))
                        .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(2)
                }
                Spacer(minLength: 8)
                switch b.state {
                case .resolving, .resolveWaiting:
                    if let r = b.resolver, r.running {
                        Button(b.state == .resolveWaiting ? "Answer \(resolverName(b) ?? r.displayName)…" : "Show \(resolverName(b) ?? r.displayName)") {
                            model.showResolver(b)
                        }
                        .controlSize(.small).tint(Theme.accentDeep)
                        .help("Opens the agent's pane")
                    }
                    AnotherAgentButton(model: model, branch: b)
                default:
                    ResolveButton(model: model, branch: b)
                }
                Button("View Changes") { model.store.openChanges(project: key(model.machine, model.project), branch: b.name) }
                    .controlSize(.small)
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(tone.opacity(0.12))
            .overlay(alignment: .bottom) { Rectangle().fill(tone.opacity(0.35)).frame(height: 1) }
            .accessibilityElement(children: .contain)
            // Nothing else changes when an agent just asked has not started
            // in time (resolveStartGrace), or as "asked 4m ago" ages: look
            // again every few seconds while an agent has it.
            .task(id: b.state.resolution ? b.name : "") {
                guard b.state.resolution else { return }
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    model.objectWillChange.send()
                }
            }
        }
    }

    func stoppedDetail(_ b: GraphBranch) -> String {
        let n = b.stoppedFiles
        return "\(n) file\(n == 1 ? "" : "s") to resolve in its worktree. Nothing has been pushed."
    }
}

/// The colour of a conflict in each state: an agent at it is work in
/// hand, one asking is the person's, stuck is an error.
@MainActor func resolveTone(_ s: GraphBranchState) -> Color {
    switch s {
    case .resolving: Theme.work
    case .resolveWaiting, .willConflict: Theme.warn
    default: Theme.err
    }
}

/// Ask the branch's agent to resolve its conflict, or pick another agent
/// to start in its worktree with the same request.
struct ResolveButton: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let branch: GraphBranch

    var body: some View {
        let _ = look
        let startable = model.store.startable(on: model.machine)
        let who = branch.agent?.agent?.name.capitalized ?? branch.agent?.displayName
        Menu {
            if let who { Button("Ask \(who) (on this branch)") { model.askToResolve(branch) } }
            Section("Start a new agent in its worktree") {
                ForEach(startable, id: \.name) { a in Button(a.title) { model.askToResolve(branch, start: a.name) } }
            }
        } label: {
            let again = { if case .resolveStopped = branch.state { return " again" } else { return "" } }()
            Text((who.map { "Ask \($0) to resolve" } ?? "Ask an agent to resolve") + again)
        } primaryAction: {
            if branch.agent != nil {
                model.askToResolve(branch)
            } else if let a = startable.first {
                model.askToResolve(branch, start: a.name)
            }
        }
        .menuStyle(.borderedButton).fixedSize().controlSize(.small).tint(Theme.accentDeep)
        .help("Sends the agent the conflicting files and asks it to resolve them and run the tests. Nothing is pushed.")
    }
}

/// While one agent is resolving a conflict: hand it to another, started
/// in the branch's worktree with the same request. The first keeps
/// running — two agents in one checkout should not both be editing, so
/// the menu says so.
struct AnotherAgentButton: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let branch: GraphBranch

    var body: some View {
        let _ = look
        let startable = model.store.startable(on: model.machine)
        let who = resolverName(branch) ?? "the agent at it"
        Menu("Another Agent") {
            Section("Start one in its worktree; \(who) keeps running") {
                ForEach(startable, id: \.name) { a in Button(a.title) { model.askToResolve(branch, start: a.name) } }
            }
        }
        .menuStyle(.borderedButton).fixedSize().controlSize(.small)
        .disabled(startable.isEmpty)
        .help("Asks a new agent, in the branch's worktree, to resolve the conflict instead. Stop \(who) first if it is still changing files.")
    }
}

/// The side panel's word on a branch that conflicts with the base: the
/// files, and an agent to ask.
struct ConflictCard: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let branch: GraphBranch

    var body: some View {
        let _ = look
        let stopped = branch.stopped
        let resolution = branch.state.resolution
        let tone = resolution ? resolveTone(branch.state) : stopped ? Theme.err : Theme.warn
        let base = model.projectInfo?.base ?? "the base"
        VStack(alignment: .leading, spacing: 6) {
            Text(resolution ? resolveHeadline(branch, base: base) : stopped ? "Stopped on a conflict with \(base)" : "Will conflict with \(base)")
                .font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(tone).fixedSize(horizontal: false, vertical: true)
            Text(resolution ? resolveDetail(branch) + "."
                 : stopped ? "The rebase or merge in \(branch.name)'s worktree is waiting for these files to be resolved."
                 : "\(base) changed the same lines as \(branch.name). Nothing has moved yet: git merge-tree tried it.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            ForEach(branch.conflicts.prefix(6), id: \.self) { f in
                Text(f).font(.ui(size: 11.5, design: .monospaced)).lineLimit(1).truncationMode(.head)
            }
            if branch.conflicts.count > 6 {
                Text("and \(branch.conflicts.count - 6) more").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
            if (branch.state == .resolving || branch.state == .resolveWaiting), let r = branch.resolver, r.running {
                HStack(spacing: 6) {
                    Button("Show \(resolverName(branch) ?? r.displayName)") { model.showResolver(branch) }.controlSize(.small)
                    AnotherAgentButton(model: model, branch: branch)
                }
            } else {
                ResolveButton(model: model, branch: branch)
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(tone.opacity(0.10)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(tone.opacity(0.35)))
    }
}


/// The commit panel's ×: back to the whole graph (Escape does it too).
struct CloseCommitButton: View {
    @ObservedObject var model: CommitsModel
    /// Read so the × is drawn again when the theme changes.
    @Environment(\.conchTheme) private var look

    var body: some View {
        Button { model.closeCommit() } label: {
            LucideIcon("x", color: Theme.muted, size: 13).padding(3).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Close — back to the whole graph (Esc)")
        .accessibilityLabel("Close the commit")
    }
}
