import Combine
import AppKit
import ConchKit
import SwiftUI

// conch's Settings, in a panel of the app's own: the browser page's form
// (internal/configweb) drawn natively — its sections down the left with
// their Lucide icons, the chosen one's settings on the right — from the
// same schema, read and saved through the page's API, so the two cannot
// disagree about a setting. Tables (actions, permission rules, sandbox
// providers, a project's checks) open the page, which edits them.

@MainActor
final class SettingsModel: ObservableObject {
    @Published var form: SettingsForm?
    @Published var section = "ui"
    @Published var status = ""
    @Published var failure = ""
    @Published var saving = false

    let store: Store
    private var base: URL?
    private var token = ""

    init(store: Store) { self.store = store }

    /// Reads the settings, starting conch's page if it is not running.
    func load() {
        Task {
            guard let u = await store.settingsPage(), let t = settingsToken(u) else {
                failure = "conch's settings could not be started"
                return
            }
            token = t
            var c = URLComponents(url: u, resolvingAgainstBaseURL: false)!
            c.fragment = nil
            c.path = "/api/config"
            base = c.url
            await read()
        }
    }

    private func request(_ method: String, body: Data? = nil) -> URLRequest? {
        guard let base else { return nil }
        var r = URLRequest(url: base)
        r.httpMethod = method
        r.setValue(token, forHTTPHeaderField: "X-Conch-Token")
        if let body {
            r.httpBody = body
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return r
    }

    func read() async {
        guard let r = request("GET") else { return }
        do {
            let (data, _) = try await URLSession.shared.data(for: r)
            let p = try JSONDecoder().decode(SettingsPayload.self, from: data)
            if form == nil { form = SettingsForm(p) } else { form?.reset(p) }
            failure = ""
        } catch {
            failure = "Could not read the settings: \(error.localizedDescription)"
        }
    }

    /// Saves the form. A file changed since it was read is said, and read
    /// again, rather than written over.
    func save() {
        guard let f = form, f.dirty, let body = try? f.saveBody(), let r = request("PUT", body: body) else { return }
        saving = true
        Task {
            defer { saving = false }
            do {
                let (data, resp) = try await URLSession.shared.data(for: r)
                let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
                struct Refusal: Decodable { var error: String; var current: SettingsPayload? }
                switch code {
                case 200:
                    form?.reset(try JSONDecoder().decode(SettingsPayload.self, from: data))
                    status = "Saved to config.toml — an open TUI takes it in, and this window too"
                    failure = ""
                case 409:
                    let rf = try? JSONDecoder().decode(Refusal.self, from: data)
                    if let cur = rf?.current { form?.reset(cur) }
                    failure = "config.toml changed since this was read (the TUI, an editor?): read again — make your change once more"
                default:
                    let rf = try? JSONDecoder().decode(Refusal.self, from: data)
                    failure = rf?.error ?? "Saving failed (\(code))"
                }
            } catch {
                failure = "Saving failed: \(error.localizedDescription)"
            }
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    /// The theme on screen, a preview included.
    @ObservedObject var look: ShownTheme

    var body: some View {
        HStack(spacing: 0) {
            sidebar.frame(width: 230)
            Rectangle().fill(Theme.line).frame(width: 1)
            VStack(spacing: 0) {
                if let f = model.form, let s = f.payload.schema.first(where: { $0.id == model.section }) {
                    ScrollView { SectionForm(model: model, section: s).padding(28) }
                } else if !model.failure.isEmpty {
                    message(model.failure)
                } else {
                    message("Reading conch's settings…")
                }
                footer
            }
            .background(Theme.window)
        }
        .frame(minWidth: 820, minHeight: 540)
        .background(Theme.window)
        .preferredColorScheme(look.theme.light ? .light : .dark)
        // A new theme repaints every view that reads this, in place: not
        // .id, as the main window does, which would make the window anew —
        // scrolled to the top, under a pointer previewing a theme.
        .environment(\.conchTheme, look.theme.id)
        .onAppear { if model.form == nil { model.load() } }
    }

    var sidebar: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("SETTINGS").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 10).padding(.top, 14).padding(.bottom, 6)
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(model.form?.payload.schema ?? []) { s in
                        let on = s.id == model.section
                        HStack(spacing: 9) {
                            LucideIcon(s.icon, color: on ? Theme.accent : Theme.text2, size: 15).frame(width: 18)
                            Text(s.title).font(.system(size: 13)).foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1)
                            Spacer()
                            if let f = model.form, sectionDirty(f, s) { Circle().fill(Theme.accent).frame(width: 6, height: 6) }
                        }
                        .padding(.horizontal, 10).frame(height: 30)
                        .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accentDeep.opacity(0.35) : .clear))
                        .contentShape(Rectangle())
                        .onTapGesture { model.section = s.id }
                    }
                }
            }
            Spacer(minLength: 0)
            if let p = model.form?.payload.path {
                Text(p).font(.system(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(2)
                    .padding(10)
            }
        }
        .padding(.horizontal, 8)
        .background(Theme.side)
    }

    func sectionDirty(_ f: SettingsForm, _ s: SettingsSection) -> Bool {
        s.fields.contains { f.value(s, $0) != SettingsForm(f.payload).value(s, $0) }
    }

    var footer: some View {
        HStack(spacing: 10) {
            if !model.failure.isEmpty, model.form != nil {
                LucideIcon("triangle-alert", color: Theme.warn, size: 13)
                Text(model.failure).foregroundStyle(Theme.warn).lineLimit(2)
            } else if let problems = model.form?.payload.problems, let first = problems.first {
                LucideIcon("triangle-alert", color: Theme.warn, size: 13)
                Text(first).foregroundStyle(Theme.warn).lineLimit(2).help(problems.joined(separator: "\n"))
            } else {
                Text(model.status).foregroundStyle(Theme.muted).lineLimit(1)
            }
            Spacer()
            Button { model.store.openSettingsInBrowser() } label: {
                HStack(spacing: 5) { LucideIcon("external-link", size: 12); Text("Open in browser") }
            }
            .buttonStyle(.plain).foregroundStyle(Theme.text2)
            Button("Revert") { model.form?.revert() }.disabled(!(model.form?.dirty ?? false))
            Button("Save") { model.save() }.keyboardShortcut("s", modifiers: .command)
                .disabled(!(model.form?.dirty ?? false) || model.saving)
        }
        .font(.system(size: 12))
        .padding(.horizontal, 18).frame(height: 46)
        .background(Theme.chrome)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    func message(_ text: String) -> some View {
        VStack { Spacer(); Text(text).foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
    }
}

/// One section: its title and what it is for, then each setting with its
/// label and help, as the page lays them out.
struct SectionForm: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 10) {
                LucideIcon(section.icon, color: Theme.accent, size: 20)
                Text(section.title).font(.system(size: 20, weight: .semibold))
            }
            Text(section.description).foregroundStyle(Theme.text2)
            if !section.simple {
                HStack(spacing: 8) {
                    Text(section.fields.allSatisfy { !$0.simple } ? "These are tables of their own, edited on the settings page."
                         : "Some of these are tables, edited on the settings page.")
                        .foregroundStyle(Theme.muted)
                    Button("Open in browser") { model.store.openSettingsInBrowser() }.buttonStyle(.link)
                }
                .font(.system(size: 12))
            }
            ForEach(section.fields.filter(\.simple), id: \.key) { f in
                FieldRow(model: model, section: section, field: f)
                Rectangle().fill(Theme.line.opacity(0.6)).frame(height: 1)
            }
        }
        .frame(maxWidth: 680, alignment: .leading)
    }
}

