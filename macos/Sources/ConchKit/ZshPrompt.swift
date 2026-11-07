import Foundation

// An Oh My Zsh theme's prompt as Settings shows it under the theme's
// name: conch draws it with zsh (/api/zsh-prompt, config.OMZPrompt) and
// hands it over as lines of styled text, colours already #rrggbb.

/// Text in one style; nil colours are the terminal's own.
public struct PromptSpan: Codable, Equatable, Sendable {
    public var text: String
    public var fg: String?
    public var bg: String?
    public var bold: Bool?

    public init(text: String, fg: String? = nil, bg: String? = nil, bold: Bool? = nil) {
        self.text = text
        self.fg = fg
        self.bg = bg
        self.bold = bold
    }
}

/// A theme's prompt: PROMPT's lines, and RPROMPT drawn at the right of the
/// last.
public struct ZshPrompt: Codable, Equatable, Sendable {
    public var theme: String
    public var lines: [[PromptSpan]]
    public var right: [PromptSpan]?

    public init(theme: String, lines: [[PromptSpan]], right: [PromptSpan]? = nil) {
        self.theme = theme
        self.lines = lines
        self.right = right
    }
}

/// What /api/zsh-prompt answers: the prompt, or why there is none.
public struct ZshPromptAnswer: Codable, Equatable, Sendable {
    public var prompt: ZshPrompt?
    public var error: String?

    public init(prompt: ZshPrompt? = nil, error: String? = nil) {
        self.prompt = prompt
        self.error = error
    }
}

/// The terminal a prompt is drawn on: the colours conch drew it for
/// (config.PromptBG, PromptFG).
public let zshPromptBG = RGB("#1d1f21")
public let zshPromptFG = RGB("#c5c8c6")

/// Whether a field's choices are Oh My Zsh themes, each shown with its
/// prompt.
public func settingsShowsPrompts(_ s: SettingsSection, _ f: SettingsField) -> Bool {
    s.key == "shell" && f.key == "omz_theme"
}

/// The query a theme's prompt is asked for with; "" is the theme .zshrc
/// chooses.
public func zshPromptQuery(_ theme: String) -> [URLQueryItem] {
    [URLQueryItem(name: "theme", value: theme.trimmingCharacters(in: .whitespaces))]
}

extension ZshPrompt {
    /// Each line as plain text, for tests and for what VoiceOver reads.
    public var text: [String] { lines.map { $0.map(\.text).joined() } }
}

/// Powerline's glyphs (U+E0A0…U+E0B3, private use: only a patched font
/// draws them) as ordinary characters any font has, for when no font here
/// has them — or agnoster's segments are boxes with a question mark.
public let powerlineFallback: [Character: Character] = [
    "\u{E0A0}": "⎇", "\u{E0A1}": "␤", "\u{E0A2}": "⚿",
    "\u{E0B0}": "▶", "\u{E0B1}": "›", "\u{E0B2}": "◀", "\u{E0B3}": "‹",
]

/// text with powerline's glyphs swapped for ordinary ones.
public func withoutPowerline(_ text: String) -> String {
    String(text.map { powerlineFallback[$0] ?? $0 })
}

/// The font a prompt is drawn in, by family: the first installed that
/// draws powerline's glyphs, a Nerd Font Mono before another Nerd Font
/// before a Powerline-patched one. nil is none: draw the fallback.
public func powerlineFamily(_ families: [String], draws: (String) -> Bool) -> String? {
    let rank: (String) -> Int = { f in
        let l = f.lowercased()
        return (l.contains("nerd font mono") ? 0 : l.contains("nerd font") ? 1 : l.contains("powerline") ? 2 : 9)
    }
    return families.filter { rank($0) < 9 }.sorted { (rank($0), $0) < (rank($1), $1) }.first(where: draws)
}
