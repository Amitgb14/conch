import Foundation

/// How a workbench tab arranges an agent's branch: its files (the Files
/// view, tree and editor), a terminal in its checkout, the agent as a chat,
/// and the agent's own terminal. Every layout holds all four; what differs
/// is where each sits and which start hidden. The mock is "Conch — one
/// window: files, chat, terminal".
public enum BenchLayout: String, CaseIterable, Equatable, Sendable {
    /// Files and editor, the terminal below them, the chat on the right.
    case ide
    /// The chat filling the window, files and a terminal in a column beside.
    case chat
    /// The chat and the agent's own terminal side by side, files and a
    /// terminal in a row below them.
    case agent
    /// IntelliJ's: the editor in the middle and the rest tool windows docked
    /// round it, opened and closed from a strip of icons down each edge.
    case jetbrains

    /// A saved choice; anything else, an older build's or none, is IDE.
    public init(setting: String?) {
        self = BenchLayout(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .ide
    }

    public var label: String {
        switch self {
        case .ide: return "IDE"
        case .chat: return "Chat First"
        case .agent: return "Agent"
        case .jetbrains: return "JetBrains"
        }
    }

    public var detail: String {
        switch self {
        case .ide: return "Files and editor, the terminal below them, the chat on the right"
        case .chat: return "The chat in the middle, files and a terminal beside it"
        case .agent: return "The chat and the agent's own terminal side by side, files and a terminal below"
        case .jetbrains: return "Tool windows round the editor, opened from strips down each edge"
        }
    }

    /// Whether the tab is framed by IntelliJ's strips of tool-window icons.
    public var stripes: Bool { self == .jetbrains }
}

/// The four parts of a workbench, by their layout keys.
public struct BenchParts: Equatable, Sendable {
    public var files: String
    public var shell: String
    public var chat: String
    public var agent: String

    public init(files: String, shell: String, chat: String, agent: String) {
        self.files = files
        self.shell = shell
        self.chat = chat
        self.agent = agent
    }

    public var all: [String] { [files, shell, chat, agent] }
}

/// A layout's arrangement of the parts: the split tree, each split's
/// shares by its path, and what starts hidden. The chat and the agent's
/// terminal are the same conversation, so where only one has room the other
/// waits hidden beside it, a click away (Chat ⇄ Terminal).
public func benchArrangement(_ l: BenchLayout, _ p: BenchParts) -> (root: SplitNode, weights: [String: [Double]], hidden: Set<String>) {
    switch l {
    case .ide, .jetbrains:
        let root = SplitNode.split(.horizontal, [.split(.vertical, [.leaf(p.files), .leaf(p.shell)]), .leaf(p.chat), .leaf(p.agent)])
        return (root, ["": [0.68, 0.32, 0.32], "0": [0.7, 0.3]], [p.agent])
    case .chat:
        let root = SplitNode.split(.horizontal, [.leaf(p.chat), .leaf(p.agent), .split(.vertical, [.leaf(p.files), .leaf(p.shell)])])
        return (root, ["": [0.54, 0.54, 0.46], "2": [0.62, 0.38]], [p.agent])
    case .agent:
        // Side by side the Files view's own tree leaves its editor no room,
        // so files and the terminal take the full width below.
        let root = SplitNode.split(.vertical, [.split(.horizontal, [.leaf(p.chat), .leaf(p.agent)]),
                                               .split(.horizontal, [.leaf(p.files), .leaf(p.shell)])])
        return (root, ["": [0.58, 0.42], "0": [0.5, 0.5], "1": [0.64, 0.36]], [])
    }
}

/// The layout key of an agent's chat: "chat|machine|pane".
public func chatKey(_ pane: String) -> String { "chat|" + pane }

/// The agent pane a chat key is of.
public func parseChatKey(_ k: String) -> String? {
    guard k.hasPrefix("chat|") else { return nil }
    let rest = String(k.dropFirst(5))
    return rest.contains("|") ? rest : nil
}

/// A workbench's terminal before its shell has started: a place in the
/// layout the shell takes when the server answers.
public func benchShellKey(_ pane: String) -> String { "benchshell|" + pane }

public func parseBenchShellKey(_ k: String) -> String? {
    guard k.hasPrefix("benchshell|") else { return nil }
    let rest = String(k.dropFirst(11))
    return rest.contains("|") ? rest : nil
}

/// The children of a split that show: those with any leaf not hidden.
public func shownKids(_ kids: [SplitNode], hidden: Set<String>) -> [Int] {
    kids.indices.filter { kids[$0].panes.contains { !hidden.contains($0) } }
}

/// Shares for the children shown, from the shares of all of them: each
/// shown one's share of what the shown ones had, so hiding one gives its
/// room to the rest in proportion.
public func shownShares(_ all: [Double], shown: [Int]) -> [Double] {
    let picked = shown.map { all.indices.contains($0) ? max(all[$0], 0) : 0 }
    let total = picked.reduce(0, +)
    guard total > 0 else { return shown.map { _ in 1 / Double(max(shown.count, 1)) } }
    return picked.map { $0 / total }
}

extension Tab {
    /// Whether a leaf is on screen: in the tab and not hidden.
    public func shows(_ leaf: String) -> Bool { root.panes.contains(leaf) && !hidden.contains(leaf) }

