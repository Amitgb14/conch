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
            do { try await c.call("pane.team", Params(id: id, team: team)) } catch { self.error = "\(error)" }
        }
    }

    /// Asks a new team's name and puts the pane in it. A team made here
    /// lives on the pane; Settings → Teams gives it a colour and rules.
    func newTeam(_ k: String) {
        guard let name = ask("New Team", info: "Put this agent in a team of this name. Settings → Teams gives a team a colour, and rules that put new agents in it.",
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

    /// A team's colour in the window.
    func teamColor(_ name: String) -> RGB { ConchKit.teamColor(name, in: teams) }
}
