import Foundation

/// How a tab's panes are laid out: one pane, or a split of others side by
/// side or one above the other — tmux's, and the TUI's.
public indirect enum SplitNode: Equatable, Sendable {
    case leaf(String)
    case split(Axis, [SplitNode])

    public enum Axis: Equatable, Sendable { case horizontal, vertical } // side by side, stacked

    public var panes: [String] {
        switch self {
        case .leaf(let id): return [id]
        case .split(_, let kids): return kids.flatMap(\.panes)
        }
    }

    /// The node with pane `at` split along axis, `new` after it. Splitting
    /// the same way as the split it is in adds a sibling rather than a
    /// split of one.
    func splitting(_ at: String, _ axis: Axis, _ new: String) -> SplitNode {
        switch self {
        case .leaf(let id):
            return id == at ? .split(axis, [.leaf(id), .leaf(new)]) : self
        case .split(let a, var kids):
            if a == axis, let i = kids.firstIndex(of: .leaf(at)) {
                kids.insert(.leaf(new), at: i + 1)
                return .split(a, kids)
            }
            return .split(a, kids.map { $0.splitting(at, axis, new) })
        }
    }

    /// The node without pane id, collapsing a split left with one child;
    /// nil when nothing is left.
    func removing(_ id: String) -> SplitNode? {
        switch self {
        case .leaf(let p):
            return p == id ? nil : self
        case .split(let a, let kids):
            let left = kids.compactMap { $0.removing(id) }
            if left.isEmpty { return nil }
            if left.count == 1 { return left[0] }
            return .split(a, left)
        }
    }
}

public struct Tab: Identifiable, Equatable, Sendable {
    public var id = UUID()
    public var root: SplitNode {
        // A split added or gone: its sizes start even again, and zoom ends.
        didSet { if root != oldValue { weights = [:]; zoomed = false } }
    }
    public var focused: String {
        didSet { if focused != oldValue { zoomed = false } }
    }
    /// The share of each split's children, by the split's path from the
    /// root ("" the root, "1" its second child, "1.0" that one's first):
    /// dragged between splits, even until then.
    public var weights: [String: [Double]] = [:]
    /// The focused split alone, filling the tab (tmux's zoom).
    public var zoomed = false
    /// What it was named, by hand or in the TUI; nil names it by its pane.
    public var name: String?
    /// Leaves kept in the tab but not drawn: a workbench's tool windows
    /// closed from its strips, or the half of Chat ⇄ Terminal not shown.
    /// Their splits give the room to the rest.
    public var hidden: Set<String> = []
    /// A workbench's layout, and the agent it is for; nil for a tab of
    /// splits made by hand.
    public var bench: BenchLayout?
    public var benchAgent: String?

    public init(pane: String) {
        root = .leaf(pane)
        focused = pane
    }

    /// The shares of the split at path with n children: what was dragged,
    /// else even.
    public func shares(_ path: String, _ n: Int) -> [Double] {
        if let w = weights[path], w.count == n, w.allSatisfy({ $0 > 0 }) { return w }
        return Array(repeating: 1 / Double(max(n, 1)), count: max(n, 1))
    }

    /// Moves the boundary after child i of the split at path by delta (a
    /// share of the split's length), each side kept to at least min.
    public mutating func resize(_ path: String, n: Int, after i: Int, by delta: Double, min: Double = 0.12) {
        guard n >= 2, i >= 0, i < n - 1 else { return }
        var w = shares(path, n)
        let total = w[i] + w[i + 1]
        let a = Swift.min(Swift.max(w[i] + delta, min), total - min)
        w[i] = a
        w[i + 1] = total - a
        weights[path] = w
    }

