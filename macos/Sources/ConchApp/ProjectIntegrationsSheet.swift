import AppKit
import Combine
import ConchKit
import SwiftUI

/// A project's integrations (mock 5): right-click a project → Integrations….
/// One row per kind from the server's integration.detect, each with its
/// state and what to do — Connect… opens the Integrations window's install
/// sheet at the key, Install… its install, Browse… the window at the kind —
/// and the checkbox that saves the names in .conch/integrations.toml for
/// the team. The rows are ConchKit's (ProjectIntegrations.swift).
@MainActor
final class ProjectIntegrationsWindow {
    private var windows: [String: NSWindow] = [:]
    private var looks: [String: AnyCancellable] = [:]
    private var closing: [String: NSObjectProtocol] = [:]
    private let integrations: () -> IntegrationsWindow?

    init(integrations: @escaping () -> IntegrationsWindow?) { self.integrations = integrations }

    /// The project's sheet, by its key (machine|id) and the window's store
    /// it was opened from, one window each. A closed sheet is forgotten:
    /// kept, its model held the Store of a window since closed, said "Not
    /// connected" for ever and kept that Store alive.
    func show(project pk: String, store: Store) {
        let id = "\(ObjectIdentifier(store).hashValue)#" + pk
        if let w = windows[id] {
            (w.contentView as? NSHostingView<ProjectIntegrationsView>)?.rootView.model.load()
            w.makeKeyAndOrderFront(nil)
            return
        }
        let (m, pid) = unkey(pk)
        let name = store.snapshots[m]?.projects.first { $0.id == pid }?.name ?? pid
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 760, height: 440),
                         styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        w.title = "\(name) — Integrations"
        w.isReleasedWhenClosed = false
        let model = ProjectIntegrationsModel(store: store, machine: m, project: pid, integrations: integrations)
        model.close = { [weak w] in w?.close() }
        w.contentView = NSHostingView(rootView: ProjectIntegrationsView(model: model, look: store.look))
        looks[id] = store.look.$theme.sink { [weak w] t in
            w?.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
            w?.backgroundColor = Theme.ns(t.chrome)
        }
        closing[id] = NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.forget(id) }
        }
        w.center()
        windows[id] = w
        model.load()
        w.makeKeyAndOrderFront(nil)
    }

    /// Drops a closed sheet's window, model and the Store it held.
    private func forget(_ id: String) {
        windows[id]?.contentView = nil
        windows[id] = nil
        looks[id] = nil
        if let o = closing.removeValue(forKey: id) { NotificationCenter.default.removeObserver(o) }
    }
}

@MainActor
final class ProjectIntegrationsModel: ObservableObject {
    let store: Store
    let machine: String
    let project: String
    let integrations: () -> IntegrationsWindow?
    var close: () -> Void = {}
    @Published var detect: IntegrationDetect?
    @Published var failure = ""
    @Published var loading = false
    /// The team file's question, once per opening.
    @Published var asking: String?
    /// What this Mac has installed (conch integration ls), for the deploy
    /// switches.
    @Published var installed: IntegrationList?
    private var asked = false

    init(store: Store, machine: String, project: String, integrations: @escaping () -> IntegrationsWindow?) {
        self.store = store; self.machine = machine; self.project = project; self.integrations = integrations
    }

    var conn: Connection? { store.connection(machine) }
    func can(_ cap: String) -> Bool { conn?.hello?.capabilities?.contains(cap) == true }
    /// A key is given where the server runs, and the app gives it through
    /// this Mac's conch.
    var here: Bool { machine == "local" }

    struct P: Encodable { var project_id: String }
    struct T: Encodable { var project_id: String; var action: String; var names: [String]? }

    func load() {
        guard let c = conn else { failure = "Not connected to that machine."; return }
        guard can("integration.detect.v1") else {
            failure = "The conch server there is from an older build: update it to see a project's integrations."
            return
        }
        loading = true
        Task { @MainActor in
            defer { loading = false }
            if here, installed == nil, let b = store.binary,
               let out = try? await runConch(b, ["integration", "ls", "-json"]) {
                installed = try? JSONDecoder().decode(IntegrationList.self, from: out)
            }
            do {
                let d = try await c.call("integration.detect", P(project_id: project), as: IntegrationDetect.self)
                detect = d
                failure = ""
                if !asked, can("integration.team.v1"), let q = ProjectIntegrations.question(d) {
                    asked = true
                    asking = q
                }
            } catch {
                failure = "\(error)"
            }
        }
    }

    /// Answers the team file, or writes or removes it.
    func team(_ action: String, names: [String]? = nil) {
        guard let c = conn else { return }
        Task { @MainActor in
            do {
                detect = try await c.call("integration.team", T(project_id: project, action: action, names: names), as: IntegrationDetect.self)
                failure = ""
            } catch {
                failure = "\(error)"
            }
        }
    }

    /// Turns a deploy integration on or off for this project
    /// (`conch integration use`, kept in config.toml), then reads it again.
    func use(_ sw: DeploySwitch, on: Bool) {
        guard here, let b = store.binary, let dir = store.snapshots[machine]?.project(project)?.path else { return }
        Task { @MainActor in
            do {
                _ = try await runConch(b, ProjectIntegrations.useArguments(name: sw.name, on: on, dir: dir))
                load()
            } catch {
                failure = "\(error)"
            }
        }
    }

