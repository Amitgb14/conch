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
            if !card.steps.isEmpty {
                VStack(alignment: .leading, spacing: 9) {
                    ForEach(Array(card.steps.enumerated()), id: \.offset) { i, step in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text("\(i + 1)")
                                .font(.ui(size: 11.5, weight: .semibold))
                                .foregroundStyle(Theme.accent)
                                .frame(width: 20, height: 20)
                                .background(Circle().fill(Theme.accentDeep.opacity(0.18)))
                            Text(step).font(.ui(size: 13)).foregroundStyle(Theme.text)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            if card.asksLook {
                HStack(spacing: 10) {
                    ForEach(welcomeLooks, id: \.id) { l in LookTile(look: l, on: store.lookChosen == l.id) { store.chooseLook(l.id) } }
                }
            }
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
        .frame(width: 600, height: 460)
        .onAppear { store.readLook() }
        .onDisappear { store.welcomeSeen() }
    }

    private func finish() {
        store.welcomeSeen()
        dismiss()
    }
}

/// One look the welcome offers: a small window in that theme's colours
/// (its sidebar, a row in its accent, a line of its text), its name and
/// a line on it. Ticked while config.toml has it; a click chooses it.
private struct LookTile: View {
    let look: WelcomeLook
    let on: Bool
    let pick: () -> Void

    var body: some View {
        let t = ConchTheme.named(look.theme)
        Button(action: pick) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 0) {
                    VStack(alignment: .leading, spacing: 5) {
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.muted).opacity(0.6)).frame(width: 26, height: 4)
                        RoundedRectangle(cornerRadius: 3).fill(Theme.color(t.accent).opacity(0.75)).frame(height: 9)
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.fg).opacity(0.5)).frame(width: 30, height: 4)
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.fg).opacity(0.5)).frame(width: 22, height: 4)
                    }
                    .padding(7).frame(width: 52, alignment: .topLeading).frame(maxHeight: .infinity, alignment: .top)
                    .background(Theme.color(t.sidebar))
                    VStack(alignment: .leading, spacing: 5) {
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.line)).frame(width: 34, height: 3)
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.fg).opacity(0.7)).frame(width: 60, height: 4)
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.ok)).frame(width: 40, height: 4)
                        RoundedRectangle(cornerRadius: 2).fill(Theme.color(t.fg).opacity(0.45)).frame(width: 50, height: 4)
                    }
                    .padding(7).frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    .background(Theme.color(t.bg))
                }
                .frame(height: 70)
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.color(t.border)))
                HStack(spacing: 5) {
                    Image(systemName: on ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 12)).foregroundStyle(on ? Theme.accent : Theme.muted)
                    Text(look.title).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                }
                Text(look.detail).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(9)
            .frame(maxWidth: .infinity, alignment: .topLeading)
            .background(RoundedRectangle(cornerRadius: 9).fill(on ? Theme.accentDeep.opacity(0.12) : Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(on ? Theme.accent : Theme.line, lineWidth: on ? 1.5 : 1))
            .contentShape(RoundedRectangle(cornerRadius: 9))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(look.title)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}
