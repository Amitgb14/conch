import Foundation

// The welcome a person sees the first time Conch.app opens: a few cards
// on what conch is and the handful of keys that get them going, and
// Help → Welcome to Conch to see it again. The cards are here, where a
// test can hold their shortcuts to the menus', so the tour cannot go on
// naming a key that has moved.

/// A key a card names: the command it is (one of paletteCommands'
/// identifiers, checked against them), or nil for a key no command
/// holds, such as ⌘K for search.
public struct WelcomeKey: Equatable, Sendable {
    public var keys: String
    public var what: String
    public var command: String?
}

/// One card of the welcome.
public struct WelcomeCard: Equatable, Sendable {
    public var symbol: String // an SF Symbol
    public var title: String
    public var body: String
    /// What to do, in order: drawn numbered, a line each.
    public var steps: [String] = []
    public var keys: [WelcomeKey]
    /// The card that asks which look the window takes (welcomeLooks).
    public var asksLook = false
}

/// The welcome, card by card: what conch is, then the three things a new
/// person does first — add a project, start an agent, move a project to
/// another machine — as steps, then the keys worth knowing. Short on
/// purpose: a step is one thing to do, said plainly.
public let welcomeCards: [WelcomeCard] = [
    WelcomeCard(symbol: "terminal",
                title: "Welcome to Conch",
                body: "Conch runs your coding agents side by side — Claude Code, Codex, Gemini and more. They keep working even when this window is closed. Pick a look to start; you can change it any time in Settings.",
                keys: [], asksLook: true),
    WelcomeCard(symbol: "folder.badge.plus",
                title: "Add a Project",
                body: "A project is a folder with your code in it.",
                steps: ["Press ⌘O, or choose File → Add Project…",
                        "Pick the folder — a git repository works best.",
                        "It appears in the sidebar, with its branches and agents."],
                keys: [WelcomeKey(keys: "⌘O", what: "add a project", command: "addProject")]),
    WelcomeCard(symbol: "sparkles",
                title: "Start an Agent",
                body: "Each agent works on a branch of its own, so several can work at once.",
                steps: ["Select your project in the sidebar.",
                        "Press ⌘N, say what you want done, and pick an agent.",
                        "Watch it work in its tab. When it has a question, it shows under Needs You at the top of the sidebar — answer with one click."],
                keys: [WelcomeKey(keys: "⌘N", what: "new task: a branch and an agent", command: "newTask"),
                       WelcomeKey(keys: "⇧⌘N", what: "an agent right where you are", command: "startAgent")]),
    WelcomeCard(symbol: "server.rack",
                title: "Move a Project to Another Machine",
                body: "Run your agents on a bigger machine, or keep them going with your Mac closed.",
                steps: ["Press ⇧⌘M and add the machine over ssh. Conch installs itself there.",
                        "Right-click your project and choose Move to Another Machine…",
                        "Check what will move — branches, uncommitted work, agents — then click Move."],
                keys: [WelcomeKey(keys: "⇧⌘M", what: "add a machine", command: "addMachine")]),
    WelcomeCard(symbol: "keyboard",
                title: "Good to Know",
                body: "Right-click any row in the sidebar to see what you can do with it.",
                keys: [WelcomeKey(keys: "⌘K", what: "search agents, branches, files and commands", command: nil),
                       WelcomeKey(keys: "⌘.", what: "go to the next agent waiting for you", command: "nextWaiting"),
                       WelcomeKey(keys: "⌘,", what: "settings", command: nil),
                       WelcomeKey(keys: "⌘/", what: "every keyboard shortcut", command: nil)]),
]

/// The welcome's version. A person who has seen this one is not shown it
/// again; a new one, worth showing, raises it. 2: rewritten as steps.
public let welcomeVersion = 2

/// Whether to show the welcome when the app opens, given the version last
/// seen (0 for none).
public func shouldShowWelcome(seen: Int) -> Bool { seen < welcomeVersion }

/// A look the welcome offers: conch's own theme, or VS Code's Modern pair.
public enum WelcomeLookID: String, Sendable, CaseIterable {
    case conch, vscodeLight, vscodeDark
}

public struct WelcomeLook: Equatable, Sendable {
    public var id: WelcomeLookID
    public var title: String
    public var detail: String
    /// The theme it is, by its id in ConchTheme.all.
    public var theme: String
}

public let welcomeLooks: [WelcomeLook] = [
    WelcomeLook(id: .vscodeLight, title: "Light", detail: "White, as VS Code's Light Modern. A new Conch starts here.", theme: "vscode-light-modern"),
    WelcomeLook(id: .conch, title: "Dark", detail: "Conch's own: teal on charcoal.", theme: "conch"),
    // The app shows a theme with a light pair in that pair while macOS is
    // light (ConchTheme.lightPairs), as VS Code follows the system.
    WelcomeLook(id: .vscodeDark, title: "Match macOS", detail: "VS Code's Modern pair: dark or light, as macOS is.", theme: "vscode-dark-modern"),
]

/// The look a new Conch.app starts in: white. Only on a new install —
/// the app has kept nothing of its own yet — and only where config.toml
/// names no theme: somebody's own choice, made in the TUI or anywhere
/// else, is left alone, and nobody who already uses Conch.app wakes up to
/// a new look after an upgrade.
public let firstLaunchLook = WelcomeLookID.vscodeLight

/// config.toml as a first launch leaves it: the first look written on a
/// new install when no theme is set, else unchanged.
public func firstLaunchTOML(_ toml: String, newInstall: Bool) -> String {
    guard newInstall, uiSetting("theme", in: toml) == nil, uiSetting("light_theme", in: toml) == nil else { return toml }
    return welcomeLookTOML(toml, firstLaunchLook)
}

/// The look config.toml has now: no theme is the default; a theme, or a
/// light_theme, the welcome does not offer is none of them.
public func welcomeLook(in toml: String?) -> WelcomeLookID? {
    if uiSetting("light_theme", in: toml) != nil { return nil }
    let theme = uiSetting("theme", in: toml) ?? "conch"
    return welcomeLooks.first { $0.theme == theme }?.id
}

/// config.toml with a look chosen: its theme in [ui] and no light_theme,
/// which would show another theme while macOS is light. Dark (no theme) takes out
/// only a theme the welcome wrote; a theme chosen elsewhere is left alone.
public func welcomeLookTOML(_ toml: String, _ id: WelcomeLookID) -> String {
    let current = welcomeLook(in: toml)
    if current == id { return toml }
    guard id != .conch else {
        guard current != nil else { return toml }
        return settingTOML(toml, section: "ui", key: "theme", value: nil)
    }
    let theme = welcomeLooks.first { $0.id == id }!.theme
    let next = settingTOML(toml, section: "ui", key: "theme", value: "\"\(theme)\"")
    return settingTOML(next, section: "ui", key: "light_theme", value: nil)
}
