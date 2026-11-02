import Foundation
import Testing
@testable import ConchKit

// A fixture like the TUI tests' a1Fixture: a project with a task branch,
// an agent in it, a terminal, and a pane that belongs to no project.
func fixture() -> Snapshot {
    let api = ProjectInfo(id: "r1", name: "api", path: "/src/api",
                          worktrees: [WorktreeInfo(path: "/src/api", branch: "main", main: true),
                                      WorktreeInfo(path: "/src/api-feat", branch: "feat")],
                          branches: [BranchInfo(name: "main"), BranchInfo(name: "feat", baseAhead: 2, worktree: "/src/api-feat")])
    return Snapshot(panes: [
        PaneInfo(id: "p1", name: "claude", agent: AgentStatus(name: "claude", state: .working), title: "Add health check", projectID: "r1", branch: "feat"),
        PaneInfo(id: "p2", name: "zsh", projectID: "r1"),
        PaneInfo(id: "p3", name: "bash"),
        PaneInfo(id: "p4", name: "codex", agent: AgentStatus(name: "codex", state: .blocked, since: Date(timeIntervalSince1970: 20)), projectID: "r1"),
    ], projects: [api])
}

@Test func decodesTheServersPanes() throws {
    let json = """
    {"panes":[{"id":"p1","name":"claude","command":["claude"],"cwd":"/src","cols":80,"rows":24,"pid":1,
      "state":"running","exit_code":0,"created":"2026-11-01T12:00:00.123456789-07:00",
      "agent":{"name":"claude","state":"blocked","since":"2026-11-01T12:00:00.5Z","message":"Allow?"},
      "title":"Fix it","project_id":"r1","branch":"feat","future_field":{"x":1}},
     {"id":"p2","name":"zsh","state":"exited","exit_code":3,"agent":{"name":"x","state":"something-new","since":"0001-01-01T00:00:00Z"}}]}
    """
    let list = try conchDecoder().decode(PaneList.self, from: Data(json.utf8))
    #expect(list.panes.count == 2)
    #expect(list.panes[0].agent?.state == .blocked)
    let half = ISO8601DateFormatter()
    half.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    #expect(list.panes[0].agent?.since == half.date(from: "2026-11-01T12:00:00.500Z"))
    #expect(list.panes[0].displayName == "Fix it")
    #expect(list.panes[0].projectID == "r1")
    #expect(list.panes[1].exitCode == 3 && !list.panes[1].running)
    #expect(list.panes[1].agent?.state == .unknown) // a state from a newer server is not an error
}

@Test func displayNameIsTheTitleUnlessRenamed() {
    var p = PaneInfo(id: "p1", name: "claude", agent: AgentStatus(name: "claude", state: .idle), title: "Task")
    #expect(p.displayName == "Task")
    p.customName = true
    #expect(p.displayName == "claude")
    p = PaneInfo(id: "p2", name: "zsh", title: "~/src")
    #expect(p.displayName == "zsh") // a terminal's title is its prompt, not a name
}

@Test func socketPath() {
    #expect(conchSocketPath(env: ["CONCH_SOCKET": "/s.sock"], home: "/h") == "/s.sock")
    #expect(conchSocketPath(env: ["CONCH_HOME": "/c"], home: "/h") == "/c/conch.sock")
    #expect(conchSocketPath(env: ["CONCH_HOME": ""], home: "/h") == "/h/.config/conch/conch.sock")
}

@Test func conchBinaryIsFound() {
    let has: (Set<String>) -> (String) -> Bool = { set in { set.contains($0) } }
    #expect(conchBinary(env: ["CONCH_BIN": "/x/conch"], exists: has([])) == "/x/conch")
    #expect(conchBinary(env: ["HOME": "/h", "PATH": "/usr/bin:/opt/bin"], exists: has(["/opt/bin/conch", "/h/.local/bin/conch"])) == "/h/.local/bin/conch")
    #expect(conchBinary(env: ["HOME": "/h", "PATH": "/opt/bin"], exists: has(["/opt/bin/conch"])) == "/opt/bin/conch")
    #expect(conchBinary(env: ["HOME": "/h", "PATH": ""], exists: has([])) == nil)
}

