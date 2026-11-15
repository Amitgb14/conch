import ConchKit
import SwiftUI

/// Settings → Mac app → Background: the scenes as cards, each drawn by
/// the scene itself (still), then how far it shows through panes, the
/// frost over it and whether it moves — [app] background and the keys
/// beside it, saved with the rest of the form.
struct BackgroundCard: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    /// background_through, background_frost and background_motion.
    let fields: [SettingsField]
    @Environment(\.conchTheme) private var theme

    static let keys = ["background", "background_through", "background_frost", "background_motion"]

    var chosen: BackgroundScene { BackgroundScene(setting: model.form?.config["app"]?.object?["background"]?.string) }

    func choose(_ s: BackgroundScene) {
        guard let f = section.fields.first(where: { $0.key == "background" }) else { return }
        withAnimation(.easeOut(duration: 0.15)) { model.form?.set(section, f, s.setting.map(JSONValue.string)) }
    }

    var body: some View {
        let _ = theme
        SettingsCard(title: "Background") {
            VStack(alignment: .leading, spacing: 12) {
                Text("A scene behind the window: by day under a light theme, by night under a dark one. The toolbar, sidebar and tabs are frosted over it; panes let it through only as far as you say.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 12), count: 3), spacing: 12) {
                    ForEach(BackgroundScene.allCases, id: \.self) { s in card(s) }
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
            ForEach(Array(fields.enumerated()), id: \.element.key) { _, f in
                FieldRow(model: model, section: section, field: f)
            }
        }
    }

    func card(_ s: BackgroundScene) -> some View {
        let on = chosen == s
        return Button { choose(s) } label: {
            VStack(spacing: 0) {
                ZStack {
                    if s == .none {
                        Theme.window
                    } else {
                        SceneView(scene: SceneLook(scene: s, still: true))
                    }
                    // The frame and a pane over it, as the window has them.
                    HStack(spacing: 5) {
                        RoundedRectangle(cornerRadius: 3).fill(Theme.side.opacity(s == .none ? 1 : 0.6)).frame(width: 30)
                        RoundedRectangle(cornerRadius: 3).fill(Theme.window.opacity(s == .none ? 1 : 0.85))
                            .overlay(RoundedRectangle(cornerRadius: 3).stroke(Theme.line))
                    }
                    .padding(8)
                }
                .frame(height: 78).clipped()
                HStack {
                    Text(s.label).font(.ui(size: 12.5, weight: on ? .semibold : .regular)).foregroundStyle(on ? Theme.text : Theme.text2)
                    Spacer()
                    if on { Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.accent) }
                }
                .padding(.horizontal, 10).padding(.vertical, 7)
            }
            .background(Theme.raise2)
            .clipShape(RoundedRectangle(cornerRadius: 9))
            .overlay(RoundedRectangle(cornerRadius: 9).strokeBorder(on ? Theme.accent : Theme.line, lineWidth: on ? 2 : 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(s.label)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}
