import ConchKit
import Foundation
import Testing

// Another agent offered at a plan limit (agent.switch.limit.v1), as the
// app's card says it.

private let agents = [
    AgentAvailability(name: "claude", label: "Claude Code", installed: true, switchTo: SwitchWay.native),
    AgentAvailability(name: "codex", label: "Codex", installed: true, switchTo: SwitchWay.native),
]

private let now = Date(timeIntervalSince1970: 1_800_000_000)

private func offer(auto: Bool = false) -> AgentSwitchOffer {
    AgentSwitchOffer(pane: "p3", from: "claude", to: "codex", crossed: SwitchWay.native,
                     accounts: [AccountRank(account: "work", room: 0, reported: true, usable: false),
                                AccountRank(account: "", room: 3, reported: true, usable: false),
                                AccountRank(account: "new", room: 100, usable: false)],
                     resetsAt: now.addingTimeInterval(2 * 3600 + 10 * 60 + 30),
                     auto: auto ? true : nil, at: auto ? now.addingTimeInterval(4.2) : nil, setting: auto ? "codex" : nil)
}

@Test func theOfferIsReadAsTheServerSendsIt() throws {
    let json = #"{"pane":"p3","from":"claude","to":"codex","crossed":"native","accounts":[{"account":"","room":0,"reported":true,"usable":false}],"resets_at":"2026-11-25T15:00:00Z","auto":true,"at":"2026-11-25T12:00:05Z","setting":"codex"}"#
    let o = try conchDecoder().decode(AgentSwitchOffer.self, from: Data(json.utf8))
    #expect(o.to == "codex" && o.auto == true && o.at != nil && o.resetsAt != nil && o.accounts?.count == 1)
    // An offer withdrawn, from a server that says only that.
    let gone = try conchDecoder().decode(AgentSwitchOffer.self, from: Data(#"{"pane":"p3","from":"claude","gone":true}"#.utf8))
    #expect(gone.gone == true && gone.to == nil)
    // A switch made at the limit says so; one from an older server does not.
    let r = try conchDecoder().decode(AgentSwitchResult.self, from: Data(#"{"from":"claude","to":"codex","reason":"limit"}"#.utf8))
    #expect(r.reason == "limit")
    let old = try conchDecoder().decode(AgentSwitchResult.self, from: Data(#"{"from":"claude","to":"codex"}"#.utf8))
    #expect(old.reason == nil)
}

@Test func theCardOffersCodex() {
    let c = LimitOffer(pane: "local|p3", offer: offer())
    #expect(limitHeading(c.offer, agents: agents) == "Claude Code is out of plan on every account")
    // Only accounts that reported; the first reset.
    #expect(limitFullness(c.offer, now: now) == "work 100% · default 97% · resets in 2h 10m")
    let lines = limitLines(c, agents: agents, now: now)
    #expect(lines.first == "Continue in Codex? It takes the whole conversation and your last message, and carries on.")
    #expect(lines.contains("Claude Code's session stays as it is. When Claude Code has room again, Switch Back is offered."))
    #expect(!lines.contains { $0.hasPrefix("Carries") })
    #expect(limitButtons(c.offer, agents: agents) == ["Continue in Codex", "Wait for the Reset", "Choose…"])
    #expect(c.secondsLeft(now: now) == nil)

    // With the preview read, what crosses.
    var p = c
    p.preview = AgentSwitchResult(from: "claude", to: "codex", crossing: SwitchCrossing(messages: 42, commands: 18, notes: 6, fits: true, plan: true))
    #expect(limitLines(p, agents: agents, now: now).contains("Carries 42 messages · 18 commands · 6 notes · the plan"))
    p.preview?.crossing = SwitchCrossing(messages: 1, shortened: 3, fits: false)
    #expect(limitLines(p, agents: agents, now: now).contains("Carries 1 message · 0 commands · 0 notes · 3 steps shortened to fit"))
}

@Test func theCardCountsDownToTheServersSwitch() {
    let c = LimitOffer(pane: "local|p3", offer: offer(auto: true))
    #expect(c.secondsLeft(now: now) == 5)
    #expect(limitLines(c, agents: agents, now: now).first?.hasPrefix("Continuing in Codex in 5s.") == true)
    #expect(limitLines(c, agents: agents, now: now.addingTimeInterval(10)).first?.hasPrefix("Continuing in Codex now.") == true)
    #expect(c.secondsLeft(now: now.addingTimeInterval(60)) == 0)
    #expect(limitButtons(c.offer) == ["Continue Now", "Cancel", "Choose…"])
}

@Test func whyAndHandoffAreSaid() {
    var o = offer()
    o.crossed = SwitchWay.handoff
    o.why = "switch_at_limit names gemini, but Gemini CLI is not installed here"
    let lines = limitLines(LimitOffer(pane: "local|p3", offer: o), agents: agents, now: now)
    #expect(lines.first?.contains("as a document for its first message") == true)
    #expect(lines.last == "Note: switch_at_limit names gemini, but Gemini CLI is not installed here.")
    // Nothing reported, nothing to reset: nothing said.
    #expect(limitFullness(AgentSwitchOffer(pane: "p", from: "claude"), now: now) == "")
    #expect(resetsIn(30) == "in a minute" && resetsIn(6 * 60) == "in 6m" && resetsIn(26 * 3600 + 5 * 60) == "in 26h 5m")
}

@Test func theSwitchMadeAtTheLimitIsSaid() {
    let ok = AgentSwitchResult(from: "claude", to: "codex", crossed: SwitchWay.native, reason: "limit")
    #expect(limitSwitchedText(ok, agents: agents) == "Claude Code was out of plan on every account: switched to Codex, with the whole conversation. Switch Back is above it.")
    let bad = AgentSwitchResult(from: "claude", to: "codex", error: "Codex is not installed here", reason: "limit")
    #expect(limitSwitchedText(bad, agents: agents) == "The switch to Codex at the plan limit failed: Codex is not installed here")
}
