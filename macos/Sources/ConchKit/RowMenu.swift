import Foundation

/// An agent a machine can start, as agent.status reports it.
public struct AgentAvailability: Decodable, Equatable, Sendable {
    public var name: String
    public var label: String?
    public var installed: Bool
    public var tier: String?

    public init(name: String, label: String? = nil, installed: Bool = true, tier: String? = nil) {
        self.name = name
        self.label = label
        self.installed = installed
        self.tier = tier
    }

    /// What a menu calls it: its label (Claude Code), else its name.
    public var title: String { (label ?? "").isEmpty ? name : label! }
}

public struct AgentStatusList: Decodable { public var agents: [AgentAvailability]? }

/// What a choice on a tree row's menu does. Panes and projects are named
/// as the tree names them, key(machine, id).
public enum RowAction: Equatable, Sendable {
    case open
    case rename(pane: String)
    case prompt(pane: String)
    case close(pane: String)
    /// Starts an agent where cwd is (nil: the machine's home). inProject
    /// false keeps it out of any project, as the TUI's CLI group does.
    case startAgent(machine: String, cwd: String?, inProject: Bool)
    case newTerminal(machine: String, cwd: String?, inProject: Bool)
    case changes(project: String, branch: String)
    /// The project's commit graph, at a branch's newest commit when one
    /// is named.
    case commits(project: String, branch: String?)
    case newTask(project: String)
    case copy(String)
    /// Adds a project on that machine (the TUI's a).
    case addProject(machine: String)
    /// A terminal here logged in to a host with ssh (the TUI's H).
    case sshToHost
    /// Another machine, conch installed there (the TUI's M).
    case addMachine
    /// A sandbox made with a provider and added as a machine (the TUI's
    /// M → New sandbox…).
    case newSandbox
    /// A machine's connection made again (the TUI's R).
    case reconnect(machine: String)
    /// A machine's server re-exec'd onto its binary, panes kept (m).
    case reloadServer(machine: String)
    /// conch installed on a machine again, from this build (U).
    case upgrade(machine: String)
    /// Settings → Skills & MCP servers for that machine: conch's skills for
    /// its agents, and the library of MCP servers and skills given to them.
    case skillsSettings(machine: String)
    /// A machine taken out of machines.json, after asking.
    case removeMachine(machine: String)
    /// A web page opened in the browser: a branch's pull request.
    case openURL(String)
    /// A branch's linked worktree removed (it must be clean); the branch
    /// stays.
    case removeWorktree(project: String, path: String)
    /// A project taken off the sidebar, after asking: out of this workspace
    /// alone when another has it too, else out of conch (files untouched).
    case removeProject(project: String)
    /// The whole project moved to another machine, with its branches,
    /// worktrees and agents.
    case moveProject(project: String)
    /// A branch deleted, with its worktree — after saying what that loses.
    case deleteBranch(project: String, branch: String)
    /// A pane's alert when it goes quiet (quiet) or prints, turned on or off.
    case monitor(pane: String, quiet: Bool)
    /// One message to the running panes of a row (the TUI's broadcast).
    case broadcast(BroadcastScope)
    /// A custom action from config.toml, by its name.
    case runAction(String)
    /// A saved ssh host logged in to again, or forgotten; a session's
    /// host saved to log in to again.
    case connectHost(String)
    case forgetHost(String)
    case saveHost(pane: String)
    /// What the agents load in a checkout (the TUI's i), in a tab.
    case agentSetup(project: String, dir: String)
    /// A hosted sandbox started, stopped, deleted or its cost asked for
    /// (conch sandbox VERB), by its machine.
    case sandbox(machine: String, verb: String)
    case divider
}

public struct RowMenuItem: Equatable, Sendable {
    public var title: String
    public var action: RowAction

    public init(_ title: String, _ action: RowAction) {
        self.title = title
        self.action = action
    }

    static let divider = RowMenuItem("", .divider)

    /// Whether choosing it asks which agent, as a submenu of them.
    public var picksAgent: Bool { if case .startAgent = action { return true }; return false }
}

