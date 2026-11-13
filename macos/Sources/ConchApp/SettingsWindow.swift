import Combine
import AppKit
import ConchKit
import SwiftUI

// conch's Settings, in a panel of the app's own: the browser page's form
// (internal/configweb) drawn natively — its sections down the left with
// their Lucide icons, the chosen one's settings on the right — from the
// same schema, read and saved through the page's API, so the two cannot
// disagree about a setting. Tables (actions, permission rules, sandbox
// providers) are lists here, each entry a card of its own fields; only a
// map (a project's checks) is left to config.toml.

@MainActor
final class SettingsModel: ObservableObject {
    @Published var form: SettingsForm?
    @Published var section = "ui"
    @Published var status = ""
    /// What was just done, shown for a moment at the top right.
    @Published var toast: String?
    /// The reviewer before the autopilot line's Review was turned off, to
    /// turn it back on with.
    var lastReviewer: String?

    func say(_ text: String) {
        toast = text
        let t = text
        Task { try? await Task.sleep(for: .seconds(2.4)); if toast == t { toast = nil } }
    }
    @Published var failure = ""
    /// An entry of a keyed list to open, asked for from elsewhere on the
    /// page: Set key… beside a provider opens its card.
    @Published var openEntry: String?
    /// How many times this window has saved: what was shown from the
    /// file, such as whether a provider has its key, is read again.
    @Published var saves = 0
    @Published var saving = false

    let store: Store
    private var base: URL?
    private var token = ""

    /// What the brain's provider lists, for Model and Summary model.
    @Published var models: BrainModels?
    @Published var modelsLoading = false
    private var modelsAsked: String?

