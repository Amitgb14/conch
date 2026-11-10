import Foundation

// A task across a group's repos on a page of its own (TaskPage.swift in
// ConchApp draws it): each repo where it stands — its base and how far
// ahead, what is uncommitted, its pull request and checks — read from what
// the snapshot already holds for each repo's branch, so the page needs no
// call of its own to say it.

/// A task page's key in the layout: "task|machine|group|branch", the group
/// and branch escaped, since either may hold a "|".
public func taskKey(_ machine: String, _ group: String, _ branch: String) -> String {
    "task|\(machine)|\(keyEscape(group))|\(keyEscape(branch))"
}

public func parseTaskKey(_ k: String) -> (machine: String, group: String, branch: String)? {
    let parts = k.split(separator: "|", maxSplits: 3, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 4, parts[0] == "task", !parts[1].isEmpty,
          let g = parts[2].removingPercentEncoding, let b = parts[3].removingPercentEncoding, !g.isEmpty, !b.isEmpty else { return nil }
    return (parts[1], g, b)
}

private func keyEscape(_ s: String) -> String {
    s.replacingOccurrences(of: "%", with: "%25").replacingOccurrences(of: "|", with: "%7C")
}

/// One repo of a task as its card says it.
public struct TaskRepo: Equatable, Sendable {
    public var id: String
    public var name: String
    /// The project's base, which the branch merges into.
    public var base: String?
    public var ahead = 0
    /// Commits not pushed to its upstream; nil with no upstream.
    public var unpushed: Int?
    public var uncommitted = 0
    public var added = 0
    public var deleted = 0
    public var pr: PRInfo?
    /// The branch is checked out in a worktree.
    public var checkedOut = false
    /// Merged: its pull request says so, or Merge All merged it.
    public var merged = false

    public init(id: String, name: String) {
        self.id = id
        self.name = name
    }

    /// "into main · ↑4 · 2 uncommitted": its card's line under the name.
    public var line: String {
        var out = [base.map { "into \($0)" } ?? "into its base"]
        if ahead > 0 { out.append("↑\(ahead)") }
        if merged {
            out.append("merged")
        } else if !checkedOut {
            out.append("not checked out")
        } else {
            out.append(uncommitted == 0 ? "clean" : "\(uncommitted) uncommitted")
        }
        return out.joined(separator: " · ")
    }

    /// What its card's footer says of its pushing.
    public var pushLine: String {
        guard !merged else { return "merged" }
        switch unpushed {
        case nil: return pr == nil ? "not pushed" : ""
        case 0: return "up to date with its upstream"
        case let n?: return "\(n) commit\(n == 1 ? "" : "s") not pushed"
        }
    }
}

extension Snapshot {
    /// A task's repos, in the order Merge All merges them (the task's own
    /// when it was never armed), those this snapshot no longer has left out.
    public func taskRepos(_ t: GroupTask) -> [TaskRepo] {
        let order = (t.merge?.order).flatMap { $0.isEmpty ? nil : $0 } ?? t.projects
        let merged = Set(t.merge?.merged ?? [])
        // A repo of the task the order leaves out still shows, last.
        return (order + t.projects.filter { !order.contains($0) }).compactMap { id -> TaskRepo? in
            guard let pr = project(id) else { return nil }
            var r = TaskRepo(id: id, name: pr.name)
            r.base = pr.base
            let b = pr.branches?.first { $0.name == t.branch }
            r.ahead = b?.baseAhead ?? 0
            r.unpushed = b?.upstream == nil ? nil : (b?.ahead ?? 0)
            r.pr = b?.pr
            if let wt = pr.worktrees?.first(where: { $0.branch == t.branch }) {
                r.checkedOut = true
                r.uncommitted = wt.status?.files ?? 0
                r.added = wt.status?.added ?? 0
                r.deleted = wt.status?.deleted ?? 0
            }
            r.merged = merged.contains(id) || r.pr?.state == "MERGED"
            return r
        }
    }
}

/// The task page's line under its name: its pull requests, their checks
/// and what is uncommitted — "2 of 3 pull requests open · checks: 1
/// passed, 1 running · 2 files uncommitted".
public func taskHeadline(_ repos: [TaskRepo]) -> String {
    guard !repos.isEmpty else { return "none of its repos are here" }
    let merged = repos.filter(\.merged).count
    if merged == repos.count { return "all \(repos.count) merged" }
    let open = repos.filter { $0.pr?.state == "OPEN" && !$0.merged }
    var out: [String] = []
    out.append(open.isEmpty ? "no pull requests yet" : "\(open.count) of \(repos.count) pull request\(repos.count == 1 ? "" : "s") open")
    if merged > 0 { out.append("\(merged) merged") }
    let pass = open.filter { $0.pr?.checks == "pass" }.count
    let fail = open.filter { $0.pr?.checks == "fail" }.count
    let running = open.filter { $0.pr?.checks == "pending" }.count
    let checks = [pass > 0 ? "\(pass) passed" : nil, fail > 0 ? "\(fail) failed" : nil, running > 0 ? "\(running) running" : nil].compactMap { $0 }
    if !checks.isEmpty { out.append("checks: " + checks.joined(separator: ", ")) }
    let files = repos.filter { !$0.merged }.reduce(0) { $0 + $1.uncommitted }
    if files > 0 { out.append("\(files) file\(files == 1 ? "" : "s") uncommitted") }
    return out.joined(separator: " · ")
}

/// What the task page's banner says of Merge All, and how: nil when it was
/// never armed.
public enum MergeTone: Equatable, Sendable { case waiting, held, merging, merged, stopped }

public func mergeBanner(_ m: GroupTaskMerge?, repos: [TaskRepo]) -> (text: String, tone: MergeTone)? {
    guard let m else { return nil }
    let done = repos.filter(\.merged).count
    let n = repos.count
    switch m.state {
    case "merged": return ("All \(n) merged.", .merged)
    case "stopped": return ("Merge All stopped" + (m.why.map { ": \($0)." } ?? "."), .stopped)
    case "merging":
        if let why = m.why, !why.isEmpty { return ("Merging, \(done) of \(n) merged — waiting: \(why).", .held) }
        let next = repos.first { !$0.merged }
        return ("Merging — \(done) of \(n) merged" + (next.map { "; \($0.name)" + ($0.pr.map { " #\($0.number)" } ?? "") + " next." } ?? "."), .merging)
    default:
        if let why = m.why, !why.isEmpty { return ("Waiting: \(why), so nothing is merged yet.", .held) }
        return ("Armed: merges all \(n) in order once every pull request is green.", .waiting)
    }
}
