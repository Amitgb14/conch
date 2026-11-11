import AppKit
import ConchKit

// Folders in the CLI, ssh hosts' names, options and folders, and login
// without a password: each change is a change to ui.json, where the TUI
// keeps the same things and merges what the app writes.
extension Store {
    var uiPath: String { (conchDir as NSString).appendingPathComponent("ui.json") }

    /// ui.json changed by edit, then read again; what went wrong is said.
    func editUI(_ what: String, _ edit: (Data?) throws -> Data) -> Bool {
        do {
            let data = try edit(FileManager.default.contents(atPath: uiPath))
            try data.write(to: URL(fileURLWithPath: uiPath), options: .atomic)
        } catch {
            say("Could not \(what): \(error)", .err)
            return false
        }
        loadWorkspaces()
        return true
    }

    /// The panes of a machine's CLI section, as the tree lists them.
    func sectionPanes(_ machine: String, _ section: FolderSection) -> [PaneInfo] {
        let s = snapshots[machine] ?? Snapshot()
        return s.panes.filter { p in
            guard p.projectID == nil || s.project(p.projectID!) == nil else { return false }
            switch section {
            case .agents: return p.agent != nil
            case .ssh: return p.agent == nil && p.isSSH
            case .terminals: return p.agent == nil && !p.isSSH
            }
        }
    }

    func section(of p: PaneInfo) -> FolderSection { p.agent != nil ? .agents : p.isSSH ? .ssh : .terminals }

    /// Opens a folder's row, as the TUI does when something is put in it.
    func openFolder(_ machine: String, _ section: FolderSection, _ name: String) {
        folds[folderFold(folderKey(machine: machine, section: section), name)] = true
        UserDefaults.standard.set(folds, forKey: "folds")
    }

    func askFolderName(_ title: String, info: String, value: String = "", button: String) -> String? {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.stringValue = value
        field.placeholderString = "eng, staging, prod…"
        a.accessoryView = field
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = field
        guard a.runModal() == .alertFirstButtonReturn else { return nil }
        let name = field.stringValue.trimmingCharacters(in: .whitespaces)
        if let p = folderNameProblem(name) {
            say(p, .err)
            return nil
        }
        return name
    }

    func newFolder(_ machine: String, _ section: FolderSection) {
        guard let name = askFolderName("New folder in \(section.title)", info: "A group of your own. Drag rows onto it, or use Move to Folder on a row's menu.", button: "Make Folder") else { return }
        let key = folderKey(machine: machine, section: section)
        if editUI("make \(name)", { try FolderEdit.adding(name, key: key, workspace: ws?.id ?? "first", in: $0) }) {
            openFolder(machine, section, name)
        }
    }

    func renameFolder(_ machine: String, _ section: FolderSection, _ old: String) {
        guard let name = askFolderName("Rename \(old)", info: "What is in it stays in it.", value: old, button: "Rename") else { return }
        let key = folderKey(machine: machine, section: section)
        if editUI("rename \(old)", { try FolderEdit.renaming(old, to: name, key: key, in: $0) }) {
            openFolder(machine, section, name)
        }
    }

    func removeFolder(_ machine: String, _ section: FolderSection, _ name: String) {
        let key = folderKey(machine: machine, section: section)
        if editUI("remove \(name)", { try FolderEdit.removing(name, key: key, in: $0) }) {
            say("Removed the folder \(name): what was in it is still there, and nothing closed")
        }
    }

