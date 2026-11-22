import Foundation

// How much memory a browser tab uses. Each tab's page runs in a WebKit
// WebContent process, and its footprint (what Activity Monitor's Memory
// column shows) is that tab's cost — but WebKit may put several tabs in
// one process, so a process is counted once in the total and each tab in
// it says whom it shares with rather than claiming all of it.

/// A tab and the WebContent process its page runs in, when known.
public struct TabProcess: Equatable, Sendable {
    public var tab: Int
    public var pid: Int32?
    /// False for a tab whose page was never made (a saved tab not shown yet).
    public var loaded: Bool

    public init(tab: Int, pid: Int32?, loaded: Bool = true) {
        self.tab = tab
        self.pid = pid
        self.loaded = loaded
    }
}

/// What one tab's memory reads as.
public struct TabMemoryLine: Equatable, Sendable {
    public var text: String
    public var bytes: UInt64?
    /// How many other tabs run in the same process.
    public var sharedWith: Int
}

public struct TabMemory: Equatable, Sendable {
    public var lines: [Int: TabMemoryLine]
    /// Every known process once.
    public var total: UInt64
    /// Loaded tabs whose memory could not be read.
    public var unknown: Int

    public init(lines: [Int: TabMemoryLine] = [:], total: UInt64 = 0, unknown: Int = 0) {
        self.lines = lines
        self.total = total
        self.unknown = unknown
    }
}

/// Bytes as Activity Monitor says them: KB and MB whole, GB to a tenth.
public func formatFootprint(_ b: UInt64) -> String {
    let kb = Double(b) / 1024
    if kb < 1024 { return "\(b == 0 ? 0 : max(1, Int(kb.rounded()))) KB" }
    let mb = kb / 1024
    if mb < 1024 { return "\(Int(mb.rounded())) MB" }
    return String(format: "%.1f GB", (mb / 1024 * 10).rounded() / 10) // half up, not printf's half to even
}

/// Each tab's line and the total, from the footprint of each process that
/// could be read.
public func tabMemory(_ tabs: [TabProcess], footprints: [Int32: UInt64]) -> TabMemory {
    var count: [Int32: Int] = [:]
    for t in tabs { if let p = t.pid { count[p, default: 0] += 1 } }
    var lines: [Int: TabMemoryLine] = [:]
    var unknown = 0
    for t in tabs {
        guard t.loaded else {
            lines[t.tab] = TabMemoryLine(text: "Not loaded", bytes: nil, sharedWith: 0)
            continue
        }
        guard let p = t.pid, let b = footprints[p] else {
            lines[t.tab] = TabMemoryLine(text: "—", bytes: nil, sharedWith: 0)
            unknown += 1
            continue
        }
        let others = (count[p] ?? 1) - 1
        let text = others == 0 ? formatFootprint(b) : formatFootprint(b) + " · shared with \(others) tab" + (others == 1 ? "" : "s")
        lines[t.tab] = TabMemoryLine(text: text, bytes: b, sharedWith: others)
    }
    let pids = Set(tabs.filter(\.loaded).compactMap(\.pid))
    let total = pids.reduce(UInt64(0)) { s, p in footprints[p].map { s &+ $0 } ?? s }
    return TabMemory(lines: lines, total: total, unknown: unknown)
}

/// A browser tab's tooltip: its title, its address, and — read while the
/// pointer is on it — its memory.
public func tabTooltip(label: String, address: String, memory: String?) -> String {
    var parts = [label]
    if !address.isEmpty { parts.append(address) }
    if let memory { parts.append("Memory: " + memory) }
    return parts.joined(separator: "\n")
}

/// What the System Monitor says started a WebContent process: the tabs
/// whose pages run in it.
public func browserProcessOrigin(_ labels: [String]) -> String {
    guard let first = labels.first else { return "Browser" }
    return "Browser · " + first + (labels.count > 1 ? " +\(labels.count - 1)" : "")
}
