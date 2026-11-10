import Foundation

// What the Files tab does beyond showing a file, the parts that can be
// tested: the project search (fs.search.v1) and its replace (fs.replace.v1),
// the file operations (fs.ops.v1), files too big to open whole read by
// their lines (fs.lines.v1), the tabs of open files, and an agent asked to
// edit a file — what it is told, and the change it made, shown as a diff
// to keep or undo.

// MARK: - Search and replace

/// What a search looks for, as fs.search and fs.replace take it.
public struct FSPattern: Codable, Equatable, Sendable {
    public var query: String
    public var regex: Bool?
    public var `case`: Bool?
    public var word: Bool?

    public init(query: String, regex: Bool = false, matchCase: Bool = false, word: Bool = false) {
        self.query = query
        self.regex = regex ? true : nil
        self.case = matchCase ? true : nil
        self.word = word ? true : nil
    }
}

/// A matching line: its number, where it starts in the file, its text (or
/// the part of a long line from byte col) and the matches in it as UTF-8
/// byte ranges of that text.
public struct FSSearchLine: Decodable, Equatable, Sendable {
    public var line: Int
    public var offset: Int64?
    public var col: Int?
    public var text: String
    public var ranges: [[Int]]?

    public init(line: Int, text: String, ranges: [[Int]], offset: Int64 = 0) {
        self.line = line
        self.text = text
        self.ranges = ranges
        self.offset = offset
    }

    /// The matches as ranges of the text, for drawing: byte ranges that
    /// don't fall on characters (or past the end) are left out.
    public var matches: [Range<String.Index>] {
        let u = text.utf8
        return (ranges ?? []).compactMap { r in
            guard r.count == 2, r[0] >= 0, r[0] < r[1], r[1] <= u.count,
                  let a = u.index(u.startIndex, offsetBy: r[0], limitedBy: u.endIndex),
                  let b = u.index(u.startIndex, offsetBy: r[1], limitedBy: u.endIndex),
                  a.samePosition(in: text) != nil, b.samePosition(in: text) != nil else { return nil }
            return a..<b
        }
    }

    /// The line cut into runs of plain text and matches, for a result row.
    public var runs: [(text: String, match: Bool)] {
        var out: [(String, Bool)] = []
        var at = text.startIndex
        for m in matches where m.lowerBound >= at {
            if m.lowerBound > at { out.append((String(text[at..<m.lowerBound]), false)) }
            out.append((String(text[m]), true))
            at = m.upperBound
        }
        if at < text.endIndex || out.isEmpty { out.append((String(text[at...]), false)) }
        return out
    }
}

public struct FSSearchFile: Decodable, Equatable, Sendable {
    public var path: String
    public var hash: String?
    public var lines: [FSSearchLine]

    public init(path: String, hash: String?, lines: [FSSearchLine]) {
        self.path = path
        self.hash = hash
        self.lines = lines
    }
}

public struct FSSearchResult: Decodable, Sendable {
    public var root: String?
    public var names: [String]?
    public var files: [FSSearchFile]?
    public var searched: Int?
    public var skipped: Int?
    public var truncated: Bool?
    public var size: Int64?
    public var next: Int64?
    public var nextLine: Int?
    public var done: Bool?

    enum CodingKeys: String, CodingKey {
        case root, names, files, searched, skipped, truncated, size, next, done
        case nextLine = "next_line"
    }

    public init(names: [String]? = nil, files: [FSSearchFile]? = nil) {
        self.names = names
        self.files = files
    }
}

/// What a search box holds, as fs.search's params.
public enum SearchScope: String, CaseIterable, Sendable { case everything, names, text }

public struct SearchQuery: Equatable, Sendable {
    public var query = ""
    public var regex = false
    public var matchCase = false
    public var word = false
    public var scope = SearchScope.everything
    public var include = ""
    public var exclude = ""
    public var ignored = false

    public init() {}

    public var pattern: FSPattern { FSPattern(query: query, regex: regex, matchCase: matchCase, word: word) }
    public var names: Bool { scope != .text }
    public var text: Bool { scope != .names }
    public var includes: [String] { globList(include) }
    public var excludes: [String] { globList(exclude) }
}

/// A list of globs as a person types it: commas or spaces between them.
public func globList(_ s: String) -> [String] {
    s.split(whereSeparator: { $0 == "," || $0 == " " || $0 == "\n" }).map(String.init).filter { !$0.isEmpty }
}