    /// A pane or saved host put in a folder (nil: out of them all). An ssh
    /// session to a saved host moves the host, which its sessions follow.
    func move(_ item: FolderItem, to folder: String?) {
        let ws = ws?.id ?? "first"
        switch item {
        case .host(let h):
            let key = folderKey(machine: "local", section: .ssh)
            guard editUI("move \(h)", { try FolderEdit.movingHost(h, to: folder, key: key, sessions: sectionPanes("local", .ssh), workspace: ws, in: $0) }) else { return }
            if let folder { openFolder("local", .ssh, folder) }
            say(folder.map { "Moved \(h) to \($0)" } ?? "Moved \(h) out of its folder")
        case .pane(let k):
            guard let p = pane(k) else { return }
            let m = unkey(k).machine, sec = section(of: p)
            if sec == .ssh, let h = p.sshTarget, savedHosts.contains(where: { $0.host == h }) {
                move(.host(h), to: folder)
                return
            }
            let key = folderKey(machine: m, section: sec)
            guard editUI("move \(p.displayName)", { try FolderEdit.moving(p, to: folder, key: key, panes: sectionPanes(m, sec), workspace: ws, in: $0) }) else { return }
            if let folder { openFolder(m, sec, folder) }
            say(folder.map { "Moved \(p.displayName) to \($0)" } ?? "Moved \(p.displayName) out of its folder")
        }
    }

    func moveToNewFolder(_ item: FolderItem) {
        let what: String
        switch item {
        case .host(let h): what = h
        case .pane(let k): what = pane(k)?.displayName ?? "it"
        }
        guard let name = askFolderName("New folder", info: "Moves \(what) into it.", button: "Make and Move") else { return }
        move(item, to: name)
    }

    /// A row dropped on a folder (into it) or on its section (out of its
    /// folder): only within the section it belongs to.
    func drop(_ dragged: String, on node: TreeNode) -> Bool {
        // A project onto another groups the two, asked first; onto a group
        // it joins that group. Only on its own machine.
        if dragged.hasPrefix("project:") {
            let pk = String(dragged.dropFirst(8))
            switch node.kind {
            case .project(let other) where other != pk && unkey(other).machine == unkey(pk).machine && canGroup(unkey(pk).machine):
                groupWith(pk, other)
                return true
            case .group(let m, let name) where m == unkey(pk).machine:
                addToGroup(pk, name, ask: true)
                return true
            default:
                return false
            }
        }
        let item: FolderItem
        let machine: String, sec: FolderSection
        if dragged.hasPrefix("host:") {
            item = .host(String(dragged.dropFirst(5)))
            machine = "local"
            sec = .ssh
        } else if let p = pane(dragged) {
            item = .pane(dragged)
            machine = unkey(dragged).machine
            sec = section(of: p)
            let s = snapshots[machine] ?? Snapshot()
            guard p.projectID == nil || s.project(p.projectID!) == nil else { return false }
        } else {
            return false
        }
        switch node.kind {
        case .folder(let m, let s, let name) where m == machine && s == sec:
            move(item, to: name)
            return true
        case .cliSection(let m, let s) where m == machine && s == sec:
            move(item, to: nil)
            return true
        default:
            return false
        }
    }

    func collapseProjects(_ machine: String) {
        for pr in snapshots[machine]?.projects ?? [] {
            folds["p:" + (machine == "local" ? pr.id : "\(machine)~\(pr.id)")] = false
        }
        UserDefaults.standard.set(folds, forKey: "folds")
    }

    // MARK: ssh hosts

    /// SSH to a Host…, or a host's details edited: the host, a name for the
    /// sidebar, its ssh options and folder, whether to keep it, and for a
    /// new one whether to put this computer's key there first.
    struct HostForm {
        var host: String
        var name: String
        var options: String
        var folder: String?
        var save: Bool
        var keyFirst: Bool
    }

