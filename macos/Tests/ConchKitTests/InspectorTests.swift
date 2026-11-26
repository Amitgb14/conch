import Foundation
import WebKit
import Testing
@testable import ConchKit

// The inspector, a conch-ui view: which branch it shows, what it is
// sent, what it may ask back, and the view itself in a real web view.

// MARK: which branch

@Test func inspectorFollowsAChangesTab() {
    let t = inspectorTarget(focused: changesKey("busybox", "p1", "fix/overdraft"), pane: nil, project: nil)
    #expect(t == InspectorTarget(machine: "busybox", project: "p1", branch: "fix/overdraft"))
}

@Test func inspectorFollowsAPanesBranch() {
    let pane = PaneInfo(id: "p3", name: "claude", projectID: "p1", branch: "fix/overdraft")
    let t = inspectorTarget(focused: key("local", "p3"), pane: pane, project: ProjectInfo(id: "p1", name: "ledger", path: "/w/ledger"))
    #expect(t == InspectorTarget(machine: "local", project: "p1", branch: "fix/overdraft"))
}

@Test func inspectorFindsTheBranchByTheFolder() {
    // An older server names no branch on the pane: the worktree it sits in
    // says, the deepest one holding its folder (a worktree inside the main
    // checkout is not the main checkout).
    var pane = PaneInfo(id: "p3", name: "sh", projectID: "p1", cwd: "/w/ledger/.worktrees/fix/internal")
    let project = ProjectInfo(id: "p1", name: "ledger", path: "/w/ledger", worktrees: [
        wt("/w/ledger", "main"), wt("/w/ledger/.worktrees/fix", "fix/overdraft"), wt("/w/ledger/.worktrees/fixed", "other"),
    ])
    #expect(inspectorTarget(focused: key("local", "p3"), pane: pane, project: project)?.branch == "fix/overdraft")
    pane.cwd = "/w/ledger/.worktrees/fixedish" // a prefix of the name is not inside it
    #expect(inspectorTarget(focused: key("local", "p3"), pane: pane, project: project)?.branch == "main")
    pane.cwd = "/elsewhere"
    #expect(inspectorTarget(focused: key("local", "p3"), pane: pane, project: project) == nil)
    pane.cwd = nil
    #expect(inspectorTarget(focused: key("local", "p3"), pane: pane, project: project) == nil)
}

@Test func inspectorHasNothingWithoutAProject() {
    #expect(inspectorTarget(focused: nil, pane: nil, project: nil) == nil)
    // A machine's own shell: no project, so no branch.
    let pane = PaneInfo(id: "p9", name: "sh", branch: "main")
    #expect(inspectorTarget(focused: key("local", "p9"), pane: pane, project: nil) == nil)
    // Its project gone from the snapshot.
    let lost = PaneInfo(id: "p3", name: "claude", projectID: "p1", branch: "x")
    #expect(inspectorTarget(focused: key("local", "p3"), pane: lost, project: nil) == nil)
    // A key that is neither a pane nor a changes tab (a browser).
    #expect(inspectorTarget(focused: "browser|1", pane: nil, project: nil) == nil)
    #expect(inspectorTarget(focused: "changes|local|p1|", pane: nil, project: nil) == nil)
}

private func wt(_ path: String, _ branch: String) -> WorktreeInfo { WorktreeInfo(path: path, branch: branch) }

// MARK: what the page is sent

private func changes(_ files: [FileChange], base: String = "main") -> Changes {
    var c = try! JSONDecoder().decode(Changes.self, from: Data(#"{"project_id":"p1","branch":"fix/overdraft","base":"main","files":[]}"#.utf8))
    c.base = base
    c.files = files
    return c
}

private let target = InspectorTarget(machine: "local", project: "p1", branch: "fix/overdraft")

@Test func inspectorViewSumsAndCapsTheFiles() {
    let files = (1...9).map { FileChange(path: "f\($0).go", code: "M", added: $0, deleted: 1) }
    let v = inspectorView(target, project: "ledger", branch: nil, changes: changes(files), comments: [], error: nil, theme: .conch)
    #expect(v.branch == "fix/overdraft" && v.base == "main" && v.project == "ledger")
    #expect(v.added == 45 && v.deleted == 9)
    #expect(v.files.count == inspectorFileCap && v.moreFiles == 9 - inspectorFileCap)
    #expect(v.files.first?.path == "f1.go" && v.loading == false)
    #expect(v.empty == nil)
}

@Test func inspectorViewWhileLoadingAndOnError() {
    let loading = inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: nil, theme: .conch)
    #expect(loading.loading && loading.files.isEmpty && loading.error == nil)
    let failed = inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: "busybox is not connected", theme: .conch)
    #expect(!failed.loading && failed.error == "busybox is not connected")
    // Nothing changed is not loading: an empty list says so.
    let clean = inspectorView(target, project: "ledger", branch: nil, changes: changes([]), comments: [], error: nil, theme: .conch)
    #expect(!clean.loading && clean.files.isEmpty && clean.moreFiles == 0)
}