    /// Moves the boundary between two shown children a and b of the split
    /// at path (n children in all) by delta, a share of the shown length —
    /// resize for a split with some children hidden.
    public mutating func resize(_ path: String, n: Int, between a: Int, and b: Int, shown: [Int], by delta: Double, min: Double = 0.08) {
        guard n >= 2, a != b, shown.contains(a), shown.contains(b) else { return }
        var w = shares(path, n)
        let total = shown.reduce(0) { $0 + w[$1] }
        guard total > 0 else { return }
        let d = delta * total
        let pair = w[a] + w[b]
        let lo = min * total
        let na = Swift.min(Swift.max(w[a] + d, lo), pair - lo)
        w[a] = na
        w[b] = pair - na
        weights[path] = w
    }
}

extension Layout {
    /// The tab holding an agent's workbench.
    public func benchTab(of agent: String) -> Int? { tabs.firstIndex { $0.benchAgent == agent } }

    /// An agent's workbench in a tab of its own, arranged by l: the parts
    /// are taken out of any other tab first, since a pane is shown in one
    /// place. One already open is rearranged and shown.
    public mutating func openBench(_ l: BenchLayout, _ p: BenchParts) {
        if let i = benchTab(of: p.agent) {
            arrange(i, l, p)
            active = i
            return
        }
        for leaf in p.all { close(leaf) }
        var t = Tab(pane: p.chat)
        t.benchAgent = p.agent
        tabs.append(t)
        arrange(tabs.count - 1, l, p)
        active = tabs.count - 1
    }

    /// The current workbench tab in another layout, its parts as they are.
    public mutating func setBench(_ l: BenchLayout) {
        guard tabs.indices.contains(active), let parts = benchParts(active) else { return }
        arrange(active, l, parts)
    }

    /// A workbench tab's parts, read off its leaves.
    public func benchParts(_ i: Int) -> BenchParts? {
        guard tabs.indices.contains(i), let agent = tabs[i].benchAgent else { return nil }
        let leaves = tabs[i].root.panes
        let files = leaves.first { parseFilesKey($0) != nil }
        let shell = leaves.first { $0 != agent && parseChatKey($0) == nil && parseFilesKey($0) == nil }
        guard let files, let shell else { return nil }
        return BenchParts(files: files, shell: shell, chat: chatKey(agent), agent: agent)
    }

    private mutating func arrange(_ i: Int, _ l: BenchLayout, _ p: BenchParts) {
        let (root, weights, hidden) = benchArrangement(l, p)
        tabs[i].root = root
        tabs[i].weights = weights
        tabs[i].hidden = hidden
        tabs[i].bench = l
        if !tabs[i].shows(tabs[i].focused) { tabs[i].focused = p.chat }
    }

    /// A leaf of the current tab shown or hidden: a tool window opened or
    /// closed from a strip. The last leaf shown stays shown, and the
    /// keyboard leaves a leaf as it hides.
    public mutating func toggleHidden(_ leaf: String) {
        guard tabs.indices.contains(active), tabs[active].root.panes.contains(leaf) else { return }
        if tabs[active].hidden.contains(leaf) {
            tabs[active].hidden.remove(leaf)
            return
        }
        let shown = tabs[active].root.panes.filter { !tabs[active].hidden.contains($0) }
        guard shown.count > 1 else { return }
        tabs[active].hidden.insert(leaf)
        if tabs[active].focused == leaf { tabs[active].focused = shown.first { $0 != leaf } ?? leaf }
    }

