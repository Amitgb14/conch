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
