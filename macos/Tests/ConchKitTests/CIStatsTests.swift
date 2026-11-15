import Foundation
import Testing
@testable import ConchKit

let ciJSON = #"""
{"days":7,"runs":20,"passed":17,"failed":3,"prev_runs":10,"prev_passed":8,
 "median_seconds":402,"prev_median_seconds":440,"queue_seconds":24,
 "red_to_green_seconds":660,"red_to_green_count":3,
 "daily":[{"date":"2026-11-08","passed":2,"failed":0},{"date":"2026-11-09","passed":5,"failed":1},
          {"date":"2026-11-10","passed":0,"failed":0},{"date":"2026-11-11","passed":3,"failed":2},
          {"date":"2026-11-12","passed":4,"failed":0},{"date":"2026-11-13","passed":1,"failed":0},
          {"date":"2026-11-14","passed":2,"failed":0}],
 "jobs":[{"name":"test macos","runs":20,"failed":2,"median_seconds":390,"p90_seconds":610},
         {"name":"lint","runs":20,"failed":0,"median_seconds":48,"p90_seconds":61}],
 "flaky":[{"name":"test macos","times":2,"how":"failed and passed on the same commit","last":"2026-11-13T10:00:00Z"}],
 "failing":[{"id":42,"workflow":"ci","branch":"conch/git3","sha":"abc","conclusion":"failure","attempt":1,"url":"https://x/42","at":"2026-11-14T11:00:00Z"}],
 "jobs_pending":5}
"""#

// The server's numbers, as the panel reads them; an older server's answer
// without the optional lists still decodes.
@Test func ciStatsDecode() throws {
    let s = try conchDecoder().decode(CIStats.self, from: Data(ciJSON.utf8))
    #expect(s.days == 7 && s.runs == 20 && s.passed == 17 && s.medianSeconds == 402)
    #expect(s.daily.count == 7 && s.daily[3].failed == 2)
    #expect(s.jobs?.first?.p90Seconds == 610)
    #expect(s.failing?.first?.id == 42 && s.failing?.first?.branch == "conch/git3")
    #expect(s.jobsPending == 5)
    let bare = try conchDecoder().decode(CIStats.self, from: Data(#"{"days":14,"runs":0,"passed":0,"failed":0,"daily":[]}"#.utf8))
    #expect(bare.jobs == nil && bare.prevRuns == 0 && bare.jobsPending == 0)
}

// Pass rate and its change in whole points; none without runs to compare.
@Test func ciRates() throws {
    let s = try conchDecoder().decode(CIStats.self, from: Data(ciJSON.utf8))
    #expect(s.passRate == 85)
    #expect(s.passRateChange == 5) // 85 now, 80 before
    #expect(s.medianChange == -38)
    var none = s
    none.prevRuns = 0
    #expect(none.passRateChange == nil && none.medianChange == nil)
    none.runs = 0
    #expect(none.passRate == nil)
}

@Test func ciDurations() {
    #expect(ciDuration(0) == "0s")
    #expect(ciDuration(45) == "45s")
    #expect(ciDuration(60) == "1m")
    #expect(ciDuration(402) == "6m 42s")
    #expect(ciDuration(3600) == "1h")
    #expect(ciDuration(3725) == "1h 02m")
    #expect(ciDuration(-5) == "0s")
}

// The chart's top is a round number at or above the busiest day, so its
// gridline label means something; bars are scaled to it.
@Test func ciChartScale() {
    #expect(ciAxisTop(0) == 1)
    #expect(ciAxisTop(1) == 1)
    #expect(ciAxisTop(6) == 6)
    #expect(ciAxisTop(7) == 8)
    #expect(ciAxisTop(11) == 12)
    #expect(ciAxisTop(23) == 25)
    #expect(ciAxisTop(130) == 150)
    let days = [CIDay(date: "a", passed: 4, failed: 2), CIDay(date: "b", passed: 0, failed: 0)]
    let bars = ciBars(days, height: 100)
    #expect(bars[0].passed == 400.0 / 6 && bars[0].failed == 200.0 / 6)
    #expect(bars[1].passed == 0 && bars[1].failed == 0)
    #expect(ciBars([], height: 100).isEmpty)
}