/// How conch's commands name a pane: its ID on this computer, and
/// machine:ID on another (`conch read busybox:p3`) — by the machine's label
/// when the commands would find this machine by it, or its ID, which they
/// always do: a label with a space or a colon would not survive being
/// typed, and one that is "local" or that another machine in among
/// (machines.json, disabled ones too) answers to — two logins to one host
/// are both "devbox" — would reach whichever the catalog lists first. As
/// the TUI's paneRef (remote.RefName).
public func paneRef(machine: String, label: String?, pane: String, among: [MachineEntry] = []) -> String {
    if machine.isEmpty || machine == "local" { return pane }
    if let l = label, !l.isEmpty, l != "local",
       l.rangeOfCharacter(from: CharacterSet.whitespacesAndNewlines.union(CharacterSet(charactersIn: ":"))) == nil,
       !among.contains(where: { $0.id != machine && ($0.id == l || $0.label == l) }) {
        return l + ":" + pane
    }
    return machine + ":" + pane
}

extension Snapshot {
    /// Whether a broadcast from scope has anyone to go to.
    func hasRunning(_ scope: BroadcastScope) -> Bool { !broadcastTargets(scope).isEmpty }

    /// A tree row's right-click menu: the TUI's row menu (overlay.go), for
    /// what the app does itself. machineOnline false leaves out whatever
    /// would start something there.
    /// among is every machine in machines.json, for paneRef.
    public func menu(for node: TreeNode, machine: String, machineOnline: Bool = true, label: String? = nil,
                     among: [MachineEntry] = []) -> [RowMenuItem] {
        var out: [RowMenuItem] = []
        switch node.kind {
        case .agent(let k), .terminal(let k):
            guard let p = pane(unkey(k).id) else { return [] }
            let agent = p.agent != nil
            out = [RowMenuItem("Open", .open), RowMenuItem("Rename…", .rename(pane: k))]
            if agent && p.running { out.append(RowMenuItem("Prompt…", .prompt(pane: k))) }
            if machineOnline {
                out += [.divider,
                        RowMenuItem("Start an Agent Here", .startAgent(machine: machine, cwd: p.cwd, inProject: p.projectID != nil)),
                        RowMenuItem("New Terminal Here", .newTerminal(machine: machine, cwd: p.cwd, inProject: p.projectID != nil))]
            }
            if let b = p.branch, !b.isEmpty, let pr = p.projectID, project(pr) != nil {
                out.append(RowMenuItem("View Changes on \(b)", .changes(project: key(machine, pr), branch: b)))
            }
            // Watching a terminal, as the TUI's ctrl+b M and A: an agent
            // says for itself when it needs you.
            if !agent && p.running && machineOnline {
                let quiet = (p.monitor?.silence ?? 0) > 0, output = p.monitor?.activity == true
                out += [.divider,
                        RowMenuItem((quiet ? "✓ " : "") + "Alert When It Goes Quiet", .monitor(pane: k, quiet: true)),
                        RowMenuItem((output ? "✓ " : "") + "Alert on Output", .monitor(pane: k, quiet: false))]
            }
            if machine == "local", p.sshTarget != nil { out += [.divider, RowMenuItem("Save This Host", .saveHost(pane: k))] }
            // What conch's commands call it (conch read p12), which no row says.
            let ref = paneRef(machine: machine, label: label, pane: p.id, among: among)
            out += [.divider, RowMenuItem("Copy Pane ID (\(ref))", .copy(ref)),
                    RowMenuItem(agent && p.running ? "Close Agent…" : "Close", .close(pane: k))]
        case .branch(let pk, let name, _):
            out = [RowMenuItem("View Changes", .changes(project: pk, branch: name))]
            let pr = project(unkey(pk).id)
            // Starting on a branch needs its checkout; one not checked out
            // is a task's to make (a worktree of its own).
            if machineOnline, let path = pr?.worktrees?.first(where: { $0.branch == name })?.path {
                out += [RowMenuItem("Start an Agent on This Branch", .startAgent(machine: machine, cwd: path, inProject: true)),
                        RowMenuItem("Open Terminal on This Branch", .newTerminal(machine: machine, cwd: path, inProject: true))]
            }
            if machineOnline { out.append(RowMenuItem("New Task in Project…", .newTask(project: pk))) }
            out.append(RowMenuItem("Show in Commit Graph", .commits(project: pk, branch: name)))
            if let w = pr?.worktrees?.first(where: { $0.branch == name })?.path {
                out.append(RowMenuItem("Agent Setup Here", .agentSetup(project: pk, dir: w)))
            }
            if let url = pr?.branches?.first(where: { $0.name == name })?.pr?.url, openableURL(url) != nil {
                out.append(RowMenuItem("Open Pull Request", .openURL(url)))
            }
            out += [.divider, RowMenuItem("Copy Branch Name", .copy(name))]
            // Taking it away: never the main checkout's, nor the base.
            let main = pr?.worktrees?.first { $0.main == true }
            if machineOnline, name != main?.branch, name != pr?.base {
                out.append(.divider)
                if let w = pr?.worktrees?.first(where: { $0.branch == name && $0.main != true }) {
                    out.append(RowMenuItem("Remove Worktree…", .removeWorktree(project: pk, path: w.path)))
                }
                out.append(RowMenuItem("Delete Branch…", .deleteBranch(project: pk, branch: name)))
            }
        case .project(let pk):
            guard machineOnline, let pr = project(unkey(pk).id) else { return [] }
            out = [RowMenuItem(pr.git == false ? "New Task (an Agent with a Prompt, Here)…" : "New Task (Branch + Worktree + Agent)…", .newTask(project: pk)),
                   RowMenuItem("Start an Agent in Project", .startAgent(machine: machine, cwd: pr.path, inProject: true)),
                   RowMenuItem("Open Terminal in Project", .newTerminal(machine: machine, cwd: pr.path, inProject: true))]
            if pr.git != false { out.append(RowMenuItem("Commit Graph", .commits(project: pk, branch: nil))) }
            out.append(RowMenuItem("Agent Setup", .agentSetup(project: pk, dir: pr.path)))
            if hasRunning(.project(pk)) { out.append(RowMenuItem("Broadcast to Its Agents…", .broadcast(.project(pk)))) }
            out += [.divider, RowMenuItem("Copy Path", .copy(pr.path))]
            if pr.git != false { out += [.divider, RowMenuItem("Move to Another Machine…", .moveProject(project: pk))] }
            out += [.divider, RowMenuItem("Remove Project…", .removeProject(project: pk))]
        case .machine, .cli:
            let isMachine: Bool = { if case .machine = node.kind { return true }; return false }()
            if machineOnline {
                out = [RowMenuItem("Start an Agent", .startAgent(machine: machine, cwd: nil, inProject: false)),
                       RowMenuItem("New Terminal", .newTerminal(machine: machine, cwd: nil, inProject: false))]
                if machine == "local" { out.append(RowMenuItem("SSH to a Host…", .sshToHost)) }
                let scope: BroadcastScope = isMachine ? .machine(machine) : .cli(machine)
                if hasRunning(scope) { out.append(RowMenuItem("Broadcast…", .broadcast(scope))) }
                if isMachine { out += [.divider, RowMenuItem("Add Project…", .addProject(machine: machine))] }
            }
            if isMachine { out += [RowMenuItem("Add Machine…", .addMachine), RowMenuItem("New Sandbox…", .newSandbox)] }
            if isMachine && machineOnline {
                out.append(RowMenuItem("Skills & MCP Servers…", .skillsSettings(machine: machine)))
            }
            if isMachine {
                // What a machine's own connection and server need, online
                // or not: a machine that went is reconnected or upgraded.
                out += [.divider, RowMenuItem("Reconnect", .reconnect(machine: machine))]
                if machineOnline { out.append(RowMenuItem("Reload Server", .reloadServer(machine: machine))) }
                if machine != "local" {
                    out += [RowMenuItem("Upgrade conch There", .upgrade(machine: machine)),
                            RowMenuItem("Remove Machine…", .removeMachine(machine: machine))]
                }
            }
        case .workspace:
            guard machineOnline else { return [] }
            out = [RowMenuItem("Add Project…", .addProject(machine: machine))]
            if hasRunning(.machine(machine)) { out.append(RowMenuItem("Broadcast…", .broadcast(.machine(machine)))) }
            if !projects.isEmpty { out.append(.divider) }
            out += projects.map { RowMenuItem("New Task in \($0.name)…", .newTask(project: key(machine, $0.id))) }
        case .section(let name):
            // A project's Agents or Terminals: a message to those alone.
            guard machineOnline, name == "Agents" || name == "Terminals", node.id.hasPrefix("s:") else { return [] }
            let rest = node.id.dropFirst(2)
            guard let colon = rest.lastIndex(of: ":") else { return [] }
            let scope = BroadcastScope.section(project: String(rest[..<colon]), agents: name == "Agents")
            return hasRunning(scope) ? [RowMenuItem(name == "Agents" ? "Broadcast to These Agents…" : "Broadcast to These Terminals…", .broadcast(scope))] : []
        case .savedHost(let h):
            return [RowMenuItem("Connect", .connectHost(h)), .divider, RowMenuItem("Forget This Host", .forgetHost(h))]
        case .sessions, .files, .subagent, .more:
            return []
        }
        return out
    }
}

