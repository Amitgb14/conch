import Foundation

/// The whole machine, as system.stats (system.stats.v1) says it: the
/// System Monitor window's figures, and the strip at the top of the
/// resource monitor. Everything the server leaves out is optional here.

public let systemStatsCapability = "system.stats.v1"

public struct SystemHost: Decodable, Equatable, Sendable {
    public var name: String?
    public var os: String?
    public var arch: String?
    public var model: String?
    public var cores: Int
    public var pCores: Int?
    public var eCores: Int?
    public var uptime: Int64?

    enum CodingKeys: String, CodingKey {
        case name, os, arch, model, cores, uptime
        case pCores = "p_cores", eCores = "e_cores"
    }

    public init(name: String? = nil, os: String? = nil, model: String? = nil, cores: Int = 0, pCores: Int? = nil, eCores: Int? = nil, uptime: Int64? = nil) {
        self.name = name; self.os = os; self.model = model; self.cores = cores
        self.pCores = pCores; self.eCores = eCores; self.uptime = uptime
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name)
        os = try c.decodeIfPresent(String.self, forKey: .os)
        arch = try c.decodeIfPresent(String.self, forKey: .arch)
        model = try c.decodeIfPresent(String.self, forKey: .model)
        cores = try c.decodeIfPresent(Int.self, forKey: .cores) ?? 0
        pCores = try c.decodeIfPresent(Int.self, forKey: .pCores)
        eCores = try c.decodeIfPresent(Int.self, forKey: .eCores)
        uptime = try c.decodeIfPresent(Int64.self, forKey: .uptime)
    }

    /// "macOS 26.1 · Apple M3 Pro · 12 cores · 36 GB".
    public func summary(memory: UInt64?) -> String {
        var parts: [String] = []
        if let os, !os.isEmpty { parts.append(os) }
        if let model, !model.isEmpty { parts.append(model) }
        if cores > 0 { parts.append(cores == 1 ? "1 core" : "\(cores) cores") }
        if let memory, memory > 0 { parts.append(formatBytes(memory, whole: true)) }
        return parts.joined(separator: " · ")
    }
}

public struct SystemCPU: Decodable, Equatable, Sendable {
    public var total: Double
    public var user: Double
    public var system: Double
    public var split: Bool
    public var cores: [Double]

    public init(total: Double, user: Double = 0, system: Double = 0, split: Bool = false, cores: [Double] = []) {
        self.total = total; self.user = user; self.system = system; self.split = split; self.cores = cores
    }

    public init(from d: Decoder) throws {
        enum K: String, CodingKey { case total, user, system, split, cores }
        let c = try d.container(keyedBy: K.self)
        total = try c.decodeIfPresent(Double.self, forKey: .total) ?? 0
        user = try c.decodeIfPresent(Double.self, forKey: .user) ?? 0
        system = try c.decodeIfPresent(Double.self, forKey: .system) ?? 0
        split = try c.decodeIfPresent(Bool.self, forKey: .split) ?? false
        cores = try c.decodeIfPresent([Double].self, forKey: .cores) ?? []
    }
}

public struct SystemMemory: Decodable, Equatable, Sendable {
    public var total, used, apps, wired, compressed, cached, free, swapTotal, swapUsed: UInt64
    public var pressure: String?

    public init(total: UInt64, used: UInt64, apps: UInt64 = 0, wired: UInt64 = 0, compressed: UInt64 = 0, cached: UInt64 = 0,
                free: UInt64 = 0, swapTotal: UInt64 = 0, swapUsed: UInt64 = 0, pressure: String? = nil) {
        self.total = total; self.used = used; self.apps = apps; self.wired = wired; self.compressed = compressed
        self.cached = cached; self.free = free; self.swapTotal = swapTotal; self.swapUsed = swapUsed; self.pressure = pressure
    }

