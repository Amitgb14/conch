import Foundation

// Starting an agent a machine does not have. A new machine — a Conch
// Cloud one just made, a project just added there — has none, and the
// start used to fail with "claude is not installed here". The app asks
// the machine first and installs it (agent.install, the installer in a
// pane of its own), says so in the status bar, and starts the agent once
// the machine finds it.

/// What starting an agent on a machine takes.
public enum AgentReadiness: Equatable, Sendable {
    /// Start it: it is there, or the machine could not say (its server
    /// then refuses with its own reason).
    case start
    /// Install it first.
    case install
    /// It is not there and the app cannot install it; why, to say.
    case missing(String)
}

/// Whether agent can be started on machine (named machineName) now, given
/// what the machine said it has (nil: it could not be asked) and whether
/// its server installs agents (agent.install.v1).
public func agentReadiness(_ agent: String, status: [AgentAvailability]?, canInstall: Bool, machineName: String) -> AgentReadiness {
    guard let status, let a = status.first(where: { $0.name == agent }) else { return .start }
    if a.installed { return .start }
    if !canInstall {
        return .missing("\(a.title) is not installed on \(machineName), and its server predates installing agents: reload or upgrade it, or install \(a.title) there yourself")
    }
    return .install
}

/// The status bar's line while agent installs.
public func agentInstallJob(_ title: String, machineName: String) -> String {
    "Installing \(title) on \(machineName)"
}

/// What came of an installer that ended with code, and whether the
/// machine then finds the agent: nil when it can be started.
public func agentInstallProblem(_ title: String, exitCode: Int, found: Bool) -> String? {
    if exitCode != 0 {
        return "the installer exited \(exitCode); its terminal is open with what it said"
    }
    if !found {
        return "the installer finished, but \(title) is not on the machine's PATH yet; open a new terminal there and check"
    }
    return nil
}

// Settings → Agents: every agent a machine reports, and installing the
// ones it lacks from there rather than only on the way to starting one.

/// How one agent stands on a machine.
public enum InstalledAgentState: Equatable, Sendable {
    /// There, at this version ("" when it did not say).
    case installed(String)
    /// Not there, and Install puts it there.
    case missing
    /// Its installer is running now.
    case installing
    /// Not there, and conch has no installer for it.
    case noInstaller
    /// Not there, and this machine's server predates installing agents.
    case unavailable
}

/// One row of the Installed agents card.
public struct InstalledAgentRow: Equatable, Sendable, Identifiable {
    public var name: String
    public var title: String
    public var state: InstalledAgentState
    /// A line under its name: the tier it is in, or why there is no Install.
    public var note: String?
    public var id: String { name }
}

/// The card's rows, in the order the machine listed them. installing names
/// the agents whose installer is running on it; canInstall is whether its
/// server has agent.install.v1.
public func installedAgentRows(_ status: [AgentAvailability], installing: Set<String>, canInstall: Bool) -> [InstalledAgentRow] {
    // An ACP agent is in the card's Over ACP section, not here.
    status.filter { !$0.isACP }.map { a in
        var note: String? = nil
        if a.tier == "runs here" {
            note = a.sessions == true ? "conch runs it and reads its sessions" : "conch runs it; its sessions are not read"
        }
        let state: InstalledAgentState
        if a.installed {
            state = .installed(a.version ?? "")
        } else if installing.contains(a.name) {
            state = .installing
        } else if a.noInstaller == true {
            state = .noInstaller
            note = "conch has no installer for it"
        } else if !canInstall {
            state = .unavailable
        } else {
            state = .missing
        }
        return InstalledAgentRow(name: a.name, title: a.title, state: state, note: note)
    }
}

/// What a machine's tab says: how many of the agents it lists it has.
public func installedAgentsSummary(_ status: [AgentAvailability]?) -> String {
    guard let status else { return "unknown" }
    if status.isEmpty { return "none known" }
    return "\(status.filter(\.installed).count) of \(status.count)"
}

/// What the Default agent menu says after each agent this machine lacks;
/// nothing while it is not known.
public func agentMenuNotes(_ status: [AgentAvailability]?) -> [String: String] {
    var notes: [String: String] = [:]
    for a in status ?? [] where !a.installed { notes[a.name] = "not installed" }
    return notes
}
