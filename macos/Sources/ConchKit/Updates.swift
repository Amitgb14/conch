import Foundation

// What can be brought up to date: each machine whose conch lacks something
// this app's conch has, and what doing so means — this computer's server
// re-exec'd onto its binary, another machine given this build. The status
// bar says how many in the warn colour, and its sheet does the ones picked.

public struct UpdateCandidate: Equatable, Sendable, Identifiable {
    public enum Action: Equatable, Sendable {
        /// This computer: conch server reload.
        case reload
        /// Another machine: conch machine upgrade, installing this build.
        case upgrade
    }
    public var id: String { machine }
    public var machine: String
    public var label: String
    public var missing: [String]
    public var action: Action

    public init(machine: String, label: String, missing: [String], action: Action) {
        self.machine = machine
        self.label = label
        self.missing = missing
        self.action = action
    }

    /// What the row says will be done.
    public var doing: String {
        action == .reload ? "Reload the server: its panes keep running" : "Install this build there, then reconnect"
    }

    /// What the machine lacks, said briefly: the first few, then how many more.
    public var lacks: String {
        let shown = missing.prefix(3).joined(separator: ", ")
        return missing.count > 3 ? "\(shown) and \(missing.count - 3) more" : shown
    }
}

/// The machines to offer, this computer first, then the rest in the order
/// given; one that is offline or up to date is not offered.
public func updateCandidates(_ machines: [(id: String, label: String, online: Bool, behind: [String])]) -> [UpdateCandidate] {
    let offered = machines.filter { $0.online && !$0.behind.isEmpty }
    let local = offered.filter { $0.id == "local" }
    let rest = offered.filter { $0.id != "local" }
    return (local + rest).map {
        UpdateCandidate(machine: $0.id, label: $0.id == "local" ? "This Mac" : $0.label, missing: $0.behind,
                        action: $0.id == "local" ? .reload : .upgrade)
    }
}

/// The status bar's version, which is also where updates are said: the
/// version alone when all is up to date, and how many machines are behind
/// beside it when some are. Nothing at all before the server has said.
public func versionLabel(_ version: String, updates n: Int) -> String? {
    if version.isEmpty { return n == 0 ? nil : "Updates · \(n)" }
    return n == 0 ? "v\(version)" : "v\(version) · \(n)"
}
