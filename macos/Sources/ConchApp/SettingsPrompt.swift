import ConchKit
import SwiftUI

// An Oh My Zsh theme's prompt, drawn as a terminal would: under each
// theme's name in the list (compact), and under the field for the theme
// chosen. conch draws it with zsh, so the theme's own code decides what
// it says; this only paints the spans.

struct PromptPreview: View {
    @ObservedObject var model: SettingsModel
    let theme: String
    var compact = true
    @Environment(\.conchTheme) private var conchTheme

    var body: some View {
        let _ = conchTheme
        let answer = model.prompts[theme]
        Group {
            if let p = answer?.prompt {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(Array(p.lines.enumerated()), id: \.offset) { i, line in
                        HStack(spacing: 0) {
                            spans(line).lineLimit(1)
                            if i == p.lines.count - 1, let r = p.right, !r.isEmpty {
                                Spacer(minLength: 16)
                                spans(r).lineLimit(1)
                            }
                        }
                    }
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(p.text.joined(separator: "\n"))
            } else if let e = answer?.error {
                Text(e).foregroundStyle(Color(nsColor: Theme.ns(zshPromptFG)).opacity(0.55)).lineLimit(compact ? 1 : 3)
            } else {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.mini)
                    Text("drawing…").foregroundStyle(Color(nsColor: Theme.ns(zshPromptFG)).opacity(0.55))
                }
            }
        }
        .font(Self.font(size: compact ? 11 : 12))
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 10).padding(.vertical, compact ? 6 : 9)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: Theme.ns(zshPromptBG))))
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
        .onAppear { model.loadPrompt(theme) }
        .onChange(of: theme) { _, t in model.loadPrompt(t) }
    }

    /// A font here that draws powerline's glyphs, looked for once.
    static let family: String? = powerlineFamily(NSFontManager.shared.availableFontFamilies) { name in
        guard let f = NSFont(name: name, size: 12) ?? NSFontManager.shared.font(withFamily: name, traits: [], weight: 5, size: 12) else { return false }
        return f.coveredCharacterSet.contains(Unicode.Scalar(0xE0B0)!)
    }

    static func font(size: CGFloat) -> Font {
        if let family { return .custom(family, size: size) }
        return .system(size: size, design: .monospaced)
    }

    /// A span's text, its powerline glyphs ordinary ones when no font
    /// here draws them.
    func shown(_ text: String) -> String { Self.family == nil ? withoutPowerline(text) : text }

    /// A line's spans as one Text, so it wraps and baselines as text does.
    func spans(_ line: [PromptSpan]) -> Text {
        line.map { s in PromptSpan(text: shown(s.text), fg: s.fg, bg: s.bg, bold: s.bold) }.reduce(Text("")) { acc, s in
            var t = Text(s.text).foregroundColor(Color(nsColor: Theme.ns(s.fg.map(RGB.init) ?? zshPromptFG)))
            if s.bold == true { t = t.bold() }
            if let bg = s.bg {
                // Text has no background of its own; an attributed run does.
                var a = AttributedString(s.text)
                a.backgroundColor = Color(nsColor: Theme.ns(RGB(bg)))
                a.foregroundColor = Color(nsColor: Theme.ns(s.fg.map(RGB.init) ?? zshPromptFG))
                t = Text(a)
                if s.bold == true { t = t.bold() }
            }
            return acc + t
        }
    }
}
