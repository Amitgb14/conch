import Foundation
import Testing
@testable import ConchKit

// The welcome names keys; each must be the one its command really has, or
// the tour goes on teaching a key that moved.
@Test func welcomeKeysAreTheMenus() {
    let commands = Dictionary(uniqueKeysWithValues: paletteCommands.map { ($0.id, $0.keys) })
    #expect(!welcomeCards.isEmpty)
    for card in welcomeCards {
        #expect(!card.title.isEmpty && !card.body.isEmpty && !card.symbol.isEmpty, "\(card.title)")
        for k in card.keys {
            guard let id = k.command else { continue }
            #expect(commands[id] != nil, "\(card.title): no command \(id)")
            #expect(commands[id] == k.keys, "\(card.title): \(id) is \(commands[id] ?? "none"), the card says \(k.keys)")
        }
        // A card's keys are told apart by their keys.
        #expect(Set(card.keys.map(\.keys)).count == card.keys.count, "\(card.title)")
    }
    // The first card says what conch is; the last points to the rest.
    #expect(welcomeCards.first?.title == "Welcome to Conch")
    #expect(welcomeCards.last?.keys.contains { $0.keys == "⌘/" } == true)
}

// Shown on the first launch, and until seen; a newer welcome is shown once
// more.
@Test func welcomeShowsUntilSeen() {
    #expect(shouldShowWelcome(seen: 0))
    #expect(!shouldShowWelcome(seen: welcomeVersion))
    #expect(!shouldShowWelcome(seen: welcomeVersion + 1))
    #expect(shouldShowWelcome(seen: welcomeVersion - 1))
}

// The welcome asks for a look: conch's own theme, or VS Code's Modern
// pair. Each is a theme conch has, and one card, not the first, asks.
@Test func welcomeOffersThreeLooks() {
    #expect(welcomeLooks.map(\.id) == [.conch, .vscodeLight, .vscodeDark])
    #expect(welcomeLooks.map(\.title) == ["Default", "VS Code Light Modern", "VS Code Dark Modern"])
    for l in welcomeLooks {
        #expect(ConchTheme.named(l.theme).id == l.theme, "\(l.title): no theme \(l.theme)")
        #expect(!l.detail.isEmpty)
    }
    #expect(ConchTheme.named(welcomeLooks[1].theme).light)
    #expect(!ConchTheme.named(welcomeLooks[2].theme).light)
    #expect(welcomeCards.filter(\.asksLook).count == 1)
    #expect(welcomeCards.first?.asksLook == false)
}

// Which look config.toml has now: none set is the default; a theme the
// welcome does not offer is none of them, so nothing is ticked.
@Test func welcomeLookReadsTheConfig() {
    #expect(welcomeLook(in: nil) == .conch)
    #expect(welcomeLook(in: "") == .conch)
    #expect(welcomeLook(in: "[ui]\ntheme = \"conch\"\n") == .conch)
    #expect(welcomeLook(in: "[ui]\ntheme = \"vscode-light-modern\"\n") == .vscodeLight)
    #expect(welcomeLook(in: "[ui]\ntheme = \"vscode-dark-modern\"\n") == .vscodeDark)
    #expect(welcomeLook(in: "[ui]\ntheme = \"terminal\"\n") == nil)
    // A light_theme of its own is not the welcome's either.
    #expect(welcomeLook(in: "[ui]\ntheme = \"vscode-dark-modern\"\nlight_theme = \"github-light\"\n") == nil)
}

// A look is written into [ui], the rest of the file kept: the theme, and
// no light_theme, which would show another theme while macOS is light.
@Test func welcomeLookWritesTheConfig() {
    #expect(welcomeLookTOML("", .vscodeLight) == "[ui]\ntheme = \"vscode-light-modern\"\n")
    let mine = "# mine\n[ui]\n  theme = \"terminal\"\n  light_theme = \"github-light\"\n  mouse = true\n\n[app]\n  icon_style = \"tiles\"\n"
    let dark = welcomeLookTOML(mine, .vscodeDark)
    #expect(dark.contains("theme = \"vscode-dark-modern\""))
    #expect(!dark.contains("light_theme") && !dark.contains("\"terminal\""))
    #expect(dark.contains("# mine") && dark.contains("mouse = true") && dark.contains("icon_style = \"tiles\""))
    #expect(welcomeLook(in: dark) == .vscodeDark)
    // Back to the default takes the welcome's theme out again.
    let back = welcomeLookTOML(dark, .conch)
    #expect(welcomeLook(in: back) == .conch)
    #expect(uiSetting("theme", in: back) == nil && back.contains("mouse = true"))
    // The default over a theme chosen elsewhere leaves it: Default is
    // what was there, not a reset of somebody's own choice.
    #expect(welcomeLookTOML(mine, .conch) == mine)
    #expect(welcomeLookTOML("", .conch) == "")
    // Choosing what is already there changes nothing.
    let light = welcomeLookTOML("", .vscodeLight)
    #expect(welcomeLookTOML(light, .vscodeLight) == light)
}
