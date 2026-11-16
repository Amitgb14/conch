import Foundation

/// What to put on the clipboard for text copied from a pane `columns`
/// wide: a link the pane's edge broke onto several rows comes back as the
/// one link it is, and anything else as it was copied.
///
/// A program that draws its own interface (Claude Code's login, an agent's
/// "open this URL") writes a long link a row at a time with a line break
/// at the edge, so the terminal has no soft wrap to undo and the copy got
/// the break with it — a login URL that no longer works. The TUI joins
/// such rows when it copies (internal/tui/selection.go); this is the app's
/// side of the same thing.
///
/// Only a link is joined, and only when it ran to the edge: every row but
/// the last is the pane's whole width (the first may be a little shorter:
/// the link can start a few columns in, after an indent or a box's edge), no row has a space inside it,
/// and no later row starts a link of its own. Two links copied one above
/// the other, or a link with a word under it, stay as they were.
public func unwrapCopiedLink(_ text: String, columns: Int) -> String {
    guard columns > 0 else { return text }
    var body = text
    while body.last?.isNewline == true { body.removeLast() }
    let rows = body.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline).map(String.init)
    guard rows.count > 1 else { return text }
    let pieces = rows.map { $0.trimmingCharacters(in: .whitespaces) }
    guard let first = pieces.first, first.hasPrefix("https://") || first.hasPrefix("http://") else { return text }
    for (i, piece) in pieces.enumerated() {
        if piece.isEmpty || piece.contains(where: \.isWhitespace) { return text }
        if i > 0, piece.contains("://") { return text }
        // A row that stopped short of the edge ended the link there.
        if i < rows.count - 1 {
            let full = i == 0 ? rows[i].count * 4 >= columns * 3 : rows[i].count == columns
            if !full || (i == 0 && rows[i].count > columns) { return text }
        }
    }
    return pieces.joined()
}
