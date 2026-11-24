import ConchKit
import Foundation
import Testing

// A browser pane's own tabs: opening, closing and reopening, reordering,
// the keys that reach them, zoom, and what is saved for a relaunch — an
// older window saved before a pane had tabs still opens.

@Test func aBrowserStartsWithOneTabAtItsAddress() {
    let b = BrowserTabs(address: "http://localhost:3000")
    #expect(b.tabs.map(\.address) == ["http://localhost:3000"])
    #expect(b.active?.address == "http://localhost:3000")
    #expect(b.activeIndex == 0)
    #expect(BrowserTabs(address: "about:blank#3").tabs.map(\.address) == [""], "a blank browser is a new tab")
}

@Test func openingAddsAtTheEndOrBesideItsOpener() {
    var b = BrowserTabs(address: "http://a")
    let first = b.tabs[0].id
    let t = b.open("http://b")
    #expect(b.tabs.map(\.address) == ["http://a", "http://b"])
    #expect(b.activeID == t)
    // A link's new tab goes right after the page it came from, behind it.
    b.select(first)
    let c = b.open("http://c", after: first, select: false)
    #expect(b.tabs.map(\.address) == ["http://a", "http://c", "http://b"])
    #expect(b.activeID == first, "a ⌘-click keeps the page in front")
    #expect(b.tabs.map(\.id).count == Set(b.tabs.map(\.id)).count, "ids are unique")
    #expect(c != t)
    // An opener that is gone: at the end.
    b.open("http://d", after: 999)
    #expect(b.tabs.last?.address == "http://d")
}

@Test func closingPicksTheNeighbourAndReopenPutsItBack() {
    var b = BrowserTabs(address: "http://a")
    let bID = b.open("http://b")
    b.open("http://c")
    b.select(bID)
    #expect(b.close(bID) == false, "tabs are left")
    #expect(b.active?.address == "http://c", "the one to its right takes its place")
    let c = b.activeID
    #expect(b.close(c) == false)
    #expect(b.active?.address == "http://a", "the last one closed: the one to its left")
    // ⌘⇧T: newest first, where it was.
    let back = b.reopen()
    #expect(back != nil)
    #expect(b.tabs.map(\.address) == ["http://a", "http://c"])
    b.reopen()
    #expect(b.tabs.map(\.address) == ["http://a", "http://b", "http://c"])
    #expect(b.active?.address == "http://b")
    #expect(b.reopen() == nil, "nothing more to reopen")
    // The last tab closed says so: the pane is the caller's to close.
    var one = BrowserTabs(address: "http://x")
    #expect(one.close(one.tabs[0].id) == true)
    #expect(one.tabs.isEmpty)
    #expect(one.active == nil)
    #expect(one.close(42) == true, "an unknown id changes nothing")
}

@Test func reopenRemembersAFewAndAPrivateTabNotAtAll() {
    var b = BrowserTabs(address: "http://keep")
    for i in 0..<(BrowserTabs.closedMax + 5) { b.close(b.open("http://\(i)")) }
    #expect(b.closed.count == BrowserTabs.closedMax)
    #expect(b.closed.last?.tab.address == "http://\(BrowserTabs.closedMax + 4)")
    let p = b.open("https://private.example", isPrivate: true)
    let before = b.closed
    b.close(p)
    #expect(b.closed == before, "a private tab leaves nothing to reopen")
}

@Test func tabsReorderAndSelectByNumber() {
    var b = BrowserTabs(address: "http://a")
    let bb = b.open("http://b")
    b.open("http://c")
    b.move(bb, to: 0)
    #expect(b.tabs.map(\.address) == ["http://b", "http://a", "http://c"])
    b.move(bb, to: 99)
    #expect(b.tabs.map(\.address) == ["http://a", "http://c", "http://b"], "past the end: last")
    b.move(bb, to: -3)
    #expect(b.tabs.map(\.address) == ["http://b", "http://a", "http://c"])
    b.move(12345, to: 1)
    #expect(b.tabs.map(\.address) == ["http://b", "http://a", "http://c"], "an unknown id moves nothing")
    b.select(number: 2)
    #expect(b.active?.address == "http://a")
    b.select(number: 9)
    #expect(b.active?.address == "http://c", "⌘9 is the last tab, however many")
    b.select(number: 8)
    #expect(b.active?.address == "http://c", "a number past the tabs changes nothing")
    b.select(number: 0)
    #expect(b.active?.address == "http://c")
    b.next()
    #expect(b.active?.address == "http://b", "round from the last to the first")
    b.previous()
    #expect(b.active?.address == "http://c")
}

