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
    /// The agent's branch in one tab: its files, a terminal, its chat and
    /// its own terminal, arranged by the workbench layout.
    case workbench(pane: String)
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
    /// A folder on this Mac shown in the Finder.
    case reveal(String)
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
    /// The same, with Conch Cloud chosen as where it goes.
    case moveToCloud(project: String)
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
    /// A Conch Cloud machine woken or put to sleep (conch cloud machine
    /// VERB), by its machine.
    case cloud(machine: String, verb: String)
    /// A folder of your own made in a section of a machine's CLI, renamed,
    /// or taken away (what is in it stays, nothing closes).
    case newFolder(machine: String, section: FolderSection)
    case renameFolder(machine: String, section: FolderSection, name: String)
    case removeFolder(machine: String, section: FolderSection, name: String)
    /// A pane or a saved host put in a folder of its section, nil for out
    /// of every one; or in a folder made for it, its name asked for.
    case moveToFolder(FolderItem, folder: String?)
    case moveToNewFolder(FolderItem)
    /// Another ssh session to the host a session is logged in to, with
    /// the same options (the TUI's d).
    case anotherSession(pane: String)
    /// A saved host's name, options and folder, or a session's host saved
    /// with them (the TUI's e).
    case editHost(host: String, args: [String])
    /// This computer's key put on a host so it logs in without a password,
    /// in a terminal where ssh asks for the password the once (the TUI's K).
    case copyKey(host: String, args: [String])
    /// A host made one of conch's machines: conch installed there.
    case addAsMachine(host: String)
    /// A pane's program asked to draw its screen again (pane.redraw).
    case redraw(pane: String)
    /// Every project of a machine's Workspace folded.
    case collapseProjects(machine: String)
    /// Project groups (project.groups.v1): a new one picked from a list; a
    /// project grouped with another (asked first) or added to a group or
    /// taken out of one; a group renamed, given notes for the agents, or
    /// taken away (its projects stay). Projects are key(machine, id).
    case newGroup(machine: String)
    case groupWith(project: String, other: String)
    case addToGroup(project: String, group: String)
    case takeOutOfGroup(project: String, group: String)
    case renameGroup(machine: String, name: String)
    case groupNotes(machine: String, name: String)
    case removeGroup(machine: String, name: String)
    /// Tasks across a group's repos (group.task.v1): a new one asked for;
    /// a task's branch pushed, or its pull requests opened, in every repo;
    /// a task forgotten, its folder removed (the branches stay).
    case newGroupTask(machine: String, group: String)
    /// A task's page: every repo on one page (task|machine|group|branch).
    case openGroupTask(machine: String, group: String, branch: String)
    case groupTaskPush(machine: String, group: String, branch: String)
    case groupTaskPRs(machine: String, group: String, branch: String)
    case removeGroupTask(machine: String, group: String, branch: String)
    case divider
}

/// What goes in a folder: a pane (key(machine, id)) or a saved ssh host.
public enum FolderItem: Equatable, Sendable {
    case pane(String)
    case host(String)
}

public struct RowMenuItem: Equatable, Sendable {
    public var title: String
    public var action: RowAction
    /// A submenu's items; its own action is then never performed.
    public var children: [RowMenuItem]

