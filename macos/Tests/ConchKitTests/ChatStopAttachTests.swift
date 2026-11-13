import Foundation
import Testing
@testable import ConchKit

private let t0 = Date(timeIntervalSince1970: 1_800_000_000)
private func at(_ s: TimeInterval) -> Date { t0.addingTimeInterval(s) }

// MARK: - Stop

// The timeline: Stopping… at once, escape again at 2 s, Stop harder at
// 3 s, ctrl+c on the press after that.
@Test func stopTimeline() {
    var s = ChatStop()
    #expect(s.label == "Stop" && s.pressable && !s.active && s.hint(agent: "Claude") == nil)
    #expect(s.press(at: t0) == .interrupt)
    #expect(s.phase == .stopping && s.label == "Stopping…" && !s.pressable && s.active)
    #expect(s.hint(agent: "Claude") == "Stopping Claude — escape sent, waiting for it to say so")
    #expect(s.press(at: at(0.5)) == .none, "a second press while stopping sends nothing more")
    #expect(s.check(at: at(1.9), stopped: false) == .none && s.phase == .stopping)
    #expect(s.check(at: at(2), stopped: false) == .interrupt && s.phase == .again)
    #expect(s.check(at: at(2.2), stopped: false) == .none, "sent again once")
    #expect(s.check(at: at(2.9), stopped: false) == .none && s.phase == .again)
    #expect(s.check(at: at(3), stopped: false) == .none && s.phase == .harder)
    #expect(s.label == "Stop harder" && s.pressable)
    #expect(s.hint(agent: "Claude")!.contains("ctrl+c"))
    #expect(s.press(at: at(4)) == .hard && s.phase == .hardSent && s.label == "Stopping…")
    #expect(s.check(at: at(7.9), stopped: false) == .none && s.phase == .hardSent)
    // Still working after ctrl+c: said, rather than Stopping… for ever.
    #expect(s.check(at: at(8), stopped: false) == .none && s.phase == .stuck)
    #expect(s.label == "Stop" && s.pressable && !s.active && s.hint(agent: "Claude")!.contains("still working"))
    #expect(s.press(at: at(9)) == .interrupt && s.phase == .stopping, "and it can be pressed again")
}

// Any look that says it stopped ends it, at whatever step.
@Test func stopEndsWhenStopped() {
    for steps in [0.0, 2.0, 3.0] {
        var s = ChatStop()
        _ = s.press(at: t0)
        var t = 0.0
        while t <= steps { _ = s.check(at: at(t), stopped: false); t += 0.2 }
        #expect(s.check(at: at(t), stopped: true) == .none)
        #expect(s.phase == .idle && s.since == nil && s.label == "Stop", "\(steps)")
    }
    var s = ChatStop()
    #expect(s.check(at: t0, stopped: false) == .none && s.phase == .idle, "nothing pressed: nothing done")
    _ = s.press(at: t0)
    s.reset()
    #expect(s.phase == .idle)
    // A clock that went backwards does not send anything.
    _ = s.press(at: at(10))
    #expect(s.check(at: t0, stopped: false) == .none && s.phase == .stopping)
}

@Test func stopKeysSaid() {
    #expect(stopKeysText(["escape"]) == "escape" && stopKeysText(["esc"]) == "escape")
    #expect(stopKeysText(["escape", "escape"]) == "escape twice" && stopKeysText(["escape", "escape", "escape"]) == "escape ×3")
    #expect(stopKeysText([]) == "escape" && stopKeysText(["ctrl+c"]) == "ctrl+c" && stopKeysText(["escape", "ctrl+c"]) == "escape, ctrl+c")
    var s = ChatStop()
    s.sent = stopKeysText(["escape", "escape"])
    _ = s.press(at: t0)
    #expect(s.hint(agent: "Devin") == "Stopping Devin — escape twice sent, waiting for it to say so")
}

// The transcript's "Interrupted" after the press is a stop; one from
// before it is not.
@Test func chatInterruptedNote() {
    var log = ChatLog()
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 1, role: "user", text: "go"), ChatTurn(seq: 2, role: "note", text: "Interrupted")], epoch: "e", ver: 1))
    let mark = log.mark
    #expect(!log.interrupted(after: mark))
    #expect(log.interrupted(after: ChatMark(seq: 1, count: 1)))
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 3, role: "note", text: "Interrupted")], epoch: "e", ver: 2))
    #expect(log.interrupted(after: mark))
    // A server without seqs counts turns.
    var old = ChatLog()
    old.apply(AgentChat(agent: "claude", turns: [ChatTurn(role: "user", text: "go")]))
    let m = old.mark
    old.apply(AgentChat(agent: "claude", turns: [ChatTurn(role: "user", text: "go"), ChatTurn(role: "assistant", text: "ok")]))
    #expect(!old.interrupted(after: m))
    old.apply(AgentChat(agent: "claude", turns: [ChatTurn(role: "user", text: "go"), ChatTurn(role: "note", text: "Interrupted")]))
    #expect(old.interrupted(after: m))
}

// MARK: - attachments

