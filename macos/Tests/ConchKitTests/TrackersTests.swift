import Foundation
import Testing
@testable import ConchKit

// ticket.check's answer as the server sends it, read by the app.
@Test func ticketCheckDecodes() throws {
    let json = #"""
    {"trackers":[
      {"tracker":"github","state":"connected","who":"amit","area":"api","open":3,"mine":1,"key_from":"gh"},
      {"tracker":"linear","state":"connected","who":"Amit","email":"amit@acme.dev","workspace":"Acme",
       "areas":[{"key":"ENG","name":"Engineering"},{"key":"MOB"}],"area":"ENG","open":100,"more":true,"mine":5,"key_from":"Keychain (LINEAR_API_KEY)"},
      {"tracker":"jira","state":"error","error":"Jira refused the key (401 Unauthorized): check it"}],
     "projects":[{"project":"api","tracker":"linear","area":"ENG","open":23},{"project":"web","tracker":"jira","error":"Jira refused the key"}]}
    """#
    let r = try JSONDecoder().decode(TicketCheckResult.self, from: Data(json.utf8))
    #expect(r.trackers.count == 3 && r.projects?.count == 2)
    let lin = r.trackers[1]
    #expect(lin.keyFrom == "Keychain (LINEAR_API_KEY)" && lin.areas?[1] == TrackerArea(key: "MOB") && lin.more == true)
    // An older answer with nothing but the state still reads.
    let bare = try JSONDecoder().decode(TicketCheckResult.self, from: Data(#"{"trackers":[{"tracker":"linear","state":"off"}]}"#.utf8))
    #expect(bare.trackers[0].who == nil && bare.projects == nil)
}

@Test func trackerBadges() {
    #expect(trackerBadge(TrackerState(tracker: "linear", state: "connected")) == ("Connected", .ok))
    #expect(trackerBadge(TrackerState(tracker: "linear", state: "off")) == ("Off", .off))
    #expect(trackerBadge(TrackerState(tracker: "jira", state: "error", error: "Jira refused the key (401 Unauthorized)")) == ("Couldn't sign in", .err))
    #expect(trackerBadge(TrackerState(tracker: "github", state: "error", error: "gh is not signed in: run gh auth login")) == ("Couldn't sign in", .err))
    #expect(trackerBadge(TrackerState(tracker: "jira", state: "error", error: "Jira: context deadline exceeded")) == ("Not reachable", .err))
    #expect(trackerBadge(nil) == ("Not checked", .off)) // before the first check, or an older server
}

@Test func trackerFactsAndNotes() {
    let lin = TrackerState(tracker: "linear", state: "connected", who: "Amit", email: "amit@acme.dev", workspace: "Acme",
                           areas: [TrackerArea(key: "ENG"), TrackerArea(key: "MOB")], area: "ENG", open: 100, mine: 5, more: true, keyFrom: "$LINEAR_API_KEY")
    #expect(trackerWho(lin) == "Amit · amit@acme.dev · Acme")
    let f = trackerFacts(lin).map { "\($0.value) \($0.label)" }
    #expect(f == ["100+ open in ENG", "5 assigned to you", "2 teams"])
    #expect(trackerKeyNote(lin) == "The key is read from $LINEAR_API_KEY.")

    let jira = TrackerState(tracker: "jira", state: "connected", who: "A", areas: [TrackerArea(key: "CLOUD")], open: 0, mine: 0)
    #expect(trackerFacts(jira).map { "\($0.value) \($0.label)" } == ["0 open", "0 assigned to you", "1 project"])
    #expect(trackerKeyNote(TrackerState(tracker: "jira", state: "connected", keyFrom: "config.toml")) == "The key is kept in config.toml as it is.")
    #expect(trackerKeyNote(TrackerState(tracker: "linear", state: "connected", keyFrom: "Keychain (LINEAR_API_KEY)")) == "The key is read from the Keychain (LINEAR_API_KEY).")
    #expect(trackerKeyNote(TrackerState(tracker: "github", state: "connected", keyFrom: "gh"))?.contains("gh auth login") == true)
    #expect(trackerKeyNote(TrackerState(tracker: "linear", state: "off")) == nil)

    // GitHub with no project: the login alone, no counts.
    #expect(trackerFacts(TrackerState(tracker: "github", state: "connected", who: "amit")).isEmpty)
    // Nothing to say about one that is not connected.
    #expect(trackerFacts(TrackerState(tracker: "jira", state: "error", open: 3)).isEmpty)
    #expect(trackerWho(TrackerState(tracker: "jira", state: "connected", who: "", email: nil, workspace: "acme.atlassian.net")) == "acme.atlassian.net")

    #expect(trackerProjectNote(TrackerProjectState(project: "api", tracker: "linear", area: "ENG", open: 23)) == ("23 open", true))
    #expect(trackerProjectNote(TrackerProjectState(project: "api", tracker: "linear", open: 100, more: true)) == ("100+ open", true))
    #expect(trackerProjectNote(TrackerProjectState(project: "w", tracker: "jira", error: "no project W in Jira")) == ("no project W in Jira", false))
}
