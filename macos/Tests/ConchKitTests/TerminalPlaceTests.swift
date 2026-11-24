import Testing
@testable import ConchKit

private let shell = key("bot", "p3")
private let mine = key("local", "api")

// A browser names no machine: "web" was taken for one, so ⌘T did nothing.
@Test func aTerminalFromABrowserIsOnThisMac() {
    for url in ["about:blank#1", "http://localhost:3000/a|b", "web"] {
        let w = browserKey(url)
        #expect(terminalPlace(focused: w, tab: [w]) == TerminalPlace(machine: "local"))
    }
}

@Test func aTerminalFromABrowserIsWhereTheTabIs() {
    let w = browserKey("http://localhost:3000")
    #expect(terminalPlace(focused: w, tab: [w, shell]) == TerminalPlace(machine: "bot", pane: shell))
    #expect(terminalPlace(focused: w, tab: [w, browserKey("about:blank#2"), shell]).pane == shell)
    let agent = key("busybox", "p9")
    #expect(terminalPlace(focused: w, tab: [shell, agent, w], agent: agent) == TerminalPlace(machine: "busybox", pane: agent))
    // A project's window is that project's, with only pages in it.
    #expect(terminalPlace(focused: w, tab: [w], window: key("bot", "api")) == TerminalPlace(machine: "bot", project: "api"))
    #expect(terminalPlace(focused: w, tab: [w, shell], window: mine).pane == shell)
}

@Test func aTerminalFromEveryKindOfTab() {
    let api = TerminalPlace(machine: "bot", project: "api")
    #expect(terminalPlace(focused: changesKey("bot", "api", "x|y"), tab: []) == api)
    #expect(terminalPlace(focused: filesKey("bot", "api"), tab: []) == api)
    #expect(terminalPlace(focused: commitsKey("bot", "api"), tab: []) == api)
    #expect(terminalPlace(focused: sessionsKey("bot", "api"), tab: []) == api)
    #expect(terminalPlace(focused: mobileKey("bot", "api"), tab: []) == api)
    #expect(terminalPlace(focused: setupKey("bot", "api", dir: "/w/api-fix"), tab: []) == TerminalPlace(machine: "bot", project: "api", dir: "/w/api-fix"))
    #expect(terminalPlace(focused: taskKey("bot", "g", "b"), tab: []) == TerminalPlace(machine: "bot"))
    #expect(terminalPlace(focused: filesKey("", "api"), tab: []) == TerminalPlace(machine: "local", project: "api"))
    #expect(terminalPlace(focused: chatKey(shell), tab: []) == TerminalPlace(machine: "bot", pane: shell))
    #expect(terminalPlace(focused: benchShellKey(shell), tab: []) == TerminalPlace(machine: "bot", pane: shell))
}

@Test func aTerminalFromAPaneOrFromNothing() {
    #expect(terminalPlace(focused: shell, tab: [shell], window: mine) == TerminalPlace(machine: "bot", pane: shell))
    #expect(terminalPlace(focused: "p3", tab: []) == TerminalPlace(machine: "local", pane: "p3"))
    // Machines named as a kind of tab are still machines.
    #expect(terminalPlace(focused: "files|p3", tab: []) == TerminalPlace(machine: "files", pane: "files|p3"))
    #expect(terminalPlace(focused: nil, tab: []) == TerminalPlace(machine: "local"))
    #expect(terminalPlace(focused: nil, tab: [], window: key("bot", "api")) == TerminalPlace(machine: "bot", project: "api"))
}