    public init(from d: Decoder) throws {
        enum K: String, CodingKey {
            case total, used, apps, wired, compressed, cached, free, pressure
            case swapTotal = "swap_total", swapUsed = "swap_used"
        }
        let c = try d.container(keyedBy: K.self)
        func n(_ k: K) throws -> UInt64 { try c.decodeIfPresent(UInt64.self, forKey: k) ?? 0 }
        total = try n(.total); used = try n(.used); apps = try n(.apps); wired = try n(.wired)
        compressed = try n(.compressed); cached = try n(.cached); free = try n(.free)
        swapTotal = try n(.swapTotal); swapUsed = try n(.swapUsed)
        pressure = try c.decodeIfPresent(String.self, forKey: .pressure)
    }

    public var usedFraction: Double { total > 0 ? Double(used) / Double(total) : 0 }

    /// Where memory goes, the parts the machine tells apart, largest
    /// category first as they sit in the bar; "Used" alone where it does
    /// not split them (Linux).
    public var parts: [(name: String, bytes: UInt64)] {
        var out: [(String, UInt64)] = []
        if wired > 0 || compressed > 0 {
            out += [("Apps", apps), ("Wired", wired), ("Compressed", compressed)]
        } else {
            out.append(("Used", used))
        }
        out += [("Cached files", cached), ("Free", free)]
        return out
    }
}

public struct SystemDisk: Decodable, Equatable, Sendable {
    public var path: String
    public var total: UInt64
    public var free: UInt64
}

public struct SystemProcess: Decodable, Equatable, Sendable, Identifiable {
    public var pid: Int
    public var ppid: Int
    public var name: String
    public var command: String?
    public var user: String?
    public var cpu: Double?
    public var rss: UInt64
    /// phys_footprint, Activity Monitor's Memory: nil from Linux, an
    /// older server, or a process the server could not read.
    public var footprint: UInt64?
    public var threads: Int?
    public var started: Date?
    public var pane: String?
    public var paneName: String?
    public var project: String?
    public var branch: String?
    public var agent: String?
    /// The pane's agent's state and since when; nil from an older server.
    public var agentState: String?
    public var agentSince: Date?
    public var conch: String?

    public var id: Int { pid }

    enum CodingKeys: String, CodingKey {
        case pid, ppid, name, command, user, cpu, rss, footprint, threads, started, pane, project, branch, agent, conch
        case paneName = "pane_name", agentState = "agent_state", agentSince = "agent_since"
    }

    public init(pid: Int, ppid: Int = 1, name: String, command: String? = nil, user: String? = nil, cpu: Double? = nil, rss: UInt64 = 0,
                footprint: UInt64? = nil, started: Date? = nil, pane: String? = nil, paneName: String? = nil, project: String? = nil,
                branch: String? = nil, agent: String? = nil, agentState: String? = nil, agentSince: Date? = nil, conch: String? = nil) {
        self.pid = pid; self.ppid = ppid; self.name = name; self.command = command; self.user = user; self.cpu = cpu
        self.rss = rss; self.footprint = footprint; self.started = started; self.pane = pane; self.paneName = paneName
        self.project = project; self.branch = branch; self.agent = agent; self.agentState = agentState
        self.agentSince = agentSince; self.conch = conch
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        pid = try c.decode(Int.self, forKey: .pid)
        ppid = try c.decodeIfPresent(Int.self, forKey: .ppid) ?? 0
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        command = try c.decodeIfPresent(String.self, forKey: .command)
        user = try c.decodeIfPresent(String.self, forKey: .user)
        cpu = try c.decodeIfPresent(Double.self, forKey: .cpu)
        rss = try c.decodeIfPresent(UInt64.self, forKey: .rss) ?? 0
        footprint = try c.decodeIfPresent(UInt64.self, forKey: .footprint)
        threads = try c.decodeIfPresent(Int.self, forKey: .threads)
        started = try? c.decodeIfPresent(Date.self, forKey: .started)
        pane = try c.decodeIfPresent(String.self, forKey: .pane)
        paneName = try c.decodeIfPresent(String.self, forKey: .paneName)
        project = try c.decodeIfPresent(String.self, forKey: .project)
        branch = try c.decodeIfPresent(String.self, forKey: .branch)
        agent = try c.decodeIfPresent(String.self, forKey: .agent)
        agentState = try c.decodeIfPresent(String.self, forKey: .agentState)
        agentSince = try? c.decodeIfPresent(Date.self, forKey: .agentSince)
        conch = try c.decodeIfPresent(String.self, forKey: .conch)
    }

