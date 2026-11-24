import AppKit
import ConchKit
import SwiftUI

/// Settings → Agents' Agent accounts: a work and a home subscription of
/// Claude Code or Codex side by side. Each account on a line — signed in or
/// not, its plan as bars with the failover line, the tokens it used this
/// week — and the rest in its ⋯ menu; each agent's failover line in its
/// group; the folders pinned to one; every agent's failover line. One
/// machine at a time, since each server keeps its own. It watches the
/// store, as InstalledAgentsCard does.
struct AgentAccountsCard: View {
    @ObservedObject var store: Store
    @State private var machine = "local"
    @State private var adding = false
    @State private var pinning = false
    @State private var removing: AgentAccount?
    /// The shared line while its slider is being dragged; sent on release.
    @State private var dragging: Double?
    /// An agent's own line while its slider is being dragged.
    @State private var draggingFor: [String: Double] = [:]
    /// The account whose usage is open, in the usage window.
    @State private var usageOf: String?
    @Environment(\.conchTheme) private var theme

    var online: [MachineLink] { store.machines.filter { $0.state.isEmpty || $0.id == "local" } }

    var body: some View {
        let _ = theme
        let list = store.agentAccounts[machine]
        SettingsCard(title: "Agent accounts", trailing: AnyView(header)) {
            if !store.keepsAccounts(on: machine) {
                note(store.conns[machine] == nil ? "\(store.label(machine)) is not connected"
                     : "conch on \(store.label(machine)) is older and keeps one account per agent: upgrade it there")
            } else if let list {
                Color.clear.frame(height: 4)
                accounts(list)
                Divider()
                pins(list)
                Divider()
                failover(list)
                Divider()
                note("Each account is a folder the agent signs in to once. A new one starts empty: copy or link your CLAUDE.md and skills into it. Only Claude Code and Codex can have more than one.")
            } else {
                note("Asking \(store.label(machine))…")
            }
        }
        .onAppear { load() }
        .onChange(of: machine) { _, _ in load() }
        .sheet(isPresented: $adding) {
            AddAccountSheet(list: list ?? AgentAccountList(), machineLabel: machine == "local" ? "this Mac" : store.label(machine)) { agent, name, dir, signIn in
                store.addAccount(agent: agent, name: name, dir: dir, on: machine, signIn: signIn)
            }
        }
        .sheet(isPresented: $pinning) {
            PinAccountSheet(list: list ?? AgentAccountList(), local: machine == "local") { path, name in
                store.pinAccount(path: path, name: name, on: machine)
            }
        }
        .sheet(isPresented: Binding(get: { usageOf != nil }, set: { if !$0 { usageOf = nil } })) {
            UsageWindow(store: store, machine: machine, start: usageOf)
        }
        .confirmationDialog(removing.map { "Remove \($0.agent)'s \($0.name) account?" } ?? "", isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } }), presenting: removing) { a in
            Button("Remove", role: .destructive) { store.removeAccount(agent: a.agent, name: a.name, on: machine) }
        } message: { a in
            let pinned = agentAccountPins(named: a.name, in: list ?? AgentAccountList()).count
            Text("conch forgets it; its folder, sign-in and conversations stay\(a.dir.map { " in \($0)" } ?? "").\(pinned > 0 ? " \(pinned) folder\(pinned == 1 ? " is" : "s are") still pinned to \(a.name)." : "")")
        }
    }

    func load() {
        store.loadAccounts(on: machine)
        store.loadAccountUsage(on: machine)
        store.loadRanks(on: machine, agent: "claude")
        store.refreshLimits(on: machine)
    }

    var header: some View {
        HStack(spacing: 8) {
            if online.count > 1 {
                Picker("Machine", selection: $machine) {
                    ForEach(online) { m in Text(m.id == "local" ? "This Mac" : m.label).tag(m.id) }
                }
                .labelsHidden().pickerStyle(.menu).fixedSize()
            }
            Button("Add Account…") { adding = true }
                .controlSize(.small).disabled(store.agentAccounts[machine] == nil)
            Button { load() } label: {
                Image(systemName: "arrow.clockwise").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.text2)
            }
            .buttonStyle(.plain).help("Check Again").accessibilityLabel("Check Again")
        }
    }

    func note(_ s: String) -> some View {
        Text(s).font(.ui(size: 12)).foregroundStyle(Theme.muted)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16).padding(.vertical, 10)
    }

    /// This machine's accounts with their plans and usage.
    func merged(_ list: AgentAccountList) -> [UsageAccount] {
        usageAccounts(limits: store.plans.on(machine), history: store.accountUsage[machine], list: list)
    }

    @ViewBuilder func accounts(_ list: AgentAccountList) -> some View {
        let all = merged(list)
        ForEach(Array(agentAccountGroups(list).enumerated()), id: \.element.id) { i, g in
            if i > 0 { Divider() }
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 8) {
                    Mark(agent: g.agent).frame(width: 18)
                    Text(store.agentTitle(g.agent)).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                    Text("\(g.accounts.count) account\(g.accounts.count == 1 ? "" : "s")").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    Spacer()
                    // Each agent's own line, whatever every agent's is: the
                    // mock's, and found where the agent is.
                    if store.failoverPerAgent(on: machine) {
                        agentLine(g.agent, list)
                    }
                }
                .padding(.horizontal, 16).padding(.top, 10).padding(.bottom, 4)
                ForEach(g.accounts) { a in
                    row(a, all.first { $0.agent == a.agent && $0.name == (a.isDefault == true ? "" : a.name) }, line: failoverLine(list, agent: a.agent))
                }
            }
        }
    }

    /// An agent's own failover line, or every agent's.
    func agentLine(_ agent: String, _ list: AgentAccountList) -> some View {
        let own = list.failoverFor?[agent]
        let shared = list.failoverAt ?? 0
        // What the menu shows: every agent's line, the agent's own, or off.
        let choice = own == nil ? "shared" : own == 0 ? "off" : "own"
        return HStack(spacing: 8) {
            Text("Failover").font(.ui(size: 12)).foregroundStyle(Theme.text2)
            Picker("Failover", selection: Binding(get: { choice }, set: { c in
                switch c {
                case "own": store.setAccountFailover(own.flatMap { $0 > 0 ? $0 : nil } ?? max(shared, 80), agent: agent, on: machine)
                case "off": store.setAccountFailover(0, agent: agent, on: machine)
                default: store.setAccountFailover(0, agent: agent, shared: true, on: machine)
                }
            })) {
                Text("Every agent's (\(agentAccountFailoverLabel(shared)))").tag("shared")
                Text("Its own line").tag("own")
                Text("Off for \(store.agentTitle(agent))").tag("off")
            }
            .labelsHidden().pickerStyle(.menu).controlSize(.small).fixedSize()
            .help("When a new \(store.agentTitle(agent)) agent's account is past this line, it starts on the next account; agents in use move at it when move_at_line is on")
            if let own, own > 0 {
                let v = draggingFor[agent] ?? Double(own)
                Slider(value: Binding(get: { v }, set: { draggingFor[agent] = $0 }), in: 50...100, step: 1) { editing in
                    if !editing, let d = draggingFor[agent] {
                        store.setAccountFailover(Int(d), agent: agent, on: machine)
                        draggingFor[agent] = nil
                    }
                }
                .frame(width: 110).controlSize(.small)
                Text("\(Int(v))%").font(.ui(size: 11.5, weight: .semibold)).monospacedDigit().frame(width: 36, alignment: .trailing)
            }
        }
    }

    func row(_ a: AgentAccount, _ u: UsageAccount?, line: Int) -> some View {
        let inView = store.agentInView(on: machine).map { $0.agent == a.agent && ($0.account ?? "") == (a.isDefault == true ? "" : a.name) } == true
        return HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(a.isDefault == true ? "default" : a.name).font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text)
                    switch agentAccountSignIn(a) {
                    case .own: badge("its own", Theme.text2, Theme.raise2)
                    case .signedIn: badge("✓ Signed in", Theme.ok, Theme.ok.opacity(0.12))
                    case .signIn: EmptyView()
                    }
                    if inView { badge("in view", Theme.accent, Theme.accent.opacity(0.14)) }
                }
                Text(a.dir ?? (a.agent == "codex" ? "~/.codex" : "~/.claude")).font(.system(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
                    .lineLimit(1).truncationMode(.middle).help(a.dir ?? "")
            }
            .frame(minWidth: 150, maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .leading, spacing: 5) {
                if let l = u?.limits, l.reportsUse {
                    TimelineView(.periodic(from: .now, by: 30)) { ctx in
                        VStack(alignment: .leading, spacing: 5) {
                            ForEach(l.bars(now: ctx.date)) { bar($0.label, $0.usedPct, line) }
                            if l.barsReset(now: ctx.date) { Text("Reset since last reported").font(.ui(size: 11)).foregroundStyle(Theme.muted) }
                        }
                    }
                } else {
                    let gap = store.usageGap(on: machine, needs: agentLimitsCapability)
                    Text(gap == nil ? "No plan reported yet" : "Can't read its plan").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        .help(gap ?? "")
                }
            }
            .frame(width: 190, alignment: .leading)
            VStack(alignment: .trailing, spacing: 0) {
                Text(humanCount(spanTotal(u?.usage, .week)?.tokens.total ?? 0)).font(.ui(size: 13, weight: .semibold)).monospacedDigit()
                Text("tokens this week").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
            }
            .frame(width: 92, alignment: .trailing)
            .opacity(u?.usage == nil && store.usageGap(on: machine, needs: usageAccountsCapability) != nil ? 0 : 1)
            HStack(spacing: 6) {
                let off = machine == "local" ? store.movedOff(a.isDefault == true ? "default" : a.name, on: machine).filter { k in store.pane(k)?.agent?.name == a.agent } : []
                if !off.isEmpty {
                    let room = moveBack(movedFrom: a.isDefault == true ? "default" : a.name, ranks: store.accountRanks[machine + "|" + a.agent] ?? []) != nil
                    Button("Move \(off.count) Back") { store.moveAllBack(to: a.isDefault == true ? "default" : a.name, on: machine) }
                        .controlSize(.small).disabled(!room)
                        .help(room ? "\(off.count) agent\(off.count == 1 ? " was" : "s were") moved off this account: move them back" : "\(off.count) agent\(off.count == 1 ? " was" : "s were") moved off this account; it has no room yet")
                }
                if agentAccountSignIn(a) == .signIn {
                    Button("Sign In") { store.signIn(agent: a.agent, account: a.name, on: machine) }
                        .controlSize(.small).help("Start \(store.agentTitle(a.agent)) on \(a.name) and sign in there once with /login")
                }
                Menu {
                    Button("Open Usage") { usageOf = u?.id }
                    if a.isDefault != true {
                        Button(a.signedIn == true ? "Sign In Again" : "Sign In") { store.signIn(agent: a.agent, account: a.name, on: machine) }
                        if machine == "local", let d = a.dir {
                            Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: d)]) }
                        }
                        Divider()
                        Button("Remove…", role: .destructive) { removing = a }
                    }
                } label: {
                    Image(systemName: "ellipsis").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.muted).frame(width: 22, height: 22)
                }
                .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize().accessibilityLabel("More for \(a.name)")
            }
            .frame(minWidth: 60, alignment: .trailing)
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { usageOf = u?.id }
    }

    func badge(_ s: String, _ fg: Color, _ bg: Color) -> some View {
        Text(s).font(.ui(size: 10, weight: .semibold)).foregroundStyle(fg)
            .padding(.horizontal, 6).padding(.vertical, 1).background(Capsule().fill(bg))
    }

    func bar(_ label: String, _ pct: Double, _ line: Int) -> some View {
        let level = usageLevel(pct, line: line)
        return HStack(spacing: 7) {
            Text(label).font(.ui(size: 11)).foregroundStyle(Theme.muted).frame(width: 18, alignment: .leading)
            LevelBar(pct: pct, line: line, color: levelColor(level), width: 120, height: 7)
                .help("\(label): \(Int(pct.rounded()))% · \(level.words)\(line > 0 ? " · failover at \(line)%" : "")")
            Text("\(Int(pct.rounded()))%").font(.ui(size: 11)).monospacedDigit()
                .foregroundStyle(level == .room ? Theme.text2 : levelColor(level)).frame(width: 34, alignment: .trailing)
        }
    }

    @ViewBuilder func pins(_ list: AgentAccountList) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                VStack(alignment: .leading, spacing: 1) {
                    Text("Pinned folders").font(.ui(size: 13)).foregroundStyle(Theme.text)
                    Text("Agents started in a pinned folder use its account; the longest pin wins.").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                Spacer()
                Button("Pin a Folder…") { pinning = true }.controlSize(.small)
            }
            ForEach(list.pins ?? []) { p in
                HStack(spacing: 8) {
                    Image(systemName: "folder").font(.system(size: 11)).foregroundStyle(Theme.muted)
                    Text(p.path).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                        .lineLimit(1).truncationMode(.middle).help(p.path)
                    Text("→ \(p.account)").font(.ui(size: 12, weight: .medium)).foregroundStyle(Theme.text)
                    Spacer()
                    Button { store.unpinAccount(path: p.path, on: machine) } label: {
                        Image(systemName: "xmark").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.muted)
                    }
                    .buttonStyle(.plain).help("Unpin").accessibilityLabel("Unpin \(p.path)")
                }
                .padding(.horizontal, 8).padding(.vertical, 5)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.raise2))
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }

    /// Every agent's failover line: on or off, and where, 50% to 100%.
    func failover(_ list: AgentAccountList) -> some View {
        let at = list.failoverAt ?? 0
        let v = dragging ?? Double(at > 0 ? at : 80)
        return HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 1) {
                Text(store.failoverPerAgent(on: machine) ? "Failover, every agent" : "Failover").font(.ui(size: 13)).foregroundStyle(Theme.text)
                Text("A new agent whose account is past the line starts on that agent's next account. Running agents never move.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            Toggle("On", isOn: Binding(get: { at > 0 }, set: { store.setAccountFailover($0 ? Int(v) : 0, on: machine) }))
                .toggleStyle(.switch).controlSize(.small).labelsHidden()
            Slider(value: Binding(get: { v }, set: { dragging = $0 }), in: 50...100, step: 1) { editing in
                if !editing, let d = dragging {
                    store.setAccountFailover(Int(d), on: machine)
                    dragging = nil
                }
            }
            .frame(width: 140).controlSize(.small).disabled(at == 0)
            Text(at > 0 || dragging != nil ? "\(Int(v))%" : "Off").font(.ui(size: 12, weight: .semibold)).monospacedDigit()
                .frame(width: 38, alignment: .trailing).foregroundStyle(at > 0 ? Theme.text : Theme.muted)
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }
}

