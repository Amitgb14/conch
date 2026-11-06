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
    @Published var request = ""
    @Published var output = ""
    @Published var state: AskSession.State?
    private var session: AskSession?

    func ask(binary: String) {
        let r = request.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !r.isEmpty else { return }
        session?.cancel()
        let s = AskSession(binary: binary, request: r)
        session = s
        output = ""
        state = .thinking
        s.onChange = { [weak self, weak s] in
            guard let self, let s, s === self.session else { return }
            self.output = s.output
            self.state = s.state
        }
        s.start()
    }

    func confirm(_ yes: Bool) { session?.confirm(yes) }
    func stop() { session?.cancel() }
}

struct AskPopover: View {
    @ObservedObject var store: Store
    @StateObject private var model = AskModel()

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("✦ Ask conch").font(.ui(size: 13, weight: .semibold))
            Text("Say what you want done; conch plans it, and runs nothing until you confirm.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary)
            HStack {
                TextField("start 2 agents on api to fix the flaky tests", text: $model.request)
                    .textFieldStyle(.roundedBorder).onSubmit { if let b = store.binary { model.ask(binary: b) } }
                Button("Plan") { if let b = store.binary { model.ask(binary: b) } }
                    .disabled(model.request.trimmingCharacters(in: .whitespaces).isEmpty || model.state == .thinking)
            }
            switch model.state {
            case .thinking?:
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Thinking…").foregroundStyle(.secondary) }
            case .failed(let why)?:
                Text(why).foregroundStyle(Theme.err).font(.ui(size: 12)).textSelection(.enabled)
            default:
                EmptyView()
            }
            if !model.output.isEmpty {
                ScrollView {
                    Text(model.output).font(.ui(size: 12, design: .monospaced)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(minHeight: 120, maxHeight: 220) // in a popover, a scroll view takes no height it is not given
            }
            if case .plan(let n)? = model.state {
                HStack {
                    Spacer()
                    Button("Don't run") { model.confirm(false) }
                    Button("Run \(n) action\(n == 1 ? "" : "s")") { model.confirm(true) }.keyboardShortcut(.defaultAction)
                }
            } else if model.state == .running {
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Running…").foregroundStyle(.secondary) }
            }
        }
        .padding(14).frame(width: 460)
        .onDisappear { if model.state == .thinking { model.stop() } }
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
