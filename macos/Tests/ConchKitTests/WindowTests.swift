import Foundation
import Testing
@testable import ConchKit

@Test func appLookReadsConfig() {
    #expect(AppLook(toml: nil) == AppLook())
    #expect(AppLook(toml: "[ui]\ntheme = \"nord\"\n") == AppLook(), "a file from before [app]")
    let all = """
    [ui]
    tabs = "left"
    [app]
    tabs = "left"
    density = "compact"
    sidebar = "glass" # see-through
    ports = false
    scratch_key = true
    prefix_keys = false
    """
    #expect(AppLook(toml: all) == AppLook(tabs: .left, compact: true, glass: true, ports: false, scratchKey: true, prefixKeys: false))
    #expect(AppLook(toml: "[app]\nprefix_keys = true\n").prefixKeys)
    #expect(AppLook(toml: "[app]\ntabs = \"right\"\n").tabs == .right)
    #expect(AppLook(toml: "[app]\ntabs = \"sideways\"\n").tabs == .top, "unknown: the default")
    #expect(AppLook(toml: "[app]\ntabs = \"\"\n").tabs == .top)
    #expect(AppLook(toml: "[ui]\ntabs = \"left\"\n").tabs == .top, "another table's key is not [app]'s")
}

@Test func tabsGroupByMachine() {
    let tabs = [Tab(pane: key("busybox", "p1")), Tab(pane: key("local", "p2")), Tab(pane: changesKey("local", "r1", "feat")),
                Tab(pane: key("box2", "p9")), Tab(pane: key("busybox", "p3"))]
    let g = tabGroups(tabs, machines: ["local", "busybox"])
    #expect(g == [TabGroup(machine: "local", indices: [1, 2]), TabGroup(machine: "busybox", indices: [0, 4]), TabGroup(machine: "box2", indices: [3])])
    #expect(tabGroups([], machines: ["local"]).isEmpty)
    #expect(tabGroups([Tab(pane: key("local", "p1"))], machines: []) == [TabGroup(machine: "local", indices: [0])])
    #expect(machine(ofKey: sessionsKey("busybox", "r2")) == "busybox")
}

@Test func letterboxGeometry() {
    let m = CellMetrics(cellWidth: 8.5, cellHeight: 19, padX: 8, padY: 6)
    #expect(m.room(width: 16 + 8.5 * 80, height: 12 + 19 * 24) == CellSize(cols: 80, rows: 24))
    #expect(m.room(width: 16 + 8.5 * 80 - 0.1, height: 12 + 19 * 24) == CellSize(cols: 79, rows: 24), "a cell short is one fewer")
    #expect(m.room(width: 10, height: 10) == nil, "no room at all")
    #expect(CellMetrics(cellWidth: 0, cellHeight: 0, padX: 0, padY: 0).room(width: 500, height: 500) == nil, "metrics not known yet")
    let p = m.points(for: CellSize(cols: 80, rows: 24))
    #expect(p.width == 16 + 680 && p.height == 12 + 456)
    #expect(m.room(width: p.width, height: p.height) == CellSize(cols: 80, rows: 24), "the letterbox holds the pane exactly")

    let room = CellSize(cols: 120, rows: 40)
    #expect(!letterboxed(pane: nil, room: room))
    #expect(!letterboxed(pane: CellSize(cols: 120, rows: 40), room: room))
    #expect(letterboxed(pane: CellSize(cols: 160, rows: 38), room: room), "bigger than the room: drawn clipped")
    #expect(letterboxed(pane: CellSize(cols: 80, rows: 24), room: room))
    #expect(!letterboxed(pane: CellSize(cols: 80, rows: 24), room: nil))
    #expect(!letterboxed(pane: CellSize(cols: 0, rows: 0), room: room), "a size not known is not one")
    #expect(letterboxNote(CellSize(cols: 160, rows: 38)).contains("160×38"))
}

@Test func previewFontFits() {
    let m = CellMetrics(cellWidth: 8.5, cellHeight: 19, padX: 8, padY: 6)
    let pane = CellSize(cols: 160, rows: 40)
    let (w, h) = m.points(for: pane)
    #expect(fittingFontSize(base: 14, metrics: m, pane: pane, width: w, height: h) == 14, "fits: unchanged")
    #expect(fittingFontSize(base: 14, metrics: m, pane: pane, width: w * 2, height: h * 2) == 14, "never bigger")
    #expect(fittingFontSize(base: 14, metrics: m, pane: pane, width: w / 2, height: h) == 7)
    #expect(fittingFontSize(base: 14, metrics: m, pane: pane, width: 10, height: 10) == 6, "no smaller than readable")
    #expect(fittingFontSize(base: 14, metrics: m, pane: CellSize(cols: 0, rows: 0), width: 10, height: 10) == 14)
}