    func askHost(title: String, info: String, button: String, start: HostForm, editing: Bool) -> HostForm? {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        let box = NSStackView(frame: NSRect(x: 0, y: 0, width: 360, height: editing ? 200 : 260))
        box.orientation = .vertical
        box.alignment = .leading
        box.spacing = 6
        func field(_ label: String, _ hint: String, _ value: String) -> NSTextField {
            box.addArrangedSubview(NSTextField(labelWithString: label))
            let f = NSTextField(frame: NSRect(x: 0, y: 0, width: 360, height: 24))
            f.placeholderString = hint
            f.stringValue = value
            f.translatesAutoresizingMaskIntoConstraints = false
            f.widthAnchor.constraint(equalToConstant: 360).isActive = true
            box.addArrangedSubview(f)
            return f
        }
        let host = field("Host", "user@host, a host alias or ssh://user@host:port", start.host)
        host.isEditable = !editing
        let name = field("Name", "optional, how the sidebar names it", start.name)
        let options = field("SSH options", "optional, e.g. -p 2222 -J bastion", start.options)
        box.addArrangedSubview(NSTextField(labelWithString: "Folder"))
        let folder = NSPopUpButton(frame: NSRect(x: 0, y: 0, width: 200, height: 26), pullsDown: false)
        let names = (folders[folderKey(machine: "local", section: .ssh)] ?? []).map(\.name)
        folder.addItem(withTitle: "No folder")
        folder.addItems(withTitles: names)
        if let f = start.folder, names.contains(f) { folder.selectItem(withTitle: f) }
        box.addArrangedSubview(folder)
        let save = NSButton(checkboxWithTitle: "Save this host, to connect again from the sidebar", target: nil, action: nil)
        save.state = start.save ? .on : .off
        let key = NSButton(checkboxWithTitle: "Set up login without a password first (ssh asks for it once, in a terminal)", target: nil, action: nil)
        key.state = start.keyFirst ? .on : .off
        if !editing {
            box.addArrangedSubview(save)
            box.addArrangedSubview(key)
        }
        a.accessoryView = box
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = editing ? name : host
        guard a.runModal() == .alertFirstButtonReturn else { return nil }
        let target = host.stringValue.trimmingCharacters(in: .whitespaces)
        guard !target.isEmpty else { return nil }
        let chosen = folder.indexOfSelectedItem > 0 ? folder.titleOfSelectedItem : nil
        let n = name.stringValue.trimmingCharacters(in: .whitespaces)
        // A name or a folder is only kept on a saved host.
        return HostForm(host: target, name: n, options: options.stringValue.trimmingCharacters(in: .whitespaces), folder: chosen,
                        save: editing || save.state == .on || !n.isEmpty || chosen != nil, keyFirst: !editing && key.state == .on)
    }

    func words(_ s: String) -> [String] { s.split(separator: " ").map(String.init) }

    func hostFolder(_ host: String) -> String? {
        (folders[folderKey(machine: "local", section: .ssh)] ?? []).first { $0.hosts.contains(host) }?.name
    }

    /// Keeps a host as the form says: saved with its name and options, and
    /// in its folder.
    func keep(_ f: HostForm) {
        let h = SavedHost(host: f.host, name: f.name.isEmpty ? nil : f.name, args: words(f.options))
        let key = folderKey(machine: "local", section: .ssh)
        let ws = ws?.id ?? "first"
        _ = editUI("save \(f.host)") { data in
            let saved = try savingHost(h, in: data)
            guard hostFolder(f.host) != f.folder else { return saved }
            return try FolderEdit.movingHost(f.host, to: f.folder, key: key, sessions: sectionPanes("local", .ssh), workspace: ws, in: saved)
        }
        if let folder = f.folder { openFolder("local", .ssh, folder) }
    }

    func sshToHost() {
        guard let f = askHost(title: "SSH to a Host", info: "Opens a terminal here logged in to the host, with your ssh config and keys. Nothing is installed there.",
                              button: "Connect", start: HostForm(host: "", name: "", options: "", folder: nil, save: true, keyFirst: false),
                              editing: false) else { return }
        if f.save { keep(f) }
        if f.keyFirst {
            copyKey(f.host, words(f.options)) { [weak self] works in
                if works { self?.login(f.host, self?.words(f.options) ?? []) }
            }
        } else {
            login(f.host, words(f.options))
        }
    }

