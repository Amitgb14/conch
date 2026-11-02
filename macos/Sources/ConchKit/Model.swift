import Foundation

/// What the server has said about this machine: its panes and projects,
/// kept up to date from events. Everything the window shows is worked out
/// from this, so it is a value the tests can build by hand.
public struct Snapshot: Equatable, Sendable {
    public var panes: [PaneInfo] = []
    public var projects: [ProjectInfo] = []

    public init(panes: [PaneInfo] = [], projects: [ProjectInfo] = []) {
        self.panes = panes
        self.projects = projects
    }

    public func pane(_ id: String) -> PaneInfo? { panes.first { $0.id == id } }
    public func project(_ id: String) -> ProjectInfo? { projects.first { $0.id == id } }

    /// A pane was created or changed: replace it where it is, or add it.
    public mutating func upsert(_ p: PaneInfo) {
        if let i = panes.firstIndex(where: { $0.id == p.id }) {
            panes[i] = p
        } else {
            panes.append(p)
        }
    }

    public mutating func remove(pane id: String) { panes.removeAll { $0.id == id } }

    public mutating func upsert(_ p: ProjectInfo) {
        if let i = projects.firstIndex(where: { $0.id == p.id }) {
            projects[i] = p
        } else {
            projects.append(p)
        }
    }

    public mutating func remove(project id: String) { projects.removeAll { $0.id == id } }

    /// Waiting for you, and finished while nobody looked — the counts in
    /// the toolbar, as the TUI's status bar counts them (inboxCount).
    public func inbox(in ws: Workspace? = nil) -> (waiting: Int, done: Int) {
        var waiting = 0, done = 0
        for p in panes where ws?.shows(p) ?? true {
            switch p.agent?.state {
            case .blocked: waiting += 1
            case .done: done += 1
            default: break
            }
        }
        return (waiting, done)
    }

    /// The agent to go to next: waiting ones first, then finished ones, each
    /// oldest first (attentionOrder), after the one given.
    public func nextAttention(after current: String?, in ws: Workspace? = nil) -> String? {
        func rank(_ p: PaneInfo) -> Int? {
            switch p.agent?.state {
            case .blocked: return 0
            case .done: return 1
            default: return nil
            }
        }
        let order = panes.filter { (ws?.shows($0) ?? true) && rank($0) != nil }.sorted {
            let (a, b) = (rank($0)!, rank($1)!)
            if a != b { return a < b }
            return ($0.agent?.since ?? .distantPast) < ($1.agent?.since ?? .distantPast)
        }
        guard !order.isEmpty else { return nil }
        if let cur = current, let i = order.firstIndex(where: { $0.id == cur }) {
            return order[(i + 1) % order.count].id
        }
        return order[0].id
    }
}

// MARK: - Machines

/// A pane, project or branch is named within its machine — this computer's
/// p3 and busybox's p3 are different panes — so the window names them
/// "machine|id". A name without a machine is this computer's.
public func key(_ machine: String, _ id: String) -> String { "\(machine)|\(id)" }

public func unkey(_ k: String) -> (machine: String, id: String) {
    guard let bar = k.firstIndex(of: "|") else { return ("local", k) }
    return (String(k[..<bar]), String(k[k.index(after: bar)...]))
}

/// A machine conch reaches besides this computer (machines.json), as the
/// TUI lists it.
public struct MachineEntry: Equatable, Sendable, Decodable {
    public var id: String
    public var label: String
    public var target: String?
    public var enabled: Bool?

    public init(id: String, label: String, target: String? = nil, enabled: Bool? = true) {
        self.id = id
        self.label = label
        self.target = target
        self.enabled = enabled
    }

    /// The provider when this machine is a hosted sandbox (its target is
    /// "provider:id", as remote.ParseSandboxTarget reads it), else nil.
    public var sandboxProvider: String? {
        guard let t = target, let colon = t.firstIndex(of: ":") else { return nil }
        let p = String(t[..<colon]), id = t[t.index(after: colon)...]
        guard ["daytona", "boat", "devin"].contains(p), !id.isEmpty, !id.contains(where: { "@/: ".contains($0) }) else { return nil }
        return p
    }

