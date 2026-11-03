import Foundation

/// A colour as #rrggbb, with the arithmetic the window's derived colours
/// need.
public struct RGB: Equatable, Sendable, CustomStringConvertible {
    public var r, g, b: Double // 0…1

    public init(_ hex: String) {
        let s = hex.hasPrefix("#") ? String(hex.dropFirst()) : hex
        let v = UInt32(s, radix: 16) ?? 0
        r = Double((v >> 16) & 0xFF) / 255
        g = Double((v >> 8) & 0xFF) / 255
        b = Double(v & 0xFF) / 255
    }

    public init(r: Double, g: Double, b: Double) {
        self.r = r
        self.g = g
        self.b = b
    }

    /// Moved toward other by f (0 is self, 1 is other).
    public func mix(_ other: RGB, _ f: Double) -> RGB {
        RGB(r: r + (other.r - r) * f, g: g + (other.g - g) * f, b: b + (other.b - b) * f)
    }

    public var hex: String {
        let c = { (x: Double) in String(format: "%02X", Int((min(max(x, 0), 1) * 255).rounded())) }
        return "#" + c(r) + c(g) + c(b)
    }

    public var description: String { hex }

    /// Relative brightness, 0 black to 1 white.
    public var luminance: Double { 0.2126 * r + 0.7152 * g + 0.0722 * b }

    static let white = RGB("#FFFFFF")
    static let black = RGB("#000000")
}

/// One of conch's themes: the TUI's palette (internal/tui/theme.go), and
/// the colours the window derives from it.
public struct ConchTheme: Equatable, Sendable, Identifiable {
    public var id: String
    public var label: String
    public var bg, fg, accent, selFG, border, muted, ok, warn, err, work, selDim: RGB
    /// A lighter accent for lines and the focused border where the theme's
    /// own is too dark to read as a line on its background (conch's teal).
    public var uiAccent: RGB?
    public var light: Bool

    /// The accent drawn as a line: the focused border, the tab underline.
    public var line: RGB { uiAccent ?? accent }

    // The window's surfaces, from the background: a dark window is lifted
    // toward white (the native title bar is ~5% lighter, measured), a light
    // one taken toward black.
    var toward: RGB { light ? .black : .white }
    public var chrome: RGB { bg.mix(toward, light ? 0.04 : 0.05) }
    public var sidebar: RGB { bg.mix(toward, light ? 0.02 : 0.025) }
    public var raised: RGB { bg.mix(toward, light ? 0.06 : 0.08) }
    public var paneHeader: RGB { bg.mix(toward, light ? 0.025 : 0.03) }
    public var well: RGB { light ? .white : bg.mix(.black, 0.25) }
    public var fg2: RGB { fg.mix(bg, 0.3) }
    public var border2: RGB { border.mix(fg, 0.15) }

    /// Text on a warn-coloured pill or button: whichever of the theme's
    /// background or white reads on it.
    public var onWarn: RGB { warn.luminance > 0.5 ? .black.mix(bg, 0.2) : .white }
}

extension ConchTheme {
    init(_ id: String, _ label: String, accent: String, selFG: String, border: String, muted: String, ok: String,
         warn: String, err: String, work: String, selDim: String, bg: String, fg: String, ui: String? = nil, light: Bool = false) {
        self.init(id: id, label: label, bg: RGB(bg), fg: RGB(fg), accent: RGB(accent), selFG: RGB(selFG), border: RGB(border),
                  muted: RGB(muted), ok: RGB(ok), warn: RGB(warn), err: RGB(err), work: RGB(work), selDim: RGB(selDim),
                  uiAccent: ui.map(RGB.init), light: light)
    }

