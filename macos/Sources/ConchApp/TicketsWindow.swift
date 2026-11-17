import AppKit
import Combine
import ConchKit
import SwiftUI

/// The Tickets window (⇧⌘I): every project's GitHub issues and Linear or
/// Jira tickets on this computer, with what conch is doing about each
/// (ticket.overview), a click from its agent, its pull request or a task.
final class TicketsWindow {
    private var window: NSWindow?
    private let store: Store
    private var look: AnyCancellable?
    private var model: TicketsModel?

    init(store: Store) { self.store = store }

    @MainActor func show() {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1240, height: 760),
                             styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = "Tickets"
            w.isReleasedWhenClosed = false
            w.minSize = NSSize(width: 760, height: 420)
            let m = TicketsModel(store: store)
            model = m
            w.contentView = NSHostingView(rootView: TicketsView(model: m, look: store.look))
            look = store.look.$theme.sink { [weak w] t in
                w?.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
                w?.backgroundColor = Theme.ns(t.chrome)
            }
            w.center()
            w.setFrameAutosaveName("conch.tickets")
            window = w
        }
        model?.load()
        window?.makeKeyAndOrderFront(nil)
    }
}

@MainActor
final class TicketsModel: ObservableObject {
    let store: Store
    @Published var overview: TicketOverview?
    @Published var loading = false
    @Published var failure = ""
    @Published var filter: TicketFilter = .all
    @Published var query = ""

    init(store: Store) { self.store = store }

    /// The labels that mean ready for conch: intake's, and ready itself.
    var readyLabels: Set<String> { store.intakeLabels.union(["ready"]) }

    func load() {
        guard !loading else { return }
        guard let c = store.connection("local") else {
            failure = "Not connected to this computer's conch server."
            return
        }
        guard c.hello?.capabilities?.contains("ticket.overview.v1") == true else {
            failure = "This conch server predates the Tickets view: reload it (right-click local → Reload Server)."
            return
        }
        loading = true
        Task { @MainActor in
            defer { loading = false }
            do {
                overview = try await c.call("ticket.overview", [String: String](), as: TicketOverview.self)
                failure = ""
            } catch {
                failure = "\(error)"
            }
        }
    }

    // MARK: actions

    /// New Task in the main window with the ticket picked.
    /// In the main window last in front, not the first window's store:
    /// that window may be closed, or behind the one being looked at.
    func start(_ t: TicketRow, in p: TicketProject) {
        guard let id = p.projectID else { return }
        let s = AppWindows.shared.frontStore ?? store
        s.newTask(project: key("local", id))
        s.taskDraft?.issue = t.issue
        s.taskDraft?.ticket = t.ticket ?? ""
        NSApp.activate(ignoringOtherApps: true)
        AppWindows.shared.bringForward(s)
    }

    func open(_ s: String?) {
        if let s, let u = URL(string: s) { NSWorkspace.shared.open(u) }
    }

    /// The agent's pane in the window that holds it, brought forward.
    func agent(_ pane: String) {
        NSApp.activate(ignoringOtherApps: true)
        (AppWindows.shared.frontStore ?? store).showPaneOf(machine: "local", pane: pane)
    }
}