    /// What the sidebar calls it: a sandbox says which provider's it is,
    /// as the mock's "sandbox · daytona".
    public var displayLabel: String {
        sandboxProvider.map { "\(label) · \($0)" } ?? label
    }

    /// The enabled machines in a machines.json; none for a file that is
    /// missing or will not read.
    public static func load(machinesJSON data: Data?) -> [MachineEntry] {
        struct File: Decodable { var machines: [MachineEntry]? }
        guard let data, let f = try? JSONDecoder().decode(File.self, from: data) else { return [] }
        return (f.machines ?? []).filter { $0.enabled != false && $0.id != "local" && !$0.id.isEmpty }
    }
}

/// A workspace as the TUI keeps it (ui.json): the first holds everything,
/// another only what was added to it. The app reads them and switches
/// between them; making and changing them stays the TUI's.
public struct Workspace: Equatable, Sendable {
    public var name: String
    /// nil is the first workspace, which shows everything. Projects are
    /// the TUI's keys: an id on this computer, "machine~id" elsewhere.
    public var projects: Set<String>?
    /// Panes added to it by hand, as "machine|id".
    public var panes: Set<String>

    public init(name: String, projects: Set<String>? = nil, panes: Set<String> = []) {
        self.name = name
        self.projects = projects
        self.panes = panes
    }

    static func projectKey(_ machine: String, _ id: String) -> String {
        machine == "local" ? id : "\(machine)~\(id)"
    }

    public func shows(_ p: PaneInfo, machine: String = "local") -> Bool {
        guard let projects else { return true }
        if let pid = p.projectID, projects.contains(Self.projectKey(machine, pid)) { return true }
        return panes.contains(key(machine, p.id))
    }

    public func shows(_ p: ProjectInfo, machine: String = "local") -> Bool {
        projects?.contains(Self.projectKey(machine, p.id)) ?? true
    }

    /// The workspaces in a ui.json: the first, then those saved after it.
    public static func load(uiJSON data: Data?) -> [Workspace] {
        struct Saved: Decodable {
            var name: String?
            var projects: [String]?
            var panes: [String]?
        }
        struct UI: Decodable { var spaces: [Saved]? }
        var out = [Workspace(name: "")]
        if let data, let ui = try? JSONDecoder().decode(UI.self, from: data) {
            for s in ui.spaces ?? [] {
                out.append(Workspace(name: s.name ?? "", projects: Set(s.projects ?? []), panes: Set(s.panes ?? [])))
            }
        }
        return out
    }

    /// What a workspace's button says: its number, and its name when it
    /// has one (the TUI's spaceLabel).
    public static func label(_ i: Int, _ ws: Workspace) -> String {
        ws.name.isEmpty ? String(i + 1) : "\(i + 1) \(ws.name)"
    }
}

// MARK: - The sidebar

public enum NodeKind: Equatable, Sendable {
    case machine(String)
    case project(String) // key(machine, project)
    case section(String) // "Agents", "Branches", "Terminals"
    case agent(String) // key(machine, pane)
    case terminal(String)
    case branch(project: String, name: String, checkedOut: Bool)
    case sessions(String) // key(machine, project)
    case cli(String)
    case workspace(String) // a machine's projects, as the TUI groups them
}

public struct TreeNode: Identifiable, Equatable, Sendable {
    public var id: String
    public var kind: NodeKind
    public var title: String
    public var detail: String = ""
    public var badge: String = ""
    public var children: [TreeNode]? = nil
    /// The TUI's id for the row's fold (ui.json "expanded"), and whether it
    /// is open when nothing is saved — the TUI's own defaults, so the two
    /// fold alike.
    public var fold: String? = nil
    public var foldDefault = true
}

