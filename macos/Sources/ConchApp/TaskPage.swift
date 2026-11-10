import AppKit
import ConchKit
import SwiftUI

// A task across a group's repos, on one page — what the task row opens
// (the mock: task-page-mock.html). Overview is each repo where it stands,
// with what acts on all of them at once; Changes is every repo's files in
// one list, each repo committed on its own, the diff the git tab's; Brief
// is the task's CONCH-TASK.md. Each repo is still its own branch, so
// everything here is that branch's model (ChangesModel) or that
// project's snapshot; only Merge All When Green is the task's own, kept
// and run by the server (group.task.merge.v1).

struct TaskPageView: View {
    @ObservedObject var store: Store
    let key: String
    let machine: String
    let group: String
    let branch: String

    enum Mode: String, CaseIterable { case overview = "Overview", changes = "Changes", brief = "Brief" }
    @State private var mode: Mode = .overview
    @State private var mergeSheet = false

    init(store: Store, key: String) {
        self.store = store
        self.key = key
        (machine, group, branch) = parseTaskKey(key) ?? ("local", "", "")
    }

    var task: GroupTask? { store.groupTask(machine, group, branch) }
    var repos: [TaskRepo] { task.flatMap { store.snapshots[machine]?.taskRepos($0) } ?? [] }
    func model(_ id: String) -> ChangesModel { store.changes(for: changesKey(machine, id, branch)) }

