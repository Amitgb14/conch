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
    #expect(top[0].kind == .workspace("local") && top[0].id == "ws:local" && top[0].badge == "1" && top[0].fold == "m:local/workspace")
    #expect(top[0].children?.map(\.title) == ["api"])
    let api = top[0].children![0]
    #expect(api.badge == "⠋1 ⚑1") // working and waiting, as the TUI counts them
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

/// A machine's projects.
func projects(_ machine: TreeNode) -> [TreeNode] {
    (machine.children?.first { if case .workspace = $0.kind { return true }; return false }?.children ?? [])
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
    // Each shows only what it has: review claims api and bash, so the first
    // — which has whatever no later one claims — has none of it now.
    #expect(ws[0].first && ws[0].id == "first" && ws[1].id == "w1" && ws[2].id == "w2")
    #expect((s.tree(in: ws[0]).children ?? []).isEmpty)
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
    #expect(tui.count == ConchTheme.all.count)
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








// A row's name and its right side share the width as the TUI's do: a long
// name cannot squeeze a branch away, nor a long branch the name.
@Test func rowWidthsShare() {
    #expect(rowWidths(name: 50, right: 40, room: 200) == (160, 40)) // both fit
    #expect(rowWidths(name: 300, right: 120, room: 200) == (120, 80)) // both cut: the right keeps 40%
    #expect(rowWidths(name: 300, right: 30, room: 200) == (170, 30)) // a short right side stays whole
    #expect(rowWidths(name: 100, right: 150, room: 200) == (100, 100)) // the name whole, the right what is left
    #expect(rowWidths(name: 10, right: 10, room: -5) == (0, 0)) // no room
}

// The app's + adds a workspace to ui.json as the TUI's does, leaving the
// rest of the file alone, so a running TUI merges it in by its id.
@Test func addingAWorkspace() throws {
    let ui = #"{"spaces":[{"name":"review","projects":["r1"]},{"id":"sabc","name":"x"}],"expanded":{"m:local":true},"tabs":[{"name":"t"}]}"#
    let out = try Workspace.adding(id: "snew", to: Data(ui.utf8))
    let obj = try #require(try JSONSerialization.jsonObject(with: out) as? [String: Any])
    let spaces = try #require(obj["spaces"] as? [[String: Any]])
    #expect(spaces.map { $0["id"] as? String } == ["w1", "sabc", "snew"]) // older ones named by place, as the TUI does
    #expect(spaces[0]["projects"] as? [String] == ["r1"] && spaces[2]["name"] == nil)
    #expect((obj["expanded"] as? [String: Bool]) == ["m:local": true] && (obj["tabs"] as? [Any])?.count == 1)
    let ws = Workspace.load(uiJSON: out)
    #expect(ws.count == 4 && ws[3].name == "" && ws[3].projects == [])
    // No file yet: just the one.
    #expect(Workspace.load(uiJSON: try Workspace.adding(name: "mine", to: nil)).map(\.name) == ["", "mine"])
    #expect(Workspace.load(uiJSON: try Workspace.adding(to: Data())).count == 2)
    // Not conch's: left alone.
    #expect(throws: ConchError.self) { try Workspace.adding(to: Data("[1,2]".utf8)) }
    #expect(throws: (any Error).self) { try Workspace.adding(to: Data("{half".utf8)) }
    let id = Workspace.newID()
    #expect(id.count == 13 && id.hasPrefix("s") && id != Workspace.newID())
}

// A row's right-click menu offers what the TUI's row menu does, for what
// the app can do itself, named by machine.
@Test func rowMenus() {
    var s = fixture()
    var p1 = s.pane("p1")!
    p1.cwd = "/src/api-feat"
    s.upsert(p1)
    let t = s.tree(machine: "box")
    let api = projects(t)[0]
    let titles = { (items: [RowMenuItem]) in items.map(\.title) }

    // An agent: open, rename, prompt, start beside it, its branch, close.
    let agent = api.children![0].children![0]
    let am = s.menu(for: agent, machine: "box")
    #expect(titles(am) == ["Open", "Rename…", "Prompt…", "", "Start an agent here", "New terminal here", "View changes on feat", "", "Close agent…"])
    #expect(am[4].action == .startAgent(machine: "box", cwd: "/src/api-feat", inProject: true) && am[4].picksAgent)
    #expect(am[6].action == .changes(project: "box|r1", branch: "feat"))
    #expect(am.last?.action == .close(pane: "box|p1"))
    // A terminal: no prompt, a plain Close; one in no project starts things outside any.
    let cli = t.children!.first { $0.kind == .cli("box") }!
    let bm = s.menu(for: cli.children![0], machine: "box")
    #expect(!titles(bm).contains("Prompt…") && bm.last?.title == "Close")
    #expect(bm.first { $0.picksAgent }?.action == .startAgent(machine: "box", cwd: nil, inProject: false))
    // An exited agent is not prompted, and closes without asking.
    var gone = s.pane("p4")!
    gone.state = "exited"
    s.upsert(gone)
    let gm = s.menu(for: TreeNode(id: "pane:box|p4", kind: .agent("box|p4"), title: "codex"), machine: "box")
    #expect(!titles(gm).contains("Prompt…") && gm.last?.title == "Close")

    // A branch checked out: start there; one that is not: a task's to make.
    let feat = api.children![1].children![0]
    let fm = s.menu(for: feat, machine: "box")
    #expect(titles(fm) == ["View changes", "Start an agent on this branch", "Open terminal on this branch", "New task in project…", "Show in commit graph", "", "Copy branch name"])
    #expect(fm[4].action == .commits(project: "box|r1", branch: "feat"))
    #expect(fm[1].action == .startAgent(machine: "box", cwd: "/src/api-feat", inProject: true))
    let old = TreeNode(id: "b:box|r1:old", kind: .branch(project: "box|r1", name: "old", checkedOut: false), title: "old")
    #expect(titles(s.menu(for: old, machine: "box")) == ["View changes", "New task in project…", "Show in commit graph", "", "Copy branch name"])

    // A project, a machine, the Workspace row.
    let pm = s.menu(for: api, machine: "box")
    #expect(pm[0].action == .newTask(project: "box|r1") && pm[1].action == .startAgent(machine: "box", cwd: "/src/api", inProject: true))
    #expect(pm.last?.action == .copy("/src/api"))
    #expect(pm.contains { $0.action == .commits(project: "box|r1", branch: nil) })
    // A folder that is not a repository has no commits to graph.
    var plain = s
    var np = s.project("r1")!
    np.git = false
    plain.upsert(np)
    #expect(!plain.menu(for: api, machine: "box").contains { if case .commits = $0.action { return true }; return false })
    #expect(s.menu(for: t, machine: "box").map(\.action) == [.startAgent(machine: "box", cwd: nil, inProject: false), .newTerminal(machine: "box", cwd: nil, inProject: false),
                                                             .divider, .addProject(machine: "box"), .addMachine])
    // This computer also offers ssh to a host, as the TUI's H; its CLI group too.
    let local = s.tree()
    #expect(s.menu(for: local, machine: "local").map(\.action).contains(.sshToHost))
    #expect(s.menu(for: local.children!.first { $0.kind == .cli("local") }!, machine: "local").map(\.action).contains(.sshToHost))
    #expect(!s.menu(for: t.children!.first { $0.kind == .cli("box") }!, machine: "box").map(\.action).contains(.sshToHost))
    // Offline: nothing that starts anything; a pane can still be opened or closed.
    #expect(s.menu(for: t, machine: "box", machineOnline: false).map(\.action) == [.addMachine]) // adding another needs no connection here
    #expect(s.menu(for: api, machine: "box", machineOnline: false).isEmpty)
    #expect(titles(s.menu(for: agent, machine: "box", machineOnline: false)) == ["Open", "Rename…", "Prompt…", "View changes on feat", "", "Close agent…"])
    // A section has none, and a pane gone from the snapshot none either.
    #expect(s.menu(for: api.children![0], machine: "box").isEmpty)
    #expect(s.menu(for: TreeNode(id: "x", kind: .agent("box|p99"), title: "x"), machine: "box").isEmpty)
}