/// The rows the TUI keeps open or folded (ui.json "expanded"), by its ids.
public func loadFolds(uiJSON data: Data?) -> [String: Bool] {
    struct UI: Decodable { var expanded: [String: Bool]? }
    guard let data, let ui = try? JSONDecoder().decode(UI.self, from: data) else { return [:] }
    return ui.expanded ?? [:]
}

/// Whether a row is open: what was chosen here, else what the TUI keeps,
/// else the TUI's default for it.
public func isOpen(_ node: TreeNode, app: [String: Bool], tui: [String: Bool]) -> Bool {
    guard let f = node.fold else { return true }
    return app[f] ?? tui[f] ?? node.foldDefault
}

/// How many branches a project lists before the rest are left to the TUI:
/// the task branches always, then the most recent others up to this.
public let branchesShown = 6

extension Snapshot {
    /// One machine in the sidebar: its projects with their agents, branches,
    /// terminals and saved sessions, and the CLI group for what belongs to
    /// no project — the TUI's tree. sessions counts each project's saved
    /// conversations, when they have been read.
    public func tree(machine: String = "local", label: String? = nil, in ws: Workspace? = nil,
                     sessions: [String: Int] = [:]) -> TreeNode {
        let shown = panes.filter { ws?.shows($0, machine: machine) ?? true }
        var projectNodes: [TreeNode] = []
        for pr in projects where ws?.shows(pr, machine: machine) ?? true {
            let pk = key(machine, pr.id)
            let tui = "p:" + (machine == "local" ? pr.id : "\(machine)~\(pr.id)") // the TUI's projectNodeID
            let mine = shown.filter { $0.projectID == pr.id }
            let agents = mine.filter { $0.agent != nil }.map { paneNode($0, machine) }
            let terminals = mine.filter { $0.agent == nil }.map { paneNode($0, machine) }
            let branches = listedBranches(pr).map { b in
                TreeNode(id: "b:\(pk):\(b.name)", kind: .branch(project: pk, name: b.name, checkedOut: b.worktree != nil),
                         title: b.name, badge: branchBadge(b))
            }
            var sections: [TreeNode] = []
            if !agents.isEmpty {
                sections.append(TreeNode(id: "s:\(pk):agents", kind: .section("Agents"), title: "Agents", badge: "\(agents.count)", children: agents, fold: tui + "/agents"))
            }
            if !branches.isEmpty {
                let all = max(pr.branches?.count ?? 0, branches.count)
                sections.append(TreeNode(id: "s:\(pk):branches", kind: .section("Branches"), title: "Branches", badge: "\(all)", children: branches, fold: tui + "/branches"))
            }
            if !terminals.isEmpty {
                sections.append(TreeNode(id: "s:\(pk):terminals", kind: .section("Terminals"), title: "Terminals", badge: "\(terminals.count)", children: terminals, fold: tui + "/terminals"))
            }
            if let n = sessions[pk], n > 0 {
                sections.append(TreeNode(id: "s:\(pk):sessions", kind: .sessions(pk), title: "Sessions", badge: "\(n)"))
            }
            let (w, d) = Snapshot(panes: mine).inbox()
            projectNodes.append(TreeNode(id: "p:\(pk)", kind: .project(pk), title: pr.name,
                                         badge: [w > 0 ? "⚑\(w)" : "", d > 0 ? "✓\(d)" : ""].filter { !$0.isEmpty }.joined(separator: " "),
                                         children: sections, fold: tui, foldDefault: !mine.isEmpty))
        }
        let loose = shown.filter { $0.projectID == nil || project($0.projectID!) == nil }
        // The projects sit under a Workspace row, the CLI group beside it:
        // the TUI's tree (kindWorkspace, open unless folded).
        var top: [TreeNode] = []
        if !projectNodes.isEmpty {
            top.append(TreeNode(id: "ws:\(machine)", kind: .workspace(machine), title: "Workspace", badge: "\(projectNodes.count)",
                                children: projectNodes, fold: "m:\(machine)/workspace"))
        }
        if !loose.isEmpty {
            top.append(TreeNode(id: "cli:\(machine)", kind: .cli(machine), title: "CLI", badge: "\(loose.count)",
                                children: loose.map { paneNode($0, machine) }, fold: "m:\(machine)/cli"))
        }
        let working = shown.filter { $0.agent?.state == .working }.count
        let (w, _) = Snapshot(panes: shown).inbox()
        return TreeNode(id: "m:\(machine)", kind: .machine(machine), title: label ?? machine,
                        badge: [working > 0 ? "⠋\(working)" : "", w > 0 ? "⚑\(w)" : ""].filter { !$0.isEmpty }.joined(separator: " "),
                        children: top, fold: "m:\(machine)")
    }

