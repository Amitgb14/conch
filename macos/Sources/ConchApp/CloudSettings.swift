import AppKit
import ConchKit
import SwiftUI

// Settings → Conch Cloud: the sign-in, the account and its machines, all
// through the conch binary (ConchKit/Cloud.swift). With no sign-in, only
// the signed-out card is drawn and nothing is asked of anything.

extension Store {
    var cloudSignedIn: Bool { CloudModel.signedIn(conchDir: conchDir) }

    /// Asks conch for the account and its machines again; with no sign-in,
    /// or Conch Cloud hidden (offersCloud), it forgets what it had and asks
    /// nothing.
    func refreshCloud() {
        guard cloudSignedIn, offersCloud else {
            cloud = nil
            cloudMachines = []
            return
        }
        guard let b = binary, !cloudAsking else { return }
        cloudAsking = true
        Task {
            defer { cloudAsking = false }
            do {
                let out = try await runConch(b, ["account", "-json"])
                let a = try conchDecoder().decode(CloudAccount.self, from: out)
                if a != cloud { cloud = a }
                let ms = try await runConch(b, ["cloud", "machine", "ls", "-json"])
                let list = try conchDecoder().decode(CloudMachines.self, from: ms).machines
                if list != cloudMachines { cloudMachines = list }
                cloudError = ""
            } catch {
                cloudError = "\(error)"
                if !cloudSignedIn { cloud = nil; cloudMachines = [] } // conch signed it out
            }
        }
    }

    /// Runs `conch login -json`: the code is shown, the page to approve it
    /// opened, and the account read once conch says it is signed in.
    func cloudSignIn() {
        guard let b = binary, cloudLoginRun == nil else { return }
        cloudLogin = .starting
        cloudError = ""
        let run = CloudLoginRun()
        cloudLoginRun = run
        do {
            try run.start(binary: b, onEvent: { e in
                Task { @MainActor in
                    let next = self.cloudLogin.after(e)
                    if case .code(_, let link?) = next, case .starting = self.cloudLogin, let u = URL(string: link) {
                        NSWorkspace.shared.open(u)
                    }
                    self.cloudLogin = next
                }
            }, onExit: { status in
                Task { @MainActor in
                    self.cloudLogin = self.cloudLogin.exited(status)
                    self.cloudLoginRun = nil
                    if self.cloudLogin == .done {
                        self.cloudLogin = .idle
                        self.refreshCloud()
                        self.say("Signed in to Conch Cloud")
                    }
                }
            })
        } catch {
            cloudLoginRun = nil
            cloudLogin = .failed("\(error)")
        }
    }

    func cloudCancelSignIn() {
        cloudLoginRun?.cancel()
        cloudLoginRun = nil
        cloudLogin = .idle
    }

    func cloudSignOut() {
        guard let b = binary else { return }
        job("Signing out of Conch Cloud", done: "Signed out of Conch Cloud") {
            defer { self.refreshCloud() }
            _ = try await runConch(b, ["logout"])
        }
    }

    /// Wakes or sleeps a machine, then reads the list again.
    func cloudMachine(_ m: CloudMachine, wake: Bool) {
        guard let b = binary else { return }
        job(wake ? "Waking \(m.name)" : "Putting \(m.name) to sleep") {
            defer { self.refreshCloud() }
            _ = try await runConch(b, ["cloud", "machine", wake ? "wake" : "sleep", m.id])
        }
    }
}

