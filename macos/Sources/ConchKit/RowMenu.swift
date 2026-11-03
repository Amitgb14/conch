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

extension Snapshot {
    /// A tree row's right-click menu: the TUI's row menu (overlay.go), for
    /// what the app does itself. machineOnline false leaves out whatever
    /// would start something there.
    public func menu(for node: TreeNode, machine: String, machineOnline: Bool = true) -> [RowMenuItem] {
        var out: [RowMenuItem] = []
        switch node.kind {
        case .agent(let k), .terminal(let k):
            guard let p = pane(unkey(k).id) else { return [] }
            let agent = p.agent != nil
            out = [RowMenuItem("Open", .open), RowMenuItem("Rename…", .rename(pane: k))]
            if agent && p.running { out.append(RowMenuItem("Prompt…", .prompt(pane: k))) }
            if machineOnline {
                out += [.divider,
                        RowMenuItem("Start an agent here", .startAgent(machine: machine, cwd: p.cwd, inProject: p.projectID != nil)),
                        RowMenuItem("New terminal here", .newTerminal(machine: machine, cwd: p.cwd, inProject: p.projectID != nil))]
            }
            if let b = p.branch, !b.isEmpty, let pr = p.projectID, project(pr) != nil {
                out.append(RowMenuItem("View changes on \(b)", .changes(project: key(machine, pr), branch: b)))
            }
            out += [.divider, RowMenuItem(agent && p.running ? "Close agent…" : "Close", .close(pane: k))]
        case .branch(let pk, let name, _):
            out = [RowMenuItem("View changes", .changes(project: pk, branch: name))]
            let pr = project(unkey(pk).id)
            // Starting on a branch needs its checkout; one not checked out
            // is a task's to make (a worktree of its own).
            if machineOnline, let path = pr?.worktrees?.first(where: { $0.branch == name })?.path {
                out += [RowMenuItem("Start an agent on this branch", .startAgent(machine: machine, cwd: path, inProject: true)),
                        RowMenuItem("Open terminal on this branch", .newTerminal(machine: machine, cwd: path, inProject: true))]
            }
            if machineOnline { out.append(RowMenuItem("New task in project…", .newTask(project: pk))) }
            out += [RowMenuItem("Show in commit graph", .commits(project: pk, branch: name)),
                    .divider, RowMenuItem("Copy branch name", .copy(name))]
        case .project(let pk):
            guard machineOnline, let pr = project(unkey(pk).id) else { return [] }
            out = [RowMenuItem(pr.git == false ? "New task (an agent with a prompt, here)…" : "New task (branch + worktree + agent)…", .newTask(project: pk)),
                   RowMenuItem("Start an agent in project", .startAgent(machine: machine, cwd: pr.path, inProject: true)),
                   RowMenuItem("Open terminal in project", .newTerminal(machine: machine, cwd: pr.path, inProject: true))]
            if pr.git != false { out.append(RowMenuItem("Commit graph", .commits(project: pk, branch: nil))) }
            out += [.divider, RowMenuItem("Copy path", .copy(pr.path))]
        case .machine, .cli:
            let isMachine: Bool = { if case .machine = node.kind { return true }; return false }()
            if machineOnline {
                out = [RowMenuItem("Start an agent", .startAgent(machine: machine, cwd: nil, inProject: false)),
                       RowMenuItem("New terminal", .newTerminal(machine: machine, cwd: nil, inProject: false))]
                if machine == "local" { out.append(RowMenuItem("SSH to a host…", .sshToHost)) }
                if isMachine { out += [.divider, RowMenuItem("Add project…", .addProject(machine: machine))] }
            }
            if isMachine { out.append(RowMenuItem("Add machine…", .addMachine)) }
        case .workspace:
            guard machineOnline else { return [] }
            out = [RowMenuItem("Add project…", .addProject(machine: machine))]
            if !projects.isEmpty { out.append(.divider) }
            out += projects.map { RowMenuItem("New task in \($0.name)…", .newTask(project: key(machine, $0.id))) }
        case .section, .sessions, .files, .subagent:
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
        case .agent(let k), .terminal(let k), .project(let k), .sessions(let k), .files(let k), .subagent(let k, _): return unkey(k).machine
        case .branch(let pk, _, _): return unkey(pk).machine
        case .section: return unkey(String(id.dropFirst(2).split(separator: ":").first ?? "")).machine
        }
    }
}
