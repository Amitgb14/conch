import AppKit
import ConchKit
import SwiftUI

// New integration (Integrations → MAKE ONE → ✦ Adapt from API docs…,
// docs/plans/marketplace-mocks m7): the kind, the service, its API docs
// and the agent; then conch does the work (`conch integration adapt
// -json`) and this draws its progress list — drafted, offline check, live
// check with your key, publish. The key is conch's: the live check runs
// only when the person presses the button that says where it goes. The
// agent's pane is shown in the main window rather than beside the sheet.

struct NewIntegrationSheet: View {
    @ObservedObject var store: Store
    var onSaved: () -> Void = {}
    @Environment(\.dismiss) private var dismiss
    @Environment(\.conchTheme) private var theme
    @State private var form = IntegrationAdaptForm()
    @State private var progress = AdaptProgress()
    @State private var running = false
    @State private var checking = false
    @State private var publish = IntegrationPublishState()
    @State private var problem = ""
    @State private var revision = ""
    @State private var kept = false

    private static let kinds: [(IntegrationKind, String)] = [(.ci, "CI"), (.host, "Code host"), (.tickets, "Tickets"), (.sandbox, "Sandbox"), (.notify, "Notify")]
    /// The agents that can write it: those installed here that conch
    /// hands a first message to (the supported tier), Claude first.
    private var agents: [(String, String)] {
        let here = (store.agents["local"] ?? []).filter { $0.installed && $0.tier != "runs here" }
        let list = here.map { ($0.name, $0.label ?? $0.name.capitalized) }
        return list.isEmpty ? [("claude", "Claude")] : list
    }

    private var projects: [(name: String, path: String)] {
        (store.snapshots["local"]?.projects ?? []).filter { $0.git == true }.map { ($0.name, $0.path) }
    }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            Text("Adapt an integration").font(.ui(size: 15, weight: .semibold))
            box { fields }
            if progress.phase != .idle { box { list } }
            if progress.phase == .registry {
                Text(progress.registryDetail).font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            }
            if progress.phase == .review && !progress.problems.isEmpty || (progress.phase == .review && progress.live?.ok == false) {
                HStack(spacing: 8) {
                    TextField("Ask \(progress.agent.isEmpty ? "the agent" : progress.agent.capitalized) to change…", text: $revision)
                        .textFieldStyle(.roundedBorder).disabled(running)
                    Button(running ? "Changing…" : "Send") { revise() }
                        .disabled(running || revision.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            if !problem.isEmpty {
                Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
            }
            footer
        }
        .padding(20).frame(width: 460)
        .onAppear { if form.project == nil { form.project = projects.first?.path } }
    }

    private func box<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        content()
            .padding(14).frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
    }

    // MARK: the form

