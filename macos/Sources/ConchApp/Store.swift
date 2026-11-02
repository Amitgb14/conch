import AppKit
import ConchKit
import Foundation

/// A machine the window shows: this computer, or one from machines.json
/// reached through `conch machine bridge`.
struct MachineLink: Identifiable, Equatable {
    var id: String
    var label: String
    /// "" when online; otherwise what it is doing or why it is not.
    var state = "connecting"
    /// Why it is offline, in the bridge's or the socket's own words.
    var why = ""
    /// A hosted sandbox: stopped is a state of its own, not a fault.
    var sandbox = false
    var version = ""

    var online: Bool { state.isEmpty }
}

/// The window's state: what each machine's server says (a Snapshot each),
/// how the tabs are laid out, and the terminal views — kept here so a view
/// moved between splits keeps its terminal and the program in it.
///
/// Panes, projects and branches are named "machine|id" throughout
/// (ConchKit.key): this computer's p3 is not busybox's p3.
@MainActor
final class Store: ObservableObject {
    @Published var machines: [MachineLink] = [MachineLink(id: "local", label: "local")]
    @Published var snapshots: [String: Snapshot] = [:]
    @Published var layout = Layout()
    @Published var workspaces: [Workspace] = [Workspace(name: "")]
    @Published var workspace = 0
    @Published var questions: [String: Question] = [:]
    @Published var sessions: [String: Int] = [:]
    /// The TUI's folds (ui.json), and the ones chosen here, which are kept
    /// apart: ui.json is the running TUI's to write.
    @Published var tuiFolds: [String: Bool] = [:]
    @Published var folds: [String: Bool] = (UserDefaults.standard.dictionary(forKey: "folds") as? [String: Bool]) ?? [:]
    @Published var limits: [PlanLimits] = []
    @Published var status: String = "Connecting to conch…"
    @Published var paletteOpen = false
    @Published var sidebarVisible = true
    @Published var error: String?
    /// The theme chosen in the app ("" follows conch's own [ui] theme),
    /// and the one in effect.
    @Published private(set) var themeChoice = UserDefaults.standard.string(forKey: "theme") ?? ""
    @Published private(set) var theme = ConchTheme.conch
    @Published private(set) var icons = IconMode.text
    /// Told when the theme changes, for what SwiftUI does not draw: the
    /// window's own background and appearance.
    var onTheme: ((ConchTheme) -> Void)?

    let binary: String?
    private var conns: [String: Connection] = [:]
    private(set) var terminals: [String: PaneTerminal] = [:]
    private(set) var changeViews: [String: ChangesModel] = [:]
    private var asking: Set<String> = []

    init() {
        binary = conchBinary()
        loadWorkspaces()
        loadTheme()
        watchClicks()
    }

    // MARK: what the window shows

    var ws: Workspace? { workspaces.indices.contains(workspace) ? workspaces[workspace] : nil }

    /// This computer's server answers: the window has something to show.
    var connected: Bool { machines.first?.online == true }
    var serverVersion: String { machines.first?.version ?? "" }

    func connection(_ machine: String) -> Connection? { conns[machine] }

