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
