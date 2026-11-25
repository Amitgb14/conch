import ConchKit
import Foundation
import Testing

// Switching an agent's conversation to another agent, as the app offers
// and says it (agent.switch.v1).

private let agents = [
    AgentAvailability(name: "claude", label: "Claude Code", installed: true, switchTo: SwitchWay.native),
    AgentAvailability(name: "codex", label: "Codex", installed: true, switchTo: SwitchWay.native),
    AgentAvailability(name: "gemini", label: "Gemini CLI", installed: true, switchTo: SwitchWay.handoff),
    AgentAvailability(name: "aider", label: "Aider", installed: true, switchTo: SwitchWay.never),
    AgentAvailability(name: "kilo", label: "Kilo Code", installed: false, switchTo: SwitchWay.handoff),
]

@Test func theSwitchMenuSaysHowEachAgentGetsIt() {
    let m = switchAgentMenu(pane: "local|p12", from: "claude", agents: agents, canSwitch: true)
    #expect(m.title == "Switch Agent")
    let titles = m.children.map(\.title)
    // Not its own, not one that is not installed.
    #expect(!titles.contains { $0.hasPrefix("Claude Code  ·") || $0.hasPrefix("Kilo") })
    #expect(m.children.first { $0.title == "Codex  ·  Full history" }?.action == .switchAgent(pane: "local|p12", to: "codex"))
    #expect(m.children.first { $0.title == "Gemini CLI  ·  Handoff document" }?.action == .switchAgent(pane: "local|p12", to: "gemini"))
    // One that cannot take a first message is said, greyed.
    #expect(m.children.first { $0.title.hasPrefix("Aider") }?.action == .disabled)
    #expect(m.children.first { $0.title.hasPrefix("Aider") }?.title.contains("can't take a first message") == true)
    // The footnote.
    #expect(titles.suffix(2) == ["Switches when this turn ends.", "Claude Code's own session is kept, so you can switch back."])
}

@Test func theSwitchMenuOnAnOlderServerOrWithNowhereToGo() {
    let old = switchAgentMenu(pane: "local|p1", from: "claude", agents: agents, canSwitch: false)
    #expect(old.action == .disabled && old.children.map(\.title) == ["The server there needs a reload (conch server reload)"])
    let alone = switchAgentMenu(pane: "local|p1", from: "claude", agents: [agents[0], agents[3]], canSwitch: true)
    #expect(alone.children.contains { $0.title == "No Other Agent Installed" && $0.action == .disabled })
    // A server too old to say how: the handoff, which every agent that
    // takes a first message gets.
    let unsaid = switchAgentMenu(pane: "local|p1", from: "codex", agents: [AgentAvailability(name: "claude", label: "Claude Code")], canSwitch: true)
    #expect(unsaid.children.first?.title == "Claude Code  ·  Handoff document")
}

@Test func aWaitingSwitchIsOfferedToCallOff() {
    let m = switchAgentMenu(pane: "local|p12", from: "claude", agents: agents, canSwitch: true,
                            waiting: AgentSwitching(pane: "p12", from: "claude", to: "codex", waits: ["the turn to end"]))
    #expect(m.children.first?.title == "Call Off the Switch to Codex")
    #expect(m.children.first?.action == .cancelSwitch(pane: "local|p12"))
}