@Test func roomAndClaimControls() throws {
    let room = attachControlBody(token: "t", .room(CellSize(cols: 120, rows: 40)))
    #expect(room == "\u{2063}conch-attach:t {\"cols\":120,\"rows\":40}")
    #expect(attachControlBody(token: "t", .take) == "\u{2063}conch-attach:t {\"claim\":true}")
    #expect(attachControlBody(token: "t", AttachControl(clear: true)) == "\u{2063}conch-attach:t {\"clear\":true}", "the search's controls unchanged")
}

@Test func attachAndServerRaw() {
    #expect(attachRaws("usage: conch attach [-hold] [-raw] PANE"))
    #expect(!attachRaws("usage: conch attach [-hold] PANE"))
    #expect(!attachRaws(""))
    #expect(serverRaws(["pane.v1", "pane.raw.v1"]))
    #expect(!serverRaws(["pane.v1"]))
    #expect(!serverRaws(nil))
}

@Test func findsTheScratchPane() {
    var helper = PaneInfo(id: "p5", name: scratchName, projectID: "r1")
    helper.createdBy = "p1"
    var ended = PaneInfo(id: "p6", name: scratchName, state: "exited", projectID: "r1")
    ended.exitCode = 0
    let panes = [
        PaneInfo(id: "p1", name: "zsh", projectID: "r1"),
        helper, ended,
        PaneInfo(id: "p7", name: scratchName, projectID: "r2"),
        PaneInfo(id: "p8", name: scratchName, agent: AgentStatus(name: "claude", state: .idle), projectID: "r1"),
        PaneInfo(id: "p9", name: scratchName, projectID: "r1"),
    ]
    #expect(scratchPane(panes, project: "r1")?.id == "p9")
    #expect(scratchPane(panes, project: "r2")?.id == "p7")
    #expect(scratchPane(panes, project: "r3") == nil)
    #expect(scratchPane([], project: "r1") == nil)
    #expect(scratchPane(panes, project: nil) == nil)
    #expect(scratchPane([PaneInfo(id: "p10", name: scratchName)], project: nil)?.id == "p10", "a machine's own, outside every project")
}

@Test func rawSearchNotes() {
    #expect(rawSearchNote(selected: nil, total: 0) == "Not found")
    #expect(rawSearchNote(selected: 0, total: 12) == "1 of 12")
    #expect(rawSearchNote(selected: nil, total: 1) == "1 match")
    #expect(rawSearchNote(selected: nil, total: 5) == "5 matches")
}

@Test func portsAndQuitting() {
    #expect(portLabel(4317, machine: "local") == ":4317")
    #expect(portLabel(4317, machine: "busybox") == "busybox:4317")
    #expect(forwardCommand(host: "me@box", port: 5173) == "ssh -N -L 5173:localhost:5173 me@box")
    #expect(runOnNote(panes: 0) == "Nothing is running in conch server.")
    #expect(runOnNote(panes: 1).hasPrefix("1 pane keeps running"))
    #expect(runOnNote(panes: 9).hasPrefix("9 panes keep running"))
}

@Test func decodesListeningPorts() throws {
    let p = try JSONDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p1","name":"dev","state":"running","cols":80,"rows":24,"listening":[4317,5173]}"#.utf8))
    #expect(p.listening == [4317, 5173])
    let old = try JSONDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p1","name":"dev","state":"running"}"#.utf8))
    #expect(old.listening == nil, "an older server says nothing")
}

@Test func savedValueStaysChoosable() {
    let light = [SettingsOption(value: "", label: "Off"), SettingsOption(value: "github-light", label: "GitHub Light")]
    #expect(keepingSaved(light, "", label: { $0 }) == light, "Off is offered already")
    #expect(keepingSaved(light, "github-light", label: { $0 }) == light)
    let kept = keepingSaved(light, "nord", label: { _ in "Nord" })
    #expect(kept.count == 3 && kept.last == SettingsOption(value: "nord", label: "Nord (from the file)"))
}

