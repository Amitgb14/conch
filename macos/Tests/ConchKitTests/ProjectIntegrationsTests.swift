import Foundation
import Testing
@testable import ConchKit

// Shaped as the server's integration.detect answers (proto.IntegrationDetect),
// a project like mock 5's: GitHub, CircleCI with no key, Actions, Jira chosen
// in Settings, Daytona set up, a CI nothing here reads.
private let detectJSON = """
{"integrations":[
 {"name":"github","label":"GitHub","kind":"host","why":"github.com remote","installed":true,"connected":true},
 {"name":"circleci","label":"CircleCI","kind":"ci","why":".circleci/config.yml","installed":true,"key_env":"CIRCLECI_TOKEN"},
 {"name":"github-actions","label":"GitHub Actions","kind":"ci","why":".github/workflows","installed":true,"connected":true},
 {"name":"bitbucket-pipelines","label":"Bitbucket Pipelines","kind":"ci","why":"bitbucket-pipelines.yml"},
 {"name":"jira","label":"Jira (RGW)","kind":"tickets","why":"chosen in Settings · tracker.ceph.com","installed":true,"connected":true,"key_env":"JIRA_API_TOKEN","team":true},
 {"name":"daytona","label":"Daytona","kind":"sandbox","why":"for tasks run off this machine","installed":true}],
 "team":{"path":".conch/integrations.toml","names":["circleci","jira"],"state":"used"},
 "later":{"a newer server":"may add this"}}
"""

private func detect(_ s: String = detectJSON) throws -> IntegrationDetect {
    try JSONDecoder().decode(IntegrationDetect.self, from: Data(s.utf8))
}

@Test func projectIntegrationRows() throws {
    let rows = ProjectIntegrations.rows(try detect())
    #expect(rows.map(\.title) == ["Code host", "CI", "Tickets", "Sandboxes", "Notifications"])

    #expect(rows[0].names == "GitHub" && rows[0].detail == "from the github.com remote" && rows[0].chips == [.connected] && rows[0].actions.isEmpty)

    let ci = rows[1]
    #expect(ci.names == "CircleCI, GitHub Actions, Bitbucket Pipelines")
    #expect(ci.detail == "CircleCI from .circleci/config.yml · GitHub Actions from .github/workflows · Bitbucket Pipelines from bitbucket-pipelines.yml")
    #expect(ci.chips == [.keyNotSet, .connected, .notInstalled])
    #expect(ci.actions == [.connect(name: "circleci", label: "CircleCI"), .install(name: "bitbucket-pipelines", label: "Bitbucket Pipelines")])
    #expect(ci.actions[0].words == "Connect CircleCI…")

    #expect(rows[2].names == "Jira (RGW)" && rows[2].detail == "chosen in Settings · tracker.ceph.com" && rows[2].chips == [.connected])
    #expect(rows[3].chips == [.setUp] && rows[3].chips[0].words == "set up")

    let none = rows[4]
    #expect(none.names == "none" && none.detail == "Slack, Discord, ntfy…" && none.chips.isEmpty && none.actions == [.browse(.notify)])
    #expect(none.actions[0].words == "Browse…")
}

@Test func projectIntegrationRowsWithNothing() throws {
    // A server that has not answered yet, and a folder with nothing found:
    // every kind is a row, each with Browse.
    for d in [nil, try detect(#"{"integrations":[]}"#)] {
        let rows = ProjectIntegrations.rows(d)
        #expect(rows.count == 5)
        #expect(rows.allSatisfy { $0.names == "none" && $0.actions.count == 1 })
        #expect(ProjectIntegrations.teamNames(d).isEmpty)
        #expect(!ProjectIntegrations.saved(d) && ProjectIntegrations.question(d) == nil)
    }
}

@Test func projectIntegrationsTeamFile() throws {
    let d = try detect()
    // Names, never a sandbox (this computer's) or what nothing here reads.
    #expect(ProjectIntegrations.teamNames(d) == ["circleci", "github", "github-actions", "jira"])
    #expect(ProjectIntegrations.saved(d))
    #expect(ProjectIntegrations.question(d) == nil)

    var asked = d
    asked.team = IntegrationTeam(path: ".conch/integrations.toml", names: ["circleci", "jira", "acme-ci"], state: "ask")
    #expect(!ProjectIntegrations.saved(asked))
    #expect(ProjectIntegrations.question(asked) == "This repository's .conch/integrations.toml says it uses CircleCI, Jira and acme-ci. Use them?")
    asked.team?.names = ["circleci"]
    #expect(ProjectIntegrations.question(asked) == "This repository's .conch/integrations.toml says it uses CircleCI. Use it?")
    asked.team?.names = []
    #expect(ProjectIntegrations.question(asked) == nil)

    var broken = d
    broken.team = IntegrationTeam(path: ".conch/integrations.toml", names: nil, state: "ignored", problem: "uses = [")
    #expect(!ProjectIntegrations.saved(broken) && ProjectIntegrations.question(broken) == nil)
    var ignored = d
    ignored.team?.state = "ignored"
    #expect(!ProjectIntegrations.saved(ignored))
}

// Connect… from the sheet opens the install sheet at the key (mock 3, step 2).
@Test func connectOpensAtTheKey() throws {
    let base = #"{"name":"circleci","label":"CircleCI","kind":"ci","source":"ships with conch","installed":true,"does":[],"hosts":{"api":"circleci.com"},"key_env":"CIRCLECI_TOKEN","key_set":false}"#
    let info = try JSONDecoder().decode(IntegrationInfo.self, from: Data(base.utf8))
    var f = IntegrationInstallFlow(connecting: info)
    #expect(f.agreed && f.step == 2)
    #expect(f.blocker == "the key")
    #expect(f.arguments == ["integration", "key", "-stdin", "circleci"])
    f.key = "k"
    #expect(f.step == 3 && f.blocker == nil)
    f.check = .ok("read 3 pipelines")
    #expect(f.step == 4)

    // One not installed starts where an install does: agreeing.
    var fresh = info
    fresh.installed = false
    #expect(IntegrationInstallFlow(connecting: fresh).step == 1)
    // Nor is a revoked one agreed to on anybody's behalf.
    var revoked = info
    revoked.revoked = "sent logs elsewhere"
    #expect(!IntegrationInstallFlow(connecting: revoked).agreed)
}

// Integrations… on a git project's menu, after Agent Setup, only where the
// server can say what a project uses.
@Test func integrationsOnTheProjectMenu() {
    var snap = fixture()
    let node = TreeNode(id: "p:local|r1", kind: .project("local|r1"), title: "api")
    let i = snap.projects.firstIndex { $0.id == "r1" }!
    snap.projects[i].git = true
    let item = RowMenuItem("Integrations…", .integrations(project: "local|r1"))
    #expect(!snap.menu(for: node, machine: "local").contains(item))
    let titles = snap.menu(for: node, machine: "local", integrations: true).map(\.title)
    let at = titles.firstIndex(of: "Agent Setup")!
    #expect(titles[at + 1] == "Integrations…")
    snap.projects[i].git = false
    #expect(!snap.menu(for: node, machine: "local", integrations: true).contains(item))
}
