import Foundation
import Testing
@testable import ConchKit

@Test func searchLineMatchesAreCharacters() {
    // fs.search's ranges are UTF-8 bytes; "é" is two of them.
    let l = FSSearchLine(line: 3, text: "café pane // pane", ranges: [[6, 10], [14, 18]])
    #expect(l.matches.map { String(l.text[$0]) } == ["pane", "pane"])
    #expect(l.runs.map(\.text) == ["café ", "pane", " // ", "pane"])
    #expect(l.runs.map(\.match) == [false, true, false, true])
    #expect(replacedLine(l, with: "view") == "café view // view")
    // A range inside a character, past the end, backwards or malformed is dropped.
    let bad = FSSearchLine(line: 1, text: "é", ranges: [[1, 2], [0, 9], [1, 0], [0]])
    #expect(bad.matches.isEmpty)
    #expect(bad.runs.map(\.text) == ["é"])
    #expect(FSSearchLine(line: 1, text: "", ranges: []).runs.map(\.text) == [""])
}

@Test func searchResultDecodes() throws {
    let json = """
    {"root":"/r","names":["a.go"],"files":[{"path":"a.go","hash":"h","lines":[{"line":2,"offset":10,"text":"x pane","ranges":[[2,6]]}]}],
     "searched":4,"truncated":true,"size":99,"next":50,"next_line":7,"done":false}
    """
    let r = try JSONDecoder().decode(FSSearchResult.self, from: Data(json.utf8))
    #expect(r.names == ["a.go"] && r.files?.first?.lines.first?.offset == 10 && r.nextLine == 7 && r.next == 50)
    #expect(searchSummary(r, scope: .everything) == "1 file name · 1 line in 1 file · 4 searched · more not shown")
    #expect(searchSummary(r, scope: .names) == "1 file name · 4 searched · more not shown")
    #expect(searchSummary(FSSearchResult(), scope: .text) == "0 lines in 0 files")
}

@Test func searchQueryParams() throws {
    var q = SearchQuery()
    q.query = "FSRead"
    q.matchCase = true
    q.include = "internal/, cmd/  *.go"
    q.scope = .text
    #expect(q.includes == ["internal/", "cmd/", "*.go"] && q.excludes.isEmpty)
    #expect(!q.names && q.text)
    let b = try JSONEncoder().encode(q.pattern)
    let back = try JSONSerialization.jsonObject(with: b) as? [String: Any]
    #expect(back?["query"] as? String == "FSRead" && back?["case"] as? Bool == true && back?["regex"] == nil)
    q.scope = .everything
    #expect(q.names && q.text)
}

@Test func replacePlanLeavesOutWhatWasStruck() {
    let files = [
        FSSearchFile(path: "a.go", hash: "ha", lines: [
            FSSearchLine(line: 1, text: "x x", ranges: [[0, 1], [2, 3]]), FSSearchLine(line: 4, text: "x", ranges: [[0, 1]]),
        ]),
        FSSearchFile(path: "b.go", hash: "hb", lines: [FSSearchLine(line: 2, text: "x", ranges: [[0, 1]])]),
        FSSearchFile(path: "big.log", hash: nil, lines: [FSSearchLine(line: 9, text: "x", ranges: [[0, 1]])]),
    ]
    let all = ReplacePlan(files)
    #expect(all.files.map(\.path) == ["a.go", "b.go"], "a piece of a big file has no fingerprint to replace against")
    #expect(all.matches == 4 && all.summary == "4 matches in 2 files")
    let some = ReplacePlan(files, skipFiles: ["b.go"], skipLines: [lineKey("a.go", 4)])
    #expect(some.files == [ReplacePlan.File(path: "a.go", hash: "ha", lines: [1])] && some.matches == 2)
    #expect(ReplacePlan(files, skipFiles: ["a.go", "b.go"]).summary == "Nothing to replace")
    #expect(ReplacePlan([FSSearchFile(path: "c", hash: "h", lines: [FSSearchLine(line: 1, text: "x", ranges: [[0, 1]])])]).summary == "1 match in 1 file")
}

@Test func replaceResultSummary() throws {
    let json = #"{"files":[{"path":"a.go","count":3,"hash":"h"},{"path":"b.go","count":1,"hash":"h"}],"skipped":[{"path":"x/c.go","reason":"changed on disk since it was searched"}]}"#
    let r = try JSONDecoder().decode(FSReplaceResult.self, from: Data(json.utf8))
    #expect(r.summary == "Replaced 4 in 2 files · 1 left: c.go changed on disk since it was searched")
    #expect(FSReplaceResult().summary == "Replaced 0 in 0 files")
}

@Test func fuzzyMarksPickLettersInOrder() {
    #expect(fuzzyMarks("fsreplace.go", "fsrep") == [true, true, true, true, true] + Array(repeating: false, count: 7))
    #expect(fuzzyMarks("Files.swift", "fsw") == [true, false, false, false, true, false, false, true, false, false, false])
    #expect(fuzzyMarks("abc", "") == [false, false, false])
    #expect(fuzzyMarks("abc", "x").allSatisfy { !$0 })
}

