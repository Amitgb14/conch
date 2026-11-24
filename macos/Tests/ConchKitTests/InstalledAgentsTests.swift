import Foundation
import Testing
@testable import ConchKit

// Settings → Agents lists every agent a machine reports, each with its
// version, an install, an install under way, or no installer at all.

private let status: [AgentAvailability] = [
    AgentAvailability(name: "claude", label: "Claude Code", installed: true, tier: "supported", version: "2.1.325"),
    AgentAvailability(name: "qwen", label: "Qwen Code", installed: false, tier: "supported"),
    AgentAvailability(name: "crush", label: "Crush", installed: false, tier: "runs here"),
    AgentAvailability(name: "pi", label: "Pi", installed: false, tier: "runs here", noInstaller: true),
    AgentAvailability(name: "goose", label: "Goose", installed: true),
    AgentAvailability(name: "grok", label: "Grok", installed: true, tier: "runs here", sessions: true),
]

@Test func installedAgentRowsSayWhatEachIs() {
    let rows = installedAgentRows(status, installing: ["crush"], canInstall: true)
    #expect(rows.map(\.name) == ["claude", "qwen", "crush", "pi", "goose", "grok"])
    #expect(rows[0].state == .installed("2.1.325") && rows[0].note == nil)
    #expect(rows[1].state == .missing)
    #expect(rows[2].state == .installing)
    #expect(rows[2].note == "conch runs it; its sessions are not read")
    #expect(rows[3].state == .noInstaller)
    #expect(rows[3].note == "conch has no installer for it")
    // A version the machine could not read is still installed.
    #expect(rows[4].state == .installed(""))
    // One it only runs whose sessions are read says that instead.
    #expect(rows[5].note == "conch runs it and reads its sessions")
}

// An older server cannot install: a missing agent is said, not offered.
@Test func installedAgentRowsWithoutInstall() {
    let rows = installedAgentRows(status, installing: [], canInstall: false)
    #expect(rows[1].state == .unavailable)
    #expect(rows[3].state == .noInstaller)
}

@Test func installedAgentsSummary() {
    #expect(installedAgentsSummary(status) == "3 of 6")
    #expect(installedAgentsSummary([]) == "none known")
    #expect(installedAgentsSummary(nil) == "unknown")
}

// What agent.status sends: version, path and no_installer, all optional.
@Test func agentAvailabilityDecodes() throws {
    let json = #"[{"name":"pi","label":"Pi","installed":false,"tier":"runs here","no_installer":true,"sessions":true},{"name":"claude","installed":true,"version":"2.1.3","path":"/x/claude"}]"#
    let a = try JSONDecoder().decode([AgentAvailability].self, from: Data(json.utf8))
    #expect(a[0].noInstaller == true && a[0].version == nil && a[0].sessions == true)
    #expect(a[1].sessions == nil)
    #expect(a[1].version == "2.1.3" && a[1].noInstaller == nil && a[1].title == "claude")
}

@Test func agentMenuNotesMarkTheMissing() {
    #expect(agentMenuNotes(status) == ["qwen": "not installed", "crush": "not installed", "pi": "not installed"])
    #expect(agentMenuNotes(nil).isEmpty)
}
