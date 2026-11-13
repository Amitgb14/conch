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
    /// What a search inside the conversations found (session.search), for
    /// the query it was asked for; nil while the query is too short.
    @Published var hits: [SessionInfo]?
    private var hitsFor = ""
    private var searches = Latest()
    /// How many times running a partial list has been asked again.
    private var asked = 0

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseSessionsKey(key) ?? ("local", "")
        load()
    }

    /// What the list shows: a search's hits inside the conversations when
    /// it has them for this query, else the titles filtered here.
    var shown: [SessionInfo] {
        if let hits, hitsFor == query { return orderSessions(hits) }
        return filterSessions(sessions, query)
    }

    var caps: [String] { store.connection(machine)?.hello?.capabilities ?? [] }

    /// Looks inside the conversations too, a moment after typing stops.
    func queryChanged() {
        let q = query
        guard searchesContents(q), caps.contains("session.search.v1"), let c = store.connection(machine) else { hits = nil; return }
        let n = searches.next()
        Task {
            try? await Task.sleep(nanoseconds: 350_000_000)
            guard searches.isLatest(n), q == query else { return }
            struct P: Encodable { var project_id: String; var query: String; var limit: Int }
            if let r = try? await c.call("session.search", P(project_id: project, query: q, limit: 200), as: SessionListResult.self),
               searches.isLatest(n) {
                hits = r.sessions ?? []
                hitsFor = q
            }
        }
    }

    /// Stars a conversation, or takes its star away.
    func star(_ s: SessionInfo) {
        guard let c = store.connection(machine), let id = s.sessionID, !id.isEmpty else { return }
        guard caps.contains("session.star.v1") else {
            store.say("conch on \(store.label(machine)) is too old to star a conversation: reload its server", .err)
            return
        }
        struct P: Encodable { var agent: String; var id: String; var on: Bool }
        let on = !(s.starred ?? false)
        Task {
            do {
                try await c.call("session.star", P(agent: s.agent, id: id, on: on))
                func flip(_ l: inout [SessionInfo]) { if let i = l.firstIndex(where: { $0.id == s.id }) { l[i].starred = on } }
                flip(&sessions)
                if hits != nil { flip(&hits!) }
            } catch {
                store.say("Could not star it: \(error)", .err)
            }
        }
    }

    /// Hands a conversation to another agent: conch writes it out where
    /// the session was and starts that agent there to read it.
    func handOff(_ s: SessionInfo, to agent: String) {
        guard let c = store.connection(machine), let id = s.sessionID, !id.isEmpty else { return }
        guard caps.contains("session.share.v1") else {
            store.say("conch on \(store.label(machine)) is too old to hand a conversation over: reload its server", .err)
            return
        }
        struct P: Encodable { var agent: String; var id: String; var dir: String; var to: String }
        struct R: Decodable { var pane: PaneInfo }
        store.job("Handing “\(s.name)” to \(agent)", done: "Handed “\(s.name)” to \(agent)") {
            let r = try await c.call("session.share", P(agent: s.agent, id: id, dir: s.dir, to: agent), as: R.self)
            self.store.snapshots[self.machine, default: Snapshot()].upsert(r.pane)
            self.store.open(ConchKit.key(self.machine, r.pane.id))
        }
    }

    /// Every interrupted conversation not open anywhere, resumed — what a
    /// restart or reboot cut off.
    func resumeAll() {
        let list = resumable(sessions)
        for s in list { resume(s) }
        store.say("Resuming \(list.count) interrupted conversation\(list.count == 1 ? "" : "s")")
    }

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
                // An agent that had not answered yet is asked again — a few
                // times, and only while the list is on screen: one whose
                // store always hangs was asked every two seconds for ever,
                // its tab closed or not.
                if partial, asked < 5, store.isShown(key) {
                    asked += 1
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                    load()
                } else if !partial {
                    asked = 0
                }
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
            Text("Sessions").font(.ui(size: 13, weight: .semibold))
            Text(name).font(.ui(size: 12)).foregroundStyle(Theme.text2)
            Text("\(model.sessions.count)").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            if model.loading || model.partial { ProgressView().controlSize(.small) }
            Spacer()
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                TextField("Search titles and conversations", text: $model.query).textFieldStyle(.plain).font(.ui(size: 12))
                    .onChange(of: model.query) { _ in model.queryChanged() }
            }
            .padding(.horizontal, 8).frame(width: 260, height: 26)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line2))
            let owed = resumable(model.sessions).count
            if owed > 1 {
                Button("Resume \(owed) interrupted") { model.resumeAll() }.controlSize(.small)
                    .help("Start every conversation a restart cut off again, each in a pane")
            }
            Button { model.load() } label: { LucideIcon("refresh-cw", size: 12) }.buttonStyle(.plain).help("Read them again")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }
}