    /// Asks conch for the models of the provider as the form has it now.
    /// The same question is asked once; one typed out letter by letter (an
    /// endpoint) waits for the typing to stop.
    func loadModels(_ brain: [String: JSONValue]?) {
        let q = modelsQuery(brain)
        let key = q.map { "\($0.name)=\($0.value ?? "")" }.joined(separator: "&")
        guard key != modelsAsked, let base, var c = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return }
        modelsAsked = key
        c.path = "/api/models"
        c.queryItems = q
        guard let url = c.url else { return }
        var r = URLRequest(url: url)
        r.setValue(token, forHTTPHeaderField: "X-Conch-Token")
        modelsLoading = true
        Task {
            try? await Task.sleep(for: .milliseconds(400))
            guard modelsAsked == key else { return }
            defer { if modelsAsked == key { modelsLoading = false } }
            do {
                let (data, _) = try await URLSession.shared.data(for: r)
                let m = try JSONDecoder().decode(BrainModels.self, from: data)
                if modelsAsked == key { models = m }
            } catch {
                if modelsAsked == key { models = BrainModels(models: [], error: "Could not ask for the models: \(error.localizedDescription)") }
            }
        }
    }

    /// Oh My Zsh themes' prompts, by theme ("" is .zshrc's), as they come.
    @Published var prompts: [String: ZshPromptAnswer] = [:]
    private var promptsAsked: Set<String> = []

    /// Asks conch to draw a theme's prompt, once a window: the list asks
    /// as each theme comes into view, and conch draws four at a time.
    func loadPrompt(_ theme: String) {
        guard !promptsAsked.contains(theme), let base, var c = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return }
        promptsAsked.insert(theme)
        c.path = "/api/zsh-prompt"
        c.queryItems = zshPromptQuery(theme)
        guard let url = c.url else { return }
        var r = URLRequest(url: url)
        r.setValue(token, forHTTPHeaderField: "X-Conch-Token")
        r.timeoutInterval = 30
        Task {
            do {
                let (data, _) = try await URLSession.shared.data(for: r)
                prompts[theme] = try JSONDecoder().decode(ZshPromptAnswer.self, from: data)
            } catch {
                prompts[theme] = ZshPromptAnswer(error: "Could not draw it: \(error.localizedDescription)")
                promptsAsked.remove(theme)
            }
        }
    }

    /// The icon set under the pointer in Appearance → Icons, shown in
    /// the sidebar until it leaves, as the mock's cards do.
    @Published var hoverIcons: SettingsIconSet?

    /// The set and style the window draws its section icons in: the form
    /// as edited, the card under the pointer first.
    var iconLook: (set: SettingsIconSet, style: SettingsIconStyle) {
        let saved = settingsIcons(form?.config["app"]?.object)
        return (hoverIcons ?? saved.set, saved.style)
    }

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
                    let saved = try JSONDecoder().decode(SettingsPayload.self, from: data)
                    // A theme chosen here is the one to show: it unpins a
                    // theme picked under View → Theme or by a style, which
                    // would otherwise keep the window as it was.
                    if savedThemeChanged(f.payload.config, saved.config) { store.chooseTheme("") }
                    // The autopilot reads [verify]; the servers are told now.
                    if f.payload.config["verify"] != saved.config["verify"] { store.pushAutopilotAll() }
                    form?.reset(saved)
                    saves += 1
                    status = themeSaveNote(f.payload.config, saved.config, dark: store.systemDark)
                        ?? "Saved to config.toml — an open TUI takes it in, and this window too"
                    say(status)
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
    @State private var query = ""
    @FocusState private var searching: Bool

    var schema: [SettingsSection] { model.form?.payload.schema ?? [] }

    var body: some View {
        HStack(spacing: 0) {
            sidebar.frame(width: 248)
            Rectangle().fill(Theme.line).frame(width: 1)
            ZStack(alignment: .bottom) {
                VStack(spacing: 0) {
                    banner
                    if model.section == SettingsNav.skills {
                        ScrollView { SkillsSettingsPane(store: model.store).padding(.horizontal, 34).padding(.top, 26).padding(.bottom, 90) }
                    } else if let f = model.form, let s = f.payload.schema.first(where: { $0.id == model.section }) {
                        ScrollView { SectionForm(model: model, section: s).padding(.horizontal, 34).padding(.top, 26).padding(.bottom, 90) }
                            .id(s.id)
                    } else if !model.failure.isEmpty {
                        message(model.failure)
                    } else {
                        message("Reading conch's settings…")
                    }
                }
                SaveBar(model: model)
            }
            // The page area takes the window's width: the cards keep
            // their own (760 at most), and without this the whole window
            // could not be made wider than they are.
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .overlay(alignment: .topTrailing) {
                if let t = model.toast { SettingsToast(text: t).padding(18).transition(.move(edge: .top).combined(with: .opacity)) }
            }
            .animation(.spring(response: 0.3, dampingFraction: 0.8), value: model.toast)
            .background(Theme.window)
        }
        .frame(minWidth: 900, minHeight: 600)
        .background(Theme.window)
        .preferredColorScheme(look.theme.light ? .light : .dark)
        // A new theme repaints every view that reads this, in place: not
        // .id, as the main window does, which would make the window anew —
        // scrolled to the top, under a pointer previewing a theme.
        .environment(\.conchTheme, look.theme.id)
        .onAppear { if model.form == nil { model.load() } }
    }

    var sidebar: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 7) {
                Image(systemName: "magnifyingglass").font(.system(size: 11)).foregroundStyle(Theme.muted)
                TextField("Search settings", text: $query).textFieldStyle(.plain).font(.ui(size: 12.5)).focused($searching)
                    .onSubmit { if let first = SettingsNav.search(query, in: schema).first { model.section = first } }
                if query.isEmpty {
                    Text("⌘F").font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
                        .padding(.horizontal, 4).overlay(RoundedRectangle(cornerRadius: 4).stroke(Theme.line2))
                } else {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill").font(.system(size: 11)) }.buttonStyle(.plain).foregroundStyle(Theme.muted)
                }
            }
            .padding(.horizontal, 10).frame(height: 32)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(searching ? Theme.accent : Theme.line, lineWidth: searching ? 1.5 : 1))
            .padding(12)
            .background { Button("") { searching = true }.keyboardShortcut("f", modifiers: .command).hidden() }
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    let found = SettingsNav.search(query, in: schema)
                    ForEach(SettingsNav.grouped(found), id: \.0) { group, ids in
                        Text(group.uppercased()).font(.ui(size: 10.5, weight: .semibold)).tracking(0.8).foregroundStyle(Theme.muted)
                            .padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 4)
                        ForEach(ids, id: \.self) { id in navRow(id) }
                    }
                    if found.isEmpty && !schema.isEmpty {
                        Text("No setting matches “\(query)”").font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(10)
                    }
                }
                .padding(.horizontal, 8).padding(.bottom, 10)
            }
            .onChange(of: query) { _, q in
                // Typing finds the first section that has it.
                if !q.isEmpty, let first = SettingsNav.search(q, in: schema).first, !SettingsNav.search(q, in: schema).contains(model.section) { model.section = first }
            }
            Spacer(minLength: 0)
            if let p = model.form?.payload.path {
                Button { model.store.editConfigFile() } label: {
                    HStack(spacing: 6) {
                        Image(systemName: "doc.text").font(.system(size: 10))
                        Text(p).font(.ui(size: 10.5, design: .monospaced)).lineLimit(1).truncationMode(.head)
                    }
                    .foregroundStyle(Theme.muted)
                }
                .buttonStyle(.plain).help("Edit config.toml in your text editor")
                .padding(.horizontal, 14).padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
            }
        }
        .background(Theme.side)
    }

    func navRow(_ id: String) -> some View {
        let s = schema.first { $0.id == id }
        let on = id == model.section
        let changed = model.form.flatMap { f in s.map { settingsChangedShown(f, $0) } } ?? 0
        // A button, so VoiceOver (and anything pressing by accessibility)
        // can choose a section too.
        return Button { model.section = id } label: {
            HStack(spacing: 10) {
                let look = model.iconLook
                SectionIcon(icon: s?.icon ?? SettingsNav.skillsIcon, set: look.set, style: look.style,
                            color: on ? Theme.accent : Theme.text2, size: 15).frame(width: 22)
                Text(s?.title ?? SettingsNav.skillsTitle).font(.ui(size: 13)).foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1)
                Spacer()
                if changed > 0 {
                    Text("\(changed)").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.onWarn)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(Capsule().fill(Theme.warn))
                        .help("\(changed) unsaved")
                }
            }
            .padding(.horizontal, 10).frame(height: 32)
            .background(RoundedRectangle(cornerRadius: 7).fill(on ? Theme.accentDeep.opacity(0.32) : .clear))
            .overlay(alignment: .leading) {
                if on { RoundedRectangle(cornerRadius: 2).fill(Theme.accent).frame(width: 3, height: 18).offset(x: -8) }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    /// What is wrong with the file, or the last save: above the page.
    @ViewBuilder var banner: some View {
        let text = !model.failure.isEmpty && model.form != nil ? model.failure : model.form?.payload.problems?.first
        if let text {
            HStack(spacing: 8) {
                LucideIcon("triangle-alert", color: Theme.warn, size: 13)
                Text(text).font(.ui(size: 12)).foregroundStyle(Theme.warn).lineLimit(2)
                    .help(model.form?.payload.problems?.joined(separator: "\n") ?? text)
                Spacer()
                Button("Edit config.toml") { model.store.editConfigFile() }.buttonStyle(.link).font(.ui(size: 12))
            }
            .padding(.horizontal, 18).padding(.vertical, 9)
            .background(Theme.warn.opacity(0.08))
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.warn.opacity(0.3)).frame(height: 1) }
        }
    }

    func message(_ text: String) -> some View {
        VStack { Spacer(); Text(text).foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
    }
}

