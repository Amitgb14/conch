import Foundation

/// One commit of a project's history, as project.log gives it: children
/// always before their parents.
public struct LogCommit: Decodable, Equatable, Identifiable, Sendable {
    public var hash: String
    public var parents: [String]?
    public var subject: String
    public var author: String?
    public var time: Date?
    /// As git decorates them: "HEAD -> main", "origin/main", "tag: v1".
    public var refs: [String]?

    public init(hash: String, parents: [String] = [], subject: String = "", refs: [String] = []) {
        self.hash = hash
        self.parents = parents
        self.subject = subject
        self.refs = refs
    }

    public var id: String { hash }
    public var short: String { String(hash.prefix(7)) }
}

public struct LogResult: Decodable, Sendable {
    public var commits: [LogCommit]?
    public var more: Bool?
    /// The repository's remotes; nil from an older server.
    public var remotes: [String]?
}

/// A project's commit graph tab's key in the layout:
/// "commits|machine|project".
public func commitsKey(_ machine: String, _ project: String) -> String { "commits|\(machine)|\(project)" }

public func parseCommitsKey(_ k: String) -> (machine: String, project: String)? {
    let parts = k.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 3, parts[0] == "commits", !parts[2].isEmpty else { return nil }
    return (parts[1], parts[2])
}

/// A ref on a commit, as a chip draws it.
public struct CommitRef: Equatable, Sendable {
    public enum Kind: Equatable, Sendable { case head, branch, remote, tag }
    public var name: String
    public var kind: Kind

    /// git's decorations, sorted for a row: HEAD's branch first, then
    /// branches, remotes and tags. "HEAD -> main" is main, marked as HEAD;
    /// a detached "HEAD" is HEAD itself.
    public static func parse(_ refs: [String]?, remotes: Set<String> = ["origin", "upstream"]) -> [CommitRef] {
        var out: [CommitRef] = []
        for r in refs ?? [] {
            // A shallow clone's boundary, which an older server passes on.
            if r == "grafted" { continue }
            if r.hasPrefix("HEAD -> ") {
                out.append(CommitRef(name: String(r.dropFirst(8)), kind: .head))
            } else if r == "HEAD" {
                out.append(CommitRef(name: "HEAD", kind: .head))
            } else if r.hasPrefix("tag: ") {
                out.append(CommitRef(name: String(r.dropFirst(5)), kind: .tag))
            } else if let slash = r.firstIndex(of: "/"), remotes.contains(String(r[..<slash])) {
                // origin/HEAD says which branch the remote calls default,
                // which the row already shows on origin/main.
                if r.hasSuffix("/HEAD") { continue }
                out.append(CommitRef(name: r, kind: .remote))
            } else {
                out.append(CommitRef(name: r, kind: .branch))
            }
        }
        let order: [Kind: Int] = [.head: 0, .branch: 1, .remote: 2, .tag: 3]
        return out.enumerated().sorted { (order[$0.element.kind]!, $0.offset) < (order[$1.element.kind]!, $1.offset) }.map(\.element)
    }
}

/// One row of a commit graph: where the commit's dot is and the lines
/// through the row. Lanes are columns; the top half of a row runs from its
/// top edge to the dot's height, the bottom half from there down.
public struct GraphRow: Equatable, Sendable {
    public var commit: LogCommit
    /// The commit's lane.
    public var lane: Int
    /// Lanes passing straight through, top to bottom, not touching it.
    public var through: [Int]
    /// Lanes arriving from the top that end at the commit: its children.
    public var into: [Int]
    /// Lanes leaving the commit for the bottom edge: its parents.
    public var outOf: [Int]
    /// How many lanes the row is wide.
    public var width: Int
    /// The colour of each lane in the row, by lane: a branch keeps its
    /// colour down the graph.
    public var colors: [Int: Int]
    public var merge: Bool { (commit.parents?.count ?? 0) > 1 }
}

/// Lays out a history (children before parents) as rows of a graph, as
/// `git log --graph` does: each lane waits for the next commit it leads
/// to; a commit takes the lane waiting for it (or a new one), lanes also
/// waiting for it join it there, and its parents go on in its own lane
/// (the first) and in lanes of their own (a merge's others). A free lane
/// is reused, so the graph stays as narrow as the history allows. A
/// parent that is not in the list (a log cut short) runs off the bottom.
public func commitGraph(_ commits: [LogCommit]) -> [GraphRow] {
    var lanes: [String?] = []
    var laneColor: [Int] = []
    var nextColor = 0
    var rows: [GraphRow] = []

    func freeLane() -> Int {
        if let i = lanes.firstIndex(where: { $0 == nil }) { return i }
        lanes.append(nil)
        laneColor.append(0)
        return lanes.count - 1
    }
    func newColor() -> Int {
        defer { nextColor += 1 }
        return nextColor
    }

    for c in commits {
        let before = lanes
        let waiting = lanes.indices.filter { lanes[$0] == c.hash }
        let lane: Int
        if let first = waiting.first {
            lane = first
        } else {
            lane = freeLane()
            laneColor[lane] = newColor()
        }
        for j in waiting where j != lane { lanes[j] = nil }
        let into = waiting
        let through = before.indices.filter { before[$0] != nil && before[$0] != c.hash }

        var outOf: [Int] = []
        let parents = c.parents ?? []
        lanes[lane] = parents.first
        if !parents.isEmpty { outOf.append(lane) }
        for p in parents.dropFirst() {
            if let k = lanes.indices.first(where: { lanes[$0] == p }) {
                outOf.append(k)
            } else {
                let k = freeLane()
                lanes[k] = p
                laneColor[k] = newColor()
                outOf.append(k)
            }
        }
        var colors: [Int: Int] = [:]
        for i in Set(through + into + outOf + [lane]) { colors[i] = laneColor[i] }
        let width = max(before.count, lanes.count, lane + 1)
        rows.append(GraphRow(commit: c, lane: lane, through: through, into: into, outOf: outOf, width: width, colors: colors))
        while let last = lanes.last, last == nil { lanes.removeLast(); laneColor.removeLast() }
    }
    return rows
}

/// The colours a commit graph's lanes take in turn: the theme's accent
/// (the lighter one lines are drawn in), working, ok, warning, a purple for
/// merged, and error, each once. A theme may give two of them the same
/// colour — GitHub Light's accent is its working blue, Monokai's and
/// Everforest's their ok green — and taking both in turn drew two lanes
/// side by side in one colour. Never empty.
public func lanePalette(_ t: ConchTheme) -> [RGB] {
    var out: [RGB] = []
    for c in [t.line, t.work, t.ok, t.warn, RGB("#A371F7"), t.err] where !out.contains(where: { $0.hex == c.hex }) {
        out.append(c)
    }
    return out
}
