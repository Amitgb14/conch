import Foundation

// Conch.app's windows (#194): more than one, each with its own workspace —
// or one project — and its own tabs and splits, while the machines and
// their servers are the same for all. What is decided here rather than in
// AppKit: what a project window shows, what is saved so the windows come
// back when the app opens again, which window holds a pane (a pane is shown
// in exactly one place, as in the TUI), and where a notification opens one.
//
// The windows are the app's own, kept in its preferences, never in ui.json:
// ui.json's workspaces are shared with the TUI, which has one screen and no
// use for a second window's tabs.

// MARK: a window for one project

/// What a project window's id starts with: no workspace in ui.json has one
/// like it, so it is never written there by mistake.
public let projectScopePrefix = "project:"

public func isProjectScope(_ id: String) -> Bool { id.hasPrefix(projectScopePrefix) }

/// A window that shows one project ("machine|id"): a workspace of that
/// project alone, never the first (which shows what no other one has).
/// What its agents start in it is in it too, as in any later workspace.
public func projectWorkspace(_ project: String, name: String) -> Workspace {
    let (m, id) = unkey(project)
    return Workspace(id: projectScopePrefix + project, name: name, projects: [Workspace.projectKey(m, id)], first: false)
}

// MARK: what is saved

/// A tab's splits as saved: a pane, or a split of others.
public indirect enum SavedNode: Codable, Equatable, Sendable {
    case pane(String)
    case split(horizontal: Bool, [SavedNode])

    private enum Keys: String, CodingKey { case pane, axis, kids }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: Keys.self)
        if let p = try c.decodeIfPresent(String.self, forKey: .pane) {
            self = .pane(p)
            return
        }
        let axis = try c.decode(String.self, forKey: .axis)
        guard axis == "h" || axis == "v" else {
            throw DecodingError.dataCorruptedError(forKey: .axis, in: c, debugDescription: "axis \(axis)")
        }
        self = .split(horizontal: axis == "h", try c.decode([SavedNode].self, forKey: .kids))
    }

    public func encode(to e: Encoder) throws {
        var c = e.container(keyedBy: Keys.self)
        switch self {
        case .pane(let p): try c.encode(p, forKey: .pane)
        case .split(let h, let kids):
            try c.encode(h ? "h" : "v", forKey: .axis)
            try c.encode(kids, forKey: .kids)
        }
    }

    init(_ n: SplitNode) {
        switch n {
        case .leaf(let p): self = .pane(p)
        case .split(let a, let kids): self = .split(horizontal: a == .horizontal, kids.map(SavedNode.init))
        }
    }

    /// nil for a split of nothing, which no layout makes.
    var node: SplitNode? {
        switch self {
        case .pane(let p): return .leaf(p)
        case .split(let h, let kids):
            let k = kids.compactMap(\.node)
            if k.isEmpty { return nil }
            return k.count == 1 ? k[0] : .split(h ? .horizontal : .vertical, k)
        }
    }
}

public struct SavedTab: Codable, Equatable, Sendable {
    public var root: SavedNode
    public var focused: String
    public var name: String?
    public var weights: [String: [Double]]?
    public var zoomed: Bool?
    public var hidden: [String]?
    public var bench: String?
    public var benchAgent: String?
}

/// A window's tabs as saved.
public struct SavedLayout: Codable, Equatable, Sendable {
    public var tabs: [SavedTab]
    public var active: Int

    /// What can come back of a layout: everything but what lives in the app
    /// alone and goes with it — a browser's page, a workbench's own shell.
    public init(_ l: Layout) {
        var l = l
        for k in l.tabs.flatMap(\.root.panes) where parseBrowserKey(k) != nil || parseBenchShellKey(k) != nil { l.close(k) }
        tabs = l.tabs.map { t in
            SavedTab(root: SavedNode(t.root), focused: t.focused, name: t.name,
                     weights: t.weights.isEmpty ? nil : t.weights, zoomed: t.zoomed ? true : nil,
                     hidden: t.hidden.isEmpty ? nil : t.hidden.sorted(), bench: t.bench?.rawValue, benchAgent: t.benchAgent)
        }
        active = l.active
    }

    private enum Keys: String, CodingKey { case tabs, active }

    /// A tab saved by a later build in a shape this one does not know is
    /// left out, rather than the whole window.
    public init(from d: Decoder) throws {
        struct Maybe: Decodable {
            var tab: SavedTab?
            init(from d: Decoder) throws { tab = try? SavedTab(from: d) }
        }
        let c = try d.container(keyedBy: Keys.self)
        tabs = (try c.decodeIfPresent([Maybe].self, forKey: .tabs) ?? []).compactMap(\.tab)
        active = try c.decodeIfPresent(Int.self, forKey: .active) ?? 0
    }

