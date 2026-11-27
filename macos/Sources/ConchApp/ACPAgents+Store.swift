import AppKit
import ConchKit
import Foundation

// Agents that speak the Agent Client Protocol, added, checked, changed and
// removed on the machine whose server is asked (agent.acp.add.v1): the
// server writes agents/NAME.toml there and offers the agent at once.

/// Add an ACP Agent's sheet: for which machine, and the agent it changes.
struct ACPSheetTarget: Identifiable {
    var machine: String
    var agent: AgentAvailability?
    var id: String { machine + "|" + (agent?.name ?? "") }
}

extension Store {
    /// Whether machine's server adds ACP agents.
    func addsACPAgents(on machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(capAgentACPAdd) == true
    }

    /// The machines an ACP agent can be added to now.
    var acpMachines: [MachineLink] { machines.filter { addsACPAgents(on: $0.id) } }

    /// The names of the agents conch supports on machine, which a
    /// manifest may not take.
    func supportedAgents(on machine: String) -> Set<String> {
        Set((agentStatus[machine] ?? []).filter { $0.tier == "supported" }.map(\.name))
    }

    /// Opens the sheet from the tree's agent menus.
    func askAddACP(on machine: String) { acpSheet = ACPSheetTarget(machine: machine) }

    /// Starts the command once on machine and says what it offers.
    func checkACP(_ spec: ACPSpec, on machine: String) async throws -> ACPCheck {
        guard let c = conns[machine] else { throw StoreError("\(label(machine)) is not connected") }
        return try await c.call("agent.acp.check", spec, as: ACPCheck.self)
    }

    /// Adds (or with replace, changes) an ACP agent on machine, and looks
    /// at the machine's agents again so every menu has it.
    @discardableResult
    func addACP(_ spec: ACPSpec, replace: Bool, on machine: String) async throws -> AgentAvailability {
        guard let c = conns[machine] else { throw StoreError("\(label(machine)) is not connected") }
        let a = try await c.call("agent.acp.add", ACPAddParams(spec: spec, replace: replace), as: AgentAvailability.self)
        checkAgents(on: machine)
        say("\(a.title) \(replace ? "changed" : "added") on \(label(machine))\(a.installed ? " — in every agent menu now" : " — not installed there yet")")
        return a
    }

    /// Removes an ACP agent added on machine, after asking: its manifest,
    /// never the program or its sessions.
    func removeACP(_ a: AgentAvailability, on machine: String) {
        let alert = NSAlert()
        alert.messageText = "Remove \(a.title)?"
        alert.informativeText = "Its manifest, agents/\(a.name).toml on \(label(machine)), is deleted. The program and its sessions stay."
        alert.addButton(withTitle: "Remove")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn, let c = conns[machine] else { return }
        struct P: Encodable { var name: String }
        Task {
            do {
                try await c.call("agent.acp.remove", P(name: a.name))
                checkAgents(on: machine)
                say("\(a.title) removed from \(label(machine))")
            } catch {
                say("Could not remove \(a.title): \(error)", .err)
            }
        }
    }

    /// Shows an ACP agent's manifest in Finder; this Mac's only.
    func showManifest(_ a: AgentAvailability) {
        let url = URL(fileURLWithPath: conchDir).appendingPathComponent("agents/\(a.name).toml")
        NSWorkspace.shared.activateFileViewerSelecting([url])
    }

    /// Whether agent on machine is run over ACP, whose sessions conch
    /// cannot delete.
    func isACPAgent(_ agent: String, on machine: String) -> Bool {
        (agentStatus[machine] ?? []).contains { $0.name == agent && $0.isACP }
    }
}

/// An error said in the app's own words.
struct StoreError: Error, CustomStringConvertible {
    var description: String
    init(_ d: String) { description = d }
}
