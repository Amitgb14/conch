import Foundation

// How Settings draws each section's icon, and the window's sidebar its
// rows': the set ([app] icons — Lucide's
// outlines, as the rest of the window, or the Mac's own SF Symbols) and
// the style ([app] icon_style — plain, or a white glyph on a square of
// the section's own colour, as System Settings does). The schema names a
// section's icon by Lucide's name, so the other set is a lookup from it.

public enum SettingsIconSet: String, Sendable, CaseIterable {
    case lucide = ""
    case sf
    /// SF Symbols' filled ones, where the Mac has one.
    case sfFill = "sf-fill"

    public var name: String {
        switch self {
        case .lucide: return "Lucide"
        case .sf: return "SF Symbols"
        case .sfFill: return "SF Symbols Fill"
        }
    }

    public var detail: String {
        switch self {
        case .lucide: return "Outline · 2px · ISC"
        case .sf: return "The Mac's own · line"
        case .sfFill: return "The Mac's own · filled"
        }
    }
}

public enum SettingsIconStyle: String, Sendable, CaseIterable {
    case plain = ""
    /// Each section's icon in its own colour.
    case tinted
    case tiles
}

/// The [app] keys Appearance shows as its Icons card, rather than Mac app.
public let SettingsIconKeys = ["icons", "icon_style"]

/// How many settings differ from what was saved on a section's page as
/// the window lays it out: Appearance counts [app]'s icon keys, which it
/// shows, and Mac app does not.
public func settingsChangedShown(_ f: SettingsForm, _ s: SettingsSection) -> Int {
    let saved = SettingsForm(f.payload)
    func changed(_ sec: SettingsSection, _ keep: (SettingsField) -> Bool) -> Int {
        sec.fields.filter { keep($0) && fieldChanged(f, saved, sec, $0) }.count
    }
    switch s.key {
    case "app":
        return changed(s) { !SettingsIconKeys.contains($0.key) }
    case "ui":
        let app = f.payload.schema.first { $0.key == "app" }
        return changed(s) { _ in true } + (app.map { changed($0) { SettingsIconKeys.contains($0.key) } } ?? 0)
    default:
        // A page also counts the sections drawn on it: Git, [verify]'s.
        let guests = f.payload.schema.filter { SettingsNav.mergedInto[$0.id] == s.id }
        return settingsChanged(f, s) + guests.reduce(0) { $0 + settingsChanged(f, $1) }
    }
}

/// What config.toml's [app] says, as the form has it now — saved or not,
/// so a choice shows as it is made. Anything unknown is the default.
public func settingsIcons(_ app: [String: JSONValue]?) -> (set: SettingsIconSet, style: SettingsIconStyle) {
    (SettingsIconSet(rawValue: app?["icons"]?.string ?? "") ?? .lucide,
     SettingsIconStyle(rawValue: app?["icon_style"]?.string ?? "") ?? .plain)
}

/// The SF Symbol for a section's Lucide icon: the nearest the Mac has (it
/// has no robot, so the agents' is a chip). One it does not know is a gear.
public func sfSymbol(lucide name: String) -> String {
    sfSymbols[name] ?? "gearshape"
}

let sfSymbols: [String: String] = [
    "palette": "paintpalette", "keyboard": "keyboard", "bell": "bell", "bot": "cpu",
    "shield-check": "checkmark.shield", "git-merge": "arrow.triangle.merge", "sparkles": "sparkles",
    "terminal": "terminal", "square-terminal": "apple.terminal", "server": "server.rack",
    "refresh-cw": "arrow.clockwise", "smartphone": "iphone", "laptop": "laptopcomputer",
    "box": "shippingbox", "zap": "bolt", "lock": "lock", "book-open": "book",
    // The window's sidebar: machines, a workspace, projects and what is
    // in them.
    "cloud": "cloud", "layers": "square.stack.3d.up", "folder": "folder", "folder-open": "folder",
    "globe": "globe", "git-branch": "arrow.triangle.branch", "history": "clock.arrow.circlepath",
]

/// A section's tile colour, by its Lucide icon: a hue each, close to the
/// ones System Settings gives the same kind of thing. One not listed is
/// grey.
public func settingsTileColor(lucide name: String) -> RGB {
    RGB(tileColors[name] ?? "#8e8e93")
}

/// A tile's gradient, top to bottom: its colour lit a little at the top.
public func settingsTileGradient(lucide name: String) -> (top: RGB, bottom: RGB) {
    let c = settingsTileColor(lucide: name)
    return (c.mix(.white, 0.12), c)
}

let tileColors: [String: String] = [
    "palette": "#e05aa0", "keyboard": "#8e8e93", "bell": "#ff453a", "bot": "#ff9f0a",
    "book-open": "#bf5af2", "shield-check": "#30d158", "sparkles": "#5e5ce6", "git-merge": "#f05033",
    "terminal": "#636366", "square-terminal": "#64748b", "laptop": "#0a84ff", "server": "#0a84ff",
    "smartphone": "#32ade6", "box": "#ac8e68", "refresh-cw": "#30b0a6", "zap": "#e6b800", "lock": "#8e8e93",
    "cloud": "#64d2ff", "layers": "#5e5ce6", "folder": "#3a8ee6", "folder-open": "#3a8ee6",
    "globe": "#32ade6", "git-branch": "#f05033", "history": "#a2845e",
]
