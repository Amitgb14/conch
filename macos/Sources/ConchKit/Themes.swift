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
        // conch's teal on a terminal's ground, after herdr: the Terminal style's.
        .init("terminal", "Terminal", accent: "#2AA5B5", selFG: "#FFFFFF", border: "#1E232A", muted: "#8B949E", ok: "#9ECE6A", warn: "#E0AF68", err: "#F7768E", work: "#7AA2F7", selDim: "#171B20", bg: "#0C0E11", fg: "#D4D9DF"),
        .init("terminal-light", "Terminal Light", accent: "#0F7B8A", selFG: "#FFFFFF", border: "#DADDE2", muted: "#6E7781", ok: "#1A7F37", warn: "#9A6700", err: "#CF222E", work: "#0969DA", selDim: "#E9EBEE", bg: "#FBFBFC", fg: "#1F2328", light: true),
        // VS Code's own, from extensions/theme-defaults: Dark/Light Modern, Dark+/Light+.
        .init("vscode-dark-modern", "VS Code Dark Modern", accent: "#0078D4", selFG: "#FFFFFF", border: "#2B2B2B", muted: "#9D9D9D", ok: "#89D185", warn: "#CCA700", err: "#F14C4C", work: "#3794FF", selDim: "#37373D", bg: "#1F1F1F", fg: "#CCCCCC"),
        .init("vscode-light-modern", "VS Code Light Modern", accent: "#005FB8", selFG: "#FFFFFF", border: "#E5E5E5", muted: "#616161", ok: "#388A34", warn: "#BF8803", err: "#E51400", work: "#1A85FF", selDim: "#E8E8E8", bg: "#FFFFFF", fg: "#3B3B3B", light: true),
        .init("vscode-dark-plus", "VS Code Dark+", accent: "#007ACC", selFG: "#FFFFFF", border: "#3C3C3C", muted: "#8F8F8F", ok: "#89D185", warn: "#CCA700", err: "#F14C4C", work: "#3794FF", selDim: "#37373D", bg: "#1E1E1E", fg: "#D4D4D4"),
        .init("vscode-light-plus", "VS Code Light+", accent: "#007ACC", selFG: "#FFFFFF", border: "#E5E5E5", muted: "#717171", ok: "#388A34", warn: "#BF8803", err: "#E51400", work: "#1A85FF", selDim: "#E4E6F1", bg: "#FFFFFF", fg: "#000000", light: true),
    ]

    /// Themes drawn as a pair, dark and light: the dark one, while it is the
    /// theme, is shown as its light one under macOS's light appearance. The
    /// light one chosen by name stays light — somebody picked it.
    public static let lightPairs: [String: String] = [
        "terminal": "terminal-light",
        // As VS Code follows the system between its dark and light pair.
        "vscode-dark-modern": "vscode-light-modern",
        "vscode-dark-plus": "vscode-light-plus",
    ]

    /// The theme to show for t under an appearance.
    public static func forAppearance(_ t: ConchTheme, dark: Bool) -> ConchTheme {
        guard !dark, let light = lightPairs[t.id] else { return t }
        return named(light)
    }

    public static var conch: ConchTheme { all[0] }

    /// The theme by name; an unknown one is conch's, as the TUI does.
    public static func named(_ id: String?) -> ConchTheme {
        let key = (id ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        return everything.first { $0.id == key } ?? conch
    }

    /// Themes of your own, read from conch's themes folder (userThemes):
    /// after the built-in ones in every list, as the TUI has them.
    nonisolated(unsafe) public static var user: [ConchTheme] = []
    public static var everything: [ConchTheme] { all + user }

    /// The accents [ui] accent may name, as the TUI's (internal/tui/theme.go
    /// accentColors): each dark enough for white text on it.
    public static let accentNames: [String: String] = [
        "teal": "#0F7B8A", "blue": "#2F6FDB", "green": "#2E7D4F", "orange": "#C2571A",
        "pink": "#C2407D", "red": "#C0392B", "gray": "#5A6472", "purple": "#7D56F4",
    ]

    /// This theme with [ui] accent over its own — a name or #rrggbb, as
    /// the TUI takes it; anything else, or nothing, leaves it be. The TUI
    /// draws selections in it with white text, and so does this.
    public func withAccent(_ setting: String?) -> ConchTheme {
        let v = (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        let hex = Self.accentNames[v] ?? (v.count == 7 && v.hasPrefix("#") && UInt32(v.dropFirst(), radix: 16) != nil ? v : nil)
        guard let hex else { return self }
        var t = self
        t.accent = RGB(hex)
        t.uiAccent = nil
        t.selFG = RGB("#FFFFFF")
        return t
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
public func uiSetting(_ key: String, in toml: String?) -> String? { tomlSetting("ui", key, in: toml) }

/// A simple value of config.toml's [section]: a string, number or bool as
/// text, or nil when it is not set there.
public func tomlSetting(_ section: String, _ key: String, in toml: String?) -> String? {
    guard let toml else { return nil }
    var inUI = false
    for raw in toml.split(whereSeparator: \.isNewline) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            inUI = line.hasPrefix("[\(section)]")
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

/// config.toml with one setting changed: key in [section] set to value as
/// written (a number, or a quoted string), or with value nil taken out.
/// Every other line is kept as it is, comments and order with it; a
/// missing section is added at the end.
public func settingTOML(_ toml: String, section: String, key: String, value: String?) -> String {
    var lines = toml.components(separatedBy: "\n")
    if lines.last == "" { lines.removeLast() }
    let header = lines.firstIndex { $0.trimmingCharacters(in: .whitespaces).hasPrefix("[\(section)]") }
    guard let start = header else {
        guard let value else { return toml }
        var out = lines
        if let last = out.last, !last.trimmingCharacters(in: .whitespaces).isEmpty { out.append("") }
        out += ["[\(section)]", "\(key) = \(value)"]
        return out.joined(separator: "\n") + "\n"
    }
    var end = lines.count
    for i in (start + 1)..<lines.count where lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("[") { end = i; break }
    if let at = (start + 1..<end).first(where: { i in
        let l = lines[i].trimmingCharacters(in: .whitespaces)
        guard let eq = l.firstIndex(of: "="), !l.hasPrefix("#") else { return false }
        return l[l.startIndex..<eq].trimmingCharacters(in: .whitespaces) == key
    }) {
        if let value { lines[at] = "\(key) = \(value)" } else { lines.remove(at: at) }
    } else if let value {
        // After the section's last setting, not after a blank line before the next.
        var at = end
        while at > start + 1, lines[at - 1].trimmingCharacters(in: .whitespaces).isEmpty { at -= 1 }
        lines.insert("\(key) = \(value)", at: at)
    }
    return lines.joined(separator: "\n") + "\n"
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

// MARK: themes of your own

/// What a theme file may set, as the TUI reads it (internal/tui/userthemes.go).
private let themeColourKeys = ["bg", "fg", "accent", "sel_fg", "border", "muted", "ok", "warn", "err", "work", "sel_dim", "merged"]

/// The themes in dir (NAME.toml each), and what was wrong with the ones
/// left out — the same files and rules as the TUI's, so a theme is one
/// thing in both: based_on's colours (conch's by default) with the file's
/// over them, its name the file's.
public func userThemes(in dir: String) -> (themes: [ConchTheme], problems: [String]) {
    let names = ((try? FileManager.default.contentsOfDirectory(atPath: dir)) ?? []).filter { $0.hasSuffix(".toml") }.sorted()
    var out: [ConchTheme] = []
    var problems: [String] = []
    for file in names {
        let id = String(file.dropLast(5)).lowercased()
        if id.isEmpty || id.range(of: #"^[a-z0-9][a-z0-9_-]*$"#, options: .regularExpression) == nil {
            problems.append("\(file): a theme's file name is its name, in lower case letters, digits, - and _")
            continue
        }
        if ConchTheme.all.contains(where: { $0.id == id }) {
            problems.append("\(file): \"\(id)\" is a built-in theme's name; call the file something else")
            continue
        }
        if out.contains(where: { $0.id == id }) { continue }
        if out.count == 64 {
            problems.append("\(file): more than 64 themes of your own; the rest are left out")
            continue
        }
        guard let text = try? String(contentsOfFile: (dir as NSString).appendingPathComponent(file), encoding: .utf8) else {
            problems.append("\(file): could not be read")
            continue
        }
        switch userTheme(id: id, text: text) {
        case .success(let t): out.append(t)
        case .failure(let e): problems.append("\(file): \(e.message)")
        }
    }
    return (out, problems)
}

public struct ThemeFileError: Error, Equatable { public var message: String }

/// One theme file's theme: its keys read as the plain TOML they are —
/// key = "value", true or false, # comments.
public func userTheme(id: String, text: String) -> Result<ConchTheme, ThemeFileError> {
    var values: [String: String] = [:]
    var light: Bool?
    for (n, raw) in text.components(separatedBy: "\n").enumerated() {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.isEmpty || line.hasPrefix("#") { continue }
        guard let eq = line.firstIndex(of: "=") else { return .failure(.init(message: "line \(n + 1): expected key = \"value\"")) }
        let key = line[..<eq].trimmingCharacters(in: .whitespaces)
        var v = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
        if v.hasPrefix("\"") {
            guard let close = v.dropFirst().firstIndex(of: "\"") else { return .failure(.init(message: "line \(n + 1): a string is not closed")) }
            v = String(v[v.index(after: v.startIndex)..<close])
        } else if let hash = v.firstIndex(of: "#") {
            v = v[..<hash].trimmingCharacters(in: .whitespaces)
        }
        switch key {
        case "name", "based_on": values[key] = v
        case "light":
            guard v == "true" || v == "false" else { return .failure(.init(message: "light = \(v) is not true or false")) }
            light = v == "true"
        case _ where themeColourKeys.contains(key):
            guard v.range(of: #"^#[0-9A-Fa-f]{6}$"#, options: .regularExpression) != nil else {
                return .failure(.init(message: "\(key) = \"\(v)\" is not a colour; write it as \"#rrggbb\""))
            }
            values[key] = v.uppercased()
        default:
            return .failure(.init(message: "\"\(key)\" is not a theme key (bg, fg, accent, sel_fg, border, muted, ok, warn, err, work, sel_dim, merged)"))
        }
    }
    var t = ConchTheme.conch
    if let base = values["based_on"]?.trimmingCharacters(in: .whitespaces).lowercased(), !base.isEmpty {
        guard let b = ConchTheme.all.first(where: { $0.id == base }) else {
            return .failure(.init(message: "based_on \"\(values["based_on"]!)\" is no built-in theme"))
        }
        t = b
    }
    t.id = id
    let name = (values["name"] ?? "").trimmingCharacters(in: .whitespaces)
    t.label = name.isEmpty ? id : name
    t.uiAccent = nil
    if let v = values["bg"] { t.bg = RGB(v) }
    if let v = values["fg"] { t.fg = RGB(v) }
    if let v = values["accent"] { t.accent = RGB(v) }
    if let v = values["sel_fg"] { t.selFG = RGB(v) }
    if let v = values["border"] { t.border = RGB(v) }
    if let v = values["muted"] { t.muted = RGB(v) }
    if let v = values["ok"] { t.ok = RGB(v) }
    if let v = values["warn"] { t.warn = RGB(v) }
    if let v = values["err"] { t.err = RGB(v) }
    if let v = values["work"] { t.work = RGB(v) }
    if let v = values["sel_dim"] { t.selDim = RGB(v) }
    // A light window when it says so, else when its ground is light.
    t.light = light ?? (t.bg.luminance > 0.5)
    return .success(t)
}
