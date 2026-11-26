import Foundation
import Testing
import WebKit
@testable import ConchKit

// A branch's Changes drawn by conch-ui: what it is sent from the model's
// state, what it may ask back, and the view itself clicked through in a
// real web view.

private func changes(files: [FileChange], committed: [FileChange] = [], worktree: Bool = true, commits: Int = 0) -> Changes {
    var c = try! JSONDecoder().decode(Changes.self, from: Data(#"{"project_id":"p1","branch":"fix/overdraft","base":"main","files":[]}"#.utf8))
    c.files = files
    c.committedFiles = committed
    c.worktree = worktree ? "/w/fix" : nil
    c.commits = (0..<commits).map { CommitInfo(hash: String(repeating: "\($0 % 10)", count: 40), subject: "commit \($0)") }
    return c
}

private let diffText = """
diff --git a/post.go b/post.go
--- a/post.go
+++ b/post.go
@@ -1,3 +1,4 @@ func Post
 package accounts
-var x = 1
+var x = 2
+// a note on it
 func Post() {}
@@ -10,2 +11,2 @@ func Other
 a
-b
+c
"""

private func base() -> ChangesWebInput {
    var i = ChangesWebInput()
    i.changes = changes(files: [FileChange(path: "post.go", code: "M", added: 2, deleted: 1), FileChange(path: "cache.go", code: "A", added: 3, deleted: 0)],
                        committed: [FileChange(path: "old.go", code: "M", added: 1, deleted: 1)], commits: 3)
    let split = splitHunks(diffText)
    i.selected = "post.go"
    i.hunks = split.hunks
    i.hunkOrder = ["post.go": split.hunks]
    i.commitWhat = "every change (2 files)"
    i.canStage = true
    i.canThread = true
    i.now = Date(timeIntervalSince1970: 10_000)
    return i
}

private func note(_ id: String, line: Int, file: String = "post.go", by: String? = nil) -> ReviewComment {
    var c = ReviewComment(projectID: "p1", branch: "fix/overdraft", file: file, line: line, note: "note \(id)", id: id)
    c.by = by
    c.at = Date(timeIntervalSince1970: 9_000)
    return c
}

// MARK: what the view is sent

@Test func changesWebListsTheFilesWithTheirTicks() {
    var i = base()
    i.picks.toggleFile("cache.go")
    i.live = ["post.go"]
    i.comments = [note("a", line: 2), note("b", line: 3, file: "cache.go")]
    i.pending = [PendingComment(file: "post.go", line: 2, note: "for the PR")]
    let v = changesWebView(i, theme: .conch)
    #expect(v.branch == "fix/overdraft" && v.base == "main" && v.worktree && !v.loading)
    #expect(v.files.map(\.path) == ["post.go", "cache.go"])
    #expect(v.files.map(\.tick) == ["off", "on"])
    #expect(v.files[0].notes == 2 && v.files[1].notes == 1, "a note and a pending comment each count")
    #expect(v.files[0].live && !v.files[1].live)
    #expect(v.allTick == "mixed")
    #expect(v.committed.map(\.path) == ["old.go"] && v.committed[0].tick == nil, "a committed file has no box")
    #expect(v.commits.count == 3 && v.commitWhat == "every change (2 files)")
}

@Test func changesWebAgainstTheBaseHasNoBoxes() {
    var i = base()
    i.changes = changes(files: [FileChange(path: "post.go", code: "M", added: 2, deleted: 1)], worktree: false)
    let v = changesWebView(i, theme: .conch)
    #expect(!v.worktree && v.allTick == nil)
    #expect(v.files.first?.tick == nil && v.files.first?.stage == "off")
}

@Test func changesWebSendsTheSelectedFilesHunks() {
    var i = base()
    let second = i.hunks[1]
    i.picks.toggleHunk("post.go", header: splitHunks(diffText).header, hunk: second.text, all: i.hunks)
    let v = changesWebView(i, theme: .conch)
    #expect(v.selected?.path == "post.go" && v.selected?.committed == false)
    #expect(v.hunks.map(\.ticked) == [false, true])
    #expect(v.diff.isEmpty)
    let lines = v.hunks[0].lines
    #expect(lines.map(\.kind) == ["context", "removed", "added", "added", "context"])
    // A comment is kept on the line's number in the new file; a removed
    // line has none there.
    #expect(lines.map(\.cl) == [1, nil, 2, 3, 4])
}

@Test func changesWebSendsACommittedFilesDiffWithoutItsHeaders() {
    var i = base()
    i.selected = "old.go"
    i.selectedCommitted = true
    i.hunks = []
    i.diff = parseDiff(diffText)
    let v = changesWebView(i, theme: .conch)
    #expect(v.hunks.isEmpty && !v.diff.isEmpty)
    #expect(!v.diff.contains { $0.kind == "meta" })
    #expect(v.diff.first?.kind == "hunk" && v.diff.first?.cl == nil)
}

@Test func changesWebSaysWhereEachNoteStands() {
    var i = base()
    var github = note("g", line: 1, by: "reviewer")
    github.source = "github"
    github.url = "https://github.com/o/r/pull/1#c"
    github.outdated = true
    var waiting = note("w", line: 2)
    waiting.replies = [ReviewReply(by: nil, note: "more", at: nil)]
    var working = note("s", line: 3)
    working.sentAt = Date(timeIntervalSince1970: 9_500)
    working.sentTo = "claude"
    working.sentPane = "p7"
    var quiet = working
    quiet.id = "q"
    quiet.sentPane = "p8"
    var answered = working
    answered.id = "r"
    answered.replies = [ReviewReply(by: "claude", note: "done", at: Date(timeIntervalSince1970: 9_600))]
    i.comments = [github, waiting, working, quiet, answered, note("x", line: 2, file: "cache.go")]
    i.agentStopped = { $0 == "p8" }
    let v = changesWebView(i, theme: .conch)
    let byID = Dictionary(uniqueKeysWithValues: v.comments.map { ($0.id, $0) })
    #expect(v.comments.count == 5, "only the selected file's")
    #expect(byID["g"]?.chip?.text == "on the pull request" && byID["g"]?.github == true && byID["g"]?.canResolve == false && byID["g"]?.outdated == true)
    #expect(byID["w"]?.chip?.text == "answered back · waiting to be sent" && byID["w"]?.waiting == true && byID["w"]?.sentPane == nil)
    #expect(byID["w"]?.replies.first?.by == "you" && byID["w"]?.replies.first?.agent == false)
    #expect(byID["s"]?.chip?.text == "sent to claude · working" && byID["s"]?.chip?.tone == "work" && byID["s"]?.sentPane == "p7" && byID["s"]?.canReply == true)
    #expect(byID["q"]?.chip?.text == "sent to claude · no answer" && byID["q"]?.noAnswer == true)
    #expect(byID["r"]?.chip?.text == "answered by claude" && byID["r"]?.chip?.tone == "ok" && byID["r"]?.replies.first?.agent == true)
    // An older server keeps no threads: nothing to reply to.
    i.canThread = false
    #expect(changesWebView(i, theme: .conch).comments.allSatisfy { !$0.canReply })
}

@Test func changesWebSendsPendingSentAndTheComposer() {
    var i = base()
    let p = PendingComment(file: "post.go", line: 2, note: "for the PR")
    i.pending = [p, PendingComment(file: "cache.go", line: 1, note: "elsewhere")]
    i.sent = [WebSentNote(id: "s1", file: "post.go", line: 3, note: "sent", to: "claude", pane: "p7", at: Date(timeIntervalSince1970: 9_000)),
              WebSentNote(id: "s2", file: "cache.go", line: 1, note: "x", to: "codex", pane: "p8", at: Date())]
    i.composingFile = "post.go"
    i.composingLine = 3
    var v = changesWebView(i, theme: .conch)
    #expect(v.pending.map(\.id) == [p.id.uuidString] && v.sent.map(\.id) == ["s1"])
    #expect(v.composing == 3)
    i.composingFile = "cache.go"
    v = changesWebView(i, theme: .conch)
    #expect(v.composing == nil, "a composer on another file is not drawn here")
}

@Test func changesWebSaysWhatCanBeCommitted() {
    var i = base()
    #expect(changesWebView(i, theme: .conch).commit.canCommit == false, "no message")
    i.message = "  \n "
    #expect(changesWebView(i, theme: .conch).commit.canCommit == false, "a blank one is none")
    i.message = "Hold the lock"
    #expect(changesWebView(i, theme: .conch).commit.canCommit == true)
    i.busy = "Committing"
    #expect(changesWebView(i, theme: .conch).commit.canCommit == false, "not while another job runs")
    i.busy = nil
    i.changes = changes(files: [])
    #expect(changesWebView(i, theme: .conch).commit.canCommit == false, "nothing to commit")
    i = base()
    #expect(changesWebView(i, theme: .conch).commit.canStageNow == false, "nothing ticked")
    i.picks.toggleFile("post.go")
    #expect(changesWebView(i, theme: .conch).commit.canStageNow == true)
    i.canStage = false
    #expect(changesWebView(i, theme: .conch).commit.canStageNow == false, "an older server cannot stage")
}

@Test func changesWebReviewAndSending() {
    var i = base()
    i.notes = 2
    var v = changesWebView(i, theme: .conch)
    #expect(!v.review.prOpen && v.review.prNumber == nil)
    #expect(v.review.events.map(\.id) == ["comment", "approve", "request_changes"])
    #expect(v.review.canSubmit, "approving needs nothing said")
    #expect(!v.send.enabled, "notes but nobody to send them to on an older server")
    i.hasAgent = true
    #expect(changesWebView(i, theme: .conch).send.enabled)
    i.canPick = true
    i.branchAgents = [PaneInfo(id: "p7", name: "claude", agent: AgentStatus(name: "claude", state: .working)),
                      PaneInfo(id: "p8", name: "", agent: AgentStatus(name: "codex", state: .blocked))]
    i.startable = [("claude", "Claude Code")]
    i.event = .comment
    i.pr = PRInfo(number: 12, state: "OPEN")
    v = changesWebView(i, theme: .conch)
    #expect(v.review.prOpen && v.review.prNumber == 12 && !v.review.canSubmit, "a comment needs something said")
    #expect(v.send.agents.map(\.label) == ["claude — working", "codex — waiting on you"])
    #expect(v.send.agents.map(\.disabled) == [false, true], "not one waiting on you")
    #expect(v.send.startable.first?.title == "Claude Code")
    i.notes = 0
    #expect(!changesWebView(i, theme: .conch).send.enabled, "no notes, nothing to send")
}

@Test func changesWebChecks() {
    var i = base()
    var pr = PRInfo(number: 12, state: "OPEN", checks: "fail", url: "https://github.com/o/r/pull/12")
    pr.passed = 1
    pr.total = 3
    pr.runs = [PRCheck(name: "lint", state: "pass", url: "https://github.com/o/r/actions/1"),
               PRCheck(name: "ci", state: "fail", url: "https://app.circleci.com/x", source: "circleci", readable: false)]
    i.pr = pr
    i.canRerun = true
    i.fixLabel = "Send the failure to claude"
    i.ciCard = CICard(pr, uses: [])
    i.canInstallCI = true
    var v = changesWebView(i, theme: .conch)
    let c = try! #require(v.checks)
    #expect(c.number == 12 && c.status == "fail" && c.failing && c.canRerun && c.fixLabel == "Send the failure to claude")
    #expect(c.progress != nil && c.runs.count == 2 && c.runs[1].where == "app.circleci.com")
    #expect(c.card?.install == "Install CircleCI…" && c.card?.open == "Open in CircleCI")
    i.canInstallCI = false
    i.pr?.checks = "pass"
    v = changesWebView(i, theme: .conch)
    #expect(v.checks?.card?.install == nil && v.checks?.progress == nil, "nothing to install elsewhere; a finished run has no bar")
    i.pr = nil
    #expect(changesWebView(i, theme: .conch).checks == nil)
}

@Test func changesWebWhileReading() {
    var i = ChangesWebInput()
    #expect(changesWebView(i, theme: .conch).loading)
    i.error = "busybox is not connected"
    let v = changesWebView(i, theme: .conch)
    #expect(!v.loading && v.files.isEmpty && v.selected == nil)
}

// MARK: what the view asks

private func ask(_ m: [String: Any]) -> ChangesIntent? { changesIntent(m.merging(["view": "changes"]) { a, _ in a }) }

@Test func changesIntentsParse() {
    #expect(ask(["type": "select", "path": "a.go", "committed": true]) == .select(path: "a.go", committed: true))
    #expect(ask(["type": "tickAll"]) == .tickAll)
    #expect(ask(["type": "tickFile", "path": "a.go", "shift": true]) == .tickFile(path: "a.go", shift: true))
    #expect(ask(["type": "tickFile", "path": "a.go"]) == .tickFile(path: "a.go", shift: false))
    #expect(ask(["type": "tickHunk", "index": 2]) == .tickHunk(2))
    #expect(ask(["type": "stageFile", "path": "a.go", "unstage": false]) == .stageFile(path: "a.go", unstage: false))
    #expect(ask(["type": "copyPath", "path": "a.go"]) == .copyPath("a.go"))
    #expect(ask(["type": "split", "on": true]) == .split(true))
    #expect(ask(["type": "compose", "line": 4]) == .compose(line: 4))
    #expect(ask(["type": "cancelCompose"]) == .cancelCompose)
    #expect(ask(["type": "addComment", "line": 4, "note": "why", "pr": false]) == .addComment(line: 4, note: "why", forPR: false))
    #expect(ask(["type": "reply", "id": "n1", "note": "ok"]) == .reply(id: "n1", note: "ok"))
    #expect(ask(["type": "resolve", "id": "n1"]) == .resolve("n1"))
    #expect(ask(["type": "dropPending", "id": "u"]) == .dropPending("u"))
    #expect(ask(["type": "dismissSent", "id": "u"]) == .dismissSent("u"))
    #expect(ask(["type": "openPane", "pane": "p7"]) == .openPane("p7"))
    #expect(ask(["type": "openURL", "url": "https://x.io/1"]) == .openURL(URL(string: "https://x.io/1")!))
    #expect(ask(["type": "sendNotes"]) == .sendNotes(to: nil, start: nil))
    #expect(ask(["type": "sendNotes", "to": "p7"]) == .sendNotes(to: "p7", start: nil))
    #expect(ask(["type": "sendNotes", "start": "claude"]) == .sendNotes(to: nil, start: "claude"))
    #expect(ask(["type": "message", "text": ""]) == .message(""), "a message may be emptied")
    #expect(ask(["type": "credit", "on": false]) == .credit(false))
    #expect(ask(["type": "event", "id": "request_changes"]) == .event(.requestChanges))
    #expect(ask(["type": "reviewBody", "text": "looks fine"]) == .reviewBody("looks fine"))
    for t in ["writeMessage", "stage", "commit", "submitReview", "reviewAgain", "openPR", "fixCI", "rerunCI", "installCI", "openCIService", "ready"] {
        #expect(ask(["type": t]) != nil, "\(t)")
    }
}

@Test func changesIntentsRefuseWhatIsWrong() {
    #expect(ask(["type": "select", "path": "a.go"]) == nil, "which side it is on")
    #expect(ask(["type": "select", "path": "", "committed": false]) == nil)
    #expect(ask(["type": "tickHunk", "index": -1]) == nil)
    #expect(ask(["type": "tickHunk", "index": "1"]) == nil)
    #expect(ask(["type": "tickHunk", "index": 1.5]) == nil)
    #expect(ask(["type": "compose", "line": 0]) == nil)
    #expect(ask(["type": "addComment", "line": 4, "note": "  ", "pr": false]) == nil, "a blank note")
    #expect(ask(["type": "addComment", "line": 4, "note": "x"]) == nil, "which kind it is")
    #expect(ask(["type": "addComment", "line": 4, "note": String(repeating: "x", count: changesWebTextCap + 1), "pr": false]) == nil, "too long")
    #expect(ask(["type": "reply", "id": "n1", "note": ""]) == nil)
    #expect(ask(["type": "sendNotes", "to": "p7", "start": "claude"]) == nil, "one or the other")
    #expect(ask(["type": "event", "id": "merge"]) == nil)
    #expect(ask(["type": "openURL", "url": "javascript:alert(1)"]) == nil)
    #expect(ask(["type": "split", "on": 1]) == nil)
    #expect(ask(["type": "runShell", "cmd": "x"]) == nil)
    #expect(changesIntent(["view": "inspector", "type": "commit"]) == nil, "another view's")
}

// MARK: the view in WebKit

@MainActor @Test func changesViewDrawsAndClicksThrough() async throws {
    let page = ConchUIPage(.changes)
    try await page.await(1)
    var i = base()
    i.comments = [note("n1", line: 2, by: "codex")]
    i.notes = 1
    i.canPick = true
    i.branchAgents = [PaneInfo(id: "p7", name: "claude", agent: AgentStatus(name: "claude", state: .working))]
    i.startable = [("codex", "Codex")]
    i.agentName = "claude"
    var pr = PRInfo(number: 12, state: "OPEN", checks: "pending", url: "https://github.com/o/r/pull/12")
    pr.runs = [PRCheck(name: "lint", state: "pass", url: "https://github.com/o/r/actions/1")]
    i.pr = pr
    try await page.show(changesWebView(i, theme: .conch))
    // Headings are drawn in capitals, which innerText says as drawn.
    let text = try await page.text().lowercased()
    for want in ["Uncommitted · 2", "post.go", "cache.go", "Committed on fix/overdraft · 1", "Commits · 3", "@@ -1,3 +1,4 @@ func Post",
                 "var x = 2", "note n1", "codex", "Commit", "Checks", "lint", "Review · #12", "1 note for the agent", "Credit claude"] {
        #expect(text.contains(want.lowercased()), "missing \(want)")
    }
    let n = page.said.count
    try await page.click("cache.go")
    try await page.click("Tick cache.go to stage or commit it", shift: true)
    try await page.click("Tick every uncommitted file")
    try await page.click("Commit this hunk")
    try await page.click("Split")
    try await page.click("Resolve")
    try await page.click("Comment on line 3")
    try await page.click("lint")
    try await page.click("✦ Write")
    try await page.click("Request changes")
    try await page.click("Send ▾")
    try await page.click("claude — working")
    // Opening the send menu says nothing; picking from it does.
    try await page.await(n + 11)
    #expect(page.types(from: n) == ["select", "tickFile", "tickAll", "tickHunk", "split", "resolve", "compose", "openURL", "writeMessage", "event", "sendNotes"])
    let got = page.said.dropFirst(n).compactMap(changesIntent)
    #expect(got.contains(.select(path: "cache.go", committed: false)))
    #expect(got.contains(.tickFile(path: "cache.go", shift: true)))
    #expect(got.contains(.tickHunk(0)))
    #expect(got.contains(.split(true)))
    #expect(got.contains(.resolve("n1")))
    #expect(got.contains(.compose(line: 3)))
    #expect(got.contains(.openURL(URL(string: "https://github.com/o/r/actions/1")!)))
    #expect(got.contains(.event(.requestChanges)))
    #expect(got.contains(.sendNotes(to: "p7", start: nil)))
}

@MainActor @Test func changesViewTyping() async throws {
    let page = ConchUIPage(.changes)
    try await page.await(1)
    var i = base()
    i.composingFile = "post.go"
    i.composingLine = 3
    i.pr = PRInfo(number: 12, state: "OPEN")
    try await page.show(changesWebView(i, theme: .conch))
    let n = page.said.count
    // A note: return adds it for the agent.
    try await page.type("Comment on line 3", "check the lock", thenPress: "Enter")
    // The commit message reaches the model as it is typed; ⌘↩ commits.
    try await page.type("Commit message", "Hold the lock")
    try await page.type("Review summary", "Looks right")
    try await page.await(n + 3)
    let got = page.said.dropFirst(n).compactMap(changesIntent)
    #expect(got.contains(.addComment(line: 3, note: "check the lock", forPR: false)))
    #expect(got.contains(.message("Hold the lock")))
    #expect(got.contains(.reviewBody("Looks right")))
    // ⌘↩ in the message box commits once the model can.
    i.composingFile = nil
    i.message = "Hold the lock"
    try await page.show(changesWebView(i, theme: .conch))
    let m = page.said.count
    try await page.type("Commit message", "Hold the lock", thenPress: "Enter", meta: true)
    try await page.await(m + 2)
    #expect(page.said.dropFirst(m).compactMap(changesIntent).contains(.commit))
}

@MainActor @Test func changesViewShowsWhatItIsGivenAsText() async throws {
    let page = ConchUIPage(.changes)
    try await page.await(1)
    var i = base()
    i.changes = changes(files: [FileChange(path: "<img src=x onerror=alert(1)>.go", code: "M", added: 1, deleted: 0)])
    i.selected = nil
    i.hunks = []
    try await page.show(changesWebView(i, theme: .conch))
    #expect(try await page.text().contains("<img src=x onerror=alert(1)>.go"))
    #expect(try await page.run("document.querySelectorAll('#root img').length") as? Int == 0)
}

@MainActor @Test func changesViewWithNothing() async throws {
    let page = ConchUIPage(.changes)
    try await page.await(1)
    try await page.show(changesWebView(ChangesWebInput(), theme: .conch))
    #expect(try await page.text().contains("Reading"))
    var i = base()
    i.changes = changes(files: [])
    i.selected = nil
    i.hunks = []
    try await page.show(changesWebView(i, theme: .conch))
    let t = try await page.text().lowercased()
    #expect(t.contains("nothing uncommitted") && t.contains("nothing to show") && t.contains("nothing to commit"))
}

// MARK: the switch back

@Test func webViewsAreOnUnlessSwitchedOff() {
    #expect(webViewsEnabled(toml: nil, env: [:]))
    #expect(webViewsEnabled(toml: "[app]\nweb_views = true\n", env: [:]))
    #expect(!webViewsEnabled(toml: "[app]\nweb_views = false\n", env: [:]))
    #expect(webViewsEnabled(toml: "[other]\nweb_views = false\n", env: [:]), "only [app]'s")
    #expect(!webViewsEnabled(toml: nil, env: [webViewsEnv: "0"]))
    #expect(!webViewsEnabled(toml: nil, env: [webViewsEnv: "Off"]))
    #expect(webViewsEnabled(toml: "[app]\nweb_views = false\n", env: [webViewsEnv: "1"]), "the variable wins")
    #expect(!webViewsEnabled(toml: "[app]\nweb_views = false\n", env: [webViewsEnv: " "]), "an empty one says nothing")
}
