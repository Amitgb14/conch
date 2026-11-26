import Foundation

// Which of a plan's windows still say something. An agent reports only
// while it works, so a window can turn over with its old share still the
// last one heard.

/// A time the server knew, or nil: Go writes one it did not as year 1,
/// omitempty or not, which would read as a reset long past.
func knownTime(_ t: Date?) -> Date? {
    t.flatMap { $0.timeIntervalSince1970 > 0 ? $0 : nil }
}

/// Whether a window's share is still true: until it resets, and always
/// when no reset was reported.
public func windowLive(_ resets: Date?, now: Date) -> Bool {
    resets.map { $0 > now } ?? true
}

/// One of the short bars an account is shown with.
public struct PlanBarWindow: Equatable, Sendable, Identifiable {
    public var label: String
    public var usedPct: Double
    public var id: String { label }

    public init(label: String, usedPct: Double) {
        self.label = label
        self.usedPct = usedPct
    }
}

extension PlanLimits {
    /// The 5h and 7d bars, without a window that has reset.
    public func bars(now: Date) -> [PlanBarWindow] {
        [("5h", fiveHour), ("7d", week)].compactMap { label, w in
            guard let w, windowLive(w.resetsAt, now: now) else { return nil }
            return PlanBarWindow(label: label, usedPct: w.usedPct)
        }
    }

    /// Whether there are no bars because every one of them has reset.
    public func barsReset(now: Date) -> Bool {
        (fiveHour != nil || week != nil) && bars(now: now).isEmpty
    }
}

/// A window as the tooltip says it: "7 days 8%", or "5 hours reset".
public func planWindowText(_ w: PlanLimits.Named, now: Date) -> String {
    let name = PlanLimits.label(w.key)
    return windowLive(w.resetsAt, now: now) ? "\(name) \(Int(w.usedPct.rounded()))%" : "\(name) reset"
}