@Test func inspectorViewCarriesThePullRequest() {
    var b = BranchInfo(name: "fix/overdraft", baseAhead: 2)
    var pr = PRInfo(number: 12, state: "OPEN", checks: "pending", url: "https://github.com/o/r/pull/12")
    pr.title = "Hold the lock"
    pr.passed = 2
    pr.total = 3
    pr.runs = [PRCheck(name: "lint", state: "pass", url: "https://ci/1"), PRCheck(name: "test", state: "pending")]
    b.pr = pr
    let v = inspectorView(target, project: "ledger", branch: b, changes: changes([]), comments: [], error: nil, theme: .conch)
    #expect(v.ahead == 2)
    #expect(v.pr?.number == 12 && v.pr?.passed == 2 && v.pr?.total == 3 && v.pr?.title == "Hold the lock")
    #expect(v.pr?.runs.map(\.name) == ["lint", "test"])
    #expect(v.pr?.runs.first?.url == "https://ci/1")
    // No pull request: none sent, not an empty one.
    let none = inspectorView(target, project: "ledger", branch: BranchInfo(name: "x"), changes: nil, comments: [], error: nil, theme: .conch)
    #expect(none.pr == nil)
}

@Test func inspectorViewListsNotesNewestFirstAndCaps() {
    var notes: [ReviewComment] = []
    for i in 1...7 {
        var c = ReviewComment(projectID: "p1", branch: "fix/overdraft", file: "post.go", line: i, note: "note \(i)", id: "n\(i)")
        c.at = Date(timeIntervalSince1970: Double(i))
        c.by = i == 7 ? "codex" : nil
        notes.append(c)
    }
    var gh = ReviewComment(projectID: "p1", branch: "fix/overdraft", file: "cache.go", line: 3, note: "from github", id: "g1")
    gh.source = "github"
    gh.at = Date(timeIntervalSince1970: 100)
    gh.replies = [ReviewReply(by: "claude", note: "done", at: nil)]
    let v = inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: notes + [gh], error: nil, theme: .conch)
    #expect(v.notes.count == inspectorNoteCap && v.moreNotes == 8 - inspectorNoteCap)
    #expect(v.notes.first?.id == "g1" && v.notes.first?.github == true && v.notes.first?.replies == 1)
    #expect(v.notes[1].id == "n7" && v.notes[1].by == "codex" && v.notes[1].github == false)
    // A note GitHub holds is resolved there, so it offers no Resolve.
    #expect(v.notes.first?.canResolve == false && v.notes[1].canResolve == true)
}

@Test func inspectorViewTakesTheThemesColours() {
    let v = inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: nil, theme: .conch)
    #expect(v.colors.fg == ConchTheme.conch.fg.hex)
    #expect(v.colors.accent == ConchTheme.conch.line.hex)
    #expect(v.colors.bg == ConchTheme.conch.sidebar.hex)
    #expect(v.colors.light == false)
    for c in [v.colors.bg, v.colors.fg, v.colors.muted, v.colors.border, v.colors.raised, v.colors.ok, v.colors.warn, v.colors.err, v.colors.work] {
        #expect(c.count == 7 && c.hasPrefix("#"), "\(c)")
    }
}

@Test func inspectorEmptyView() {
    let v = inspectorEmpty("Focus an agent or a branch to see its changes.", theme: .conch)
    #expect(v.empty == "Focus an agent or a branch to see its changes." && v.branch.isEmpty && !v.loading)
}

// MARK: what the page sends back

private func ins(_ m: [String: Any]) -> InspectorIntent? { inspectorIntent(m.merging(["view": "inspector"]) { a, _ in a }) }

@Test func inspectorIntentsParse() {
    #expect(ins(["type": "ready"]) == .ready)
    #expect(ins(["type": "openChanges"]) == .openChanges)
    #expect(ins(["type": "openFile", "path": "internal/post.go"]) == .openFile("internal/post.go"))
    #expect(ins(["type": "resolve", "id": "n1"]) == .resolve("n1"))
    #expect(ins(["type": "openURL", "url": "https://github.com/o/r/pull/12"]) == .openURL(URL(string: "https://github.com/o/r/pull/12")!))
    #expect(ins(["type": "openURL", "url": "http://ci.local/1"]) == .openURL(URL(string: "http://ci.local/1")!))
}

