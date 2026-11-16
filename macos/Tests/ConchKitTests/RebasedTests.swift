import Foundation
import Testing
@testable import ConchKit

let rebasedJSON = #"""
{"rebases":[
 {"branch":"conch/git-graph","before":"aaaa1111","after":"bbbb2222","onto":"cccc3333","at":"2026-11-14T12:00:00Z",
  "remote":"aaaa1111",
  "commits":[{"old":"a1","new":"b1","subject":"Draw merge curves","change":"changed"},
             {"old":"a2","new":"b2","subject":"Lane colours","change":"same"},
             {"old":"a3","subject":"Already on master","change":"dropped"}]},
 {"branch":"fresh","before":"d1","after":"d2","onto":"c"},
 {"branch":"raced","before":"e1","after":"e2","onto":"c","remote":"e9"}
]}
"""#

// The server's rebases, and what pushing each means: over the old branch
// with a lease, a first push, or one somebody else pushed since.
@Test func rebasedDecodesAndPushes() throws {
    let r = try conchDecoder().decode(BranchRebased.self, from: Data(rebasedJSON.utf8))
    let rs = r.rebases ?? []
    #expect(rs.count == 3)
    #expect(rs[0].changed == 1 && rs[0].dropped == 1 && rs[0].at != nil)
    #expect(rs[0].push == .over("aaaa1111"))
    #expect(rs[1].push == .fresh && rs[1].commits == nil)
    #expect(rs[2].push == .moved("e9"))
    #expect(rs[0].summary == "3 commits rewritten · 1 changed resolving the conflict · 1 dropped")
    #expect(rs[1].summary == "Nothing to compare")
    let empty = try conchDecoder().decode(BranchRebased.self, from: Data("{}".utf8))
    #expect(empty.rebases == nil)
}

// A rebased branch that has not gone up needs the person: after a
// conflict stopped somewhere, before anything else.
@Test func rebasedBranchState() throws {
    let project = try conchDecoder().decode(ProjectInfo.self, from: Data(#"""
    {"id":"r1","name":"conch","path":"/p","base":"master",
     "worktrees":[{"path":"/p","branch":"master","main":true},
                  {"path":"/w/a","branch":"asking"},
                  {"path":"/w/b","branch":"conch/git-graph","conflicts":["a.go"]},
                  {"path":"/w/c","rebasing":"stopped","status":{"files":1,"conflicts":1}}],
     "branches":[{"name":"conch/git-graph","pr":{"number":3,"state":"OPEN","checks":"fail"}}]}
    """#.utf8))
    let panes = try conchDecoder().decode([PaneInfo].self, from: Data(#"""
    [{"id":"p2","name":"d","state":"running","project_id":"r1","branch":"asking","agent":{"name":"claude","state":"blocked"}}]
    """#.utf8))
    let rs = try conchDecoder().decode(BranchRebased.self, from: Data(rebasedJSON.utf8)).rebases ?? []
    let bs = graphBranches(project, panes: panes, rebased: Dictionary(uniqueKeysWithValues: rs.map { ($0.branch, $0) }))
    #expect(bs.map(\.name) == ["stopped", "conch/git-graph", "asking"])
    #expect(bs[1].state == .rebased && bs[1].rebase?.after == "bbbb2222")
    #expect(!bs[1].state.conflicts)
    // Without the server's word, as before.
    #expect(graphBranches(project, panes: panes)[2].state == .ciFailing)
    let order: [GraphBranchState] = [.conflicted(1), .rebased, .waiting]
    #expect(order.map(\.urgency) == order.map(\.urgency).sorted())
}

// git range-diff's account, line by line: a pair of commits, the
// resolution's own additions and removals, the rest as context.
@Test func rangeDiffReads() {
    let text = """
    1:  c92020d ! 1:  6e6da5d feat one
        @@ Commit message
         
          ## f ##
         @@
        - a
        --b
        + A
        +-b2
         +B
          c
    2:  860eca6 = 2:  eadbc9d feat two
    """
    let ls = rangeDiffLines(text)
    #expect(ls.first?.kind == .commit && ls.first?.text == "1:  c92020d ! 1:  6e6da5d feat one")
    #expect(ls[1].kind == .hunk && ls[1].text == "@@ Commit message")
    #expect(ls.filter { $0.kind == .removed }.map(\.text) == ["- a", "--b"])
    #expect(ls.filter { $0.kind == .added }.map(\.text) == ["+ A", "+-b2"])
    #expect(ls.last?.kind == .commit)
    #expect(ls.contains { $0.kind == .context && $0.text == " +B" })
    #expect(rangeDiffLines("").isEmpty)
}
