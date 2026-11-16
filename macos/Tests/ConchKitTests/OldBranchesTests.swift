import Foundation
import Testing
@testable import ConchKit

@Suite struct OldBranchesTests {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    func ago(_ days: Double) -> Date { now.addingTimeInterval(-days * 86400) }

    @Test func decodesTheServersAnswer() throws {
        let json = """
        {"days":30,"branches":[
          {"name":"main","committed":"2026-08-01T10:00:00.123456789-07:00","base":true,"worktree":"/src/api"},
          {"name":"old","committed":"2026-09-01T10:00:00Z","merged":true,"suggested":true},
          {"name":"wip","committed":"2026-09-02T10:00:00Z","unmerged":2}
        ]}
        """
        let s = try conchDecoder().decode(BranchStale.self, from: Data(json.utf8))
        #expect(s.days == 30)
        #expect(s.branches.map(\.name) == ["main", "old", "wip"])
        #expect(s.branches[0].committed != nil && s.branches[0].base == true)
        #expect(s.branches[2].unmerged == 2)
        let r = try conchDecoder().decode(BranchPruneResult.self, from: Data(#"{"deleted":["old"],"failed":[{"path":"wip","error":"would lose 2 commits"}]}"#.utf8))
        #expect(r.deleted == ["old"] && r.failed?.first?.path == "wip")
        let empty = try conchDecoder().decode(BranchPruneResult.self, from: Data("{}".utf8))
        #expect(empty.deleted == nil && empty.failed == nil)
    }

    @Test func onlyWhatLosesNothingStartsTicked() {
        let bs = [
            StaleBranch(name: "main", base: true, suggested: true), // a server's word is not enough for the base
            StaleBranch(name: "feat", worktree: "/w/feat", suggested: true),
            StaleBranch(name: "old", merged: true, suggested: true),
            StaleBranch(name: "wip", unmerged: 1),
            StaleBranch(name: "pushed", upstream: "origin/pushed", suggested: true),
        ]
        #expect(OldBranches.initialTicks(bs) == ["old", "pushed"])
        #expect(!OldBranches.deletable(bs[0]) && !OldBranches.deletable(bs[1]))
        #expect(OldBranches.deletable(bs[3])) // offered, unticked
        #expect(OldBranches.initialTicks([]).isEmpty)
    }

    @Test func eachRowSaysWhy() {
        #expect(OldBranches.state(StaleBranch(name: "main", base: true)).text == "base branch · kept")
        #expect(OldBranches.state(StaleBranch(name: "f", worktree: "/w/f")).text == "checked out in /w/f · remove its worktree")
        let one = OldBranches.state(StaleBranch(name: "w", unmerged: 1))
        #expect(one.text == "1 commit not merged or pushed · lost if deleted" && one.warn)
        #expect(OldBranches.state(StaleBranch(name: "w", unmerged: 3)).text.hasPrefix("3 commits"))
        #expect(OldBranches.state(StaleBranch(name: "m", merged: true)) == ("merged", false))
        #expect(OldBranches.state(StaleBranch(name: "p", upstream: "origin/p")).text == "pushed to origin/p")
        var gone = StaleBranch(name: "g", upstream: "origin/g")
        gone.gone = true
        #expect(OldBranches.state(gone).text == "nothing of its own")
    }

    @Test func ageIsInDays() {
        #expect(OldBranches.age(ago(90), now: now) == "90 days ago")
        #expect(OldBranches.age(ago(1.5), now: now) == "1 day ago")
        #expect(OldBranches.age(ago(0), now: now) == "0 days ago")
        #expect(OldBranches.age(now.addingTimeInterval(3600), now: now) == "0 days ago") // a clock ahead
        #expect(OldBranches.age(nil, now: now) == "")
    }

    @Test func daysFallBackTo30() {
        #expect(OldBranches.days(nil) == 30)
        #expect(OldBranches.days(0) == 30)
        #expect(OldBranches.days(-4) == 30)
        #expect(OldBranches.days(14) == 14)
        #expect(OldBranches.days(1) == 1)
    }

    @Test func theConfirmationSaysWhatIsLost() {
        let safe = OldBranches.confirm([StaleBranch(name: "a", merged: true)])
        #expect(safe.title == "Delete 1 branch?" && !safe.losing)
        let mixed = OldBranches.confirm([StaleBranch(name: "a", merged: true), StaleBranch(name: "w", unmerged: 2), StaleBranch(name: "x", unmerged: 1)])
        #expect(mixed.title == "Delete 3 branches?" && mixed.losing)
        #expect(mixed.detail.hasPrefix("w, x have 3 commits"))
        #expect(OldBranches.confirm([StaleBranch(name: "w", unmerged: 1)]).detail.hasPrefix("w has a commit"))
    }

    @Test func mergedIsItsOwnQuestion() throws {
        let merged = OldBranches.Which.merged, old = OldBranches.Which.old
        #expect(old.capabilities == ["branch.prune.v1"])
        #expect(merged.capabilities == ["branch.prune.v1", "branch.prune.merged.v1"])
        #expect(old.intro(days: 45).contains("45 days") && merged.intro(days: 45).contains("however recent"))
        #expect(old.none(days: 30) == "No branch older than 30 days." && merged.none(days: 30) == "No merged branches.")
        // An older server's answer to "merged" is its old branches: refused.
        let json = #"{"days":30,"branches":[{"name":"x","suggested":true}]}"#
        let f = try JSONDecoder().decode(BranchStale.self, from: Data(json.utf8))
        #expect(f.merged == nil && !merged.answered(f) && old.answered(f))
        let m = try JSONDecoder().decode(BranchStale.self, from: Data(#"{"days":30,"merged":true,"branches":[]}"#.utf8))
        #expect(merged.answered(m))
    }
}
