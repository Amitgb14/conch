import ConchKit
import Foundation
import Testing

// A plan window that has turned over says nothing about now, and one
// reported without a reset has not turned over.

private let now = Date(timeIntervalSince1970: 1_795_000_000)

/// As Go writes a window with no reset time: omitempty keeps a time.Time.
private func unknownReset() throws -> PlanLimits {
    let json = """
    {"agent":"codex","week":{"used_pct":97,"resets_at":"0001-01-01T00:00:00Z"},
     "windows":[{"key":"seven_day","used_pct":97,"resets_at":"0001-01-01T00:00:00Z"}]}
    """
    return try conchDecoder().decode(PlanLimits.self, from: Data(json.utf8))
}

@Test func anUnknownResetIsNotAPastOne() throws {
    let l = try unknownReset()
    #expect(agentAccountUsage(l, now: now) == "7d 97%")
    #expect(agentAccountUsed(l, now: now) == 97)
    var seen: [String: Int] = [:]
    #expect(limitCrossings(&seen, machine: "local", l, thresholds: [75, 90], now: now).map(\.pct) == [97])
}

@Test func anAlertWithoutAResetIsRaisedOnce() throws {
    let l = try unknownReset()
    #expect(l.week?.resetsAt == nil && l.windows?.first?.resetsAt == nil)
    var seen: [String: Int] = [:]
    let first = limitCrossings(&seen, machine: "local", l, thresholds: [90], now: now)
    #expect(first.first?.text(now: now) == "Codex week limit 97% used")
    var kept = pruneLimitAlerts(seen, now: now)
    #expect(limitCrossings(&kept, machine: "local", l, thresholds: [90], now: now).isEmpty)
}

@Test func aWindowIsLiveUntilItsReset() {
    #expect(windowLive(nil, now: now))
    #expect(windowLive(now.addingTimeInterval(1), now: now))
    #expect(!windowLive(now, now: now))
    #expect(!windowLive(now.addingTimeInterval(-1), now: now))
}

@Test func barsLeaveOutAWindowThatHasReset() {
    let past = now.addingTimeInterval(-60), ahead = now.addingTimeInterval(3600)
    let both = PlanLimits(agent: "claude", fiveHour: .init(usedPct: 96, resetsAt: past), week: .init(usedPct: 8, resetsAt: ahead))
    #expect(both.bars(now: now) == [PlanBarWindow(label: "7d", usedPct: 8)])
    #expect(both.bars(now: past.addingTimeInterval(-1)).map(\.label) == ["5h", "7d"])
    #expect(!both.barsReset(now: now))

    let none = PlanLimits(agent: "claude", fiveHour: .init(usedPct: 96, resetsAt: past), week: .init(usedPct: 99, resetsAt: past))
    #expect(none.bars(now: now).isEmpty)
    #expect(none.barsReset(now: now))
    // Still an account that reported: the chip says reset rather than
    // falling back to another account's numbers.
    #expect(none.reportsUse)

    // A week and no five hours, and only spend: nothing there has reset.
    #expect(PlanLimits(agent: "codex", week: .init(usedPct: 4)).bars(now: now) == [PlanBarWindow(label: "7d", usedPct: 4)])
    #expect(!PlanLimits(agent: "claude", spend: .init(usedPct: 12)).barsReset(now: now))
    #expect(!PlanLimits(agent: "claude").barsReset(now: now))
}

@Test func theTooltipSaysAWindowHasReset() {
    let past = PlanLimits.Named(key: "five_hour", usedPct: 96, resetsAt: now.addingTimeInterval(-1))
    #expect(planWindowText(past, now: now) == "5 hours reset")
    #expect(planWindowText(.init(key: "seven_day", usedPct: 8.4), now: now) == "7 days 8%")
}
