import Foundation
import Testing
@testable import ConchKit

// The same names as gitx.RepoName gives the CLI.
@Test func repoNamesAsGitDoes() {
    let cases: [String: String] = [
        "https://github.com/Amitgb14/conch.git": "conch",
        "https://github.com/Amitgb14/conch": "conch",
        "https://github.com/Amitgb14/conch/": "conch",
        "https://github.com/Amitgb14/conch.git/": "conch",
        "  https://gitlab.com/a/b/deep-repo.git ": "deep-repo",
        "git@github.com:Amitgb14/conch.git": "conch",
        "github.com:owner/my.tool.git": "my.tool",
        "ssh://git@host:2222/org/repo.git": "repo",
        "file:///srv/git/api.git": "api",
        "/srv/git/api.git": "api",
        "../repo": "repo",
        "repo.git": "repo",
        "": "",
        "https://github.com/": "",
        "https://github.com": "",
        "git@github.com:": "",
        ".git": "",
    ]
    for (url, want) in cases {
        #expect(repoName(url) == want, "\(url)")
    }
}

@Test func cloneableURLs() {
    for ok in ["https://github.com/a/b.git", "git@github.com:a/b.git", "ssh://git@h/a/b", "git://h/a", "file:///srv/a.git", "/srv/a.git", "~/src/a", "HTTPS://GITHUB.COM/A/B"] {
        #expect(looksCloneable(ok), "\(ok)")
    }
    for bad in ["", "   ", "https://", "github", "a b", "git@github.com:", "just words here"] {
        #expect(!looksCloneable(bad), "\(bad)")
    }
}

@Test func projectPaths() {
    #expect(projectPath(parent: "~/workspace", name: "conch") == "~/workspace/conch")
    #expect(projectPath(parent: "/src/", name: " api ") == "/src/api")
    #expect(projectPath(parent: "", name: "x") == nil && projectPath(parent: "~", name: "") == nil)
    #expect(projectPath(parent: "~", name: "a/b") == nil && projectPath(parent: "~", name: "..") == nil)
    #expect(defaultProjectParent(home: "/Users/a", exists: { $0 == "/Users/a/workspace" }) == "~/workspace")
    #expect(defaultProjectParent(home: "/Users/a", exists: { _ in false }) == "~")
    #expect(AddProjectMode.allCases.map(\.title) == ["Open a folder", "Clone a repository", "New repository", "From this Mac"])
}