    public init(_ title: String, _ action: RowAction, children: [RowMenuItem] = []) {
        self.title = title
        self.action = action
        self.children = children
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
    /// folders is ui.json's (loadFolders) and savedHosts this computer's,
    /// for what goes in a folder and where it is now.
    public func menu(for node: TreeNode, machine: String, machineOnline: Bool = true, label: String? = nil,
                     among: [MachineEntry] = [], folders: [String: [Folder]] = [:], savedHosts: [SavedHost] = [],
                     canGroup: Bool = true, canGroupTask: Bool = true) -> [RowMenuItem] {
        var out: [RowMenuItem] = []
        switch node.kind {
        case .agent(let k), .terminal(let k):
            guard let p = pane(unkey(k).id) else { return [] }
            let agent = p.agent != nil
            out = [RowMenuItem("Open", .open), RowMenuItem("Rename…", .rename(pane: k))]
            if agent && p.running {
                out.append(RowMenuItem(p.agent?.state == .blocked ? "Prompt… (It Is Waiting for an Answer)" : "Prompt…", .prompt(pane: k)))
                out.append(RowMenuItem("Open Workbench", .workbench(pane: k)))
            }
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
            let saved = savedHosts.first { $0.host == p.sshTarget }
            if machine == "local", let host = p.sshTarget {
                let args = saved?.args ?? p.sshArgs
                out += [.divider]
                if machineOnline { out.append(RowMenuItem("Another Session to \(saved?.title ?? host)", .anotherSession(pane: k))) }
                out += [RowMenuItem("Copy ssh Command", .copy(sshCommandLine(host: host, args: args))),
                        RowMenuItem(saved == nil ? "Save This Host…" : "Edit Host (Name, Folder, Options)…", .editHost(host: host, args: args)),
                        RowMenuItem("Set Up Login Without a Password…", .copyKey(host: host, args: args)),
                        RowMenuItem("Add as a Machine (conch There Too)…", .addAsMachine(host: host))]
            }
            // In a folder only where folders are: outside every project.
            if p.projectID == nil || project(p.projectID!) == nil {
                let sec: FolderSection = agent ? .agents : p.isSSH ? .ssh : .terminals
                let item: FolderItem = sec == .ssh && saved != nil ? .host(saved!.host) : .pane(k)
                out += [.divider, folderMenu(item, machine: machine, section: sec, folders: folders, panes: panes)]
            }
            // What conch's commands call it (conch read p12), which no row says.
            let ref = paneRef(machine: machine, label: label, pane: p.id, among: among)
            out += [.divider, RowMenuItem("Copy Pane ID (\(ref))", .copy(ref))]
            if p.running && machineOnline { out.append(RowMenuItem("Redraw", .redraw(pane: k))) }
            out += [
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
            // Only this Mac's folders are the Finder's to show.
            if machine == "local" { out.append(RowMenuItem("Reveal in Finder", .reveal(pr.path))) }
            if canGroup { out += [.divider] + groupItems(pk, machine: machine) }
            if pr.git != false {
                out += [.divider, RowMenuItem("Move to Another Machine…", .moveProject(project: pk))]
                // This Mac's project, before the lid closes.
                if machine == "local" { out.append(RowMenuItem("Move to Conch Cloud…", .moveToCloud(project: pk))) }
            }
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
            if !isMachine { out += [.divider, newFolderMenu(machine)] }
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
            if canGroup && projects.count > 1 { out += [.divider, RowMenuItem("New Project Group…", .newGroup(machine: machine))] }
            if !projects.isEmpty { out += [.divider, RowMenuItem("Collapse All Projects", .collapseProjects(machine: machine))] }
        case .section(let name):
            // A project's Agents or Terminals: a message to those alone.
            guard machineOnline, name == "Agents" || name == "Terminals", node.id.hasPrefix("s:") else { return [] }
            let rest = node.id.dropFirst(2)
            guard let colon = rest.lastIndex(of: ":") else { return [] }
            let scope = BroadcastScope.section(project: String(rest[..<colon]), agents: name == "Agents")
            return hasRunning(scope) ? [RowMenuItem(name == "Agents" ? "Broadcast to These Agents…" : "Broadcast to These Terminals…", .broadcast(scope))] : []
        case .savedHost(let h):
            let args = savedHosts.first { $0.host == h }?.args ?? []
            return [RowMenuItem("Connect", .connectHost(h)),
                    RowMenuItem("Copy ssh Command", .copy(sshCommandLine(host: h, args: args))), .divider,
                    folderMenu(.host(h), machine: "local", section: .ssh, folders: folders, panes: panes),
                    RowMenuItem("Edit Host (Name, Folder, Options)…", .editHost(host: h, args: args)),
                    RowMenuItem("Set Up Login Without a Password…", .copyKey(host: h, args: args)),
                    RowMenuItem("Add as a Machine (conch There Too)…", .addAsMachine(host: h)),
                    .divider, RowMenuItem("Forget This Host", .forgetHost(h))]
        case .cliSection(_, let sec):
            var items: [RowMenuItem] = [RowMenuItem("New Folder…", .newFolder(machine: machine, section: sec))]
            if machineOnline {
                switch sec {
                case .agents: items.append(RowMenuItem("Start an Agent", .startAgent(machine: machine, cwd: nil, inProject: false)))
                case .terminals: items.append(RowMenuItem("New Terminal", .newTerminal(machine: machine, cwd: nil, inProject: false)))
                case .ssh: if machine == "local" { items.append(RowMenuItem("SSH to a Host…", .sshToHost)) }
                }
            }
            return items
        case .group(_, let name):
            guard let g = groups.first(where: { $0.name == name }) else { return [] }
            let others = projects.filter { !g.projects.contains($0.id) }
            let members = g.projects.compactMap { project($0) }
            var items: [RowMenuItem] = []
            if canGroupTask && machineOnline {
                items += [RowMenuItem("New Task Across Repos…", .newGroupTask(machine: machine, group: name)), .divider]
            }
            items += [RowMenuItem("Rename Group…", .renameGroup(machine: machine, name: name)),
                                        RowMenuItem((g.notes ?? "").isEmpty ? "Notes for the Agents…" : "Edit Notes for the Agents…", .groupNotes(machine: machine, name: name))]
            if !others.isEmpty {
                items.append(RowMenuItem("Add a Project", .divider, children: others.map {
                    RowMenuItem($0.name, .addToGroup(project: key(machine, $0.id), group: name))
                }))
            }
            if members.count > 1 {
                items.append(RowMenuItem("Take a Project Out", .divider, children: members.map {
                    RowMenuItem($0.name, .takeOutOfGroup(project: key(machine, $0.id), group: name))
                }))
            }
            return items + [.divider, RowMenuItem("Remove Group (Projects Stay)", .removeGroup(machine: machine, name: name))]
        case .groupTask(_, let group, let branch):
            guard let t = groups.first(where: { $0.name == group })?.tasks?.first(where: { $0.branch == branch }) else { return [] }
            var items = [RowMenuItem("Open Task Page", .openGroupTask(machine: machine, group: group, branch: branch))]
            if t.panes?.contains(where: { pane($0) != nil }) == true { items.append(RowMenuItem("Open Its Agent", .open)) }
            if machineOnline {
                items += [RowMenuItem("Push \(branch) in Every Repo", .groupTaskPush(machine: machine, group: group, branch: branch)),
                          RowMenuItem("Open Pull Requests in Every Repo…", .groupTaskPRs(machine: machine, group: group, branch: branch))]
            }
            items += [.divider, RowMenuItem("Copy Task Folder", .copy(t.folder))]
            if machine == "local" { items.append(RowMenuItem("Reveal Task Folder in Finder", .reveal(t.folder))) }
            return items + [.divider, RowMenuItem("Remove Task (Branches Stay)…", .removeGroupTask(machine: machine, group: group, branch: branch))]
        case .folder(_, let sec, let name):
            return [RowMenuItem("Rename Folder…", .renameFolder(machine: machine, section: sec, name: name)),
                    RowMenuItem("New Folder…", .newFolder(machine: machine, section: sec)), .divider,
                    RowMenuItem("Remove Folder (What's in It Stays)", .removeFolder(machine: machine, section: sec, name: name))]
        case .sessions, .files, .subagent, .more:
            return []
        }
        return out
    }
}

extension Snapshot {
    /// Move to Folder ▸: the section's folders, the one it is in ticked,
    /// out of them when it is in one, and a new folder for it.
    func folderMenu(_ item: FolderItem, machine: String, section: FolderSection, folders: [String: [Folder]], panes: [PaneInfo]) -> RowMenuItem {
        let fs = folders[folderKey(machine: machine, section: section)] ?? []
        let now = currentFolder(item, machine: machine, section: section, folders: fs)
        var kids = fs.map { RowMenuItem(($0.name == now ? "✓ " : "") + $0.name, .moveToFolder(item, folder: $0.name)) }
        if !kids.isEmpty { kids.append(.divider) }
        kids.append(RowMenuItem("New Folder…", .moveToNewFolder(item)))
        if now != nil { kids.append(RowMenuItem("Out of Any Folder", .moveToFolder(item, folder: nil))) }
        return RowMenuItem("Move to Folder", .divider, children: kids)
    }

    /// The folder an item is in now, as the tree places it.
    func currentFolder(_ item: FolderItem, machine: String, section: FolderSection, folders fs: [Folder]) -> String? {
        switch item {
        case .host(let h): return fs.first { $0.hosts.contains(h) }?.name
        case .pane(let k):
            let id = unkey(k).id
            var taken: Set<String> = []
            let mine = panes.filter { $0.projectID == nil || project($0.projectID!) == nil }
            for f in fs where f.claim(mine, taken: &taken).contains(where: { $0.id == id }) { return f.name }
            return nil
        }
    }

    /// A project's group items: Group With ▸ the other projects and the
    /// groups it is not in, and Take Out of each group it is in.
    func groupItems(_ pk: String, machine: String) -> [RowMenuItem] {
        let id = unkey(pk).id
        var with = projects.filter { $0.id != id }.map { RowMenuItem($0.name, .groupWith(project: pk, other: key(machine, $0.id))) }
        let joinable = groups.filter { !$0.projects.contains(id) }
        if !joinable.isEmpty {
            if !with.isEmpty { with.append(.divider) }
            with += joinable.map { RowMenuItem("Add to \($0.name)", .addToGroup(project: pk, group: $0.name)) }
        }
        var out: [RowMenuItem] = []
        if !with.isEmpty { out.append(RowMenuItem("Group With", .divider, children: with)) }
        out += groups(of: id).map { RowMenuItem("Take Out of \($0.name)", .takeOutOfGroup(project: pk, group: $0.name)) }
        return out
    }

    /// New Folder ▸ on a CLI row: which of its sections it goes in.
    func newFolderMenu(_ machine: String) -> RowMenuItem {
        RowMenuItem("New Folder", .divider, children: FolderSection.allCases.map {
            RowMenuItem("In \($0.title)…", .newFolder(machine: machine, section: $0))
        })
    }
}

/// A login as somebody would type it: ssh, its options, then the host —
/// each word quoted for a shell where it needs to be.
public func sshCommandLine(host: String, args: [String]) -> String {
    (["ssh"] + args + [host]).map(shellQuote).joined(separator: " ")
}

func shellQuote(_ w: String) -> String {
    let safe = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./-_")
    if !w.isEmpty, w.unicodeScalars.allSatisfy(safe.contains) { return w }
    return "'" + w.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

/// The buttons a project or machine row shows on the pointer, as Orca's
/// project row has them: what it is most often for, an agent (or a
/// terminal) started there, and its whole menu. Every one is on the row's
/// right-click menu too.
public struct RowButtons: Equatable, Sendable {
    /// The + button: a new task in a project, a project on a machine.
    public var primary: RowMenuItem?
    /// Where Start Agent starts one, and whether in the project: the bot
    /// button lists the agents there, and a terminal.
    public var agentMachine: String?
    public var agentCwd: String?
    public var agentInProject: Bool
    /// The first button's icon: + for most, folder-plus on CLI, where it
    /// is a menu of the sections to make a folder in (primary's children).
    public var primaryIcon: String
    /// What the bot button lists instead of agents: Workspace's new task
    /// in each of its projects, a task needing a project to be in.
    public var tasks: [RowMenuItem]
    /// Whether the bot button also offers SSH to a Host… (this CLI).
    public var ssh: Bool

    public init(primary: RowMenuItem? = nil, agentMachine: String? = nil, agentCwd: String? = nil, agentInProject: Bool = false,
                primaryIcon: String = "plus", tasks: [RowMenuItem] = [], ssh: Bool = false) {
        self.primary = primary
        self.agentMachine = agentMachine
        self.agentCwd = agentCwd
        self.agentInProject = agentInProject
        self.primaryIcon = primaryIcon
        self.tasks = tasks
        self.ssh = ssh
    }

    /// Starting the agent named, or a terminal for nil, where it says.
    public func start(_ agent: String?) -> RowAction? {
        guard let m = agentMachine else { return nil }
        return agent == nil ? .newTerminal(machine: m, cwd: agentCwd, inProject: agentInProject)
            : .startAgent(machine: m, cwd: agentCwd, inProject: agentInProject)
    }
}

extension Snapshot {
    /// A row's buttons: a project's, a machine's, Workspace's and CLI's.
    /// Offline nothing is started, so a machine shows only its menu
    /// (reconnect, upgrade), CLI its folders and menu, and a project and
    /// Workspace none at all.
    public func rowButtons(for node: TreeNode, machine: String, machineOnline: Bool = true) -> RowButtons? {
        switch node.kind {
        case .project(let pk):
            guard machineOnline, let pr = project(unkey(pk).id) else { return nil }
            return RowButtons(primary: RowMenuItem(pr.git == false ? "New Task (an Agent with a Prompt, Here)…" : "New Task…", .newTask(project: pk)),
                              agentMachine: machine, agentCwd: pr.path, agentInProject: true)
        case .machine:
            guard machineOnline else { return RowButtons() }
            return RowButtons(primary: RowMenuItem("Add Project…", .addProject(machine: machine)), agentMachine: machine)
        case .group(let m, let name):
            guard machineOnline else { return nil }
            return RowButtons(primary: RowMenuItem("New Task Across Repos…", .newGroupTask(machine: m, group: name)))
        case .workspace:
            guard machineOnline else { return nil }
            return RowButtons(primary: RowMenuItem("Add Project…", .addProject(machine: machine)),
                              tasks: projects.map { RowMenuItem("New Task in \($0.name)…", .newTask(project: key(machine, $0.id))) })
        case .cli:
            // Folders are ui.json's, so they are made with the machine away.
            return RowButtons(primary: newFolderMenu(machine), agentMachine: machineOnline ? machine : nil,
                              primaryIcon: "folder-plus", ssh: machineOnline && machine == "local")
        default:
            return nil
        }
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
        case .cliSection(let m, _), .folder(let m, _, _), .group(let m, _), .groupTask(let m, _, _): return m
        }
    }
}

/// The capabilities a server lacks that this app's own conch has: what a
/// reload or an upgrade would bring. A server newer than the app lacks
/// none; one that said nothing (older than capabilities) lacks them all.
/// Those optional (a server of this build may leave them out where its
/// machine cannot give them, as one with no Keychain does) are no sign of
/// an older build.
public func capabilitiesBehind(server: [String]?, app: [String], optional: [String] = []) -> [String] {
    let have = Set(server ?? [])
    return app.filter { !have.contains($0) && !optional.contains($0) }
}

/// What `conch version --json` says of this app's conch.
public struct BuildInfo: Decodable, Sendable {
    public var version: String?
    public var capabilities: [String]?
    public var optional: [String]?
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
