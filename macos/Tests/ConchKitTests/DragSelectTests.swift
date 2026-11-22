import Foundation
import Testing
@testable import ConchKit

struct DragSelectTests {
    let a = CGPoint(x: 40, y: 80)

    func at(_ dx: Double, _ dy: Double = 0) -> CGPoint { CGPoint(x: 40 + dx, y: 80 + dy) }

    func pressed(clicks: Int = 1, modified: Bool = false, agent: Bool = true, captured: Bool = true,
                 cell: Double = 8) -> (DragSelect, [DragStep]) {
        var d = DragSelect()
        let steps = d.press(at: a, clicks: clicks, modified: modified, agent: agent, captured: captured, cellWidth: cell)
        return (d, steps)
    }

    @Test func clickIsStillTheAgents() {
        var (d, steps) = pressed()
        #expect(steps == []) // held
        let up = d.release(at: a)
        #expect(up == [.click(a)])
    }

    @Test func aShakyClickIsAClickAtItsPress() {
        var (d, _) = pressed()
        let moved = d.drag(to: at(4, -4)) // half a cell exactly
        let up = d.release(at: at(4, -4))
        #expect(moved == [])
        #expect(up == [.click(a)])
    }

    @Test func dragSelectsFromThePressAndCopiesOnce() {
        var (d, _) = pressed()
        let first = d.drag(to: at(4.5))
        let next = d.drag(to: at(120, 30))
        let up = d.release(at: at(121, 30))
        let after = d.release(at: a)
        #expect(first == [.select(a, clicks: 1), .extend(at(4.5))])
        #expect(next == [.extend(at(120, 30))])
        #expect(up == [.end(at(121, 30)), .copy])
        #expect(after == [.pass]) // nothing is left to copy again
    }

    @Test(arguments: [CGPoint(x: 5, y: 80), CGPoint(x: 40, y: -12), CGPoint(x: 40, y: 900), CGPoint(x: -3, y: -3)])
    func dragsInEveryDirectionAndPastTheEdge(_ to: CGPoint) {
        var (d, _) = pressed()
        let moved = d.drag(to: to)
        #expect(moved == [.select(a, clicks: 1), .extend(to)])
    }

    @Test func comingBackToThePressIsStillADrag() {
        var (d, _) = pressed()
        _ = d.drag(to: at(50))
        let back = d.drag(to: a)
        let up = d.release(at: a)
        #expect(back == [.extend(a)])
        #expect(up == [.end(a), .copy]) // never a click the agent acts on
    }

    @Test func flickWithNoDragReported() {
        var (d, _) = pressed()
        let up = d.release(at: at(60))
        #expect(up == [.select(a, clicks: 1), .extend(at(60)), .end(at(60)), .copy])
    }

    @Test(arguments: [2, 3, 4])
    func severalClicksSelect(_ n: Int) {
        var (d, steps) = pressed(clicks: n)
        let up = d.release(at: a)
        #expect(steps == [.select(a, clicks: n)])
        #expect(up == [.end(a), .copy])
    }

    @Test func modifiersKeepTheirMeaning() {
        var (d, steps) = pressed(modified: true)
        let moved = d.drag(to: at(90))
        let up = d.release(at: at(90))
        #expect(steps == [.pass])
        #expect(moved == [.pass])
        #expect(up == [.pass])
    }

    @Test(arguments: [true, false])
    func notAnAgentIsUntouched(_ captured: Bool) {
        var (d, steps) = pressed(agent: false, captured: captured)
        let moved = d.drag(to: at(90))
        let up = d.release(at: at(90))
        #expect(steps == [.pass]) // vim's drag stays vim's
        #expect(moved == [.pass])
        #expect(up == [.pass])
    }

    @Test func agentWithoutTheMouseOnlyGainsTheCopy() {
        var (d, steps) = pressed(captured: false)
        let moved = d.drag(to: at(90))
        let up = d.release(at: at(90))
        #expect(steps == [.pass])
        #expect(moved == [.pass])
        #expect(up == [.pass, .copy])

        var (click, _) = pressed(captured: false)
        let clicked = click.release(at: a)
        #expect(clicked == [.pass])

        var (word, _) = pressed(clicks: 2, captured: false)
        let copied = word.release(at: a)
        #expect(copied == [.pass, .copy])
    }

    @Test(arguments: [0.0, -1, 2])
    func slopHasAFloor(_ cell: Double) {
        var (d, _) = pressed(cell: cell)
        let small = d.drag(to: at(DragSelect.minSlop))
        let over = d.drag(to: at(DragSelect.minSlop + 0.1))
        #expect(small == [])
        #expect(over.count == 2)
    }

    @Test func eventsWithNoPressPass() {
        var d = DragSelect()
        let moved = d.drag(to: a)
        let up = d.release(at: a)
        #expect(moved == [.pass])
        #expect(up == [.pass])
    }

    @Test func aPressWhoseReleaseNeverCameIsForgotten() {
        var (d, _) = pressed()
        let second = d.press(at: at(200), clicks: 1, modified: true, agent: true, captured: true, cellWidth: 8)
        let up = d.release(at: at(200))
        #expect(second == [.pass])
        #expect(up == [.pass])
    }
}
