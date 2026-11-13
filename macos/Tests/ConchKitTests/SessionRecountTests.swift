import Foundation
import Testing
@testable import ConchKit

// A burst of asks about one project counts it once, a settle after the
// last; one asked about without pause is still counted by maxWait; each
// project waits on its own.
@Test func sessionRecountsCoalesce() {
    let t0 = Date(timeIntervalSince1970: 1000)
    var r = SessionRecounts()
    #expect(r.next == nil && r.take(now: t0).isEmpty)
    r.note("box|r1", now: t0)
    r.note("box|r1", now: t0.addingTimeInterval(1))
    r.note("box|r1", now: t0.addingTimeInterval(2))
    #expect(r.take(now: t0.addingTimeInterval(4)).isEmpty) // 2 + 3 not reached
    #expect(r.next == t0.addingTimeInterval(5))
    #expect(r.take(now: t0.addingTimeInterval(5)) == ["box|r1"])
    #expect(r.take(now: t0.addingTimeInterval(60)).isEmpty) // once

    // Asked about every second: still counted by maxWait.
    for i in 0..<30 { r.note("box|busy", now: t0.addingTimeInterval(Double(i))) }
    #expect(r.next == t0.addingTimeInterval(SessionRecounts.maxWait))

    // Now (after 0) beside a later one; dropping one forgets it.
    r.note("local|r2", now: t0, after: 0)
    r.note("local|r3", now: t0)
    #expect(r.take(now: t0) == ["local|r2"])
    r.drop("local|r3")
    r.drop("box|busy")
    #expect(r.next == nil)
}

// Only an agent's pane in a project stopping work, or ending, says a
// conversation may have been saved there.
@Test func sessionsMayHaveChangedOnlyWhenAnAgentStops() {
    func pane(_ state: AgentState?, project: String? = "r1", exited: Bool = false) -> PaneInfo {
        PaneInfo(id: "p1", name: "claude", state: exited ? "exited" : "running",
                 agent: state.map { AgentStatus(name: "claude", state: $0) }, projectID: project)
    }
    #expect(sessionsMayHaveChanged(old: pane(.working), new: pane(.idle)))
    #expect(sessionsMayHaveChanged(old: pane(.working), new: pane(.done)))
    #expect(sessionsMayHaveChanged(old: pane(.working), new: pane(.blocked)))
    #expect(!sessionsMayHaveChanged(old: pane(.working), new: pane(.working))) // a frame
    #expect(!sessionsMayHaveChanged(old: pane(.idle), new: pane(.working)))
    #expect(!sessionsMayHaveChanged(old: pane(.idle), new: pane(.idle)))
    #expect(!sessionsMayHaveChanged(old: nil, new: pane(.working))) // just started
    #expect(sessionsMayHaveChanged(old: pane(.idle), new: pane(.idle, exited: true)))
    #expect(!sessionsMayHaveChanged(old: pane(.idle, exited: true), new: pane(.idle, exited: true)))
    #expect(!sessionsMayHaveChanged(old: pane(.working, project: nil), new: pane(.idle, project: nil)))
    #expect(!sessionsMayHaveChanged(old: pane(nil), new: pane(nil, exited: true))) // a terminal
}

// What a Sessions view read becomes the row's count when it is the whole
// list.
@Test func sessionCountFromAList() {
    #expect(sessionCount(listed: 2, partial: false, limit: 300, known: nil) == 2)
    #expect(sessionCount(listed: 0, partial: false, limit: 300, known: 4) == 0) // deleted
    #expect(sessionCount(listed: 2, partial: true, limit: 300, known: 1) == nil)
    #expect(sessionCount(listed: 300, partial: false, limit: 300, known: 420) == 420)
    #expect(sessionCount(listed: 300, partial: false, limit: 300, known: 10) == 300)
}
