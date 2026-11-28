import Foundation
import Testing
@testable import ConchKit

// What a server sends, field for field as proto.SystemStats writes it:
// nanosecond times, empty fields left out.
private let reply = """
{"at":"2026-11-13T12:00:02.123456789Z",
 "host":{"name":"mac","os":"macOS 26.1","arch":"arm64","model":"Apple M3 Pro","cores":12,"p_cores":6,"e_cores":6,"uptime":533000},
 "cpu":{"total":62.5,"user":49,"system":13.5,"split":true,"cores":[90,80]},
 "memory":{"total":38654705664,"used":21260088115,"apps":13743895347,"wired":4187593113,"compressed":3328599654,
           "cached":6657199308,"free":10737418240,"swap_total":2147483648,"swap_used":1288490188,"pressure":"normal"},
 "load":[7.42,6.1,4.88],
 "disk":{"path":"/Users/me","total":994000000000,"free":412000000000},
 "procs":612,
 "processes":[
   {"pid":52310,"ppid":52288,"name":"go","command":"go test -race ./...","user":"me","cpu":388,"rss":2791728742,
    "started":"2026-11-13T11:56:00Z","pane":"p7","pane_name":"tests","project":"conch","branch":"conch/readme-desktop"},
   {"pid":51022,"ppid":4121,"name":"claude","cpu":64.1,"rss":851443712,"pane":"p3","pane_name":"Fix the docs","agent":"claude","branch":"conch/readme-desktop"},
   {"pid":4410,"ppid":4398,"name":"Google Chrome Helper (Renderer)","user":"me","cpu":41.2,"rss":1478492160},
   {"pid":4121,"ppid":1,"name":"conch","cpu":3.1,"rss":100663296,"conch":"server"},
   {"pid":4090,"ppid":1,"name":"Conch","rss":325058560}
 ],
 "history":[{"at":"2026-11-13T12:00:00Z","cpu":60,"user":48,"system":12,"mem_used":21000000000,"compressed":3000000000},
            {"at":"2026-11-13T12:00:02.5Z","cpu":62.5,"mem_used":21260088115}]}
"""

private func decoded() throws -> SystemStats {
    try conchDecoder().decode(SystemStats.self, from: Data(reply.utf8))
}

@Test func systemStatsDecodesTheServersReply() throws {
    let s = try decoded()
    #expect(s.host.cores == 12 && s.host.pCores == 6 && s.host.uptime == 533000)
    #expect(s.cpu?.split == true && s.cpu?.cores == [90, 80])
    #expect(s.memory?.swapUsed == 1288490188 && s.memory?.pressure == "normal")
    #expect(s.load.count == 3 && s.disk?.free == 412000000000 && s.procs == 612)
    #expect(s.processes.count == 5)
    #expect(s.processes[0].started != nil && s.processes[0].paneName == "tests")
    #expect(s.processes[4].cpu == nil, "the first sample has no CPU for it")
    #expect(s.history.count == 2 && s.history[1].user == 0)
    #expect(s.host.summary(memory: s.memory?.total) == "macOS 26.1 · Apple M3 Pro · 12 cores · 36 GB")
}

