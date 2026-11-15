import Foundation
import Testing
@testable import ConchKit

// The commit graph's history as conch has it: master with a merged pull
// request, and two agents' branches off it — one two commits long, one
// that started before the merge.
//
//   u1  (conch/graph)          codex
//   r2  (HEAD -> conch/git3)   claude
//   m5  (master) merge of a2
//   r1                          claude
//   u0                          codex
//   a2  (origin/feature)
//   m4
func history() -> [LogCommit] {
    [
        LogCommit(hash: "u1", parents: ["u0"], subject: "lanes", refs: ["conch/graph"]),
        LogCommit(hash: "r2", parents: ["r1"], subject: "bundle", refs: ["HEAD -> conch/git3"]),
        LogCommit(hash: "m5", parents: ["m4", "a2"], subject: "merge", refs: ["master", "origin/master"]),
        LogCommit(hash: "r1", parents: ["m4"], subject: "stamp"),
        LogCommit(hash: "u0", parents: ["m4"], subject: "model"),
        LogCommit(hash: "a2", parents: ["m4"], subject: "feature", refs: ["origin/feature"]),
        LogCommit(hash: "m4", parents: [], subject: "root"),
    ]
}

// Each branch's commits are what its first parents reach before the base
// does: the merged feature is master's, and so is everything below.
@Test func ownersFollowFirstParentsToTheBase() {
    let owners = commitOwners(history(), base: "master")
    #expect(owners["u1"] == "conch/graph")
    #expect(owners["u0"] == "conch/graph")
    #expect(owners["r2"] == "conch/git3")
    #expect(owners["r1"] == "conch/git3")
    #expect(owners["m5"] == nil && owners["a2"] == nil && owners["m4"] == nil)
    #expect(branchTips(history())["conch/git3"] == "r2")
    #expect(branchTips(history())["master"] == "m5")
    // A remote's branch is not a tip of a local one.
    #expect(branchTips(history())["origin/feature"] == nil)
}

// A branch stacked on another keeps only its own commits; the one below
// it keeps the rest. A base that is not in the history (a log cut short,
// a base never fetched) leaves each branch everything its first parents
// reach, and an empty history owns nothing.
@Test func ownersOfStackedAndOddHistories() {
    let stacked = [
        LogCommit(hash: "c3", parents: ["c2"], subject: "top", refs: ["top"]),
        LogCommit(hash: "c2", parents: ["c1"], subject: "mid", refs: ["mid"]),
        LogCommit(hash: "c1", parents: ["c0"], subject: "low"),
        LogCommit(hash: "c0", parents: [], subject: "base", refs: ["main"]),
    ]
    let o = commitOwners(stacked, base: "main")
    #expect(o["c3"] == "top")
    #expect(o["c2"] == "mid" && o["c1"] == "mid")
    #expect(o["c0"] == nil)
    let noBase = commitOwners(stacked, base: "trunk")
    #expect(noBase["c0"] != nil)
    #expect(commitOwners([], base: "main").isEmpty)
    // A parent outside the history ends the walk without a crash.
    #expect(commitOwners([LogCommit(hash: "x", parents: ["gone"], subject: "", refs: ["b"])], base: "main") == ["x": "b"])
}