@Test func environmentLeavesThePaneOut() {
    let env = conchEnvironment(["CONCH_PANE_ID": "p3", "CONCH_SOCKET": "/s", "TERM": "dumb"])
    #expect(env["CONCH_PANE_ID"] == nil)
    #expect(env["CONCH_SOCKET"] == "/s") // the same server is the point
    #expect(env["TERM"] == "xterm-256color")
}

@Test func treeIsTheTUIsForOneMachine() {
    let tree = fixture().tree()
    #expect(tree.title == "local" && tree.kind == .machine("local"))
    #expect(tree.badge == "⠋1 ⚑1") // one working, one waiting
    let top = tree.children ?? []
    // The projects under Workspace, the CLI group beside it: the TUI's tree.
    #expect(top.map(\.title) == ["Workspace", "CLI"])
    #expect(top[0].kind == .workspace("local") && top[0].id == "ws:local" && top[0].badge == "1")
    #expect(top[0].children?.map(\.title) == ["api"])
    let api = top[0].children![0]
    #expect(api.badge == "⚑1")
    #expect(api.children?.map(\.title) == ["Agents", "Branches", "Terminals"])
    #expect(agents0(api)?.kind == .agent("local|p1"))
    let agents = api.children?[0].children ?? []
    #expect(agents.map(\.title) == ["Add health check", "codex"])
    #expect(agents.map(\.badge) == ["working", "waiting"])
    // The task branch; main is the project's own checkout.
    #expect(api.children?[1].children?.map(\.title) == ["feat"])
    #expect(api.children?[1].children?.first?.kind == .branch(project: "local|r1", name: "feat", checkedOut: true))
    #expect(api.children?[1].children?.first?.badge == "↑2")
    #expect(top[1].children?.map(\.title) == ["bash"])
}

func agents0(_ project: TreeNode) -> TreeNode? { project.children?.first?.children?.first }

/// A machine's projects: the children of its Workspace row.
func projects(_ machine: TreeNode) -> [TreeNode] {
    machine.children?.first { if case .workspace = $0.kind { return true }; return false }?.children ?? []
}

@Test func treeOfNothing() {
    let tree = Snapshot().tree()
    #expect((tree.children ?? []).isEmpty && tree.badge.isEmpty)
}

// Another machine's tree names everything with its machine, under the
// label it is shown by: its p1 is not this computer's p1.
@Test func treeOfAnotherMachine() {
    let t = fixture().tree(machine: "box", label: "busybox", sessions: ["box|r1": 16])
    #expect(t.title == "busybox" && t.id == "m:box")
    let api = projects(t).first
    #expect(t.children?.first?.kind == .workspace("box"))
    #expect(api?.kind == .project("box|r1"))
    #expect(agents0(api!)?.kind == .agent("box|p1"))
    // The saved conversations, counted, as the TUI lists them.
    #expect(api?.children?.last?.kind == .sessions("box|r1") && api?.children?.last?.badge == "16")
    #expect(projects(fixture().tree(sessions: ["local|r1": 0])).first?.children?.contains { $0.title == "Sessions" } == false)
}

@Test func keys() {
    #expect(key("box", "p3") == "box|p3")
    #expect(unkey("box|p3") == ("box", "p3"))
    #expect(unkey("p3") == ("local", "p3"))
    #expect(unkey("local|p3").machine == "local")
}

@Test func machinesFile() {
    let json = """
    {"machines": [{"id": "10-0-0-115", "label": "10.0.0.115", "target": "aghadge@10.0.0.115", "enabled": true},
                  {"id": "off", "label": "off", "enabled": false},
                  {"id": "sbx", "label": "sandbox", "target": "x", "provider": "daytona"}]}
    """
    let ms = MachineEntry.load(machinesJSON: Data(json.utf8))
    #expect(ms.map(\.id) == ["10-0-0-115", "sbx"]) // the disabled one is left out; an unknown field is fine
    #expect(MachineEntry.load(machinesJSON: nil).isEmpty)
    #expect(MachineEntry.load(machinesJSON: Data("{".utf8)).isEmpty)
}