/// What a replace of one search would do: how many matches, in how many
/// files, leaving out the files and lines a person struck from it.
public struct ReplacePlan: Equatable, Sendable {
    public struct File: Equatable, Sendable {
        public var path: String
        public var hash: String
        public var lines: [Int]
    }

    public var files: [File] = []
    public var matches = 0

    public init(_ results: [FSSearchFile], skipFiles: Set<String> = [], skipLines: Set<String> = []) {
        for f in results where !skipFiles.contains(f.path) {
            guard let hash = f.hash, !hash.isEmpty else { continue } // a big file's piece: not replaceable
            let lines = f.lines.filter { !skipLines.contains(lineKey(f.path, $0.line)) }
            guard !lines.isEmpty else { continue }
            files.append(File(path: f.path, hash: hash, lines: lines.map(\.line)))
            matches += lines.reduce(0) { $0 + max(1, $1.matches.count) }
        }
    }

    public var summary: String {
        matches == 0 ? "Nothing to replace" : "\(matches) match\(matches == 1 ? "" : "es") in \(files.count) file\(files.count == 1 ? "" : "s")"
    }
}

/// A line of a result named on its own, for striking it from a replace.
public func lineKey(_ path: String, _ line: Int) -> String { "\(path):\(line)" }

/// The line as it would read after a replace: each match swapped for the
/// replacement as written (a regular expression's $1 shown as is: the
/// server expands it).
public func replacedLine(_ l: FSSearchLine, with repl: String) -> String {
    l.runs.map { $0.match ? repl : $0.text }.joined()
}

/// The search's summary line: "2 file names · 9 lines in 4 files".
public func searchSummary(_ r: FSSearchResult, scope: SearchScope) -> String {
    var parts: [String] = []
    let names = r.names?.count ?? 0
    let files = r.files ?? []
    let lines = files.reduce(0) { $0 + $1.lines.count }
    if scope != .text { parts.append("\(names) file name\(names == 1 ? "" : "s")") }
    if scope != .names { parts.append("\(lines) line\(lines == 1 ? "" : "s") in \(files.count) file\(files.count == 1 ? "" : "s")") }
    if let n = r.searched, n > 0 { parts.append("\(n) searched") }
    if r.truncated == true { parts.append("more not shown") }
    return parts.joined(separator: " · ")
}

/// Which letters of a name a few typed letters picked out, in order, as
/// fs.find matches them: for drawing them brighter.
public func fuzzyMarks(_ name: String, _ query: String) -> [Bool] {
    let q = Array(query.lowercased().filter { !$0.isWhitespace })
    var j = 0
    return name.map { ch in
        guard j < q.count, Character(ch.lowercased()) == q[j] else { return false }
        j += 1
        return true
    }
}

public struct FSReplaced: Decodable, Sendable { public var path: String; public var count: Int; public var hash: String }
public struct FSSkipped: Decodable, Sendable { public var path: String; public var reason: String }
public struct FSReplaceResult: Decodable, Sendable {
    public var files: [FSReplaced]?
    public var skipped: [FSSkipped]?

    /// "Replaced 9 in 4 files · 1 left: a.go changed on disk since it was searched".
    public var summary: String {
        let done = files ?? [], left = skipped ?? []
        let n = done.reduce(0) { $0 + $1.count }
        var s = "Replaced \(n) in \(done.count) file\(done.count == 1 ? "" : "s")"
        if !left.isEmpty {
            s += " · \(left.count) left: " + left.prefix(3).map { "\(($0.path as NSString).lastPathComponent) \($0.reason)" }.joined(separator: "; ")
        }
        return s
    }
}

public struct FSPathResult: Decodable, Sendable { public var path: String }

// MARK: - File operations

/// Where a file or folder made "in" a row goes: beside a file, inside a
/// folder, at the top for none.
public func newEntryPath(in row: FileRow?, name: String) -> String {
    let name = name.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    guard let row else { return name }
    let dir = row.dir ? row.path : (row.path as NSString).deletingLastPathComponent
    return dir.isEmpty ? name : dir + "/" + name
}