/// Unsaved changes, said where the eye already is: a bar that rises from
/// the bottom while there are any, to save or revert them.
struct SaveBar: View {
    @ObservedObject var model: SettingsModel
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let n = model.form.map { settingsChanged($0) } ?? 0
        HStack(spacing: 12) {
            Circle().fill(Theme.warn).frame(width: 8, height: 8)
            Text("\(n) unsaved change\(n == 1 ? "" : "s")").font(.ui(size: 12.5))
            Button("Revert") { withAnimation { model.form?.revert() } }.buttonStyle(.plain).foregroundStyle(Theme.text2)
                .padding(.horizontal, 10).frame(height: 26)
            Button { model.save() } label: {
                HStack(spacing: 6) {
                    if model.saving { ProgressView().controlSize(.mini) }
                    Text("Save").font(.ui(size: 12.5, weight: .semibold))
                    Text("⌘S").font(.ui(size: 10.5, design: .monospaced)).opacity(0.7)
                }
                .foregroundStyle(Theme.selectedText)
                .padding(.horizontal, 12).frame(height: 26)
                .background(RoundedRectangle(cornerRadius: 7).fill(Theme.accent))
            }
            .buttonStyle(.plain)
            .keyboardShortcut("s", modifiers: .command)
            .disabled(model.saving)
        }
        .padding(.leading, 14).padding(.trailing, 6).padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 12).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line2))
        .shadow(color: .black.opacity(0.4), radius: 14, y: 6)
        .padding(.bottom, 18)
        .offset(y: n > 0 ? 0 : 90)
        .opacity(n > 0 ? 1 : 0)
        .animation(.spring(response: 0.32, dampingFraction: 0.78), value: n > 0)
    }
}

