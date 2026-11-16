import Testing
@testable import ConchKit

private let login = "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a&response_type=code&scope=org%3Acreate_api_key+user%3Ainference&state=0KSH"

/// The link as a program writes it at width w: a row at a time.
private func rows(_ s: String, _ w: Int, indent: String = "") -> String {
    var out: [String] = []
    var rest = Substring(s)
    var first = true
    while !rest.isEmpty {
        let room = w - (first ? 0 : indent.count)
        out.append((first ? "" : indent) + rest.prefix(room))
        rest = rest.dropFirst(room)
        first = false
    }
    return out.joined(separator: "\n")
}

@Test func aLinkBrokenAtTheEdgeIsJoined() {
    #expect(unwrapCopiedLink(rows(login, 60), columns: 60) == login)
    #expect(unwrapCopiedLink(rows(login, 60) + "\n", columns: 60) == login)
    #expect(unwrapCopiedLink(rows(login, 60).replacingOccurrences(of: "\n", with: "\r\n"), columns: 60) == login)
    // Two rows, the pane of the report: 244 wide.
    let long = login + String(repeating: "x", count: 300)
    #expect(unwrapCopiedLink(rows(long, 244), columns: 244) == long)
}

@Test func aLinkStartingAFewColumnsInStillJoins() {
    // Printed after a four-column indent: its first row is 56 long, and
    // the selection starts at the link.
    let copied = String(login.prefix(56)) + "\n" + rows(String(login.dropFirst(56)), 60)
    #expect(unwrapCopiedLink(copied, columns: 60) == login)
}

@Test func indentedContinuationRowsJoin() {
    #expect(unwrapCopiedLink(rows(login, 60, indent: "  "), columns: 60) == login)
}

@Test func whatIsNotOneBrokenLinkIsLeftAlone() {
    let cases = [
        "https://a.example/x\nhttps://b.example/y",          // two links
        "https://a.example/x\ndone",                        // a short row ended it
        String(rows(login, 60).prefix(30)) + "\nmore",      // first row far from the edge
        "see https://a.example\n/x",                        // a space inside
        "hello\nworld",
        "https://a.example/only-one-row",
        "",
    ]
    for c in cases { #expect(unwrapCopiedLink(c, columns: 60) == c, "\(c)") }
    // A middle row that stops short of the edge.
    let broken = rows(login, 60).split(separator: "\n").enumerated()
        .map { $0.offset == 1 ? String($0.element.dropLast(5)) : String($0.element) }.joined(separator: "\n")
    #expect(unwrapCopiedLink(broken, columns: 60) == broken)
    // An empty row in the middle.
    #expect(unwrapCopiedLink(String(repeating: "h", count: 0) + "https://" + String(repeating: "a", count: 52) + "\n\nb", columns: 60)
            == "https://" + String(repeating: "a", count: 52) + "\n\nb")
}

@Test func noWidthLeavesItAlone() {
    let r = rows(login, 60)
    #expect(unwrapCopiedLink(r, columns: 0) == r)
    #expect(unwrapCopiedLink(r, columns: -1) == r)
    // Rows wider than the pane said are not this pane's rows.
    #expect(unwrapCopiedLink(r, columns: 40) == r)
}
