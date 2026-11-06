import ConchKit
import SwiftUI

// conch's skills, per machine: whether each agent there has them, and
// installing or removing them — the TUI's Settings → Agents → conch's
// skills. Every machine is read with agent.skill as a plan, which writes
// nothing; a write is asked again with apply, after a question naming the
// paths it touches, and the plan is read afresh after it.

@MainActor
final class AgentSkillModel: ObservableObject {
    unowned let store: Store
    struct Row: Identifiable {
        var id: String // the machine
        var result: AgentSkillResult?
        var error = ""
        var loading = false
    }
    @Published var rows: [Row] = []
    @Published var open: String?
    @Published var busy = false

    init(store: Store) { self.store = store }

    func load() {
        rows = store.machines.map { Row(id: $0.id) }
        for r in rows { read(r.id) }
        if open == nil { open = rows.first?.id }
    }

    func read(_ m: String) {
        guard let i = rows.firstIndex(where: { $0.id == m }) else { return }
        guard let c = store.connection(m) else { rows[i].error = "\(store.label(m)) is not connected"; return }
        guard c.hello?.capabilities?.contains("agent.skill.v1") == true else {
            rows[i].error = "its server predates the skill; reload or upgrade it"
            return
        }
        rows[i].loading = true
        Task {
            struct P: Encodable {}
            do {
                let r = try await c.call("agent.skill", P(), as: AgentSkillResult.self)
                set(m) { $0.result = r; $0.error = ""; $0.loading = false }
            } catch {
                set(m) { $0.error = "\(error)"; $0.loading = false }
            }
        }
    }

    /// Installs it for every agent on the machine, or takes conch's copy
    /// away, after asking with the paths it touches.
    func apply(_ m: String, remove: Bool) {
        guard let c = store.connection(m) else { return }
        struct P: Encodable { var remove: Bool?; var apply: Bool? }
        busy = true
        Task {
            defer { busy = false }
            do {
                // The plan for exactly this write, so the question names it.
                let plan = try await c.call("agent.skill", P(remove: remove ? true : nil), as: AgentSkillResult.self)
                let writes = skillWrites(plan)
                if writes.isEmpty {
                    store.say(remove ? "conch's skills aren't installed on \(store.label(m))" : "Every agent on \(store.label(m)) has conch's skills", .info)
                    return
                }
                let a = NSAlert()
                a.messageText = remove ? "Take conch's skills away on \(store.label(m))?" : "Install conch's skills on \(store.label(m))?"
                a.informativeText = writes.map { "\($0.path)\n    for \($0.agents.joined(separator: ", "))" }.joined(separator: "\n")
                    + (remove ? "\n\nOnly conch's own copies go; a skill of yours of that name is left alone."
                              : "\n\nBeside your own skills, replacing none of them.")
                a.addButton(withTitle: remove ? "Remove" : "Install")
                a.addButton(withTitle: "Cancel")
                guard a.runModal() == .alertFirstButtonReturn else { return }
                let done = try await c.call("agent.skill", P(remove: remove ? true : nil, apply: true), as: AgentSkillResult.self)
                if let failed = done.changes.first(where: { !($0.error ?? "").isEmpty }) {
                    store.error = "\(failed.path): \(failed.error ?? "")"
                } else {
                    store.say(remove ? "conch's skills removed on \(store.label(m))" : "conch's skills installed on \(store.label(m))")
                }
                read(m) // what was written is the new plan
            } catch {
                store.error = "\(error)"
            }
        }
    }

    private func set(_ m: String, _ f: (inout Row) -> Void) {
        guard let i = rows.firstIndex(where: { $0.id == m }) else { return }
        f(&rows[i])
    }
}

struct AgentSkillSheet: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    @StateObject private var model: AgentSkillModel

    init(store: Store) {
        self.store = store
        _model = StateObject(wrappedValue: AgentSkillModel(store: store))
    }

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 10) {
            Text("conch's skills for agents").font(.ui(size: 15, weight: .semibold))
            Text("So an agent can run others: conch starts a helper and reads its answer, conch-team runs several at once, conch-review loops review → fix → review, conch-ship takes a branch to a merged pull request, and conch-factory takes a ticket all the way. They go where each agent reads your skills, beside them, replacing none.")
                .font(.ui(size: 12)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            ScrollView {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(model.rows) { r in machine(r) }
                }
            }
            .frame(height: 400)
            HStack {
                Text("conch agent skill, on each machine").font(.ui(size: 11.5)).foregroundStyle(.secondary)
                Spacer()
                Button("Read again") { model.load() }.disabled(model.busy)
                Button("Done") { store.skillOpen = false }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 600)
        .onAppear { model.load() }
    }

    @ViewBuilder func machine(_ r: AgentSkillModel.Row) -> some View {
        let isOpen = model.open == r.id
        VStack(alignment: .leading, spacing: 6) {
            Button {
                model.open = isOpen ? nil : r.id
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: isOpen ? "chevron.down" : "chevron.right").font(.system(size: 10, weight: .semibold)).foregroundStyle(.secondary).frame(width: 12)
                    Text(store.label(r.id)).font(.ui(size: 13, weight: .semibold))
                    Spacer()
                    standing(r)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if isOpen, let res = r.result {
                ForEach(skillFolders(res)) { f in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(f.agents.joined(separator: ", ")).font(.ui(size: 12, weight: .medium))
                        Text(f.path).font(.ui(size: 11, design: .monospaced)).foregroundStyle(.secondary).textSelection(.enabled)
                        ForEach(f.changes, id: \.path) { c in
                            HStack {
                                Text(c.skillName).font(.ui(size: 11.5, design: .monospaced))
                                Spacer()
                                Text(skillLine(c)).font(.ui(size: 11.5)).lineLimit(1).truncationMode(.tail)
                                    .foregroundStyle(!(c.error ?? "").isEmpty ? Theme.err : c.action == "same" ? Theme.ok : c.action == "skip" ? Theme.muted : Theme.warn)
                            }
                            .padding(.leading, 12)
                        }
                    }
                    .padding(.leading, 20)
                }
                HStack(spacing: 8) {
                    let (have, todo, _) = skillCounts(res)
                    Button(todo > 0 && have > 0 ? "Install for the rest…" : "Install for every agent…") { model.apply(r.id, remove: false) }
                        .disabled(todo == 0 || model.busy)
                    Button("Take conch's copies away…") { model.apply(r.id, remove: true) }
                        .disabled(!skillAnyInstalled(res) || model.busy)
                }
                .controlSize(.small).padding(.leading, 20)
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
    }

    @ViewBuilder func standing(_ r: AgentSkillModel.Row) -> some View {
        if !r.error.isEmpty {
            Text(r.error).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).lineLimit(1).truncationMode(.tail)
        } else if let res = r.result {
            let s = skillStanding(res)
            let theirs = skillCounts(res).theirs
            Text(s.text + (theirs > 0 ? " · \(theirs) yours" : "")).font(.ui(size: 11.5))
                .foregroundStyle(s == .everyAgent ? Theme.ok : s == .noAgentReadsSkills ? Theme.muted : Theme.warn)
        } else if r.loading {
            ProgressView().controlSize(.small)
        }
    }
}
