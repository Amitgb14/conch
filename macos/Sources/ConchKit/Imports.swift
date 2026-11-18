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
    /// The handoff it is, or would be, written to: none for a conversation
    /// that comes over as a session of its agent's own.
    public var file: String?
    /// "workspace", "repository", "chosen" (a folder the person picked,
    /// Resume In…) or "gone".
    public var `where`: String?
    public var lastUsed: String?
    /// Its file is there: imported now, or by an earlier import.
    public var imported: Bool?
    /// How it comes over: "written", "added", "unchanged" or "carried-on"
    /// for a session of its agent's own; "markdown" for a handoff. Older
    /// conches said nothing: a handoff.
    public var status: String?
    /// Why it comes over as it does, when that needs saying.
    public var why: String?
    /// The agent as conch names it ("claude"), and its session to resume.
    public var agentName: String?
    public var session: String?
    public var sessionFile: String?
    /// The agent's own words that resume it, after `conch new -agent A
    /// -cwd DIR --`: ["resume", ID] for Codex. Older conches said none,
    /// and wrote only Claude Code's, whose are ["--resume", ID].
    public var resumeArgs: [String]?
    /// The conch project its repository was added to as, after the import.
    public var project: String?

    enum CodingKeys: String, CodingKey {
        case id, title, agent, repo, branch, workspace, dir, messages, file, `where`, imported, status, why, agentName, session, sessionFile, project, resumeArgs
        case lastUsed = "last_used"
    }

    public init(id: String, title: String, agent: String, repo: String? = nil, branch: String? = nil, workspace: String? = nil,
                dir: String? = nil, messages: Int, file: String? = nil, where w: String? = nil, lastUsed: String? = nil, imported: Bool? = nil,
                status: String? = nil, why: String? = nil, agentName: String? = nil, session: String? = nil, sessionFile: String? = nil) {
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
        self.status = status
        self.why = why
        self.agentName = agentName
        self.session = session
        self.sessionFile = sessionFile
    }

    /// It comes over as a session its agent resumes, not a handoff.
    public var resumes: Bool { status != nil && status != "markdown" }

    /// How it comes over, in the sheet's words: "resumes in Claude Code",
    /// "as Markdown: Codex conversations come over as Markdown for now".
    public var how: String {
        switch status {
        case "written", "added", "unchanged": return "resumes in \(agent)"
        case "found": return "resumes in \(agent), which kept it itself"
        case "carried-on": return "left alone: carried on in conch since"
        default:
            guard let why, !why.isEmpty else { return "as Markdown" }
            return "as Markdown: " + why
        }
    }

    /// The command that resumes it, as `conch import` prints it; nil for a
    /// handoff, or one with nowhere to resume.
    public var resumeArguments: [String]? {
        guard resumes, let session, !session.isEmpty, let dir, !dir.isEmpty else { return nil }
        let words = resumeArgs.flatMap { $0.isEmpty ? nil : $0 } ?? ["--resume", session]
        return ["new", "-agent", agentName ?? "claude", "-cwd", dir, "--"] + words
    }

    /// Its state in the sheet, in words.
    public var state: String {
        switch self.where {
        case "workspace": return "worktree on disk"
        case "repository": return "worktree removed · carries on in the repository"
        case "chosen": return "resumes in a folder you chose"
        default: return "neither worktree nor repository on this Mac"
        }
    }

    /// Its row's second line, as the mock's: the repository, the branch
    /// (or the workspace's name), and what became of its folders —
    /// "shop · berlin · worktree removed".
    public var place: String {
        var parts = [repo.flatMap { $0.isEmpty ? nil : $0 } ?? "unknown repository"]
        if let b = branch, !b.isEmpty {
            parts.append(b)
        } else if let w = workspace, !w.isEmpty {
            parts.append((w as NSString).lastPathComponent)
        }
        switch self.where {
        case "repository": parts.append("worktree removed")
        case "chosen": parts.append("in a folder you chose")
        case "gone": parts.append("worktree and repository removed")
        default: break
        }
        return parts.joined(separator: " · ")
    }

    /// Whether a folder can be chosen for it to resume in: a Claude
    /// conversation that came over as a handoff only because its workspace
    /// and repository are gone, or a Codex one Codex kept (it names the
    /// thread) in the same state. A session written before whose folder
    /// has since gone is the same: a new folder can be chosen for it.
    public var canResumeIn: Bool {
        guard self.where == "gone", resumeArguments == nil else { return false }
        switch agentName ?? "" {
        case "claude": return true
        case "codex": return !(session ?? "").isEmpty
        default: return false
        }
    }

    /// The row's column beside its name: how it carries on, short.
    public var outcome: String {
        if canResumeIn { return resumes ? "\(agent) · its folder is gone" : "Handoff · nowhere to resume" }
        if resumes {
            switch status {
            case "carried-on": return "\(agent) · carried on since"
            default: return self.where == "repository" ? "Resumes in the repository" : "\(agent) · resumable"
            }
        }
        return "\(agent) · handoff"
    }

    /// The row's button: Resume, Resume In… for one with nowhere to
    /// resume, or Copy Prompt for a handoff; nil for none.
    public enum Action: Equatable, Sendable { case resume([String]), resumeIn, copyPrompt }
    public var action: Action? {
        if let a = resumeArguments { return .resume(a) }
        if canResumeIn { return .resumeIn }
        return file == nil ? nil : .copyPrompt
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
        // By time, not by text: "…+05:30" and "…Z", or ".5Z" and "Z", sort
        // wrongly as strings. A time that cannot be read goes last; ties
        // keep the order they came in.
        let when: (ImportConversation) -> Date = { $0.lastUsed.flatMap(parseRFC3339) ?? .distantPast }
        func newestFirst<T>(_ xs: [T], _ d: (T) -> Date) -> [T] {
            xs.enumerated().sorted { a, b in
                let da = d(a.element), db = d(b.element)
                return da != db ? da > db : a.offset < b.offset
            }.map(\.element)
        }
        var out = order.map { by[$0]! }
        for i in out.indices { out[i].conversations = newestFirst(out[i].conversations, when) }
        out = newestFirst(out) { $0.conversations.first.map(when) ?? .distantPast }
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
        guard n > 0 else { return "Nothing was imported." }
        let resumable = results.filter(\.resumes).count
        var s = "Imported \(n) conversation\(n == 1 ? "" : "s") from \(label)."
        if resumable == n {
            s += " Each resumes in its agent."
        } else if resumable > 0 {
            s += resumable == 1 ? " 1 resumes in its agent; \(n - 1) as Markdown." : " \(resumable) resume in their agents; \(n - resumable) as Markdown."
        }
        if results.contains(where: { $0.project != nil }) {
            s += " Their repositories are in conch now."
        }
        return s
    }

    /// The folder every handoff went into, for Show in Finder; nil when
    /// none was written.
    public var folder: String? {
        guard let first = results.lazy.compactMap(\.file).first else { return nil }
        // …/imports/READER/REPO/FILE.md: the import's folder is two up.
        return ((first as NSString).deletingLastPathComponent as NSString).deletingLastPathComponent
    }

    /// What a scan says came over already, for the import's page once the
    /// sheet is closed: the conversations conch has, as a session or a handoff, newest
    /// first. Nothing readable is nothing imported, not an error, since
    /// the page shows it beside everything else.
    public static func importedAlready(_ data: Data) -> [ImportConversation] {
        guard let all = try? JSONDecoder().decode([ImportConversation]?.self, from: data) else { return [] }
        var f = ImportFlow(name: "", label: "", reader: "")
        f.conversations = (all ?? []).filter { $0.imported == true }
        return f.groups.flatMap(\.conversations)
    }

    /// Resume In…: writes the conversation as a session in dir, which
    /// `conch import` remembers, and answers what it wrote as -json does.
    public static func resumeInArguments(reader: String, id: String, dir: String) -> [String] {
        ["import", reader, "-json", "-ids", id, "-dir", dir]
    }

    /// Resume: brings that one conversation up to date first — the page's
    /// list is a dry run, which moves and writes nothing, so a session
    /// filed for a worktree since removed is moved to where it resumes
    /// only by this — then resumes what it answers.
    public static func refreshArguments(reader: String, id: String) -> [String] {
        ["import", reader, "-json", "-ids", id]
    }

    /// What Resume or Resume In… brought back: the conversation, now resumable, or
    /// why it is not.
    public static func resumedIn(_ data: Data, id: String) -> Result<ImportConversation, ConchError> {
        guard let all = try? JSONDecoder().decode([ImportConversation]?.self, from: data),
              let c = (all ?? []).first(where: { $0.id == id }) else {
            return .failure(ConchError(code: "import", message: "conch import said nothing this app can read about it"))
        }
        guard c.resumeArguments != nil else {
            return .failure(ConchError(code: "import", message: c.why.map { "It cannot resume there: \($0)" } ?? "It cannot resume there."))
        }
        return .success(c)
    }

    /// What an agent is told to carry one on: as `conch import` says it;
    /// nil for one with no handoff.
    public static func prompt(_ c: ImportConversation) -> String? {
        c.file.map { "Read \($0) and carry on from where it leaves off." }
    }
}
