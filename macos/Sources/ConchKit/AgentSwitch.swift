import Foundation

// Switching a running agent's conversation to another agent in the same
// pane (agent.switch.v1, #352): Switch Agent ▸ on the agent's menu, a card
// saying what crosses that counts down before asking, and the pane then
// saying where its conversation came from, with Switch back. The server
// does the switching; never mid-turn, so one asked for while the agent
// works waits for the turn to end (agent.switching) and is made then
// (agent.switched).

/// The server's capability for switching agents.
public let agentSwitchCapability = "agent.switch.v1"

/// How a conversation reaches an agent (AgentAvailability.switchTo, and
/// AgentSwitchResult.crossed).
public enum SwitchWay {
    /// Written as a session of the agent's own, resumed with the history.
    public static let native = "native"
    /// Handed over as a Markdown document, its first message.
    public static let handoff = "handoff"
    /// Not at all: it cannot be given a first message.
    public static let never = "never"
}

/// What a switch carries and leaves behind.
public struct SwitchCrossing: Decodable, Equatable, Sendable {
    public var messages: Int
    public var commands: Int
    public var notes: Int
    public var shortened: Int?
    public var fits: Bool
    public var bytes: Int?
    public var plan: Bool?
    public var left: [String]?
    public init(messages: Int, commands: Int = 0, notes: Int = 0, shortened: Int? = nil, fits: Bool = true, bytes: Int? = nil, plan: Bool? = nil, left: [String]? = nil) {
        self.messages = messages
        self.commands = commands
        self.notes = notes
        self.shortened = shortened
        self.fits = fits
        self.bytes = bytes
        self.plan = plan
        self.left = left
    }
}

/// Where a pane's conversation was switched from (PaneInfo.switchedFrom).
public struct SwitchOrigin: Decodable, Equatable, Sendable {
    public var agent: String
    public var session: String?
    public var at: Date?
    /// What crossed; none from a server before it was kept.
    public var crossing: SwitchCrossing?
    /// The switch went back to where the conversation started: the round
    /// trip is over, and Switch back is not offered.
    public var back: Bool?
    public init(agent: String, session: String? = nil, at: Date? = nil, crossing: SwitchCrossing? = nil, back: Bool? = nil) {
        self.agent = agent
        self.session = session
        self.at = at
        self.crossing = crossing
        self.back = back
    }
}

public struct AgentSwitchParams: Encodable, Sendable {
    public var id: String
    public var to: String
    public var preview: Bool?
    public init(id: String, to: String, preview: Bool? = nil) {
        self.id = id
        self.to = to
        self.preview = preview
    }
}

public struct AgentSwitchCancelParams: Encodable, Sendable {
    public var id: String
    public init(id: String) { self.id = id }
}

/// A switch made, waiting, or previewed — and, as agent.switched, one
/// that failed when its moment came (error set).
public struct AgentSwitchResult: Decodable, Equatable, Sendable {
    public var pane: PaneInfo?
    public var from: String
    public var to: String
    public var crossed: String?
    public var why: String?
    public var crossing: SwitchCrossing?
    public var pending: Bool?
    public var waits: [String]?
    public var preview: Bool?
    public var error: String?
    /// "limit" for a switch the server made itself at a plan limit
    /// (agent.switch.limit.v1).
    public var reason: String?
    public init(pane: PaneInfo? = nil, from: String, to: String, crossed: String? = nil, why: String? = nil, crossing: SwitchCrossing? = nil,
                pending: Bool? = nil, waits: [String]? = nil, preview: Bool? = nil, error: String? = nil, reason: String? = nil) {
        self.reason = reason
        self.pane = pane
        self.from = from
        self.to = to
        self.crossed = crossed
        self.why = why
        self.crossing = crossing
        self.pending = pending
        self.waits = waits
        self.preview = preview
        self.error = error
    }
}

/// A switch waiting for its moment (agent.switching).
public struct AgentSwitching: Decodable, Equatable, Sendable {
    public var pane: String
    public var from: String
    public var to: String
    public var waits: [String]?
    public init(pane: String, from: String, to: String, waits: [String]? = nil) {
        self.pane = pane
        self.from = from
        self.to = to
        self.waits = waits
    }
}

/// The name people know an agent by, for the few places that have only
/// its name.
public func agentTitle(_ name: String, among agents: [AgentAvailability] = []) -> String {
    if let a = agents.first(where: { $0.name == name }) { return a.title }
    let known = ["claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin",
                 "copilot": "Copilot CLI", "cline": "Cline", "qwen": "Qwen Code", "goose": "Goose"]
    return known[name] ?? name
}

