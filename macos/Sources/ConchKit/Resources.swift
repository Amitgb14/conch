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

/// One line of the monitor: a name, its memory and its share of a core.
public struct ResourceRow: Equatable, Sendable {
    public var name: String
    public var rssKiB: Int64
    public var cpuPct: Double

    public init(name: String, rssKiB: Int64, cpuPct: Double) {
        self.name = name
        self.rssKiB = rssKiB
        self.cpuPct = cpuPct
    }
}

/// The monitor's lines from two samples: each root with everything under
/// it — a pane's tree first, so the server's line is the server itself
/// and not its panes — the panes by memory, the most after `most` folded
/// into one line, and the total. A root whose process is gone is left out.
public func resourceRows(prev: ProcSample?, cur: ProcSample, app: (String, Int), server: (String, Int)?,
                         panes: [(String, Int)], most: Int = 8) -> (rows: [ResourceRow], total: ResourceRow) {
    var children: [Int: [Int]] = [:]
    for p in cur.procs.values where p.pid != p.ppid { children[p.ppid, default: []].append(p.pid) }
    var claimed: Set<Int> = []
    func tree(_ root: Int) -> [Int] {
        guard cur.procs[root] != nil, !claimed.contains(root) else { return [] }
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
    func row(_ name: String, _ pids: [Int]) -> ResourceRow {
        var rss: Int64 = 0
        var cpu = 0.0
        for pid in pids {
            guard let p = cur.procs[pid] else { continue }
            rss += p.rssKiB
            if dt > 0, let was = prev?.procs[pid], was.comm == p.comm {
                cpu += max(p.cpuSeconds - was.cpuSeconds, 0)
            }
        }
        return ResourceRow(name: name, rssKiB: rss, cpuPct: dt > 0 ? cpu / dt * 100 : 0)
    }
    // Panes first: what runs in a pane is the pane's, not the server's.
    var paneRows = panes.compactMap { name, pid -> ResourceRow? in
        let pids = tree(pid)
        return pids.isEmpty ? nil : row(name, pids)
    }
    paneRows.sort { $0.rssKiB != $1.rssKiB ? $0.rssKiB > $1.rssKiB : $0.name < $1.name }
    var rows: [ResourceRow] = []
    if let server, case let pids = tree(server.1), !pids.isEmpty { rows.append(row(server.0, pids)) }
    if case let pids = tree(app.1), !pids.isEmpty { rows.insert(row(app.0, pids), at: 0) }
    rows += paneRows.prefix(most)
    if paneRows.count > most {
        let rest = paneRows.dropFirst(most)
        rows.append(ResourceRow(name: "\(rest.count) more", rssKiB: rest.reduce(0) { $0 + $1.rssKiB }, cpuPct: rest.reduce(0) { $0 + $1.cpuPct }))
    }
    let all = ResourceRow(name: "conch", rssKiB: rows.reduce(0) { $0 + $1.rssKiB }, cpuPct: rows.reduce(0) { $0 + $1.cpuPct })
    return (rows, all)
}

/// Memory as the monitor writes it: 812 KB, 41 MB, 1.2 GB.
public func formatMemory(_ kib: Int64) -> String {
    switch kib {
    case ..<1024: return "\(kib) KB"
    case ..<(1024 * 1024): return "\(kib / 1024) MB"
    default: return String(format: "%.1f GB", Double(kib) / 1024 / 1024)
    }
}