    /// The layout again. Panes that have since gone are the caller's to
    /// close once their machine answers, as for tabs taken from the TUI.
    public func layout() -> Layout {
        var l = Layout()
        for s in tabs {
            guard let root = s.root.node else { continue }
            let panes = root.panes
            var t = Tab(pane: panes[0])
            t.root = root
            t.focused = panes.contains(s.focused) ? s.focused : panes[0]
            t.name = s.name
            t.weights = s.weights ?? [:]
            t.zoomed = s.zoomed ?? false
            t.hidden = Set(s.hidden ?? []).intersection(panes)
            t.bench = s.bench.flatMap(BenchLayout.init(rawValue:))
            t.benchAgent = s.benchAgent
            l.tabs.append(t)
        }
        l.active = l.tabs.isEmpty ? 0 : min(max(active, 0), l.tabs.count - 1)
        return l
    }
}

/// A window as saved when the app quits, to open again when it starts: the
/// workspace it was on (by its ui.json id), or the project it shows, where
/// it was, and its tabs.
public struct SavedWindow: Codable, Equatable, Sendable {
    public var id: String
    public var workspace: String
    /// "machine|id" for a window of one project.
    public var project: String?
    /// NSWindow's frame descriptor.
    public var frame: String?
    public var layout: SavedLayout

    public init(id: String, workspace: String, project: String?, frame: String?, layout: SavedLayout) {
        self.id = id
        self.workspace = workspace
        self.project = project
        self.frame = frame
        self.layout = layout
    }
}

/// The windows saved, none for nothing or anything that does not read: a
/// window lost is a window to open again, never an app that will not start.
public func loadSavedWindows(_ data: Data?) -> [SavedWindow] {
    struct Maybe: Decodable {
        var w: SavedWindow?
        init(from d: Decoder) throws { w = try? SavedWindow(from: d) }
    }
    guard let data, let all = try? JSONDecoder().decode([Maybe].self, from: data) else { return [] }
    return all.compactMap(\.w)
}

public func encodeSavedWindows(_ ws: [SavedWindow]) -> Data {
    let e = JSONEncoder()
    e.outputFormatting = .sortedKeys
    return (try? e.encode(ws)) ?? Data("[]".utf8)
}

// MARK: which window

/// The window among shown that holds pane k, leaving out the one asking.
public func windowHolding(_ k: String, in shown: [(id: String, layout: Layout)], except: String? = nil) -> String? {
    shown.first { $0.id != except && $0.layout.tab(showing: k) != nil }?.id
}

/// A window as far as choosing one goes: what it shows and its tabs.
public struct WindowPlace: Sendable {
    public var id: String
    public var workspace: Workspace?
    public var layout: Layout

    public init(id: String, workspace: Workspace?, layout: Layout) {
        self.id = id
        self.workspace = workspace
        self.layout = layout
    }
}

/// Where a pane is opened from outside any window — a notification
/// clicked: the window showing it; else a window of its project; else a
/// window whose workspace has it, the key one first; else the key window,
/// or the first.
public func windowFor(pane k: String, snapshot: Snapshot, in windows: [WindowPlace], key keyWindow: String?) -> String? {
    if let w = windows.first(where: { $0.layout.tab(showing: k) != nil }) { return w.id }
    let (m, id) = unkey(k)
    func has(_ w: WindowPlace) -> Bool { snapshot.shown(in: w.workspace, machine: m).contains { $0.id == id } }
    if let w = windows.first(where: { isProjectScope($0.workspace?.id ?? "") && has($0) }) { return w.id }
    let showing = windows.filter { !isProjectScope($0.workspace?.id ?? "") && has($0) }
    if let w = showing.first(where: { $0.id == keyWindow }) ?? showing.first { return w.id }
    if let keyWindow, windows.contains(where: { $0.id == keyWindow }) { return keyWindow }
    return windows.first?.id
}

/// The window after (or before) the key one, round the end: ⌘` and ⌘~.
/// nil when there is no other.
public func nextWindow(after current: String?, in ids: [String], back: Bool = false) -> String? {
    guard let current, let i = ids.firstIndex(of: current) else { return ids.first }
    guard ids.count > 1 else { return nil }
    return ids[(i + (back ? ids.count - 1 : 1)) % ids.count]
}
