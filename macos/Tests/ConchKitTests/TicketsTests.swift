import Foundation
import Testing
@testable import ConchKit

private func overview() throws -> TicketOverview {
    let json = #"""
    {"projects":[
      {"project_id":"r1","name":"conch","sources":["github","linear · ENG"],"tickets":[
        {"source":"github","key":"221","number":221,"title":"Tickets view","url":"https://x/221","status":"open","labels":["ready"],"mine":true,
         "doing":{"state":"working","pane":"p9","branch":"conch/issue-221-tickets-view"}},
        {"source":"github","key":"222","number":222,"title":"Triage","labels":["needs-info"],"doing":{}},
        {"source":"github","key":"219","number":219,"title":"Intake","labels":[],"doing":{"state":"merged","branch":"conch/issue-219-x","pr":{"number":219,"state":"MERGED"}}},
        {"source":"linear","key":"ENG-142","title":"Retry uploads","status":"In Progress","labels":["go"],"doing":{"state":"queued","schedule":"s1","note":"2 agents are busy"}}]},
      {"project_id":"r2","name":"cloud","sources":["jira · CLOUD"],"tickets":[],"errors":["Jira refused the key"]},
      {"name":"Linear","sources":["linear · OPS"],"tickets":[{"source":"linear","key":"OPS-1","title":"Rotate keys","doing":{}}]}]}
    """#
    return try JSONDecoder().decode(TicketOverview.self, from: Data(json.utf8))
}

@Test func ticketOverviewDecodesAndReads() throws {
    let ov = try overview()
    #expect(ov.projects.count == 3 && ov.projects[2].projectID == nil)
    let t = ov.projects[0].tickets[0]
    #expect(t.shown == "#221" && t.issue == 221 && t.ticket == nil && t.id == "github:221")
    let e = ov.projects[0].tickets[3]
    #expect(e.shown == "ENG-142" && e.issue == nil && e.ticket == "ENG-142")
}

@Test func ticketFilters() throws {
    let ov = try overview()
    let ready: Set<String> = ["ready", "go"]
    #expect(ticketCount(ov, .all, readyLabels: ready) == 5)
    #expect(ticketCount(ov, .mine, readyLabels: ready) == 1)
    #expect(ticketCount(ov, .ready, readyLabels: ready) == 2) // ready, and intake's own go
    #expect(ticketCount(ov, .needsInfo, readyLabels: ready) == 1)
    #expect(ticketCount(ov, .onIt, readyLabels: ready) == 2) // working and queued; merged is done
    #expect(ticketCount(ov, .source("linear"), readyLabels: ready) == 2)

    // All: a project with an error and no tickets still shows, to say why.
    let all = filterTickets(ov, .all, query: "", readyLabels: ready)
    #expect(all.map(\.name) == ["conch", "cloud", "Linear"])
    // Other views leave it out.
    #expect(filterTickets(ov, .mine, query: "", readyLabels: ready).map(\.name) == ["conch"])
    // The project's own view keeps it, empty.
    #expect(filterTickets(ov, .project("r2|cloud"), query: "", readyLabels: ready).map(\.name) == ["cloud"])
    // Search: key, title or label, any case.
    #expect(filterTickets(ov, .all, query: "eng-1", readyLabels: ready).flatMap(\.tickets).map(\.key) == ["ENG-142"])
    #expect(filterTickets(ov, .all, query: "TRIAGE", readyLabels: ready).flatMap(\.tickets).map(\.key) == ["222"])
    #expect(filterTickets(ov, .all, query: "needs", readyLabels: ready).flatMap(\.tickets).map(\.key) == ["222"])
    #expect(filterTickets(ov, .all, query: "#221", readyLabels: ready).flatMap(\.tickets).map(\.key) == ["221"])
    #expect(filterTickets(ov, .all, query: "nothing like it", readyLabels: ready).isEmpty)
    #expect(filterTickets(TicketOverview(projects: []), .all, query: "", readyLabels: ready).isEmpty)
}

@Test func ticketDoingWords() {
    #expect(ticketDoingText(nil) == ("", .none))
    #expect(ticketDoingText(TicketDoing()) == ("", .none))
    #expect(ticketDoingText(TicketDoing(branch: "conch/issue-3-x")) == ("a branch: conch/issue-3-x", .none))
    #expect(ticketDoingText(TicketDoing(state: "waiting", pane: "p1")) == ("waiting on you", .wait))
    #expect(ticketDoingText(TicketDoing(state: "working")) == ("agent working", .work))
    #expect(ticketDoingText(TicketDoing(state: "pr", pr: PRInfo(number: 30, checks: "pending"))) == ("PR #30 · CI running", .pr))
    #expect(ticketDoingText(TicketDoing(state: "pr", pr: PRInfo(number: 30, checks: "fail"))) == ("PR #30 · CI failed", .pr))
    #expect(ticketDoingText(TicketDoing(state: "pr", pr: PRInfo(number: 30, checks: "pass"))) == ("PR #30 · CI green", .pr))
    #expect(ticketDoingText(TicketDoing(state: "queued", note: "2 agents are busy")) == ("queued · 2 agents are busy", .queued))
    #expect(ticketDoingText(TicketDoing(state: "queued")) == ("queued", .queued))
    #expect(ticketDoingText(TicketDoing(state: "idle")) == ("agent idle", .none))
    #expect(ticketDoingText(TicketDoing(state: "merged", pr: PRInfo(number: 9))) == ("merged · PR #9", .done))
}

// The labels that mean ready, from config.toml in either form.
@Test func intakeLabelsFromTOML() {
    let table = """
    [verify]
    auto_pr = true
    [verify.intake]
    rabd597f = "ready"   # conch
    r2 = "go"
    r3 = ""
    [verify.triage]
    rabd597f = "new"
    """
    #expect(tomlTableValues("verify", "intake", in: table) == ["ready", "go"])
    #expect(tomlTableValues("verify", "triage", in: table) == ["new"])
    let inline = "[verify]\nintake = { r1 = \"ready\", r2 = \"go\" }\n[ui]\nintake = { x = \"no\" }\n"
    #expect(tomlTableValues("verify", "intake", in: inline) == ["ready", "go"])
    #expect(tomlTableValues("verify", "intake", in: nil).isEmpty)
    #expect(tomlTableValues("verify", "intake", in: "[verify]\nauto_pr = true\n").isEmpty)
}
