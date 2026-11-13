import Foundation
import Testing
@testable import ConchKit

private let claude = AgentAvailability(name: "claude", label: "Claude Code", installed: false)
private let codex = AgentAvailability(name: "codex", label: "Codex", installed: true)

// A new cloud machine has no agents: starting one installs it first.
@Test func agentReadinessInstallsAMissingAgent() {
    #expect(agentReadiness("claude", status: [claude, codex], canInstall: true, machineName: "bot") == .install)
    #expect(agentReadiness("codex", status: [claude, codex], canInstall: true, machineName: "bot") == .start)
}

// An older server cannot install: said, with the agent's own name.
@Test func agentReadinessWithoutInstall() {
    guard case .missing(let why) = agentReadiness("claude", status: [claude], canInstall: false, machineName: "bot") else {
        Issue.record("expected missing"); return
    }
    #expect(why.contains("Claude Code is not installed on bot"))
    #expect(why.contains("reload or upgrade"))
}

// Nothing to go by — the machine could not be asked, the list is empty,
// an agent it does not list — and the start goes on: the server's own
// refusal says why better than a guess here.
@Test func agentReadinessUnknownStarts() {
    #expect(agentReadiness("claude", status: nil, canInstall: true, machineName: "bot") == .start)
    #expect(agentReadiness("claude", status: [], canInstall: true, machineName: "bot") == .start)
    #expect(agentReadiness("pi", status: [claude, codex], canInstall: false, machineName: "bot") == .start)
    #expect(agentReadiness("", status: [claude], canInstall: true, machineName: "bot") == .start)
}

@Test func agentInstallJobText() {
    #expect(agentInstallJob("Claude Code", machineName: "bot") == "Installing Claude Code on bot")
    #expect(jobSummary([agentInstallJob("Claude Code", machineName: "bot")]) == "Installing Claude Code on bot…")
}

@Test func agentInstallProblems() {
    #expect(agentInstallProblem("Claude Code", exitCode: 0, found: true) == nil)
    #expect(agentInstallProblem("Claude Code", exitCode: 1, found: false)?.contains("exited 1") == true)
    #expect(agentInstallProblem("Claude Code", exitCode: 127, found: true)?.contains("exited 127") == true)
    #expect(agentInstallProblem("Claude Code", exitCode: 0, found: false)?.contains("not on the machine's PATH") == true)
}
