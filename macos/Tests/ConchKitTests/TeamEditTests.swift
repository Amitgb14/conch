import Foundation
import Testing
@testable import ConchKit

/// Renaming and deleting a team: config.toml's [[teams]] entry, who is in
/// it, and which names it may take.
@Suite struct TeamEditTests {
    let toml = """
    [ui]
    theme = "white"

    [[teams]]
    name = "Backend"
    color = "blue"
    projects = [
      "api",
      "[[teams]]",
    ]
    branches = ["conch/cloud-*"]

    [[teams]]
      name = "UI"
    projects = ["web"]

    [[actions]]
    name = "Backend"
    run = "make"
    """

    @Test func renamesOnlyThatTeam() {
        let out = renameTeamTOML(toml, from: "UI", to: "Front \"end\"")
        #expect(parseTeams(toml: out).map(\.name) == ["Backend", "Front \"end\""])
        // Its indentation and rules kept; an action of another name's left alone.
        #expect(out.contains("  name = \"Front \\\"end\\\"\""))
        #expect(parseTeams(toml: out)[1].projects == ["web"])
        #expect(out.contains("[[actions]]\nname = \"Backend\""))
        // Only these lines change.
        #expect(out.components(separatedBy: "\n").count == toml.components(separatedBy: "\n").count)
    }

    @Test func renameKeepsColourAndRules() {
        let out = renameTeamTOML(toml, from: "Backend", to: "API")
        let t = parseTeams(toml: out)
        #expect(t.map(\.name) == ["API", "UI"])
        #expect(t[0].color == "blue" && t[0].projects == ["api", "[[teams]]"] && t[0].branches == ["conch/cloud-*"])
        #expect(out.contains("[[actions]]\nname = \"Backend\""))
    }

    @Test func renameOfATeamNotListedChangesNothing() {
        #expect(renameTeamTOML(toml, from: "Nobody", to: "X") == toml)
        #expect(renameTeamTOML("", from: "UI", to: "X") == "")
    }

    @Test func removesTheWholeEntry() {
        // A "[[teams]]" inside a multi-line array is not a header.
        let out = removeTeamTOML(toml, name: "Backend")
        #expect(parseTeams(toml: out).map(\.name) == ["UI"])
        #expect(!out.contains("conch/cloud-*") && !out.contains("\"api\""))
        #expect(out.hasPrefix("[ui]\ntheme = \"white\"\n\n[[teams]]\n  name = \"UI\""))
        #expect(out.contains("[[actions]]\nname = \"Backend\""))
    }

    @Test func removesTheLastEntry() {
        let tail = "[ui]\ntheme = \"white\"\n\n[[teams]]\nname = \"UI\"\nprojects = [\"web\"]\n"
        let out = removeTeamTOML(tail, name: "UI")
        #expect(parseTeams(toml: out).isEmpty)
        #expect(out.hasPrefix("[ui]\ntheme = \"white\"\n"))
        #expect(!out.contains("teams"))
    }

    @Test func removesEveryEntryOfThatName() {
        let twice = "[[teams]]\nname = \"UI\"\n\n[[teams]]\nname = \"Ops\"\n\n[[teams]]\nname = \"UI\"\nbranches = [\"ui/*\"]\n"
        #expect(parseTeams(toml: removeTeamTOML(twice, name: "UI")).map(\.name) == ["Ops"])
        #expect(removeTeamTOML(twice, name: "Nobody") == twice)
        #expect(removeTeamTOML("", name: "UI") == "")
    }

    @Test func membersByHandAndByRuleOnEveryMachine() {
        let teams = [AgentTeam(name: "Backend", projects: ["api"]), AgentTeam(name: "UI", branches: ["ui/*"])]
        func agent(_ id: String, branch: String? = nil, team: String? = nil, agent: Bool = true) -> PaneInfo {
            var p = PaneInfo(id: id, name: id, agent: agent ? AgentStatus(name: "claude", state: .working) : nil, projectID: "r1", branch: branch)
            p.team = team
            return p
        }
        let panes = [
            TeamMemberCandidate(key: "local|p1", pane: agent("p1"), project: "api"),                       // by rule
            TeamMemberCandidate(key: "local|p2", pane: agent("p2", team: "Backend"), project: "web"),       // by hand
            TeamMemberCandidate(key: "busybox|p1", pane: agent("p1"), project: "api"),                     // another machine
            TeamMemberCandidate(key: "local|p3", pane: agent("p3", team: AgentTeam.none), project: "api"),  // kept out
            TeamMemberCandidate(key: "local|p4", pane: agent("p4", team: "UI"), project: "api"),            // by hand elsewhere
            TeamMemberCandidate(key: "local|p5", pane: agent("p5", agent: false), project: "api"),          // a terminal
            TeamMemberCandidate(key: "local|p6", pane: agent("p6", branch: "ui/x"), project: nil),           // no project
        ]
        #expect(teamMembers("Backend", teams: teams, panes: panes) == ["local|p1", "local|p2", "busybox|p1"])
        #expect(teamMembers("UI", teams: teams, panes: panes) == ["local|p4", "local|p6"])
        #expect(teamMembers("Nobody", teams: teams, panes: panes).isEmpty)
        #expect(teamMembers("", teams: teams, panes: panes).isEmpty)
        #expect(teamMembers("Backend", teams: teams, panes: []).isEmpty)
    }

    @Test func namesATeamMayTake() {
        let existing = ["Backend", "UI"]
        #expect(teamRenameProblem("API", from: "Backend", existing: existing) == nil)
        #expect(teamRenameProblem("  Backend ", from: "Backend", existing: existing) == nil) // unchanged
        #expect(teamRenameProblem("UI", from: "Backend", existing: existing) != nil)
        #expect(teamRenameProblem("", from: "Backend", existing: existing) != nil)
        #expect(teamRenameProblem("   ", from: "Backend", existing: existing) != nil)
        #expect(teamRenameProblem("-", from: "Backend", existing: existing) != nil)
        #expect(teamRenameProblem("no TEAM", from: "Backend", existing: existing) != nil)
        // Names differ by case: another team's in other case is another name.
        #expect(teamRenameProblem("ui", from: "Backend", existing: existing) == nil)
    }
}
