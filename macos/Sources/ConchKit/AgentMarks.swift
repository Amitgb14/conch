import Foundation

/// How agents are marked, as conch's `[ui] icons` says for the TUI
/// (iconMode in internal/tui/fileicons.go): Nerd Font glyphs, coloured
/// dots (the default), or nothing.
public enum IconMode: String, Equatable, Sendable {
    case nerd, text, off

    /// The mode a setting names; anything else is text, as the TUI reads it.
    public init(setting: String?) {
        self = IconMode(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .text
    }
}

/// An agent's mark: the TUI's own (agentGlyphs, agentDots and agentColors
/// in internal/tui/view.go), so a row reads the same in both. symbol is an
/// SF Symbol of the same shape, for icons = "nerd" on a Mac with no Nerd
/// Font to draw the glyph — a glyph a font has not got is worse than none.
public struct AgentMark: Equatable, Sendable {
    /// The Nerd Font glyph (Font Awesome 4.7's range, or a plain character).
    public var glyph: String
    /// The coloured emoji the TUI draws without icons = "nerd".
    public var dot: String
    public var symbol: String?
    /// The agent's own colour, for the glyph; nil is the theme's muted one.
    public var color: RGB?

    public static func of(_ name: String?) -> AgentMark {
        switch name {
        case "claude": return .init(glyph: "\u{f069}", dot: "🟠", symbol: "asterisk", color: RGB("#D97757"))
        case "codex": return .init(glyph: "\u{f2dc}", dot: "🟢", symbol: "snowflake", color: RGB("#10A37F"))
        case "gemini": return .init(glyph: "\u{f1a0}", dot: "🔵", symbol: "sparkle", color: RGB("#4285F4"))
        case "opencode": return .init(glyph: "\u{f120}", dot: "🟣", symbol: "terminal.fill", color: RGB("#C792EA"))
        case "devin": return .init(glyph: "\u{f135}", dot: "🟡", symbol: "paperplane.fill", color: RGB("#E3B341"))
        case "aider": return .init(glyph: "\u{f0c0}", dot: "🟩", symbol: "person.2.fill", color: RGB("#4CAF50"))
        case "amp": return .init(glyph: "\u{f0e7}", dot: "🟧", symbol: "bolt.fill", color: RGB("#FF6B35"))
        case "cursor": return .init(glyph: "\u{f1b2}", dot: "🧊", symbol: "cube.fill", color: RGB("#C9D1D9"))
        case "grok": return .init(glyph: "\u{f00d}", dot: "🟦", symbol: "xmark", color: RGB("#E8E8E8"))
        case "kilo": return .init(glyph: "\u{f009}", dot: "🟨", symbol: "square.grid.2x2.fill", color: RGB("#FEE685"))
        case "pi": return .init(glyph: "π", dot: "🟥", symbol: nil, color: RGB("#4D9ABF"))
        // An agent from somebody's own manifest: the TUI's default, a
        // microchip or a robot, so the row still says it is an agent.
        default: return .init(glyph: "\u{f2db}", dot: "🤖", symbol: "cpu", color: nil)
        }
    }

    /// Whether the glyph needs a Nerd Font, or is a character any font has.
    public var glyphNeedsNerdFont: Bool {
        glyph.unicodeScalars.allSatisfy { (0xE000...0xF8FF).contains($0.value) }
    }
}

/// What a pane's row says it is doing, before the agent's mark: the TUI's
/// paneGlyph (internal/tui/view.go) — a spinner while an agent works, !
/// waiting for you, ✓ done, ○ idle or exited, ✗ failed, and tmux's # and ~
/// for a terminal's activity and silence.
public struct StateGlyph: Equatable, Sendable {
    public enum Tone: Equatable, Sendable { case muted, work, warn, ok, err }
    public var glyph: String
    public var tone: Tone
    /// Whether the glyph is the spinner, which the view turns.
    public var spins = false

    /// The TUI's spinner, a frame every spinInterval (120ms).
    public static let spinner = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
    public static let spinInterval = 0.12

