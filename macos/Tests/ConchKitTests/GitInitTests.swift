import Foundation
import Testing
@testable import ConchKit

private func project(_ id: String, _ name: String, git: Bool?) -> ProjectInfo {
    var p = ProjectInfo(id: id, name: name, path: "/src/\(name)")
    p.git = git
    return p
}

private let caps = ["project.v1", gitInitCapability]

// A task in a repository goes on; in a plain folder it asks first, naming
// the folder and what initializing does, and offers the folder itself.
@Test func branchReadinessForOneProject() {
    #expect(branchReadiness([project("r1", "api", git: true)], capabilities: caps, machineName: "this Mac", inFolder: true) == .ready)
    // Unknown is the server's to refuse, not the app's to guess.
    #expect(branchReadiness([project("r1", "api", git: nil)], capabilities: caps, machineName: "this Mac") == .ready)
    #expect(branchReadiness([], capabilities: nil, machineName: "this Mac") == .ready)

    guard case .ask(let q) = branchReadiness([project("r2", "conch-mobile-app", git: false)], capabilities: caps, machineName: "this Mac", inFolder: true) else {
        Issue.record("a plain folder did not ask")
        return
    }
    #expect(q.title == "conch-mobile-app isn't a git repository yet")
    #expect(q.info.hasPrefix("A task works on a branch of its own, which needs git. Initialize git here?"))
    #expect(q.info.contains("git init") && q.info.contains("empty first commit") && q.info.contains("not committed"))
    #expect(q.projects == ["r2"] && q.inFolder)
    #expect(q.info.contains("folder itself"))
    // Without the folder offered, it is not mentioned.
    guard case .ask(let bare) = branchReadiness([project("r2", "notes", git: false)], capabilities: caps, machineName: "x") else {
        Issue.record("no question")
        return
    }
    #expect(!bare.inFolder && !bare.info.contains("folder itself"))
}

// Across a group: only the plain folders are initialized, all named, and
// the folder itself is never offered, since the task needs every branch.
@Test func branchReadinessForAGroup() {
    let repos = [project("r1", "api", git: true), project("r2", "web", git: false), project("r3", "specs", git: false), project("r4", "docs", git: false)]
    guard case .ask(let q) = branchReadiness(repos, capabilities: caps, machineName: "busybox", inFolder: true) else {
        Issue.record("a group with plain folders did not ask")
        return
    }
    #expect(q.title == "web, specs and docs aren't git repositories yet")
    #expect(q.projects == ["r2", "r3", "r4"] && !q.inFolder)
    #expect(q.info.contains("a branch in each") && q.info.contains("in each?"))
    #expect(namesListed(["a", "b"]) == "a and b" && namesListed([]) == "")
}

// An older server cannot make one: it says to reload, never asks.
@Test func branchReadinessAgainstAnOlderServer() {
    let plain = [project("r2", "notes", git: false)]
    #expect(branchReadiness(plain, capabilities: ["project.v1"], machineName: "busybox") ==
        .reloadServer("notes isn't a git repository yet, and the server on busybox predates making one: reload the server, or run git init and commit once there yourself"))
    if case .reloadServer = branchReadiness(plain, capabilities: nil, machineName: "busybox") {} else { Issue.record("no capabilities read as having it") }
    #expect(branchReadiness([project("r1", "api", git: true)], capabilities: nil, machineName: "busybox") == .ready)

    #expect(gitInitRefusal(plain[0], capabilities: caps, machineName: "x") == nil)
    #expect(gitInitRefusal(plain[0], capabilities: ["project.v1"], machineName: "busybox")?.contains("reload the server") == true)
    #expect(gitInitRefusal(project("r1", "api", git: true), capabilities: caps, machineName: "x") == "api is already a git repository")
}

// A plain folder's menu offers Initialize Git…; a repository's does not.
@Test func plainFolderMenuOffersGitInit() {
    let s = Snapshot(projects: [project("r1", "api", git: true), project("r2", "notes", git: false)])
    let node = { (id: String) in TreeNode(id: "p:box|\(id)", kind: .project("box|\(id)"), title: id) }
    let plain = s.menu(for: node("r2"), machine: "box")
    #expect(plain.contains { $0.action == .gitInit(project: "box|r2") && $0.title == "Initialize Git…" })
    #expect(!s.menu(for: node("r1"), machine: "box").contains { if case .gitInit = $0.action { return true }; return false })
    // Offline: no menu at all, as before.
    #expect(s.menu(for: node("r2"), machine: "box", machineOnline: false).isEmpty)
}
