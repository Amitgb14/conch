import AppKit
import ConchKit

// More than one window (#194). Each window has a Store of its own — its
// workspace or project, its tabs and splits, its scratch layer, its
// terminals — and is a client of each machine's server as the TUI and a
// phone are, so the server settles a pane's size between them by who last
// typed. What is said once for all of them (a notification, the Dock's
// badge, a plan limit) is the first window's store's (`primary`).
//
// A pane is shown in exactly one place, as in the TUI: opening one another
// window shows brings that window forward with it (Store.open); a pane put
// in a window any other way — a split, a workbench, a pane started there —
// is taken out of the window that had it (claimPanes).

/// Every window's store, the first window's first.
@MainActor
final class AppWindows {
    static let shared = AppWindows()

    private(set) var stores: [Store] = []
    /// Brings a store's window to the front (AppDelegate's).
    var onBringForward: (Store) -> Void = { _ in }

    func add(_ s: Store) { if !stores.contains(where: { $0 === s }) { stores.append(s) } }
    func remove(_ s: Store) { stores.removeAll { $0 === s } }

    /// The windows on screen, minimised ones too. A window closed (the
    /// first one can be, and comes back) holds nothing.
    var showing: [Store] {
        stores.filter { $0.mainWindow.map { $0.isVisible || $0.isMiniaturized } ?? false }
    }

    /// The store whose window has the keyboard, else the first window's.
    var keyStore: Store? {
        showing.first { $0.mainWindow?.isKeyWindow == true } ?? showing.first ?? stores.first
    }

    /// The other window on screen that shows pane k.
    func holder(of k: String, except s: Store) -> Store? {
        let others = showing.filter { $0 !== s }
        guard let id = windowHolding(k, in: others.map { ($0.windowID, $0.layout) }) else { return nil }
        return others.first { $0.windowID == id }
    }

    /// Whether somebody is looking at pane k: the key window's focused one.
    func looking(at k: String) -> Bool {
        showing.contains { $0.mainWindow?.isKeyWindow == true && $0.layout.focused == k }
    }

    /// The window a clicked notification opens pane k in (ConchKit's
    /// windowFor).
    func store(forPane k: String) -> Store? {
        let all = showing.isEmpty ? Array(stores.prefix(1)) : showing
        let snap = all.first?.snapshots[unkey(k).machine] ?? Snapshot()
        let places = all.map { WindowPlace(id: $0.windowID, workspace: $0.ws, layout: $0.layout) }
        let id = windowFor(pane: k, snapshot: snap, in: places, key: keyStore?.windowID)
        return all.first { $0.windowID == id }
    }

    func bringForward(_ s: Store) { onBringForward(s) }

    /// Opens a window (AppDelegate's): on a workspace by its ui.json id,
    /// or showing one project alone.
    var onOpenWindow: (_ workspace: String?, _ project: String?) -> Void = { _, _ in }

    /// A project's row → Open in New Window. A window already showing that
    /// project alone comes forward instead of a second one opening.
    func openWindow(project pk: String, from s: Store) {
        if let w = showing.first(where: { $0.projectScope == pk }) {
            bringForward(w)
            return
        }
        onOpenWindow(s.homeWs?.id, pk)
    }

    /// A workspace's menu → Open in New Window.
    func openWindow(workspace id: String) { onOpenWindow(id, nil) }
}

extension Store {
    /// The panes put in this window since old, taken out of any other
    /// window that showed them: a pane is in one place.
    func claimPanes(since old: Layout) {
        let was = Set(old.tabs.flatMap(\.root.panes))
        for k in layout.tabs.flatMap(\.root.panes) where !was.contains(k) && isPaneKey(k) {
            AppWindows.shared.holder(of: k, except: self)?.release(k)
        }
    }

    /// Lets go of a pane another window has taken: out of the layout, and
    /// its `conch attach` ended (the pane itself goes on).
    func release(_ k: String) {
        layout.close(k)
        for w in parkedTabs.keys { parkedTabs[w]?.close(k) }
        terminals[k]?.stop()
        dropTerminal(k)
    }

    /// Back on screen after being closed: what another window took in the
    /// meantime is that window's.
    func dropHeldElsewhere() {
        for k in layout.tabs.flatMap(\.root.panes) where AppWindows.shared.holder(of: k, except: self) != nil { release(k) }
    }

    /// This window as saved, to open again when the app does.
    func saved(frame: String?) -> SavedWindow {
        SavedWindow(id: windowID, workspace: homeWs?.id ?? "first", project: projectScope, frame: frame, layout: SavedLayout(layout))
    }

    /// A window closed for good: its connections and the bridges behind
    /// them, its terminals' attaches and its timers end with it.
    func closeWindow() {
        windowClosed = true
        configTimer?.invalidate()
        configTimer = nil
        for (k, t) in terminals {
            t.stop()
            dropTerminal(k)
        }
        for c in conns.values { c.shutdown() }
        conns = [:]
        shutdown()
    }

    /// The window's title, for the Window menu and Mission Control: the
    /// project it shows, or its workspace.
    var windowTitle: String {
        if let p = projectScope { return "conch — " + scopeName(p) }
        guard let w = homeWs, workspaces.count > 1 else { return "conch" }
        return "conch — " + Workspace.label(workspace, w)
    }
}