/// The relay conch web is on, and whether it is now — watching the store,
/// since the form around it doesn't.
struct RelayCard: View {
    @ObservedObject var store: Store

    var body: some View {
        if let p = store.phones {
            SettingsCard(title: "conch web") {
                HStack(spacing: 10) {
                    Circle().fill(p.running == true ? Theme.ok : Theme.muted).frame(width: 7, height: 7)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(p.running == true ? "Running" : "Not running").font(.ui(size: 13, weight: .medium))
                        Text(p.running == true ? "The gateway phones reach. Restart it after an update to run this app's conch."
                             : "Phones can't reach this Mac until it runs.")
                            .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                    Spacer()
                    if p.running == true {
                        Button(store.webRestarting ? "Restarting…" : "Restart") { store.restartWeb() }.disabled(store.webRestarting)
                    } else {
                        Button(store.webRestarting ? "Starting…" : "Start") { store.restartWeb() }.disabled(store.webRestarting)
                    }
                }
                .padding(.horizontal, 16).padding(.vertical, 12)
            }
        }
        if let p = store.phones, let r = p.relay, !r.isEmpty {
            SettingsCard(title: "Relay") {
                ReachRow(icon: "globe", title: URL(string: r)?.host ?? r,
                         detail: "Phones reach this Mac from any network, end to end: the relay forwards bytes it cannot read.",
                         note: p.relayConnected == true ? nil : p.relayStatus,
                         chip: p.relayConnected == true ? "connected" : "not connected", on: p.relayConnected == true)
                    .padding(.horizontal, 6).padding(.vertical, 4)
            }
        }
    }
}

