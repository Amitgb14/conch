@testable import ConchKit
import Foundation
import Testing

// A browser lives while a layout holds its pane: closed any way, it stops
// playing; moved, hidden or in another workspace, it goes on.

private let page = browserKey("https://video.test/watch")
private let other = browserKey("https://other.test/")
private let agent = key("local", "p1")

@Test func aClosedBrowserIsShutDown() {
    var l = Layout()
    #expect(browsersClosed(made: [], layouts: []).isEmpty)
    #expect(browsersClosed(made: [page], layouts: []) == [page])
    l.open(page)
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty)
    l.close(page)
    #expect(browsersClosed(made: [page], layouts: [l]) == [page], "the only tab")

    l.open(agent)
    l.split(.horizontal, with: page)
    l.close(page)
    #expect(browsersClosed(made: [page], layouts: [l]) == [page], "a split beside an agent")
}

@Test func closingATabShutsDownEveryBrowserInItAndNoOther() {
    var l = Layout()
    let third = browserKey("https://third.test/")
    l.open(third)
    l.open(agent)
    l.split(.horizontal, with: page)
    l.split(.vertical, with: other)
    for p in l.tabs[1].root.panes { l.close(p) } // as Store.closeTab does
    #expect(browsersClosed(made: [page, other, third], layouts: [l]) == [page, other])
}

@Test func aMovedBrowserKeepsPlaying() {
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page)
    l.detach(page)
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty, "to a tab of its own")
    l.join(1, into: 0, .vertical)
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty, "back beside the agent")
    l.close(page)
    l.open(page)
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty, "out and back within one turn")
}

@Test func aBrowserInAnotherWorkspaceKeepsPlaying() {
    var parked = Layout()
    parked.open(page)
    #expect(browsersClosed(made: [page, other], layouts: [Layout(), parked]) == [other])
}

@Test func aHiddenBrowserKeepsPlaying() {
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: page)
    l.toggleHidden(page)
    #expect(l.tabs[0].hidden.contains(page))
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty)
}

@Test func aBrowserReplacedInAWorkbenchIsShutDown() {
    var l = Layout()
    l.openBench(.ide, BenchParts(files: filesKey("local", "r1"), shell: key("local", "p2"), chat: chatKey(agent), agent: agent))
    let added = l.addBenchBrowser(page)
    #expect(added)
    #expect(browsersClosed(made: [page], layouts: [l]).isEmpty)
    let replaced = l.addBenchBrowser(other)
    #expect(replaced)
    #expect(browsersClosed(made: [page, other], layouts: [l]) == [page], "in place of the one it had")
    l.removeBenchBrowser()
    #expect(browsersClosed(made: [other], layouts: [l]) == [other], "taken out of it")
}

@Test func drawingAClosedPaneMakesNoBrowser() {
    var l = Layout()
    #expect(!mayMakeBrowser(page, in: l, windowClosed: false), "no tabs")
    l.open(page)
    #expect(mayMakeBrowser(page, in: l, windowClosed: false))
    #expect(!mayMakeBrowser(other, in: l, windowClosed: false))
    #expect(!mayMakeBrowser(page, in: l, windowClosed: true), "its window went")
    l.close(page)
    #expect(!mayMakeBrowser(page, in: l, windowClosed: false), "drawn once more as it closes")
}

// The app's side has no test target, so its use of the rules is read.
@Test func theAppEndsClosedBrowsers() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp/"
    func code(_ file: String) throws -> String { try String(contentsOfFile: dir + file, encoding: .utf8) }
    let store = try code("Store.swift")
    #expect(store.contains("guard mayMakeBrowser("), "drawing cannot bring a closed browser back")
    #expect(store.contains("self?.endClosedBrowsers()"), "every layout change looks for closed browsers")
    let windows = try code("Windows+Store.swift")
    let closing = try #require(windows.components(separatedBy: "func closeWindow()").last?.components(separatedBy: "\n    }").first)
    #expect(closing.contains("endBrowsers()"), "a closed window's browsers end")
    #expect(try code("main.swift").contains("m.pauseMedia()"), "the kept first window goes quiet")
}
