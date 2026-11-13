import Foundation

// A project's Sessions row shows how many conversations its agents saved,
// and is there only when that is more than none. The app counted once per
// connection, so a conversation saved later — an agent's first in a
// project, or anything in a project added since — never made the row
// appear until the app connected again. Seen: a task's two Claude
// conversations, saved minutes apart, and no Sessions row. So a project is
// counted again when something there may have saved one: it was added, an
// agent in it stopped working or ended. Per project, and a burst — an
// agent flickering between working and idle — counts once.

/// The projects (key(machine, id)) whose saved conversations are to be
/// counted again, and when.
public struct SessionRecounts: Equatable, Sendable {
    /// How long a project is left after the last ask before it is counted:
    /// an agent's state changes come in bursts.
    public static let settle: TimeInterval = 3
    /// The longest a project waits however often it is asked about, so a
    /// busy one is still counted now and then.
    public static let maxWait: TimeInterval = 15

    public private(set) var due: [String: Date] = [:]
    private var first: [String: Date] = [:]

    public init() {}

    /// Asks for project pk to be counted delay from now; asked again before
    /// then, it waits from the newest ask, but never past maxWait from the
    /// first.
    public mutating func note(_ pk: String, now: Date, after delay: TimeInterval = settle) {
        let start = first[pk] ?? now
        first[pk] = start
        due[pk] = min(now.addingTimeInterval(delay), start.addingTimeInterval(Self.maxWait))
    }

    /// The projects due by now, sorted, taken off the list.
    public mutating func take(now: Date) -> [String] {
        let ready = due.filter { $0.value <= now }.map(\.key).sorted()
        for pk in ready {
            due.removeValue(forKey: pk)
            first.removeValue(forKey: pk)
        }
        return ready
    }

    /// When the next project is due; nil when none is waiting.
    public var next: Date? { due.values.min() }

    /// A project forgotten: removed, or its machine gone.
    public mutating func drop(_ pk: String) {
        due.removeValue(forKey: pk)
        first.removeValue(forKey: pk)
    }
}

/// Whether a pane changing from old to new may have saved a conversation in
/// its project: an agent that stops working (idle, done, waiting on the
/// person), or a pane with an agent that ends. Frames and every other
/// change say nothing about sessions.
public func sessionsMayHaveChanged(old: PaneInfo?, new: PaneInfo) -> Bool {
    guard new.projectID != nil, new.agent != nil || old?.agent != nil else { return false }
    if new.state == "exited" { return old?.state != "exited" }
    guard let was = old?.agent?.state, let now = new.agent?.state else { return false }
    return was == .working && now != .working
}

/// Whether a project update may change how many conversations it has: a
/// project not known before, or one whose worktrees changed. A project is
/// announced before its worktrees are read, and a task branch's agent
/// saves its conversations in its worktree's folder, so counting only on
/// the first announcement counted none of them — the Sessions row never
/// came for a project whose agents worked on task branches.
public func sessionsMayHaveChanged(old: ProjectInfo?, new: ProjectInfo) -> Bool {
    guard let old else { return true }
    let paths = { (p: ProjectInfo) in Set((p.worktrees ?? []).map(\.path)) }
    return paths(old) != paths(new)
}

/// How many conversations the sidebar asks for when it counts a project's.
public let sessionCountLimit = 500

/// The count a project's Sessions row takes from a list its Sessions view
/// read (listed of at most limit, partial when an agent had not answered):
/// a whole list is the count, so the row follows a conversation saved or
/// deleted since; a list cut at its limit says only that there are at
/// least that many; a partial one says nothing (nil).
public func sessionCount(listed: Int, partial: Bool, limit: Int, known: Int?) -> Int? {
    if partial { return nil }
    if listed < limit { return listed }
    return max(known ?? 0, listed)
}
