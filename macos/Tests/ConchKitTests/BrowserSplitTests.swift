import ConchKit
import Foundation
import Testing

// An agent and a browser in one tab (#323): a browser split beside a pane,
// a whole tab moved beside (or below) another, a one-pane tab moved beside
// a pane and back out, and what a split's header menu offers.

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

private let agent = key("local", "p1")
private let shell = key("local", "p2")
private let page = browserKey("http://localhost:3000")

@Test func splittingWithABrowser() {
    var l = Layout()
    l.split(.horizontal, with: page)
    #expect(l.tabs.map(\.root) == [.leaf(page)], "no tab yet: a tab of its own")
    l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page)
    #expect(l.tabs.count == 1)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(agent), .leaf(page)]))
    #expect(l.focused == page, "the new split has the keyboard")
    let down = browserKey("http://localhost:4000")
    l.split(.vertical, with: down)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(agent), .split(.vertical, [.leaf(page), .leaf(down)])]))
}

@Test func aPortOpensBesideItsAgent() {
    // The agent is not the focused split: its page still goes beside it.
    var l = Layout()
    l.open(agent)
    l.split(.vertical, with: shell)
    #expect(l.focused == shell)
    l.split(.horizontal, with: page, beside: agent)
    #expect(l.tabs[0].root == .split(.vertical, [.split(.horizontal, [.leaf(agent), .leaf(page)]), .leaf(shell)]))
    #expect(l.focused == page)
    // In another tab: that tab comes forward with it.
    l.open(key("local", "p9"))
    #expect(l.active == 1)
    let other = browserKey("http://localhost:5173")
    l.split(.horizontal, with: other, beside: agent)
    #expect(l.active == 0 && l.focused == other)
    #expect(l.tabs[1].root == .leaf(key("local", "p9")), "the tab in front is left alone")
    // Beside a pane shown nowhere: a tab of its own, never lost.
    let lone = browserKey("http://localhost:8080")
    l.split(.horizontal, with: lone, beside: key("local", "gone"))
    #expect(l.tabs.last?.root == .leaf(lone) && l.focused == lone)
    // Beside itself: stays put and is shown.
    l.select(0)
    let before = l.tabs
    l.split(.horizontal, with: lone, beside: lone)
    #expect(l.tabs == before && l.focused == lone)
}

@Test func splitBeforeGoesAhead() {
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page, before: true)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(page), .leaf(agent)]))
    l.split(.horizontal, with: shell, beside: agent, before: true)
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(page), .leaf(shell), .leaf(agent)]), "a sibling, not a split of one")
}

@Test func aBrowserTabMovedIntoASplitAndBack() {
    var l = Layout()
    l.open(agent)
    l.open(page)
    l.tabs[1].name = "docs"
    #expect(l.tabs.count == 2 && l.active == 1)
    l.moveTab(1, beside: agent)
    #expect(l.tabs.count == 1, "its tab goes: the page is in one place")
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(agent), .leaf(page)]))
    #expect(l.active == 0 && l.focused == page)
    // Back to a tab of its own, after the one it was in, same key.
    l.detach(page)
    #expect(l.tabs.map(\.root) == [.leaf(agent), .leaf(page)])
    #expect(l.active == 1 && l.focused == page)
    #expect(l.tabs[0].focused == agent, "the split it left focuses what remains")
    // Alone already: nothing to take it out of.
    let same = l
    l.detach(page)
    l.detach(key("local", "gone"))
    #expect(l == same)
    // Dropped below, ahead of: the side it was dropped on.
    l.moveTab(1, beside: agent, .vertical, before: true)
    #expect(l.tabs.map(\.root) == [.split(.vertical, [.leaf(page), .leaf(agent)])])
}