    func editHost(_ host: String, _ args: [String]) {
        let saved = savedHosts.first { $0.host == host }
        guard let f = askHost(title: saved == nil ? "Save \(host)" : "Edit \(saved!.title)",
                              info: "How the sidebar names it, its ssh options and its folder. The TUI shows the same.",
                              button: "Save", start: HostForm(host: host, name: saved?.name ?? "", options: (saved?.args ?? args).joined(separator: " "),
                                                              folder: hostFolder(host), save: true, keyFirst: false),
                              editing: true) else { return }
        keep(f)
        say("Saved \(f.name.isEmpty ? f.host : f.name)")
    }

    /// A terminal logged in to a host with ssh (conch ssh), shown.
    func login(_ host: String, _ args: [String]) {
        guard let b = binary else { return }
        Task {
            do {
                let out = try await runConch(b, ["ssh", host] + args)
                let id = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !id.isEmpty else { return }
                layout.open(key("local", id))
                if let p = pane(key("local", id)) { joinWorkspace(machine: "local", pane: p) }
                focusTerminal()
            } catch {
                say("Could not connect to \(host): \(error)", .err)
            }
        }
    }

    func anotherSession(_ k: String) {
        guard let p = pane(k), let h = p.sshTarget else { return }
        login(h, savedHosts.first { $0.host == h }?.args ?? p.sshArgs)
    }

    /// This computer's key put on a host (conch ssh -copy-key), in a
    /// terminal of its own where ssh asks for the password: the app never
    /// sees it. When that terminal is done, the host is asked again whether
    /// it takes the key, and that is what is said — then done.
    func copyKey(_ host: String, _ args: [String], then done: ((Bool) -> Void)? = nil) {
        guard let b = binary else { return }
        Task {
            let k: String
            do {
                let out = try await runConch(b, ["ssh", "-copy-key", host] + args)
                let id = String(decoding: out, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !id.isEmpty else { return }
                k = key("local", id)
            } catch {
                say("Could not set up a key login to \(host): \(error)", .err)
                return
            }
            layout.open(k)
            focusTerminal()
            // Until its terminal ends: the password typed, the key copied,
            // enter pressed. Up to a quarter of an hour.
            for _ in 0..<450 {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                if pane(k)?.running != true { break }
            }
            let works = await checkKey(host, args)
            if works == true {
                say("\(host) now logs in with your key: no password")
            } else if works == false {
                say("\(host) still asks for a password: the key was not taken", .err)
            }
            done?(works == true)
        }
    }

    /// Whether a host logs in with this computer's keys (conch ssh
    /// -check-key: ssh in batch mode, which never asks), remembered for the
    /// rows' marks. nil when it could not be reached.
    @discardableResult
    func checkKey(_ host: String, _ args: [String]) async -> Bool? {
        guard let b = binary, let out = try? await runConch(b, ["ssh", "-check-key", host] + args) else { return nil }
        let word = String(decoding: out, as: UTF8.self).split(whereSeparator: \.isWhitespace).first.map(String.init)
        let works: Bool? = word == "key" ? true : word == "password" ? false : nil
        keyLogin[host] = works
        return works
    }

    /// Asks each ssh host listed — saved, or with a session open — whether
    /// it takes the key, once a host while the app runs, one at a time.
    func checkKeys() {
        var todo: [(String, [String])] = savedHosts.map { ($0.host, $0.args) }
        for p in snapshots["local"]?.panes ?? [] {
            if let h = p.sshTarget, p.running, !todo.contains(where: { $0.0 == h }) { todo.append((h, p.sshArgs)) }
        }
        todo.removeAll { keyAsked.contains($0.0) }
        guard !todo.isEmpty else { return }
        keyAsked.formUnion(todo.map(\.0))
        Task {
            for (h, args) in todo { await checkKey(h, args) }
        }
    }

    /// conch installed on a host, as Add Machine… does, that host filled in.
    func addAsMachine(_ host: String) { addMachine(target: host) }

}
