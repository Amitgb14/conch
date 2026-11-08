import AppKit
import ConchKit
import CoreImage.CIFilterBuiltins
import SwiftUI

// The status bar's other buttons, as the TUI's ends: ✦ Ask (the brain
// bar), ⊙ for reporting a problem, and pairing a phone — each a popover
// over the conch command the TUI's button stands for.

/// The buttons, side by side.
struct StatusExtras: View {
    @ObservedObject var store: Store
    @State private var reporting = false
    /// When the pair popover last closed: a click on its button that
    /// closed it must not open a new one once the code has been made.
    @State private var pairClosed = Date.distantPast

    var body: some View {
        HStack(spacing: 10) {
            Button { store.askOpen.toggle() } label: {
                Text("✦ Ask").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.accent)
            }
            .buttonStyle(.plain).help("Ask conch in plain words — it plans, and runs nothing until you confirm")
            .barPopover(isPresented: $store.askOpen) { AskPopover(store: store) }
            Button { reporting.toggle() } label: {
                Text("⊙").font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.muted)
            }
            .buttonStyle(.plain).help("Report a problem: the facts about this conch, for a GitHub issue")
            .barPopover(isPresented: $reporting) { ReportPopover(store: store) }
            Button {
                if store.pairOpen { store.pairOpen = false } else if Date().timeIntervalSince(pairClosed) > 0.3 { store.openPair() }
            } label: {
                PhoneIconLabel(icon: store.phones?.icon ?? .idle)
            }
            .buttonStyle(.plain).help(store.phones?.tooltip ?? "Pair a phone, to see and answer your agents from it")
            .barPopover(isPresented: $store.pairOpen) { PairPopover(store: store) { store.pairOpen = false } }
            .onChange(of: store.pairOpen) { if !$0 { pairClosed = Date() } }
        }
        .disabled(store.binary == nil)
    }
}

// MARK: ✦ Ask

@MainActor
final class AskModel: ObservableObject {
    @Published var request = "" { didSet { if request != oldValue { pick = nil } } }
    @Published var output = ""
    @Published var state: AskSession.State?
    @Published var fast = false
    /// What conch suggests asking now (`conch ask -suggest`); nil until
    /// it answers, and for good from a conch too old to.
    @Published var suggested: AskSuggestions?
    /// The highlighted option; nil is the request as typed.
    @Published var pick: Int?
    private var session: AskSession?
    private static let historyKey = "askHistory"
    private var history: [String] { UserDefaults.standard.stringArray(forKey: Self.historyKey) ?? [] }

    /// The options left by what is typed.
    var shown: [AskSuggestion] { askMatching(suggested?.suggestions ?? [], typed: request) }

    func suggest(binary: String, pane: String?) {
        let args = askSuggestArguments(pane: pane, history: history)
        Task { @MainActor in
            guard let out = try? await runConch(binary, args) else { return } // an older conch has no -suggest
            suggested = try? conchDecoder().decode(AskSuggestions.self, from: out)
        }
    }

    func ask(binary: String, pane: String?) {
        let r = request.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !r.isEmpty else { return }
        UserDefaults.standard.set(askRemember(history, r), forKey: Self.historyKey)
        session?.cancel()
        let s = AskSession(binary: binary, request: r, pane: pane)
        session = s
        output = ""
        fast = false
        state = .thinking
        s.onChange = { [weak self, weak s] in
            guard let self, let s, s === self.session else { return }
            self.output = s.output
            self.state = s.state
            self.fast = s.fast
        }
        s.start()
    }

    /// Enter: the highlighted option, else what is typed. One to finish
    /// goes in the box instead of being asked.
    func submit(binary: String, pane: String?) {
        if let i = pick, i < shown.count {
            choose(shown[i], binary: binary, pane: pane)
        } else {
            ask(binary: binary, pane: pane)
        }
    }

    func choose(_ o: AskSuggestion, binary: String, pane: String?) {
        request = o.stem
        pick = nil
        if !o.fill { ask(binary: binary, pane: pane) }
    }

    /// ↑↓ through the options; up from the first is back to the request.
    func move(_ by: Int) {
        let n = shown.count
        guard n > 0 else { return }
        let next = (pick ?? -1) + by
        pick = next < 0 ? nil : min(next, n - 1)
    }