    func paneNode(_ p: PaneInfo, _ machine: String) -> TreeNode {
        let k = key(machine, p.id)
        let kind: NodeKind = p.agent == nil ? .terminal(k) : .agent(k)
        var badge = ""
        switch p.agent?.state {
        case .working: badge = "working"
        case .blocked: badge = "waiting"
        case .done: badge = "done"
        default: badge = p.running ? "" : "exited"
        }
        return TreeNode(id: "pane:\(k)", kind: kind, title: p.displayName, detail: p.branch ?? "", badge: badge)
    }

    /// What opening a task branch shows: its agent, else a terminal already
    /// running in its worktree, else nil — and only then is a new terminal
    /// started there. Clicking the branch again must not start another.
    public func paneFor(branch name: String, project: String) -> String? {
        if let p = panes.first(where: { $0.projectID == project && $0.branch == name && $0.agent != nil && $0.running }) {
            return p.id
        }
        guard let path = self.project(project)?.worktrees?.first(where: { $0.branch == name })?.path else { return nil }
        return panes.first(where: { p in
            guard p.running, let cwd = p.cwd else { return false }
            return cwd == path || cwd.hasPrefix(path + "/")
        })?.id
    }

    /// Branches checked out in a worktree of their own: where an agent's
    /// work is. The main checkout is the project itself.
    public func taskBranches(_ pr: ProjectInfo) -> [BranchInfo] {
        let main = Set((pr.worktrees ?? []).filter { $0.main == true }.compactMap(\.branch))
        let task = (pr.worktrees ?? []).filter { $0.main != true }.compactMap(\.branch)
        let byName = Dictionary((pr.branches ?? []).map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
        return task.filter { !main.contains($0) }.map {
            var b = byName[$0] ?? BranchInfo(name: $0)
            if b.worktree == nil { b.worktree = pr.worktrees?.first(where: { w in w.branch == b.name })?.path }
            return b
        }
    }

    /// The branches the sidebar lists: the task branches, then the most
    /// recent of the rest (newest commit first, as the server sends them)
    /// up to branchesShown, never the main checkout's own.
    public func listedBranches(_ pr: ProjectInfo) -> [BranchInfo] {
        var out = taskBranches(pr)
        let main = Set((pr.worktrees ?? []).filter { $0.main == true }.compactMap(\.branch))
        let have = Set(out.map(\.name))
        for b in pr.branches ?? [] where out.count < branchesShown && !have.contains(b.name) && !main.contains(b.name) {
            var other = b
            other.worktree = nil
            out.append(other)
        }
        return out
    }

    func branchBadge(_ b: BranchInfo) -> String {
        var parts: [String] = []
        if let pr = b.pr {
            parts.append("#\(pr.number)" + (pr.checks == "pass" ? "✓" : pr.checks == "fail" ? "✗" : ""))
        }
        if let a = b.baseAhead, a > 0 { parts.append("↑\(a)") }
        return parts.joined(separator: " ")
    }
}

// MARK: - The palette

public enum PaletteAction: Equatable, Sendable {
    case pane(String) // key(machine, pane)
    case project(String) // key(machine, project)
    case branch(project: String, name: String)
    case command(String) // a menu command's identifier
}

public struct PaletteItem: Equatable, Sendable {
    public var title: String
    public var detail: String
    public var kind: String // "agent name", "terminal", "project", "branch", or a shortcut
    public var action: PaletteAction
    public var waiting: Bool = false
}

/// The commands the palette lists, by identifier, with their shortcut.
public let paletteCommands: [(id: String, title: String, keys: String)] = [
    ("newTab", "New terminal tab", "⌘T"),
    ("splitRight", "Split right", "⌘D"),
    ("splitDown", "Split down", "⇧⌘D"),
    ("closeSplit", "Close split", "⌘W"),
    ("nextWaiting", "Next agent waiting for you", "⌘."),
    ("nextTab", "Next tab", "⇧⌘]"),
    ("previousTab", "Previous tab", "⇧⌘["),
    ("toggleSidebar", "Show or hide the sidebar", "⌃⌘S"),
    ("openTUI", "Open conch in a terminal", ""),
]

public func paletteCommandItems() -> [PaletteItem] {
    paletteCommands.map { PaletteItem(title: $0.title, detail: "", kind: $0.keys, action: .command($0.id)) }
}

extension Snapshot {
    /// What one machine gives the palette: agents waiting for you, the other
    /// panes, then the projects and their task branches. label names the
    /// machine in each item's detail, for a window showing more than one.
    public func paletteItems(machine: String = "local", label: String? = nil, in ws: Workspace? = nil)
        -> (waiting: [PaletteItem], panes: [PaletteItem], places: [PaletteItem]) {
        var waiting: [PaletteItem] = [], others: [PaletteItem] = [], places: [PaletteItem] = []
        let names = Dictionary(projects.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a })
        for p in panes where ws?.shows(p, machine: machine) ?? true {
            let detail = [p.projectID.flatMap { names[$0] }, p.branch, label].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
            var item = PaletteItem(title: p.displayName, detail: detail, kind: p.agent?.name ?? "terminal", action: .pane(key(machine, p.id)))
            if p.agent?.state == .blocked {
                item.waiting = true
                waiting.append(item)
            } else {
                others.append(item)
            }
        }
        for pr in projects where ws?.shows(pr, machine: machine) ?? true {
            let pk = key(machine, pr.id)
            places.append(PaletteItem(title: pr.name, detail: label ?? "", kind: "project", action: .project(pk)))
            for b in taskBranches(pr) {
                places.append(PaletteItem(title: b.name, detail: [pr.name, label].compactMap { $0 }.joined(separator: " · "),
                                          kind: "branch", action: .branch(project: pk, name: b.name)))
            }
        }
        return (waiting, others, places)
    }

