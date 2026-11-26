import ConchKit
import Foundation
import Testing

// The Artifacts tab's ConchKit half: what the server's list decodes to,
// which artifacts each scope shows, the tree's counts, the sections and
// words a row says, which links it takes over from the system browser,
// what a publish does to the list, and the row menu's Artifacts ▸.

private let utc: Calendar = {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "UTC")!
    return c
}()
private let now = Date(timeIntervalSince1970: 1_795_600_000) // 2026-11-25 09:46:40 UTC

private func art(_ url: String, _ title: String = "T", kind: String = "Design", v: Int = 1, ago: TimeInterval = 60,
                 panes: [String]? = nil, project: String? = nil) -> Artifact {
    Artifact(url: "https://claude.ai/artifact/" + url, title: title, kind: kind, versions: v,
             updated: now.addingTimeInterval(-ago), panes: panes, project: project)
}

@Test func theServersListDecodes() throws {
    let json = #"{"artifacts":[{"url":"https://claude.ai/artifact/A","title":"Mock","kind":"Design","versions":3,"created":"2026-11-24T10:00:00Z","updated":"2026-11-24T11:00:00.5Z","dir":"/w","panes":["p1"],"project":"pr1"},{"url":"https://claude.ai/artifact/B","title":"","kind":"","versions":1,"created":"2026-11-24T10:00:00Z","updated":"2026-11-24T10:00:00Z"}],"note":"x"}"#
    let l = try conchDecoder().decode(ArtifactList.self, from: Data(json.utf8))
    #expect(l.artifacts?.count == 2 && l.note == "x")
    let a = try #require(l.artifacts?.first)
    #expect(a.id == "https://claude.ai/artifact/A" && a.versions == 3 && a.panes == ["p1"] && a.project == "pr1" && a.dir == "/w")
    #expect(a.updated! > a.created!)
    #expect(l.artifacts?[1].panes == nil && l.artifacts?[1].project == nil, "an older answer's missing fields are nil")
    // An empty answer, and the event.
    #expect(try conchDecoder().decode(ArtifactList.self, from: Data(#"{"artifacts":[]}"#.utf8)).artifacts == [])
    let ev = try conchDecoder().decode(ArtifactPublished.self, from: Data(
        #"{"pane":"p1","agent":"claude","artifact":{"url":"https://claude.ai/artifact/A","title":"Mock","kind":"Design","versions":2,"created":"2026-11-24T10:00:00Z","updated":"2026-11-24T10:00:00Z","panes":["p1"]}}"#.utf8))
    #expect(ev.pane == "p1" && ev.agent == "claude" && ev.artifact.versions == 2)
}

@Test func anArtifactsTabsAddressNamesItsPane() {
    #expect(artifactsAddress(pane: "local|p27") == "conch-artifacts:local|p27")
    #expect(parseArtifactsAddress("conch-artifacts:busybox|p3") == "busybox|p3")
    #expect(parseArtifactsAddress("conch-artifacts:") == nil)
    #expect(parseArtifactsAddress("https://claude.ai/artifact/A") == nil)
    #expect(parseArtifactsAddress("") == nil)
    // It is never handed to WebKit, though URL(string:) would take it.
    #expect(!webLoads(artifactsAddress(pane: "local|p27")))
    #expect(webLoads("https://claude.ai/artifact/A") && webLoads("http://localhost:3000"))
    #expect(!webLoads("") && !webLoads(viewSourcePrefix + "http://localhost:3000"))
    // It survives the browser key's encoding, as a saved window keeps it.
    #expect(parseBrowserKey(browserKey(artifactsAddress(pane: "local|p27"))) == "conch-artifacts:local|p27")
}

@Test func onlyClaudeArtifactLinksAreTakenOver() {
    for yes in ["https://claude.ai/artifact/DojTP3z1YM1QH7E63LDTyh", "https://claude.ai/code/artifact/abc-123",
                " https://claude.ai/artifact/X#artboard-Main ", "https://CLAUDE.ai/artifact/X?v=2"] {
        #expect(isArtifactURL(yes), "\(yes)")
    }
    for no in ["http://claude.ai/artifact/X", "https://claude.ai/artifact/", "https://claude.ai/artifact",
               "https://claude.ai/chat/123", "https://claude.ai/artifact/X/more", "https://evil.example/artifact/X",
               "https://claude.ai.evil.example/artifact/X", "https://claude.ai/code/artifact/", "", "not a url",
               "conch-artifacts:local|p1"] {
        #expect(!isArtifactURL(no), "\(no)")
    }
    #expect(artifactBase("https://claude.ai/artifact/X#artboard-Main") == "https://claude.ai/artifact/X")
    #expect(artifactBase("https://claude.ai/artifact/X/?v=2") == "https://claude.ai/artifact/X")
    #expect(artifactBase("https://claude.ai/artifact/X") == "https://claude.ai/artifact/X")
}

@Test func eachScopeShowsItsOwnNewestFirst() {
    let all = [art("OLD", "Old notes", kind: "Doc", ago: 3 * 86400, panes: ["p1"], project: "pr"),
               art("MINE", "Artifacts Tab", ago: 120, panes: ["p1"], project: "pr"),
               art("SIB", "Factory sheet", ago: 30, panes: ["p2"], project: "pr"),
               art("ELSE", "Elsewhere", ago: 10)]
    #expect(artifactsShown(all, scope: .agent, pane: "p1", project: "pr").map(\.title) == ["Artifacts Tab", "Old notes"])
    #expect(artifactsShown(all, scope: .project, pane: "p1", project: "pr").map(\.title) == ["Factory sheet", "Artifacts Tab", "Old notes"])
    #expect(artifactsShown(all, scope: .project, pane: "p1", project: nil).isEmpty, "an agent in no project has no project's")
    #expect(artifactsShown(all, scope: .machine, pane: "p1", project: nil).count == 4)
    // The filter: every word, case aside, in the title or the type.
    #expect(artifactsShown(all, scope: .machine, pane: "", project: nil, query: "TAB arti").map(\.title) == ["Artifacts Tab"])
    #expect(artifactsShown(all, scope: .machine, pane: "", project: nil, query: "doc").map(\.title) == ["Old notes"])
    #expect(artifactsShown(all, scope: .machine, pane: "", project: nil, query: "nothing like it").isEmpty)
    #expect(artifactsShown([], scope: .agent, pane: "p1", project: "pr").isEmpty)
    #expect(ArtifactScope.machine.label(machine: "local") == "This Mac" && ArtifactScope.machine.label(machine: "busybox") == "This Machine")
}

@Test func theTreeCountsEachAgentsArtifacts() {
    let all = [art("A", panes: ["p1"]), art("B", panes: ["p1", "p2"]), art("C"), art("D", panes: ["p2", "p2"])]
    #expect(artifactCounts(all) == ["p1": 2, "p2": 2], "a pane named twice counts once")
    #expect(artifactCounts([]).isEmpty)
}

@Test func theListIsInDays() {
    let today = art("T", ago: 60), yesterday = art("Y", ago: 86400), old = art("O", ago: 30 * 86400)
    var undated = art("U")
    undated.updated = nil
    let s = artifactSections([today, yesterday, old, undated], now: now, calendar: utc)
    #expect(s.map(\.title) == ["Today", "Yesterday", "Earlier"])
    #expect(s.map { $0.items.count } == [1, 1, 2])
    #expect(artifactSections([old], now: now, calendar: utc).map(\.title) == ["Earlier"], "empty sections are left out")
    #expect(artifactSections([], now: now, calendar: utc).isEmpty)
}

@Test func aRowSaysWhenInWords() {
    #expect(artifactWhen(now.addingTimeInterval(-5), now: now, calendar: utc) == "just now")
    #expect(artifactWhen(now.addingTimeInterval(-59), now: now, calendar: utc) == "just now")
    #expect(artifactWhen(now.addingTimeInterval(-60), now: now, calendar: utc) == "1 min ago")
    #expect(artifactWhen(now.addingTimeInterval(-40 * 60), now: now, calendar: utc) == "40 min ago")
    #expect(artifactWhen(now.addingTimeInterval(-3 * 3600), now: now, calendar: utc) == "3 h ago")
    #expect(artifactWhen(now.addingTimeInterval(-86400), now: now, calendar: utc) == "yesterday")
    #expect(artifactWhen(now.addingTimeInterval(-10 * 86400), now: now, calendar: utc) == "Nov 15")
    #expect(artifactWhen(now.addingTimeInterval(-400 * 86400), now: now, calendar: utc) == "Oct 21, 2025")
    #expect(artifactWhen(nil, now: now) == "")
    #expect(artifactWhen(Date(timeIntervalSince1970: 0), now: now) == "", "a time the decoder could not read")
    // A clock behind the transcript's: never "-3 min ago".
    #expect(artifactWhen(now.addingTimeInterval(120), now: now, calendar: utc) == "just now")
    #expect(artifactDetail(art("A", kind: "Design", v: 3, ago: 120), now: now, calendar: utc) == "Design · v3 · 2 min ago")
    #expect(artifactDetail(art("A", kind: "", v: 0, ago: 120), now: now, calendar: utc) == "Artifact · v1 · 2 min ago")
    #expect(artifactIcon(kind: "Design") == "pen-tool" && artifactIcon(kind: "page") == "app-window"
        && artifactIcon(kind: "Doc") == "file-text" && artifactIcon(kind: "Spreadsheet") == "layers")
    for k in ["Design", "Page", "Doc", "x"] { #expect(LucideIcons.all[artifactIcon(kind: k)] != nil, "\(k)") }
}

@Test func aPublishGoesToTheTopAndIsNewsUntilOpened() {
    var s = ArtifactShelf()
    s.load("local", [art("A", "Old", ago: 600, panes: ["p1", "p3"], project: "pr"), art("B", "Other", ago: 900, panes: ["p2"])])
    #expect(s.count("local", pane: "p1") == (1, false))
    #expect(s.count("local", pane: "p9") == (0, false))

    // A new version of A, from p1: to the top, the other pane kept, unseen.
    var a2 = art("A", "Renamed", v: 2, ago: 0, panes: ["p1"])
    a2.project = nil
    s.published("local", ArtifactPublished(pane: "p1", agent: "claude", artifact: a2))
    #expect(s.list("local").map(\.title) == ["Renamed", "Other"])
    #expect(s.list("local")[0].panes == ["p1", "p3"] && s.list("local")[0].project == "pr")
    #expect(s.count("local", pane: "p1") == (1, true) && s.count("local", pane: "p3") == (1, true))
    #expect(s.isUnseen("local", "https://claude.ai/artifact/A"))

    // Opened, by a link to one of its artboards: seen.
    s.opened("local", "https://claude.ai/artifact/A#artboard-Main")
    #expect(!s.isUnseen("local", "https://claude.ai/artifact/A"))

    // A machine that had none listed yet.
    s.published("busybox", ArtifactPublished(pane: "p1", agent: "claude", artifact: art("N", panes: ["p1"])))
    #expect(s.list("busybox").count == 1 && s.count("busybox", pane: "p1") == (1, true))
    #expect(s.count("local", pane: "p1").unseen == false, "the same pane id on another machine is another pane")
    #expect(s.find("https://claude.ai/artifact/N#x")?.machine == "busybox")
    #expect(s.find("https://claude.ai/artifact/NONE") == nil)

    // A machine that cannot list, then gone.
    s.failed("old", "old runs an older conch")
    #expect(s.notes["old"] == "old runs an older conch" && s.list("old").isEmpty)
    s.drop("busybox")
    #expect(s.list("busybox").isEmpty && !s.isUnseen("busybox", "https://claude.ai/artifact/N"))
    s.load("old", [])
    #expect(s.notes["old"] == nil, "a list loaded clears the note")
}

@Test func theRowMenuOffersTheNewestAndShowAll() {
    let list = (0..<12).map { art("A\($0)", "Artifact \($0)", panes: ["p1"]) }
    let m = artifactsMenu(pane: "local|p1", artifacts: list)
    #expect(m.title == "Artifacts")
    #expect(m.children.count == 8 + 2, "eight, a divider and Show All")
    #expect(m.children.first?.action == .openArtifact(pane: "local|p1", url: "https://claude.ai/artifact/A0"))
    #expect(m.children.last?.action == .artifacts(pane: "local|p1") && m.children.last?.title == "Show All")
    // None yet: only Show All, which says why it is empty.
    let none = artifactsMenu(pane: "local|p1", artifacts: [])
    #expect(none.children.map(\.title) == ["Show All"])
    // An artifact with no title is named by its address.
    let untitled = artifactsMenu(pane: "local|p1", artifacts: [art("U", "")])
    #expect(untitled.children.first?.title == "https://claude.ai/artifact/U")
}

@Test func anArtifactsTabIsSavedUnderItsAgent() throws {
    let agent = key("local", "p1")
    let k = browserKey(artifactsAddress(pane: agent))
    var l = Layout()
    l.open(agent)
    l.split(.horizontal, with: k)
    var b = BrowserTabs(address: artifactsAddress(pane: agent))
    b.open("https://claude.ai/artifact/A")
    let back = try JSONDecoder().decode(SavedLayout.self, from: JSONEncoder().encode(SavedLayout(l, browsers: [k: b])))
    #expect(back.layout().tabs.map(\.root.panes) == [[agent, k]], "named for its agent, not the artifact in front")
    #expect(back.browsers?[k]?.tabs.map(\.address) == [artifactsAddress(pane: agent), "https://claude.ai/artifact/A"])
}

@Test func googlesPasskeyStepIsRecognised() {
    // Both the step and its failure, as Conch's browser was sent to them.
    #expect(isPasskeyChallenge("https://accounts.google.com/v3/signin/challenge/pk?TL=AAxq8nu&checkConnection=youtube"))
    #expect(isPasskeyChallenge("https://accounts.google.com/v3/signin/challenge/pk/error?TL=AAxq8nuwTYASu-CMib1_W_fF3sr_Dk2KTy"))
    #expect(isPasskeyChallenge("https://accounts.google.com/signin/v2/challenge/pk/presend"))
    for no in ["https://accounts.google.com/v3/signin/identifier?flowName=GlifWebSignIn",
               "https://accounts.google.com/v3/signin/challenge/pwd", "https://accounts.google.com/v3/signin/challenge/pkce",
               "https://evil.example/v3/signin/challenge/pk", "https://claude.ai/login", "", "not a url"] {
        #expect(!isPasskeyChallenge(no), "\(no)")
    }
}

@Test func aClaudeSignInIsItsSessionCookie() {
    #expect(isClaudeSession(name: "sessionKey", domain: ".claude.ai"))
    #expect(isClaudeSession(name: "sessionKey", domain: "claude.ai"))
    for (n, d) in [("sessionKey", "notclaude.ai"), ("sessionKey", "claude.ai.evil.example"), ("lastActiveOrg", ".claude.ai"),
                   ("sessionkey", ".claude.ai"), ("sessionKey", ""), ("SID", ".google.com")] {
        #expect(!isClaudeSession(name: n, domain: d), "\(n) on \(d)")
    }
}

@Test func whereAnArtifactOpens() {
    // Automatic: here when Conch's browser is signed in, else the person's own.
    #expect(!ArtifactOpening.automatic.outside(signedIn: true))
    #expect(ArtifactOpening.automatic.outside(signedIn: false))
    #expect(!ArtifactOpening.conch.outside(signedIn: false), "chosen: here, signing in here")
    #expect(ArtifactOpening.browser.outside(signedIn: true), "chosen: always the person's browser")
    // A saved choice; anything else (none, an older build's) is automatic.
    #expect(ArtifactOpening(setting: "browser") == .browser && ArtifactOpening(setting: "conch") == .conch)
    #expect(ArtifactOpening(setting: nil) == .automatic && ArtifactOpening(setting: "safari") == .automatic)
    #expect(ArtifactOpening.allCases.map(\.label) == ["Automatically", "In Conch", "In My Browser"])
}
