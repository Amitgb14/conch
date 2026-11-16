import Foundation
import Testing
@testable import ConchKit

let activityJSON = #"""
{"notes":[
 {"project_id":"r1","branch":"conch/git3","text":"CI failed on conch/git3: sent it to claude-git3 to fix","kind":"ci","at":"2026-11-14T12:01:00Z"},
 {"project_id":"r1","branch":"conch/phone","text":"merged #234 on GitHub (squash), green","kind":"merge","merged":true,"at":"2026-11-14T11:55:00Z"},
 {"project_id":"r1","branch":"conch/graph","text":"#9 is green but GitHub would not merge it: conflict","kind":"merge","error":true,"at":"2026-11-13T18:00:00Z"},
 {"project_id":"r1","branch":"","text":"took #241 Fix the login as a queued task (label conch)","kind":"intake","at":"2026-11-10T09:12:00Z"},
 {"project_id":"r1","branch":"old","text":"from an older server"}
]}
"""#

// The server's notes, newest first; one from an older server has no kind
// or time and still reads.
@Test func activityDecodes() throws {
    let a = try conchDecoder().decode(AutopilotActivity.self, from: Data(activityJSON.utf8))
    #expect(a.notes.count == 5)
    #expect(a.notes[1].merged == true && a.notes[1].kind == "merge")
    #expect(a.notes[2].error == true)
    #expect(a.notes[4].kind == nil && a.notes[4].at == nil)
}

// How each note is marked: a merge done, a failure, something handed to
// an agent, the rest.
@Test func activityTones() throws {
    let a = try conchDecoder().decode(AutopilotActivity.self, from: Data(activityJSON.utf8))
    #expect(a.notes[0].tone == .handed)
    #expect(a.notes[1].tone == .done)
    #expect(a.notes[2].tone == .failed)
    #expect(a.notes[3].tone == .info)
    #expect(a.notes[0].kindLabel == "CI")
    #expect(a.notes[3].kindLabel == "Intake")
    #expect(a.notes[4].kindLabel == "")
}

// Grouped by day as the person's calendar has it: Today, Yesterday, then
// the date; a note without a time goes last, under Earlier.
@Test func activityByDay() throws {
    let a = try conchDecoder().decode(AutopilotActivity.self, from: Data(activityJSON.utf8))
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC")!
    let now = ISO8601DateFormatter().date(from: "2026-11-14T15:00:00Z")!
    let days = activityDays(a.notes, now: now, calendar: cal)
    #expect(days.map(\.title) == ["Today", "Yesterday", "Nov 10", "Earlier"])
    #expect(days[0].notes.count == 2 && days[1].notes.count == 1)
    #expect(activityDays([], now: now, calendar: cal).isEmpty)
}

// The rules said in words, for the panel's top: what is on, and for this
// project its check, intake and triage labels.
@Test func autopilotRules() throws {
    let p = try conchDecoder().decode(AutopilotPolicy.self, from: Data(#"""
    {"auto_fix":3,"auto_merge":true,"auto_pr":false,"auto_resolve":2,"require_tests":true,
     "checks":{"r1":"go test ./..."},"intake":{"r1":"conch"},"triage":{"r2":"triage"}}
    """#.utf8))
    let lines = autopilotRules(p, project: "r1")
    #expect(lines.first { $0.text.contains("Failed CI") }?.on == true)
    #expect(lines.first { $0.text.contains("3 times") } != nil)
    #expect(lines.first { $0.text.contains("Merge") }?.on == true)
    #expect(lines.first { $0.text.contains("Push finished") }?.on == false)
    #expect(lines.first { $0.text.contains("go test ./...") }?.on == true)
    #expect(lines.first { $0.text.contains("label conch") }?.on == true)
    #expect(lines.first { $0.text.contains("Triage") }?.on == false)
    #expect(p.anyOn)
    let off = try conchDecoder().decode(AutopilotPolicy.self, from: Data("{}".utf8))
    #expect(!off.anyOn)
    #expect(autopilotRules(off, project: "r1").allSatisfy { !$0.on })
    // Another project's check and labels are not this one's.
    #expect(autopilotRules(p, project: "r2").first { $0.text.hasPrefix("No check") }?.on == false)
    #expect(autopilotRules(p, project: "r2").first { $0.text.contains("Triage") }?.on == true)
}
