import Foundation
import Testing
@testable import ConchKit

let twoHunks = """
diff --git a/h.go b/h.go
index 1..2 100644
--- a/h.go
+++ b/h.go
@@ -1,3 +1,4 @@ func a(
 one
+two
 three
 four
@@ -10,2 +11,2 @@ func b(
-old
+new
 end

"""

@Test func splitsHunks() {
    let (header, hunks) = splitHunks(twoHunks)
    #expect(header.hasPrefix("diff --git a/h.go b/h.go\n") && header.hasSuffix("+++ b/h.go\n"))
    #expect(hunks.count == 2)
    #expect(hunks[0].header == "@@ -1,3 +1,4 @@ func a(")
    #expect(hunks[0].text.hasPrefix("@@ -1,3 +1,4 @@") && hunks[0].text.hasSuffix(" four\n"))
    #expect(hunks[1].text == "@@ -10,2 +11,2 @@ func b(\n-old\n+new\n end\n")
    let added = hunks[0].lines.first { $0.kind == .added }
    #expect(added?.text == "two" && added?.new == 2, "lines keep their numbers")
    #expect(hunks[1].lines.first { $0.kind == .removed }?.old == 10)
    #expect(splitHunks("").hunks.isEmpty && splitHunks("").header == "")
    #expect(hunkPatch(header: header, hunks: []) == "")
    #expect(hunkPatch(header: header, hunks: [hunks[1].text]) == header + hunks[1].text)
}

@Test func stagingPicks() {
    let (header, hunks) = splitHunks(twoHunks)
    var p = StagePicks()
    #expect(p.isEmpty && p.state("h.go", of: hunks) == .off)
    p.toggleHunk("h.go", header: header, hunk: hunks[1].text, all: hunks)
    #expect(p.state("h.go", of: hunks) == .mixed && p.hunkCount == 1)
    let one = p.selection(order: ["h.go": hunks])
    #expect(one.files.isEmpty && one.hunks == 1 && one.patch == header + hunks[1].text)
    p.toggleHunk("h.go", header: header, hunk: hunks[0].text, all: hunks)
    #expect(p.state("h.go", of: hunks) == .on)
    #expect(p.selection(order: ["h.go": hunks]).patch == header + hunks[0].text + hunks[1].text, "in the diff's order")
    // The file's box: off from any state, then whole.
    p.toggleFile("h.go")
    #expect(p.isEmpty)
    p.toggleFile("h.go")
    #expect(p.state("h.go", of: hunks) == .on && p.selection(order: [:]).files == ["h.go"])
    // A hunk unticked from a whole file leaves the others.
    p.toggleHunk("h.go", header: header, hunk: hunks[0].text, all: hunks)
    #expect(p.whole.isEmpty && p.hunks["h.go"] == [hunks[1].text])
    // A hunk that changed underneath loses its tick.
    p.prune("h.go", header: header, hunks: [Hunk(index: 0, header: "@@", text: "@@ changed\n", lines: [])])
    #expect(p.isEmpty)
}

@Test func creditsTheAgent() {
    #expect(withTrailer("Fix it", agent: nil) == "Fix it")
    #expect(withTrailer("Fix it\n", agent: "claude") == "Fix it\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
    let once = withTrailer("Fix it", agent: "codex")
    #expect(withTrailer(once, agent: "codex") == once, "never twice")
    #expect(agentTrailerName("amp") == "Amp")
}