    /// Everything the palette lists for this one machine, then the commands.
    public func paletteItems(in ws: Workspace? = nil) -> [PaletteItem] {
        let (w, p, pl) = paletteItems(machine: "local", label: nil, in: ws)
        return w + p + pl + paletteCommandItems()
    }
}

/// Narrows the palette to what matches: every word of the query somewhere
/// in an item, and a title that starts with the query (or has a word
/// starting with it) before one that only contains it — as the TUI's
/// palette does.
public func filterPalette(_ items: [PaletteItem], query: String) -> [PaletteItem] {
    let q = query.trimmingCharacters(in: .whitespaces).lowercased()
    if q.isEmpty { return items }
    let words = q.split(separator: " ").map(String.init)
    var hits: [(rank: Int, i: Int)] = []
    for (i, it) in items.enumerated() {
        let hay = "\(it.title) \(it.detail) \(it.kind)".lowercased()
        guard words.allSatisfy({ hay.contains($0) }) else { continue }
        let title = it.title.lowercased()
        let rank = title.hasPrefix(q) || title.contains(" " + q) ? 0 : title.contains(q) ? 1 : 2
        hits.append((rank, i))
    }
    return hits.sorted { $0.rank != $1.rank ? $0.rank < $1.rank : $0.i < $1.i }.map { items[$0.i] }
}
