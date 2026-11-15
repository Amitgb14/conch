import Foundation

/// Settings → Git → Old branches: a project's local branches nobody has
/// committed to for a while (branch.stale), and deleting the ones ticked
/// (branch.prune, branch.prune.v1). What may be ticked and what each row
/// says are decided here, so they can be tested without a window.

/// An old local branch and what deleting it would lose (proto.StaleBranch).
public struct StaleBranch: Decodable, Equatable, Sendable, Identifiable {
    public var name: String
    public var committed: Date?
    public var subject: String?
    public var upstream: String?
    /// The base branch, never deleted.
    public var base: Bool?
    /// Where it is checked out: it goes with that worktree, not alone.
    public var worktree: String?
    public var merged: Bool?
    public var gone: Bool?
    /// Its commits neither the base nor its upstream has: what deleting it loses.
    public var unmerged: Int?
    /// It can go without losing anything.
    public var suggested: Bool?

    public var id: String { name }

    public init(name: String, committed: Date? = nil, upstream: String? = nil, base: Bool? = nil, worktree: String? = nil,
                merged: Bool? = nil, unmerged: Int? = nil, suggested: Bool? = nil) {
        self.name = name
        self.committed = committed
        self.upstream = upstream
        self.base = base
        self.worktree = worktree
        self.merged = merged
        self.unmerged = unmerged
        self.suggested = suggested
    }
}

/// branch.stale's answer: the age asked for and the branches, oldest first.
public struct BranchStale: Decodable, Equatable, Sendable {
    public var days: Int
    public var branches: [StaleBranch]
}

/// branch.prune's answer; a failure's path is the branch's name.
public struct BranchPruneResult: Decodable, Equatable, Sendable {
    public struct Failure: Decodable, Equatable, Sendable {
        public var path: String
        public var error: String
    }
    public var deleted: [String]?
    public var failed: [Failure]?
}

public enum OldBranches {
    /// The capability a machine's server needs for any of this.
    public static let capability = "branch.prune.v1"

    /// A branch that may be ticked at all: never the base, never one
    /// checked out, since the server refuses both.
    public static func deletable(_ b: StaleBranch) -> Bool { b.base != true && b.worktree == nil }

    /// Ticked when the list arrives: the ones that lose nothing. One that
    /// would lose commits is offered, unticked, for the person to choose.
    public static func initialTicks(_ bs: [StaleBranch]) -> Set<String> {
        Set(bs.filter { $0.suggested == true && deletable($0) }.map(\.name))
    }

    /// What its row says after the age, and whether that is a warning.
    public static func state(_ b: StaleBranch) -> (text: String, warn: Bool) {
        if b.base == true { return ("base branch · kept", false) }
        if let wt = b.worktree { return ("checked out in \(abbreviate(wt)) · remove its worktree", false) }
        if let n = b.unmerged, n > 0 { return ("\(n) commit\(n == 1 ? "" : "s") not merged or pushed · lost if deleted", true) }
        if b.merged == true { return ("merged", false) }
        if let up = b.upstream, !up.isEmpty, b.gone != true { return ("pushed to \(up)", false) }
        return ("nothing of its own", false)
    }

    /// "90 days ago", in the unit the setting is in.
    public static func age(_ date: Date?, now: Date = Date()) -> String {
        guard let date else { return "" }
        let d = max(0, Int(now.timeIntervalSince(date) / 86400))
        return d == 1 ? "1 day ago" : "\(d) days ago"
    }

    /// The days to ask for: the setting, or 30 when it is unset or 0.
    public static func days(_ setting: Double?) -> Int {
        guard let s = setting, s >= 1 else { return 30 }
        return Int(s)
    }

    /// The confirmation's text for the branches ticked: how many, and how
    /// many commits deleting them loses.
    public static func confirm(_ ticked: [StaleBranch]) -> (title: String, detail: String, losing: Bool) {
        let n = ticked.count
        let title = "Delete \(n) branch\(n == 1 ? "" : "es")?"
        let lost = ticked.reduce(0) { $0 + ($1.unmerged ?? 0) }
        if lost > 0 {
            let who = ticked.filter { ($0.unmerged ?? 0) > 0 }.map(\.name).joined(separator: ", ")
            return (title, "\(who) \(lost == 1 ? "has a commit" : "have \(lost) commits") that are not merged or pushed anywhere: deleting loses them.", true)
        }
        return (title, "Nothing is lost: their work is merged or pushed. Only the local branches go.", false)
    }

    static func abbreviate(_ path: String) -> String {
        let home = NSHomeDirectory()
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}
