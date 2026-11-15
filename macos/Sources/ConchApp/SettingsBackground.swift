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

    static let keys = ["background", "background_through", "background_frost", "background_motion", "background_fireworks", "project_backgrounds"]

    var mine: [String] { model.store.myScenes.map(\.id) }
    var chosen: BackgroundScene { BackgroundScene(setting: model.form?.config["app"]?.object?["background"]?.string, mine: mine) }
    /// Every scene to pick: conch's, then yours.
    var scenes: [BackgroundScene] { BackgroundScene.builtIn + model.store.myScenes.map { .mine($0.id) } }

    /// A scene's name: yours by what its file calls it.
    func label(_ s: BackgroundScene) -> String {
        if case .mine(let id) = s { return model.store.myScenes.first { $0.id == id }?.label ?? id }
        return s.label
    }

    /// A board in Settings shows something to read.
    static let sample = [
        Departure(time: Date(timeIntervalSinceReferenceDate: 0), agent: "claude", to: "api/login", state: .working),
        Departure(time: Date(timeIntervalSinceReferenceDate: 600), agent: "codex", to: "web/search", state: .blocked),
    ]

    /// $CONCH_HOME/scenes, made if it is not there yet.
    func openFolder() {
        let dir = (model.store.conchDir as NSString).appendingPathComponent("scenes")
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        NSWorkspace.shared.open(URL(fileURLWithPath: dir))
    }

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
                    ForEach(scenes, id: \.self) { s in card(s) }
                }
                HStack(alignment: .firstTextBaseline) {
                    Text("Your own: a file in the scenes folder — colours, a picture, snow or bubbles. It describes a scene; it runs nothing.")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                    Spacer()
                    Button("Open Scenes Folder") { openFolder() }.controlSize(.small)
                }
                ForEach(model.store.sceneProblems, id: \.self) { p in
                    Text(p).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
            ForEach(Array(fields.enumerated()), id: \.element.key) { _, f in
                FieldRow(model: model, section: section, field: f)
            }
        }
        if let f = section.fields.first(where: { $0.key == "project_backgrounds" }) {
            ProjectScenes(model: model, section: section, field: f)
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
                        SceneView(scene: SceneLook(scene: s, still: true), departures: Self.sample)
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
                    Text(label(s)).font(.ui(size: 12.5, weight: on ? .semibold : .regular)).foregroundStyle(on ? Theme.text : Theme.text2)
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
        .accessibilityLabel(label(s))
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// [app.project_backgrounds]: each project a scene of its own, or the
/// window's. Every project conch knows is listed, and any named in the
/// file that it does not (another machine's, one since removed).
struct ProjectScenes: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField
    @Environment(\.conchTheme) private var theme

    var chosen: [String: String] {
        Dictionary((model.form?.mapEntries(section, field) ?? []).map { ($0.key, $0.value) }, uniquingKeysWith: { a, _ in a })
    }

    var mine: [String] { model.store.myScenes.map(\.id) }

    /// The scenes to pick, conch's and yours, by name.
    var options: [(id: String, label: String)] {
        let builtIn = BackgroundScene.builtIn.filter { $0 != .none }.map { (id: $0.rawValue, label: $0.label) }
        return builtIn + model.store.myScenes.map { (id: $0.id, label: $0.label) }
    }

    var names: [String] {
        var all = Set(chosen.keys)
        for snap in model.store.snapshots.values { for p in snap.projects { all.insert(p.name) } }
        return all.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }

    /// What the picker shows for a saved value: a typo is the window's
    /// scene, as the window reads it.
    static func choice(_ v: String?, mine: [String]) -> String {
        let s = BackgroundScene(setting: v, mine: mine)
        if s != .none { return s.rawValue }
        return v?.trimmingCharacters(in: .whitespaces).lowercased() == "none" ? "none" : ""
    }

    var body: some View {
        let _ = theme
        let set = chosen
        SettingsCard(title: field.label) {
            VStack(alignment: .leading, spacing: 0) {
                Text("A project's window shows its own scene, and so does any window while one of its tabs is in front.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 16).padding(.top, 4).padding(.bottom, 8)
                if names.isEmpty {
                    Text("No projects yet.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                        .padding(.horizontal, 16).padding(.bottom, 12)
                }
                ForEach(names, id: \.self) { name in
                    HStack(spacing: 10) {
                        Text(name).font(.ui(size: 12.5, weight: .medium)).lineLimit(1).frame(width: 190, alignment: .leading)
                        Picker("", selection: Binding(
                            get: { Self.choice(set[name], mine: mine) },
                            set: { v in model.form?.setMapEntry(section, field, name, v.isEmpty ? nil : v) })) {
                            Text("Same as the window").tag("")
                            ForEach(options, id: \.id) { o in Text(o.label).tag(o.id) }
                            Text("No scene").tag("none")
                        }
                        .labelsHidden().frame(width: 200)
                        Spacer()
                    }
                    .padding(.horizontal, 16).padding(.vertical, 6)
                }
            }
            .padding(.bottom, 6)
        }
    }
}