    /// Started by conch: in a pane, the server, or this app.
    public var isConch: Bool { (pane ?? "").isEmpty == false || (conch ?? "").isEmpty == false }

    /// The memory it is charged for: its footprint where the server read
    /// one, which counts pages shared with other processes once, else RSS.
    public var memory: UInt64 { footprint.flatMap { $0 > 0 ? $0 : nil } ?? rss }

    /// Which of conch's it is: conch's own programs (the server, this app,
    /// attach, web, bridges, and a conch an agent runs, such as conch mcp),
    /// a pane with an agent and what it started, or the rest of a pane's
    /// processes; nil for what conch did not start.
    public var group: SystemGroup? {
        if let c = conch, !c.isEmpty { return .conch }
        guard let p = pane, !p.isEmpty else { return nil }
        if name == "conch" { return .conch }
        if let a = agent, !a.isEmpty { return .agents }
        return .other
    }

    /// Where conch started it, as the table's chip says it: "Claude ·
    /// conch/readme-desktop", "pane tests · conch/readme-desktop",
    /// "conch server"; nil for anything conch did not start.
    public var origin: String? {
        if let c = conch, !c.isEmpty { return c == "app" ? "conch · this window" : "conch \(c)" }
        guard let p = pane, !p.isEmpty else { return nil }
        let head = agent.map { agentDisplayName($0) } ?? "pane \(paneName ?? p)"
        let place = [branch, project].compactMap { $0 }.first { !$0.isEmpty }
        return place.map { "\(head) · \($0)" } ?? head
    }
}

/// What conch started, told apart for "Heavy right now" and the table.
public enum SystemGroup: Equatable, Sendable { case conch, agents, other }

public struct SystemPoint: Decodable, Equatable, Sendable {
    public var at: Date
    public var cpu: Double
    public var user: Double
    public var system: Double
    public var memUsed: UInt64
    public var compressed: UInt64

    public init(at: Date, cpu: Double, user: Double = 0, system: Double = 0, memUsed: UInt64 = 0, compressed: UInt64 = 0) {
        self.at = at; self.cpu = cpu; self.user = user; self.system = system; self.memUsed = memUsed; self.compressed = compressed
    }

    public init(from d: Decoder) throws {
        enum K: String, CodingKey { case at, cpu, user, system, compressed; case memUsed = "mem_used" }
        let c = try d.container(keyedBy: K.self)
        at = try c.decode(Date.self, forKey: .at)
        cpu = try c.decodeIfPresent(Double.self, forKey: .cpu) ?? 0
        user = try c.decodeIfPresent(Double.self, forKey: .user) ?? 0
        system = try c.decodeIfPresent(Double.self, forKey: .system) ?? 0
        memUsed = try c.decodeIfPresent(UInt64.self, forKey: .memUsed) ?? 0
        compressed = try c.decodeIfPresent(UInt64.self, forKey: .compressed) ?? 0
    }
}

public struct SystemStats: Decodable, Equatable, Sendable {
    public var at: Date?
    public var host: SystemHost
    public var cpu: SystemCPU?
    public var memory: SystemMemory?
    public var load: [Double]
    public var disk: SystemDisk?
    public var procs: Int
    public var processes: [SystemProcess]
    public var history: [SystemPoint]

    public init(host: SystemHost = SystemHost(), cpu: SystemCPU? = nil, memory: SystemMemory? = nil, load: [Double] = [],
                disk: SystemDisk? = nil, procs: Int = 0, processes: [SystemProcess] = [], history: [SystemPoint] = []) {
        self.host = host; self.cpu = cpu; self.memory = memory; self.load = load; self.disk = disk
        self.procs = procs; self.processes = processes; self.history = history
    }