/// One section: a heading with its icon, what it is for, then its settings
/// in cards — the theme in pictures, [verify] as a line, the quiet hours as
/// a day.
struct SectionForm: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    @Environment(\.conchTheme) private var theme

    var fields: [SettingsField] { section.fields.filter(\.simple) }
    /// Its tables, drawn as lists after the rest.
    var tables: [SettingsField] { section.fields.filter { $0.kind == "tables" || $0.kind == "keyed" } }
    /// Its maps, drawn as pairs: [keys.bind]. [verify]'s commands are
    /// drawn with the rest of Checks & autopilot.
    var maps: [SettingsField] { section.fields.filter { $0.kind == "map" } }

    /// The section another's page draws: Checks & autopilot on Git's.
    func guest(_ id: String) -> SettingsSection? { model.form?.payload.schema.first { $0.id == id } }

    /// Keys a map offers: the projects on this computer for a check, the
    /// actions conch's keys do for a rebinding.
    func offer(_ s: SettingsSection, _ f: SettingsField) -> [(key: String, label: String)] {
        if s.key == "verify" && f.key == "commands" {
            return (model.store.snapshots["local"]?.projects ?? []).map { (key: $0.id, label: $0.name) }.sorted { $0.label < $1.label }
        }
        return (f.suggest ?? []).map { (key: $0, label: $0) }
    }

    /// Checks & autopilot: the line a finished branch takes, the check
    /// each project runs, then what happens after it.
    static let checkKeys = ["auto_fix", "require_tests", "max_diff"]

    @ViewBuilder func verifyCards(_ v: SettingsSection) -> some View {
        let simple = v.fields.filter(\.simple)
        let checks = Self.checkKeys
        let checkFields = simple.filter { checks.contains($0.key) }
        SettingsCard(title: "The line a finished branch takes") {
            AutopilotLine(steps: autopilotSteps(model.form?.config["verify"]?.object ?? [:])) { step in
                guard let f = v.fields.first(where: { $0.key == step.key }) else { return }
                if step.key == "reviewer", step.on { model.lastReviewer = model.form?.value(v, f)?.string }
                if let val = autopilotToggle(step, lastReviewer: model.lastReviewer) { model.form?.set(v, f, val) }
            }
        }
        ForEach(v.fields.filter { $0.kind == "map" }, id: \.key) { m in
            MapList(model: model, section: v, field: m, offer: offer(v, m))
        }
        if !checkFields.isEmpty { card("Checks", checkFields, in: v) }
        let rest = simple.filter { !checks.contains($0.key) }
        if !rest.isEmpty { card("Autopilot", rest, in: v) }
    }

    func pick(_ keys: [String]) -> [SettingsField] { fields.filter { keys.contains($0.key) } }
    func rest(_ keys: [String]) -> [SettingsField] { fields.filter { !keys.contains($0.key) } }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                let look = model.iconLook
                if look.style == .tiles {
                    SectionIcon(icon: section.icon, set: look.set, style: .tiles, color: .white, size: 26)
                        .frame(width: 38, height: 38)
                } else {
                    SectionIcon(icon: section.icon, set: look.set, style: .plain, color: Theme.accent, size: 19)
                        .frame(width: 38, height: 38)
                        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.accentDeep.opacity(0.3)))
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text(section.title).font(.ui(size: 21, weight: .semibold))
                    Text(section.description).font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                }
            }
            .padding(.bottom, 4)
            if let note = section.tableNote {
                HStack(spacing: 8) {
                    Image(systemName: "tablecells").font(.system(size: 11)).foregroundStyle(Theme.muted)
                    Text(note).foregroundStyle(Theme.muted)
                    Button("Edit config.toml") { model.store.editConfigFile() }.buttonStyle(.link)
                }
                .font(.ui(size: 12))
            }
            switch section.key {
            case "ui":
                // [app] icons and icon_style are the app's, but they are
                // how it looks, so they are shown here, first: the theme
                // grids below are long.
                if let app = model.form?.payload.schema.first(where: { $0.key == "app" }) {
                    let icons = app.fields.filter { SettingsIconKeys.contains($0.key) }
                    if !icons.isEmpty {
                        SettingsCard(title: "Icons") {
                            ForEach(Array(icons.enumerated()), id: \.element.key) { i, f in
                                FieldRow(model: model, section: app, field: f, first: i == 0)
                            }
                        }
                    }
                }
                let themes = pick(["theme", "light_theme"])
                if !themes.isEmpty { card("Theme", themes) }
                card(nil, rest(["theme", "light_theme"]))
            case "app":
                card(nil, rest(SettingsIconKeys))
            case "git":
                // Git's own, then Checks & autopilot: what a finished
                // branch passes, its pull request and its merge.
                if !fields.isEmpty { card("Merging", fields) }
                if let v = guest("verify") {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(v.title).font(.ui(size: 15, weight: .semibold))
                        Text(v.description).font(.ui(size: 12)).foregroundStyle(Theme.text2)
                    }
                    .padding(.top, 10)
                    verifyCards(v)
                }
            case "verify":
                // Its own page only for a schema with no Git to put it on.
                verifyCards(section)
            case "remote":
                card(nil, fields)
                SettingsCard(title: "Machines", trailing: AnyView(
                    Button("Add a Machine…") { model.store.addMachine() }.controlSize(.small))) {
                    MachinesList(store: model.store).padding(12)
                }
            case "notify":
                let quiet = ["quiet_start", "quiet_end"]
                card(nil, rest(quiet))
                if !pick(quiet).isEmpty {
                    SettingsCard(title: "Quiet hours") {
                        ForEach(Array(pick(quiet).enumerated()), id: \.element.key) { i, f in
                            FieldRow(model: model, section: section, field: f, first: i == 0)
                        }
                        QuietDay(start: model.form?.config["notify"]?.object?["quiet_start"]?.string,
                                 end: model.form?.config["notify"]?.object?["quiet_end"]?.string)
                            .padding(.horizontal, 16).padding(.bottom, 14)
                    }
                }
            case "agents":
                card(nil, fields)
                Button { model.section = SettingsNav.skills } label: {
                    HStack(spacing: 12) {
                        LucideIcon(SettingsNav.skillsIcon, color: Theme.accent, size: 16)
                            .frame(width: 32, height: 32).background(RoundedRectangle(cornerRadius: 8).fill(Theme.accentDeep.opacity(0.3)))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(SettingsNav.skillsTitle).font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text)
                            Text("conch's skills, and the MCP servers and skills you give the agents").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                        }
                        Spacer()
                        Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    }
                    .padding(14)
                    .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            case "web":
                // The relay's state beside where it is set, and the phones
                // paired with this computer: which are on now, and how.
                card(nil, fields)
                RelayCard(store: model.store)
                SettingsCard(title: "Paired phones", trailing: AnyView(
                    Button("Pair a phone…") { model.store.openPair() }.controlSize(.small))) {
                    PhonesList(store: model.store).padding(12)
                }
            case "cloud":
                // The switch and the server to sign in to (conch's schema
                // leaves only the switch while Cloud is hidden), then the
                // account and its machines (CloudSettings.swift) once it is on.
                card(nil, fields)
                if model.store.offersCloud { CloudCard(store: model.store) }
            case "sandbox":
                // Which providers there are, built in and adapted, and the
                // way to add one conch does not know.
                if !fields.isEmpty { card(nil, fields) }
                ProvidersCard(store: model.store, saves: model.saves) { name, label in
                    // Its card in the list below, made if it has none, and
                    // opened at the API key.
                    guard let t = tables.first(where: { $0.kind == "keyed" }) else { return }
                    model.form?.addEntry(section, t, name)
                    model.openEntry = name
                    model.say("\(label): its API key is in its card below")
                }
            default:
                if !fields.isEmpty { card(nil, fields) }
            }
            if section.key != "verify" {
                ForEach(maps, id: \.key) { m in
                    MapList(model: model, section: section, field: m, offer: offer(section, m))
                }
            }
            ForEach(tables, id: \.key) { t in
                if t.kind == "tables" {
                    TableList(model: model, section: section, field: t)
                } else {
                    KeyedList(model: model, section: section, field: t)
                }
            }
        }
        .frame(maxWidth: 760, alignment: .leading)
    }

    func card(_ title: String?, _ fs: [SettingsField], in s: SettingsSection? = nil) -> some View {
        SettingsCard(title: title) {
            ForEach(Array(fs.enumerated()), id: \.element.key) { i, f in
                FieldRow(model: model, section: s ?? section, field: f, first: i == 0 && title == nil)
            }
        }
    }
}