/// Adds an account: which agent, its name, and a folder of the person's
/// own if they have one; then, ticked, starts the agent there to sign in.
struct AddAccountSheet: View {
    let list: AgentAccountList
    let machineLabel: String
    let add: (String, String, String?, Bool) -> Void
    /// Read so the sheet is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @Environment(\.dismiss) private var dismiss
    @State private var agent = ""
    @State private var name = ""
    @State private var dir = ""
    @State private var signIn = true

    var agents: [String] { list.agents ?? Array(Set((list.accounts ?? []).map(\.agent))).sorted() }

    var body: some View {
        let _ = look
        let problem = agentAccountNameProblem(name, agent: agent, existing: list.accounts ?? [])
        VStack(alignment: .leading, spacing: 14) {
            Text("Add an Agent Account").font(.ui(size: 15, weight: .semibold))
            Text("A second subscription of an agent on \(machineLabel), with its own sign-in, settings and conversations.")
                .font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            Form {
                Picker("Agent", selection: $agent) {
                    ForEach(agents, id: \.self) { Text($0).tag($0) }
                }
                TextField("Name", text: $name, prompt: Text("work"))
                TextField("Folder", text: $dir, prompt: Text("optional — conch makes one"))
                Toggle("Start it now to sign in", isOn: $signIn)
            }
            if !name.isEmpty, let problem {
                Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.warn)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Add") {
                    add(agent, name.trimmingCharacters(in: .whitespaces), dir.isEmpty ? nil : dir, signIn)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction).disabled(agent.isEmpty || problem != nil)
            }
        }
        .padding(20).frame(width: 440)
        .onAppear { if agent.isEmpty { agent = agents.first ?? "" } }
    }
}

