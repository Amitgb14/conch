import Foundation
import Testing
@testable import ConchKit

/// A project whose one agent branch, feat, is in the worktree given, with
/// the panes given (JSON).
private func resolving(_ worktree: String, panes: String, branches: String = #"[{"name":"main"}]"#) throws -> [GraphBranch] {
    try resolving(worktree, panes: panes, branches: branches, now: Date(timeIntervalSince1970: 1_800_000_100))
}

private func resolving(_ worktree: String, panes: String, branches: String = #"[{"name":"main"}]"#, now: Date,
                       rebased: [String: BranchRebase] = [:]) throws -> [GraphBranch] {
    let project = try conchDecoder().decode(ProjectInfo.self, from: Data(#"""
    {"id":"r1","name":"api","path":"/p","base":"main",
     "worktrees":[{"path":"/p","branch":"main","main":true},\#(worktree)],
     "branches":\#(branches)}
    """#.utf8))
    let ps = try conchDecoder().decode([PaneInfo].self, from: Data(panes.utf8))
    return graphBranches(project, panes: ps, rebased: rebased, now: now)
}

// 2027-01-15T08:00:00Z is 1_800_000_000: asked 100s before now.
private let stopped = #"{"path":"/w/feat","rebasing":"feat","status":{"files":2,"conflicts":2},"conflicts":["a.go","b.go"],"resolve":{"pane":"p9","at":"2027-01-15T08:00:00Z","files":["a.go","b.go","c.go"]}}"#
private func pane(_ id: String, _ state: String, running: Bool = true) -> String {
    #"{"id":"\#(id)","name":"x","state":"\#(running ? "running" : "exited")","project_id":"r1","cwd":"/w/feat","agent":{"name":"claude","state":"\#(state)"}}"#
}

// The agent asked is at it, asks the person something, or stopped with
// the conflict still there — and the branch says which.
@Test func resolvingStates() throws {
    let working = try resolving(stopped, panes: "[\(pane("p9", "working"))]")[0]
    #expect(working.state == .resolving)
    #expect(working.resolver?.id == "p9" && working.ask?.files?.count == 3)
    #expect(working.stopped && working.stoppedFiles == 2 && working.state.conflicts)

    #expect(try resolving(stopped, panes: "[\(pane("p9", "blocked"))]")[0].state == .resolveWaiting)
    #expect(try resolving(stopped, panes: "[\(pane("p9", "done"))]")[0].state == .resolveStopped(ended: false))
    // Its pane gone, or its program ended.
    #expect(try resolving(stopped, panes: "[]")[0].state == .resolveStopped(ended: true))
    #expect(try resolving(stopped, panes: "[\(pane("p9", "idle", running: false))]")[0].state == .resolveStopped(ended: true))
    // Just asked: it has not started working yet, which is not stopping.
    let soon = Date(timeIntervalSince1970: 1_800_000_010)
    #expect(try resolving(stopped, panes: "[\(pane("p9", "idle"))]", now: soon)[0].state == .resolving)
}

// Asked before anything moved — the branch will conflict — and working.
@Test func resolvingBeforeTheRebase() throws {
    let coming = #"{"path":"/w/feat","branch":"feat","conflicts":["a.go"],"resolve":{"pane":"p9","by":"autopilot","at":"2027-01-15T08:00:00Z"}}"#
    let b = try resolving(coming, panes: "[\(pane("p9", "working"))]")[0]
    #expect(b.state == .resolving && !b.stopped && b.ask?.by == "autopilot")
    // Idle long after: it stopped, the conflict still coming.
    #expect(try resolving(coming, panes: "[\(pane("p9", "idle"))]")[0].state == .resolveStopped(ended: false))
}

// An ask whose conflict is gone is no longer news: the branch is rebased,
// or whatever else it is.
@Test func resolvedAskGivesWay() throws {
    let done = #"{"path":"/w/feat","branch":"feat","resolve":{"pane":"p9","at":"2027-01-15T08:00:00Z"}}"#
    let r = try conchDecoder().decode(BranchRebase.self, from: Data(#"{"branch":"feat","before":"a","after":"b","onto":"c"}"#.utf8))
    #expect(try resolving(done, panes: "[\(pane("p9", "done"))]", now: Date(), rebased: ["feat": r])[0].state == .rebased)
    #expect(try resolving(done, panes: "[\(pane("p9", "working"))]")[0].state == .working)
}

// From a server that keeps no asks: an agent working in a worktree
// stopped mid-rebase is resolving it; one doing nothing leaves it stuck.
@Test func resolvingWithoutAnAsk() throws {
    let old = #"{"path":"/w/feat","rebasing":"feat","status":{"files":1,"conflicts":1}}"#
    #expect(try resolving(old, panes: "[\(pane("p1", "working"))]")[0].state == .resolving)
    #expect(try resolving(old, panes: "[\(pane("p1", "blocked"))]")[0].state == .resolveWaiting)
    #expect(try resolving(old, panes: "[\(pane("p1", "idle"))]")[0].state == .conflicted(1))
    #expect(try resolving(old, panes: "[]")[0].state == .conflicted(1))
    // Will conflict, an agent working: it is doing its own work, not resolving.
    let coming = #"{"path":"/w/feat","branch":"feat","conflicts":["a.go"]}"#
    #expect(try resolving(coming, panes: "[\(pane("p1", "working"))]")[0].state == .willConflict)
}

// Stuck needs the person most, then an agent asking, and an agent at it
// needs nobody yet.
@Test func resolvingUrgency() {
    let order: [GraphBranchState] = [.conflicted(1), .resolveStopped(ended: false), .resolveWaiting, .rebased, .waiting,
                                     .ciFailing, .willConflict, .resolving, .working, .done, .idle]
    #expect(order.map(\.urgency) == order.map(\.urgency).sorted())
    #expect(Set(order.map(\.urgency)).count == order.count)
    #expect(GraphBranchState.resolveStopped(ended: true).urgency == GraphBranchState.resolveStopped(ended: false).urgency)
}

// What the banner and the card say of it.
@Test func resolvingWords() throws {
    let b = try resolving(stopped, panes: "[\(pane("p9", "working"))]")[0]
    #expect(resolveHeadline(b, base: "main") == "Claude is resolving the conflict with main")
    #expect(resolveDetail(b, now: Date(timeIntervalSince1970: 1_800_000_100)) == "2 of 3 files left · you asked 1m ago")
    let w = try resolving(stopped, panes: "[\(pane("p9", "blocked"))]")[0]
    #expect(resolveHeadline(w, base: "main") == "Claude is waiting on you while resolving feat")
    let s = try resolving(stopped, panes: "[\(pane("p9", "done"))]")[0]
    #expect(resolveHeadline(s, base: "main") == "Claude stopped; feat still conflicts with main")
    let e = try resolving(stopped, panes: "[]")[0]
    #expect(resolveHeadline(e, base: "main") == "The agent resolving feat ended; it still conflicts with main")
    let coming = #"{"path":"/w/feat","branch":"feat","conflicts":["a.go"],"resolve":{"pane":"p9","by":"autopilot","at":"2027-01-15T07:00:00Z"}}"#
    let a = try resolving(coming, panes: "[\(pane("p9", "working"))]")[0]
    #expect(resolveDetail(a, now: Date(timeIntervalSince1970: 1_800_000_100)) == "not rebased yet · the autopilot asked 1h ago")
}

// The banner is about the branch most in need: stuck before one in hand,
// and none for a conflict merely coming.
@Test func conflictBannerPicks() throws {
    let working = try resolving(stopped, panes: "[\(pane("p9", "working"))]")
    #expect(conflictBannerBranch(working)?.name == "feat")
    let coming = try resolving(#"{"path":"/w/feat","branch":"feat","conflicts":["a.go"]}"#, panes: "[]")
    #expect(conflictBannerBranch(coming) == nil)
    let stuck = try resolving(#"{"path":"/w/feat","rebasing":"feat","status":{"files":1,"conflicts":1}}"#, panes: "[]")
    #expect(conflictBannerBranch(stuck)?.state == .conflicted(1))
    #expect(conflictBannerBranch([]) == nil)
}

// A new agent is started where the branch is checked out, which is a
// worktree naming no branch while its rebase is stopped.
@Test func branchWorktreeMidRebase() throws {
    let p = try conchDecoder().decode(ProjectInfo.self, from: Data(#"""
    {"id":"r1","name":"api","path":"/p","base":"main",
     "worktrees":[{"path":"/p","branch":"main","main":true},{"path":"/w/feat","rebasing":"feat"},{"path":"/w/b","branch":"b"}]}
    """#.utf8))
    #expect(branchWorktree(p, "feat")?.path == "/w/feat")
    #expect(branchWorktree(p, "b")?.path == "/w/b")
    #expect(branchWorktree(p, "nope") == nil)
}

// One file asked, one left: not "1 of 1"; and asked a moment ago.
@Test func resolvingWordsJustAsked() throws {
    let one = #"{"path":"/w/feat","rebasing":"feat","status":{"files":1,"conflicts":1},"resolve":{"pane":"p9","at":"2027-01-15T08:00:00Z","files":["a.go"]}}"#
    let b = try resolving(one, panes: "[\(pane("p9", "working"))]")[0]
    #expect(resolveDetail(b, now: Date(timeIntervalSince1970: 1_800_000_020)) == "1 file left · you asked just now")
}
