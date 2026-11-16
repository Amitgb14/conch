import AppKit
import Combine
import ConchKit
import SwiftUI

/// The Integrations window (Window → Integrations, ⌘K "Integrations"):
/// what conch talks to besides itself — built in, shipped, installed, and
/// what the registries list — with an integration's page and the sheet
/// that installs one (docs/plans/marketplace-mocks m1–m3). It asks conch
/// (`conch integration … -json`) rather than doing any of it twice, and
/// the server which projects use what (integration.detect). The logic is
/// ConchKit's (Integrations.swift); this draws it.
final class IntegrationsWindow {
    private var window: NSWindow?
    private let store: Store
    private var look: AnyCancellable?
    private var model: IntegrationsModel?

    init(store: Store) { self.store = store }

    @MainActor func show() {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 760),
                             styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = "Integrations"
            w.isReleasedWhenClosed = false
            w.minSize = NSSize(width: 820, height: 480)
            let m = IntegrationsModel(store: store)
            model = m
            w.contentView = NSHostingView(rootView: IntegrationsView(model: m, look: store.look))
            look = store.look.$theme.sink { [weak w] t in
                w?.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
                w?.backgroundColor = Theme.ns(t.chrome)
            }
            w.center()
            w.setFrameAutosaveName("conch.integrations")
            window = w
        }
        model?.load()
        window?.makeKeyAndOrderFront(nil)
    }

    /// The install sheet for one integration, from a branch's card for a
    /// red check conch cannot read: checked against that branch's checkout.
    @MainActor func install(_ name: String, checkIn: String?) {
        show()
        model?.connect(name, checkIn: checkIn)
    }

    /// The install sheet for one, at its key when it is installed: a
    /// project's sheet's Connect… and Install….
    @MainActor func connect(_ name: String) {
        show()
        model?.connect(name)
    }

    /// The window at a kind: a project's sheet's Browse….
    @MainActor func show(kind: IntegrationKind) {
        show()
        model?.page = nil
        model?.only = nil
        model?.section = .kind(kind.rawValue)
    }
}

/// Which list the sidebar shows.
enum IntegrationsSection: Hashable {
    /// A kind by its name: the registry's folders decide the kinds.
    case discover, installed, kind(String)
}

@MainActor
final class IntegrationsModel: ObservableObject {
    let store: Store
    @Published var here: IntegrationList?
    @Published var listed: [IntegrationListed] = []
    @Published var uses: [String: [IntegrationUse]] = [:]
    /// A project's checkout, by its name, to check a key against.
    var paths: [String: String] = [:]
    @Published var section: IntegrationsSection = .discover
    @Published var filter: IntegrationFilter = .all
    @Published var query = ""
    @Published var only: Set<String>?
    @Published var page: IntegrationInfo?
    @Published var flow: IntegrationInstallFlow?
    @Published var loading = false
    @Published var failure = ""
    @Published var registryNote = ""
    /// The registry's kinds and each integration's icon, as conch
    /// downloaded them (conch integration icons).
    @Published var icons: IntegrationIcons?

    init(store: Store) { self.store = store }

    var cards: [IntegrationCard] { IntegrationCatalog.cards(here: here, listed: listed, uses: uses) }

    /// The sidebar's kinds: the registry's, in its order.
    var kinds: [IntegrationKindInfo] { IntegrationCatalog.kinds(icons?.kinds, cards: cards) }

    /// An integration's icon, as a file here.
    func icon(_ name: String) -> String? { icons?.icons?[name] }

    var shown: [IntegrationCard] {
        var kind: String?
        var cs = cards
        switch section {
        case .discover: break
        case .installed: cs = cs.filter(\.installed)
        case .kind(let k): kind = k
        }
        if let o = only { cs = cs.filter { o.contains($0.name) } }
        return IntegrationCatalog.filter(cs, filter: filter, kind: kind, query: query)
    }

