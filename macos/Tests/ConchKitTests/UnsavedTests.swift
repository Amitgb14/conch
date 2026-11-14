import Foundation
import Testing
@testable import ConchKit

private func editable(_ text: String, hash: String = "h1") -> FSRead {
    FSRead(path: "a.go", size: Int64(text.utf8.count), data: text, truncated: nil, binary: nil, mime: nil, hash: hash)
}

// A save is answered for the file it was for: ⌘S on a.go, then b.go
// picked, must not give b.go a.go's text and fingerprint.
@Test func savedAnswerGoesToItsFile() {
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r", selected: "a.go", aside: false) == .shown)
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r", selected: "b.go", aside: true) == .aside)
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r", selected: "b.go", aside: false) == .gone, "its tab was closed")
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r", selected: nil, aside: false) == .gone)
    // Another checkout shown since: the same path there is another file.
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r-feat", selected: "a.go", aside: false) == .gone)
    #expect(savedFileAt(path: "a.go", root: "/r", nowRoot: "/r-feat", selected: "b.go", aside: true) == .gone)
}

@Test func fileBufferSaved() {
    var b = FileBuffer(preview: editable("one"), text: "two", original: "one", baseHash: "h1", diskChanged: true)
    #expect(b.dirty)
    b.saved("two", hash: "h2")
    #expect(!b.dirty && b.baseHash == "h2" && !b.diskChanged && b.original == "two")
    // Typed into after the save was asked for: still not saved.
    var c = FileBuffer(preview: editable("one"), text: "three", original: "one", baseHash: "h1")
    c.saved("two", hash: "h2")
    #expect(c.dirty && c.text == "three" && c.original == "two")
    // Not editable (read in part, binary): never dirty, so never asked about.
    var big = FileBuffer(preview: FSRead(data: "x", truncated: true, hash: "h"), text: "y", original: "x")
    #expect(!big.dirty)
    big.preview = nil
    #expect(!big.dirty)
}

// A task's brief keeps its edits through a read again (the page shown
// again re-reads it), and says when the file moved under them.
@Test func briefDraftKeepsEdits() {
    var d = BriefDraft()
    #expect(!d.dirty)
    var moved = d.read("# Task\n", hash: "h1", truncated: false)
    #expect(!moved)
    #expect(d.loaded && d.text == "# Task\n" && !d.dirty)
    d.text += "more\n"
    #expect(d.dirty)
    // Read again with the file as it was: the edits stay.
    moved = d.read("# Task\n", hash: "h1", truncated: false)
    #expect(!moved)
    #expect(d.text == "# Task\nmore\n" && d.hash == "h1")
    // Somebody wrote it meanwhile: still kept, still against h1, said changed.
    moved = d.read("# Theirs\n", hash: "h9", truncated: false)
    #expect(moved)
    #expect(d.text == "# Task\nmore\n" && d.hash == "h1" && d.dirty)
    d.saved(d.text, hash: "h2")
    #expect(!d.dirty && d.hash == "h2")
    // With no edits, a read takes what is on disk.
    d.read("# New\n", hash: "h3", truncated: false)
    #expect(d.text == "# New\n" && d.hash == "h3")
    // Read only in part: not to be saved, so not dirty however edited.
    var part = BriefDraft()
    part.read("# Tas", hash: nil, truncated: true)
    part.text = "changed"
    #expect(!part.loaded && !part.dirty)
    var empty = BriefDraft()
    moved = empty.read("", hash: "h", truncated: false)
    #expect(!moved && empty.loaded && !empty.dirty)
}

@MainActor
@Test func saveAllStopsAtTheFirstFailure() async {
    var ran: [String] = []
    func item(_ n: String, _ ok: Bool) -> Unsaved { Unsaved(name: n) { ran.append(n); return ok } }
    #expect(await saveAll([]))
    #expect(await saveAll([item("a", true), item("b", true)]) && ran == ["a", "b"])
    ran = []
    #expect(!(await saveAll([item("a", true), item("b", false), item("c", true)])))
    #expect(ran == ["a", "b"], "nothing after a failed save is written, and the quit stops")
}

@Test func unsavedQuestionText() {
    let one = unsavedQuestion(["main.go"])
    #expect(one.title == "Save your changes to main.go before quitting?" && !one.info.contains("main.go"))
    let two = unsavedQuestion(["main.go", "the brief of fix-login"])
    #expect(two.title.hasPrefix("2 ") && two.info.contains("main.go\nthe brief of fix-login"))
    let many = unsavedQuestion((1...9).map { "f\($0).go" })
    #expect(many.info.contains("f6.go") && !many.info.contains("f7.go") && many.info.hasSuffix("…and 3 more"))
}
