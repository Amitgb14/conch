import Foundation
import Testing
@testable import ConchKit

private let parts = BenchParts(files: filesKey("local", "pr1"), shell: "local|p9", chat: chatKey("local|p3"), agent: "local|p3")

@Test func benchLayoutSettings() {
    #expect(BenchLayout(setting: "jetbrains") == .jetbrains && BenchLayout(setting: " CHAT ") == .chat)
    #expect(BenchLayout(setting: nil) == .ide && BenchLayout(setting: "") == .ide && BenchLayout(setting: "vim") == .ide)
    #expect(BenchLayout.allCases.map(\.label) == ["IDE", "Chat First", "Agent", "JetBrains", "Mobile"])
    #expect(BenchLayout(setting: "mobile") == .mobile)
    #expect(BenchLayout.allCases.filter(\.stripes) == [.jetbrains])
    #expect(BenchLayout.allCases.allSatisfy { !$0.detail.isEmpty })
}

// Every layout holds all four parts, once each; where only one of the chat
// and the agent's terminal has room, the terminal waits hidden.
@Test func benchArrangements() {
    for l in BenchLayout.allCases {
        let (root, weights, hidden) = benchArrangement(l, parts)
        #expect(Set(root.panes) == Set(parts.all) && root.panes.count == 4, "\(l)")
        #expect(hidden.isSubset(of: Set(parts.all)) && !hidden.contains(parts.chat), "\(l)")
        for (path, w) in weights { #expect(!w.isEmpty && w.allSatisfy { $0 > 0 }, "\(l) \(path)") }
    }
    #expect(benchArrangement(.ide, parts).hidden == [parts.agent])
    #expect(benchArrangement(.chat, parts).hidden == [parts.agent])
    #expect(benchArrangement(.agent, parts).hidden.isEmpty)
    // Chat first puts the chat first; the others start with the files.
    #expect(benchArrangement(.chat, parts).root.panes.first == parts.chat)
    #expect(benchArrangement(.ide, parts).root.panes.first == parts.files)
    #expect(benchArrangement(.agent, parts).root.panes == [parts.chat, parts.agent, parts.files, parts.shell])
}

@Test func benchKeys() {
    #expect(parseChatKey(chatKey("busybox|p12")) == "busybox|p12")
    #expect(parseChatKey("chat|nobar") == nil && parseChatKey("local|p1") == nil)
    #expect(parseBenchShellKey(benchShellKey("local|p3")) == "local|p3" && parseBenchShellKey("benchshell|x") == nil)
    #expect(!isPaneKey(chatKey("local|p3")) && !isPaneKey(benchShellKey("local|p3")) && isPaneKey("local|p3"))
}

@Test func shownSharesAndKids() {
    let kids: [SplitNode] = [.leaf("a"), .split(.vertical, [.leaf("b"), .leaf("c")]), .leaf("d")]
    #expect(shownKids(kids, hidden: []) == [0, 1, 2])
    #expect(shownKids(kids, hidden: ["b"]) == [0, 1, 2]) // c still shows
    #expect(shownKids(kids, hidden: ["b", "c", "d"]) == [0])
    #expect(shownKids([], hidden: []).isEmpty)
    #expect(shownShares([0.5, 0.25, 0.25], shown: [0, 2]) == [2.0 / 3, 1.0 / 3])
    #expect(shownShares([0, 0], shown: [0, 1]) == [0.5, 0.5])
    #expect(shownShares([0.5], shown: [0, 3]) == [1, 0]) // an index past the end has no share
    #expect(shownShares([], shown: []).isEmpty)
}

@Test func openBenchTakesItsPartsAndRearranges() {
    var l = Layout()
    l.open("local|p3")             // the agent in a tab of its own
    l.open(parts.files)            // and the project's files in another
    l.open("local|p1")
    l.openBench(.ide, parts)
    #expect(l.tabs.count == 2, "the agent's and the files' tabs gave up their panes")
    #expect(l.current?.benchAgent == parts.agent && l.current?.bench == .ide)
    #expect(l.current?.focused == parts.chat)
    #expect(l.tab(showing: parts.agent) == l.active && l.current?.hidden == [parts.agent])
    #expect(l.benchParts(l.active) == parts)

    // Opened again it is the same tab, rearranged.
    l.select(0)
    l.openBench(.agent, parts)
    #expect(l.tabs.count == 2 && l.current?.bench == .agent && l.current?.hidden.isEmpty == true)
    l.setBench(.chat)
    #expect(l.current?.bench == .chat && l.current?.root.panes.first == parts.chat)
    // setBench on a tab that is not a workbench changes nothing.
    l.select(0)
    let before = l.tabs[0]
    l.setBench(.jetbrains)
    #expect(l.tabs[0] == before)
    #expect(l.benchParts(0) == nil && l.benchParts(9) == nil)
}

@Test func toggleHiddenKeepsOneShown() {
    var l = Layout()
    l.openBench(.agent, parts)
    l.focus(parts.files)
    l.toggleHidden(parts.files)
    #expect(l.current?.hidden == [parts.files] && l.current?.focused != parts.files, "the keyboard left what hid")
    l.toggleHidden(parts.files)
    #expect(l.current?.hidden.isEmpty == true)
    for leaf in [parts.files, parts.shell, parts.chat] { l.toggleHidden(leaf) }
    #expect(l.current?.shows(parts.agent) == true)
    l.toggleHidden(parts.agent)
    #expect(l.current?.shows(parts.agent) == true, "the last part shown stays")
    l.toggleHidden("local|nope") // not in the tab: nothing
    #expect(l.current?.hidden.count == 3)
    var empty = Layout()
    empty.toggleHidden("x") // no tab: nothing
    #expect(empty.tabs.isEmpty)
}

@Test func swapChatAndRevealing() {
    var l = Layout()
    l.openBench(.ide, parts)
    l.swapChat()
    #expect(l.current?.hidden == [parts.chat] && l.current?.focused == parts.agent)
    l.swapChat()
    #expect(l.current?.hidden == [parts.agent] && l.current?.focused == parts.chat)
    // Asking for the agent's pane (a click on it in the sidebar) shows it in
    // the chat's place.
    l.open(parts.agent)
    #expect(l.current?.hidden == [parts.chat] && l.current?.focused == parts.agent)
    l.focus(parts.chat)
    #expect(l.current?.hidden == [parts.agent])
    // In the Agent layout both show: nothing to swap.
    l.setBench(.agent)
    l.swapChat()
    #expect(l.current?.hidden.isEmpty == true)
    // A hidden files view asked for is shown, the chat left alone.
    l.toggleHidden(parts.files)
    l.open(parts.files)
    #expect(l.current?.hidden.isEmpty == true)
    // A tab that is no workbench: swapping does nothing.
    var plain = Layout()
    plain.open("local|p1")
    plain.swapChat()
    #expect(plain.current?.hidden.isEmpty == true)
}

@Test func replaceKeepsSharesAndPlace() {
    var l = Layout()
    let placeholder = BenchParts(files: parts.files, shell: benchShellKey(parts.agent), chat: parts.chat, agent: parts.agent)
    l.openBench(.ide, placeholder)
    l.tabs[0].weights["0"] = [0.6, 0.4]
    l.focus(benchShellKey(parts.agent))
    l.replace(benchShellKey(parts.agent), with: "local|p9")
    #expect(l.current?.root.panes.contains("local|p9") == true && l.current?.root.panes.contains(benchShellKey(parts.agent)) == false)
    #expect(l.current?.weights["0"] == [0.6, 0.4], "the shares dragged stay")
    #expect(l.current?.focused == "local|p9")
    #expect(l.benchParts(0)?.shell == "local|p9")
    // The new pane is taken from any tab it was in.
    l.open("local|p10")
    l.select(0)
    l.toggleHidden("local|p9")
    l.replace("local|p9", with: "local|p10")
    #expect(l.tabs.count == 1 && l.current?.hidden.contains("local|p10") == true && !(l.current?.hidden.contains("local|p9") ?? true))
    l.replace("local|gone", with: "local|p11") // not shown anywhere: nothing
    #expect(l.tab(showing: "local|p11") == nil)
}

@Test func framesSkipHidden() {
    var l = Layout()
    l.openBench(.ide, parts)
    let f = l.current!.frames()
    #expect(f[parts.agent] == nil, "a hidden part has no frame")
    #expect(Set(f.keys) == [parts.files, parts.shell, parts.chat])
    let width = f.values.filter { $0.minY == 0 }.reduce(0) { $0 + $1.width }
    #expect(abs(width - 1) < 1e-9, "the shown parts fill the width")
    #expect(abs(f[parts.chat]!.width - 0.32) < 1e-9)
}

@Test func resizeBetweenShown() {
    var t = Tab(pane: "a")
    t.root = .split(.horizontal, [.leaf("a"), .leaf("b"), .leaf("c")])
    t.weights[""] = [0.5, 0.25, 0.25]
    // b hidden: a and c share 0.75; moving their edge by a tenth of the shown length.
    t.resize("", n: 3, between: 0, and: 2, shown: [0, 2], by: 0.1)
    let w = t.shares("", 3)
    #expect(abs(w[0] - 0.575) < 1e-9 && abs(w[2] - 0.175) < 1e-9 && w[1] == 0.25)
    // Kept to a minimum either side.
    t.resize("", n: 3, between: 0, and: 2, shown: [0, 2], by: 5)
    #expect(t.shares("", 3)[2] > 0)
    // Not both shown, or the same one: nothing.
    let before = t.weights
    t.resize("", n: 3, between: 0, and: 1, shown: [0, 2], by: 0.1)
    t.resize("", n: 3, between: 0, and: 0, shown: [0, 2], by: 0.1)
    #expect(t.weights == before)
}

@Test func chatBlocksGroupTools() {
    let turns = [
        ChatTurn(role: "user", text: "Add a workbench"),
        ChatTurn(role: "assistant", text: "Reading first."),
        ChatTurn(role: "tool", text: "Read", detail: "a.swift"),
        ChatTurn(role: "tool", text: "Edit", detail: "a.swift"),
        ChatTurn(role: "assistant", text: "Done."),
        ChatTurn(role: "tool", text: "Bash", detail: "swift test"),
        ChatTurn(role: "user", text: ""),         // nothing typed: dropped
        ChatTurn(role: "system", text: "ignored"),
    ]
    let b = chatBlocks(turns)
    #expect(b.count == 5)
    #expect(b[0] == .user("Add a workbench", 0) && b[1] == .agent("Reading first.", 1))
    if case .tools(let run, let at) = b[2] { #expect(run.count == 2 && at == 2) } else { Issue.record("no tool run") }
    if case .tools(let run, _) = b[4] { #expect(run.map(\.text) == ["Bash"]) } else { Issue.record("no last run") }
    #expect(Set(b.map(\.id)).count == b.count, "ids are unique")
    #expect(chatBlocks([]).isEmpty)
}

@Test func toolNames() {
    #expect(toolSymbol("Read") == "doc.text" && toolSymbol("MultiEdit") == "pencil" && toolSymbol("Bash") == "terminal")
    #expect(toolSymbol("mcp__jira__search") == "puzzlepiece.extension" && toolSymbol("Whatever") == "wrench.and.screwdriver")
    #expect(toolLabel("mcp__jira__get_issue") == "jira · get_issue")
    #expect(toolLabel("mcp__x") == "mcp__x" && toolLabel("Read") == "Read")
    let run = [ChatTurn(role: "tool", text: "Read"), ChatTurn(role: "tool", text: "Read"), ChatTurn(role: "tool", text: "Bash"),
               ChatTurn(role: "tool", text: "Grep"), ChatTurn(role: "tool", text: "Edit"), ChatTurn(role: "tool", text: "Task")]
    #expect(toolsSummary(run) == "Read 2 files, ran 1 command, searched once, edited 1 file, used 1 tool")
    #expect(toolsSummary([ChatTurn(role: "tool", text: "Bash"), ChatTurn(role: "tool", text: "Bash")]) == "Ran 2 commands")
    #expect(toolsSummary([]) == "")
    #expect(agentDisplayName("claude") == "Claude" && agentDisplayName("opencode") == "OpenCode" && agentDisplayName("pi") == "Pi")
}

@Test func agentChatDecodes() throws {
    let json = #"{"agent":"claude","session":"c1","turns":[{"role":"user","text":"hi","time":"2026-11-11T10:00:00.123456789Z"},{"role":"tool","text":"Read","detail":"/a"}]}"#
    let c = try conchDecoder().decode(AgentChat.self, from: Data(json.utf8))
    #expect(c.agent == "claude" && c.turns?.count == 2 && c.turns?[0].time != nil && c.turns?[1].detail == "/a")
    // An older or empty answer: no turns, no session.
    let e = try conchDecoder().decode(AgentChat.self, from: Data(#"{"agent":"codex"}"#.utf8))
    #expect(e.turns == nil && e.session == nil)
}

@Test func jetbrainsStyle() {
    let j = AppStyle(setting: "jetbrains")
    #expect(j == .jetbrains && j.label == "JetBrains" && j.theme == "jetbrains")
    #expect(j.corner == 0 && j.gutter < AppStyle.standard.gutter && j.border == 1 && !j.mono && !j.roster && j.paintsTitleBar)
    let dark = ConchTheme.named("jetbrains"), light = ConchTheme.named("jetbrains-light")
    #expect(dark.bg.hex == "#1E1F22" && dark.accent.hex == "#3574F0" && !dark.light)
    #expect(light.bg.hex == "#FFFFFF" && light.light)
    #expect(ConchTheme.forAppearance(dark, dark: false).id == "jetbrains-light" && ConchTheme.forAppearance(dark, dark: true).id == "jetbrains")
    #expect(ConchTheme.forAppearance(light, dark: true).id == "jetbrains-light", "light picked by name stays light")
}

@Test func filesSideNarrows() {
    #expect(filesSideWidth(search: false, card: 1200) == 300 && filesSideWidth(search: true, card: 1200) == 380)
    #expect(filesSideWidth(search: false, card: 500) == 225)
    #expect(filesSideWidth(search: false, card: 200) == 170, "never under 170")
    #expect(filesSideWidth(search: false, card: 0) == 300, "before it is measured")
}

@Test func reusingAShell() {
    func shell(_ id: String, cwd: String = "/w", command: [String]? = ["/bin/zsh", "-l"], at: Double = 0) -> PaneInfo {
        var p = PaneInfo(id: id, name: "zsh", cwd: cwd)
        p.command = command
        p.created = Date(timeIntervalSince1970: at)
        return p
    }
    var agent = PaneInfo(id: "p1", name: "claude", agent: AgentStatus(name: "claude", state: .idle), cwd: "/w")
    agent.command = ["claude"]
    var helper = shell("p5", at: 9)
    helper.createdBy = "p1"
    var ended = shell("p6", at: 8)
    ended.state = "exited"
    let vim = shell("p7", command: ["vim", "a.go"], at: 7)
    let panes = [agent, shell("p2", at: 1), shell("p3", at: 3), shell("p4", cwd: "/elsewhere", at: 4), helper, ended, vim, shell("p8", command: ["-bash"], at: 2)]
    #expect(reusableShell(panes, cwd: "/w", shown: { _ in false })?.id == "p3", "the newest idle shell there")
    #expect(reusableShell(panes, cwd: "/w", shown: { $0 == "p3" })?.id == "p8", "a login shell counts; one shown elsewhere does not")
    #expect(reusableShell(panes, cwd: "/w", shown: { ["p2", "p3", "p8"].contains($0) }) == nil)
    #expect(reusableShell(panes, cwd: nil, shown: { _ in false }) == nil && reusableShell(panes, cwd: "", shown: { _ in false }) == nil)
    #expect(reusableShell([], cwd: "/w", shown: { _ in false }) == nil)
}

@Test func chatLogMergesUpdates() {
    var log = ChatLog()
    #expect(!log.loaded)
    let first = AgentChat(agent: "claude", session: "s1", turns: [
        ChatTurn(seq: 0, role: "user", text: "hi"),
        ChatTurn(seq: 1, role: "tool", text: "Bash", done: false),
    ], epoch: "e1", ver: 5, reset: true)
    let c1 = log.apply(first)
    #expect(c1)
    #expect(log.loaded && log.turns.count == 2 && log.ver == 5 && log.epoch == "e1")
    // An update with reset left out (the server omits false): the tool's
    // result replaces its turn, a new turn is added.
    let up = AgentChat(agent: "claude", session: "s1", turns: [
        ChatTurn(seq: 1, role: "tool", text: "Bash", output: "ok", done: true),
        ChatTurn(seq: 2, role: "assistant", text: "Done."),
    ], epoch: "e1", ver: 7)
    let c2 = log.apply(up)
    #expect(c2)
    #expect(log.turns.map(\.seq) == [0, 1, 2] && log.turns[1].output == "ok" && log.ver == 7)
    // Nothing changed: says so.
    let c3 = log.apply(AgentChat(agent: "claude", session: "s1", turns: [], epoch: "e1", ver: 7))
    #expect(!c3)
    // A turn arriving out of order goes in its place.
    log.apply(AgentChat(agent: "claude", session: "s1", turns: [ChatTurn(seq: 4, role: "user", text: "b")], epoch: "e1", ver: 8))
    log.apply(AgentChat(agent: "claude", session: "s1", turns: [ChatTurn(seq: 3, role: "user", text: "a")], epoch: "e1", ver: 9))
    #expect(log.turns.map(\.seq) == [0, 1, 2, 3, 4])
    // Another log (the server restarted, a new conversation): replaced.
    log.apply(AgentChat(agent: "claude", session: "s1", turns: [ChatTurn(seq: 0, role: "user", text: "x")], epoch: "e2", ver: 1, reset: true))
    #expect(log.turns.count == 1 && log.epoch == "e2")
    log.apply(AgentChat(agent: "claude", session: "s2", turns: [], epoch: "e2", ver: 2))
    #expect(log.turns.isEmpty && log.session == "s2", "a new session starts empty")
    // A server that keeps no versions sends everything: replaced each time.
    var old = ChatLog()
    old.apply(AgentChat(agent: "codex", session: "x", turns: [ChatTurn(role: "user", text: "a")]))
    old.apply(AgentChat(agent: "codex", session: "x", turns: [ChatTurn(role: "user", text: "a"), ChatTurn(role: "assistant", text: "b")]))
    #expect(old.turns.count == 2 && old.epoch == nil)
}

@Test func chatLogKnowsWhatWasSent() {
    var log = ChatLog()
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 0, role: "user", text: "fix it")], epoch: "e", ver: 1, reset: true))
    let at = log.mark
    #expect(at == ChatMark(seq: 0, count: 1))
    #expect(!log.has(sent: "fix it", after: at), "the same words said before do not count")
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 1, role: "user", text: "fix it\n")], epoch: "e", ver: 2))
    #expect(log.has(sent: " fix it", after: at))
    #expect(!log.has(sent: "other", after: ChatMark(count: 0)) && !log.has(sent: "fix it", after: ChatMark(count: 9)))
    #expect(!log.has(sent: "fix it", after: ChatMark(seq: 5, count: 0)))
}