/// One setting: its control at the right, label and help at the left — a
/// switch, a menu, a field, or a box of one entry a line.
struct FieldRow: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField

    @Environment(\.conchTheme) private var theme

    var value: JSONValue? { model.form?.value(section, field) }

    func set(_ v: JSONValue?) { model.form?.set(section, field, v) }

    var body: some View {
        let _ = theme
        let wide = ["text", "list", "ints"].contains(field.kind) || isTheme
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 16) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(field.label).font(.system(size: 13, weight: .medium))
                    if let h = field.help, !h.isEmpty {
                        Text(h).font(.system(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 12)
                if !wide { control.frame(width: field.kind == "bool" ? nil : 260, alignment: .trailing) }
            }
            if wide { control }
        }
    }

    /// [ui] theme is chosen from pictures of the window rather than names.
    var isTheme: Bool { section.key == "ui" && field.key == "theme" && field.kind == "select" }

    @ViewBuilder var control: some View {
        if isTheme {
            ThemeGrid(options: field.options ?? [], chosen: value?.string ?? "",
                      choose: { set(.string($0)) }, preview: { model.store.previewTheme($0) })
        } else {
            plainControl
        }
    }

    @ViewBuilder var plainControl: some View {
        switch field.kind {
        case "bool":
            Toggle("", isOn: Binding(get: { value?.bool ?? false }, set: { set(.bool($0)) })).toggleStyle(.switch).labelsHidden()
                .tint(Theme.accentDeep)
        case "select":
            Picker("", selection: Binding(get: { value?.string ?? "" }, set: { set(.string($0)) })) {
                ForEach(field.options ?? [], id: \.value) { Text($0.label).tag($0.value) }
                // A value the schema does not list (a #rrggbb accent) still shows.
                if let v = value?.string, !(field.options ?? []).contains(where: { $0.value == v }) { Text(v).tag(v) }
            }
            .labelsHidden()
        case "int", "float":
            TextField(field.placeholder ?? "", text: Binding(
                get: { value?.number.map { field.kind == "int" ? String(Int($0)) : String($0) } ?? "" },
                set: { t in
                    let t = t.trimmingCharacters(in: .whitespaces)
                    if t.isEmpty { set(nil) } else if let n = Double(t) { set(.number(field.kind == "int" ? n.rounded() : n)) }
                }))
                .textFieldStyle(.roundedBorder).multilineTextAlignment(.trailing)
        case "secret":
            SecureField(field.placeholder ?? "", text: Binding(get: { value?.string ?? "" }, set: { set(.string($0)) }))
                .textFieldStyle(.roundedBorder)
        case "text", "list", "ints":
            TextEditor(text: Binding(
                get: { field.kind == "text" ? value?.string ?? "" : listText(value) },
                set: { set(field.kind == "text" ? .string($0) : listValue($0, ints: field.kind == "ints")) }))
                .font(.system(size: 12, design: .monospaced)).frame(height: 70)
                .scrollContentBackground(.hidden).padding(5)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line2))
        default: // string
            TextField(field.placeholder ?? "", text: Binding(get: { value?.string ?? "" }, set: { set(.string($0)) }))
                .textFieldStyle(.roundedBorder)
        }
    }
}

