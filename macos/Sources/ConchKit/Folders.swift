import Foundation

// Folders are groups of your own in the tree, as the TUI keeps them
// (internal/tui/folders.go): a name and what you put in it, inside one
// section of the CLI — Agents, Terminals or SSH. They live in ui.json's
// "folders", by section, and a TUI running at the same time merges a
// change made here (internal/tui/uisync.go), so both show the same ones.
//
// A member is a pane, by id and the name it had (an id handed out again by
// a new server, to a pane of another name, is not that pane), or in the
// SSH section a saved host, whose sessions all follow it in.

/// A CLI section that holds folders, by the number the TUI saves it under
/// (its nodeKind): the key of a section's folders has it in it.
public enum FolderSection: Int, Sendable, CaseIterable {
    case agents = 3
    case terminals = 4
    case ssh = 11

    public var title: String {
        switch self {
        case .agents: return "Agents"
        case .terminals: return "Terminals"
        case .ssh: return "SSH"
        }
    }
}

public struct FolderMember: Equatable, Sendable {
    public var name: String?
    public var id: String?
    public var host: String?

    public init(name: String? = nil, id: String? = nil, host: String? = nil) {
        self.name = name
        self.id = id
        self.host = host
    }

    /// How a pane is written down in a folder (the TUI's folderMember).
    public init(pane p: PaneInfo) {
        self.init(name: p.name.isEmpty ? nil : p.name, id: p.id)
    }
}

public struct Folder: Equatable, Sendable {
    public var name: String
    public var members: [FolderMember]

    public init(name: String, members: [FolderMember] = []) {
        self.name = name
        self.members = members
    }

    /// The panes this folder holds of those given, by id — and only where
    /// the name written down agrees — skipping any already taken, which it
    /// then takes (the TUI's claim): a pane is in one folder.
    public func claim(_ panes: [PaneInfo], taken: inout Set<String>) -> [PaneInfo] {
        let byID = Dictionary(panes.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        var out: [PaneInfo] = []
        for mem in members {
            guard let id = mem.id, !id.isEmpty, let p = byID[id], !taken.contains(p.id) else { continue }
            if let n = mem.name, !n.isEmpty, p.name != n { continue }
            taken.insert(p.id)
            out.append(p)
        }
        return out
    }

    public var hosts: [String] { members.compactMap { ($0.host ?? "").isEmpty ? nil : $0.host } }
}

/// Where a machine's CLI section keeps its folders in ui.json (the TUI's
/// folderKey with no project): "/3" here, "busybox~/3" on busybox.
public func folderKey(machine: String, section: FolderSection) -> String {
    (machine == "local" ? "" : machine + "~") + "/\(section.rawValue)"
}

/// What a workspace's list of folders calls one (the TUI's spaceFolder).
public func spaceFolder(_ key: String, _ name: String) -> String { key + "\n" + name }

/// The TUI's id for a folder's row, for its fold: folded until opened.
public func folderFold(_ key: String, _ name: String) -> String { "folder:" + key + "/" + name }

/// The folders in a ui.json, by section key, each in the order made.
public func loadFolders(uiJSON data: Data?) -> [String: [Folder]] {
    guard let data, let ui = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let all = ui["folders"] as? [String: Any] else { return [:] }
    var out: [String: [Folder]] = [:]
    for (k, v) in all {
        guard let list = v as? [[String: Any]] else { continue }
        out[k] = list.compactMap { f in
            guard let name = f["name"] as? String, !name.isEmpty else { return nil }
            let mems = (f["members"] as? [[String: Any]] ?? []).map {
                FolderMember(name: $0["name"] as? String, id: $0["id"] as? String, host: $0["host"] as? String)
            }
            return Folder(name: name, members: mems)
        }
    }
    return out
}

/// A folder's name as the TUI takes one (checkFolderName): something, at
/// most 64 characters, and no control characters. nil when it will do.
public func folderNameProblem(_ name: String) -> String? {
    if name.isEmpty { return "A folder needs a name" }
    if name.count > 64 { return "A folder's name is at most 64 characters" }
    if name.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7f }) { return "A folder's name can't contain control characters" }
    return nil
}