@Test func duplicateCloseOthersAndTake() {
    var b = BrowserTabs(address: "http://a")
    let a = b.tabs[0].id
    b.setZoom(a, 1.25)
    b.open("http://b")
    let d = b.duplicate(a)
    #expect(b.tabs.map(\.address) == ["http://a", "http://a", "http://b"])
    #expect(b.tab(d!)?.zoom == 1.25, "a copy keeps its zoom")
    #expect(b.activeID == d)
    b.closeOthers(d!)
    #expect(b.tabs.map(\.id) == [d!])
    #expect(b.closed.count == 2)
    #expect(b.take(d!) == nil, "the only tab is the pane itself: nothing to move out")
    let e = b.open("http://e")
    let taken = b.take(e)
    #expect(taken?.address == "http://e")
    #expect(b.tabs.map(\.id) == [d!])
    #expect(b.closed.count == 2, "moved out, not closed: nothing to reopen")
    #expect(b.duplicate(777) == nil)
}

@Test func pagesUpdateTheirTab() {
    var b = BrowserTabs(address: "")
    let t = b.tabs[0].id
    b.update(t, address: "https://go.dev/", title: "Go")
    #expect(b.tab(t)?.address == "https://go.dev/")
    #expect(b.tab(t)?.title == "Go")
    b.update(t, title: "The Go Programming Language")
    #expect(b.tab(t)?.address == "https://go.dev/", "a title alone leaves the address")
    #expect(b.tab(t)?.label == "The Go Programming Language")
    b.update(t, title: "")
    #expect(b.tab(t)?.label == "go.dev", "no title: its host")
    b.update(t, address: "")
    #expect(b.tab(t)?.label == "New Tab")
    b.update(99, address: "http://nowhere")
    #expect(b.tabs.count == 1)
}

// MARK: zoom

@Test func zoomStepsAndLabels() {
    #expect(BrowserZoom.bigger(1) == 1.1)
    #expect(BrowserZoom.smaller(1) == 0.9)
    #expect(BrowserZoom.bigger(BrowserZoom.steps.last!) == BrowserZoom.steps.last!, "no bigger than the last step")
    #expect(BrowserZoom.smaller(BrowserZoom.steps.first!) == BrowserZoom.steps.first!)
    #expect(BrowserZoom.bigger(1.05) == 1.1, "between steps: the next one up")
    #expect(BrowserZoom.smaller(1.05) == 1)
    #expect(BrowserZoom.label(1.25) == "125%")
    #expect(BrowserZoom.label(0.666) == "67%")
    #expect(BrowserZoom.clamped(.nan) == 1)
    #expect(BrowserZoom.clamped(40) == BrowserZoom.steps.last!)
    #expect(BrowserZoom.clamped(0) == BrowserZoom.steps.first!)
    var b = BrowserTabs(address: "http://a")
    b.setZoom(b.activeID, 99)
    #expect(b.active?.zoom == BrowserZoom.steps.last!)
}

// MARK: keys

private func k(_ c: String, code: UInt16 = 0, cmd: Bool = true, shift: Bool = false, opt: Bool = false, ctrl: Bool = false) -> BrowserKey? {
    browserKeyAction(characters: c, keyCode: code, command: cmd, shift: shift, option: opt, control: ctrl)
}

@Test func browserKeys() {
    #expect(k("t") == .newTab)
    #expect(k("w") == .closeTab)
    #expect(k("t", shift: true) == .reopenTab)
    #expect(k("T", shift: true) == .reopenTab, "the shifted letter as AppKit gives it")
    #expect(k("l") == .address)
    #expect(k("r") == .reload)
    #expect(k("[") == .back)
    #expect(k("]") == .forward)
    #expect(k("1") == .tab(1))
    #expect(k("9") == .tab(9))
    #expect(k("f") == .find)
    #expect(k("g") == .findNext)
    #expect(k("g", shift: true) == .findPrevious)
    #expect(k("=") == .zoomIn)
    #expect(k("+") == .zoomIn)
    #expect(k("-") == .zoomOut)
    #expect(k("0") == .zoomReset)
    #expect(k("i", opt: true) == .inspector)
    #expect(k("ˆ", opt: true) == nil, "⌥ letters other than I stay conch's")
    #expect(k("\t", code: 48, cmd: false, ctrl: true) == .nextTab)
    #expect(k("\t", code: 48, cmd: false, shift: true, ctrl: true) == .previousTab)
}

