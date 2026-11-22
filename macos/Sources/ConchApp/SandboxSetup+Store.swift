import AppKit
import ConchKit
import Foundation

/// A sandbox being set up from New Sandbox…: `conch sandbox create`
/// running in a terminal of the server's (so it outlives the sheet and the
/// app), writing its steps to a file the sheet reads (SandboxSetup.swift).
@MainActor
final class SandboxSetupRun: ObservableObject, Identifiable {
    let id = UUID()
    let provider: String
    let providerLabel: String
    /// What was asked for, under the title: "us-west · small".
    let summary: String
    let labelAsked: String?
    var file: String
    /// The terminal it runs in, by key.
    var pane: String?
    @Published var state = SandboxSetupState()
    /// Its terminal ended before the file said how it finished.
    @Published var stopped = false
    @Published var busy: String?
    var told = false

    init(provider: String, providerLabel: String, summary: String, label: String?, file: String) {
        self.provider = provider
        self.providerLabel = providerLabel
        self.summary = summary
        self.labelAsked = label
        self.file = file
    }

    var finished: Bool { state.end != nil || stopped }
    var failed: Bool {
        if stopped && state.end == nil { return true }
        if case .failed = state.end { return true }
        return false
    }
    var ready: Bool { state.end == .ready }

    /// Why it failed, in the file's words.
    var failure: String? {
        if case .failed(let e, _)? = state.end { return e }
        return stopped && state.end == nil ? "conch stopped before it said how setting up ended — Show the Whole Log says what it printed." : nil
    }
    /// Whether the sandbox is still there after a failure.
    var kept: Bool {
        if case .failed(_, let k)? = state.end { return k && state.sandbox != nil }
        return stopped && state.sandbox != nil
    }
}

extension Store {
    /// Where setups write their steps.
    private static var setupsDir: String {
        let home = ProcessInfo.processInfo.environment["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 }
            ?? (NSHomeDirectory() as NSString).appendingPathComponent(".config/conch")
        return (home as NSString).appendingPathComponent("sandbox-setups")
    }

    private static func newSetupFile() -> String? {
        let dir = setupsDir
        do { try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true) } catch { return nil }
        return (dir as NSString).appendingPathComponent(UUID().uuidString + ".jsonl")
    }

    /// Starts setting a sandbox up, the sheet following it: create's
    /// arguments, the steps written to a file, a failed one kept for the
    /// sheet to ask about.
    func startSandboxSetup(args: [String], provider: String, providerLabel: String, summary: String, label: String?) -> SandboxSetupRun? {
        guard let file = Self.newSetupFile() else {
            error = "conch couldn't make \(Self.setupsDir)"
            return nil
        }
        let run = SandboxSetupRun(provider: provider, providerLabel: providerLabel, summary: summary, label: label, file: file)
        sandboxSetups.append(run)
        expectMachine()
        launch(run, args: args + ["-keep", "-progress", file], name: "sandbox create · " + providerLabel)
        return run
    }

    /// Sets up again a sandbox whose setup failed but which is still there.
    func retrySandboxSetup(_ run: SandboxSetupRun) {
        guard let sb = run.state.sandbox, let file = Self.newSetupFile() else { return }
        closeSetupPane(run)
        run.file = file
        run.state = SandboxSetupState()
        run.stopped = false
        run.told = false
        launch(run, args: sandboxRetryArguments(provider: run.provider, sandbox: sb, label: run.labelAsked, progress: file),
               name: "sandbox add · " + run.providerLabel)
    }

    private func launch(_ run: SandboxSetupRun, args: [String], name: String) {
        guard let b = binary, let c = conns["local"] else {
            run.stopped = true
            return
        }
        struct Create: Encodable {
            var name: String
            var command: [String]
            var cwd: String
            var no_project: Bool
            var env: [String]?
        }
        let env = cliPaneEnv()
        Task {
            do {
                // Not opened: the sheet shows its steps; Show the Whole
                // Log opens the terminal.
                let p = try await c.call("pane.create", Create(name: name, command: [b] + args, cwd: NSHomeDirectory(), no_project: true,
                                                               env: env.isEmpty ? nil : env), as: PaneInfo.self)
                snapshots["local", default: Snapshot()].upsert(p)
                run.pane = key("local", p.id)
                followSandboxSetups()
            } catch {
                run.stopped = true
                self.error = "\(error)"
            }
        }
    }

