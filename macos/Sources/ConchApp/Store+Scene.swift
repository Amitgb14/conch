import ConchKit
import Foundation

/// Fireworks for a merge: which pull request, and since when.
struct Celebration: Equatable {
    var merged: MergedPR
    var at: Date
}

extension Store {
    /// The project whose scene this window shows ([app.project_backgrounds]):
    /// a project's window, its project; any other, the project of the tab
    /// in front. Nil is the window's own scene.
    var sceneProject: String? {
        if let p = projectScope { return scopeName(p) }
        return layout.focused.flatMap(projectName(ofTab:))
    }

    /// The project a tab belongs to, by name: a pane's, a branch's
    /// changes, a project's sessions, commits, files or agent setup. A
    /// browser or a task's page is no one project's.
    func projectName(ofTab k: String) -> String? {
        if let a = parseChatKey(k) ?? parseBenchShellKey(k) { return projectName(ofTab: a) }
        if let c = parseChangesKey(k) { return snapshots[c.machine]?.project(c.project)?.name }
        if let c = parseSessionsKey(k) ?? parseCommitsKey(k) ?? parseFilesKey(k) ?? parseSetupKey(k).map({ ($0.machine, $0.project) }) {
            return snapshots[c.machine]?.project(c.project)?.name
        }
        guard parseBrowserKey(k) == nil, parseTaskKey(k) == nil else { return nil }
        let (m, _) = unkey(k)
        return pane(k)?.projectID.flatMap { snapshots[m]?.project($0)?.name }
    }

    /// The agents this window shows, for the departure board: when each
    /// started, which agent, its project and branch, and its state.
    var departures: [Departure] {
        var out: [Departure] = []
        for (m, snap) in snapshots {
            for p in snap.shown(in: ws, machine: m) where p.running {
                guard let a = p.agent else { continue }
                let project = p.projectID.flatMap { snap.project($0)?.name }
                let to = [project, p.branch].compactMap { $0 }.joined(separator: "/")
                out.append(Departure(time: p.created, agent: a.name, to: to.isEmpty ? p.name : to, state: a.state))
            }
        }
        return out
    }

    /// Fireworks over the scene for a pull request that merged, in a
    /// window with a scene that shows its project — unless
    /// background_fireworks is off. They end by themselves.
    func celebrate(_ m: MergedPR, machine: String, project: ProjectInfo) {
        let look = appLook.scene
        guard look.fireworks, look.scene(for: project.name) != .none,
              ws?.shows(project, machine: machine) ?? true else { return }
        let c = Celebration(merged: m, at: Date())
        celebration = c
        DispatchQueue.main.asyncAfter(deadline: .now() + Fireworks.duration) { [weak self] in
            if self?.celebration == c { self?.celebration = nil }
        }
    }
}
