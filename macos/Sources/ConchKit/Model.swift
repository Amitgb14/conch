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

/// A workspace as the TUI keeps it (ui.json): the first holds everything,
/// another only what was added to it. The app reads them and switches
/// between them; making and changing them stays the TUI's.
public struct Workspace: Equatable, Sendable {
    public var name: String
    /// nil is the first workspace, which shows everything.
    public var projects: Set<String>?
    public var panes: Set<String>

    public init(name: String, projects: Set<String>? = nil, panes: Set<String> = []) {
        self.name = name
        self.projects = projects
        self.panes = panes
    }

    public func shows(_ p: PaneInfo) -> Bool {
        guard let projects else { return true }
        if let pid = p.projectID, projects.contains(pid) { return true }
        return panes.contains("local|" + p.id)
    }

    public func shows(_ p: ProjectInfo) -> Bool { projects?.contains(p.id) ?? true }

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
            // Projects and panes elsewhere than this computer are written
            // "machine~id"; this window is this computer's.
            for s in ui.spaces ?? [] {
                out.append(Workspace(name: s.name ?? "",
                                     projects: Set((s.projects ?? []).filter { !$0.contains("~") }),
                                     panes: Set(s.panes ?? [])))
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
    case machine
    case project(String)
    case section(String) // "Agents", "Branches", "Terminals"
    case agent(String)
    case terminal(String)
    case branch(project: String, name: String)
    case cli
}

public struct TreeNode: Identifiable, Equatable, Sendable {
    public var id: String
    public var kind: NodeKind
    public var title: String
    public var detail: String = ""
    public var badge: String = ""
    public var children: [TreeNode]? = nil
}

extension Snapshot {
    /// The sidebar: this computer, its projects with their agents, task
    /// branches and terminals, and the CLI group for what belongs to no
    /// project — the TUI's tree, for one machine.
    public func tree(machine: String = "local", in ws: Workspace? = nil) -> [TreeNode] {
        let shown = panes.filter { ws?.shows($0) ?? true }
        var projectNodes: [TreeNode] = []
        for pr in projects where ws?.shows(pr) ?? true {
            let mine = shown.filter { $0.projectID == pr.id }
            let agents = mine.filter { $0.agent != nil }.map(paneNode)
            let terminals = mine.filter { $0.agent == nil }.map(paneNode)
            let branches = taskBranches(pr).map { b in
                TreeNode(id: "b:\(pr.id):\(b.name)", kind: .branch(project: pr.id, name: b.name), title: b.name,
                         badge: branchBadge(b))
            }
            var sections: [TreeNode] = []
            if !agents.isEmpty {
                sections.append(TreeNode(id: "s:\(pr.id):agents", kind: .section("Agents"), title: "Agents", badge: "\(agents.count)", children: agents))
            }
            if !branches.isEmpty {
                sections.append(TreeNode(id: "s:\(pr.id):branches", kind: .section("Branches"), title: "Branches", badge: "\(branches.count)", children: branches))
            }
            if !terminals.isEmpty {
                sections.append(TreeNode(id: "s:\(pr.id):terminals", kind: .section("Terminals"), title: "Terminals", badge: "\(terminals.count)", children: terminals))
            }
            let (w, d) = Snapshot(panes: mine).inbox()
            projectNodes.append(TreeNode(id: "p:\(pr.id)", kind: .project(pr.id), title: pr.name,
                                         badge: [w > 0 ? "⚑\(w)" : "", d > 0 ? "✓\(d)" : ""].filter { !$0.isEmpty }.joined(separator: " "),
                                         children: sections))
        }
        let loose = shown.filter { $0.projectID == nil || project($0.projectID!) == nil }
        var top = projectNodes
        if !loose.isEmpty {
            top.append(TreeNode(id: "cli", kind: .cli, title: "CLI", badge: "\(loose.count)", children: loose.map(paneNode)))
        }
        return [TreeNode(id: "m:\(machine)", kind: .machine, title: machine, children: top)]
    }

    func paneNode(_ p: PaneInfo) -> TreeNode {
        let kind: NodeKind = p.agent == nil ? .terminal(p.id) : .agent(p.id)
        var badge = ""
        switch p.agent?.state {
        case .working: badge = "working"
        case .blocked: badge = "waiting"
        case .done: badge = "done"
        default: badge = p.running ? "" : "exited"
        }
        return TreeNode(id: "pane:\(p.id)", kind: kind, title: p.displayName, detail: p.branch ?? "", badge: badge)
    }

    /// Branches checked out in a worktree of their own: where an agent's
    /// work is. The main checkout is the project itself.
    public func taskBranches(_ pr: ProjectInfo) -> [BranchInfo] {
        let main = Set((pr.worktrees ?? []).filter { $0.main == true }.compactMap(\.branch))
        let task = (pr.worktrees ?? []).filter { $0.main != true }.compactMap(\.branch)
        let byName = Dictionary((pr.branches ?? []).map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
        return task.filter { !main.contains($0) }.map { byName[$0] ?? BranchInfo(name: $0) }
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
    case pane(String)
    case project(String)
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

extension Snapshot {
    /// Everything the palette can go to or run, in the order an empty query
    /// lists it: agents waiting for you, then the other panes, the projects
    /// and their task branches, and the commands.
    public func paletteItems(in ws: Workspace? = nil) -> [PaletteItem] {
        var waiting: [PaletteItem] = [], others: [PaletteItem] = [], places: [PaletteItem] = []
        let names = Dictionary(projects.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a })
        for p in panes where ws?.shows(p) ?? true {
            let detail = [p.projectID.flatMap { names[$0] }, p.branch].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
            var item = PaletteItem(title: p.displayName, detail: detail, kind: p.agent?.name ?? "terminal", action: .pane(p.id))
            if p.agent?.state == .blocked {
                item.waiting = true
                waiting.append(item)
            } else {
                others.append(item)
            }
        }
        for pr in projects where ws?.shows(pr) ?? true {
            places.append(PaletteItem(title: pr.name, detail: "", kind: "project", action: .project(pr.id)))
            for b in taskBranches(pr) {
                places.append(PaletteItem(title: b.name, detail: pr.name, kind: "branch", action: .branch(project: pr.id, name: b.name)))
            }
        }
        let cmds = paletteCommands.map { PaletteItem(title: $0.title, detail: "", kind: $0.keys, action: .command($0.id)) }
        return waiting + others + places + cmds
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
