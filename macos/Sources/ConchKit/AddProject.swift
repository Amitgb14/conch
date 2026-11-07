import Foundation

// Add project, as the TUI's project browser and the CLI offer it: a folder
// that is already there, a git repository cloned from a URL, or a new
// folder made a git repository. What the sheet decides is here, so it can
// be tested.

public enum AddProjectMode: String, CaseIterable, Identifiable, Sendable {
    case folder, clone, create
    public var id: String { rawValue }
    public var title: String {
        switch self {
        case .folder: return "Open a folder"
        case .clone: return "Clone a repository"
        case .create: return "New repository"
        }
    }
}

/// The folder a clone of url is given by default — what `git clone` names
/// it (gitx.RepoName): the last part of the repository's path, without
/// .git. https and ssh URLs, scp-like ones (git@github.com:owner/repo.git)
/// and local paths; "" when the URL names no repository.
public func repoName(_ url: String) -> String {
    let s = url.trimmingCharacters(in: .whitespaces)
    var p = s
    if s.contains("://") {
        guard let u = URL(string: s) else { return "" }
        p = u.path
    } else if let c = s.firstIndex(of: ":"), c > s.startIndex, !s[..<c].contains("/"), !s[..<c].contains("\\") {
        p = String(s[s.index(after: c)...])
    }
    while p.hasSuffix("/") { p.removeLast() }
    if p.hasSuffix(".git") { p.removeLast(4) }
    while p.hasSuffix("/") { p.removeLast() }
    let name = (p as NSString).lastPathComponent
    return ["", ".", "..", "/"].contains(name) ? "" : name
}

/// Whether s looks like something git can clone: a URL with a scheme git
/// speaks, scp-like user@host:path, or an absolute or ~ path.
public func looksCloneable(_ s: String) -> Bool {
    let t = s.trimmingCharacters(in: .whitespaces)
    guard !t.isEmpty, !t.contains(" ") else { return false }
    for scheme in ["https://", "http://", "ssh://", "git://", "file://"] where t.lowercased().hasPrefix(scheme) {
        return t.count > scheme.count
    }
    if t.hasPrefix("/") || t.hasPrefix("~/") { return true }
    if let c = t.firstIndex(of: ":"), c > t.startIndex, !t[..<c].contains("/") {
        return t.index(after: c) < t.endIndex
    }
    return false
}

/// The project's folder: parent and name joined, ~ kept for the server to
/// resolve; nil while either is missing or the name is not one folder.
public func projectPath(parent: String, name: String) -> String? {
    let p = parent.trimmingCharacters(in: .whitespaces)
    let n = name.trimmingCharacters(in: .whitespaces)
    guard !p.isEmpty, !n.isEmpty, !n.contains("/"), n != ".", n != ".." else { return nil }
    return p.hasSuffix("/") ? p + n : p + "/" + n
}

/// Where a new project goes by default: ~/workspace when it exists (as
/// conch's own projects do), else the home folder.
public func defaultProjectParent(home: String, exists: (String) -> Bool) -> String {
    exists((home as NSString).appendingPathComponent("workspace")) ? "~/workspace" : "~"
}