    public init(from d: Decoder) throws {
        enum K: String, CodingKey { case at, host, cpu, memory, load, disk, procs, processes, history }
        let c = try d.container(keyedBy: K.self)
        at = try? c.decodeIfPresent(Date.self, forKey: .at)
        host = try c.decodeIfPresent(SystemHost.self, forKey: .host) ?? SystemHost()
        cpu = try c.decodeIfPresent(SystemCPU.self, forKey: .cpu)
        memory = try c.decodeIfPresent(SystemMemory.self, forKey: .memory)
        load = try c.decodeIfPresent([Double].self, forKey: .load) ?? []
        disk = try c.decodeIfPresent(SystemDisk.self, forKey: .disk)
        procs = try c.decodeIfPresent(Int.self, forKey: .procs) ?? 0
        processes = try c.decodeIfPresent([SystemProcess].self, forKey: .processes) ?? []
        history = try c.decodeIfPresent([SystemPoint].self, forKey: .history) ?? []
    }

    /// The processes, this app's own marked as conch's: the server cannot
    /// know which of its clients is which.
    public func marking(app pid: Int) -> SystemStats {
        var s = self
        for i in s.processes.indices where s.processes[i].pid == pid && !s.processes[i].isConch {
            s.processes[i].conch = "app"
        }
        return s
    }
}

/// What the window asks: the history after `since` (Go reads RFC 3339),
/// or only the figures for a machine's line in the sidebar.
public struct SystemStatsParams: Encodable, Equatable, Sendable {
    public var since: String?
    public var summary: Bool?

    public init(since: Date? = nil, summary: Bool = false) {
        if let since {
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            self.since = f.string(from: since)
        }
        self.summary = summary ? true : nil
    }
}

/// A machine's history as the window holds it: what the server has sent,
/// newest last, an hour at most, each point once.
public struct SystemHistory: Equatable, Sendable {
    public private(set) var points: [SystemPoint] = []
    public static let keep: TimeInterval = 3600

    public init(_ points: [SystemPoint] = []) { merge(points) }

    public var last: Date? { points.last?.at }

    /// Adds what is newer than the last point held, and lets go of what
    /// is past the hour. Points arrive in order, but a reply raced by
    /// another, or a server's clock stepping back, must not tangle them.
    public mutating func merge(_ new: [SystemPoint]) {
        for p in new.sorted(by: { $0.at < $1.at }) where points.last.map({ p.at > $0.at }) ?? true {
            points.append(p)
        }
        if let newest = points.last?.at {
            points.removeAll { newest.timeIntervalSince($0.at) > Self.keep }
        }
    }

    /// The points of the last `seconds` before `now`.
    public func window(_ seconds: TimeInterval, now: Date) -> [SystemPoint] {
        points.filter { now.timeIntervalSince($0.at) <= seconds }
    }
}

/// The ranges the window offers, in seconds.
public let systemRanges: [(label: String, seconds: TimeInterval)] = [("1m", 60), ("5m", 300), ("15m", 900), ("1h", 3600)]

public enum SystemSort: String, CaseIterable, Sendable {
    case cpu = "CPU", memory = "Memory", name = "Name"
}

/// Which processes the table shows: every one, all conch started, or one
/// of the groups heavyNotes speaks of.
public enum SystemShow: String, CaseIterable, Sendable {
    case all = "Everything", started = "All conch started", conch = "conch itself", agents = "Agents", other = "Other pane processes"

    func admits(_ p: SystemProcess) -> Bool {
        switch self {
        case .all: return true
        case .started: return p.isConch
        case .conch: return p.group == .conch
        case .agents: return p.group == .agents
        case .other: return p.group == .other
        }
    }
}

/// The table's rows: filtered by a few words (each must be found in the
/// name, the command, the pid, the user or where it came from), to what
/// `show` admits, in the order chosen.
public func systemRows(_ ps: [SystemProcess], sort: SystemSort, filter: String = "", show: SystemShow = .all) -> [SystemProcess] {
    let words = filter.lowercased().split(separator: " ").map(String.init)
    var out = ps.filter { p in
        if !show.admits(p) { return false }
        if words.isEmpty { return true }
        let hay = [p.name, p.command ?? "", String(p.pid), p.user ?? "", p.origin ?? ""].joined(separator: " ").lowercased()
        return words.allSatisfy { hay.contains($0) }
    }
    switch sort {
    case .cpu:
        out.sort { ($0.cpu ?? -1, $0.rss) > ($1.cpu ?? -1, $1.rss) }
    case .memory:
        out.sort { ($0.memory, $0.cpu ?? -1) > ($1.memory, $1.cpu ?? -1) }
    case .name:
        out.sort { ($0.name.lowercased(), $0.pid) < ($1.name.lowercased(), $1.pid) }
    }
    return out
}

