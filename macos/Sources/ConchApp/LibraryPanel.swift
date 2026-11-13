import ConchKit
import SwiftUI

// The library: MCP servers and skills kept in conch and given to the agents
// chosen for each — the TUI's Settings → Agents → Shared MCP servers &
// skills, per machine; in the app, Settings → Skills & MCP servers. Editing it (a tick, a form, an import) writes to no
// agent; Apply asks for the plan, names every write, and only then makes
// them. Put the last one back undoes the last apply, file by file.

@MainActor
final class LibraryModel: ObservableObject {
    unowned let store: Store
    @Published var machine: String
    @Published var result: AgentLibraryResult?
    @Published var error = ""
    @Published var busy = false
    @Published var serverForm: ServerForm?
    @Published var skillForm: SkillForm?

    init(store: Store, machine: String) {
        self.store = store
        self.machine = machine
    }

    private var conn: Connection? {
        guard let c = store.connection(machine) else {
            error = "\(store.label(machine)) is not connected"
            return nil
        }
        guard c.hello?.capabilities?.contains("agent.library.v1") == true else {
            error = "the server on \(store.label(machine)) predates the library; reload or upgrade it"
            return nil
        }
        return c
    }

    struct Params: Encodable {
        var set: Library?
        var `import`: String?
    }

    /// Whether a token can be kept in the Keychain for this machine's
    /// agents: this Mac's server, which reads it from there for each pane.
    var canKeepTokens: Bool {
        machine == "local" && store.connection(machine)?.hello?.capabilities?.contains("agent.library.keychain.v1") == true
    }

    /// Whether this machine's server takes a token written out in plain
    /// text, for somebody who chose that over the Keychain — on any machine.
    var canWritePlain: Bool { store.connection(machine)?.hello?.capabilities?.contains("agent.library.plain.v1") == true }

    /// Whether the form takes a token at all.
    var takesTokens: Bool { canKeepTokens || canWritePlain }

    /// Whether this machine's server can fetch a skill from GitHub.
    var canFetch: Bool { store.connection(machine)?.hello?.capabilities?.contains("agent.library.fetch.v1") == true }

    /// A skill on GitHub downloaded into this machine's library, by its
    /// server: that is where its agents read it.
    func fetch(source: String, name: String) async throws -> LibraryFetchResult {
        guard let c = conn else { throw LibraryFormError(error) }
        struct P: Encodable { var source: String; var name: String? }
        let n = name.trimmingCharacters(in: .whitespaces)
        return try await c.call("agent.library.fetch", P(source: source.trimmingCharacters(in: .whitespaces), name: n.isEmpty ? nil : n), as: LibraryFetchResult.self)
    }

    func load() {
        result = nil
        error = ""
        call(Params(), flash: nil)
    }

    /// Saves lib as the library (checked by the server, which says what is
    /// wrong with it), or imports an agent's — writing to no agent either way.
    func save(_ lib: Library, flash: String) { call(Params(set: lib), flash: flash) }
    func importFrom(_ agent: String) { call(Params(import: agent), flash: "Took \(agentName(agent))'s servers and skills into the library") }

    private func call(_ p: Params, flash: String?) {
        guard let c = conn else { return }
        busy = true
        let m = machine
        Task {
            defer { busy = false }
            do {
                let r = try await c.call("agent.library", p, as: AgentLibraryResult.self)
                guard m == machine else { return } // the picker moved on
                result = r
                error = ""
                if let f = flash {
                    var text = f
                    if let s = r.skipped, !s.isEmpty { text += " · left out: " + s.joined(separator: "; ") }
                    store.say(text + (r.pending > 0 ? " · Apply… writes it" : ""))
                }
            } catch {
                if result == nil { self.error = "\(error)" } else { store.error = "\(error)" }
            }
        }
    }

    func toggle(_ kind: String, _ name: String, _ agent: String) {
        guard let r = result else { return }
        let on = r.library.toggling(kind, name, agent, order: r.agents)
        save(on, flash: "\(name) \(isOn(r.library, kind, name, agent) ? "off" : "on") for \(agentName(agent))")
    }