@Test func reviewCommentsAndEvents() throws {
    let json = #"{"comments":[{"id":"c1","project_id":"r1","branch":"b","file":"h.go","line":24,"note":"2100ms is tight","at":"2026-11-04T10:00:00Z"},{"project_id":"r1","branch":"b","file":"h.go","line":24,"note":"Don't drop the error","by":"maria","source":"github","url":"https://x","replies":[{"by":"claude","note":"Intended."}],"at":"2026-11-04T09:00:00Z"},{"project_id":"r1","branch":"b","file":"t.go","line":3,"note":"x"}]}"#
    let dec = JSONDecoder()
    dec.dateDecodingStrategy = .iso8601
    let list = try dec.decode(ReviewList.self, from: Data(json.utf8))
    let here = comments(list.comments, file: "h.go", line: 24)
    #expect(here.count == 2 && here[0].fromGitHub && here[0].replies?.first?.by == "claude", "the oldest first")
    #expect(openComments(list.comments, file: "h.go") == 2 && openComments(list.comments, file: "x") == 0)
    #expect(try dec.decode(ReviewList.self, from: Data("{}".utf8)).comments.isEmpty)
    let enc = JSONEncoder()
    enc.outputFormatting = .sortedKeys
    let out = String(decoding: try enc.encode(ReviewComment(projectID: "r1", branch: "b", file: "h.go", line: 5, note: "n")), as: UTF8.self)
    #expect(out == #"{"branch":"b","file":"h.go","line":5,"note":"n","project_id":"r1"}"#, "only what review.set takes")
    #expect(ReviewEvent.approve.canSubmit(body: "", comments: 0))
    #expect(!ReviewEvent.comment.canSubmit(body: "  ", comments: 0))
    #expect(ReviewEvent.requestChanges.canSubmit(body: "", comments: 1))
    let sub = String(decoding: try enc.encode(ReviewSubmit(projectID: "r1", branch: "b", event: ReviewEvent.requestChanges.rawValue, body: "s", comments: [PendingComment(file: "h.go", line: 3, note: "n")])), as: UTF8.self)
    #expect(sub == #"{"body":"s","branch":"b","comments":[{"file":"h.go","line":3,"note":"n"}],"event":"request_changes","project_id":"r1"}"#)
}