    /// Tab: the highlighted option, or the first, in the box to finish.
    func fillIn() {
        let opts = shown
        guard !opts.isEmpty else { return }
        request = opts[min(pick ?? 0, opts.count - 1)].stem
        pick = nil
    }

    /// Back to typing a new request, the plan before it put away.
    func again() {
        session?.cancel()
        session = nil
        state = nil
        output = ""
    }

    func confirm(_ yes: Bool) { session?.confirm(yes) }
    func stop() { session?.cancel() }
}

struct AskPopover: View {
    @ObservedObject var store: Store
    @StateObject private var model = AskModel()
    @FocusState private var typing: Bool

    /// The pane in front, when it is this computer's: `conch ask` speaks
    /// to this computer's server, and "this" means that pane.
    private var pane: String? {
        guard let k = store.layout.focused else { return nil }
        let (m, id) = unkey(k)
        return m == "local" ? id : nil
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            field
            if let s = model.suggested { context(s) }
            switch model.state {
            case nil:
                options
            case .thinking?:
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("Thinking with \(model.suggested?.provider ?? "the model")…").foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            case .failed(let why)?:
                Text(why).foregroundStyle(Theme.err).font(.ui(size: 12)).textSelection(.enabled)
                options
            default:
                if model.output.isEmpty { Spacer(minLength: 0) }
            }
            if !model.output.isEmpty { result }
            footer
        }
        // A fixed size: the suggestions arrive after the popover opens,
        // and a popover does not grow to fit what arrives later — the list
        // was cut off. What does not fit scrolls.
        .padding(16).frame(width: 620, height: 470, alignment: .top)
        .onAppear {
            typing = true
            if let b = store.binary { model.suggest(binary: b, pane: pane) }
        }
        .onDisappear { if model.state == .thinking { model.stop() } }
    }

    /// Enter in the box asks, unless a plan is already in front or on
    /// its way: then it is the plan's buttons that answer.
    private func submit() {
        guard let b = store.binary else { return }
        switch model.state {
        case .thinking?, .plan?, .running?: return
        default: model.submit(binary: b, pane: pane)
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            Text("✦ Ask conch").font(.ui(size: 15, weight: .semibold))
            Spacer()
            if let p = model.suggested?.provider, !p.isEmpty {
                Text(model.suggested?.fast == false ? "\(p) · every request" : "\(p) · simple requests answered at once")
                    .font(.ui(size: 11)).foregroundStyle(.secondary)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(Capsule().fill(Theme.well))
            }
        }
    }

    private var field: some View {
        HStack(spacing: 8) {
            Text("✦").foregroundStyle(Theme.accent).font(.ui(size: 15, weight: .semibold))
            TextField("Say what you want, or pick one below", text: $model.request)
                .textFieldStyle(.plain).font(.ui(size: 14))
                .focused($typing)
                .onSubmit { submit() }
                .onKeyPress(.downArrow) { model.move(1); return .handled }
                .onKeyPress(.upArrow) { model.move(-1); return .handled }
                .onKeyPress(.tab) { model.fillIn(); return .handled }
            Button("Ask") { submit() }
                .disabled(model.request.trimmingCharacters(in: .whitespaces).isEmpty && model.pick == nil || model.state == .thinking)
        }
        .padding(.horizontal, 10).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(typing ? Theme.accent.opacity(0.6) : Theme.line, lineWidth: 1))
    }

    private func context(_ s: AskSuggestions) -> some View {
        Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 3) {
            GridRow {
                Text("Context").foregroundStyle(.secondary)
                Text(s.context.flatMap { $0.isEmpty ? nil : $0 } ?? "this computer").lineLimit(1).truncationMode(.middle)
            }
            GridRow {
                Text("Agents").foregroundStyle(.secondary)
                Text(s.summary ?? "")
            }
        }
        .font(.ui(size: 11.5))
    }

    @ViewBuilder private var options: some View {
        let opts = model.shown
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(opts.enumerated()), id: \.element) { i, o in
                    if i == 0 || opts[i - 1].isRecent != o.isRecent {
                        Text(o.isRecent ? "RECENT" : "SUGGESTED")
                            .font(.ui(size: 10, weight: .semibold)).foregroundStyle(.secondary)
                            .padding(.top, i == 0 ? 0 : 8).padding(.bottom, 2)
                    }
                    optionRow(o, lit: model.pick == i)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxHeight: .infinity)
    }

    private func optionRow(_ o: AskSuggestion, lit: Bool) -> some View {
        Button {
            if let b = store.binary { model.choose(o, binary: b, pane: pane) }
        } label: {
            HStack(spacing: 8) {
                Image(systemName: o.isRecent ? "clock.arrow.circlepath" : (o.fill ? "square.and.pencil" : "sparkle"))
                    .font(.ui(size: 11)).foregroundStyle(lit ? Theme.accent : Theme.muted).frame(width: 16)
                Text(o.text).font(.ui(size: 13)).lineLimit(1).truncationMode(.tail)
                Spacer(minLength: 12)
                if let n = o.note, !n.isEmpty {
                    Text(n).font(.ui(size: 11)).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .contentShape(Rectangle())
            .background(RoundedRectangle(cornerRadius: 6).fill(lit ? Theme.accent.opacity(0.16) : Color.clear))
        }
        .buttonStyle(.plain)
        .help(o.fill ? "Put it in the box to finish" : "Ask this")
    }

    @ViewBuilder private var result: some View {
        if model.fast {
            Text("Answered without the model").font(.ui(size: 11)).foregroundStyle(.secondary)
        }
        ScrollView {
            Text(model.output).font(.ui(size: 12, design: .monospaced)).textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxHeight: .infinity)
        .padding(8).background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
    }

    @ViewBuilder private var footer: some View {
        switch model.state {
        case .plan(let n)?:
            HStack {
                Text("Nothing has run yet.").font(.ui(size: 11)).foregroundStyle(.secondary)
                Spacer()
                Button("Don't run") { model.confirm(false) }
                Button("Run \(n) action\(n == 1 ? "" : "s")") { model.confirm(true) }.keyboardShortcut(.defaultAction)
            }
        case .running?:
            HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Running…").foregroundStyle(.secondary) }
        case .done?:
            HStack {
                Spacer()
                Button("Ask something else") {
                    model.again()
                    model.request = ""
                    typing = true
                    // What was just asked is now among the recent.
                    if let b = store.binary { model.suggest(binary: b, pane: pane) }
                }
            }
        default:
            Text("↑↓ choose · ↩ ask · ⇥ fill in · esc close — nothing runs until you confirm")
                .font(.ui(size: 11)).foregroundStyle(.secondary)
        }
    }
}

// MARK: ⊙ report a problem

struct ReportPopover: View {
    @ObservedObject var store: Store
    @State private var what = ""
    @State private var report = ""
    @State private var error = ""
    @State private var opened = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Report a problem").font(.ui(size: 13, weight: .semibold))
            Text("The facts about this conch — versions, counts and the last error, never a path, a project or a screen. Nothing is sent: a GitHub issue opens with them, for you to read before you submit it.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            TextField("What happened (optional)", text: $what).textFieldStyle(.roundedBorder)
            if !report.isEmpty {
                ScrollView {
                    Text(report).font(.ui(size: 11.5, design: .monospaced)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(minHeight: 120, maxHeight: 220) // in a popover, a scroll view takes no height it is not given
            }
            if !error.isEmpty { Text(error).foregroundStyle(Theme.err).font(.ui(size: 12)) }
            HStack {
                if opened { Text("Opened in your browser.").foregroundStyle(.secondary).font(.ui(size: 12)) }
                Spacer()
                Button("Show the facts") { run(open: false) }
                Button("Open a GitHub issue") { run(open: true) }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(14).frame(width: 480)
        .onAppear { run(open: false) }
    }

    func run(open: Bool) {
        guard let b = store.binary else { return }
        let words = what.trimmingCharacters(in: .whitespacesAndNewlines)
        var args = ["bug"]
        if open { args.append("-open") }
        if !words.isEmpty { args.append(words) }
        Task {
            do {
                let out = try await runConch(b, args)
                if open { opened = true } else { report = String(decoding: out, as: UTF8.self) }
                error = ""
            } catch {
                self.error = "\(error)"
            }
        }
    }
}

// MARK: pair a phone

/// A popover AppKit places, from the view it is attached to. SwiftUI's
/// own placed the status bar's popovers for the size they opened at and
/// then let them grow downward as their content arrived — the pair, the
/// report and the monitor opened over their button and off the bottom of
/// the screen. NSPopover is shown relative to the real view, stays
/// attached to it as it grows, and keeps itself on screen.
struct AppKitPopover<Content: View>: NSViewRepresentable {
    @Binding var isPresented: Bool
    @ViewBuilder var content: () -> Content
    @Environment(\.conchTheme) private var theme

    final class Coordinator: NSObject, NSPopoverDelegate {
        var popover: NSPopover?
        var host: NSHostingController<AnyView>?
        var dismissed: (() -> Void)?
        /// When it last closed: a click on its own button closes a
        /// transient popover on the way down, and the button's toggle
        /// would open it again on the way up.
        var closedAt = Date.distantPast
        func popoverDidClose(_ notification: Notification) {
            popover = nil
            host = nil
            closedAt = Date()
            dismissed?()
        }
    }

    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ view: NSView, context: Context) {
        let c = context.coordinator
        c.dismissed = { [binding = $isPresented] in if binding.wrappedValue { binding.wrappedValue = false } }
        let root = AnyView(content().environment(\.conchTheme, theme))
        if isPresented, c.popover == nil {
            guard view.window != nil, Date().timeIntervalSince(c.closedAt) > 0.3 else {
                DispatchQueue.main.async { [binding = $isPresented] in binding.wrappedValue = false }
                return
            }
            let p = NSPopover()
            p.behavior = .transient
            p.animates = true
            p.delegate = c
            let host = NSHostingController(rootView: root)
            host.sizingOptions = .preferredContentSize
            p.contentViewController = host
            c.popover = p
            c.host = host
            p.show(relativeTo: view.bounds, of: view, preferredEdge: .maxY)
            // Its window takes the keys, so a field that asks for focus as
            // it appears (✦ Ask's) has it: SwiftUI's popover did this itself.
            host.view.window?.makeKey()
        } else if isPresented, let host = c.host {
            host.rootView = root
        } else if !isPresented, let p = c.popover {
            c.popover = nil
            c.host = nil
            p.close()
        }
    }
}

extension View {
    /// The status bar's popovers: AppKit's, above the view they belong to.
    func barPopover<Content: View>(isPresented: Binding<Bool>, @ViewBuilder content: @escaping () -> Content) -> some View {
        background(AppKitPopover(isPresented: isPresented, content: content))
    }
}

/// The phone icon in the status bar: quiet, teal with a count while
/// phones are on, amber while conch web is off its relay.
struct PhoneIconLabel: View {
    let icon: PhoneIcon
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        return HStack(spacing: 3) {
            switch icon {
            case .idle:
                LucideIcon("smartphone", color: Theme.muted, size: 13)
            case .relayDown:
                LucideIcon("smartphone", color: Theme.warn, size: 13)
                Circle().fill(Theme.warn).frame(width: 5, height: 5)
            case .on(let n):
                LucideIcon("smartphone", color: Theme.accent, size: 13)
                Text("\(n)").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.accent)
            }
        }
        .padding(.horizontal, 4).padding(.vertical, 3).contentShape(Rectangle())
    }
}

struct PairPopover: View {
    @ObservedObject var store: Store
    var close: () -> Void = {}
    @State private var info: PairInfo?
    @State private var permission = "full"
    @State private var error = ""
    @State private var starting = false
    @State private var showPhones = false
    /// The pair page's height, which the phones page keeps: a popover is
    /// placed for the size it opens at, and one that changes size grows
    /// off its edge rather than moving.
    @State private var pageHeight: CGFloat = 0

    var body: some View {
        Group {
            if showPhones {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Button { showPhones = false } label: {
                            Label("Pair a phone", systemImage: "chevron.left").font(.ui(size: 12))
                        }
                        .buttonStyle(.link)
                        Spacer()
                    }
                    Text("Paired phones").font(.ui(size: 15, weight: .semibold))
                    ScrollView { PhonesList(store: store) }
                    Spacer(minLength: 0)
                    HStack { Spacer(); Button("Done") { close() }.keyboardShortcut(.defaultAction) }
                }
                .frame(height: pageHeight > 0 ? pageHeight : nil, alignment: .top)
            } else {
                pairPage.background(GeometryReader { g in
                    Color.clear.onAppear { pageHeight = g.size.height }.onChange(of: g.size.height) { pageHeight = $0 }
                })
            }
        }
        .padding(16).frame(width: 470)
        .onAppear {
            // Opened with the code already made (Store.openPair), so it
            // opens at its full size, where there is room for it.
            if let first = store.pairInfo { info = first; store.pairInfo = nil } else { pair() }
            store.refreshPhones()
        }
    }

    private var pairPage: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Pair a phone").font(.ui(size: 15, weight: .semibold))
                Text("Scan with the phone's camera. The code works once, for 5 minutes.")
                    .font(.ui(size: 12)).foregroundStyle(.secondary)
            }
            if let i = info {
                HStack(alignment: .top, spacing: 16) {
                    if let link = i.link, let qr = qrImage(link) {
                        Image(nsImage: qr).interpolation(.none).resizable().frame(width: 150, height: 150)
                            .padding(6).background(RoundedRectangle(cornerRadius: 8).fill(Color.white)).help(link)
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        Text("or type the code").font(.ui(size: 11.5)).foregroundStyle(.secondary)
                        Text(i.code).font(.ui(size: 26, weight: .bold, design: .monospaced)).textSelection(.enabled)
                        HStack(spacing: 6) {
                            if let e = i.expires {
                                Text("expires \(e.formatted(date: .omitted, time: .shortened))")
                            }
                            Text("·")
                            Text("this Mac \(Host.current().localizedName ?? "")").lineLimit(1)
                        }
                        .font(.ui(size: 11.5)).foregroundStyle(.secondary)
                        HStack {
                            Button("Copy code") { copy(i.code) }
                            if let l = i.link { Button("Copy link") { copy(l) } }
                        }
                        .padding(.top, 2)
                    }
                }
            }
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text("It may").font(.ui(size: 12, weight: .medium)).frame(width: 64, alignment: .leading)
                VStack(alignment: .leading, spacing: 4) {
                    Picker("", selection: $permission) {
                        Text("Full").tag("full")
                        Text("Reply").tag("reply")
                        Text("View").tag("view")
                    }
                    .pickerStyle(.segmented).labelsHidden().frame(width: 220)
                    .onChange(of: permission) { _ in pair() }
                    Text(permission == "full" ? "See, answer and type into terminals." : permission == "reply" ? "See and answer agents, not type into terminals." : "Only look.")
                        .font(.ui(size: 11.5)).foregroundStyle(.secondary)
                }
            }
            if let i = info {
                reachRows(i)
                if let r = i.relayLine, !r.words.isEmpty { safetyCode(r.words) }
                if i.running != true {
                    HStack {
                        Text("conch web, the gateway a phone reaches, is not running.").font(.ui(size: 12)).foregroundStyle(Theme.warn)
                        Spacer()
                        if i.canStart == true {
                            Button(starting ? "Starting…" : "Start it") { start() }.disabled(starting)
                        }
                    }
                }
            }
            if !error.isEmpty { Text(error).foregroundStyle(Theme.err).font(.ui(size: 12)).fixedSize(horizontal: false, vertical: true) }
            Divider()
            HStack {
                Button { showPhones = true } label: {
                    let n = store.phones?.devices?.count ?? 0
                    let on = store.phones?.onNow.count ?? 0
                    Text(n == 0 ? "Paired phones…" : on > 0 ? "Paired phones (\(n), \(on) on now)…" : "Paired phones (\(n))…")
                }
                Spacer()
                if info?.running == true {
                    Button(store.webRestarting ? "Restarting…" : "Restart conch web") { store.restartWeb() }
                        .disabled(store.webRestarting)
                        .help("Stop conch web and start it again on this app's conch — after an update. Phones reconnect by themselves.")
                }
                Button("Done") { close() }.keyboardShortcut(.defaultAction)
            }
        }
    }

    /// Reach it from: one row a route, the relay first since it works
    /// from anywhere, each saying whether it is up now.
    @ViewBuilder private func reachRows(_ i: PairInfo) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("REACH IT FROM").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(.secondary).padding(.bottom, 6)
            VStack(spacing: 0) {
                if let r = i.relayLine {
                    ReachRow(icon: "globe", title: "Anywhere",
                             detail: "🔒 end-to-end through \(r.host) · the relay can't read it",
                             note: r.connected ? nil : r.status,
                             chip: r.connected ? "connected" : "not connected", on: r.connected)
                } else {
                    ReachRow(icon: "globe", title: "Anywhere",
                             detail: "Through a relay: set [web] relay to a server running conch relay.",
                             note: nil, chip: nil, on: false) {
                        Button("Set up…") { close(); store.openSettings("web") }.controlSize(.small)
                    }
                }
                Divider().padding(.leading, 34)
                let ts = tailnetHost(i.url)
                ReachRow(icon: "shield-check", title: "Tailscale",
                         detail: ts.map { "\($0) · on your tailnet" } ?? "not set up: conch web -url https://…ts.net",
                         note: nil, chip: ts == nil ? nil : (i.running == true ? "listening" : "stopped"),
                         on: ts != nil && i.running == true)
            }
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.04)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.primary.opacity(0.08)))
        }
    }

    /// The key's four words: the phone shows the same if nothing came
    /// between them.
    private func safetyCode(_ words: [String]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text("SAFETY CODE").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(.secondary)
                Text("check after scanning").font(.ui(size: 10.5)).foregroundStyle(.secondary)
            }
            HStack(spacing: 6) {
                ForEach(Array(words.enumerated()), id: \.offset) { _, w in
                    Text(w).font(.ui(size: 13, weight: .semibold, design: .monospaced))
                        .padding(.horizontal, 8).padding(.vertical, 4)
                        .background(RoundedRectangle(cornerRadius: 5).fill(Theme.accent.opacity(0.12)))
                        .foregroundStyle(Theme.accent)
                }
            }
            .textSelection(.enabled)
            Text("The phone shows the same four words if nothing came between them.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
    }

    /// A new one-time code, as `conch web pair` gives one.
    func pair() {
        guard let b = store.binary else { return }
        Task {
            do {
                let out = try await runConch(b, ["web", "pair", "-json", "-permission", permission])
                info = try conchDecoder().decode(PairInfo.self, from: out)
                error = ""
            } catch {
                self.error = "No pairing code: \(error)"
            }
        }
    }

    func start() {
        guard let b = store.binary else { return }
        starting = true
        Task {
            do {
                _ = try await runConch(b, ["web", "start"])
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                pair()
                store.refreshPhones()
            } catch {
                self.error = "\(error)"
            }
            starting = false
        }
    }

    func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}