// The side's list: one entry per agent's worktree branch, never the base
// or the main checkout, each in the state that needs the person most.
@Test func graphBranchStates() throws {
    let json = #"""
    {"id":"r1","name":"conch","path":"/p","base":"master",
     "worktrees":[{"path":"/p","branch":"master","main":true},
                  {"path":"/w/a","rebasing":"stopped","status":{"files":2,"conflicts":2}},
                  {"path":"/w/b","branch":"red"},
                  {"path":"/w/c","branch":"forecast","conflicts":["a.go","b.go"]},
                  {"path":"/w/d","branch":"asking"},
                  {"path":"/w/e","branch":"busy"},
                  {"path":"/w/f","branch":"quiet"}],
     "branches":[{"name":"master"},
                 {"name":"red","base_ahead":2,"base_behind":1,"pr":{"number":7,"state":"OPEN","checks":"fail"}},
                 {"name":"forecast","base_ahead":1}]}
    """#
    let project = try conchDecoder().decode(ProjectInfo.self, from: Data(json.utf8))
    let panes = try conchDecoder().decode([PaneInfo].self, from: Data(#"""
    [{"id":"p1","name":"a","state":"running","project_id":"r1","cwd":"/w/a/sub","agent":{"name":"codex","state":"working"}},
     {"id":"p2","name":"d","state":"running","project_id":"r1","branch":"asking","agent":{"name":"claude","state":"blocked"}},
     {"id":"p3","name":"e","state":"running","project_id":"r1","branch":"busy","agent":{"name":"gemini","state":"working"}},
     {"id":"p4","name":"other","state":"running","project_id":"r2","branch":"quiet","agent":{"name":"claude","state":"working"}}]
    """#.utf8))
    let bs = graphBranches(project, panes: panes)
    #expect(bs.map(\.name) == ["stopped", "asking", "red", "forecast", "busy", "quiet"])
    let by = Dictionary(uniqueKeysWithValues: bs.map { ($0.name, $0) })
    // Mid-rebase git names no branch: the worktree says which it is
    // rewriting, and its agent is found by the folder it works in.
    #expect(by["stopped"]?.state == .conflicted(2))
    #expect(by["stopped"]?.agent?.id == "p1")
    #expect(by["stopped"]?.stopped == true && by["red"]?.stopped == false)
    #expect(by["red"]?.state == .ciFailing && by["red"]?.ahead == 2 && by["red"]?.behind == 1)
    #expect(by["forecast"]?.state == .willConflict && by["forecast"]?.conflicts == ["a.go", "b.go"])
    #expect(by["asking"]?.state == .waiting)
    #expect(by["busy"]?.state == .working)
    // Another project's agent on a branch of the same name is not this one's.
    #expect(by["quiet"]?.state == .idle && by["quiet"]?.agent == nil)
    #expect(graphBranches(ProjectInfo(id: "x", name: "x", path: "/x"), panes: []).isEmpty)
}

// A conflict stopped mid-rebase is the most urgent, then an agent asking,
// then red CI, then one coming: the order the side's list sorts by.
@Test func graphBranchStateOrder() {
    let order: [GraphBranchState] = [.conflicted(1), .waiting, .ciFailing, .willConflict, .working, .done, .idle]
    #expect(order.map(\.urgency) == order.map(\.urgency).sorted())
    #expect(Set(order.map(\.urgency)).count == order.count)
}

// What an agent is asked: the branch, the base, the files, and to finish
// what was started — continue a rebase that stopped, or bring the branch
// up to date when nothing has moved yet. It never asks it to push.
@Test func resolvePromptSaysWhatToDo() {
    let stopped = resolvePrompt(branch: "conch/graph", base: "master", files: ["a.go", "b.go"], stopped: true)
    #expect(stopped.contains("conch/graph") && stopped.contains("master"))
    #expect(stopped.contains("a.go") && stopped.contains("b.go"))
    #expect(stopped.contains("--continue"))
    #expect(!stopped.contains("push"))
    let ahead = resolvePrompt(branch: "conch/graph", base: "master", files: ["a.go"], stopped: false)
    #expect(ahead.contains("Rebase conch/graph onto master"))
    #expect(!ahead.contains("--continue"))
    // Many files are named a few at a time, with a count of the rest.
    let many = resolvePrompt(branch: "b", base: "main", files: (1...12).map { "f\($0).go" }, stopped: true)
    #expect(many.contains("f1.go") && !many.contains("f12.go") && many.contains("and 4 more"))
    // No files known (an older server): it still says where to look.
    #expect(resolvePrompt(branch: "b", base: "main", files: [], stopped: true).contains("git status"))
}

// The checks of a pull request as marks on its commit, failures first, a
// few at most; none for a pull request without checks.
@Test func ciMarksFromChecks() {
    var pr = PRInfo(number: 1, state: "OPEN", checks: "fail")
    pr.runs = [PRCheck(name: "build", state: "pass"), PRCheck(name: "test macos", state: "fail"),
               PRCheck(name: "swift", state: "pending")]
    #expect(ciMarks(pr).map(\.name) == ["test macos", "swift", "build"])
    pr.runs = (1...20).map { PRCheck(name: "c\($0)", state: "pass") }
    #expect(ciMarks(pr).count == 8)
    #expect(ciMarks(PRInfo(number: 2)).isEmpty)
}

// The repository's format in a word or two, git 3's shown as such.
@Test func repoFormatLabel() {
    #expect(RepoFormat(objects: "sha256", refs: "reftable").label == "SHA-256 · reftable")
    #expect(RepoFormat(objects: "sha256", refs: "reftable").git3)
    #expect(!RepoFormat(objects: "sha1", refs: "files").git3)
    #expect(RepoFormat(objects: "sha1", refs: "files").label == "SHA-1 · files")
    #expect(RepoFormat(objects: nil, refs: nil).label == "")
}
