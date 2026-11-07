import Foundation

// A project's files, as the TUI's Files view shows them (internal/tui/
// files.go): its main checkout as a lazily opened tree, each folder read
// when it is opened (fs.list with files, fs.files.v1), beside the start of
// the file picked (fs.read, fs.read.v1). Not an editor: it answers "what
// did the agent just write?" without leaving the window.

/// One entry of a folder, as fs.list gives it in a checkout listing.
public struct FSEntry: Decodable, Equatable, Sendable {
    public var name: String
    public var dir: Bool?
    public var size: Int64?
    public var symlink: Bool?
    public var broken: Bool?
    public var ignored: Bool?
    /// Its git status in the changes view's letters (M, A, D, R, U, ?); a
    /// folder carries the most telling one of what is in it.
    public var status: String?

    public init(name: String, dir: Bool = false, size: Int64? = nil, status: String? = nil) {
        self.name = name
        self.dir = dir
        self.size = size
        self.status = status
    }

    public var isDir: Bool { dir ?? false }
}

public struct FSList: Decodable, Sendable {
    public var path: String?
    public var entries: [FSEntry]?
    /// The folder had more entries than were sent.
    public var truncated: Bool?
}

/// The start of a file, for its preview. A binary file has no data.
public struct FSRead: Decodable, Sendable {
    public var path: String?
    public var size: Int64?
    public var data: String?
    public var truncated: Bool?
    public var binary: Bool?
    public var mime: String?
    /// The whole file's fingerprint, when data is all of it: what a save
    /// is checked against (fs.write.v1). nil from an older server, or for
    /// a file read only in part.
    public var hash: String?

    /// Whether it can be edited and saved: all of it read, as text, with a
    /// fingerprint to save against.
    public var editable: Bool { binary != true && truncated != true && data != nil && !(hash ?? "").isEmpty }
}

/// The result of a save (fs.write).
public struct FSWriteResult: Decodable, Sendable {
    public var path: String?
    public var size: Int64?
    public var hash: String
}

/// A project's Files tab's key in the layout: "files|machine|project".
public func filesKey(_ machine: String, _ project: String) -> String { "files|\(machine)|\(project)" }

public func parseFilesKey(_ k: String) -> (machine: String, project: String)? {
    let parts = k.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 3, parts[0] == "files", !parts[2].isEmpty else { return nil }
    return (parts[1], parts[2])
}

/// One row of the files tree: a folder or a file, how deep, and its path
/// in the checkout ("src/app.go"; "" is the checkout itself).
public struct FileRow: Equatable, Sendable, Identifiable {
    public var path: String
    public var name: String
    public var depth: Int
    public var dir: Bool
    public var open: Bool
    public var status: String
    public var ignored: Bool
    public var id: String { path }
}

/// The tree as rows: each folder read so far (by path, "" the top), the
/// open ones' entries under them, folders before files and each by name
/// as a person sorts them. A folder open but not read yet shows nothing
/// under it until it is.
public func fileRows(_ dirs: [String: [FSEntry]], open: Set<String>) -> [FileRow] {
    var out: [FileRow] = []
    func walk(_ dir: String, _ depth: Int) {
        let entries = (dirs[dir] ?? []).sorted {
            if $0.isDir != $1.isDir { return $0.isDir }
            return $0.name.localizedStandardCompare($1.name) == .orderedAscending
        }
        for e in entries {
            let p = dir.isEmpty ? e.name : dir + "/" + e.name
            let isOpen = e.isDir && open.contains(p)
            out.append(FileRow(path: p, name: e.name, depth: depth, dir: e.isDir, open: isOpen,
                               status: e.status ?? "", ignored: e.ignored ?? false))
            if isOpen { walk(p, depth + 1) }
        }
    }
    walk("", 0)
    return out
}

/// A size as a file list says it: 812 B, 4.1 KB, 2.3 MB.
public func fileSize(_ n: Int64) -> String {
    if n < 1024 { return "\(n) B" }
    let units = ["KB", "MB", "GB", "TB"]
    var v = Double(n) / 1024, i = 0
    while v >= 1024 && i < units.count - 1 { v /= 1024; i += 1 }
    return v < 10 ? String(format: "%.1f %@", v, units[i]) : String(format: "%.0f %@", v, units[i])
}