/// One setting: its label and help at the left, and the control
/// settingsControl gives it at the right.
struct FieldRow: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField
    var first = false
    /// Where its value is: the section's table, or a row or entry of one
    /// of its tables.
    var place: SettingsPlace = .top

    @Environment(\.conchTheme) private var theme

    var value: JSONValue? { model.form?.value(section, field, at: place) }
    var saved: JSONValue? { model.form.map { SettingsForm($0.payload).value(section, field, at: place) } ?? nil }

    func set(_ v: JSONValue?) { withAnimation(.easeOut(duration: 0.15)) { model.form?.set(section, field, at: place, v) } }

    var control: SettingsControl { settingsControl(section, field) }

    var str: Binding<String> {
        Binding(get: { value?.string ?? "" }, set: { set($0.isEmpty ? nil : .string($0)) })
    }
    var num: Binding<Double?> {
        Binding(get: { value?.number }, set: { set($0.map(JSONValue.number)) })
    }
    var choice: Binding<String> {
        Binding(get: { value?.string ?? field.default?.string ?? "" }, set: { set(.string($0)) })
    }
    /// The choices, a value the schema does not list (a #rrggbb accent) kept.
    var options: [SettingsOption] {
        let o = field.options ?? []
        if let v = value?.string, !v.isEmpty, !o.contains(where: { $0.value == v }) { return o + [SettingsOption(value: v, label: v)] }
        return o
    }

    var body: some View {
        let _ = theme
        let c = control
        SettingsRow(label: field.label, help: field.help, changed: value != saved, first: first,
                    wide: c == .theme || c == .multiline || c == .iconSets,
                    below: c == .combo && settingsShowsPrompts(section, field)
                        ? AnyView(PromptPreview(model: model, theme: value?.string ?? "", compact: false)) : nil) {
            view(c)
        }
    }

    @ViewBuilder func view(_ c: SettingsControl) -> some View {
        switch c {
        case .toggle:
            ConchSwitch(isOn: Binding(get: { value?.bool ?? field.default?.bool ?? false }, set: { set(.bool($0)) }))
        case .segments:
            Segments(options: options, value: choice)
        case .menu(let agents):
            RichMenu(options: options, value: choice, agents: agents)
        case .stepper(let step):
            StepperField(value: num, step: step, field: field, fallback: Double(field.placeholder ?? "") ?? 0)
        case .slider(let lo, let hi, let step, let unit):
            SliderControl(value: num, min: lo, max: hi, step: step, unit: unit, fallback: Double(field.placeholder ?? "") ?? 100)
        case .swatches:
            Swatches(options: options, value: choice)
        case .pictures:
            LayoutPictures(options: options, value: choice)
        case .keys:
            KeyRecorder(value: str, placeholder: field.placeholder ?? "ctrl+b")
        case .time:
            CheckedField(control: c, field: field, text: str, icon: "clock", width: 120)
        case .url:
            CheckedField(control: c, field: field, text: str, icon: "globe", width: 300)
        case .envVar:
            CheckedField(control: c, field: field, text: str, icon: "key")
        case .secret:
            CheckedField(control: c, field: field, text: str, icon: "lock")
        case .chips:
            ChipsField(values: Binding(get: { value?.array?.compactMap(\.number) ?? [] },
                                       set: { set($0.isEmpty ? nil : .array($0.map(JSONValue.number))) }))
        case .theme:
            ThemeGrid(options: keepingSaved(field.options ?? [], value?.string ?? "", label: { ConchTheme.named($0).label }),
                      chosen: value?.string ?? "",
                      choose: { id in
                          set(.string(id))
                          // The theme saved already: nothing to save, so
                          // showing it is unpinning what the window holds.
                          // An unset key is its default: theme "conch", light_theme Off.
                          if let f = model.form, SettingsForm(f.payload).value(section, field)?.string ?? "" == id { model.store.chooseTheme("") }
                      }, preview: { model.store.previewTheme($0) })
        case .multiline:
            TextEditor(text: Binding(
                get: { field.kind == "text" ? value?.string ?? "" : listText(value) },
                set: { set(field.kind == "text" ? ($0.isEmpty ? nil : .string($0)) : listValue($0, ints: field.kind == "ints")) }))
                .font(.ui(size: 12, design: .monospaced)).frame(height: 64)
                .scrollContentBackground(.hidden).padding(6)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line2))
        case .text:
            CheckedField(control: c, field: field, text: str)
        case .iconSets:
            IconSetGrid(chosen: SettingsIconSet(rawValue: value?.string ?? "") ?? .lucide,
                        style: settingsIcons(model.form?.config["app"]?.object).style,
                        choose: { set($0 == .lucide ? nil : .string($0.rawValue)) },
                        hover: { model.hoverIcons = $0 })
        case .combo:
            let prompts = settingsShowsPrompts(section, field)
            ComboField(options: keepingSaved([SettingsOption(value: "", label: field.placeholder.map { "None: \($0)" } ?? "None")]
                                             + (field.suggest ?? []).map { SettingsOption(value: $0, label: $0) },
                                             str.wrappedValue, label: { $0 }),
                       text: str, placeholder: field.placeholder ?? "",
                       note: "\(field.suggest?.count ?? 0) installed here; any other name can be typed",
                       preview: prompts ? { theme in AnyView(PromptPreview(model: model, theme: theme, compact: true)) } : nil)
        case .model(let summary):
            let brain = model.form?.config["brain"]
            ComboField(options: modelOptions(model.models?.models ?? [], summary: summary, value: str.wrappedValue),
                       text: str, placeholder: field.placeholder ?? "", note: model.models?.error, loading: model.modelsLoading)
                .onAppear { model.loadModels(brain?.object) }
                .onChange(of: brain) { _, b in model.loadModels(b?.object) }
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

    /// Whether one of the choices is Off ("": light_theme's).
    var offable: Bool { options.contains { $0.value.isEmpty } }

    var body: some View {
        let _ = theme
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 220), spacing: 14)], alignment: .leading, spacing: 14) {
            ForEach(options, id: \.value) { o in
                let on = themeChosen(o.value, chosen, off: offable)
                Button { choose(o.value) } label: {
                    VStack(alignment: .leading, spacing: 6) {
                        Group {
                            if o.value.isEmpty { OffCard() } else { ThemePreview(theme: ConchTheme.named(o.value)) }
                        }
                            .frame(height: 96)
                            .clipShape(RoundedRectangle(cornerRadius: 7))
                            .overlay(RoundedRectangle(cornerRadius: 7).stroke(on ? Theme.accent : Theme.line, lineWidth: on ? 2 : 1))
                        HStack(spacing: 6) {
                            Text(o.label).font(.ui(size: 12, weight: on ? .semibold : .regular))
                                .foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1)
                            Spacer(minLength: 0)
                            if !o.value.isEmpty, ConchTheme.named(o.value).light { Text("light").font(.ui(size: 10.5)).foregroundStyle(Theme.muted) }
                            if on { LucideIcon("check", color: Theme.accent, size: 12) }
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(o.label)
                .onHover { if $0 { preview(o.value.isEmpty ? nil : o.value) } }
            }
        }
        .contentShape(Rectangle())
        // Between cards the last one stays shown; off the grid, none.
        .onHover { if !$0 { preview(nil) } }
        .onDisappear { preview(nil) }
    }
}