    public init(glyph: String, tone: Tone, spins: Bool = false) {
        self.glyph = glyph
        self.tone = tone
        self.spins = spins
    }

    public static func of(_ p: PaneInfo, frame: Int = 0) -> StateGlyph {
        if p.state == "exited" {
            return (p.exitCode ?? 0) == 0 ? .init(glyph: "○", tone: .muted) : .init(glyph: "✗", tone: .err)
        }
        guard let a = p.agent else {
            switch p.alert {
            case "activity": return .init(glyph: "#", tone: .warn)
            case "silence": return .init(glyph: "~", tone: .warn)
            default: return .init(glyph: "›", tone: .muted)
            }
        }
        if a.failed == true && (a.state == .done || a.state == .idle) { return .init(glyph: "✗", tone: .err) }
        switch a.state {
        case .working: return .init(glyph: spinner[((frame % spinner.count) + spinner.count) % spinner.count], tone: .work, spins: true)
        case .blocked: return .init(glyph: "!", tone: .warn)
        case .done: return .init(glyph: "✓", tone: .ok)
        default: return .init(glyph: "○", tone: .muted)
        }
    }
}

extension StateGlyph {
    /// What a pane's row says at its right, as the TUI's tree does: how it
    /// exited, else its branch, else an agent's state — nil for a plain
    /// terminal on no branch.
    public static func detail(_ p: PaneInfo) -> (text: String, tone: Tone)? {
        if p.state == "exited" {
            let code = p.exitCode ?? 0
            return code == 0 ? ("exited", .muted) : ("exit \(code)", .err)
        }
        if let b = p.branch, !b.isEmpty { return (b, .muted) }
        guard let a = p.agent else { return nil }
        let g = of(p)
        if a.failed == true && (a.state == .done || a.state == .idle) { return ("failed", g.tone) }
        switch a.state {
        case .working: return ("working", g.tone)
        case .blocked: return ("waiting", g.tone)
        case .done: return ("done", g.tone)
        default: return ("idle", g.tone)
        }
    }
}

/// What agents have used, summed as the TUI's tree sums it (usage in
/// internal/tui/cost.go): only agents that report usage are counted.
public struct Usage: Equatable, Sendable {
    public var agents = 0, output = 0, input = 0
    public var cost = 0.0

    public init() {}

    public init(_ panes: [PaneInfo]) {
        for p in panes { add(p) }
    }

    public mutating func add(_ p: PaneInfo) {
        guard let t = p.agent?.tokens else { return }
        agents += 1
        output += t.output
        input += t.input + t.cacheRead + t.cacheWrite
        if let c = t.costUSD, c > 0 { cost += c }
    }

    /// The tree row's chip: what it cost when the agent reports one, else
    /// what it wrote, else what it read; "" for nothing.
    public var chip: String {
        if agents == 0 || (output == 0 && input == 0 && cost == 0) { return "" }
        if cost > 0 { return Usage.usd(cost) }
        if output > 0 { return Usage.count(output) + " out" }
        if input > 0 { return Usage.count(input) + " in" }
        return ""
    }

    public static func usd(_ v: Double) -> String { v < 0.01 ? "<$0.01" : String(format: "$%.2f", v) }

    /// A count as the TUI writes one: 950, 9.2k, 30k, 1.2M.
    public static func count(_ n: Int) -> String {
        switch n {
        case 1_000_000...: return String(format: "%.1fM", Double(n) / 1e6)
        case 10_000...: return "\(n / 1000)k"
        case 1000...: return String(format: "%.1fk", Double(n) / 1000)
        default: return "\(n)"
        }
    }
}

/// How a tree row shares its width between the name and what is at its
/// right: both whole when they fit; otherwise the right side keeps what
/// the name leaves, but never less than its share of the room (or its
/// whole width, if smaller), and the name has the rest.
public func rowWidths(name: Double, right: Double, room: Double, share: Double = 0.4) -> (name: Double, right: Double) {
    let room = max(room, 0)
    var r = right
    if name + right > room { r = min(right, max(room * share, room - name)) }
    return (max(room - r, 0), r)
}
