import AppKit
import ConchKit

/// Agent teams in the window: putting an agent in one (pane.team.v1), a
/// new one, and a team's agents side by side (Teams.swift for the rules).
extension Store {
    /// Puts a pane in a team by hand: a name, "-" for none whatever the
    /// rules say, "" to leave it to the rules again.
    func setTeam(_ k: String, _ team: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m] else { return }
        guard c.hello?.capabilities?.contains("pane.team.v1") == true else {
            error = "conch on \(label(m)) is from an older build without teams: reload its server (conch server reload)."
            return
        }
        struct Params: Encodable { var id: String; var team: String }
        Task {
            do {
                let p = try await c.call("pane.team", Params(id: id, team: team), as: PaneInfo.self)
                // Kept in config.toml too, so the team is still there once
                // its agents close — once the server took it, and by the
                // name it kept (control characters made printable): a name
                // it refused, too long, would have been a team with nobody.
                // Only for this computer's panes: config.toml is this Mac's.
                if let kept = teamKeptInConfig(machine: m, asked: team, answered: p.team) {
                    self.changeConfig("the team") { addTeamTOML($0, name: kept) }
                }
            } catch { self.error = "\(error)" }
        }
    }

    /// Asks a new team's name and puts the pane in it. A team made here
    /// lives on the pane; Settings → Teams gives it a colour and rules.
    func newTeam(_ k: String) {
        guard let name = ask("New Team", info: "Put this agent in a team of this name. It is kept in config.toml, so it stays when its agents close; Settings → Teams gives it a colour, and rules that put new agents in it.",
                             value: "", button: "Create")?.trimmingCharacters(in: .whitespaces), !name.isEmpty, name != AgentTeam.none else { return }
        setTeam(k, name)
    }

    /// A team's agents side by side in one tab, named for it.
    func showTogether(_ keys: [String], name: String) {
        let alive = keys.filter { pane($0)?.running == true && AppWindows.shared.holder(of: $0, except: self) == nil }
        guard !alive.isEmpty else { return }
        layout.gather(alive, name: name)
        if let first = alive.first { focus(first) }
    }

    /// Every agent and terminal in the window, on each machine, with its
    /// project's name — what a team's rules match on.
    var teamCandidates: [TeamMemberCandidate] {
        snapshots.sorted { $0.key < $1.key }.flatMap { m, s in
            s.panes.map { p in
                TeamMemberCandidate(key: key(m, p.id), pane: p, project: p.projectID.flatMap { id in s.projects.first { $0.id == id }?.name })
            }
        }
    }

    /// Renames a team everywhere: its [[teams]] entry in config.toml, and
    /// every agent put in it by hand. Agents its rules put there follow
    /// the entry.
    func renameTeam(_ name: String) {
        let all = teamCandidates
        let existing = teamNames(teams, panes: all.map(\.pane))
        guard var to = ask("Rename Team", info: "A new name for “\(name)”, on every machine and in every project.", value: name, button: "Rename") else { return }
        to = to.trimmingCharacters(in: .whitespaces)
        if let problem = teamRenameProblem(to, from: name, existing: existing) {
            say(problem, .err)
            return
        }
        guard to != name else { return }
        if teams.contains(where: { $0.name == name }) {
            changeConfig("the team") { renameTeamTOML($0, from: name, to: to) }
        }
        for c in all where c.pane.team == name { setTeam(c.key, to) }
    }

    /// Deletes a team, asking first. One with agents in it asks what
    /// becomes of them: closed, or moved to No team (kept out of every
    /// team, so another team's rules do not take them either).
    func deleteTeam(_ name: String) {
        let all = teamCandidates
        let members = teamMembers(name, teams: teams, panes: all)
        let running = members.filter { pane($0)?.running == true }
        let a = NSAlert()
        a.messageText = "Delete the team “\(name)”?"
        if members.isEmpty {
            a.informativeText = "No agent is in it now."
            a.addButton(withTitle: "Delete")
            a.addButton(withTitle: "Cancel")
            guard a.runModal() == .alertFirstButtonReturn else { return }
        } else {
            let n = members.count
            let projects = Set(all.filter { members.contains($0.key) }.compactMap(\.project)).count
            a.informativeText = "\(n) agent\(n == 1 ? " is" : "s are") in it" + (projects > 1 ? ", in \(projects) projects" : "")
                + ". Close \(n == 1 ? "it" : "them all") with the team, or move \(n == 1 ? "it" : "them") to No team. A closed agent's conversation is saved and can be resumed from Sessions."
            a.alertStyle = .warning
            a.addButton(withTitle: "Move to No Team")
            let close = a.addButton(withTitle: running.isEmpty ? "Delete Agents Too" : "Close Agents and Delete")
            close.hasDestructiveAction = true
            a.addButton(withTitle: "Cancel")
            switch a.runModal() {
            case .alertFirstButtonReturn:
                for k in members { setTeam(k, AgentTeam.none) }
            case .alertSecondButtonReturn:
                for k in members { endPane(k) }
            default:
                return
            }
        }
        if teams.contains(where: { $0.name == name }) {
            changeConfig("the team") { removeTeamTOML($0, name: name) }
        }
    }

    /// Ends a pane without asking: the question was asked for all of them.
    private func endPane(_ k: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m] else { return }
        Task {
            do { try await c.call("pane.close", PaneRefParams(id: id)) } catch { self.error = "\(error)" }
        }
    }

    /// A team's colour in the window.
    func teamColor(_ name: String) -> RGB { ConchKit.teamColor(name, in: teams) }
}
