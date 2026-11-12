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
