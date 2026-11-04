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

    /// The panes a workspace shows on this machine, as the TUI's
    /// spaceShown: those in its projects, those started in it, and what
    /// they started in turn — a helper is in its agent's workspace — and,
    /// in the first, every pane no later workspace shows. nil is every pane.
    public func shown(in ws: Workspace?, machine: String = "local") -> [PaneInfo] {
        guard let ws else { return panes }
        let known = Set(projects.map(\.id))
        func members(_ projs: Set<String>, _ own: Set<String>) -> Set<String> {
            var in_: Set<String> = []
            for p in panes {
                if let pr = p.projectID, known.contains(pr), projs.contains(Workspace.projectKey(machine, pr)) { in_.insert(p.id) }
                if own.contains(key(machine, p.id)) { in_.insert(p.id) }
            }
            // What a pane in it started is in it too.
            var grew = true
            while grew {
                grew = false
                for p in panes where !in_.contains(p.id) {
                    if let by = p.createdBy, in_.contains(by) {
                        in_.insert(p.id)
                        grew = true
                    }
                }
            }
            return in_
        }
        var in_ = members(ws.projects, ws.panes)
        if ws.first {
            // Everything the later workspaces do not show is the first's:
            // a project of theirs it was given too is in its own set above.
            let elsewhere = members(ws.claimedProjects, ws.claimedPanes)
            let unclaimedProjects = Set(projects.map { Workspace.projectKey(machine, $0.id) }).subtracting(ws.claimedProjects)
            in_.formUnion(members(unclaimedProjects, []))
            for p in panes where !elsewhere.contains(p.id) { in_.insert(p.id) }
        }
        return panes.filter { in_.contains($0.id) }
    }

    /// Waiting for you, and finished while nobody looked — the counts in
    /// the toolbar, as the TUI's status bar counts them (inboxCount).
    public func inbox(in ws: Workspace? = nil, machine: String = "local") -> (waiting: Int, done: Int) {
        var waiting = 0, done = 0
        for p in shown(in: ws, machine: machine) {
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
    public func nextAttention(after current: String?, in ws: Workspace? = nil, machine: String = "local") -> String? {
        func rank(_ p: PaneInfo) -> Int? {
            switch p.agent?.state {
            case .blocked: return 0
            case .done: return 1
            default: return nil
            }
        }
        let order = shown(in: ws, machine: machine).filter { rank($0) != nil }.sorted {
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

/// A workspace as the TUI keeps it (ui.json): what was added to it — its
/// projects and the panes started in it — and, for the first, everything
/// no later workspace has claimed (the TUI's spaceShown). The app reads
/// them, switches between them, and makes one by adding it to the file
/// (adding), which every TUI running merges in by its id
/// (internal/tui/uisync.go).
public struct Workspace: Equatable, Sendable {
    /// Its id in ui.json; "first" for the first, which has none there.
    public var id: String
    public var name: String
    /// Projects added to it, as the TUI keys them: an id on this computer,
    /// "machine~id" elsewhere.
    public var projects: Set<String>
    /// Panes started in it outside its projects, as "machine|id".
    public var panes: Set<String>
    /// The first: it also shows whatever no later workspace has.
    public var first: Bool
    /// For the first, what the later ones have between them.
    public var claimedProjects: Set<String> = []
    public var claimedPanes: Set<String> = []

    public init(id: String = "first", name: String, projects: Set<String> = [], panes: Set<String> = [], first: Bool = false) {
        self.id = id
        self.name = name
        self.projects = projects
        self.panes = panes
        self.first = first
    }

    /// The first workspace while it is the only one: everything.
    public static let everything = Workspace(name: "", first: true)

    static func projectKey(_ machine: String, _ id: String) -> String {
        machine == "local" ? id : "\(machine)~\(id)"
    }

    public func shows(_ p: ProjectInfo, machine: String = "local") -> Bool {
        let k = Self.projectKey(machine, p.id)
        return projects.contains(k) || first && !claimedProjects.contains(k)
    }

    /// The workspaces in a ui.json: the first, then those saved after it,
    /// each with its id as the TUI names it ("w1"… for one saved before
    /// ids). The first knows what the others claim.
    public static func load(uiJSON data: Data?) -> [Workspace] {
        struct Saved: Decodable {
            var id: String?
            var name: String?
            var projects: [String]?
            var panes: [String]?
        }
        struct UI: Decodable {
            var spaces: [Saved]?
            var first_space: Saved?
        }
        let ui = data.flatMap { try? JSONDecoder().decode(UI.self, from: $0) }
        let later = (ui?.spaces ?? []).enumerated().map { i, s in
            Workspace(id: (s.id ?? "").isEmpty ? "w\(i + 1)" : s.id!, name: s.name ?? "",
                      projects: Set(s.projects ?? []), panes: Set(s.panes ?? []))
        }
        var first = Workspace(name: ui?.first_space?.name ?? "", first: true)
        if !later.isEmpty, let f = ui?.first_space {
            first.projects = Set(f.projects ?? [])
            first.panes = Set(f.panes ?? [])
        }
        first.claimedProjects = later.reduce(into: []) { $0.formUnion($1.projects) }
        first.claimedPanes = later.reduce(into: []) { $0.formUnion($1.panes) }
        return [first] + later
    }

    /// ui.json with an empty workspace added after the rest, as the TUI's
    /// + makes one, and nothing else changed. Workspaces saved before they
    /// had ids are given the ids the TUI gives them by place ("w1"…), so
    /// both name them alike. A file that does not parse is not written
    /// over: that throws.
    public static func adding(name: String = "", id: String = newID(), to data: Data?) throws -> Data {
        var ui: [String: Any] = [:]
        if let data, !data.isEmpty {
            guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                throw ConchError(code: "ui.json", message: "ui.json is not what conch writes; not changing it")
            }
            ui = obj
        }
        var spaces = (ui["spaces"] as? [[String: Any]]) ?? []
        for i in spaces.indices where (spaces[i]["id"] as? String ?? "").isEmpty {
            spaces[i]["id"] = "w\(i + 1)"
        }
        var new: [String: Any] = ["id": id]
        if !name.isEmpty { new["name"] = name }
        spaces.append(new)
        ui["spaces"] = spaces
        return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
    }

    /// ui.json with a pane (key(machine, id)) put in the workspace with
    /// that id, and nothing else changed — a pane started in it, as the
    /// TUI's joinSpace records one. Unknown workspace or unreadable file:
    /// that throws.
    public static func addingPane(_ pane: String, to id: String, in data: Data?) throws -> Data {
        guard let data, !data.isEmpty, var ui = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              var spaces = ui["spaces"] as? [[String: Any]] else {
            throw ConchError(code: "ui.json", message: "ui.json has no workspaces to put the pane in")
        }
        for i in spaces.indices where (spaces[i]["id"] as? String ?? "").isEmpty { spaces[i]["id"] = "w\(i + 1)" }
        guard let i = spaces.firstIndex(where: { $0["id"] as? String == id }) else {
            throw ConchError(code: "ui.json", message: "no workspace \(id) in ui.json")
        }
        var panes = spaces[i]["panes"] as? [String] ?? []
        if !panes.contains(pane) { panes.append(pane) }
        spaces[i]["panes"] = panes.sorted()
        ui["spaces"] = spaces
        return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
    }

    /// ui.json with the workspace of that id named name — "" numbers it
    /// again — and nothing else changed. The first's name is kept in
    /// first_space, where the TUI keeps what the first holds; the others'
    /// in their entry. An unknown workspace or an unreadable file throws.
    public static func renaming(_ id: String, to name: String, in data: Data?) throws -> Data {
        var ui: [String: Any] = [:]
        if let data, !data.isEmpty {
            guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                throw ConchError(code: "ui.json", message: "ui.json is not what conch writes; not changing it")
            }
            ui = obj
        }
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if id == "first" {
            var first = ui["first_space"] as? [String: Any] ?? [:]
            if name.isEmpty { first.removeValue(forKey: "name") } else { first["name"] = name }
            ui["first_space"] = first
        } else {
            var spaces = ui["spaces"] as? [[String: Any]] ?? []
            for i in spaces.indices where (spaces[i]["id"] as? String ?? "").isEmpty { spaces[i]["id"] = "w\(i + 1)" }
            guard let i = spaces.firstIndex(where: { $0["id"] as? String == id }) else {
                throw ConchError(code: "ui.json", message: "no workspace \(id) in ui.json")
            }
            if name.isEmpty { spaces[i].removeValue(forKey: "name") } else { spaces[i]["name"] = name }
            ui["spaces"] = spaces
        }
        return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
    }

    /// ui.json with a project (as the TUI keys one: its id here,
    /// "machine~id" elsewhere) put in the workspace with that id — one
    /// added from that workspace, as the TUI's receiveSpaceProject records
    /// it — and nothing else changed. Unknown workspace: that throws.
    public static func addingProject(_ project: String, to id: String, in data: Data?) throws -> Data {
        guard let data, !data.isEmpty, var ui = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              var spaces = ui["spaces"] as? [[String: Any]] else {
            throw ConchError(code: "ui.json", message: "ui.json has no workspaces to put the project in")
        }
        for i in spaces.indices where (spaces[i]["id"] as? String ?? "").isEmpty { spaces[i]["id"] = "w\(i + 1)" }
        guard let i = spaces.firstIndex(where: { $0["id"] as? String == id }) else {
            throw ConchError(code: "ui.json", message: "no workspace \(id) in ui.json")
        }
        var projects = spaces[i]["projects"] as? [String] ?? []
        if !projects.contains(project) { projects.append(project) }
        spaces[i]["projects"] = projects.sorted()
        ui["spaces"] = spaces
        return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
    }

    /// A project's key in ui.json, as the TUI writes it.
    public static func key(project id: String, machine: String) -> String { projectKey(machine, id) }

    /// A fresh id, as the TUI's newSpaceID makes them: "s" and twelve hex.
    public static func newID() -> String {
        "s" + (0..<6).map { _ in String(format: "%02x", UInt8.random(in: 0...255)) }.joined()
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
    case files(String) // key(machine, project)
    /// An agent's subagent: the key of the pane it runs in, and its id.
    case subagent(pane: String, id: String)
    case cli(String)
    case workspace(String) // a machine's projects, grouped as the TUI groups them
    /// "… N more" under a project's branches (count hidden), or, with
    /// every branch shown, the row that shows fewer again (count 0).
    case more(project: String, count: Int)
}

public struct TreeNode: Identifiable, Equatable, Sendable {
    public var id: String
    public var kind: NodeKind
    public var title: String
    public var detail: String = ""
    public var badge: String = ""
    /// The badge in coloured parts, where a row has more to say than one
    /// colour (a branch's state, changes, pull request and distance).
    public var parts: [BadgePart] = []
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
/// How many branches committed to lately the tree lists besides those
/// checked out, the base and those a pane works on; and how lately (the
/// TUI's maxListedBranches and recentBranchAge).
public let branchesRecent = 12
public let branchRecentAge: TimeInterval = 14 * 24 * 3600

extension Snapshot {
    /// One machine in the sidebar: its projects with their agents, branches,
    /// terminals and saved sessions, and the CLI group for what belongs to
    /// no project — the TUI's tree. sessions counts each project's saved
    /// conversations, when they have been read.
    public func tree(machine: String = "local", label: String? = nil, in ws: Workspace? = nil,
                     sessions: [String: Int] = [:], allBranches: Set<String> = [], now: Date = Date()) -> TreeNode {
        let shown = self.shown(in: ws, machine: machine)
        var projectNodes: [TreeNode] = []
        for pr in projects where ws?.shows(pr, machine: machine) ?? true {
            let pk = key(machine, pr.id)
            let tui = "p:" + (machine == "local" ? pr.id : "\(machine)~\(pr.id)") // the TUI's projectNodeID
            let mine = shown.filter { $0.projectID == pr.id }
            let agentPanes = mine.filter { $0.agent != nil }, terminalPanes = mine.filter { $0.agent == nil }
            let agents = paneForest(agentPanes, machine)
            let terminals = paneForest(terminalPanes, machine)
            let all = allBranches.contains(pk)
            let (listed, hidden) = listedBranches(pr, panes: mine, all: all, now: now)
            var branches = listed.map { b in
                TreeNode(id: "b:\(pk):\(b.name)", kind: .branch(project: pk, name: b.name, checkedOut: b.worktree != nil),
                         title: b.name, badge: branchBadge(b),
                         parts: branchBadgeParts(b, worktree: pr.worktrees?.first { $0.branch == b.name }, isBase: b.name == pr.base,
                                                 agents: mine.filter { $0.branch == b.name && $0.running }.compactMap { $0.agent?.state }))
            }
            // What is left out is said, as the TUI's "… N more", and a
            // click lists it; listed, a click puts it away again.
            if hidden > 0 {
                branches.append(TreeNode(id: "more:\(pk)", kind: .more(project: pk, count: hidden), title: "… \(hidden) more"))
            } else if all, listedBranches(pr, panes: mine, all: false, now: now).hidden > 0 {
                branches.append(TreeNode(id: "more:\(pk)", kind: .more(project: pk, count: 0), title: "Show fewer"))
            }
            var sections: [TreeNode] = []
            if !agents.isEmpty {
                // Every agent, helpers nested under theirs included, as the TUI counts.
                sections.append(TreeNode(id: "s:\(pk):agents", kind: .section("Agents"), title: "Agents", badge: "\(agentPanes.count)",
                                         children: agents, fold: tui + "/agents"))
            }
            if !branches.isEmpty {
                let count = max(pr.branches?.count ?? 0, listed.count)
                sections.append(TreeNode(id: "s:\(pk):branches", kind: .section("Branches"), title: "Branches", badge: "\(count)", children: branches, fold: tui + "/branches"))
            }
            if !terminals.isEmpty {
                sections.append(TreeNode(id: "s:\(pk):terminals", kind: .section("Terminals"), title: "Terminals", badge: "\(terminalPanes.count)", children: terminals, fold: tui + "/terminals"))
            }
            // Its files, as the TUI lists them, before its sessions.
            sections.append(TreeNode(id: "s:\(pk):files", kind: .files(pk), title: "Files"))
            if let n = sessions[pk], n > 0 {
                sections.append(TreeNode(id: "s:\(pk):sessions", kind: .sessions(pk), title: "Sessions", badge: "\(n)"))
            }
            projectNodes.append(TreeNode(id: "p:\(pk)", kind: .project(pk), title: pr.name,
                                         badge: attention(mine),
                                         children: sections, fold: tui, foldDefault: !mine.isEmpty))
        }
        let loose = shown.filter { $0.projectID == nil || project($0.projectID!) == nil }
        // The projects under a Workspace row, the CLI group beside it: the
        // TUI's tree (kindWorkspace, open unless folded).
        var top: [TreeNode] = []
        if !projectNodes.isEmpty {
            top.append(TreeNode(id: "ws:\(machine)", kind: .workspace(machine), title: "Workspace", badge: "\(projectNodes.count)",
                                children: projectNodes, fold: "m:\(machine)/workspace"))
        }
        if !loose.isEmpty {
            top.append(TreeNode(id: "cli:\(machine)", kind: .cli(machine), title: "CLI", badge: "\(loose.count)",
                                children: paneForest(loose, machine), fold: "m:\(machine)/cli"))
        }
        return TreeNode(id: "m:\(machine)", kind: .machine(machine), title: label ?? machine,
                        badge: attention(shown),
                        children: top, fold: "m:\(machine)")
    }

    /// A machine or project row's counts, as the TUI's attentionBadge:
    /// working, waiting for you, done.
    func attention(_ panes: [PaneInfo]) -> String {
        let working = panes.filter { $0.agent?.state == .working }.count
        let (w, d) = Snapshot(panes: panes).inbox()
        return [working > 0 ? "⠋\(working)" : "", w > 0 ? "⚑\(w)" : "", d > 0 ? "✓\(d)" : ""].filter { !$0.isEmpty }.joined(separator: " ")
    }

    /// Panes as the TUI lists them (tree.go paneRows): each one an agent
    /// started under that agent, when both are in the same list, and an
    /// agent's subagents under it. A pane whose creator is listed elsewhere
    /// stays where it would have been; one descending from itself stops.
    public func paneForest(_ panes: [PaneInfo], _ machine: String) -> [TreeNode] {
        let here = Set(panes.map(\.id))
        var kids: [String: [PaneInfo]] = [:]
        for p in panes {
            if let by = p.createdBy, by != p.id, here.contains(by) { kids[by, default: []].append(p) }
        }
        var seen: Set<String> = []
        func node(_ p: PaneInfo) -> TreeNode? {
            guard seen.insert(p.id).inserted else { return nil }
            var n = paneNode(p, machine)
            let k = key(machine, p.id)
            var under = (kids[p.id] ?? []).compactMap(node)
            if p.running {
                under += (p.agent?.subagents ?? []).map { a in
                    TreeNode(id: "sub:\(k):\(a.id)", kind: .subagent(pane: k, id: a.id), title: a.title,
                             detail: a.description?.isEmpty == false ? (a.type ?? "") : "")
                }
            }
            if !under.isEmpty {
                n.children = under
                n.fold = "pane:" + (machine == "local" ? p.id : "\(machine)~\(p.id)") // the TUI's paneNodeID
            }
            return n
        }
        let roots = panes.filter { p in p.createdBy == nil || p.createdBy == p.id || !here.contains(p.createdBy!) }
        var out = roots.compactMap(node)
        // A pane caught in a loop of creators belongs to no root: still listed.
        out += panes.filter { !seen.contains($0.id) }.compactMap(node)
        return out
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

    /// The branches the sidebar lists, as the TUI picks them (tree.go's
    /// listedBranches): the main checkout's first, then those checked out
    /// in a worktree, the base and those a pane works on, then the ones
    /// committed to in the last two weeks, up to branchesRecent of them,
    /// newest first as the server sends them. all lists every one; hidden
    /// counts what was left out.
    public func listedBranches(_ pr: ProjectInfo, panes: [PaneInfo] = [], all: Bool = false, now: Date = Date()) -> (list: [BranchInfo], hidden: Int) {
        let main = (pr.worktrees ?? []).first { $0.main == true }?.branch
        let worked = Set(panes.compactMap(\.branch))
        let checkedOut = Set((pr.worktrees ?? []).compactMap(\.branch))
        func rank(_ b: BranchInfo) -> Int {
            if b.name == main { return 0 }
            if b.worktree != nil || checkedOut.contains(b.name) { return 1 }
            if b.name == pr.base || worked.contains(b.name) { return 2 }
            return 3
        }
        // A worktree's branch the server has not listed yet is still one.
        var branches = pr.branches ?? []
        let named = Set(branches.map(\.name))
        for w in pr.worktrees ?? [] where w.main != true {
            if let n = w.branch, !named.contains(n) { branches.append(BranchInfo(name: n, worktree: w.path)) }
        }
        let ranked = branches.enumerated().sorted { (rank($0.element), $0.offset) < (rank($1.element), $1.offset) }.map(\.element)
        var list: [BranchInfo] = [], hidden = 0, recent = 0
        for var b in ranked {
            if b.worktree == nil { b.worktree = pr.worktrees?.first { $0.branch == b.name }?.path }
            var keep = all || rank(b) < 3
            if !keep, let c = b.committed, now.timeIntervalSince(c) < branchRecentAge, recent < branchesRecent {
                keep = true
                recent += 1
            }
            if keep { list.append(b) } else { hidden += 1 }
        }
        return (list, hidden)
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
    /// A file of a checkout, opened in that machine's $EDITOR.
    case file(machine: String, root: String, path: String)
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
    ("newTask", "New task (branch, worktree and agent)…", "⌘N"),
    ("startAgent", "Start an agent here", "⇧⌘N"),
    ("newTab", "New terminal tab", "⌘T"),
    ("addProject", "Add project…", "⌘O"),
    ("sshToHost", "SSH to a host…", "⇧⌘H"),
    ("addMachine", "Add machine…", "⇧⌘M"),
    ("splitRight", "Split right", "⌘D"),
    ("splitDown", "Split down", "⇧⌘D"),
    ("closeSplit", "Close split", "⌘W"),
    ("nextWaiting", "Next agent waiting for you", "⌘."),
    ("nextTab", "Next tab", "⇧⌘]"),
    ("previousTab", "Previous tab", "⇧⌘["),
    ("toggleSidebar", "Show or hide the sidebar", "⌃⌘S"),
    ("focusSidebar", "Go to the sidebar (its keys: ↑↓ ⏎ t c n)", "⌘0"),
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
        for p in shown(in: ws, machine: machine) {
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

extension Snapshot {
    /// The port a pane's checkout is given (CONCH_PORT), for its header:
    /// the worktree the pane's folder is in, the deepest if several hold
    /// it; nil for none, or one not given a port yet.
    public func port(of p: PaneInfo) -> Int? {
        guard let cwd = p.cwd, !cwd.isEmpty else { return nil }
        var best: WorktreeInfo?
        for pr in projects {
            for wt in pr.worktrees ?? [] where (wt.port ?? 0) > 0 && (cwd == wt.path || cwd.hasPrefix(wt.path + "/")) {
                if wt.path.count > (best?.path.count ?? -1) { best = wt }
            }
        }
        return best?.port
    }

    /// The agents running on a machine, each once, in the order the tree
    /// lists them: what a folded machine's row shows of what it holds.
    public func agentNames(in ws: Workspace? = nil, machine: String = "local") -> [String] {
        var seen: Set<String> = []
        var out: [String] = []
        for p in shown(in: ws, machine: machine) where p.running {
            if let a = p.agent?.name, seen.insert(a).inserted { out.append(a) }
        }
        return out
    }
}

/// Palette items for files fs.find found in a project's checkout: the name
/// first, then where it is. label names the machine, for a window showing
/// more than one.
public func fileItems(_ found: FindResult, machine: String, project: String, label: String? = nil) -> [PaletteItem] {
    (found.paths ?? []).map { path in
        let name = (path as NSString).lastPathComponent
        let dir = (path as NSString).deletingLastPathComponent
        let where_ = [project + (dir.isEmpty ? "" : " · " + dir), label ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
        return PaletteItem(title: name, detail: where_, kind: "file", action: .file(machine: machine, root: found.root, path: path))
    }
}

/// How wide the sidebar may be dragged: wide enough for a name and its
/// branch, never so wide the panes have nothing.
public let sidebarRange: ClosedRange<Double> = 200...560

/// The sidebar's width until it is dragged: the mock's.
public let sidebarDefault: Double = 260

public func clampSidebar(_ w: Double) -> Double {
    guard w.isFinite else { return sidebarDefault }
    return min(max(w, sidebarRange.lowerBound), sidebarRange.upperBound)
}

/// The workspace a ui.json's TUI was last on (active_space): 0, the first,
/// when it says none.
public func activeWorkspace(uiJSON data: Data?) -> Int {
    struct UI: Decodable { var active_space: Int? }
    return data.flatMap { try? JSONDecoder().decode(UI.self, from: $0) }?.active_space ?? 0
}

extension Snapshot {
    /// The agents waiting for you here — direction C's "Needs you" — those
    /// the workspace shows, the longest waiting first.
    public func needsYou(in ws: Workspace? = nil, machine: String = "local") -> [PaneInfo] {
        shown(in: ws, machine: machine).filter { $0.agent?.state == .blocked && $0.running }
            .sorted { ($0.agent?.since ?? .distantPast) < ($1.agent?.since ?? .distantPast) }
    }

    /// Where a waiting agent is, for its card: its project and branch.
    public func place(of p: PaneInfo) -> String {
        [p.projectID.flatMap { project($0)?.name } ?? "", p.branch ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

/// What a waiting agent's card calls its question, from what the agent
/// said it wants: a permission, an answer, or just waiting.
public func questionKind(_ message: String?) -> String {
    let m = (message ?? "").lowercased()
    if m.contains("permission") || m.contains("allow") || m.contains("approve") { return "permission" }
    if m.contains("input") || m.contains("question") || m.contains("answer") { return "question" }
    return "waiting"
}

/// A waiting agent's question as its card says it: without the "answer:"
/// an agent's notification starts with, and without the choices listed at
/// its end — "(hello · hi)" — which the card's buttons already are.
public func cardText(_ text: String, choices: [Choice]) -> String {
    var t = text.trimmingCharacters(in: .whitespacesAndNewlines)
    // "answer:" is how an agent's notification starts; "? " how Gemini CLI
    // heads a question.
    for lead in ["answer:", "Answer:", "? "] where t.hasPrefix(lead) {
        t = String(t.dropFirst(lead.count)).trimmingCharacters(in: .whitespaces)
    }
    if t.hasSuffix(")"), let open = t.range(of: " (", options: .backwards) {
        let inside = t[open.upperBound..<t.index(before: t.endIndex)]
        let parts = inside.components(separatedBy: " · ").map { $0.trimmingCharacters(in: .whitespaces) }
        let labels = Set(choices.map(\.label))
        if parts.count >= 2, !labels.isEmpty, parts.allSatisfy({ labels.contains($0) }) {
            t = String(t[..<open.lowerBound])
        }
    }
    return t
}

/// How long after it starts a pane that fails is kept, so its error can be
/// read: the TUI's launchWindow.
public let launchWindow: TimeInterval = 10

/// Whether a pane whose program has just exited should be closed, as the
/// TUI closes one (model.go, EventPaneExited): a shell's exit, an ssh
/// logout, an agent's /exit — anything but a failure in its first seconds,
/// which stays so its error can be read, and a check that failed, whose
/// output is its report.
public func closesOnExit(_ p: PaneInfo, now: Date = Date()) -> Bool {
    guard p.state == "exited" else { return false }
    let code = p.exitCode ?? 0
    if code != 0, let c = p.created, now.timeIntervalSince(c) < launchWindow { return false }
    if code != 0, p.name.hasPrefix("check · ") { return false }
    return true
}

/// One part of a row's badge, and what it says, which sets its colour.
public struct BadgePart: Equatable, Sendable {
    public enum Tone: Equatable, Sendable { case muted, ok, warn, err, work, merged, added, removed }
    public var text: String
    public var tone: Tone
    public init(_ text: String, _ tone: Tone) {
        self.text = text
        self.tone = tone
    }
}

/// A branch row's badge, as the TUI's (view.go branchParts): its agents'
/// state, worst first; conflicts or uncommitted changes; its pull request
/// (merged, closed, draft, its checks); gone, or how far it is ahead of
/// and behind the base (the base itself against its upstream).
public func branchBadgeParts(_ b: BranchInfo, worktree w: WorktreeInfo?, isBase: Bool, agents: [AgentState]) -> [BadgePart] {
    var out: [BadgePart] = []
    if agents.contains(.blocked) { out.append(BadgePart("!", .warn)) }
    else if agents.contains(.working) { out.append(BadgePart("⠋", .work)) }
    else if agents.contains(.done) { out.append(BadgePart("✓", .ok)) }
    if let st = w?.status {
        if let c = st.conflicts, c > 0 {
            out.append(BadgePart("⚠\(c)", .err))
        } else if let f = st.files, f > 0 {
            let add = st.added ?? 0, del = st.deleted ?? 0
            if add > 0 { out.append(BadgePart("+\(add)", .added)) }
            if del > 0 { out.append(BadgePart("−\(del)", .removed)) }
            if add == 0 && del == 0 { out.append(BadgePart("~\(f)", .warn)) }
        }
    }
    if let pr = b.pr {
        let n = "#\(pr.number)"
        switch (pr.state ?? "", pr.draft ?? false, pr.checks ?? "") {
        case ("MERGED", _, _): out.append(BadgePart(n, .merged))
        case ("CLOSED", _, _): out.append(BadgePart(n + "×", .muted))
        case (_, true, _): out.append(BadgePart(n, .muted))
        case (_, _, "pass"): out.append(BadgePart(n + "✓", .ok))
        case (_, _, "fail"): out.append(BadgePart(n + "✗", .err))
        case (_, _, "pending"): out.append(BadgePart(n + "●", .warn))
        default: out.append(BadgePart(n, .work))
        }
    }
    if b.gone == true {
        out.append(BadgePart("gone", .err))
    } else if !isBase, (b.baseAhead ?? 0) > 0 || (b.baseBehind ?? 0) > 0 {
        let a = b.baseAhead ?? 0, d = b.baseBehind ?? 0
        out.append(BadgePart((a > 0 ? "↑\(a)" : "") + (d > 0 ? "↓\(d)" : ""), .muted))
    } else if isBase, (b.ahead ?? 0) > 0 || (b.behind ?? 0) > 0 {
        out.append(BadgePart("⇡\(b.ahead ?? 0)⇣\(b.behind ?? 0)", .muted))
    }
    return out
}

/// A pane row's badge when it is not an agent at work (the TUI's
/// paneGlyph): how it ended — exited, or exit N and failed in red — or a
/// terminal's activity or silence alert; nil leaves the state pill.
public func paneEndBadge(_ p: PaneInfo) -> BadgePart? {
    if !p.running {
        let code = p.exitCode ?? 0
        return code == 0 ? BadgePart("exited", .muted) : BadgePart("exit \(code)", .err)
    }
    if let a = p.agent {
        if a.failed == true && (a.state == .done || a.state == .idle) { return BadgePart("failed", .err) }
        return nil
    }
    switch p.alert {
    case "activity": return BadgePart("# output", .warn)
    case "silence": return BadgePart("~ quiet", .warn)
    default: return nil
    }
}
