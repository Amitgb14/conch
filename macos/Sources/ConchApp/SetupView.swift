import AppKit
import ConchKit
import SwiftUI

// What each agent loads in a checkout, in a tab of its own — the TUI's
// setup view (i): instructions, skills, MCP servers and the rest, each
// with where it comes from, and a worktree's local files against the main
// checkout. Read only: syncing one agent's setup to another is the TUI's.

@MainActor
final class SetupModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    let dir: String
    unowned let store: Store
    @Published var result: AgentSetupResult?
    @Published var error = ""

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project, dir) = parseSetupKey(key) ?? ("local", "", "")
        load()
    }

    func load() {
        guard let c = store.connection(machine) else { error = "\(store.label(machine)) is not connected"; return }
        guard c.hello?.capabilities?.contains("agent.setup.v1") == true else {
            error = "conch on \(store.label(machine)) is older than this app and can't say what agents load: reload its server."
            return
        }
        struct P: Encodable { var dir: String }
        Task {
            do {
                result = try await c.call("agent.setup", P(dir: dir), as: AgentSetupResult.self)
                error = ""
            } catch {
                self.error = "Could not read the agents' setup: \(error)"
            }
        }
    }
}

struct SetupView: View {
    @ObservedObject var model: SetupModel
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                LucideIcon("bot", color: Theme.accent, size: 14)
                Text("Agent setup").font(.system(size: 13, weight: .semibold))
                Text(model.dir).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
                Spacer()
                Button { model.load() } label: { LucideIcon("refresh-cw", size: 12) }.buttonStyle(.plain).help("Read it again")
            }
            .padding(.horizontal, 12).frame(height: 40)
            .background(Theme.paneHeader)
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(16).frame(maxWidth: .infinity, alignment: .leading)
                Spacer()
            } else if let r = model.result {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        ForEach(r.agents ?? []) { a in agent(a) }
                        if let files = r.localFiles, !files.isEmpty {
                            section("LOCAL FILES", note: "untracked files of the main checkout this worktree should have") {
                                ForEach(files, id: \.path) { f in
                                    HStack(spacing: 8) {
                                        Text(f.path).font(.system(size: 12, design: .monospaced))
                                        Text(localFileNote(f.state)).font(.system(size: 11.5))
                                            .foregroundStyle(f.state == "same" ? Theme.muted : Theme.warn)
                                    }
                                }
                            }
                        }
                    }
                    .padding(16).frame(maxWidth: .infinity, alignment: .leading)
                }
            } else {
                Text("Reading…").foregroundStyle(Theme.muted).padding(16)
                Spacer()
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .overlay(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)).stroke(Theme.line))
    }

    func agent(_ a: AgentSetup) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Mark(agent: a.agent)
                Text(a.label).font(.system(size: 13.5, weight: .semibold))
                Text("\(a.loaded) loaded").font(.system(size: 11.5)).foregroundStyle(Theme.muted)
            }
            ForEach(a.groups ?? [], id: \.title) { g in
                VStack(alignment: .leading, spacing: 3) {
                    Text(g.title.uppercased()).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
                    if (g.items ?? []).isEmpty {
                        Text("none").font(.system(size: 12)).foregroundStyle(Theme.muted)
                    }
                    ForEach(Array((g.items ?? []).enumerated()), id: \.offset) { _, it in item(it) }
                }
                .padding(.leading, 26)
            }
            ForEach(a.notes ?? [], id: \.self) { n in
                Text(n).font(.system(size: 11.5)).foregroundStyle(Theme.text2).padding(.leading, 26).fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    func item(_ it: SetupItem) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(it.name).font(.system(size: 12.5)).foregroundStyle(it.missing == true ? Theme.err : Theme.text)
            Text(it.scope).font(.system(size: 10.5)).foregroundStyle(Theme.text2)
                .padding(.horizontal, 5).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 4).fill(Theme.raise))
            if it.missing == true { Text("missing").font(.system(size: 11)).foregroundStyle(Theme.err) }
            if let d = it.detail, !d.isEmpty { Text(d).font(.system(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1) }
            Spacer(minLength: 0)
            if let p = it.path, !p.isEmpty, it.missing != true {
                // Opened in the machine's own editor, as Files opens one.
                Button { store.edit(machine: model.machine, root: (p as NSString).deletingLastPathComponent, path: (p as NSString).lastPathComponent) } label: {
                    Text((p as NSString).abbreviatingWithTildeInPath).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
                }
                .buttonStyle(.plain).help("Open \(p) in its machine's editor")
            }
        }
    }

    func section(_ title: String, note: String, @ViewBuilder _ body: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text(title).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
                Text(note).font(.system(size: 11)).foregroundStyle(Theme.muted)
            }
            body()
        }
    }
}