    func load() {
        guard !loading else { return }
        guard let bin = store.binary else {
            failure = "conch itself was not found, so the integrations cannot be read."
            return
        }
        loading = true
        Task { @MainActor in
            defer { loading = false }
            do {
                here = try JSONDecoder().decode(IntegrationList.self, from: try await runConch(bin, ["integration", "ls", "-json"]))
                failure = ""
            } catch {
                failure = "conch integration ls: \(error)"
            }
            do {
                listed = try JSONDecoder().decode([IntegrationListed]?.self, from: try await runConch(bin, ["integration", "search", "-json"])) ?? []
                registryNote = listed.isEmpty ? "The registries list nothing yet." : (listed.contains { $0.signed == false } ? "Some of the registries' indexes are unsigned." : "")
            } catch {
                listed = []
                registryNote = "The registries could not be read: \(error)"
            }
            await detect()
            // Icons last: the cards are already up, and draw them once here.
            if let out = try? await runConch(bin, ["integration", "icons", "-json"]),
               let got = try? JSONDecoder().decode(IntegrationIcons.self, from: out) {
                icons = got
            }
        }
    }

    /// Which local projects use what, from the server.
    private func detect() async {
        guard let c = store.connection("local"), c.hello?.capabilities?.contains("integration.detect.v1") == true else { return }
        var out: [String: [IntegrationUse]] = [:]
        for pr in store.snapshots["local"]?.projects ?? [] where pr.git == true {
            struct P: Encodable { var project_id: String }
            if let d = try? await c.call("integration.detect", P(project_id: pr.id), as: IntegrationDetect.self) {
                out[pr.name] = d.integrations.filter { $0.name != "github" && $0.name != "github-actions" }
                paths[pr.name] = pr.path
            }
        }
        uses = out
    }

    func open(_ card: IntegrationCard) {
        guard let bin = store.binary else { return }
        Task { @MainActor in
            do {
                page = try JSONDecoder().decode(IntegrationInfo.self, from: try await runConch(bin, ["integration", "info", "-json", card.name]))
            } catch {
                failure = "\(error)"
            }
        }
    }

    /// Opens one's page with its install sheet over it, at the key when
    /// it is installed already, checked against checkIn when given (a
    /// branch's card) or a project that uses it (a project's sheet).
    func connect(_ name: String, checkIn: String? = nil) {
        guard let bin = store.binary else {
            failure = "conch itself was not found, so the integrations cannot be read."
            return
        }
        Task { @MainActor in
            do {
                let info = try JSONDecoder().decode(IntegrationInfo.self, from: try await runConch(bin, ["integration", "info", "-json", name]))
                page = info
                guard info.revoked == nil else { return }
                var f = IntegrationInstallFlow(connecting: info)
                f.checkIn = uses.first(where: { $0.value.contains { $0.name == info.name } }).flatMap { paths[$0.key] }
                if let dir = checkIn, !dir.isEmpty { f.checkIn = dir }
                flow = f
            } catch {
                failure = "\(error)"
            }
        }
    }

    /// Reads the open page again: after an install, or the sheet closing,
    /// it said Install over a provider that was installed.
    func refreshPage() {
        guard let name = page?.name, let bin = store.binary else { return }
        Task { @MainActor in
            if let info = try? JSONDecoder().decode(IntegrationInfo.self, from: try await runConch(bin, ["integration", "info", "-json", name])),
               page?.name == name {
                page = info
            }
        }
    }

    func startInstall(_ info: IntegrationInfo) {
        var f = IntegrationInstallFlow(info: info)
        f.checkIn = uses.first(where: { $0.value.contains { $0.name == info.name } }).flatMap { paths[$0.key] }
        flow = f
    }

    func install() {
        guard var f = flow, f.blocker == nil, let bin = store.binary else { return }
        // A basic-auth user is not a secret, but lives where the key does,
        // so the server finds both the same way.
        if f.asksUser, let env = f.info.userEnv {
            do { try Keychain.keep(f.user.trimmingCharacters(in: .whitespaces), as: env) } catch {
                f.check = .failed("\(error)")
                flow = f
                return
            }
        }
        f.check = .running
        flow = f
        let input = Data((f.key.trimmingCharacters(in: .whitespacesAndNewlines) + "\n").utf8)
        Task { @MainActor in
            var done = f
            do {
                let out = try await runConch(bin, f.arguments, input: input)
                done.finished(output: String(decoding: out, as: UTF8.self), error: nil)
            } catch {
                done.finished(output: "", error: "\(error)")
            }
            done.key = "" // not kept a moment longer than the install
            flow = done
            loading = false
            load()
            refreshPage()
        }
    }
}

struct IntegrationsView: View {
    @ObservedObject var model: IntegrationsModel
    @ObservedObject var look: ShownTheme
    @Environment(\.conchTheme) private var theme
    @State private var adapting = false