    /// The chat and the agent's terminal swapped where only one shows.
    public mutating func swapChat() {
        guard tabs.indices.contains(active), let a = tabs[active].benchAgent else { return }
        let c = chatKey(a)
        var h = tabs[active].hidden
        switch (h.contains(c), h.contains(a)) {
        case (false, true): h.remove(a); h.insert(c); if tabs[active].focused == c { tabs[active].focused = a }
        case (true, false): h.remove(c); h.insert(a); if tabs[active].focused == a { tabs[active].focused = c }
        default: return
        }
        tabs[active].hidden = h
    }

    /// One leaf put in another's place: a workbench's terminal once its
    /// shell has started.
    public mutating func replace(_ old: String, with new: String) {
        guard let i = tab(showing: old) else { return }
        if let j = tab(showing: new), j != i { close(new) }
        guard let k = tab(showing: old) else { return }
        let w = tabs[k].weights
        let hid = tabs[k].hidden.contains(old)
        tabs[k].root = tabs[k].root.replacing(old, with: new)
        tabs[k].weights = w
        if hid { tabs[k].hidden.remove(old); tabs[k].hidden.insert(new) }
        if tabs[k].focused == old { tabs[k].focused = new }
    }
}

extension SplitNode {
    func replacing(_ old: String, with new: String) -> SplitNode {
        switch self {
        case .leaf(let id): return id == old ? .leaf(new) : self
        case .split(let a, let kids): return .split(a, kids.map { $0.replacing(old, with: new) })
        }
    }
}

// MARK: - the chat

/// One entry of an agent's conversation (agent.chat): what the person
/// typed, what the agent said, a tool it used — with what it was given and
/// what came back — or a note (an interruption).
public struct ChatTurn: Decodable, Equatable, Sendable {
    /// Its place in the whole conversation: an update replaces the turn
    /// with the same seq (a tool's result arriving). nil from the first
    /// servers with chats, which sent the whole conversation every time.
    public var seq: Int?
    public var role: String
    public var text: String?
    public var detail: String?
    public var time: Date?
    public var input: String?
    public var inputKind: String?
    public var output: String?
    public var error: Bool?
    public var done: Bool?

    enum CodingKeys: String, CodingKey {
        case seq, role, text, detail, time, input, output, error, done
        case inputKind = "input_kind"
    }

    public init(seq: Int? = nil, role: String, text: String? = nil, detail: String? = nil, time: Date? = nil,
                input: String? = nil, inputKind: String? = nil, output: String? = nil, error: Bool? = nil, done: Bool? = nil) {
        self.seq = seq
        self.role = role
        self.text = text
        self.detail = detail
        self.time = time
        self.input = input
        self.inputKind = inputKind
        self.output = output
        self.error = error
        self.done = done
    }
}

/// A pane's agent's conversation, as agent.chat answers: the turns new or
/// changed since the epoch and version asked with, or — reset, or from a
/// server that keeps no versions — all of them.
public struct AgentChat: Decodable, Equatable, Sendable {
    public var agent: String
    public var session: String?
    public var turns: [ChatTurn]?
    public var note: String?
    public var epoch: String?
    public var ver: Int64?
    public var reset: Bool?

    public init(agent: String, session: String? = nil, turns: [ChatTurn]? = nil, note: String? = nil,
                epoch: String? = nil, ver: Int64? = nil, reset: Bool? = nil) {
        self.agent = agent
        self.session = session
        self.turns = turns
        self.note = note
        self.epoch = epoch
        self.ver = ver
        self.reset = reset
    }
}

/// What a chat holds: the conversation so far, and the version of the
/// server's log it is, to ask for only what changed after it.
public struct ChatLog: Equatable, Sendable {
    public var agent = ""
    public var session: String?
    public var note: String?
    public var epoch: String?
    public var ver: Int64 = 0
    public var turns: [ChatTurn] = []
    /// Whether an answer has come at all.
    public var loaded = false

