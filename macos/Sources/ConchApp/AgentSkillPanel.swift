import ConchKit
import SwiftUI

// conch's skills, per machine: whether each agent there has them, and
// installing or removing them — the TUI's Settings → Agents → conch's
// skills; in the app, Settings → Skills & MCP servers. Every machine is read with agent.skill as a plan, which writes
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
    @Published var busy = false

    init(store: Store) { self.store = store }

    func load() {
        rows = store.machines.map { Row(id: $0.id) }
        for r in rows { read(r.id) }
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

/// conch's skills in Settings → Skills & MCP servers, for the machine
/// picked there (store.skillsMachine).
struct AgentSkillPanel: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    @StateObject private var model: AgentSkillModel

    init(store: Store) {
        self.store = store
        _model = StateObject(wrappedValue: AgentSkillModel(store: store))
    }

    @State private var showWhere = false

    var body: some View {
        let _ = look
        let row = model.rows.first { $0.id == store.skillsMachine }
        SettingsCard(title: "conch's skills", trailing: AnyView(row.map { standing($0) })) {
            if let r = row, let res = r.result {
                content(r.id, res)
            } else if let r = row, !r.error.isEmpty {
                Text(r.error).font(.ui(size: 12)).foregroundStyle(Theme.warn).padding(16)
            } else {
                ProgressView().controlSize(.small).padding(16)
            }
        }
        .onAppear { model.load() }
        .onChange(of: store.skillsMachine) { _, m in
            if !model.rows.contains(where: { $0.id == m }) { model.load() } else { model.read(m) }
        }
    }

    @ViewBuilder func content(_ m: String, _ res: AgentSkillResult) -> some View {
        let cover = skillCoverage(res)
        let full = cover.filter(\.everywhere).count
        HStack(spacing: 14) {
            ZStack {
                Circle().stroke(Theme.line2, lineWidth: 4)
                Circle().trim(from: 0, to: cover.isEmpty ? 0 : CGFloat(full) / CGFloat(cover.count))
                    .stroke(full == cover.count && !cover.isEmpty ? Theme.ok : Theme.accent, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                    .animation(.easeOut(duration: 0.5), value: full)
                Text("\(full)/\(cover.count)").font(.ui(size: 11.5, weight: .bold))
            }
            .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 2) {
                Text(cover.isEmpty ? "No agent here reads skills" : full == cover.count ? "Every agent has all \(cover.count)" : "\(full) of \(cover.count) skills on every agent")
                    .font(.ui(size: 13, weight: .medium))
                Text("conch starts a helper; team, review, ship, factory and autopilot build on it. Beside your own skills, replacing none.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            let (have, todo, _) = skillCounts(res)
            Button { model.apply(m, remove: false) } label: {
                HStack(spacing: 6) {
                    if model.busy { ProgressView().controlSize(.mini) }
                    Text(todo == 0 ? "✓ Installed" : have > 0 || skillAnyInstalled(res) ? "Install the rest…" : "Install all…").font(.ui(size: 12.5, weight: .semibold))
                }
                .foregroundStyle(todo > 0 ? Theme.selectedText : Theme.ok)
                .padding(.horizontal, 14).frame(height: 30)
                .background(RoundedRectangle(cornerRadius: 8).fill(todo > 0 ? Theme.accent : Theme.ok.opacity(0.12)))
            }
            .buttonStyle(.plain)
            .disabled(todo == 0 || model.busy)
        }
        .padding(16)
        ForEach(cover) { c in
            HStack(spacing: 12) {
                Image(systemName: c.everywhere ? "checkmark.square.fill" : "book.closed").font(.system(size: 13))
                    .foregroundStyle(c.everywhere ? Theme.ok : Theme.muted)
                    .frame(width: 30, height: 30).background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise2))
                VStack(alignment: .leading, spacing: 1) {
                    Text(c.name).font(.ui(size: 12.5, weight: .medium, design: .monospaced))
                    Text(conchSkillBlurbs[c.name] ?? "").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                Spacer()
                let tag = c.everywhere ? "✓ every agent" : c.have == 0 ? "not installed" : "\(c.have) of \(c.of) folders"
                Text(tag).font(.ui(size: 11)).foregroundStyle(c.everywhere ? Theme.ok : c.have == 0 ? Theme.muted : Theme.warn)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .overlay(Capsule().stroke(c.everywhere ? Theme.ok.opacity(0.4) : Theme.line2))
            }
            .padding(.horizontal, 16).padding(.vertical, 8)
            .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        }
        HStack(spacing: 14) {
            Button(showWhere ? "Hide where" : "Show where they go") { withAnimation { showWhere.toggle() } }
                .buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.accent)
            Spacer()
            Button("Read again") { model.read(m) }.buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.text2).disabled(model.busy)
            Button("Take conch's copies away…") { model.apply(m, remove: true) }
                .buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(skillAnyInstalled(res) ? Theme.err : Theme.muted)
                .disabled(!skillAnyInstalled(res) || model.busy)
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        if showWhere {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(skillFolders(res)) { f in
                    VStack(alignment: .leading, spacing: 1) {
                        Text(f.agents.joined(separator: ", ")).font(.ui(size: 12, weight: .medium))
                        Text(f.path).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).textSelection(.enabled)
                        ForEach(f.changes.filter { $0.action == "skip" || !($0.error ?? "").isEmpty }, id: \.path) { c in
                            Text("\(c.skillName): \(skillLine(c))").font(.ui(size: 11)).foregroundStyle(Theme.warn)
                        }
                    }
                }
            }
            .padding(.horizontal, 16).padding(.bottom, 12)
        }
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
