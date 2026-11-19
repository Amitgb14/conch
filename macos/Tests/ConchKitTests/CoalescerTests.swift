import Combine
import Foundation
import Testing
@testable import ConchKit

// A burst of marks fires once, after the delay; a mark after it fired
// fires again; no marks, nothing. The schedule is a list the test runs.
@MainActor @Test func coalescerFiresOncePerBurst() {
    var queued: [(Double, () -> Void)] = []
    var fired = 0
    let c = Coalescer(delay: 0.05, schedule: { queued.append(($0, $1)) }) { fired += 1 }
    #expect(queued.isEmpty && fired == 0 && !c.pending)

    for _ in 0..<50 { c.mark() }
    #expect(queued.count == 1 && queued[0].0 == 0.05)
    #expect(fired == 0 && c.pending)
    queued.removeFirst().1()
    #expect(fired == 1 && !c.pending)

    // The next burst is its own.
    c.mark()
    c.mark()
    #expect(queued.count == 1)
    queued.removeFirst().1()
    #expect(fired == 2)
}

// Marked from inside its own firing (an event handled while views
// redraw): the mark is kept for the next round, not lost.
@MainActor @Test func coalescerMarkDuringFire() {
    var queued: [() -> Void] = []
    var fired = 0
    var c: Coalescer!
    c = Coalescer(delay: 0, schedule: { queued.append($1) }) {
        fired += 1
        if fired == 1 { c.mark() }
    }
    c.mark()
    queued.removeFirst()()
    #expect(fired == 1 && queued.count == 1)
    queued.removeFirst()()
    #expect(fired == 2 && queued.isEmpty)
}

// A published dictionary that is asked to drop a key it does not hold
// tells nobody: the window's pane events did that on every update and
// redrew the whole window each time. One it holds is dropped and said.
@MainActor final class Held: ObservableObject {
    @Published var questions: [String: Int] = [:]
}

@MainActor @Test func dropValueOnlyPublishesAChange() {
    let h = Held()
    var told = 0
    let sub = h.objectWillChange.sink { told += 1 }
    defer { sub.cancel() }

    dropValue("local|p1", from: \Held.questions, of: h)
    #expect(told == 0)

    // What it replaces: removing a missing key still publishes.
    h.questions.removeValue(forKey: "local|p1")
    #expect(told == 1)

    h.questions["local|p2"] = 1
    told = 0
    dropValue("local|p2", from: \Held.questions, of: h)
    #expect(told == 1 && h.questions.isEmpty)
}

// The window redraws for pane updates ten times a second, not once a
// frame: forty busy panes redrawn at 60 Hz kept a third of a core busy
// redrawing tree rows. Not slower than a glance either, so an agent's
// state still changes on screen at once.
@Test func paneRedrawIsTenASecond() {
    #expect(Coalescer.paneRedraw >= 0.1)
    #expect(Coalescer.paneRedraw <= 0.2)
}
