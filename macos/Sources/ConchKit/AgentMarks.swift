import Foundation

/// How the sidebar marks an agent's row: the TUI's agent marks
/// (agentGlyphs and agentColors in internal/tui/view.go) drawn with SF
/// Symbols, since the app cannot count on a Nerd Font. The shapes follow
/// the TUI's — an asterisk for Claude, six-fold for Codex — and where the
/// TUI uses a logo SF Symbols has not got, the nearest plain shape.
public struct AgentMark: Equatable, Sendable {
    /// An SF Symbol's name, or nil when the mark is text.
    public var symbol: String?
    /// The mark as text, for an agent whose mark is a character (π).
    public var text: String?
    /// The agent's own colour, or nil for the theme's muted one.
    public var color: RGB?

    /// The mark for an agent by name; an agent conch has no mark for gets
    /// the TUI's default, a microchip, so the row still says it is one.
    public static func of(_ name: String?) -> AgentMark {
        switch name {
        case "claude": return .init(symbol: "asterisk", color: RGB("#D97757"))
        case "codex": return .init(symbol: "snowflake", color: RGB("#10A37F"))
        case "gemini": return .init(symbol: "sparkle", color: RGB("#4285F4")) // Gemini's own mark; the TUI's is Google's G
        case "opencode": return .init(symbol: "terminal.fill", color: RGB("#C792EA"))
        case "devin": return .init(symbol: "paperplane.fill", color: RGB("#E3B341")) // the TUI's rocket, which SF Symbols has not got
        case "aider": return .init(symbol: "person.2.fill", color: RGB("#4CAF50"))
        case "amp": return .init(symbol: "bolt.fill", color: RGB("#FF6B35"))
        case "cursor": return .init(symbol: "cube.fill", color: RGB("#C9D1D9"))
        case "grok": return .init(symbol: "xmark", color: RGB("#E8E8E8"))
        case "kilo": return .init(symbol: "square.grid.2x2.fill", color: RGB("#FEE685"))
        case "pi": return .init(text: "π", color: RGB("#4D9ABF"))
        default: return .init(symbol: "cpu")
        }
    }
}
