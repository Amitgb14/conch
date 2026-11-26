import Foundation
import Testing

// Every SwiftUI root the app hosts — a window, a toolbar view, a popover —
// turns keyboard focus rings off (noFocusRings, FocusRings.swift), so with
// Keyboard navigation on no sheet opens with a ring round its first button
// as if that one were selected. Sheets inherit it from their window, so the
// roots are what is checked: a new window left without it fails here.
@Test func everyHostedRootHasNoFocusRings() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    let files = try FileManager.default.contentsOfDirectory(atPath: dir).filter { $0.hasSuffix(".swift") }
    var src: [String: String] = [:]
    for f in files { src[f] = try String(contentsOfFile: dir + "/" + f, encoding: .utf8) }

    /// The text of `struct NAME` up to its closing brace at the margin.
    func type(_ name: String) -> String? {
        for s in src.values {
            guard let start = s.range(of: "struct \(name):") else { continue }
            let rest = s[start.lowerBound...]
            let end = rest.range(of: "\n}\n")?.upperBound ?? rest.endIndex
            return String(rest[..<end])
        }
        return nil
    }

    let host = try Regex(#"(?:NSHostingView|NSHostingController|WorkspacesHost)\(rootView: *([A-Za-z_]+)[^\n]*"#)
    var seen = 0
    var missing: [String] = []
    for (f, s) in src {
        for m in s.matches(of: host) {
            seen += 1
            let line = String(m.output[0].substring ?? "")
            let root = String(m.output[1].substring ?? "")
            if line.contains(".noFocusRings()") { continue }
            if root == "root" {
                // A root built a line before, as the popover's is.
                if s.split(separator: "\n").contains(where: { $0.contains("let root =") && $0.contains(".noFocusRings()") }) { continue }
            } else if let t = type(root), t.contains(".noFocusRings()") {
                continue
            }
            missing.append("\(f): \(line)")
        }
    }
    #expect(seen >= 9, "found only \(seen) hosted roots; has the pattern stopped matching?")
    #expect(missing.isEmpty, "a hosted root without .noFocusRings():\n\(missing.joined(separator: "\n"))")
}
