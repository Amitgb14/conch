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
    /// The providers' icons, as conch downloaded them from the registry.
    @State private var icons: [String: String] = [:]
    /// The provider's note in full, rather than its first lines.
    @State private var noteOpen = false

    var chosen: SandboxProviderInfo? { providers?.first { $0.name == form.provider } }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            VStack(alignment: .leading, spacing: 16) {
                if let ps = providers {
                    if !ps.isEmpty {
                        caption("Provider")
                        VStack(spacing: 6) { ForEach(ps) { p in providerRow(p) } }
                    }
                    if let p = chosen, p.ready {
                        caption("Sandbox")
                        fields(p)
                    }
                } else {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Asking conch which providers it has…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                    .padding(.vertical, 20)
                }
                if !problem.isEmpty {
                    Label { Text(problem).fixedSize(horizontal: false, vertical: true) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .font(.ui(size: 12)).foregroundStyle(Theme.err)
                }
            }
            .padding(20)
            Divider()
            footer
        }
        .frame(width: 520)
        .background(Theme.window)
        // Nothing wears a focus ring: the first link took the keyboard's
        // focus as the sheet opened and looked chosen.
        .focusEffectDisabled()
        .task { await load() }
        .task { await loadIcons() }
        .task(id: form.provider) { await loadSettings(form.provider) }
        .sheet(isPresented: $adding) { AddProviderSheet(store: store) { Task { await load() } } }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            LucideIcon("cloud", color: .white, size: 17)
                .frame(width: 34, height: 34)
                .background(RoundedRectangle(cornerRadius: 9).fill(Theme.accent))
            VStack(alignment: .leading, spacing: 2) {
                Text("New sandbox").font(.ui(size: 15, weight: .semibold)).foregroundStyle(Theme.text)
                Text("A machine in the cloud with conch installed, added to the sidebar. It runs, and costs, until you stop it.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 20).padding(.vertical, 16)
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Menu {
                Button("Sandbox Settings…") { dismiss(); store.openSettings("sandbox") }
                Button("Browse Sandboxes…") { dismiss(); store.openIntegrations(kind: .sandbox) }
                Button("Add a Provider from Its API Docs…") { adding = true }
            } label: {
                Label("Providers", systemImage: "slider.horizontal.3").font(.ui(size: 12))
            }
            .menuStyle(.borderlessButton).fixedSize()
            .help("Its settings, installing one from the registry, or adding one conch doesn't know")
            Spacer()
            Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
            Button(chosen.map { "Create on \($0.label)" } ?? "Create") { create() }
                .keyboardShortcut(.defaultAction).buttonStyle(.borderedProminent)
                .disabled(chosen?.ready != true)
        }
        .controlSize(.regular)
        .padding(.horizontal, 20).padding(.vertical, 12)
    }

    private func caption(_ s: String) -> some View {
        Text(s.uppercased()).font(.ui(size: 10.5, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.muted)
    }

    private func providerRow(_ p: SandboxProviderInfo) -> some View {
        let on = form.provider == p.name
        return Button {
            guard p.ready else { return }
            form = NewSandboxForm(provider: p.name)
            noteOpen = false
            problem = ""
        } label: {
            HStack(spacing: 11) {
                IntegrationMark(label: p.label, icon: icons[p.name], size: 30)
                    .opacity(p.ready ? 1 : 0.5)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(p.label).font(.ui(size: 13, weight: .semibold)).foregroundStyle(p.ready ? Theme.text : Theme.muted)
                        if p.adapted == true {
                            Text(p.origin).font(.ui(size: 10, weight: .medium)).foregroundStyle(Theme.warn)
                                .padding(.horizontal, 6).padding(.vertical, 1)
                                .overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                                .help("A provider file of your own, not one from the registry")
                        }
                    }
                    HStack(spacing: 5) {
                        Circle().fill(p.ready ? Theme.ok : Theme.muted.opacity(0.6)).frame(width: 6, height: 6)
                        Text(p.ready ? "Key set" : (p.why ?? "Needs its API key"))
                            .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                    }
                }
                Spacer(minLength: 8)
                if p.ready {
                    Image(systemName: on ? "checkmark.circle.fill" : "circle")
                        .font(.system(size: 16)).foregroundStyle(on ? Theme.accent : Theme.line)
                } else {
                    Button("Set Key…") { dismiss(); store.openSettings("sandbox") }
                        .controlSize(.small).focusable(false)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 10)
            .background(RoundedRectangle(cornerRadius: 10).fill(on ? Theme.accent.opacity(0.09) : Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? Theme.accent.opacity(0.7) : Theme.line, lineWidth: on ? 1.5 : 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .focusable(false)
    }

    @ViewBuilder private func fields(_ p: SandboxProviderInfo) -> some View {
        let st = settings?.name == p.name ? settings : nil
        VStack(spacing: 0) {
            row("Name", first: true) {
                input("optional: how the sidebar names it", $form.label)
            }
            if let r = st?.setting("region") {
                row(r.label) {
                    Picker("", selection: $form.region) {
                        Text(r.default.map { "Default (\($0))" } ?? "Account default").tag("")
                        ForEach(choices(r, keeping: form.region), id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden().pickerStyle(.menu).fixedSize()
                    Spacer()
                }
            }
            if st == nil || st?.setting("image") != nil {
                let image = st?.setting("image")
                row(image?.label ?? "Image") {
                    input("optional: \(p.label)'s default", $form.snapshot)
                    if let image, !image.choices.isEmpty {
                        Menu {
                            ForEach(image.values, id: \.self) { v in Button(v) { form.snapshot = v } }
                        } label: { Image(systemName: "chevron.down").font(.system(size: 10, weight: .semibold)) }
                        .menuStyle(.borderlessButton).fixedSize()
                        .help("Your snapshots at \(p.label)")
                    }
                }
            }
            switch p.takes {
            case .snapshot:
                let z = st?.size
                row("Size") {
                    number("vCPU", $form.cpu, z?.cpu)
                    number("GiB", $form.memory, z?.memoryGib)
                    number("GiB disk", $form.disk, z?.diskGib)
                    Spacer(minLength: 0)
                }
            case .sizes(let sizes):
                row("Size") {
                    Picker("", selection: $form.size) {
                        Text("Default").tag("")
                        ForEach(sizes, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                    .labelsHidden().pickerStyle(.segmented).fixedSize()
                    Spacer()
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
        if let e = st?.settings.compactMap(\.error).first {
            Text("Couldn't read \(p.label)'s choices: \(e)").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                .lineLimit(2).fixedSize(horizontal: false, vertical: true)
        }
        if let note = p.note, !note.isEmpty {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "info.circle").font(.system(size: 12)).foregroundStyle(Theme.muted)
                VStack(alignment: .leading, spacing: 3) {
                    Text(note).font(.ui(size: 11.5)).foregroundStyle(Theme.text2)
                        .lineLimit(noteOpen ? nil : 2).fixedSize(horizontal: false, vertical: true)
                    Button(noteOpen ? "Less" : "More") { noteOpen.toggle() }
                        .buttonStyle(.plain).font(.ui(size: 11, weight: .medium)).foregroundStyle(Theme.accent).focusable(false)
                }
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
        }
    }

    /// A row of the sandbox's card: its label at the left, a line above it
    /// but for the first.
    private func row<C: View>(_ label: String, first: Bool = false, @ViewBuilder _ content: () -> C) -> some View {
        VStack(spacing: 0) {
            if !first { Divider().padding(.leading, 12) }
            HStack(spacing: 10) {
                Text(label).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 96, alignment: .leading)
                content()
            }
            .padding(.horizontal, 12).frame(minHeight: 40)
        }
    }

    private func input(_ prompt: String, _ text: Binding<String>) -> some View {
        TextField("", text: text, prompt: Text(prompt).foregroundColor(Theme.muted))
            .textFieldStyle(.plain).font(.ui(size: 12.5))
            .padding(.horizontal, 8).frame(height: 26)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
    }

    /// A number with its unit, the file's limits as its prompt.
    private func number(_ unit: String, _ text: Binding<String>, _ l: [Int]?) -> some View {
        HStack(spacing: 4) {
            TextField("", text: text, prompt: Text(limit(l)).foregroundColor(Theme.muted))
                .textFieldStyle(.plain).font(.ui(size: 12.5)).multilineTextAlignment(.trailing)
                .padding(.horizontal, 6).frame(width: 52, height: 26)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
            Text(unit).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
        }
    }

    /// The providers' icons, downloaded by conch from the registry.
    private func loadIcons() async {
        guard let b = store.binary,
              let out = try? await runConch(b, ["integration", "icons", "-json"]),
              let got = try? JSONDecoder().decode(IntegrationIcons.self, from: out) else { return }
        icons = got.icons ?? [:]
    }

    /// A setting's choices, with what is already chosen kept among them
    /// when the provider no longer lists it.
    private func choices(_ s: SandboxSettingsInfo.Setting, keeping v: String) -> [String] {
        var out = s.values
        if !v.isEmpty && !out.contains(v) { out.append(v) }
        return out
    }

    /// A limit as a prompt: "1–4", or "auto".
    private func limit(_ l: [Int]?) -> String {
        guard let l, l.count == 2 else { return "auto" }
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
