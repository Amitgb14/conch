import Foundation

// A project that is a plain folder — an empty folder added as a project —
// has no branch for a task to work on, and the server refused the task
// with "is not a git repository". Nothing offered the way out but a
// terminal: `git init` and a first commit by hand. Now the app asks before
// the task starts and makes it a repository (project.git_init), then
// carries on with what was asked for.

/// The capability a server answers project.git_init with.
public let gitInitCapability = "project.git_init.v1"

/// What to ask before a task that needs a branch in projects some of which
/// are plain folders.
public struct GitInitQuestion: Equatable, Sendable {
    public var title: String
    public var info: String
    /// The plain folders, by project id, in the order given: each made a
    /// repository on Initialize.
    public var projects: [String]
    /// Whether the agent could instead work in the folder itself, with no
    /// branch: a task in one project can; a task across a group cannot.
    public var inFolder: Bool
}

/// What starting work needing a branch should do first.
public enum BranchReadiness: Equatable, Sendable {
    /// Every project is a repository (or could not say): go on.
    case ready
    /// Some are plain folders: ask, and make them repositories on yes.
    case ask(GitInitQuestion)
    /// Some are plain folders, and the server predates making them
    /// repositories; why, to say.
    case reloadServer(String)
}

/// Whether a task needing a branch in each of projects can start as it is,
/// on a machine (named machineName) whose server said capabilities.
/// inFolder offers the agent the folder itself instead, for a task in one
/// project. A project whose git is unknown (nil) is taken as a repository:
/// its server then refuses with its own reason.
public func branchReadiness(_ projects: [ProjectInfo], capabilities: [String]?, machineName: String, inFolder: Bool = false) -> BranchReadiness {
    let plain = projects.filter { $0.git == false }
    guard !plain.isEmpty else { return .ready }
    let names = plain.map(\.name)
    let named = namesListed(names)
    let are = plain.count == 1 ? "isn't a git repository" : "aren't git repositories"
    guard capabilities?.contains(gitInitCapability) == true else {
        return .reloadServer("\(named) \(are) yet, and the server on \(machineName) predates making one: reload the server, or run git init and commit once there yourself")
    }
    let one = projects.count == 1
    var info = (one ? "A task works on a branch of its own, which needs git."
                    : "A task across repos works on a branch in each, which needs git.")
        + " Initialize git \(plain.count == 1 ? "here" : "in each")? conch runs git init and makes an empty first commit; the files already there stay as they are, not committed."
    let folder = inFolder && one
    if folder { info += "\n\nOr start the agent in the folder itself, with no branch or worktree." }
    return .ask(GitInitQuestion(title: "\(named) \(are) yet", info: info, projects: plain.map(\.id), inFolder: folder))
}

/// The row menu's Initialize Git… against a server that may predate it:
/// nil to go on, else why not, to say.
public func gitInitRefusal(_ project: ProjectInfo, capabilities: [String]?, machineName: String) -> String? {
    if project.git == true { return "\(project.name) is already a git repository" }
    guard capabilities?.contains(gitInitCapability) == true else {
        return "The server on \(machineName) predates making a project a git repository: reload the server, or run git init there yourself"
    }
    return nil
}

/// "a", "a and b", "a, b and c".
func namesListed(_ names: [String]) -> String {
    switch names.count {
    case 0: return ""
    case 1: return names[0]
    default: return names.dropLast().joined(separator: ", ") + " and " + names.last!
    }
}
