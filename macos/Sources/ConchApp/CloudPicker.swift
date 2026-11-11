import AppKit
import ConchKit
import SwiftUI

// Conch Cloud as a place a project goes, for the Add project and Move
// sheets: signed out, the sign-in (Settings → Conch Cloud's, run here);
// signed in, the account's machines and a new one's name and size
// (ConchKit/CloudPlace.swift decides the defaults).

/// Signed out: "Sign in to use", then the code to approve, as Settings
/// shows it. The sheet goes on by itself once conch says it is signed in.
struct CloudSignInPrompt: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 8) {
            Text("Cloud machines keep your agents working with your Mac closed. Sign in to use them.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            switch store.cloudLogin {
            case .idle, .done:
                Button("Sign in to use") { store.cloudSignIn() }
            case .starting:
                HStack(spacing: 8) { ProgressView().controlSize(.small); Text("Asking for a code…").font(.ui(size: 12)) }
            case .code(let code, let link):
                HStack(spacing: 10) {
                    Text(code).font(.system(size: 17, weight: .semibold, design: .monospaced)).textSelection(.enabled)
                    ProgressView().controlSize(.small)
                    Text("Approve it in your browser…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    Spacer()
                    if let l = link, let u = URL(string: l) {
                        Button("Open the page again") { NSWorkspace.shared.open(u) }.buttonStyle(.link)
                    }
                    Button("Cancel") { store.cloudCancelSignIn() }
                }
            case .failed(let why):
                HStack(spacing: 8) {
                    Text(why).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
                    Button("Try again") { store.cloudLogin = .idle; store.cloudSignIn() }
                }
            }
        }
    }
}

/// A new cloud machine's name and size.
struct CloudNewMachineFields: View {
    @ObservedObject var store: Store
    @Binding var name: String
    @Binding var size: String
    @Environment(\.conchTheme) private var theme

    /// The sizes conch says, or the CLI's own list while they are read.
    var sizes: [CloudSize] {
        if !store.cloudSizes.isEmpty { return store.cloudSizes }
        return ["small", "standard", "large", "xl"].map { CloudSize(id: $0, name: $0.capitalized, vcpu: 0, memoryGB: 0, diskGB: 0, plans: []) }
    }

    var body: some View {
        let _ = theme
        let plan = store.cloud?.me?.plan
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text("Name").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
                TextField("", text: $name, prompt: Text("box"))
                    .textFieldStyle(.plain).font(.ui(size: 12.5, design: .monospaced))
                    .padding(.horizontal, 9).frame(height: 28)
                    .background(RoundedRectangle(cornerRadius: 7).fill(Theme.well))
                    .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
            }
            if !name.isEmpty && !validCloudMachineName(name) {
                Text("Lowercase letters, digits and -, starting with a letter; 31 at most")
                    .font(.ui(size: 11)).foregroundStyle(Theme.warn).padding(.leading, 78)
            } else if store.cloudMachines.contains(where: { $0.name == name }) {
                Text("The account has a machine called \(name)").font(.ui(size: 11)).foregroundStyle(Theme.warn).padding(.leading, 78)
            }
            HStack(spacing: 8) {
                Text("Size").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
                Picker("", selection: $size) {
                    ForEach(sizes) { s in
                        Text(s.vcpu > 0 ? s.label : s.name).tag(s.id).disabled(!s.offered(to: plan))
                    }
                }
                .labelsHidden().frame(maxWidth: 280, alignment: .leading)
                Spacer()
            }
        }
        .onAppear {
            store.refreshCloudSizes()
            if size.isEmpty || !sizes.contains(where: { $0.id == size }) { size = defaultCloudSize(sizes, plan: plan) ?? "standard" }
        }
        .onChange(of: store.cloudSizes) { _, s in
            if !s.contains(where: { $0.id == size && $0.offered(to: plan) }) { size = defaultCloudSize(s, plan: plan) ?? size }
        }
    }
}

/// Whether a new machine's name and size will do.
@MainActor func cloudNewReady(_ store: Store, name: String, size: String) -> Bool {
    validCloudMachineName(name) && !size.isEmpty && !store.cloudMachines.contains { $0.name == name }
}

/// A cloud machine's state colour, as Settings draws it.
@MainActor func cloudToneColor(_ t: CloudTone) -> Color {
    switch t {
    case .on: return Theme.ok
    case .moving: return Theme.warn
    case .error: return Theme.err
    case .resting: return Theme.muted
    }
}