@Test func typedLettersAndConchsOwnKeysAreNotTheBrowsers() {
    // A page's typing is the page's: no letter without ⌘ or ⌃ is taken.
    for c in ["t", "w", "c", "n", "x", "j", "k", "h", "l", "1", "[", " ", "\t"] {
        #expect(k(c, cmd: false) == nil, "\(c)")
        #expect(k(c, cmd: false, shift: true) == nil, "⇧\(c)")
    }
    #expect(k("\t", code: 48, cmd: false) == nil, "Tab moves between a page's fields")
    // What stays conch's while a page has the keyboard.
    #expect(k("]", shift: true) == nil, "⇧⌘] is conch's next tab")
    #expect(k("[", shift: true) == nil)
    #expect(k("d") == nil, "⌘D splits")
    #expect(k("d", opt: true) == nil)
    #expect(k("k") == nil, "⌘K searches")
    #expect(k("0", ctrl: true) == nil, "⌃⌘0 is the terminal's text size")
    #expect(k("+", shift: true) == nil, "⇧⌘+ is the window's text")
    #expect(k("w", shift: true) == nil)
    #expect(k("1", opt: true) == nil, "⌥⌘1 is a workbench layout")
    #expect(k("a") == nil && k("c") == nil && k("v") == nil, "editing keys are the page's own")
    #expect(k("") == nil)
}

// MARK: saved

@Test func savedTabsComeBack() {
    var b = BrowserTabs(address: "http://localhost:3000")
    b.update(b.activeID, title: "App")
    let docs = b.open("https://go.dev/doc")
    b.setZoom(docs, 1.5)
    b.open("") // a new tab never given an address is not kept
    b.open("https://secret.example", isPrivate: true) // nor is a private one
    b.select(docs)
    let s = try! #require(b.saved)
    #expect(s.tabs.map(\.address) == ["http://localhost:3000", "https://go.dev/doc"])
    #expect(s.active == 1)
    let back = BrowserTabs(saved: try! JSONDecoder().decode(SavedBrowser.self, from: JSONEncoder().encode(s)), address: "http://ignored")
    #expect(back.tabs.map(\.address) == ["http://localhost:3000", "https://go.dev/doc"])
    #expect(back.tabs.map(\.zoom) == [1, 1.5])
    #expect(back.tabs.map(\.title) == ["App", ""])
    #expect(back.active?.address == "https://go.dev/doc")
    // The address the pane is renamed to on saving.
    #expect(b.savedAddress == "https://go.dev/doc")
    // Only a blank or private tab: nothing to save.
    var blank = BrowserTabs(address: "")
    #expect(blank.saved == nil)
    blank.open("https://p.example", isPrivate: true)
    #expect(blank.saved == nil)
    #expect(blank.savedAddress == nil)
    // The active one blank: the pane is named for the first kept tab.
    var mixed = BrowserTabs(address: "http://a")
    mixed.open("")
    #expect(mixed.saved?.active == 0)
    #expect(mixed.savedAddress == "http://a")
}

@Test func savedBrowsersReadTolerantly() {
    // A saved tab with nothing but its address; zoom out of range; an
    // active index past the end; an empty list.
    let json = #"{"tabs":[{"address":"http://a"},{"address":"http://b","zoom":80}],"active":7}"#
    let s = try! JSONDecoder().decode(SavedBrowser.self, from: Data(json.utf8))
    let b = BrowserTabs(saved: s, address: "http://x")
    #expect(b.tabs.map(\.address) == ["http://a", "http://b"])
    #expect(b.tabs[1].zoom == BrowserZoom.steps.last!)
    #expect(b.activeIndex == 1)
    let none = BrowserTabs(saved: SavedBrowser(tabs: [], active: 0), address: "http://x")
    #expect(none.tabs.map(\.address) == ["http://x"], "nothing saved: the pane's own address")
    #expect(BrowserTabs(saved: nil, address: "http://y").tabs.map(\.address) == ["http://y"])
}