struct SessionRow: View {
    /// Read so this is drawn again when the theme changes: its model does
    /// not change with it.
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: SessionsModel
    let session: SessionInfo
    @State private var hovering = false

    var body: some View {
        let _ = look
        let open = !(session.paneID ?? "").isEmpty
        HStack(spacing: 10) {
            Mark(agent: session.agent)
            VStack(alignment: .leading, spacing: 3) {
                Text(session.name).font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text).lineLimit(1)
                HStack(spacing: 8) {
                    Text(agentTitle(session.agent)).foregroundStyle(Theme.text2)
                    if let b = session.branch, !b.isEmpty {
                        HStack(spacing: 4) {
                            LucideIcon("git-branch", color: Theme.muted, size: 10)
                            Text(b).font(.ui(size: 11.5, design: .monospaced))
                        }
                    }
                    Text(agoText(session.updated))
                }
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
                if let sn = session.snippet, !sn.isEmpty {
                    // Where a search found the words inside it.
                    Text(sn).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1).truncationMode(.middle)
                }
            }
            Spacer(minLength: 8)
            if !(session.sessionID ?? "").isEmpty {
                Button { model.star(session) } label: {
                    Image(systemName: session.starred == true ? "star.fill" : "star").font(.ui(size: 11))
                        .foregroundStyle(session.starred == true ? Theme.warn : Theme.muted)
                }
                .buttonStyle(.plain).opacity(session.starred == true || hovering ? 1 : 0)
                .help(session.starred == true ? "Take its star away" : "Star it, to find it again: starred ones come first")
            }
            if session.interrupted == true { Pill(text: "interrupted", fg: Theme.onWarn, bg: Theme.warn) }
            if open { Pill(text: "open", fg: Theme.ok, bg: Theme.ok.opacity(0.16)) }
            Button(open ? "Open" : "Resume") { model.resume(session) }
                .buttonStyle(.plain).font(.ui(size: 12, weight: .medium))
                .foregroundStyle(Theme.selectedText)
                .padding(.horizontal, 10).padding(.vertical, 3)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.accentDeep))
                .help(open ? "Go to the pane it is open in" : "Start \(session.agent) on this conversation again, in a pane")
            Button { model.delete(session) } label: { Image(systemName: "trash").font(.ui(size: 11)) }
                .buttonStyle(.plain).foregroundStyle(Theme.muted).opacity(hovering ? 1 : 0)
                .help("Delete this conversation")
        }
        .padding(.horizontal, 10).frame(height: 48)
        .background(RoundedRectangle(cornerRadius: 7).fill(hovering ? Theme.raise : .clear))
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture(count: 2) { model.resume(session) }
        .contextMenu {
            Button(open ? "Open" : "Resume") { model.resume(session) }
            if !(session.sessionID ?? "").isEmpty {
                Button(session.starred == true ? "Unstar" : "Star") { model.star(session) }
                Menu("Hand Off To") {
                    ForEach(model.store.startable(on: model.machine), id: \.name) { a in
                        Button(a.title) { model.handOff(session, to: a.name) }
                    }
                }
            }
            Divider()
            Button("Delete…") { model.delete(session) }
        }
    }

    func agentTitle(_ name: String) -> String {
        ["claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin"][name] ?? name
    }
}
