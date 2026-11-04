import Foundation

/// The sidebar driven from the keyboard, as the TUI's tree is: the rows on
/// screen in order — a folded row's children are not — with each one's
/// parent, so a cursor can step through them, fold and unfold, and climb.
public struct TreeNav: Sendable {
    public private(set) var rows: [TreeNode] = []
    private var parents: [String: String] = [:]
    private var index: [String: Int] = [:]

    /// The rows of these trees that are on screen, given which are open.
    public init(_ trees: [TreeNode], isOpen: (TreeNode) -> Bool) {
        func walk(_ n: TreeNode, _ parent: String?) {
            index[n.id] = rows.count
            rows.append(n)
            if let parent { parents[n.id] = parent }
            if let kids = n.children, isOpen(n) {
                for k in kids { walk(k, n.id) }
            }
        }
        for t in trees { walk(t, nil) }
    }

    public func row(_ id: String?) -> TreeNode? { id.flatMap { index[$0] }.map { rows[$0] } }

    /// The row by steps from the one given, kept at the ends; the first
    /// row when the one given is not on screen (it was folded away).
    public func step(from id: String?, by d: Int) -> String? {
        guard !rows.isEmpty else { return nil }
        guard let id, let i = index[id] else { return rows[0].id }
        return rows[min(max(i + d, 0), rows.count - 1)].id
    }

    public func parent(of id: String) -> String? { parents[id] }

    /// The first child on screen of an open row.
    public func firstChild(of id: String) -> String? {
        guard let i = index[id], i + 1 < rows.count, parents[rows[i + 1].id] == id else { return nil }
        return rows[i + 1].id
    }
}

/// What a key does in the sidebar: the TUI's tree keys.
public enum TreeKey: Equatable, Sendable {
    case up, down, left, right, open, fold, task, agent, terminal, close

    /// The key's meaning, by its key code (arrows, Return, space) or its
    /// character; nil for one the sidebar leaves alone. Keys with ⌘, ⌃ or
    /// ⌥ are the menus', never the tree's.
    public init?(keyCode: UInt16, characters: String?, modified: Bool) {
        if modified { return nil }
        switch keyCode {
        case 126: self = .up; return
        case 125: self = .down; return
        case 123: self = .left; return
        case 124: self = .right; return
        case 36, 76: self = .open; return
        case 49: self = .fold; return
        default: break
        }
        switch characters {
        case "k": self = .up
        case "j": self = .down
        case "h": self = .left
        case "l": self = .right
        case "t": self = .task
        case "c": self = .agent
        case "n": self = .terminal
        case "x": self = .close
        default: return nil
        }
    }
}
