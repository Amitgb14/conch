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
