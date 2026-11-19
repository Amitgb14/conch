import Foundation

/// Many marks, one firing: the first mark schedules `fire` after `delay`,
/// and marks before it runs ride along. The window uses it to tell its
/// views that the servers' panes changed — each working agent sends
/// pane.updated as its title and tokens move, and redrawing the whole
/// window for each one left a click waiting behind the redraws.
@MainActor
public final class Coalescer {
    /// How often the window redraws for the servers' panes: ten times a
    /// second. Once a frame (1/60) was too often: every tree row is drawn
    /// again for each, and with forty busy panes that kept a third of a
    /// core busy with nothing touched, which a click — a workspace
    /// switched — waited behind. What the person does is published at
    /// once; only the panes' own churn waits for this.
    public static let paneRedraw = 0.1

    public private(set) var pending = false
    let delay: Double
    let schedule: (Double, @escaping () -> Void) -> Void
    let fire: () -> Void

    public init(delay: Double,
                schedule: @escaping (Double, @escaping () -> Void) -> Void = { d, f in
                    DispatchQueue.main.asyncAfter(deadline: .now() + d) { MainActor.assumeIsolated { f() } }
                },
                fire: @escaping () -> Void) {
        self.delay = delay
        self.schedule = schedule
        self.fire = fire
    }

    public func mark() {
        guard !pending else { return }
        pending = true
        schedule(delay) { [weak self] in
            guard let self else { return }
            // Cleared first, so a mark made while firing is the next round's.
            self.pending = false
            self.fire()
        }
    }
}

/// Takes key out of a store's published dictionary only when it is
/// there. Changing a @Published value tells every view at once, whether or
/// not anything changed — `removeValue` of a key that is not there
/// included — so the window's pane events, which cleared an agent's
/// question on every update, redrew the whole window for each one past
/// the coalescer: 95% of a core with forty busy panes, and a workspace
/// switch waiting behind it.
@MainActor
public func dropValue<Root: AnyObject, Key: Hashable, Value>(_ key: Key, from path: ReferenceWritableKeyPath<Root, [Key: Value]>, of root: Root) {
    guard root[keyPath: path][key] != nil else { return }
    root[keyPath: path].removeValue(forKey: key)
}