@Test func decodesUsageHistory() throws {
    let json = #"{"at":"2026-11-04T12:00:00Z","agents":[{"agent":"claude","periods":[{"hours":24,"input":12,"output":34,"cache_read":1000,"messages":2,"models":[{"model":"claude-opus-5-5","input":12,"output":34,"cache_read":1000,"messages":2}]},{"hours":168,"input":20,"output":40},{"hours":720,"input":0,"output":0}]}]}"#
    let dec = JSONDecoder()
    dec.dateDecodingStrategy = .iso8601
    let h = try dec.decode(UsageHistory.self, from: Data(json.utf8))
    #expect(h.agents.count == 1 && h.agents[0].periods.count == 3)
    let day = try #require(h.agents[0].period(24))
    #expect(day.tokens == UsageTokens(input: 12, cacheWrite: 0, cacheRead: 1000, output: 34, messages: 2))
    #expect(day.models.first?.model == "claude-opus-5-5" && day.models.first?.tokens.total == 1046)
    #expect(h.agents[0].period(168)?.models.isEmpty == true, "no models said: none")
    #expect(h.agents[0].period(5) == nil)
    let empty = try dec.decode(UsageHistory.self, from: Data(#"{"at":"2026-11-04T12:00:00Z"}"#.utf8))
    #expect(empty.agents.isEmpty, "nothing saved in 30 days")
}

@Test func usageLabels() {
    #expect(usagePeriodLabel(24) == "24 hours")
    #expect(usagePeriodLabel(168) == "7 days")
    #expect(usagePeriodLabel(720) == "30 days")
    #expect(usagePeriodLabel(1) == "1 hour")
    #expect(usageLine(UsageTokens(input: 1_200_000, output: 340_000)) == "in 1.2M · out 340k")
    #expect(usageLine(UsageTokens(input: 5, cacheWrite: 210_000, cacheRead: 4_100_000_000, output: 7)) == "in 5 · out 7 · cache 4.1B read, 210k written")
    #expect(usageShare(UsageTokens(input: 25), of: UsageTokens(input: 100)) == 0.25)
    #expect(usageShare(UsageTokens(input: 1), of: UsageTokens()) == 0, "nothing used: no share")
}

@Test func clicksGiveTheKeyboardOnlyWhereTheyLand() {
    func owner(_ inTerminal: Bool, _ x: Double, _ y: Double, sidebar: Double? = 260) -> ClickOwner {
        clickOwner(inTerminal: inTerminal, x: x, y: y, sidebarWidth: sidebar, statusBar: 26, windowHeight: 820, toolbar: 52)
    }
    #expect(owner(true, 700, 400) == .terminal)
    #expect(owner(false, 120, 400) == .sidebar, "a row in the tree")
    #expect(owner(false, 260, 400) == .sidebar, "its very edge")
    #expect(owner(false, 1250, 13) == .unchanged, "the status bar's monitor")
    #expect(owner(false, 60, 13) == .unchanged, "the status bar under the sidebar")
    #expect(owner(false, 120, 800) == .unchanged, "the toolbar over the sidebar")
    #expect(owner(false, 700, 400) == .unchanged, "a split's header, the empty area")
    #expect(owner(false, 120, 400, sidebar: nil) == .unchanged, "no sidebar on screen")
    #expect(owner(true, 120, 13, sidebar: nil) == .terminal, "a terminal wins wherever it is")
}

@Test func conchFindsItsSourceAndGo() {
    let base = ["HOME": "/Users/me", "PATH": "/usr/bin:/bin", "CONCH_PANE_ID": "p1"]
    let env = conchEnvironment(base, source: "/src/conch", exists: { $0 == "/src/conch/go.mod" })
    #expect(env["CONCH_SOURCE"] == "/src/conch")
    #expect(env["CONCH_PANE_ID"] == nil)
    let path = env["PATH"]!.split(separator: ":").map(String.init)
    #expect(path.prefix(2) == ["/usr/bin", "/bin"], "what was there stays first")
    #expect(path.contains("/opt/homebrew/bin") && path.contains("/usr/local/go/bin") && path.contains("/Users/me/go/bin"))
    // Moved away since the app was built: not given.
    #expect(conchEnvironment(base, source: "/src/conch", exists: { _ in false })["CONCH_SOURCE"] == nil)
    // Set already: kept.
    var mine = base
    mine["CONCH_SOURCE"] = "/elsewhere"
    #expect(conchEnvironment(mine, source: "/src/conch", exists: { _ in true })["CONCH_SOURCE"] == "/elsewhere")
    // A PATH holding them already is not doubled.
    let full = conchEnvironment(["HOME": "/h", "PATH": "/opt/homebrew/bin:/usr/bin"], source: nil, exists: { _ in true })
    #expect(full["PATH"]!.components(separatedBy: "/opt/homebrew/bin").count == 2)
}

@Test func whatCanBeUpdated() {
    let c = updateCandidates([
        (id: "busybox", label: "10.0.0.115", online: true, behind: ["pane.raw.v1", "pane.listening.v1", "usage.history.v1", "x.v1"]),
        (id: "box2", label: "box2", online: false, behind: ["pane.raw.v1"]),
        (id: "local", label: "local", online: true, behind: ["usage.history.v1"]),
        (id: "fresh", label: "fresh", online: true, behind: []),
    ])
    #expect(c.map(\.machine) == ["local", "busybox"], "this Mac first; offline and up-to-date ones left out")
    #expect(c[0].label == "This Mac" && c[0].action == .reload && c[0].doing.hasPrefix("Reload"))
    #expect(c[1].action == .upgrade && c[1].label == "10.0.0.115")
    #expect(c[1].lacks == "pane.raw.v1, pane.listening.v1, usage.history.v1 and 1 more")
    #expect(c[0].lacks == "usage.history.v1")
    #expect(updateCandidates([]).isEmpty)
    #expect(versionLabel("0.1.9", updates: 0) == "v0.1.9")
    #expect(versionLabel("0.1.9", updates: 2) == "v0.1.9 · 2")
    #expect(versionLabel("", updates: 0) == nil)
    #expect(versionLabel("", updates: 1) == "Updates · 1")
}

@Test func closingAWorkspace() throws {
    let ui = #"""
    {"tabs":[{"panes":["p1"]}],"active_space":2,
     "first_space":{"name":"main","projects":["r1"],"panes":["local|p1"]},
     "spaces":[{"id":"s2","name":"review","projects":["r2"],"panes":["local|p2"],"hosts":["box"],"tabs":[{"panes":["p2"]}]},
               {"id":"s3","projects":["r3"]}]}
    """#
    let out = try Workspace.closing("s2", in: Data(ui.utf8))
    let obj = try #require(try JSONSerialization.jsonObject(with: out) as? [String: Any])
    let first = try #require(obj["first_space"] as? [String: Any])
    #expect(first["projects"] as? [String] == ["r1", "r2"], "what it held is the first's")
    #expect(first["panes"] as? [String] == ["local|p1", "local|p2"])
    #expect(first["hosts"] as? [String] == ["box"])
    #expect(first["name"] as? String == "main")
    #expect((obj["tabs"] as? [Any])?.count == 2, "its tabs after the first's")
    #expect((obj["spaces"] as? [[String: Any]])?.map { $0["id"] as? String } == ["s3"])
    #expect(obj["active_space"] as? Int == 1, "the one on screen moves down past it")
    let ws = Workspace.load(uiJSON: out)
    #expect(ws.count == 2 && ws[1].id == "s3")

    // Closing the one on screen goes to the first; closing the last other
    // one leaves no list.
    let onScreen = try JSONSerialization.jsonObject(with: try Workspace.closing("s3", in: Data(ui.utf8))) as? [String: Any]
    #expect(onScreen?["active_space"] as? Int == 0)
    let single = try Workspace.closing("s3", in: try Workspace.closing("s2", in: Data(ui.utf8)))
    let s = try JSONSerialization.jsonObject(with: single) as? [String: Any]
    #expect(s?["spaces"] == nil && s?["active_space"] == nil)
    #expect(Workspace.load(uiJSON: single).count == 1)

    #expect(throws: (any Error).self) { try Workspace.closing("first", in: Data(ui.utf8)) }
    #expect(throws: (any Error).self) { try Workspace.closing("nope", in: Data(ui.utf8)) }
    #expect(throws: (any Error).self) { try Workspace.closing("s2", in: Data("not json".utf8)) }
    #expect(throws: (any Error).self) { try Workspace.closing("s2", in: nil) }
}

// text_size and font_size read from [app], kept in range; their steps.
@Test func textAndFontSize() {
    #expect(AppLook(toml: nil).textScale == 1 && AppLook(toml: nil).fontSize == 14)
    let l = AppLook(toml: "[app]\ntext_size = 125\nfont_size = 16.5\n")
    #expect(l.textScale == 1.25 && l.fontSize == 16.5)
    #expect(AppLook(toml: "[app]\ntext_size = 500\nfont_size = 2\n").textScale == 1.6)
    #expect(AppLook(toml: "[app]\ntext_size = 500\nfont_size = 2\n").fontSize == 9)
    #expect(AppLook(toml: "[app]\ntext_size = \"110\"\nfont_size = 0\n").textScale == 1.1)
    #expect(AppLook(toml: "[app]\ntext_size = big\n").textScale == 1)
    // Go reads text_size as a whole number; a fraction is not understood
    // here either. font_size takes half points on both sides.
    #expect(AppLook(toml: "[app]\ntext_size = 112.5\n").textScale == 1)
    #expect(AppLook(toml: "[app]\nfont_size = 13.5\n").fontSize == 13.5)
    #expect(AppLook.textStep(from: 1, bigger: true) == 1.1 && AppLook.textStep(from: 1, bigger: false) == 0.9)
    #expect(AppLook.textStep(from: 1.6, bigger: true) == 1.6 && AppLook.textStep(from: 0.8, bigger: false) == 0.8)
    #expect(AppLook.textStep(from: 1.17, bigger: true) == 1.25)
    #expect(AppLook.fontStep(from: 14, bigger: true) == 15 && AppLook.fontStep(from: 9, bigger: false) == 9 && AppLook.fontStep(from: 28, bigger: true) == 28)
    #expect(AppLook.fontStep(from: 16.5, bigger: false) == 16)
}

// One setting written into config.toml, everything else kept.
@Test func writesOneSetting() {
    let toml = "# mine\n[ui]\ntheme = \"terminal\"\n\n[app]\ntabs = \"left\"\n\n[notify]\nbell = true\n"
    let set = settingTOML(toml, section: "app", key: "font_size", value: "16")
    #expect(set == "# mine\n[ui]\ntheme = \"terminal\"\n\n[app]\ntabs = \"left\"\nfont_size = 16\n\n[notify]\nbell = true\n")
    #expect(settingTOML(set, section: "app", key: "font_size", value: "17").contains("font_size = 17\n") && !settingTOML(set, section: "app", key: "font_size", value: "17").contains("= 16"))
    #expect(settingTOML(set, section: "app", key: "font_size", value: nil) == toml)
    // A missing section is added; nothing to take out of one that is not there.
    #expect(settingTOML("[ui]\ntheme = \"x\"\n", section: "app", key: "text_size", value: "110") == "[ui]\ntheme = \"x\"\n\n[app]\ntext_size = 110\n")
    #expect(settingTOML("", section: "app", key: "text_size", value: "110") == "[app]\ntext_size = 110\n")
    #expect(settingTOML("[ui]\n", section: "app", key: "text_size", value: nil) == "[ui]\n")
    // A key of the same name in another section, or commented out, is left.
    let other = "[ui]\nfont_size = 3\n[app]\n# font_size = 99\n"
    #expect(settingTOML(other, section: "app", key: "font_size", value: "15") == "[ui]\nfont_size = 3\n[app]\n# font_size = 99\nfont_size = 15\n")
    // What it writes reads back.
    #expect(AppLook(toml: settingTOML(toml, section: "app", key: "text_size", value: "140")).textScale == 1.4)
}

// Every command a menu item names is handled — by the menu's own switch or
// the store's. A new item named a command nobody handled (the text sizes)
// and did nothing at all, with nothing said.
@Test func everyMenuCommandIsHandled() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    let menu = try String(contentsOfFile: dir + "/main.swift", encoding: .utf8)
    let store = try String(contentsOfFile: dir + "/Store.swift", encoding: .utf8)
    // item("Title", "key", .command or [.command, .shift], "command")
    let named = try Regex(#"item\("[^"]*", "[^"]*"(?:, (?:\[[^\]]*\]|[^,\[\)]+))?, "(\w+)"\)"#)
    var ids = Set(menu.matches(of: named).compactMap { $0.output[1].substring.map(String.init) })
    #expect(ids.count > 20)
    ids = ids.filter { !$0.hasPrefix("tab") } // tab1…tab9, handled by prefix
    let missing = ids.filter { !menu.contains("case \"\($0)\"") && !store.contains("case \"\($0)\"") }
    #expect(missing.isEmpty, "menu commands nobody handles: \(missing.sorted())")
}

// A theme file read as the TUI reads it: based_on's colours with the file's
// over them; each way a file can be wrong left out with why.
@Test func themesOfYourOwn() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("conch-themes-\(UUID().uuidString)").path
    try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(atPath: dir) }
    func put(_ name: String, _ body: String) throws { try body.write(toFile: dir + "/" + name, atomically: true, encoding: .utf8) }
    try put("my-dark.toml", "# mine\nname = \"My Dark\"\nbased_on = \"vscode-dark-modern\"\nbg = \"#16161e\"  # deeper\naccent = \"#7AA2F7\"\n")
    try put("paper.toml", "bg = \"#FAFAF5\"\nfg = \"#222222\"\n")          // no base: conch's; light by its ground
    try put("forced.toml", "bg = \"#FAFAF5\"\nlight = false\n")
    try put("nord.toml", "bg = \"#000000\"\n")
    try put("Bad Name.toml", "bg = \"#000000\"\n")
    try put("badcolour.toml", "accent = \"blue\"\n")
    try put("typo.toml", "backgroud = \"#000000\"\n")
    try put("open.toml", "bg = \"#000000\n")
    try put("nobase.toml", "based_on = \"solarised\"\n")
    try put("notes.txt", "x")
    let (themes, problems) = userThemes(in: dir)
    #expect(themes.map(\.id) == ["forced", "my-dark", "paper"])
    let my = themes[1], base = ConchTheme.named("vscode-dark-modern")
    #expect(my.label == "My Dark" && my.bg.hex == "#16161E" && my.accent.hex == "#7AA2F7" && my.fg == base.fg && my.ok == base.ok && !my.light)
    #expect(themes[2].label == "paper" && themes[2].light && themes[2].border == ConchTheme.conch.border)
    #expect(!themes[0].light)
    for (file, want) in [("nord.toml", "built-in theme's name"), ("Bad Name.toml", "lower case"),
                         ("badcolour.toml", "accent = \"blue\" is not a colour"), ("typo.toml", "\"backgroud\" is not a theme key"),
                         ("open.toml", "not closed"), ("nobase.toml", "based_on \"solarised\" is no built-in theme")] {
        #expect(problems.contains { $0.hasPrefix(file + ":") && $0.contains(want) }, "\(file): \(problems)")
    }
    #expect(userThemes(in: dir + "/none").themes.isEmpty && userThemes(in: dir + "/none").problems.isEmpty)

    // In the lists and lookups once read; gone again when the files are.
    ConchTheme.user = themes
    defer { ConchTheme.user = [] }
    #expect(ConchTheme.named("my-dark").label == "My Dark" && ConchTheme.everything.count == ConchTheme.all.count + 3)
}

// [ui] accent over a theme, as the TUI applies it: a name or #rrggbb, with
// white text on it; anything else leaves the theme as it was. The names
// are the TUI's own.
@Test func accentOverTheme() throws {
    let t = ConchTheme.named("nord")
    #expect(t.withAccent("orange").accent.hex == "#C2571A" && t.withAccent("orange").selFG.hex == "#FFFFFF")
    #expect(t.withAccent(" #FF00AA ").accent.hex == "#FF00AA" && t.withAccent("#ff00aa").line.hex == "#FF00AA")
    #expect(t.withAccent(nil) == t && t.withAccent("") == t && t.withAccent("chartreuse") == t && t.withAccent("#12") == t && t.withAccent("#zzzzzz") == t)
    let go = try String(contentsOfFile: #filePath.replacingOccurrences(of: "macos/Tests/ConchKitTests/WindowTests.swift", with: "internal/tui/theme.go"), encoding: .utf8)
    let block = go.components(separatedBy: "var accentColors = map[string]lipgloss.Color{")[1].components(separatedBy: "}")[0]
    var tui: [String: String] = [:]
    for m in block.matches(of: try Regex(#""(\w+)":\s*"(#[0-9A-Fa-f]{6})""#)) {
        tui[String(m.output[1].substring!)] = String(m.output[2].substring!).uppercased()
    }
    #expect(tui == ConchTheme.accentNames && !tui.isEmpty)
}

// What a file is shown as; the browser tab's key round trip; and what the
// address bar loads for what was typed.
@Test func mediaAndBrowser() {
    #expect(MediaKind(path: "a/logo.PNG") == .image && MediaKind(path: "x.svg") == .image)
    #expect(MediaKind(path: "demo.mov") == .video && MediaKind(path: "song.mp3") == .audio && MediaKind(path: "spec.pdf") == .pdf)
    #expect(MediaKind(path: "site/index.html") == .html && MediaKind(path: "main.go") == .text && MediaKind(path: "Makefile") == .text)
    #expect(MediaKind.html.needsFile && !MediaKind.text.needsFile)
    #expect(mediaHasSource("a/index.HTML") && mediaHasSource("logo.svg") && !mediaHasSource("logo.png") && !mediaHasSource("main.go"))

    for u in ["http://localhost:3000/a?b=c|d", "https://example.com/x y", "about:blank"] {
        let k = browserKey(u)
        #expect(parseBrowserKey(k) == u && k.hasPrefix("web|") && !k.dropFirst(4).contains("|"))
        #expect(!isPaneKey(k) && !isTerminalKey(k) && machine(ofKey: k) == "local")
    }
    #expect(parseBrowserKey("local|p3") == nil && parseBrowserKey("files|local|r1") == nil)

    #expect(browserURL("localhost:3000")?.absoluteString == "http://localhost:3000")
    #expect(browserURL("127.0.0.1:8080/x")?.absoluteString == "http://127.0.0.1:8080/x")
    #expect(browserURL("192.168.1.20")?.absoluteString == "http://192.168.1.20")
    #expect(browserURL("myapp.local")?.absoluteString == "http://myapp.local")
    #expect(browserURL("example.com:8443")?.absoluteString == "http://example.com:8443")
    #expect(browserURL("example.com/docs")?.absoluteString == "https://example.com/docs")
    #expect(browserURL("https://github.com/x")?.absoluteString == "https://github.com/x")
    #expect(browserURL("  http://a.b  ")?.absoluteString == "http://a.b")
    #expect(browserURL("") == nil && browserURL("hello") == nil && browserURL("two words.com") == nil)
}

@Test func mediaCachePaths() {
    let base = URL(fileURLWithPath: "/c")
    let a = mediaCachePath(base: base, machine: "busybox", path: "/home/u/app/logo.png")
    #expect(a.path.hasPrefix("/c/media/busybox/") && a.lastPathComponent == "logo.png")
    #expect(a == mediaCachePath(base: base, machine: "busybox", path: "/home/u/app/logo.png"))
    // The same name elsewhere, or on another machine, is another file.
    #expect(a != mediaCachePath(base: base, machine: "busybox", path: "/home/u/web/logo.png"))
    #expect(a.deletingLastPathComponent().lastPathComponent != mediaCachePath(base: base, machine: "box2", path: "/home/u/app/logo.png").deletingLastPathComponent().lastPathComponent)
    // A machine's name never climbs out of the cache.
    let odd = mediaCachePath(base: base, machine: "../x/y", path: "/")
    #expect(odd.path.hasPrefix("/c/media/") && !odd.pathComponents.contains("..") && odd.lastPathComponent == "file")
}

// Each file is marked by what it is; every mark is an icon that exists,
// and a colour too close to the background gives way to the text's.
@Test func fileIcons() {
    // A language's own logo, in its colour, with an outline behind it.
    #expect(fileIcon("main.go") == .logo("go") && fileIcon("main.go").color == "#00ADD8" && fileIcon("main.go").icon == "file-code")
    #expect(fileIcon("server.py").logo == "python" && fileIcon("server.py").color == "#3776AB")
    #expect(fileIcon("Dockerfile").logo == "docker" && fileIcon("docker-compose.yml").logo == "docker" && fileIcon(".dockerignore").logo == "docker")
    #expect(fileIcon("App.TSX").logo == "react" && fileIcon("api.ts").logo == "typescript" && fileIcon("index.js").logo == "javascript")
    #expect(fileIcon("go.mod").logo == "go" && fileIcon("go.sum").logo == "go" && fileIcon("Cargo.lock").logo == "rust")
    #expect(fileIcon("package.json").logo == "npm" && fileIcon("yarn.lock").logo == "yarn" && fileIcon("tsconfig.json").logo == "typescript")
    #expect(fileIcon("vite.config.ts").logo == "vite" && fileIcon(".eslintrc.json").logo == "eslint" && fileIcon(".gitignore").logo == "git")
    #expect(fileIcon("notebook.ipynb").logo == "jupyter" && fileIcon("Program.cs").logo == "dotnet" && fileIcon("main.rs").logo == "rust")
    // A logo that is black is given a colour it can be seen in.
    #expect(fileIcon("x.json").logo == "json" && fileIcon("x.json").color == "#CBCB41" && fileIcon("README.md").logo == nil)
    #expect(fileIcon("notes.md").logo == "markdown" && fileIcon("notes.md").color == "#519ABA")
    // No logo of its own: the outline for its kind.
    #expect(fileIcon("pnpm-lock.yaml").logo == "pnpm" && fileIcon("Gemfile.lock").logo == "ruby" && fileIcon("other.lock").icon == "file-lock")
    #expect(fileIcon("Makefile").icon == "file-terminal" && fileIcon("Makefile").logo == nil && fileIcon("deploy.sh").logo == "gnubash")
    #expect(fileIcon("Dockerfile").icon == "container" && fileIcon("Dockerfile.dev").logo == "docker")
    #expect(fileIcon("README.md").icon == "book-open" && fileIcon("LICENSE").icon == "scale" && fileIcon("NOTES.md").icon == "file-text")
    #expect(fileIcon(".env").icon == "file-key" && fileIcon(".env.local").icon == "file-key" && fileIcon("ca.pem").icon == "file-key")
    #expect(fileIcon(".gitignore").icon == "git-branch" && fileIcon(".prettierrc").logo == "prettier" && fileIcon(".toolrc").icon == "file-cog")
    #expect(fileIcon("logo.png").icon == "file-image" && fileIcon("demo.mp4").icon == "file-video-camera" && fileIcon("a.mp3").icon == "file-music")
    #expect(fileIcon("spec.pdf").icon == "file-text" && fileIcon("x.tar.gz").icon == "file-archive" && fileIcon("data.csv").icon == "file-spreadsheet")
    #expect(fileIcon("fix.patch").icon == "file-diff" && fileIcon("app.sqlite").logo == "sqlite" && fileIcon("Inter.woff2").icon == "file-type")
    // Nothing known: a plain page, uncoloured; no name or no extension too.
    #expect(fileIcon("mystery.xyz") == FileIcon("file") && fileIcon("") == FileIcon("file") && fileIcon("NOTICE") == FileIcon("file"))

    for n in ["main.go", "a.rs", "x.json", "y.yml", "README", "LICENSE", "Dockerfile", "go.sum", "a.png", "b.mov", "c.wav",
              "d.zip", "e.csv", "f.ttf", "g.pem", "h.diff", "i.db", ".gitignore", ".zshrc", "Makefile", "j.pdf", "k"] {
        let f = fileIcon(n)
        #expect(LucideIcons.all[f.icon] != nil, "\(n) → \(f.icon)")
        #expect(!LucideIcons.ops(f.icon).isEmpty)
    }

    let dark = RGB("#0D1117"), light = RGB("#FFFFFF"), fg = RGB("#E6EDF3"), ink = RGB("#1F2328"), muted = RGB("#7D8590")
    #expect(fileTint(fileIcon("main.go"), on: dark, text: fg, muted: muted) == RGB("#00ADD8"))
    #expect(fileTint(fileIcon("main.rs"), on: dark, text: fg, muted: muted) == fg) // Rust's black on dark
    #expect(fileTint(fileIcon("a.js"), on: light, text: ink, muted: muted) == ink) // yellow on white
    #expect(fileTint(fileIcon("notes.txt"), on: dark, text: fg, muted: muted) == muted)
}

// Every logo a file can be given exists, parses, and has a colour.
@Test func fileLogosAllDraw() {
    #expect(FileLogos.logos.count >= 60)
    for (slug, l) in FileLogos.logos {
        #expect((FileLogos.ops(slug) ?? []).count > 3, "\(slug)")
        #expect(l.hex.hasPrefix("#") && l.hex.count == 7, "\(slug)")
    }
    // Every slug the table names is one we have: a typo would draw the outline.
    let names = ["a.go", "a.py", "a.ts", "a.js", "a.jsx", "a.rs", "a.swift", "a.rb", "a.java", "a.kt", "a.c", "a.cpp", "a.cs", "a.fs",
                 "a.php", "a.html", "a.css", "a.scss", "a.less", "a.vue", "a.svelte", "a.astro", "a.lua", "a.ex", "a.erl", "a.hs",
                 "a.scala", "a.dart", "a.zig", "a.jl", "a.r", "a.pl", "a.elm", "a.graphql", "a.nix", "a.tf", "a.ml", "a.clj", "a.sol",
                 "a.sh", "a.zsh", "a.ipynb", "a.tex", "a.db", "a.mdx", "a.json", "a.yaml", "a.toml", "a.md", "Dockerfile", ".gitignore",
                 "package.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "deno.json", "go.mod", "Cargo.toml", "Package.swift",
                 "pyproject.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "flake.nix", "vite.config.ts",
                 "webpack.config.js", ".eslintrc", ".prettierrc", ".npmrc", ".nvmrc", ".yarnrc.yml", "CMakeLists.txt", "nginx.conf",
                 ".editorconfig", ".zshrc", ".bashrc"]
    for n in names {
        let f = fileIcon(n)
        #expect(f.logo != nil && FileLogos.logos[f.logo!] != nil, "\(n) → \(f.logo ?? "none")")
    }
}

// The new icons parse the way the others do: every one draws something.
@Test func lucideFileIconsParse() {
    for n in ["file", "file-code", "file-image", "file-video-camera", "file-music", "file-braces", "file-archive", "file-spreadsheet",
              "file-terminal", "file-cog", "file-lock", "file-key", "file-type", "file-diff", "file-text", "book-open", "scale",
              "package", "database", "container"] {
        let ops = LucideIcons.ops(n)
        #expect(ops.count > 3, "\(n)")
        #expect(ops != LucideIcons.ops("settings"), "\(n) fell back")
    }
}

// Work handed over from the git tab always has somewhere to go: the
// running agent, a new one in the branch's checkout, or a new one in a
// worktree made for a branch checked out nowhere.
@Test func handoffPlans() throws {
    let a = try JSONDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p7","name":"fixer","state":"running","cols":80,"rows":24}"#.utf8))
    #expect(handoffPlan(agent: a, worktree: "/w/x") == .prompt(pane: "p7"))
    #expect(handoffPlan(agent: a, worktree: nil) == .prompt(pane: "p7"))
    #expect(handoffPlan(agent: nil, worktree: "/w/x") == .start(in: "/w/x"))
    #expect(handoffPlan(agent: nil, worktree: nil) == .checkOutThenStart)
    #expect(handoffPlan(agent: nil, worktree: "") == .checkOutThenStart)
}
