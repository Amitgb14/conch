import Foundation

/// How agents are marked, as conch's `[ui] icons` says for the TUI
/// (iconMode in internal/tui/fileicons.go): Nerd Font glyphs, coloured
/// dots (the default, and the mock's), or nothing.
public enum IconMode: String, Equatable, Sendable {
    case nerd, text, off

    /// The mode a setting names; anything else is text, as the TUI reads it.
    public init(setting: String?) {
        self = IconMode(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .text
    }
}

/// An agent's mark under icons = "nerd": the TUI's own glyph (agentGlyphs
/// in internal/tui/view.go), so Claude is its asterisk in both. symbol is
/// an SF Symbol of the same shape, for a Mac with no Nerd Font to draw the
/// glyph — a glyph a font has not got is worse than none.
public struct AgentMark: Equatable, Sendable {
    /// The Nerd Font glyph (Font Awesome 4.7's range, or a plain character).
    public var glyph: String
    public var symbol: String?

    public static func of(_ name: String?) -> AgentMark {
        switch name {
        case "claude": return .init(glyph: "\u{f069}", symbol: "asterisk")
        case "codex": return .init(glyph: "\u{f2dc}", symbol: "snowflake")
        case "gemini": return .init(glyph: "\u{f1a0}", symbol: "sparkle")
        case "opencode": return .init(glyph: "\u{f120}", symbol: "terminal.fill")
        case "devin": return .init(glyph: "\u{f135}", symbol: "paperplane.fill")
        case "aider": return .init(glyph: "\u{f0c0}", symbol: "person.2.fill")
        case "amp": return .init(glyph: "\u{f0e7}", symbol: "bolt.fill")
        case "cursor": return .init(glyph: "\u{f1b2}", symbol: "cube.fill")
        case "grok": return .init(glyph: "\u{f00d}", symbol: "xmark")
        case "kilo": return .init(glyph: "\u{f009}", symbol: "square.grid.2x2.fill")
        case "pi": return .init(glyph: "π", symbol: nil)
        // An agent from somebody's own manifest: the TUI's default, a
        // microchip, so the row still says it is an agent.
        default: return .init(glyph: "\u{f2db}", symbol: "cpu")
        }
    }

    /// Whether the glyph needs a Nerd Font, or is a character any font has.
    public var glyphNeedsNerdFont: Bool {
        glyph.unicodeScalars.allSatisfy { (0xE000...0xF8FF).contains($0.value) }
    }
}

/// An agent's own colour, for its dot in the sidebar, tabs and pane
/// headers: the TUI's agentColors (internal/tui/view.go), the brand's where
/// one is published. nil for an agent conch has none for.
public func agentColor(_ name: String?) -> RGB? {
    switch name {
    case "claude": return RGB("#D97757") // Anthropic's terracotta
    case "codex": return RGB("#10A37F") // OpenAI's green
    case "gemini": return RGB("#4285F4") // Google's blue
    case "opencode": return RGB("#C792EA")
    case "devin": return RGB("#E3B341")
    case "aider": return RGB("#4CAF50") // chosen: aider publishes none
    case "amp": return RGB("#FF6B35") // chosen
    case "cursor": return RGB("#C9D1D9") // its mark is white
    case "grok": return RGB("#E8E8E8") // xAI is black and white
    case "kilo": return RGB("#FEE685") // Kilo Code's own
    case "pi": return RGB("#4D9ABF") // the blue of its three blocks
    default: return nil
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

/// The TUI's spinner (internal/tui/view.go): a frame every 120ms while an
/// agent works.
public let spinnerFrames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
public let spinnerInterval = 0.12

/// The spinner's frame at a moment, the same in every view drawing it.
public func spinnerFrame(at date: Date) -> String {
    let n = Int((date.timeIntervalSinceReferenceDate / spinnerInterval).rounded(.down))
    return spinnerFrames[((n % spinnerFrames.count) + spinnerFrames.count) % spinnerFrames.count]
}
