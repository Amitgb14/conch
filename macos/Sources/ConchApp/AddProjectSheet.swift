import AppKit
import ConchKit
import SwiftUI

// Add project, three ways — the TUI's project browser and the CLI's
// `conch project add | clone | create`: a folder that is already there, a
// git repository cloned from a URL, or a new folder made a git repository.
// On this Mac a folder is chosen in a panel; on another machine its path is
// typed, since the panel can only see this one.

/// The machine an Add project sheet is for.
struct AddProjectTarget: Identifiable {
    var id: String // the machine
}

struct AddProjectSheet: View {
    @ObservedObject var store: Store
    let machine: String
    @Environment(\.conchTheme) private var theme

    @State private var mode: AddProjectMode = .clone
    @State private var folder = ""
    @State private var url = ""
    @State private var parent = ""
    @State private var name = ""
    @State private var nameEdited = false
    @State private var busy = false
    @State private var problem = ""

    var local: Bool { machine == "local" }
    var cloneSupported: Bool { store.connection(machine)?.hello?.capabilities?.contains("worktree.move.v1") == true }

    var target: String? {
        switch mode {
        case .folder: return folder.trimmingCharacters(in: .whitespaces).isEmpty ? nil : folder.trimmingCharacters(in: .whitespaces)
        case .clone, .create: return projectPath(parent: parent, name: name)
        }
    }

    var ready: Bool {
        guard !busy, target != nil else { return false }
        return mode != .clone || (looksCloneable(url) && cloneSupported)
    }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                LucideIcon("folder-open", color: Theme.accent, size: 18)
                    .frame(width: 34, height: 34)
                    .background(RoundedRectangle(cornerRadius: 9).fill(Theme.accentDeep.opacity(0.3)))
                VStack(alignment: .leading, spacing: 1) {
                    Text("Add a project").font(.ui(size: 15, weight: .semibold))
                    Text(local ? "On this Mac" : "On \(store.label(machine))").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            Picker("", selection: $mode) {
                ForEach(AddProjectMode.allCases) { Text($0.title).tag($0) }
            }
            .pickerStyle(.segmented).labelsHidden()

            switch mode {
            case .folder: folderForm
            case .clone: cloneForm
            case .create: createForm
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
                    Text(mode == .clone ? "Cloning…" : "Adding…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
                Spacer()
                Button("Cancel") { store.addProjectTarget = nil }.keyboardShortcut(.cancelAction).disabled(busy)
                Button(mode == .clone ? "Clone & Add" : mode == .create ? "Create & Add" : "Add") { submit() }
                    .keyboardShortcut(.defaultAction).disabled(!ready)
            }
        }
        .padding(20).frame(width: 520)
        .onAppear {
            if parent.isEmpty {
                parent = local ? defaultProjectParent(home: NSHomeDirectory(), exists: { FileManager.default.fileExists(atPath: $0) }) : "~"
            }
        }
        .onChange(of: url) { _, u in if !nameEdited { name = repoName(u) } }
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
            Text("Clones the repository on \(local ? "this Mac" : store.label(machine)) and adds it. A private one uses that machine's git credentials — gh, an ssh key or a credential helper; git never stops to ask for a password here.")
                .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            field("Repository", text: $url, prompt: "https://github.com/owner/repo.git  or  git@github.com:owner/repo.git", mono: true)
            if !url.isEmpty && !looksCloneable(url) {
                Text("A git URL: https://…, ssh://…, git@host:owner/repo, or a path").font(.ui(size: 11)).foregroundStyle(Theme.warn)
            }
            whereFields
            if !cloneSupported {
                Text("The server on \(store.label(machine)) predates cloning a project; reload or upgrade it.").font(.ui(size: 11.5)).foregroundStyle(Theme.warn)
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
        guard let t = target else { return }
        busy = true
        problem = ""
        let u = url.trimmingCharacters(in: .whitespaces)
        Task {
            defer { busy = false }
            do {
                try await store.addProject(machine: machine, mode: mode, path: t, url: u)
                store.addProjectTarget = nil
            } catch {
                problem = "\(error)"
            }
        }
    }
}