/// The name a duplicate gets: "a copy.go", then "a copy 2.go", never one
/// that is taken.
public func duplicateName(_ path: String, taken: (String) -> Bool) -> String {
    let dir = (path as NSString).deletingLastPathComponent
    let file = (path as NSString).lastPathComponent
    let ext = (file as NSString).pathExtension
    let stem = ext.isEmpty || file.hasPrefix(".") && !file.dropFirst().contains(".") ? file : (file as NSString).deletingPathExtension
    let tail = stem == file ? "" : "." + ext
    for n in 1...1000 {
        let name = stem + (n == 1 ? " copy" : " copy \(n)") + tail
        let p = dir.isEmpty ? name : dir + "/" + name
        if !taken(p) { return p }
    }
    return path + " copy"
}

/// A rename's new path: the same folder, the new name — or, with a slash
/// in it, the path typed, from the top.
public func renamedPath(_ path: String, to name: String) -> String {
    let name = name.trimmingCharacters(in: .whitespaces)
    if name.contains("/") { return name.trimmingCharacters(in: CharacterSet(charactersIn: "/")) }
    let dir = (path as NSString).deletingLastPathComponent
    return dir.isEmpty ? name : dir + "/" + name
}

/// The paths a change under old leaves stale: old itself and everything in
/// it, moved under new (nil when it was deleted).
public func movedPath(_ p: String, from old: String, to new: String?) -> String?? {
    guard p == old || p.hasPrefix(old + "/") else { return .none }
    guard let new else { return .some(nil) }
    return .some(new + p.dropFirst(old.count))
}

// MARK: - Big files

/// How a file is opened, by its size: whole and edited as any other; read
/// a window of lines at a time, edited a range at a time; or so big that
/// only reading and finding are offered.
public enum FileTier: Equatable, Sendable {
    case whole, large, huge

    /// fs.write takes this much at once (FSWriteMax).
    public static let wholeMax: Int64 = 4 << 20
    /// Past this a range is not edited: a save copies the whole file.
    public static let editMax: Int64 = 2 << 30

    public init(size: Int64) {
        switch size {
        case ...FileTier.wholeMax: self = .whole
        case ...FileTier.editMax: self = .large
        default: self = .huge
        }
    }
}

/// fs.lines' answer.
public struct FSLines: Decodable, Sendable {
    public var path: String?
    public var size: Int64?
    public var line: Int
    public var lines: Int
    public var offset: Int64
    public var length: Int64
    public var data: String?
    public var hash: String?
    public var lossy: Bool?
    public var binary: Bool?

    public init(line: Int, lines: Int, offset: Int64 = 0, length: Int64 = 0, data: String?, hash: String? = nil) {
        self.line = line
        self.lines = lines
        self.offset = offset
        self.length = length
        self.data = data
        self.hash = hash
    }

    /// The lines read, without their line endings.
    public var texts: [String] {
        guard let data, !data.isEmpty else { return [] }
        var out = data.components(separatedBy: "\n")
        if data.hasSuffix("\n") { out.removeLast() }
        return out.map { $0.hasSuffix("\r") ? String($0.dropLast()) : $0 }
    }

    /// Whether these lines can be saved back as they were read.
    public var editable: Bool { binary != true && lossy != true && !(hash ?? "").isEmpty }
}

/// The lines held of a big file: pieces of `chunk` lines each, by the
/// number of their first line, kept to a few around what is on screen.
public struct LineWindow: Sendable {
    public let chunk: Int
    public private(set) var pieces: [Int: [String]] = [:]
    public var total = 0

    public init(chunk: Int = 400) { self.chunk = chunk }

    /// The first line of the piece line n is in.
    public func start(of n: Int) -> Int { (max(n, 1) - 1) / chunk * chunk + 1 }

    /// The pieces to have for rows lines from top: the ones they fall in.
    public func wanted(top: Int, rows: Int) -> [Int] {
        guard total > 0 else { return [1] }
        let last = min(total, max(top, 1) + max(rows, 1) - 1)
        return Array(stride(from: start(of: top), through: start(of: last), by: chunk))
    }

    public mutating func put(_ first: Int, _ lines: [String], keepNear top: Int) {
        pieces[first] = lines
        // A few pieces either side of the screen; the rest let go.
        let near = start(of: top)
        for k in pieces.keys where abs(k - near) > chunk * 4 { pieces[k] = nil }
    }

    public mutating func clear() { pieces = [:] }