// A message sent with the log full (1500 turns kept): the turn that
// answers it pushes the oldest out, and it must still be seen to have
// arrived — or "Sent" never clears, Stop shows for ever, and the chat is
// asked for every second.
@Test func chatLogKnowsWhatWasSentAtTheCap() {
    var log = ChatLog()
    let full = (0..<chatLogMax).map { ChatTurn(seq: $0, role: "assistant", text: "t\($0)") }
    log.apply(AgentChat(agent: "claude", session: "s", turns: full, epoch: "e", ver: 1, reset: true))
    let at = log.mark
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: chatLogMax, role: "user", text: "go on")], epoch: "e", ver: 2))
    #expect(log.turns.count == chatLogMax && log.trimmed == 1)
    #expect(log.has(sent: "go on", after: at))
    #expect(!chatBusy(state: .idle, pending: 0, turns: log.turns + [ChatTurn(seq: chatLogMax + 1, role: "assistant", text: "ok")]))

    // A server that numbers no turns: counted, trimmed turns included.
    var old = ChatLog()
    old.apply(AgentChat(agent: "claude", session: "s", turns: (0..<chatLogMax).map { _ in ChatTurn(role: "assistant", text: "x") }, epoch: "e", ver: 1, reset: true))
    let oldAt = old.mark
    #expect(oldAt == ChatMark(seq: nil, count: chatLogMax))
    old.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(role: "assistant", text: "y"), ChatTurn(role: "user", text: "go on")], epoch: "e", ver: 2))
    #expect(old.trimmed == 2 && old.has(sent: "go on", after: oldAt))
    #expect(!old.has(sent: "y", after: old.mark))
    // Replaced whole: nothing counted as trimmed any more.
    old.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(role: "user", text: "a")], epoch: "e2", ver: 1, reset: true))
    #expect(old.trimmed == 0 && old.mark.count == 1)
}

