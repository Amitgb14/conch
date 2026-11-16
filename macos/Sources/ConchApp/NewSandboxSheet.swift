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
    /// What the chosen provider's file lets be chosen, and what Settings
    /// chose: read when a provider is picked.
    @State private var settings: SandboxSettingsInfo?

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
                Button("Browse sandboxes…") { dismiss(); store.openIntegrations(kind: .sandbox) }.buttonStyle(.link)
                    .help("Install a provider from the registry: Daytona, boat.dev, Tensorlake and more")
                Button("Add a provider…") { adding = true }.buttonStyle(.link)
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create") { create() }.keyboardShortcut(.defaultAction).disabled(chosen?.ready != true)
            }
        }
        .padding(20).frame(width: 480)
        .task { await load() }
        .task(id: form.provider) { await loadSettings(form.provider) }
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
                            Text(p.origin).font(.ui(size: 10)).foregroundStyle(Theme.warn)
                                .padding(.horizontal, 6).padding(.vertical, 1)
                                .overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                                .help("Described by a provider file of your own, not one from the registry")
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
        let st = settings?.name == p.name ? settings : nil
        Form {
            TextField("Name", text: $form.label, prompt: Text("optional: how the sidebar names it"))
            if let r = st?.setting("region") {
                Picker(r.label, selection: $form.region) {
                    Text(r.default.map { "Default (\($0))" } ?? "Default").tag("")
                    ForEach(choices(r, keeping: form.region), id: \.self) { Text($0).tag($0) }
                }
            }
            if st == nil || st?.setting("image") != nil {
                let image = st?.setting("image")
                HStack(spacing: 6) {
                    TextField(image?.label ?? "Image", text: $form.snapshot, prompt: Text("optional: the provider's default"))
                    if let image, !image.choices.isEmpty {
                        Menu {
                            ForEach(image.values, id: \.self) { v in Button(v) { form.snapshot = v } }
                        } label: { Image(systemName: "chevron.down") }
                        .menuStyle(.borderlessButton).fixedSize()
                        .help("Its snapshots, from \(p.label)")
                    }
                }
            }
            switch p.takes {
            case .snapshot:
                let z = st?.size
                HStack {
                    TextField("vCPUs", text: $form.cpu, prompt: Text(limit(z?.cpu)))
                    TextField("Memory GiB", text: $form.memory, prompt: Text(limit(z?.memoryGib)))
                    TextField("Disk GiB", text: $form.disk, prompt: Text(limit(z?.diskGib)))
                }
            case .sizes(let sizes):
                Picker("Size", selection: $form.size) {
                    Text("Default").tag("")
                    ForEach(sizes, id: \.self) { Text($0).tag($0) }
                }
            }
        }
        .formStyle(.columns)
        if let e = st?.settings.compactMap(\.error).first {
            Text("Couldn't read \(p.label)'s choices: \(e)").font(.ui(size: 11)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
        }
        if let note = p.note {
            Text(note).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
        }
    }

    /// A setting's choices, with what is already chosen kept among them
    /// when the provider no longer lists it.
    private func choices(_ s: SandboxSettingsInfo.Setting, keeping v: String) -> [String] {
        var out = s.values
        if !v.isEmpty && !out.contains(v) { out.append(v) }
        return out
    }

    /// A limit as a prompt: "1–4", or "default".
    private func limit(_ l: [Int]?) -> String {
        guard let l, l.count == 2 else { return "default" }
        return "\(l[0])–\(l[1])"
    }

    /// Reads what the provider's file lets be chosen, and starts the form
    /// from what Settings chose for it.
    private func loadSettings(_ name: String) async {
        guard !name.isEmpty, let b = store.binary else { return }
        guard let out = try? await runConch(b, ["sandbox", "-provider", name, "settings", "-json"]),
              let st = try? JSONDecoder().decode(SandboxSettingsInfo.self, from: out), form.provider == name else { return }
        settings = st
        form.region = st.chosen.region ?? ""
        form.snapshot = st.chosen.image ?? ""
        form.size = st.chosen.size ?? ""
        form.cpu = st.chosen.cpu.map(String.init) ?? ""
        form.memory = st.chosen.memory.map(String.init) ?? ""
        form.disk = st.chosen.disk.map(String.init) ?? ""
    }

    private func load() async {
        guard let b = store.binary else { problem = "No conch to ask"; providers = []; return }
        do {
            let out = try await runConch(b, ["sandbox", "providers", "-json"])
            let ps = try JSONDecoder().decode([SandboxProviderInfo].self, from: out)
            providers = ps
            if let first = ps.first(where: \.ready) { form = NewSandboxForm(provider: first.name) }
            if ps.isEmpty {
                problem = "No sandbox provider is installed: Browse sandboxes in Integrations installs one."
            } else if !ps.contains(where: \.ready) {
                problem = "No provider has its API key yet: add one in Settings → Sandboxes."
            }
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
