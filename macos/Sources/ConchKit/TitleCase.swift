import Foundation

/// s in Title Case, as the Mac names a menu item, a button or a section:
/// every word capitalized but the short ones inside a name (a, the, and,
/// to, in, of…), which stay small unless they begin or end it. A word
/// already capitalized is left as it is (CI, GitHub, MCP), and so is one
/// that is a name written small — conch, gh, git, ssh — or a path or a
/// file (config.toml, .conch/…).
///
/// For text that comes from elsewhere — the registry's kinds ("Code
/// hosts") — and is shown where the app's own names are in Title Case.
public func titleCase(_ s: String) -> String {
    let small: Set<String> = ["a", "an", "the", "and", "but", "or", "nor", "for", "so", "yet", "as", "at", "by",
                              "in", "of", "on", "to", "up", "via", "with", "from", "into", "over", "per", "vs", "than", "off"]
    let kept: Set<String> = ["conch", "conch's", "gh", "git", "ssh", "npm"]
    let parts = s.split(separator: " ", omittingEmptySubsequences: false).map(String.init)
    let wordAt = parts.indices.filter { parts[$0].contains(where: \.isLetter) }
    var out = parts
    for (n, i) in wordAt.enumerated() {
        let w = parts[i]
        guard let first = w.firstIndex(where: \.isLetter) else { continue }
        let lead = w[..<first]
        let core = String(w[first...]).trimmingCharacters(in: CharacterSet.letters.union(CharacterSet(charactersIn: "'’.-")).inverted)
        if kept.contains(core.lowercased()) || w.contains("/") || core.dropFirst().contains(".") || lead.hasSuffix(".") { continue }
        if w[first].isUppercase { continue }
        if small.contains(core.lowercased()), n != 0, n != wordAt.count - 1 { continue }
        out[i] = String(lead) + w[first].uppercased() + w[w.index(after: first)...]
    }
    return out.joined(separator: " ")
}