/// One route a phone can come by, with whether it is up now.
struct ReachRow<Trailing: View>: View {
    let icon: String
    let title: String
    let detail: String
    let note: String?
    let chip: String?
    let on: Bool
    let trailing: Trailing
    @Environment(\.conchTheme) private var theme

    init(icon: String, title: String, detail: String, note: String?, chip: String?, on: Bool,
         @ViewBuilder trailing: () -> Trailing = { EmptyView() }) {
        self.icon = icon
        self.title = title
        self.detail = detail
        self.note = note
        self.chip = chip
        self.on = on
        self.trailing = trailing()
    }

    var body: some View {
        let _ = theme
        return HStack(alignment: .top, spacing: 10) {
            LucideIcon(icon, color: on ? Theme.accent : Theme.muted, size: 14).padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.ui(size: 12.5, weight: .semibold))
                Text(detail).font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                if let n = note {
                    Text(n).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 6)
            if let c = chip {
                HStack(spacing: 4) {
                    Circle().fill(on ? Theme.ok : Theme.warn).frame(width: 6, height: 6)
                    Text(c).font(.ui(size: 11, weight: .medium))
                }
                .padding(.horizontal, 7).padding(.vertical, 3)
                .background(Capsule().fill((on ? Theme.ok : Theme.warn).opacity(0.14)))
                .foregroundStyle(on ? Theme.ok : Theme.warn)
            }
            trailing
        }
        .padding(.horizontal, 10).padding(.vertical, 9)
    }
}