// The branches listed: task branches first, then the most recent others
// up to the limit, never the main checkout's.
@Test func listedBranches() {
    var pr = fixture().projects[0]
    pr.branches = [BranchInfo(name: "main")] + (1...10).map { BranchInfo(name: "old-\($0)") } + [BranchInfo(name: "feat", baseAhead: 2)]
    let names = Snapshot(projects: [pr]).listedBranches(pr).map(\.name)
    #expect(names == ["feat", "old-1", "old-2", "old-3", "old-4", "old-5"])
    #expect(Snapshot(projects: [pr]).listedBranches(pr)[1].worktree == nil)
    let tree = Snapshot(projects: [pr]).tree()
    #expect(projects(tree).first?.children?.first { $0.title == "Branches" }?.badge == "12")
}

@Test func inboxAndNextAttention() {
    var s = fixture()
    #expect(s.inbox() == (1, 0))
    s.upsert(PaneInfo(id: "p5", name: "gemini", agent: AgentStatus(name: "gemini", state: .done, since: Date(timeIntervalSince1970: 5))))
    s.upsert(PaneInfo(id: "p6", name: "claude", agent: AgentStatus(name: "claude", state: .blocked, since: Date(timeIntervalSince1970: 10))))
    #expect(s.inbox() == (2, 1))
    // Waiting first, oldest first; then done; round again.
    #expect(s.nextAttention(after: nil) == "p6")
    #expect(s.nextAttention(after: "p6") == "p4")
    #expect(s.nextAttention(after: "p4") == "p5")
    #expect(s.nextAttention(after: "p5") == "p6")
    #expect(Snapshot().nextAttention(after: nil) == nil)
}

@Test func upsertAndRemove() {
    var s = fixture()
    var p = s.pane("p2")!
    p.name = "renamed"
    s.upsert(p)
    #expect(s.panes.count == 4 && s.pane("p2")?.name == "renamed")
    s.remove(pane: "p2")
    #expect(s.pane("p2") == nil)
    s.remove(project: "r1")
    #expect(s.projects.isEmpty)
    // Its panes now belong to no project the window knows: under CLI.
    // No projects, so no Workspace row either.
    #expect(s.tree().children?.map(\.title) == ["CLI"])
}

@Test func workspaces() throws {
    let ui = """
    {"spaces":[{"name":"review","projects":["r1","busybox~r9"],"panes":["local|p3"]},{}],"expanded":{}}
    """
    let ws = Workspace.load(uiJSON: Data(ui.utf8))
    #expect(ws.count == 3)
    #expect(Workspace.label(0, ws[0]) == "1" && Workspace.label(1, ws[1]) == "2 review" && Workspace.label(2, ws[2]) == "3")
    let s = fixture()
    // The first shows everything; another only what was added to it.
    #expect(s.tree(in: ws[0]).children?.count == 2)
    let review = s.tree(in: ws[1]).children ?? []
    #expect(review.map(\.title) == ["Workspace", "CLI"])
    #expect(projects(s.tree(in: ws[1])).map(\.title) == ["api"])
    #expect(review[1].children?.map(\.title) == ["bash"])
    #expect((s.tree(in: ws[2]).children ?? []).isEmpty)
    // A project of another machine is kept as the TUI writes it.
    #expect(ws[1].shows(ProjectInfo(id: "r9", name: "x", path: "/"), machine: "busybox"))
    #expect(!ws[1].shows(ProjectInfo(id: "r9", name: "x", path: "/")))
    #expect(s.inbox(in: ws[2]) == (0, 0))
    // No file, or one from before workspaces: just the first.
    #expect(Workspace.load(uiJSON: nil).count == 1)
    #expect(Workspace.load(uiJSON: Data("not json".utf8)).count == 1)
}

