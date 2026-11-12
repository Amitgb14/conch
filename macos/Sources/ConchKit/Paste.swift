import Foundation

/// What the app asks before a paste Ghostty holds back: text with a line
/// break, going to a program that did not ask for bracketed paste, where
/// each line runs as if typed and Return pressed.
public func pasteWarning(_ text: String) -> (title: String, info: String) {
    // A line copied with its newline is one line, not two.
    var body = text
    while body.last?.isNewline == true { body.removeLast() }
    // \r\n is one Character, so isNewline counts it once.
    let rows = body.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline)
    let lines = max(rows.count, 1)
    let title = lines == 1 ? "Paste this line?" : "Paste \(lines) lines?"
    let what = lines > 1 ? "Each line runs" : body.count < text.count ? "It ends in a line break, so it runs" : "It runs"
    var preview = rows.prefix(4).map { $0.count > 80 ? String($0.prefix(79)) + "…" : String($0) }.joined(separator: "\n")
    if lines > 4 { preview += "\n…" }
    if text.contains("\u{1b}[201~") {
        return (title, "\(what) as soon as it is pasted, and it holds the end-of-paste sequence, which a program could take for the end of the paste and run what follows.\n\n\(preview)")
    }
    return (title, "\(what) as soon as it is pasted: the program in this pane does not tell a paste from typing.\n\n\(preview)")
}