    var body: some View {
        let _ = theme
        let _ = look.theme
        HStack(spacing: 0) {
            side.frame(width: 220)
            Divider()
            if let p = model.page {
                IntegrationPage(model: model, info: p)
            } else {
                discover
            }
        }
        .background(Theme.window)
        // No focus squares: AppKit drew one round whichever button took the
        // keyboard first (All, then Refresh), which read as chosen.
        .focusEffectDisabled()
        .sheet(isPresented: Binding(get: { model.flow != nil }, set: { if !$0 { model.flow = nil } }), onDismiss: { model.load(); model.refreshPage() }) {
            IntegrationInstallSheet(model: model)
        }
        .sheet(isPresented: $adapting) {
            NewIntegrationSheet(store: model.store) { model.load() }
        }
    }

    // MARK: the side

    var side: some View {
        let cards = model.cards
        return ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                item("Discover", .discover, nil)
                item("Installed", .installed, cards.filter(\.installed).count)
                heading("Kinds")
                ForEach(model.kinds, id: \.kind) { k in
                    let n = IntegrationCatalog.count(cards, kind: k.kind)
                    item(k.label, .kind(k.kind), nil, note: "\(n.installed) / \(n.listed)")
                }
                heading("Registry")
                Text("conch-integrations").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).padding(.horizontal, 9).padding(.vertical, 5)
                Button("+ Add a registry…") { model.store.openSettings() }
                    .buttonStyle(.plain).font(.ui(size: 12.5)).foregroundStyle(Theme.muted).padding(.horizontal, 9)
                heading("Make one")
                Button("✦ Adapt from API docs…") { adapting = true }
                    .buttonStyle(.plain).font(.ui(size: 12.5)).foregroundStyle(Theme.accent).padding(.horizontal, 9)
                    .help("An agent writes an integration file from a service's API docs; conch checks it with your key")
            }
            .padding(8)
        }
        .background(Theme.side)
    }

    func heading(_ s: String) -> some View {
        Text(s.uppercased()).font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted)
            .padding(.horizontal, 9).padding(.top, 12).padding(.bottom, 3)
    }

    func item(_ title: String, _ s: IntegrationsSection, _ n: Int?, note: String? = nil) -> some View {
        let on = model.section == s && model.page == nil
        return Button { model.section = s; model.page = nil; model.only = nil } label: {
            HStack {
                Text(title).lineLimit(1)
                Spacer()
                Text(note ?? n.map(String.init) ?? "").foregroundStyle(Theme.muted).font(.ui(size: 11))
            }
            .font(.ui(size: 12.5, weight: on ? .semibold : .regular))
            .foregroundStyle(on ? Theme.text : Theme.text2)
            .padding(.horizontal, 9).padding(.vertical, 5)
            .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accent.opacity(0.14) : .clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    // MARK: Discover

    var discover: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                TextField("Search integrations — circleci, gitlab, buildkite…", text: $model.query).textFieldStyle(.roundedBorder)
                ForEach(IntegrationFilter.allCases, id: \.self) { f in
                    Button(f.rawValue) { model.filter = f }
                        .buttonStyle(.plain).font(.ui(size: 11.5, weight: model.filter == f ? .semibold : .regular))
                        .foregroundStyle(model.filter == f ? Theme.accent : Theme.text2)
                        .padding(.horizontal, 9).padding(.vertical, 3)
                        .overlay(Capsule().stroke(model.filter == f ? Theme.accent : Theme.line))
                        // No focus ring: a square drawn round All read as a second choice.
                        .focusable(false)
                }
                if model.loading { ProgressView().controlSize(.small) }
                Button("Refresh") { model.load() }.disabled(model.loading).focusable(false)
            }
            let found = IntegrationCatalog.banner(model.uses)
            if !found.isEmpty {
                HStack(spacing: 8) {
                    Text("◎").foregroundStyle(Theme.accent)
                    (Text("Found in your projects: ").foregroundColor(Theme.accent).bold() + Text(found.joined(separator: ", ") + "."))
                        .font(.ui(size: 12.5)).lineLimit(2)
                    Spacer()
                    Button(model.only == nil ? "Show \(found.count == 2 ? "both" : found.count == 1 ? "it" : "them")" : "Show all") {
                        model.only = model.only == nil ? IntegrationCatalog.bannerNames(model.uses) : nil
                    }
                }
                .padding(10)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.accent.opacity(0.08)))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.accent.opacity(0.4)))
            }
            if !model.failure.isEmpty {
                Text(model.failure).font(.ui(size: 12)).foregroundStyle(Theme.warn).textSelection(.enabled)
            }
            if !model.registryNote.isEmpty {
                Text(model.registryNote).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    let shown = model.shown
                    if shown.isEmpty && !model.loading {
                        Text(IntegrationCatalog.emptyWords(filter: model.filter, query: model.query)).foregroundStyle(Theme.muted).padding(20)
                    }
                    ForEach(model.kinds, id: \.kind) { k in
                        let of = shown.filter { $0.kind == k.kind }
                        if !of.isEmpty {
                            (Text(k.label).bold() + Text("  " + (k.about ?? "")).foregroundColor(Theme.muted)).font(.ui(size: 12.5))
                            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 12), count: 3), spacing: 12) {
                                ForEach(of) { c in IntegrationCardView(card: c, icon: model.icon(c.name), newer: !k.known) { model.open(c) } }
                            }
                        }
                    }
                }
                .padding(.bottom, 16)
            }
        }
        .padding(14)
    }
}

