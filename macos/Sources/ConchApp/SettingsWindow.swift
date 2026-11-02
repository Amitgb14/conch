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
    @ObservedObject var store: Store

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
        .preferredColorScheme(Theme.current.light ? .light : .dark)
        .id(store.theme.id) // a new theme repaints it, as the main window
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

    var body: some View {
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

    var value: JSONValue? { model.form?.value(section, field) }

    func set(_ v: JSONValue?) { model.form?.set(section, field, v) }

    var body: some View {
        let wide = ["text", "list", "ints"].contains(field.kind)
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

    @ViewBuilder var control: some View {
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

/// The Settings window, made once and brought forward again.
@MainActor
final class SettingsWindow {
    private var window: NSWindow?
    private let model: SettingsModel

    init(store: Store) { model = SettingsModel(store: store) }

    func show() {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 960, height: 640),
                             styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = "conch Settings"
            w.isReleasedWhenClosed = false
            w.contentView = NSHostingView(rootView: SettingsView(model: model, store: model.store))
            w.backgroundColor = Theme.ns(Theme.current.chrome)
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
