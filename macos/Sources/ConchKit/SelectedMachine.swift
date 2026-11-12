import Foundation

// Where what is selected lives. A folder, a file, a branch or an agent
// looks the same on this Mac as on busybox or a cloud machine, and the
// tabs mix them, so the toolbar's top right says which machine it is.

/// The kinds of tab named "kind|machine|…" (changesKey, filesKey, …); any
/// other tab is a pane, "machine|id".
private let tabKinds: Set<String> = ["changes", "commits", "files", "sessions", "setup", "task"]

/// The machine a tab is on.
public func tabMachine(_ k: String) -> String {
    let parts = k.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
    if parts.count == 3, tabKinds.contains(parts[0]) { return parts[1].isEmpty ? "local" : parts[1] }
    return unkey(k).machine
}

/// What kind of machine it is, for its mark.
public enum MachineKind: Equatable, Sendable {
    case local, remote, cloud, sandbox
}

/// The toolbar's chip: which machine, said as a person would.
public struct MachinePlace: Equatable, Sendable {
    public var kind: MachineKind
    public var name: String
    public var online: Bool

    /// The chip's words: "This Mac", "busybox · remote", "bot · Conch Cloud".
    public var title: String {
        switch kind {
        case .local: return "This Mac"
        case .remote: return "\(name) · remote"
        case .cloud: return "\(name) · Conch Cloud"
        case .sandbox: return "\(name) · sandbox"
        }
    }

    /// The pointer's longer word on it.
    public var help: String {
        let what: String
        switch kind {
        case .local: return "What is selected is on this Mac"
        case .remote: what = "on \(name), a remote machine"
        case .cloud: what = "on \(name), a Conch Cloud machine"
        case .sandbox: what = "on \(name), a sandbox"
        }
        return "What is selected is \(what)" + (online ? "" : " (not connected)")
    }
}

/// The chip for machine id, its label, and what it is.
public func machinePlace(id: String, label: String, cloud: Bool, sandbox: Bool, online: Bool) -> MachinePlace {
    if id == "local" { return MachinePlace(kind: .local, name: "This Mac", online: true) }
    // A sandbox's label carries its provider ("x · daytona"); the chip says
    // sandbox itself.
    let name = label.components(separatedBy: " · ").first ?? label
    let kind: MachineKind = cloud ? .cloud : sandbox ? .sandbox : .remote
    return MachinePlace(kind: kind, name: name.isEmpty ? id : name, online: online)
}

/// The machine of what is selected: the sidebar row the cursor is on,
/// else the tab in front, else none.
public func selectedMachine(row: String?, focusedTab: String?) -> String? {
    if let row { return row }
    return focusedTab.map(tabMachine)
}
