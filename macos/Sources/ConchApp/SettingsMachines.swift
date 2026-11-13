import ConchKit
import SwiftUI

/// The machines conch reaches, in Settings → Remote machines, as Phone
/// lists the phones paired: this Mac, then machines.json's, each with how
/// it is reached, how it is now, and what can be done to it — the same
/// commands as its row's menu in the window's sidebar.
struct MachinesList: View {
    @ObservedObject var store: Store
    /// The machine whose label is being edited, and what is typed.
    @State private var naming: String?
    @State private var draft = ""
    @FocusState private var nameFocused: Bool
    @Environment(\.conchTheme) private var theme

    var rows: [MachineRow] {
        let states = Dictionary(store.machines.map {
            ($0.id, MachineState(state: $0.state, why: $0.why, version: $0.version, behind: $0.behind.count))
        }, uniquingKeysWith: { a, _ in a })
        return machineRows(catalog: store.catalog, states: states)
    }

    var body: some View {
        let _ = theme
        let rs = rows
        VStack(alignment: .leading, spacing: 8) {
            VStack(spacing: 0) {
                ForEach(Array(rs.enumerated()), id: \.element.id) { i, r in
                    if i > 0 { Divider().padding(.leading, 44) }
                    row(r)
                }
            }
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well.opacity(0.5)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
            if rs.count == 1 {
                Text("No other machines yet. Add one to run agents there over ssh: conch is installed on it, and its agents keep running when you close this.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func row(_ r: MachineRow) -> some View {
        HStack(spacing: 10) {
            LucideIcon(r.isLocal ? "laptop" : "server", color: r.online ? Theme.accent : Theme.muted, size: 16).frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                if naming == r.id {
                    TextField("Label", text: $draft)
                        .textFieldStyle(.roundedBorder).font(.ui(size: 12.5))
                        .focused($nameFocused)
                        .onSubmit { rename(r) }
                        .onExitCommand { naming = nil }
                        .onAppear { nameFocused = true }
                } else {
                    Text(r.label).font(.ui(size: 12.5, weight: .semibold)).lineLimit(1)
                }
                Text([r.detail, r.version.isEmpty ? nil : "conch \(r.version)"].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 6)
            HStack(spacing: 5) {
                Circle().fill(r.online ? (r.behind > 0 ? Theme.warn : Theme.ok) : Theme.muted).frame(width: 6, height: 6)
                Text(r.status)
            }
            .font(.ui(size: 11)).foregroundStyle(r.online ? (r.behind > 0 ? Theme.warn : Theme.ok) : Theme.muted)
            .help(r.behind > 0 ? "Its conch is from an older build: \(r.isLocal ? "reload its server" : "upgrade conch there")" : r.why)
            if r.online && r.behind > 0 {
                Button(r.isLocal ? "Reload" : "Upgrade") { r.isLocal ? store.reloadServer(r.id) : store.upgrade(r.id) }.controlSize(.small)
            }
            Menu {
                if r.enabled {
                    Button("Reconnect") { store.reconnect(r.id) }
                    Button("Reload Server") { store.reloadServer(r.id) }.disabled(!r.online)
                    if !r.isLocal { Button("Upgrade conch There") { store.upgrade(r.id) } }
                }
                if !r.isLocal {
                    Divider()
                    Button("Rename…") { draft = r.label; naming = r.id }
                    Button("Remove…", role: .destructive) { store.removeMachine(r.id) }
                }
            } label: {
                LucideIcon("ellipsis", color: Theme.muted, size: 14).padding(3).contentShape(Rectangle())
            }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            .help("What to do with \(r.label)")
        }
        .padding(.horizontal, 10).padding(.vertical, 8)
    }

    /// The typed label, as `conch machine rename` gives it.
    private func rename(_ r: MachineRow) {
        naming = nil
        let name = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != r.label else { return }
        store.renameMachine(r.id, to: name)
    }
}

/// A map of config.toml — a project's check command, a key rebound — as
/// pairs: each key with its value to change or remove, and a row at the
/// end to add one, its key picked from what conch offers or typed.
struct MapList: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField
    /// Keys to offer, with what to call each: the projects for checks,
    /// the actions for keys.
    var offer: [(key: String, label: String)] = []
    @State private var newKey = ""
    @State private var newValue = ""
    @Environment(\.conchTheme) private var theme

    var entries: [(key: String, value: String)] { model.form?.mapEntries(section, field) ?? [] }
    var saved: [String: String] {
        guard let f = model.form else { return [:] }
        return Dictionary(SettingsForm(f.payload).mapEntries(section, field).map { ($0.key, $0.value) }, uniquingKeysWith: { a, _ in a })
    }

    func name(_ key: String) -> String { offer.first { $0.key == key }?.label ?? key }

    var body: some View {
        let _ = theme
        let es = entries, was = saved
        SettingsCard(title: field.label) {
            VStack(alignment: .leading, spacing: 0) {
                if let h = field.help {
                    Text(h).font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 16).padding(.top, 4).padding(.bottom, 8)
                }
                ForEach(es, id: \.key) { e in
                    HStack(spacing: 10) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(name(e.key)).font(.ui(size: 12.5, weight: .medium)).lineLimit(1)
                            if name(e.key) != e.key {
                                Text(e.key).font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1)
                            }
                        }
                        .frame(width: 190, alignment: .leading)
                        TextField(field.placeholder ?? "", text: Binding(
                            get: { model.form?.mapEntries(section, field).first { $0.key == e.key }?.value ?? "" },
                            set: { v in model.form?.setMapEntry(section, field, e.key, v) }))
                            .textFieldStyle(.roundedBorder).font(.ui(size: 12, design: .monospaced))
                        if was[e.key] != e.value {
                            Circle().fill(Theme.warn).frame(width: 6, height: 6).help("Unsaved")
                        }
                        Button { withAnimation(.easeOut(duration: 0.15)) { _ = model.form?.setMapEntry(section, field, e.key, nil) } } label: {
                            LucideIcon("trash-2", color: Theme.muted, size: 13).padding(3).contentShape(Rectangle())
                        }
                        .buttonStyle(.plain).help("Remove \(name(e.key))")
                    }
                    .padding(.horizontal, 16).padding(.vertical, 6)
                    .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
                }
                addRow(es)
            }
        }
    }

    private func addRow(_ es: [(key: String, value: String)]) -> some View {
        let taken = Set(es.map(\.key))
        let left = offer.filter { !taken.contains($0.key) }
        let key = newKey.trimmingCharacters(in: .whitespaces)
        return HStack(spacing: 10) {
            HStack(spacing: 4) {
                TextField(field.keyLabel ?? "Key", text: $newKey).textFieldStyle(.roundedBorder).font(.ui(size: 12, design: .monospaced))
                if !left.isEmpty {
                    Menu {
                        ForEach(left, id: \.key) { o in
                            Button(o.label == o.key ? o.key : "\(o.label) — \(o.key)") { newKey = o.key }
                        }
                    } label: { Image(systemName: "chevron.down").font(.system(size: 10, weight: .semibold)) }
                    .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
                    .help("Choose a \((field.keyLabel ?? "key").lowercased())")
                }
            }
            .frame(width: 190)
            TextField(field.placeholder ?? "", text: $newValue).textFieldStyle(.roundedBorder).font(.ui(size: 12, design: .monospaced))
                .onSubmit { add() }
            Button("Add") { add() }.controlSize(.small).disabled(key.isEmpty || taken.contains(key))
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    private func add() {
        let key = newKey.trimmingCharacters(in: .whitespaces)
        guard !key.isEmpty, model.form?.mapEntries(section, field).contains(where: { $0.key == key }) == false else { return }
        withAnimation(.easeOut(duration: 0.15)) { _ = model.form?.setMapEntry(section, field, key, newValue) }
        newKey = ""
        newValue = ""
    }
}
