import Foundation

// A project's GitHub Actions over a period of days, as ci.stats gives it,
// and what the CI panel works out from it.

public struct CIStats: Decodable, Equatable, Sendable {
    public var days: Int
    public var runs: Int
    public var passed: Int
    public var failed: Int
    public var prevRuns: Int
    public var prevPassed: Int
    public var medianSeconds: Int
    public var prevMedianSeconds: Int
    public var queueSeconds: Int
    public var redToGreenSeconds: Int
    public var redToGreenCount: Int
    public var daily: [CIDay]
    public var jobs: [CIJob]?
    public var flaky: [CIFlaky]?
    public var failing: [CIRun]?
    public var jobsPending: Int

    enum CodingKeys: String, CodingKey {
        case days, runs, passed, failed, daily, jobs, flaky, failing
        case prevRuns = "prev_runs", prevPassed = "prev_passed"
        case medianSeconds = "median_seconds", prevMedianSeconds = "prev_median_seconds", queueSeconds = "queue_seconds"
        case redToGreenSeconds = "red_to_green_seconds", redToGreenCount = "red_to_green_count"
        case jobsPending = "jobs_pending"
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        days = try c.decode(Int.self, forKey: .days)
        runs = try c.decodeIfPresent(Int.self, forKey: .runs) ?? 0
        passed = try c.decodeIfPresent(Int.self, forKey: .passed) ?? 0
        failed = try c.decodeIfPresent(Int.self, forKey: .failed) ?? 0
        prevRuns = try c.decodeIfPresent(Int.self, forKey: .prevRuns) ?? 0
        prevPassed = try c.decodeIfPresent(Int.self, forKey: .prevPassed) ?? 0
        medianSeconds = try c.decodeIfPresent(Int.self, forKey: .medianSeconds) ?? 0
        prevMedianSeconds = try c.decodeIfPresent(Int.self, forKey: .prevMedianSeconds) ?? 0
        queueSeconds = try c.decodeIfPresent(Int.self, forKey: .queueSeconds) ?? 0
        redToGreenSeconds = try c.decodeIfPresent(Int.self, forKey: .redToGreenSeconds) ?? 0
        redToGreenCount = try c.decodeIfPresent(Int.self, forKey: .redToGreenCount) ?? 0
        daily = try c.decodeIfPresent([CIDay].self, forKey: .daily) ?? []
        jobs = try c.decodeIfPresent([CIJob].self, forKey: .jobs)
        flaky = try c.decodeIfPresent([CIFlaky].self, forKey: .flaky)
        failing = try c.decodeIfPresent([CIRun].self, forKey: .failing)
        jobsPending = try c.decodeIfPresent(Int.self, forKey: .jobsPending) ?? 0
    }

    /// The share of finished runs that passed, in whole percent; nil
    /// without any.
    public var passRate: Int? { runs > 0 ? Int((Double(passed) * 100 / Double(runs)).rounded()) : nil }

    /// How many points the pass rate moved from the period before; nil when
    /// either has no runs.
    public var passRateChange: Int? {
        guard let now = passRate, prevRuns > 0 else { return nil }
        return now - Int((Double(prevPassed) * 100 / Double(prevRuns)).rounded())
    }

    /// How many seconds the median run moved from the period before.
    public var medianChange: Int? {
        guard prevRuns > 0, prevMedianSeconds > 0, medianSeconds > 0 else { return nil }
        return medianSeconds - prevMedianSeconds
    }
}

public struct CIDay: Decodable, Equatable, Sendable {
    public var date: String
    public var passed: Int
    public var failed: Int

    public init(date: String, passed: Int, failed: Int) {
        self.date = date
        self.passed = passed
        self.failed = failed
    }
}

public struct CIJob: Decodable, Equatable, Sendable {
    public var name: String
    public var runs: Int
    public var failed: Int
    public var medianSeconds: Int
    public var p90Seconds: Int

    enum CodingKeys: String, CodingKey {
        case name, runs, failed
        case medianSeconds = "median_seconds", p90Seconds = "p90_seconds"
    }
}

public struct CIFlaky: Decodable, Equatable, Sendable {
    public var name: String
    public var times: Int
    public var how: String?
    public var last: Date?
}

public struct CIRun: Decodable, Equatable, Sendable, Identifiable {
    public var id: Int64
    public var workflow: String
    public var branch: String?
    public var sha: String?
    public var conclusion: String?
    public var attempt: Int?
    public var url: String?
    public var at: Date?
}

/// A duration as the panel says it: "45s", "6m 42s", "1h 02m".
public func ciDuration(_ seconds: Int) -> String {
    let s = max(seconds, 0)
    if s < 60 { return "\(s)s" }
    if s < 3600 { return s % 60 == 0 ? "\(s / 60)m" : "\(s / 60)m \(s % 60)s" }
    let m = (s % 3600) / 60
    return m == 0 ? "\(s / 3600)h" : "\(s / 3600)h " + String(format: "%02dm", m)
}

/// The top of the runs-per-day chart: the busiest day rounded up to the
/// nearest number worth labelling — 1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6 or 8
/// times a power of ten, whole — at least 1, so the tallest bar fills
/// most of the chart.
public func ciAxisTop(_ busiest: Int) -> Int {
    guard busiest > 1 else { return 1 }
    var scale = 1
    while busiest > scale * 10 { scale *= 10 }
    for step in [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10] {
        let v = step * Double(scale)
        if v == v.rounded(), Int(v) >= busiest { return Int(v) }
    }
    return 10 * scale
}

/// Each day's bar, passed and failed, in points of a chart height high
/// whose top is ciAxisTop of the busiest day.
public func ciBars(_ days: [CIDay], height: Double) -> [(passed: Double, failed: Double)] {
    let top = Double(ciAxisTop(days.map { $0.passed + $0.failed }.max() ?? 0))
    return days.map { (Double($0.passed) * height / top, Double($0.failed) * height / top) }
}
