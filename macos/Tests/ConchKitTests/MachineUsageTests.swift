import ConchKit
import Foundation
import Testing

// Plans by machine: a remote machine's agents report to their own server,
// and the status bar shows the machine in view's.

private func plan(_ agent: String, _ account: String? = nil, _ pct: Double) -> PlanLimits {
    PlanLimits(agent: agent, account: account, fiveHour: .init(usedPct: pct))
}

@Test func plansAreKeptPerMachine() {
    var p = MachinePlans()
    #expect(p.on("busybox").isEmpty)
    p.set([plan("claude", nil, 80)], on: "local")
    p.set([plan("claude", nil, 2)], on: "busybox")
    // The same agent on two machines is two plans, not one replacing the other.
    #expect(p.on("local") == [plan("claude", nil, 80)])
    #expect(p.on("busybox") == [plan("claude", nil, 2)])
    #expect(p.on("other").isEmpty)
}

@Test func aReportReplacesOnlyItsAccountOnItsMachine() {
    var p = MachinePlans()
    // The first report may come before the machine was ever asked.
    p.report(plan("claude", nil, 2), on: "busybox")
    p.report(plan("claude", "work", 30), on: "busybox")
    p.report(plan("claude", nil, 50), on: "local")
    p.report(plan("claude", "", 7), on: "busybox") // "" is the agent's own, as nil is
    #expect(p.on("busybox") == [plan("claude", "work", 30), plan("claude", "", 7)])
    #expect(p.on("local") == [plan("claude", nil, 50)])
}

@Test func askingAgainReplacesWhatWasKept() {
    var p = MachinePlans()
    p.set([plan("claude", nil, 2), plan("codex", nil, 9)], on: "busybox")
    p.set([plan("codex", nil, 10)], on: "busybox")
    #expect(p.on("busybox") == [plan("codex", nil, 10)])
    p.set([], on: "busybox") // a server started again has heard nothing yet
    #expect(p.on("busybox").isEmpty)
}

@Test func aMachineTakenOutTakesItsPlans() {
    var p = MachinePlans()
    p.set([plan("claude", nil, 1)], on: "local")
    p.set([plan("claude", nil, 2)], on: "gone")
    p.keep(only: ["local", "new"])
    #expect(p.on("gone").isEmpty && p.on("local").count == 1)
}

@Test func usageIsTheMachineInViews() {
    let known: Set<String> = ["local", "busybox", "box2"]
    func at(_ typing: Bool, _ row: String?, _ tab: String?) -> String {
        usageMachine(typing: typing, row: row, focusedTab: tab, known: known)
    }
    #expect(at(false, nil, nil) == "local")
    #expect(at(false, nil, "busybox|p1") == "busybox")
    #expect(at(false, "box2", "busybox|p1") == "box2")
    // The agent being typed into wins over a row the cursor was left on.
    #expect(at(true, "box2", "busybox|p1") == "busybox")
    #expect(at(true, "box2", nil) == "box2")
    #expect(at(true, nil, "local|p1") == "local")
    // Tabs that are not panes.
    #expect(at(false, nil, changesKey("busybox", "proj", "main")) == "busybox")
    #expect(at(false, nil, chatKey("busybox|p1")) == "busybox")
    #expect(at(true, "box2", chatKey("busybox|p1")) == "busybox")
    #expect(at(false, nil, browserKey("http://localhost:3000")) == "local")
    // A machine no longer there.
    #expect(at(false, "gone", nil) == "local")
    #expect(at(false, nil, "gone|p1") == "local")
}

@Test func whyAMachineShowsNoUsage() {
    let cap = agentLimitsCapability
    #expect(usageGap(machine: "busybox", label: "Busy", capabilities: [cap], needs: cap) == nil)
    #expect(usageGap(machine: "busybox", label: "Busy", capabilities: nil, needs: cap) == "Busy is not connected.")
    // A remote's reload runs the same old binary again: it needs an upgrade.
    let old = usageGap(machine: "busybox", label: "Busy", capabilities: [], needs: cap) ?? ""
    #expect(old.contains("Busy") && old.contains("conch machine upgrade busybox") && !old.contains("reload"))
    let here = usageGap(machine: "local", label: "Local", capabilities: [], needs: cap) ?? ""
    #expect(here.contains("reload its server") && !here.contains("upgrade"))
    #expect(usageGap(machine: "local", label: "Local", capabilities: nil, needs: cap) == "This Mac is not connected.")
}