    func remove(_ kind: String, _ name: String) {
        guard let r = result else { return }
        let a = NSAlert()
        a.messageText = "Take \(name) out of the library?"
        a.informativeText = "Nothing changes in the agents until you Apply, which takes conch's copies out. One you set up by hand is left alone."
        a.addButton(withTitle: "Take it out")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        // Its token goes from the Keychain with it.
        if kind == LibraryKind.server, canKeepTokens, r.library.servers.first(where: { $0.name == name })?.usesKeptToken == true {
            try? Keychain.forget(tokenVariable(for: name))
        }
        save(r.library.removing(kind, name), flash: "Took \(name) out of the library")
    }

    /// The plan, a question naming every write, then the writes.
    func apply() {
        guard let c = conn else { return }
        struct P: Encodable { var apply: Bool? }
        busy = true
        let label = store.label(machine)
        Task {
            defer { busy = false }
            do {
                let plan = try await c.call("agent.library.apply", P(), as: AgentSyncResult.self)
                let writes = plan.writes
                if writes.isEmpty {
                    store.say("Every agent on \(label) already has what the library says", .info)
                    return
                }
                let a = NSAlert()
                a.messageText = "Give each agent on \(label) what the library says?"
                let lines = writes.prefix(24).map(syncLine)
                a.informativeText = lines.joined(separator: "\n")
                    + (writes.count > 24 ? "\n…and \(writes.count - 24) more" : "")
                    + "\n\nA server or link somebody made by hand is left alone, and only what conch wrote is ever taken out. Put the last one back… undoes it."
                a.addButton(withTitle: "Apply")
                a.addButton(withTitle: "Cancel")
                guard a.runModal() == .alertFirstButtonReturn else { return }
                let done = try await c.call("agent.library.apply", P(apply: true), as: AgentSyncResult.self)
                if let f = done.failed.first {
                    store.error = "\(f.kind) \(f.name): \(f.error ?? "")"
                } else {
                    store.say("Gave each agent what the library says · \(done.writes.count) change\(done.writes.count == 1 ? "" : "s")")
                }
                load()
            } catch {
                store.error = "\(error)"
            }
        }
    }

    func undo() {
        guard let c = conn else { return }
        let a = NSAlert()
        a.messageText = "Put the library's last apply back on \(store.label(machine))?"
        a.informativeText = "What it wrote is taken out and what it replaced is put back, file by file."
        a.addButton(withTitle: "Put it back")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        struct P: Encodable { var undo = true }
        busy = true
        Task {
            defer { busy = false }
            do {
                let done = try await c.call("agent.library.apply", P(), as: AgentSyncResult.self)
                if let f = done.failed.first {
                    store.error = "\(f.kind) \(f.name): \(f.error ?? "")"
                } else {
                    store.say("Put the library's last apply back")
                }
                load()
            } catch {
                store.error = "\(error)"
            }
        }
    }

    func isOn(_ lib: Library, _ kind: String, _ name: String, _ agent: String) -> Bool {
        kind == LibraryKind.server
            ? lib.servers.first { $0.name == name }?.agents.contains(agent) == true
            : lib.skills.first { $0.name == name }?.agents.contains(agent) == true
    }

    func agentName(_ a: String) -> String {
        ["claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode", "devin": "Devin"][a] ?? a
    }
}

/// The server form's fields; old is the name being edited, nil when new.
struct ServerForm: Identifiable {
    var id = UUID()
    var old: String?
    var name = ""
    var command = ""
    var env = ""
    var headers = ""
    var sse = false
    var agents: Set<String> = []
    /// A token to keep in this Mac's Keychain; never shown again once kept.
    var token = ""
    /// The server it edits already has one kept.
    var tokenKept = false
}

struct SkillForm: Identifiable {
    var id = UUID()
    var old: String?
    var name = ""
    var path = ""
    var agents: Set<String> = []
    /// Where on GitHub it comes from: what the GitHub tab fetches, and what
    /// an edit of a fetched skill keeps.
    var source = ""
    var fromGitHub = false
}

