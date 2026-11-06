import ConchKit
import SwiftUI

// The library: MCP servers and skills kept in conch and given to the agents
// chosen for each — the TUI's Settings → Agents → Shared MCP servers &
// skills, per machine. Editing it (a tick, a form, an import) writes to no
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
}

struct SkillForm: Identifiable {
    var id = UUID()
    var old: String?
    var name = ""
    var path = ""
    var agents: Set<String> = []
}

struct LibrarySheet: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    @StateObject private var model: LibraryModel

    init(store: Store, machine: String) {
        self.store = store
        _model = StateObject(wrappedValue: LibraryModel(store: store, machine: machine))
    }

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text("MCP servers & skills").font(.ui(size: 15, weight: .semibold))
                Spacer()
                if store.machines.count > 1 {
                    Picker("", selection: $model.machine) {
                        ForEach(store.machines, id: \.id) { m in Text(m.label).tag(m.id) }
                    }
                    .labelsHidden().frame(width: 160)
                }
            }
            Text("Kept in conch and given to the agents you tick, in your home on \(store.label(model.machine)) — the setup that follows you from project to project. Ticking writes to no agent; Apply… shows every write first.")
                .font(.ui(size: 12)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if !model.error.isEmpty {
                        Text(model.error).font(.ui(size: 12)).foregroundStyle(Theme.warn)
                    } else if let r = model.result {
                        content(r)
                    } else {
                        ProgressView().controlSize(.small)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(height: 420)
            footer
        }
        .padding(18).frame(width: 680)
        .onAppear { model.load() }
        .onChange(of: model.machine) { _, _ in model.load() }
        .sheet(item: $model.serverForm) { f in
            ServerFormView(form: f, agents: model.result?.agents ?? [], model: model)
        }
        .sheet(item: $model.skillForm) { f in
            SkillFormView(form: f, agents: model.result?.agents ?? [], local: model.machine == "local", model: model)
        }
    }

    @ViewBuilder func content(_ r: AgentLibraryResult) -> some View {
        section("MCP servers", r)
        ForEach(r.library.servers.sorted { $0.name < $1.name }, id: \.name) { s in
            row(r, LibraryKind.server, s.name, s.what, edit: {
                model.serverForm = ServerForm(old: s.name, name: s.name, command: s.commandLine, env: joinPairs(s.env),
                                              headers: joinPairs(s.headers), sse: s.transport == "sse", agents: Set(s.agents))
            })
        }
        HStack(spacing: 8) {
            Button("Add a server…") { model.serverForm = ServerForm(agents: Set(r.agents)) }
            if !r.library.hasConchTools {
                Button("Add conch's own tools") {
                    model.save(r.library.withConchTools(agents: r.agents), flash: "conch's tools added")
                }
                .help("conch mcp: start, prompt, wait on and read other agents, as tools")
            }
        }
        .controlSize(.small).disabled(model.busy)

        section("Skills", r)
        ForEach(r.library.skills.sorted { $0.name < $1.name }, id: \.name) { s in
            row(r, LibraryKind.skill, s.name, s.path, edit: {
                model.skillForm = SkillForm(old: s.name, name: s.name, path: s.path, agents: Set(s.agents))
            })
        }
        HStack(spacing: 8) {
            Button("Add a skill…") { model.skillForm = SkillForm(agents: Set(r.agents)) }
            Menu("Take an agent's in") {
                ForEach(r.agents, id: \.self) { a in Button(model.agentName(a)) { model.importFrom(a) } }
            }
            .fixedSize()
            .help("Its MCP servers and skills in your home go in, ticked for it. Nothing is written to any agent.")
        }
        .controlSize(.small).disabled(model.busy)

        // conch's own skills are installed their own way; say where.
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text("conch's skills").font(.ui(size: 12.5, weight: .medium))
                Text("conch, conch-team, conch-review, conch-ship and conch-factory: run other agents, a review loop, a ticket to a merged pull request.")
                    .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            Button("Open…") {
                store.libraryOpen = false
                DispatchQueue.main.async { store.skillOpen = true }
            }
            .controlSize(.small)
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))

        Text("✓ has it   + Apply writes it   − Apply takes it out   · off   ○ set by hand   ! differs, left alone   ✗ cannot")
            .font(.ui(size: 11)).foregroundStyle(.secondary)
        Text("Secrets stay in your environment: write ${TOKEN}, not the token.")
            .font(.ui(size: 11)).foregroundStyle(.secondary)
    }

    func section(_ title: String, _ r: AgentLibraryResult) -> some View {
        HStack(spacing: 0) {
            Text(title).font(.ui(size: 13, weight: .semibold))
            Spacer()
            ForEach(r.agents, id: \.self) { a in
                Text(libraryAgentShort(a)).font(.ui(size: 11, weight: .medium)).foregroundStyle(.secondary)
                    .frame(width: 30).help(model.agentName(a))
            }
            Color.clear.frame(width: 52)
        }
        .padding(.top, 4)
    }

    @ViewBuilder func row(_ r: AgentLibraryResult, _ kind: String, _ name: String, _ what: String, edit: @escaping () -> Void) -> some View {
        let cells = r.agents.map { r.cell(kind, name, $0) }
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 0) {
                Text(name).font(.ui(size: 12.5, weight: .medium))
                Text("  " + what).font(.ui(size: 11.5)).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                Spacer(minLength: 8)
                ForEach(cells, id: \.agent) { c in
                    Button(libraryMark(c.state)) { model.toggle(kind, name, c.agent) }
                        .buttonStyle(.plain)
                        .font(.ui(size: 13, weight: .semibold, design: .monospaced))
                        .foregroundStyle(color(c.state))
                        .frame(width: 30)
                        .help("\(model.agentName(c.agent)): \(libraryMeaning(c)) — click to turn \(model.isOn(r.library, kind, name, c.agent) ? "off" : "on")")
                        .disabled(model.busy)
                }
                Button { edit() } label: { Image(systemName: "pencil") }.buttonStyle(.plain).frame(width: 24).help("Edit")
                Button { model.remove(kind, name) } label: { Image(systemName: "trash") }.buttonStyle(.plain).frame(width: 24).help("Take it out of the library")
            }
            ForEach(cells.filter { ($0.state == "differs" || $0.state == "cannot") && !($0.detail ?? "").isEmpty }, id: \.agent) { c in
                Text("\(libraryAgentShort(c.agent)) \(c.detail ?? "")").font(.ui(size: 11)).foregroundStyle(Theme.warn).padding(.leading, 14)
            }
        }
        .padding(.vertical, 3)
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

    var footer: some View {
        HStack {
            let pending = model.result?.pending ?? 0
            Text(pending > 0 ? "\(pending) change\(pending == 1 ? "" : "s") to write" : "conch agent library, on each machine")
                .font(.ui(size: 11.5)).foregroundStyle(pending > 0 ? Theme.accent : .secondary)
            Spacer()
            Button("Put the last one back…") { model.undo() }
                .disabled(model.busy || (model.result?.undos ?? []).isEmpty)
            Button("Apply…") { model.apply() }.disabled(model.busy || model.result == nil)
            Button("Done") { store.libraryOpen = false }.keyboardShortcut(.defaultAction)
        }
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
                Toggle("The URL speaks SSE rather than streamable HTTP", isOn: $form.sse)
            }
            Text("Give it to").font(.ui(size: 12, weight: .medium))
            AgentTicks(agents: agents, on: $form.agents, name: model.agentName)
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
            let lib = try r.library.addingServer(name: form.name, commandOrURL: form.command, env: form.env, headers: form.headers,
                                                 sse: form.sse, agents: r.agents.filter(form.agents.contains), replacing: form.old)
            model.save(lib, flash: "Saved \(form.name.trimmingCharacters(in: .whitespaces))")
            dismiss()
        } catch {
            problem = "\(error)"
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
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 10) {
            Text(form.old == nil ? "Add a skill" : form.old!).font(.ui(size: 14, weight: .semibold))
            Text("A folder holding a SKILL.md, wherever you keep it. Each agent gets a link to it, not a copy, so editing the folder changes it for all of them.")
                .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            Form {
                HStack {
                    TextField("Folder", text: $form.path, prompt: Text("~/skills/review"))
                    if local { Button("Choose…") { choose() } }
                }
                TextField("Name", text: $form.name, prompt: Text("the folder's name"))
            }
            Text("Give it to").font(.ui(size: 12, weight: .medium))
            AgentTicks(agents: agents, on: $form.agents, name: model.agentName)
            if !problem.isEmpty { Text(problem).font(.ui(size: 11.5)).foregroundStyle(Theme.err) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save") { save() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 520)
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
        do {
            let lib = try r.library.addingSkill(name: form.name, path: form.path, agents: r.agents.filter(form.agents.contains), replacing: form.old)
            model.save(lib, flash: "Saved \(lib.skills.last?.name ?? form.name)")
            dismiss()
        } catch {
            problem = "\(error)"
        }
    }
}
