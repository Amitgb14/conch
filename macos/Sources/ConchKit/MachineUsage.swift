import Foundation

// Whose usage the app shows. Every machine's server hears from its own
// agents, so plans are kept by machine, as the TUI keeps them, and the
// status bar shows the machine in view's.

/// The server capability behind agent.limits.
public let agentLimitsCapability = "agent.limits.v1"

/// Each machine's plans, as its agents last reported them.
public struct MachinePlans: Equatable, Sendable {
    private var plans: [String: [PlanLimits]] = [:]

    public init() {}

    public func on(_ machine: String) -> [PlanLimits] { plans[machine] ?? [] }

    /// What a machine's server answered, in place of what was kept.
    public mutating func set(_ all: [PlanLimits], on machine: String) { plans[machine] = all }

    /// One account's new report (the agent.limits event).
    public mutating func report(_ l: PlanLimits, on machine: String) {
        var all = on(machine)
        all.removeAll { $0.usageKey == l.usageKey }
        all.append(l)
        plans[machine] = all
    }

    /// Lets go of machines that are no longer there.
    public mutating func keep(only machines: Set<String>) {
        plans = plans.filter { machines.contains($0.key) }
    }
}

/// The machine whose usage the status bar shows: the pane's being typed
/// into, as in the TUI, else that of what is selected; this computer for
/// what is on none (a browser tab).
public func usageMachine(typing: Bool, row: String?, focusedTab: String?, known: Set<String>) -> String {
    // A chat is its agent's.
    let tab = focusedTab.map { parseChatKey($0) ?? $0 }
    let m = selectedMachine(row: typing && tab != nil ? nil : row, focusedTab: tab)
    return m.flatMap { known.contains($0) ? $0 : nil } ?? "local"
}

/// Why a machine cannot say its usage, or nil when it can. capabilities is
/// nil while it is not connected. Reloading another machine's server runs
/// the same old binary again, so there it is an upgrade.
public func usageGap(machine: String, label: String, capabilities: [String]?, needs: String) -> String? {
    let name = machine == "local" ? "This Mac" : label
    guard let capabilities else { return "\(name) is not connected." }
    if capabilities.contains(needs) { return nil }
    let fix = machine == "local" ? "reload its server" : "run conch machine upgrade \(machine)"
    return "conch on \(name) is older and cannot say this: \(fix)."
}
