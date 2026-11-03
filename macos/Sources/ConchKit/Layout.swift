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
    public var root: SplitNode
    public var focused: String

    public init(pane: String) {
        root = .leaf(pane)
        focused = pane
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
            tabs[i].focused = pane
        }
    }

    public mutating func step(_ by: Int) {
        guard !tabs.isEmpty else { return }
        active = ((active + by) % tabs.count + tabs.count) % tabs.count
    }

    public mutating func select(_ i: Int) {
        if tabs.indices.contains(i) { active = i }
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
            if let focus, keys.contains(focus) { tab.focused = focus }
            tabs.append(tab)
            if t.active == true && !hadTabs { active = tabs.count - 1 }
        }
    }
}

/// Whether a layout key is a pane's ("machine|id"), rather than a view of
/// a branch's changes, a project's commits or its sessions.
public func isPaneKey(_ k: String) -> Bool {
    parseChangesKey(k) == nil && parseCommitsKey(k) == nil && parseSessionsKey(k) == nil
}

/// How long to wait before trying this computer's server again, the nth
/// time in a row (from 1): soon, since a reload is back within a second or
/// two, and less often after, never more than 15s.
public func reconnectDelay(_ attempt: Int) -> Double {
    min(pow(2, Double(min(max(attempt, 1), 5) - 1)), 15)
}
