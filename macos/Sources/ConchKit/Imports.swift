import Foundation

// Integrations → Import (docs/plans/import.md in conch-integrations, the
// mock's screens 2 and 3): another app's conversations, listed by
// `conch import READER -n -json` and brought in by `-ids`. What decides —
// the groups, what can be ticked, the count on the button, the results —
// is here, so it is tested without AppKit.

/// One conversation, as `conch import READER -json` says it.
public struct ImportConversation: Decodable, Equatable, Identifiable, Sendable {
    public var id: String
    public var title: String
    public var agent: String
    public var repo: String?
    public var branch: String?
    public var workspace: String?
    /// Where to carry it on: the workspace, or the repository.
    public var dir: String?
    public var messages: Int
    /// The handoff it is, or would be, written to.
    public var file: String
    /// "workspace", "repository" or "gone".
    public var `where`: String?
    public var lastUsed: String?
    /// Its file is there: imported now, or by an earlier import.
    public var imported: Bool?

    enum CodingKeys: String, CodingKey {
        case id, title, agent, repo, branch, workspace, dir, messages, file, `where`, imported
        case lastUsed = "last_used"
    }

    public init(id: String, title: String, agent: String, repo: String? = nil, branch: String? = nil, workspace: String? = nil,
                dir: String? = nil, messages: Int, file: String, where w: String? = nil, lastUsed: String? = nil, imported: Bool? = nil) {
        self.id = id
        self.title = title
        self.agent = agent
        self.repo = repo
        self.branch = branch
        self.workspace = workspace
        self.dir = dir
        self.messages = messages
        self.file = file
        self.where = w
        self.lastUsed = lastUsed
        self.imported = imported
    }

    /// Its state in the sheet, in words.
    public var state: String {
        switch self.where {
        case "workspace": return "worktree on disk"
        case "repository": return "worktree removed · carries on in the repository"
        default: return "neither worktree nor repository on this Mac"
        }
    }
}

/// A workspace's conversations: the sheet's groups.
public struct ImportGroup: Equatable, Identifiable, Sendable {
    public var repo: String
    public var branch: String
    public var conversations: [ImportConversation]
    public var id: String { repo + "\u{1}" + branch }
    public var title: String { branch.isEmpty ? repo : repo + " · " + branch }
}

/// The sheet: scanning, choosing, importing, imported.
public struct ImportFlow: Equatable, Sendable {
    public var name: String
    public var label: String
    /// The reader's name: the word after `conch import`.
    public var reader: String
    public enum Phase: Equatable, Sendable { case scanning, choosing, importing, done, failed(String) }
    public var phase: Phase = .scanning
    public var conversations: [ImportConversation] = []
    /// The ids ticked.
    public var chosen: Set<String> = []
    /// What the import wrote, once done.
    public var results: [ImportConversation] = []

    public init(name: String, label: String, reader: String) {
        self.name = name
        self.label = label
        self.reader = reader
    }

    /// The scan's arguments: say what would be written, write nothing.
    public var scanArguments: [String] { ["import", reader, "-n", "-json"] }

    /// The import's arguments: only the ticked, in Conductor's order.
    public var runArguments: [String] {
        ["import", reader, "-json", "-ids", conversations.filter { chosen.contains($0.id) }.map(\.id).joined(separator: ",")]
    }

    /// Reads the scan: every conversation ticked but those imported
    /// already, which importing again would only replace.
    public mutating func scanned(_ data: Data) {
        do {
            conversations = try JSONDecoder().decode([ImportConversation]?.self, from: data) ?? []
            chosen = Set(conversations.filter { $0.imported != true }.map(\.id))
            phase = .choosing
        } catch {
            phase = .failed("conch import \(reader) said something this app cannot read: \(error)")
        }
    }

    /// The conversations by workspace, each group newest first, the groups
    /// by their newest.
    public var groups: [ImportGroup] {
        var order: [String] = []
        var by: [String: ImportGroup] = [:]
        for c in conversations {
            let g = ImportGroup(repo: c.repo ?? "Unknown repository", branch: c.branch ?? "", conversations: [])
            if by[g.id] == nil { order.append(g.id); by[g.id] = g }
            by[g.id]!.conversations.append(c)
        }
        var out = order.map { by[$0]! }
        for i in out.indices { out[i].conversations.sort { ($0.lastUsed ?? "") > ($1.lastUsed ?? "") } }
        out.sort { ($0.conversations.first?.lastUsed ?? "") > ($1.conversations.first?.lastUsed ?? "") }
        return out
    }

    public mutating func toggle(_ id: String) {
        if chosen.contains(id) { chosen.remove(id) } else { chosen.insert(id) }
    }

    /// Ticks or clears a whole workspace.
    public mutating func toggle(group: ImportGroup) {
        let ids = Set(group.conversations.map(\.id))
        if ids.isSubset(of: chosen) { chosen.subtract(ids) } else { chosen.formUnion(ids) }
    }

    /// The footer: "11 of 12 conversations · 1 imported already".
    public var footer: String {
        let n = conversations.filter { chosen.contains($0.id) }.count
        var s = "\(n) of \(conversations.count) conversation\(conversations.count == 1 ? "" : "s")"
        let again = conversations.filter { $0.imported == true }.count
        if again > 0 { s += " · \(again) imported already" }
        return s
    }

    /// The button: "Import 11", off with nothing ticked.
    public var button: String { "Import \(conversations.filter { chosen.contains($0.id) }.count)" }
    public var canImport: Bool { phase == .choosing && conversations.contains { chosen.contains($0.id) } }

    /// Reads what the import wrote.
    public mutating func imported(_ data: Data) {
        do {
            results = try JSONDecoder().decode([ImportConversation]?.self, from: data) ?? []
            phase = .done
        } catch {
            phase = .failed("conch import \(reader) said something this app cannot read: \(error)")
        }
    }

    /// The results' heading.
    public var doneWords: String {
        let n = results.count
        return n == 0 ? "Nothing was imported." : "Imported \(n) conversation\(n == 1 ? "" : "s") from \(label)."
    }

    /// The folder every handoff went into, for Show in Finder.
    public var folder: String? {
        guard let first = results.first?.file else { return nil }
        // …/imports/READER/REPO/FILE.md: the import's folder is two up.
        return ((first as NSString).deletingLastPathComponent as NSString).deletingLastPathComponent
    }

    /// What an agent is told to carry one on: as `conch import` says it.
    public static func prompt(_ c: ImportConversation) -> String {
        "Read \(c.file) and carry on from where it leaves off."
    }
}