@Test func theCardSaysWhatCrosses() {
    let pre = AgentSwitchResult(from: "claude", to: "codex", crossed: SwitchWay.native,
                                crossing: SwitchCrossing(messages: 42, commands: 18, notes: 6, fits: true, bytes: 48 << 10, plan: true,
                                                         left: ["Claude Code's thinking and reasoning"]), preview: true)
    let lines = switchCardLines(pre, agents: agents)
    #expect(lines.map(\.label) == ["Carries", "Fits", "Leaves", "How", "Keeps"])
    #expect(lines[0].value == "42 messages · 18 commands with their output · 6 notes for reads and edits · the plan")
    #expect(lines[1].value == "all 48 KB, nothing shortened")
    #expect(lines[3].value == "as Codex's own session, resumed with the history")
    #expect(lines[4].value == "Claude Code's session as it is, so Switch back works at any time")

    // A handoff says why; a conversation too long says what was shortened.
    let hand = AgentSwitchResult(from: "claude", to: "gemini", crossed: SwitchWay.handoff, why: "conch does not write Gemini CLI's sessions",
                                 crossing: SwitchCrossing(messages: 1, commands: 1, notes: 1, shortened: 12, fits: false, bytes: 3 << 20))
    let h = switchCardLines(hand, agents: agents)
    #expect(h.first { $0.label == "How" }?.value == "the handoff document, as Gemini CLI's first message: conch does not write Gemini CLI's sessions")
    #expect(h.first { $0.label == "Fits" }?.value == "3.0 MB, 12 steps shortened to fit Gemini CLI's context")
    #expect(h[0].value.hasPrefix("1 message · 1 command with"))
    #expect(!h.contains { $0.label == "Leaves" })
    // None at all (an older server's answer): still a card.
    let bare = switchCardLines(AgentSwitchResult(from: "codex", to: "claude"))
    #expect(bare.first { $0.label == "Fits" }?.value == "all of it, nothing shortened")
}

@Test func theCountdownAndWhatTheCardHolds() {
    let now = Date(timeIntervalSince1970: 1000)
    var ask = SwitchAsk(pane: "local|p12", to: "codex", preview: AgentSwitchResult(from: "claude", to: "codex"),
                        stage: .counting(until: now.addingTimeInterval(switchCountdown)))
    #expect(ask.secondsLeft(now: now) == 5)
    #expect(ask.secondsLeft(now: now.addingTimeInterval(4.2)) == 1)
    #expect(ask.secondsLeft(now: now.addingTimeInterval(60)) == 0)
    #expect(!ask.held)
    ask.stage = .asking
    #expect(ask.secondsLeft(now: now) == nil && ask.held)
    ask.stage = .waiting(["the turn to end"])
    #expect(ask.held)
    ask.stage = .failed("no")
    #expect(!ask.held)
}

@Test func theBannerAfterASwitch() throws {
    let json = """
    {"id":"p12","name":"claude","state":"running","agent":{"name":"codex","state":"idle"},
     "switched_from":{"agent":"claude","session":"s1","at":"2026-11-24T10:00:00Z","crossing":{"messages":42,"commands":18,"notes":6,"fits":true,"bytes":1000}}}
    """
    let dec = JSONDecoder()
    dec.dateDecodingStrategy = .iso8601
    var p = try dec.decode(PaneInfo.self, from: Data(json.utf8))
    #expect(switchBanner(p, agents: agents) == "⇄ Continued from Claude Code · 42 messages · 18 commands · 6 notes")
    let k1 = switchBannerKey("local|p12", p)
    // Back where it started: none.
    p.switchedFrom?.back = true
    #expect(switchBanner(p) == nil)
    // An older server's origin, without counts.
    p.switchedFrom = SwitchOrigin(agent: "claude")
    #expect(switchBanner(p) == "⇄ Continued from Claude Code")
    // A later switch has a key of its own.
    p.switchedFrom = SwitchOrigin(agent: "claude", at: Date(timeIntervalSince1970: 5))
    #expect(switchBannerKey("local|p12", p) != k1)
    // Never switched, or exited: none.
    p.state = "exited"
    #expect(switchBanner(p) == nil)
    #expect(switchBanner(PaneInfo(id: "p1", name: "zsh")) == nil)
}

@Test func theEventsDecode() throws {
    let dec = JSONDecoder()
    let w = try dec.decode(AgentSwitching.self, from: Data(#"{"pane":"p12","from":"claude","to":"codex","waits":["the turn to end"]}"#.utf8))
    #expect(w == AgentSwitching(pane: "p12", from: "claude", to: "codex", waits: ["the turn to end"]))
    let failed = try dec.decode(AgentSwitchResult.self, from: Data(#"{"pane":{"id":"p12","name":"x","state":"running"},"from":"claude","to":"codex","error":"the agent is no longer running there"}"#.utf8))
    #expect(failed.error == "the agent is no longer running there" && failed.pane?.id == "p12")
    let av = try dec.decode(AgentAvailability.self, from: Data(#"{"name":"aider","installed":true,"switch_to":"never"}"#.utf8))
    #expect(av.switchTo == SwitchWay.never)
}