    var body: some View {
        VStack(spacing: 0) {
            if let t = task {
                header(t)
                Rectangle().fill(Theme.line).frame(height: 1)
                switch mode {
                case .overview: overview(t)
                case .changes: TaskChanges(store: store, machine: machine, branch: branch, repos: repos.filter { !$0.merged && $0.checkedOut })
                case .brief: TaskBrief(store: store, machine: machine, task: t)
                }
            } else {
                VStack(spacing: 8) {
                    Text("This task is gone").font(.ui(size: 14, weight: .semibold)).foregroundStyle(Theme.text)
                    Text("It was removed, or its group was. Each repo keeps the branch \(branch).").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(store.layout.focused == key ? Theme.accent : Theme.line))
        .sheet(isPresented: $mergeSheet) {
            if let t = task { MergeAllSheet(store: store, machine: machine, group: group, task: t, repos: repos, shown: $mergeSheet) }
        }
    }

    func header(_ t: GroupTask) -> some View {
        HStack(spacing: 10) {
            LucideIcon("git-branch", color: Theme.text2, size: 12)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 8) {
                    Text(branch).font(.ui(size: 13, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1)
                    Chip(text: "\(group) · \(t.projects.count) repo\(t.projects.count == 1 ? "" : "s")" + (t.each == true ? " · an agent each" : ""))
                }
                Text(taskHeadline(repos)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            Spacer(minLength: 8)
            let online = store.connection(machine) != nil
            Button("Push All") { store.groupTaskEach(machine, group, branch, pr: false) }.controlSize(.small).disabled(!online)
            Button("Open Pull Requests…") { store.groupTaskEach(machine, group, branch, pr: true) }.controlSize(.small).disabled(!online)
                .help("A pull request for the branch in every repo, each naming the others")
            mergeButton(t).disabled(!online)
            Picker("", selection: $mode) {
                ForEach(Mode.allCases, id: \.self) { m in
                    Text(m == .changes ? "Changes \(repos.reduce(0) { $0 + $1.uncommitted })" : m.rawValue).tag(m)
                }
            }
            .pickerStyle(.segmented).labelsHidden().frame(width: 260)
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
    }

    @ViewBuilder func mergeButton(_ t: GroupTask) -> some View {
        if t.merge?.armed == true {
            Menu("Merge All: Armed") {
                Button("Change the Order…") { mergeSheet = true }
                Button("Stop Merge All") { store.groupTaskMerge(machine, group, branch, stop: true) }
            }
            .menuStyle(.borderlessButton).fixedSize().controlSize(.small)
        } else if t.merge?.state != "merged" {
            Button("Merge All When Green…") { mergeSheet = true }.buttonStyle(.borderedProminent).controlSize(.small)
                .disabled(store.connection(machine)?.hello?.capabilities?.contains("group.task.merge.v1") != true)
                .help(store.connection(machine)?.hello?.capabilities?.contains("group.task.merge.v1") == true
                    ? "Merge every repo's pull request, in an order you set, once all of them are green"
                    : "The server on \(store.label(machine)) predates Merge All: reload it")
        }
    }

    // MARK: overview

    func overview(_ t: GroupTask) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                agents(t)
                if let (text, tone) = mergeBanner(t.merge, repos: repos) { banner(text, tone, t) }
                ForEach(Array(repos.enumerated()), id: \.element.id) { i, r in
                    TaskRepoCard(store: store, machine: machine, branch: branch, index: i + 1, repo: r, model: model(r.id))
                }
                if repos.isEmpty {
                    Text("None of the task's repos are in conch now.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            .padding(14)
        }
    }

    /// The task's agents, each with its state and a way to it.
    @ViewBuilder func agents(_ t: GroupTask) -> some View {
        let panes = (t.panes ?? []).compactMap { store.pane(ConchKit.key(machine, $0)) }
        if !panes.isEmpty {
            HStack(spacing: 12) {
                ForEach(panes, id: \.id) { p in
                    Button { store.open(ConchKit.key(machine, p.id)) } label: {
                        HStack(spacing: 6) {
                            if let a = p.agent { Mark(agent: a.name) }
                            Text(p.displayName).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                            if p.agent?.state == .working {
                                TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                                    Text(spinnerFrame(at: ctx.date)).font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.work)
                                }
                            }
                            Text(p.running ? (p.agent?.state.rawValue ?? "running") : "exited").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain).help("Open \(p.displayName)")
                }
                Spacer(minLength: 8)
                if panes.count == 1, let p = panes.first {
                    Button("Open Agent") { store.open(ConchKit.key(machine, p.id)) }.controlSize(.small)
                    Button("Send a Message…") { store.promptAgent(ConchKit.key(machine, p.id)) }.controlSize(.small).disabled(p.agent == nil)
                } else {
                    Button("Message Every Agent…") { store.messageTaskAgents(machine, panes.map(\.id), what: "Message every agent of \(branch)") }
                        .controlSize(.small)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise.opacity(0.55)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        }
    }

    func banner(_ text: String, _ tone: MergeTone, _ t: GroupTask) -> some View {
        let color: Color = switch tone {
        case .held, .stopped: Theme.err
        case .merged: Theme.ok
        case .waiting, .merging: Theme.accent
        }
        return HStack(spacing: 10) {
            if tone == .merging {
                TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                    Text(spinnerFrame(at: ctx.date)).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.work)
                }
            }
            Text(text).font(.ui(size: 12.5)).foregroundStyle(Theme.text).lineLimit(3)
            Spacer(minLength: 8)
            switch t.merge?.state {
            case "waiting", "merging":
                Button("Stop") { store.groupTaskMerge(machine, group, branch, stop: true) }.controlSize(.small)
            case "stopped":
                Button("Arm Again…") { mergeSheet = true }.controlSize(.small)
            case "merged":
                Button("Remove Task…") { store.removeGroupTask(machine, group, branch) }.controlSize(.small)
            default: EmptyView()
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 8).fill(color.opacity(0.10)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(color.opacity(0.4)))
    }
}

/// One repo of a task: where its branch stands, its files and its pull
/// request, and what acts on that repo alone.
struct TaskRepoCard: View {
    @ObservedObject var store: Store
    let machine: String
    let branch: String
    let index: Int
    let repo: TaskRepo
    @ObservedObject var model: ChangesModel

    var failed: Bool { repo.pr?.checks == "fail" && !repo.merged }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text("\(index)").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.window)
                    .frame(width: 18, height: 18).background(Circle().fill(repo.merged ? Theme.merged : Theme.accent))
                    .help("Merge All merges it \(ordinal(index))")
                LucideIcon("folder", color: Theme.text2, size: 12)
                Text(repo.name).font(.ui(size: 12.5, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1)
                Text(repo.line).font(.ui(size: 11.5)).foregroundStyle(repo.uncommitted > 0 && !repo.merged ? Theme.warn : Theme.muted).lineLimit(1)
                Spacer(minLength: 8)
                if repo.added + repo.deleted > 0 && !repo.merged {
                    Text("+\(repo.added)").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.ok)
                    Text("−\(repo.deleted)").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.err)
                }
                if let pr = repo.pr {
                    if repo.merged {
                        Chip(text: "#\(pr.number) merged", tone: Theme.merged)
                    } else if ciChip(pr) != nil {
                        CIChip(pr: pr, number: true)
                    } else {
                        Chip(text: "#\(pr.number)" + (pr.draft == true ? " draft" : ""))
                    }
                    if !repo.merged, let r = pr.review {
                        Chip(text: r == "APPROVED" ? "approved" : r == "CHANGES_REQUESTED" ? "changes asked for" : "review: none",
                             tone: r == "APPROVED" ? Theme.ok : r == "CHANGES_REQUESTED" ? Theme.err : nil)
                    }
                } else if repo.merged {
                    Chip(text: "merged", tone: Theme.merged)
                } else {
                    Chip(text: "no pull request")
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            if !repo.merged { files }
            if failed, let pr = repo.pr, let runs = pr.runs?.filter({ $0.state == "fail" }), !runs.isEmpty {
                Rectangle().fill(Theme.line).frame(height: 1)
                ChecksList(runs: runs, limit: 4).padding(.horizontal, 12).padding(.vertical, 8)
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 6) {
                Button("Open Changes") { store.openChanges(project: key(machine, repo.id), branch: branch) }.controlSize(.small)
                if let pr = repo.pr, let u = pr.url.flatMap(URL.init(string:)) {
                    Button("View #\(pr.number) on GitHub") { NSWorkspace.shared.open(u) }.controlSize(.small)
                } else if !repo.merged {
                    Button("Open Pull Request") { model.openPR() }.controlSize(.small)
                }
                if repo.checkedOut && !repo.merged {
                    Button("Terminal Here") { store.openBranch(project: key(machine, repo.id), name: branch) }.controlSize(.small)
                        .help("Its agent, or a terminal in its worktree")
                }
                if failed {
                    Button("Send to the Agent") { model.fixCI() }.controlSize(.small)
                        .help("The failed check's log to the agent on this branch")
                }
                Spacer(minLength: 8)
                Text(repo.pushLine).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            .padding(.horizontal, 12).padding(.vertical, 7)
        }
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise.opacity(0.55)))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(failed ? Theme.err.opacity(0.5) : Theme.line))
    }

    /// What the branch changed against its base, then what is uncommitted
    /// (marked): a few lines, the rest counted.
    @ViewBuilder var files: some View {
        let ch = model.changes
        let committed = (ch?.committedFiles ?? []).filter { f in !(ch?.files ?? []).contains { $0.path == f.path } }
        let rows = (ch?.files ?? []).map { ($0, true) } + committed.map { ($0, false) }
        if !rows.isEmpty {
            Rectangle().fill(Theme.line).frame(height: 1)
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(rows.prefix(6).enumerated()), id: \.offset) { _, row in
                    let (f, open) = row
                    Button { store.openChanges(project: key(machine, repo.id), branch: branch); model.select(f.path, committed: !open) } label: {
                        HStack(spacing: 8) {
                            Text(f.code).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted).frame(width: 12)
                            Text(f.path).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.head)
                            Spacer(minLength: 6)
                            if f.added > 0 { Text("+\(f.added)").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.ok) }
                            if f.deleted > 0 { Text("−\(f.deleted)").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.err) }
                            if open { Circle().fill(Theme.warn).frame(width: 6, height: 6).help("Uncommitted") }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                if rows.count > 6 {
                    Text("and \(rows.count - 6) more").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 7)
        }
    }

    func ordinal(_ n: Int) -> String {
        switch n {
        case 1: return "first"
        case 2: return "second"
        case 3: return "third"
        default: return "\(n)th"
        }
    }
}

