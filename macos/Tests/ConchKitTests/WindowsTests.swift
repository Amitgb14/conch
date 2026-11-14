import Foundation
import Testing
@testable import ConchKit

// Conch.app's windows (#194): what each one is scoped to, what is saved
// so they come back, which window holds a pane, and where a notification
// opens one.

@Test func projectScopeShowsOnlyItsProject() {
    let pr = ProjectInfo(id: "r1", name: "api", path: "/src/api")
    let other = ProjectInfo(id: "r2", name: "web", path: "/src/web")
    let ws = projectWorkspace(key("local", "r1"), name: "api")
    #expect(ws.name == "api")
    #expect(!ws.first, "never the first: that one shows whatever no other workspace has")
    #expect(ws.shows(pr))
    #expect(!ws.shows(other))
    #expect(!ws.shows(pr, machine: "busybox"), "the same id on another machine is another project")
    let remote = projectWorkspace(key("busybox", "r1"), name: "api")
    #expect(remote.shows(pr, machine: "busybox"))
    #expect(!remote.shows(pr))
    #expect(ws.id.hasPrefix(projectScopePrefix), "an id no ui.json workspace has")
    #expect(isProjectScope(ws.id))
    #expect(!isProjectScope("w1") && !isProjectScope("first"))
}

@Test func layoutSavesAndComesBack() {
    var l = Layout()
    l.open(key("local", "p1"))
    l.split(.horizontal, with: key("busybox", "p2"))
    l.split(.vertical, with: changesKey("local", "r1", "feat"))
    l.tabs[0].name = "work"
    l.tabs[0].resize("", n: 2, after: 0, by: 0.1)
    l.open(key("local", "p3"))
    l.select(0)
    let saved = SavedLayout(l)
    let data = try! JSONEncoder().encode(saved)
    let back = try! JSONDecoder().decode(SavedLayout.self, from: data)
    #expect(back == saved)
    let r = back.layout()
    #expect(r.tabs.count == 2)
    #expect(r.active == 0)
    #expect(r.tabs[0].root == l.tabs[0].root)
    #expect(r.tabs[0].focused == l.tabs[0].focused)
    #expect(r.tabs[0].name == "work")
    #expect(r.tabs[0].weights == l.tabs[0].weights, "split sizes come back too")
    #expect(r.tabs[1].root == .leaf(key("local", "p3")))
}

@Test func savedLayoutDropsWhatCannotComeBack() {
    // A browser's page and a workbench's own shell live in the app alone:
    // gone with it, so their splits are not brought back empty.
    var l = Layout()
    l.open(browserKey("http://localhost:3000"))
    l.open(key("local", "p1"))
    l.split(.horizontal, with: benchShellKey(key("local", "p1")))
    l.select(1)
    let r = SavedLayout(l).layout()
    #expect(r.tabs.count == 1)
    #expect(r.tabs[0].root == .leaf(key("local", "p1")))
    #expect(r.tabs[0].focused == key("local", "p1"), "focus moves off a split that did not come back")
    #expect(r.active == 0, "the active tab is found again after one before it went")
    #expect(SavedLayout(Layout()).layout() == Layout())
    var only = Layout()
    only.open(browserKey("http://x"))
    #expect(SavedLayout(only).layout().tabs.isEmpty)
}