/// The themes Settings offers, each as a small picture of the window in
/// it: the toolbar, the sidebar with a row selected, a tab and a pane of
/// output in the theme's colours. The pointer on one shows the app in it,
/// this window included; a click chooses it, saved with the rest.
struct ThemeGrid: View {
    let options: [SettingsOption]
    let chosen: String
    let choose: (String) -> Void
    let preview: (String?) -> Void
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 220), spacing: 14)], alignment: .leading, spacing: 14) {
            ForEach(options, id: \.value) { o in
                let on = themeChosen(o.value, chosen)
                Button { choose(o.value) } label: {
                    VStack(alignment: .leading, spacing: 6) {
                        ThemePreview(theme: ConchTheme.named(o.value))
                            .frame(height: 96)
                            .clipShape(RoundedRectangle(cornerRadius: 7))
                            .overlay(RoundedRectangle(cornerRadius: 7).stroke(on ? Theme.accent : Theme.line, lineWidth: on ? 2 : 1))
                        HStack(spacing: 6) {
                            Text(o.label).font(.system(size: 12, weight: on ? .semibold : .regular))
                                .foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1)
                            Spacer(minLength: 0)
                            if ConchTheme.named(o.value).light { Text("light").font(.system(size: 10.5)).foregroundStyle(Theme.muted) }
                            if on { LucideIcon("check", color: Theme.accent, size: 12) }
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(o.label)
                .onHover { if $0 { preview(o.value) } }
            }
        }
        .contentShape(Rectangle())
        // Between cards the last one stays shown; off the grid, none.
        .onHover { if !$0 { preview(nil) } }
        .onDisappear { preview(nil) }
    }
}

/// The window in miniature, drawn from a theme's colours as the window
/// derives them.
struct ThemePreview: View {
    let theme: ConchTheme

    func c(_ x: RGB) -> Color { Theme.color(x) }

