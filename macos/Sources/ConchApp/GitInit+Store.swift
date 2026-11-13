import AppKit
import ConchKit

// A project that is a plain folder made a git repository when work needs a
// branch in it — a task, a task across a group — or from its row's
// Initialize Git…. What to ask is ConchKit's (branchReadiness); the asking
// and the call are here.
extension Store {
    /// project.git_init for a project on machine; the repository it became
    /// put in the snapshot at once, so what was asked for next finds it.
    func initGit(machine: String, project id: String) async throws {
        guard let c = connection(machine) else { throw ConchError(code: "closed", message: "\(label(machine)) is not connected") }
        struct P: Encodable { var id: String }
        let p = try await c.call("project.git_init", P(id: id), as: ProjectInfo.self)
        snapshots[machine, default: Snapshot()].upsert(p)
    }

    /// Runs go once every one of projects (ids on machine) is a git
    /// repository: those that are plain folders are made repositories after
    /// asking. inFolder, when given, is offered for a task in one project:
    /// the agent works in the folder itself, with no branch. Cancel does
    /// nothing, so a sheet the question came from keeps what was typed.
    func whenBranchable(machine: String, projects ids: [String], inFolder: (() -> Void)? = nil, _ go: @escaping () -> Void) {
        let projects = ids.compactMap { snapshots[machine]?.project($0) }
        let caps = connection(machine)?.hello?.capabilities
        switch branchReadiness(projects, capabilities: caps, machineName: label(machine), inFolder: inFolder != nil) {
        case .ready:
            go()
        case .reloadServer(let why):
            say(why, .err)
        case .ask(let q):
            let a = NSAlert()
            a.messageText = q.title
            a.informativeText = q.info
            a.addButton(withTitle: "Initialize Git")
            if q.inFolder { a.addButton(withTitle: "Work in the Folder") }
            a.addButton(withTitle: "Cancel")
            switch a.runModal() {
            case .alertFirstButtonReturn:
                Task {
                    do {
                        try await running("Initializing git") {
                            for id in q.projects { try await initGit(machine: machine, project: id) }
                        }
                    } catch {
                        say("Could not initialize git: \(error)", .err)
                        return
                    }
                    go()
                }
            case .alertSecondButtonReturn where q.inFolder:
                inFolder?()
            default:
                return
            }
        }
    }

    /// The row menu's Initialize Git…: asks, then makes the project's
    /// folder a git repository.
    func gitInitProject(_ pk: String) {
        let (m, id) = unkey(pk)
        guard let pr = snapshots[m]?.project(id) else { return }
        if let why = gitInitRefusal(pr, capabilities: connection(m)?.hello?.capabilities, machineName: label(m)) {
            say(why, .err)
            return
        }
        let a = NSAlert()
        a.messageText = "Make \(pr.name) a git repository?"
        a.informativeText = "\(pr.path)\n\nconch runs git init and makes an empty first commit, so tasks can work on branches of their own. The files already there stay as they are, not committed."
        a.addButton(withTitle: "Initialize Git")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        job("Initializing git in \(pr.name)", done: "\(pr.name) is a git repository now") {
            try await self.initGit(machine: m, project: id)
        }
    }

    /// A task in a plain folder with no branch: the agent started in the
    /// folder itself with the prompt, as the TUI's plain task does. An
    /// issue or a time goes through `conch task`, which does the same.
    func startTaskInFolder(_ d: TaskDraft) {
        let (m, pr) = unkey(d.project)
        if (d.issue ?? 0) > 0 || !taskWhenArgs(d.when).isEmpty {
            startTask(d, checked: true)
            return
        }
        guard let c = connection(m), let dir = snapshots[m]?.project(pr)?.path else { return }
        struct Create: Encodable {
            var agent: String?
            var prompt: String
            var cwd: String
            var cols: Int?
            var rows: Int?
        }
        let size = newPaneSize()
        taskDraft = nil
        Task {
            if !d.agent.isEmpty, !(await readyAgent(d.agent, on: m)) { return }
            do {
                let p = try await c.call("pane.create", Create(agent: d.agent.isEmpty ? nil : d.agent, prompt: d.prompt, cwd: dir,
                                                               cols: size?.cols, rows: size?.rows), as: PaneInfo.self)
                snapshots[m, default: Snapshot()].upsert(p)
                joinWorkspace(machine: m, pane: p)
                layout.open(key(m, p.id))
                focusTerminal()
            } catch {
                self.error = "Could not start the task: \(error)"
            }
        }
    }
}
