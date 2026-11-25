import Foundation
import JavaScriptCore
import Testing
@testable import ConchKit

// The inspector is the first of the window's views drawn by a web view
// (the hybrid: native shell and terminals, rich views in HTML). What it
// shows is worked out here, what comes back from the page is checked
// here, and the page's own script is run here in JavaScriptCore — the
// engine WKWebView runs it in.

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

@Test func inspectorIntentsParse() {
    #expect(inspectorIntent(["type": "ready"]) == .ready)
    #expect(inspectorIntent(["type": "openChanges"]) == .openChanges)
    #expect(inspectorIntent(["type": "openFile", "path": "internal/post.go"]) == .openFile("internal/post.go"))
    #expect(inspectorIntent(["type": "resolve", "id": "n1"]) == .resolve("n1"))
    #expect(inspectorIntent(["type": "openURL", "url": "https://github.com/o/r/pull/12"]) == .openURL(URL(string: "https://github.com/o/r/pull/12")!))
    #expect(inspectorIntent(["type": "openURL", "url": "http://ci.local/1"]) == .openURL(URL(string: "http://ci.local/1")!))
}

@Test func inspectorIntentsRefuseWhatThePageShouldNotAsk() {
    // The page is ours, but what it draws is not: a branch name, a note, a
    // check's link. Anything the native side does on its word is checked.
    #expect(inspectorIntent(["type": "openURL", "url": "javascript:alert(1)"]) == nil)
    #expect(inspectorIntent(["type": "openURL", "url": "file:///etc/passwd"]) == nil)
    #expect(inspectorIntent(["type": "openURL", "url": "conch://x"]) == nil)
    #expect(inspectorIntent(["type": "openURL", "url": ""]) == nil)
    #expect(inspectorIntent(["type": "openURL", "url": "https://"]) == nil)
    #expect(inspectorIntent(["type": "openURL"]) == nil)
    #expect(inspectorIntent(["type": "openFile", "path": ""]) == nil)
    #expect(inspectorIntent(["type": "openFile", "path": 3]) == nil)
    #expect(inspectorIntent(["type": "resolve", "id": ""]) == nil)
    #expect(inspectorIntent(["type": "runCommand", "cmd": "rm -rf /"]) == nil)
    #expect(inspectorIntent(["type": 1]) == nil)
    #expect(inspectorIntent("openChanges") == nil)
    #expect(inspectorIntent([String: Any]()) == nil)
    #expect(inspectorIntent(NSNull()) == nil)
}

// MARK: the page

@Test func inspectorPageAllowsNoNetwork() {
    #expect(inspectorPage.contains("Content-Security-Policy"))
    #expect(inspectorPage.contains("default-src 'none'"))
    #expect(!inspectorPage.contains("http://") && !inspectorPage.contains("https://"))
}

@Test func inspectorUpdateScriptIsOneCall() throws {
    var v = inspectorEmpty("x", theme: .conch)
    v.empty = "line\u{2028}sep\u{2029}end </script>"
    let js = inspectorUpdateScript(v)
    #expect(js.hasPrefix("window.conchInspector && window.conchInspector.update("))
    #expect(js.hasSuffix(");"))
    // Separators JavaScript once refused inside a string are escaped.
    #expect(!js.contains("\u{2028}") && !js.contains("\u{2029}"))
    #expect(js.contains("\\u2028") && js.contains("\\u2029"))
}

/// The page's script run in JavaScriptCore with no DOM: renderInspector
/// is the whole of what it draws.
private func render(_ v: InspectorView) throws -> String {
    let ctx = try #require(JSContext())
    var thrown: String?
    ctx.exceptionHandler = { _, e in thrown = e?.toString() }
    ctx.evaluateScript(inspectorScript)
    let json = String(data: try JSONEncoder().encode(v), encoding: .utf8)!
    let out = ctx.evaluateScript("renderInspector(\(json))")
    #expect(thrown == nil, "\(thrown ?? "")")
    return try #require(out?.toString())
}

@Test func inspectorPageDrawsTheBranch() throws {
    var b = BranchInfo(name: "fix/overdraft", baseAhead: 2)
    var pr = PRInfo(number: 12, state: "OPEN", checks: "pending", url: "https://github.com/o/r/pull/12")
    pr.runs = [PRCheck(name: "lint", state: "pass"), PRCheck(name: "test (macos)", state: "pending")]
    pr.passed = 1
    pr.total = 2
    b.pr = pr
    var note = ReviewComment(projectID: "p1", branch: "fix/overdraft", file: "post.go", line: 14, note: "Lock after Validate", id: "n1")
    note.by = "codex"
    let v = inspectorView(target, project: "ledger", branch: b,
                          changes: changes([FileChange(path: "internal/post.go", code: "M", added: 6, deleted: 0)]),
                          comments: [note], error: nil, theme: .conch)
    let html = try render(v)
    for want in ["fix/overdraft", "#12", "internal/post.go", "+6", "lint", "test (macos)", "Lock after Validate", "codex",
                 "data-intent=\"openChanges\"", "data-intent=\"openFile\"", "data-intent=\"resolve\"", "data-intent=\"openURL\""] {
        #expect(html.contains(want), "missing \(want)")
    }
}

@Test func inspectorPageEscapesWhatItIsGiven() throws {
    var note = ReviewComment(projectID: "p1", branch: "x", file: "a\"><img src=x onerror=alert(1)>.go", line: 1,
                             note: "<script>alert(1)</script> & done", id: "n\"1")
    note.by = "<b>me</b>"
    var v = inspectorView(target, project: "<i>ledger</i>", branch: nil,
                          changes: changes([FileChange(path: "<svg onload=x>", code: "M", added: 1, deleted: 0)]),
                          comments: [note], error: nil, theme: .conch)
    v.branch = "feat/<x>&'\""
    let html = try render(v)
    #expect(!html.contains("<script>") && !html.contains("<img") && !html.contains("<svg") && !html.contains("<b>me") && !html.contains("<i>ledger"))
    #expect(html.contains("&lt;script&gt;alert(1)&lt;/script&gt; &amp; done"))
    #expect(html.contains("feat/&lt;x&gt;&amp;&#39;&quot;"))
    // Inside an attribute too: the quote cannot end it.
    #expect(html.contains("data-id=\"n&quot;1\""))
}

@Test func inspectorPageDrawsEveryState() throws {
    let empty = try render(inspectorEmpty("Focus an agent or a branch to see its changes.", theme: .conch))
    #expect(empty.contains("Focus an agent or a branch to see its changes."))
    let loading = try render(inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: nil, theme: .conch))
    #expect(loading.contains("Reading"))
    let failed = try render(inspectorView(target, project: "ledger", branch: nil, changes: nil, comments: [], error: "busybox is not connected", theme: .conch))
    #expect(failed.contains("busybox is not connected"))
    let clean = try render(inspectorView(target, project: "ledger", branch: nil, changes: changes([]), comments: [], error: nil, theme: .conch))
    #expect(clean.contains("Nothing uncommitted"))
    let many = (1...9).map { FileChange(path: "f\($0)", code: "A", added: 1, deleted: 0) }
    let capped = try render(inspectorView(target, project: "ledger", branch: nil, changes: changes(many), comments: [], error: nil, theme: .conch))
    #expect(capped.contains("and \(9 - inspectorFileCap) more"))
}

@Test func inspectorHasAMenuCommand() {
    let c = paletteCommands.first { $0.id == "toggleInspector" }
    #expect(c?.keys == "⌃⌘I")
    #expect(c?.title.contains("Inspector") == true)
}
