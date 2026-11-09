import AppKit
import ConchKit
import SwiftUI

// Add provider…: a sandbox provider conch does not know, adapted from its
// API docs. conch does the work (`conch sandbox adapt -json`): it fetches
// the docs, starts the agent in a pane of its own, and checks the file it
// writes and the key. This sheet shows that happening, then the file — as
// what conch will call for what, and as the file itself — and saves it
// (`conch sandbox provider save`) only when asked.

struct AddProviderSheet: View {
    @ObservedObject var store: Store
    var onSaved: () -> Void = {}
    @Environment(\.dismiss) private var dismiss
    @Environment(\.conchTheme) private var theme
    @State private var form = AdaptForm()
    @State private var progress = AdaptProgress()
    @State private var running = false
    @State private var problem = ""
    @State private var tab = 0
    @State private var revision = ""
    @State private var saving = false

    private static let agents: [(String, String)] = [("claude", "Claude"), ("codex", "Codex"), ("gemini", "Gemini")]

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Text(progress.phase == .review ? "Review \(adaptName(form))" : "Add a sandbox provider").font(.ui(size: 15, weight: .semibold))
                if progress.phase == .review {
                    Text("adapted · not built in").font(.ui(size: 10.5)).foregroundStyle(Theme.warn)
                        .padding(.horizontal, 7).padding(.vertical, 1)
                        .overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                }
            }
            switch progress.phase {
            case .idle: ask
            case .docs, .writing: writing
            case .review: review
            case .failed: failed
            }
            if !problem.isEmpty {
                Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
            }
            footer
        }
        .padding(20).frame(width: 640)
    }

    // MARK: asking

    private var ask: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("conch downloads the provider's API docs, and an agent reads them in a pane of its own and writes a provider file: which calls make, list, start, stop and delete a sandbox, and how ssh gets in. conch checks it, and the list call with your key, before you save it. The key is never given to the agent.")
                .font(.ui(size: 12)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            Form {
                TextField("API or docs URL", text: $form.url, prompt: Text("https://api.example.dev/sandboxes"))
                TextField("Name", text: $form.name, prompt: Text(adaptName(form).isEmpty ? "from the URL" : adaptName(form)))
                Picker("Adapt with", selection: $form.agent) {
                    ForEach(Self.agents, id: \.0) { Text($0.1).tag($0.0) }
                }
                .pickerStyle(.segmented)
            }
            .formStyle(.columns)
            Text("Takes a few minutes of that agent's usage. A provider with no ssh way into its sandboxes cannot be adapted: conch says so rather than saving a file that would never connect.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
        }
    }

    // MARK: writing

    private var writing: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(Array(progress.docs.enumerated()), id: \.offset) { _, d in
                step(.ok, "API docs", d)
            }
            if progress.docs.isEmpty {
                step(.run, "API docs", progress.misses.last ?? "looking…")
            }
            if progress.phase == .writing {
                step(progress.blocked ? .warn : .run, progress.agent.capitalized + " is writing the file", progress.status)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
    }

    enum Mark { case ok, run, warn, bad }

    private func step(_ m: Mark, _ title: String, _ detail: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 9) {
            Group {
                switch m {
                case .ok: Image(systemName: "checkmark").foregroundStyle(Theme.ok)
                case .run: ProgressView().controlSize(.mini)
                case .warn: Image(systemName: "exclamationmark.bubble").foregroundStyle(Theme.warn)
                case .bad: Image(systemName: "xmark").foregroundStyle(Theme.err)
                }
            }
            .frame(width: 14)
            Text(title).font(.ui(size: 12.5))
            Spacer()
            Text(detail).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(2).truncationMode(.middle)
        }
    }

    // MARK: review

    private var review: some View {
        VStack(alignment: .leading, spacing: 10) {
            Picker("", selection: $tab) {
                Text("How conch read it").tag(0)
                Text("File").tag(1)
                Text(progress.problems.isEmpty ? "Check" : "Check · \(progress.problems.count) to fix").tag(2)
            }
            .pickerStyle(.segmented).labelsHidden()
            Group {
                switch tab {
                case 0: mappingView
                case 1: fileView
                default: checkView
                }
            }
            .frame(height: 280, alignment: .top)
            HStack(spacing: 8) {
                TextField("Ask \(progress.agent.isEmpty ? "the agent" : progress.agent.capitalized) to change…", text: $revision)
                    .textFieldStyle(.roundedBorder).disabled(running)
                Button(running ? "Changing…" : "Send") { revise() }
                    .disabled(running || revision.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            if running { step(.run, progress.agent.capitalized + " is changing the file", progress.status) }
        }
    }

    private var mappingView: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(progress.mapping.enumerated()), id: \.offset) { i, m in
                    HStack(alignment: .top, spacing: 10) {
                        Text(m.does).font(.ui(size: 12)).frame(width: 110, alignment: .leading)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(m.call).font(.system(size: 11, design: .monospaced)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                            if let r = m.reads, !r.isEmpty {
                                Text("reads " + r).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                        }
                        Spacer(minLength: 0)
                    }
                    .padding(.vertical, 6).padding(.horizontal, 10)
                    .background(i % 2 == 0 ? Color.clear : Color.primary.opacity(0.03))
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
    }

    private var fileView: some View {
        VStack(alignment: .leading, spacing: 6) {
            ScrollView {
                Text(progress.file).font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(10)
            }
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.black.opacity(0.25)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
            HStack {
                Text(progress.path).font(.system(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                Spacer()
                Button("Open in editor") { NSWorkspace.shared.open(URL(fileURLWithPath: progress.path)) }
                    .controlSize(.small).disabled(progress.path.isEmpty)
            }
        }
    }

    private var checkView: some View {
        VStack(alignment: .leading, spacing: 8) {
            if progress.problems.isEmpty {
                step(.ok, "The file reads, and has every call conch needs", "")
            } else {
                ForEach(progress.problems, id: \.self) { p in step(.bad, p, "") }
            }
            if let c = progress.check {
                if c.signed {
                    step(.ok, "Signed in with $\(c.keyEnv ?? "the key")", "the list answered: \(c.all ?? 0) sandboxes, \(c.mine ?? 0) of them conch's")
                } else {
                    step(.warn, "Not checked against the API", c.why ?? "")
                }
            }
            if !progress.error.isEmpty && progress.problems.isEmpty {
                step(.bad, progress.error, "")
            }
            if progress.file.contains("key = \"account\"") {
                Text("Its ssh gateway lets in the keys registered with your account there, which conch cannot do for you: register ~/.ssh/id_ed25519.pub with the provider once.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.text2).padding(8)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Theme.warn.opacity(0.08)))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
        }
    }

    // MARK: failed

    private var failed: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(progress.docs, id: \.self) { d in step(.ok, "API docs", d) }
            step(.bad, progress.error, "")
            ForEach(progress.misses.suffix(3), id: \.self) { m in
                Text(m).font(.system(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
            }
        }
    }

    // MARK: buttons

    private var footer: some View {
        HStack {
            if !progress.pane.isEmpty {
                Button("Show \(progress.agent.isEmpty ? "the agent" : progress.agent.capitalized)'s pane") { store.showLocalPane(progress.pane) }
                    .buttonStyle(.link)
            }
            Spacer()
            switch progress.phase {
            case .idle:
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Adapt it") { start() }.keyboardShortcut(.defaultAction)
                    .disabled(form.url.trimmingCharacters(in: .whitespaces).isEmpty)
            case .docs, .writing:
                Button("Close") { dismiss() }.help("The agent carries on in its pane; conch sandbox provider check \(adaptName(form)) checks its file later")
            case .review:
                Button("Discard") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(saving ? "Saving…" : "Save provider") { save() }.keyboardShortcut(.defaultAction)
                    .disabled(!progress.canSave || saving || running)
            case .failed:
                Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Try again") { progress = AdaptProgress(); problem = "" }
            }
        }
    }

    // MARK: running conch

    private func start() {
        problem = ""
        switch form.arguments() {
        case .failure(let e): problem = e.message
        case .success(let args): run(args)
        }
    }

    private func revise() {
        let text = revision.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return }
        revision = ""
        run(["sandbox", "adapt", "-json", "-revise", text, adaptName(form)])
    }

    private func run(_ args: [String]) {
        guard let b = store.binary else { problem = "No conch to run"; return }
        running = true
        if progress.phase == .idle { progress.phase = .docs }
        Task {
            let lines = AsyncStream<String> { cont in
                Task {
                    _ = try? await runConchLines(b, args) { cont.yield($0) }
                    cont.finish()
                }
            }
            for await line in lines { progress.take(line) }
            running = false
            if progress.phase == .docs || progress.phase == .writing, progress.error.isEmpty {
                progress.take(AdaptEvent(event: "error", detail: "conch stopped before the file was written"))
            }
        }
    }

    private func save() {
        guard let b = store.binary else { return }
        saving = true
        Task {
            do {
                _ = try await runConch(b, ["sandbox", "provider", "save", adaptName(form)])
                onSaved()
                dismiss()
            } catch {
                problem = "\(error)"
            }
            saving = false
        }
    }
}

/// The providers conch has, built in and adapted, with the way to add one
/// it does not know: the Sandboxes section of Settings.
struct ProvidersCard: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme
    @State private var providers: [SandboxProviderInfo] = []
    @State private var adding = false
    @State private var problem = ""

    var body: some View {
        let _ = theme
        SettingsCard(title: "Providers", trailing: AnyView(Button("Add provider…") { adding = true }.controlSize(.small))) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(providers) { p in
                    HStack(spacing: 10) {
                        Circle().fill(p.ready ? Theme.ok : Theme.muted.opacity(0.6)).frame(width: 7, height: 7)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(p.label).font(.ui(size: 13, weight: .medium))
                            Text(p.ready ? (p.adapted == true ? (p.file ?? "") : "ready") : (p.why ?? "not set up"))
                                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(2)
                        }
                        Spacer()
                        Text(p.adapted == true ? "adapted" : "built in").font(.ui(size: 10.5))
                            .foregroundStyle(p.adapted == true ? Theme.warn : Theme.muted)
                            .padding(.horizontal, 7).padding(.vertical, 1)
                            .overlay(Capsule().stroke(p.adapted == true ? Theme.warn.opacity(0.5) : Theme.line))
                        if p.adapted == true {
                            Button("Remove") { remove(p) }.controlSize(.small)
                        }
                    }
                    .padding(.horizontal, 16).padding(.vertical, 9)
                }
                if !problem.isEmpty {
                    Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.err).padding(.horizontal, 16).padding(.bottom, 8)
                }
            }
        }
        .task { await load() }
        .sheet(isPresented: $adding) {
            AddProviderSheet(store: store) { Task { await load() } }
        }
    }

    private func load() async {
        guard let b = store.binary else { return }
        if let out = try? await runConch(b, ["sandbox", "providers", "-json"]),
           let ps = try? JSONDecoder().decode([SandboxProviderInfo].self, from: out) {
            providers = ps
        }
    }

    private func remove(_ p: SandboxProviderInfo) {
        let a = NSAlert()
        a.messageText = "Remove \(p.label)?"
        a.informativeText = "Its file goes; sandboxes already made with it are left as they are at \(p.label), and still cost until deleted there."
        a.addButton(withTitle: "Remove")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn, let b = store.binary else { return }
        Task {
            do {
                _ = try await runConch(b, ["sandbox", "provider", "rm", p.name])
                problem = ""
            } catch {
                problem = "\(error)"
            }
            await load()
        }
    }
}