    /// Where each pane sits in the tab, in a unit square: what moving the
    /// keyboard by direction goes by.
    public func frames() -> [String: CGRect] {
        var out: [String: CGRect] = [:]
        func walk(_ n: SplitNode, _ r: CGRect, _ path: String) {
            switch n {
            case .leaf(let id): if !hidden.contains(id) { out[id] = r }
            case .split(let axis, let kids):
                // Hidden children take no room, as they are drawn.
                let shown = shownKids(kids, hidden: hidden)
                let w = shownShares(shares(path, kids.count), shown: shown)
                var at = axis == .horizontal ? r.minX : r.minY
                for (j, i) in shown.enumerated() {
                    let k = kids[i]
                    let len = (axis == .horizontal ? r.width : r.height) * w[j]
                    let kr = axis == .horizontal ? CGRect(x: at, y: r.minY, width: len, height: r.height)
                                                 : CGRect(x: r.minX, y: at, width: r.width, height: len)
                    walk(k, kr, path.isEmpty ? "\(i)" : "\(path).\(i)")
                    at += len
                }
            }
        }
        walk(root, CGRect(x: 0, y: 0, width: 1, height: 1), "")
        return out
    }

    public enum Direction: Sendable { case left, right, up, down }

    /// The pane beside the focused one in a direction: of those whose
    /// edge faces it and that overlap it across, the nearest, then the most
    /// overlapping, then the top or left one — a tie is common (a beside b
    /// over c), and the dictionary's order would make it a coin toss. nil
    /// at the tab's edge.
    public func neighbor(_ d: Direction) -> String? {
        let f = frames()
        guard let me = f[focused] else { return nil }
        let eps = 1e-6
        var best: (String, Double, Double, Double)?
        for (id, r) in f where id != focused {
            let gap: Double, overlap: Double
            switch d {
            case .left: gap = me.minX - r.maxX; overlap = min(me.maxY, r.maxY) - max(me.minY, r.minY)
            case .right: gap = r.minX - me.maxX; overlap = min(me.maxY, r.maxY) - max(me.minY, r.minY)
            case .up: gap = me.minY - r.maxY; overlap = min(me.maxX, r.maxX) - max(me.minX, r.minX)
            case .down: gap = r.minY - me.maxY; overlap = min(me.maxX, r.maxX) - max(me.minX, r.minX)
            }
            guard gap > -eps, overlap > eps else { continue }
            let across = Double(d == .left || d == .right ? r.minY : r.minX)
            if let b = best {
                let nearer = gap < b.1 - eps, same = abs(gap - b.1) <= eps
                let more = overlap > b.2 + eps, level = abs(overlap - b.2) <= eps
                guard nearer || (same && (more || (level && across < b.3))) else { continue }
            }
            best = (id, gap, overlap, across)
        }
        return best?.0
    }
}

/// The tabs of the window, tmux's way: a pane is shown in exactly one
/// place, opening one already shown goes to it, and a tab whose last pane
/// went closes.
public struct Layout: Equatable, Sendable {
    public var tabs: [Tab] = []
    public var active = 0

    public init() {}

    public var current: Tab? { tabs.indices.contains(active) ? tabs[active] : nil }
    public var focused: String? { current?.focused }

    public func tab(showing pane: String) -> Int? { tabs.firstIndex { $0.root.panes.contains(pane) } }

    /// Shows a pane: where it already is, or in a tab of its own.
    public mutating func open(_ pane: String) {
        if let i = tab(showing: pane) {
            active = i
            reveal(pane, in: i)
            tabs[i].focused = pane
            return
        }
        tabs.append(Tab(pane: pane))
        active = tabs.count - 1
    }

    /// Puts a new pane beside the focused one. With no tab yet it is a tab.
    public mutating func split(_ axis: SplitNode.Axis, with new: String) {
        guard let t = current else {
            open(new)
            return
        }
        if let old = tab(showing: new) { remove(new, from: old) }
        guard let i = tabs.firstIndex(where: { $0.id == t.id }) else {
            open(new)
            return
        }
        tabs[i].root = tabs[i].root.splitting(t.focused, axis, new)
        tabs[i].focused = new
        active = i
    }

    /// Takes a pane out of the window — closed, or gone from the server.
    /// Its split closes, and its tab when it was the last.
    public mutating func close(_ pane: String) {
        if let i = tab(showing: pane) { remove(pane, from: i) }
    }