/// Edits to ui.json's folders, each leaving the rest of the file as it
/// was. A file that does not parse is not written over: that throws.
public enum FolderEdit {
    static func read(_ data: Data?) throws -> [String: Any] {
        guard let data, !data.isEmpty else { return [:] }
        guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ConchError(code: "ui.json", message: "ui.json is not what conch writes; not changing it")
        }
        return obj
    }

    static func write(_ ui: [String: Any]) throws -> Data {
        try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
    }

    static func folders(_ ui: [String: Any], _ key: String) -> [[String: Any]] {
        (ui["folders"] as? [String: Any])?[key] as? [[String: Any]] ?? []
    }

    static func setFolders(_ ui: inout [String: Any], _ key: String, _ list: [[String: Any]]) {
        var all = ui["folders"] as? [String: Any] ?? [:]
        if list.isEmpty { all.removeValue(forKey: key) } else { all[key] = list }
        if all.isEmpty { ui.removeValue(forKey: "folders") } else { ui["folders"] = all }
    }

    /// Changes a workspace's list of folders: "first" is first_space's.
    static func editSpaceFolders(_ ui: inout [String: Any], workspace: String, _ change: (inout [String]) -> Void) {
        if workspace == "first" {
            var first = ui["first_space"] as? [String: Any] ?? [:]
            var list = first["folders"] as? [String] ?? []
            change(&list)
            if list.isEmpty { first.removeValue(forKey: "folders") } else { first["folders"] = list }
            if first.isEmpty { ui.removeValue(forKey: "first_space") } else { ui["first_space"] = first }
            return
        }
        guard var spaces = ui["spaces"] as? [[String: Any]] else { return }
        for i in spaces.indices where (spaces[i]["id"] as? String ?? "").isEmpty { spaces[i]["id"] = "w\(i + 1)" }
        guard let i = spaces.firstIndex(where: { $0["id"] as? String == workspace }) else { return }
        var list = spaces[i]["folders"] as? [String] ?? []
        change(&list)
        if list.isEmpty { spaces[i].removeValue(forKey: "folders") } else { spaces[i]["folders"] = list }
        ui["spaces"] = spaces
    }

    /// Every workspace's list of folders changed alike.
    static func editAllSpaceFolders(_ ui: inout [String: Any], _ change: (inout [String]) -> Void) {
        editSpaceFolders(&ui, workspace: "first", change)
        for s in (ui["spaces"] as? [[String: Any]] ?? []).indices {
            let id = ((ui["spaces"] as? [[String: Any]])?[s]["id"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? "w\(s + 1)"
            editSpaceFolders(&ui, workspace: id, change)
        }
    }

    static func member(_ m: FolderMember) -> [String: Any] {
        var d: [String: Any] = [:]
        if let n = m.name, !n.isEmpty { d["name"] = n }
        if let i = m.id, !i.isEmpty { d["id"] = i }
        if let h = m.host, !h.isEmpty { d["host"] = h }
        return d
    }

    /// An empty folder in a section, shown in the workspace it was made in.
    /// A folder of that name already there is an error: two folders called
    /// prod in one section would be two places for one thing.
    public static func adding(_ name: String, key: String, workspace: String, in data: Data?) throws -> Data {
        let name = name.trimmingCharacters(in: .whitespaces)
        if let p = folderNameProblem(name) { throw ConchError(code: "folder", message: p) }
        var ui = try read(data)
        var list = folders(ui, key)
        if list.contains(where: { $0["name"] as? String == name }) {
            throw ConchError(code: "folder", message: "There is a folder called \(name) there already")
        }
        list.append(["name": name])
        setFolders(&ui, key, list)
        editSpaceFolders(&ui, workspace: workspace) { l in
            let k = spaceFolder(key, name)
            if !l.contains(k) { l.append(k) }
        }
        return try write(ui)
    }

    /// A folder renamed, its members with it, and every workspace that
    /// lists it listing the new name.
    public static func renaming(_ old: String, to name: String, key: String, in data: Data?) throws -> Data {
        let name = name.trimmingCharacters(in: .whitespaces)
        if let p = folderNameProblem(name) { throw ConchError(code: "folder", message: p) }
        var ui = try read(data)
        var list = folders(ui, key)
        guard let i = list.firstIndex(where: { $0["name"] as? String == old }) else {
            throw ConchError(code: "folder", message: "No folder called \(old)")
        }
        if name == old { return try write(ui) }
        if list.contains(where: { $0["name"] as? String == name }) {
            throw ConchError(code: "folder", message: "There is a folder called \(name) there already")
        }
        list[i]["name"] = name
        setFolders(&ui, key, list)
        editAllSpaceFolders(&ui) { l in
            l = l.map { $0 == spaceFolder(key, old) ? spaceFolder(key, name) : $0 }
        }
        return try write(ui)
    }

    /// A folder taken away. What was in it stays, out of any folder, and
    /// nothing closes.
    public static func removing(_ name: String, key: String, in data: Data?) throws -> Data {
        var ui = try read(data)
        var list = folders(ui, key)
        list.removeAll { $0["name"] as? String == name }
        setFolders(&ui, key, list)
        editAllSpaceFolders(&ui) { l in l.removeAll { $0 == spaceFolder(key, name) } }
        return try write(ui)
    }

    /// A pane put in the folder of that name (made, in that workspace, when
    /// there is none), or with folder nil taken out of the section's
    /// folders. It is taken out of every folder of the section first — by
    /// what stands for it and no other pane, so the second of two panes
    /// called zsh leaves the first where it is. panes is the section's.
    public static func moving(_ pane: PaneInfo, to folder: String?, key: String, panes: [PaneInfo],
                              workspace: String, in data: Data?) throws -> Data {
        var ui = try read(data)
        if let folder, !folders(ui, key).contains(where: { $0["name"] as? String == folder }) {
            ui = try read(adding(folder, key: key, workspace: workspace, in: write(ui)))
        }
        var list = folders(ui, key)
        let parsed = loadFolders(uiJSON: try write(ui))[key] ?? []
        for (i, f) in parsed.enumerated() {
            // Which member is this pane, worked out as the tree does.
            var taken: Set<String> = []
            let byID = Dictionary(panes.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            for (j, mem) in f.members.enumerated() {
                guard let id = mem.id, let p = byID[id], !taken.contains(id) else { continue }
                if let n = mem.name, !n.isEmpty, p.name != n { continue }
                taken.insert(id)
                if id == pane.id {
                    var mems = list[i]["members"] as? [[String: Any]] ?? []
                    if j < mems.count { mems.remove(at: j) }
                    if mems.isEmpty { list[i].removeValue(forKey: "members") } else { list[i]["members"] = mems }
                    break
                }
            }
        }
        if let folder, let i = list.firstIndex(where: { $0["name"] as? String == folder }) {
            var mems = list[i]["members"] as? [[String: Any]] ?? []
            mems.append(member(FolderMember(pane: pane)))
            list[i]["members"] = mems
        }
        setFolders(&ui, key, list)
        return try write(ui)
    }

    /// A saved host put in an SSH folder (made when there is none), or with
    /// folder nil out of all of them. Its sessions go with it, so any of
    /// them held in a folder as a pane comes out of there (the TUI's
    /// putHostInFolder).
    public static func movingHost(_ host: String, to folder: String?, key: String, sessions: [PaneInfo],
                                  workspace: String, in data: Data?) throws -> Data {
        var ui = try read(data)
        for p in sessions where p.sshTarget == host {
            ui = try read(moving(p, to: nil, key: key, panes: sessions, workspace: workspace, in: write(ui)))
        }
        if let folder, !folders(ui, key).contains(where: { $0["name"] as? String == folder }) {
            ui = try read(adding(folder, key: key, workspace: workspace, in: write(ui)))
        }
        var list = folders(ui, key)
        for i in list.indices {
            var mems = list[i]["members"] as? [[String: Any]] ?? []
            mems.removeAll { $0["host"] as? String == host }
            if mems.isEmpty { list[i].removeValue(forKey: "members") } else { list[i]["members"] = mems }
        }
        if let folder, let i = list.firstIndex(where: { $0["name"] as? String == folder }) {
            var mems = list[i]["members"] as? [[String: Any]] ?? []
            mems.append(["host": host])
            list[i]["members"] = mems
        }
        setFolders(&ui, key, list)
        return try write(ui)
    }
}

extension Workspace {
    /// Whether this workspace shows folder f of section key (the TUI's
    /// hasFolder): one made in it, in the first one no other made, one
    /// holding a pane it shows, or one holding a saved host.
    public func shows(folder f: Folder, key: String, panes: [PaneInfo]) -> Bool {
        let k = spaceFolder(key, f.name)
        if folders.contains(k) || first && !claimedFolders.contains(k) { return true }
        var taken: Set<String> = []
        if !f.claim(panes, taken: &taken).isEmpty { return true }
        return !f.hosts.isEmpty
    }
}
