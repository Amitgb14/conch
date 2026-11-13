import AppKit
import ConchKit
import SwiftUI

// New Sandbox…, as the TUI's M → New sandbox…: a provider, a name, and
// what to make it from, then `conch sandbox -provider P create` in a
// terminal of its own so the install is watched, as Add Machine… does with
// `conch machine add`. The sandbox shows in the sidebar when machines.json
// has it. Which providers have their key here is conch's to say
// (`conch sandbox providers -json`), so one without is shown, not offered.

struct NewSandboxSheet: View {
    @ObservedObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @Environment(\.conchTheme) private var theme
    @State private var providers: [SandboxProviderInfo]?
    @State private var form = NewSandboxForm(provider: "")
    @State private var problem = ""
    @State private var adding = false

    var chosen: SandboxProviderInfo? { providers?.first { $0.name == form.provider } }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            Text("New sandbox").font(.ui(size: 15, weight: .semibold))
            Text("A machine in the cloud: conch is installed there and it is added to the sidebar. It runs, and costs, until you stop it.")
                .font(.ui(size: 12)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let ps = providers {
                VStack(spacing: 6) {
                    ForEach(ps) { p in providerRow(p) }
                }
                if let p = chosen, p.ready { fields(p) }
            } else {
                HStack(spacing: 8) { ProgressView().controlSize(.small); Text("Asking conch which providers it has…").font(.ui(size: 12)) }
            }
            if !problem.isEmpty {
                Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("Sandbox settings…") { dismiss(); store.openSettings("sandbox") }.buttonStyle(.link)
                Button("Add a provider…") { adding = true }.buttonStyle(.link)
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create") { create() }.keyboardShortcut(.defaultAction).disabled(chosen?.ready != true)
            }
        }
        .padding(20).frame(width: 480)
        .task { await load() }
        .sheet(isPresented: $adding) { AddProviderSheet(store: store) { Task { await load() } } }
    }

    private func providerRow(_ p: SandboxProviderInfo) -> some View {
        let on = form.provider == p.name
        return Button {
            guard p.ready else { return }
            form = NewSandboxForm(provider: p.name)
            problem = ""
        } label: {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: on ? "largecircle.fill.circle" : "circle")
                    .foregroundStyle(p.ready ? (on ? Theme.accent : Theme.muted) : Theme.muted.opacity(0.5))
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(p.label).font(.ui(size: 13, weight: .medium)).foregroundStyle(p.ready ? Theme.text : Theme.muted)
                        if p.adapted == true {
                            Text("adapted").font(.ui(size: 10)).foregroundStyle(Theme.warn)
                                .padding(.horizontal, 6).padding(.vertical, 1)
                                .overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                                .help("Described by a file conch adapted from its API docs, not built into conch")
                        }
                    }
                    if !p.ready, let why = p.why {
                        Text("Needs its API key: " + why).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer()
            }
            .padding(10)
            .background(RoundedRectangle(cornerRadius: 8).fill(on ? Theme.accent.opacity(0.10) : Color.primary.opacity(0.04)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(on ? Theme.accent.opacity(0.6) : Color.primary.opacity(0.08)))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!p.ready)
    }

    @ViewBuilder private func fields(_ p: SandboxProviderInfo) -> some View {
        Form {
            TextField("Name", text: $form.label, prompt: Text("optional: how the sidebar names it"))
            switch p.takes {
            case .snapshot:
                TextField("Snapshot", text: $form.snapshot, prompt: Text("optional: the provider's default"))
                HStack {
                    TextField("vCPUs", text: $form.cpu, prompt: Text("default"))
                    TextField("Memory GiB", text: $form.memory, prompt: Text("default"))
                    TextField("Disk GiB", text: $form.disk, prompt: Text("default"))
                }
            case .sizes(let sizes):
                Picker("Size", selection: $form.snapshot) {
                    Text("Default").tag("")
                    ForEach(sizes, id: \.self) { Text($0).tag($0) }
                }
            case .place(let hint):
                TextField("Where", text: $form.snapshot, prompt: Text(hint))
            }
        }
        .formStyle(.columns)
        if let note = p.note {
            Text(note).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
        }
    }

    private func load() async {
        guard let b = store.binary else { problem = "No conch to ask"; providers = []; return }
        do {
            let out = try await runConch(b, ["sandbox", "providers", "-json"])
            let ps = try JSONDecoder().decode([SandboxProviderInfo].self, from: out)
            providers = ps
            if let first = ps.first(where: \.ready) { form = NewSandboxForm(provider: first.name) }
            if !ps.contains(where: \.ready) { problem = "No provider has its API key yet: add one in Settings → Sandboxes." }
        } catch {
            providers = []
            problem = "conch couldn't list its providers: \(error)"
        }
    }

    private func create() {
        guard let p = chosen else { return }
        switch form.arguments(for: p) {
        case .failure(let e):
            problem = e.message
        case .success(let args):
            store.expectMachine()
            store.runCLIPane(name: "sandbox create · " + p.label, args: args)
            dismiss()
        }
    }
}
