import AppKit
import ConchKit
import SwiftUI

/// Settings → Git → Old branches: how old counts as old, then a project's
/// local branches nobody has committed to for that long, ticked where
/// deleting loses nothing, and deleted on a button — conch branch prune, in
/// the window. The server decides what each would lose and checks every
/// one again before it deletes; ConchKit/OldBranches.swift says what may be
/// ticked and what each row says.
struct OldBranchesCard: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    /// [git] stale_days, drawn here rather than under Merging.
    let fields: [SettingsField]
    @Environment(\.conchTheme) private var theme

    @State private var project = ""
    @State private var found: BranchStale?
    @State private var ticked: Set<String> = []
    @State private var busy = false
    @State private var failure = ""

    /// Every git project on every machine that is online, as machine|id.
    var projects: [(key: String, label: String)] {
        let store = model.store
        var out: [(key: String, label: String)] = []
        for m in store.machines where m.state.isEmpty {
            for p in store.snapshots[m.id]?.projects ?? [] where p.git != false {
                out.append((ConchKit.key(m.id, p.id), store.machines.count > 1 ? "\(p.name)  ·  \(m.label)" : p.name))
            }
        }
        return out.sorted { $0.label.localizedCaseInsensitiveCompare($1.label) == .orderedAscending }
    }

    var days: Int { OldBranches.days(model.form?.config["git"]?.object?["stale_days"]?.number) }

    var body: some View {
        let _ = theme
        SettingsCard(title: "Old branches") {
            ForEach(Array(fields.enumerated()), id: \.element.key) { i, f in
                FieldRow(model: model, section: section, field: f, first: i == 0)
            }
            VStack(alignment: .leading, spacing: 10) {
                Text("Local branches with no commit in \(days) days. The base branch and branches checked out in a worktree are kept; one with commits that are not merged or pushed is ticked only by you.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 8) {
                    Picker("", selection: $project) {
                        Text("Choose a project…").tag("")
                        ForEach(projects, id: \.key) { p in Text(p.label).tag(p.key) }
                    }
                    .labelsHidden().frame(maxWidth: 320, alignment: .leading)
                    Button("Find Old Branches") { find() }.disabled(project.isEmpty || busy)
                    if busy { ProgressView().controlSize(.small) }
                    Spacer()
                }
                if !failure.isEmpty {
                    Text(failure).font(.ui(size: 12)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                }
                if let f = found {
                    if f.branches.isEmpty {
                        Text("No branch older than \(f.days) days.").font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                    } else {
                        list(f.branches)
                    }
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
        }
        .onChange(of: project) { _, _ in found = nil; ticked = []; failure = "" }
    }

    @ViewBuilder func list(_ branches: [StaleBranch]) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(branches) { b in
                let can = OldBranches.deletable(b)
                let st = OldBranches.state(b)
                HStack(spacing: 10) {
                    Toggle("", isOn: Binding(get: { ticked.contains(b.name) }, set: { on in
                        if on { ticked.insert(b.name) } else { ticked.remove(b.name) }
                    }))
                    .toggleStyle(.checkbox).labelsHidden().disabled(!can)
                    Text(b.name).font(.ui(size: 12.5, weight: .medium, design: .monospaced))
                        .foregroundStyle(can ? Theme.text : Theme.muted).lineLimit(1).truncationMode(.middle)
                    Text(OldBranches.age(b.committed)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    Spacer(minLength: 8)
                    Text(st.text).font(.ui(size: 11.5)).foregroundStyle(st.warn ? Theme.warn : Theme.text2).lineLimit(1)
                }
                .padding(.vertical, 5)
                if b.id != branches.last?.id { Divider().overlay(Theme.line) }
            }
            HStack {
                // Only when some lose nothing: with none, there is nothing safe to tick.
                let safe = OldBranches.initialTicks(branches)
                if !safe.isEmpty {
                    Button(ticked == safe ? "Tick None" : "Tick Safe Ones") { ticked = ticked == safe ? [] : safe }
                        .buttonStyle(.link).font(.ui(size: 12))
                }
                Spacer()
                Button(ticked.isEmpty ? "Delete Branches" : "Delete \(ticked.count) Branch\(ticked.count == 1 ? "" : "es")…") {
                    delete(branches.filter { ticked.contains($0.name) })
                }
                .disabled(ticked.isEmpty || busy)
            }
            .padding(.top, 10)
        }
    }

    func connection() -> (Connection, String)? {
        let (m, pid) = unkey(project)
        guard let c = model.store.connection(m) else {
            failure = "\(model.store.label(m)) is not connected."
            return nil
        }
        guard c.hello?.capabilities?.contains(OldBranches.capability) == true else {
            failure = "conch on \(model.store.label(m)) is from an older build: reload its server (conch server reload) to find old branches."
            return nil
        }
        return (c, pid)
    }

    func find() {
        failure = ""
        guard let (c, pid) = connection() else { return }
        struct P: Encodable { var project_id: String; var days: Int }
        busy = true
        Task {
            defer { busy = false }
            do {
                let f = try await c.call("branch.stale", P(project_id: pid, days: days), as: BranchStale.self)
                found = f
                ticked = OldBranches.initialTicks(f.branches)
            } catch {
                failure = "Could not list the branches: \(error)"
            }
        }
    }

    func delete(_ chosen: [StaleBranch]) {
        guard !chosen.isEmpty, let (c, pid) = connection() else { return }
        let what = OldBranches.confirm(chosen)
        let a = NSAlert()
        a.messageText = what.title
        a.informativeText = chosen.map(\.name).joined(separator: "\n") + "\n\n" + what.detail
        a.alertStyle = what.losing ? .warning : .informational
        a.addButton(withTitle: what.losing ? "Delete Anyway" : "Delete")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        struct B: Encodable { var name: String; var force: Bool? }
        struct P: Encodable { var project_id: String; var branches: [B] }
        // Forced only where the person ticked one that loses commits, having
        // just been told so; the server still keeps the base and checkouts.
        let params = P(project_id: pid, branches: chosen.map { B(name: $0.name, force: ($0.unmerged ?? 0) > 0 ? true : nil) })
        busy = true
        Task {
            defer { busy = false }
            do {
                let r = try await c.call("branch.prune", params, as: BranchPruneResult.self)
                let n = r.deleted?.count ?? 0
                if let failed = r.failed, !failed.isEmpty {
                    failure = failed.map { "Kept \($0.path): \($0.error)" }.joined(separator: "\n")
                }
                model.say("Deleted \(n) branch\(n == 1 ? "" : "es")")
                find()
            } catch {
                failure = "Could not delete: \(error)"
            }
        }
    }
}
