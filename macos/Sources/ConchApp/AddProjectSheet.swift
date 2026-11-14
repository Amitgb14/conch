import AppKit
import ConchKit
import SwiftUI

// Add project, three ways — the TUI's project browser and the CLI's
// `conch project add | clone | create`: a folder that is already there, a
// git repository cloned from a URL, or a new folder made a git repository.
// On this Mac a folder is chosen in a panel; on another machine its path is
// typed, since the panel can only see this one.
//
// Where it goes is chosen first: this Mac, another machine, or Conch
// Cloud — one of the account's cloud machines, made or woken and then
// connected to before the project is added there (Store.readyCloud). For
// Conch Cloud a fourth way, From this Mac, hands a project here to the
// Move sheet, which moves it there.

/// The machine an Add project sheet is for; cloud, a cloud machine's id
/// when it is one that is not connected (asleep), for the sheet to ready.
struct AddProjectTarget: Identifiable {
    var id: String // the machine
    var cloud: String?
}

struct AddProjectSheet: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme

    @State private var place: ProjectPlace
    @State private var mode: AddProjectMode = .clone
    @State private var folder = ""
    @State private var url = ""
    @State private var parent = ""
    @State private var name = ""
    @State private var nameEdited = false
    @State private var busy = false
    @State private var problem = ""
    // Conch Cloud: the machine ("new" for one made for it), a new one's
    // name and size, the project From this Mac moves, and the step under way.
    @State private var cloudChoice: String?
    @State private var cloudTouched = false
    @State private var newName = ""
    @State private var newNameEdited = false
    @State private var newSize = ""
    @State private var fromProject = ""
    @State private var prep: CloudPrep?

    init(store: Store, machine: String, cloud: String? = nil) {
        self.store = store
        _place = State(initialValue: cloud != nil ? .cloud : .machine(machine))
        _cloudChoice = State(initialValue: cloud)
        _cloudTouched = State(initialValue: cloud != nil)
    }

    var isCloud: Bool { place == .cloud }
    /// The machine it goes to, when that is not Conch Cloud.
    var machine: String? { if case .machine(let m) = place { return m } else { return nil } }
    var local: Bool { machine == "local" }
    var placeName: String { isCloud ? "Conch Cloud" : local ? "this Mac" : store.label(machine ?? "") }
    var signedIn: Bool { store.cloudSignedIn }

    var places: [PlaceOption] {
        let ms = store.machines.map { PlaceMachine(id: $0.id, label: $0.label, online: $0.online, cloud: $0.cloud) }
        return projectPlaces(machines: ms, signedIn: signedIn, cloudMachines: store.cloudMachines.filter(\.pickable).count,
                             offerCloud: store.offersCloud)
            .filter { $0.enabled || $0.place == place }
    }

    var modes: [AddProjectMode] { AddProjectMode.offered(cloud: isCloud) }

    /// A cloud machine runs this build's conch, which clones.
    var cloneSupported: Bool {
        guard let m = machine else { return true }
        return store.connection(m)?.hello?.capabilities?.contains("worktree.move.v1") == true
    }

    var target: String? {
        switch mode {
        case .folder: return folder.trimmingCharacters(in: .whitespaces).isEmpty ? nil : folder.trimmingCharacters(in: .whitespaces)
        case .clone, .create: return projectPath(parent: parent, name: name)
        case .fromMac: return nil
        }
    }

    var cloudPick: CloudPick? {
        guard let c = cloudChoice else { return nil }
        return c == "new" ? .new(name: newName, size: newSize) : .existing(id: c)
    }

    /// This Mac's git projects, which From this Mac can move.
    var localProjects: [ProjectInfo] { (store.snapshots["local"]?.projects ?? []).filter { $0.git != false } }

    var ready: Bool {
        guard !busy else { return false }
        if isCloud {
            guard signedIn, cloudPick != nil else { return false }
            if cloudChoice == "new" && !cloudNewReady(store, name: newName, size: newSize) { return false }
        }
        if mode == .fromMac { return !fromProject.isEmpty }
        guard target != nil else { return false }
        return mode != .clone || (looksCloneable(url) && cloneSupported)
    }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                LucideIcon(isCloud ? "cloud" : "folder-open", color: Theme.accent, size: 18)
                    .frame(width: 34, height: 34)
                    .background(RoundedRectangle(cornerRadius: 9).fill(Theme.accentDeep.opacity(0.3)))
                VStack(alignment: .leading, spacing: 1) {
                    Text("Add a project").font(.ui(size: 15, weight: .semibold))
                    Text(isCloud ? "On Conch Cloud" : local ? "On this Mac" : "On \(store.label(machine ?? ""))").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text("Where").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
                Picker("", selection: $place) {
                    ForEach(places) { p in
                        Text(p.place == .cloud ? "Conch Cloud ☁" : p.title).tag(p.place)
                    }
                }
                .pickerStyle(.radioGroup).horizontalRadioGroupLayout().labelsHidden()
                .disabled(busy)
            }
            if isCloud { cloudSection }

            if !isCloud || signedIn {
                Picker("", selection: $mode) {
                    ForEach(modes) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented).labelsHidden().disabled(busy)

                switch mode {
                case .folder: folderForm
                case .clone: cloneForm
                case .create: createForm
                case .fromMac: fromMacForm
                }
            }

            if !problem.isEmpty {
                HStack(alignment: .top, spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 11))
                    Text(problem).font(.ui(size: 12)).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                }
                .foregroundStyle(Theme.err)
            }
            HStack {
                if busy {
                    ProgressView().controlSize(.small)
                    Text(prep?.text ?? (mode == .clone ? "Cloning…" : mode == .create ? "Creating…" : "Adding…"))
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
                Spacer()
                Button("Cancel") { store.addProjectTarget = nil }.keyboardShortcut(.cancelAction).disabled(busy)
                Button(submitTitle) { submit() }
                    .keyboardShortcut(.defaultAction).disabled(!ready)
            }
        }
        .padding(20).frame(width: 560)
        .onAppear {
            if parent.isEmpty { parent = defaultParent }
            if !modes.contains(mode) { mode = modes.first ?? .clone }
            if isCloud { readyCloudChoices() }
        }
        .onChange(of: place) { _, _ in
            problem = ""
            parent = defaultParent
            if !modes.contains(mode) { mode = modes.first ?? .clone }
            if isCloud { readyCloudChoices() }
        }
        .onChange(of: store.cloudMachines) { _, _ in defaultCloudChoice() }
        .onChange(of: store.cloud) { _, _ in defaultCloudChoice() } // signed in here, or read again
        .onChange(of: url) { _, u in
            if !nameEdited { name = repoName(u) }
            suggestName()
        }
        .onChange(of: name) { _, _ in suggestName() }
        .onChange(of: fromProject) { _, _ in suggestName() }
        .onChange(of: cloudChoice) { _, _ in suggestName() }
    }

    var submitTitle: String {
        switch mode {
        case .clone: return "Clone & Add"
        case .create: return "Create & Add"
        case .folder: return "Add"
        case .fromMac: return "Continue"
        }
    }

    var defaultParent: String {
        local ? defaultProjectParent(home: NSHomeDirectory(), exists: { FileManager.default.fileExists(atPath: $0) }) : "~"
    }

    // MARK: Conch Cloud

    @ViewBuilder var cloudSection: some View {
        if !signedIn {
            CloudSignInPrompt(store: store)
        } else {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    Text("Machine").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
                    Picker("", selection: Binding(get: { cloudChoice }, set: { cloudChoice = $0; cloudTouched = true })) {
                        ForEach(store.cloudMachines.filter(\.pickable)) { m in
                            Text("\(m.name) — \(m.stateText)").tag(Optional(m.id))
                        }
                        if !store.cloudMachines.filter(\.pickable).isEmpty { Divider() }
                        Text("New machine…").tag(Optional("new"))
                    }
                    .labelsHidden().frame(maxWidth: 320, alignment: .leading).disabled(busy)
                    if let m = store.cloudMachines.first(where: { $0.id == cloudChoice }) {
                        Circle().fill(cloudToneColor(m.tone)).frame(width: 7, height: 7)
                        if m.asleep { Text("woken first").font(.ui(size: 11.5)).foregroundStyle(Theme.muted) }
                    }
                    Spacer()
                }
                if cloudChoice == "new" {
                    CloudNewMachineFields(store: store, name: Binding(get: { newName }, set: { newName = $0; newNameEdited = true }), size: $newSize)
                        .disabled(busy)
                }
            }
        }
    }

    /// The account's machines and sizes read, and one chosen.
    func readyCloudChoices() {
        store.refreshCloud()
        store.refreshCloudSizes()
        defaultCloudChoice()
    }

    /// One running, else one asleep, else a new one — until somebody picks.
    func defaultCloudChoice() {
        guard isCloud, !cloudTouched || cloudChoice == nil else { return }
        if let p = defaultCloudPick(store.cloudMachines), case .existing(let id) = p {
            cloudChoice = id
        } else {
            cloudChoice = "new"
        }
        suggestName()
    }

    /// A new machine is named after the project, until its name is typed.
    func suggestName() {
        guard !newNameEdited else { return }
        let base = mode == .fromMac ? (localProjects.first { $0.id == fromProject }?.name ?? "") : name
        newName = defaultCloudMachineName(for: base, taken: store.cloudMachines.map(\.name))
    }

    // MARK: forms

    var folderForm: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("A folder that is already there. A git repository gets branches, worktrees and tasks of its own.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            HStack {
                field("Folder", text: $folder, prompt: local ? "Choose a folder…" : "~/src/api", mono: true)
                if local { Button("Choose…") { choose { folder = $0 } } }
            }
        }
    }

    var cloneForm: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Clones the repository on \(placeName) and adds it. A private one uses that machine's git credentials — gh, an ssh key or a credential helper; git never stops to ask for a password here.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            field("Repository", text: $url, prompt: "https://github.com/owner/repo.git  or  git@github.com:owner/repo.git", mono: true)
            if !url.isEmpty && !looksCloneable(url) {
                Text("A git URL: https://…, ssh://…, git@host:owner/repo, or a path").font(.ui(size: 11)).foregroundStyle(Theme.warn)
            }
            whereFields
            if !cloneSupported {
                Text("The server on \(placeName) predates cloning a project; reload or upgrade it.").font(.ui(size: 11.5)).foregroundStyle(Theme.warn)
            }
        }
    }

    var createForm: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("A new, empty folder with `git init` run in it, ready for agents to work in.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            whereFields
        }
    }

    var fromMacForm: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Moves a project from this Mac there — its branches, worktrees, uncommitted work and agents — so they keep working with your Mac closed. You see what moves before anything does.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            if localProjects.isEmpty {
                Text("This Mac has no git project to move.").font(.ui(size: 12)).foregroundStyle(Theme.warn)
            } else {
                HStack(spacing: 8) {
                    Text("Project").font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
                    Picker("", selection: $fromProject) {
                        Text("Choose a project…").tag("")
                        ForEach(localProjects, id: \.id) { p in Text("\(p.name)  ·  \(tilde(p.path))").tag(p.id) }
                    }
                    .labelsHidden().frame(maxWidth: 380, alignment: .leading)
                    Spacer()
                }
            }
        }
    }

    var whereFields: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                field("In", text: $parent, prompt: "~/workspace", mono: true)
                if local { Button("Choose…") { choose { parent = tilde($0) } } }
            }
            field("Name", text: Binding(get: { name }, set: { name = $0; nameEdited = true }), prompt: "repo", mono: true)
            if let t = target {
                HStack(spacing: 4) {
                    Image(systemName: "arrow.turn.down.right").font(.system(size: 10))
                    Text(t).font(.ui(size: 11.5, design: .monospaced))
                }
                .foregroundStyle(Theme.muted)
            }
        }
    }

    func field(_ label: String, text: Binding<String>, prompt: String, mono: Bool = false) -> some View {
        HStack(spacing: 8) {
            Text(label).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).frame(width: 70, alignment: .leading)
            TextField("", text: text, prompt: Text(prompt))
                .textFieldStyle(.plain).font(.ui(size: 12.5, design: mono ? .monospaced : .default))
                .padding(.horizontal, 9).frame(height: 30)
                .background(RoundedRectangle(cornerRadius: 7).fill(Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
                .disabled(busy)
        }
    }

    // MARK: actions

    func choose(_ done: (String) -> Void) {
        let p = NSOpenPanel()
        p.canChooseDirectories = true
        p.canChooseFiles = false
        p.canCreateDirectories = true
        p.prompt = "Choose"
        guard p.runModal() == .OK, let u = p.url else { return }
        done(u.path)
    }

    func tilde(_ path: String) -> String {
        let home = NSHomeDirectory()
        return path == home ? "~" : path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }

    func submit() {
        problem = ""
        let u = url.trimmingCharacters(in: .whitespaces)
        if isCloud {
            guard let pick = cloudPick else { return }
            if mode == .fromMac {
                // The Move sheet does the rest — readies the machine, says
                // what moves, moves it — once this one has gone.
                let t = MoveProjectTarget(id: key("local", fromProject), cloud: true, pick: pick)
                store.addProjectTarget = nil
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { store.moveProjectTarget = t }
                return
            }
            guard let t = target else { return }
            busy = true
            Task {
                defer { busy = false; prep = nil }
                do {
                    let m = try await store.readyCloud(pick) { prep = $0 }
                    // Made or woken now: asked again, it is only connected to.
                    if let id = store.catalog.first(where: { $0.id == m })?.cloudID { cloudChoice = id; cloudTouched = true }
                    prep = nil
                    try await store.addProject(machine: m, mode: mode, path: t, url: u)
                    store.addProjectTarget = nil
                } catch {
                    problem = "\(error)"
                }
            }
            return
        }
        guard let t = target, let m = machine else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                try await store.addProject(machine: m, mode: mode, path: t, url: u)
                store.addProjectTarget = nil
            } catch {
                problem = "\(error)"
            }
        }
    }
}