    /// Reads every running setup's file a few times a second until none
    /// is left running.
    func followSandboxSetups() {
        guard setupTimer == nil else { return }
        setupTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.readSandboxSetups() }
        }
    }

    private func readSandboxSetups() {
        var running = false
        for run in sandboxSetups where !run.finished {
            if let d = FileManager.default.contents(atPath: run.file) {
                let st = readSandboxSetup(d)
                if st != run.state {
                    run.state = st
                    objectWillChange.send() // the status bar's count
                }
            }
            // Its terminal gone or over with no ending written: killed,
            // or a conch too old to write one.
            if run.state.end == nil, let k = run.pane, pane(k).map({ !$0.running }) ?? true {
                run.stopped = true
            }
            if run.finished {
                objectWillChange.send()
                tellSetupFinished(run)
            } else {
                running = true
            }
        }
        if !running {
            setupTimer?.invalidate()
            setupTimer = nil
        }
    }

    /// A notification, unless the sheet is showing it.
    private func tellSetupFinished(_ run: SandboxSetupRun) {
        guard !run.told else { return }
        run.told = true
        if run.ready, let k = run.pane { closePaneQuietly(k) }
        if newSandboxOpen && sandboxSheetRun == run.id && NSApp.isActive { return }
        let name = run.state.label ?? run.state.sandbox ?? run.providerLabel
        let a = run.ready
            ? Alert(title: "Sandbox ready", body: "\(run.providerLabel) \(name) is in the sidebar.")
            : Alert(title: "Sandbox setup failed", body: "\(run.providerLabel) \(name): \(run.failure ?? "")")
        Notifier.shared.post(a, id: "sandbox:" + run.id.uuidString, sound: true, desktop: true)
    }

    /// The sheet closed: a setup that is over and fine is forgotten; one
    /// still going, or failed, stays in the status bar. The next New
    /// Sandbox… opens on the form.
    func sandboxSheetClosed() {
        if let run = shownSandboxSetup, run.ready { dropSandboxSetup(run) }
        sandboxSheetRun = nil
    }

    /// Shows a setup's steps again.
    func showSandboxSetup(_ run: SandboxSetupRun) {
        sandboxSheetRun = run.id
        newSandboxOpen = true
    }

    var shownSandboxSetup: SandboxSetupRun? {
        sandboxSheetRun.flatMap { id in sandboxSetups.first { $0.id == id } }
    }

    /// Setups the status bar shows: running, or failed and not yet dealt with.
    var pendingSandboxSetups: [SandboxSetupRun] { sandboxSetups.filter { !$0.finished || $0.failed } }

    /// Forgets a setup the person is done with.
    func dropSandboxSetup(_ run: SandboxSetupRun) {
        sandboxSetups.removeAll { $0.id == run.id }
        if sandboxSheetRun == run.id { sandboxSheetRun = nil }
        try? FileManager.default.removeItem(atPath: run.file)
    }

    /// Stops a setup under way and deletes the sandbox, after asking.
    func cancelSandboxSetup(_ run: SandboxSetupRun) {
        let a = NSAlert()
        a.messageText = "Stop setting up and delete the sandbox?"
        a.informativeText = run.state.sandbox == nil
            ? "The provider may already be making it: if it is, conch sandbox -provider \(run.provider) ls lists it to delete."
            : "\(run.providerLabel) sandbox \(run.state.sandbox ?? "") is deleted, with anything in it."
        a.addButton(withTitle: "Delete")
        a.addButton(withTitle: "Keep Setting Up")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        closeSetupPane(run)
        deleteSetupSandbox(run)
    }

    /// Deletes a sandbox whose setup failed.
    func deleteSetupSandbox(_ run: SandboxSetupRun) {
        closeSetupPane(run)
        guard let sb = run.state.sandbox, let b = binary else {
            dropSandboxSetup(run)
            return
        }
        run.busy = "Deleting…"
        Task {
            do {
                _ = try await runConch(b, sandboxDeleteArguments(provider: run.provider, sandbox: sb))
                say("Deleted \(run.providerLabel) sandbox \(sb)")
                dropSandboxSetup(run)
            } catch {
                run.busy = nil
                self.error = "Couldn't delete \(sb): \(error)"
            }
        }
    }

    /// Leaves a sandbox whose setup failed as it is.
    func keepSetupSandbox(_ run: SandboxSetupRun) {
        closeSetupPane(run)
        if let sb = run.state.sandbox {
            say("Kept \(sb): it runs, and costs, until it is deleted · conch sandbox -provider \(run.provider) ls lists it")
        }
        dropSandboxSetup(run)
    }

    /// The setup's terminal, for what it printed.
    func showSetupLog(_ run: SandboxSetupRun) {
        guard let k = run.pane else { return }
        newSandboxOpen = false
        open(k)
    }

    private func closeSetupPane(_ run: SandboxSetupRun) {
        guard let k = run.pane else { return }
        closePaneQuietly(k)
        run.pane = nil
    }

    private func closePaneQuietly(_ k: String) {
        let (m, id) = unkey(k)
        layout.close(k)
        guard let c = conns[m] else { return }
        struct Ref: Encodable { var id: String }
        Task { try? await c.call("pane.close", Ref(id: id)) }
    }
}