    /// Line n's text, if its piece has been read.
    public func line(_ n: Int) -> String? {
        guard let p = pieces[start(of: n)] else { return nil }
        let i = n - start(of: n)
        return i < p.count ? p[i] : nil
    }
}

/// The top line that puts line n in view with a little above it, for go to
/// line and the next match.
public func topFor(line n: Int, rows: Int, total: Int) -> Int {
    let top = n - max(rows / 3, 0)
    return max(1, min(top, max(total - rows + 1, 1)))
}

// MARK: - Open files

/// The files open in the tab, in the order they were opened, and the one
/// shown.
public struct OpenFiles: Equatable, Sendable {
    public private(set) var paths: [String] = []
    public var active: String?

    public init() {}

    public mutating func open(_ p: String) {
        if !paths.contains(p) {
            // Beside the one shown, as an editor opens it.
            if let a = active, let i = paths.firstIndex(of: a) { paths.insert(p, at: i + 1) } else { paths.append(p) }
        }
        active = p
    }

    /// Closes p; the one shown after it is its neighbour.
    public mutating func close(_ p: String) {
        guard let i = paths.firstIndex(of: p) else { return }
        paths.remove(at: i)
        if active == p { active = paths.isEmpty ? nil : paths[min(i, paths.count - 1)] }
    }

    /// A file renamed or moved (new) or deleted (nil), with what was in it.
    public mutating func moved(from old: String, to new: String?) {
        var out: [String] = []
        for p in paths {
            switch movedPath(p, from: old, to: new) {
            case .none: out.append(p)
            case .some(.some(let n)): if !out.contains(n) { out.append(n) }
            case .some(.none): break
            }
        }
        if let a = active {
            switch movedPath(a, from: old, to: new) {
            case .none: break
            case .some(.some(let n)): active = n
            case .some(.none):
                let i = paths.firstIndex(of: a) ?? 0
                active = out.isEmpty ? nil : out[min(i, out.count - 1)]
            }
        }
        paths = out
    }
}

// MARK: - Edit with an agent

/// The lines a selection covers, from 1: the caret's line when nothing is
/// selected, and a selection ending at a line's start leaves that line out.
public func selectedLines(_ text: String, utf16 range: NSRange) -> ClosedRange<Int> {
    let ns = text as NSString
    let start = min(range.location, ns.length)
    var end = min(range.location + range.length, ns.length)
    if range.length > 0, end > start, ns.character(at: end - 1) == 10 { end -= 1 }
    func line(_ at: Int) -> Int { ns.substring(to: at).reduce(1) { $1 == "\n" ? $0 + 1 : $0 } }
    let a = line(start), b = line(max(end, start))
    return a...max(a, b)
}

/// What an agent is told when asked to edit a file from the Files tab:
/// where, what, and that its change will be looked at before it is kept.
public func agentEditPrompt(path: String, lines: ClosedRange<Int>?, ask: String, also: [String] = []) -> String {
    let ask = ask.trimmingCharacters(in: .whitespacesAndNewlines)
    var where_ = "In \(path)"
    if let lines {
        where_ += lines.count == 1 ? ", line \(lines.lowerBound)" : ", lines \(lines.lowerBound)–\(lines.upperBound)"
    }
    var s = "\(where_): \(ask)"
    if !also.isEmpty { s += "\n\nYou may also need: " + also.joined(separator: ", ") + "." }
    s += "\n\nEdit the file in place and change nothing you were not asked to; the person reviews your change in conch's Files tab and keeps or undoes it."
    return s
}

/// The quick asks offered beside the prompt.
public let agentQuickAsks: [(label: String, ask: String)] = [
    ("Explain", "Explain what this code does. Don't change the file."),
    ("Fix the bug", "Find and fix the bug here."),
    ("Add tests", "Add tests for this code, in the project's own test files."),
    ("Add comments", "Add short comments that explain why, where it isn't obvious."),
    ("Simplify", "Simplify this code without changing what it does."),
]

/// A line of a diff between two texts.
public struct EditDiffLine: Equatable, Sendable {
    public enum Kind: Equatable, Sendable { case same, removed, added }
    public var kind: Kind
    public var text: String
    /// Its number in the old text (same, removed) and the new (same, added).
    public var old: Int?
    public var new: Int?
}