// MARK: changes

/// Every repo's files in one list, grouped by repo, each with its own
/// commit box — a commit belongs to one repo — and the diff of the file
/// picked, the git tab's own.
struct TaskChanges: View {
    @ObservedObject var store: Store
    let machine: String
    let branch: String
    let repos: [TaskRepo]
    @State private var picked: String?

    var body: some View {
        let shown = picked.flatMap { p in repos.first { $0.id == p } } ?? repos.first
        HStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Spacer()
                        Button("Commit Each…") { commitEach() }.controlSize(.small)
                            .disabled(!repos.contains { ($0.uncommitted) > 0 })
                            .help("Commit every repo's uncommitted work, each in its own repo")
                    }
                    ForEach(repos, id: \.id) { r in
                        TaskRepoFiles(store: store, repo: r, model: store.changes(for: changesKey(machine, r.id, branch)),
                                      picked: shown?.id == r.id) { picked = r.id }
                    }
                    if repos.isEmpty {
                        Text("No repo has the branch checked out.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                }
                .padding(10)
            }
            .frame(width: 300)
            .background(Theme.side)
            Rectangle().fill(Theme.line).frame(width: 1)
            if let r = shown {
                VStack(spacing: 0) {
                    HStack(spacing: 6) {
                        LucideIcon("folder", color: Theme.text2, size: 12)
                        Text(r.name).font(.ui(size: 12, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text2)
                        Spacer()
                        Button("Open in Its Git Tab") { store.openChanges(project: key(machine, r.id), branch: branch) }.controlSize(.small)
                            .help("Hunks and lines ticked, comments, the review")
                    }
                    .padding(.horizontal, 12).padding(.top, 8)
                    GitDiff(model: store.changes(for: changesKey(machine, r.id, branch)))
                }
            } else {
                Spacer()
            }
        }
    }