    func bar(_ color: RGB, _ w: CGFloat) -> some View {
        RoundedRectangle(cornerRadius: 1).fill(c(color)).frame(width: w, height: 3)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 3) {
                ForEach(["#FF5F57", "#FEBC2E", "#28C840"], id: \.self) { Circle().fill(c(RGB($0))).frame(width: 5, height: 5) }
                Spacer()
                RoundedRectangle(cornerRadius: 3).fill(c(theme.well)).frame(width: 52, height: 8)
                    .overlay(RoundedRectangle(cornerRadius: 3).stroke(c(theme.border), lineWidth: 0.5))
                Spacer()
                Capsule().fill(c(theme.warn)).frame(width: 14, height: 6)
            }
            .padding(.horizontal, 6).frame(height: 16).background(c(theme.chrome))
            HStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 5) {
                    bar(theme.muted, 18)
                    HStack(spacing: 3) { Circle().fill(c(theme.ok)).frame(width: 4, height: 4); bar(theme.fg2, 20) }
                    HStack(spacing: 3) { Circle().fill(c(RGB("#D97757"))).frame(width: 4, height: 4); bar(theme.selFG, 18) }
                        .padding(.horizontal, 3).padding(.vertical, 2)
                        .background(RoundedRectangle(cornerRadius: 2).fill(c(theme.accent)))
                        .padding(.horizontal, -3)
                    HStack(spacing: 3) { Circle().fill(c(RGB("#10A37F"))).frame(width: 4, height: 4); bar(theme.fg2, 16) }
                    HStack(spacing: 3) { Circle().fill(c(theme.work)).frame(width: 4, height: 4); bar(theme.fg2, 22) }
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 6).padding(.top, 6)
                .frame(width: 44, alignment: .leading).frame(maxHeight: .infinity, alignment: .top)
                .background(c(theme.sidebar))
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 6) {
                        VStack(spacing: 2) { bar(theme.fg, 22); Rectangle().fill(c(theme.line)).frame(width: 22, height: 1.5) }
                        bar(theme.fg2, 18).padding(.bottom, 3.5)
                        Spacer()
                    }
                    .padding(.horizontal, 6).padding(.top, 5)
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(spacing: 3) { bar(theme.muted, 6); bar(theme.fg, 34) }
                        HStack(spacing: 3) { bar(RGB("#D97757"), 4); bar(theme.fg, 26) }
                        bar(theme.ok, 30)
                        bar(theme.err, 22)
                        HStack(spacing: 3) { bar(theme.work, 4); bar(theme.warn, 18) }
                        Spacer(minLength: 0)
                    }
                    .padding(6)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    .background(c(theme.bg))
                    .overlay(RoundedRectangle(cornerRadius: 3).stroke(c(theme.line), lineWidth: 1))
                    .clipShape(RoundedRectangle(cornerRadius: 3))
                    .padding(4)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(c(theme.bg))
            }
        }
        .accessibilityLabel("\(theme.label) theme")
    }
}

/// The Settings window, made once and brought forward again.
@MainActor
final class SettingsWindow {
    private var window: NSWindow?
    private let model: SettingsModel
    private var watch: [Any] = []
    private var look: AnyCancellable?

    init(store: Store) { model = SettingsModel(store: store) }

    func show() {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 960, height: 640),
                             styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = "conch Settings"
            w.isReleasedWhenClosed = false
            w.contentView = NSHostingView(rootView: SettingsView(model: model, look: model.store.look))
            look = model.store.look.$theme.sink { [weak w] t in
                w?.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
                w?.backgroundColor = Theme.ns(t.chrome)
            }
            // A preview never outlives the window, or its being left for
            // another app while the pointer was on a card.
            let store = model.store
            for n in [NSWindow.willCloseNotification, NSWindow.didResignKeyNotification] {
                watch.append(NotificationCenter.default.addObserver(forName: n, object: w, queue: .main) { _ in
                    MainActor.assumeIsolated { store.previewTheme(nil) }
                })
            }
            w.center()
            w.setFrameAutosaveName("conch.settings")
            window = w
        } else {
            // A file changed meanwhile is read again, unless edits are pending.
            if !(model.form?.dirty ?? false) { Task { await model.read() } }
        }
        window?.makeKeyAndOrderFront(nil)
    }
}

/// The id of the theme on screen, for the Settings window's views to
/// depend on: reading it is what repaints them when it changes.
private struct ConchThemeKey: EnvironmentKey {
    static let defaultValue = ""
}

extension EnvironmentValues {
    var conchTheme: String {
        get { self[ConchThemeKey.self] }
        set { self[ConchThemeKey.self] = newValue }
    }
}