    /// "api · local": where a pane is, for the end of the tab bar.
    func scope(of k: String) -> String {
        if let c = parseChangesKey(k) {
            return [snapshots[c.machine]?.project(c.project)?.name ?? "", label(c.machine)].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        let (m, _) = unkey(k)
        let project = pane(k)?.projectID.flatMap { snapshots[m]?.project($0)?.name }
        return [project ?? "CLI", label(m)].joined(separator: " · ")
    }

    /// Whether a pane's terminal has the keyboard, for the status bar's
    /// chip; set as the window's first responder changes.
    @Published var terminalHasKeyboard = false

    func pane(_ k: String) -> PaneInfo? {
        let (m, id) = unkey(k)
        return snapshots[m]?.pane(id)
    }

    func label(_ machine: String) -> String { machines.first { $0.id == machine }?.label ?? machine }

    func isOpen(_ node: TreeNode) -> Bool { ConchKit.isOpen(node, app: folds, tui: tuiFolds) }

    func toggle(_ node: TreeNode) {
        guard let f = node.fold else { return }
        folds[f] = !isOpen(node)
        UserDefaults.standard.set(folds, forKey: "folds")
    }

    /// The sidebar: a tree per machine, this computer first.
    var tree: [TreeNode] {
        machines.map { m in
            var node = (snapshots[m.id] ?? Snapshot()).tree(machine: m.id, label: m.label, in: ws, sessions: sessions)
            if !m.online { node.badge = m.state }
            return node
        }
    }

    /// Waiting and done, across every machine.
    var inbox: (waiting: Int, done: Int) {
        machines.reduce((0, 0)) { acc, m in
            let (w, d) = (snapshots[m.id] ?? Snapshot()).inbox(in: ws)
            return (acc.0 + w, acc.1 + d)
        }
    }

    var paletteItems: [PaletteItem] {
        var w: [PaletteItem] = [], p: [PaletteItem] = [], pl: [PaletteItem] = []
        let many = machines.count > 1
        for m in machines {
            let (a, b, c) = (snapshots[m.id] ?? Snapshot()).paletteItems(machine: m.id, label: many ? m.label : nil, in: ws)
            w += a
            p += b
            pl += c
        }
        return w + p + pl + paletteCommandItems()
    }

    // MARK: theme

    /// The theme in effect: the one chosen here, else conch's own setting
    /// in config.toml, so the window matches the TUI until told otherwise.
    func loadTheme() {
        let toml = try? String(contentsOfFile: configFile, encoding: .utf8)
        // How agents are marked follows the TUI's setting, read with the theme.
        let icons = IconMode(setting: uiSetting("icons", in: toml))
        if icons != self.icons {
            self.icons = icons
            Theme.icons = icons
        }
        let configured = ConchTheme.configured(in: toml)
        let next = ConchTheme.named(themeChoice.isEmpty ? configured : themeChoice)
        guard next != theme || Theme.current != next else { return }
        theme = next
        Theme.current = next
        for t in terminals.values { t.apply(next) }
        #if GHOSTTY
        GhosttyRuntime.shared.apply(next)
        #endif
        onTheme?(next)
    }

    func chooseTheme(_ id: String) {
        themeChoice = id
        UserDefaults.standard.set(id, forKey: "theme")
        loadTheme()
    }

    var conchTheme: ConchTheme {
        ConchTheme.named(ConchTheme.configured(in: try? String(contentsOfFile: configFile, encoding: .utf8)))
    }

    private var conchDir: String {
        ProcessInfo.processInfo.environment["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 }
            ?? (NSHomeDirectory() as NSString).appendingPathComponent(".config/conch")
    }

    private var configFile: String { (conchDir as NSString).appendingPathComponent("config.toml") }

    /// A click in a pane's terminal gives that pane the keyboard: the split
    /// it is in becomes the focused one.
    private func watchClicks() {
        NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] e in
            MainActor.assumeIsolated {
                guard let self, let win = e.window, let hit = win.contentView?.hitTest(e.locationInWindow) else { return }
                var inTerminal = false
                for (pane, t) in self.terminals where hit === t.view || hit.isDescendant(of: t.view) {
                    self.focus(pane)
                    inTerminal = true
                }
                self.terminalHasKeyboard = inTerminal
            }
            return e
        }
    }

    // MARK: connecting

    /// This computer, and every enabled machine in machines.json.
    func connect() {
        let entries = MachineEntry.load(machinesJSON: FileManager.default.contents(atPath: (conchDir as NSString).appendingPathComponent("machines.json")))
        var next = [machines.first { $0.id == "local" } ?? MachineLink(id: "local", label: "local")]
        for e in entries {
            var link = machines.first { $0.id == e.id } ?? MachineLink(id: e.id, label: e.displayLabel)
            link.sandbox = e.sandboxProvider != nil
            next.append(link)
        }
        machines = next
        for m in machines where conns[m.id] == nil { Task { await connect(m.id) } }
    }

