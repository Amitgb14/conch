import ConchKit
import SwiftUI

/// The welcome: ConchKit's cards one at a time, the keys each names, and
/// Back, Next and Skip. Seen once it is not shown again when the app
/// opens (Store.welcomeSeen); Help → Welcome to Conch brings it back.
struct WelcomeSheet: View {
    @ObservedObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @Environment(\.conchTheme) private var theme
    @State private var page = 0

    var body: some View {
        let _ = theme
        let cards = welcomeCards
        let card = cards[min(page, cards.count - 1)]
        let last = page >= cards.count - 1
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 12) {
                Image(systemName: card.symbol)
                    .font(.system(size: 22, weight: .medium))
                    .foregroundStyle(Theme.accent)
                    .frame(width: 44, height: 44)
                    .background(RoundedRectangle(cornerRadius: 10).fill(Theme.accentDeep.opacity(0.3)))
                VStack(alignment: .leading, spacing: 2) {
                    Text(card.title).font(.ui(size: 17, weight: .semibold))
                    Text("\(page + 1) of \(cards.count)").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                Spacer()
            }
            Text(card.body)
                .font(.ui(size: 13.5))
                .foregroundStyle(Theme.text)
                .lineSpacing(3)
                .fixedSize(horizontal: false, vertical: true)
            if !card.keys.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(card.keys, id: \.keys) { k in
                        HStack(spacing: 10) {
                            Text(k.keys)
                                .font(.system(size: 12, weight: .semibold, design: .monospaced))
                                .padding(.horizontal, 7).padding(.vertical, 2)
                                .background(RoundedRectangle(cornerRadius: 5).fill(Theme.well))
                                .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line))
                                .frame(minWidth: 56, alignment: .leading)
                            Text(k.what).font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                        }
                    }
                }
                .padding(12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
            }
            Spacer(minLength: 0)
            HStack(spacing: 8) {
                // Where in the tour, as dots; a dot goes to its card.
                HStack(spacing: 6) {
                    ForEach(cards.indices, id: \.self) { i in
                        Circle().fill(i == page ? Theme.accent : Theme.muted.opacity(0.35))
                            .frame(width: 7, height: 7)
                            .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { page = i } }
                    }
                }
                Spacer()
                if !last {
                    Button("Skip") { finish() }.buttonStyle(.plain).foregroundStyle(Theme.text2)
                        .padding(.trailing, 6)
                }
                if page > 0 {
                    Button("Back") { withAnimation(.easeOut(duration: 0.15)) { page -= 1 } }
                        .keyboardShortcut(.leftArrow, modifiers: [])
                }
                Button(last ? "Get Started" : "Next") {
                    if last { finish() } else { withAnimation(.easeOut(duration: 0.15)) { page += 1 } }
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(24)
        .frame(width: 540, height: 400)
        .onDisappear { store.welcomeSeen() }
    }

    private func finish() {
        store.welcomeSeen()
        dismiss()
    }
}
