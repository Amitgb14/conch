import Foundation
import Testing
@testable import ConchKit

// Settings › Agents › Over ACP and the Add an ACP Agent sheet.

private let geminiACP = AgentAvailability(name: "gemini-acp", label: "Gemini (ACP)", installed: true, tier: "runs here", sessions: true,
                                          proto: "acp", acp: ACPSpec(name: "gemini-acp", label: "Gemini (ACP)", command: "gemini --experimental-acp",
                                                                     dirs: ["~/.local/bin"], env: ["GEMINI_MODEL=pro"]), removable: true)

@Test func agentStatusDecodesAnACPAgent() throws {
    let json = #"""
    {"agents":[{"name":"claude","installed":true,"tier":"supported"},
     {"name":"gemini-acp","label":"Gemini (ACP)","installed":true,"tier":"runs here","sessions":true,"protocol":"acp",
      "acp":{"name":"gemini-acp","command":"gemini --experimental-acp","env":["A=1"]},"removable":true}]}
    """#
    let list = try JSONDecoder().decode(AgentStatusList.self, from: Data(json.utf8))
    let a = try #require(list.agents?.last)
    #expect(a.isACP && a.removable == true && a.acp?.command == "gemini --experimental-acp" && a.acp?.env == ["A=1"])
    #expect(a.menuTitle == "Gemini (ACP) — ACP")
    #expect(list.agents?.first?.isACP == false && list.agents?.first?.menuTitle == "claude")
}

@Test func installedAgentRowsLeaveACPAgentsToTheirSection() {
    let rows = installedAgentRows([AgentAvailability(name: "claude", installed: true), geminiACP], installing: [], canInstall: true)
    #expect(rows.map(\.name) == ["claude"])
    #expect(acpAgentNote(geminiACP) == "Reopens and lists its sessions")
    var missing = geminiACP
    missing.installed = false
    missing.sessions = nil
    #expect(acpAgentNote(missing) == "Its sessions are not listed · not installed")
}

@Test func acpNameFromTheCommand() {
    #expect(acpName(for: "gemini --experimental-acp") == "gemini-acp")
    #expect(acpName(for: "goose acp") == "goose-acp")
    #expect(acpName(for: "/opt/bin/Claude_Code_ACP") == "claude-code-acp")
    #expect(acpName(for: "LANG=C codex-acp") == "codex-acp")
    #expect(acpName(for: "") == "" && acpName(for: "  ") == "" && acpName(for: "!!!") == "")
}

@Test func acpFormBuildsTheSpec() {
    var f = ACPForm()
    f.command = "  goose acp "
    f.dirs = "~/.local/bin\n\n /opt/goose \n"
    f.env = "GOOSE_X=1\nB=two words"
    #expect(f.spec == ACPSpec(name: "goose-acp", command: "goose acp", dirs: ["~/.local/bin", "/opt/goose"], env: ["GOOSE_X=1", "B=two words"]))
    #expect(f.problem(supported: ["claude"]) == nil)
    f.name = "mine"
    f.label = "Mine"
    #expect(f.spec.name == "mine" && f.spec.label == "Mine")
}

@Test func acpFormRefusesWhatTheServerWould() {
    var f = ACPForm()
    #expect(f.problem(supported: []) != nil) // no command
    f.command = "x --acp"
    f.name = "Not A Name"
    #expect(f.problem(supported: [])?.contains("lower-case") == true)
    f.name = "claude"
    #expect(f.problem(supported: ["claude"])?.contains("claude-acp is free") == true)
    f.name = "ok"
    f.env = "not an assignment"
    #expect(f.problem(supported: [])?.contains("NAME=value") == true)
    f.env = "=v"
    #expect(f.problem(supported: []) != nil)
    f.env = "EMPTY="
    #expect(f.problem(supported: []) == nil)
}

@Test func acpFormEditsKeepTheName() {
    var f = ACPForm(geminiACP)
    #expect(f.command == "gemini --experimental-acp" && f.dirs == "~/.local/bin" && f.env == "GEMINI_MODEL=pro" && f.label == "Gemini (ACP)")
    f.name = "renamed"
    #expect(f.spec.name == "gemini-acp")
    // Editing an agent named like a supported one is not refused for it.
    #expect(f.problem(supported: ["gemini-acp"]) == nil)
}

@Test func acpAddParamsAreFlat() throws {
    let p = ACPAddParams(spec: ACPSpec(name: "x", command: "x --acp"), replace: false)
    let s = String(decoding: try JSONEncoder().encode(p), as: UTF8.self)
    #expect(s.contains(#""name":"x""#) && s.contains(#""command":"x --acp""#) && !s.contains("replace") && !s.contains("label"))
    let r = String(decoding: try JSONEncoder().encode(ACPAddParams(spec: p.spec, replace: true)), as: UTF8.self)
    #expect(r.contains(#""replace":true"#))
}

@Test func acpCheckSays() throws {
    let ok = try JSONDecoder().decode(ACPCheck.self, from: Data(#"{"agent":"gemini-cli","version":"0.24.1","protocol_version":1,"load":true,"list":true}"#.utf8))
    #expect(ok.speaksACP)
    #expect(acpCheckHeadline(ok) == "It speaks ACP · gemini-cli 0.24.1 · protocol version 1")
    let lines = acpCheckLines(ok)
    #expect(lines.map(\.mark) == [.yes, .yes, .yes, .yes])
    #expect(lines[2].what == "Lists its sessions" && lines[3].what == "No sign-in asked")

    let partial = ACPCheck(agent: "goose", protocolVersion: 1, load: true, auth: ["Sign in with Google", "an API key"])
    let pl = acpCheckLines(partial)
    #expect(pl[2].mark == .no && pl[3].mark == .warn && pl[3].why == "Sign in with Google, an API key")

    #expect(acpCheckHeadline(ACPCheck(problem: "exited", message: "it exited before answering initialize")) == "It exited while starting")
    #expect(acpCheckHeadline(ACPCheck(problem: "not_acp")) == "It does not speak ACP")
    #expect(acpCheckHeadline(ACPCheck(problem: "not_found", message: "gemini was not found on PATH")) == "Gemini was not found on PATH")
    #expect(acpCheckLines(ACPCheck(problem: "failed", message: "x")).isEmpty)
}
