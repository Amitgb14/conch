import Foundation

/// One program an agent left running when it stopped working
/// (proto.BackgroundJob): the process below the agent, with the ports it
/// and its children listen on.
public struct BackgroundJob: Decodable, Equatable, Sendable {
    public var pid: Int
    public var command: String
    public var since: Date?
    public var ports: [Int]?

    public init(pid: Int, command: String, since: Date? = nil, ports: [Int]? = nil) {
        self.pid = pid
        self.command = command
        self.since = since
        self.ports = ports
    }
}

extension AgentStatus {
    /// How many things it left running; never negative.
    public var runningCount: Int { max(background ?? 0, 0) }

    /// The pill beside its state, "idle · 2 running", or nil when nothing
    /// runs — or while it works, when its children are the work.
    public var backgroundPill: String? {
        guard runningCount > 0, state != .working else { return nil }
        let word: String
        switch state {
        case .blocked: word = "waiting"
        case .done: word = "done"
        default: word = "idle"
        }
        return "\(word) · \(runningCount) running"
    }
}

/// The pill's tooltip: each job on a line, its command, pid, how long it
/// has run and the ports it listens on.
public func backgroundTip(_ jobs: [BackgroundJob], now: Date = Date()) -> String {
    jobs.map { j in
        var parts = ["pid \(j.pid)"]
        if let s = j.since { parts.append(jobAge(now.timeIntervalSince(s))) }
        if let ports = j.ports, !ports.isEmpty { parts.append(ports.map { ":\($0)" }.joined(separator: " ")) }
        let tail = parts.joined(separator: " · ")
        return j.command.isEmpty ? tail : "\(j.command) — \(tail)"
    }.joined(separator: "\n")
}

/// How long a job has run, as a person says it: 9s, 3m, 2h 2m, 3d 1h.
public func jobAge(_ t: TimeInterval) -> String {
    let s = max(Int(t), 0)
    switch s {
    case ..<60: return "\(s)s"
    case ..<3600: return "\(s / 60)m"
    case ..<86400: return "\(s / 3600)h \(s % 3600 / 60)m"
    default: return "\(s / 86400)d \(s % 86400 / 3600)h"
    }
}

/// What an agent left running, for a notification, as the TUI says it:
/// "1 still running (npm run dev)", or "" for nothing.
public func backgroundSummary(_ a: AgentStatus) -> String {
    let n = a.runningCount
    guard n > 0 else { return "" }
    var names = (a.jobs ?? []).prefix(2).map { j in j.command.count > 40 ? String(j.command.prefix(39)) + "…" : j.command }
    if (a.jobs?.count ?? 0) > 2 { names.append("…") }
    return "\(n) still running" + (names.isEmpty ? "" : " (" + names.joined(separator: ", ") + ")")
}
