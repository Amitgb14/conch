import AppKit
import ConchKit
import Foundation
import UniformTypeIdentifiers

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
    /// The agents each machine has installed, for starting one.
    @Published var agents: [String: [AgentAvailability]] = [:]
    /// The task being written, while its sheet is up.
    @Published var taskDraft: TaskDraft?
    /// The machines whose TUI tabs this launch has taken in.
    private var viewAdopted: Set<String> = []
    /// Files the palette's search found, for the query they were found for.
    @Published private(set) var fileHits: [PaletteItem] = []
    private var fileQuery = ""
    private var fileSearch: Task<Void, Never>?
    @Published var layout = Layout()
    @Published var workspaces: [Workspace] = [Workspace(name: "")]
    /// The workspace on screen. Each has tabs of its own, as in the TUI:
    /// switching puts this one's away and brings that one's back.
    @Published var workspace = 0 {
        didSet { swapTabs() }
    }
    /// Each workspace's tabs while another is on screen, by its id.
    private var parkedTabs: [String: Layout] = [:]
    /// The workspace whose tabs `layout` holds.
    private var tabsOwner = "first"
    /// The workspace the TUI was last on (ui.json's active_space): where
    /// the tabs it shows are put.
    private var tuiWorkspace = "first"
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
    /// How the chrome is drawn (View → Style), the app's own choice.
    @Published private(set) var style = AppStyle(setting: UserDefaults.standard.string(forKey: "style"))
    /// A theme shown while the pointer is on its card in Settings, and the
    /// theme on screen, apart from everything else the store publishes, for
    /// the Settings window to repaint by.
    private(set) var previewing: String?
    let look = ShownTheme()
    /// Told when the theme changes, for what SwiftUI does not draw: the
    /// window's own background and appearance.
    var onTheme: ((ConchTheme) -> Void)?

    let binary: String?
    private var conns: [String: Connection] = [:]
    private(set) var terminals: [String: PaneTerminal] = [:]
    private(set) var changeViews: [String: ChangesModel] = [:]
    private(set) var sessionViews: [String: SessionsModel] = [:]
    private(set) var commitViews: [String: CommitsModel] = [:]
    private var asking: Set<String> = []

    init() {
        binary = conchBinary()
        loadWorkspaces()
        Theme.style = style
        loadTheme()
        watchClicks()
        watchConfig()
        watchTreeKeys()
        watchAppearance()
    }

    /// Whether macOS is in dark appearance. Read from the system's own
    /// setting, not NSApp's appearance, which the app sets to its theme.
    var systemDark: Bool {
        (CFPreferencesCopyAppValue("AppleInterfaceStyle" as CFString, kCFPreferencesAnyApplication) as? String) == "Dark"
    }

    /// macOS switching between light and dark (by hand, or by the time of
    /// day) switches the theme when config.toml sets a light one.
    private func watchAppearance() {
        DistributedNotificationCenter.default().addObserver(
            forName: Notification.Name("AppleInterfaceThemeChangedNotification"), object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.loadTheme()
                self?.onConfigChange?() // "Same as conch (…)" names the theme now in effect
            }
        }
    }

    /// config.toml as last read, by its modification time and size.
    private var configStamp = ""
    private var machinesStamp = ""
    private var machinesFile: String { (conchDir as NSString).appendingPathComponent("machines.json") }
    private var configTimer: Timer?

    /// Reads conch's settings again within a second of their changing — a
    /// theme chosen on the settings page, or in the TUI, shows here at
    /// once, not only when the window next comes forward.
    private func watchConfig() {
        configStamp = fileStamp(configFile)
        machinesStamp = fileStamp(machinesFile)
        configTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                // A machine added (conch machine add, the TUI's M) is
                // reached as soon as machines.json has it.
                let mnow = fileStamp(self.machinesFile)
                if mnow != self.machinesStamp {
                    self.machinesStamp = mnow
                    self.connect()
                }
                let now = fileStamp(self.configFile)
                guard now != self.configStamp else { return }
                self.configStamp = now
                self.loadTheme()
                self.onConfigChange?()
            }
        }
    }

    /// Told when conch's settings change, for the theme menu's check mark.
    var onConfigChange: (() -> Void)?

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
        if let c = parseSessionsKey(k) ?? parseCommitsKey(k) {
            return [snapshots[c.machine]?.project(c.project)?.name ?? "", label(c.machine)].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        let (m, _) = unkey(k)
        let project = pane(k)?.projectID.flatMap { snapshots[m]?.project($0)?.name }
        return [project ?? "CLI", label(m)].joined(separator: " · ")
    }

    /// Whether a pane's terminal has the keyboard, for the status bar's
    /// chip; set as the window's first responder changes.
    @Published var terminalHasKeyboard = false
    /// The sidebar row the keyboard is on while no terminal has it.
    @Published var treeCursor: String?

    /// The port of the checkout a pane is in (CONCH_PORT), if it has one.
    func port(of k: String) -> Int? {
        let (m, _) = unkey(k)
        guard let p = pane(k) else { return nil }
        return snapshots[m]?.port(of: p)
    }

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

    /// Every agent waiting for you, across the machines, longest first:
    /// the sidebar's Needs you.
    var needsYou: [(key: String, pane: PaneInfo, machine: String)] {
        machines.flatMap { m in
            (snapshots[m.id] ?? Snapshot()).needsYou(in: ws, machine: m.id).map { (key(m.id, $0.id), $0, m.id) }
        }
        .sorted { ($0.pane.agent?.since ?? .distantPast) < ($1.pane.agent?.since ?? .distantPast) }
    }

    /// Waiting and done, across every machine.
    var inbox: (waiting: Int, done: Int) {
        machines.reduce((0, 0)) { acc, m in
            let (w, d) = (snapshots[m.id] ?? Snapshot()).inbox(in: ws, machine: m.id)
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

    // MARK: the status bar's own: limits, settings, what conch uses

    /// Asks again what each agent's plan has used, as the limits popover
    /// opens: a report from minutes ago is not what anyone opened it for.
    func refreshLimits() {
        guard let c = conns["local"] else { return }
        Task {
            if let l = try? await c.call("agent.limits", as: AgentLimitsResult.self) { limits = l.limits ?? [] }
        }
    }

    /// What the resource monitor shows, while it is open.
    @Published private(set) var resources: (rows: [ResourceRow], total: ResourceRow)?
    private var monitor: Task<Void, Never>?

    /// Samples ps every two seconds for as long as the monitor is open,
    /// as the TUI's does, and not otherwise.
    func startMonitor() {
        monitor?.cancel()
        monitor = Task { [weak self] in
            var prev: ProcSample?
            while !Task.isCancelled {
                let out = (try? await runProgram("/bin/ps", psArguments)) ?? ""
                let cur = ProcSample(at: Date(), procs: parsePS(out))
                guard let self else { return }
                self.resources = resourceRows(prev: prev, cur: cur, app: ("This window (Conch.app)", Int(ProcessInfo.processInfo.processIdentifier)),
                                              server: self.conns["local"]?.hello?.pid,
                                              panes: (self.snapshots["local"]?.panes ?? []).compactMap { p in p.pid.map { (p.displayName, $0) } })
                prev = cur
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    func stopMonitor() {
        monitor?.cancel()
        monitor = nil
        resources = nil
    }

    /// conch's settings page (`conch config web`), started once and opened
    /// again on each press. A TUI that is open takes in what it saves.
    private var settingsPage: Process?
    private var settingsURL: URL?

    /// Opens conch's Settings panel (SettingsWindow.swift).
    var onOpenSettings: (() -> Void)?

    func openSettings() { onOpenSettings?() }

    /// config.toml in the text editor, for the tables the panel leaves to
    /// it. The file is made first if it is not there yet, since an editor
    /// asked to open a missing file opens nothing.
    func editConfigFile() {
        let fm = FileManager.default
        if !fm.fileExists(atPath: configFile) {
            try? fm.createDirectory(atPath: conchDir, withIntermediateDirectories: true)
            fm.createFile(atPath: configFile, contents: Data())
        }
        let file = URL(fileURLWithPath: configFile)
        if let editor = NSWorkspace.shared.urlForApplication(toOpen: .plainText) {
            NSWorkspace.shared.open([file], withApplicationAt: editor, configuration: NSWorkspace.OpenConfiguration())
        } else {
            NSWorkspace.shared.open(file)
        }
    }

    /// The address of conch's settings API (`conch config web -no-open`),
    /// started once and kept while the app runs; nil when it would not
    /// start. The panel reads and saves through it; nothing opens a browser.
    func settingsPage() async -> URL? {
        if let p = settingsPage, p.isRunning, let u = settingsURL { return u }
        guard let b = binary else { return nil }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: b)
        p.arguments = ["config", "web", "-no-open"]
        p.environment = conchEnvironment()
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch {
            self.error = "Could not start conch's settings: \(error)"
            return nil
        }
        settingsPage = p
        // Its first line names the page: "conch settings: http://127.0.0.1:…#t=token".
        let h = out.fileHandleForReading
        let line = await Task.detached { () -> String in
            var buf = Data()
            while !buf.contains(0x0a) {
                let d = h.availableData
                if d.isEmpty { break }
                buf.append(d)
            }
            return String(data: buf, encoding: .utf8)?.split(separator: "\n").first.map(String.init) ?? ""
        }.value
        guard let u = settingsPageURL(line) else {
            error = "conch's settings page did not start"
            return nil
        }
        settingsURL = u
        return u
    }

    /// Ends what the window started that would outlive it.
    func shutdown() {
        settingsPage?.terminate()
        monitor?.cancel()
    }

    // MARK: files

    /// Whether no machine here can search files: their servers predate
    /// fs.find, and the palette says so rather than finding none.
    var filesUnsearchable: Bool {
        let online = machines.filter(\.online)
        return !online.isEmpty && !online.contains { conns[$0.id]?.hello?.capabilities?.contains("fs.find.v1") == true }
    }

    /// Looks for files matching the palette's query in every project the
    /// workspace shows, on each machine whose server can (fs.find.v1): a
    /// few from each, a moment after the typing stops. A newer query
    /// replaces an older one's answers rather than mixing with them.
    func searchFiles(_ query: String) {
        let q = query.trimmingCharacters(in: .whitespaces)
        fileQuery = q
        fileSearch?.cancel()
        guard q.count >= 2 else {
            fileHits = []
            return
        }
        let many = machines.count > 1
        var asks: [(String, String?, ProjectInfo, Connection)] = []
        for m in machines where m.online {
            guard let c = conns[m.id], c.hello?.capabilities?.contains("fs.find.v1") == true else { continue }
            for pr in snapshots[m.id]?.projects ?? [] where ws?.shows(pr, machine: m.id) ?? true {
                asks.append((m.id, many ? m.label : nil, pr, c))
            }
        }
        fileSearch = Task {
            try? await Task.sleep(nanoseconds: 150_000_000)
            if Task.isCancelled { return }
            struct Params: Encodable { var root: String; var query: String; var limit: Int }
            var hits: [PaletteItem] = []
            for (m, label, pr, c) in asks {
                guard let found = try? await c.call("fs.find", Params(root: pr.path, query: q, limit: 8), as: FindResult.self) else { continue }
                if Task.isCancelled { return }
                hits += fileItems(found, machine: m, project: pr.name, label: label)
            }
            if !Task.isCancelled && fileQuery == q { fileHits = Array(hits.prefix(40)) }
        }
    }

    /// Opens a file in its machine's own $EDITOR, in a terminal tab in the
    /// checkout, as the TUI's files view does with e.
    func edit(machine: String, root: String, path: String) {
        guard let c = conns[machine] else { return }
        struct Create: Encodable {
            var name: String
            var cwd: String
            var command: [String]
        }
        let full = (root as NSString).appendingPathComponent(path)
        let params = Create(name: "edit · " + (path as NSString).lastPathComponent, cwd: root,
                            command: ["/bin/sh", "-lc", #"exec ${VISUAL:-${EDITOR:-vi}} "$1""#, "sh", full])
        Task {
            do {
                let p = try await c.call("pane.create", params, as: PaneInfo.self)
                snapshots[machine, default: Snapshot()].upsert(p)
                joinWorkspace(machine: machine, pane: p)
                layout.open(key(machine, p.id))
                focusTerminal()
            } catch {
                self.error = "Could not open \(path): \(error)"
            }
        }
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
        let configured = ConchTheme.configured(in: toml, dark: systemDark)
        let next = ConchTheme.resolve(choice: themeChoice, configured: configured, preview: previewing).shown
        guard next != theme || Theme.current != next else { return }
        theme = next
        Theme.current = next
        look.theme = next
        for t in terminals.values { t.apply(next) }
        #if GHOSTTY
        GhosttyRuntime.shared.apply(next)
        #endif
        onTheme?(next)
    }

    /// Shows a theme without choosing it, or (nil) goes back to the one
    /// settled on.
    func previewTheme(_ id: String?) {
        guard id != previewing else { return }
        previewing = id
        loadTheme()
    }

    /// A style, and the theme it is drawn for (Ink's black on paper); the
    /// theme can be changed after, and going back to standard keeps it.
    func chooseStyle(_ st: AppStyle) {
        style = st
        Theme.style = st
        UserDefaults.standard.set(st.rawValue, forKey: "style")
        if let t = st.theme { chooseTheme(t) }
        onStyle?(st)
    }

    /// Told when the style changes, for the menu's tick.
    var onStyle: ((AppStyle) -> Void)?

    func chooseTheme(_ id: String) {
        themeChoice = id
        UserDefaults.standard.set(id, forKey: "theme")
        loadTheme()
    }

    var conchTheme: ConchTheme {
        ConchTheme.named(ConchTheme.configured(in: try? String(contentsOfFile: configFile, encoding: .utf8), dark: systemDark))
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

    // MARK: the sidebar from the keyboard

    /// Keys go to the sidebar while nothing else takes them: no terminal
    /// has the keyboard, no palette, sheet or text field is up — the TUI's
    /// tree keys (TreeKey). Everything else passes through.
    private func watchTreeKeys() {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] e in
            let modified = !e.modifierFlags.intersection([.command, .control, .option]).isEmpty
            guard let k = TreeKey(keyCode: e.keyCode, characters: e.charactersIgnoringModifiers, modified: modified) else { return e }
            let taken = MainActor.assumeIsolated { () -> Bool in
                guard let self, !self.terminalHasKeyboard, !self.paletteOpen, self.taskDraft == nil,
                      let win = NSApp.keyWindow, win.attachedSheet == nil,
                      !(win.firstResponder is NSText), !(win.firstResponder is NSTextField) else { return false }
                self.treeKey(k)
                return true
            }
            return taken ? nil : e
        }
    }

    var treeNav: TreeNav { TreeNav(tree, isOpen: { self.isOpen($0) }) }

    /// Takes the keyboard from the terminal for the sidebar, its cursor on
    /// the focused pane's row: Go → Sidebar, as clicking the tree does.
    func focusSidebar() {
        NSApp.keyWindow?.makeFirstResponder(nil)
        terminalHasKeyboard = false
        if treeCursor == nil || treeNav.row(treeCursor) == nil {
            treeCursor = layout.focused.map { "pane:" + $0 } ?? treeNav.step(from: nil, by: 0)
        }
    }

    func treeKey(_ k: TreeKey) {
        let nav = treeNav
        guard let cur = nav.row(treeCursor) else {
            treeCursor = layout.focused.flatMap { nav.row("pane:" + $0)?.id } ?? nav.step(from: nil, by: 0)
            return
        }
        switch k {
        case .up: treeCursor = nav.step(from: cur.id, by: -1)
        case .down: treeCursor = nav.step(from: cur.id, by: 1)
        case .left:
            if cur.children != nil && isOpen(cur) { toggle(cur) } else if let p = nav.parent(of: cur.id) { treeCursor = p }
        case .right:
            if cur.children != nil && !isOpen(cur) { toggle(cur) } else if let c = nav.firstChild(of: cur.id) { treeCursor = c }
        case .fold: if cur.children != nil { toggle(cur) }
        case .open:
            switch cur.kind {
            case .agent(let id), .terminal(let id):
                // ⏎ on a pane is going into it, as in the TUI.
                open(id)
                focusTerminal()
            case .branch(let pr, let name, _): openChanges(project: pr, branch: name)
            case .sessions(let pk): openSessions(project: pk)
            default: toggle(cur)
            }
        case .task:
            if let a = menu(for: cur).first(where: { if case .newTask = $0.action { return true }; return false }) {
                perform(a.action, on: cur)
            } else {
                newTaskHere()
            }
        case .agent, .terminal:
            let want = { (a: RowAction) -> Bool in
                switch a {
                case .startAgent: return k == .agent
                case .newTerminal: return k == .terminal
                default: return false
                }
            }
            if let a = menu(for: cur).first(where: { want($0.action) }) { perform(a.action, on: cur) }
        case .close:
            if case .agent(let id) = cur.kind { closePane(id) } else if case .terminal(let id) = cur.kind { closePane(id) }
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
            if let a = try? await c.call("agent.status", as: AgentStatusList.self) {
                agents[machine] = (a.agents ?? []).filter(\.installed)
            }
            // The tabs the TUI is showing here, once a launch: the window
            // opens on the same work, with nothing given the keyboard.
            if !viewAdopted.contains(machine), c.hello?.capabilities?.contains("view.v1") == true,
               let v = try? await c.call("view.get", as: ViewState.self) {
                viewAdopted.insert(machine)
                let alive = Set((snapshots[machine]?.panes ?? []).filter(\.running).map { key(machine, $0.id) })
                // Into the workspace the TUI is on, which may not be this one.
                if tuiWorkspace == tabsOwner {
                    layout.adopt(v, machine: machine, alive: alive)
                } else {
                    parkedTabs[tuiWorkspace, default: Layout()].adopt(v, machine: machine, alive: alive)
                }
            }
            // Nothing is opened, and nothing given the keyboard, until a
            // pane is picked: a window that opened on an agent with its
            // terminal focused sent the first key anyone pressed — an Enter
            // — to that agent, which took it as accepting its suggestion.
            // Asked for by name at launch (`-open p3`, `-open busybox|p3`),
            // a pane is shown — still without the keyboard.
            if let o = UserDefaults.standard.string(forKey: "open") {
                if let c = parseChangesKey(o) {
                    if c.machine == machine { layout.open(o) } // a branch's changes
                } else if let c = parseCommitsKey(o) ?? parseSessionsKey(o) {
                    if c.machine == machine { layout.open(o) } // a project's commits or sessions
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
            // Exited on its own — a shell's exit, an ssh logout, an agent's
            // /exit: closed, as the TUI closes it, so it goes from the
            // sidebar and its tab. A TUI open may close it first; the
            // second close finds it gone, which is fine.
            if name == "pane.exited", closesOnExit(p), let c = conns[machine] {
                Task { try? await c.call("pane.close", PaneRefParams(id: p.id)) }
            }
            if p.agent?.state == .blocked {
                if was != .blocked || questions[k] == nil { ask(k) }
            } else {
                questions.removeValue(forKey: k)
            }
        case "pane.closed":
            // The server says only which pane ({"id": …}); read as a whole
            // pane it never decoded, and every closed pane stayed — its row,
            // its tab, a dead terminal — until the app started again.
            struct Ref: Decodable { var id: String }
            guard let p = try? dec.decode(Ref.self, from: data) else { return }
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
                // And an open commit graph, unless it is already reading.
                for v in commitViews.values where v.machine == machine && v.project == p.id && !v.loading && layout.tab(showing: v.key) != nil { v.load() }
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

    func sessions(for k: String) -> SessionsModel {
        if let m = sessionViews[k] { return m }
        let m = SessionsModel(key: k, store: self)
        sessionViews[k] = m
        return m
    }

    func commits(for k: String) -> CommitsModel {
        if let m = commitViews[k] { return m }
        let m = CommitsModel(key: k, store: self)
        commitViews[k] = m
        return m
    }

    /// A project's commit graph, in a tab of its own; from a branch's menu,
    /// at that branch's newest commit.
    func openCommits(project pk: String, branch: String? = nil) {
        let (m, pid) = unkey(pk)
        let k = commitsKey(m, pid)
        let model = commits(for: k)
        model.focusBranch = branch
        if layout.tab(showing: k) != nil || branch != nil { model.load() }
        layout.open(k)
        terminalHasKeyboard = false
    }

    /// A project's saved conversations, in a tab of their own — what the
    /// Sessions row opens, rather than the TUI.
    func openSessions(project pk: String) {
        let (m, pid) = unkey(pk)
        let k = sessionsKey(m, pid)
        if layout.tab(showing: k) != nil { sessionViews[k]?.load() }
        layout.open(k)
        terminalHasKeyboard = false
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
                joinWorkspace(machine: machine, pane: p)
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
            var next = snapshots[m.id]?.nextAttention(after: nil, in: ws, machine: m.id)
            while let n = next, !seen.contains(n) {
                seen.insert(n)
                order.append(key(m.id, n))
                next = snapshots[m.id]?.nextAttention(after: n, in: ws, machine: m.id)
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
        case .file(let m, let root, let path): edit(machine: m, root: root, path: path)
        case .pane(let p): open(p)
        case .project(let id): openProject(id)
        case .branch(let pr, let name): openChanges(project: pr, branch: name)
        case .command(let id): command(id)
        }
    }

    func command(_ id: String) {
        switch id {
        case "focusSidebar": focusSidebar()
        case "settings": openSettings()
        case "addProject": addProject()
        case "sshToHost": sshToHost()
        case "addMachine": addMachine()
        case "newTask": newTaskHere()
        case "startAgent": startAgentHere()
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

    // MARK: starting agents and tasks, and a row's menu

    /// A row's right-click menu (ConchKit's Snapshot.menu).
    func menu(for node: TreeNode) -> [RowMenuItem] {
        guard let m = node.machine else { return [] }
        let online = machines.first { $0.id == m }?.online ?? false
        return (snapshots[m] ?? Snapshot()).menu(for: node, machine: m, machineOnline: online)
    }

    /// The agents to offer on a machine: those installed there, else the
    /// supported ones by name, so the menu is never empty while the list
    /// is still being asked for.
    func startable(on machine: String) -> [AgentAvailability] {
        let have = agents[machine] ?? []
        return have.isEmpty ? ["claude", "codex", "gemini", "opencode", "devin"].map { AgentAvailability(name: $0) } : have
    }

    func perform(_ a: RowAction, on node: TreeNode, agent: String? = nil) {
        switch a {
        case .open, .divider: activate(node)
        case .rename(let k): renamePane(k)
        case .prompt(let k): promptAgent(k)
        case .close(let k): closePane(k)
        case .startAgent(let m, let cwd, let inProject):
            newPane(machine: m, cwd: cwd, inProject: inProject, agent: agent ?? startable(on: m).first?.name)
        case .newTerminal(let m, let cwd, let inProject): newPane(machine: m, cwd: cwd, inProject: inProject, agent: nil)
        case .changes(let pr, let b): openChanges(project: pr, branch: b)
        case .commits(let pr, let b): openCommits(project: pr, branch: b)
        case .newTask(let pr): newTask(project: pr)
        case .copy(let text):
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
        case .addProject(let m): addProject(on: m)
        case .sshToHost: sshToHost()
        case .addMachine: addMachine()
        }
    }

    // MARK: projects, ssh hosts and machines

    /// Adds a project, as the TUI's a: on this computer a folder picked in
    /// the Finder's panel; on another machine a path typed in, since a Mac
    /// panel cannot browse it. Added from a workspace after the first, the
    /// project is put in it, as the TUI does.
    func addProject(on machine: String = "local") {
        guard let c = conns[machine] else {
            error = "\(label(machine)) is not connected"
            return
        }
        var path: String?
        if machine == "local" {
            let panel = NSOpenPanel()
            panel.canChooseDirectories = true
            panel.canChooseFiles = false
            panel.canCreateDirectories = true
            panel.allowsMultipleSelection = false
            panel.prompt = "Add project"
            panel.message = "A folder to work in — a git repository gets branches, worktrees and tasks of its own."
            guard panel.runModal() == .OK, let u = panel.url else { return }
            path = u.path
        } else {
            path = ask("Add a project on \(label(machine))", info: "The folder's path on that machine, e.g. ~/src/api.", value: "", button: "Add")
        }
        guard let p = path?.trimmingCharacters(in: .whitespaces), !p.isEmpty else { return }
        struct Params: Encodable { var path: String }
        Task {
            do {
                let info = try await c.call("project.add", Params(path: p), as: ProjectInfo.self)
                joinWorkspace(machine: machine, project: info.id)
                status = "Added \(info.name)"
            } catch {
                self.error = "Could not add \(p): \(error)"
            }
        }
    }

    /// A project added from a workspace after the first joins it (ui.json).
    private func joinWorkspace(machine: String, project id: String) {
        guard let w = ws, !w.first else { return }
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try Workspace.addingProject(Workspace.key(project: id, machine: machine), to: w.id, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
            loadWorkspaces()
        } catch {
            self.error = "Could not put the project in this workspace: \(error)"
        }
    }

    /// A terminal here logged in to a host, as the TUI's H: `conch ssh`,
    /// which builds the session the TUI does.
    func sshToHost() {
        guard let b = binary,
              let (host, options) = askTwo("SSH from this computer", info: "Opens a terminal here logged in to the host, with your ssh config and keys. Nothing is installed there.",
                                           first: ("Host", "user@host, a host alias or ssh://user@host:port"),
                                           second: ("SSH options", "optional, e.g. -p 2222 -o KexAlgorithms=+diffie-hellman-group14-sha1"),
                                           button: "Connect") else { return }
        let target = host.trimmingCharacters(in: .whitespaces)
        guard !target.isEmpty else { return }
        let args = ["ssh", target] + options.split(separator: " ").map(String.init)
        Task {
            do {
                let out = try await runConch(b, args)
                let id = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !id.isEmpty else { return }
                layout.open(key("local", id))
                if let p = pane(key("local", id)) { joinWorkspace(machine: "local", pane: p) }
                focusTerminal()
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// Another machine, as the TUI's M: `conch machine add` in a terminal of
    /// its own, so the install is watched; the machine shows in the sidebar
    /// when machines.json has it.
    func addMachine() {
        guard let b = binary, let c = conns["local"],
              let (host, label) = askTwo("Add a machine", info: "conch is installed there over ssh, and runs agents there that keep running when you close this.",
                                         first: ("SSH target", "user@host or a host alias"), second: ("Label", "optional, how the sidebar names it"),
                                         button: "Add") else { return }
        let target = host.trimmingCharacters(in: .whitespaces)
        guard !target.isEmpty else { return }
        var command = [b, "machine", "add"]
        let l = label.trimmingCharacters(in: .whitespaces)
        if !l.isEmpty { command += ["-label", l] }
        command.append(target)
        struct Create: Encodable {
            var name: String
            var command: [String]
            var cwd: String
            var no_project: Bool
        }
        Task {
            do {
                let p = try await c.call("pane.create", Create(name: "machine add · " + target, command: command, cwd: NSHomeDirectory(), no_project: true), as: PaneInfo.self)
                snapshots["local", default: Snapshot()].upsert(p)
                layout.open(key("local", p.id))
                focusTerminal()
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// Two lines from a small dialog, or nil when cancelled.
    private func askTwo(_ title: String, info: String, first: (String, String), second: (String, String), button: String) -> (String, String)? {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        let box = NSStackView(frame: NSRect(x: 0, y: 0, width: 360, height: 110))
        box.orientation = .vertical
        box.alignment = .leading
        box.spacing = 6
        func field(_ label: String, _ hint: String) -> NSTextField {
            box.addArrangedSubview(NSTextField(labelWithString: label))
            let f = NSTextField(frame: NSRect(x: 0, y: 0, width: 360, height: 24))
            f.placeholderString = hint
            f.translatesAutoresizingMaskIntoConstraints = false
            f.widthAnchor.constraint(equalToConstant: 360).isActive = true
            box.addArrangedSubview(f)
            return f
        }
        let one = field(first.0, first.1), two = field(second.0, second.1)
        a.accessoryView = box
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = one
        return a.runModal() == .alertFirstButtonReturn ? (one.stringValue, two.stringValue) : nil
    }

    /// What a click on the row does, for the menu's Open.
    func activate(_ node: TreeNode) {
        switch node.kind {
        case .agent(let id), .terminal(let id): open(id)
        default: break
        }
    }

    /// Starts an agent (or, without one, a terminal) and shows it in a tab
    /// of its own, as the TUI's c and n do.
    func newPane(machine: String, cwd: String?, inProject: Bool, agent: String?) {
        guard let c = conns[machine] else { return }
        struct Create: Encodable {
            var agent: String?
            var cwd: String?
            var no_project: Bool?
        }
        Task {
            do {
                let p = try await c.call("pane.create", Create(agent: agent, cwd: cwd, no_project: inProject ? nil : true), as: PaneInfo.self)
                snapshots[machine, default: Snapshot()].upsert(p)
                joinWorkspace(machine: machine, pane: p)
                layout.open(key(machine, p.id))
                focusTerminal()
            } catch {
                self.error = "Could not start \(agent ?? "a terminal"): \(error)"
            }
        }
    }

    /// Opens the task sheet for a project (key(machine, project)), with
    /// the machine's first agent chosen.
    func newTask(project: String) {
        let (m, _) = unkey(project)
        taskDraft = TaskDraft(project: project, agent: startable(on: m).first?.name ?? "claude")
    }

    /// The task sheet's project when none was picked: the focused pane's,
    /// else the first project on this computer.
    func newTaskHere() {
        if let f = layout.focused, let p = pane(f), let pr = p.projectID {
            newTask(project: key(unkey(f).machine, pr))
        } else if let pr = snapshots["local"]?.projects.first {
            newTask(project: key("local", pr.id))
        } else {
            error = "Add a project first (conch project add PATH): a task needs one"
        }
    }

    /// Starts the machine's first agent where the focused pane is (its
    /// folder, in its project), else in this computer's home.
    func startAgentHere() {
        if let f = layout.focused, let p = pane(f) {
            let m = unkey(f).machine
            newPane(machine: m, cwd: p.cwd, inProject: p.projectID != nil, agent: startable(on: m).first?.name)
        } else {
            newPane(machine: "local", cwd: nil, inProject: false, agent: startable(on: "local").first?.name)
        }
    }

    /// Starts the task: a branch, a worktree and an agent given the prompt.
    func startTask(_ d: TaskDraft) {
        let (m, pr) = unkey(d.project)
        guard let c = conns[m] else { return }
        struct Params: Encodable {
            var project_id: String
            var prompt: String
            var agent: String?
            var branch: String?
        }
        let branch = d.branch.trimmingCharacters(in: .whitespaces)
        taskDraft = nil
        Task {
            do {
                let p = try await c.call("task.create", Params(project_id: pr, prompt: d.prompt, agent: d.agent,
                                                               branch: branch.isEmpty ? nil : branch), as: PaneInfo.self)
                snapshots[m, default: Snapshot()].upsert(p)
                joinWorkspace(machine: m, pane: p)
                layout.open(key(m, p.id))
                focusTerminal()
            } catch {
                self.error = "Could not start the task: \(error)"
            }
        }
    }

    func renamePane(_ k: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m], let p = pane(k) else { return }
        guard let name = ask("Rename \(p.displayName)", info: "Leave empty to give it its own name back.", value: p.customName == true ? p.name : "", button: "Rename") else { return }
        struct Params: Encodable { var id: String; var name: String }
        Task {
            do { try await c.call("pane.rename", Params(id: id, name: name.trimmingCharacters(in: .whitespaces))) } catch { self.error = "\(error)" }
        }
    }

    /// Sends an agent a message, as the TUI's Prompt… does: the server
    /// types it in and submits it.
    func promptAgent(_ k: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m], let p = pane(k) else { return }
        guard c.hello?.capabilities?.contains("agent.prompt.v1") == true else {
            error = "The server on \(label(m)) predates prompting an agent; reload it"
            return
        }
        let waiting = p.agent?.state == .blocked ? " It is waiting for an answer; this goes in as a new message." : ""
        guard let text = ask("Prompt \(p.displayName)", info: "Sent to the agent as if typed and submitted." + waiting, value: "", button: "Send"),
              !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        struct Params: Encodable { var id: String; var text: String }
        Task {
            do { try await c.call("agent.prompt", Params(id: id, text: text)) } catch { self.error = "\(error)" }
        }
    }

    /// Ends a pane, after asking when an agent is running in it.
    func closePane(_ k: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m], let p = pane(k) else { return }
        if p.agent != nil && p.running {
            let a = NSAlert()
            a.messageText = "Close \(p.displayName)?"
            a.informativeText = "The agent stops. Its conversation is saved and can be resumed from Sessions."
            a.addButton(withTitle: "Close")
            a.addButton(withTitle: "Cancel")
            guard a.runModal() == .alertFirstButtonReturn else { return }
        }
        Task {
            do { try await c.call("pane.close", PaneRefParams(id: id)) } catch { self.error = "\(error)" }
        }
    }

    /// A line of text from a small dialog, or nil when cancelled.
    private func ask(_ title: String, info: String, value: String, button: String) -> String? {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 24))
        field.stringValue = value
        a.accessoryView = field
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = field
        return a.runModal() == .alertFirstButtonReturn ? field.stringValue : nil
    }

    // MARK: workspaces

    /// Makes a workspace, as the TUI's + does, and shows it. It goes into
    /// ui.json, where a running TUI takes it in within a second.
    func newWorkspace() {
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try Workspace.adding(to: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            self.error = "Could not add a workspace: \(error)"
            return
        }
        loadWorkspaces()
        workspace = workspaces.count - 1
    }

    /// Names a workspace, as the TUI's ctrl+b $ does: in ui.json, which
    /// the TUI merges, so both show it. Empty numbers it again.
    func renameWorkspace(_ i: Int) {
        guard workspaces.indices.contains(i) else { return }
        let w = workspaces[i]
        guard let name = ask("Name workspace \(i + 1)", info: "As the mock's \u{201C}1 main  2 review\u{201D}. Leave empty to number it; conch's TUI shows the name too.",
                             value: w.name, button: "Name") else { return }
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try Workspace.renaming(w.id, to: name, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            self.error = "Could not name the workspace: \(error)"
            return
        }
        loadWorkspaces()
    }

    func loadWorkspaces() {
        let data = FileManager.default.contents(atPath: (conchDir as NSString).appendingPathComponent("ui.json"))
        workspaces = Workspace.load(uiJSON: data)
        tuiFolds = loadFolds(uiJSON: data)
        let active = activeWorkspace(uiJSON: data)
        tuiWorkspace = workspaces.indices.contains(active) ? workspaces[active].id : "first"
        // The one on screen, found again by its id: the list may have
        // changed under it in the TUI.
        if let i = workspaces.firstIndex(where: { $0.id == tabsOwner }) {
            if workspace != i { workspace = i }
        } else {
            workspace = 0
        }
        swapTabs()
    }

    /// Shows the tabs of the workspace on screen, putting away the ones of
    /// the workspace that was. Nothing is given the keyboard.
    private func swapTabs() {
        let id = ws?.id ?? "first"
        guard id != tabsOwner else { return }
        parkedTabs[tabsOwner] = layout
        layout = parkedTabs.removeValue(forKey: id) ?? Layout()
        tabsOwner = id
    }

    /// A pane started here in a workspace after the first joins it, as one
    /// started there in the TUI does — unless its project is in it already,
    /// which shows it anyway. Written into ui.json, which the TUI merges.
    func joinWorkspace(machine: String, pane: PaneInfo) {
        guard let w = ws, !w.first else { return }
        if let pr = pane.projectID, snapshots[machine]?.project(pr).map({ w.shows($0, machine: machine) }) == true { return }
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try Workspace.addingPane(key(machine, pane.id), to: w.id, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            self.error = "Could not put the pane in this workspace: \(error)"
            return
        }
        loadWorkspaces()
    }
}

/// A task being written in its sheet.
struct TaskDraft: Identifiable {
    let id = UUID()
    var project: String // key(machine, project)
    var prompt = ""
    var agent: String
    var branch = ""
}

private struct PaneRefParams: Encodable { var id: String }

/// The theme on screen, apart from the store, so that what observes it is
/// not redrawn by everything else the store publishes.
@MainActor
final class ShownTheme: ObservableObject {
    @Published var theme = ConchTheme.conch
}
