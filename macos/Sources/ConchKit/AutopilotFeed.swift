import Foundation

// What the server's autopilot did and is set to do, for the commit
// graph's Autopilot panel: its notes (autopilot.activity) and its policy
// (autopilot.get).

public struct AutopilotNote: Decodable, Equatable, Sendable {
    public var projectID: String
    public var branch: String?
    public var text: String
    public var error: Bool?
    public var merged: Bool?
    /// ci, check, ship, merge, resolve, intake or triage; nil from an
    /// older server.
    public var kind: String?
    public var at: Date?
    /// The failed CI run it is about — its service, workflow and job, and
    /// what was read of it — from a server with autopilot.note.ci.v1.
    public var ci: NoteCI?

    enum CodingKeys: String, CodingKey {
        case branch, text, error, merged, kind, at, ci
        case projectID = "project_id"
    }

    /// The line a failed run leads with: "CI failed on B — CircleCI ·
    /// workflow W · job J"; else what the server said.
    public var headline: String { ci.map { $0.headline(branch: branch ?? "") } ?? text }

    public enum Tone: Equatable, Sendable { case done, failed, handed, info }

    /// How the feed marks it: a merge done, something that failed or was
    /// refused, a failure handed to an agent to fix, anything else.
    public var tone: Tone {
        if merged == true { return .done }
        if error == true { return .failed }
        if (kind == "ci" || kind == "check" || kind == "resolve") && text.contains("sent it to") { return .handed }
        if kind == "resolve" && text.contains(" to resolve") { return .handed }
        if kind == "review" && text.hasPrefix("sent ") { return .handed }
        return .info
    }

    public var kindLabel: String {
        switch kind {
        case "ci": "CI"
        case "check": "Check"
        case "ship": "Push"
        case "merge": "Merge"
        case "resolve": "Conflict"
        case "intake": "Intake"
        case "triage": "Triage"
        case "review": "Review"
        case "hook": "Hook"
        default: ""
        }
    }
}

public struct AutopilotActivity: Decodable, Sendable {
    public var notes: [AutopilotNote]
}

/// One day of the feed, newest first.
public struct ActivityDay: Equatable, Sendable {
    public var title: String
    public var notes: [AutopilotNote]
}

/// The notes grouped by the day they were made, in the person's calendar:
/// Today, Yesterday, then "Nov 10" — and notes an older server made
/// without a time, last, under Earlier.
public func activityDays(_ notes: [AutopilotNote], now: Date = Date(), calendar: Calendar = .current) -> [ActivityDay] {
    var days: [ActivityDay] = []
    var undated: [AutopilotNote] = []
    let f = DateFormatter()
    f.calendar = calendar
    f.timeZone = calendar.timeZone
    f.locale = Locale(identifier: "en_US_POSIX")
    f.dateFormat = "MMM d"
    for n in notes {
        guard let at = n.at else { undated.append(n); continue }
        let title: String
        if calendar.isDate(at, inSameDayAs: now) {
            title = "Today"
        } else if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(at, inSameDayAs: y) {
            title = "Yesterday"
        } else {
            title = f.string(from: at)
        }
        if days.last?.title == title { days[days.count - 1].notes.append(n) } else { days.append(ActivityDay(title: title, notes: [n])) }
    }
    if !undated.isEmpty { days.append(ActivityDay(title: "Earlier", notes: undated)) }
    return days
}

/// The server's autopilot settings (proto.AutopilotPolicy), as much of
/// them as the panel says.
public struct AutopilotPolicy: Decodable, Equatable, Sendable {
    public var autoFix: Int?
    public var autoMerge: Bool?
    public var autoPR: Bool?
    public var autoResolve: Int?
    public var requireTests: Bool?
    public var maxDiff: Int?
    public var checks: [String: String]?
    public var intake: [String: String]?
    public var triage: [String: String]?
    public var autoReview: Bool?
    public var resolveThreads: Bool?

    enum CodingKeys: String, CodingKey {
        case checks, intake, triage
        case autoFix = "auto_fix", autoMerge = "auto_merge", autoPR = "auto_pr", autoResolve = "auto_resolve"
        case requireTests = "require_tests", maxDiff = "max_diff"
        case autoReview = "auto_review", resolveThreads = "resolve_threads"
    }

