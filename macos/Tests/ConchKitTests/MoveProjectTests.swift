import Foundation
import Testing
@testable import ConchKit

// The lines `conch project move -json` writes, as the Go side encodes them
// (relocate.Plan, Event, Result).
@Test func moveLinesParse() throws {
    let plan = #"{"plan":{"project_id":"r1","name":"daos","path":"/Users/a/workspace/daos","remote":"git@github.com:daos-stack/daos.git","from":"local","to":"busybox","clone_to":"~/workspace/daos","worktrees":[{"branch":"fix/pool","path":"/w/fix","info":{"branch":"fix/pool","head":"abc","staged":4,"unstaged":3,"other":1,"history":["a","b"]},"agents":[{"id":"p3","name":"Fix pool","state":"running","cols":80,"rows":24}]}],"branches":[{"name":"wip/vos-gc","committed":"2026-11-06T10:00:00Z"},{"name":"old","upstream":"origin/old","gone":true,"committed":"2026-11-06T10:00:00Z"}],"main_agents":[{"id":"p9","name":"helper","state":"running","cols":80,"rows":24}],"main_dirty":2,"terminals":1,"already":["feat/old"]},"steps":4}"#
    guard case .plan(let p, let steps)? = parseMoveLine(plan) else { Issue.record("plan"); return }
    #expect(steps == 4 && p.projectID == "r1" && p.name == "daos" && p.to == "busybox")
    #expect(p.destination == "~/workspace/daos" && p.target == nil)
    #expect(p.worktrees?.first?.info.staged == 4 && p.worktrees?.first?.agents?.first?.id == "p3")
    #expect(p.already == ["feat/old"] && p.worktrees?.first?.there == nil)
    // A worktree there already: its agents join it.
    let joined = #"{"plan":{"project_id":"r1","name":"daos","path":"/p","from":"local","to":"twin","target":"r7","worktrees":[{"branch":"feat/a","path":"/w/a","info":{},"there":"/t/a"}]},"steps":1}"#
    guard case .plan(let j, _)? = parseMoveLine(joined) else { Issue.record("joined plan didn't parse"); return }
    #expect(j.worktrees?.first?.there == "/t/a" && j.target == "r7")
    #expect(p.branches?.map(\.name) == ["wip/vos-gc", "old"] && p.mainDirty == 2 && p.terminals == 1)
    #expect(p.agents.map(\.id) == ["p3", "p9"])

    guard case .event(let e)? = parseMoveLine(#"{"event":{"kind":"worktree","name":"fix/pool","state":"ok","detail":"/there/fix","step":2,"steps":4}}"#) else { Issue.record("event"); return }
    #expect(e.kind == "worktree" && e.state == "ok" && e.step == 2 && e.steps == 4 && e.detail == "/there/fix")

    guard case .result(let r)? = parseMoveLine(#"{"result":{"target":"r7","target_path":"/home/a/workspace/daos","moved":["wip/vos-gc","fix/pool"],"failed":["x: no"]}}"#) else { Issue.record("result"); return }
    #expect(r.target == "r7" && r.targetPath == "/home/a/workspace/daos" && r.moved?.count == 2 && r.failed == ["x: no"])

    #expect(parseMoveLine(#"{"error":"no project \"x\" on local"}"#) == .error(#"no project "x" on local"#))
    #expect(parseMoveLine("not json") == nil && parseMoveLine("{}") == nil && parseMoveLine("") == nil)
}

@Test func moveCommands() {
    #expect(moveArgs(project: "r1", from: "local", to: "busybox", into: nil, plan: true) == ["project", "move", "r1", "-to", "busybox", "-json", "-plan"])
    #expect(moveArgs(project: "r1", from: "gpu", to: "local", into: " ~/src/daos ", plan: false, cols: 100, rows: 30)
            == ["-m", "gpu", "project", "move", "r1", "-to", "local", "-json", "-into", "~/src/daos", "-y", "-cols", "100", "-rows", "30"])
    #expect(!moveArgs(project: "r1", from: "local", to: "b", into: "  ", plan: true).contains("-into"))
}

@Test func remotesAndClones() {
    #expect(sameRemote("git@github.com:daos-stack/daos.git", "https://github.com/DAOS-stack/daos/"))
    #expect(sameRemote("ssh://git@github.com/daos-stack/daos", "https://github.com/daos-stack/daos.git"))
    #expect(!sameRemote("https://github.com/a/b", "https://github.com/a/c") && !sameRemote(nil, nil) && !sameRemote("", ""))
    #expect(normalRemote("/srv/git/api.git") == "/srv/git/api")
    var a = ProjectInfo(id: "r9", name: "daos", path: "/home/x/src/daos")
    a.git = true
    a.remote = "https://github.com/daos-stack/daos"
    var b = ProjectInfo(id: "r8", name: "other", path: "/o")
    b.git = true
    b.remote = "https://github.com/x/other"
    #expect(cloneOf("git@github.com:daos-stack/daos.git", in: [b, a])?.id == "r9")
    #expect(cloneOf("git@github.com:daos-stack/daos.git", in: [b]) == nil && cloneOf(nil, in: [a]) == nil)
    #expect(moveTakes(MoveInfo(staged: 2, unstaged: 0, other: 1)) == ["2 staged", "1 untracked"])
    #expect(moveTakes(MoveInfo()) == ["committed"])
    var gone = BranchInfo(name: "x")
    gone.gone = true
    var ahead = BranchInfo(name: "y")
    ahead.upstream = "origin/y"
    ahead.ahead = 1
    #expect(branchWhy(gone) == "its upstream is gone" && branchWhy(ahead) == "1 commit not on origin/y" && branchWhy(BranchInfo(name: "z")) == "only here")
}

// A count says the noun that fits it.
@Test func moveCounts() {
    #expect(moveCount(1, "agent continues", "agents continue") == "1 agent continues")
    #expect(moveCount(2, "agent continues", "agents continue") == "2 agents continue")
    #expect(moveCount(0, "branch", "branches") == "0 branches")
}

// A project with no origin is copied, said by the plan's bundle.
@Test func movePlanBundle() throws {
    let json = #"{"project_id":"r1","name":"cachemind","path":"/p","from":"local","to":"bot","clone_to":"~/cachemind","bundle":true}"#
    let p = try conchDecoder().decode(MovePlan.self, from: Data(json.utf8))
    #expect(p.bundle == true && p.remote == nil && p.destination == "~/cachemind")
    let old = try conchDecoder().decode(MovePlan.self, from: Data(#"{"project_id":"r1","name":"x","path":"/p","from":"a","to":"b"}"#.utf8))
    #expect(old.bundle == nil)
}
