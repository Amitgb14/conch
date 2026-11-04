import Foundation

// One message to several panes at once, as the TUI's broadcast
// (internal/tui/broadcast.go): the running panes of the row it was asked
// from, each ticked or not. An agent takes it as its next prompt; a
// terminal runs it as a command, so terminals start ticked only when a
// Terminals section was asked from, and an agent waiting on an answer
// starts unticked — the message would answer its question.

/// Where a broadcast was asked from.
public enum BroadcastScope: Equatable, Sendable {
    /// Every running pane on a machine the workspace shows.
    case machine(String)
    /// A project's (key(machine, project)).
    case project(String)
    /// A project's Agents (agents true) or Terminals section.
    case section(project: String, agents: Bool)
    /// A machine's panes in no project.
    case cli(String)

    public var machine: String {
        switch self {
        case .machine(let m), .cli(let m): return m
        case .project(let pk), .section(let pk, _): return unkey(pk).machine
        }
    }
}

/// A pane a broadcast may go to.
public struct BroadcastTarget: Equatable, Sendable, Identifiable {
    public var key: String
    public var name: String
    public var agent: String?
    public var waiting: Bool
    public var ticked: Bool
    public var id: String { key }
}

extension Snapshot {
    /// The running panes a broadcast from scope may go to, agents first,
    /// each ticked as the TUI ticks it.
    public func broadcastTargets(_ scope: BroadcastScope, in ws: Workspace? = nil) -> [BroadcastTarget] {
        let m = scope.machine
        var panes = shown(in: ws, machine: m).filter(\.running)
        var terminalsTicked = false
        switch scope {
        case .machine: break
        case .cli: panes = panes.filter { $0.projectID == nil || project($0.projectID!) == nil }
        case .project(let pk): panes = panes.filter { $0.projectID == unkey(pk).id }
        case .section(let pk, let agents):
            panes = panes.filter { $0.projectID == unkey(pk).id && ($0.agent != nil) == agents }
            terminalsTicked = !agents
        }
        let targets = panes.map { p in
            BroadcastTarget(key: key(m, p.id), name: p.displayName, agent: p.agent?.name, waiting: p.agent?.state == .blocked,
                            ticked: p.agent != nil ? p.agent?.state != .blocked : terminalsTicked)
        }
        return targets.filter { $0.agent != nil } + targets.filter { $0.agent == nil }
    }
}

/// What a broadcast's results say, in a line: how many took it, and why
/// the others did not.
public struct BroadcastOutcome: Decodable, Equatable, Sendable {
    public var id: String
    public var sent: Bool
    public var error: String?
    public init(id: String, sent: Bool, error: String? = nil) {
        self.id = id
        self.sent = sent
        self.error = error
    }
}

public func broadcastSummary(_ results: [BroadcastOutcome], names: [String: String] = [:]) -> (text: String, failed: Bool) {
    let sent = results.filter(\.sent).count
    let failed = results.filter { !$0.sent }
    var text = "Sent to \(sent) pane\(sent == 1 ? "" : "s")"
    if let f = failed.first {
        text += " · \(failed.count) not: \(names[f.id] ?? f.id) — \(f.error ?? "refused")"
        if failed.count > 1 { text += ", …" }
    }
    return (text, !failed.isEmpty)
}