/// The library in Settings → Skills & MCP servers, for the machine picked
/// there (store.skillsMachine).
struct LibraryPanel: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    @StateObject private var model: LibraryModel

    init(store: Store) {
        self.store = store
        _model = StateObject(wrappedValue: LibraryModel(store: store, machine: store.skillsMachine))
    }

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 12) {
            if !model.error.isEmpty {
                Text(model.error).font(.ui(size: 12)).foregroundStyle(Theme.warn)
            } else if let r = model.result {
                content(r)
                actions
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .onAppear { model.load() }
        .onChange(of: store.skillsMachine) { _, m in model.machine = m; model.load() }
        .sheet(item: $model.serverForm) { f in
            ServerFormView(form: f, agents: model.result?.agents ?? [], model: model)
        }
        .sheet(item: $model.skillForm) { f in
            SkillFormView(form: f, agents: model.result?.agents ?? [], local: model.machine == "local", model: model)
        }
    }

    @ViewBuilder func content(_ r: AgentLibraryResult) -> some View {
        SettingsCard(title: "Your skills", trailing: AnyView(heads(r))) {
            let skills = r.library.skills.sorted { $0.name < $1.name }
            ForEach(Array(skills.enumerated()), id: \.element.name) { i, s in
                row(r, LibraryKind.skill, s.name, s.path, icon: "book", first: i == 0, edit: {
                    model.skillForm = SkillForm(old: s.name, name: s.name, path: s.path, agents: Set(s.agents), source: s.source ?? "", fromGitHub: s.source != nil)
                })
            }
            if skills.isEmpty { empty("No skills of your own yet: a folder holding a SKILL.md, given to the agents you tick.") }
            HStack(spacing: 8) {
                pill("Add a skill…", "plus") { model.skillForm = SkillForm(agents: Set(r.agents)) }
                Menu {
                    ForEach(r.agents, id: \.self) { a in Button(model.agentName(a)) { model.importFrom(a) } }
                } label: { Text("Take an agent's in").font(.ui(size: 12)) }
                    .menuStyle(.borderlessButton).fixedSize()
                    .help("Its MCP servers and skills in your home go in, ticked for it. Nothing is written to any agent.")
            }
            .padding(12).frame(maxWidth: .infinity, alignment: .leading).disabled(model.busy)
            .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        }
        SettingsCard(title: "MCP servers", trailing: AnyView(heads(r))) {
            let servers = r.library.servers.sorted { $0.name < $1.name }
            ForEach(Array(servers.enumerated()), id: \.element.name) { i, s in
                row(r, LibraryKind.server, s.name, s.what, icon: "server", first: i == 0, edit: {
                    // A plain token is taken back out of its header into
                    // the Token field, so it is never shown in the clear.
                    let p = s.plain == true ? formOfPlain(s) : nil
                    model.serverForm = ServerForm(old: s.name, name: s.name, command: s.commandLine, env: joinPairs((p?.server ?? s).env),
                                                  headers: joinPairs((p?.server ?? s).headers), sse: s.transport == "sse", agents: Set(s.agents),
                                                  token: p?.token ?? "", tokenKept: s.usesKeptToken)
                })
            }
            if servers.isEmpty { empty("No MCP servers yet: a command or a URL, with secrets as ${VAR}.") }
            HStack(spacing: 8) {
                pill("Add a server…", "plus") { model.serverForm = ServerForm(agents: Set(r.agents)) }
                if !r.library.hasConchTools {
                    pill("Add conch's own tools", "command") { model.save(r.library.withConchTools(agents: r.agents), flash: "conch's tools added") }
                        .help("conch mcp: start, prompt, wait on and read other agents, as tools")
                }
            }
            .padding(12).frame(maxWidth: .infinity, alignment: .leading).disabled(model.busy)
            .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        }
        HStack(spacing: 14) {
            ForEach([("on", "has it"), ("pending", "Apply gives it"), ("remove", "Apply takes it out"), ("theirs", "set by hand"), ("differs", "differs, left alone")], id: \.0) { st, t in
                HStack(spacing: 5) { tick(st, size: 14); Text(t) }
            }
        }
        .font(.ui(size: 11)).foregroundStyle(Theme.muted)
    }

    func heads(_ r: AgentLibraryResult) -> some View {
        HStack(spacing: 0) {
            ForEach(r.agents, id: \.self) { a in
                Text(libraryAgentShort(a)).font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    .frame(width: 34).help(model.agentName(a))
            }
            Color.clear.frame(width: 52)
        }
    }

    func empty(_ t: String) -> some View {
        Text(t).font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(.horizontal, 16).padding(.vertical, 12)
    }

    func pill(_ t: String, _ sym: String, _ act: @escaping () -> Void) -> some View {
        Button(action: act) {
            HStack(spacing: 6) { Image(systemName: sym).font(.system(size: 10, weight: .bold)); Text(t).font(.ui(size: 12, weight: .medium)) }
                .foregroundStyle(Theme.text)
                .padding(.horizontal, 10).frame(height: 26)
                .background(RoundedRectangle(cornerRadius: 7).fill(Theme.raise2))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    /// A cell: a box coloured by where the item stands with the agent.
    func tick(_ state: String, size: CGFloat = 22) -> some View {
        let c = color(state)
        let filled = state != "off"
        return Text(state == "off" ? "" : libraryMark(state))
            .font(.ui(size: size * 0.55, weight: .bold)).foregroundStyle(c)
            .frame(width: size, height: size)
            .background(RoundedRectangle(cornerRadius: size * 0.27).fill(filled ? c.opacity(0.15) : Theme.well))
            .overlay(RoundedRectangle(cornerRadius: size * 0.27).stroke(filled ? c.opacity(0.55) : Theme.line2))
    }

    @ViewBuilder func row(_ r: AgentLibraryResult, _ kind: String, _ name: String, _ what: String, icon: String, first: Bool, edit: @escaping () -> Void) -> some View {
        let cells = r.agents.map { r.cell(kind, name, $0) }
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 12) {
                LucideIcon(icon == "book" ? SettingsNav.skillsIcon : "server", color: Theme.text2, size: 14)
                    .frame(width: 30, height: 30).background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise2))
                VStack(alignment: .leading, spacing: 1) {
                    Text(name).font(.ui(size: 13, weight: .medium))
                    Text(what).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                }
                Spacer(minLength: 8)
                ForEach(cells, id: \.agent) { c in
                    Button { model.toggle(kind, name, c.agent) } label: { tick(c.state) }
                        .buttonStyle(.plain).frame(width: 34)
                        .help("\(model.agentName(c.agent)): \(libraryMeaning(c)) — click to turn \(model.isOn(r.library, kind, name, c.agent) ? "off" : "on")")
                        .disabled(model.busy)
                }
                Button { edit() } label: { Image(systemName: "pencil").font(.system(size: 11)) }.buttonStyle(.plain).foregroundStyle(Theme.text2).frame(width: 24).help("Edit")
                Button { model.remove(kind, name) } label: { Image(systemName: "trash").font(.system(size: 11)) }.buttonStyle(.plain).foregroundStyle(Theme.text2).frame(width: 24).help("Take it out of the library")
            }
            ForEach(cells.filter { ($0.state == "differs" || $0.state == "cannot") && !($0.detail ?? "").isEmpty }, id: \.agent) { c in
                Text("\(model.agentName(c.agent)): \(c.detail ?? "")").font(.ui(size: 11)).foregroundStyle(Theme.warn).padding(.leading, 42)
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .overlay(alignment: .top) { if !first { Rectangle().fill(Theme.line).frame(height: 1) } }
    }

    func color(_ state: String) -> Color {
        switch state {
        case "on": return Theme.ok
        case "pending": return Theme.accent
        case "remove", "differs": return Theme.warn
        case "cannot": return Theme.err
        default: return Theme.muted
        }
    }

    var actions: some View {
        let pending = model.result?.pending ?? 0
        return HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(pending > 0 ? "\(pending) change\(pending == 1 ? "" : "s") to write" : "Every agent has what the library says")
                    .font(.ui(size: 13, weight: .medium)).foregroundStyle(pending > 0 ? Theme.accent : Theme.text)
                Text("Ticking writes to no agent. Apply shows every write first.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
            Spacer()
            Button("Put the last one back…") { model.undo() }.buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.text2)
                .disabled(model.busy || (model.result?.undos ?? []).isEmpty)
            Button { model.apply() } label: {
                HStack(spacing: 6) {
                    if model.busy { ProgressView().controlSize(.mini) }
                    Text("Apply…").font(.ui(size: 12.5, weight: .semibold))
                }
                .foregroundStyle(pending > 0 ? Theme.selectedText : Theme.text)
                .padding(.horizontal, 14).frame(height: 30)
                .background(RoundedRectangle(cornerRadius: 8).fill(pending > 0 ? Theme.accent : Theme.raise2))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(pending > 0 ? Theme.accent : Theme.line2))
            }
            .buttonStyle(.plain)
            .disabled(model.busy || model.result == nil)
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
    }
}

