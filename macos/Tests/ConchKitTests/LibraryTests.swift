import Foundation
import Testing
@testable import ConchKit

// The library as the server sends it, and the edits the sheet makes to it
// before sending it back with set.

private let answer = #"""
{"library":{"servers":[{"name":"github","command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"${GITHUB_TOKEN}"},"agents":["claude","codex"]},{"name":"docs","url":"https://docs.example/mcp","transport":"sse","agents":[]}],"skills":[{"name":"review","path":"~/skills/review","agents":["claude"]}]},
 "cells":[{"kind":"mcp server","name":"github","agent":"claude","state":"on"},{"kind":"mcp server","name":"github","agent":"codex","state":"pending"},{"kind":"mcp server","name":"github","agent":"gemini","state":"differs","detail":"declared there by hand"},{"kind":"skill","name":"review","agent":"codex","state":"remove"}],
 "agents":["claude","codex","gemini"],"undos":["20261106-1"]}
"""#

@Test func libraryDecodes() throws {
    let r = try JSONDecoder().decode(AgentLibraryResult.self, from: Data(answer.utf8))
    #expect(r.library.servers.count == 2 && r.library.skills.count == 1)
    #expect(r.library.servers[0].what == "stdio · npx" && r.library.servers[1].what == "sse")
    #expect(r.library.servers[0].commandLine == "npx -y @modelcontextprotocol/server-github")
    #expect(r.library.servers[1].commandLine == "https://docs.example/mcp")
    #expect(r.cell("mcp server", "github", "codex").state == "pending")
    #expect(r.cell("skill", "review", "gemini").state == "off") // nothing said: off
    #expect(r.pending == 2) // a + and a −
    #expect(r.undos == ["20261106-1"])
    // An empty library from the server is nulls, and still decodes.
    let empty = try JSONDecoder().decode(AgentLibraryResult.self, from: Data(#"{"library":{"servers":null,"skills":null},"agents":["claude"]}"#.utf8))
    #expect(empty.library == Library() && empty.pending == 0 && empty.cells == nil)
    let bare = try JSONDecoder().decode(AgentLibraryResult.self, from: Data("{}".utf8))
    #expect(bare.agents.isEmpty && bare.library.servers.isEmpty)
}

// #166: a server or skill given to no agent came as "agents": null (an add
// without -to), and the whole library failed to decode. Null or missing is
// none, and what else the server said is kept.
@Test func libraryNullAgents() throws {
    let json = #"{"library":{"servers":[{"name":"gh","command":"gh","agents":["claude"]},{"name":"jira-mcp","url":"http://jira.example/v1/jira/mcp","headers":{"Authorization":"Bearer ${CONCH_MCP_JIRA_MCP_TOKEN}"},"transport":"sse","agents":null},{"name":"wiki","url":"https://wiki.example/mcp"}],"skills":[{"name":"review","path":"~/r","agents":null,"source":"o/r/review@main"}]},"agents":["claude"]}"#
    let r = try JSONDecoder().decode(AgentLibraryResult.self, from: Data(json.utf8))
    #expect(r.library.servers.map(\.agents) == [["claude"], [], []])
    #expect(r.library.servers[1].headers?["Authorization"] == "Bearer ${CONCH_MCP_JIRA_MCP_TOKEN}" && r.library.servers[1].what == "sse")
    #expect(r.library.skills[0].agents == [] && r.library.skills[0].source == "o/r/review@main")
    // And it goes back as an empty list, never a null.
    let back = String(decoding: try JSONEncoder().encode(r.library), as: UTF8.self)
    #expect(!back.contains("null") && back.contains(#""agents":[]"#))
}

@Test func libraryEdits() throws {
    let r = try JSONDecoder().decode(AgentLibraryResult.self, from: Data(answer.utf8))
    let order = r.agents
    // A tick on and off, kept in the library's order of agents.
    let on = r.library.toggling(LibraryKind.server, "github", "gemini", order: order)
    #expect(on.servers[0].agents == ["claude", "codex", "gemini"])
    let off = on.toggling(LibraryKind.server, "github", "claude", order: order)
    #expect(off.servers[0].agents == ["codex", "gemini"])
    #expect(r.library.toggling(LibraryKind.skill, "review", "codex", order: order).skills[0].agents == ["claude", "codex"])
    #expect(r.library.toggling(LibraryKind.skill, "nope", "codex", order: order) == r.library) // a name it lacks
    #expect(r.library.toggling(LibraryKind.server, "review", "codex", order: order) == r.library) // wrong kind

    #expect(r.library.removing(LibraryKind.server, "docs").servers.map(\.name) == ["github"])
    #expect(r.library.removing(LibraryKind.skill, "review").skills.isEmpty)
    #expect(r.library.removing(LibraryKind.skill, "github") == r.library)

    // A skill named after its folder when no name is given, replacing one
    // of that name; renamed in the form, the old one goes.
    let s = try r.library.addingSkill(name: " ", path: "~/skills/factory/", agents: ["claude"])
    #expect(s.skills.last == LibrarySkill(name: "factory", path: "~/skills/factory/", agents: ["claude"]))
    let renamed = try r.library.addingSkill(name: "look", path: "~/skills/review", agents: [], replacing: "review")
    #expect(renamed.skills.map(\.name) == ["look"])
    #expect(throws: LibraryFormError.self) { try r.library.addingSkill(name: "x", path: "  ", agents: []) }

    // A server from a command line or a URL, with its pairs.
    let cmd = try Library().addingServer(name: "fs", commandOrURL: #"npx -y "@scope/server fs" /tmp"#, env: "A=${A}; B=${B}", headers: "", sse: true, agents: ["codex"])
    #expect(cmd.servers[0] == LibraryServer(name: "fs", command: "npx", args: ["-y", "@scope/server fs", "/tmp"], env: ["A": "${A}", "B": "${B}"], agents: ["codex"]))
    let url = try Library().addingServer(name: "docs", commandOrURL: "https://d.example/mcp", env: "", headers: "Authorization=Bearer ${T}", sse: true, agents: [])
    #expect(url.servers[0].url == "https://d.example/mcp" && url.servers[0].transport == "sse" && url.servers[0].command == nil)
    #expect(url.servers[0].headers == ["Authorization": "Bearer ${T}"])
    let edited = try r.library.addingServer(name: "gh", commandOrURL: "gh-mcp", env: "", headers: "", sse: false, agents: [], replacing: "github")
    #expect(edited.servers.map(\.name).sorted() == ["docs", "gh"])
    #expect(throws: LibraryFormError.self) { try Library().addingServer(name: "", commandOrURL: "x", env: "", headers: "", sse: false, agents: []) }
    #expect(throws: LibraryFormError.self) { try Library().addingServer(name: "x", commandOrURL: "  ", env: "", headers: "", sse: false, agents: []) }
    #expect(throws: LibraryFormError.self) { try Library().addingServer(name: "x", commandOrURL: "y", env: "NOEQUALS", headers: "", sse: false, agents: []) }

    // conch's own tools, once however often it is added.
    let tools = r.library.withConchTools(agents: ["claude"]).withConchTools(agents: order)
    #expect(tools.servers.filter { $0.name == "conch" }.count == 1 && tools.hasConchTools && !r.library.hasConchTools)
    #expect(tools.servers.last == LibraryServer(name: "conch", command: "conch", args: ["mcp"], agents: order))

    // What is sent back: optional fields left out, as the server's omitempty.
    let json = String(data: try JSONEncoder().encode(LibrarySkill(name: "a", path: "/p", agents: [])), encoding: .utf8)!
    #expect(json.contains(#""agents":[]"#))
    let sj = String(data: try JSONEncoder().encode(LibraryServer(name: "a", command: "c", agents: [])), encoding: .utf8)!
    #expect(!sj.contains("url") && !sj.contains("env"))
}

@Test func libraryPairsAndArgs() throws {
    #expect(try parsePairs("") == nil && parsePairs(" ; ") == nil)
    #expect(try parsePairs("A = 1; B=x=y") == ["A": "1", "B": "x=y"])
    #expect(throws: LibraryFormError.self) { try parsePairs("=1") }
    #expect(joinPairs(["B": "2", "A": "1"]) == "A=1; B=2" && joinPairs(nil) == "")
    #expect(splitArgs(#"a "b c" "" d"#) == ["a", "b c", "", "d"])
    #expect(splitArgs("") == [] && splitArgs("   ") == [])
    #expect(joinArgs(["a", "b c", ""]) == #"a "b c" """#)
    #expect(splitArgs(joinArgs(["x", "y z"])) == ["x", "y z"])
}

@Test func libraryMarksAndApply() throws {
    #expect(["on", "pending", "remove", "off", "theirs", "differs", "cannot", "?"].map(libraryMark) == ["✓", "+", "−", "·", "○", "!", "✗", "·"])
    #expect(libraryMeaning(LibraryCell(kind: "skill", name: "a", agent: "codex", state: "differs", detail: "yours")) == "declared by hand, differently: left alone — yours")
    #expect(libraryMeaning(LibraryCell(kind: "skill", name: "a", agent: "codex", state: "off")) == "off")
    #expect(libraryAgentShort("opencode") == "Oc" && libraryAgentShort("aider") == "ai" && libraryAgentShort("x") == "x")

    let plan = try JSONDecoder().decode(AgentSyncResult.self, from: Data(#"""
    {"dir":"","changes":[{"agent":"codex","kind":"skill","name":"review","path":"/h/.agents/skills/review","action":"link"},{"agent":"claude","kind":"mcp server","name":"github","action":"same"},{"agent":"gemini","kind":"mcp server","name":"github","action":"skip","detail":"by hand"},{"agent":"codex","kind":"skill","name":"old","path":"/h/.agents/skills/old","action":"remove","error":"busy"}]}
    """#.utf8))
    #expect(plan.writes.map(\.name) == ["review", "old"])
    #expect(plan.failed.map(\.name) == ["old"])
    #expect(syncLine(plan.writes[0]) == "link skill review for codex\n    /h/.agents/skills/review")
    #expect(syncLine(SyncChange(kind: "mcp server", name: "x", action: "create")) == "create mcp server x")
    // Nothing to do is an empty answer, not an error.
    #expect(try JSONDecoder().decode(AgentSyncResult.self, from: Data(#"{"dir":""}"#.utf8)).writes.isEmpty)
}