    private mutating func remove(_ pane: String, from i: Int) {
        let before = tabs[i].root.panes
        if let root = tabs[i].root.removing(pane) {
            tabs[i].root = root
            if tabs[i].focused == pane {
                // The split beside it takes the keyboard: the one before it,
                // or after it when it was the first.
                let at = before.firstIndex(of: pane) ?? 0
                tabs[i].focused = at > 0 ? before[at - 1] : (root.panes.first ?? "")
            }
            return
        }
        tabs.remove(at: i)
        // The tab before the one that closed, as closing the last does.
        if active > i || (active == i && active > 0) { active -= 1 }
        active = min(active, max(tabs.count - 1, 0))
    }

    /// Drops every pane the server no longer has.
    public mutating func keep(only alive: Set<String>) {
        for t in tabs {
            for p in t.root.panes where !alive.contains(p) { close(p) }
        }
    }

    public mutating func focus(_ pane: String) {
        if let i = tab(showing: pane) {
            active = i
            reveal(pane, in: i)
            tabs[i].focused = pane
        }
    }

    /// A hidden leaf shown, as it is asked for: in a workbench, the agent's
    /// terminal takes the chat's place where they share one, and the other
    /// way round.
    private mutating func reveal(_ pane: String, in i: Int) {
        guard tabs[i].hidden.contains(pane) else { return }
        tabs[i].hidden.remove(pane)
        if let a = tabs[i].benchAgent, tabs[i].bench != .agent {
            if pane == a { tabs[i].hidden.insert(chatKey(a)) } else if pane == chatKey(a) { tabs[i].hidden.insert(a) }
        }
    }

    public mutating func step(_ by: Int) {
        guard !tabs.isEmpty else { return }
        active = ((active + by) % tabs.count + tabs.count) % tabs.count
    }

    public mutating func select(_ i: Int) {
        if tabs.indices.contains(i) { active = i }
    }

    /// A tab moved to another place in the bar, the one on screen staying
    /// on screen wherever it went.
    public mutating func moveTab(from i: Int, to j: Int) {
        guard tabs.indices.contains(i), tabs.indices.contains(j), i != j else { return }
        let showing = current?.id
        let t = tabs.remove(at: i)
        tabs.insert(t, at: j)
        if let showing, let k = tabs.firstIndex(where: { $0.id == showing }) { active = k }
    }

    /// Names a tab; an empty name goes back to naming it by its pane.
    public mutating func rename(_ i: Int, to name: String) {
        guard tabs.indices.contains(i) else { return }
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        tabs[i].name = n.isEmpty ? nil : n
    }

    /// The focused split alone, or the tab's splits again.
    public mutating func toggleZoom() {
        guard tabs.indices.contains(active), tabs[active].root.panes.count > 1 || tabs[active].zoomed else { return }
        tabs[active].zoomed.toggle()
    }

    /// The keyboard to the next split of the tab, round to the first after
    /// the last — tmux's ctrl+b o.
    public mutating func nextSplit() {
        guard tabs.indices.contains(active) else { return }
        let panes = tabs[active].root.panes
        guard panes.count > 1, let i = panes.firstIndex(of: tabs[active].focused) else { return }
        tabs[active].focused = panes[(i + 1) % panes.count]
    }

    /// The tab's splits back to even shares — tmux's ctrl+b =.
    public mutating func equalize() {
        if tabs.indices.contains(active) { tabs[active].weights = [:] }
    }

    /// The keyboard to the split beside the focused one; nothing at an edge.
    @discardableResult
    public mutating func move(_ d: Tab.Direction) -> Bool {
        guard tabs.indices.contains(active), let n = tabs[active].neighbor(d) else { return false }
        tabs[active].focused = n
        return true
    }
}

/// What a TUI is showing, as the server keeps it (view.get): its tabs, each
/// with its panes left to right then top to bottom.
public struct ViewState: Decodable, Sendable {
    public var focus: String?
    public var tabs: [ViewTab]?

    public init(focus: String? = nil, tabs: [ViewTab]? = nil) {
        self.focus = focus
        self.tabs = tabs
    }
}

public struct ViewTab: Decodable, Sendable {
    public var name: String?
    public var panes: [String]?
    public var active: Bool?

    public init(name: String? = nil, panes: [String]? = nil, active: Bool? = nil) {
        self.name = name
        self.panes = panes
        self.active = active
    }
}

