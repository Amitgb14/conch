import Foundation

// Plan limit alerts, as the TUI raises them (internal/tui/limitalerts.go):
// once per threshold per limit window. A window is its machine, agent,
// name and reset time, so a new window (after a reset) alerts again, and
// so does usage that drops below every threshold and climbs back.

/// [notify] limit_at as written in config.toml ("[80, 95]") cleaned up as
/// the TUI does (config.Thresholds): percentages in 1…100, ascending, once
/// each; 80 and 95 when none are valid.
public func limitThresholds(_ raw: String?) -> [Int] {
    let nums = (raw ?? "").split(whereSeparator: { !$0.isNumber && $0 != "-" }).compactMap { Int($0) }
    let out = Array(Set(nums.filter { (1...100).contains($0) })).sorted()
    return out.isEmpty ? [80, 95] : out
}

public struct LimitAlert: Equatable, Sendable {
    public var machine: String
    public var agent: String
    /// "5-hour", "week" or "spend".
    public var window: String
    public var pct: Double
    public var threshold: Int
    public var resets: Date?

    /// "Claude 5-hour limit 91% used · resets 15:04", and " on busybox"
    /// for another machine.
    public func text(machineLabel: String? = nil, now: Date, calendar: Calendar = .current) -> String {
        var s = "\(limitAgentName(agent)) \(window) limit \(Int(pct.rounded(.down)))% used"
        if let r = resets { s += " · resets " + limitResetText(r, now: now, calendar: calendar) }
        if let m = machineLabel, !m.isEmpty { s += " on " + m }
        return s
    }
}

func limitAgentName(_ a: String) -> String {
    ["claude": "Claude", "codex": "Codex", "gemini": "Gemini", "opencode": "OpenCode", "devin": "Devin"][a] ?? a
}

/// When a window resets, as the TUI says it: in 12m, 15:04, Mon 15:04, Jan 2 15:04.
public func limitResetText(_ t: Date, now: Date, calendar: Calendar = .current) -> String {
    let d = t.timeIntervalSince(now)
    let f = DateFormatter()
    f.calendar = calendar
    f.timeZone = calendar.timeZone
    f.locale = Locale(identifier: "en_US_POSIX")
    if d < 3600 { return "in \(Int(d / 60) + 1)m" }
    if d < 86400 && calendar.component(.day, from: t) == calendar.component(.day, from: now) {
        f.dateFormat = "HH:mm"
    } else if d < 7 * 86400 {
        f.dateFormat = "EEE HH:mm"
    } else {
        f.dateFormat = "MMM d HH:mm"
    }
    return f.string(from: t)
}

/// The key a window's alerts are remembered by.
public func limitKey(_ machine: String, _ agent: String, _ window: String, _ resets: Date?) -> String {
    "\(machine)|\(agent)|\(window)|\(Int(resets?.timeIntervalSince1970 ?? 0))"
}

/// Updates seen (key → highest threshold alerted) with l, and returns the
/// alerts to raise: at most one per window, for the highest threshold
/// passed since the last alert. A window whose reset has passed is stale
/// and says nothing.
public func limitCrossings(_ seen: inout [String: Int], machine: String, _ l: PlanLimits, thresholds: [Int], now: Date) -> [LimitAlert] {
    var out: [LimitAlert] = []
    for (name, w) in [("5-hour", l.fiveHour), ("week", l.week), ("spend", l.spend)] {
        guard let w, w.resetsAt.map({ $0 > now }) ?? true else { continue }
        let key = limitKey(machine, l.agent, name, w.resetsAt)
        let passed = thresholds.filter { w.usedPct >= Double($0) }.max() ?? 0
        if passed == 0 {
            seen[key] = nil // back under every threshold: alert again next time
            continue
        }
        if (seen[key] ?? 0) >= passed { continue }
        seen[key] = passed
        out.append(LimitAlert(machine: machine, agent: l.agent, window: name, pct: w.usedPct, threshold: passed, resets: w.resetsAt))
    }
    return out
}

/// seen without the windows that have reset.
public func pruneLimitAlerts(_ seen: [String: Int], now: Date) -> [String: Int] {
    seen.filter { key, _ in
        guard let last = key.split(separator: "|").last, let unix = Int(last) else { return false }
        return unix == 0 || Date(timeIntervalSince1970: TimeInterval(unix)) > now
    }
}
