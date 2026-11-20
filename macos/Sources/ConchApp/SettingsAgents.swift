import ConchKit
import SwiftUI

/// Settings → Agents' Installed agents: every agent a machine reports, its
/// version, and Install for the ones it lacks — one machine at a time,
/// since agents are installed per machine. It watches the store, as
/// RelayCard does, because the form around it doesn't.
struct InstalledAgentsCard: View {
    @ObservedObject var store: Store
    @State private var machine = "local"
    /// Folded to its one line by default: the list is long, and the
    /// accounts above it are what people come here for.
    @AppStorage("conch.settings.installedAgentsOpen") private var open = false
    @Environment(\.conchTheme) private var theme

    var online: [MachineLink] { store.machines.filter { $0.state.isEmpty || $0.id == "local" } }

    var body: some View {
        let _ = theme
        let status = store.agentStatus[machine]
        let installing = Set(store.installingAgents.compactMap { k -> String? in
            let (m, a) = unkey(k)
            return m == machine ? a : nil
        })
        let rows = installedAgentRows(status ?? [], installing: installing, canInstall: store.installsAgents(on: machine))
        SettingsCard(title: "Installed agents", trailing: AnyView(header)) {
            if !open {
                // Folded: the installed agents' marks, a click from the
                // list, with as much room under them as above.
                let marks = rows.filter { if case .installed = $0.state { return true } else { return false } }
                Button { open = true } label: {
                    HStack(spacing: 8) {
                        ForEach(marks) { r in Mark(agent: r.name).frame(width: 16).help(r.title) }
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.horizontal, 16).padding(.top, 8).padding(.bottom, 12)
                .accessibilityLabel("Show the installed agents")
            }
            if open {
            // Room under the header before the first row.
            Color.clear.frame(height: 8)
            if status == nil {
                Text(store.conns[machine] == nil ? "\(store.label(machine)) is not connected" : "Asking \(store.label(machine))…")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 16).padding(.vertical, 12)
            }
            ForEach(rows) { r in
                Divider()
                row(r)
            }
            Divider()
            Text("Install runs the agent's own installer in a terminal on \(machine == "local" ? "this Mac" : store.label(machine)); the terminal opens if it fails.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 16).padding(.vertical, 10)
            }
        }
        .onAppear { if status == nil { store.checkAgents(on: machine) } }
        .onChange(of: machine) { _, m in store.checkAgents(on: m) }
    }

    var header: some View {
        HStack(spacing: 8) {
            Button { open.toggle() } label: {
                HStack(spacing: 4) {
                    Image(systemName: open ? "chevron.down" : "chevron.right").font(.system(size: 10, weight: .semibold))
                    Text(open ? "Hide" : "Show").font(.ui(size: 12))
                }
                .foregroundStyle(Theme.text2)
            }
            .buttonStyle(.plain).help(open ? "Hide the installed agents" : "Show the installed agents")
            if online.count > 1 {
                Picker("Machine", selection: $machine) {
                    ForEach(online) { m in
                        Text("\(m.id == "local" ? "This Mac" : m.label) · \(installedAgentsSummary(store.agentStatus[m.id]))").tag(m.id)
                    }
                }
                .labelsHidden().pickerStyle(.menu).fixedSize()
            } else {
                Text("This Mac · \(installedAgentsSummary(store.agentStatus[machine]))")
                    .font(.ui(size: 12)).foregroundStyle(Theme.text2)
            }
            Button { store.checkAgents(on: machine) } label: {
                Image(systemName: "arrow.clockwise").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.text2)
            }
            .buttonStyle(.plain).help("Check Again").accessibilityLabel("Check Again")
        }
    }

    func row(_ r: InstalledAgentRow) -> some View {
        HStack(spacing: 12) {
            Mark(agent: r.name).frame(width: 18)
            VStack(alignment: .leading, spacing: 1) {
                Text(r.title).font(.ui(size: 13)).foregroundStyle(Theme.text)
                if let note = r.note {
                    Text(note).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
            }
            Spacer()
            switch r.state {
            case .installed(let version):
                if !version.isEmpty {
                    Text(version).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
                }
                Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.ok)
            case .installing:
                ProgressView().controlSize(.small)
                Text("Installing…").font(.ui(size: 12)).foregroundStyle(Theme.work)
            case .missing:
                Text("Not installed").font(.ui(size: 12)).foregroundStyle(Theme.warn)
                Button("Install") { store.install(agent: r.name, on: machine) }
                    .buttonStyle(.borderedProminent).tint(Theme.accentDeep).controlSize(.small)
            case .noInstaller:
                Text("Not installed").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            case .unavailable:
                Text("Not installed · upgrade conch there to install").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
    }
}

/// An agent menu that says which agents this Mac lacks. It watches the
/// store itself: the row around it is drawn by the form, which does not
/// change when the agent list arrives.
struct AgentMenu: View {
    @ObservedObject var store: Store
    let options: [SettingsOption]
    @Binding var value: String

    var body: some View {
        RichMenu(options: options, value: $value, agents: true, notes: agentMenuNotes(store.agentStatus["local"]))
    }
}