/// A card: its mark, name, kind and source, what it does, and its chips.
struct IntegrationCardView: View {
    let card: IntegrationCard
    /// Its icon, as a file here.
    var icon: String?
    /// Of a kind this conch has no code for: shown, not installable.
    var newer = false
    let open: () -> Void
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        Button(action: open) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 9) {
                    IntegrationMark(label: card.label, icon: icon, size: 34)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(card.label).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                        Text("\(kindWord(card.kind)) · \(card.source)").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    }
                }
                Group {
                    if card.usedIn.isEmpty { Text(card.summary) } else { Text(card.summary + " Used in ") + Text(card.usedIn.joined(separator: ", ")).bold() + Text(".") }
                }
                .font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(3).frame(maxWidth: .infinity, minHeight: 46, alignment: .topLeading)
                HStack(spacing: 5) {
                    if newer {
                        chip("needs a newer conch", Theme.muted)
                    } else {
                        chip(card.standing.words, standingColor)
                        chip(card.auth, Theme.muted)
                    }
                    Spacer()
                    if !newer && (!card.installed || card.wanted) {
                        Text(card.installed ? "Connect" : "Install").font(.ui(size: 11.5, weight: .semibold))
                            .foregroundStyle(card.wanted ? Color.white : Theme.text)
                            .padding(.horizontal, 10).padding(.vertical, 3)
                            .background(RoundedRectangle(cornerRadius: 5).fill(card.wanted ? Theme.accent : Theme.raise))
                    }
                }
            }
            .padding(14)
            .background(RoundedRectangle(cornerRadius: 9).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(card.wanted ? Theme.accent : Theme.line))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    var standingColor: Color {
        switch card.standing {
        case .builtIn, .installed, .verified: return Theme.ok
        case .notChecked: return Theme.warn
        case .revoked: return Theme.err
        }
    }

    func chip(_ s: String, _ c: Color) -> some View {
        Text(s).font(.ui(size: 10.5, weight: .medium)).foregroundStyle(c)
            .padding(.horizontal, 6).padding(.vertical, 2)
            .overlay(Capsule().stroke(c.opacity(0.5)))
    }
}

func kindWord(_ kind: String) -> String {
    switch kind {
    case "ci": return "CI"
    case "host": return "Code host"
    case "tickets": return "Tickets"
    case "notify": return "Notify"
    case "sandbox": return "Sandbox"
    default: return kind
    }
}

/// A service's mark: its own icon, from the registry (conch integration
/// icons downloads and checks it), or its initial where it has none.
struct IntegrationMark: View {
    let label: String
    /// The icon's file here.
    let icon: String?
    let size: CGFloat
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        return RoundedRectangle(cornerRadius: size * 0.24).fill(Theme.raise)
            .frame(width: size, height: size)
            .overlay {
                if let img = IntegrationIconCache.image(icon) {
                    Image(nsImage: img).resizable().interpolation(.high).scaledToFit()
                        .frame(width: size, height: size)
                        .clipShape(RoundedRectangle(cornerRadius: size * 0.24))
                } else {
                    Text(String(label.prefix(1)).uppercased()).font(.ui(size: size * 0.45, weight: .bold)).foregroundStyle(Theme.text2)
                }
            }
    }
}

