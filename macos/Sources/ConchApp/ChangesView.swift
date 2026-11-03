import AppKit
import ConchKit
import SwiftUI

// A branch's changes, in a tab or split of their own, as the TUI opens a
// branch: what it changes (the worktree's uncommitted work, and what its
// commits change against the base), one file's diff, and commit, push and
// open a pull request. Read from the branch's machine's server; the diff
// follows an agent's edits as the server announces them.

@MainActor
final class ChangesModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    let branch: String
    weak var store: Store?

    @Published var changes: Changes?
    @Published var selected: String?
    /// Whether the selected file is one the branch committed (its diff is
    /// against the base) rather than uncommitted work.
    @Published var selectedCommitted = false
    @Published var diff: [DiffLine] = []
    @Published var error: String?
    @Published var busy: String?
    /// Files written in the last few seconds, marked as the TUI marks them.
    @Published var live: Set<String> = []

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

    /// Reads and diffs in flight: each one started — a click, a file
    /// written — only shows if no newer one was started meanwhile, or a
    /// slow reply put an older list, or another file's diff, on screen.
    private var reads = Latest(), diffs = Latest()

    func load() async {
        guard let c = store?.connection(machine) else {
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
            if selected == nil || !all.contains(selected!) {
                if let f = ch.files.first {
                    select(f.path, committed: false)
                } else if let f = ch.committedFiles?.first {
                    select(f.path, committed: true)
                } else {
                    selected = nil
                    diff = []
                }
            } else {
                await loadDiff()
            }
        } catch {
            if reads.isLatest(n) { self.error = "\(error)" }
        }
    }

    func select(_ path: String, committed: Bool) {
        selected = path
        selectedCommitted = committed
        Task { await loadDiff() }
    }

    func loadDiff() async {
        guard let c = store?.connection(machine), let path = selected else { return }
        let n = diffs.next()
        do {
            if selectedCommitted, let base = changes?.base {
                // What the branch's commits did to it: git in the branch's
                // worktree, since project.diff there is the uncommitted work.
                struct Git: Encodable {
                    var project_id: String
                    var branch: String
                    var commands: [[String]]
                }
                struct Out: Decodable { var output: String? }
                let out = try await c.call("branch.git", Git(project_id: project, branch: branch,
                                                              commands: [["diff", "\(base)...HEAD", "--", path]]), as: Out.self)
                guard diffs.isLatest(n) else { return }
                diff = parseDiff(out.output ?? "")
            } else {
                let d = try await c.call("project.diff", Ref(project_id: project, branch: branch, file: path), as: DiffResult.self)
                guard diffs.isLatest(n) else { return }
                diff = parseDiff(d.diff)
            }
        } catch {
            if diffs.isLatest(n) { diff = [DiffLine(id: 0, kind: .meta, text: "\(error)")] }
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

    func commit(_ message: String) {
        run("Committing") { c in
            struct Commit: Encodable {
                var project_id: String
                var branch: String
                var message: String
            }
            try await c.call("branch.commit", Commit(project_id: self.project, branch: self.branch, message: message))
        }
    }

    func push() {
        run("Pushing") { c in
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

    private func run(_ what: String, _ body: @escaping (Connection) async throws -> Void) {
        guard let c = store?.connection(machine), busy == nil else { return }
        busy = what
        Task {
            defer { busy = nil }
            do {
                try await body(c)
                await load()
            } catch {
                self.error = "\(error)"
            }
        }
    }
}

struct ChangesView: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel
    @State private var committing = false
    @State private var message = ""

    var info: BranchInfo? {
        store.snapshots[model.machine]?.project(model.project)?.branches?.first { $0.name == model.branch }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 0) {
                files.frame(width: 300)
                Rectangle().fill(Theme.line).frame(width: 1)
                diffView
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(store.layout.focused == model.key ? Theme.accent : Theme.line))
        .sheet(isPresented: $committing) { commitSheet }
    }

    var header: some View {
        HStack(spacing: 10) {
            LucideIcon(name: .gitBranch, color: Theme.accent, size: 12)
            Text(model.branch).font(.system(size: 13, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text)
            if let ch = model.changes {
                Text("into \(ch.base)").font(.system(size: 12)).foregroundStyle(Theme.muted)
                let (a, d) = ch.totals
                if a + d > 0 {
                    Text("+\(a)").font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.ok)
                    Text("−\(d)").font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.err)
                }
            }
            if let i = info {
                if let ahead = i.baseAhead, ahead > 0 { Text("↑\(ahead)").font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.muted) }
                if let pr = i.pr {
                    Pill(text: "#\(pr.number)" + (pr.checks == "pass" ? " ✓" : pr.checks == "fail" ? " ✗" : ""),
                         fg: pr.checks == "fail" ? Theme.err : Theme.ok, bg: (pr.checks == "fail" ? Theme.err : Theme.ok).opacity(0.16))
                }
            }
            Spacer()
            if let b = model.busy { Text(b + "…").font(.system(size: 12)).foregroundStyle(Theme.muted) }
            Button("Agent or terminal") { store.openBranch(project: key(model.machine, model.project), name: model.branch) }
                .help("This branch's agent, or a terminal in its worktree")
            Button("Commit…") { committing = true }
                .disabled(model.changes?.worktree == nil || (model.changes?.files.isEmpty ?? true) || model.busy != nil)
            Button("Push") { model.push() }.disabled(model.busy != nil)
            Button(info?.pr == nil ? "Open pull request" : "Update pull request") { model.openPR() }.disabled(model.busy != nil)
        }
        .controlSize(.small)
        .padding(.horizontal, 12).frame(height: 38)
        .background(Theme.paneHeader)
    }

    var files: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 1) {
                if let e = model.error {
                    Text(e).font(.system(size: 12)).foregroundStyle(Theme.err).padding(10)
                }
                if let ch = model.changes {
                    if ch.worktree != nil {
                        section("Uncommitted", ch.files, committed: false, empty: "Nothing uncommitted")
                        if let c = ch.committedFiles, !c.isEmpty { section("Committed on \(ch.branch)", c, committed: true, empty: "") }
                    } else {
                        section("Changes", ch.files, committed: false, empty: "Nothing changed against \(ch.base)")
                    }
                    if let commits = ch.commits, !commits.isEmpty {
                        Text("COMMITS").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                            .padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 4)
                        ForEach(commits.prefix(20)) { c in
                            HStack(spacing: 8) {
                                Text(String(c.hash.prefix(7))).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.work)
                                Text(c.subject).font(.system(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                            }
                            .padding(.horizontal, 10).frame(height: 22)
                        }
                    }
                } else if model.error == nil {
                    Text("Reading…").font(.system(size: 12)).foregroundStyle(Theme.muted).padding(10)
                }
            }
            .padding(.vertical, 6)
        }
        .background(Theme.side)
    }

    @ViewBuilder func section(_ title: String, _ list: [FileChange], committed: Bool, empty: String) -> some View {
        Text(title.uppercased()).font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
            .padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 4)
        if list.isEmpty && !empty.isEmpty {
            Text(empty).font(.system(size: 12)).foregroundStyle(Theme.muted).padding(.horizontal, 10)
        }
        ForEach(list) { f in
            let on = model.selected == f.path && model.selectedCommitted == committed
            HStack(spacing: 8) {
                Text(f.code).font(.system(size: 11, weight: .bold, design: .monospaced))
                    .foregroundStyle(f.code == "A" || f.code == "?" ? Theme.ok : f.code == "D" ? Theme.err : f.code == "U" ? Theme.warn : Theme.work)
                    .frame(width: 12)
                if model.live.contains(f.path) { Rectangle().fill(Theme.warn).frame(width: 2, height: 14) }
                Text(f.path).font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.text)
                    .lineLimit(1).truncationMode(.head)
                Spacer(minLength: 4)
                if f.binary == true {
                    Text("binary").font(.system(size: 11)).foregroundStyle(Theme.muted)
                } else {
                    Text("+\(f.added)").font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.ok)
                    Text("−\(f.deleted)").font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.err)
                }
            }
            .padding(.horizontal, 10).frame(height: 24)
            .background(on ? Theme.accentDeep.opacity(0.45) : model.live.contains(f.path) ? Theme.warn.opacity(0.12) : .clear)
            .contentShape(Rectangle())
            .onTapGesture { model.select(f.path, committed: committed) }
        }
    }

    var diffView: some View {
        GeometryReader { geo in
        ScrollView([.vertical, .horizontal]) {
            LazyVStack(alignment: .leading, spacing: 0) {
                if model.selected == nil {
                    Text(model.changes == nil ? "" : "Pick a file to see its diff.").foregroundStyle(Theme.muted).padding(14)
                }
                ForEach(model.diff) { l in
                    HStack(spacing: 0) {
                        Text(l.old.map(String.init) ?? "").frame(width: 44, alignment: .trailing).foregroundStyle(Theme.muted)
                        Text(l.new.map(String.init) ?? "").frame(width: 44, alignment: .trailing).foregroundStyle(Theme.muted)
                        Text(marker(l.kind)).frame(width: 20).foregroundStyle(colour(l.kind))
                        Text(l.text.isEmpty ? " " : l.text).foregroundStyle(colour(l.kind)).fixedSize()
                        Spacer(minLength: 0)
                    }
                    .font(.system(size: 12, design: .monospaced))
                    .padding(.vertical, 1)
                    .background(background(l.kind))
                }
            }
            .padding(.vertical, 6)
            // From the top left, however short: a scroll view in both
            // directions otherwise centres what is smaller than itself.
            .frame(minWidth: geo.size.width, minHeight: geo.size.height, alignment: .topLeading)
        }
        }
        .background(Theme.window)
    }

    func marker(_ k: DiffLine.Kind) -> String {
        switch k {
        case .added: return "+"
        case .removed: return "−"
        default: return ""
        }
    }

    func colour(_ k: DiffLine.Kind) -> Color {
        switch k {
        case .added: return Theme.ok
        case .removed: return Theme.err
        case .hunk: return Theme.work
        case .meta: return Theme.muted
        case .context: return Theme.text
        }
    }

    func background(_ k: DiffLine.Kind) -> Color {
        switch k {
        case .added: return Theme.ok.opacity(0.10)
        case .removed: return Theme.err.opacity(0.10)
        case .hunk: return Theme.work.opacity(0.08)
        default: return .clear
        }
    }

    var commitSheet: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Commit on \(model.branch)").font(.headline)
            Text("Every uncommitted change in its worktree (\(model.changes?.files.count ?? 0) files).").font(.system(size: 12)).foregroundStyle(.secondary)
            TextField("Message", text: $message, axis: .vertical).lineLimit(3...8).textFieldStyle(.roundedBorder).frame(width: 420)
            HStack {
                Spacer()
                Button("Cancel") { committing = false }.keyboardShortcut(.cancelAction)
                Button("Commit") {
                    model.commit(message)
                    message = ""
                    committing = false
                }
                .keyboardShortcut(.defaultAction)
                .disabled(message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(20)
    }
}