    public init() {}

    /// An answer taken in: a reset — or one from a server that keeps no
    /// versions — is the whole conversation; otherwise each turn replaces
    /// the one with its seq or is added after the rest. Returns whether
    /// anything changed.
    @discardableResult
    public mutating func apply(_ c: AgentChat) -> Bool {
        let before = self
        agent = c.agent
        note = c.note
        if c.session != session { session = c.session; turns = [] }
        let incoming = c.turns ?? []
        // The server leaves reset out when false, so absent is not a reset.
        if c.reset == true || c.epoch == nil || c.epoch != epoch {
            turns = incoming
        } else {
            for t in incoming {
                if let s = t.seq, let i = turns.lastIndex(where: { $0.seq == s }) {
                    turns[i] = t
                } else if let s = t.seq, let i = turns.firstIndex(where: { ($0.seq ?? -1) > s }) {
                    turns.insert(t, at: i)
                } else {
                    turns.append(t)
                }
            }
            if turns.count > 1500 { turns.removeFirst(turns.count - 1500) }
        }
        epoch = c.epoch
        ver = c.ver ?? 0
        loaded = true
        return self != before
    }

    /// Whether the person's message has reached the conversation: the last
    /// thing they said, after the turn count it was sent at.
    public func has(sent text: String, after count: Int) -> Bool {
        let want = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return turns.dropFirst(min(count, turns.count)).contains { $0.role == "user" && ($0.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines) == want }
    }
}

/// What a chat draws: a message, or the tools used in a row between two —
/// a run of Read, Edit, Bash is one block, folded, not a screenful.
public enum ChatBlock: Equatable, Sendable, Identifiable {
    case user(String, Int)
    case agent(String, Int)
    case tools([ChatTurn], Int)
    case note(String, Int)

