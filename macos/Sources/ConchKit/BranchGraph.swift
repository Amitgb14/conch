import Foundation

// What the commit graph says about the agents' branches: which commits are
// whose, what each branch needs from the person, and what an agent is
// asked when its branch conflicts with the base.

/// The local branches pointing at commits of a history, each at its newest:
/// "HEAD -> main" is main; a remote's branch and a tag are not branches.
public func branchTips(_ commits: [LogCommit], remotes: Set<String> = ["origin", "upstream"]) -> [String: String] {
    var tips: [String: String] = [:]
    for c in commits {
        for r in CommitRef.parse(c.refs, remotes: remotes) where (r.kind == .head || r.kind == .branch) && r.name != "HEAD" {
            if tips[r.name] == nil { tips[r.name] = c.hash }
        }
    }
    return tips
}

/// Which branch each commit is on, for the graph's agent column: a
/// branch's commits are those its first parents reach before reaching the
/// base. The base's own history, and what was merged into it, is nobody's
/// branch. A branch stacked on another keeps only its own commits, since
/// the branch nearer the top claims first.
public func commitOwners(_ commits: [LogCommit], base: String, remotes: Set<String> = ["origin", "upstream"]) -> [String: String] {
    let byHash = Dictionary(commits.map { ($0.hash, $0) }, uniquingKeysWith: { a, _ in a })
    let tips = branchTips(commits, remotes: remotes)
    // Everything the base reaches, by any parent.
    var onBase = Set<String>()
    if let t = tips[base] {
        var stack = [t]
        while let h = stack.popLast() {
            guard onBase.insert(h).inserted, let c = byHash[h] else { continue }
            stack.append(contentsOf: c.parents ?? [])
        }
    }
    // Newest tip first, so a stacked branch claims its commits before the
    // one beneath it walks through them.
    let order = Dictionary(commits.enumerated().map { ($1.hash, $0) }, uniquingKeysWith: { a, _ in a })
    var owners: [String: String] = [:]
    let tipHashes = Set(tips.filter { $0.key != base }.values)
    for (name, tip) in tips.sorted(by: { (order[$0.value] ?? 0, $0.key) < (order[$1.value] ?? 0, $1.key) }) where name != base {
        var h: String? = tip
        // Another branch's tip is where this one was stacked on it.
        while let cur = h, !onBase.contains(cur), owners[cur] == nil, cur == tip || !tipHashes.contains(cur), let c = byHash[cur] {
            owners[cur] = name
            h = c.parents?.first
        }
    }
    return owners
}

/// What an agent's branch needs, most urgent first.
public enum GraphBranchState: Equatable, Sendable {
    /// Its worktree stopped mid-rebase or merge with files to resolve.
    case conflicted(Int)
    /// The agent asked to resolve its conflict stopped, the conflict still
    /// there — or ended, its pane gone.
    case resolveStopped(ended: Bool)
    /// The agent asked to resolve its conflict is waiting for the person.
    case resolveWaiting
    /// Rebased onto the base — a conflict resolved — and not pushed: the
    /// remote still has the old commits.
    case rebased
    /// Its agent is waiting for the person.
    case waiting
    /// Its pull request's checks failed.
    case ciFailing
    /// Merging it into the base would conflict (git merge-tree), though
    /// nothing has moved yet.
    case willConflict
    /// An agent is resolving its conflict with the base.
    case resolving
    case working
    case done
    case idle

    /// Sorts the side's list: lower comes first.
    public var urgency: Int {
        switch self {
        case .conflicted: 0
        case .resolveStopped: 1
        case .resolveWaiting: 2
        case .rebased: 3
        case .waiting: 4
        case .ciFailing: 5
        case .willConflict: 6
        case .resolving: 7
        case .working: 8
        case .done: 9
        case .idle: 10
        }
    }

    /// Whether the branch conflicts with the base: stuck, coming, or in
    /// an agent's hands.
    public var conflicts: Bool {
        switch self {
        case .conflicted, .willConflict, .resolving, .resolveWaiting, .resolveStopped: true
        default: false
        }
    }

    /// Whether an agent is, or was, asked to resolve it: the banner and
    /// card say how that is going rather than offer to ask.
    public var resolution: Bool {
        switch self {
        case .resolving, .resolveWaiting, .resolveStopped: true
        default: false
        }
    }
}

/// An agent's branch as the graph's side lists it.
public struct GraphBranch: Equatable, Sendable {
    public var name: String
    /// The agent working on it, if one is running.
    public var agent: PaneInfo?
    public var state: GraphBranchState
    /// Commits ahead of and behind the base.
    public var ahead: Int
    public var behind: Int
    /// The files merging it into the base would conflict in.
    public var conflicts: [String]
    public var pr: PRInfo?
    /// Its rebase not yet pushed (branch.rebased).
    public var rebase: BranchRebase?
    /// The files its worktree, stopped mid-rebase or merge, still has to
    /// resolve.
    public var stoppedFiles: Int = 0
    /// A rebase is unfinished in its worktree.
    public var midRebase: Bool = false
    /// Who was asked to resolve its conflict (branch.resolving), and that
    /// agent's pane while it is there — or, from a server that keeps no
    /// asks, the agent working in a worktree stopped mid-rebase.
    public var ask: ResolveAsk?
    public var resolver: PaneInfo?

