import Foundation

// A drag over an agent's pane selects and copies, as in the TUI
// (selectOrClick). An agent asks for the mouse, so a drag went to it, and
// what it copies by itself on a machine with no clipboard is an OSC 52,
// which conch does not let reach the person's clipboard (pane/raw.go). So
// a left press is held until it is either a click — the agent's, sent
// whole on the release — or a drag, the terminal's to select.

/// One thing for the terminal to do with the pointer.
public enum DragStep: Equatable, Sendable {
    /// The event, as it came.
    case pass
    /// A press and a release there, for the program.
    case click(CGPoint)
    /// Begin a selection there, past the program's hold on the mouse: a
    /// new one, never the last one extended. Two clicks are a word, three
    /// a line.
    case select(CGPoint, clicks: Int)
    case extend(CGPoint)
    case end(CGPoint)
    /// What is selected onto the clipboard, if anything is.
    case copy
}

/// One pane's left button, from press to release.
public struct DragSelect: Sendable {
    private enum State {
        case idle
        case held(at: CGPoint, slop: Double)
        case selecting
        /// The program does not hold the mouse: the terminal selects by
        /// itself, and only the copy is added.
        case native(at: CGPoint, slop: Double, selected: Bool)
    }

    /// The least a press moves to be a drag, in points.
    public static let minSlop: Double = 3

    private var state = State.idle

    public init() {}

    /// modified is ⌘, ⇧, ⌥ or ⌃ held: those keep what they meant, so ⌥-drag
    /// is how a drag is still given to the program.
    public mutating func press(at p: CGPoint, clicks: Int, modified: Bool, agent: Bool, captured: Bool,
                               cellWidth: Double) -> [DragStep] {
        state = .idle
        guard agent, !modified else { return [.pass] }
        let slop = max(Self.minSlop, cellWidth / 2)
        guard captured else {
            state = .native(at: p, slop: slop, selected: clicks >= 2)
            return [.pass]
        }
        if clicks >= 2 {
            state = .selecting
            return [.select(p, clicks: clicks)]
        }
        state = .held(at: p, slop: slop)
        return []
    }

    public mutating func drag(to p: CGPoint) -> [DragStep] {
        switch state {
        case .idle:
            return [.pass]
        case let .held(at, slop):
            guard Self.moved(at, p, slop) else { return [] }
            state = .selecting
            // From where it was pressed, not where it was seen to be a drag.
            return [.select(at, clicks: 1), .extend(p)]
        case .selecting:
            return [.extend(p)]
        case let .native(at, slop, selected):
            state = .native(at: at, slop: slop, selected: selected || Self.moved(at, p, slop))
            return [.pass]
        }
    }

    public mutating func release(at p: CGPoint) -> [DragStep] {
        defer { state = .idle }
        switch state {
        case .idle:
            return [.pass]
        case let .held(at, slop):
            // A flick can end before any drag was reported.
            return Self.moved(at, p, slop)
                ? [.select(at, clicks: 1), .extend(p), .end(p), .copy]
                : [.click(at)]
        case .selecting:
            return [.end(p), .copy]
        case let .native(at, slop, selected):
            return selected || Self.moved(at, p, slop) ? [.pass, .copy] : [.pass]
        }
    }

    private static func moved(_ a: CGPoint, _ b: CGPoint, _ slop: Double) -> Bool {
        abs(Double(a.x - b.x)) > slop || abs(Double(a.y - b.y)) > slop
    }
}
