import Foundation

// A branch rebased and not pushed yet (branch.rebased): an agent asked to
// resolve a conflict with the base leaves one. The remote still has the
// old commits, so before it goes up the person can see what resolving
// the conflict changed, push it over the old with a lease, or undo it.

public struct BranchRebased: Decodable, Sendable {
    public var rebases: [BranchRebase]?
}

public struct BranchRebase: Decodable, Equatable, Sendable {
    public var branch: String
    public var before: String
    public var after: String
    public var onto: String
    public var at: Date?
    public var commits: [RebasedCommit]?
    /// What the remote has for the branch, as the checkout last saw it.
    public var remote: String?
    /// git range-diff's account, when asked for.
    public var diff: String?

    /// How a push of the rewrite would go.
    public enum Push: Equatable, Sendable {
        /// Never pushed: a plain push.
        case fresh
        /// Over the old branch the remote still has, and only that.
        case over(String)
        /// The remote has something else: somebody pushed since.
        case moved(String)
    }

    public var push: Push {
        guard let r = remote, !r.isEmpty else { return .fresh }
        return r == before ? .over(r) : .moved(r)
    }

    public var changed: Int { (commits ?? []).filter { $0.change == "changed" }.count }
    public var dropped: Int { (commits ?? []).filter { $0.change == "dropped" }.count }

    /// "3 commits rewritten · 1 changed resolving the conflict · 1 dropped".
    public var summary: String {
        let cs = commits ?? []
        guard !cs.isEmpty else { return "Nothing to compare" }
        let n = cs.filter { $0.change != "added" }.count
        var parts = ["\(n) commit\(n == 1 ? "" : "s") rewritten"]
        if changed > 0 { parts.append("\(changed) changed resolving the conflict") }
        if dropped > 0 { parts.append("\(dropped) dropped") }
        let added = cs.count - n
        if added > 0 { parts.append("\(added) added") }
        return parts.joined(separator: " · ")
    }
}

public struct RebasedCommit: Decodable, Equatable, Sendable {
    public var old: String?
    public var new: String?
    public var subject: String
    /// same, changed, dropped or added.
    public var change: String
}

/// A line of git range-diff's account.
public struct RangeDiffLine: Equatable, Sendable, Identifiable {
    public enum Kind: Equatable, Sendable {
        /// "1:  abc ! 1:  def subject": a commit before and after.
        case commit
        /// A section of the diff of diffs.
        case hunk
        /// What the rewrite added to the commit's change, or took from it.
        case added, removed
        case context
    }

    public var id: Int
    public var kind: Kind
    public var text: String
}

/// Reads git range-diff: a commit pair's line stands alone; beneath a
/// changed one, each line is indented four, its first character saying
/// whether the rewrite added it to the commit's diff or took it away.
public func rangeDiffLines(_ text: String) -> [RangeDiffLine] {
    var out: [RangeDiffLine] = []
    for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
        let line = String(raw)
        if line.isEmpty && out.isEmpty { continue }
        let kind: RangeDiffLine.Kind
        var body = line
        if line.hasPrefix("    ") {
            body = String(line.dropFirst(4))
            switch body.first {
            case "+": kind = .added
            case "-": kind = .removed
            case "@": kind = .hunk
            default: kind = .context
            }
        } else if line.isEmpty {
            kind = .context
        } else {
            kind = .commit
        }
        out.append(RangeDiffLine(id: out.count, kind: kind, text: body))
    }
    while out.last?.text.isEmpty == true { out.removeLast() }
    return out
}
