import AppKit
import ConchKit
import Foundation

/// The window's state: what the server says (Snapshot), how the tabs are
/// laid out, and the terminal views — kept here so a view moved between
/// splits keeps its terminal and the program in it.
@MainActor
final class Store: ObservableObject {
    @Published var snapshot = Snapshot()
    @Published var layout = Layout()
    @Published var workspaces: [Workspace] = [Workspace(name: "")]
    @Published var workspace = 0
    @Published var questions: [String: Question] = [:]
    @Published var status: String = "Connecting to conch…"
    @Published var connected = false
    @Published var paletteOpen = false
    @Published var sidebarVisible = true
    @Published var serverVersion = ""
    @Published var error: String?

    let binary: String?
    private var conn: Connection?
    private(set) var terminals: [String: PaneTerminal] = [:]
    private var asking: Set<String> = []

    init() {
        binary = conchBinary()
        loadWorkspaces()
        watchClicks()
    }

    /// A click in a pane's terminal gives that pane the keyboard: the split
    /// it is in becomes the focused one.
    private func watchClicks() {
        NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] e in
            MainActor.assumeIsolated {
                guard let self, let win = e.window, let hit = win.contentView?.hitTest(e.locationInWindow) else { return }
                for (pane, t) in self.terminals where hit === t.view || hit.isDescendant(of: t.view) {
                    self.focus(pane)
                }
            }
            return e
        }
    }

    var ws: Workspace? { workspaces.indices.contains(workspace) ? workspaces[workspace] : nil }

    // MARK: connecting

    func connect() {
        Task { await connectNow(start: false) }
    }

    /// Starts conch's server (the way `conch` does) and connects.
    func startServer() {
        guard let b = binary else { return }
        status = "Starting conch's server…"
        Task {
            do { _ = try await runConch(b, ["agent", "status"]) } catch { self.error = "\(error)" }
            await connectNow(start: true)
        }
    }

    private func connectNow(start: Bool) async {
        do {
            let c = try Connection(path: conchSocketPath())
            c.onEvent = { [weak self] name, data in
                MainActor.assumeIsolated { self?.event(name, data) }
            }
            c.onClose = { [weak self] _ in
                MainActor.assumeIsolated {
                    self?.connected = false
                    self?.status = "The connection to conch's server closed."
                }
            }
            try await c.handshake()
            conn = c
            serverVersion = c.hello?.version ?? ""
            let panes = try await c.call("pane.list", as: PaneList.self)
            let projects = try await c.call("project.list", as: ProjectList.self)
            snapshot = Snapshot(panes: panes.panes, projects: projects.projects)
            connected = true
            error = nil
            status = ""
            for p in snapshot.panes where p.agent?.state == .blocked { ask(p.id) }
            if layout.tabs.isEmpty, let first = snapshot.nextAttention(after: nil, in: ws) ?? snapshot.panes.first(where: { $0.running })?.id {
                layout.open(first)
            }
        } catch {
            connected = false
            status = "\(error)"
        }
    }

    private func event(_ name: String, _ data: Data) {
        let dec = conchDecoder()
        switch name {
        case "pane.created", "pane.updated", "pane.exited":
            guard let p = try? dec.decode(PaneInfo.self, from: data) else { return }
            let was = snapshot.pane(p.id)?.agent?.state
            snapshot.upsert(p)
            if p.agent?.state == .blocked {
                if was != .blocked || questions[p.id] == nil { ask(p.id) }
            } else {
                questions.removeValue(forKey: p.id)
            }
        case "pane.closed":
            guard let p = try? dec.decode(PaneInfo.self, from: data) else { return }
            snapshot.remove(pane: p.id)
            layout.close(p.id)
            terminals.removeValue(forKey: p.id)?.stop()
            questions.removeValue(forKey: p.id)
        case "project.updated":
            if let p = try? dec.decode(ProjectInfo.self, from: data) { snapshot.upsert(p) }
        case "project.removed":
            struct Ref: Decodable { var id: String }
            if let r = try? dec.decode(Ref.self, from: data) { snapshot.remove(project: r.id) }
        default:
            break
        }
    }

    // MARK: answering

    /// Reads what a waiting agent asks, for its answer buttons.
    func ask(_ pane: String) {
        guard let b = binary, !asking.contains(pane) else { return }
        asking.insert(pane)
        Task {
            defer { asking.remove(pane) }
            if let q = try? await readQuestion(b, pane: pane) {
                questions[pane] = q
            } else {
                questions.removeValue(forKey: pane)
            }
        }
    }

    func answer(_ pane: String, _ q: Question, _ c: Choice) {
        guard let b = binary else { return }
        questions.removeValue(forKey: pane) // answered: the buttons go at once
        Task {
            do {
                try await ConchKit.answer(b, pane: pane, question: q, choice: c)
            } catch {
                self.error = "\(error)"
                ask(pane)
            }
        }
    }

    // MARK: panes and tabs

    func terminal(for pane: String) -> PaneTerminal {
        if let t = terminals[pane] { return t }
        let t = PaneTerminal(pane: pane, binary: binary, store: self)
        terminals[pane] = t
        return t
    }

    func open(_ pane: String) {
        layout.open(pane)
        focusTerminal()
    }

    func focus(_ pane: String) {
        if layout.focused != pane { layout.focus(pane) }
    }

    func focusTerminal() {
        guard let p = layout.focused else { return }
        DispatchQueue.main.async { self.terminals[p]?.view.window?.makeFirstResponder(self.terminals[p]?.view) }
    }

    /// A new shell where the focused pane is (its folder, so its project),
    /// in a split beside it or a tab of its own.
    func newTerminal(split axis: SplitNode.Axis?) {
        guard let c = conn else { return }
        struct Create: Encodable { var cwd: String? }
        let cwd = layout.focused.flatMap { snapshot.pane($0)?.cwd }
        Task {
            do {
                let p = try await c.call("pane.create", Create(cwd: cwd), as: PaneInfo.self)
                snapshot.upsert(p)
                if let axis { layout.split(axis, with: p.id) } else { layout.open(p.id) }
                focusTerminal()
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// Closes the focused split. The pane keeps running — it is in the
    /// sidebar to open again — since a window closing is not a reason to
    /// stop an agent.
    func closeSplit() {
        guard let p = layout.focused else { return }
        layout.close(p)
        terminals.removeValue(forKey: p)?.stop()
        focusTerminal()
    }

    func nextWaiting() {
        if let p = snapshot.nextAttention(after: layout.focused, in: ws) { open(p) }
    }

    func openBranch(project: String, name: String) {
        // The branch's agent if it has one, else a shell in its worktree.
        if let p = snapshot.panes.first(where: { $0.projectID == project && $0.branch == name && $0.agent != nil }) {
            open(p.id)
            return
        }
        guard let c = conn, let pr = snapshot.project(project),
              let wt = pr.worktrees?.first(where: { $0.branch == name }) else { return }
        struct Create: Encodable { var cwd: String }
        Task {
            if let p = try? await c.call("pane.create", Create(cwd: wt.path), as: PaneInfo.self) {
                snapshot.upsert(p)
                open(p.id)
            }
        }
    }

    func openProject(_ id: String) {
        if let p = snapshot.panes.first(where: { $0.projectID == id && $0.agent != nil }) ?? snapshot.panes.first(where: { $0.projectID == id }) {
            open(p.id)
        }
    }

    func run(_ action: PaletteAction) {
        paletteOpen = false
        switch action {
        case .pane(let p): open(p)
        case .project(let id): openProject(id)
        case .branch(let pr, let name): openBranch(project: pr, name: name)
        case .command(let id): command(id)
        }
    }

    func command(_ id: String) {
        switch id {
        case "newTab": newTerminal(split: nil)
        case "splitRight": newTerminal(split: .horizontal)
        case "splitDown": newTerminal(split: .vertical)
        case "closeSplit": closeSplit()
        case "nextWaiting": nextWaiting()
        case "nextTab": layout.step(1); focusTerminal()
        case "previousTab": layout.step(-1); focusTerminal()
        case "toggleSidebar": sidebarVisible.toggle()
        case "openTUI": openTUI()
        default: break
        }
    }

    /// conch's own interface, for what this window does not have yet —
    /// settings, sessions, the git panel: `conch app`, its own window.
    func openTUI() {
        guard let b = binary else { return }
        Task { _ = try? await runConch(b, ["app"]) }
    }

    // MARK: workspaces

    func loadWorkspaces() {
        let dir = ProcessInfo.processInfo.environment["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 }
            ?? (NSHomeDirectory() as NSString).appendingPathComponent(".config/conch")
        let data = FileManager.default.contents(atPath: (dir as NSString).appendingPathComponent("ui.json"))
        workspaces = Workspace.load(uiJSON: data)
        workspace = min(workspace, workspaces.count - 1)
    }
}
