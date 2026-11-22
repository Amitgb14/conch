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

// The welcome asks for a look on its first card: Light first, as a new
// Conch starts, then Dark and macOS's own. Each is a theme conch has.
@Test func welcomeOffersThreeLooks() {
    #expect(welcomeLooks.map(\.id) == [.vscodeLight, .conch, .vscodeDark])
    #expect(welcomeLooks.map(\.title) == ["Light", "Dark", "Match macOS"])
    for l in welcomeLooks {
        #expect(ConchTheme.named(l.theme).id == l.theme, "\(l.title): no theme \(l.theme)")
        #expect(!l.detail.isEmpty)
    }
    #expect(ConchTheme.named(welcomeLooks[0].theme).light)
    #expect(!ConchTheme.named(welcomeLooks[1].theme).light)
    #expect(welcomeLooks.first?.id == firstLaunchLook)
    #expect(welcomeCards.filter(\.asksLook).count == 1)
    #expect(welcomeCards.first?.asksLook == true)
}

// The tour teaches the three things a new person does first, in order,
// as numbered steps: add a project, start an agent, move a project to
// another machine. Each step is one plain line.
@Test func welcomeTeachesTheFirstThings() {
    let titles = welcomeCards.map(\.title)
    let firsts = ["Add a Project", "Start an Agent", "Move a Project to Another Machine"]
    let at = firsts.compactMap { titles.firstIndex(of: $0) }
    #expect(at.count == 3, "\(titles)")
    #expect(at == at.sorted())
    for t in firsts {
        let card = welcomeCards.first { $0.title == t }!
        #expect((2...4).contains(card.steps.count), "\(t): \(card.steps.count) steps")
        for step in card.steps {
            #expect(!step.isEmpty && step.count <= 140, "\(t): \(step)")
        }
    }
    // What a step tells somebody to choose is what the menus call it.
    let move = welcomeCards.first { $0.title == "Move a Project to Another Machine" }!
    #expect(move.steps.contains { $0.contains("Move to Another Machine…") })
    #expect(welcomeCards.count <= 6)
}

// A new install starts white; anyone with a theme of their own, or an
// app that has been used before, keeps what they have.
@Test func firstLaunchStartsLight() {
    let light = firstLaunchTOML("", newInstall: true)
    #expect(uiSetting("theme", in: light) == "vscode-light-modern")
    #expect(welcomeLook(in: light) == .vscodeLight)
    // Another file's settings are kept.
    let other = "[app]\n  icon_style = \"tiles\"\n"
    let both = firstLaunchTOML(other, newInstall: true)
    #expect(both.contains("icon_style = \"tiles\"") && uiSetting("theme", in: both) == "vscode-light-modern")
    // Used before: nothing changes, theme or not.
    #expect(firstLaunchTOML("", newInstall: false) == "")
    #expect(firstLaunchTOML(other, newInstall: false) == other)
    // A theme or a light_theme chosen anywhere, the TUI included, stays.
    let mine = "[ui]\n  theme = \"terminal\"\n"
    #expect(firstLaunchTOML(mine, newInstall: true) == mine)
    let lightOnly = "[ui]\n  light_theme = \"github-light\"\n"
    #expect(firstLaunchTOML(lightOnly, newInstall: true) == lightOnly)
    // Dark from the welcome undoes it: no theme, conch's own.
    #expect(uiSetting("theme", in: welcomeLookTOML(light, .conch)) == nil)
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