@Test func chatBlocksNotesAndSeqs() {
    let b = chatBlocks([ChatTurn(seq: 10, role: "user", text: "a"), ChatTurn(seq: 11, role: "note", text: "Interrupted"),
                        ChatTurn(seq: 12, role: "tool", text: "Read"), ChatTurn(seq: 13, role: "tool", text: "Edit")])
    #expect(b.count == 3 && b[0].id == 10 && b[1] == .note("Interrupted", 11))
    if case .tools(let run, let at) = b[2] { #expect(run.count == 2 && at == 12) } else { Issue.record("no run") }
}

@Test func chatTurnDecodesToolFields() throws {
    let json = #"{"agent":"claude","epoch":"e","ver":3,"turns":[{"seq":4,"role":"tool","text":"Edit","detail":"/a","input":"- a\n+ b","input_kind":"diff","output":"ok","error":true,"done":true}]}"#
    let c = try conchDecoder().decode(AgentChat.self, from: Data(json.utf8))
    let t = try #require(c.turns?.first)
    #expect(c.epoch == "e" && c.ver == 3 && c.reset == nil)
    #expect(t.seq == 4 && t.inputKind == "diff" && t.output == "ok" && t.error == true && t.done == true)
}

@Test func markdownBlocksOfAnAnswer() {
    let md = """
    # Title
    Some **bold** text
    carried on.

    - one
    - two
      wrapped
      - nested
    1. first
    2) second
    - [ ] todo
    - [x] done

    ```swift
    let x = 1
    ```

    > quoted
    > more

    | A | B |
    |---|:-:|
    | 1 | 2 |
    | 3 |

    ---
    #nothash
    """
    let b = markdownBlocks(md)
    #expect(b[0] == .heading(1, "Title"))
    #expect(b[1] == .paragraph("Some **bold** text\ncarried on."))
    #expect(b[2] == .list([MDItem(depth: 0, marker: "•", text: "one"), MDItem(depth: 0, marker: "•", text: "two wrapped"),
                           MDItem(depth: 1, marker: "•", text: "nested"), MDItem(depth: 0, marker: "1.", text: "first"),
                           MDItem(depth: 0, marker: "2.", text: "second"), MDItem(depth: 0, marker: "☐", text: "todo"),
                           MDItem(depth: 0, marker: "☑", text: "done")]))
    #expect(b[3] == .code("swift", "let x = 1"))
    #expect(b[4] == .quote("quoted\nmore"))
    #expect(b[5] == .table(["A", "B"], [["1", "2"], ["3"]]))
    #expect(b[6] == .rule)
    #expect(b[7] == .paragraph("#nothash"))
    #expect(b.count == 8)
    // An unclosed fence runs to the end; nothing gives nothing.
    #expect(markdownBlocks("```\ncode") == [.code("", "code")])
    #expect(markdownBlocks("").isEmpty && markdownBlocks("\n\n").isEmpty)
    // A pipe line without a divider under it is text.
    #expect(markdownBlocks("| not a table") == [.paragraph("| not a table")])

}

