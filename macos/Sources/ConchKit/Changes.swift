import Foundation

// A branch's changes, as the TUI shows them when a branch is opened: the
// files it changes against the base (or the uncommitted work in its
// worktree), its commits, and one file's diff at a time — project.changes
// and project.diff, read rather than worked out again here.

public struct FileChange: Decodable, Equatable, Sendable, Identifiable {
    public var path: String
    public var origPath: String?
    /// M A D R C T, U for a conflict, ? untracked.
    public var code: String
    public var added: Int
    public var deleted: Int
    public var binary: Bool?
    /// Some of it is in the index (git add), and some of it is not.
    public var staged: Bool?
    public var unstaged: Bool?

    public var id: String { path }

    /// In the index whole, in part, or not at all.
    public var stage: Tick { staged != true ? .off : unstaged == true ? .mixed : .on }

    enum CodingKeys: String, CodingKey {
        case path, code, added, deleted, binary, staged, unstaged
        case origPath = "orig_path"
    }

    public init(path: String, code: String, added: Int, deleted: Int) {
        self.path = path
        self.code = code
        self.added = added
        self.deleted = deleted
    }
}

public struct CommitInfo: Decodable, Equatable, Sendable, Identifiable {
    public var hash: String
    public var subject: String
    public var author: String?
    public var time: Date?
    public var id: String { hash }
}

public struct Changes: Decodable, Equatable, Sendable {
    public var projectID: String
    public var branch: String
    public var base: String
    /// Set when Files are a worktree's uncommitted work.
    public var worktree: String?
    public var watched: Bool?
    public var files: [FileChange]
    public var commits: [CommitInfo]?
    /// What the branch's commits change against the base, beside the
    /// uncommitted work, when asked for.
    public var committedFiles: [FileChange]?

    enum CodingKeys: String, CodingKey {
        case branch, base, worktree, watched, files, commits
        case projectID = "project_id"
        case committedFiles = "committed_files"
    }

    /// Lines added and removed across the files.
    public var totals: (added: Int, deleted: Int) {
        files.reduce((0, 0)) { ($0.0 + $1.added, $0.1 + $1.deleted) }
    }
}

public struct DiffResult: Decodable { public var diff: String }

/// One line of a unified diff, ready to draw.
public struct DiffLine: Equatable, Sendable, Identifiable {
    public enum Kind: Equatable, Sendable { case meta, hunk, context, added, removed }
    public var id: Int
    public var kind: Kind
    public var text: String
    /// Line numbers in the old and new file, where the line has one.
    public var old: Int?
    public var new: Int?

    public init(id: Int, kind: Kind, text: String, old: Int? = nil, new: Int? = nil) {
        self.id = id
        self.kind = kind
        self.text = text
        self.old = old
        self.new = new
    }
}

/// A unified diff, line by line, with each line's numbers on either side
/// worked out from the hunk headers (@@ -a,b +c,d @@).
public func parseDiff(_ diff: String) -> [DiffLine] {
    var out: [DiffLine] = []
    var old = 0, new = 0, inHunk = false
    for (i, raw) in diff.split(separator: "\n", omittingEmptySubsequences: false).enumerated() {
        let line = String(raw)
        if line.hasPrefix("@@") {
            // @@ -12,7 +12,9 @@ context
            let parts = line.split(separator: " ")
            if parts.count >= 3 {
                old = Int(parts[1].dropFirst().split(separator: ",")[0]) ?? 0
                new = Int(parts[2].dropFirst().split(separator: ",")[0]) ?? 0
            }
            inHunk = true
            out.append(DiffLine(id: i, kind: .hunk, text: line))
            continue
        }
        if !inHunk || line.hasPrefix("diff --git") || line.hasPrefix("index ") || line.hasPrefix("--- ") || line.hasPrefix("+++ ") {
            if line.hasPrefix("diff --git") { inHunk = false }
            if !line.isEmpty { out.append(DiffLine(id: i, kind: .meta, text: line)) }
            continue
        }
        if line.hasPrefix("+") {
            out.append(DiffLine(id: i, kind: .added, text: String(line.dropFirst()), new: new))
            new += 1
        } else if line.hasPrefix("-") {
            out.append(DiffLine(id: i, kind: .removed, text: String(line.dropFirst()), old: old))
            old += 1
        } else if line.hasPrefix("\\") {
            out.append(DiffLine(id: i, kind: .meta, text: line)) // \ No newline at end of file
        } else if !(line.isEmpty && i == diff.split(separator: "\n", omittingEmptySubsequences: false).count - 1) {
            out.append(DiffLine(id: i, kind: .context, text: line.isEmpty ? "" : String(line.dropFirst()), old: old, new: new))
            old += 1
            new += 1
        }
    }
    return out
}

/// A branch's changes have a place in the layout of their own, beside the
/// panes: "changes|machine|project|branch" (branch names may hold "/").
public func changesKey(_ machine: String, _ project: String, _ branch: String) -> String {
    "changes|\(machine)|\(project)|\(branch)"
}

public func parseChangesKey(_ k: String) -> (machine: String, project: String, branch: String)? {
    let parts = k.split(separator: "|", maxSplits: 3, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 4, parts[0] == "changes", !parts[3].isEmpty else { return nil }
    return (parts[1], parts[2], parts[3])
}

extension Changes {
    /// The files a commit takes: those picked that are still uncommitted —
    /// one committed or reverted meanwhile drops out — or nil for every
    /// one, when nothing is picked. An empty list (all picked are gone)
    /// commits nothing rather than everything.
    public func commitFiles(picked: Set<String>) -> [String]? {
        if picked.isEmpty { return nil }
        return files.map(\.path).filter { picked.contains($0) }
    }

    /// Whether the branch has work to merge into its base: commits of its
    /// own, on a branch that is not the base.
    public var canMerge: Bool { branch != base && !(commits ?? []).isEmpty }
}