/// Pins a folder to an account by name: on this Mac chosen in a panel,
/// on another machine typed, since the panel only sees this one.
struct PinAccountSheet: View {
    let list: AgentAccountList
    let local: Bool
    let pin: (String, String) -> Void
    /// Read so the sheet is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @Environment(\.dismiss) private var dismiss
    @State private var path = ""
    @State private var name = ""

    /// A pin names an account by name, any agent's: each name once.
    var names: [String] {
        var out: [String] = []
        for a in list.accounts ?? [] where a.isDefault != true && !out.contains(a.name) { out.append(a.name) }
        return out + ["default"]
    }

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 14) {
            Text("Pin a Folder to an Account").font(.ui(size: 15, weight: .semibold))
            Text("Agents started in it — a project, a worktree or a task folder — use that account, each agent its own of that name.")
                .font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            HStack {
                TextField("Folder", text: $path, prompt: Text(local ? "~/src/acme" : "a path on that machine"))
                if local {
                    Button("Choose…") {
                        let p = NSOpenPanel()
                        p.canChooseFiles = false
                        p.canChooseDirectories = true
                        if p.runModal() == .OK, let u = p.url { path = u.path }
                    }
                }
            }
            Picker("Account", selection: $name) {
                ForEach(names, id: \.self) { Text($0).tag($0) }
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Pin") {
                    // ~ is this Mac's home only on this Mac.
                    pin(local ? (path as NSString).expandingTildeInPath : path, name)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction).disabled(path.trimmingCharacters(in: .whitespaces).isEmpty || name.isEmpty)
            }
        }
        .padding(20).frame(width: 460)
        .onAppear { if name.isEmpty { name = names.first ?? "" } }
    }
}
