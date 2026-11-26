import Foundation
import Testing
@testable import ConchKit

@Suite struct TeamsTests {
    let teams = [
        AgentTeam(name: "Backend", color: "blue", projects: ["api", "worker"], branches: ["conch/cloud-*"]),
        AgentTeam(name: "UI", projects: ["web"], branches: ["ui/*", "*-redesign", "*desktop*"]),
        AgentTeam(name: "Everything", branches: ["*"]),
    ]

    @Test func readsTheTeamsOfConfigToml() {
        let toml = """
        [app]
        name = "not a team"

        [[teams]]
        name = "Backend"
        color = "Blue"
        projects = ["api", "worker"]
        branches = ["conch/cloud-*"]

        [[teams]]
        # no name: left out
        projects = ["x"]

        [[teams]]
        name = " UI "

        [[actions]]
        name = "Run tests"
        run = "go test ./..."
        """
        let got = parseTeams(toml: toml)
        #expect(got == [AgentTeam(name: "Backend", color: "blue", projects: ["api", "worker"], branches: ["conch/cloud-*"]), AgentTeam(name: "UI")])
        #expect(parseTeams(toml: "").isEmpty)
    }

    // Arrays over several lines, comments after a value or inside an array,
    // and a header spaced out are all TOML Go reads; so must the app.
    @Test func readsMultiLineArraysAndComments() {
        let toml = """
        [[ teams ]]   # the server side
        name = "Backend" # who
        projects = [
          "api",   # the "web" one is not here]
          'worker',
        ]
        branches = ["conch/cloud-*"] # and ["x"]

        [[teams]]
        name = "UI"
        branches = [
          "ui/*"
        ]
        projects = ["web"]
        """
        #expect(parseTeams(toml: toml) == [
            AgentTeam(name: "Backend", projects: ["api", "worker"], branches: ["conch/cloud-*"]),
            AgentTeam(name: "UI", projects: ["web"], branches: ["ui/*"]),
        ])
        // A line inside an array that starts with "[" is not a header.
        let nested = "[[teams]]\nname = \"A\"\nprojects = [\n\"p\"\n]\n[[teams]]\nname = \"B\""
        #expect(parseTeams(toml: nested).map(\.name) == ["A", "B"])
        // Never closed: what it held, and the team is kept.
        #expect(parseTeams(toml: "[[teams]]\nname = \"A\"\nprojects = [\"p\",") == [AgentTeam(name: "A", projects: ["p"])])
    }

    // The same cases as Go's TestTeamFor: the app and the CLI say the same team.
    @Test func ruleOrHand() {
        let cases: [(String?, String?, String?, String?)] = [
            (nil, "api", "main", "Backend"),
            (nil, "API", nil, "Backend"),
            (nil, "conch", "conch/cloud-agent", "Backend"),
            (nil, "conch", "ui/nav/dark", "UI"),
            (nil, "conch", "checkout-redesign", "UI"),
            (nil, "conch", "conch/readme-desktop-shots", "UI"),
            (nil, "web", "conch/cloud-x", "Backend"),
            (nil, "conch", "anything", "Everything"),
            (nil, nil, nil, nil),
            ("", "", "", nil),
            ("UX", "api", "main", "UX"),
            ("Gone", "api", "main", "Gone"),
            ("-", "api", "main", nil),
        ]
        for (set, project, branch, want) in cases {
            #expect(teamFor(teams, set: set, project: project, branch: branch) == want, "\(String(describing: set)) \(String(describing: project)) \(String(describing: branch))")
        }
        #expect(teamFor([], set: nil, project: "api", branch: "main") == nil)
    }

    @Test func branchPatterns() {
        let cases: [(String, String, Bool)] = [
            ("main", "main", true), ("main", "mainline", false),
            ("ui/*", "ui/", true), ("ui/*", "ux/a", false),
            ("*-copy", "a-copy", true), ("*-copy", "-copy", true), ("*-copy", "copy", false),
            ("a*b*c", "abc", true), ("a*b*c", "axxbyyc", true), ("a*b*c", "acb", false),
            ("a*a", "a", false), ("*", "", true), ("**", "x/y", true),
        ]
        for (p, s, ok) in cases { #expect(globMatch(p, s) == ok, "\(p) \(s)") }
    }

    @Test func colours() {
        #expect(teamColor("Backend", in: teams) == RGB("#3E7BD6")) // named
        #expect(teamColor("UI", in: teams) == AgentTeam.colors[1].rgb) // by place
        #expect(teamColor("Hex", in: [AgentTeam(name: "Hex", color: "#112233")]) == RGB("#112233"))
        // One put on a pane by hand: from its name, the same every time.
        #expect(teamColor("Ad hoc", in: teams) == teamColor("Ad hoc", in: []))
        #expect(AgentTeam.colors.map(\.name) == ["blue", "orange", "purple", "teal", "green", "red", "pink", "yellow", "gray"])
    }

    func agent(_ id: String, project: String = "r1", branch: String, team: String? = nil, state: AgentState = .working) -> PaneInfo {
        var p = PaneInfo(id: id, name: id, agent: AgentStatus(name: "claude", state: state), projectID: project, branch: branch)
        p.team = team
        return p
    }

    func agentsSection(_ s: Snapshot, teams: [AgentTeam]) -> TreeNode? {
        let tree = s.tree(machine: "local", teams: teams)
        func find(_ n: TreeNode) -> TreeNode? {
            if case .section("Agents") = n.kind { return n }
            for c in n.children ?? [] { if let f = find(c) { return f } }
            return nil
        }
        return find(tree)
    }

    @Test func aProjectsAgentsUnderTheirTeams() throws {
        let s = Snapshot(panes: [
            agent("p1", branch: "conch/cloud-agent"),
            agent("p2", branch: "ui/nav"),
            agent("p3", branch: "docs", team: "Report"),
            agent("p4", branch: "ui/x", team: "-"),
            agent("p5", branch: "conch/cloud-y"),
        ], projects: [ProjectInfo(id: "r1", name: "conch", path: "/c")])
        let rules = [teams[0], teams[1]]
        let agents = try #require(agentsSection(s, teams: rules))
        let kids = agents.children ?? []
        #expect(kids.map(\.title) == ["Backend", "UI", "Report", "No team"])
        #expect(kids.map(\.badge) == ["2", "1", "1", "1"])
        #expect(kids[0].kind == .team(project: "local|r1", name: "Backend") && kids[3].kind == .team(project: "local|r1", name: ""))
        #expect(kids[0].agentKeys == ["local|p1", "local|p5"])
        #expect(kids[0].fold?.hasSuffix("/agents/team:Backend") == true)
        // Nobody in a team: the list as it always was.
        let plain = Snapshot(panes: [agent("p1", branch: "x"), agent("p2", branch: "y")], projects: [ProjectInfo(id: "r1", name: "conch", path: "/c")])
        let flat = try #require(agentsSection(plain, teams: rules))
        #expect((flat.children ?? []).allSatisfy { if case .agent = $0.kind { return true }; return false })
        #expect(try #require(agentsSection(plain, teams: [])).children?.count == 2)
    }

    @Test func menus() throws {
        var p1 = agent("p1", branch: "conch/cloud-agent")
        let p2 = agent("p2", branch: "x", team: "Hand")
        p1.team = nil
        let s = Snapshot(panes: [p1, p2], projects: [ProjectInfo(id: "r1", name: "conch", path: "/c")])
        let row = TreeNode(id: "pane:local|p1", kind: .agent("local|p1"), title: "p1")
        // No team menu from a server without teams.
        #expect(!s.menu(for: row, machine: "local", teams: teams).contains { $0.title == "Team" })
        let menu = s.menu(for: row, machine: "local", teams: teams, canTeam: true)
        let team = try #require(menu.first { $0.title == "Team" })
        let titles = team.children.map(\.title)
        #expect(titles == ["✓ Backend", "UI", "Everything", "Hand", "", "New Team…", "No Team"])
        #expect(team.children[1].action == .setTeam(pane: "local|p1", team: "UI"))
        // One put in a team by hand can go back to the rules.
        let row2 = TreeNode(id: "pane:local|p2", kind: .agent("local|p2"), title: "p2")
        let kids2 = try #require(s.menu(for: row2, machine: "local", teams: teams, canTeam: true).first { $0.title == "Team" }).children
        #expect(kids2.contains { $0.action == .setTeam(pane: "local|p2", team: "") && $0.title == "By Rule" })
        #expect(kids2.first { $0.title == "✓ Hand" } != nil)
        // A team's row: side by side, and a message to its agents.
        let teamRow = TreeNode(id: "t:local|r1:Backend", kind: .team(project: "local|r1", name: "Backend"), title: "Backend",
                               children: [TreeNode(id: "pane:local|p1", kind: .agent("local|p1"), title: "p1")])
        let items = s.menu(for: teamRow, machine: "local")
        #expect(items.map(\.title) == ["Start an Agent in This Team", "Show Side by Side", "Broadcast to This Team…", "", "Rename Team…", "Delete Team…"])
        #expect(items[0].action == .startTeamAgent(project: "local|r1", team: "Backend") && items[0].picksAgent)
        #expect(items[4].action == .renameTeam(name: "Backend") && items[5].action == .deleteTeam(name: "Backend"))
        #expect(items[1].action == .showTogether(panes: ["local|p1"], name: "Backend · conch"))
        // Offline, nothing is started; a project gone from the snapshot has nowhere to start one.
        #expect(!s.menu(for: teamRow, machine: "local", machineOnline: false).contains { $0.action == items[0].action })
        let gone = TreeNode(id: "t", kind: .team(project: "local|r9", name: "Backend"), title: "Backend", children: teamRow.children)
        #expect(!s.menu(for: gone, machine: "local").contains { if case .startTeamAgent = $0.action { return true }; return false })
        #expect(s.broadcastTargets(.panes(machine: "local", keys: ["local|p1"])).map(\.key) == ["local|p1"])
        #expect(teamRow.machine == "local")
        #expect(s.menu(for: TreeNode(id: "t", kind: .team(project: "local|r1", name: ""), title: "No team"), machine: "local").isEmpty)
    }

    // A team's row starts agents only, in its project, into the team;
    // "No team" starts none, and offline nothing is started.
    @Test func aTeamsRowStartsAgentsInIt() throws {
        let s = Snapshot(panes: [agent("p1", branch: "x", team: "Hand")], projects: [ProjectInfo(id: "r1", name: "conch", path: "/c")])
        let row = TreeNode(id: "t:local|r1:Hand", kind: .team(project: "local|r1", name: "Hand"), title: "Hand")
        let b = try #require(s.rowButtons(for: row, machine: "local"))
        #expect(b.primary == nil && b.agentMachine == "local" && b.agentCwd == "/c" && !b.offersTerminal)
        #expect(b.start("codex") == .startTeamAgent(project: "local|r1", team: "Hand"))
        #expect(b.start(nil) == nil) // never a terminal
        #expect(s.rowButtons(for: TreeNode(id: "t", kind: .team(project: "local|r1", name: ""), title: "No team"), machine: "local") == nil)
        #expect(s.rowButtons(for: row, machine: "local", machineOnline: false) == nil)
        #expect(s.rowButtons(for: TreeNode(id: "t", kind: .team(project: "local|r9", name: "Hand"), title: "Hand"), machine: "local") == nil)
        // Other rows still offer a terminal.
        #expect(RowButtons(agentMachine: "local").offersTerminal)
    }

    // Settings → Teams lists the teams put on agents by hand that
    // config.toml does not: once each, in the order first seen.
    @Test func teamsOnlyOnAgentsAreListed() {
        var shell = PaneInfo(id: "p9", name: "zsh", projectID: "r1")
        shell.team = "Terminal team" // a terminal is in no team
        let panes = [
            agent("p1", branch: "a", team: "bugs"),
            agent("p2", branch: "b", team: "Backend"),
            agent("p3", branch: "c", team: "-"),
            agent("p4", branch: "d", team: " sf "),
            agent("p5", branch: "e", team: "bugs"),
            agent("p6", branch: "f", team: ""),
            agent("p7", branch: "g"),
            shell,
        ]
        #expect(teamsOnlyOnAgents(listed: ["Backend"], panes: panes) == ["bugs", "sf"])
        #expect(teamsOnlyOnAgents(listed: ["Backend", "bugs", "sf"], panes: panes).isEmpty)
        #expect(teamsOnlyOnAgents(listed: [], panes: []).isEmpty)
    }

    @Test func gatherPutsTeamInOneTab() {
        var l = Layout()
        l.open("a")
        l.split(.horizontal, with: "b")
        l.open("c")
        l.gather(["a", "c", "a"], name: "UI · web")
        #expect(l.tabs.count == 2) // b alone, then the team's
        #expect(l.tabs.last?.root.panes == ["a", "c"] && l.tabs.last?.name == "UI · web" && l.active == 1)
        #expect(l.tabs.first?.root.panes == ["b"])
        var empty = Layout()
        empty.gather([], name: "x")
        #expect(empty.tabs.isEmpty)
    }

    @Test func newTaskTeam() {
        let now = taskArgs(dir: "/c", prompt: "do it", agent: "claude", branch: "", issue: 3, when: "", team: " UI ")
        #expect(now == ["task", "-cwd", "/c", "-agent", "claude", "-team", "UI", "-issue", "3", "do it"])
        // A task kept for later has no pane to put in a team.
        #expect(!taskArgs(dir: "/c", prompt: "x", agent: "claude", branch: "", issue: nil, when: "+30m", team: "UI").contains("-team"))
        #expect(!taskArgs(dir: "/c", prompt: "x", agent: "claude", branch: "", issue: nil, when: "", team: "").contains("-team"))
    }

    @Test func decodesTheTeamFromAServer() throws {
        let p = try JSONDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p1","name":"x","state":"running","team":"UI"}"#.utf8))
        #expect(p.team == "UI")
        let old = try JSONDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p1","name":"x","state":"running"}"#.utf8))
        #expect(old.team == nil)
    }
}
