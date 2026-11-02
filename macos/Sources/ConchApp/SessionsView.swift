import AppKit
import ConchKit
import SwiftUI

// A project's saved conversations, in a tab of their own — what the
// Sessions row opens, as the TUI's Sessions view: each agent's
// conversations newest first (interrupted ones above them), searched by
// title, branch or agent; Resume starts the agent on it again in a pane,
// Open goes to the pane it is open in, and delete asks first.

@MainActor
final class SessionsModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    unowned let store: Store
    @Published var sessions: [SessionInfo] = []
    @Published var query = ""
    @Published var loading = false
    @Published var partial = false
    @Published var error = ""

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseSessionsKey(key) ?? ("local", "")
        load()
    }

    var shown: [SessionInfo] { filterSessions(sessions, query) }

    func load() {
        guard let c = store.connection(machine) else {
            error = "\(store.label(machine)) is not connected"
            return
        }
        struct Params: Encodable { var project_id: String; var limit: Int }
        loading = true
        Task {
            defer { loading = false }
            do {
                let r = try await c.call("session.list", Params(project_id: project, limit: 300), as: SessionListResult.self)
                sessions = r.sessions ?? []
                partial = r.partial ?? false
                error = ""
                // An agent that had not answered yet is asked again.
                if partial { try? await Task.sleep(nanoseconds: 2_000_000_000); load() }
            } catch {
                self.error = "Could not read the sessions: \(error)"
            }
        }
    }

    private struct Ref: Encodable {
        var agent: String
        var id: String?
        var dir: String
    }

    /// Resumes a conversation in a pane, or goes to the pane it is open in.
    func resume(_ s: SessionInfo) {
        if let p = s.paneID, !p.isEmpty {
            store.open(ConchKit.key(machine, p))
            return
        }
        guard let c = store.connection(machine) else { return }
        Task {
            do {
                let p = try await c.call("session.resume", Ref(agent: s.agent, id: s.sessionID, dir: s.dir), as: PaneInfo.self)
                store.snapshots[machine, default: Snapshot()].upsert(p)
                store.open(ConchKit.key(machine, p.id))
                load()
            } catch {
                self.error = "Could not resume it: \(error)"
            }
        }
    }

    /// Deletes a conversation, after asking: the agent's own record of it
    /// goes, which nothing brings back.
    func delete(_ s: SessionInfo) {
        let a = NSAlert()
        a.messageText = "Delete “\(s.name)”?"
        a.informativeText = "The \(s.agent) conversation is deleted where \(s.agent) keeps it, and cannot be resumed again."
        a.addButton(withTitle: "Delete")
        a.addButton(withTitle: "Cancel")
        a.buttons.first?.hasDestructiveAction = true
        guard a.runModal() == .alertFirstButtonReturn, let c = store.connection(machine) else { return }
        Task {
            do {
                try await c.call("session.delete", Ref(agent: s.agent, id: s.sessionID, dir: s.dir))
                sessions.removeAll { $0.id == s.id }
            } catch {
                self.error = "Could not delete it: \(error)"
            }
        }
    }
}

struct SessionsView: View {
    @ObservedObject var model: SessionsModel
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(16)
            }
            let shown = model.shown
            if shown.isEmpty && !model.loading {
                VStack {
                    Spacer()
                    Text(model.sessions.isEmpty ? "No saved conversations in this project yet." : "Nothing matches “\(model.query)”")
                        .foregroundStyle(Theme.muted)
                    Spacer()
                }
                .frame(maxWidth: .infinity)
            } else {
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(shown) { SessionRow(model: model, session: $0) }
                    }
                    .padding(10)
                }
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
    }

    var header: some View {
        let name = store.snapshots[model.machine]?.project(model.project)?.name ?? model.project
        return HStack(spacing: 10) {
            LucideIcon("history", color: Theme.accent, size: 14)
            Text("Sessions").font(.system(size: 13, weight: .semibold))
            Text(name).font(.system(size: 12)).foregroundStyle(Theme.text2)
            Text("\(model.sessions.count)").font(.system(size: 11)).foregroundStyle(Theme.muted)
            if model.loading || model.partial { ProgressView().controlSize(.small) }
            Spacer()
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").font(.system(size: 11)).foregroundStyle(Theme.muted)
                TextField("Search title, branch, agent", text: $model.query).textFieldStyle(.plain).font(.system(size: 12))
            }
            .padding(.horizontal, 8).frame(width: 260, height: 26)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line2))
            Button { model.load() } label: { LucideIcon("refresh-cw", size: 12) }.buttonStyle(.plain).help("Read them again")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }
}

struct SessionRow: View {
    @ObservedObject var model: SessionsModel
    let session: SessionInfo
    @State private var hovering = false

    var body: some View {
        let open = !(session.paneID ?? "").isEmpty
        HStack(spacing: 10) {
            Mark(agent: session.agent)
            VStack(alignment: .leading, spacing: 3) {
                Text(session.name).font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.text).lineLimit(1)
                HStack(spacing: 8) {
                    Text(agentTitle(session.agent)).foregroundStyle(Theme.text2)
                    if let b = session.branch, !b.isEmpty {
                        HStack(spacing: 4) {
                            LucideIcon("git-branch", color: Theme.muted, size: 10)
                            Text(b).font(.system(size: 11.5, design: .monospaced))
                        }
                    }
                    Text(agoText(session.updated))
                }
                .font(.system(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            Spacer(minLength: 8)
            if session.interrupted == true { Pill(text: "interrupted", fg: Theme.onWarn, bg: Theme.warn) }
            if open { Pill(text: "open", fg: Theme.ok, bg: Theme.ok.opacity(0.16)) }
            Button(open ? "Open" : "Resume") { model.resume(session) }
                .buttonStyle(.plain).font(.system(size: 12, weight: .medium))
                .foregroundStyle(Theme.selectedText)
                .padding(.horizontal, 10).padding(.vertical, 3)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.accentDeep))
                .help(open ? "Go to the pane it is open in" : "Start \(session.agent) on this conversation again, in a pane")
            Button { model.delete(session) } label: { Image(systemName: "trash").font(.system(size: 11)) }
                .buttonStyle(.plain).foregroundStyle(Theme.muted).opacity(hovering ? 1 : 0)
                .help("Delete this conversation")
        }
        .padding(.horizontal, 10).frame(height: 48)
        .background(RoundedRectangle(cornerRadius: 7).fill(hovering ? Theme.raise : .clear))
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture(count: 2) { model.resume(session) }
    }

    func agentTitle(_ name: String) -> String {
        ["claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin"][name] ?? name
    }
}