struct TicketsView: View {
    @ObservedObject var model: TicketsModel
    @ObservedObject var look: ShownTheme
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let _ = look.theme
        HStack(spacing: 0) {
            side.frame(width: 220)
            Divider()
            main
        }
        .background(Theme.window)
    }

    // MARK: the side

    var side: some View {
        let ov = model.overview ?? TicketOverview(projects: [])
        let ready = model.readyLabels
        return ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                heading("Views")
                item("All open", .all, ticketCount(ov, .all, readyLabels: ready))
                item("Mine", .mine, ticketCount(ov, .mine, readyLabels: ready))
                item("Ready for conch", .ready, ticketCount(ov, .ready, readyLabels: ready))
                item("Needs info", .needsInfo, ticketCount(ov, .needsInfo, readyLabels: ready), warn: true)
                item("conch is on it", .onIt, ticketCount(ov, .onIt, readyLabels: ready))
                if !ov.projects.isEmpty {
                    heading("Projects")
                    ForEach(ov.projects) { p in
                        item(p.name, .project(p.id), p.tickets.count, note: (p.errors ?? []).isEmpty ? nil : "!")
                    }
                }
                let sources = Array(Set(ov.projects.flatMap { $0.tickets.map(\.source) })).sorted()
                if sources.count > 1 {
                    heading("Trackers")
                    ForEach(sources, id: \.self) { s in item(sourceName(s), .source(s), ticketCount(ov, .source(s), readyLabels: ready)) }
                }
            }
            .padding(8)
        }
        .background(Theme.side)
    }

    func heading(_ s: String) -> some View {
        Text(s.uppercased()).font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted)
            .padding(.horizontal, 9).padding(.top, 10).padding(.bottom, 3)
    }

    func item(_ title: String, _ f: TicketFilter, _ n: Int, warn: Bool = false, note: String? = nil) -> some View {
        let on = model.filter == f
        return Button { model.filter = f } label: {
            HStack {
                Text(title).lineLimit(1)
                Spacer()
                Text(note ?? "\(n)").foregroundStyle(warn && n > 0 || note != nil ? Theme.warn : Theme.muted)
                    .font(.ui(size: 11, weight: warn && n > 0 ? .bold : .regular))
            }
            .font(.ui(size: 12.5, weight: on ? .semibold : .regular))
            .foregroundStyle(on ? Theme.text : Theme.text2)
            .padding(.horizontal, 9).padding(.vertical, 5)
            .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accent.opacity(0.14) : .clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    // MARK: the list

    var main: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                TextField("Search tickets", text: $model.query).textFieldStyle(.roundedBorder).frame(maxWidth: 280)
                Spacer()
                if model.loading { ProgressView().controlSize(.small) }
                Button("Refresh") { model.load() }.disabled(model.loading)
            }
            if !model.failure.isEmpty {
                Text(model.failure).font(.ui(size: 12)).foregroundStyle(Theme.warn).textSelection(.enabled)
            }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    let shown = filterTickets(model.overview ?? TicketOverview(projects: []), model.filter, query: model.query, readyLabels: model.readyLabels)
                    if shown.isEmpty && !model.loading && model.overview != nil {
                        Text(model.query.isEmpty ? "No tickets here." : "No ticket matches.").foregroundStyle(Theme.muted).padding(20)
                    }
                    ForEach(shown) { p in group(p) }
                }
                .padding(.bottom, 16)
            }
        }
        .padding(14)
    }

    func group(_ p: TicketProject) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text(p.name).font(.ui(size: 13, weight: .semibold))
                Text((p.sources ?? []).map(sourceName).joined(separator: ", ")).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                Spacer()
                Text("\(p.tickets.count) open").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(Theme.raise)
            ForEach(p.errors ?? [], id: \.self) { e in
                Text(e).font(.ui(size: 11.5)).foregroundStyle(Theme.err).padding(.horizontal, 12).padding(.vertical, 6)
                    .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
            }
            ForEach(p.tickets) { t in
                Divider()
                row(t, in: p)
            }
        }
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
        .clipShape(RoundedRectangle(cornerRadius: 9))
    }

    func row(_ t: TicketRow, in p: TicketProject) -> some View {
        let doing = ticketDoingText(t.doing)
        return HStack(spacing: 10) {
            Text(t.shown).font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                .foregroundStyle(sourceColor(t.source)).frame(width: 74, alignment: .leading)
            VStack(alignment: .leading, spacing: 1) {
                Text(t.title).font(.ui(size: 12.5, weight: .medium)).lineLimit(1)
                Text(meta(t)).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 4) {
                ForEach((t.labels ?? []).prefix(3), id: \.self) { l in label(l) }
            }
            .frame(width: 150, alignment: .leading)
            HStack(spacing: 6) {
                if !doing.text.isEmpty { Circle().fill(toneColor(doing.tone)).frame(width: 7, height: 7) }
                Text(doing.text).font(.ui(size: 11.5, weight: doing.tone == .none ? .regular : .semibold))
                    .foregroundStyle(doing.tone == .none ? Theme.muted : toneColor(doing.tone)).lineLimit(1)
            }
            .frame(width: 210, alignment: .leading)
            actions(t, in: p).frame(width: 140, alignment: .trailing)
        }
        .padding(.horizontal, 12).padding(.vertical, 7)
        .contentShape(Rectangle())
        .contextMenu {
            Button("Open in \(sourceName(t.source))") { model.open(t.url) }
            if p.projectID != nil { Button("New Task from It…") { model.start(t, in: p) } }
            if let pane = t.doing?.pane { Button("Show Its Agent") { model.agent(pane) } }
            if let u = t.doing?.pr?.url { Button("Open Pull Request") { model.open(u) } }
        }
    }

    @ViewBuilder func actions(_ t: TicketRow, in p: TicketProject) -> some View {
        HStack(spacing: 5) {
            if let pane = t.doing?.pane, ["waiting", "working", "idle"].contains(t.doing?.state ?? "") {
                Button(t.doing?.state == "waiting" ? "Answer" : "Agent") { model.agent(pane) }
            } else if let u = t.doing?.pr?.url {
                Button("PR") { model.open(u) }
            } else if p.projectID != nil, (t.doing?.state ?? "").isEmpty {
                Button("Task") { model.start(t, in: p) }
            }
            Button("Open") { model.open(t.url) }
        }
        .controlSize(.small)
    }

    func meta(_ t: TicketRow) -> String {
        var parts = [sourceName(t.source)]
        if let s = t.status, !s.isEmpty, s != "open" { parts.append(s) }
        if t.mine == true { parts.append("yours") }
        if let b = t.doing?.branch, !b.isEmpty { parts.append(b) }
        return parts.joined(separator: " · ")
    }

    func label(_ l: String) -> some View {
        let c: Color = l == "needs-info" ? Theme.warn : l == "too-big" ? Theme.err : model.readyLabels.contains(l) ? Theme.ok : Theme.muted
        return Text(l).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(c)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .background(Capsule().fill(c.opacity(0.13))).lineLimit(1)
    }

    func toneColor(_ t: DoingTone) -> Color {
        switch t {
        case .work: return Theme.work
        case .wait: return Theme.warn
        case .pr, .done: return Theme.ok
        case .queued: return Theme.accent
        case .none: return Theme.muted
        }
    }

    func sourceName(_ s: String) -> String {
        if s.hasPrefix("github") { return "GitHub" }
        if s.hasPrefix("linear") { return "Linear" + s.dropFirst(6) }
        if s.hasPrefix("jira") { return "Jira" + s.dropFirst(4) }
        return s
    }

    func sourceColor(_ s: String) -> Color {
        switch s {
        case "linear": return Color(red: 0.37, green: 0.42, blue: 0.82)
        case "jira": return Color(red: 0.15, green: 0.52, blue: 1.0)
        default: return Theme.text
        }
    }
}