extension TreeNode {
    /// The machine a row is on.
    public var machine: String? {
        switch kind {
        case .machine(let m), .cli(let m), .workspace(let m): return m
        case .agent(let k), .terminal(let k), .project(let k), .sessions(let k), .files(let k), .subagent(let k, _), .more(let k, _): return unkey(k).machine
        case .savedHost: return "local"
        case .branch(let pk, _, _): return unkey(pk).machine
        case .section: return unkey(String(id.dropFirst(2).split(separator: ":").first ?? "")).machine
        }
    }
}

/// The capabilities a server lacks that this app's own conch has: what a
/// reload or an upgrade would bring. A server newer than the app lacks
/// none; one that said nothing (older than capabilities) lacks them all.
public func capabilitiesBehind(server: [String]?, app: [String]) -> [String] {
    let have = Set(server ?? [])
    return app.filter { !have.contains($0) }
}

/// What `conch version --json` says of this app's conch.
public struct BuildInfo: Decodable, Sendable {
    public var version: String?
    public var capabilities: [String]?
}

/// What deleting a branch would lose, as branch.discard's dry run says it.
public struct BranchDiscardResult: Decodable, Equatable, Sendable {
    public var worktree: String?
    public var uncommitted: [String]?
    public var unmerged: Int?
    public var done: Bool?