@Test func savedLayoutCarriesEachBrowsersTabs() {
    let agent = key("local", "p1")
    let first = browserKey("http://localhost:3000")
    let blank = browserKey("about:blank#1")
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: first)
    l.open(blank)
    var a = BrowserTabs(address: "http://localhost:3000")
    a.open("https://go.dev/")
    var b = BrowserTabs(address: "about:blank#1")
    b.update(b.activeID, address: "https://example.com/")
    b.open("https://example.org/")
    let saved = SavedLayout(l, browsers: [first: a, blank: b])
    let data = try! JSONEncoder().encode(saved)
    let back = try! JSONDecoder().decode(SavedLayout.self, from: data)
    let goKey = browserKey("https://go.dev/"), orgKey = browserKey("https://example.org/")
    #expect(back.layout().tabs.map(\.root.panes) == [[agent, goKey], [orgKey]], "each pane named for the page in front")
    #expect(back.browsers?[goKey]?.tabs.map(\.address) == ["http://localhost:3000", "https://go.dev/"])
    #expect(back.browsers?[orgKey]?.tabs.map(\.address) == ["https://example.com/", "https://example.org/"])
    // A blank pane with only blank tabs is dropped as before.
    var gone = Layout()
    gone.open(browserKey("about:blank#9"))
    #expect(SavedLayout(gone, browsers: [browserKey("about:blank#9"): BrowserTabs(address: "")]).layout().tabs.isEmpty)
}

@Test func aLayoutSavedBeforeTabsStillLoads() {
    // What an older build wrote: no browsers at all.
    let json = #"{"tabs":[{"root":{"pane":"web|http://localhost:3000/login"},"focused":"web|http://localhost:3000/login"}],"active":0}"#
    let s = try! JSONDecoder().decode(SavedLayout.self, from: Data(json.utf8))
    #expect(s.browsers == nil)
    let k = s.layout().tabs[0].root.panes[0]
    let b = BrowserTabs(saved: s.browsers?[k], address: parseBrowserKey(k)!)
    #expect(b.tabs.map(\.address) == ["http://localhost:3000/login"])
    // And one with a browsers entry this build cannot read keeps the rest.
    let odd = #"{"tabs":[{"root":{"pane":"web|http://a"},"focused":"web|http://a"}],"active":0,"browsers":{"web|http://a":{"tabs":"nonsense"}}}"#
    let o = try! JSONDecoder().decode(SavedLayout.self, from: Data(odd.utf8))
    #expect(o.layout().tabs.count == 1)
    #expect(o.browsers?["web|http://a"] == nil)
}

@Test func theFirstWindowKeepsOnlyItsBrowsers() {
    // The first window's terminals are the TUI's to bring back; its
    // browsers, beside an agent or in a tab or workbench, are kept.
    let agent = key("local", "p1"), shell = key("local", "p2")
    let a = browserKey("http://localhost:3000"), b = browserKey("https://go.dev/"), c = browserKey("https://example.com/")
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: a)
    l.open(b)
    l.split(.vertical, with: c)
    l.open(shell)
    var tabs: [String: BrowserTabs] = [:]
    for k in [a, b, c] {
        var t = BrowserTabs(address: parseBrowserKey(k)!)
        t.open(parseBrowserKey(k)! + "two")
        t.select(t.tabs[0].id)
        tabs[k] = t
    }
    let only = SavedLayout(l, browsers: tabs).onlyBrowsers
    let back = only.layout()
    #expect(back.tabs.map(\.root.panes) == [[a], [b, c]], "the agent's split and the shell's tab are left to the TUI")
    #expect(back.tabs[1].root == .split(.vertical, [.leaf(b), .leaf(c)]))
    #expect(Set(only.browsers!.keys) == [a, b, c])
    #expect(only.browsers?[a]?.tabs.count == 2)
    // Nothing but terminals: nothing kept.
    var t = Layout()
    t.open(shell)
    #expect(SavedLayout(t).onlyBrowsers.tabs.isEmpty)
    #expect(SavedLayout(t).onlyBrowsers.browsers == nil)
}