/// One line of "Heavy right now".
public struct HeavyNote: Equatable, Sendable {
    public enum Kind: Equatable, Sendable { case cpu, memory, conch, agents, other, idle, pressure }
    public var kind: Kind
    public var title: String
    public var detail: String
    /// The process it is about, for a click to select.
    public var pid: Int?
}

/// How long an agent sits idle (or done, unread) before it is said to hold
/// memory for nothing: an hour, as the issue that asked for it put it —
/// long enough that it is not a pause between two prompts.
public let idleAgentAge: TimeInterval = 3600

/// What is worth saying about the machine before anyone reads the table:
/// the busiest process when it holds most of a core or more, the largest
/// when it holds a tenth of memory or a gigabyte, what conch uses of the
/// whole — conch itself, the agents in its panes, the rest of its panes
/// when that is large, and the agents idle for an hour — and memory
/// pressure that is not normal. `now` is the reading's moment by default.
public func heavyNotes(_ s: SystemStats, now: Date? = nil) -> [HeavyNote] {
    var out: [HeavyNote] = []
    if let p = s.processes.filter({ ($0.cpu ?? 0) >= 80 }).max(by: { ($0.cpu ?? 0) < ($1.cpu ?? 0) }) {
        let c = p.cpu ?? 0
        let amount = c >= 150 ? String(format: "%.1f cores", c / 100) : "\(Int(c.rounded()))% of a core"
        out.append(HeavyNote(kind: .cpu, title: "\(p.name) is using \(amount)", detail: whereSaid(p), pid: p.pid))
    }
    let total = s.memory?.total ?? 0
    let big: UInt64 = total > 0 ? min(total / 10, 1 << 30) : 1 << 30
    if let p = s.processes.max(by: { $0.memory < $1.memory }), p.memory >= big {
        out.append(HeavyNote(kind: .memory, title: "\(p.name) holds \(formatBytes(p.memory))", detail: whereSaid(p), pid: p.pid))
    }
    let cores = Double(s.host.cores)
    func cpu(_ ps: [SystemProcess]) -> Double { ps.reduce(0) { $0 + ($1.cpu ?? 0) } }
    func mem(_ ps: [SystemProcess]) -> UInt64 { ps.reduce(UInt64(0)) { $0 + $1.memory } }
    // "1% of CPU and 640 MB", or the memory alone where the cores are not known.
    func uses(_ ps: [SystemProcess]) -> String {
        cores > 0 ? "\(Int((cpu(ps) / cores).rounded()))% of CPU and \(formatBytes(mem(ps)))" : formatBytes(mem(ps))
    }
    func plural(_ n: Int, _ one: String, _ many: String? = nil) -> String { "\(n) \(n == 1 ? one : many ?? one + "s")" }
    // Each pane's processes, largest pane first, with the largest of them —
    // its program, mostly — for a click to select.
    func byPane(_ ps: [SystemProcess]) -> [(pane: String, bytes: UInt64, top: SystemProcess)] {
        Dictionary(grouping: ps) { $0.pane ?? "" }.compactMap { pane, ps in
            ps.max(by: { $0.memory < $1.memory }).map { (pane, mem(ps), $0) }
        }.sorted { ($0.bytes, $1.pane) > ($1.bytes, $0.pane) }
    }

    let own = s.processes.filter { $0.group == .conch }
    if !own.isEmpty {
        var parts: [String] = []
        let server = own.filter { $0.conch == "server" }, app = own.filter { $0.conch == "app" }
        let rest = own.filter { $0.conch != "server" && $0.conch != "app" }
        if !server.isEmpty { parts.append("server \(formatBytes(mem(server)))") }
        if !app.isEmpty { parts.append("this app \(formatBytes(mem(app)))") }
        if !rest.isEmpty { parts.append("\(plural(rest.count, "more", "more")) \(formatBytes(mem(rest)))") }
        out.append(HeavyNote(kind: .conch, title: "conch uses \(uses(own))", detail: parts.joined(separator: " · ")))
    }

    let agents = s.processes.filter { $0.group == .agents }
    if !agents.isEmpty {
        let panes = byPane(agents)
        let largest = panes[0]
        let named = "\(largest.top.origin ?? largest.top.name) \(formatBytes(largest.bytes))"
        out.append(HeavyNote(kind: .agents, title: "\(plural(panes.count, "agent")) \(panes.count == 1 ? "uses" : "use") \(uses(agents))",
                             detail: panes.count == 1 ? named : "largest \(named)", pid: largest.top.pid))
    }

    // Shells cost a few megabytes each, so seventy of them are nothing to
    // say; a build or a test run that holds as much as one large process
    // (the bar above), or most of a core between them, is.
    let other = s.processes.filter { $0.group == .other }
    if !other.isEmpty, mem(other) >= big || cpu(other) >= 80 {
        let panes = byPane(other)
        let top = other.max(by: { $0.memory < $1.memory })!
        out.append(HeavyNote(kind: .other, title: "Other pane processes use \(uses(other))",
                             detail: "\(plural(other.count, "process", "processes")) in \(plural(panes.count, "pane")) · largest \(top.name) \(formatBytes(top.memory))",
                             pid: top.pid))
    }

    // Agents idle for an hour hold their memory for nothing: closing them
    // is what frees it. Only a server that says the state can tell.
    let at = now ?? s.at ?? Date()
    let idle = agents.filter { p in
        guard p.agentState == "idle" || p.agentState == "done", let since = p.agentSince else { return false }
        return at.timeIntervalSince(since) >= idleAgentAge
    }
    if !idle.isEmpty {
        let panes = byPane(idle)
        let n = panes.count
        out.append(HeavyNote(kind: .idle, title: "\(plural(n, "agent")) idle for over an hour \(n == 1 ? "holds" : "hold") \(formatBytes(mem(idle)))",
                             detail: "closing \(n == 1 ? "it" : "them") frees it · largest \(panes[0].top.origin ?? panes[0].top.name) \(formatBytes(panes[0].bytes))",
                             pid: panes[0].top.pid))
    }

    if let m = s.memory, let pr = m.pressure, pr == "warn" || pr == "critical" {
        let swap = m.swapUsed > 0 ? "swap \(formatBytes(m.swapUsed)) used" : "no swap in use"
        out.append(HeavyNote(kind: .pressure, title: pr == "critical" ? "Memory is critically short" : "Memory is under pressure", detail: swap))
    }
    return out
}

