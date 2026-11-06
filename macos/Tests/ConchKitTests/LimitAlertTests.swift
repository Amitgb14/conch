import Foundation
import Testing
@testable import ConchKit

@Test func limitThresholdsAsTheTUI() {
    #expect(limitThresholds("[80, 95]") == [80, 95])
    #expect(limitThresholds("[95, 80, 80, 0, 101, 50]") == [50, 80, 95])
    #expect(limitThresholds(nil) == [80, 95] && limitThresholds("[]") == [80, 95] && limitThresholds("[0, 200]") == [80, 95])
    #expect(NotifySettings(toml: "[notify]\nlimit_at = [70]\nlimits = false\n").limitAt == [70])
    #expect(NotifySettings(toml: "[notify]\nlimits = false\n").limits == false)
    #expect(NotifySettings(toml: "").limits && NotifySettings(toml: "").limitAt == [80, 95])
}

@Test func limitCrossingsOncePerThreshold() {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    let reset = now.addingTimeInterval(3 * 3600)
    var seen: [String: Int] = [:]
    func at(_ pct: Double, resets: Date? = nil) -> PlanLimits {
        PlanLimits(agent: "claude", fiveHour: .init(usedPct: pct, resetsAt: resets ?? reset))
    }
    #expect(limitCrossings(&seen, machine: "local", at(50), thresholds: [80, 95], now: now).isEmpty)
    // 90%: the 80 alert, once — what the person saw no alert for.
    let a = limitCrossings(&seen, machine: "local", at(90), thresholds: [80, 95], now: now)
    #expect(a.count == 1 && a[0].threshold == 80 && a[0].window == "5-hour")
    #expect(limitCrossings(&seen, machine: "local", at(92), thresholds: [80, 95], now: now).isEmpty)
    #expect(limitCrossings(&seen, machine: "local", at(96), thresholds: [80, 95], now: now).map(\.threshold) == [95])
    // Straight past both at once: one alert, for the higher.
    var fresh: [String: Int] = [:]
    #expect(limitCrossings(&fresh, machine: "local", at(97), thresholds: [80, 95], now: now).map(\.threshold) == [95])
    // A new window (after a reset) alerts again.
    #expect(limitCrossings(&seen, machine: "local", at(85, resets: reset.addingTimeInterval(5 * 3600)), thresholds: [80, 95], now: now).count == 1)
    // Under every threshold, then back over: again.
    _ = limitCrossings(&seen, machine: "local", at(10), thresholds: [80, 95], now: now)
    #expect(limitCrossings(&seen, machine: "local", at(81), thresholds: [80, 95], now: now).count == 1)
    // A stale window — its reset has passed — says nothing.
    var s2: [String: Int] = [:]
    #expect(limitCrossings(&s2, machine: "local", at(99, resets: now.addingTimeInterval(-60)), thresholds: [80, 95], now: now).isEmpty)
    // Week and spend too; a window with no reset time still counts.
    var s3: [String: Int] = [:]
    let all = PlanLimits(agent: "codex", fiveHour: nil, week: .init(usedPct: 81, resetsAt: nil), spend: .init(usedPct: 99, resetsAt: reset))
    #expect(limitCrossings(&s3, machine: "box", all, thresholds: [80, 95], now: now).map(\.window) == ["week", "spend"])
}

@Test func limitAlertTextAndPruning() {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC")!
    let now = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15 08:00 UTC
    let a = LimitAlert(machine: "local", agent: "claude", window: "5-hour", pct: 90.7, threshold: 80, resets: now.addingTimeInterval(3 * 3600))
    #expect(a.text(now: now, calendar: cal) == "Claude 5-hour limit 90% used · resets 11:00")
    #expect(a.text(machineLabel: "busybox", now: now, calendar: cal).hasSuffix(" on busybox"))
    #expect(limitResetText(now.addingTimeInterval(600), now: now, calendar: cal) == "in 11m")
    #expect(limitResetText(now.addingTimeInterval(2 * 86400), now: now, calendar: cal).count > 5)
    var b = a; b.resets = nil; b.agent = "aider"
    #expect(b.text(now: now, calendar: cal) == "aider 5-hour limit 90% used")
    let seen = [limitKey("local", "claude", "5-hour", now.addingTimeInterval(60)): 80,
                limitKey("local", "claude", "week", now.addingTimeInterval(-60)): 95,
                limitKey("local", "codex", "week", nil): 80, "junk": 1]
    #expect(Set(pruneLimitAlerts(seen, now: now).keys) == [limitKey("local", "claude", "5-hour", now.addingTimeInterval(60)), limitKey("local", "codex", "week", nil)])
}