@Test func paletteListsWaitingFirstThenPanesPlacesCommands() {
    let items = fixture().paletteItems()
    #expect(items.first?.title == "codex" && items.first?.waiting == true)
    #expect(items.contains { $0.action == .branch(project: "local|r1", name: "feat") })
    #expect(!items.contains { $0.action == .branch(project: "local|r1", name: "main") })
    #expect(items.last?.action == .command("openTUI"))
    #expect(items.first { $0.action == .pane("local|p1") }?.detail == "api · feat")
    // Another machine's items say which machine.
    let (_, panes, _) = fixture().paletteItems(machine: "box", label: "busybox")
    #expect(panes.first { $0.action == .pane("box|p1") }?.detail == "api · feat · busybox")
}

@Test func paletteFilter() {
    let items = fixture().paletteItems()
    #expect(filterPalette(items, query: "") == items)
    let split = filterPalette(items, query: "split")
    #expect(split.map(\.title) == ["Split right", "Split down", "Close split"])
    #expect(filterPalette(items, query: "down split").map(\.title) == ["Split down"])
    #expect(filterPalette(items, query: "HEALTH").first?.title == "Add health check")
    #expect(filterPalette(items, query: "zzqq").isEmpty)
}

@Test func layoutOpensSplitsAndCloses() {
    var l = Layout()
    l.open("p1")
    l.open("p2")
    #expect(l.tabs.count == 2 && l.active == 1)
    l.open("p1") // already shown: goes there
    #expect(l.tabs.count == 2 && l.active == 0)
    l.split(.horizontal, with: "p3")
    #expect(l.current?.root == .split(.horizontal, [.leaf("p1"), .leaf("p3")]))
    #expect(l.focused == "p3")
    l.split(.horizontal, with: "p4") // the same way: a sibling, not a nested split
    #expect(l.current?.root == .split(.horizontal, [.leaf("p1"), .leaf("p3"), .leaf("p4")]))
    l.split(.vertical, with: "p5")
    #expect(l.current?.root == .split(.horizontal, [.leaf("p1"), .leaf("p3"), .split(.vertical, [.leaf("p4"), .leaf("p5")])]))
    // Splitting in a pane shown elsewhere moves it: one place only.
    l.split(.vertical, with: "p2")
    #expect(l.tabs.count == 1 && l.current!.root.panes.contains("p2"))
    l.close("p5")
    l.close("p2")
    #expect(l.current?.root == .split(.horizontal, [.leaf("p1"), .leaf("p3"), .leaf("p4")]))
    #expect(l.focused == "p4")
    l.keep(only: ["p1"])
    #expect(l.current?.root == .leaf("p1"))
    l.close("p1")
    #expect(l.tabs.isEmpty && l.current == nil && l.focused == nil)
    l.split(.horizontal, with: "p9") // nothing to split: a tab
    #expect(l.tabs.count == 1 && l.focused == "p9")
}

@Test func layoutStepsAndSelects() {
    var l = Layout()
    l.step(1) // no tabs: nothing
    for p in ["a", "b", "c"] { l.open(p) }
    l.step(1)
    #expect(l.active == 0)
    l.step(-1)
    #expect(l.active == 2)
    l.select(7)
    #expect(l.active == 2)
    l.select(1)
    l.close("b")
    #expect(l.active == 0 && l.tabs.count == 2)
    l.select(1)
    l.close("c") // the last tab, active: the one before it
    #expect(l.active == 0)
}

