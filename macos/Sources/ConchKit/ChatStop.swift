import Foundation

/// What a chat's Stop has to send next.
public enum ChatStopAction: Equatable, Sendable {
    case none
    /// The agent's own interrupt key (agent.interrupt, or escape on a
    /// server from before it).
    case interrupt
    /// ctrl+c: Stop harder.
    case hard
}

/// A chat's Stop, from the press until the agent says it has stopped:
/// "Stopping…" at once, escape again if it still works after two seconds,
/// then Stop harder (ctrl+c) offered rather than waiting for ever. The
/// chat looks at the agent every moment meanwhile (poll), and each look
/// is a check: anything that says it stopped — its state leaving working,
/// or the conversation's "Interrupted" — ends it. Times are given, not
/// read, so the steps can be tested without waiting for them.
public struct ChatStop: Equatable, Sendable {
    public enum Phase: Equatable, Sendable {
        case idle
        /// The interrupt sent; waiting for the agent to say it stopped.
        case stopping
        /// Still working after retryAfter: the interrupt sent again.
        case again
        /// Still working after that: Stop harder offered.
        case harder
        /// ctrl+c sent.
        case hardSent
        /// Still working after ctrl+c too: said, and Stop offered again.
        case stuck
    }

    public private(set) var phase: Phase = .idle
    public private(set) var since: Date?
    /// The keys the last interrupt sent, as the hint says them.
    public var sent = "escape"

    public static let retryAfter: TimeInterval = 2
    public static let harderAfter: TimeInterval = 3
    public static let stuckAfter: TimeInterval = 4
    /// How often the chat looks while stopping: the 1 s poll is too slow
    /// to say at once that it took.
    public static let poll: TimeInterval = 0.2

    public init() {}

    /// Whether Stop is under way: the button reads Stopping… or Stop harder.
    public var active: Bool { phase != .idle && phase != .stuck }

    /// Stop pressed, by the button or escape.
    public mutating func press(at now: Date) -> ChatStopAction {
        switch phase {
        case .idle, .stuck:
            phase = .stopping
            since = now
            return .interrupt
        case .harder:
            phase = .hardSent
            since = now
            return .hard
        case .stopping, .again, .hardSent:
            return .none // on its way; pressing again would only queue keys
        }
    }

    /// A look at the agent. stopped is anything saying it has: its state
    /// no longer working, the conversation's "Interrupted".
    public mutating func check(at now: Date, stopped: Bool) -> ChatStopAction {
        guard phase != .idle else { return .none }
        if stopped {
            phase = .idle
            since = nil
            return .none
        }
        let t = now.timeIntervalSince(since ?? now)
        switch phase {
        case .stopping where t >= Self.retryAfter:
            phase = .again
            return .interrupt
        case .again where t >= Self.harderAfter:
            phase = .harder
        case .hardSent where t >= Self.stuckAfter:
            phase = .stuck
        default:
            break
        }
        return .none
    }

    /// The agent has gone (its pane ended): nothing left to stop.
    public mutating func reset() {
        phase = .idle
        since = nil
    }

    /// What the Stop button says.
    public var label: String {
        switch phase {
        case .idle, .stuck: return "Stop"
        case .stopping, .again, .hardSent: return "Stopping…"
        case .harder: return "Stop harder"
        }
    }

    /// Whether the button does anything now.
    public var pressable: Bool { phase == .idle || phase == .harder || phase == .stuck }

    /// What the line beside the button says, or nil for the usual hint.
    public func hint(agent: String) -> String? {
        switch phase {
        case .idle: return nil
        case .stopping: return "Stopping \(agent) — \(sent) sent, waiting for it to say so"
        case .again: return "Still working — \(sent) sent again"
        case .harder: return "Still working after \(sent) twice · Stop harder sends ctrl+c, which some agents need"
        case .hardSent: return "Stopping \(agent) — ctrl+c sent, waiting for it to say so"
        case .stuck: return "\(agent) is still working after escape and ctrl+c · its terminal may say why"
        }
    }
}

/// The keys an interrupt sent, as a hint says them: "escape", "escape
/// twice", "ctrl+c".
public func stopKeysText(_ keys: [String]) -> String {
    guard let first = keys.first else { return "escape" }
    if keys.allSatisfy({ $0 == first }) {
        let name = first == "esc" ? "escape" : first
        switch keys.count {
        case 1: return name
        case 2: return "\(name) twice"
        default: return "\(name) ×\(keys.count)"
        }
    }
    return keys.joined(separator: ", ")
}
