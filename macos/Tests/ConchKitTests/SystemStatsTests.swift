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
    #expect(systemRows(ps, sort: .cpu, conchOnly: true).map(\.pid) == [52310, 51022, 4121])
    #expect(systemRows(ps, sort: .cpu, filter: "readme").count == 2)
    #expect(systemRows(ps, sort: .cpu, filter: "GOOGLE renderer").map(\.pid) == [4410])
    #expect(systemRows(ps, sort: .cpu, filter: "4121").map(\.pid) == [4121])
    #expect(systemRows(ps, sort: .cpu, filter: "nothing like it").isEmpty)
    #expect(systemRows([], sort: .name).isEmpty)
}

@Test func heavyNotesSayWhatMatters() throws {
    let notes = heavyNotes(try decoded())
    #expect(notes.map(\.kind) == [.cpu, .memory, .conch])
    #expect(notes[0].title == "go is using 3.9 cores" && notes[0].pid == 52310)
    #expect(notes[0].detail == "pane tests · conch/readme-desktop")
    #expect(notes[1].title == "go holds 2.6 GB")
    #expect(notes[2].title.hasPrefix("conch and its panes use 38% of CPU"))
    #expect(notes[2].detail == "3 processes in 2 panes · 1 agent")

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