// Opening a task branch reuses what is there: its agent, else a terminal
// in its worktree; a second click must not start a second shell.
@Test func paneForBranch() {
    var s = fixture()
    #expect(s.paneFor(branch: "feat", project: "r1") == "p1") // its agent
    s.remove(pane: "p1")
    #expect(s.paneFor(branch: "feat", project: "r1") == nil) // nothing: start one
    s.upsert(PaneInfo(id: "p9", name: "zsh", projectID: "r1", cwd: "/src/api-feat/internal"))
    #expect(s.paneFor(branch: "feat", project: "r1") == "p9") // a terminal inside it
    s.upsert(PaneInfo(id: "p9", name: "zsh", state: "exited", projectID: "r1", cwd: "/src/api-feat"))
    #expect(s.paneFor(branch: "feat", project: "r1") == nil) // an ended one is no use
    s.upsert(PaneInfo(id: "p10", name: "zsh", projectID: "r1", cwd: "/src/api-feature"))
    #expect(s.paneFor(branch: "feat", project: "r1") == nil) // a sibling folder is not inside it
    #expect(s.paneFor(branch: "nope", project: "r1") == nil)
    #expect(s.paneFor(branch: "feat", project: "r9") == nil)
}

@Test func questionDecodes() throws {
    let json = #"{"id":"q_1","text":"Allow command?","choices":[{"choice":"1","label":"Yes","default":true},{"choice":"2","label":"No"}]}"#
    let q = try JSONDecoder().decode(Question.self, from: Data(json.utf8))
    #expect(q.choices.count == 2 && q.choices[0].default == true && q.choices[1].default == nil)
}

// The client against a server of its own: hello, a call, an error, an
// event, and the connection going.
@Test func connectionTalksNDJSON() async throws {
    let dir = try FileManager.default.url(for: .itemReplacementDirectory, in: .userDomainMask,
                                          appropriateFor: URL(fileURLWithPath: "/tmp"), create: true)
    let path = "/tmp/conch-test-\(UUID().uuidString.prefix(8)).sock"
    defer { unlink(path); try? FileManager.default.removeItem(at: dir) }
    let server = try FakeServer(path: path) { msg in
        switch msg["method"] as? String {
        case "hello": return ["result": ["version": "0.1.9", "capabilities": ["x"]]]
        case "pane.list": return ["result": ["panes": [["id": "p1", "name": "zsh", "state": "running"]]], "event": true]
        default: return ["error": ["code": "unknown_method", "message": "no such method"]]
        }
    }
    let events = EventBox()
    let c = try Connection(path: path, queue: DispatchQueue(label: "t"))
    c.onEvent = { name, data in events.add(name, data) }
    try await c.handshake()
    #expect(c.hello?.capabilities == ["x"])
    let list = try await c.call("pane.list", as: PaneList.self)
    #expect(list.panes.map(\.id) == ["p1"])
    await #expect(throws: ConchError.self) { try await c.call("nope", as: PaneList.self) }
    for _ in 0..<200 where events.count == 0 { try await Task.sleep(nanoseconds: 5_000_000) }
    #expect(events.first?.0 == "pane.updated")
    server.stop()
    await #expect(throws: (any Error).self) { try await c.call("pane.list", as: PaneList.self) }
}

final class EventBox: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [(String, Data)] = []
    func add(_ n: String, _ d: Data) { lock.lock(); items.append((n, d)); lock.unlock() }
    var count: Int { lock.lock(); defer { lock.unlock() }; return items.count }
    var first: (String, Data)? { lock.lock(); defer { lock.unlock() }; return items.first }
}

/// A unix-socket server answering each line with reply(msg); a reply with
/// "event": true also pushes a pane.updated event first.
final class FakeServer: @unchecked Sendable {
    let fd: Int32
    var client: Int32 = -1
    init(path: String, reply: @escaping @Sendable ([String: Any]) -> [String: Any]) throws {
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &addr.sun_path) { buf in
            for (i, b) in path.utf8.enumerated() { buf[i] = b }
        }
        _ = withUnsafePointer(to: &addr) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
        listen(fd, 1)
        let lfd = fd
        Thread { [weak self] in
            let c = accept(lfd, nil, nil)
            self?.client = c
            var acc = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while true {
                let n = read(c, &buf, buf.count)
                if n <= 0 { return }
                acc.append(contentsOf: buf[0..<n])
                while let nl = acc.firstIndex(of: 0x0a) {
                    let line = acc[acc.startIndex..<nl]
                    acc.removeSubrange(acc.startIndex...nl)
                    guard let msg = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else { continue }
                    var r = reply(msg)
                    if r.removeValue(forKey: "event") != nil {
                        Self.send(c, ["event": "pane.updated", "data": ["id": "p1", "name": "zsh", "state": "running"]])
                    }
                    r["id"] = msg["id"]
                    Self.send(c, r)
                }
            }
        }.start()
    }
    static func send(_ c: Int32, _ obj: [String: Any]) {
        var d = try! JSONSerialization.data(withJSONObject: obj)
        d.append(0x0a)
        _ = d.withUnsafeBytes { write(c, $0.baseAddress, $0.count) }
    }
    func stop() {
        if client >= 0 { shutdown(client, SHUT_RDWR); close(client) }
        close(fd)
    }
}

