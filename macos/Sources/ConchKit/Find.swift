import Foundation

// Finding text in a pane's history, as the TUI's / and ? do
// (internal/tui/scrollsearch.go): the server searches the whole history
// (pane.search), and the view brings the match into sight with every match
// marked. The view is `conch attach`'s, since the server keeps a scroll per
// connection, so what to show is pasted into it with a token only this app
// knows (attachControlBody; cmd/conch/attach.go's attachControl).

/// pane.search's answer: the next match, counted from the oldest line of
/// history, the screen starting at History.
public struct PaneSearchResult: Decodable, Equatable, Sendable {
    public var found: Bool?
    /// Left out when it is 0, the oldest line, as are col and history.
    public var line: Int?
    public var col: Int?
    public var width: Int?
    public var history: Int?
    public var wrapped: Bool?

    public init(found: Bool? = nil, line: Int? = nil, col: Int? = nil, history: Int? = nil, wrapped: Bool? = nil) {
        self.found = found
        self.line = line
        self.col = col
        self.history = history
        self.wrapped = wrapped
    }
}

/// The find bar of one pane: what is looked for, and where the next look
/// starts — at the end, until something is found.
public struct PaneFind: Equatable, Sendable {
    public let pane: String
    public var query = "" {
        didSet { if query != oldValue { line = PaneFind.end; col = 0; note = "" } }
    }
    public private(set) var line = PaneFind.end
    public private(set) var col = 0
    /// What the last look said: not found, or that it went round.
    public private(set) var note = ""

    /// Past the last line: pane.search starts there from that end.
    public static let end = Int(Int32.max)

    public init(pane: String) { self.pane = pane }

    /// pane.search's parameters for the next look, older (back) or newer.
    public func params(id: String, back: Bool) -> PaneSearchParams {
        PaneSearchParams(id: id, query: query, line: line, col: col, backward: back ? true : nil)
    }

    /// A look that could not be made, and why.
    public mutating func failed(_ why: String) { note = why }

    /// Takes what a look found, and says what the view is to be told: the
    /// match to show, or — nothing found — that nothing is to be marked.
    public mutating func took(_ r: PaneSearchResult, back: Bool) -> AttachControl {
        guard r.found == true else {
            note = "Not found"
            return AttachControl(clear: true)
        }
        // The oldest line is 0, which the server leaves out (omitempty).
        let l = r.line ?? 0
        line = l
        col = r.col ?? 0
        note = r.wrapped == true ? (back ? "Went round from the bottom" : "Went round from the top") : ""
        return AttachControl(find: query, line: l, history: r.history ?? 0)
    }
}

/// What a control paste tells `conch attach`.
public struct AttachControl: Encodable, Equatable, Sendable {
    public var find: String?
    public var line: Int?
    public var history: Int?
    public var clear: Bool?

    public init(find: String? = nil, line: Int? = nil, history: Int? = nil, clear: Bool = false) {
        self.find = find
        self.line = line
        self.history = history
        self.clear = clear ? true : nil
    }
}

/// The environment variable that gives `conch attach` the token.
public let attachControlEnv = "CONCH_ATTACH_CONTROL"

/// A control paste: an invisible separator, the token, and the JSON —
/// one line whatever the query holds, since JSON escapes it.
public func attachControlBody(token: String, _ c: AttachControl) -> String {
    let enc = JSONEncoder()
    enc.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
    let json = (try? enc.encode(c)).map { String(decoding: $0, as: UTF8.self) } ?? "{}"
    return "\u{2063}conch-attach:\(token) \(json)"
}

public struct PaneSearchParams: Encodable, Equatable, Sendable {
    public var id: String
    public var query: String
    public var line: Int
    public var col: Int
    public var backward: Bool?
}

/// Whether a layout key is a pane's terminal, which can be searched, rather
/// than a view of the app's own (changes, sessions, commits, files).
public func isTerminalKey(_ k: String) -> Bool {
    parseChangesKey(k) == nil && parseSessionsKey(k) == nil && parseCommitsKey(k) == nil && parseFilesKey(k) == nil
}
