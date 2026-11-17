import Foundation
import Testing
@testable import ConchKit

@Test func titleCaseWords() {
    let cases: [(String, String)] = [
        ("Code hosts", "Code Hosts"),
        ("Ticket trackers", "Ticket Trackers"),
        ("Deploy previews", "Deploy Previews"),
        ("Mobile preview", "Mobile Preview"),
        ("CI", "CI"),
        ("Sandboxes", "Sandboxes"),
        ("Add to PR review", "Add to PR Review"),            // small words stay small inside
        ("Open a GitHub issue", "Open a GitHub Issue"),      // a name keeps its capitals
        ("Check with my key", "Check with My Key"),
        ("Skills & MCP servers", "Skills & MCP Servers"),
        ("+ Add a registry…", "+ Add a Registry…"),          // marks and ellipses kept
        ("✦ Adapt from API docs…", "✦ Adapt from API Docs…"),
        ("Start conch's server", "Start conch's Server"),    // conch is written small
        ("Edit config.toml", "Edit config.toml"),            // a file stays as it is
        ("Save in .conch/integrations.toml for the team", "Save in .conch/integrations.toml for the Team"),
        ("Re-run failed jobs", "Re-run Failed Jobs"),
        ("the end", "The End"),                              // a small word first or last is capitalized
        ("Sign in to", "Sign in To"),
        ("All in graph →", "All in Graph →"),
        ("", ""),
        ("  two  spaces ", "  Two  Spaces "),
        ("x", "X"),
    ]
    for (s, want) in cases { #expect(titleCase(s) == want, "\(s)") }
}

// The registry says its kinds in sentence case; the app shows them as it
// shows its own names.
@Test func registryKindsAreTitleCase() {
    let reg = [IntegrationKindInfo(kind: "host", label: "Code hosts", about: "where the code lives"),
               IntegrationKindInfo(kind: "ci", label: "CI"),
               IntegrationKindInfo(kind: "deploy", label: "Deploy previews")]
    let kinds = IntegrationCatalog.kinds(reg, cards: [])
    #expect(kinds.map(\.label) == ["Code Hosts", "CI", "Deploy Previews"])
    #expect(kinds[0].about == "where the code lives" && kinds.map(\.kind) == ["host", "ci", "deploy"])
    // Without a registry, conch's own.
    #expect(IntegrationCatalog.kinds(nil, cards: []).map(\.label).contains("Ticket Trackers"))
}

// Every button and menu item the app names in its source is in Title Case,
// as the Mac names them. Checkboxes (Toggle) are sentence case on the Mac,
// so they are not read; nor is a label with a value in it.
@Test func appButtonsAndMenusAreTitleCase() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    let files = try FileManager.default.contentsOfDirectory(atPath: dir).filter { $0.hasSuffix(".swift") }
    let call = try Regex(#"(?:NSMenuItem\(title: *|Button\(|Menu\()"([^"\\]*)""#)
    var wrong: [String] = []
    for f in files {
        let src = try String(contentsOfFile: dir + "/" + f, encoding: .utf8)
        for m in src.matches(of: call) {
            guard let label = m.output[1].substring.map(String.init), label.split(separator: " ").count > 1 else { continue }
            if titleCase(label) != label { wrong.append("\(f): \"\(label)\" → \"\(titleCase(label))\"") }
        }
    }
    #expect(wrong.isEmpty, "not in Title Case:\n\(wrong.joined(separator: "\n"))")
}