/// An agent's Switch Agent ▸: every installed agent but its own, saying
/// how the conversation would get there, one that cannot take it said
/// why; a switch already waiting first, to call off; then when it
/// happens. A server that cannot switch keeps the item, saying so.
public func switchAgentMenu(pane key: String, from: String, agents: [AgentAvailability], canSwitch: Bool, waiting: AgentSwitching? = nil) -> RowMenuItem {
    guard canSwitch else {
        return RowMenuItem("Switch Agent", .disabled, children: [RowMenuItem("The server there needs a reload (conch server reload)", .disabled)])
    }
    var kids: [RowMenuItem] = []
    if let w = waiting {
        kids.append(RowMenuItem("Call Off the Switch to \(agentTitle(w.to, among: agents))", .cancelSwitch(pane: key)))
        kids.append(.divider)
    }
    for a in agents where a.installed && a.name != from {
        switch a.switchTo {
        case SwitchWay.never?:
            kids.append(RowMenuItem("\(a.title)  ·  can't take a first message", .disabled))
        case SwitchWay.native?:
            kids.append(RowMenuItem("\(a.title)  ·  Full history", .switchAgent(pane: key, to: a.name)))
        default:
            kids.append(RowMenuItem("\(a.title)  ·  Handoff document", .switchAgent(pane: key, to: a.name)))
        }
    }
    if !kids.contains(where: { if case .switchAgent = $0.action { return true }; return false }) {
        kids.append(RowMenuItem("No Other Agent Installed", .disabled))
    }
    kids.append(.divider)
    kids.append(RowMenuItem("Switches when this turn ends.", .disabled))
    kids.append(RowMenuItem("\(agentTitle(from, among: agents))'s own session is kept, so you can switch back.", .disabled))
    return RowMenuItem("Switch Agent", .divider, children: kids)
}

/// "1 message", "42 messages".
public func counted(_ n: Int, _ what: String) -> String { n == 1 ? "1 \(what)" : "\(n) \(what)s" }

/// A size as a person reads one.
public func readableBytes(_ n: Int) -> String {
    if n >= 1 << 20 { return String(format: "%.1f MB", Double(n) / Double(1 << 20)) }
    if n >= 1 << 10 { return "\(n >> 10) KB" }
    return "\(n) bytes"
}

/// The card's lines, label and value: what a preview says would cross.
public func switchCardLines(_ r: AgentSwitchResult, agents: [AgentAvailability] = []) -> [(label: String, value: String)] {
    let from = agentTitle(r.from, among: agents), to = agentTitle(r.to, among: agents)
    let cr = r.crossing ?? SwitchCrossing(messages: 0)
    var carries = [counted(cr.messages, "message"), counted(cr.commands, "command") + " with their output", counted(cr.notes, "note") + " for reads and edits"]
    if cr.plan == true { carries.append("the plan") }
    var out: [(label: String, value: String)] = [("Carries", carries.joined(separator: " · "))]
    let shortened = cr.shortened ?? 0
    if !cr.fits || shortened > 0 {
        out.append(("Fits", "\(readableBytes(cr.bytes ?? 0)), \(counted(shortened, "step")) shortened to fit \(to)'s context"))
    } else if let b = cr.bytes, b > 0 {
        out.append(("Fits", "all \(readableBytes(b)), nothing shortened"))
    } else {
        out.append(("Fits", "all of it, nothing shortened"))
    }
    if let left = cr.left, !left.isEmpty { out.append(("Leaves", left.joined(separator: "; "))) }
    if r.crossed == SwitchWay.handoff {
        out.append(("How", "the handoff document, as \(to)'s first message" + ((r.why ?? "").isEmpty ? "" : ": \(r.why!)")))
    } else {
        out.append(("How", "as \(to)'s own session, resumed with the history"))
    }
    out.append(("Keeps", "\(from)'s session as it is, so Switch back works at any time"))
    return out
}

/// The line over a switched pane: where its conversation came from and
/// what crossed; nil once it is back where it started, or not running.
public func switchBanner(_ p: PaneInfo, agents: [AgentAvailability] = []) -> String? {
    guard let sf = p.switchedFrom, sf.back != true, p.agent != nil, p.running else { return nil }
    var s = "⇄ Continued from \(agentTitle(sf.agent, among: agents))"
    if let cr = sf.crossing {
        s += " · \(counted(cr.messages, "message")) · \(counted(cr.commands, "command")) · \(counted(cr.notes, "note"))"
    }
    return s
}

/// Names one switch of one pane, so putting its banner away leaves the
/// next switch's to show.
public func switchBannerKey(_ key: String, _ p: PaneInfo) -> String {
    key + "|" + (p.switchedFrom?.at.map { String($0.timeIntervalSince1970) } ?? "")
}

/// A switch being decided on the card.
public struct SwitchAsk: Equatable, Sendable {
    public enum Stage: Equatable, Sendable {
        /// Counting down to the moment it asks by itself.
        case counting(until: Date)
        /// Asked; no answer yet.
        case asking
        /// The server holds it until the turn ends.
        case waiting([String])
        case failed(String)
    }
    /// The pane, keyed as the app keys panes.
    public var pane: String
    public var to: String
    public var preview: AgentSwitchResult
    public var stage: Stage
    public init(pane: String, to: String, preview: AgentSwitchResult, stage: Stage) {
        self.pane = pane
        self.to = to
        self.preview = preview
        self.stage = stage
    }

    /// Seconds left of the countdown, never below zero; nil once asked.
    public func secondsLeft(now: Date = Date()) -> Int? {
        guard case .counting(let until) = stage else { return nil }
        return max(Int(until.timeIntervalSince(now).rounded(.up)), 0)
    }

    /// Whether the server holds a switch the card would call off.
    public var held: Bool {
        switch stage {
        case .asking, .waiting: return true
        default: return false
        }
    }
}

/// How long the card counts before it asks.
public let switchCountdown: TimeInterval = 5