    private var fields: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("NEW INTEGRATION").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted)
            row("Kind") {
                HStack(spacing: 5) {
                    ForEach(Self.kinds, id: \.0) { k in
                        chip(k.1, on: form.kind == k.0, enabled: IntegrationAdaptForm.adaptable.contains(k.0)) { form.kind = k.0 }
                    }
                }
            }
            row("Service") { TextField("", text: $form.service, prompt: Text("Semaphore")).textFieldStyle(.roundedBorder) }
            row("API docs") { TextField("", text: $form.docs, prompt: Text("https://docs.example.com/api")).textFieldStyle(.roundedBorder) }
            row("Agent") {
                HStack(spacing: 5) {
                    ForEach(agents, id: \.0) { a in chip(a.1, on: form.agent == a.0, enabled: true, agent: a.0) { form.agent = a.0 } }
                }
            }
            if form.kind != .sandbox && !projects.isEmpty {
                row("Check on") {
                    Picker("", selection: Binding(get: { form.project ?? "" }, set: { form.project = $0 })) {
                        ForEach(projects, id: \.path) { Text($0.name).tag($0.path) }
                    }
                    .labelsHidden()
                }
            }
            HStack {
                Spacer()
                Button("✦ Adapt") { start() }.keyboardShortcut(.defaultAction)
                    .disabled(running || progress.phase != .idle && progress.phase != .failed && progress.phase != .registry
                              || form.docs.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .disabled(running && progress.phase == .idle)
    }

    private func row<C: View>(_ label: String, @ViewBuilder _ content: () -> C) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label).font(.ui(size: 12)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
            content()
        }
    }

    /// A choice: an agent's with its own mark. One that cannot be chosen
    /// is drawn faint and takes no click, and none takes the keyboard's
    /// focus — a focus ring left on CI read as CI chosen.
    private func chip(_ s: String, on: Bool, enabled: Bool, agent: String? = nil, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 4) {
                if let agent { Mark(agent: agent) }
                Text(s).font(.ui(size: 11.5, weight: on ? .semibold : .regular))
            }
            .foregroundStyle(on ? Theme.accent : enabled ? Theme.text2 : Theme.muted.opacity(0.6))
            .padding(.horizontal, 9).padding(.vertical, 3)
            .background(Capsule().fill(on ? Theme.accent.opacity(0.12) : .clear))
            .overlay(Capsule().stroke(on ? Theme.accent : Theme.line))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .focusable(false)
        .disabled(!enabled)
        .help(enabled ? "" : "conch reads no \(s.lowercased()) files yet")
    }

    // MARK: progress

    private var list: some View {
        let steps = IntegrationAdaptSteps.steps(progress, name: form.name, checking: checking, publish: publish)
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(steps.enumerated()), id: \.element.id) { i, s in
                if i > 0 { Divider().padding(.vertical, 7) }
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    Group {
                        switch s.state {
                        case .done: Image(systemName: "checkmark").foregroundStyle(Theme.ok)
                        case .running: Circle().fill(Theme.accent).frame(width: 8, height: 8)
                        case .waiting: Circle().stroke(Theme.muted).frame(width: 8, height: 8)
                        case .failed: Image(systemName: "xmark").foregroundStyle(Theme.err)
                        }
                    }
                    .frame(width: 14)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(s.title).font(.ui(size: 12.5))
                        if !s.detail.isEmpty {
                            Text(s.detail).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(3).textSelection(.enabled)
                        }
                    }
                    Spacer(minLength: 0)
                    action(s.id)
                }
            }
        }
    }

    @ViewBuilder private func action(_ id: String) -> some View {
        switch id {
        case "live" where IntegrationAdaptSteps.canCheck(progress) && !checking && form.kind != .sandbox:
            Button("Check with my key") { check() }.controlSize(.small)
                .help(progress.consent.isEmpty ? "conch reads the service with your key" : "Sends \(progress.consent); the agent never sees it")
        case "publish" where IntegrationAdaptSteps.canPublish(progress) && publish.url.isEmpty && !publish.running:
            Button("Publish…") { doPublish() }.controlSize(.small)
        case "publish" where !publish.url.isEmpty:
            Button("Open") { if let u = URL(string: publish.url) { NSWorkspace.shared.open(u) } }.controlSize(.small)
        default:
            EmptyView()
        }
    }

    // MARK: buttons

    private var footer: some View {
        HStack {
            if !progress.pane.isEmpty {
                Button("Show \(progress.agent.isEmpty ? "the agent" : progress.agent.capitalized)'s pane") { store.showLocalPane(progress.pane) }
                    .buttonStyle(.link)
            }
            if !progress.path.isEmpty {
                Button("Open the file") { NSWorkspace.shared.open(URL(fileURLWithPath: progress.path)) }.buttonStyle(.link)
            }
            Spacer()
            Button(progress.phase == .idle ? "Cancel" : "Close") { dismiss() }.keyboardShortcut(.cancelAction)
                .help("An agent still writing carries on in its pane")
            if progress.phase == .registry {
                Button("Adapt anyway") { form.anyway = true; progress = AdaptProgress(); start() }
            }
            if form.kind != .sandbox && progress.offline && !kept {
                Button("Keep it here") { keep() }.disabled(running || checking)
                    .help("Save it as a file of your own; give it a key with conch integration key \(form.name)")
            }
        }
    }

    // MARK: running conch

    private func start() {
        problem = ""
        publish = IntegrationPublishState()
        switch form.arguments() {
        case .failure(let e): problem = e.message
        case .success(let args):
            progress = AdaptProgress()
            run(args)
        }
    }

    private func revise() {
        let text = revision.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return }
        revision = ""
        progress.live = nil
        run(["integration", "adapt", "-json", "-revise", text, form.name])
    }

    private func check() {
        checking = true
        progress.live = nil
        run(form.checkArguments()) { checking = false }
    }

    private func run(_ args: [String], then: @escaping @MainActor () -> Void = {}) {
        guard let b = store.binary else { problem = "No conch to run"; return }
        running = true
        if progress.phase == .idle { progress.phase = .docs }
        Task { @MainActor in
            let lines = AsyncStream<String> { cont in
                Task {
                    _ = try? await runConchLines(b, args) { cont.yield($0) }
                    cont.finish()
                }
            }
            for await line in lines { progress.take(line) }
            running = false
            then()
            if progress.phase == .docs || progress.phase == .writing, progress.error.isEmpty {
                progress.take(AdaptEvent(event: "error", detail: "conch stopped before the file was written"))
            }
        }
    }

    private func keep() {
        guard let b = store.binary else { return }
        Task { @MainActor in
            do {
                _ = try await runConch(b, form.saveArguments())
                kept = true
                onSaved()
            } catch {
                problem = "\(error)"
            }
        }
    }

    private func doPublish() {
        let a = NSAlert()
        a.messageText = "Publish \(form.name) to conch-integrations?"
        a.informativeText = "conch checks it with your key again (\(progress.consent.isEmpty ? "the key the file names" : progress.consent)), then opens a pull request to github.com/Amitgb14/conch-integrations through gh, as you: the file, its meta.json and the check's result. Your key is not part of it. It is listed once a maintainer has reviewed it."
        a.addButton(withTitle: "Publish")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn, let b = store.binary else { return }
        publish = IntegrationPublishState()
        publish.running = true
        Task { @MainActor in
            do {
                let out = try await runConch(b, form.publishArguments())
                if let r = try? JSONDecoder().decode(PublishResult.self, from: out) { publish.url = r.url }
            } catch {
                publish.error = "\(error)"
            }
            publish.running = false
        }
    }
}
