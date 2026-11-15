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
    /// Its agent is waiting for the person.
    case waiting
    /// Its pull request's checks failed.
    case ciFailing
    /// Merging it into the base would conflict (git merge-tree), though
    /// nothing has moved yet.
    case willConflict
    case working
    case done
    case idle

    /// Sorts the side's list: lower comes first.
    public var urgency: Int {
        switch self {
        case .conflicted: 0
        case .waiting: 1
        case .ciFailing: 2
        case .willConflict: 3
        case .working: 4
        case .done: 5
        case .idle: 6
        }
    }

    /// Whether an agent should be asked to resolve a conflict.
    public var conflicts: Bool {
        switch self {
        case .conflicted, .willConflict: true
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
}

/// The project's agent branches — every branch checked out in a worktree
/// of its own, never the base or the main checkout — with the state each
/// is in, most urgent first and otherwise as the worktrees are listed.
public func graphBranches(_ project: ProjectInfo, panes: [PaneInfo]) -> [GraphBranch] {
    let base = project.base ?? ""
    let mainBranches = Set((project.worktrees ?? []).filter { $0.main == true }.compactMap(\.branch))
    let info = Dictionary((project.branches ?? []).map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
    var out: [GraphBranch] = []
    for w in project.worktrees ?? [] where w.main != true {
        guard let name = w.branch, !name.isEmpty, name != base, !mainBranches.contains(name),
              !out.contains(where: { $0.name == name }) else { continue }
        let b = info[name]
        let agent = branchAgent(branchAgents(panes, project: project.id, branch: name), handedTo: nil)
        let pr = b?.pr.flatMap { ($0.state ?? "OPEN") == "OPEN" ? $0 : nil }
        let state: GraphBranchState
        if let n = w.status?.conflicts, n > 0 {
            state = .conflicted(n)
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
                               conflicts: w.conflicts ?? [], pr: pr))
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