    /// Whether it does anything on its own at all.
    public var anyOn: Bool {
        (autoFix ?? 0) > 0 || autoMerge == true || autoPR == true || (autoResolve ?? 0) > 0
            || !(checks ?? [:]).isEmpty || !(intake ?? [:]).isEmpty || !(triage ?? [:]).isEmpty
    }
}

/// A branch the autopilot is minding (proto.AutopilotBranch).
public struct AutopilotBranch: Decodable, Equatable, Sendable {
    public var projectID: String
    public var branch: String
    public var fixes: Int?
    public var waiting: Bool?
    public var held: String?
    public var checkFailed: Bool?
    public var checking: String?
    public var unshipped: String?
    public var conflicting: Bool?
    public var resolves: Int?

    enum CodingKeys: String, CodingKey {
        case branch, fixes, waiting, held, checking, unshipped, conflicting, resolves
        case projectID = "project_id", checkFailed = "check_failed"
    }

    /// What it is waiting on, in a few words.
    public var status: String {
        if conflicting == true { return "conflicts with its base" + ((resolves ?? 0) > 0 ? " · asked \(resolves ?? 0)×" : "") }
        if checking != nil { return "running the check" }
        if checkFailed == true { return "check failed" }
        if waiting == true { return held.map { "green, held: \($0)" } ?? "green, merging" }
        if let u = unshipped { return "not pushed: \(u)" }
        if (fixes ?? 0) > 0 { return "sent \(fixes ?? 0) failure\(fixes == 1 ? "" : "s") to fix" }
        return "watching"
    }
}

public struct AutopilotState: Decodable, Sendable {
    public var policy: AutopilotPolicy
    public var branches: [AutopilotBranch]?
}

/// One of the autopilot's rules as the panel says it.
public struct AutopilotRule: Equatable, Sendable {
    public var on: Bool
    public var text: String
}

/// The policy said rule by rule, for a project: what goes back to the
/// agents, what is pushed and merged, and this project's check, intake
/// label and triage label.
public func autopilotRules(_ p: AutopilotPolicy, project: String) -> [AutopilotRule] {
    let fix = p.autoFix ?? 0, resolve = p.autoResolve ?? 0
    var out = [
        AutopilotRule(on: fix > 0, text: fix > 0 ? "Failed CI and checks go back to the agent, \(fix) times at most" : "Failed CI and checks stay with you"),
        AutopilotRule(on: resolve > 0, text: resolve > 0 ? "Conflicts with the base go to the agent, \(resolve) times at most" : "Conflicts with the base stay with you"),
        // Review comments ride on auto_fix, as the server sends them.
        AutopilotRule(on: fix > 0 && p.autoReview == true,
                      text: fix > 0 && p.autoReview == true
                          ? "Review comments go to the agent" + (p.resolveThreads == true ? ", and threads its push answered are resolved" : "")
                          : "Review comments stay with you"),
        AutopilotRule(on: p.autoPR == true, text: "Push finished branches and open their pull requests"),
        AutopilotRule(on: p.autoMerge == true, text: "Merge pull requests when green"),
    ]
    if p.requireTests == true || (p.maxDiff ?? 0) > 0 {
        var why: [String] = []
        if p.requireTests == true { why.append("without a test") }
        if let m = p.maxDiff, m > 0 { why.append("over \(m) lines") }
        out.append(AutopilotRule(on: true, text: "Pull requests " + why.joined(separator: " or ") + " stay drafts"))
    }
    if let c = p.checks?[project], !c.isEmpty {
        out.append(AutopilotRule(on: true, text: "Check: \(c)"))
    } else {
        out.append(AutopilotRule(on: false, text: "No check for this project"))
    }
    if let l = p.intake?[project], !l.isEmpty {
        out.append(AutopilotRule(on: true, text: "Issues with the label \(l) become queued tasks"))
    }
    if let l = p.triage?[project], !l.isEmpty {
        out.append(AutopilotRule(on: true, text: "Triage issues labelled \(l)"))
    } else {
        out.append(AutopilotRule(on: false, text: "Triage: off for this project"))
    }
    return out
}