    func perform(_ a: ProjectIntegrationAction) {
        guard let win = integrations() else { return }
        switch a {
        case .connect(let name, _): win.connect(name)
        case .install(let name, _): win.connect(name)
        case .browse(let k): win.show(kind: k)
        }
    }
}

struct ProjectIntegrationsView: View {
    @ObservedObject var model: ProjectIntegrationsModel
    @ObservedObject var look: ShownTheme
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let _ = look.theme
        let rows = ProjectIntegrations.rows(model.detect)
        VStack(alignment: .leading, spacing: 14) {
            VStack(spacing: 0) {
                ForEach(Array(rows.enumerated()), id: \.element.id) { i, r in
                    if i > 0 { Divider() }
                    row(r)
                }
            }
            .padding(.horizontal, 12)
            .background(RoundedRectangle(cornerRadius: 9).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
            let switches = ProjectIntegrations.deploySwitches(model.detect, installed: model.installed)
            if !switches.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("DEPLOY PREVIEWS FOR THIS PROJECT").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted)
                    ForEach(switches) { sw in
                        HStack(spacing: 8) {
                            Toggle(sw.words, isOn: Binding(get: { sw.on }, set: { model.use(sw, on: $0) }))
                                .toggleStyle(.switch).controlSize(.small).font(.ui(size: 12.5))
                                .disabled(sw.fixed != nil || !model.here)
                            if let f = sw.fixed { Text(f).font(.ui(size: 11.5)).foregroundStyle(Theme.muted) }
                        }
                        .help(sw.fixed != nil ? "Its files or checks show it; remove them to stop."
                              : "For a service that links this repository from its own side, with no file conch can find. Kept in config.toml, [integrations.projects].")
                    }
                }
            }
            if !model.failure.isEmpty {
                Text(model.failure).font(.ui(size: 12)).foregroundStyle(Theme.warn).textSelection(.enabled)
            }
            if let p = model.detect?.team?.problem {
                Text("\(model.detect?.team?.path ?? ".conch/integrations.toml"): \(p)").font(.ui(size: 11.5)).foregroundStyle(Theme.err).textSelection(.enabled)
            }
            HStack {
                if model.can("integration.team.v1") {
                    Toggle("Save in .conch/integrations.toml for the Team", isOn: Binding(
                        get: { ProjectIntegrations.saved(model.detect) },
                        set: { on in model.team(on ? "save" : "remove", names: on ? ProjectIntegrations.teamNames(model.detect) : nil) }))
                        .toggleStyle(.checkbox).font(.ui(size: 12))
                        .disabled(model.detect == nil || (ProjectIntegrations.teamNames(model.detect).isEmpty && !ProjectIntegrations.saved(model.detect)))
                        .help("Names only — every key stays in each person's Keychain.")
                }
                if model.loading { ProgressView().controlSize(.small) }
                Spacer()
                Button("Refresh") { model.load() }
                Button("Done") { model.close() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18)
        .frame(minWidth: 620, minHeight: 360, alignment: .top)
        .background(Theme.window)
        .noFocusRings()
        .alert(model.asking ?? "", isPresented: Binding(get: { model.asking != nil }, set: { if !$0 { model.asking = nil } })) {
            Button("Use It") { model.team("use"); model.asking = nil }
            Button("Not Now", role: .cancel) { model.team("ignore"); model.asking = nil }
        } message: {
            Text("It names integrations only; your keys stay yours. conch asks again only when it names something new.")
        }
    }

    func row(_ r: ProjectIntegrationRow) -> some View {
        HStack(alignment: .center, spacing: 10) {
            Text(icon(r.kind)).font(.ui(size: 12)).foregroundStyle(Theme.text2).frame(width: 16)
            VStack(alignment: .leading, spacing: 2) {
                (Text(r.title).bold() + Text(" · " + r.names)).font(.ui(size: 13)).foregroundStyle(Theme.text)
                Text(r.detail).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(2)
            }
            Spacer()
            ForEach(Array(r.chips.enumerated()), id: \.offset) { _, c in
                Text(c.words).font(.ui(size: 10.5, weight: .medium)).foregroundStyle(color(c))
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .overlay(Capsule().stroke(color(c).opacity(0.6)))
            }
            ForEach(Array(r.actions.enumerated()), id: \.offset) { _, a in
                if model.here || a == .browse(r.kind) {
                    Button(a.words) { model.perform(a) }
                }
            }
        }
        .padding(.vertical, 10)
    }

    func icon(_ k: IntegrationKind) -> String {
        switch k {
        case .host: return "◉"
        case .ci: return "⚙"
        case .tickets: return "⚑"
        case .sandbox: return "▣"
        case .notify: return "✉"
        case .deploy: return "▲"
        }
    }

    func color(_ c: ProjectIntegrationChip) -> Color {
        switch c {
        case .connected: return Theme.ok
        case .keyNotSet: return Theme.err
        case .notInstalled: return Theme.warn
        case .setUp: return Theme.muted
        }
    }
}