private func att(_ name: String, _ size: Int64, _ src: ChatAttachment.Source = .file("/x")) -> ChatAttachment {
    ChatAttachment(name: name, size: size, source: src)
}

@Test func attachmentLimits() {
    let big = att("movie.mov", attachMaxSize + 1)
    let exact = att("a.bin", attachMaxSize)
    let inCheckout = att("huge.sql", attachMaxSize * 3, .checkout("db/huge.sql"))
    #expect(attachmentProblem([big], big) == "over 20 MB · not sent")
    #expect(attachmentProblem([exact], exact) == nil, "exactly the limit goes")
    #expect(attachmentProblem([inCheckout], inCheckout) == nil, "a file in the checkout is named, not copied: no limit")
    // The first ten go; an oversized one does not take a place.
    var list = [big] + (1...11).map { att("f\($0).txt", 10) }
    #expect(attachmentsToSend(list).count == 10)
    #expect(attachmentProblem(list, list[10]) == nil && attachmentProblem(list, list[11]) == "over 10 files · not sent")
    list.removeFirst()
    #expect(attachmentsToSend(list).map(\.name) == (1...10).map { "f\($0).txt" })
    #expect(attachmentsToSend([]).isEmpty)
    #expect(att("a.png", 1, .data(Data())).origin == "pasted" && att("a", 1).origin == "from Finder" && inCheckout.origin == "in this checkout")
    #expect(!inCheckout.copied && big.copied)
}

@Test func attachmentNames() {
    #expect(attachmentFileName("Screenshot 2026-11-13 at 9.41.png") == "Screenshot-2026-11-13-at-9.41.png")
    #expect(attachmentFileName("login spec (v2).pdf") == "login-spec-v2-.pdf")
    #expect(attachmentFileName(".env") == "env" && attachmentFileName("..") == "attachment" && attachmentFileName("") == "attachment")
    #expect(attachmentFileName("a\"b'c;d.txt") == "a-b-c-d.txt")
    #expect(attachmentFileName("日本語.txt") == "日本語.txt")
    let long = attachmentFileName(String(repeating: "x", count: 300) + ".png")
    #expect(long.count == 100 && long.hasSuffix(".png"))
    #expect(uniqueNames(["a.png", "a.png", "A.png", "b", "b"]) == ["a.png", "a-2.png", "A-3.png", "b", "b-2"])
    #expect(uniqueNames([]) == [])
    #expect(isImageName("x.PNG") && isImageName("a.jpeg") && !isImageName("a.pdf") && !isImageName("png"))
    #expect(pastedImageName(at: t0).hasPrefix("Pasted 20") && pastedImageName(at: t0).hasSuffix(".png"))
    #expect(pastedImageName(at: t0, n: 2).hasSuffix(" 2.png"))
}

@Test func attachmentPaths() {
    let proj = ProjectInfo(id: "r1", name: "api", path: "/w/api", worktrees: [WorktreeInfo(path: "/w/api", branch: "main"), WorktreeInfo(path: "/w/api-feat", branch: "feat")])
    #expect(chatCheckout(cwd: "/w/api-feat", project: proj) == "/w/api-feat")
    #expect(chatCheckout(cwd: "/w/api-feat/pkg", project: proj) == "/w/api-feat")
    #expect(chatCheckout(cwd: "/w/api", project: proj) == "/w/api")
    #expect(chatCheckout(cwd: "/w/apix", project: proj) == nil, "a name that starts the same is not inside")
    #expect(chatCheckout(cwd: nil, project: proj) == nil && chatCheckout(cwd: "/w/api", project: nil) == nil)
    #expect(pathFor(agentIn: "/w/api", "/w/api/src/a.go") == "src/a.go")
    #expect(pathFor(agentIn: "/w/api/", "/w/api/src/a.go") == "src/a.go")
    #expect(pathFor(agentIn: "/w/api", "/w/api") == ".")
    #expect(pathFor(agentIn: "/w/api/pkg", "/w/api/src/a.go") == "/w/api/src/a.go", "outside the agent's folder: absolute")
    #expect(pathFor(agentIn: "/", "/etc/hosts") == "etc/hosts")
    #expect(checkoutPath(root: "/w/api", cwd: "/w/api", "src/a.go") == "src/a.go")
    #expect(checkoutPath(root: "/w/api", cwd: "/w/api/src", "src/a.go") == "a.go")
    #expect(checkoutPath(root: "/w/api", cwd: nil, "src/a.go") == "src/a.go")
    let f = AttachFolder(dir: "/w/api/.conch/attachments/7f3a", root: "/w/api", cwd: "/w/api")
    #expect(attachedPath(folder: f, name: "a.png") == ".conch/attachments/7f3a/a.png")
}

