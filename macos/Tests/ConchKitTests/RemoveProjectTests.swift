import ConchKit
import Testing

// Removing a project says what still runs in it, and offers to close it.

private let api = ProjectInfo(id: "r1", name: "api", path: "/src/api")

@Test func removingAProjectWithPanesRunning() {
    let panes = [
        PaneInfo(id: "p1", name: "claude", agent: AgentStatus(name: "claude", state: .idle), projectID: "r1"),
        PaneInfo(id: "p2", name: "codex", agent: AgentStatus(name: "codex", state: .idle), projectID: "r1"),
        PaneInfo(id: "p3", name: "zsh", projectID: "r1"),
        PaneInfo(id: "p4", name: "zsh", state: "exited", projectID: "r1"),
        PaneInfo(id: "p5", name: "claude", agent: AgentStatus(name: "claude", state: .idle), projectID: "r2"),
    ]
    let w = removeProjectWords(api, panes: panes, canClose: true)
    #expect(w.info.hasSuffix("Its 2 agents and 1 terminal keep running unless you close them with it."))
    #expect(w.close == "Remove and Close Them")
    // One pane: "It".
    #expect(removeProjectWords(api, panes: [panes[2]], canClose: true).close == "Remove and Close It")
    #expect(removeProjectWords(api, panes: [panes[0]], canClose: true).info.contains("Its 1 agent keep"))
    // An older server would ignore it: said, not offered.
    let old = removeProjectWords(api, panes: panes, canClose: false)
    #expect(old.close == nil && old.info.contains("keep running"))
}

@Test func removingAProjectWithNothingRunning() {
    let w = removeProjectWords(api, panes: [PaneInfo(id: "p4", name: "zsh", state: "exited", projectID: "r1")], canClose: true)
    #expect(w.close == nil && !w.info.contains("keep running") && w.info.hasPrefix("/src/api"))
}