    /// Starts conch's server (the way `conch` does) and connects.
    func startServer() {
        guard let b = binary else { return }
        status = "Starting conch's server…"
        Task {
            do { _ = try await runConch(b, ["agent", "status"]) } catch { self.error = "\(error)" }
            await connect("local")
        }
    }

    private func setState(_ machine: String, _ state: String, version: String? = nil) {
        guard let i = machines.firstIndex(where: { $0.id == machine }) else { return }
        machines[i].state = state
        if let version { machines[i].version = version }
    }

    private func connect(_ machine: String) async {
        setState(machine, "connecting")
        do {
            let c: Connection
            if machine == "local" {
                c = try Connection(path: conchSocketPath())
            } else {
                guard let b = binary else { throw ConchError(code: "conch", message: "no conch to reach it with") }
                c = try Connection(command: b, args: ["machine", "bridge", machine], environment: conchEnvironment())
            }
            c.onEvent = { [weak self] name, data in
                MainActor.assumeIsolated { self?.event(machine, name, data) }
            }
            c.onClose = { [weak self, weak c] _ in
                MainActor.assumeIsolated { self?.closed(machine, why: c?.bridgeError ?? "") }
            }
            conns[machine] = c
            try await c.handshake()
            let panes = try await c.call("pane.list", as: PaneList.self)
            let projects = try await c.call("project.list", as: ProjectList.self)
            snapshots[machine] = Snapshot(panes: panes.panes, projects: projects.projects)
            setState(machine, "", version: c.hello?.version ?? "")
            if machine == "local" {
                error = nil
                status = ""
                if let l = try? await c.call("agent.limits", as: AgentLimitsResult.self) { limits = l.limits ?? [] }
            }
            for p in panes.panes where p.agent?.state == .blocked { ask(key(machine, p.id)) }
            countSessions(machine)
            // Nothing is opened, and nothing given the keyboard, until a
            // pane is picked: a window that opened on an agent with its
            // terminal focused sent the first key anyone pressed — an Enter
            // — to that agent, which took it as accepting its suggestion.
            // Asked for by name at launch (`-open p3`, `-open busybox|p3`),
            // a pane is shown — still without the keyboard.
            if let o = UserDefaults.standard.string(forKey: "open") {
                if let c = parseChangesKey(o) {
                    if c.machine == machine { layout.open(o) } // a branch's changes
                } else {
                    let k = o.contains("|") ? o : key("local", o)
                    if unkey(k).machine == machine, pane(k) != nil { layout.open(k) }
                }
            }
        } catch {
            conns[machine]?.shutdown()
            conns[machine] = nil
            closed(machine, why: "\(error)")
        }
    }

    /// A machine's connection went: said on its row, and tried again in a
    /// while for one elsewhere (this computer's has a button).
    private func closed(_ machine: String, why: String) {
        guard conns[machine] != nil || machines.first(where: { $0.id == machine })?.state == "connecting" else { return }
        conns[machine] = nil
        let reason = why.split(separator: "\n").last.map(String.init) ?? ""
        // A sandbox that is not running says so ("sandbox x is stopped"):
        // its state, said as the mock says it, rather than offline. The app
        // never starts one; `conch sandbox start` does.
        let link = machines.first { $0.id == machine }
        var state = "offline"
        if link?.sandbox == true, let r = reason.range(of: #"sandbox .* is (\w+)"#, options: .regularExpression) {
            state = String(reason[r].split(separator: " ").last ?? "stopped")
        }
        setState(machine, state)
        if let i = machines.firstIndex(where: { $0.id == machine }) { machines[i].why = reason }
        if machine == "local" {
            status = reason.isEmpty ? "The connection to conch's server closed." : reason
        } else {
            // A stopped sandbox is looked at less often: asking costs a call
            // to its provider, and it will not start by itself.
            DispatchQueue.main.asyncAfter(deadline: .now() + (state == "offline" ? 30 : 300)) { [weak self] in
                guard let self, self.conns[machine] == nil, self.machines.contains(where: { $0.id == machine }) else { return }
                Task { await self.connect(machine) }
            }
        }
    }