/// Ticks for the agents a server or skill is given to.
struct AgentTicks: View {
    let agents: [String]
    @Binding var on: Set<String>
    let name: (String) -> String

    var body: some View {
        HStack(spacing: 12) {
            ForEach(agents, id: \.self) { a in
                Toggle(name(a), isOn: Binding(get: { on.contains(a) }, set: { if $0 { on.insert(a) } else { on.remove(a) } }))
                    .toggleStyle(.checkbox)
            }
        }
    }
}

struct ServerFormView: View {
    @Environment(\.conchTheme) private var look
    @State var form: ServerForm
    let agents: [String]
    @ObservedObject var model: LibraryModel
    @State private var problem = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 10) {
            Text(form.old == nil ? "Add a server" : form.old!).font(.ui(size: 14, weight: .semibold))
            Text("A command to run, or the URL of a server. Refer to secrets as ${VAR}: the value stays in your environment, and each agent is given its own way of naming it.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            Form {
                TextField("Name", text: $form.name, prompt: Text("github"))
                TextField("Command or URL", text: $form.command, prompt: Text("npx -y @modelcontextprotocol/server-github, or https://…"))
                TextField("Environment", text: $form.env, prompt: Text("GITHUB_TOKEN=${GITHUB_TOKEN}; …"))
                TextField("Headers", text: $form.headers, prompt: Text("Authorization=Bearer ${TOKEN}; … (a URL's)"))
                if model.takesTokens {
                    SecureField("Token", text: $form.token,
                                prompt: Text(form.tokenKept ? "Kept in the Keychain · type a new one to replace it" : "Pasted here · on Save you choose how it is kept"))
                }
                Toggle("The URL speaks SSE rather than streamable HTTP", isOn: $form.sse)
            }
            Text("Give it to").font(.ui(size: 12, weight: .medium))
            AgentTicks(agents: agents, on: $form.agents, name: model.agentName)
            if model.takesTokens {
                Text(model.canKeepTokens
                     ? "On Save you choose: this Mac's Keychain as \(tokenVariable(for: form.name.isEmpty ? "name" : form.name)) — agents conch starts are given it and their files only name it — or plain text in each agent's file. One pasted into a header is taken out of it either way."
                     : "A token here is written in plain text in each agent's file on \(model.store.label(model.machine)): the Keychain is this Mac's alone.")
                    .font(.ui(size: 11)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } else if model.machine != "local" {
                Text("On \(model.store.label(model.machine)), set the variables in the environment its conch server starts with.")
                    .font(.ui(size: 11)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            if !problem.isEmpty { Text(problem).font(.ui(size: 11.5)).foregroundStyle(Theme.err) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save") { save() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 560)
    }

    func save() {
        guard let r = model.result else { return }
        do {
            var lib = try r.library.addingServer(name: form.name, commandOrURL: form.command, env: form.env, headers: form.headers,
                                                 sse: form.sse, agents: r.agents.filter(form.agents.contains), replacing: form.old)
            var flash = "Saved \(form.name.trimmingCharacters(in: .whitespaces))"
            var stepsFor: String?
            if model.takesTokens, let i = lib.servers.lastIndex(where: { $0.name == form.name.trimmingCharacters(in: .whitespaces) }) {
                let kept = try keepingToken(lib.servers[i], token: form.token)
                lib.servers[i] = kept.server
                if let t = kept.token {
                    switch askWhereToKeep(kept.variable) {
                    case .cancel:
                        return
                    case .keychain:
                        try Keychain.keep(t, as: kept.variable)
                        flash += " · its token is in the Keychain; agents started from now on have it"
                        stepsFor = kept.variable
                    case .plain:
                        lib.servers[i] = writingTokenPlain(kept)
                        flash += " · its token is in plain text in each agent's file"
                        // It was in the Keychain: it is not kept twice.
                        if form.tokenKept, model.canKeepTokens { try? Keychain.forget(kept.variable) }
                    }
                }
                // Renamed: the token goes with it.
                if model.canKeepTokens, let old = form.old, old != lib.servers[i].name, form.tokenKept {
                    if kept.token == nil, let t = Keychain.read(tokenVariable(for: old)) {
                        try Keychain.keep(t, as: kept.variable)
                        lib.servers[i] = try keepingToken(lib.servers[i], token: t).server
                    }
                    try? Keychain.forget(tokenVariable(for: old))
                }
            }
            model.save(lib, flash: flash)
            if let v = stepsFor { showKeychainSteps(v) }
            dismiss()
        } catch {
            problem = "\(error)"
        }
    }

    enum TokenHome { case keychain, plain, cancel }

    /// Where the person wants the token: the Keychain (this Mac only), or
    /// plain text in the agents' files — asked, never assumed.
    func askWhereToKeep(_ variable: String) -> TokenHome {
        let a = NSAlert()
        a.messageText = "Keep this token securely?"
        if model.canKeepTokens {
            a.informativeText = "Yes keeps it in this Mac's Keychain as \(variable): agents conch starts are given it, and their files only name it.\n\nNo writes it in plain text into each agent's config file, where anything running as you can read it — and Claude Code is handed it on its command line, so anybody else logged in to this Mac can see it in the process list for that moment."
            a.addButton(withTitle: "Yes, Use the Keychain")
            a.addButton(withTitle: "No, Save as Plain Text")
        } else {
            a.informativeText = "The Keychain is this Mac's, and \(model.store.label(model.machine)) cannot reach it. Saved, the token is written in plain text into each agent's config file there, where anything running as you can read it — and Claude Code is handed it on its command line, so anybody else logged in to that machine can see it in the process list for that moment. On a machine others use, set it as a variable in the environment its conch server starts with instead."
            a.addButton(withTitle: "Save as Plain Text")
        }
        a.addButton(withTitle: "Cancel")
        switch a.runModal() {
        case .alertFirstButtonReturn: return model.canKeepTokens ? .keychain : .plain
        case .alertSecondButtonReturn: return model.canKeepTokens ? .plain : .cancel
        default: return .cancel
        }
    }

    /// What was just done in the Keychain, and how to find it there.
    func showKeychainSteps(_ variable: String) {
        let a = NSAlert()
        a.messageText = "Token saved in your Keychain"
        a.informativeText = """
        1. It is in your login keychain as “conch: \(variable)”.
        2. Click Apply, so each agent's file names $\(variable).
        3. Agents conch starts from now on are given it. Restart any already running.
        4. To see, change or delete it: Keychain Access → login → search “conch”.
        """
        a.addButton(withTitle: "Done")
        a.addButton(withTitle: "Open Keychain Access")
        if a.runModal() == .alertSecondButtonReturn,
           let app = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.keychainaccess") {
            NSWorkspace.shared.openApplication(at: app, configuration: NSWorkspace.OpenConfiguration())
        }
    }
}

struct SkillFormView: View {
    @Environment(\.conchTheme) private var look
    @State var form: SkillForm
    let agents: [String]
    let local: Bool
    @ObservedObject var model: LibraryModel
    @State private var problem = ""
    @State private var found: [String] = []
    @State private var fetching = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 10) {
            Text(form.old == nil ? "Add a skill" : form.old!).font(.ui(size: 14, weight: .semibold))
            Picker("", selection: $form.fromGitHub) {
                Text("Folder").tag(false)
                Text("From GitHub").tag(true)
            }
            .pickerStyle(.segmented).labelsHidden().frame(width: 240)
            if form.fromGitHub {
                Text("A skill shared on GitHub: owner/repo, owner/repo/path/to/skill, @ref at the end, or the link to its folder. The server on \(model.store.label(model.machine)) downloads it into conch's library there — nothing in it is run — and each agent gets a link to it.")
                    .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                Form {
                    TextField("GitHub", text: $form.source, prompt: Text("anthropics/skills/skills/pdf"))
                    TextField("Name", text: $form.name, prompt: Text("its folder's name"))
                }
                if !found.isEmpty {
                    Text("That repository holds \(found.count) skills; pick one:").font(.ui(size: 12, weight: .medium))
                    ScrollView {
                        VStack(alignment: .leading, spacing: 4) {
                            ForEach(found, id: \.self) { f in
                                Button { form.source = f; found = []; save() } label: {
                                    Text(f).font(.ui(size: 12, design: .monospaced)).frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .buttonStyle(.link)
                            }
                        }
                    }
                    .frame(maxHeight: 140)
                }
                if !model.canFetch {
                    Text("The server on \(model.store.label(model.machine)) can't fetch from GitHub yet: reload or upgrade it.")
                        .font(.ui(size: 11.5)).foregroundStyle(Theme.warn)
                }
            } else {
                Text("A folder holding a SKILL.md, wherever you keep it. Each agent gets a link to it, not a copy, so editing the folder changes it for all of them.")
                    .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                Form {
                    HStack {
                        TextField("Folder", text: $form.path, prompt: Text("~/skills/review"))
                        if local { Button("Choose…") { choose() } }
                    }
                    TextField("Name", text: $form.name, prompt: Text("the folder's name"))
                }
            }
            Text("Give it to").font(.ui(size: 12, weight: .medium))
            AgentTicks(agents: agents, on: $form.agents, name: model.agentName)
            if !problem.isEmpty { Text(problem).font(.ui(size: 11.5)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(form.fromGitHub ? (fetching ? "Fetching…" : form.old == nil ? "Fetch and add" : "Fetch again") : "Save") { save() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(fetching || (form.fromGitHub && (!model.canFetch || form.source.trimmingCharacters(in: .whitespaces).isEmpty)))
            }
        }
        .padding(18).frame(width: 540)
    }

    func choose() {
        let p = NSOpenPanel()
        p.canChooseDirectories = true
        p.canChooseFiles = false
        p.prompt = "Choose"
        p.message = "A folder holding a SKILL.md"
        guard p.runModal() == .OK, let u = p.url else { return }
        if !FileManager.default.fileExists(atPath: u.appendingPathComponent("SKILL.md").path) {
            problem = "\(u.lastPathComponent) has no SKILL.md"
        } else {
            problem = ""
        }
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        form.path = u.path.hasPrefix(home + "/") ? "~" + u.path.dropFirst(home.count) : u.path
        if form.name.isEmpty { form.name = u.lastPathComponent }
    }

    func save() {
        guard let r = model.result else { return }
        let ticked = r.agents.filter(form.agents.contains)
        if form.fromGitHub {
            guard isGitHubSkill(form.source) else {
                problem = "That isn't a skill on GitHub: owner/repo, owner/repo/path/to/skill, @ref at the end, or a github.com link."
                return
            }
            fetching = true
            problem = ""
            Task {
                defer { fetching = false }
                do {
                    let f = try await model.fetch(source: form.source, name: form.name)
                    if let several = f.found, !several.isEmpty {
                        found = several
                        return
                    }
                    guard let name = f.name, let path = f.path else { problem = "Nothing came back"; return }
                    let lib = try r.library.addingSkill(name: name, path: path, agents: ticked, replacing: form.old, source: f.source)
                    model.save(lib, flash: "Fetched \(f.source ?? name) into the library")
                    dismiss()
                } catch {
                    problem = "\(error)"
                }
            }
            return
        }
        do {
            let lib = try r.library.addingSkill(name: form.name, path: form.path, agents: ticked, replacing: form.old)
            model.save(lib, flash: "Saved \(lib.skills.last?.name ?? form.name)")
            dismiss()
        } catch {
            problem = "\(error)"
        }
    }

}