@Test func chatBusyWhenStopCanEndSomething() {
    let asked = ChatTurn(seq: 0, role: "user", text: "fix it")
    let said = ChatTurn(seq: 1, role: "assistant", text: "done")
    let running = ChatTurn(seq: 2, role: "tool", text: "Bash", done: false)
    let ran = ChatTurn(seq: 2, role: "tool", text: "Bash", done: true)
    let stopped = ChatTurn(seq: 3, role: "note", text: "Interrupted")
    // Nothing said, nothing sent: nothing to stop.
    #expect(!chatBusy(state: nil, pending: 0, turns: []))
    #expect(!chatBusy(state: .idle, pending: 0, turns: [asked, said]))
    #expect(!chatBusy(state: .done, pending: 0, turns: [asked, said]))
    // conch says it works, whatever the transcript says.
    #expect(chatBusy(state: .working, pending: 0, turns: []))
    // A message sent and not yet in the transcript, before conch's state catches up.
    #expect(chatBusy(state: .idle, pending: 1, turns: [asked, said]))
    // The transcript ends in a question not yet answered, or a tool still running.
    #expect(chatBusy(state: .idle, pending: 0, turns: [asked]))
    #expect(chatBusy(state: .idle, pending: 0, turns: [asked, running]))
    #expect(!chatBusy(state: .idle, pending: 0, turns: [asked, ran]))
    // A tool from an older server, which says nothing of done.
    #expect(!chatBusy(state: .idle, pending: 0, turns: [asked, ChatTurn(seq: 2, role: "tool", text: "Bash")]))
    // Stopped: Stop goes away.
    #expect(!chatBusy(state: .idle, pending: 0, turns: [asked, stopped]))
    // Waiting on a question is answered, not stopped.
    #expect(!chatBusy(state: .blocked, pending: 0, turns: [asked, said]))
}