    /// conch's themes: the TUI's, in its order, the light ones last. Tests on
    /// both sides (themesMatchTheTUI, TestAppThemesMatchTheTUI) say they agree.
    public static let all: [ConchTheme] = [
        .init("conch", "Conch", accent: "#0F7B8A", selFG: "#FFFFFF", border: "#444C56", muted: "#8B949E", ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#58A6FF", selDim: "#2D333B", bg: "#15191E", fg: "#C9D1D9", ui: "#2AA5B5"),
        .init("dracula", "Dracula", accent: "#BD93F9", selFG: "#282A36", border: "#6272A4", muted: "#6272A4", ok: "#50FA7B", warn: "#F1FA8C", err: "#FF5555", work: "#8BE9FD", selDim: "#44475A", bg: "#282A36", fg: "#F8F8F2"),
        .init("catppuccin", "Catppuccin Mocha", accent: "#89B4FA", selFG: "#1E1E2E", border: "#585B70", muted: "#7F849C", ok: "#A6E3A1", warn: "#F9E2AF", err: "#F38BA8", work: "#89DCEB", selDim: "#313244", bg: "#1E1E2E", fg: "#CDD6F4"),
        .init("nord", "Nord", accent: "#88C0D0", selFG: "#2E3440", border: "#4C566A", muted: "#7B88A1", ok: "#A3BE8C", warn: "#EBCB8B", err: "#BF616A", work: "#81A1C1", selDim: "#3B4252", bg: "#2E3440", fg: "#D8DEE9"),
        .init("gruvbox", "Gruvbox Dark", accent: "#FE8019", selFG: "#282828", border: "#504945", muted: "#928374", ok: "#B8BB26", warn: "#FABD2F", err: "#FB4934", work: "#83A598", selDim: "#3C3836", bg: "#282828", fg: "#EBDBB2"),
        .init("tokyo-night", "Tokyo Night", accent: "#7AA2F7", selFG: "#1A1B26", border: "#3B4261", muted: "#737AA2", ok: "#9ECE6A", warn: "#E0AF68", err: "#F7768E", work: "#7DCFFF", selDim: "#292E42", bg: "#1A1B26", fg: "#C0CAF5"),
        .init("one-dark", "One Dark", accent: "#61AFEF", selFG: "#282C34", border: "#4B5263", muted: "#7F848E", ok: "#98C379", warn: "#E5C07B", err: "#E06C75", work: "#56B6C2", selDim: "#3E4451", bg: "#282C34", fg: "#ABB2BF"),
        .init("solarized", "Solarized Dark", accent: "#268BD2", selFG: "#FDF6E3", border: "#586E75", muted: "#839496", ok: "#859900", warn: "#B58900", err: "#DC322F", work: "#2AA198", selDim: "#073642", bg: "#002B36", fg: "#93A1A1"),
        .init("rose-pine", "Rosé Pine", accent: "#C4A7E7", selFG: "#191724", border: "#524F67", muted: "#6E6A86", ok: "#9CCFD8", warn: "#F6C177", err: "#EB6F92", work: "#31748F", selDim: "#26233A", bg: "#191724", fg: "#E0DEF4"),
        .init("kanagawa", "Kanagawa", accent: "#7E9CD8", selFG: "#1F1F28", border: "#54546D", muted: "#727169", ok: "#98BB6C", warn: "#E6C384", err: "#E82424", work: "#7FB4CA", selDim: "#2D4F67", bg: "#1F1F28", fg: "#DCD7BA"),
        .init("everforest", "Everforest Dark", accent: "#A7C080", selFG: "#2D353B", border: "#475258", muted: "#859289", ok: "#A7C080", warn: "#DBBC7F", err: "#E67E80", work: "#7FBBB3", selDim: "#3D484D", bg: "#2D353B", fg: "#D3C6AA"),
        .init("monokai", "Monokai", accent: "#A6E22E", selFG: "#272822", border: "#49483E", muted: "#75715E", ok: "#A6E22E", warn: "#E6DB74", err: "#F92672", work: "#66D9EF", selDim: "#3E3D32", bg: "#272822", fg: "#F8F8F2"),
        .init("github-dark", "GitHub Dark", accent: "#1F6FEB", selFG: "#FFFFFF", border: "#30363D", muted: "#8B949E", ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#79C0FF", selDim: "#21262D", bg: "#0D1117", fg: "#C9D1D9", ui: "#4493F8"),
        .init("ayu", "Ayu Dark", accent: "#E6B450", selFG: "#0B0E14", border: "#3D424D", muted: "#565B66", ok: "#AAD94C", warn: "#FFB454", err: "#F07178", work: "#59C2FF", selDim: "#1E232B", bg: "#0B0E14", fg: "#BFBDB6"),
        .init("night-owl", "Night Owl", accent: "#82AAFF", selFG: "#011627", border: "#1D3B53", muted: "#637777", ok: "#22DA6E", warn: "#ADDB67", err: "#EF5350", work: "#7FDBCA", selDim: "#1D3B53", bg: "#011627", fg: "#D6DEEB"),
        .init("firefly", "Firefly", accent: "#9CC77A", selFG: "#1D2333", border: "#3A4560", muted: "#8189A0", ok: "#7FD1AE", warn: "#F2D46B", err: "#EF8A73", work: "#8EC5E6", selDim: "#2C3650", bg: "#1D2333", fg: "#E6E1CF"),
        .init("cel", "Cel", accent: "#FF5C8A", selFG: "#1A1C2C", border: "#4A4D78", muted: "#9A9CC4", ok: "#7AE582", warn: "#FFD166", err: "#FF6B6B", work: "#4CC9F0", selDim: "#2E3152", bg: "#1A1C2C", fg: "#F1F0FF"),
        .init("github-light", "GitHub Light", accent: "#0969DA", selFG: "#FFFFFF", border: "#D0D7DE", muted: "#656D76", ok: "#1A7F37", warn: "#9A6700", err: "#CF222E", work: "#0969DA", selDim: "#DDF4FF", bg: "#FFFFFF", fg: "#1F2328", light: true),
        .init("solarized-light", "Solarized Light", accent: "#268BD2", selFG: "#FDF6E3", border: "#D3CBB7", muted: "#93A1A1", ok: "#859900", warn: "#B58900", err: "#DC322F", work: "#2AA198", selDim: "#EEE8D5", bg: "#FDF6E3", fg: "#586E75", light: true),
        .init("catppuccin-latte", "Catppuccin Latte", accent: "#1E66F5", selFG: "#EFF1F5", border: "#BCC0CC", muted: "#8C8FA1", ok: "#40A02B", warn: "#DF8E1D", err: "#D20F39", work: "#04A5E5", selDim: "#CCD0DA", bg: "#EFF1F5", fg: "#4C4F69", light: true),
        .init("ink", "Ink", accent: "#E0353D", selFG: "#FFFFFF", border: "#111111", muted: "#6B6860", ok: "#2F7D32", warn: "#B26A00", err: "#9B1C1C", work: "#1F5FA8", selDim: "#E4E1D8", bg: "#FBFAF6", fg: "#111111", light: true),
        .init("meadow", "Meadow", accent: "#4F8A3C", selFG: "#FFFFFF", border: "#D9CFB4", muted: "#7E8B80", ok: "#2E8B6E", warn: "#C9822A", err: "#C15A3F", work: "#3F84AD", selDim: "#E6EDD8", bg: "#F7F1DF", fg: "#34453A", light: true),
    ]

    public static var conch: ConchTheme { all[0] }

    /// The theme by name; an unknown one is conch's, as the TUI does.
    public static func named(_ id: String?) -> ConchTheme {
        let key = (id ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        return all.first { $0.id == key } ?? conch
    }

    /// The theme the window settles on — the one chosen in the app ("" is
    /// none), else config.toml's — and the one it shows, which is a theme
    /// being previewed while the pointer is on its card in Settings.
    public static func resolve(choice: String, configured: String?, preview: String?) -> (settled: ConchTheme, shown: ConchTheme) {
        let settled = named(choice.isEmpty ? configured : choice)
        return (settled, preview.map { named($0) } ?? settled)
    }

    /// The theme a config.toml sets in [ui] (theme = "…"), or nil when it
    /// sets none.
    public static func configured(in toml: String?) -> String? { uiSetting("theme", in: toml) }

    /// The theme a config.toml sets for the app under an appearance: its
    /// light_theme while macOS is light, when it sets one, else its theme.
    public static func configured(in toml: String?, dark: Bool) -> String? {
        if !dark, let light = uiSetting("light_theme", in: toml) { return light }
        return configured(in: toml)
    }

    /// Ghostty's settings for a pane in this theme.
    ///
    /// Agents draw with colours chosen for a dark terminal — Claude Code's
    /// and Cursor's own themes put white text in true colour, which no
    /// palette reaches — so on a light theme whole lines vanished. Ghostty's
    /// minimum-contrast moves any text too close to the colour behind it
    /// until it reads: 3, the WCAG floor for large text, on a light theme,
    /// and just above 1 on a dark one, to rescue text drawn in its own
    /// background colour without touching anything else.
    public var ghosttyColors: String {
        """
        background = \(bg.hex)
        foreground = \(fg.hex)
        cursor-color = \(line.hex)
        selection-background = \(selDim.hex)
        selection-foreground = \(fg.hex)
        minimum-contrast = \(light ? "3" : "1.1")
        """
    }
}

/// A string a config.toml sets in [ui] (key = "…"), or nil when it sets
/// none. Read by line, not by a TOML library: the app needs a key or two,
/// and a file it cannot parse must still leave it working.
public func uiSetting(_ key: String, in toml: String?) -> String? {
    guard let toml else { return nil }
    var inUI = false
    for raw in toml.split(whereSeparator: \.isNewline) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            inUI = line.hasPrefix("[ui]")
            continue
        }
        guard inUI, line.hasPrefix(key), let eq = line.firstIndex(of: "=") else { continue }
        guard line[line.startIndex..<eq].trimmingCharacters(in: .whitespaces) == key else { continue }
        var value = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
        if let hash = value.firstIndex(of: "#"), !value.hasPrefix("\"") { value = String(value[..<hash]).trimmingCharacters(in: .whitespaces) }
        value = value.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        if let close = value.firstIndex(of: "\"") { value = String(value[..<close]) }
        return value.isEmpty ? nil : value
    }
    return nil
}

/// Whether a theme option is the one config.toml sets: an empty or unknown
/// theme is conch's, as the TUI reads it, so conch's card shows chosen.
///
/// A menu that can be off (light_theme) has "" for Off, which is chosen
/// when nothing is set; a theme in it is chosen as in theme's.
public func themeChosen(_ option: String, _ value: String, off: Bool) -> Bool {
    guard off else { return themeChosen(option, value) }
    if value.trimmingCharacters(in: .whitespaces).isEmpty { return option.isEmpty }
    return !option.isEmpty && themeChosen(option, value)
}

public func themeChosen(_ option: String, _ value: String) -> Bool {
    ConchTheme.named(option).id == ConchTheme.named(value).id && ConchTheme.all.contains { $0.id == option }
        || option == value
}