@Test func systemStatsFromAnOlderOrSparserServer() throws {
    // A machine that says almost nothing: everything is optional.
    let s = try conchDecoder().decode(SystemStats.self, from: Data(#"{"host":{"cores":0}}"#.utf8))
    #expect(s.cpu == nil && s.memory == nil && s.processes.isEmpty && s.history.isEmpty && s.load.isEmpty)
    #expect(s.host.summary(memory: nil) == "")
    let empty = try conchDecoder().decode(SystemStats.self, from: Data("{}".utf8))
    #expect(empty.host.cores == 0)
    // Linux: no wired or compressed, so "Used" stands for them.
    let linux = SystemMemory(total: 100, used: 40, cached: 30, free: 30)
    #expect(linux.parts.map(\.name) == ["Used", "Cached files", "Free"])
    let mac = SystemMemory(total: 100, used: 40, apps: 20, wired: 10, compressed: 10, cached: 30, free: 30)
    #expect(mac.parts.map(\.name) == ["Apps", "Wired", "Compressed", "Cached files", "Free"])
    #expect(SystemMemory(total: 0, used: 0).usedFraction == 0)
}

@Test func whereAProcessCameFrom() throws {
    let s = try decoded()
    #expect(s.processes[0].origin == "pane tests · conch/readme-desktop")
    #expect(s.processes[1].origin == "Claude · conch/readme-desktop")
    #expect(s.processes[2].origin == nil && !s.processes[2].isConch)
    #expect(s.processes[3].origin == "conch server")
    // This app is its own: the server cannot tell its clients apart.
    let marked = s.marking(app: 4090)
    #expect(marked.processes[4].isConch && marked.processes[4].origin == "conch · this window")
    #expect(s.marking(app: 4121).processes[3].conch == "server", "the server stays the server")
    #expect(s.marking(app: 99).processes == s.processes)
    // A pane in no project, on no branch, with no name: its id.
    #expect(SystemProcess(pid: 1, name: "sh", pane: "p9").origin == "pane p9")
    #expect(SystemProcess(pid: 1, name: "x", pane: "p9", agent: "newagent").origin == "Newagent")
}

@Test func systemRowsSortAndFilter() throws {
    let ps = try decoded().processes
    #expect(systemRows(ps, sort: .cpu).map(\.pid) == [52310, 51022, 4410, 4121, 4090])
    #expect(systemRows(ps, sort: .memory).first?.pid == 52310)
    #expect(systemRows(ps, sort: .memory).last?.pid == 4121)
    #expect(systemRows(ps, sort: .name).map(\.name).first == "claude")
    #expect(systemRows(ps, sort: .cpu, show: .started).map(\.pid) == [52310, 51022, 4121])
    #expect(systemRows(ps, sort: .cpu, show: .conch).map(\.pid) == [4121])
    #expect(systemRows(ps, sort: .cpu, show: .agents).map(\.pid) == [51022])
    #expect(systemRows(ps, sort: .cpu, show: .other).map(\.pid) == [52310])
    #expect(systemRows(ps, sort: .cpu, show: .all).count == 5)
    // Memory sorts by the footprint where there is one.
    let fp = [SystemProcess(pid: 1, name: "a", rss: 900, footprint: 100), SystemProcess(pid: 2, name: "b", rss: 500)]
    #expect(systemRows(fp, sort: .memory).map(\.pid) == [2, 1])
    #expect(systemRows(ps, sort: .cpu, filter: "readme").count == 2)
    #expect(systemRows(ps, sort: .cpu, filter: "GOOGLE renderer").map(\.pid) == [4410])
    #expect(systemRows(ps, sort: .cpu, filter: "4121").map(\.pid) == [4121])
    #expect(systemRows(ps, sort: .cpu, filter: "nothing like it").isEmpty)
    #expect(systemRows([], sort: .name).isEmpty)
}

@Test func heavyNotesSayWhatMatters() throws {
    let notes = heavyNotes(try decoded())
    #expect(notes.map(\.kind) == [.cpu, .memory, .conch, .agents, .other])
    #expect(notes[0].title == "go is using 3.9 cores" && notes[0].pid == 52310)
    #expect(notes[0].detail == "pane tests · conch/readme-desktop")
    #expect(notes[1].title == "go holds 2.6 GB")
    #expect(notes[2].title == "conch uses 0% of CPU and 96 MB" && notes[2].detail == "server 96 MB")
    #expect(notes[3].title == "1 agent uses 5% of CPU and 812 MB" && notes[3].detail == "Claude · conch/readme-desktop 812 MB")
    #expect(notes[3].pid == 51022)
    #expect(notes[4].title == "Other pane processes use 32% of CPU and 2.6 GB")
    #expect(notes[4].detail == "1 process in 1 pane · largest go 2.6 GB" && notes[4].pid == 52310)

    // A quiet machine: nothing busy or large, nothing of conch's.
    var quiet = SystemStats(host: SystemHost(cores: 4), memory: SystemMemory(total: 8 << 30, used: 1 << 30))
    quiet.processes = [SystemProcess(pid: 1, name: "a", user: "me", cpu: 79, rss: 100 << 20)]
    #expect(heavyNotes(quiet).isEmpty)
    quiet.processes[0].cpu = 80
    #expect(heavyNotes(quiet).first?.title == "a is using 80% of a core")
    #expect(heavyNotes(quiet).first?.detail == "not started by conch · me")
    // A small machine: a tenth of its memory is large.
    quiet.memory = SystemMemory(total: 512 << 20, used: 400 << 20, pressure: "critical")
    let small = heavyNotes(quiet)
    #expect(small.contains { $0.kind == .memory })
    #expect(small.last?.title == "Memory is critically short" && small.last?.detail == "no swap in use")
    #expect(heavyNotes(SystemStats()).isEmpty)
}

@Test func historyMergesOnceAndKeepsAnHour() {
    let t0 = Date(timeIntervalSince1970: 1_000_000)
    var h = SystemHistory([SystemPoint(at: t0, cpu: 1)])
    h.merge([SystemPoint(at: t0, cpu: 1), SystemPoint(at: t0.addingTimeInterval(2), cpu: 2)])
    #expect(h.points.count == 2, "the point already held is not added twice")
    h.merge([SystemPoint(at: t0.addingTimeInterval(1), cpu: 9)])
    #expect(h.points.map(\.cpu) == [1, 2], "an older point arriving late is dropped")
    h.merge([SystemPoint(at: t0.addingTimeInterval(6), cpu: 4), SystemPoint(at: t0.addingTimeInterval(4), cpu: 3)])
    #expect(h.points.map(\.cpu) == [1, 2, 3, 4], "out of order within a reply: sorted")
    h.merge([SystemPoint(at: t0.addingTimeInterval(3601), cpu: 5)])
    #expect(h.points.map(\.cpu) == [2, 3, 4, 5], "past the hour: let go")
    #expect(h.last == t0.addingTimeInterval(3601))
    #expect(h.window(10, now: t0.addingTimeInterval(3601)).map(\.cpu) == [5])
    #expect(SystemHistory().last == nil)
    h.merge([])
    #expect(h.points.count == 4)
}

@Test func statsParamsAreWhatGoReads() throws {
    let p = SystemStatsParams(since: Date(timeIntervalSince1970: 1_700_000_000.25), summary: false)
    let json = String(decoding: try JSONEncoder().encode(p), as: UTF8.self)
    #expect(json == #"{"since":"2023-11-14T22:13:20.250Z"}"#)
    #expect(String(decoding: try JSONEncoder().encode(SystemStatsParams(summary: true)), as: UTF8.self) == #"{"summary":true}"#)
    #expect(String(decoding: try JSONEncoder().encode(SystemStatsParams()), as: UTF8.self) == "{}")
}

@Test func formattingForPeople() {
    #expect(formatBytes(0) == "0 B")
    #expect(formatBytes(812 << 10) == "812 KB")
    #expect(formatBytes(41 << 20) == "41 MB")
    #expect(formatBytes(UInt64(1.94 * Double(1 << 30))) == "1.9 GB")
    #expect(formatBytes(36 << 30, whole: true) == "36 GB")
    #expect(formatBytes(412 << 30) == "412 GB")
    #expect(formatBytes(UInt64(1.25 * Double(1 << 40))) == "1.2 TB" || formatBytes(UInt64(1.25 * Double(1 << 40))) == "1.3 TB")
    #expect(formatSpan(40) == "40 s")
    #expect(formatSpan(300) == "5 min")
    #expect(formatSpan(3 * 3600 + 12 * 60) == "3 h 12 min")
    #expect(formatSpan(7200) == "2 h")
    #expect(formatSpan(6 * 86400 + 4 * 3600 + 59) == "6 d 4 h")
    #expect(formatSpan(86400) == "1 d")
    #expect(formatSpan(-5) == "0 s")
    #expect(formatCorePct(nil) == "…")
    #expect(formatCorePct(388.0) == "388%")
    #expect(formatCorePct(64.13) == "64.1%")
}

private let mb: UInt64 = 1 << 20

/// A Mac like the one in #388: the server, this app, attaches and a conch
/// mcp; three agents (one with an MCP server of its own, two idle for
/// over an hour); shells, and a build.
private func busyMac(now: Date) -> SystemStats {
    let hourAgo = now.addingTimeInterval(-3700), justNow = now.addingTimeInterval(-60)
    var s = SystemStats(host: SystemHost(cores: 10), memory: SystemMemory(total: 32 << 30, used: 20 << 30))
    s.processes = [
        SystemProcess(pid: 10, name: "conch", cpu: 5, rss: 300 * mb, footprint: 200 * mb, conch: "server"),
        SystemProcess(pid: 11, name: "Conch", cpu: 3, rss: 400 * mb, footprint: 394 * mb),
        SystemProcess(pid: 12, ppid: 11, name: "conch", rss: 20 * mb, footprint: 10 * mb, conch: "client"),
        SystemProcess(pid: 13, ppid: 11, name: "conch", rss: 20 * mb, footprint: 10 * mb, conch: "client"),
        SystemProcess(pid: 20, name: "claude", cpu: 20, rss: 600 * mb, footprint: 515 * mb, pane: "p1", paneName: "readme",
                      branch: "conch/readme-desktop", agent: "claude", agentState: "working", agentSince: justNow),
        SystemProcess(pid: 21, ppid: 20, name: "node", rss: 60 * mb, footprint: 50 * mb, pane: "p1", agent: "claude",
                      agentState: "working", agentSince: justNow),
        SystemProcess(pid: 22, ppid: 20, name: "conch", rss: 15 * mb, footprint: 15 * mb, pane: "p1", agent: "claude",
                      agentState: "working", agentSince: justNow),
        SystemProcess(pid: 30, name: "claude", rss: 300 * mb, footprint: 300 * mb, pane: "p2", branch: "conch/a", agent: "claude",
                      agentState: "idle", agentSince: hourAgo),
        SystemProcess(pid: 40, name: "codex", rss: 200 * mb, footprint: 200 * mb, pane: "p3", branch: "conch/b", agent: "codex",
                      agentState: "done", agentSince: hourAgo),
        SystemProcess(pid: 50, name: "zsh", rss: 5 * mb, footprint: 4 * mb, pane: "p4", paneName: "shell"),
        SystemProcess(pid: 51, name: "zsh", rss: 5 * mb, footprint: 4 * mb, pane: "p5", paneName: "shell"),
        SystemProcess(pid: 60, name: "Safari", cpu: 30, rss: 700 * mb, footprint: 700 * mb),
    ]
    return s.marking(app: 11)
}

@Test func heavyNotesSplitWhatConchStarted() {
    let now = Date(timeIntervalSince1970: 2_000_000)
    let notes = heavyNotes(busyMac(now: now), now: now)
    #expect(notes.map(\.kind) == [.conch, .agents, .idle], "the shells are small: no line for them")
    // conch itself: the server, the app, two attaches and the conch mcp
    // under an agent — by footprint, not RSS.
    #expect(notes[0].title == "conch uses 1% of CPU and 629 MB")
    #expect(notes[0].detail == "server 200 MB · this app 394 MB · 3 more 35 MB")
    // The agents: three panes, the conch mcp not among them.
    #expect(notes[1].title == "3 agents use 2% of CPU and 1.0 GB")
    #expect(notes[1].detail == "largest Claude · conch/readme-desktop 565 MB")
    #expect(notes[1].pid == 20, "a click selects the largest agent's program")
    #expect(notes[2].title == "2 agents idle for over an hour hold 500 MB")
    #expect(notes[2].detail == "closing them frees it · largest Claude · conch/a 300 MB" && notes[2].pid == 30)

    // An hour has not yet passed: not idle for long.
    #expect(!heavyNotes(busyMac(now: now), now: now.addingTimeInterval(-200)).contains { $0.kind == .idle })
    // The reading's own moment stands for now.
    var at = busyMac(now: now)
    at.at = now
    #expect(heavyNotes(at).map(\.kind) == [.conch, .agents, .idle])
}

@Test func heavyNotesOtherPaneProcessesOnlyWhenLarge() {
    let now = Date(timeIntervalSince1970: 2_000_000)
    var s = busyMac(now: now)
    s.processes.append(SystemProcess(pid: 70, ppid: 50, name: "go", cpu: 10, rss: 1200 * mb, pane: "p4"))
    let other = heavyNotes(s, now: now).first { $0.kind == .other }
    #expect(other?.title == "Other pane processes use 1% of CPU and 1.2 GB")
    #expect(other?.detail == "3 processes in 2 panes · largest go 1.2 GB" && other?.pid == 70)
    // Small, but busy: most of a core between them.
    s.processes[s.processes.count - 1].rss = 50 * mb
    s.processes[s.processes.count - 1].cpu = 90
    #expect(heavyNotes(s, now: now).contains { $0.kind == .other })
    s.processes[s.processes.count - 1].cpu = 70
    #expect(!heavyNotes(s, now: now).contains { $0.kind == .other })
}

@Test func heavyNotesWithNoAgentsOrOnlyAgents() {
    let now = Date(timeIntervalSince1970: 2_000_000)
    let all = busyMac(now: now)
    var none = all
    none.processes = all.processes.filter { $0.agent == nil }
    #expect(heavyNotes(none, now: now).map(\.kind) == [.conch])
    #expect(heavyNotes(none, now: now)[0].detail == "server 200 MB · this app 394 MB · 2 more 20 MB")

    var only = all
    only.processes = all.processes.filter { $0.group == .agents }
    let notes = heavyNotes(only, now: now)
    #expect(notes.map(\.kind) == [.agents, .idle], "no conch line without conch's own processes")
    // One agent: no "largest".
    var one = only
    one.processes = only.processes.filter { $0.pane == "p2" }
    #expect(heavyNotes(one, now: now)[0].title == "1 agent uses 0% of CPU and 300 MB")
    #expect(heavyNotes(one, now: now)[0].detail == "Claude · conch/a 300 MB")
    #expect(heavyNotes(one, now: now)[1].title == "1 agent idle for over an hour holds 300 MB")
    // Cores unknown: memory alone.
    one.host.cores = 0
    #expect(heavyNotes(one, now: now)[0].title == "1 agent uses 300 MB")
}

@Test func heavyNotesFromAnOlderServer() throws {
    // No footprint, no agent state, no client marks: RSS, and no idle line.
    let old = """
    {"host":{"cores":4},"processes":[
      {"pid":1,"ppid":0,"name":"conch","rss":104857600,"conch":"server"},
      {"pid":2,"ppid":1,"name":"claude","cpu":40,"rss":209715200,"pane":"p1","agent":"claude"},
      {"pid":3,"ppid":1,"name":"zsh","rss":5242880,"pane":"p2"}]}
    """
    let s = try conchDecoder().decode(SystemStats.self, from: Data(old.utf8))
    #expect(s.processes.allSatisfy { $0.footprint == nil && $0.agentState == nil && $0.agentSince == nil })
    #expect(s.processes[1].memory == 200 * mb)
    let notes = heavyNotes(s, now: Date())
    #expect(notes.map(\.kind) == [.conch, .agents])
    #expect(notes[0].title == "conch uses 0% of CPU and 100 MB")
    #expect(notes[1].title == "1 agent uses 10% of CPU and 200 MB")

    // A newer one: the fields decode, nanoseconds and all.
    let new = """
    {"pid":5,"ppid":1,"name":"claude","rss":300,"footprint":200,"pane":"p1","agent":"claude",
     "agent_state":"idle","agent_since":"2026-11-28T08:00:00.123456789Z"}
    """
    let p = try conchDecoder().decode(SystemProcess.self, from: Data(new.utf8))
    #expect(p.footprint == 200 && p.memory == 200 && p.agentState == "idle" && p.agentSince != nil)
    // A footprint of nothing, or missing: RSS.
    #expect(SystemProcess(pid: 1, name: "x", rss: 7, footprint: 0).memory == 7)
    #expect(SystemProcess(pid: 1, name: "x", rss: 7).memory == 7)
    // Which group: conch's own programs, even inside an agent's pane.
    #expect(SystemProcess(pid: 1, name: "conch", pane: "p1", agent: "claude").group == .conch)
    #expect(SystemProcess(pid: 1, name: "x", conch: "client").group == .conch)
    #expect(SystemProcess(pid: 1, name: "x", pane: "p1", agent: "claude").group == .agents)
    #expect(SystemProcess(pid: 1, name: "x", pane: "p1").group == .other)
    #expect(SystemProcess(pid: 1, name: "conch").group == nil, "a conch the server did not mark is not known to be ours")
}
