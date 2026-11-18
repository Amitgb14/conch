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
