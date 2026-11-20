import ConchKit
import Foundation
import Testing

// An agent and a browser in one tab: a browser split beside a pane, and a
// whole tab moved beside (or below) another.

private func twoTabs() -> Layout {
    var l = Layout()
    l.open("local|p1")
    l.open(browserKey("http://localhost:3000"))
    return l
}

@Test func aBrowserSplitsBesideTheFocusedPane() {
    var l = Layout()
    l.open("local|p1")
    let b = browserKey("http://localhost:3000")
    l.split(.horizontal, with: b)
    #expect(l.tabs.count == 1)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf("local|p1"), .leaf(b)]))
    #expect(l.focused == b)
}

@Test func aBrowserAlreadyInATabMovesIntoTheSplit() {
    var l = twoTabs()
    let b = browserKey("http://localhost:3000")
    l.select(0)
    l.split(.horizontal, with: b)
    #expect(l.tabs.count == 1, "its own tab closes: a pane is shown in one place")
    #expect(l.tabs[0].root.panes == ["local|p1", b])
}

@Test func aTabMovesBesideAnother() {
    var l = twoTabs()
    let b = browserKey("http://localhost:3000")
    l.join(1, into: 0, .horizontal)
    #expect(l.tabs.count == 1)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf("local|p1"), .leaf(b)]))
    #expect(l.active == 0)
    #expect(l.focused == b, "the tab moved in takes the keyboard")
}

@Test func aTabMovesBelowAnotherAndKeepsItsSplits() {
    var l = Layout()
    l.open("local|p1")
    l.open("local|p2")
    l.split(.horizontal, with: "local|p3")
    l.open("local|p4")
    // Tab 1 (p2 | p3) below tab 0; tab 2 stays and stays last.
    l.join(1, into: 0, .vertical)
    #expect(l.tabs.count == 2)
    #expect(l.tabs[0].root == .split(.vertical, [.leaf("local|p1"), .split(.horizontal, [.leaf("local|p2"), .leaf("local|p3")])]))
    #expect(l.tabs[0].focused == "local|p3")
    #expect(l.tabs[1].root.panes == ["local|p4"])
    #expect(l.active == 0)
}

@Test func joiningAnEarlierTabIntoALaterOneKeepsTheRightIndex() {
    var l = Layout()
    l.open("local|a")
    l.open("local|b")
    l.open("local|c")
    l.join(0, into: 2, .horizontal)
    #expect(l.tabs.count == 2)
    #expect(l.tabs[1].root.panes == ["local|c", "local|a"])
    #expect(l.active == 1)
}

@Test func joinRefusesItselfOutOfRangeAndWorkbenches() {
    var l = twoTabs()
    let before = l
    l.join(0, into: 0, .horizontal)
    l.join(0, into: 5, .horizontal)
    l.join(-1, into: 0, .horizontal)
    #expect(l == before)
    #expect(!l.canJoin(0, into: 0))
    #expect(l.canJoin(1, into: 0))
    l.tabs[0].bench = .ide
    #expect(!l.canJoin(1, into: 0))
    #expect(!l.canJoin(0, into: 1))
    l.join(1, into: 0, .horizontal)
    #expect(l == { var x = before; x.tabs[0].bench = .ide; return x }())
}

@Test func joinDropsOldSharesAndZoom() {
    var l = twoTabs()
    l.tabs[0].weights = ["": [0.3, 0.7]]
    l.tabs[0].zoomed = true
    l.tabs[1].hidden = ["x"]
    l.join(1, into: 0, .horizontal)
    #expect(l.tabs[0].weights.isEmpty)
    #expect(!l.tabs[0].zoomed)
    #expect(l.tabs[0].hidden == ["x"])
}

@Test func thePaletteOffersBrowserSplits() {
    let ids = paletteCommands.map(\.id)
    #expect(ids.contains("splitRightBrowser"))
    #expect(ids.contains("splitDownBrowser"))
    #expect(paletteCommands.first { $0.id == "splitRightBrowser" }?.title == "Split Right with Browser")
}