@Test func savedWindowsLoadTolerantly() {
    var l = Layout()
    l.open(key("local", "p1"))
    let ws = [SavedWindow(id: "w-a", workspace: "w1", project: nil, frame: "0 0 800 600 0 0 1440 900", layout: SavedLayout(l)),
              SavedWindow(id: "w-b", workspace: "first", project: key("busybox", "r2"), frame: nil, layout: SavedLayout(Layout()))]
    let data = encodeSavedWindows(ws)
    #expect(loadSavedWindows(data) == ws)
    #expect(loadSavedWindows(nil).isEmpty)
    #expect(loadSavedWindows(Data()).isEmpty)
    #expect(loadSavedWindows(Data("{not json".utf8)).isEmpty)
    #expect(loadSavedWindows(Data("[]".utf8)).isEmpty)
    // From a later build with more to say: what is known still loads.
    let later = Data(#"[{"id":"w-c","workspace":"w2","layout":{"tabs":[],"active":0},"sidebar":false}]"#.utf8)
    #expect(loadSavedWindows(later).map(\.id) == ["w-c"])
    // A tab whose tree a later build draws differently is dropped, not the window.
    let odd = Data(#"[{"id":"w-d","workspace":"first","layout":{"active":0,"tabs":[{"root":{"grid":[]},"focused":"x"},{"root":{"pane":"local|p1"},"focused":"local|p1"}]}}]"#.utf8)
    let w = loadSavedWindows(odd)
    #expect(w.count == 1)
    #expect(w.first?.layout.layout().tabs.map(\.root) == [.leaf(key("local", "p1"))])
}

@Test func windowHoldingAPane() {
    var a = Layout(), b = Layout()
    a.open(key("local", "p1"))
    b.open(key("local", "p2"))
    b.split(.horizontal, with: key("busybox", "p1"))
    let shown = [(id: "main", layout: a), (id: "w2", layout: b)]
    #expect(windowHolding(key("local", "p1"), in: shown) == "main")
    #expect(windowHolding(key("busybox", "p1"), in: shown) == "w2")
    #expect(windowHolding(key("local", "p1"), in: shown, except: "main") == nil, "the asking window is not another")
    #expect(windowHolding(key("local", "p9"), in: shown) == nil)
    #expect(windowHolding(key("local", "p1"), in: []) == nil)
}

@Test func notificationOpensInTheRightWindow() {
    let api = ProjectInfo(id: "r1", name: "api", path: "/a")
    let web = ProjectInfo(id: "r2", name: "web", path: "/w")
    let main = Workspace(name: "", first: true)
    var second = Workspace(id: "w1", name: "web", projects: ["r2"])
    second.first = false
    var held = Layout()
    held.open(key("local", "p7"))
    let windows = [
        WindowPlace(id: "main", workspace: main, layout: Layout()),
        WindowPlace(id: "w-web", workspace: second, layout: Layout()),
        WindowPlace(id: "w-api", workspace: projectWorkspace(key("local", "r1"), name: "api"), layout: held),
    ]
    func pane(_ id: String, _ project: String?) -> PaneInfo { PaneInfo(id: id, name: id, projectID: project) }
    let snap = Snapshot(panes: [pane("p1", "r1"), pane("p2", "r2"), pane("p3", nil), pane("p7", nil)], projects: [api, web])
    func at(_ id: String) -> String? { windowFor(pane: key("local", id), snapshot: snap, in: windows, key: "w-web") }
    #expect(at("p7") == "w-api", "the window showing it wins, whatever its scope")
    #expect(at("p1") == "w-api", "then a window scoped to its project")
    #expect(at("p2") == "w-web", "then a window whose workspace shows it, the key one first")
    #expect(windowFor(pane: key("local", "p2"), snapshot: snap, in: windows, key: "main") == "main")
    #expect(windowFor(pane: key("local", "p2"), snapshot: snap, in: windows, key: "w-api") == "main",
            "the key window does not show it: the first that does")
    #expect(at("p3") == "main", "a pane of no project: the workspace that has it")
    #expect(windowFor(pane: key("busybox", "p1"), snapshot: Snapshot(), in: windows, key: "w-api") == "w-api",
            "a pane nobody knows of yet: the key window")
    #expect(windowFor(pane: key("local", "p1"), snapshot: snap, in: [], key: nil) == nil)
    #expect(windowFor(pane: key("local", "p3"), snapshot: snap, in: Array(windows.dropFirst(2)), key: nil) == "w-api",
            "nothing shows it: the first window, rather than none")
}

@Test func windowsCycle() {
    #expect(nextWindow(after: "a", in: ["a", "b", "c"]) == "b")
    #expect(nextWindow(after: "c", in: ["a", "b", "c"]) == "a", "round to the first")
    #expect(nextWindow(after: "c", in: ["a", "b", "c"], back: true) == "b")
    #expect(nextWindow(after: "a", in: ["a", "b", "c"], back: true) == "c")
    #expect(nextWindow(after: "a", in: ["a"]) == nil, "one window: nowhere to go")
    #expect(nextWindow(after: nil, in: ["a", "b"]) == "a", "none key: the first")
    #expect(nextWindow(after: "gone", in: ["a", "b"]) == "a")
    #expect(nextWindow(after: nil, in: []) == nil)
}

@Test func projectRowOpensInNewWindow() {
    let s = fixture()
    let api = projects(s.tree(machine: "box"))[0]
    let pm = s.menu(for: api, machine: "box")
    let i = pm.firstIndex { $0.action == .openWindow(project: "box|r1") }
    #expect(i != nil)
    #expect(i.map { pm[$0].title } == "Open in New Window")
    #expect(i.map { pm[$0 - 1] } == RowMenuItem("", .divider), "set apart from starting things")
}
