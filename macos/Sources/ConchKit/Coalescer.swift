import Foundation

/// Many marks, one firing: the first mark schedules `fire` after `delay`,
/// and marks before it runs ride along. The window uses it to tell its
/// views that the servers' panes changed — each working agent sends
/// pane.updated as its title and tokens move, and redrawing the whole
/// window for each one left a click waiting behind the redraws.
@MainActor
public final class Coalescer {
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