/// The section's cards below its server field.
struct CloudCard: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme
    /// While the pane is open the machines are read again every few seconds.
    private let tick = Timer.publish(every: 5, on: .main, in: .common).autoconnect()

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 16) {
            if store.cloudSignedIn {
                account
                machines
            } else {
                signedOut
            }
            if !store.cloudError.isEmpty {
                Text(store.cloudError).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
            }
        }
        .onAppear { store.refreshCloud() }
        .onReceive(tick) { _ in if store.cloudSignedIn { store.refreshCloud() } }
    }

    private var signedOut: some View {
        SettingsCard(title: "Account") {
            VStack(alignment: .leading, spacing: 10) {
                Text("Cloud machines that keep your agents working with your Mac closed — in the sidebar like any other machine.")
                    .font(.ui(size: 12.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                switch store.cloudLogin {
                case .idle, .done:
                    Button("Sign in…") { store.cloudSignIn() }.controlSize(.regular)
                case .starting:
                    HStack(spacing: 8) { ProgressView().controlSize(.small); Text("Asking for a code…").font(.ui(size: 12)) }
                case .code(let code, let link):
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Approve this code in your browser:").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                        Text(code).font(.system(size: 22, weight: .semibold, design: .monospaced)).textSelection(.enabled)
                        HStack(spacing: 8) {
                            ProgressView().controlSize(.small)
                            Text("Waiting for you to approve it…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                            Spacer()
                            if let l = link, let u = URL(string: l) {
                                Button("Open the page again") { NSWorkspace.shared.open(u) }.buttonStyle(.link)
                            }
                            Button("Cancel") { store.cloudCancelSignIn() }
                        }
                    }
                case .failed(let why):
                    VStack(alignment: .leading, spacing: 8) {
                        Text(why).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
                        Button("Try again") { store.cloudLogin = .idle; store.cloudSignIn() }
                    }
                }
            }
            .padding(16)
        }
    }

    private var account: some View {
        SettingsCard(title: "Account", trailing: AnyView(Button("Sign out") { store.cloudSignOut() }.controlSize(.small))) {
            VStack(alignment: .leading, spacing: 10) {
                if let me = store.cloud?.me {
                    HStack(spacing: 8) {
                        Text(me.handle).font(.ui(size: 15, weight: .semibold))
                        Pill(text: me.plan.capitalized, fg: Theme.accent, bg: Theme.accentDeep.opacity(0.25))
                        if let e = me.email { Text(e).font(.ui(size: 12)).foregroundStyle(Theme.muted) }
                        Spacer()
                    }
                    let lines = CloudModel.allowanceLines(me.allowance)
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text("Running time this month").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                            Spacer()
                            Text(lines.hours).font(.ui(size: 12, weight: .medium))
                        }
                        ProgressView(value: CloudModel.usedFraction(me.allowance)).progressViewStyle(.linear)
                        HStack {
                            Text("Storage").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                            Spacer()
                            Text(lines.storage).font(.ui(size: 12, weight: .medium))
                        }
                    }
                    if let d = store.cloud?.thisDevice {
                        Text("This device: \(d.name)").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    }
                    if let api = store.cloud?.api, api != CloudModel.defaultServer {
                        Text("Server: \(api)").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    }
                } else {
                    HStack(spacing: 8) { ProgressView().controlSize(.small); Text("Reading…").font(.ui(size: 12)) }
                }
            }
            .padding(16)
        }
    }

    private var machines: some View {
        SettingsCard(title: "Machines") {
            VStack(alignment: .leading, spacing: 0) {
                if store.cloudMachines.isEmpty {
                    Text(store.cloud == nil ? "Reading…" : "No cloud machines yet. conch cloud machine new NAME makes one.")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(16)
                }
                ForEach(Array(store.cloudMachines.enumerated()), id: \.element.id) { i, m in
                    if i > 0 { Divider().padding(.leading, 16) }
                    row(m)
                }
            }
        }
    }

    private func color(_ t: CloudTone) -> Color {
        switch t {
        case .on: return Theme.ok
        case .moving: return Theme.warn
        case .error: return Theme.err
        case .resting: return Theme.muted
        }
    }

    private func row(_ m: CloudMachine) -> some View {
        HStack(spacing: 10) {
            Circle().fill(color(m.tone)).frame(width: 7, height: 7)
            VStack(alignment: .leading, spacing: 2) {
                Text(m.name).font(.ui(size: 13, weight: .medium))
                Text(m.subtitle).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            }
            Spacer(minLength: 8)
            Text(m.stateText).font(.ui(size: 12)).foregroundStyle(color(m.tone)).lineLimit(2).multilineTextAlignment(.trailing)
            if m.canWake { Button("Wake") { store.cloudMachine(m, wake: true) }.controlSize(.small) }
            if m.canSleep { Button("Sleep") { store.cloudMachine(m, wake: false) }.controlSize(.small) }
            if let w = m.webURL, let u = URL(string: w) {
                Button { NSWorkspace.shared.open(u) } label: { Image(systemName: "arrow.up.right.square") }
                    .buttonStyle(.borderless).help("Manage in the browser")
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }
}