    /// One message for every repo with uncommitted work, or each repo's own
    /// box where that has one.
    func commitEach() {
        let models = repos.map { store.changes(for: changesKey(machine, $0.id, branch)) }.filter { !($0.changes?.files.isEmpty ?? true) }
        guard !models.isEmpty else { return }
        let names = models.compactMap { m in repos.first { $0.id == m.project }?.name }
        guard let text = store.askCommitMessage(names) else { return }
        let msg = text.trimmingCharacters(in: .whitespacesAndNewlines)
        var skipped: [String] = []
        for m in models {
            if !msg.isEmpty { m.message = msg }
            if m.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                skipped.append(repos.first { $0.id == m.project }?.name ?? m.project)
                continue
            }
            m.commit()
        }
        if !skipped.isEmpty { store.say("Not committed, with no message: \(skipped.joined(separator: ", "))", .err) }
    }
}

/// A repo's group in the task's file list.
struct TaskRepoFiles: View {
    @ObservedObject var store: Store
    let repo: TaskRepo
    @ObservedObject var model: ChangesModel
    let picked: Bool
    let pick: () -> Void

    var body: some View {
        let ch = model.changes
        let open = ch?.files ?? []
        let committed = (ch?.committedFiles ?? []).filter { f in !open.contains { $0.path == f.path } }
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                LucideIcon("folder", color: Theme.text2, size: 12)
                Text(repo.name).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text)
                Spacer()
                Text(open.isEmpty ? "\(committed.count) · committed" : "\(open.count) uncommitted")
                    .font(.ui(size: 11)).foregroundStyle(open.isEmpty ? Theme.muted : Theme.warn)
            }
            .padding(.bottom, 2)
            ForEach(open) { f in row(f, committed: false) }
            ForEach(committed) { f in row(f, committed: true) }
            if open.isEmpty && committed.isEmpty {
                Text(ch == nil ? "Reading…" : "Nothing changed yet").font(.ui(size: 11.5)).foregroundStyle(Theme.muted).padding(.leading, 18)
            }
            if !open.isEmpty {
                HStack(spacing: 6) {
                    TextField("Message", text: $model.message).textFieldStyle(.roundedBorder).font(.ui(size: 11.5))
                    Button { model.writeMessage() } label: {
                        if model.writing { ProgressView().controlSize(.mini) } else { Text("✦") }
                    }
                    .controlSize(.small).disabled(model.writing).help("Draft the message from this repo's changes")
                    Button("Commit") { model.commit() }.controlSize(.small)
                        .disabled(model.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.busy != nil)
                }
                .padding(.leading, 18).padding(.top, 4)
            }
            if let e = model.error {
                Text(e).font(.ui(size: 11)).foregroundStyle(Theme.err).lineLimit(2).padding(.leading, 18)
            }
        }
    }

    func row(_ f: FileChange, committed: Bool) -> some View {
        let on = picked && model.selected == f.path && model.selectedCommitted == committed
        return Button {
            pick()
            model.select(f.path, committed: committed)
        } label: {
            HStack(spacing: 6) {
                Text(f.code).font(.ui(size: 11, design: .monospaced)).foregroundStyle(on ? Theme.window : Theme.muted).frame(width: 12)
                Text(f.path).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(on ? Theme.window : Theme.text).lineLimit(1).truncationMode(.head)
                Spacer(minLength: 4)
                Text("+\(f.added)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(on ? Theme.window : Theme.ok)
                if f.deleted > 0 { Text("−\(f.deleted)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(on ? Theme.window : Theme.err) }
            }
            .padding(.leading, 18).padding(.trailing, 4).padding(.vertical, 2)
            .background(RoundedRectangle(cornerRadius: 4).fill(on ? Theme.accent : .clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: brief

/// The task's CONCH-TASK.md — the prompt, its repos and the group's notes,
/// what its agents read first — edited here, and the agents told.
struct TaskBrief: View {
    @ObservedObject var store: Store
    let machine: String
    let task: GroupTask
    @State private var text = ""
    @State private var original = ""
    @State private var hash: String?
    @State private var error: String?
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                LucideIcon("file-text", color: Theme.text2, size: 12)
                Text(task.folder + "/CONCH-TASK.md").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
                    .lineLimit(1).truncationMode(.head).textSelection(.enabled)
                Spacer()
                Button("Save") { save() }.controlSize(.small).disabled(text == original || !loaded)
                    .keyboardShortcut("s", modifiers: .command)
                Button("Tell the Agents") { tell() }.controlSize(.small)
                    .disabled((task.panes ?? []).allSatisfy { store.pane(key(machine, $0))?.agent == nil })
                    .help("Ask every agent of the task to read the brief again")
            }
            if let e = error { Text(e).font(.ui(size: 11.5)).foregroundStyle(Theme.err).lineLimit(2) }
            TextEditor(text: $text).font(.ui(size: 12.5, design: .monospaced))
                .scrollContentBackground(.hidden).padding(8)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise.opacity(0.55)))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        }
        .padding(14)
        .task(id: task.folder) { await load() }
    }

    struct Ref: Encodable { var root: String; var path: String }

    func load() async {
        guard let c = store.connection(machine) else { error = "\(store.label(machine)) is not connected"; return }
        // Reading a task's folder came with Merge All (group.task.merge.v1).
        guard c.hello?.capabilities?.contains("group.task.merge.v1") == true else {
            error = "The server on \(store.label(machine)) predates reading a task's brief here: reload it, or open \(task.folder)/CONCH-TASK.md yourself"
            return
        }
        do {
            let r = try await c.call("fs.read", Ref(root: task.folder, path: "CONCH-TASK.md"), as: FSRead.self)
            text = r.data ?? ""
            original = text
            hash = r.hash
            loaded = true
            error = r.truncated == true ? "Only the start of the brief was read; saving is off" : nil
            if r.truncated == true { loaded = false }
        } catch {
            self.error = "Could not read the brief: \(error)"
        }
    }

    func save() {
        guard let c = store.connection(machine) else { return }
        struct W: Encodable { var root: String; var path: String; var data: String; var base: String? }
        let data = text
        Task {
            do {
                let r = try await c.call("fs.write", W(root: task.folder, path: "CONCH-TASK.md", data: data, base: hash), as: FSWriteResult.self)
                original = data
                hash = r.hash
                error = nil
                store.say("Saved the brief")
            } catch {
                self.error = "Could not save the brief: \(error)"
            }
        }
    }

    func tell() {
        let panes = (task.panes ?? []).filter { store.pane(key(machine, $0))?.agent != nil }
        store.sendTaskAgents(machine, panes, text: "The task's brief changed: read \(task.folder)/CONCH-TASK.md again and carry on with what it says now.")
    }
}

// MARK: merge all

/// Merge All When Green…: the order the repos merge in, dragged, and
/// whether the task goes once all are merged.
struct MergeAllSheet: View {
    @ObservedObject var store: Store
    let machine: String
    let group: String
    let task: GroupTask
    @Binding var shown: Bool
    @State private var order: [TaskRepo]
    @State private var removeTask: Bool

    init(store: Store, machine: String, group: String, task: GroupTask, repos: [TaskRepo], shown: Binding<Bool>) {
        self.store = store
        self.machine = machine
        self.group = group
        self.task = task
        _shown = shown
        _order = State(initialValue: repos)
        _removeTask = State(initialValue: task.merge?.removeTask ?? false)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Merge all \(order.count) pull requests when every one is green?").font(.ui(size: 14, weight: .semibold))
            Text("Nothing merges until all of them are open with passing checks, and nothing holds one back that would hold it for the autopilot — a draft, changes asked for, a conflict, an agent still at it. Then they merge one by one in this order, each after the one before has landed; if GitHub refuses one, it stops there. The server waits, so this carries on with the app closed. Each repo merges its own way, as GitHub has it set.")
                .font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            Text("ORDER — drag to change").font(.ui(size: 11, weight: .semibold)).tracking(0.4).foregroundStyle(Theme.muted)
            List {
                ForEach(Array(order.enumerated()), id: \.element.id) { i, r in
                    HStack(spacing: 8) {
                        Image(systemName: "line.3.horizontal").foregroundStyle(Theme.muted)
                        Text("\(i + 1)").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.window)
                            .frame(width: 18, height: 18).background(Circle().fill(r.merged ? Theme.merged : Theme.accent))
                        Text(r.name).font(.ui(size: 12.5, design: .monospaced))
                        Text(r.pr.map { "#\($0.number) into \(r.base ?? "its base")" } ?? "no pull request yet").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        Spacer()
                        if r.merged {
                            Chip(text: "merged", tone: Theme.merged)
                        } else if let pr = r.pr, ciChip(pr) != nil {
                            CIChip(pr: pr)
                        }
                    }
                }
                .onMove { order.move(fromOffsets: $0, toOffset: $1) }
            }
            .frame(height: CGFloat(min(order.count, 6)) * 30 + 12)
            Toggle(isOn: $removeTask) {
                Text("Then remove the task: its folder, and each repo's worktree and branch").font(.ui(size: 12.5))
            }
            .toggleStyle(.checkbox)
            HStack {
                Spacer()
                Button("Cancel") { shown = false }.keyboardShortcut(.cancelAction)
                Button(task.merge?.armed == true ? "Save Order" : "Arm Merge All") {
                    store.groupTaskMerge(machine, group, task.branch, order: order.map(\.id), removeTask: removeTask)
                    shown = false
                }
                .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(order.isEmpty)
            }
        }
        .padding(18)
        .frame(width: 520)
    }
}