    public init(worktree: String? = nil, uncommitted: [String]? = nil, unmerged: Int? = nil) {
        self.worktree = worktree
        self.uncommitted = uncommitted
        self.unmerged = unmerged
    }

    /// What is lost, said in a sentence; nil when nothing is.
    public var loss: String? {
        var parts: [String] = []
        let files = uncommitted ?? []
        if !files.isEmpty {
            let shown = files.prefix(5).joined(separator: ", ")
            parts.append("\(files.count) uncommitted file\(files.count == 1 ? "" : "s") (\(shown)\(files.count > 5 ? ", …" : ""))")
        }
        if let n = unmerged, n > 0 {
            parts.append("\(n) commit\(n == 1 ? "" : "s") in neither the base nor its upstream")
        }
        return parts.isEmpty ? nil : "This loses " + parts.joined(separator: " and ") + "."
    }
}

/// A sandbox machine's own menu items, as the TUI's sandbox rows offer:
/// start one that is not running, stop or delete it, and what it has cost.
public func sandboxMenu(machine: String, running: Bool) -> [RowMenuItem] {
    var out: [RowMenuItem] = [.divider]
    out.append(running ? RowMenuItem("Stop Sandbox…", .sandbox(machine: machine, verb: "stop"))
                       : RowMenuItem("Start Sandbox", .sandbox(machine: machine, verb: "start")))
    out += [RowMenuItem("What It Has Cost", .sandbox(machine: machine, verb: "usage")),
            RowMenuItem("Delete Sandbox…", .sandbox(machine: machine, verb: "rm"))]
    return out
}

/// `conch sandbox`'s arguments for a verb on a machine: -y where the app
/// has asked already, since conch would otherwise ask on a terminal nobody
/// is at.
public func sandboxArgs(provider: String, verb: String, machine: String) -> [String] {
    ["sandbox", "-provider", provider, verb] + (verb == "stop" || verb == "rm" ? ["-y"] : []) + [machine]
}

/// Whether a server.reload that threw is a reload under way: its connection
/// closing under the call, as an exec drops it. Anything the server said is
/// a refusal, to be said — "already runs build …" most often.
public func reloadUnderway(_ error: Error) -> Bool {
    (error as? ConchError)?.code == "closed"
}

/// What removing a project from the sidebar does, as the TUI's x decides:
/// a project another workspace shows too only leaves the one on screen;
/// otherwise conch forgets it (project.remove), its files left alone.
public enum ProjectRemoval: Equatable, Sendable {
    case leaveWorkspace(id: String)
    case removeFromConch
}

public func projectRemoval(_ project: ProjectInfo, machine: String, workspaces: [Workspace], current: Int) -> ProjectRemoval {
    guard workspaces.indices.contains(current), workspaces.count > 1 else { return .removeFromConch }
    let here = workspaces[current]
    let elsewhere = workspaces.indices.contains { $0 != current && workspaces[$0].shows(project, machine: machine) }
    // The first shows whatever no other claims, so it can't leave a
    // project on its own: there it goes from conch.
    return elsewhere && !here.first ? .leaveWorkspace(id: here.id) : .removeFromConch
}