@Test func newEntriesGoWhereTheRowIs() {
    let file = FileRow(path: "internal/tui/files.go", name: "files.go", depth: 2, dir: false, open: false, status: "", ignored: false)
    let dir = FileRow(path: "internal/tui", name: "tui", depth: 1, dir: true, open: true, status: "", ignored: false)
    let top = FileRow(path: "go.mod", name: "go.mod", depth: 0, dir: false, open: false, status: "", ignored: false)
    #expect(newEntryPath(in: file, name: "new.go") == "internal/tui/new.go")
    #expect(newEntryPath(in: dir, name: " sub/x.go ") == "internal/tui/sub/x.go")
    #expect(newEntryPath(in: top, name: "a.txt") == "a.txt")
    #expect(newEntryPath(in: nil, name: "/docs/") == "docs")
}

@Test func duplicateAndRenameNames() {
    var taken: Set<String> = ["a/b.go", "a/b copy.go"]
    #expect(duplicateName("a/b.go") { taken.contains($0) } == "a/b copy 2.go")
    taken = []
    #expect(duplicateName("Makefile") { taken.contains($0) } == "Makefile copy")
    #expect(duplicateName(".env") { taken.contains($0) } == ".env copy")
    #expect(duplicateName("x/archive.tar.gz") { taken.contains($0) } == "x/archive.tar copy.gz")
    #expect(renamedPath("a/b.go", to: "c.go") == "a/c.go")
    #expect(renamedPath("b.go", to: " c.go ") == "c.go")
    #expect(renamedPath("a/b.go", to: "/d/e.go") == "d/e.go", "a slash names the path from the top")
}

@Test func movedPaths() {
    #expect(movedPath("a/b/c.go", from: "a/b", to: "x") == .some("x/c.go"))
    #expect(movedPath("a/b", from: "a/b", to: nil) == .some(nil))
    #expect(movedPath("a/bc", from: "a/b", to: "x") == .none, "a name that only starts the same is another file")
}

@Test func fileTiers() {
    #expect(FileTier(size: 0) == .whole)
    #expect(FileTier(size: 4 << 20) == .whole)
    #expect(FileTier(size: (4 << 20) + 1) == .large)
    #expect(FileTier(size: 2 << 30) == .large)
    #expect(FileTier(size: (2 << 30) + 1) == .huge)
}

@Test func linesDecodeAndSplit() throws {
    let json = #"{"path":"/x","size":20,"line":5,"lines":900,"offset":12,"length":8,"data":"a\r\nb\n","hash":"h"}"#
    let l = try JSONDecoder().decode(FSLines.self, from: Data(json.utf8))
    #expect(l.texts == ["a", "b"] && l.editable && l.line == 5 && l.lines == 900)
    #expect(FSLines(line: 1, lines: 2, data: "a\nlast").texts == ["a", "last"])
    #expect(FSLines(line: 1, lines: 0, data: "").texts.isEmpty)
    var lossy = FSLines(line: 1, lines: 1, data: "x", hash: "h")
    lossy.lossy = true
    #expect(!lossy.editable && !FSLines(line: 1, lines: 1, data: "x").editable)
}

@Test func lineWindowHoldsPiecesNearTheScreen() {
    var w = LineWindow(chunk: 100)
    #expect(w.wanted(top: 1, rows: 10) == [1], "nothing known yet: the first piece")
    w.total = 1000
    #expect(w.start(of: 1) == 1 && w.start(of: 100) == 1 && w.start(of: 101) == 101 && w.start(of: 0) == 1)
    #expect(w.wanted(top: 95, rows: 20) == [1, 101])
    #expect(w.wanted(top: 990, rows: 40) == [901], "not past the last line")
    w.put(1, (1...100).map { "l\($0)" }, keepNear: 1)
    #expect(w.line(1) == "l1" && w.line(100) == "l100" && w.line(101) == nil)
    w.put(901, ["l901"], keepNear: 901)
    #expect(w.pieces[1] == nil, "far from the screen: let go")
    #expect(w.line(901) == "l901" && w.line(902) == nil, "a short last piece")
    #expect(topFor(line: 500, rows: 30, total: 1000) == 490)
    #expect(topFor(line: 3, rows: 30, total: 1000) == 1)
    #expect(topFor(line: 1000, rows: 30, total: 1000) == 971)
    #expect(topFor(line: 5, rows: 30, total: 10) == 1)
}