extension SplitNode {
    /// Panes put in a tab as the mock lays them out, since the TUI says
    /// which panes a tab has but not how its splits are drawn: one fills
    /// it, two sit side by side, three are a tall one beside two stacked,
    /// and more are two stacked columns. nil for none.
    public static func arranged(_ panes: [String]) -> SplitNode? {
        switch panes.count {
        case 0: return nil
        case 1: return .leaf(panes[0])
        case 2: return .split(.horizontal, panes.map { .leaf($0) })
        case 3: return .split(.horizontal, [.leaf(panes[0]), .split(.vertical, [.leaf(panes[1]), .leaf(panes[2])])])
        default:
            let half = (panes.count + 1) / 2
            return .split(.horizontal, [.split(.vertical, panes[..<half].map { .leaf($0) }),
                                        .split(.vertical, panes[half...].map { .leaf($0) })])
        }
    }
}

extension Layout {
    /// Takes in the tabs a TUI shows on a machine, after any already here:
    /// panes named by key(machine, id), only those alive, and none shown
    /// already, since a pane is in one place. The TUI's active tab is shown
    /// when the window had none of its own; the pane the TUI's keyboard is
    /// on is each tab's focused one. It gives nothing the keyboard: that
    /// is the window's to do when somebody picks a pane.
    public mutating func adopt(_ view: ViewState, machine: String, alive: Set<String>) {
        let hadTabs = !tabs.isEmpty
        let focus = view.focus.map { key(machine, $0) }
        var shown = Set(tabs.flatMap(\.root.panes))
        for t in view.tabs ?? [] {
            var keys: [String] = []
            for id in t.panes ?? [] {
                let k = key(machine, id)
                if alive.contains(k), shown.insert(k).inserted { keys.append(k) }
            }
            guard let root = SplitNode.arranged(keys) else { continue }
            var tab = Tab(pane: keys[0])
            tab.root = root
            if let n = t.name, !n.isEmpty { tab.name = n }
            if let focus, keys.contains(focus) { tab.focused = focus }
            tabs.append(tab)
            if t.active == true && !hadTabs { active = tabs.count - 1 }
        }
    }
}

/// Whether a layout key is a pane's ("machine|id"), rather than a view of
/// a branch's changes, a project's commits or its sessions.
public func isPaneKey(_ k: String) -> Bool {
    parseChatKey(k) == nil && parseBenchShellKey(k) == nil && parseChangesKey(k) == nil && parseCommitsKey(k) == nil && parseSessionsKey(k) == nil && parseFilesKey(k) == nil && parseSetupKey(k) == nil && parseBrowserKey(k) == nil && parseTaskKey(k) == nil
}

/// How long to wait before trying this computer's server again, the nth
/// time in a row (from 1): soon, since a reload is back within a second or
/// two, and less often after, never more than 15s.
public func reconnectDelay(_ attempt: Int) -> Double {
    min(pow(2, Double(min(max(attempt, 1), 5) - 1)), 15)
}

/// Requests of one kind, numbered, so a reply that arrives after a newer
/// request was made is dropped rather than shown over the newer one's.
public struct Latest: Sendable {
    public private(set) var n = 0
    public init() {}
    /// A new request's number.
    public mutating func next() -> Int {
        n += 1
        return n
    }
    /// Whether the request numbered so is still the newest.
    public func isLatest(_ m: Int) -> Bool { m == n }
}

/// The size a new pane should start at: that of a pane shown alone in a
/// tab — the tab in front first — since a new pane opens in a tab of its
/// own. Started at the server's 80×24 and resized a moment later, an
/// agent that draws inline (Claude Code) had what it printed rewrapped to
/// the new width: lines broken mid-word, its highlighted blocks repeated
/// as blank bars. nil when no tab shows one pane alone yet.
public func fullTabSize(_ layout: Layout, size: (String) -> (cols: Int, rows: Int)?) -> (cols: Int, rows: Int)? {
    let order = layout.tabs.indices.contains(layout.active) ? [layout.active] + layout.tabs.indices.filter { $0 != layout.active } : Array(layout.tabs.indices)
    for i in order {
        if case .leaf(let k) = layout.tabs[i].root, isPaneKey(k), let s = size(k), s.cols > 1, s.rows > 1 { return s }
    }
    return nil
}