/// Icons read once: a card is drawn on every redraw of the grid. A file is
/// named by its hash, so a new icon is a new path.
@MainActor
enum IntegrationIconCache {
    private static var images: [String: NSImage] = [:]
    static func image(_ path: String?) -> NSImage? {
        guard let path else { return nil }
        if let i = images[path] { return i }
        guard let i = NSImage(contentsOfFile: path) else { return nil }
        images[path] = i
        return i
    }
}

/// An integration's page (mock 2).
struct IntegrationPage: View {
    @ObservedObject var model: IntegrationsModel
    let info: IntegrationInfo
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                Button("‹ Integrations") { model.page = nil }.buttonStyle(.plain).foregroundStyle(Theme.muted).font(.ui(size: 12))
                if let why = info.revoked {
                    Text("Revoked: \(why)").font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(.white)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(10)
                        .background(RoundedRectangle(cornerRadius: 7).fill(Theme.err))
                }
                HStack(alignment: .center, spacing: 12) {
                    IntegrationMark(label: info.label, icon: model.icon(info.name), size: 52)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(info.label).font(.ui(size: 20, weight: .semibold))
                        Text(subtitle).font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                    Spacer()
                    if let v = info.verifiedWords {
                        Text(v).font(.ui(size: 11)).foregroundStyle(Theme.ok).padding(.horizontal, 8).padding(.vertical, 3)
                            .overlay(Capsule().stroke(Theme.ok.opacity(0.5)))
                    }
                    if info.offersKey {
                        Button(info.installed ? (info.keySet ? "Change Key…" : "Connect…") : "Install") { model.startInstall(info) }
                            .keyboardShortcut(.defaultAction)
                    }
                }
                HStack(alignment: .top, spacing: 14) {
                    VStack(alignment: .leading, spacing: 14) {
                        box("What it does in conch") {
                            ForEach(Array(info.does.enumerated()), id: \.offset) { _, d in
                                HStack(alignment: .top, spacing: 9) {
                                    Text(mark(d.level)).foregroundStyle(markColor(d.level)).frame(width: 14)
                                    VStack(alignment: .leading, spacing: 1) {
                                        Text(d.what).font(.ui(size: 12.5, weight: .medium))
                                        Text(d.where).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                                    }
                                }
                                .padding(.vertical, 3)
                            }
                        }
                        if let help = info.keyHelp {
                            box("Setting it up") {
                                Text(help + " conch keeps it in your Keychain, never in a file.").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                                if let s = info.scopes, !s.isEmpty { Text("Scopes: " + s.joined(separator: " ")).font(.ui(size: 11.5)).foregroundStyle(Theme.muted) }
                            }
                        }
                        if let n = info.note {
                            Text(n).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        }
                    }
                    VStack(alignment: .leading, spacing: 14) {
                        box("Your key goes to") {
                            host(info.baseURL == "ask" ? "your own server" : info.hosts.api, "API")
                            ForEach(info.hosts.follow ?? [], id: \.self) { h in host(h, "logs, without your key") }
                            Text("Nowhere else. If an update adds a host, conch asks again before using it.").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        }
                        box("Access") {
                            row("key", "$" + info.keyEnv + (info.keySet ? " · set" : " · not set"))
                            if let u = info.userEnv { row("user", "$" + u) }
                            row("runs", "no programs — HTTP only")
                        }
                        box("About") {
                            if let by = info.adaptedBy { row("written by", by + (info.adapted.map { ", " + $0 } ?? "")) }
                            if let r = info.registry { row("registry", r + (info.signed == false ? " (unsigned)" : "")) }
                            if let n = info.installs { row("installs", "\(n)") }
                            if let h = info.shortHash { row("sha256", h) }
                            if let d = info.docs, let u = URL(string: d) { Link("API docs", destination: u).font(.ui(size: 11.5)) }
                        }
                    }
                    .frame(width: 300)
                }
            }
            .padding(18)
        }
    }

    var subtitle: String {
        var parts = [kindWord(info.kind), info.source]
        if let v = info.version { parts.append("v\(v)") }
        return parts.joined(separator: " · ")
    }

    func mark(_ level: String) -> String { ["yes": "✓", "partly": "◐", "no": "✗"][level] ?? "—" }
    func markColor(_ level: String) -> Color { level == "yes" ? Theme.ok : level == "partly" ? Theme.warn : Theme.muted }

    func box<C: View>(_ title: String, @ViewBuilder _ content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title.uppercased()).font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted)
            content()
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
    }

    func host(_ h: String, _ role: String) -> some View {
        HStack {
            Circle().fill(Theme.ok).frame(width: 7, height: 7)
            Text(h).font(.system(size: 12, design: .monospaced))
            Spacer()
            Text(role).font(.ui(size: 11)).foregroundStyle(Theme.muted)
        }
    }

    func row(_ k: String, _ v: String) -> some View {
        HStack(alignment: .top) {
            Text(k).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).frame(width: 80, alignment: .leading)
            Text(v).font(.ui(size: 11.5)).textSelection(.enabled)
        }
    }
}