/// light_theme's Off: no picture of its own, since it is whichever theme
/// is chosen above, in light appearance as in dark.
struct OffCard: View {
    /// Read so the card is drawn again in a theme being previewed.
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        return VStack(spacing: 6) {
            HStack(spacing: 6) {
                LucideIcon("moon", color: Theme.text2, size: 14)
                Text("=").foregroundStyle(Theme.muted)
                LucideIcon("sun", color: Theme.text2, size: 14)
            }
            Text("The theme above, light or dark").font(.ui(size: 11)).foregroundStyle(Theme.muted)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.well)
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

    func show(section: String? = nil) {
        if let section {
            model.section = SettingsNav.page(section, in: model.form?.payload.schema.map(\.id) ?? ["git"])
        }
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

/// Settings → Skills & MCP servers: conch's own skills for the agents, and
/// the library of MCP servers and skills of yours, for one machine at a time
/// — both are written to the agents' folders on that machine, through its
/// server, not to config.toml.
struct SkillsSettingsPane: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .center, spacing: 12) {
                LucideIcon(SettingsNav.skillsIcon, color: Theme.accent, size: 19)
                    .frame(width: 38, height: 38)
                    .background(RoundedRectangle(cornerRadius: 10).fill(Theme.accentDeep.opacity(0.3)))
                VStack(alignment: .leading, spacing: 1) {
                    Text(SettingsNav.skillsTitle).font(.ui(size: 21, weight: .semibold))
                    Text("What the agents on \(store.label(store.skillsMachine)) load from your home. Nothing is written until you install or apply.")
                        .font(.ui(size: 12.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                }
            }
            if store.machines.count > 1 {
                HStack(spacing: 8) {
                    ForEach(store.machines, id: \.id) { m in
                        let on = m.id == store.skillsMachine
                        let online = store.connection(m.id) != nil
                        Button { store.skillsMachine = m.id } label: {
                            HStack(spacing: 7) {
                                Circle().fill(online ? Theme.ok : Theme.muted).frame(width: 7, height: 7)
                                Text(m.label).font(.ui(size: 12, weight: on ? .medium : .regular)).foregroundStyle(on ? Theme.text : Theme.text2)
                            }
                            .padding(.horizontal, 12).frame(height: 28)
                            .background(RoundedRectangle(cornerRadius: 8).fill(on ? Theme.accentDeep.opacity(0.3) : Theme.raise2))
                            .overlay(RoundedRectangle(cornerRadius: 8).stroke(on ? Theme.accent : Theme.line2))
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help(online ? m.label : "\(m.label) is not connected")
                    }
                }
            }
            AgentSkillPanel(store: store)
            LibraryPanel(store: store)
        }
        .frame(maxWidth: 760, alignment: .leading)
        // A machine that went from the list leaves this computer showing.
        .onAppear { if !store.machines.contains(where: { $0.id == store.skillsMachine }) { store.skillsMachine = "local" } }
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