/// The paired phones: each one's route now or when it was last seen, what
/// it may do (changed here), and revoking it — `conch web devices`,
/// `permission` and `revoke`, as the TUI's list does.
struct PhonesList: View {
    @ObservedObject var store: Store
    @State private var confirm: String?
    @State private var error = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            let devs = store.phones?.devices ?? []
            if store.phones == nil {
                Text("Reading…").font(.ui(size: 12)).foregroundStyle(.secondary)
            } else if devs.isEmpty {
                Text("No phones paired yet.").font(.ui(size: 12)).foregroundStyle(.secondary)
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(devs.enumerated()), id: \.element.id) { i, d in
                        if i > 0 { Divider().padding(.leading, 44) }
                        row(d)
                    }
                }
                .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.04)))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.primary.opacity(0.08)))
            }
            if !error.isEmpty { Text(error).foregroundStyle(Theme.err).font(.ui(size: 12)).fixedSize(horizontal: false, vertical: true) }
        }
        .onAppear { store.refreshPhones() }
    }

    private func row(_ d: PhoneDevice) -> some View {
        let on = d.connected == true
        return HStack(spacing: 10) {
            LucideIcon(d.name.lowercased().contains("ipad") || d.name.lowercased().contains("browser") ? "laptop" : "smartphone",
                       color: on ? Theme.accent : Theme.muted, size: 16)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(d.name).font(.ui(size: 12.5, weight: .semibold)).lineLimit(1)
                Text([d.created.map { "paired \($0.formatted(.dateTime.month(.abbreviated).day()))" }, d.reach]
                    .compactMap { $0 }.joined(separator: " · "))
                    .font(.ui(size: 11)).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 6)
            HStack(spacing: 4) {
                if on {
                    Circle().fill(Theme.ok).frame(width: 6, height: 6)
                    Text(d.route == "relay" ? "🔒 via relay" : "direct")
                } else {
                    Text(d.seen())
                }
            }
            .font(.ui(size: 11)).foregroundStyle(on ? Theme.ok : Theme.muted)
            .help(on ? "On now" : "Last seen")
            Menu(d.permission.capitalized) {
                ForEach(["full", "reply", "view"], id: \.self) { p in
                    Button { run(["web", "permission", d.id, p]) } label: {
                        if p == d.permission { Label(p.capitalized, systemImage: "checkmark") } else { Text(p.capitalized) }
                    }
                }
            }
            .menuStyle(.borderlessButton).fixedSize().font(.ui(size: 11.5))
            .help("What it may do: Full types into terminals too, Reply answers agents, View only looks")
            if confirm == d.id {
                Button("Revoke") { confirm = nil; run(["web", "revoke", d.id]) }
                    .controlSize(.small).foregroundStyle(Theme.err)
                    .help("Signed out at once; it pairs again to come back")
            } else {
                Button { confirm = d.id } label: {
                    LucideIcon("trash-2", color: Theme.muted, size: 13).padding(3).contentShape(Rectangle())
                }
                .buttonStyle(.plain).help("Revoke \(d.name)")
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 8)
    }

    private func run(_ args: [String]) {
        guard let b = store.binary else { return }
        Task {
            do {
                _ = try await runConch(b, args)
                error = ""
            } catch {
                self.error = "\(error)"
            }
            store.refreshPhones()
        }
    }
}

/// The tailnet name a phone opens, when conch web is behind Tailscale.
func tailnetHost(_ url: String?) -> String? {
    guard let u = url, let h = URL(string: u)?.host, h.hasSuffix(".ts.net") else { return nil }
    return h
}

/// A QR code of text, sharp at any size (drawn without smoothing).
@MainActor func qrImage(_ text: String) -> NSImage? {
    let f = CIFilter.qrCodeGenerator()
    f.message = Data(text.utf8)
    f.correctionLevel = "M"
    guard let out = f.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else { return nil }
    let rep = NSCIImageRep(ciImage: out)
    let img = NSImage(size: rep.size)
    img.addRepresentation(rep)
    return img
}