    /// Whether its worktree stopped mid-rebase or merge on a conflict.
    public var stopped: Bool { stoppedFiles > 0 || midRebase }
}

/// How long an agent just asked has to start before not working means it
/// stopped.
public let resolveStartGrace: TimeInterval = 30

/// The project's agent branches — every branch checked out in a worktree
/// of its own, never the base or the main checkout — with the state each
/// is in, most urgent first and otherwise as the worktrees are listed.
/// Rebased is the server's list of branches rebased and not pushed, by
/// name.
public func graphBranches(_ project: ProjectInfo, panes: [PaneInfo], rebased: [String: BranchRebase] = [:], now: Date = Date()) -> [GraphBranch] {
    let base = project.base ?? ""
    let mainBranches = Set((project.worktrees ?? []).filter { $0.main == true }.compactMap(\.branch))
    let info = Dictionary((project.branches ?? []).map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
    var out: [GraphBranch] = []
    for w in project.worktrees ?? [] where w.main != true {
        // Mid-rebase git names no branch; the server says which it rewrites.
        guard let name = w.branch ?? w.rebasing, !name.isEmpty, name != base, !mainBranches.contains(name),
              !out.contains(where: { $0.name == name }) else { continue }
        let b = info[name]
        // An agent whose branch is blank (its HEAD detached mid-rebase) is
        // found by the folder it works in.
        let inFolder = panes.filter { p in
            p.agent != nil && p.running && p.projectID == project.id && (p.branch ?? "").isEmpty
                && (p.cwd == w.path || (p.cwd ?? "").hasPrefix(w.path + "/"))
        }
        let agent = branchAgent(branchAgents(panes, project: project.id, branch: name) + inFolder, handedTo: nil)
        let pr = b?.pr.flatMap { ($0.state ?? "OPEN") == "OPEN" ? $0 : nil }
        let stoppedFiles = max(w.status?.conflicts ?? 0, 0)
        let midRebase = w.rebasing != nil && w.branch == nil
        let stopped = stoppedFiles > 0 || midRebase
        let conflicting = stopped || !(w.conflicts ?? []).isEmpty
        // The agent at its conflict: the one asked, while its pane is
        // there; else, from a server keeping no asks, one busy in a
        // worktree stopped mid-rebase — nothing else is done there.
        var resolver: PaneInfo?
        var resolve: GraphBranchState?
        if let ask = w.resolve, conflicting {
            let p = panes.first { $0.id == ask.pane }
            resolver = p
            if let p, p.running, p.agent?.state == .working {
                resolve = .resolving
            } else if let p, p.running, p.agent?.state == .blocked {
                resolve = .resolveWaiting
            } else if let p, p.running, let at = ask.at, now.timeIntervalSince(at) < resolveStartGrace {
                resolve = .resolving
            } else {
                resolve = .resolveStopped(ended: !(p?.running ?? false))
            }
        } else if w.resolve == nil, stopped, let a = agent, a.agent?.state == .working || a.agent?.state == .blocked {
            resolver = a
            resolve = a.agent?.state == .working ? .resolving : .resolveWaiting
        }
        let state: GraphBranchState
        if let resolve {
            state = resolve
        } else if stoppedFiles > 0 {
            state = .conflicted(stoppedFiles)
        } else if rebased[name] != nil {
            state = .rebased
        } else if agent?.agent?.state == .blocked {
            state = .waiting
        } else if pr?.checks == "fail" {
            state = .ciFailing
        } else if !(w.conflicts ?? []).isEmpty {
            state = .willConflict
        } else if agent?.agent?.state == .working {
            state = .working
        } else if agent?.agent?.state == .done {
            state = .done
        } else {
            state = .idle
        }
        out.append(GraphBranch(name: name, agent: agent, state: state, ahead: b?.baseAhead ?? 0, behind: b?.baseBehind ?? 0,
                               conflicts: w.conflicts ?? [], pr: pr, rebase: rebased[name], stoppedFiles: stoppedFiles,
                               midRebase: midRebase, ask: conflicting ? w.resolve : nil, resolver: resolver))
    }
    return out.enumerated().sorted { ($0.element.state.urgency, $0.offset) < ($1.element.state.urgency, $1.offset) }.map(\.element)
}

/// What an agent is asked when its branch conflicts with the base: finish
/// the rebase that stopped, or bring the branch up to date before the
/// conflict reaches a pull request. Tests are run; pushing is left to the
/// person or the autopilot.
public func resolvePrompt(branch: String, base: String, files: [String], stopped: Bool) -> String {
    let shown = files.prefix(8).joined(separator: ", ")
    let rest = files.count > 8 ? " and \(files.count - 8) more" : ""
    let which = files.isEmpty ? "the conflicting files (git status lists them)" : shown + rest
    if stopped {
        return "The rebase of \(branch) onto \(base) stopped on conflicts in \(which). Resolve each one, keeping what both sides meant, "
            + "run the tests that cover those files, then git add them and git rebase --continue. Say what you kept from each side."
    }
    return "Rebase \(branch) onto \(base): it will conflict in \(which). Resolve each conflict, keeping what both sides meant, "
        + "run the tests that cover those files, and say what you kept from each side."
}

/// One of a pull request's checks, as a mark on the commit it ran on.
public struct CIMark: Equatable, Sendable {
    public var name: String
    /// pass, fail or pending.
    public var state: String
    public var url: String?
}

/// A pull request's checks as marks: failures first, then those still
/// running, then passes, a handful at most so a row stays a row.
public func ciMarks(_ pr: PRInfo, limit: Int = 8) -> [CIMark] {
    let rank = ["fail": 0, "pending": 1, "pass": 2]
    let runs = (pr.runs ?? []).enumerated().sorted {
        (rank[$0.element.state] ?? 3, $0.offset) < (rank[$1.element.state] ?? 3, $1.offset)
    }
    return runs.prefix(limit).map { CIMark(name: $0.element.name, state: $0.element.state, url: $0.element.url) }
}

/// How a repository stores itself (project.log's format): git 3 makes new
/// ones SHA-256 with reftable refs.
public struct RepoFormat: Decodable, Equatable, Sendable {
    public var objects: String?
    public var refs: String?

    public init(objects: String?, refs: String?) {
        self.objects = objects
        self.refs = refs
    }

    public var git3: Bool { objects == "sha256" }

    /// "SHA-256 · reftable", or "" when the server did not say.
    public var label: String {
        guard let o = objects, !o.isEmpty else { return "" }
        let hash = o == "sha256" ? "SHA-256" : o == "sha1" ? "SHA-1" : o
        return refs.map { "\(hash) · \($0)" } ?? hash
    }
}

/// The agent resolving a branch's conflict, by name: "Claude".
public func resolverName(_ b: GraphBranch) -> String? {
    guard let p = b.resolver else { return nil }
    return p.agent?.name.capitalized ?? p.displayName
}

/// The banner's line on a branch whose conflict is in an agent's hands.
public func resolveHeadline(_ b: GraphBranch, base: String) -> String {
    let who = resolverName(b) ?? "The agent"
    switch b.state {
    case .resolving: return "\(who) is resolving the conflict with \(base)"
    case .resolveWaiting: return "\(who) is waiting on you while resolving \(b.name)"
    case .resolveStopped(ended: true): return "The agent asked to resolve \(b.name) has ended; it still conflicts with \(base)"
    case .resolveStopped(ended: false): return "\(who) stopped; \(b.name) still conflicts with \(base)"
    default: return "\(b.name) conflicts with \(base)"
    }
}

/// How far it is and who asked when: "2 of 3 files left · you asked 4m
/// ago".
public func resolveDetail(_ b: GraphBranch, now: Date = Date()) -> String {
    var parts: [String] = []
    let asked = b.ask?.files?.count ?? 0
    if b.stoppedFiles > 0 {
        let n = b.stoppedFiles
        parts.append(asked >= n ? "\(n) of \(asked) files left" : "\(n) file\(n == 1 ? "" : "s") left")
    } else if b.midRebase {
        parts.append("files resolved, the rebase not finished")
    } else {
        parts.append("not rebased yet")
    }
    if let ask = b.ask, let at = ask.at {
        let who = ask.by == "autopilot" ? "the autopilot" : "you"
        parts.append("\(who) asked \(agoText(at, now: now))")
    }
    return parts.joined(separator: " · ")
}


/// The branch the conflict banner is about: one stopped on a conflict, or
/// whose conflict is in an agent's hands — the most urgent, as the list is
/// sorted. nil when there is none, and the rebased banner may show.
public func conflictBannerBranch(_ bs: [GraphBranch]) -> GraphBranch? {
    bs.first { b in
        if case .conflicted = b.state { return true }
        return b.state.resolution
    }
}

/// Where branch is checked out — mid-rebase too, when git names no
/// branch and the server says which one the rebase rewrites.
public func branchWorktree(_ project: ProjectInfo, _ branch: String) -> WorktreeInfo? {
    (project.worktrees ?? []).first { $0.branch == branch } ?? (project.worktrees ?? []).first { $0.branch == nil && $0.rebasing == branch }
}