@Test func graphHelpers() throws {
    let order = ["c3", "c2", "c1"]
    #expect(compareRange("c3", "c1", order: order)! == ("c1", "c3"))
    #expect(compareRange("c1", "c2", order: order)! == ("c1", "c2"))
    #expect(compareRange("c1", "c1", order: order) == nil && compareRange("x", "c1", order: order) == nil)
    var c = LogCommit(hash: "abc123", subject: "Fix the flaky test")
    c.author = "Amit"
    #expect(commitMatches(c, "") && commitMatches(c, "FLAKY") && commitMatches(c, "abc") && commitMatches(c, "amit"))
    #expect(!commitMatches(c, "bc1") && !commitMatches(c, "redis"))
    let d = try JSONDecoder().decode(CommitDetail.self, from: Data(#"{"hash":"h","subject":"s","files":[{"path":"a.go","code":"A","added":3,"deleted":0}],"commits":1}"#.utf8))
    #expect(d.files?.first?.added == 3 && d.parents == nil)
}

@Test func overviewStats() {
    var cal = Calendar(identifier: .gregorian)
    cal.timeZone = TimeZone(identifier: "UTC")!
    let now = Date(timeIntervalSince1970: 1_793_800_000) // a fixed day
    func commit(_ daysAgo: Double, _ who: String) -> LogCommit {
        var c = LogCommit(hash: UUID().uuidString)
        c.time = now.addingTimeInterval(-daysAgo * 86_400)
        c.author = who
        return c
    }
    let log = [commit(0, "claude"), commit(0, "Amit"), commit(1, "claude"), commit(13, "codex"), commit(14, "old"), commit(30, "older")]
    let days = commitsByDay(log, now: now, days: 14, calendar: cal)
    #expect(days.count == 14 && days.last == 2 && days[12] == 1 && days[0] == 1 && days.reduce(0, +) == 4)
    let who = authorsSince(log, now: now, days: 7)
    #expect(who.map(\.author) == ["claude", "Amit"] && who[0].count == 2 && abs(who[0].share - 2.0 / 3) < 0.001)
    #expect(authorsSince([], now: now).isEmpty)
    #expect(cleanLine(branch: "master", ahead: 0, behind: 3, upstream: "origin/master") == "master has nothing uncommitted, 3 commits behind origin/master.")
    #expect(cleanLine(branch: "m", ahead: 1, behind: 0, upstream: "o/m").contains("1 commit ahead"))
    #expect(cleanLine(branch: "m", ahead: 0, behind: 0, upstream: "o/m").contains("up to date"))
    #expect(cleanLine(branch: "m", ahead: 2, behind: 2, upstream: "o/m").contains("2 ahead"))
    #expect(cleanLine(branch: "m", ahead: 0, behind: 0, upstream: nil).contains("no upstream"))
}

@Test func readsLogLines() {
    let out = "$ git log -n 400 …\nabcdef1234\u{1f}Fix it\u{1f}Amit\u{1f}1793800000\nnot a commit\nzzzzzzz\u{1f}x\u{1f}y\u{1f}1\n1234567\u{1f}Sub with \u{1f} inside?\u{1f}a\n"
    let c = parseLogLines(out)
    #expect(c.count == 1 && c[0].hash == "abcdef1234" && c[0].subject == "Fix it" && c[0].author == "Amit")
    #expect(c[0].time == Date(timeIntervalSince1970: 1_793_800_000))
    #expect(parseLogLines("").isEmpty)
}

// A removed line and the new line after it can share a number (old 35,
// new 35): only the new one takes a comment, so the composer opens once.
@Test func removedLinesTakeNoComment() {
    let d = parseDiff("@@ -34,3 +34,2 @@\n running bool\n-step string\n // remotes\n")
    let keys = d.filter { $0.kind != .hunk }.map(commentLine)
    #expect(keys == [34, nil, 35])
    #expect(Set(keys.compactMap { $0 }).count == keys.compactMap { $0 }.count)
}

// The send menu lists the branch's running agents, oldest first: not a
// shell, not an agent that ended, not another branch's or project's.
@Test func agentsToSendTo() throws {
    let json = #"""
    [{"id":"p2","name":"newer","state":"running","project_id":"r1","branch":"main","agent":{"name":"claude","state":"idle"},"created":"2026-11-04T10:00:00Z"},
     {"id":"p1","name":"older","state":"running","project_id":"r1","branch":"main","agent":{"name":"codex","state":"working"},"created":"2026-11-03T10:00:00Z"},
     {"id":"p3","name":"zsh","state":"running","project_id":"r1","branch":"main","created":"2026-11-01T10:00:00Z"},
     {"id":"p4","name":"gone","state":"exited","project_id":"r1","branch":"main","agent":{"name":"claude","state":"done"}},
     {"id":"p5","name":"other branch","state":"running","project_id":"r1","branch":"feat","agent":{"name":"claude","state":"idle"}},
     {"id":"p6","name":"other project","state":"running","project_id":"r2","branch":"main","agent":{"name":"claude","state":"idle"}}]
    """#
    let panes = try conchDecoder().decode([PaneInfo].self, from: Data(json.utf8))
    #expect(branchAgents(panes, project: "r1", branch: "main").map(\.id) == ["p1", "p2"])
    #expect(branchAgents(panes, project: "r1", branch: "nope").isEmpty)
    #expect(branchAgents([], project: "r1", branch: "main").isEmpty)
}

// A file says how much of it is staged, read from the server's changes, and
// a commit takes what is ticked, else what is staged, else everything.
@Test func stagedFilesAndWhatACommitTakes() throws {
    let json = #"""
    [{"path":"a","code":"M","added":1,"deleted":0,"staged":true},
     {"path":"b","code":"M","added":1,"deleted":0,"staged":true,"unstaged":true},
     {"path":"c","code":"?","added":1,"deleted":0,"unstaged":true},
     {"path":"d","code":"M","added":1,"deleted":0}]
    """#
    let files = try JSONDecoder().decode([FileChange].self, from: Data(json.utf8))
    #expect(files.map(\.stage) == [.on, .mixed, .off, .off])
    #expect(commitScope(picked: true, files: files, canStage: true) == .picked)
    #expect(commitScope(picked: false, files: files, canStage: true) == .staged(2))
    // An older server cannot commit the index alone: everything, as before.
    #expect(commitScope(picked: false, files: files, canStage: false) == .everything(4))
    let none = files.filter { $0.staged != true }
    #expect(commitScope(picked: false, files: none, canStage: true) == .everything(2))
    #expect(commitScope(picked: false, files: [], canStage: true) == .everything(0))
}

// A note waits, is sent, and is answered; an answer from before it was last
// sent does not count, and GitHub's comments are their own.
@Test func noteStates() throws {
    let json = #"""
    [{"project_id":"r1","branch":"b","file":"a","note":"w","id":"c1"},
     {"project_id":"r1","branch":"b","file":"a","note":"s","id":"c2","sent_to":"claude","sent_pane":"p3","sent_at":"2026-11-04T10:00:00Z"},
     {"project_id":"r1","branch":"b","file":"a","note":"r","id":"c3","sent_to":"claude","sent_pane":"p3","sent_at":"2026-11-04T10:00:00Z",
      "replies":[{"by":"claude · b","note":"done","at":"2026-11-04T10:05:00Z"}]},
     {"project_id":"r1","branch":"b","file":"a","note":"again","id":"c4","sent_to":"claude","sent_at":"2026-11-04T11:00:00Z",
      "replies":[{"by":"claude · b","note":"old answer","at":"2026-11-04T10:05:00Z"},{"note":"and zero?","at":"2026-11-04T10:30:00Z"}]},
     {"project_id":"r1","branch":"b","file":"a","note":"gh","id":"g1","source":"github"}]
    """#
    let cs = try conchDecoder().decode([ReviewComment].self, from: Data(json.utf8))
    #expect(noteState(cs[0], agentStopped: false) == .waiting)
    #expect(cs[1].sentPane == "p3" && cs[1].sentTo == "claude")
    #expect(noteState(cs[1], agentStopped: false) == .sent(to: "claude", agentStopped: false))
    #expect(noteState(cs[1], agentStopped: true) == .sent(to: "claude", agentStopped: true))
    #expect(noteState(cs[2], agentStopped: true) == .replied(by: "claude · b"))
    // Sent again after the person answered back: the old answer is not this one's.
    #expect(noteState(cs[3], agentStopped: false) == .sent(to: "claude", agentStopped: false))
    #expect(noteState(cs[4], agentStopped: false) == .github)
}

// The heading's box ticks every file or none, says a dash for some, and a
// shift-click ticks the run from the last box clicked, as the anchor is.
@Test func tickingManyFiles() {
    let files = ["a", "b", "c", "d", "e"]
    var p = StagePicks()
    #expect(p.allState(files) == .off)
    #expect(p.allState([]) == .off)
    p.set(files, on: true)
    #expect(p.allState(files) == .on && p.whole.count == 5)
    p.toggleFile("c")
    #expect(p.allState(files) == .mixed)
    p.set(files, on: false)
    #expect(p.isEmpty)

    // A hunk-ticked file counts as some, and set() makes it whole.
    let h = splitHunks("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\n@@ -9 +9 @@\n-p\n+q\n")
    p.toggleHunk("a", header: h.header, hunk: h.hunks[0].text, all: h.hunks)
    #expect(p.allState(["a"], hunks: ["a": h.hunks]) == .mixed)
    p.set(["a"], on: true)
    #expect(p.whole == ["a"] && p.hunks["a"] == nil)

    // Shift-click: b on as the anchor, then shift on e ticks b…e.
    p = StagePicks()
    p.toggleFile("b")
    p.extend(to: "e", from: "b", in: files)
    #expect(p.whole == ["b", "c", "d", "e"])
    // Upwards from an anchor that is off clears the run.
    p.toggleFile("d")
    p.extend(to: "b", from: "d", in: files)
    #expect(p.whole == ["e"])
    // No anchor, or one not in the list: an ordinary click.
    p.extend(to: "a", from: nil, in: files)
    p.extend(to: "c", from: "gone", in: files)
    #expect(p.whole == ["a", "c", "e"])
}

// A plain button takes clicks only where it draws. An empty tick box draws
// a thin outline and nothing inside, so clicks went through it to the row
// (which opened the file) and the box never ticked — accessibility presses,
// which the live checks used, skip hit-testing and so never saw it. Every
// plain-style button in the git views says what its clickable shape is.
@Test func plainButtonsHaveAShape() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    for f in ["GitTab.swift", "CommitsView.swift"] {
        let src = try String(contentsOfFile: dir + "/" + f, encoding: .utf8)
        var stale: [Int] = []
        let lines = src.components(separatedBy: "\n")
        for (i, l) in lines.enumerated() where l.contains(".buttonStyle(.plain)") {
            // The label is the line itself, or the block above it.
            let from = max(0, i - 14)
            let near = lines[from...i].joined(separator: "\n")
            let label = near.range(of: "Button", options: .backwards).map { String(near[$0.lowerBound...]) } ?? near
            if !label.contains("contentShape") && !label.contains(".background(") {
                stale.append(i + 1)
            }
        }
        #expect(stale.isEmpty, "\(f): a plain button without a clickable shape at line(s) \(stale)")
    }
}

// A pull request's state, said: conflicts (with the files when the server
// found them), checks running, failing or passed, and the review.
@Test func pullRequestStatus() throws {
    let json = #"""
    {"number":87,"state":"OPEN","checks":"pending","passed":3,"total":7,"conflicting":true,"review":"REVIEW_REQUIRED","title":"conch/app rex","head":"60123ac"}
    """#
    let pr = try JSONDecoder().decode(PRInfo.self, from: Data(json.utf8))
    #expect(pr.conflicting == true && pr.passed == 3 && pr.total == 7 && pr.title == "conch/app rex")
    var st = prStatus(pr, base: "master", conflicts: ["cmd/conch/main.go"])
    #expect(st.merge == .init(tone: .bad, text: "Conflicts with master in cmd/conch/main.go"))
    #expect(st.ci == .init(tone: .busy, text: "Checks running · 3 of 7 done"))
    #expect(st.progress == 3.0 / 7 && st.conflicting && !st.failing)
    #expect(st.review?.text == "Review required")
    // GitHub's word alone, no files; several files counted.
    #expect(prStatus(pr, base: "master", conflicts: []).merge.text == "Conflicts with master")
    #expect(prStatus(pr, base: "main", conflicts: ["a", "b", "c"]).merge.text == "Conflicts with main in a and 2 more")

    var ok = PRInfo(number: 1, state: "OPEN", checks: "pass")
    ok.total = 4; ok.passed = 4; ok.review = "APPROVED"
    st = prStatus(ok, base: "main", conflicts: [])
    #expect(st.merge.tone == .ok && st.ci.text == "Checks passed · 4/4" && st.review?.tone == .ok && !st.conflicting)
    ok.checks = "fail"; ok.passed = 2
    #expect(prStatus(ok, base: "main", conflicts: []).failing && prStatus(ok, base: "main", conflicts: []).ci.text == "Checks failing · 2 of 4 passed")
    // No checks at all; a draft; merged and closed say so and are not "conflicting".
    #expect(prStatus(PRInfo(number: 2), base: "main", conflicts: []).ci.text == "No checks")
    #expect(prStatus(PRInfo(number: 2), base: "main", conflicts: []).progress == nil)
    #expect(prStatus(PRInfo(number: 2, draft: true), base: "main", conflicts: []).merge.tone == .quiet)
    var merged = PRInfo(number: 3, state: "MERGED"); merged.conflicting = true
    #expect(prStatus(merged, base: "main", conflicts: []).merge.text == "Merged into main" && !prStatus(merged, base: "main", conflicts: []).conflicting)
    #expect(prStatus(PRInfo(number: 4, state: "CLOSED"), base: "main", conflicts: []).merge.tone == .quiet)

    let ask = conflictPrompt(number: 87, branch: "conch/app-rex", base: "master", files: ["cmd/conch/main.go"])
    #expect(ask.contains("#87") && ask.contains("in cmd/conch/main.go") && ask.contains("merge origin/master") && ask.contains("don't rebase"))
    #expect(conflictPrompt(number: 1, branch: "b", base: "main", files: []).hasPrefix("Pull request #1 (b) conflicts with main.\n"))
}

// Handed work is begun, worked, asks, and finishes — and an agent still
// idle from before it was given the work has not finished, it has not begun.
@Test func handedWork() throws {
    let at = Date(timeIntervalSince1970: 1_793_900_000)
    func pane(_ state: String?, since: Double? = nil, running: Bool = true) throws -> PaneInfo {
        var json = #"{"id":"p3","name":"claude","state":"\#(running ? "running" : "exited")""#
        if let state {
            json += #","agent":{"name":"claude","state":"\#(state)""#
            if let since { json += #","since":"\#(ISO8601DateFormatter().string(from: at.addingTimeInterval(since)))""# }
            json += "}"
        }
        return try conchDecoder().decode(PaneInfo.self, from: Data((json + "}").utf8))
    }
    #expect(handoffState(nil, handedAt: at) == .gone)
    #expect(handoffState(try pane("working", running: false), handedAt: at) == .gone)
    #expect(handoffState(try pane(nil), handedAt: at) == .starting)
    #expect(handoffState(try pane("idle", since: -60), handedAt: at) == .starting)
    #expect(handoffState(try pane("idle"), handedAt: at) == .starting)
    #expect(handoffState(try pane("working", since: 2), handedAt: at) == .working)
    #expect(handoffState(try pane("blocked", since: 30), handedAt: at) == .waiting)
    #expect(handoffState(try pane("done", since: 300), handedAt: at) == .finished)
    #expect(handoffState(try pane("idle", since: 300), handedAt: at) == .finished)

    let agents = try [pane("idle", since: -60), pane("working", since: 1)].enumerated().map { i, p -> PaneInfo in
        var p = p; p.id = "p\(i + 1)"; return p
    }
    #expect(branchAgent(agents, handedTo: "p1")?.id == "p1")
    #expect(branchAgent(agents, handedTo: nil)?.id == "p2")
    #expect(branchAgent(agents, handedTo: "gone")?.id == "p2")
    #expect(branchAgent([agents[0]], handedTo: nil)?.id == "p1")
    #expect(branchAgent([], handedTo: nil) == nil)
}

// CI in a few words for the header, and the checks listed failing first.
@Test func ciInAFewWords() throws {
    let json = #"""
    {"number":87,"checks":"pending","passed":1,"total":3,"runs":[
     {"name":"test (ubuntu-latest)","state":"pending","url":"https://gh/1"},
     {"name":"lint","state":"pass"},
     {"name":"test (macos-latest)","state":"pending"}]}
    """#
    var pr = try JSONDecoder().decode(PRInfo.self, from: Data(json.utf8))
    #expect(pr.runs?.count == 3 && pr.runs?[0].url == "https://gh/1")
    #expect(ciChip(pr)?.text == "CI running 1/3" && ciChip(pr)?.tone == .busy)
    pr.checks = "fail"; pr.runs?[2].state = "fail"
    #expect(ciChip(pr)?.text == "CI failing · 1 failed" && ciChip(pr)?.tone == .bad)
    #expect(checksToRead(pr.runs ?? []).map(\.name) == ["test (macos-latest)", "test (ubuntu-latest)", "lint"])
    pr.checks = "pass"; pr.passed = 3
    #expect(ciChip(pr)?.text == "CI passed 3/3")
    // No checks; an older server with counts and no names.
    #expect(ciChip(PRInfo(number: 1)) == nil)
    var old = PRInfo(number: 2, checks: "pending"); old.total = 2
    #expect(ciChip(old)?.text == "CI running 0/2")
    #expect(checksToRead([]).isEmpty)
}

// The server's trial merge is with the base as it is here. With commits on
// it not pushed, a conflict it finds may be with those alone: GitHub says
// the pull request is clean, so that is a note, not a conflict to resolve —
// the card once kept saying "Conflicts" after the conflict was resolved.
@Test func conflictOnlyWithTheUnpushedBase() {
    var pr = PRInfo(number: 87, state: "OPEN", checks: "pass")
    pr.conflicting = false
    let st = prStatus(pr, base: "master", conflicts: ["docs/testing/end-to-end.md"], baseUnpushed: 4)
    #expect(!st.conflicting && st.merge.tone == .ok && st.merge.text == "No conflicts with master")
    #expect(st.local == .init(tone: .warn, text: "Your local master (4 commits not pushed) would conflict in docs/testing/end-to-end.md"))
    // One commit; the base pushed (nothing local) — the trial merge is then
    // GitHub's own base and its conflict is the pull request's.
    #expect(prStatus(pr, base: "main", conflicts: ["a"], baseUnpushed: 1).local?.text.contains("(1 commit not pushed)") == true)
    let pushed = prStatus(pr, base: "main", conflicts: ["a"], baseUnpushed: 0)
    #expect(pushed.conflicting && pushed.merge.tone == .bad && pushed.local == nil)
    // GitHub saying it conflicts wins whatever is unpushed.
    pr.conflicting = true
    let gh = prStatus(pr, base: "main", conflicts: ["a"], baseUnpushed: 3)
    #expect(gh.conflicting && gh.local == nil && gh.merge.text == "Conflicts with main in a")
    // Clean everywhere: no note.
    pr.conflicting = false
    #expect(prStatus(pr, base: "main", conflicts: [], baseUnpushed: 3).local == nil)
    // Merged: nothing to warn about.
    var merged = PRInfo(number: 9, state: "MERGED")
    merged.conflicting = false
    #expect(prStatus(merged, base: "main", conflicts: ["a"], baseUnpushed: 2).local == nil)
}

// The editor's language by name, and its colouring: comments, strings with
// escapes, numbers not inside names, keywords as whole words.
@Test func editorLanguagesAndColours() {
    #expect(CodeLanguage(path: "cmd/main.go") == .go && CodeLanguage(path: "App.TSX") == .typescript)
    #expect(CodeLanguage(path: "Makefile") == .shell && CodeLanguage(path: "notes") == .plain && CodeLanguage(path: "a.md") == .markdown)
    let go = "package main\n// say hi\nfunc f() { s := \"a\\\"b\"; n := 42; x2 := 1 }\n"
    let runs = highlight(go, .go)
    func text(_ r: NSRange) -> String { (go as NSString).substring(with: r) }
    #expect(runs.contains { $0.token == .keyword && text($0.range) == "package" })
    #expect(runs.contains { $0.token == .comment && text($0.range) == "// say hi" })
    #expect(runs.contains { $0.token == .string && text($0.range) == "\"a\\\"b\"" })
    #expect(runs.contains { $0.token == .number && text($0.range) == "42" })
    #expect(!runs.contains { $0.token == .number && text($0.range) == "2" }) // inside x2
    #expect(!runs.contains { $0.token == .keyword && text($0.range) == "main" })
    // Block comments, a string left open at the end, and none for plain text.
    let c = "/* a\nb */ int x = 'q"
    let first = highlight(c, .c).first
    #expect(first?.token == .comment && first?.range.location == 0 && first?.range.length == 9)
    #expect(highlight(c, .c).last?.token == .string)
    #expect(highlight("anything 12 // x", .plain).isEmpty)
    #expect(highlight("", .go).isEmpty)
    // Markdown: a heading line and a code span.
    let md = "# Title\nuse `go test` here\n"
    let m = highlight(md, .markdown)
    #expect(m.contains { $0.token == .heading && ($0.range.location, $0.range.length) == (0, 7) })
    #expect(m.contains { $0.token == .string && (md as NSString).substring(with: $0.range) == "`go test`" })
    // Non-ASCII before a run keeps the offsets right (UTF-16, as NSString).
    let u = "é // x"
    #expect(highlight(u, .go).first.map { (u as NSString).substring(with: $0.range) } == "// x")
}

// The checkouts a project's files can be shown from: main first, then the
// worktrees by branch, then branches checked out nowhere; editable only
// with fs.write's fingerprint.
@Test func filesCheckoutsAndEditable() throws {
    let json = #"""
    {"id":"r1","name":"conch","path":"/w/conch",
     "worktrees":[{"path":"/w/conch.worktrees/b","branch":"feat-b"},{"path":"/w/conch","branch":"master","main":true},{"path":"/w/conch.worktrees/a","branch":"feat-a"}],
     "branches":[{"name":"master"},{"name":"feat-a"},{"name":"old"},{"name":"feat-b"},{"name":"alpha"}]}
    """#
    let p = try conchDecoder().decode(ProjectInfo.self, from: Data(json.utf8))
    let cs = checkouts(p)
    #expect(cs.map(\.branch) == ["master", "feat-a", "feat-b", "alpha", "old"])
    #expect(cs[0].main && cs[0].path == "/w/conch" && cs[0].label == "master (main checkout)")
    #expect(cs[1].path == "/w/conch.worktrees/a" && cs[3].path == nil)
    // A project from an older server, no worktrees: its path, as main.
    let bare = try conchDecoder().decode(ProjectInfo.self, from: Data(#"{"id":"r2","name":"x","path":"/x"}"#.utf8))
    #expect(checkouts(bare).map(\.path) == ["/x"])

    func read(_ j: String) throws -> FSRead { try JSONDecoder().decode(FSRead.self, from: Data(j.utf8)) }
    #expect(try read(#"{"data":"a","hash":"ab12"}"#).editable)
    #expect(try !read(#"{"data":"a"}"#).editable)                       // an older server: no fingerprint
    #expect(try !read(#"{"data":"a","hash":"ab","truncated":true}"#).editable)
    #expect(try !read(#"{"binary":true,"hash":"ab"}"#).editable)
    #expect(try read(#"{"data":"","hash":"e3b0"}"#).editable)            // an empty file is text
    // As the server sends one: data is omitempty, so an empty file has none.
    #expect(try read(#"{"size":0,"hash":"e3b0"}"#).editable)
    #expect(try read(#"{"size":0,"hash":"e3b0"}"#).isText)
    #expect(try !read(#"{"binary":true,"size":9}"#).isText)
}
