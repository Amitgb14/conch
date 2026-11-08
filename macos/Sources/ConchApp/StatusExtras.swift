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
    @State private var pairing = false

    var body: some View {
        HStack(spacing: 10) {
            Button { store.askOpen.toggle() } label: {
                Text("✦ Ask").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.accent)
            }
            .buttonStyle(.plain).help("Ask conch in plain words — it plans, and runs nothing until you confirm")
            .popover(isPresented: $store.askOpen, arrowEdge: .top) { AskPopover(store: store) }
            Button { reporting.toggle() } label: {
                Text("⊙").font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.muted)
            }
            .buttonStyle(.plain).help("Report a problem: the facts about this conch, for a GitHub issue")
            .popover(isPresented: $reporting, arrowEdge: .top) { ReportPopover(store: store) }
            Button { pairing.toggle() } label: {
                LucideIcon("smartphone", color: Theme.muted, size: 13)
            }
            .buttonStyle(.plain).help("Pair a phone, to see and answer your agents from it")
            .popover(isPresented: $pairing, arrowEdge: .top) { PairPopover(store: store) }
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

struct PairPopover: View {
    @ObservedObject var store: Store
    @State private var info: PairInfo?
    @State private var permission = "full"
    @State private var error = ""
    @State private var starting = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Pair a phone with this computer").font(.ui(size: 13, weight: .semibold))
            Picker("", selection: $permission) {
                Text("Full").tag("full")
                Text("Reply").tag("reply")
                Text("View").tag("view")
            }
            .pickerStyle(.segmented).labelsHidden()
            .onChange(of: permission) { _ in pair() }
            Text(permission == "full" ? "It can see, answer and type into terminals." : permission == "reply" ? "It can see and answer agents, not type into terminals." : "It can only look.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary)
            if let i = info {
                HStack(alignment: .top, spacing: 14) {
                    if let link = i.link, let qr = qrImage(link) {
                        Image(nsImage: qr).interpolation(.none).resizable().frame(width: 150, height: 150)
                            .background(Color.white).help(link)
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        Text(i.code).font(.ui(size: 26, weight: .bold, design: .monospaced)).textSelection(.enabled)
                        if let e = i.expires {
                            Text("Good once, until \(e.formatted(date: .omitted, time: .shortened)).").font(.ui(size: 11.5)).foregroundStyle(.secondary)
                        }
                        if let r = i.reach { Text(r).font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true) }
                        HStack {
                            Button("Copy code") { copy(i.code) }
                            if let l = i.link { Button("Copy link") { copy(l) } }
                        }
                    }
                }
                if let w = i.where { Text(w).font(.ui(size: 12)).fixedSize(horizontal: false, vertical: true) }
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
        }
        .padding(14).frame(width: 440)
        .onAppear { pair() }
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