/// The lines that changed between two texts, with those the same around
/// them. Lines common to both ends are matched first; the middle is
/// compared line by line where it is small enough, and shown as taken out
/// and put back where it is not.
public func lineDiff(_ a: String, _ b: String, cell: Int = 4_000_000) -> [EditDiffLine] {
    let x = splitLines(a), y = splitLines(b)
    var pre = 0
    while pre < x.count, pre < y.count, x[pre] == y[pre] { pre += 1 }
    var suf = 0
    while suf < x.count - pre, suf < y.count - pre, x[x.count - 1 - suf] == y[y.count - 1 - suf] { suf += 1 }
    var out: [EditDiffLine] = (0..<pre).map { EditDiffLine(kind: .same, text: x[$0], old: $0 + 1, new: $0 + 1) }
    let xm = Array(x[pre..<(x.count - suf)]), ym = Array(y[pre..<(y.count - suf)])
    if xm.count * ym.count <= cell {
        // Longest common subsequence, then walked.
        let n = xm.count, m = ym.count
        var t = [[Int]](repeating: [Int](repeating: 0, count: m + 1), count: n + 1)
        for i in stride(from: n - 1, through: 0, by: -1) {
            for j in stride(from: m - 1, through: 0, by: -1) {
                t[i][j] = xm[i] == ym[j] ? t[i + 1][j + 1] + 1 : max(t[i + 1][j], t[i][j + 1])
            }
        }
        var i = 0, j = 0
        while i < n || j < m {
            if i < n, j < m, xm[i] == ym[j] {
                out.append(EditDiffLine(kind: .same, text: xm[i], old: pre + i + 1, new: pre + j + 1)); i += 1; j += 1
            } else if i < n, j == m || t[i + 1][j] >= t[i][j + 1] {
                // On a tie the old line goes first, as a diff reads.
                out.append(EditDiffLine(kind: .removed, text: xm[i], old: pre + i + 1, new: nil)); i += 1
            } else {
                out.append(EditDiffLine(kind: .added, text: ym[j], old: nil, new: pre + j + 1)); j += 1
            }
        }
    } else {
        out += xm.enumerated().map { EditDiffLine(kind: .removed, text: $1, old: pre + $0 + 1, new: nil) }
        out += ym.enumerated().map { EditDiffLine(kind: .added, text: $1, old: nil, new: pre + $0 + 1) }
    }
    out += (0..<suf).map { k in
        EditDiffLine(kind: .same, text: x[x.count - suf + k], old: x.count - suf + k + 1, new: y.count - suf + k + 1)
    }
    return out
}

func splitLines(_ s: String) -> [String] {
    guard !s.isEmpty else { return [] }
    var l = s.components(separatedBy: "\n")
    if s.hasSuffix("\n") { l.removeLast() }
    return l
}

/// A diff cut to what changed and context lines around each change, a
/// gap (nil) where lines were left out.
public func diffHunks(_ d: [EditDiffLine], context: Int = 3) -> [EditDiffLine?] {
    let changed = d.indices.filter { d[$0].kind != .same }
    guard !changed.isEmpty else { return [] }
    var keep = Set<Int>()
    for i in changed { for k in max(0, i - context)...min(d.count - 1, i + context) { keep.insert(k) } }
    var out: [EditDiffLine?] = []
    var last = -1
    for i in keep.sorted() {
        if last >= 0, i > last + 1 { out.append(nil) } else if last < 0, i > 0 { out.append(nil) }
        out.append(d[i])
        last = i
    }
    if last < d.count - 1 { out.append(nil) }
    return out
}

/// "+6 −3" for a diff.
public func diffStat(_ d: [EditDiffLine]) -> String {
    let add = d.filter { $0.kind == .added }.count, del = d.filter { $0.kind == .removed }.count
    return "+\(add) −\(del)"
}

/// The characters of line n (from 1) without its newline; the end of the
/// text for a line past it.
public func lineRange(_ ns: NSString, line n: Int) -> NSRange {
    var at = 0, line = 1
    while line < n, at < ns.length {
        let r = ns.lineRange(for: NSRange(location: at, length: 0))
        at = NSMaxRange(r)
        line += 1
    }
    guard at < ns.length else { return NSRange(location: ns.length, length: 0) }
    var r = ns.lineRange(for: NSRange(location: at, length: 0))
    while r.length > 0, [10, 13].contains(ns.character(at: NSMaxRange(r) - 1)) { r.length -= 1 }
    return r
}
