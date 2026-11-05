import Foundation

// What agents are spending, as the TUI shows it beside what they are doing
// (internal/tui/cost.go): a cost where the agent reports one — Claude Code
// and OpenCode do — else the tokens it used. Only what conch has seen is
// counted: an agent started outside conch spends the same plan window
// without appearing here.

/// What one or more agents have used so far.
public struct Usage: Equatable, Sendable {
    public var agents = 0
    public var output = 0
    /// Prompt tokens, cache reads and writes included.
    public var input = 0
    public var cost = 0.0

    public init() {}

    public var isEmpty: Bool { agents == 0 || (output == 0 && input == 0 && cost == 0) }

    /// Counts a pane's usage, if it is an agent that reports any.
    public mutating func add(_ p: PaneInfo) {
        guard let t = p.agent?.tokens else { return }
        agents += 1
        output += t.output
        input += t.input + t.cacheRead + t.cacheWrite
        cost += t.costUSD ?? 0
    }

    /// The short form for a tree row: the cost when reported, else what it
    /// wrote, else what it read — an agent that has read a large context
    /// but written nothing yet still shows what it spent getting there.
    public var chip: String {
        if isEmpty { return "" }
        if cost > 0 { return usd(cost) }
        if output > 0 { return humanCount(output) + " out" }
        if input > 0 { return humanCount(input) + " in" }
        return ""
    }
}

/// Sums the agents among panes.
public func usage(of panes: [PaneInfo]) -> Usage {
    var u = Usage()
    for p in panes { u.add(p) }
    return u
}

/// A pane header's usage, as the TUI's: the context against the window,
/// the output so far, and the cost.
public func tokenSummary(_ t: Tokens) -> String {
    var parts: [String] = []
    if t.context > 0 && t.contextSize > 0 {
        parts.append("ctx \(humanCount(t.context))/\(humanCount(t.contextSize))")
    } else if t.context > 0 {
        parts.append("ctx " + humanCount(t.context))
    }
    parts.append("out " + humanCount(t.output))
    if let c = t.costUSD, c > 0 { parts.append(usd(c)) }
    return parts.joined(separator: " · ")
}

public func usd(_ v: Double) -> String { v < 0.01 ? "<$0.01" : String(format: "$%.2f", v) }

public func humanCount(_ n: Int) -> String {
    switch n {
    case 1_000_000...: return String(format: "%.1fM", Double(n) / 1e6)
    case 10_000...: return "\(n / 1000)k"
    case 1000...: return String(format: "%.1fk", Double(n) / 1000)
    default: return "\(n)"
    }
}

/// [ui] cost: whether spending is shown at all. On unless turned off.
public func showsCost(toml: String) -> Bool { tomlSetting("ui", "cost", in: toml) != "false" }