@Test func onlyASinglePaneTabMovesIntoASplit() {
    var l = Layout()
    l.open(agent)
    l.open(page)
    l.split(.horizontal, with: shell)
    let before = l
    l.moveTab(1, beside: agent)
    #expect(l == before, "a tab of splits is not moved whole")
    l.moveTab(0, beside: agent)
    #expect(l == before, "not onto itself")
    l.moveTab(7, beside: agent)
    l.moveTab(0, beside: key("local", "gone"))
    #expect(l == before)
    // A workbench draws its own arrangement: nothing moves in or out of one.
    var w = Layout()
    w.open(agent)
    w.open(page)
    w.tabs[0].bench = .ide
    let bench = w
    w.moveTab(1, beside: agent)
    #expect(w == bench)
    w.tabs[0].bench = nil
    w.tabs[1].bench = .ide
    let other = w
    w.moveTab(1, beside: agent)
    #expect(w == other)
}

@Test func renamingLeaves() {
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page)
    l.tabs[0].resize("", n: 2, after: 0, by: 0.1)
    l.tabs[0].hidden = [page]
    let weights = l.tabs[0].weights
    let now = browserKey("http://localhost:3000/a")
    l.rename(leaves: [page: now, key("local", "none"): "x"])
    #expect(l.tabs[0].root == .split(.horizontal, [.leaf(agent), .leaf(now)]))
    #expect(l.tabs[0].focused == now && l.tabs[0].hidden == [now])
    #expect(l.tabs[0].weights == weights, "renaming is not a new split: its sizes stay")
    // Onto a name already shown: left alone.
    l.rename(leaves: [now: agent])
    #expect(l.tabs[0].root.panes == [agent, now])
    l.rename(leaves: [:])
    #expect(l.tabs[0].root.panes == [agent, now])
}

@Test func whichLeavesTakeTheKeyboardAsATerminal() {
    // Moving between splits hands the keyboard to a terminal, or takes it
    // from one for a browser.
    #expect(leafHasTerminal(agent))
    #expect(leafHasTerminal(benchShellKey(agent)))
    #expect(!leafHasTerminal(page))
    #expect(!leafHasTerminal(browserKey("about:blank#1")))
    #expect(!leafHasTerminal(changesKey("local", "r1", "feat")))
    #expect(!leafHasTerminal(chatKey(agent)))
    // Moving the keyboard to the browser split and back.
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page)
    #expect(!leafHasTerminal(l.focused!))
    #expect(l.move(.left) && l.focused == agent && leafHasTerminal(l.focused!))
    #expect(l.move(.right) && l.focused == page)
    l.nextSplit()
    #expect(l.focused == agent)
}

@Test func splitMenu() {
    #expect(splitCommands.map(\.id) == ["splitRight", "splitDown", "splitRightBrowser", "splitDownBrowser", "splitRightAgent", "splitDownAgent"])
    for c in splitCommands { #expect(titleCase(c.title) == c.title, "\(c.title)") }
    // Every one is a command ⌘K knows, with the same shortcut.
    let palette = Dictionary(uniqueKeysWithValues: paletteCommands.map { ($0.id, $0.keys) })
    for c in splitCommands where c.id != "splitDownAgent" { #expect(palette[c.id] == c.keys, "\(c.id)") }
}

// The File menu has each split command under its title, in Title Case, and
// the tab bar's + and a split's header menu are drawn from the lists.
@Test func menusOfferSplitsWithABrowser() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    let menu = try String(contentsOfFile: dir + "/main.swift", encoding: .utf8)
    for c in splitCommands {
        #expect(menu.contains("item(\"\(c.title)\""), "File menu lacks \(c.title)")
        #expect(menu.contains("\"\(c.id)\")"), "File menu lacks \(c.id)")
    }
    #expect(menu.contains(#"item("Split Right with Browser", "d", [.command, .option], "splitRightBrowser")"#), "⌥⌘D")
    let items = try Regex(#"item\("([^"]+)""#)
    for m in menu.matches(of: items) {
        let t = String(m.output[1].substring ?? "")
        if t.contains("\\(") { continue } // a value in it: Tab \(n)
        #expect(titleCase(t) == t, "\(t)")
    }
    let views = try String(contentsOfFile: dir + "/Views.swift", encoding: .utf8)
    #expect(views.contains("ForEach(splitCommands"), "a split's menu lists splitCommands")
}