    private func event(_ machine: String, _ name: String, _ data: Data) {
        let dec = conchDecoder()
        var snap = snapshots[machine] ?? Snapshot()
        switch name {
        case "pane.created", "pane.updated", "pane.exited":
            guard let p = try? dec.decode(PaneInfo.self, from: data) else { return }
            let k = key(machine, p.id)
            let was = snap.pane(p.id)?.agent?.state
            snap.upsert(p)
            if p.agent?.state == .blocked {
                if was != .blocked || questions[k] == nil { ask(k) }
            } else {
                questions.removeValue(forKey: k)
            }
        case "pane.closed":
            guard let p = try? dec.decode(PaneInfo.self, from: data) else { return }
            let k = key(machine, p.id)
            snap.remove(pane: p.id)
            layout.close(k)
            terminals.removeValue(forKey: k)?.stop()
            questions.removeValue(forKey: k)
        case "project.updated":
            if let p = try? dec.decode(ProjectInfo.self, from: data) {
                snap.upsert(p)
                // Git moved (a commit, a push): open changes read again.
                for v in changeViews.values where v.machine == machine && v.project == p.id { Task { await v.load() } }
            }
        case "project.removed":
            struct Ref: Decodable { var id: String }
            if let r = try? dec.decode(Ref.self, from: data) { snap.remove(project: r.id) }
        case "worktree.changed":
            struct Changed: Decodable {
                var project_id: String
                var worktree: String
                var paths: [String]?
            }
            if let ch = try? dec.decode(Changed.self, from: data) {
                for v in changeViews.values where v.machine == machine && v.project == ch.project_id {
                    v.written(ch.paths ?? [], worktree: ch.worktree)
                }
            }
            return
        case "agent.limits":
            if machine == "local", let l = try? dec.decode(PlanLimits.self, from: data) {
                limits.removeAll { $0.agent == l.agent }
                limits.append(l)
            }
            return
        default:
            return
        }
        snapshots[machine] = snap
    }

    /// How many saved conversations each of a machine's projects has, for
    /// the Sessions row.
    private func countSessions(_ machine: String) {
        guard let c = conns[machine] else { return }
        struct List: Encodable {
            var project_id: String
            var limit: Int
        }
        for pr in snapshots[machine]?.projects ?? [] {
            Task {
                if let n = try? await c.call("session.list", List(project_id: pr.id, limit: 500), as: SessionCount.self) {
                    sessions[key(machine, pr.id)] = n.count
                }
            }
        }
    }

    // MARK: answering

    /// Reads what a waiting agent asks, for its answer buttons.
    func ask(_ k: String) {
        guard let b = binary, !asking.contains(k) else { return }
        asking.insert(k)
        let (m, id) = unkey(k)
        Task {
            defer { asking.remove(k) }
            if let q = try? await readQuestion(b, pane: id, machine: m) {
                questions[k] = q
            } else {
                questions.removeValue(forKey: k)
            }
        }
    }

    func answer(_ k: String, _ q: Question, _ c: Choice) {
        guard let b = binary else { return }
        questions.removeValue(forKey: k) // answered: the buttons go at once
        let (m, id) = unkey(k)
        Task {
            do {
                try await ConchKit.answer(b, pane: id, machine: m, question: q, choice: c)
            } catch {
                self.error = "\(error)"
                ask(k)
            }
        }
    }

    // MARK: panes and tabs

    func terminal(for k: String) -> PaneTerminal {
        if let t = terminals[k] { return t }
        let t = PaneTerminal(key: k, binary: binary, store: self)
        terminals[k] = t
        return t
    }

    func open(_ k: String) {
        layout.open(k)
        focusTerminal()
    }

    func changes(for k: String) -> ChangesModel {
        if let m = changeViews[k] { return m }
        let m = ChangesModel(key: k, store: self)
        changeViews[k] = m
        return m
    }

    /// A branch's changes, in a tab of their own — what a click on a branch
    /// opens, as in the TUI.
    func openChanges(project pk: String, branch: String) {
        let (m, pid) = unkey(pk)
        layout.open(changesKey(m, pid, branch))
        terminalHasKeyboard = false
    }