/// Install and connect (mock 3): agree to the hosts, give the key, check
/// it against a project that uses it.
struct IntegrationInstallSheet: View {
    @ObservedObject var model: IntegrationsModel
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let f = model.flow ?? IntegrationInstallFlow(info: IntegrationInfo.placeholder)
        let bind = { (kp: WritableKeyPath<IntegrationInstallFlow, String>) in
            Binding(get: { model.flow?[keyPath: kp] ?? "" }, set: { model.flow?[keyPath: kp] = $0 })
        }
        return VStack(alignment: .leading, spacing: 12) {
            Text(f.info.installed ? "Connect \(f.info.label)" : "Install \(f.info.label)").font(.ui(size: 15, weight: .semibold))
            Text(f.info.registry.map { "From \($0)" + (f.info.signed == true ? " · checked against its sha256" : " · its index is unsigned") } ?? "Ships with conch")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            Divider()
            step(1, done: f.agreed) {
                if f.asksBase {
                    TextField("https://jenkins.example.com", text: bind(\.base)).textFieldStyle(.roundedBorder)
                }
                Toggle(f.hostsWords, isOn: Binding(get: { model.flow?.agreed ?? false }, set: { model.flow?.agreed = $0 }))
                    .font(.ui(size: 12.5))
            }
            step(2, done: !f.key.isEmpty || f.info.keySet) {
                if f.asksUser {
                    TextField("User name ($\(f.info.userEnv ?? ""))", text: bind(\.user)).textFieldStyle(.roundedBorder)
                }
                SecureField(f.info.keySet ? "Set already — type to replace it" : "\(f.info.label) key ($\(f.info.keyEnv))", text: bind(\.key))
                    .textFieldStyle(.roundedBorder)
                Text("🔑 Kept in the Keychain, or read from $\(f.info.keyEnv)").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
            step(3, done: { if case .ok = f.check { return true }; return false }()) {
                switch f.check {
                case .idle: Text(f.checkIn == nil ? "Checked when a project uses it." : "One read-only call, against \((f.checkIn! as NSString).lastPathComponent).")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                case .running: HStack { ProgressView().controlSize(.small); Text("Checking…").font(.ui(size: 12)) }
                case .ok(let s): Text("Checked: " + s).font(.ui(size: 12)).foregroundStyle(Theme.ok).textSelection(.enabled)
                case .failed(let s): Text(s).font(.ui(size: 12)).foregroundStyle(Theme.err).textSelection(.enabled)
                }
            }
            HStack {
                if let b = f.blocker, f.check == .idle { Text(b).font(.ui(size: 11.5)).foregroundStyle(Theme.muted) }
                Spacer()
                Button({ if case .ok = f.check { return "Done" }; return "Cancel" }()) { model.flow = nil }
                    .keyboardShortcut(.cancelAction)
                if !{ if case .ok = f.check { return true }; return false }() {
                    Button(f.info.installed ? "Connect" : "Install") { model.install() }
                        .keyboardShortcut(.defaultAction).disabled(f.blocker != nil)
                }
            }
        }
        .padding(20)
        .frame(width: 520)
    }

    func step<C: View>(_ n: Int, done: Bool, @ViewBuilder _ content: () -> C) -> some View {
        HStack(alignment: .top, spacing: 10) {
            ZStack {
                Circle().fill(done ? Theme.ok : Theme.raise).frame(width: 20, height: 20)
                Text(done ? "✓" : "\(n)").font(.ui(size: 11, weight: .bold)).foregroundStyle(done ? Color.white : Theme.text2)
            }
            VStack(alignment: .leading, spacing: 6) { content() }
        }
    }
}

extension IntegrationInfo {
    /// What a sheet draws for the moment it closes and no flow is left.
    static let placeholder = try! JSONDecoder().decode(IntegrationInfo.self, from: Data(
        #"{"name":"","label":"","kind":"ci","source":"","installed":false,"does":[],"hosts":{"api":""},"key_env":"","key_set":false}"#.utf8))
}