@Test func openFilesTabs() {
    var o = OpenFiles()
    o.open("a")
    o.open("b")
    o.active = "a"
    o.open("c")
    #expect(o.paths == ["a", "c", "b"] && o.active == "c", "opened beside the one shown")
    o.open("b")
    #expect(o.paths == ["a", "c", "b"] && o.active == "b", "open already: shown, not added")
    o.close("b")
    #expect(o.paths == ["a", "c"] && o.active == "c")
    o.close("a")
    #expect(o.active == "c")
    o.close("c")
    #expect(o.paths.isEmpty && o.active == nil)
    o.close("zzz")

    o.open("dir/x.go")
    o.open("dir/y.go")
    o.open("other.go")
    o.active = "dir/y.go"
    o.moved(from: "dir", to: "pkg")
    #expect(o.paths == ["pkg/x.go", "pkg/y.go", "other.go"] && o.active == "pkg/y.go")
    o.moved(from: "pkg", to: nil)
    #expect(o.paths == ["other.go"] && o.active == "other.go")
}

@Test func selectionToLines() {
    let t = "one\ntwo\nthree\nfour\n"
    #expect(selectedLines(t, utf16: NSRange(location: 0, length: 0)) == 1...1)
    #expect(selectedLines(t, utf16: NSRange(location: 5, length: 0)) == 2...2)
    #expect(selectedLines(t, utf16: NSRange(location: 4, length: 9)) == 2...3)
    #expect(selectedLines(t, utf16: NSRange(location: 4, length: 10)) == 2...3, "ending at a line's start leaves that line out")
    #expect(selectedLines(t, utf16: NSRange(location: 99, length: 5)) == 5...5)
    #expect(selectedLines("", utf16: NSRange(location: 0, length: 0)) == 1...1)
}

@Test func agentPromptSaysWhereAndWhat() {
    let p = agentEditPrompt(path: "internal/tui/files.go", lines: 142...151, ask: "  keep the scroll  \n", also: ["files_test.go"])
    #expect(p.hasPrefix("In internal/tui/files.go, lines 142–151: keep the scroll\n"))
    #expect(p.contains("You may also need: files_test.go."))
    #expect(p.contains("keeps or undoes it"))
    #expect(agentEditPrompt(path: "a.go", lines: 7...7, ask: "x").hasPrefix("In a.go, line 7: x"))
    #expect(agentEditPrompt(path: "a.go", lines: nil, ask: "x").hasPrefix("In a.go: x"))
    #expect(agentQuickAsks.count == 5)
}

@Test func lineDiffs() {
    let d = lineDiff("a\nb\nc\nd\n", "a\nB\nc\nd\ne\n")
    #expect(d.map(\.kind) == [.same, .removed, .added, .same, .same, .added])
    #expect(d[2] == EditDiffLine(kind: .added, text: "B", old: nil, new: 2))
    #expect(d[3] == EditDiffLine(kind: .same, text: "c", old: 3, new: 3))
    #expect(diffStat(d) == "+2 −1")
    #expect(lineDiff("same\n", "same\n").allSatisfy { $0.kind == .same })
    #expect(lineDiff("", "x\n") == [EditDiffLine(kind: .added, text: "x", old: nil, new: 1)])
    #expect(lineDiff("x\n", "") == [EditDiffLine(kind: .removed, text: "x", old: 1, new: nil)])
    // Too big to compare line by line: taken out and put back.
    let big = lineDiff("p\na\nb\ns\n", "p\nb\na\ns\n", cell: 1)
    #expect(big.map(\.kind) == [.same, .removed, .removed, .added, .added, .same])
    #expect(big.last == EditDiffLine(kind: .same, text: "s", old: 4, new: 4))
}

@Test func diffHunksKeepContext() {
    let old = (1...20).map { "l\($0)" }.joined(separator: "\n") + "\n"
    let new = old.replacingOccurrences(of: "l10\n", with: "ten\n")
    let h = diffHunks(lineDiff(old, new), context: 2)
    #expect(h.first! == nil && h.last! == nil, "gaps before and after")
    #expect(h.compactMap { $0?.text } == ["l8", "l9", "l10", "ten", "l11", "l12"])
    #expect(diffHunks(lineDiff("a\n", "a\n")).isEmpty)
    let top = diffHunks(lineDiff("a\nb\n", "A\nb\n"), context: 3)
    #expect(top.first! != nil && top.count == 3, "a change at the top has no gap before it")
}

@Test func lineRanges() {
    let ns = "one\r\ntwo\n\nfour" as NSString
    #expect(lineRange(ns, line: 1) == NSRange(location: 0, length: 3), "without its CR LF")
    #expect(lineRange(ns, line: 2) == NSRange(location: 5, length: 3))
    #expect(lineRange(ns, line: 3) == NSRange(location: 9, length: 0), "an empty line")
    #expect(lineRange(ns, line: 4) == NSRange(location: 10, length: 4), "the last, with no newline")
    #expect(lineRange(ns, line: 9) == NSRange(location: 14, length: 0), "past the end: the end")
    #expect(lineRange(ns, line: 0) == NSRange(location: 0, length: 3))
    #expect(lineRange("" as NSString, line: 1) == NSRange(location: 0, length: 0))
}