// The themes are the TUI's: the same names and palettes, conch first.
@Test func themesMatchTheTUI() throws {
    let go = try String(contentsOfFile: #filePath.replacingOccurrences(of: "macos/Tests/ConchKitTests/ConchKitTests.swift", with: "internal/tui/theme.go"), encoding: .utf8)
    let tui = go.split(separator: "\n").compactMap { line -> (String, String, String)? in
        guard let n = line.range(of: "{name: \""), let a = line.range(of: "accent: \"") else { return nil }
        let name = line[n.upperBound...].prefix { $0 != "\"" }
        let accent = line[a.upperBound...].prefix { $0 != "\"" }
        let bgLine = go[go.range(of: String(line))!.upperBound...].split(separator: "\n").first ?? ""
        let bg = bgLine.range(of: "bg: \"").map { bgLine[$0.upperBound...].prefix { $0 != "\"" } } ?? ""
        return (String(name), String(accent), String(bg))
    }
    #expect(tui.count == 15)
    for (i, (name, accent, bg)) in tui.enumerated() {
        let t = ConchTheme.all[i]
        #expect(t.id == name && t.accent.hex == accent.uppercased() && t.bg.hex == bg.uppercased(), "theme \(name)")
    }
    #expect(ConchTheme.all.filter(\.light).map(\.id) == ["github-light", "solarized-light", "catppuccin-latte"])
    #expect(Set(ConchTheme.all.map(\.id)).count == ConchTheme.all.count)
}

@Test func themeNamedAndConfigured() {
    #expect(ConchTheme.named("Nord").id == "nord")
    #expect(ConchTheme.named(" tokyo-night ").id == "tokyo-night")
    #expect(ConchTheme.named("no-such").id == "conch")
    #expect(ConchTheme.named(nil).id == "conch")
    let toml = """
    [keys]
    theme = "not this one"
    [ui]
      mouse = true
      theme = "catppuccin"   # a comment
      accent = ""
    [notify]
    theme = "nor this"
    """
    #expect(ConchTheme.configured(in: toml) == "catppuccin")
    #expect(ConchTheme.configured(in: "[ui]\ntheme='nord'\n") == "nord")
    #expect(ConchTheme.configured(in: "[ui]\ntheme = \"\"\n") == nil)
    #expect(ConchTheme.configured(in: "[ui]\nthemes = \"x\"\n") == nil)
    #expect(ConchTheme.configured(in: "[ui\ntheme =") == nil) // broken: no theme, not a crash
    #expect(ConchTheme.configured(in: nil) == nil)
}

@Test func derivedColours() {
    let c = ConchTheme.conch
    // The toolbar is the measured title bar: #15191E lifted 5%.
    #expect(c.chrome.hex == "#212529") // the TUI's lighten(bg, 0.05): the title bar measured #20242A
    #expect(c.line.hex == "#2AA5B5")
    let light = ConchTheme.named("github-light")
    #expect(light.chrome.luminance < light.bg.luminance) // a light window is taken down, not up
    #expect(light.well.hex == "#FFFFFF")
    // Text on a warn pill reads: dark on a bright warn, light on a dark one.
    #expect(ConchTheme.named("dracula").onWarn.luminance < 0.3)
    #expect(light.onWarn.hex == "#FFFFFF")
    #expect(RGB("#000000").mix(RGB("#FFFFFF"), 0.5).hex == "#808080")
    #expect(c.ghosttyColors.contains("background = #15191E"))
}

@Test func changesDecode() throws {
    let json = """
    {"project_id":"r1","branch":"feat/login","base":"main","worktree":"/src/api-login","watched":true,
     "files":[{"path":"internal/auth/login.go","code":"M","added":48,"deleted":6},
              {"path":"web/new.tsx","code":"?","added":9,"deleted":0,"unstaged":true},
              {"path":"logo.png","code":"A","added":0,"deleted":0,"binary":true}],
     "commits":[{"hash":"9f3c1a2","subject":"rate limit login","author":"a","time":"2026-11-01T12:00:00Z"}],
     "committed_files":[{"path":"internal/auth/session.go","code":"M","added":12,"deleted":2}]}
    """
    let c = try conchDecoder().decode(Changes.self, from: Data(json.utf8))
    #expect(c.files.count == 3 && c.files[2].binary == true && c.worktree == "/src/api-login")
    #expect(c.totals == (57, 6))
    #expect(c.commits?.first?.subject == "rate limit login")
    #expect(c.committedFiles?.first?.path == "internal/auth/session.go")
    // An older server's answer, with no commits or worktree, still reads.
    let old = try conchDecoder().decode(Changes.self, from: Data(#"{"project_id":"r1","branch":"x","base":"main","files":[]}"#.utf8))
    #expect(old.commits == nil && old.files.isEmpty)
}

@Test func diffParses() {
    let diff = """
    diff --git a/a.go b/a.go
    index 1..2 100644
    --- a/a.go
    +++ b/a.go
    @@ -10,4 +10,5 @@ func Login() {
     	var req loginRequest
    -	old()
    +	new()
    +	more()
     	return
    \\ No newline at end of file
    """
    let lines = parseDiff(diff)
    #expect(lines.prefix(4).allSatisfy { $0.kind == .meta })
    #expect(lines[4].kind == .hunk)
    #expect(lines[5] == DiffLine(id: 5, kind: .context, text: "\tvar req loginRequest", old: 10, new: 10))
    #expect(lines[6].kind == .removed && lines[6].old == 11 && lines[6].text == "\told()")
    #expect(lines[7].kind == .added && lines[7].new == 11)
    #expect(lines[8].kind == .added && lines[8].new == 12)
    #expect(lines[9].kind == .context && lines[9].old == 12 && lines[9].new == 13)
    #expect(lines[10].kind == .meta)
    #expect(parseDiff("").isEmpty)
    // Two files: the second's header is not a line of the first's hunk.
    let two = parseDiff("@@ -1 +1 @@\n-a\n+b\ndiff --git a/c b/c\n--- a/c\n+++ b/c\n@@ -1 +1 @@\n-x\n+y\n")
    #expect(two.map(\.kind) == [.hunk, .removed, .added, .meta, .meta, .meta, .hunk, .removed, .added])
}

@Test func changesKeys() {
    let k = changesKey("box", "r1", "feat/login|x")
    #expect(parseChangesKey(k)! == ("box", "r1", "feat/login|x"))
    #expect(parseChangesKey("box|p3") == nil)
    #expect(parseChangesKey("changes|box|r1|") == nil)
}

// Rows fold as the TUI folds them: its saved state by its ids, its
// defaults where nothing is saved, and a choice made here over both.
@Test func foldsFollowTheTUI() {
    let s = fixture()
    let t = s.tree()
    #expect(t.fold == "m:local")
    #expect(t.children![0].fold == "m:local/workspace" && t.children![0].foldDefault)
    let api = projects(t)[0]
    #expect(api.fold == "p:r1" && api.foldDefault) // a project with panes starts open
    #expect(api.children!.map(\.fold) == ["p:r1/agents", "p:r1/branches", "p:r1/terminals"])
    #expect(t.children![1].fold == "m:local/cli")
    #expect(projects(Snapshot(projects: s.projects).tree())[0].foldDefault == false) // none: folded
    #expect(projects(s.tree(machine: "box"))[0].fold == "p:box~r1")

    let tui = loadFolds(uiJSON: Data(#"{"expanded":{"p:r1/terminals":false,"m:local":true},"tabs":[]}"#.utf8))
    let terminals = api.children![2]
    #expect(!isOpen(terminals, app: [:], tui: tui))
    #expect(isOpen(terminals, app: ["p:r1/terminals": true], tui: tui))
    #expect(isOpen(api.children![0], app: [:], tui: tui)) // nothing saved: the default
    #expect(loadFolds(uiJSON: nil).isEmpty && loadFolds(uiJSON: Data("x".utf8)).isEmpty)
}

@Test func sandboxes() {
    #expect(MachineEntry(id: "s1", label: "sandbox", target: "daytona:abc123").sandboxProvider == "daytona")
    #expect(MachineEntry(id: "s1", label: "sandbox", target: "daytona:abc123").displayLabel == "sandbox · daytona")
    #expect(MachineEntry(id: "b", label: "busybox", target: "aghadge@10.0.0.115").sandboxProvider == nil)
    #expect(MachineEntry(id: "b", label: "box", target: "unknown:x").sandboxProvider == nil)
    #expect(MachineEntry(id: "b", label: "box", target: "daytona:").sandboxProvider == nil)
    #expect(MachineEntry(id: "b", label: "box", target: "boat:a/b").sandboxProvider == nil)
    #expect(MachineEntry(id: "b", label: "box").displayLabel == "box")
}


// Each agent gets the TUI's marks: its Nerd Font glyph and dot, its own
// colour, and an SF Symbol of the same shape for a Mac with no Nerd Font;
// an agent conch has no mark for still gets one, so the row reads as one.
@Test func agentMarks() {
    #expect(AgentMark.of("claude") == AgentMark(glyph: "\u{f069}", dot: "🟠", symbol: "asterisk", color: RGB("#D97757")))
    #expect(AgentMark.of("claude").glyphNeedsNerdFont)
    #expect(AgentMark.of("pi").glyph == "π" && !AgentMark.of("pi").glyphNeedsNerdFont)
    let names = ["claude", "codex", "gemini", "opencode", "devin", "aider", "amp", "cursor", "grok", "kilo", "pi"]
    let marks = names.map { AgentMark.of($0) }
    #expect(Set(marks.map(\.glyph)).count == names.count && Set(marks.map(\.dot)).count == names.count) // no two alike
    #expect(marks.allSatisfy { $0.color != nil })
    // Font Awesome 4.7's range, which a v2 Nerd Font still has.
    #expect(marks.filter(\.glyphNeedsNerdFont).allSatisfy { $0.glyph.unicodeScalars.allSatisfy { (0xF000...0xF2FF).contains($0.value) } })
    let other = AgentMark.of("mine")
    #expect(other.glyph == "\u{f2db}" && other.dot == "🤖" && other.symbol == "cpu" && other.color == nil)
    #expect(AgentMark.of(nil) == other)
}

// [ui] icons as the TUI reads it: nerd, off, else text.
@Test func iconModeFromConfig() {
    #expect(IconMode(setting: uiSetting("icons", in: "[ui]\ntheme = \"nord\"\n  icons = \"nerd\"\n")) == .nerd)
    #expect(IconMode(setting: " OFF ") == .off)
    #expect(IconMode(setting: nil) == .text && IconMode(setting: "emoji") == .text)
    // Only [ui]'s key: another table's, or a longer key, is not it.
    #expect(uiSetting("icons", in: "[files]\nicons = \"nerd\"\n") == nil)
    #expect(uiSetting("icons", in: "[ui]\nicons_size = \"x\"\n") == nil)
    #expect(uiSetting("icons", in: nil) == nil)
}