@Test func inspectorIntentsRefuseWhatThePageShouldNotAsk() {
    // The page is ours, but what it draws is not: a branch name, a note, a
    // check's link. Anything the native side does on its word is checked.
    #expect(ins(["type": "openURL", "url": "javascript:alert(1)"]) == nil)
    #expect(ins(["type": "openURL", "url": "file:///etc/passwd"]) == nil)
    #expect(ins(["type": "openURL", "url": "conch://x"]) == nil)
    #expect(ins(["type": "openURL", "url": ""]) == nil)
    #expect(ins(["type": "openURL", "url": "https://"]) == nil)
    #expect(ins(["type": "openURL"]) == nil)
    #expect(ins(["type": "openFile", "path": ""]) == nil)
    #expect(ins(["type": "openFile", "path": 3]) == nil)
    #expect(ins(["type": "resolve", "id": ""]) == nil)
    #expect(ins(["type": "runCommand", "cmd": "rm -rf /"]) == nil)
    #expect(ins(["type": 1]) == nil)
    // Another view's message, or none named, is not the inspector's.
    #expect(inspectorIntent(["view": "changes", "type": "openChanges"]) == nil)
    #expect(inspectorIntent(["type": "openChanges"]) == nil)
    #expect(inspectorIntent("openChanges") == nil)
    #expect(inspectorIntent([String: Any]()) == nil)
    #expect(inspectorIntent(NSNull()) == nil)
}

@Test func inspectorHasAMenuCommand() {
    let c = paletteCommands.first { $0.id == "toggleInspector" }
    #expect(c?.keys == "⌃⌘I")
    #expect(c?.title.contains("Inspector") == true)
}

// MARK: the view in WebKit

@MainActor @Test func inspectorDrawsInWebKit() async throws {
    let page = ConchUIPage(.inspector)
    try await page.await(1)
    #expect(page.said.first?["type"] as? String == "ready")

    var b = BranchInfo(name: "fix/overdraft", baseAhead: 2)
    var pr = PRInfo(number: 12, state: "OPEN", checks: "pending", url: "https://github.com/o/r/pull/12")
    pr.runs = [PRCheck(name: "lint", state: "pass", url: "https://ci/1"), PRCheck(name: "test (macos)", state: "pending")]
    pr.passed = 1
    pr.total = 2
    b.pr = pr
    var note = ReviewComment(projectID: "p1", branch: "fix/overdraft", file: "post.go", line: 14, note: "<b>lock</b> first", id: "n1")
    note.by = "codex"
    let v = inspectorView(target, project: "ledger", branch: b,
                          changes: changes([FileChange(path: "internal/post.go", code: "M", added: 6, deleted: 0)]),
                          comments: [note], error: nil, theme: .conch)
    try await page.show(v)
    let text = try await page.text()
    for want in ["fix/overdraft", "#12", "internal/post.go", "+6", "lint", "test (macos)", "<b>lock</b> first", "codex", "1 of 2"] {
        #expect(text.contains(want), "missing \(want)")
    }
    #expect(try await page.run("document.querySelectorAll('#root b').length") as? Int == 0, "a note's markup is text")
    let bg = try await page.run("getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()") as? String
    #expect(bg?.uppercased() == ConchTheme.conch.sidebar.hex)

    try await page.click("Open changes")
    try await page.click("internal/post.go")
    try await page.click("Resolve")
    try await page.click("lint")
    try await page.await(5)
    #expect(page.said[1...4].map { inspectorIntent($0) } == [.openChanges, .openFile("internal/post.go"), .resolve("n1"), .openURL(URL(string: "https://ci/1")!)])
}

@MainActor @Test func inspectorDrawsEveryState() async throws {
    let page = ConchUIPage(.inspector)
    try await page.await(1)
    try await page.show(inspectorEmpty("Focus an agent or a branch to see where it stands.", theme: .conch))
    #expect(try await page.text().contains("Focus an agent or a branch"))
    try await page.show(inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: nil, theme: .conch))
    #expect(try await page.text().contains("Reading the branch"))
    try await page.show(inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: "busybox is not connected", theme: .conch))
    #expect(try await page.text().contains("busybox is not connected"))
    try await page.show(inspectorView(target, project: "ledger", branch: nil, changes: changes([]), comments: [], error: nil, theme: .conch))
    #expect(try await page.text().contains("Nothing uncommitted"))
    let many = (1...9).map { FileChange(path: "f\($0)", code: "A", added: 1, deleted: 0) }
    try await page.show(inspectorView(target, project: "ledger", branch: nil, changes: changes(many), comments: [], error: nil, theme: .conch))
    #expect(try await page.text().contains("and \(9 - inspectorFileCap) more"))
}

// The app's side has no test target, so it is read, as BrowserView is.
@Test func inspectorActsOnlyOnWhatItListed() throws {
    let path = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp/InspectorPanel.swift"
    let code = try String(contentsOfFile: path, encoding: .utf8)
    #expect(code.contains("listed.contains(u.absoluteString)"), "only a link the column listed opens")
    #expect(code.contains("model.changes?.files.contains(where: { $0.path == path })"), "only a file the branch changes opens")
    #expect(code.contains("if let i = inspectorIntent(body)"), "messages are checked before use")
}
