import AppKit
import Combine
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
    /// Its provider (daytona, boat, devin), for conch sandbox.
    var provider: String?
    var version = ""
    /// Capabilities this app's conch has that the server lacks: it is from
    /// an older build, and a reload (or an upgrade, elsewhere) brings them.
    var behind: [String] = []

    var online: Bool { state.isEmpty }
}

/// A broadcast being written: where from, to whom, and what.
struct BroadcastDraft: Identifiable {
    let id = UUID()
    var scope: BroadcastScope
    var title: String
    var targets: [BroadcastTarget]
    var text = ""
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
    /// Every machine in machines.json, disabled ones too, as last read:
    /// what conch's commands look a name up in, for paneRef.
    var catalog: [MachineEntry] = []
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
    @Published var layout = Layout() {
        // A tab came to the front — from ⌘K, a click in the review queue, a
        // port chip, ⌘1–9: the sidebar's cursor goes to its row rather than
        // staying on the row last clicked.
        didSet { if layout.focused != oldValue.focused, let f = layout.focused { treeCursor = treeRow(forTab: f) } }
    }
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
    @Published var limits: [PlanLimits] = [] { didSet { alertLimits() } }
    /// Plan limit alerts already raised (limitCrossings), kept with the
    /// app's preferences so a restart does not raise them again.
    private var limitSeen: [String: Int] = (UserDefaults.standard.dictionary(forKey: "conch.limitSeen") as? [String: Int]) ?? [:]
    @Published var status: String = "Connecting to conch…"
    @Published var paletteOpen = false
    /// Help → Keyboard Shortcuts and Help → Check conch… on screen.
    @Published var helpOpen = false
    /// The welcome (WelcomeSheet): shown when the app opens until it has
    /// been seen, and from Help → Welcome to Conch.
    @Published var welcomeOpen = shouldShowWelcome(seen: UserDefaults.standard.integer(forKey: Store.welcomeKey))
    static let welcomeKey = "conch.welcomeSeen"

    /// The welcome has been seen: it is not shown again when the app opens.
    func welcomeSeen() {
        UserDefaults.standard.set(welcomeVersion, forKey: Store.welcomeKey)
    }
    /// The status bar's ✦ Ask, open (its button, or the prefix and :).
    @Published var askOpen = false
    @Published var doctorOpen = false
    /// The machine Settings → Skills & MCP servers shows: conch's skills
    /// and the library are both per machine.
    @Published var skillsMachine = "local"
    /// Add project…'s sheet, for the machine it adds to.
    @Published var addProjectTarget: AddProjectTarget?
    /// Move to another machine…'s sheet, for the project it moves.
    @Published var moveProjectTarget: MoveProjectTarget?
    /// A broadcast being written (BroadcastSheet).
    @Published var broadcastDraft: BroadcastDraft?
    /// The focused pane's find bar, while it is open.
    @Published var find: PaneFind?
    /// Bumped to put the keyboard in the find bar's field.
    @Published var findFocus = 0
    @Published var sidebarVisible = true
    /// What the status bar says for a moment (say), and the long jobs
    /// running (job), oldest first.
    @Published private(set) var flash: Flash?
    @Published private(set) var jobs: [(id: UUID, what: String)] = []
    /// A failure, said in the status bar as an error.
    var error: String? {
        get { flash?.tone == .err ? flash?.text : nil }
        set { if let e = newValue { say(e, .err) } else if flash?.tone == .err { flash = nil } }
    }

    /// Says something in the status bar until it has been there long
    /// enough, or something newer is said.
    func say(_ text: String, _ tone: Flash.Tone = .ok) {
        let f = Flash(text, tone)
        flash = f
        DispatchQueue.main.asyncAfter(deadline: .now() + f.lasts) { [weak self] in
            if self?.flash?.id == f.id { self?.flash = nil }
        }
    }

    func dismissFlash() { flash = nil }

    /// Runs a long job with the status bar saying so, then what came of
    /// it: done (said with done's text) or the error.
    func job(_ what: String, done: String? = nil, _ body: @escaping () async throws -> Void) {
        let id = UUID()
        jobs.append((id, what))
        Task {
            defer { jobs.removeAll { $0.id == id } }
            do {
                try await body()
                if let done { say(done) }
            } catch {
                say("\(what) failed: \(error)", .err)
            }
        }
    }
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
    /// What the window is drawn in, as one value its views can depend on:
    /// the theme, the style and how agents are marked.
    /// What a view reading Theme or the window's face depends on, handed
    /// down so it redraws when any part changes — the text size included,
    /// or a label watching only this kept its old size.
    var themeLook: String { theme.id + "|" + style.rawValue + "|" + icons.rawValue + "|" + String(appLook.textScale) }

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
        if style.roster { sidebarVisible = false }
        watchChanges()
        loadTheme()
        watchClicks()
        watchConfig()
        watchQuestions()
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
    private var phonesStamp = ""
    private var phonesTick = 0
    private var phonesFile: String { (conchDir as NSString).appendingPathComponent("phone.json") }
    /// The paired phones, which are on now, and conch web's relay: for the
    /// phone icon and its list. nil until conch first answers.
    @Published var phones: PhonesInfo?
    /// The pair popover, open from the status bar or Settings → Phone.
    @Published var pairOpen = false
    /// The pairing the popover opens with, made before it opens.
    var pairInfo: PairInfo?

    /// conch web stopped and started again, on this app's conch: how a
    /// new build reaches the gateway phones talk to, as the server's
    /// reload does for the server. Phones reconnect by themselves.
    @Published var webRestarting = false
    func restartWeb() {
        guard let b = binary, !webRestarting else { return }
        webRestarting = true
        job("Restarting conch web", done: "conch web restarted") {
            defer { self.webRestarting = false; self.refreshPhones() }
            _ = try await runConch(b, ["web", "stop"])
            _ = try await runConch(b, ["web", "start"])
            // It says it runs once it listens; give it that moment.
            try? await Task.sleep(nanoseconds: 1_500_000_000)
        }
    }

    /// Makes a pairing code, then opens the pair popover with it. A
    /// popover is placed for the size it opens at; opened empty and
    /// filled a moment later, it grew down off the screen from a status
    /// bar at the bottom of it.
    func openPair() {
        guard let b = binary else { return }
        Task {
            if let out = try? await runConch(b, ["web", "pair", "-json", "-permission", "full"]) {
                pairInfo = try? conchDecoder().decode(PairInfo.self, from: out)
            }
            pairOpen = true
        }
    }
    private var phonesAsking = false

    /// Asks conch for the phones again. conch web writes phone.json as a
    /// phone comes and goes, so a change there is when to ask; and every
    /// half minute besides, since a conch web that stops writes nothing.
    func refreshPhones() {
        guard let b = binary, !phonesAsking else { return }
        phonesAsking = true
        Task {
            defer { phonesAsking = false }
            guard let out = try? await runConch(b, ["web", "devices", "-json"]),
                  let p = try? conchDecoder().decode(PhonesInfo.self, from: out) else { return }
            if p != phones { phones = p }
        }
    }

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
                let pnow = fileStamp(self.phonesFile)
                self.phonesTick += 1
                if pnow != self.phonesStamp || self.phonesTick >= 30 {
                    self.phonesStamp = pnow
                    self.phonesTick = 0
                    self.refreshPhones()
                }
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

    /// Asks a machine's server to read a project's git and its pull requests
    /// now rather than at its next turn — a minute, or five with no panes in
    /// the project — so a merge on GitHub shows when somebody opens, reopens
    /// or refreshes the graph or a git tab. Its answer comes back as project.updated, which
    /// reads an open graph or git tab again. Only on someone's asking: from
    /// load(), which every project.updated runs, it would ask again at every
    /// answer.
    func refreshProject(machine: String, project: String) {
        guard let c = conns[machine], !project.isEmpty else { return }
        struct Ref: Encodable { var id: String }
        Task { try? await c.call("project.refresh", Ref(id: project)) }
    }