    func focus(_ k: String) {
        if layout.focused != k { layout.focus(k) }
    }

    func focusTerminal() {
        guard let p = layout.focused else { return }
        DispatchQueue.main.async {
            if let v = self.terminals[p]?.view, v.window?.makeFirstResponder(v) == true { self.terminalHasKeyboard = true }
        }
    }

    /// A new shell where the focused pane is — its machine and its folder,
    /// so its project — in a split beside it or a tab of its own.
    func newTerminal(split axis: SplitNode.Axis?) {
        let machine = layout.focused.map { parseChangesKey($0)?.machine ?? unkey($0).machine } ?? "local"
        guard let c = conns[machine] else { return }
        struct Create: Encodable { var cwd: String? }
        let cwd = layout.focused.flatMap { k in
            pane(k)?.cwd ?? parseChangesKey(k).flatMap { c in changeViews[k]?.changes?.worktree ?? snapshots[c.machine]?.project(c.project)?.path }
        }
        Task {
            do {
                let p = try await c.call("pane.create", Create(cwd: cwd), as: PaneInfo.self)
                snapshots[machine, default: Snapshot()].upsert(p)
                let k = key(machine, p.id)
                if let axis { layout.split(axis, with: k) } else { layout.open(k) }
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
        changeViews.removeValue(forKey: p)
        focusTerminal()
    }

    /// The next agent waiting for you, on any machine: waiting ones first,
    /// then the ones that finished, each machine in the sidebar's order.
    func nextWaiting() {
        var order: [String] = []
        for m in machines {
            var seen: Set<String> = []
            var next = snapshots[m.id]?.nextAttention(after: nil, in: ws)
            while let n = next, !seen.contains(n) {
                seen.insert(n)
                order.append(key(m.id, n))
                next = snapshots[m.id]?.nextAttention(after: n, in: ws)
            }
        }
        guard !order.isEmpty else { return }
        let i = layout.focused.flatMap { order.firstIndex(of: $0) }.map { ($0 + 1) % order.count } ?? 0
        open(order[i])
    }

    /// Worktrees a terminal is being started in, so a second click while
    /// the first is on its way does not start another.
    private var starting: Set<String> = []

    func openBranch(project pk: String, name: String) {
        let (m, pid) = unkey(pk)
        guard let snap = snapshots[m] else { return }
        // Its agent, or a terminal already in its worktree; only failing
        // both, a new terminal there — for a branch that has a worktree.
        if let p = snap.paneFor(branch: name, project: pid) {
            open(key(m, p))
            return
        }
        guard let c = conns[m], let pr = snap.project(pid),
              let wt = pr.worktrees?.first(where: { $0.branch == name }), !starting.contains(key(m, wt.path)) else { return }
        struct Create: Encodable { var cwd: String }
        starting.insert(key(m, wt.path))
        Task {
            defer { starting.remove(key(m, wt.path)) }
            if let p = try? await c.call("pane.create", Create(cwd: wt.path), as: PaneInfo.self) {
                snapshots[m, default: Snapshot()].upsert(p)
                open(key(m, p.id))
            }
        }
    }

    func openProject(_ pk: String) {
        let (m, pid) = unkey(pk)
        guard let snap = snapshots[m] else { return }
        if let p = snap.panes.first(where: { $0.projectID == pid && $0.agent != nil }) ?? snap.panes.first(where: { $0.projectID == pid }) {
            open(key(m, p.id))
        }
    }

    func run(_ action: PaletteAction) {
        paletteOpen = false
        switch action {
        case .pane(let p): open(p)
        case .project(let id): openProject(id)
        case .branch(let pr, let name): openChanges(project: pr, branch: name)
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
        let data = FileManager.default.contents(atPath: (conchDir as NSString).appendingPathComponent("ui.json"))
        workspaces = Workspace.load(uiJSON: data)
        tuiFolds = loadFolds(uiJSON: data)
        workspace = min(workspace, workspaces.count - 1)
    }
}