private func whereSaid(_ p: SystemProcess) -> String {
    p.origin ?? (p.user.map { "not started by conch · \($0)" } ?? "not started by conch")
}

/// Bytes as people read them: 812 KB, 41 MB, 1.9 GB, 1.2 TB. `whole`
/// drops the decimal — "36 GB" for a machine's memory.
public func formatBytes(_ b: UInt64, whole: Bool = false) -> String {
    let units: [(UInt64, String)] = [(1 << 40, "TB"), (1 << 30, "GB"), (1 << 20, "MB"), (1 << 10, "KB")]
    for (size, name) in units where b >= size {
        let v = Double(b) / Double(size)
        if whole || name == "MB" || name == "KB" || v >= 100 { return "\(Int(v.rounded())) \(name)" }
        return String(format: "%.1f %@", v, name)
    }
    return "\(b) B"
}

/// How long, in its two largest units: "6 d 4 h", "3 h 12 min", "5 min",
/// "40 s".
public func formatSpan(_ seconds: TimeInterval) -> String {
    let s = max(Int(seconds), 0)
    let d = s / 86400, h = s % 86400 / 3600, m = s % 3600 / 60
    if d > 0 { return h > 0 ? "\(d) d \(h) h" : "\(d) d" }
    if h > 0 { return m > 0 ? "\(h) h \(m) min" : "\(h) h" }
    if m > 0 { return "\(m) min" }
    return "\(s) s"
}

/// A process's share of one core as the table writes it: "388%", "64.1%".
public func formatCorePct(_ v: Double?) -> String {
    guard let v else { return "…" }
    return v >= 100 ? "\(Int(v.rounded()))%" : String(format: "%.1f%%", v)
}