// The chat is on unless CONCH_CHAT or [app] chat says off (it was off in
// 0.2.0, the other way round); the variable wins over the file either way.
@Test func chatSwitch() {
    #expect(chatEnabled(toml: nil, env: [:]))
    #expect(chatEnabled(toml: "", env: [:]))
    #expect(chatEnabled(toml: "[app]\ntabs = \"left\"\n", env: [:]))
    #expect(chatEnabled(toml: "[app]\nchat = true\n", env: [:]))
    #expect(!chatEnabled(toml: "[app]\nchat = false\n", env: [:]))
    #expect(chatEnabled(toml: "[cloud]\nchat = false\n", env: [:]), "another section's key")
    for on in ["1", "true", "YES", " on ", "2"] { #expect(chatEnabled(toml: "[app]\nchat = false\n", env: [chatEnv: on]), "\(on)") }
    for off in ["0", "false", "OFF", " no "] { #expect(!chatEnabled(toml: "[app]\nchat = true\n", env: [chatEnv: off]), "\(off)") }
    #expect(!chatEnabled(toml: "[app]\nchat = false\n", env: [chatEnv: ""]), "blank is unset")
}

// Without the chat every layout shows the agent's own terminal where the
// chat was, Chat First is not offered, and a saved Chat First is IDE.
@Test func benchWithoutChat() {
    let p = BenchParts(files: "files|local|r1", shell: "local|p2", chat: chatKey("local|p1"), agent: "local|p1")
    for l in BenchLayout.allCases {
        let (root, _, hidden) = benchArrangement(l, p, chat: false)
        #expect(hidden == [p.chat], "\(l)")
        #expect(Set(root.panes) == Set(p.all), "\(l): every part still in the tree")
    }
    #expect(BenchLayout.offered(chat: false) == [.ide, .agent, .jetbrains, .mobile])
    #expect(BenchLayout.offered(chat: true) == BenchLayout.allCases)
    #expect(BenchLayout.chat.shown(chat: false) == .ide && BenchLayout.chat.shown(chat: true) == .chat)

    var layout = Layout()
    layout.openBench(.chat, p, chat: false)
    let t = layout.tabs[layout.active]
    #expect(t.bench == .ide && !t.shows(p.chat) && t.shows(p.agent) && t.focused == p.agent)
    layout.setBench(.agent, chat: true)
    #expect(layout.tabs[layout.active].shows(p.chat), "turned back on")

    #expect(!paletteCommandItems(chat: false).contains { $0.title.contains("Chat") }, "no chat named in ⌘K")
    #expect(paletteCommandItems(chat: true).contains { $0.title.contains("Chat First") })
}

// A workbench tab that came back showing its chat — restored, another
// workspace's, opened before the chat was turned off — loses it: the
// agent's own terminal takes its place, keyboard and all. Seen in 0.2.0's
// hands-on run: the chat still on screen with the switch off.
@Test func hideChatInTabsThatShowIt() {
    let p = BenchParts(files: "files|local|r1", shell: "local|p2", chat: chatKey("local|p1"), agent: "local|p1")
    let q = BenchParts(files: "files|local|r2", shell: "local|p4", chat: chatKey("local|p3"), agent: "local|p3")
    var layout = Layout()
    layout.openBench(.chat, p, chat: true)   // as an older build left it
    layout.tabs[layout.active].focused = p.chat
    layout.openBench(.ide, q, chat: true)
    layout.open("local|p9")                  // a plain tab beside them
    #expect(layout.showsChat)

    layout.hideChat()
    #expect(!layout.showsChat)
    for (i, parts) in [(0, p), (1, q)] {
        let t = layout.tabs[i]
        #expect(!t.shows(parts.chat) && t.shows(parts.agent), "tab \(i)")
        #expect(t.bench != .chat, "tab \(i): Chat First is IDE")
    }
    #expect(layout.tabs[0].focused == p.agent, "the keyboard goes to the agent")
    #expect(layout.tabs[2].root.panes == ["local|p9"], "other tabs untouched")

    let before = layout
    layout.hideChat()
    #expect(layout == before, "nothing to do the second time")
}

// A project's app on a phone as a fifth part: shown beside the editor and
// the chat in the Mobile layout, waiting hidden in the others, so a click
// in the bar brings it out wherever the workbench is.
private let phoneParts = BenchParts(files: parts.files, shell: parts.shell, chat: parts.chat, agent: parts.agent, phone: mobileKey("local", "pr1"))

@Test func benchWithAPhone() {
    #expect(phoneParts.all.count == 5 && phoneParts.all.last == mobileKey("local", "pr1"))
    for l in BenchLayout.allCases {
        let (root, weights, hidden) = benchArrangement(l, phoneParts)
        #expect(Set(root.panes) == Set(phoneParts.all) && root.panes.count == 5, "\(l)")
        #expect(!hidden.contains(phoneParts.chat), "\(l)")
        #expect(hidden.contains(phoneParts.phone!) == (l != .mobile), "\(l): the phone shows in Mobile alone")
        for (path, w) in weights { #expect(!w.isEmpty && w.allSatisfy { $0 > 0 }, "\(l) \(path)") }
        // Every split's shares are one a child.
        func check(_ n: SplitNode, _ path: String) {
            guard case .split(_, let kids) = n else { return }
            if let w = weights[path] { #expect(w.count == kids.count, "\(l) \(path)") }
            for (i, k) in kids.enumerated() { check(k, path.isEmpty ? "\(i)" : "\(path).\(i)") }
        }
        check(root, "")
    }
    // Mobile: files and the terminal, the chat, then the phone at the end;
    // the agent's terminal waits behind the chat.
    let m = benchArrangement(.mobile, phoneParts)
    #expect(m.root.panes == [parts.files, parts.shell, parts.chat, parts.agent, phoneParts.phone!])
    #expect(m.hidden == [parts.agent])
    // The phone's column is narrower than the editor's.
    let shares = shownShares(m.weights[""]!, shown: shownKids({ if case .split(_, let k) = m.root { return k }; return [] }(), hidden: m.hidden))
    #expect(shares.count == 3 && shares[2] < shares[0] && shares[2] > 0.1)
    // Without a phone (another machine's agent) Mobile is IDE.
    #expect(benchArrangement(.mobile, parts).root == benchArrangement(.ide, parts).root)
    #expect(benchArrangement(.mobile, parts).hidden == benchArrangement(.ide, parts).hidden)
}

@Test func benchPartsFindThePhone() {
    var l = Layout()
    l.openBench(.ide, phoneParts)
    // The phone is not taken for the terminal, whatever order the leaves are in.
    #expect(l.benchParts(l.active) == phoneParts)
    #expect(l.current?.shows(phoneParts.phone!) == false)
    l.setBench(.mobile)
    #expect(l.current?.bench == .mobile && l.current?.shows(phoneParts.phone!) == true)
    l.toggleHidden(phoneParts.phone!)
    #expect(l.current?.shows(phoneParts.phone!) == false)
    // A workbench without one reads as before.
    var plain = Layout()
    plain.openBench(.mobile, parts)
    #expect(plain.benchParts(plain.active) == parts)
}

// The chat off and a phone on: Mobile keeps its phone beside the files,
// the agent's own terminal in the chat's place; the other layouts keep the
// phone hidden as before; and hiding the chat keeps a workbench's phone.
@Test func mobileWithoutChat() {
    let p = BenchParts(files: "files|local|r1", shell: "local|p2", chat: chatKey("local|p1"), agent: "local|p1", phone: mobileKey("local", "r1"))
    let m = benchArrangement(.mobile, p, chat: false)
    #expect(m.hidden == [p.chat] && Set(m.root.panes) == Set(p.all))
    for l in BenchLayout.offered(chat: false) where l != .mobile {
        let (root, _, hidden) = benchArrangement(l, p, chat: false)
        #expect(hidden == [p.chat, p.phone!], "\(l)")
        #expect(Set(root.panes) == Set(p.all), "\(l)")
    }
    var layout = Layout()
    layout.openBench(.mobile, p, chat: true)
    #expect(layout.current?.shows(p.chat) == true && layout.current?.shows(p.phone!) == true)
    layout.hideChat()
    #expect(layout.current?.shows(p.chat) == false && layout.current?.shows(p.agent) == true)
    #expect(layout.current?.shows(p.phone!) == true && layout.benchParts(layout.active) == p, "the phone survives hiding the chat")
}