// What each agent is sent, and that a sent message comes apart again into
// its words and its chips.
@Test func attachmentPromptPerAgent() {
    let refs = [AttachedRef(path: "src/a.go", image: false), AttachedRef(path: ".conch/attachments/7f3a/shot.png", image: true)]
    #expect(attachmentPrompt("  fix it \n", refs, agent: "claude") == "fix it\n\nAttached: @src/a.go\nAttached image: @.conch/attachments/7f3a/shot.png")
    #expect(attachmentPrompt("fix it", refs, agent: "gemini").contains("Attached: @src/a.go"))
    #expect(attachmentPrompt("fix it", refs, agent: "codex") == "fix it\n\nAttached: src/a.go\nAttached image: .conch/attachments/7f3a/shot.png")
    #expect(attachmentPrompt("fix it", refs, agent: "devin").hasSuffix("shot.png " + attachImageNote))
    #expect(attachmentPrompt("fix it", [], agent: "claude") == "fix it")
    #expect(attachmentPrompt("", refs, agent: "opencode") == "Attached: src/a.go\nAttached image: .conch/attachments/7f3a/shot.png")
    // A path with a space is quoted, and loses the @ an agent would stop at.
    #expect(attachmentLine(AttachedRef(path: "docs/my spec.pdf", image: false), agent: "claude") == "Attached: \"docs/my spec.pdf\"")
    #expect(agentReadsImages("claude") && agentReadsImages("gemini") && !agentReadsImages("codex") && !agentReadsImages("devin"))

    for agent in ["claude", "gemini", "codex", "opencode", "devin", "pi"] {
        let sent = attachmentPrompt("fix it\nplease", refs + [AttachedRef(path: "a b.txt", image: false)], agent: agent)
        let (text, back) = splitAttached(sent)
        #expect(text == "fix it\nplease", "\(agent)")
        #expect(back == refs + [AttachedRef(path: "a b.txt", image: false)], "\(agent)")
    }
    #expect(splitAttached("just words").refs.isEmpty && splitAttached("just words").text == "just words")
    #expect(splitAttached("").text == "" && splitAttached("Attached: ").refs.isEmpty)
    // Only lines at the end are attachments.
    let mid = splitAttached("Attached: x\nthen words")
    #expect(mid.refs.isEmpty && mid.text == "Attached: x\nthen words")
}

// A message with attachments reaches the conversation when its words do,
// whatever the agent made of the lines naming the files.
@Test func pendingWithAttachmentsMatches() {
    var log = ChatLog()
    log.apply(AgentChat(agent: "claude", session: "s", turns: [], epoch: "e", ver: 1))
    let mark = log.mark
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 1, role: "user", text: "fix it\n\n[Image #1]")], epoch: "e", ver: 2))
    #expect(!log.has(sent: "fix it\n\nAttached image: @a.png", after: mark), "the agent's own rewrite is not ours")
    log.apply(AgentChat(agent: "claude", session: "s", turns: [ChatTurn(seq: 2, role: "user", text: "look\n\nAttached image: @.conch/attachments/1/a.png")], epoch: "e", ver: 3))
    #expect(log.has(sent: "look\n\nAttached image: @.conch/attachments/1/a.png", after: mark))
    #expect(log.has(sent: "look", after: mark))
    #expect(!log.has(sent: "Attached: x", after: mark), "nothing written matches nothing written only where it was said")
}

@Test func atPicker() {
    func q(_ s: String, _ c: Int? = nil) -> String? { atQuery(s, cursor: c ?? (s as NSString).length)?.query }
    #expect(q("Start from @login") == "login")
    #expect(q("@") == "" && q("@src/a") == "src/a")
    #expect(q("mail me@home") == nil, "an @ inside a word is not the picker's")
    #expect(q("@login and") == nil, "a space since the @ ends it")
    #expect(q("") == nil && q("x", 0) == nil && q("x", 5) == nil)
    #expect(q("a\n@b") == "b")
    #expect(q("Start from @login", 13) == "l", "up to the cursor")
    let r = atQuery("Start from @login", cursor: 17)!.range
    #expect(r == NSRange(location: 11, length: 6))
    #expect(removingAt("Start from @login", r) == "Start from ")
    #expect(removingAt("fix @x please", NSRange(location: 4, length: 2)) == "fix please")
    #expect(removingAt("x", NSRange(location: 4, length: 2)) == "x", "a range past the end changes nothing")
    #expect(q("é @ü") == "ü")
}

@Test func dropOverlay() {
    #expect(dropOverlayText(count: 3, images: 1) == "Drop to attach 3 files · images go in as pictures")
    #expect(dropOverlayText(count: 1, images: 0) == "Drop to attach 1 file")
    #expect(dropOverlayText(count: 0, images: 0) == "Drop to attach files")
}

@Test func attachFolderDecodes() throws {
    let f = try JSONDecoder().decode(AttachFolder.self, from: Data(#"{"dir":"/w/.conch/attachments/ab","root":"/w","cwd":"/w/pkg","max_size":20971520,"max_files":10}"#.utf8))
    #expect(f == AttachFolder(dir: "/w/.conch/attachments/ab", root: "/w", cwd: "/w/pkg", maxSize: 20 << 20, maxFiles: 10))
    let bare = try JSONDecoder().decode(AttachFolder.self, from: Data(#"{"dir":"/d","root":"/r","cwd":"/c"}"#.utf8))
    #expect(bare.maxSize == nil)
}
