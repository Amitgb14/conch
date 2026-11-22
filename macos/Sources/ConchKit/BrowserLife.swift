import Foundation

// How long a browser pane lives: while a layout of its window holds it.
// Its pages play sound and run scripts, so one left behind by a close is
// heard and cannot be reached.

/// The browsers to shut down: those made whose pane none of the window's
/// layouts — the one on screen and the other workspaces' — holds any more.
/// A pane moved, hidden or in another workspace is still held.
public func browsersClosed(made: Set<String>, layouts: [Layout]) -> Set<String> {
    made.subtracting(layouts.flatMap { $0.tabs.flatMap(\.root.panes) })
}

/// Whether drawing a pane may make its browser. SwiftUI draws a pane once
/// more as it is closed, and making one then loaded the page again in a
/// browser nobody could see or close.
public func mayMakeBrowser(_ key: String, in layout: Layout, windowClosed: Bool) -> Bool {
    !windowClosed && layout.tab(showing: key) != nil
}
