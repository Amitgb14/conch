import Foundation
import Testing
@testable import ConchKit

// What an idle agent left running (agent.background.v1): decoded from the
// server, said as a pill beside its state, listed in the pill's tooltip.
@Test func backgroundJobsDecode() throws {
    let a = try conchDecoder().decode(AgentStatus.self, from: Data(#"{"name":"claude","state":"done","background":2,"jobs":[{"pid":41,"command":"npm run dev","since":"2026-11-13T09:00:00.123456789Z","ports":[5173]},{"pid":42,"command":"pytest -f","since":"2026-11-13T09:00:00Z"}]}"#.utf8))
    #expect(a.background == 2 && a.jobs?.count == 2)
    #expect(a.jobs?[0].pid == 41 && a.jobs?[0].command == "npm run dev" && a.jobs?[0].ports == [5173])
    #expect(a.jobs?[1].ports == nil)
    // From an older server: nothing.
    let old = try conchDecoder().decode(AgentStatus.self, from: Data(#"{"name":"claude","state":"idle"}"#.utf8))
    #expect(old.background == nil && old.jobs == nil && old.runningCount == 0 && old.backgroundPill == nil)
    // A change in what runs is a change to draw.
    var b = a
    b.jobs = nil
    #expect(a != b)
}

@Test func backgroundPillAndTip() {
    var a = AgentStatus(name: "claude", state: .idle)
    #expect(a.backgroundPill == nil)
    a.background = 2
    #expect(a.backgroundPill == "idle · 2 running")
    a.state = .done
    #expect(a.backgroundPill == "done · 2 running")
    a.state = .blocked
    #expect(a.backgroundPill == "waiting · 2 running")
    // Working, its children are its work: the server sends none, and none is said.
    a.state = .working
    #expect(a.backgroundPill == nil)
    a.state = .idle
    a.background = -1
    #expect(a.runningCount == 0 && a.backgroundPill == nil)

    let now = Date(timeIntervalSince1970: 10_000)
    let jobs = [
        BackgroundJob(pid: 41, command: "npm run dev", since: now.addingTimeInterval(-185), ports: [5173, 8080]),
        BackgroundJob(pid: 42, command: "pytest -f", since: now.addingTimeInterval(-9)),
        BackgroundJob(pid: 43, command: "", since: nil),
    ]
    #expect(backgroundTip(jobs, now: now) == "npm run dev — pid 41 · 3m · :5173 :8080\npytest -f — pid 42 · 9s\npid 43")
    #expect(backgroundTip([], now: now) == "")
    #expect(jobAge(7200 + 120) == "2h 2m" && jobAge(3 * 86400 + 3600) == "3d 1h" && jobAge(-5) == "0s")
}

// The notification that it finished or waits says what it left running,
// as the TUI's does.
@Test func attentionAlertSaysWhatStillRuns() {
    func pane(_ s: AgentState, msg: String? = nil, n: Int, jobs: [BackgroundJob]? = nil) -> PaneInfo {
        var p = PaneInfo(id: "p1", name: "w", agent: AgentStatus(name: "claude", state: s, message: msg))
        p.agent?.background = n
        p.agent?.jobs = jobs
        return p
    }
    let noon = Calendar.current.date(bySettingHour: 12, minute: 0, second: 0, of: Date())!
    let dev = [BackgroundJob(pid: 4, command: "npm run dev")]
    func body(_ p: PaneInfo) -> String? {
        attentionAlert(old: pane(.working, n: 0), new: p, machine: nil, viewing: false, settings: NotifySettings(), now: noon)?.body
    }
    #expect(body(pane(.done, n: 1, jobs: dev)) == "w finished, 1 still running (npm run dev)")
    #expect(body(pane(.blocked, n: 1, jobs: dev)) == "w is waiting for you, 1 still running (npm run dev)")
    #expect(body(pane(.blocked, msg: "Allow Bash?", n: 1, jobs: dev)) == "Allow Bash? · 1 still running (npm run dev)")
    #expect(body(pane(.done, n: 3, jobs: dev + [BackgroundJob(pid: 5, command: "b"), BackgroundJob(pid: 6, command: "c")]))
        == "w finished, 3 still running (npm run dev, b, …)")
    #expect(body(pane(.done, n: 2)) == "w finished, 2 still running")
    #expect(body(pane(.done, n: 0)) == "w finished")
    #expect(backgroundSummary(AgentStatus(name: "x", state: .done)) == "")
}
