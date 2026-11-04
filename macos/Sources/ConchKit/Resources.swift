import Foundation

/// What conch uses on this computer — the window, the server, and each
/// pane with everything running inside it — as the TUI's resource monitor
/// (internal/tui/resources.go) reckons it: from ps, two samples apart.

/// One process, as ps lists it.
public struct Proc: Equatable, Sendable {
    public var pid, ppid: Int
    public var rssKiB: Int64
    public var cpuSeconds: Double
    public var comm: String
}

/// Every process on this computer at one moment.
public struct ProcSample: Sendable {
    public var at: Date
    public var procs: [Int: Proc]

    public init(at: Date, procs: [Int: Proc]) {
        self.at = at
        self.procs = procs
    }
}

/// The ps the sample is read from: the same fields in procps and BSD ps,
/// comm last because it may hold spaces.
public let psArguments = ["-A", "-o", "pid=,ppid=,rss=,time=,comm="]

/// Reads ps's output, skipping a line it cannot read.
public func parsePS(_ out: String) -> [Int: Proc] {
    var procs: [Int: Proc] = [:]
    for line in out.split(whereSeparator: \.isNewline) {
        let f = line.split(separator: " ", omittingEmptySubsequences: true)
        guard f.count >= 5, let pid = Int(f[0]), let ppid = Int(f[1]), let rss = Int64(f[2]),
              let cpu = parseCPUTime(String(f[3])) else { continue }
        procs[pid] = Proc(pid: pid, ppid: ppid, rssKiB: rss, cpuSeconds: cpu, comm: f[4...].joined(separator: " "))
    }
    return procs
}

/// ps's time column, [DD-][HH:]MM:SS[.ss], in seconds; nil for anything else.
public func parseCPUTime(_ s: String) -> Double? {
    var rest = Substring(s)
    var days = 0.0
    if let dash = rest.firstIndex(of: "-") {
        guard let d = Double(rest[..<dash]), d >= 0 else { return nil }
        days = d
        rest = rest[rest.index(after: dash)...]
    }
    let parts = rest.split(separator: ":", omittingEmptySubsequences: false)
    guard (2...3).contains(parts.count), let sec = Double(parts[parts.count - 1]), sec >= 0, sec.isFinite else { return nil }
    var total = sec + days * 86400
    var mult = 60.0
    for p in parts.dropLast().reversed() {
        guard let n = Int(p), n >= 0 else { return nil }
        total += Double(n) * mult
        mult *= 60
    }
    return (total * 1000).rounded() / 1000
}

/// One line of the monitor: a name, its memory, its share of a core —
/// nil until there is a second sample to measure it by — and whether it
/// sits under the line above it (a pane under "Panes").
public struct ResourceRow: Equatable, Sendable {
    public var name: String
    public var rssKiB: Int64
    public var cpuPct: Double?
    public var indent: Bool

    public init(name: String, rssKiB: Int64, cpuPct: Double?, indent: Bool = false) {
        self.name = name
        self.rssKiB = rssKiB
        self.cpuPct = cpuPct
        self.indent = indent
    }
}

/// The monitor's lines from two samples, as the TUI's box has them: this
/// window; the server, with what it runs that is no pane's; "Panes (n)"
/// adding them up, and each pane with everything running in it, indented,
/// the largest first, past `most` folded into "N more" (never a lone "1
/// more"); and the total. A pane's tree is counted before the server's,
/// whose children the panes are.
public func resourceRows(prev: ProcSample?, cur: ProcSample, app: (String, Int), server: Int?,
                         panes: [(String, Int)], most: Int = 8) -> (rows: [ResourceRow], total: ResourceRow) {
    var children: [Int: [Int]] = [:]
    for p in cur.procs.values where p.pid != p.ppid { children[p.ppid, default: []].append(p.pid) }
    var claimed: Set<Int> = []
    func tree(_ root: Int) -> [Int] {
        guard root > 0, cur.procs[root] != nil, !claimed.contains(root) else { return [] }
        var out: [Int] = []
        var stack = [root]
        while let pid = stack.popLast() {
            guard !claimed.contains(pid) else { continue }
            claimed.insert(pid)
            out.append(pid)
            stack += children[pid] ?? []
        }
        return out
    }
    let dt = prev.map { cur.at.timeIntervalSince($0.at) } ?? 0
    var all: [Int] = []
    func sum(_ pids: [Int]) -> (Int64, Double?) {
        var rss: Int64 = 0
        var cpu = 0.0
        for pid in pids {
            guard let p = cur.procs[pid] else { continue }
            rss += p.rssKiB
            if let was = prev?.procs[pid], was.comm == p.comm { cpu += max(p.cpuSeconds - was.cpuSeconds, 0) }
        }
        return (rss, dt > 0 ? cpu / dt * 100 : nil)
    }
    func row(_ name: String, _ pids: [Int], indent: Bool = false) -> ResourceRow {
        all += pids
        let (rss, cpu) = sum(pids)
        return ResourceRow(name: name, rssKiB: rss, cpuPct: cpu, indent: indent)
    }
    func adding(_ name: String, _ us: [ResourceRow], indent: Bool) -> ResourceRow {
        var out = ResourceRow(name: name, rssKiB: 0, cpuPct: nil, indent: indent)
        for u in us {
            out.rssKiB += u.rssKiB
            if let c = u.cpuPct { out.cpuPct = (out.cpuPct ?? 0) + c }
        }
        return out
    }
    // The server is held back until the panes are counted: they are its.
    if let server { claimed.insert(server) }
    var rows = [row(app.0, tree(app.1))]
    var paneRows = panes.compactMap { name, pid -> ResourceRow? in
        let pids = tree(pid)
        return pids.isEmpty ? nil : row(name, pids, indent: true)
    }
    if let server, cur.procs[server] != nil {
        claimed.remove(server)
        rows.append(row("Server · pid \(server)", tree(server)))
    }
    paneRows.sort { $0.rssKiB > $1.rssKiB }
    let head = adding("Panes (\(paneRows.count))", paneRows, indent: false)
    if paneRows.count > most + 1 {
        let rest = Array(paneRows[most...])
        paneRows = Array(paneRows[..<most]) + [adding("\(rest.count) more", rest, indent: true)]
    }
    rows.append(head)
    rows += paneRows
    let (rss, cpu) = sum(all)
    return (rows, ResourceRow(name: "Total", rssKiB: rss, cpuPct: cpu))
}

/// How hot a share of a core is: fine, busy from 80%, hot from two cores.
public enum CPULevel: Equatable, Sendable { case fine, busy, hot }

public func cpuLevel(_ pct: Double) -> CPULevel { pct >= 200 ? .hot : pct >= 80 ? .busy : .fine }

/// A share of a core as the TUI's bar: cells filled out of `width`, a full
/// bar one core.
public func cpuCells(_ pct: Double, width: Int = 10) -> Int {
    Int((min(max(pct, 0), 100) / 100 * Double(width)).rounded())
}

/// Memory as the TUI writes it: 812 KB, 41 MB, 1.2 GB.
public func formatMemory(_ kib: Int64) -> String {
    switch kib {
    case (1024 * 1024)...: return String(format: "%.1f GB", Double(kib) / (1024 * 1024))
    case 1024...: return "\((kib + 512) / 1024) MB"
    default: return "\(kib) KB"
    }
}
