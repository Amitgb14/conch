import ConchKit
import Foundation
import Testing

// Settings → Agents' Agent accounts: what account.list decodes to, and how
// the page groups, names and checks accounts.

@Test func accountListDecodesWhatTheServerSends() throws {
    let json = #"""
    {"accounts":[
      {"agent":"claude","name":"default","default":true,"agents":2,
       "limits":{"agent":"claude","five_hour":{"used_pct":95,"resets_at":"2099-01-01T00:00:00Z"},"week":{"used_pct":18.4}}},
      {"agent":"claude","name":"work","dir":"/h/.config/conch/accounts/claude/work"},
      {"agent":"codex","name":"default","default":true}],
     "pins":[{"path":"/src/acme","account":"work"}],
     "failover_at":90,"agents":["claude","codex"]}
    """#
    let l = try conchDecoder().decode(AgentAccountList.self, from: Data(json.utf8))
    #expect(l.accounts?.count == 3)
    #expect(l.accounts?[0].isDefault == true && l.accounts?[0].agents == 2)
    #expect(l.accounts?[1].dir == "/h/.config/conch/accounts/claude/work")
    #expect(l.pins == [AgentAccountPin(path: "/src/acme", account: "work")])
    #expect(l.failoverAt == 90)
    #expect(agentAccountUsage(l.accounts?[0].limits, now: Date(timeIntervalSince1970: 0)) == "5h 95% · 7d 18%")
}

@Test func anOlderOrEmptyAnswerDecodes() throws {
    // Every field but the list is omitempty on the server.
    let l = try conchDecoder().decode(AgentAccountList.self, from: Data(#"{"accounts":[]}"#.utf8))
    #expect(l.accounts == [] && l.pins == nil && l.failoverAt == nil)
    #expect(agentAccountGroups(l).isEmpty)
    let none = try conchDecoder().decode(AgentAccountList.self, from: Data("{}".utf8))
    #expect(agentAccountGroups(none).isEmpty)
}

@Test func groupsPutTheAgentsOwnAccountFirst() {
    let l = AgentAccountList(accounts: [
        AgentAccount(agent: "codex", name: "home"),
        AgentAccount(agent: "claude", name: "work"),
        AgentAccount(agent: "claude", name: "default", isDefault: true),
        AgentAccount(agent: "codex", name: "default", isDefault: true),
        AgentAccount(agent: "claude", name: "home"),
    ], agents: ["claude"])
    let g = agentAccountGroups(l)
    // The server's order of agents, then one it did not name.
    #expect(g.map(\.agent) == ["claude", "codex"])
    #expect(g[0].accounts.map(\.name) == ["default", "work", "home"])
    #expect(g[1].accounts.map(\.name) == ["default", "home"])
    #expect(agentAccountLabel(g[0].accounts[0]) == "default (its own)")
    #expect(agentAccountLabel(g[0].accounts[1]) == "work")
}

@Test func usageLeavesOutWhatHasTurnedOver() {
    let now = Date(timeIntervalSince1970: 1_000_000)
    #expect(agentAccountUsage(nil, now: now) == "")
    #expect(agentAccountUsed(nil, now: now) == nil)
    let past = PlanLimits(agent: "claude", fiveHour: .init(usedPct: 99, resetsAt: now.addingTimeInterval(-1)),
                          week: .init(usedPct: 40.6, resetsAt: now.addingTimeInterval(3600)))
    #expect(agentAccountUsage(past, now: now) == "7d 41%")
    #expect(agentAccountUsed(past, now: now) == 40.6)
    // A window with no reset time is live.
    let open = PlanLimits(agent: "codex", spend: .init(usedPct: 12))
    #expect(agentAccountUsage(open, now: now) == "spend 12%")
    #expect(agentAccountUsed(open, now: now) == 12)
    // Reported, but nothing in it.
    #expect(agentAccountUsage(PlanLimits(agent: "claude"), now: now) == "")
    #expect(agentAccountUsed(PlanLimits(agent: "claude"), now: now) == nil)
}

@Test func namesFollowTheServersRule() {
    let have = [AgentAccount(agent: "claude", name: "work"), AgentAccount(agent: "claude", name: "default", isDefault: true)]
    #expect(agentAccountNameProblem("home", agent: "claude", existing: have) == nil)
    #expect(agentAccountNameProblem("team-2.b_x", agent: "claude", existing: have) == nil)
    #expect(agentAccountNameProblem(String(repeating: "a", count: 32), agent: "claude", existing: have) == nil)
    #expect(agentAccountNameProblem("", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("   ", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("Default", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("my work", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("a/b", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem(".hidden", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem(String(repeating: "a", count: 33), agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("wörk", agent: "claude", existing: have) != nil)
    // Taken for this agent, in any case; free for another.
    #expect(agentAccountNameProblem("Work", agent: "claude", existing: have) != nil)
    #expect(agentAccountNameProblem("work", agent: "codex", existing: have) == nil)
}

@Test func failoverAndPins() {
    #expect(agentAccountFailoverChoices.first == 0)
    #expect(agentAccountFailoverLabel(0) == "Off")
    #expect(agentAccountFailoverLabel(-1) == "Off")
    #expect(agentAccountFailoverLabel(90) == "At 90%")
    let l = AgentAccountList(pins: [AgentAccountPin(path: "/a", account: "work"), AgentAccountPin(path: "/b", account: "home"),
                                    AgentAccountPin(path: "/a/web", account: "work")])
    #expect(agentAccountPins(named: "work", in: l).map(\.path) == ["/a", "/a/web"])
    #expect(agentAccountPins(named: "none", in: l).isEmpty)
    #expect(agentAccountPins(named: "work", in: AgentAccountList()).isEmpty)
}

@Test func paramsEncodeAsTheServerReadsThem() throws {
    func json(_ v: some Encodable) throws -> String {
        let e = JSONEncoder()
        e.outputFormatting = .sortedKeys
        return String(decoding: try e.encode(v), as: UTF8.self)
    }
    #expect(try json(AccountAddParams(agent: "claude", name: "work")) == #"{"agent":"claude","name":"work"}"#)
    #expect(try json(AccountAddParams(agent: "claude", name: "home", dir: "/h/x")) == #"{"agent":"claude","dir":"\/h\/x","name":"home"}"#)
    #expect(try json(AccountRefParams(agent: "codex", name: "work")) == #"{"agent":"codex","name":"work"}"#)
    // No account unpins.
    #expect(try json(AccountPinParams(path: "/src")) == #"{"path":"\/src"}"#)
    #expect(try json(AccountPinParams(path: "/src", account: "work")) == #"{"account":"work","path":"\/src"}"#)
    #expect(try json(AccountFailoverParams(at: 0)) == #"{"at":0}"#)
}

@Test func signInIsOfferedOnlyWhereItIsNeeded() throws {
    // The server's answer: signed in, not, and an older server that says nothing.
    let json = #"""
    {"accounts":[{"agent":"claude","name":"default","default":true,"signed_in":true},
      {"agent":"claude","name":"work","dir":"/w","signed_in":true},
      {"agent":"claude","name":"home","dir":"/h","signed_in":false},
      {"agent":"codex","name":"old","dir":"/o"}]}
    """#
    let a = try conchDecoder().decode(AgentAccountList.self, from: Data(json.utf8)).accounts ?? []
    #expect(a.map(agentAccountSignIn) == [.own, .signedIn, .signIn, .signIn])
    #expect(a[1].signedIn == true && a[2].signedIn == false && a[3].signedIn == nil)
    // The agent's own is never offered a sign-in, whatever the server says.
    #expect(agentAccountSignIn(AgentAccount(agent: "codex", name: "default", isDefault: true, signedIn: false)) == .own)
}

@Test func aCustomFailoverLine() {
    #expect(agentAccountFailoverValue("85") == 85)
    #expect(agentAccountFailoverValue(" 85% ") == 85)
    #expect(agentAccountFailoverValue("1") == 1)
    #expect(agentAccountFailoverValue("100") == 100)
    for bad in ["", "0", "101", "-5", "85.5", "eighty", "%", "8 5"] {
        #expect(agentAccountFailoverValue(bad) == nil, "\(bad)")
    }
}

@Test func usageIsPerAgentAndAccount() throws {
    // What agent.limits sends: the account when it is not the agent's own.
    let work = try conchDecoder().decode(PlanLimits.self, from: Data(#"{"agent":"claude","account":"work","five_hour":{"used_pct":40}}"#.utf8))
    let own = try conchDecoder().decode(PlanLimits.self, from: Data(#"{"agent":"claude","five_hour":{"used_pct":4}}"#.utf8))
    #expect(work.account == "work" && own.account == nil)
    #expect(work.usageKey != own.usageKey)
    #expect(own.usageKey == PlanLimits(agent: "claude", account: "").usageKey || own.usageKey == "claude|")
    // Codex on a plan with only a week still shows; an empty report does not.
    let codexWeek = try conchDecoder().decode(PlanLimits.self, from: Data(#"{"agent":"codex","week":{"used_pct":12}}"#.utf8))
    #expect(codexWeek.reportsUse)
    #expect(!PlanLimits(agent: "codex").reportsUse)
    #expect(PlanLimits(agent: "codex", windows: [.init(key: "primary", usedPct: 3)]).reportsUse)
}

// An Antigravity CLI account is a Gemini API key, named by its variable:
// asked for only there, checked as the server checks it, sent and read
// back as key_env.
@Test func antigravityAccountsAreKeys() throws {
    func json(_ v: some Encodable) throws -> String {
        let e = JSONEncoder()
        e.outputFormatting = .sortedKeys
        return String(decoding: try e.encode(v), as: UTF8.self)
    }
    #expect(agentAccountUsesKey("antigravity"))
    #expect(!agentAccountUsesKey("claude"))
    #expect(agentAccountKeyProblem("", agent: "antigravity") != nil)
    #expect(agentAccountKeyProblem("1KEY", agent: "antigravity") != nil)
    #expect(agentAccountKeyProblem("AIza-SyD", agent: "antigravity") != nil)
    #expect(agentAccountKeyProblem(" TEAM_KEY ", agent: "antigravity") == nil)
    #expect(agentAccountKeyProblem("", agent: "claude") == nil)
    #expect(try json(AccountAddParams(agent: "antigravity", name: "team", keyEnv: "TEAM_KEY")) == #"{"agent":"antigravity","key_env":"TEAM_KEY","name":"team"}"#)
    let a = try JSONDecoder().decode(AgentAccount.self, from: Data(#"{"agent":"antigravity","name":"team","dir":"/d","key_env":"TEAM_KEY"}"#.utf8))
    #expect(a.keyEnv == "TEAM_KEY")
}
