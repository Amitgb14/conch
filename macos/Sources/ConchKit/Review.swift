import Foundation

// The TUI's review queue (internal/tui/queue.go), counted for the status
// bar's "N to review": what needs a decision, across every machine — an
// agent waiting or finished, a branch whose pull request's CI failed, one
// with commits nobody has pushed or merged, one with uncommitted work and
// no agent on it. The TUI also puts a project's own check and the server's
// scheduled tasks there, which this app does not hold; those are left out.

public struct ReviewItem: Equatable, Sendable {
    /// Why it is here, most urgent first, as the TUI's bands.
    public enum Band: Int, Sendable, Comparable {
        case waiting, failed, done, unshipped, dirty
        public static func < (a: Band, b: Band) -> Bool { a.rawValue < b.rawValue }
    }
    public var band: Band
    public var machine: String
    public var project: String // its id
    public var projectName: String
    public var branch: String
    /// The agent's pane, when an agent is what it is about.
    public var pane: String
    public var detail: String
    public var since: Date?

    /// The TUI's key for the row (queueItem.key), which dismissals use.
    public var key: String { "\(machine)|\(project)|\(branch)|\(pane)" }
}

/// Branches untouched for longer, and checked out nowhere, are no longer
/// a decision owed today (recentBranchAge).
let recentBranchAge: TimeInterval = 14 * 24 * 3600

func plural(_ n: Int, _ what: String) -> String { "\(n) \(what)\(n == 1 ? "" : "s")" }

/// What needs a decision, most urgent first and, within a band, what has
/// waited longest. machines are those online, each with its snapshot.
/// dismissed is ui.json's queue_dismissed: a row dismissed stays away
/// until its band or the time it started changes, as the TUI keeps it.
public func reviewQueue(_ machines: [(id: String, snapshot: Snapshot)], dismissed: [String: String] = [:], now: Date = Date()) -> [ReviewItem] {
    var items: [ReviewItem] = []
    for (mid, snap) in machines {
        var seen: Set<String> = []
        func ciFailed(_ project: String, _ branch: String) -> PRInfo? {
            guard let pr = snap.project(project)?.branches?.first(where: { $0.name == branch })?.pr,
                  pr.state == "OPEN", pr.checks == "fail" else { return nil }
            return pr
        }
        for p in snap.panes {
            guard let a = p.agent, a.state == .blocked || a.state == .done else { continue }
            var band: ReviewItem.Band = a.state == .blocked ? .waiting : .done
            var detail = a.state == .blocked ? "waiting for an answer" : "finished"
            if a.failed == true { detail = "stopped with an error" }
            let pid = p.projectID ?? "", branch = p.branch ?? ""
            if !branch.isEmpty {
                seen.insert(pid + "|" + branch)
                if let pr = ciFailed(pid, branch) {
                    if band > .failed { band = .failed }
                    detail += " · CI failed on #\(pr.number)"
                }
            }
            items.append(ReviewItem(band: band, machine: mid, project: pid, projectName: snap.project(pid)?.name ?? "",
                                    branch: branch, pane: p.id, detail: detail, since: a.since ?? p.created))
        }
        for proj in snap.projects {
            var dirty: [String: Int] = [:]
            for w in proj.worktrees ?? [] { if let b = w.branch, let f = w.status?.files, f > 0 { dirty[b] = f } }
            for b in proj.branches ?? [] where b.name != proj.base && !seen.contains(proj.id + "|" + b.name) {
                if b.worktree == nil, let c = b.committed, now.timeIntervalSince(c) > recentBranchAge { continue }
                let ahead = b.ahead ?? 0, baseAhead = b.baseAhead ?? 0, up = b.upstream ?? ""
                var band: ReviewItem.Band, detail: String
                if let pr = b.pr, pr.state == "OPEN", pr.checks == "fail" {
                    (band, detail) = (.failed, "CI failed on #\(pr.number)")
                } else if let pr = b.pr, pr.state == "OPEN", ahead == 0 {
                    continue // somebody else's move
                } else if baseAhead > 0 && (up.isEmpty || ahead > 0) {
                    (band, detail) = (.unshipped, up.isEmpty ? plural(baseAhead, "commit") + " not pushed" : plural(ahead, "commit") + " ahead of " + up)
                } else if baseAhead > 0 {
                    (band, detail) = (.unshipped, plural(baseAhead, "commit") + " not merged into " + (proj.base ?? "the base"))
                } else if let f = dirty[b.name] {
                    (band, detail) = (.dirty, plural(f, "file") + " uncommitted")
                } else {
                    continue
                }
                items.append(ReviewItem(band: band, machine: mid, project: proj.id, projectName: proj.name,
                                        branch: b.name, pane: "", detail: detail, since: b.committed))
            }
        }
    }
    // Dismissed rows, until what they say changes. The TUI remembers the
    // band, the words and the time; the words are the TUI's to word, so
    // the band and the time are what is compared here.
    items = items.filter { it in
        guard let was = dismissed[it.key] else { return true }
        let parts = was.split(separator: "|", omittingEmptySubsequences: false)
        guard parts.count >= 3, let band = Int(parts[0]), let unix = Int(parts[parts.count - 1]) else { return true }
        return band != it.band.rawValue || unix != Int((it.since ?? .distantPast).timeIntervalSince1970)
    }
    return items.sorted {
        if $0.band != $1.band { return $0.band < $1.band }
        switch ($0.since, $1.since) {
        case let (a?, b?): return a < b
        case (nil, _?): return false // without a time, last
        case (_?, nil): return true
        default: return $0.key < $1.key
        }
    }
}

/// ui.json's queue_dismissed: the review queue rows dismissed in the TUI.
public func loadQueueDismissed(uiJSON data: Data?) -> [String: String] {
    struct UI: Decodable {
        var queue_dismissed: [String: String]?
    }
    guard let data, let ui = try? JSONDecoder().decode(UI.self, from: data) else { return [:] }
    return ui.queue_dismissed ?? [:]
}

extension ReviewItem {
    /// The state a dismissal is kept against, as the TUI's queueItem.state
    /// words it: the band, the words and when it started. The TUI compares
    /// all three, so a row this app words differently comes back there —
    /// shown again, never hidden by mistake.
    public var dismissState: String {
        "\(band.rawValue)|\(detail)|\(Int((since ?? .distantPast).timeIntervalSince1970))"
    }

    /// Whether its agent can be sent what failed: CI on its pull request.
    public var canFix: Bool { band == .failed || detail.contains("CI failed") }
}

/// ui.json with a review row dismissed, as the TUI's x keeps it
/// (queue_dismissed: the row's key and its state); the rest of the file
/// as it was.
public func dismissing(_ it: ReviewItem, in data: Data?) throws -> Data {
    var ui: [String: Any] = [:]
    if let data, !data.isEmpty {
        guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ConchError(code: "ui.json", message: "ui.json is not what conch writes; not changing it")
        }
        ui = obj
    }
    var seen = ui["queue_dismissed"] as? [String: Any] ?? [:]
    seen[it.key] = it.dismissState
    ui["queue_dismissed"] = seen
    return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
}
