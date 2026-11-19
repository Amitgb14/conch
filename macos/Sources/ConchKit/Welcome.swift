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
    public var keys: [WelcomeKey]
    /// The card that asks which look the window takes (welcomeLooks).
    public var asksLook = false
}

/// The welcome, card by card.
public let welcomeCards: [WelcomeCard] = [
    WelcomeCard(symbol: "terminal",
                title: "Welcome to Conch",
                body: "conch runs your coding agents — Claude Code, Codex, Gemini CLI, OpenCode, Devin — in terminals its own server keeps. Close this window and they carry on; open it again, or the TUI, and they are all there.",
                keys: []),
    WelcomeCard(symbol: "paintpalette",
                title: "Pick a Look",
                body: "The window, the TUI and every terminal take it. Settings → Appearance has the rest of conch's themes, and View → Theme tries one on this window alone.",
                keys: [], asksLook: true),
    WelcomeCard(symbol: "sidebar.left",
                title: "Everything in the Sidebar",
                body: "Machines, then their projects, branches, agents and terminals. Click a row to open it; right-click it for what you can do there. An agent's dot says what it is doing: working, waiting for you, or done.",
                keys: [WelcomeKey(keys: "⌘0", what: "go to the sidebar", command: "focusSidebar"),
                       WelcomeKey(keys: "⌘O", what: "add a project", command: "addProject")]),
    WelcomeCard(symbol: "sparkles",
                title: "Start Some Work",
                body: "A task is a branch of its own, a worktree for it, and an agent given your prompt — so several can work at once without stepping on each other. Or start an agent, or a plain terminal, wherever you are.",
                keys: [WelcomeKey(keys: "⌘N", what: "new task", command: "newTask"),
                       WelcomeKey(keys: "⇧⌘N", what: "start an agent here", command: "startAgent"),
                       WelcomeKey(keys: "⌘T", what: "new terminal tab", command: "newTab")]),
    WelcomeCard(symbol: "bell.badge",
                title: "When an Agent Needs You",
                body: "An agent asking a question shows it with its answers as buttons, so you can answer without finding its terminal. Jump from one waiting agent to the next; a branch's changes are in its git tab, hunk by hunk.",
                keys: [WelcomeKey(keys: "⌘.", what: "next agent waiting", command: "nextWaiting"),
                       WelcomeKey(keys: "⌘D", what: "split right", command: "splitRight")]),
    WelcomeCard(symbol: "magnifyingglass",
                title: "Find Anything",
                body: "Search finds agents, terminals, branches, files and commands, waiting agents first, and shows a pane beside the list as it is right now.",
                keys: [WelcomeKey(keys: "⌘K", what: "search", command: nil)]),
    WelcomeCard(symbol: "server.rack",
                title: "More Than This Mac",
                body: "Add a machine over ssh and conch installs itself there; make a sandbox in the cloud; or pair your phone with conch web to answer agents from anywhere. Settings has the rest, and conch → Run in Terminal opens the TUI.",
                keys: [WelcomeKey(keys: "⇧⌘M", what: "add a machine", command: "addMachine"),
                       WelcomeKey(keys: "⌘,", what: "settings", command: nil),
                       WelcomeKey(keys: "⌘/", what: "every keyboard shortcut", command: nil)]),
]

/// The welcome's version. A person who has seen this one is not shown it
/// again; a new one, worth showing, raises it.
public let welcomeVersion = 1

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
    WelcomeLook(id: .conch, title: "Default", detail: "conch's own: dark, teal on charcoal.", theme: "conch"),
    WelcomeLook(id: .vscodeLight, title: "VS Code Light Modern", detail: "Light, as VS Code draws it.", theme: "vscode-light-modern"),
    // The app shows a theme with a light pair in that pair while macOS is
    // light (ConchTheme.lightPairs), as VS Code follows the system.
    WelcomeLook(id: .vscodeDark, title: "VS Code Dark Modern", detail: "Dark; Light Modern while macOS is light, as VS Code does.", theme: "vscode-dark-modern"),
]

/// The look config.toml has now: no theme is the default; a theme, or a
/// light_theme, the welcome does not offer is none of them.
public func welcomeLook(in toml: String?) -> WelcomeLookID? {
    if uiSetting("light_theme", in: toml) != nil { return nil }
    let theme = uiSetting("theme", in: toml) ?? "conch"
    return welcomeLooks.first { $0.theme == theme }?.id
}

/// config.toml with a look chosen: its theme in [ui] and no light_theme,
/// which would show another theme while macOS is light. Default takes out
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