    /// "api · local": where a pane is, for the end of the tab bar.
    func scope(of k: String) -> String {
        if parseBrowserKey(k) != nil { return "Browser · this Mac" } // a page is not any project's
        if let c = parseChangesKey(k) {
            return [snapshots[c.machine]?.project(c.project)?.name ?? "", label(c.machine)].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        if let c = parseSessionsKey(k) ?? parseCommitsKey(k) ?? parseFilesKey(k) ?? parseSetupKey(k).map({ ($0.machine, $0.project) }) {
            return [snapshots[c.machine]?.project(c.project)?.name ?? "", label(c.machine)].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        let (m, _) = unkey(k)
        let project = pane(k)?.projectID.flatMap { snapshots[m]?.project($0)?.name }
        return [project ?? "CLI", label(m)].joined(separator: " · ")
    }

    /// Whether a pane's terminal has the keyboard, for the status bar's
    /// chip; set as the window's first responder changes.
    @Published var terminalHasKeyboard = false
    /// The TUI's prefix keys, from conch keys -json; armed after the prefix.
    @Published var prefixKeys = PrefixKeys()
    /// What the table was last read for: config.toml's text and whether
    /// prefix keys are on, so it is read again only when that changes.
    var keysStamp: String?
    /// The sidebar row the keyboard is on while no terminal has it.
    @Published var treeCursor: String?

    /// The port of the checkout a pane is in (CONCH_PORT), if it has one.
    func port(of k: String) -> Int? {
        let (m, _) = unkey(k)
        guard let p = pane(k) else { return nil }
        return snapshots[m]?.port(of: p)
    }

    /// What a pane opened in a tab of its own will measure (fullTabSize).
    func newPaneSize() -> (cols: Int, rows: Int)? {
        fullTabSize(layout) { k in
            guard let p = self.pane(k), let c = p.cols, let r = p.rows else { return nil }
            return (c, r)
        }
    }

    func pane(_ k: String) -> PaneInfo? {
        let (m, id) = unkey(k)
        return snapshots[m]?.pane(id)
    }

    func label(_ machine: String) -> String { machines.first { $0.id == machine }?.label ?? machine }

    /// The label conch's commands know the machine by: machines.json's,
    /// not the sidebar's ("x · daytona" for a sandbox).
    func refLabel(_ machine: String) -> String? { catalog.first { $0.id == machine }?.label ?? label(machine) }

    /// How conch's commands name the pane at key — p12 here, busybox:p12
    /// elsewhere — or nil when key is not a pane (a git tab, sessions).
    func paneRef(key k: String) -> String? {
        guard isPaneKey(k) else { return nil }
        let (m, id) = unkey(k)
        guard snapshots[m]?.pane(id) != nil else { return nil }
        return ConchKit.paneRef(machine: m, label: refLabel(m), pane: id, among: catalog)
    }

    func copyPaneRef(_ ref: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(ref, forType: .string)
        say("Copied \(ref): conch read \(ref), conch send \(ref) …")
    }

    func isOpen(_ node: TreeNode) -> Bool { ConchKit.isOpen(node, app: folds, tui: tuiFolds) }

    func toggle(_ node: TreeNode) {
        guard let f = node.fold else { return }
        folds[f] = !isOpen(node)
        UserDefaults.standard.set(folds, forKey: "folds")
    }

    /// Projects listing every branch, "… N more" clicked (by key).
    @Published var allBranches: Set<String> = []

    func toggleAllBranches(_ project: String) {
        if allBranches.contains(project) { allBranches.remove(project) } else { allBranches.insert(project) }
    }

    /// The sidebar: a tree per machine, this computer first.
    var tree: [TreeNode] {
        machines.map { m in
            var node = (snapshots[m.id] ?? Snapshot()).tree(machine: m.id, label: m.label, in: ws, sessions: sessions, allBranches: allBranches,
                                                            savedHosts: savedHosts)
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

    /// The project the Cel roster is narrowed to ("machine|id"), or every one.
    @Published var rosterProject: String?

    /// The Cel roster: every agent and terminal the workspace shows, on
    /// every machine, narrowed to rosterProject when one is picked.
    var rosterCards: [RosterCard] {
        // Once per change of the store, however many views read it: the
        // roster and the log both do, and every card redraws on any change.
        if let c = rosterMemo { return c }
        let cards = buildRoster()
        rosterMemo = cards
        return cards
    }

    /// The review queue rows dismissed in the TUI (ui.json).
    @Published private(set) var queueDismissed: [String: String] = [:]

    /// Puts a review row aside, as the TUI's x: in ui.json, so the TUI
    /// leaves it out too, until it changes.
    func dismiss(_ it: ReviewItem) {
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try dismissing(it, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            say("Could not dismiss it: \(error)", .err)
            return
        }
        loadWorkspaces()
        say("Dismissed \([it.projectName, it.branch].filter { !$0.isEmpty }.joined(separator: " · ")); it comes back if it changes", .info)
    }

    /// A branch's failed CI run sent to its agent, or an agent started in
    /// its worktree with it — the TUI's f, as `conch branch fix-ci`.
    func fixCI(_ it: ReviewItem) { fixCI(machine: it.machine, project: it.project, branch: it.branch) }

    func fixCI(machine: String, project: String, branch: String) {
        guard let b = binary, let dir = snapshots[machine]?.project(project)?.path else { return }
        let agent = startable(on: machine).first?.name ?? "claude"
        job("Sending CI's failure on \(branch)", done: "Sent CI's failure on \(branch) to its agent") {
            _ = try await runConch(b, onMachine(machine, ["branch", "fix-ci", "-cwd", dir, "-branch", branch, "-agent", agent]))
        }
    }

    /// What needs a decision, across every machine reached: the TUI's
    /// review queue, as its "N to review" counts it. Built once per change.
    var reviewItems: [ReviewItem] {
        if let r = reviewMemo { return r }
        let online = machines.filter { $0.state.isEmpty }.map { (id: $0.id, snapshot: snapshots[$0.id] ?? Snapshot()) }
        let r = reviewQueue(online, dismissed: queueDismissed)
        reviewMemo = r
        return r
    }
    private var reviewMemo: [ReviewItem]?

    /// The roster as last built; let go whenever the store is about to
    /// change (watchChanges), so the next read builds it afresh.
    private var rosterMemo: [RosterCard]?
    private var memoWatch: AnyCancellable?

    private func watchChanges() {
        memoWatch = objectWillChange.sink { [weak self] _ in
            self?.rosterMemo = nil
            self?.reviewMemo = nil
        }
    }

    private func buildRoster() -> [RosterCard] {
        let only = rosterFilter
        return roster(machines.flatMap { m -> [RosterCard] in
            let snap = snapshots[m.id] ?? Snapshot()
            return snap.shown(in: ws, machine: m.id)
                .filter { p in only == nil || p.projectID.map { key(m.id, $0) } == only }
                .map { p in
                    let place = [snap.place(of: p), m.id == "local" ? "" : label(m.id)].filter { !$0.isEmpty }.joined(separator: " · ")
                    return RosterCard(key: key(m.id, p.id), pane: p, place: place)
                }
        })
    }

    /// The projects the Cel rail offers: those with a pane the workspace
    /// shows, on every machine, each with its machine's name when there
    /// are several.
    var railProjects: [(key: String, name: String, machine: String)] {
        let many = machines.count > 1
        return machines.flatMap { m -> [(key: String, name: String, machine: String)] in
            let snap = snapshots[m.id] ?? Snapshot()
            let used = Set(snap.shown(in: ws, machine: m.id).compactMap(\.projectID))
            return snap.projects.filter { used.contains($0.id) }.map { (key(m.id, $0.id), $0.name, many ? m.label : "") }
        }
    }

    /// The project the roster is narrowed to, while it is still on the
    /// rail: one whose last pane closed, or one another workspace holds,
    /// leaves the roster showing everything rather than nothing.
    var rosterFilter: String? {
        guard let p = rosterProject, railProjects.contains(where: { $0.key == p }) else { return nil }
        return p
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
            let (a, b, c) = (snapshots[m.id] ?? Snapshot()).paletteItems(machine: m.id, label: many ? m.label : nil, in: ws, among: catalog)
            w += a
            p += b
            pl += c
        }
        return w + p + pl + paletteCommandItems()
    }

    // MARK: the status bar's own: limits, settings, what conch uses

    /// Asks again what each agent's plan has used, as the limits popover
    /// opens: a report from minutes ago is not what anyone opened it for.
    /// What the agents on each machine used over the last day, week and
    /// month (usage.history.v1), in the sidebar's order of machines; asked
    /// when the usage popover opens.
    @Published private(set) var usageHistory: [(machine: String, history: UsageHistory)] = []
    @Published private(set) var usageLoading = false
    /// Machines online whose conch is too old to say (no usage.history.v1).
    @Published private(set) var usageTooOld: [String] = []

    func refreshUsageHistory() {
        let online = machines.filter(\.online).map(\.id)
        let able = online.filter { conns[$0]?.hello?.capabilities?.contains("usage.history.v1") == true }
        usageTooOld = online.filter { !able.contains($0) }
        guard !able.isEmpty else { usageHistory = []; return }
        usageLoading = true
        Task {
            var got: [(machine: String, history: UsageHistory)] = []
            for m in able {
                guard let c = conns[m], let h = try? await c.call("usage.history", as: UsageHistory.self) else { continue }
                got.append((m, h))
            }
            usageHistory = got
            usageLoading = false
        }
    }

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
    /// Opens the Settings window, at a section when one is named.
    var onOpenSettings: ((String?) -> Void)?

    func openSettings(_ section: String? = nil) { onOpenSettings?(section) }

    /// Settings → Skills & MCP servers, showing machine m.
    func openSkillsSettings(_ m: String) {
        skillsMachine = m
        openSettings(SettingsNav.skills)
    }

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
            var cols: Int?
            var rows: Int?
        }
        let full = (root as NSString).appendingPathComponent(path)
        let size = newPaneSize()
        let params = Create(name: "edit · " + (path as NSString).lastPathComponent, cwd: root,
                            command: ["/bin/sh", "-lc", #"exec ${VISUAL:-${EDITOR:-vi}} "$1""#, "sh", full], cols: size?.cols, rows: size?.rows)
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
        notifySettings = NotifySettings(toml: toml)
        let acts = parseActions(toml: toml ?? "")
        if acts != actions { actions = acts }
        let cost = showsCost(toml: toml ?? "")
        if cost != showCost { showCost = cost }
        let app = AppLook(toml: toml)
        loadKeys(on: app.prefixKeys, stamp: toml ?? "")
        if app != appLook {
            // Before the views redraw: the window's text is read at its size.
            Theme.textScale = app.textScale
            appLook = app
            onAppLook?(app)
        }
        // Themes of your own, read again each time: a file added or changed
        // is in the lists, and in use, without restarting.
        let mine = userThemes(in: (conchDir as NSString).appendingPathComponent("themes"))
        if mine.themes != ConchTheme.user {
            ConchTheme.user = mine.themes
            onThemeList?()
        }
        userThemeProblems = mine.problems
        let configured = ConchTheme.configured(in: toml, dark: systemDark)
        let resolved = ConchTheme.resolve(choice: themeChoice, configured: configured, preview: previewing)
        // A theme drawn as a pair (Terminal) follows macOS to its light one;
        // a theme being previewed is shown as it is. [ui] accent over it,
        // as the TUI draws it.
        let next = (previewing == nil ? ConchTheme.forAppearance(resolved.shown, dark: systemDark) : resolved.shown)
            .withAccent(uiSetting("accent", in: toml))
        guard next != theme || Theme.current != next else { return }
        theme = next
        Theme.current = next
        look.theme = next
        for t in terminals.values { t.apply(next) }
        #if GHOSTTY
        GhosttyRuntime.shared.apply(next)
        #endif
        onTheme?(next)
        keepKeyboard()
    }

    /// A new theme or style draws the window again, every terminal's view
    /// put back in a new box, which loses it the keyboard: give it back to
    /// the pane that had it, once the window has been drawn. Within the
    /// main window only, so a Settings window being typed in keeps it.
    private func keepKeyboard() {
        guard terminalHasKeyboard, let p = layout.focused else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) {
            guard self.terminalHasKeyboard, self.layout.focused == p, let v = self.terminals[p]?.view, let w = v.window,
                  w === self.mainWindow, w.firstResponder !== v else { return }
            w.makeFirstResponder(v)
        }
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
        // Cel's column of projects stands in for the tree, which is a click
        // away; leaving it brings the tree back.
        sidebarVisible = !st.roster
        rosterProject = nil
        if let t = st.theme { chooseTheme(t) }
        onStyle?(st)
        keepKeyboard()
    }

    /// Told when the style changes, for the menu's tick.
    var onStyle: ((AppStyle) -> Void)?

    func chooseTheme(_ id: String) {
        themeChoice = id
        UserDefaults.standard.set(id, forKey: "theme")
        loadTheme()
        // The menu's tick follows the choice even when what is shown stays
        // the same (pinned to the theme conch already sets, then unpinned).
        onThemeChoice?()
    }

    /// Told when the theme chosen changes, for View → Theme's tick.
    var onThemeChoice: (() -> Void)?

    var conchTheme: ConchTheme {
        ConchTheme.named(ConchTheme.configured(in: try? String(contentsOfFile: configFile, encoding: .utf8), dark: systemDark))
    }

    var conchDir: String {
        ProcessInfo.processInfo.environment["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 }
            ?? (NSHomeDirectory() as NSString).appendingPathComponent(".config/conch")
    }

    private var configFile: String { (conchDir as NSString).appendingPathComponent("config.toml") }

    /// One [app] setting written to config.toml, the rest of the file kept,
    /// then read back so the window takes it — as the settings page would.
    /// A TUI open meanwhile holds its own copy of the file; the settings
    /// page is the way to change it there.
    private func setAppSetting(_ key: String, _ value: String?) {
        let toml = (try? String(contentsOfFile: configFile, encoding: .utf8)) ?? ""
        let next = settingTOML(toml, section: "app", key: key, value: value)
        guard next != toml else { return }
        do {
            try FileManager.default.createDirectory(atPath: conchDir, withIntermediateDirectories: true)
            try next.write(toFile: configFile, atomically: true, encoding: .utf8)
        } catch {
            say("Could not save \(key): \(error.localizedDescription)", .err)
            return
        }
        loadTheme()
    }

    /// ⌘+ / ⌘−: the terminal's font a point bigger or smaller; nil, the default.
    func stepFontSize(bigger: Bool?) {
        let pt = bigger.map { AppLook.fontStep(from: appLook.fontSize, bigger: $0) } ?? AppLook.fontDefault
        setAppSetting("font_size", pt == AppLook.fontDefault ? nil : (pt.truncatingRemainder(dividingBy: 1) == 0 ? String(Int(pt)) : String(pt)))
        say("Terminal text \(Int(pt.rounded())) pt", .info)
    }

    /// ⇧⌘+ / ⇧⌘−: the window's text a step bigger or smaller; nil, 100%.
    func stepTextSize(bigger: Bool?) {
        let scale = bigger.map { AppLook.textStep(from: appLook.textScale, bigger: $0) } ?? 1
        let pct = Int((scale * 100).rounded())
        setAppSetting("text_size", pct == 100 ? nil : String(pct))
        say("Window text \(pct)%", .info)
    }

    /// A click in a pane's terminal gives that pane the keyboard: the split
    /// it is in becomes the focused one.
    /// The main window: clicks and tree keys are its, never the Settings
    /// window's — a key there once reached the tree and closed a terminal.
    weak var mainWindow: NSWindow?

    private func watchClicks() {
        NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] e in
            MainActor.assumeIsolated {
                guard let self, let win = e.window, win === self.mainWindow,
                      let hit = win.contentView?.hitTest(e.locationInWindow) else { return }
                var inTerminal = false
                for (pane, t) in self.terminals where hit === t.view || hit.isDescendant(of: t.view) {
                    self.focus(pane)
                    inTerminal = true
                }
                // Only where the click landed: the status bar's buttons and
                // the toolbar leave the keyboard where it was (ClickOwner).
                let saved = UserDefaults.standard.double(forKey: "sidebarWidth")
                let width = self.sidebarVisible ? self.style.sidebarWidth(saved > 0 ? saved : Double(sidebarDefault)) + sidebarExtra(scale: self.appLook.textScale) : nil
                let p = e.locationInWindow
                let toolbar = Double(win.frame.height - win.contentLayoutRect.height)
                switch clickOwner(inTerminal: inTerminal, x: p.x, y: p.y, sidebarWidth: width, statusBar: 28,
                                  windowHeight: Double(win.frame.height), toolbar: toolbar) {
                case .terminal: self.terminalHasKeyboard = true
                case .sidebar: self.terminalHasKeyboard = false
                case .unchanged: break
                }
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
            // The prefix first, wherever the keyboard is: it is how the TUI
            // reaches the tree from a pane, and a key after it is conch's
            // even where the tree would have taken it.
            switch MainActor.assumeIsolated({ self?.prefixKey(e) ?? .pass }) {
            case .taken: return nil
            case .typed: return e
            case .pass: break
            }
            let modified = !e.modifierFlags.intersection([.command, .control, .option]).isEmpty
            guard let k = TreeKey(keyCode: e.keyCode, characters: e.charactersIgnoringModifiers, modified: modified) else { return e }
            let taken = MainActor.assumeIsolated { () -> Bool in
                // The tree's keys need the tree: in the main window, and on
                // screen (Cel hides it behind its column of projects).
                guard let self, !self.terminalHasKeyboard, !self.paletteOpen, self.taskDraft == nil, self.sidebarVisible,
                      let win = NSApp.keyWindow, win === self.mainWindow, win.attachedSheet == nil,
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
            case .files(let pk): openFiles(project: pk)
            case .more(let pk, _): toggleAllBranches(pk)
            case .savedHost(let h): connectSaved(h)
            case .subagent(let pane, _):
                open(pane)
                focusTerminal()
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
        let data = FileManager.default.contents(atPath: (conchDir as NSString).appendingPathComponent("machines.json"))
        catalog = MachineEntry.all(machinesJSON: data)
        let entries = MachineEntry.load(machinesJSON: data)
        var next = [machines.first { $0.id == "local" } ?? MachineLink(id: "local", label: "local")]
        for e in entries {
            var link = machines.first { $0.id == e.id } ?? MachineLink(id: e.id, label: e.displayLabel)
            link.sandbox = e.sandboxProvider != nil
            link.provider = e.sandboxProvider
            next.append(link)
        }
        // A machine taken out of machines.json is let go: its connection,
        // and the bridge process behind it.
        let kept = Set(next.map(\.id))
        for (id, c) in conns where !kept.contains(id) {
            conns[id] = nil
            c.shutdown()
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
            await noteBehind(machine, c)
            localRetries = 0
            settle(machine, alive: Set(panes.panes.filter(\.running).map { key(machine, $0.id) }))
            pushAutopilot(machine)
            updateBadge()
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
                } else if let c = parseCommitsKey(o) ?? parseSessionsKey(o) ?? parseFilesKey(o) ?? parseSetupKey(o).map({ ($0.machine, $0.project) }) {
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

    /// The layer taken down because its pane is gone; option-space starts
    /// a new one.
    func dropScratch() {
        guard let k = scratch else { return }
        scratch = nil
        terminals.removeValue(forKey: k)?.stop()
        focusTerminal()
    }

    /// Back in touch with a machine: tabs on its panes that went while it
    /// was away are closed, in every workspace, and the panes whose attach
    /// ended meanwhile are started again.
    private func settle(_ machine: String, alive: Set<String>) {
        func gone(_ id: String) -> Bool {
            isPaneKey(id) && unkey(id).machine == machine && !alive.contains(id)
        }
        for id in layout.tabs.flatMap(\.root.panes) where gone(id) { layout.close(id) }
        if let s = scratch, gone(s) { dropScratch() }
        for w in parkedTabs.keys {
            for id in parkedTabs[w]?.tabs.flatMap(\.root.panes) ?? [] where gone(id) { parkedTabs[w]?.close(id) }
        }
        for k in restartOnConnect where unkey(k).machine == machine {
            restartOnConnect.remove(k)
            if alive.contains(k) { restartTerminal(k) } else { terminals.removeValue(forKey: k)?.stop() }
        }
        // Any other attach of this machine's that ended with the old
        // connection is live again too.
        for (k, t) in terminals where unkey(k).machine == machine && t.ended != nil && alive.contains(k) { restartTerminal(k) }
    }

    /// Tries to reach this computer's server again after it went: a
    /// reload comes back within a second or two, a stop does not.
    private var localRetries = 0

    /// The capabilities of this app's own conch, asked once.
    private var appCapabilities: [String]?

    /// Whether a server is behind this app's conch, said on its row.
    private func noteBehind(_ machine: String, _ c: Connection) async {
        if appCapabilities == nil, let b = binary, let out = try? await runConch(b, ["version", "--json"]) {
            appCapabilities = (try? JSONDecoder().decode(BuildInfo.self, from: out))?.capabilities ?? []
        }
        guard let i = machines.firstIndex(where: { $0.id == machine }) else { return }
        machines[i].behind = capabilitiesBehind(server: c.hello?.capabilities, app: appCapabilities ?? [])
    }

    /// Machines reloading: their connection is tried again at once when it
    /// goes, not after the usual wait.
    private var reloading: Set<String> = []

    /// Makes a machine's connection again: the old one let go first,
    /// so its going is not taken for this one's.
    func reconnect(_ machine: String) {
        if let old = conns[machine] {
            old.onClose = nil
            old.shutdown()
        }
        conns[machine] = nil
        Task { await connect(machine) }
    }

    /// A machine's server re-exec'd onto its binary, as the TUI's m: its
    /// panes stay, the connection drops and is made again.
    func reloadServer(_ machine: String) {
        guard let c = conns[machine] else { return }
        guard c.hello?.capabilities?.contains("server.reload.v1") == true else {
            say("conch on \(label(machine)) is too old to reload from here: run conch server reload there", .err)
            return
        }
        reloading.insert(machine)
        job("Reloading \(label(machine))'s server", done: "\(label(machine))'s server reloaded") {
            struct NoParams: Encodable {}
            defer { self.reloading.remove(machine) }
            do {
                try await c.call("server.reload", NoParams())
            } catch {
                // A refusal is said — most often that it already runs this
                // build, so there is nothing to reload onto; the connection
                // closing under the call is the reload itself.
                guard reloadUnderway(error) else { throw error }
            }
            // Back within a second or two; the row says when.
            for _ in 0..<40 {
                try await Task.sleep(nanoseconds: 250_000_000)
                if self.machines.first(where: { $0.id == machine })?.online == true, self.conns[machine] !== c { return }
            }
            throw ConchError(code: "reload", message: "the server did not come back within 10s: see its log (conch server log)")
        }
    }

    /// conch installed on another machine again from this build, then the
    /// machine reached anew — its server is the new one only after that.
    func upgrade(_ machine: String) {
        guard let b = binary else { return }
        job("Upgrading conch on \(label(machine))", done: "conch on \(label(machine)) upgraded") {
            _ = try await runConch(b, ["machine", "upgrade", machine])
            self.reconnect(machine)
        }
    }

    /// Opens the broadcast sheet for a row's running panes.
    func startBroadcast(_ scope: BroadcastScope, title: String) {
        let m = scope.machine
        guard let c = conns[m], let s = snapshots[m] else { return }
        guard c.hello?.capabilities?.contains("agent.broadcast.v1") == true else {
            say("conch on \(label(m)) is too old to broadcast: reload its server", .err)
            return
        }
        broadcastDraft = BroadcastDraft(scope: scope, title: title, targets: s.broadcastTargets(scope, in: ws))
    }

    /// Sends a broadcast to the panes ticked, and says how it went.
    func sendBroadcast(_ d: BroadcastDraft) {
        broadcastDraft = nil
        let m = d.scope.machine
        guard let c = conns[m] else { return }
        let ticked = d.targets.filter(\.ticked)
        let shells = ticked.contains { $0.agent == nil }
        if shells && c.hello?.capabilities?.contains("agent.broadcast.shells.v1") != true {
            say("conch on \(label(m)) is too old to type into terminals: untick them, or reload its server", .err)
            return
        }
        struct P: Encodable { var ids: [String]; var text: String; var shells: Bool? }
        struct R: Decodable { var results: [BroadcastOutcome]? }
        let names = Dictionary(ticked.map { (unkey($0.key).id, $0.name) }, uniquingKeysWith: { a, _ in a })
        Task {
            do {
                let r = try await c.call("agent.broadcast", P(ids: ticked.map { unkey($0.key).id }, text: d.text, shells: shells ? true : nil), as: R.self)
                let (text, failed) = broadcastSummary(r.results ?? [], names: names)
                self.say(text, failed ? .err : .ok)
            } catch {
                self.say("Broadcast failed: \(error)", .err)
            }
        }
    }

    /// A terminal watched, or no longer, for going quiet or printing — the
    /// TUI's ctrl+b M and A (pane.monitor).
    func toggleMonitor(_ k: String, quiet: Bool) {
        let (m, id) = unkey(k)
        guard let c = conns[m], let p = pane(k) else { return }
        guard c.hello?.capabilities?.contains("pane.monitor.v1") == true else {
            say("conch on \(label(m)) is too old to watch a pane: reload its server", .err)
            return
        }
        let toml = (try? String(contentsOfFile: configFile, encoding: .utf8)) ?? ""
        let next = (p.monitor ?? PaneMonitor()).toggled(quiet: quiet, seconds: silenceAfter(toml: toml))
        struct P: Encodable { var id: String; var activity: Bool?; var silence: Int? }
        let what = quiet ? ((next.silence ?? 0) > 0 ? "alert when it is quiet for \(next.silence!)s after output" : "no alert when it goes quiet")
                         : (next.activity == true ? "alert when it prints while you are elsewhere" : "no alert on output")
        Task {
            do {
                try await c.call("pane.monitor", P(id: id, activity: next.activity, silence: next.silence))
                self.say("\(p.displayName): \(what)")
            } catch {
                self.say("Could not watch \(p.displayName): \(error)", .err)
            }
        }
    }

    /// Removes a branch's worktree, keeping the branch; the server refuses
    /// one with uncommitted work, and says so.
    func removeWorktree(project pk: String, path: String) {
        let (m, pid) = unkey(pk)
        guard let c = conns[m] else { return }
        let a = NSAlert()
        a.messageText = "Remove the worktree?"
        a.informativeText = "\(path)\n\nThe branch stays, with its commits. A worktree with uncommitted changes is not removed."
        a.addButton(withTitle: "Remove")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        struct P: Encodable { var project_id: String; var path: String }
        job("Removing the worktree", done: "Removed \((path as NSString).lastPathComponent)") {
            try await c.call("worktree.remove", P(project_id: pid, path: path))
        }
    }

    /// A project off the sidebar, after asking, as the TUI's x: out of this
    /// workspace when another has it too, else out of conch. Its folder,
    /// branches and worktrees are never touched.
    func removeProject(_ pk: String) {
        let (m, pid) = unkey(pk)
        guard let c = conns[m], let pr = snapshots[m]?.project(pid) else { return }
        let a = NSAlert()
        a.addButton(withTitle: "Remove")
        a.addButton(withTitle: "Cancel")
        switch projectRemoval(pr, machine: m, workspaces: workspaces, current: workspace) {
        case .leaveWorkspace(let id):
            a.messageText = "Take \(pr.name) out of this workspace?"
            a.informativeText = "Other workspaces keep it, and it stays in conch."
            guard a.runModal() == .alertFirstButtonReturn else { return }
            let path = (conchDir as NSString).appendingPathComponent("ui.json")
            do {
                let data = try Workspace.removingProject(Workspace.key(project: pid, machine: m), from: id, in: FileManager.default.contents(atPath: path))
                try data.write(to: URL(fileURLWithPath: path), options: .atomic)
                loadWorkspaces()
                say("Took \(pr.name) out of this workspace", .info)
            } catch {
                self.error = "Could not take the project out of this workspace: \(error)"
            }
        case .removeFromConch:
            a.messageText = "Remove \(pr.name) from the sidebar?"
            a.informativeText = "\(pr.path)\n\nconch forgets the project; its files, branches and worktrees are not touched. Add it again any time."
            guard a.runModal() == .alertFirstButtonReturn else { return }
            struct P: Encodable { var id: String }
            job("Removing \(pr.name)", done: "Removed \(pr.name) from the sidebar") {
                try await c.call("project.remove", P(id: pid))
            }
        }
    }

    /// Deletes a branch and its worktree, after saying what that loses —
    /// the server's own dry run, so nothing is guessed here.
    func deleteBranch(project pk: String, branch: String) {
        let (m, pid) = unkey(pk)
        guard let c = conns[m] else { return }
        struct P: Encodable { var project_id: String; var branch: String; var dry_run: Bool?; var force: Bool? }
        Task {
            let dry: BranchDiscardResult
            do {
                dry = try await c.call("branch.discard", P(project_id: pid, branch: branch, dry_run: true), as: BranchDiscardResult.self)
            } catch {
                self.say("Could not delete \(branch): \(error)", .err)
                return
            }
            let a = NSAlert()
            a.messageText = "Delete \(branch)?"
            a.informativeText = (dry.worktree.map { "Its worktree goes too: \($0)\n\n" } ?? "") + (dry.loss ?? "Nothing is lost: its work is merged or pushed.")
            a.alertStyle = dry.loss == nil ? .informational : .warning
            a.addButton(withTitle: dry.loss == nil ? "Delete" : "Delete anyway")
            a.addButton(withTitle: "Cancel")
            guard a.runModal() == .alertFirstButtonReturn else { return }
            self.job("Deleting \(branch)", done: "Deleted \(branch)") {
                try await c.call("branch.discard", P(project_id: pid, branch: branch, force: dry.loss == nil ? nil : true))
            }
        }
    }

    /// Takes a machine out of machines.json, after asking; its panes there
    /// keep running, since conch on it is not stopped.
    func removeMachine(_ machine: String) {
        guard let b = binary else { return }
        let a = NSAlert()
        a.messageText = "Remove \(label(machine))?"
        a.informativeText = "conch stops reaching it. Its agents and terminals there keep running, and adding the machine again finds them."
        a.addButton(withTitle: "Remove")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        let name = label(machine)
        job("Removing \(name)", done: "Removed \(name)") {
            _ = try await runConch(b, ["machine", "rm", machine])
            self.connect() // machines.json changed: let it go now
        }
    }

    /// A machine's connection went: said on its row, and tried again —
    /// this computer's soon and then less often (a reload is back in a
    /// moment), one elsewhere in a while.
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
        if reloading.contains(machine) {
            // Reloading: back in a moment, so asked again soon.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                guard let self, self.conns[machine] == nil else { return }
                Task { await self.connect(machine) }
            }
            return
        }
        if machine == "local" {
            status = reason.isEmpty ? "The connection to conch's server closed." : reason
            localRetries += 1
            let wait = reconnectDelay(localRetries)
            DispatchQueue.main.asyncAfter(deadline: .now() + wait) { [weak self] in
                guard let self, self.conns["local"] == nil else { return }
                Task { await self.connect("local") }
            }
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
            let before = snap.pane(p.id)
            let was = before?.agent?.state
            snap.upsert(p)
            attention(machine, old: before, new: p)
            if scratch == k, scratchLost(scratch, pane: p) { dropScratch() }
            // Exited on its own — a shell's exit, an ssh logout, an agent's
            // /exit: closed, as the TUI closes it, so it goes from the
            // sidebar and its tab. A TUI open may close it first; the
            // second close finds it gone, which is fine.
            if name == "pane.exited", closesOnExit(p), !keptPanes.contains(key(machine, p.id)), let c = conns[machine] {
                // A TUI open on the machine closes it itself, and knows the
                // panes it keeps that this app cannot tell apart — a custom
                // action's terminal, an installer's — so it is left to it.
                Task {
                    if c.hello?.capabilities?.contains("view.v1") == true,
                       let v = try? await c.call("view.get", as: ViewState.self), !(v.tabs ?? []).isEmpty { return }
                    try? await c.call("pane.close", PaneRefParams(id: p.id))
                }
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
            if scratch == k { dropScratch() }
            layout.close(k)
            for w in parkedTabs.keys { parkedTabs[w]?.close(k) } // other workspaces' tabs too
            restartOnConnect.remove(k)
            terminals.removeValue(forKey: k)?.stop()
            defer { updateBadge() }
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
        case "review.changed":
            // A note answered, sent or resolved elsewhere — an agent's
            // reply above all: the open tab of that branch reads its
            // thread again.
            struct Ref: Decodable { var project_id: String?; var branch: String? }
            if let r = try? dec.decode(Ref.self, from: data) {
                for v in changeViews.values where v.machine == machine && (r.project_id == nil || v.project == r.project_id) && (r.branch == nil || v.branch == r.branch) {
                    Task { await v.loadComments() }
                }
            }
        case "worktree.changed":
            struct Changed: Decodable {
                var project_id: String
                var worktree: String
                var paths: [String]?
            }
            if let ch = try? dec.decode(Changed.self, from: data) {
                for (k, v) in changeViews where v.machine == machine && v.project == ch.project_id && isShown(k) {
                    v.written(ch.paths ?? [], worktree: ch.worktree)
                }
                for (k, v) in fileViews where v.machine == machine && v.project == ch.project_id && isShown(k) {
                    v.written(worktree: ch.worktree)
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
            let q = try? await readQuestion(b, pane: id, machine: m)
            // Only a change is drawn again: read every tick, most reads
            // find the same question.
            if questions[k] != q { questions[k] = q }
        }
    }

    private var questionTimer: Timer?

    /// Reads each waiting agent's question again while it waits. It is
    /// waiting from the moment its hook says so, which is before its menu
    /// is drawn — read only then, the card had no answers and the
    /// paragraph above as its question — and Claude Code asking several
    /// questions stays waiting through all of them, a tab each, so the
    /// first was the only one ever shown.
    private func watchQuestions() {
        questionTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                for (m, s) in self.snapshots where self.conns[m] != nil {
                    for p in s.panes where p.running && p.agent?.state == .blocked { self.ask(key(m, p.id)) }
                }
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

    /// Files dropped on a pane, as the TUI takes a drop (drop.go): their
    /// paths pasted in, as a terminal pastes them — on another machine the
    /// files are uploaded there first (conch -m MACHINE upload) and the
    /// paths they landed at pasted instead, so the agent there can open
    /// them. The pane is checked to be there still once the copy is done.
    func drop(_ paths: [String], on k: String) {
        let (machine, id) = unkey(k)
        guard !paths.isEmpty, let c = conns[machine] else { return }
        struct Paste: Encodable { var id: String; var text: String; var paste: Bool }
        func paste(_ p: [String]) {
            Task { try? await c.call("pane.send_text", Paste(id: id, text: dropText(p), paste: true)) }
            focus(k)
            focusTerminal()
        }
        if machine == "local" {
            paste(paths)
            return
        }
        guard let b = binary else { return }
        let label = self.label(machine)
        status = "Copying \(paths.count == 1 ? (paths[0] as NSString).lastPathComponent : "\(paths.count) files") to \(label)…"
        Task {
            do {
                let out = try await runConch(b, onMachine(machine, ["upload"] + paths))
                let there = uploadedPaths(String(decoding: out, as: UTF8.self))
                status = ""
                guard there.count == paths.count, pane(k)?.running == true else {
                    error = there.isEmpty ? "The files did not reach \(label)" : "Copied to \(label), but the pane has closed · nothing pasted"
                    return
                }
                paste(there)
            } catch {
                status = ""
                self.error = "Could not copy to \(label): \(error)"
            }
        }
    }

    /// Hands a machine's server the autopilot policy from config.toml's
    /// [verify] (conch autopilot push), as the TUI does when it connects:
    /// with only this app open, a server acted on whatever a TUI last gave
    /// it, and on nothing where no TUI had run.
    func pushAutopilot(_ machine: String) {
        guard let b = binary, conns[machine]?.hello?.capabilities?.contains("autopilot.v1") == true else { return }
        Task { _ = try? await runConch(b, onMachine(machine, ["autopilot", "push"])) }
    }

    /// The policy to every server reached, after Settings saved a change.
    func pushAutopilotAll() {
        for m in machines where m.state.isEmpty { pushAutopilot(m.id) }
    }

    /// [notify] as config.toml has it, read again with the theme.
    private(set) var notifySettings = NotifySettings()
    /// [ui] cost: what agents spend, on the tree's rows and in pane headers.
    @Published private(set) var showCost = true
    /// config.toml's [app]: where the tabs go, density, glass, ports.
    @Published private(set) var appLook = AppLook()
    /// Told when [app] changes, for what AppKit draws (the toolbar, the
    /// global option-space).
    var onAppLook: ((AppLook) -> Void)?
    /// Told when the themes of your own change, for the Theme menu.
    var onThemeList: (() -> Void)?
    /// What was wrong with the theme files left out, for the Theme menu.
    var userThemeProblems: [String] = []
    /// The scratch layer on screen: its pane's key, floating over the
    /// splits (nil: hidden). Scratch.swift.
    @Published var scratch: String?
    /// The updates sheet, opened from the status bar's warn-coloured item.
    @Published var updatesOpen = false
    /// The scratch pane being started, so a second option-space while it
    /// starts does not start another.
    var scratchStarting: String?
    /// The query a raw pane's Ghostty search was last given, so Return
    /// moves to the next match rather than searching again.
    var rawSearched: String?

    /// The usage chip for a row's panes ("" with none, or cost turned off).
    func costChip(machine m: String, project: String? = nil, agentsOnly: Bool = false, pane: String? = nil) -> String {
        guard showCost, let s = snapshots[m] else { return "" }
        var panes = s.shown(in: ws, machine: m)
        if let project { panes = panes.filter { $0.projectID == project } }
        if agentsOnly { panes = panes.filter { $0.agent != nil } }
        if let pane { panes = panes.filter { $0.id == pane } }
        return usage(of: panes).chip
    }

    /// Tells you an agent needs you, when it has just come to wait or
    /// finished and you are not looking at it (attentionAlert), and keeps
    /// the Dock's badge. A TUI open on that machine notifies itself, so the
    /// app leaves the notification to it rather than saying it twice.
    func attention(_ machine: String, old: PaneInfo?, new p: PaneInfo) {
        let k = key(machine, p.id)
        let looking = NSApp.isActive && (mainWindow?.isKeyWindow ?? false) && layout.focused == k
        let newly = p.agent?.state == .blocked && old?.agent?.state != .blocked
        defer { updateBadge(newlyWaiting: newly) }
        guard let alert = attentionAlert(old: old, new: p, machine: machine == "local" ? nil : label(machine),
                                         viewing: looking, settings: notifySettings),
              let c = conns[machine] else { return }
        let s = notifySettings
        Task {
            if c.hello?.capabilities?.contains("view.v1") == true,
               let v = try? await c.call("view.get", as: ViewState.self), !(v.tabs ?? []).isEmpty { return }
            Notifier.shared.post(alert, pane: k, sound: s.sound, desktop: s.desktop)
        }
    }

    /// Tells you a plan limit has passed a threshold of [notify] limit_at,
    /// as the TUI does (limitalerts.go): once per threshold per window. The
    /// app used to leave this to the TUI alone, so with only the app open a
    /// plan could reach 90% with nothing said. A TUI open on this computer
    /// raises its own, so the notification is left to it.
    func alertLimits() {
        let s = notifySettings
        guard s.limits else { return }
        let now = Date()
        var seen = pruneLimitAlerts(limitSeen, now: now)
        var alerts: [LimitAlert] = []
        for l in limits { alerts += limitCrossings(&seen, machine: "local", l, thresholds: s.limitAt, now: now) }
        if seen != limitSeen {
            limitSeen = seen
            UserDefaults.standard.set(seen, forKey: "conch.limitSeen")
        }
        guard !alerts.isEmpty else { return }
        let texts = alerts.map { $0.text(now: now) }
        // Red from 90% used, whichever threshold was crossed to say it.
        say(texts.joined(separator: " · "), alerts.contains { $0.pct >= 90 } ? .err : .info)
        guard s.enabled, !s.quiet(now), let c = conns["local"] else { return }
        let alert = Alert(title: "conch · plan limit", body: texts.joined(separator: "\n"))
        Task {
            if c.hello?.capabilities?.contains("view.v1") == true,
               let v = try? await c.call("view.get", as: ViewState.self), !(v.tabs ?? []).isEmpty { return }
            Notifier.shared.post(alert, id: "limits", sound: s.sound, desktop: s.desktop)
        }
    }

    /// The Dock's badge: agents waiting, on every machine and workspace.
    func updateBadge(newlyWaiting: Bool = false) {
        let n = machines.reduce(0) { acc, m in acc + (snapshots[m.id]?.panes.filter { $0.agent?.state == .blocked && $0.running }.count ?? 0) }
        Notifier.shared.badge(waiting: n, newlyWaiting: newlyWaiting)
    }

    /// A boundary between splits dragged (SplitStack).
    // MARK: find in a pane

    /// ⌘F on the focused pane: its find bar, the field taking the keyboard
    /// (FindBar), or the same bar again with what was typed.
    func openFind() {
        guard let k = layout.focused, isTerminalKey(k) else { NSSound.beep(); return }
        if find?.pane != k {
            if let old = find { endFind(old.pane) }
            find = PaneFind(pane: k)
            rawSearched = nil
        }
        findFocus &+= 1
    }

    /// The next match, older (back) or newer, from the last one: the
    /// server searches, and the pane's view is told where to look.
    func findNext(back: Bool) {
        guard let f = find, !f.query.isEmpty else { openFind(); return }
        // A raw pane's history is Ghostty's, and so is the search.
        if let t = terminals[f.pane], t.raw {
            if rawSearched != f.query {
                rawSearched = f.query
                t.search(f.query)
            } else {
                t.searchNext(back: back)
            }
            return
        }
        let (machine, id) = unkey(f.pane)
        guard let c = connection(machine) else { return }
        guard c.hello?.capabilities?.contains("pane.search.v1") == true else {
            find?.failed("conch on \(label(machine)) is too old to search a pane: reload its server")
            return
        }
        let params = f.params(id: id, back: back)
        Task {
            do {
                let r = try await c.call("pane.search", params, as: PaneSearchResult.self)
                // Closed, or another query typed, while it looked.
                guard var now = self.find, now.pane == f.pane, now.query == params.query else { return }
                let show = now.took(r, back: back)
                self.find = now
                self.terminals[f.pane]?.control(show)
            } catch {
                self.find?.failed("\(error)")
            }
        }
    }

    /// The bar closed: nothing marked, and the keyboard back in the pane.
    func closeFind() {
        if let f = find { endFind(f.pane) }
        find = nil
        rawSearched = nil
        focusTerminal()
    }

    /// Nothing marked in a pane any more: Ghostty's search ended for a raw
    /// one, attach's marks cleared otherwise.
    private func endFind(_ pane: String) {
        guard let t = terminals[pane] else { return }
        if t.raw { t.search(nil) } else { t.control(AttachControl(clear: true)) }
    }

    /// Every pane running in every conch server this window reaches: what
    /// quitting leaves running.
    var runningPanes: Int {
        snapshots.values.reduce(0) { $0 + $1.panes.filter(\.running).count }
    }

    /// Names a tab, as the TUI's tab rename does; empty names it by its
    /// pane again.
    func renameTab(_ i: Int) {
        guard layout.tabs.indices.contains(i),
              let name = ask("Rename tab \(i + 1)", info: "Leave it empty to name it by its pane again.", value: layout.tabs[i].name ?? "", button: "Rename")
        else { return }
        layout.rename(i, to: name)
    }

    func resizeSplit(_ path: String, n: Int, after i: Int, by delta: Double) {
        guard layout.tabs.indices.contains(layout.active) else { return }
        layout.tabs[layout.active].resize(path, n: n, after: i, by: delta)
    }

    /// Whether a tab or split shows k, in this workspace or another's.
    func isShown(_ k: String) -> Bool {
        layout.tab(showing: k) != nil || parkedTabs.values.contains { $0.tab(showing: k) != nil }
    }

    /// Panes whose `conch attach` ended while their machine was away —
    /// a server reloading closes every connection, attach's with it — to
    /// be started again once it is back.
    private var restartOnConnect: Set<String> = []

    /// A pane's `conch attach` ended. If the pane is still there (its
    /// machine reloaded, a bridge dropped) a new one is started, so the
    /// view is live again rather than the last screen for ever. One that
    /// ended within seconds of starting is left ended: restarting it would
    /// only end again, as fast as it can.
    func terminalEnded(_ k: String, lived: TimeInterval) {
        guard lived >= 2, isShown(k) else { return }
        let machine = unkey(k).machine
        guard conns[machine] != nil else {
            restartOnConnect.insert(k)
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
            guard let self, self.terminals[k]?.ended != nil else { return }
            if self.pane(k)?.running == true { self.restartTerminal(k) } else { self.restartOnConnect.insert(k) }
        }
    }

    private func restartTerminal(_ k: String) {
        let hadKeyboard = terminalHasKeyboard && layout.focused == k
        terminals.removeValue(forKey: k)?.stop()
        objectWillChange.send() // its card asks for a new one
        if hadKeyboard { focusTerminal() }
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

    private(set) var fileViews: [String: FilesModel] = [:]

    private var setupViews: [String: SetupModel] = [:]
    func setup(for k: String) -> SetupModel {
        if let m = setupViews[k] { return m }
        let m = SetupModel(key: k, store: self)
        setupViews[k] = m
        return m
    }

    private(set) var browserViews: [String: BrowserModel] = [:]
    private var blankBrowsers = 0

    func browser(for k: String) -> BrowserModel {
        if let m = browserViews[k] { return m }
        let m = BrowserModel(key: k, store: self)
        browserViews[k] = m
        return m
    }

    /// A page in a tab of its own beside the agents; nil for an empty one
    /// to type an address into. A page already open is shown, not opened twice.
    func openBrowser(_ url: String?) {
        let k: String
        if let url {
            k = browserKey(url)
        } else {
            blankBrowsers += 1
            k = browserKey("about:blank#\(blankBrowsers)")
        }
        layout.open(k)
        terminalHasKeyboard = false
    }

    func files(for k: String) -> FilesModel {
        if let m = fileViews[k] { return m }
        let m = FilesModel(key: k, store: self)
        fileViews[k] = m
        return m
    }

    /// A project's files, in a tab of their own — what the Files row opens.
    /// A tab opened again reads again.
    func openFiles(project pk: String) {
        let (m, pid) = unkey(pk)
        let k = filesKey(m, pid)
        if let v = fileViews[k] { v.load() }
        layout.open(k)
        terminalHasKeyboard = false
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
        // A new one reads as it is made, and takes the branch when its
        // reply comes; one kept from before (its tab closed, or open) reads
        // again, or a graph reopened after a commit showed the old history.
        let fresh = commitViews[k] == nil
        let model = commits(for: k)
        model.focusBranch = branch
        if !fresh { model.refresh() } // and ask GitHub: a new one does as it is made
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
        let k = changesKey(m, pid, branch)
        // One kept from before is not made again, so it asks here: its pull
        // request as GitHub has it now, not at the server's next turn.
        if changeViews[k] != nil { refreshProject(machine: m, project: pid) }
        layout.open(k)
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
        // Closing a split hides a pane, never ends it (closePane does,
        // after asking) — said for an agent, which goes on working out of
        // sight, as the TUI would have asked before ending it.
        if let pi = pane(p), pi.agent != nil, pi.running {
            say("\(pi.displayName) keeps running: open it again from the sidebar", .info)
        }
        layout.close(p)
        terminals.removeValue(forKey: p)?.stop()
        changeViews.removeValue(forKey: p)
        if let b = browserViews.removeValue(forKey: p) { b.web.stopLoading(); b.web.loadHTMLString("", baseURL: nil) } // its page goes quiet
        focusTerminal()
    }

    /// Closes every split of the tab on screen — the TUI's ctrl+b &. As a
    /// split closing, its panes keep running, in the sidebar to open again.
    func closeTab() {
        guard let t = layout.current else { return }
        let agents = t.root.panes.compactMap { pane($0) }.filter { $0.agent != nil && $0.running }
        if !agents.isEmpty {
            say(agents.count == 1 ? "\(agents[0].displayName) keeps running: open it again from the sidebar"
                                  : "\(agents.count) agents keep running: open them again from the sidebar", .info)
        }
        for p in t.root.panes {
            layout.close(p)
            terminals.removeValue(forKey: p)?.stop()
            changeViews.removeValue(forKey: p)
        }
        focusTerminal()
    }

    /// The next agent waiting for you, on any machine: waiting ones first,
    /// then the ones that finished, each machine in the sidebar's order.
    /// The done count clicked: the next finished agent in the workspace,
    /// on any machine, round again at the end.
    func nextDone() {
        var order: [String] = []
        for m in machines {
            var seen: Set<String> = []
            var next = snapshots[m.id]?.nextDone(after: nil, in: ws, machine: m.id)
            while let n = next, !seen.contains(n) {
                seen.insert(n)
                order.append(key(m.id, n))
                next = snapshots[m.id]?.nextDone(after: n, in: ws, machine: m.id)
            }
        }
        guard !order.isEmpty else { return }
        let i = layout.focused.flatMap { order.firstIndex(of: $0) }.map { ($0 + 1) % order.count } ?? 0
        open(order[i])
    }

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
        case "newSandbox": newSandboxOpen = true
        case "newTask": newTaskHere()
        case "startAgent": startAgentHere()
        case "newTab": newTerminal(split: nil)
        case "splitRight": newTerminal(split: .horizontal)
        case "splitDown": newTerminal(split: .vertical)
        case "closeSplit": closeSplit()
        case "nextWaiting": nextWaiting()
        case "find": openFind()
        case "shortcuts": helpOpen = true
        case "welcome": welcomeOpen = true
        case "doctor": doctorOpen = true
        case "skillsSettings": openSkillsSettings("local")
        case "findOlder": findNext(back: true)
        case "findNewer": findNext(back: false)
        case "zoomSplit": layout.toggleZoom(); focusTerminal()
        case "splitLeft": if layout.move(.left) { focusTerminal() }
        case "splitRightward": if layout.move(.right) { focusTerminal() }
        case "splitAbove": if layout.move(.up) { focusTerminal() }
        case "splitBelow": if layout.move(.down) { focusTerminal() }
        case "nextTab": layout.step(1); focusTerminal()
        case "previousTab": layout.step(-1); focusTerminal()
        case "toggleSidebar": sidebarVisible.toggle()
        case "newBrowser": openBrowser(nil)
        case "closeWorkspace":
            if workspace > 0 { closeWorkspace(workspace) } else { say("The first workspace stays: it holds what no other has", .info) }
        case "biggerFont": stepFontSize(bigger: true)
        case "smallerFont": stepFontSize(bigger: false)
        case "defaultFont": stepFontSize(bigger: nil)
        case "biggerText": stepTextSize(bigger: true)
        case "smallerText": stepTextSize(bigger: false)
        case "defaultText": stepTextSize(bigger: nil)
        case "openTUI": openTUI()
        case "scratch": toggleScratch()
        case "dockScratch": dockScratch()
        case "scratchTab": scratchToTab()
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
        let s = snapshots[m] ?? Snapshot()
        var items = s.menu(for: node, machine: m, machineOnline: online, label: refLabel(m), among: catalog)
        // A sandbox's own: start, stop, delete, what it cost.
        if case .machine = node.kind, let link = machines.first(where: { $0.id == m }), link.sandbox {
            items += sandboxMenu(machine: m, running: link.online)
        }
        // The actions of your own that belong on this kind of row.
        if online, let (place, _) = s.actionPlace(node, machine: m, home: conns[m]?.hello?.home) {
            let mine = actions.filter { $0.offers(place) }
            if !mine.isEmpty { items += [RowMenuItem("", .divider)] + mine.map { RowMenuItem($0.name, .runAction($0.name)) } }
        }
        return items
    }

    /// config.toml's [[actions]], read with the theme.
    @Published private(set) var actions: [CustomAction] = []
    /// Panes an action with keep runs in: left open when they exit, so
    /// what the command printed can be read.
    private var keptPanes: Set<String> = []

    /// Runs an action where a row is, in a terminal of its own, as the TUI
    /// does: `sh -lc RUN` in the row's folder with conch's names for it.
    func runCustomAction(_ name: String, on node: TreeNode) {
        guard let a = actions.first(where: { $0.name == name }), let m = node.machine, let c = conns[m],
              let (_, at) = snapshots[m]?.actionPlace(node, machine: m, home: c.hello?.home) else { return }
        guard let dir = at.dir, !dir.isEmpty else {
            say(at.branch.isEmpty ? "Nothing here to run \(a.name) in" : "\(at.branch) has no worktree to run in: open a terminal on it first", .err)
            return
        }
        struct Create: Encodable {
            var name: String
            var cwd: String
            var command: [String]
            var env: [String]
            var no_project: Bool?
            var cols: Int?
            var rows: Int?
        }
        let size = newPaneSize()
        let params = Create(name: a.name + " · " + at.label, cwd: dir, command: ["/bin/sh", "-lc", a.run],
                            env: at.env.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }, no_project: at.loose ? true : nil,
                            cols: size?.cols, rows: size?.rows)
        Task {
            do {
                let p = try await c.call("pane.create", params, as: PaneInfo.self)
                if a.keep { keptPanes.insert(key(m, p.id)) }
                snapshots[m, default: Snapshot()].upsert(p)
                joinWorkspace(machine: m, pane: p)
                layout.open(key(m, p.id))
                focusTerminal()
            } catch {
                say("Could not run \(a.name): \(error)", .err)
            }
        }
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
            say("Copied \(text.count > 60 ? String(text.prefix(57)) + "…" : text)")
        case .addProject(let m): addProject(on: m)
        case .sshToHost: sshToHost()
        case .addMachine: addMachine()
        case .newSandbox: newSandboxOpen = true
        case .reconnect(let m): reconnect(m)
        case .reloadServer(let m): reloadServer(m)
        case .upgrade(let m): upgrade(m)
        case .removeMachine(let m): removeMachine(m)
        case .skillsSettings(let m): openSkillsSettings(m)
        case .openURL(let s): if let u = openableURL(s) { NSWorkspace.shared.open(u) }
        case .removeWorktree(let pk, let path): removeWorktree(project: pk, path: path)
        case .removeProject(let pk): removeProject(pk)
        case .moveProject(let pk): moveProjectTarget = MoveProjectTarget(id: pk)
        case .deleteBranch(let pk, let b): deleteBranch(project: pk, branch: b)
        case .monitor(let k, let quiet): toggleMonitor(k, quiet: quiet)
        case .broadcast(let scope): startBroadcast(scope, title: node.title)
        case .runAction(let name): runCustomAction(name, on: node)
        case .connectHost(let h): connectSaved(h)
        case .sandbox(let m, let verb): sandbox(m, verb)
        case .agentSetup(let pk, let dir):
            let (m, pid) = unkey(pk)
            let k = setupKey(m, pid, dir: dir)
            setupViews[k]?.load()
            layout.open(k)
            terminalHasKeyboard = false
        case .forgetHost(let h): saveHost(SavedHost(host: h), save: false)
        case .saveHost(let k):
            if let p = pane(k), let h = p.sshTarget { saveHost(SavedHost(host: h, args: p.sshArgs)) }
        }
    }

    // MARK: projects, ssh hosts and machines

    /// Adds a project, as the TUI's a: on this computer a folder picked in
    /// the Finder's panel; on another machine a path typed in, since a Mac
    /// panel cannot browse it. Added from a workspace after the first, the
    /// project is put in it, as the TUI does.
    /// Add project…: a sheet to open a folder, clone a repository or make
    /// a new one (AddProjectSheet), for that machine.
    func addProject(on machine: String = "local") {
        guard conns[machine] != nil else {
            error = "\(label(machine)) is not connected"
            return
        }
        addProjectTarget = AddProjectTarget(id: machine)
    }

    /// The sheet's work: project.add, project.clone or project.create, then
    /// the project joins the workspace on screen. Throws what the server
    /// said, for the sheet to show.
    func addProject(machine: String, mode: AddProjectMode, path: String, url: String) async throws {
        guard let c = conns[machine] else { throw ConchError(code: "closed", message: "\(label(machine)) is not connected") }
        struct Add: Encodable { var path: String }
        struct Clone: Encodable { var url: String; var path: String }
        struct Create: Encodable { var path: String; var git: Bool }
        let info: ProjectInfo
        switch mode {
        case .folder: info = try await c.call("project.add", Add(path: path), as: ProjectInfo.self)
        case .clone: info = try await c.call("project.clone", Clone(url: url, path: path), as: ProjectInfo.self)
        case .create: info = try await c.call("project.create", Create(path: path, git: true), as: ProjectInfo.self)
        }
        joinWorkspace(machine: machine, project: info.id)
        status = mode == .clone ? "Cloned and added \(info.name)" : "Added \(info.name)"
    }

    /// A project added from a workspace after the first joins it (ui.json).
    func joinWorkspace(machine: String, project id: String) {
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
    /// ui.json's saved ssh hosts, as the TUI keeps them.
    @Published private(set) var savedHosts: [SavedHost] = []

    /// A sandbox started, stopped or deleted (after asking), or what it has
    /// cost shown — conch sandbox, which talks to its provider.
    func sandbox(_ m: String, _ verb: String) {
        guard let b = binary, let link = machines.first(where: { $0.id == m }), let p = link.provider else { return }
        let name = link.label
        if verb == "stop" || verb == "rm" {
            let a = NSAlert()
            a.messageText = verb == "rm" ? "Delete \(name) for good?" : "Stop \(name)?"
            a.informativeText = verb == "rm" ? "The sandbox and everything on it are deleted at \(p); nothing brings it back." : "Its agents and terminals stop with it. Starting it again brings the sandbox back, not what was running."
            a.addButton(withTitle: verb == "rm" ? "Delete" : "Stop")
            a.addButton(withTitle: "Cancel")
            if verb == "rm" { a.buttons.first?.hasDestructiveAction = true }
            guard a.runModal() == .alertFirstButtonReturn else { return }
        }
        let args = sandboxArgs(provider: p, verb: verb, machine: m)
        if verb == "usage" {
            job("Asking \(p) what \(name) cost") {
                let out = String(decoding: try await runConch(b, args), as: UTF8.self)
                let a = NSAlert()
                a.messageText = "What \(name) has cost"
                a.informativeText = out.trimmingCharacters(in: .whitespacesAndNewlines)
                a.runModal()
            }
            return
        }
        let doing = ["start": "Starting", "stop": "Stopping", "rm": "Deleting"][verb] ?? verb
        let did = ["start": "Started", "stop": "Stopped", "rm": "Deleted"][verb] ?? verb
        job("\(doing) \(name)", done: "\(did) \(name)") {
            _ = try await runConch(b, args)
            if verb == "start" { self.reconnect(m) } else { self.connect() }
        }
    }

    /// Logs in to a saved host again, with the options it was saved with.
    func connectSaved(_ host: String) {
        guard let b = binary, let h = savedHosts.first(where: { $0.host == host }) else { return }
        Task {
            do {
                let out = try await runConch(b, ["ssh", h.host] + h.args)
                let id = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !id.isEmpty else { return }
                layout.open(key("local", id))
                if let p = pane(key("local", id)) { joinWorkspace(machine: "local", pane: p) }
                focusTerminal()
            } catch {
                say("Could not connect to \(h.title): \(error)", .err)
            }
        }
    }

    /// Saves a host to log in to again (save) or forgets it, in ui.json,
    /// where the TUI lists it too.
    func saveHost(_ h: SavedHost, save: Bool = true) {
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try savingHost(h, save: save, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            say("Could not \(save ? "save" : "forget") \(h.host): \(error)", .err)
            return
        }
        loadWorkspaces()
        say(save ? "Saved \(h.host): it stays under CLI to connect again" : "Forgot \(h.host)")
    }

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

    /// Brings a pane on this Mac to the front: the agent adapting a
    /// provider, from the sheet that started it.
    func showLocalPane(_ id: String) {
        layout.open(key("local", id))
        focusTerminal()
    }

    /// New Sandbox…, the sheet that makes one (NewSandboxSheet).
    @Published var newSandboxOpen = false

    /// conch run with args in a terminal of its own on this Mac, opened in
    /// a tab, so what it does — an install, a sandbox being made — is
    /// watched and its questions answered.
    func runCLIPane(name: String, args: [String]) {
        guard let b = binary, let c = conns["local"] else { return }
        struct Create: Encodable {
            var name: String
            var command: [String]
            var cwd: String
            var no_project: Bool
        }
        Task {
            do {
                let p = try await c.call("pane.create", Create(name: name, command: [b] + args, cwd: NSHomeDirectory(), no_project: true), as: PaneInfo.self)
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
            var cols: Int?
            var rows: Int?
        }
        let size = newPaneSize()
        Task {
            do {
                let p = try await c.call("pane.create", Create(agent: agent, cwd: cwd, no_project: inProject ? nil : true,
                                                               cols: size?.cols, rows: size?.rows), as: PaneInfo.self)
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
    /// A project's open issues, for the task sheet's picker; none from a
    /// server without project.issues.v1, or a project not on GitHub.
    func issues(for pk: String) async -> [IssueInfo] {
        let (m, pr) = unkey(pk)
        guard let c = conns[m], c.hello?.capabilities?.contains("project.issues.v1") == true else { return [] }
        struct P: Encodable { var project_id: String; var limit: Int }
        struct R: Decodable { var issues: [IssueInfo]? }
        return (try? await c.call("project.issues", P(project_id: pr, limit: 50), as: R.self))?.issues ?? []
    }

    func startTask(_ d: TaskDraft) {
        let (m, pr) = unkey(d.project)
        guard let c = conns[m] else { return }
        // An issue or a time other than now: `conch task`, which reads the
        // issue and the time's words the way the TUI does.
        if (d.issue ?? 0) > 0 || !taskWhenArgs(d.when).isEmpty {
            guard let b = binary, let dir = snapshots[m]?.project(pr)?.path else { return }
            taskDraft = nil
            let args = onMachine(m, taskArgs(dir: dir, prompt: d.prompt, agent: d.agent, branch: d.branch, issue: d.issue, when: d.when))
            job(d.when.isEmpty ? "Starting the task" : "Scheduling the task") {
                let out = String(decoding: try await runConch(b, args), as: UTF8.self)
                let said = out.split(separator: "\n").last.map(String.init) ?? ""
                self.say(said.isEmpty ? "Task started" : said)
            }
            return
        }
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

    /// Closes a workspace, as the TUI's ctrl+b X does, after asking: what
    /// it held goes to the first workspace, and an agent or terminal only
    /// it had is moved there too or ended, as asked. ui.json carries it to
    /// the TUI, which drops the workspace within a second.
    func closeWorkspace(_ i: Int) {
        guard i > 0, workspaces.indices.contains(i) else { return }
        let w = workspaces[i]
        let label = Workspace.label(i, w)
        let running = machines.flatMap { m in
            (snapshots[m.id]?.panes ?? []).filter { $0.running && w.panes.contains(key(m.id, $0.id)) }.map { (m.id, $0) }
        }
        let a = NSAlert()
        a.messageText = "Close workspace \(label)?"
        let agents = running.filter { $0.1.agent != nil }.count
        let what = [agents > 0 ? "\(agents) agent\(agents == 1 ? "" : "s")" : nil,
                    running.count - agents > 0 ? "\(running.count - agents) terminal\(running.count - agents == 1 ? "" : "s")" : nil]
            .compactMap { $0 }.joined(separator: " and ")
        if running.isEmpty {
            a.informativeText = "Its projects, machines, hosts and tabs move to workspace 1. Nothing is running in it."
            a.addButton(withTitle: "Close")
        } else {
            a.informativeText = "Its projects, machines, hosts and tabs move to workspace 1. \(what) run in it."
            a.addButton(withTitle: "Move \(what) to workspace 1")
            a.addButton(withTitle: "End \(what), and close it")
        }
        a.addButton(withTitle: "Cancel")
        let r = a.runModal()
        let end: Bool
        switch r {
        case .alertFirstButtonReturn: end = false
        case .alertSecondButtonReturn where !running.isEmpty: end = true
        default: return
        }
        if end {
            struct Ref: Encodable { var id: String }
            for (m, p) in running {
                guard let c = conns[m] else { continue }
                Task { try? await c.call("pane.close", Ref(id: p.id)) }
            }
        }
        let path = (conchDir as NSString).appendingPathComponent("ui.json")
        do {
            let data = try Workspace.closing(w.id, in: FileManager.default.contents(atPath: path))
            try data.write(to: URL(fileURLWithPath: path), options: .atomic)
        } catch {
            self.error = "Could not close the workspace: \(error)"
            return
        }
        let was = workspace
        loadWorkspaces()
        workspace = was == i ? 0 : was > i ? was - 1 : was
        say("Closed workspace \(label)")
    }

    func loadWorkspaces() {
        let data = FileManager.default.contents(atPath: (conchDir as NSString).appendingPathComponent("ui.json"))
        workspaces = Workspace.load(uiJSON: data)
        tuiFolds = loadFolds(uiJSON: data)
        let hosts = loadSavedHosts(uiJSON: data)
        if hosts != savedHosts { savedHosts = hosts }
        let dismissed = loadQueueDismissed(uiJSON: data)
        if dismissed != queueDismissed { queueDismissed = dismissed }
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
    /// A GitHub issue to work on (its title and body are the prompt).
    var issue: Int?
    /// When it starts: "" now, else as `conch task -at` and the rest take it.
    var when = ""
}

private struct PaneRefParams: Encodable { var id: String }

/// The theme on screen, apart from the store, so that what observes it is
/// not redrawn by everything else the store publishes.
@MainActor
final class ShownTheme: ObservableObject {
    @Published var theme = ConchTheme.conch
}