    /// Its place in the conversation, which is what keeps a block the same
    /// one as turns are added after it.
    public var id: Int {
        switch self {
        case .user(_, let i), .agent(_, let i), .tools(_, let i), .note(_, let i): return i
        }
    }
}

/// Whether the agent may be busy with something Stop could end. conch's
/// own state is read off the screen and hooks and can lag a message just
/// sent, so a conversation that ends in a question not yet answered, or a
/// tool still running, counts too; one that ends in an interruption does not.
public func chatBusy(state: AgentState?, pending: Int, turns: [ChatTurn]) -> Bool {
    if state == .working || pending > 0 { return true }
    guard let last = turns.last else { return false }
    switch last.role {
    case "user": return true
    case "tool": return last.done == false
    default: return false
    }
}

public func chatBlocks(_ turns: [ChatTurn]) -> [ChatBlock] {
    var out: [ChatBlock] = []
    for (n, t) in turns.enumerated() {
        let i = t.seq ?? n
        switch t.role {
        case "note":
            if let s = t.text, !s.isEmpty { out.append(.note(s, i)) }
        case "user":
            if let s = t.text, !s.isEmpty { out.append(.user(s, i)) }
        case "assistant":
            if let s = t.text, !s.isEmpty { out.append(.agent(s, i)) }
        case "tool":
            if case .tools(var run, let at)? = out.last {
                run.append(t)
                out[out.count - 1] = .tools(run, at)
            } else {
                out.append(.tools([t], i))
            }
        default:
            continue
        }
    }
    return out
}

/// An SF Symbol for a tool an agent used, by Claude Code's names for them.
public func toolSymbol(_ name: String) -> String {
    switch name {
    case "Read", "NotebookRead": return "doc.text"
    case "Edit", "MultiEdit", "Write", "NotebookEdit": return "pencil"
    case "Bash", "BashOutput", "KillShell": return "terminal"
    case "Grep", "Glob", "LS": return "magnifyingglass"
    case "WebFetch", "WebSearch": return "globe"
    case "Task", "Agent": return "person.2"
    case "TodoWrite": return "checklist"
    default: return name.hasPrefix("mcp__") ? "puzzlepiece.extension" : "wrench.and.screwdriver"
    }
}

/// A tool's name as a chat shows it: an MCP server's tool as server · tool.
public func toolLabel(_ name: String) -> String {
    guard name.hasPrefix("mcp__") else { return name }
    let parts = name.dropFirst(5).split(separator: "_", omittingEmptySubsequences: true)
    guard parts.count >= 2 else { return name }
    let rest = name.dropFirst(5 + parts[0].count).drop { $0 == "_" }
    return "\(parts[0]) · \(rest)"
}

/// A short summary of a run of tools: "Read 3 files, ran 2 commands".
public func toolsSummary(_ run: [ChatTurn]) -> String {
    var counts: [(String, Int)] = []
    func add(_ k: String) {
        if let i = counts.firstIndex(where: { $0.0 == k }) { counts[i].1 += 1 } else { counts.append((k, 1)) }
    }
    for t in run {
        switch t.text ?? "" {
        case "Read", "NotebookRead": add("read")
        case "Edit", "MultiEdit", "Write", "NotebookEdit": add("edit")
        case "Bash": add("ran")
        case "Grep", "Glob", "LS": add("searched")
        default: add("used")
        }
    }
    func n(_ c: Int, _ one: String, _ many: String) -> String { c == 1 ? "1 \(one)" : "\(c) \(many)" }
    let words = counts.map { k, c -> String in
        switch k {
        case "read": return "read " + n(c, "file", "files")
        case "edit": return "edited " + n(c, "file", "files")
        case "ran": return "ran " + n(c, "command", "commands")
        case "searched": return "searched " + (c == 1 ? "once" : "\(c) times")
        default: return "used " + n(c, "tool", "tools")
        }
    }
    let s = words.joined(separator: ", ")
    return s.prefix(1).uppercased() + s.dropFirst()
}

/// A shell a workbench can take rather than start another: a plain
/// terminal in the agent's folder, running, and shown nowhere — the one a
/// workbench opened before the app last quit, say. The newest such.
public func reusableShell(_ panes: [PaneInfo], cwd: String?, shown: (String) -> Bool) -> PaneInfo? {
    guard let cwd, !cwd.isEmpty else { return nil }
    let shells: Set<String> = ["zsh", "bash", "fish", "sh", "nu", "dash", "ksh", "tcsh"]
    func isShell(_ c: [String]?) -> Bool {
        guard let first = c?.first else { return true }
        return shells.contains((first as NSString).lastPathComponent.trimmingCharacters(in: CharacterSet(charactersIn: "-")))
    }
    return panes.filter { $0.agent == nil && $0.running && !$0.isSSH && $0.cwd == cwd && !shown($0.id) && $0.createdBy == nil && isShell($0.command) }
        .max { ($0.created ?? .distantPast) < ($1.created ?? .distantPast) }
}

/// An agent's name as a chat's header says it.
public func agentDisplayName(_ name: String) -> String {
    switch name {
    case "claude": return "Claude"
    case "codex": return "Codex"
    case "gemini": return "Gemini"
    case "opencode": return "OpenCode"
    case "devin": return "Devin"
    default: return name.prefix(1).uppercased() + name.dropFirst()
    }
}

/// The width of the Files view's tree (or search) beside its file: 300
/// points (380 searching) as it has always been, less in a card too narrow
/// for that to leave the file half — never under 170.
public func filesSideWidth(search: Bool, card: Double) -> Double {
    let usual = search ? 380.0 : 300.0
    guard card > 0 else { return usual }
    return max(min(usual, card * 0.45), min(170, usual))
}

// MARK: - markdown

/// A block of an answer's markdown, as a chat lays it out: what Claude and
/// the others write — headings, lists, code, tables, quotes — rather than
/// one run of text with its asterisks showing.
public enum MDBlock: Equatable, Sendable {
    case heading(Int, String)
    case paragraph(String)
    /// Items, each with its depth (0 the outermost) and its marker: "•" or
    /// "1." and so on.
    case list([MDItem])
    case code(String, String) // its language, its text
    case quote(String)
    case table([String], [[String]])
    case rule
}

public struct MDItem: Equatable, Sendable {
    public var depth: Int
    public var marker: String
    public var text: String
    public init(depth: Int, marker: String, text: String) {
        self.depth = depth
        self.marker = marker
        self.text = text
    }
}

public func markdownBlocks(_ src: String) -> [MDBlock] {
    let lines = src.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")
    var out: [MDBlock] = []
    var para: [String] = []
    var i = 0
    func flush() {
        if !para.isEmpty { out.append(.paragraph(para.joined(separator: "\n"))); para = [] }
    }
    func trimmed(_ s: String) -> String { s.trimmingCharacters(in: .whitespaces) }
    func indent(_ s: String) -> Int { s.prefix { $0 == " " || $0 == "\t" }.reduce(0) { $0 + ($1 == "\t" ? 4 : 1) } }
    func listItem(_ s: String) -> (depth: Int, marker: String, text: String)? {
        let t = trimmed(s)
        let d = indent(s) / 2
        for m in ["- ", "* ", "+ "] where t.hasPrefix(m) {
            var rest = String(t.dropFirst(2))
            var mark = "•"
            if rest.hasPrefix("[ ] ") { mark = "☐"; rest = String(rest.dropFirst(4)) } else if rest.lowercased().hasPrefix("[x] ") { mark = "☑"; rest = String(rest.dropFirst(4)) }
            return (d, mark, rest)
        }
        let digits = t.prefix { $0.isNumber }
        if !digits.isEmpty, digits.count <= 3 {
            let after = t.dropFirst(digits.count)
            if after.hasPrefix(". ") || after.hasPrefix(") ") { return (d, digits + ".", String(after.dropFirst(2))) }
        }
        return nil
    }
    func cells(_ s: String) -> [String] {
        var t = trimmed(s)
        if t.hasPrefix("|") { t.removeFirst() }
        if t.hasSuffix("|") { t.removeLast() }
        return t.components(separatedBy: "|").map { trimmed($0) }
    }
    func isDivider(_ s: String) -> Bool {
        let c = cells(s)
        return !c.isEmpty && c.allSatisfy { !$0.isEmpty && $0.allSatisfy { "-:".contains($0) } && $0.contains("-") }
    }
    while i < lines.count {
        let line = lines[i]
        let t = trimmed(line)
        if t.hasPrefix("```") || t.hasPrefix("~~~") {
            flush()
            let fence = String(t.prefix(3))
            let lang = trimmed(String(t.dropFirst(3)))
            var body: [String] = []
            i += 1
            while i < lines.count, !trimmed(lines[i]).hasPrefix(fence) {
                body.append(lines[i])
                i += 1
            }
            out.append(.code(lang, body.joined(separator: "\n")))
            i += 1
            continue
        }
        if t.isEmpty { flush(); i += 1; continue }
        if t.hasPrefix("#") {
            let level = t.prefix { $0 == "#" }.count
            if level <= 6, t.dropFirst(level).hasPrefix(" ") {
                flush()
                out.append(.heading(level, trimmed(String(t.dropFirst(level)))))
                i += 1
                continue
            }
        }
        if t == "---" || t == "***" || t == "___" {
            flush(); out.append(.rule); i += 1; continue
        }
        if t.hasPrefix("|"), i + 1 < lines.count, isDivider(lines[i + 1]) {
            flush()
            let head = cells(line)
            var rows: [[String]] = []
            i += 2
            while i < lines.count, trimmed(lines[i]).hasPrefix("|") {
                rows.append(cells(lines[i]))
                i += 1
            }
            out.append(.table(head, rows))
            continue
        }
        if t.hasPrefix(">") {
            flush()
            var q: [String] = []
            while i < lines.count, trimmed(lines[i]).hasPrefix(">") {
                q.append(trimmed(String(trimmed(lines[i]).dropFirst())))
                i += 1
            }
            out.append(.quote(q.joined(separator: "\n")))
            continue
        }
        if listItem(line) != nil {
            flush()
            var items: [MDItem] = []
            while i < lines.count {
                if let it = listItem(lines[i]) {
                    items.append(MDItem(depth: it.depth, marker: it.marker, text: it.text))
                } else if !trimmed(lines[i]).isEmpty, indent(lines[i]) >= 2, !items.isEmpty {
                    items[items.count - 1].text += " " + trimmed(lines[i]) // an item carried onto the next line
                } else {
                    break
                }
                i += 1
            }
            out.append(.list(items))
            continue
        }
        para.append(t)
        i += 1
    }
    flush()
    return out
}