// Every row knows its machine, which is where its menu acts.
@Test func rowMachines() {
    let t = fixture().tree(machine: "box")
    var all: [TreeNode] = []
    func walk(_ n: TreeNode) { all.append(n); n.children?.forEach(walk) }
    walk(t)
    #expect(all.count > 8 && all.allSatisfy { $0.machine == "box" })
    #expect(AgentAvailability(name: "claude", label: "Claude Code").title == "Claude Code" && AgentAvailability(name: "pi").title == "pi")
    let st = try? conchDecoder().decode(AgentStatusList.self, from: Data(#"{"agents":[{"name":"claude","label":"Claude Code","installed":true,"tier":"supported"},{"name":"amp","installed":false}]}"#.utf8))
    #expect(st?.agents?.filter(\.installed).map(\.name) == ["claude"])
}

// A terminal's header shows its checkout's port: the worktree its folder
// is in, the deepest; none for a checkout given no port.
@Test func portOfAPane() {
    var s = fixture()
    s.projects[0].worktrees = [WorktreeInfo(path: "/src/api", branch: "main", main: true, port: 4310),
                               WorktreeInfo(path: "/src/api/nested", branch: "feat", port: 4320),
                               WorktreeInfo(path: "/src/other", branch: "x")]
    var p = PaneInfo(id: "p9", name: "dev-server", projectID: "r1", cwd: "/src/api/cmd")
    #expect(s.port(of: p) == 4310)
    p.cwd = "/src/api/nested/web"
    #expect(s.port(of: p) == 4320)
    p.cwd = "/src/api-other" // a prefix of the name is not inside it
    #expect(s.port(of: p) == nil)
    p.cwd = "/src/other"
    #expect(s.port(of: p) == nil) // no port given yet
    p.cwd = nil
    #expect(s.port(of: p) == nil)
    let wt = try? conchDecoder().decode(WorktreeInfo.self, from: Data(#"{"path":"/x","branch":"b","port":4317}"#.utf8))
    #expect(wt?.port == 4317)
}

// A folded machine lists its agents once each, in order, running ones only.
@Test func foldedMachineAgents() {
    var s = fixture()
    #expect(s.agentNames() == ["claude", "codex"])
    s.upsert(PaneInfo(id: "p7", name: "claude", agent: AgentStatus(name: "claude", state: .idle)))
    s.upsert(PaneInfo(id: "p8", name: "gemini", state: "exited", agent: AgentStatus(name: "gemini", state: .done)))
    #expect(s.agentNames() == ["claude", "codex"])
    let review = Workspace(name: "r", projects: [], panes: ["local|p4"])
    #expect(s.agentNames(in: review) == ["codex"] && Snapshot().agentNames().isEmpty)
}

// Files found become palette items that say where they are, and open on
// their own machine.
@Test func filesInThePalette() throws {
    let found = try conchDecoder().decode(FindResult.self, from: Data(#"{"root":"/src/api","paths":["internal/http/health.go","README.md"],"truncated":true}"#.utf8))
    let items = fileItems(found, machine: "box", project: "api", label: "busybox")
    #expect(items.map(\.title) == ["health.go", "README.md"])
    #expect(items.map(\.detail) == ["api · internal/http · busybox", "api · busybox"])
    #expect(items[0].action == .file(machine: "box", root: "/src/api", path: "internal/http/health.go") && items[0].kind == "file")
    #expect(fileItems(FindResult(root: "/x", paths: nil), machine: "local", project: "x").isEmpty)
}

@Test func sidebarWidths() {
    #expect(clampSidebar(300) == 300 && clampSidebar(10) == 200 && clampSidebar(5000) == 560 && clampSidebar(.nan) == sidebarDefault && sidebarDefault == 260 && sidebarRange.contains(sidebarDefault))
}

// conch's [ui] settings as the app reads them: a key of [ui] and no other.
@Test func uiSettings() {
    #expect(uiSetting("icons", in: "[ui]\ntheme = \"nord\"\n  icons = \"nerd\"\n") == "nerd")
    #expect(uiSetting("icons", in: "[files]\nicons = \"nerd\"\n") == nil)
    #expect(uiSetting("icons", in: "[ui]\nicons_size = \"x\"\n") == nil)
    #expect(uiSetting("icons", in: nil) == nil)
}

// An agent's tokens come off the wire, missing fields as zero.
@Test func tokensDecode() {
    let a = try? conchDecoder().decode(AgentStatus.self, from: Data(#"{"name":"claude","state":"idle","tokens":{"output":12,"cost_usd":1.5}}"#.utf8))
    #expect(a?.tokens == Tokens(output: 12, costUSD: 1.5))
}

// Each agent's dot is its own colour; one conch has none for is muted.
@Test func agentColors() {
    #expect(agentColor("claude") == RGB("#D97757") && agentColor("mine") == nil && agentColor(nil) == nil)
    let names = ["claude", "codex", "gemini", "opencode", "devin", "aider", "amp", "cursor", "grok", "kilo", "pi"]
    #expect(Set(names.compactMap { agentColor($0)?.hex }).count == names.count)
}

// The machine and project rows count working, waiting and done.
@Test func treeCounts() {
    var s = fixture()
    s.upsert(PaneInfo(id: "p9", name: "gemini", agent: AgentStatus(name: "gemini", state: .done), projectID: "r1"))
    let t = s.tree()
    #expect(t.badge == "⠋1 ⚑1 ✓1" && projects(t)[0].badge == "⠋1 ⚑1 ✓1")
}

// A tab's panes are laid out as the mock lays them out.
@Test func arrangedSplits() {
    #expect(SplitNode.arranged([]) == nil)
    #expect(SplitNode.arranged(["a"]) == .leaf("a"))
    #expect(SplitNode.arranged(["a", "b"]) == .split(.horizontal, [.leaf("a"), .leaf("b")]))
    #expect(SplitNode.arranged(["a", "b", "c"]) == .split(.horizontal, [.leaf("a"), .split(.vertical, [.leaf("b"), .leaf("c")])]))
    #expect(SplitNode.arranged(["a", "b", "c", "d", "e"]) == .split(.horizontal, [.split(.vertical, [.leaf("a"), .leaf("b"), .leaf("c")]),
                                                                                     .split(.vertical, [.leaf("d"), .leaf("e")])]))
    #expect(SplitNode.arranged(["a", "b", "c", "d", "e"])?.panes == ["a", "b", "c", "d", "e"])
}

// The window opens on the TUI's tabs: only live panes, none twice, the
// TUI's active tab shown and its keyboard's pane focused in its tab.
@Test func adoptTheTUIsTabs() throws {
    let json = #"{"client":"tui","focus":"p3","tabs":[{"name":"one","panes":["p1"]},{"panes":["p2","p3","p9"],"active":true},{"panes":["p9"]},{"panes":["p1","p4"]}],"at":"2026-11-01T12:00:00Z"}"#
    let v = try conchDecoder().decode(ViewState.self, from: Data(json.utf8))
    var l = Layout()
    l.adopt(v, machine: "local", alive: ["local|p1", "local|p2", "local|p3", "local|p4"])
    #expect(l.tabs.map(\.root.panes) == [["local|p1"], ["local|p2", "local|p3"], ["local|p4"]]) // p9 gone, p1 shown once
    #expect(l.active == 1 && l.tabs[1].focused == "local|p3" && l.tabs[0].focused == "local|p1")

    // Another machine's tabs come after, and do not move the window.
    l.adopt(ViewState(tabs: [ViewTab(panes: ["p1"], active: true)]), machine: "box", alive: ["box|p1"])
    #expect(l.tabs.count == 4 && l.tabs[3].root == .leaf("box|p1") && l.active == 1)
    // Nothing alive, or nothing at all: nothing added.
    l.adopt(ViewState(tabs: [ViewTab(panes: ["p7"])]), machine: "local", alive: [])
    l.adopt(ViewState(), machine: "local", alive: ["local|p1"])
    #expect(l.tabs.count == 4)
    // A pane the window already shows stays where it is.
    var mine = Layout()
    mine.open("local|p2")
    mine.adopt(v, machine: "local", alive: ["local|p1", "local|p2", "local|p3"])
    #expect(mine.tabs.map(\.root.panes) == [["local|p2"], ["local|p1"], ["local|p3"]] && mine.active == 0)
}

// Every window a plan reported, under its own name, or the three every
// server sends; and what each is called.
@Test func planWindows() throws {
    let json = #"{"limits":[{"agent":"claude","five_hour":{"used_pct":41,"resets_at":"2026-11-01T23:00:00Z"},"week":{"used_pct":17},"windows":[{"key":"five_hour","used_pct":41},{"key":"seven_day","used_pct":17},{"key":"seven_day_opus","used_pct":3,"resets_at":"2026-11-05T00:00:00Z"}],"at":"2026-11-01T21:00:00Z"},{"agent":"codex","five_hour":{"used_pct":5},"spend":{"used_pct":60}}]}"#
    let l = try conchDecoder().decode(AgentLimitsResult.self, from: Data(json.utf8)).limits ?? []
    #expect(l[0].allWindows.map(\.key) == ["five_hour", "seven_day", "seven_day_opus"] && l[0].allWindows[2].resetsAt != nil)
    #expect(l[0].fiveHour?.resetsAt != nil && l[0].at != nil)
    #expect(l[1].allWindows.map(\.key) == ["five_hour", "spend_limit"]) // an older server's three
    #expect(PlanLimits(agent: "x").allWindows.isEmpty)
    #expect(["five_hour", "seven_day", "seven_day_opus", "spend_limit", "monthly_tokens"].map(PlanLimits.label)
            == ["5 hours", "7 days", "7 days · opus", "Spend", "monthly tokens"])
}

// ps read as the TUI reads it: each field, comm with spaces, bad lines skipped.
@Test func readingPS() {
    let out = """
      1     0  12345 1-02:03:04.50 /sbin/launchd
    200     1   2048     0:01.25 conch server
    201   200    512    01:00 /bin/zsh -l
    bad line
    202   200   x      0:00.00 nope
    """
    let p = parsePS(out)
    #expect(p.count == 3 && p[200]?.comm == "conch server" && p[200]?.cpuSeconds == 1.25 && p[201]?.ppid == 200)
    let day = 93784.5 // a day, two hours, three minutes, 4.5 seconds
    #expect(p[1]?.cpuSeconds == day)
    #expect(parseCPUTime("12:34") == 754 && parseCPUTime("1:00:00") == 3600 && parseCPUTime("x") == nil)
    #expect(parseCPUTime("1:2:3:4") == nil && parseCPUTime("-1:00") == nil && parseCPUTime("1-") == nil)
    #expect(parsePS("").isEmpty)
}

// The monitor's lines, as the TUI's box: this window, the server alone (its
// panes are theirs), "Panes (n)" adding them up, each pane indented and
// largest first, and the total; CPU unknown until a second sample.
@Test func resourceLines() {
    func proc(_ pid: Int, _ ppid: Int, _ rss: Int64, _ cpu: Double, _ comm: String = "x") -> Proc {
        Proc(pid: pid, ppid: ppid, rssKiB: rss, cpuSeconds: cpu, comm: comm)
    }
    let t0 = Date(timeIntervalSince1970: 1000)
    let prev = ProcSample(at: t0, procs: [10: proc(10, 1, 100, 1), 20: proc(20, 1, 200, 5), 21: proc(21, 20, 50, 0, "zsh"),
                                           22: proc(22, 21, 1000, 10, "claude"), 23: proc(23, 20, 10, 0, "zsh")])
    var now = prev.procs
    now[22] = proc(22, 21, 1000, 11, "claude") // one CPU second in two: 50%
    now[20] = proc(20, 1, 200, 5.2)
    let cur = ProcSample(at: t0.addingTimeInterval(2), procs: now)
    let r = resourceRows(prev: prev, cur: cur, app: ("This window", 10), server: 20,
                         panes: [("zsh", 23), ("claude", 21), ("gone", 99)])
    #expect(r.rows.map(\.name) == ["This window", "Server · pid 20", "Panes (2)", "claude", "zsh"])
    #expect(r.rows.map(\.indent) == [false, false, false, true, true])
    #expect(r.rows[1].rssKiB == 200 && abs((r.rows[1].cpuPct ?? 0) - 10) < 0.001) // the server alone
    #expect(r.rows[2].rssKiB == 1060 && abs((r.rows[2].cpuPct ?? 0) - 50) < 0.001)
    #expect(r.rows[3].rssKiB == 1050)
    let total: Int64 = 1360
    #expect(r.total.name == "Total" && r.total.rssKiB == total)
    // One sample: memory, CPU not yet known.
    let first = resourceRows(prev: nil, cur: cur, app: ("w", 10), server: nil, panes: [("zsh", 23)])
    #expect(first.rows.map(\.name) == ["w", "Panes (1)", "zsh"] && first.rows.allSatisfy { $0.cpuPct == nil } && first.total.cpuPct == nil)
    // Past `most`, the rest in one line — but never a lone "1 more".
    var many: [Int: Proc] = [1: proc(1, 0, 1, 0)]
    for i in 0..<5 { many[100 + i] = proc(100 + i, 1, Int64(10 * (i + 1)), 0) }
    let m = resourceRows(prev: nil, cur: ProcSample(at: t0, procs: many), app: ("app", 999), server: nil,
                         panes: (0..<5).map { ("p\($0)", 100 + $0) }, most: 2)
    let rest: Int64 = 60
    #expect(m.rows.map(\.name) == ["app", "Panes (5)", "p4", "p3", "3 more"] && m.rows[4].rssKiB == rest && m.rows[4].indent)
    let four = resourceRows(prev: nil, cur: ProcSample(at: t0, procs: many), app: ("app", 999), server: nil,
                            panes: (0..<3).map { ("p\($0)", 100 + $0) }, most: 2)
    #expect(four.rows.map(\.name) == ["app", "Panes (3)", "p2", "p1", "p0"])
    #expect(formatMemory(812) == "812 KB" && formatMemory(41 * 1024 + 600) == "42 MB" && formatMemory(Int64(1.5 * 1024 * 1024)) == "1.5 GB")
    #expect(cpuLevel(10) == .fine && cpuLevel(80) == .busy && cpuLevel(250) == .hot)
    #expect(cpuCells(0) == 0 && cpuCells(34) == 3 && cpuCells(100) == 10 && cpuCells(300) == 10 && cpuCells(-5) == 0)
}

@Test func settingsPageAddress() {
    #expect(settingsPageURL("conch settings: http://127.0.0.1:5123/#t=abc")?.absoluteString == "http://127.0.0.1:5123/#t=abc")
    #expect(settingsPageURL("Editing /x/config.toml.") == nil && settingsPageURL("") == nil)
}

// Agents are marked as [ui] icons says; the TUI's glyphs under "nerd".
@Test func agentMarksFollowIcons() {
    #expect(IconMode(setting: "nerd") == .nerd && IconMode(setting: " OFF ") == .off && IconMode(setting: nil) == .text && IconMode(setting: "x") == .text)
    #expect(AgentMark.of("claude") == AgentMark(glyph: "\u{f069}", symbol: "asterisk") && AgentMark.of("claude").glyphNeedsNerdFont)
    #expect(AgentMark.of("pi").glyph == "π" && !AgentMark.of("pi").glyphNeedsNerdFont)
    #expect(AgentMark.of("mine").glyph == "\u{f2db}" && AgentMark.of(nil) == AgentMark.of("mine"))
    let names = ["claude", "codex", "gemini", "opencode", "devin", "aider", "amp", "cursor", "grok", "kilo", "pi"]
    #expect(Set(names.map { AgentMark.of($0).glyph }).count == names.count)
}

// A file's stamp changes when it is written, and is empty for none.
@Test func fileStamps() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }
    let f = dir.appendingPathComponent("config.toml").path
    #expect(fileStamp(f) == "")
    try "[ui]\ntheme = \"conch\"\n".write(toFile: f, atomically: true, encoding: .utf8)
    let first = fileStamp(f)
    try "[ui]\ntheme = \"night-owl\"\n".write(toFile: f, atomically: true, encoding: .utf8)
    #expect(!first.isEmpty && fileStamp(f) != first) // a different size, at the least
}

// Workspaces isolated as the TUI's are: the first has what it was given
// and whatever no later one claims; a later one only its own; a helper
// follows the agent that started it.
@Test func workspaceIsolation() throws {
    var s = fixture() // api (r1): p1, p2, p4; loose: p3
    s.upsert(PaneInfo(id: "p5", name: "zsh")) // loose, claimed by nobody
    var helper = PaneInfo(id: "p6", name: "helper", agent: AgentStatus(name: "codex", state: .working))
    helper.createdBy = "p3"
    s.upsert(helper)
    let ui = #"{"spaces":[{"id":"sA","panes":["local|p3"]}],"first_space":{"projects":[]}}"#
    let ws = Workspace.load(uiJSON: Data(ui.utf8))
    // The first: api (no one claims it) and p5; not p3, nor the helper p3 started.
    #expect(Set(s.shown(in: ws[0]).map(\.id)) == ["p1", "p2", "p4", "p5"])
    // The later one: p3 and its helper, nothing else.
    #expect(Set(s.shown(in: ws[1]).map(\.id)) == ["p3", "p6"])
    #expect(s.shown(in: nil).count == 6 && s.shown(in: .everything).count == 6)
    // A project both have shows in both.
    let both = #"{"spaces":[{"id":"sA","projects":["r1"]}],"first_space":{"projects":["r1"]}}"#
    let wb = Workspace.load(uiJSON: Data(both.utf8))
    #expect(s.shown(in: wb[0]).contains { $0.id == "p1" } && s.shown(in: wb[1]).contains { $0.id == "p1" })
    // Another machine's panes are named with its machine.
    let remote = Workspace.load(uiJSON: Data(#"{"spaces":[{"id":"sB","panes":["box|p3"]}]}"#.utf8))
    #expect(s.shown(in: remote[1], machine: "box").map(\.id) == ["p3", "p6"] && s.shown(in: remote[1]).isEmpty)
    #expect(s.inbox(in: remote[1], machine: "box") == (0, 0))
    #expect(activeWorkspace(uiJSON: Data(#"{"active_space":2}"#.utf8)) == 2 && activeWorkspace(uiJSON: nil) == 0)
}

// A pane started in a workspace is put in it in ui.json, and nothing else
// changes; an unknown workspace or a file without any is said.
@Test func addingAPaneToAWorkspace() throws {
    let ui = #"{"spaces":[{"name":"old"},{"id":"sA","panes":["local|p1"]}],"tabs":[{"name":"t"}]}"#
    let out = try Workspace.addingPane("box|p7", to: "sA", in: Data(ui.utf8))
    let ws = Workspace.load(uiJSON: out)
    #expect(ws[2].panes == ["local|p1", "box|p7"] && ws[1].id == "w1")
    let again = try Workspace.addingPane("box|p7", to: "sA", in: out)
    #expect(Workspace.load(uiJSON: again)[2].panes.count == 2) // once
    #expect(try Workspace.addingPane("local|p2", to: "w1", in: out).count > 0) // an old one by its place
    #expect(throws: ConchError.self) { try Workspace.addingPane("local|p2", to: "nope", in: out) }
    #expect(throws: ConchError.self) { try Workspace.addingPane("local|p2", to: "sA", in: nil) }
    #expect(throws: ConchError.self) { try Workspace.addingPane("local|p2", to: "sA", in: Data("{}".utf8)) }
}

// The sidebar's rows walked as the keyboard walks them: only what is on
// screen, stepping and stopping at the ends, climbing to a parent.
@Test func treeNavigation() {
    let t = fixture().tree()
    let api = projects(t)[0]
    var open: Set<String> = [t.id, "ws:local", api.id, api.children![0].id] // machine, Workspace, project, Agents
    let nav = TreeNav([t], isOpen: { open.contains($0.id) })
    #expect(nav.rows.map(\.title) == ["local", "Workspace", "api", "Agents", "Add health check", "codex", "Branches", "Terminals", "CLI"])
    #expect(nav.step(from: nil, by: 1) == t.id)
    #expect(nav.step(from: t.id, by: -1) == t.id) // kept at the top
    #expect(nav.step(from: "pane:local|p1", by: 1) == "pane:local|p4")
    #expect(nav.step(from: nav.rows.last!.id, by: 5) == nav.rows.last!.id)
    #expect(nav.step(from: "gone", by: 1) == t.id) // folded away: the first
    #expect(nav.parent(of: "pane:local|p1") == api.children![0].id && nav.parent(of: t.id) == nil)
    #expect(nav.firstChild(of: api.id) == api.children![0].id)
    #expect(nav.firstChild(of: api.children![1].id) == nil) // Branches is folded
    #expect(nav.row("pane:local|p4")?.title == "codex" && nav.row(nil) == nil)
    open.remove(api.id)
    #expect(TreeNav([t], isOpen: { open.contains($0.id) }).rows.map(\.title) == ["local", "Workspace", "api", "CLI"])
    #expect(TreeNav([], isOpen: { _ in true }).step(from: nil, by: 1) == nil)
}

// Keys as the TUI's tree reads them; anything with ⌘, ⌃ or ⌥ is the menus'.
@Test func treeKeys() {
    #expect(TreeKey(keyCode: 126, characters: nil, modified: false) == .up)
    #expect(TreeKey(keyCode: 36, characters: "\r", modified: false) == .open)
    #expect(TreeKey(keyCode: 49, characters: " ", modified: false) == .fold)
    #expect(TreeKey(keyCode: 0, characters: "t", modified: false) == .task)
    #expect(TreeKey(keyCode: 0, characters: "j", modified: false) == .down)
    #expect(TreeKey(keyCode: 0, characters: "x", modified: false) == .close)
    #expect(TreeKey(keyCode: 0, characters: "t", modified: true) == nil)
    #expect(TreeKey(keyCode: 0, characters: "q", modified: false) == nil)
}

// A workspace is named in ui.json as the TUI keeps names: the first's in
// first_space, the others' in their entry; empty numbers it again; the
// rest of the file is left as it was.
@Test func namingWorkspaces() throws {
    let ui = #"{"spaces":[{"projects":["r1"]},{"id":"sB","name":"old"}],"first_space":{"projects":["r2"]},"tabs":[{"name":"t"}]}"#
    var out = try Workspace.renaming("first", to: " main ", in: Data(ui.utf8))
    out = try Workspace.renaming("w1", to: "review", in: out)
    out = try Workspace.renaming("sB", to: "", in: out)
    let ws = Workspace.load(uiJSON: out)
    #expect(ws.map(\.name) == ["main", "review", ""])
    #expect(ws.enumerated().map { Workspace.label($0.offset, $0.element) } == ["1 main", "2 review", "3"])
    #expect(ws[0].projects == ["r2"] && ws[1].projects == ["r1"] && ws[1].id == "w1") // what each holds kept
    let obj = try #require(try JSONSerialization.jsonObject(with: out) as? [String: Any])
    #expect((obj["tabs"] as? [Any])?.count == 1)
    // The first named with no others yet, and from no file at all.
    #expect(Workspace.load(uiJSON: try Workspace.renaming("first", to: "solo", in: nil)).map(\.name) == ["solo"])
    #expect(throws: ConchError.self) { try Workspace.renaming("nope", to: "x", in: Data(ui.utf8)) }
    #expect(throws: ConchError.self) { try Workspace.renaming("first", to: "x", in: Data("[1]".utf8)) }
}

// The spinner turns through the TUI's frames at its pace, the same frame
// for every view at one moment.
@Test func spinnerFrames() {
    let t0 = Date(timeIntervalSinceReferenceDate: 0)
    #expect(spinnerFrame(at: t0) == "⠋")
    #expect(spinnerFrame(at: t0.addingTimeInterval(0.13)) == "⠙")
    #expect(spinnerFrame(at: t0.addingTimeInterval(0.12 * 10 + 0.01)) == "⠋") // round again
    #expect(spinnerFrame(at: t0.addingTimeInterval(-0.05)) == "⠏") // before the reference, still a frame
    #expect(spinnerFrames.count == 10 && spinnerInterval == 0.12)
}

// An ssh session is told as the TUI tells one: no agent, and ssh the program.
@Test func sshPanes() throws {
    let json = #"{"panes":[{"id":"p1","name":"ssh","state":"running","command":["/usr/bin/ssh","-t","--","me@box"]},{"id":"p2","name":"zsh","state":"running","command":["/bin/zsh","-l"]},{"id":"p3","name":"claude","state":"running","command":["ssh"],"agent":{"name":"claude","state":"idle"}},{"id":"p4","name":"x","state":"running"}]}"#
    let panes = try conchDecoder().decode(PaneList.self, from: Data(json.utf8)).panes
    #expect(panes.map(\.isSSH) == [true, false, false, false])
}

// The Workspace row: its machine, and a new task in each of its projects.
@Test func workspaceRow() {
    let s = fixture()
    let w = s.tree(machine: "box").children![0]
    #expect(w.kind == .workspace("box") && w.machine == "box")
    #expect(s.menu(for: w, machine: "box").map(\.action) == [.addProject(machine: "box"), .divider, .newTask(project: "box|r1")])
    #expect(s.menu(for: w, machine: "box", machineOnline: false).isEmpty)
    #expect(Snapshot(panes: s.panes).tree().children?.map(\.title) == ["CLI"]) // no projects, no Workspace row
}

// SVG paths as Lucide writes them: absolute and relative, implicit lines
// after a move, numbers run together, arcs ending exactly where they say.
@Test func svgPaths() {
    func end(_ ops: [PathOp]) -> CGPoint? {
        switch ops.last {
        case .move(let p)?, .line(let p)?: return p
        case .cubic(_, _, let p)?, .quad(_, let p)?: return p
        default: return nil
        }
    }
    #expect(parseSVGPath("M4 17 10 11 4 5") == [.move(CGPoint(x: 4, y: 17)), .line(CGPoint(x: 10, y: 11)), .line(CGPoint(x: 4, y: 5))])
    #expect(parseSVGPath("m7 11 2-2-2-2") == [.move(CGPoint(x: 7, y: 11)), .line(CGPoint(x: 9, y: 9)), .line(CGPoint(x: 7, y: 7))])
    #expect(parseSVGPath("M3 3v5h5") == [.move(CGPoint(x: 3, y: 3)), .line(CGPoint(x: 3, y: 8)), .line(CGPoint(x: 8, y: 8))])
    #expect(parseSVGPath("M1.5.5L-.5 1e1") == [.move(CGPoint(x: 1.5, y: 0.5)), .line(CGPoint(x: -0.5, y: 10))])
    #expect(parseSVGPath("M0 0h2Z").last == .close)
    // A quarter arc ends where it says, and stays on its circle.
    let arc = parseSVGPath("M18 9a9 9 0 0 1-9 9")
    let e = end(arc)!
    #expect(abs(e.x - 9) < 1e-6 && abs(e.y - 18) < 1e-6)
    if case .cubic(_, _, let p) = arc[1] { #expect(abs(hypot(p.x - 9, p.y - 9) - 9) < 0.01) }
    // Flags written together ("0 01"), and a full circle made of two arcs.
    #expect(end(parseSVGPath("M0 0a1 1 0 01 2 0")) == CGPoint(x: 2, y: 0))
    let circle = parseSVGPath(svgCircle(12, 12, 3))
    #expect(end(circle) == CGPoint(x: 9, y: 12) && circle.count > 2)
    // Smooth curves mirror the last control point.
    let s = parseSVGPath("M0 0C1 1 2 1 3 0S5 -1 6 0")
    if case .cubic(let c1, _, _) = s[2] { #expect(c1 == CGPoint(x: 4, y: -1)) }
    // Junk ends the path where it is, rather than failing it.
    #expect(parseSVGPath("M1 1L2 2X3 3").count == 2 && parseSVGPath("").isEmpty)
    #expect(parseSVGPath(svgRect(3, 3, 18, 18, 2)).count > 8 && parseSVGPath(svgRect(0, 0, 4, 4)).last == .close)
}

// Every icon the tree and the settings sections use is there and reads
// to something to draw; an unknown one draws as the cog.
@Test func lucideIconsRead() {
    let names = ["folder", "folder-open", "git-branch", "terminal", "square-terminal", "history", "laptop", "server", "cloud", "globe", "layers",
                 "palette", "keyboard", "bell", "bot", "shield-check", "git-merge", "sparkles", "refresh-cw", "smartphone", "box", "zap", "lock", "settings"]
    for n in names {
        #expect(LucideIcons.all[n] != nil, "\(n)")
        let ops = LucideIcons.ops(n)
        #expect(ops.count >= 2, "\(n)")
        // Everything inside the 24×24 box, give or take a stroke.
        for op in ops {
            if case .line(let p) = op { #expect(p.x >= -1 && p.x <= 25 && p.y >= -1 && p.y <= 25, "\(n) \(p)") }
        }
    }
    #expect(LucideIcons.ops("no-such-icon") == LucideIcons.ops("settings"))
}

// The settings page's payload, read and edited as the panel does: values
// by section, defaults for absent keys, the top level for "" sections,
// lists one a line, and a save body that keeps whole numbers whole.
@Test func settingsForm() throws {
    let json = #"""
    {"config":{"ui":{"theme":"night-owl","mouse":true,"cost":false},"notify":{"limit_alerts":[50,80]},"remote":{"timeout":30}},
     "raw":"[ui]\ntheme = \"night-owl\"\n","version":"abc123","path":"/x/config.toml","exists":true,
     "schema":[{"id":"ui","key":"ui","title":"Appearance","description":"d","icon":"palette","fields":[
        {"key":"theme","label":"Theme","kind":"select","options":[{"value":"conch","label":"Conch"},{"value":"night-owl","label":"Night Owl"}]},
        {"key":"mouse","label":"Mouse","kind":"bool"},
        {"key":"hover","label":"Hover","kind":"bool","default":true}]},
      {"id":"notify","key":"notify","title":"Notifications","description":"d","icon":"bell","fields":[{"key":"limit_alerts","label":"At","kind":"ints"}]},
      {"id":"remote","key":"remote","title":"Remote","description":"d","icon":"server","fields":[{"key":"timeout","label":"Timeout","kind":"int"}]},
      {"id":"actions","key":"","title":"Actions","description":"d","icon":"zap","fields":[{"key":"actions","label":"Actions","kind":"tables","fields":[]}]}],
     "problems":["one"]}
    """#
    let p = try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
    var f = SettingsForm(p)
    let ui = p.schema[0], notify = p.schema[1], remote = p.schema[2], actions = p.schema[3]
    #expect(f.value(ui, ui.fields[0]) == .string("night-owl") && f.value(ui, ui.fields[1]) == .bool(true))
    #expect(f.value(ui, ui.fields[2]) == .bool(true)) // absent: its default
    #expect(listText(f.value(notify, notify.fields[0])) == "50\n80")
    #expect(ui.simple && !actions.simple && !f.dirty)
    // A section drawn whole says nothing; one of tables sends you to config.toml.
    #expect(ui.tableNote == nil && actions.tableNote == "These are tables of their own, edited in config.toml.")
    var mixed = ui
    mixed.fields.append(actions.fields[0])
    #expect(mixed.tableNote == "Some of these are tables, edited in config.toml.")
    #expect(LucideIcons.all["file-text"] != nil && !LucideIcons.ops("file-text").isEmpty)
    f.set(ui, ui.fields[0], .string("conch"))
    f.set(remote, remote.fields[0], .number(45))
    f.set(notify, notify.fields[0], listValue("50\n 90, ", ints: true))
    f.set(actions, actions.fields[0], .array([]))
    #expect(f.dirty && f.config["actions"] == .array([]) && f.value(notify, notify.fields[0]) == .array([.number(50), .number(90)]))
    let body = String(decoding: try f.saveBody(), as: UTF8.self)
    #expect(body.contains(#""version":"abc123""#) && body.contains(#""timeout":45"#) && !body.contains("45.0"))
    #expect(body.contains(#""theme":"conch""#))
    f.revert()
    #expect(!f.dirty)
    #expect(settingsToken(URL(string: "http://127.0.0.1:5/#t=b7af")!) == "b7af" && settingsToken(URL(string: "http://127.0.0.1:5/")!) == nil)
    #expect(listValue("a\nb,c") == .array([.string("a"), .string("b"), .string("c")]) && listText(nil) == "")
    let half = try JSONEncoder().encode(JSONValue.number(1.5))
    #expect(JSONValue.number(1.5) != .number(1) && half == Data("1.5".utf8))
}

// "Needs you": the waiting agents the workspace shows, the longest waiting
// first, each with where it is and what kind of question it has.
@Test func needsYou() {
    var s = fixture()
    s.upsert(PaneInfo(id: "p6", name: "claude", agent: AgentStatus(name: "claude", state: .blocked, message: "Claude needs your permission to use Bash",
                                                                  since: Date(timeIntervalSince1970: 5)), projectID: "r1", branch: "feat"))
    var gone = PaneInfo(id: "p7", name: "x", state: "exited", agent: AgentStatus(name: "codex", state: .blocked, since: Date(timeIntervalSince1970: 1)))
    gone.projectID = "r1"
    s.upsert(gone)
    #expect(s.needsYou().map(\.id) == ["p6", "p4"]) // p6 has waited longer; an exited one is no question
    #expect(s.place(of: s.pane("p6")!) == "api · feat" && s.place(of: s.pane("p3")!) == "")
    let review = Workspace(name: "r", projects: [], panes: ["local|p4"])
    #expect(s.needsYou(in: review).map(\.id) == ["p4"])
    #expect(questionKind("Claude needs your permission to use Bash") == "permission")
    #expect(questionKind("Claude is waiting for your input") == "question")
    #expect(questionKind(nil) == "waiting" && questionKind("") == "waiting")
}

// A card's question, without the lead-in and the choices the buttons show.
@Test func cardTexts() {
    let c = [Choice(choice: "1", label: "hello", default: true), Choice(choice: "2", label: "hi", default: nil)]
    #expect(cardText(#"answer: Should it say "hi" or "hello"? (hello · hi)"#, choices: c) == #"Should it say "hi" or "hello"?"#)
    #expect(cardText("Do it? (It is fine.)", choices: c) == "Do it? (It is fine.)") // not the choices: kept
    #expect(cardText("  plain  ", choices: []) == "plain")
    #expect(cardText("? Get started How would you like to authenticate?", choices: []) == "Get started How would you like to authenticate?")
    #expect(cardText("?", choices: []) == "?") // a lone mark is the question
    #expect(cardText("pick (hello · other)", choices: c) == "pick (hello · other)")
}

// A project added from a workspace is put in it, as the TUI keys it.
@Test func addingAProjectToAWorkspace() throws {
    let ui = #"{"spaces":[{"id":"sA","projects":["r1"]}],"tabs":[]}"#
    let out = try Workspace.addingProject(Workspace.key(project: "r9", machine: "box"), to: "sA", in: Data(ui.utf8))
    #expect(Workspace.load(uiJSON: out)[1].projects == ["r1", "box~r9"])
    let again = try Workspace.addingProject("r1", to: "sA", in: out)
    #expect(Workspace.load(uiJSON: again)[1].projects.count == 2)
    #expect(Workspace.key(project: "r1", machine: "local") == "r1")
    #expect(throws: ConchError.self) { try Workspace.addingProject("r1", to: "nope", in: out) }
    #expect(throws: ConchError.self) { try Workspace.addingProject("r1", to: "sA", in: nil) }
}

// A project's sessions: read off the wire, keyed in the layout, searched
// by every word, interrupted ones first, then the newest; and how long ago.
@Test func sessionsList() throws {
    let json = #"{"sessions":[{"agent":"claude","id":"a1","dir":"/src/api","branch":"feat","title":"Add health check","updated":"2026-11-01T10:00:00Z","cost_usd":0.4},{"agent":"codex","id":"b2","dir":"/src/api","title":"","updated":"2026-11-01T12:00:00Z","pane_id":"p4"},{"agent":"claude","dir":"/src/api","title":"Fix login","updated":"2026-10-30T10:00:00Z","interrupted":true}],"partial":true}"#
    let r = try conchDecoder().decode(SessionListResult.self, from: Data(json.utf8))
    let list = r.sessions ?? []
    #expect(list.count == 3 && r.partial == true && list[1].paneID == "p4" && list[0].sessionID == "a1" && list[0].costUSD == 0.4)
    #expect(list[1].name == "codex" && list[0].name == "Add health check")
    #expect(filterSessions(list, "").map(\.name) == ["Fix login", "codex", "Add health check"])
    #expect(filterSessions(list, "CLAUDE feat").map(\.name) == ["Add health check"])
    #expect(filterSessions(list, "nothing").isEmpty)
    #expect(Set(list.map(\.id)).count == 3)
    #expect(parseSessionsKey(sessionsKey("box", "r1"))! == ("box", "r1") && parseSessionsKey("changes|box|r1|b") == nil && parseSessionsKey("sessions|box|") == nil)
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    #expect(agoText(now.addingTimeInterval(-30), now: now) == "now" && agoText(now.addingTimeInterval(-300), now: now) == "5m ago")
    #expect(agoText(now.addingTimeInterval(-7200), now: now) == "2h ago" && agoText(now.addingTimeInterval(-3 * 86400), now: now) == "3d ago")
    #expect(agoText(nil) == "" && !agoText(now.addingTimeInterval(-40 * 86400), now: now).isEmpty)
}

// A pane whose program exited closes, as the TUI closes it — but not one
// that failed in its first seconds, nor a check that failed: their output
// is what there is to read.
@Test func panesCloseOnExit() throws {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    func pane(_ name: String = "zsh", code: Int, age: TimeInterval, state: String = "exited") -> PaneInfo {
        var p = PaneInfo(id: "p1", name: name, state: state)
        p.exitCode = code
        p.created = now.addingTimeInterval(-age)
        return p
    }
    #expect(closesOnExit(pane(code: 0, age: 2), now: now)) // exit at once, cleanly: closes
    #expect(closesOnExit(pane(code: 1, age: 600), now: now)) // an ssh logout after a while, whatever its code
    #expect(!closesOnExit(pane(code: 127, age: 2), now: now)) // failed to start: kept to read
    #expect(closesOnExit(pane(code: 127, age: 11), now: now)) // past the window
    #expect(!closesOnExit(pane("check · feat", code: 1, age: 600), now: now))
    #expect(closesOnExit(pane("check · feat", code: 0, age: 600), now: now))
    #expect(!closesOnExit(pane(code: 0, age: 600, state: "running"), now: now))
    var none = PaneInfo(id: "p2", name: "x", state: "exited")
    none.exitCode = 2
    #expect(closesOnExit(none, now: now)) // no start time: not one to keep
    let p = try conchDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p3","name":"zsh","state":"exited","exit_code":0,"created":"2026-11-02T08:00:00Z"}"#.utf8))
    #expect(p.created != nil && p.exitCode == 0)
}

@Test func themeChosenReadsAsTheTUIDoes() {
    #expect(themeChosen("night-owl", "night-owl"))
    #expect(!themeChosen("conch", "night-owl"))
    // No theme set, or one this build has never heard of, is conch's.
    #expect(themeChosen("conch", ""))
    #expect(themeChosen("conch", "  Conch "))
    #expect(themeChosen("conch", "no-such-theme"))
    #expect(!themeChosen("dracula", ""))
    // An option the app has no palette for is chosen only by its own name.
    #expect(themeChosen("from-a-newer-tui", "from-a-newer-tui"))
    #expect(!themeChosen("from-a-newer-tui", ""))
}

@Test func themePreviewShowsWithoutSettling() {
    // Following config.toml, a preview is shown and the config's settled.
    var r = ConchTheme.resolve(choice: "", configured: "nord", preview: "github-light")
    #expect(r.settled.id == "nord" && r.shown.id == "github-light" && r.shown.light)
    // No preview: shown is settled.
    r = ConchTheme.resolve(choice: "", configured: "nord", preview: nil)
    #expect(r.settled.id == "nord" && r.shown.id == "nord")
    // A theme picked in the app wins over the config, and a preview over both.
    r = ConchTheme.resolve(choice: "dracula", configured: "nord", preview: nil)
    #expect(r.settled.id == "dracula" && r.shown.id == "dracula")
    r = ConchTheme.resolve(choice: "dracula", configured: "nord", preview: "ayu")
    #expect(r.settled.id == "dracula" && r.shown.id == "ayu")
    // Nothing set anywhere, or an unknown preview, is conch's.
    r = ConchTheme.resolve(choice: "", configured: nil, preview: "no-such-theme")
    #expect(r.settled.id == "conch" && r.shown.id == "conch")
}

@Test func lightThemeFollowsTheAppearance() {
    let both = "[ui]\ntheme = \"nord\"\nlight_theme = \"github-light\"\n"
    #expect(ConchTheme.configured(in: both, dark: true) == "nord")
    #expect(ConchTheme.configured(in: both, dark: false) == "github-light")
    // No light theme, or an empty one: the theme, light or dark.
    for toml in ["[ui]\ntheme = \"nord\"\n", "[ui]\ntheme = \"nord\"\nlight_theme = \"\"\n"] {
        #expect(ConchTheme.configured(in: toml, dark: false) == "nord")
        #expect(ConchTheme.configured(in: toml, dark: true) == "nord")
    }
    // A light theme alone, with no theme: conch's in dark.
    #expect(ConchTheme.named(ConchTheme.configured(in: "[ui]\nlight_theme = \"github-light\"\n", dark: true)).id == "conch")
    // Outside [ui] it is not the setting.
    #expect(ConchTheme.configured(in: "[other]\nlight_theme = \"github-light\"\n", dark: false) == nil)
    #expect(ConchTheme.configured(in: nil, dark: false) == nil)
}

@Test func offIsChosenOnlyWhenNothingIsSet() {
    #expect(themeChosen("", "", off: true))
    #expect(themeChosen("", "  ", off: true))
    #expect(!themeChosen("conch", "", off: true)) // unlike theme's menu, where empty is conch's
    #expect(themeChosen("github-light", "github-light", off: true))
    #expect(!themeChosen("", "github-light", off: true))
    #expect(themeChosen("conch", "no-such-theme", off: true)) // the app draws an unknown one as conch's
    #expect(themeChosen("conch", "", off: false))
}

// Text an agent draws for a dark terminal must still read on a light
// theme: every pane's Ghostty settings carry a contrast floor, higher on
// a light theme.
@Test func panesKeepTextReadable() {
    for t in ConchTheme.all {
        let want = t.light ? "minimum-contrast = 3" : "minimum-contrast = 1.1"
        #expect(t.ghosttyColors.contains(want), "\(t.id)")
        #expect(t.ghosttyColors.contains("background = \(t.bg.hex)"))
    }
}

// MARK: commit graph

private func c(_ h: String, _ parents: String...) -> LogCommit { LogCommit(hash: h, parents: parents, subject: h) }

@Test func graphOfAStraightLine() {
    let rows = commitGraph([c("c", "b"), c("b", "a"), c("a")])
    #expect(rows.map(\.lane) == [0, 0, 0])
    #expect(rows[0].into.isEmpty && rows[0].outOf == [0])
    #expect(rows[1].into == [0] && rows[1].outOf == [0] && rows[1].through.isEmpty)
    #expect(rows[2].into == [0] && rows[2].outOf.isEmpty) // a root ends its lane
    #expect(rows.allSatisfy { $0.width == 1 })
}

@Test func graphOfABranchMergedBack() {
    // m merges f2 into main; f2 <- f1 <- a; main: m <- b <- a.
    let rows = commitGraph([c("m", "b", "f2"), c("f2", "f1"), c("b", "a"), c("f1", "a"), c("a")])
    let m = rows[0], f2 = rows[1], b = rows[2], f1 = rows[3], a = rows[4]
    #expect(m.merge && m.lane == 0 && m.outOf == [0, 1])
    #expect(f2.lane == 1 && f2.into == [1] && f2.through == [0])
    #expect(b.lane == 0 && b.through == [1])
    #expect(f1.lane == 1 && f1.outOf == [1])
    // Both lanes wait for a: it takes the first, the other joins it.
    #expect(a.lane == 0 && a.into == [0, 1] && a.outOf.isEmpty)
    // The branch keeps one colour down its lane, unlike main's.
    #expect(f2.colors[1] == f1.colors[1] && f2.colors[1] != m.colors[0])
    #expect(rows.map(\.width).max() == 2)
}

@Test func graphReusesAFreedLane() {
    // Two branch tips off a, one after the other: x's lane closes at a's
    // join, and nothing needs a third lane.
    let rows = commitGraph([c("x", "a"), c("y", "a"), c("a")])
    #expect(rows[0].lane == 0 && rows[1].lane == 1 && rows[2].into == [0, 1])
    #expect(rows.map(\.width) == [1, 2, 2])
}

@Test func graphOfACutShortLog() {
    // A parent beyond the limit runs off the bottom; unrelated roots each
    // get a lane of their own.
    let rows = commitGraph([c("b", "a"), c("z")])
    #expect(rows[0].outOf == [0])
    #expect(rows[1].lane == 1 && rows[1].through == [0])
    #expect(commitGraph([]).isEmpty)
}

@Test func graphMergeIntoAWaitingLane() {
    // A merge whose second parent another lane already waits for points
    // at that lane rather than opening a third.
    let rows = commitGraph([c("t", "p"), c("m", "q", "p"), c("q", "p"), c("p")])
    #expect(rows[1].lane == 1 && rows[1].outOf == [1, 0])
    #expect(rows.map(\.width).max() == 2)
}

@Test func refsParse() {
    let r = CommitRef.parse(["tag: v1", "origin/main", "HEAD -> main", "feat", "origin/HEAD"])
    #expect(r.map(\.name) == ["main", "feat", "origin/main", "v1"])
    #expect(r.map(\.kind) == [.head, .branch, .remote, .tag])
    #expect(CommitRef.parse(["HEAD"]) == [CommitRef(name: "HEAD", kind: .head)])
    #expect(CommitRef.parse(nil).isEmpty)
    // A branch with a slash in its name is a branch, not a remote.
    #expect(CommitRef.parse(["conch/feat"]).first?.kind == .branch)
}

@Test func commitsKeys() {
    #expect(parseCommitsKey(commitsKey("busybox", "p1"))! == ("busybox", "p1"))
    #expect(parseCommitsKey("commits|local|") == nil && parseCommitsKey("sessions|local|p") == nil)
}

@Test func logDecodes() throws {
    let json = #"{"commits":[{"hash":"abc1234567","parents":["def"],"subject":"s","author":"a","time":"2026-11-02T09:00:00Z","refs":["HEAD -> main"]},{"hash":"def","subject":"root","author":"a","time":"2026-11-01T09:00:00Z"}],"more":true}"#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let r = try d.decode(LogResult.self, from: Data(json.utf8))
    #expect(r.commits?.count == 2 && r.more == true && r.commits?[1].parents == nil && r.commits?[0].short == "abc1234")
}
