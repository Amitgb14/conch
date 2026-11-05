import Foundation

// The Files tab's editor, what can be tested of it: which language a file
// is, the colouring of its text (comments, strings, numbers, keywords —
// enough to read code by, not a parser), and the checkouts of a project the
// tab can show, so a branch's worktree can be looked at and edited there.

public enum CodeLanguage: String, Sendable, CaseIterable {
    case go, swift, typescript, python, rust, shell, json, yaml, toml, markdown, c, plain

    /// The language of a file by its name.
    public init(path: String) {
        let name = (path as NSString).lastPathComponent.lowercased()
        let ext = (name as NSString).pathExtension
        switch ext {
        case "go": self = .go
        case "swift": self = .swift
        case "ts", "tsx", "js", "jsx", "mjs", "cjs": self = .typescript
        case "py": self = .python
        case "rs": self = .rust
        case "sh", "bash", "zsh": self = .shell
        case "json": self = .json
        case "yml", "yaml": self = .yaml
        case "toml": self = .toml
        case "md", "mdx", "markdown": self = .markdown
        case "c", "h", "cc", "cpp", "hpp", "m", "mm", "java", "kt": self = .c
        default:
            switch name {
            case "makefile", "dockerfile", ".zshrc", ".bashrc", ".env": self = .shell
            default: self = .plain
            }
        }
    }

    public var label: String {
        switch self {
        case .go: return "Go"
        case .swift: return "Swift"
        case .typescript: return "TypeScript"
        case .python: return "Python"
        case .rust: return "Rust"
        case .shell: return "Shell"
        case .json: return "JSON"
        case .yaml: return "YAML"
        case .toml: return "TOML"
        case .markdown: return "Markdown"
        case .c: return "C"
        case .plain: return "Plain text"
        }
    }

    var lineComment: [String] {
        switch self {
        case .go, .swift, .typescript, .rust, .c: return ["//"]
        case .python, .shell, .yaml, .toml: return ["#"]
        default: return []
        }
    }

    var blockComment: Bool { [.go, .swift, .typescript, .rust, .c].contains(self) }

    var keywords: Set<String> {
        switch self {
        case .go: return ["package", "import", "func", "return", "if", "else", "for", "range", "switch", "case", "default", "type", "struct", "interface", "map", "chan", "go", "defer", "var", "const", "nil", "true", "false", "break", "continue", "select", "fallthrough", "goto"]
        case .swift: return ["import", "func", "return", "if", "else", "guard", "for", "in", "while", "switch", "case", "default", "let", "var", "struct", "class", "enum", "protocol", "extension", "public", "private", "fileprivate", "internal", "static", "self", "Self", "nil", "true", "false", "try", "await", "async", "throws", "init", "some", "any", "where", "break", "continue", "defer", "do", "catch", "final", "override", "@MainActor"]
        case .typescript: return ["import", "export", "from", "function", "return", "if", "else", "for", "while", "switch", "case", "default", "const", "let", "var", "class", "interface", "type", "extends", "implements", "new", "this", "null", "undefined", "true", "false", "async", "await", "try", "catch", "throw", "of", "in", "as", "typeof", "break", "continue"]
        case .python: return ["import", "from", "def", "return", "if", "elif", "else", "for", "while", "in", "class", "with", "as", "try", "except", "finally", "raise", "lambda", "None", "True", "False", "and", "or", "not", "is", "pass", "break", "continue", "yield", "async", "await", "self"]
        case .rust: return ["use", "mod", "fn", "return", "if", "else", "for", "in", "while", "loop", "match", "let", "mut", "struct", "enum", "impl", "trait", "pub", "self", "Self", "true", "false", "as", "where", "crate", "super", "async", "await", "move", "ref", "const", "static", "break", "continue"]
        case .shell: return ["if", "then", "else", "elif", "fi", "for", "in", "do", "done", "while", "case", "esac", "function", "return", "export", "local", "echo", "exit"]
        case .c: return ["if", "else", "for", "while", "switch", "case", "default", "return", "struct", "enum", "typedef", "const", "static", "void", "int", "char", "unsigned", "long", "class", "public", "private", "new", "null", "true", "false", "break", "continue", "include"]
        case .json, .yaml, .toml: return ["true", "false", "null"]
        default: return []
        }
    }
}

/// What a run of text is, for its colour.
public enum CodeToken: Equatable, Sendable { case comment, string, number, keyword, heading }

/// The coloured runs of text, as UTF-16 offsets and lengths (what
/// NSTextStorage takes). Text not in a run keeps the plain colour.
public func highlight(_ text: String, _ lang: CodeLanguage) -> [(range: NSRange, token: CodeToken)] {
    guard lang != .plain else { return [] }
    let u = Array(text.utf16)
    var out: [(NSRange, CodeToken)] = []
    var i = 0
    func starts(_ s: String, at j: Int) -> Bool {
        let p = Array(s.utf16)
        return j + p.count <= u.count && Array(u[j..<j + p.count]) == p
    }
    func isIdent(_ c: UInt16) -> Bool { (c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c == 64 }
    let nl: UInt16 = 10
    // Markdown: headings and code spans, line by line.
    if lang == .markdown {
        var start = 0
        while start < u.count {
            var end = start
            while end < u.count && u[end] != nl { end += 1 }
            if end > start && u[start] == 35 { out.append((NSRange(location: start, length: end - start), .heading)) }
            var j = start
            while j < end {
                if u[j] == 96, let close = (j + 1..<end).first(where: { u[$0] == 96 }) {
                    out.append((NSRange(location: j, length: close - j + 1), .string))
                    j = close + 1
                } else { j += 1 }
            }
            start = end + 1
        }
        return out
    }
    let kw = lang.keywords
    while i < u.count {
        let c = u[i]
        if lang.blockComment && starts("/*", at: i) {
            var j = i + 2
            while j < u.count && !starts("*/", at: j) { j += 1 }
            j = min(j + 2, u.count)
            out.append((NSRange(location: i, length: j - i), .comment)); i = j; continue
        }
        if lang.lineComment.contains(where: { starts($0, at: i) }) {
            var j = i
            while j < u.count && u[j] != nl { j += 1 }
            out.append((NSRange(location: i, length: j - i), .comment)); i = j; continue
        }
        if c == 34 || c == 39 || (c == 96 && [.go, .typescript, .shell].contains(lang)) {
            var j = i + 1
            while j < u.count && u[j] != c {
                if u[j] == 92 { j += 1 } // an escape: the next character is part of it
                else if u[j] == nl && c != 96 { break } // a string ends at the line, but for a raw one
                j += 1
            }
            j = min(j + 1, u.count)
            out.append((NSRange(location: i, length: j - i), .string)); i = j; continue
        }
        if c >= 48 && c <= 57, i == 0 || !isIdent(u[i - 1]) {
            var j = i
            while j < u.count && (isIdent(u[j]) || u[j] == 46) { j += 1 }
            out.append((NSRange(location: i, length: j - i), .number)); i = j; continue
        }
        if isIdent(c) {
            var j = i
            while j < u.count && isIdent(u[j]) { j += 1 }
            if let word = String(utf16CodeUnits: Array(u[i..<j]), count: j - i) as String?, kw.contains(word) {
                out.append((NSRange(location: i, length: j - i), .keyword))
            }
            i = j; continue
        }
        i += 1
    }
    return out
}

/// A checkout of a project the Files tab can show: its main checkout or a
/// branch's worktree, by path; a branch with neither is listed to say so.
public struct Checkout: Equatable, Sendable, Identifiable {
    public var branch: String
    public var path: String?
    public var main: Bool
    public var id: String { path ?? "branch:" + branch }
    public var label: String { main ? "\(branch) (main checkout)" : branch }
}

/// The checkouts to choose from: the main checkout first, then each
/// worktree by branch name, then branches checked out nowhere — which can
/// be looked at only once they have a worktree.
public func checkouts(_ p: ProjectInfo) -> [Checkout] {
    let wts = p.worktrees ?? []
    var out: [Checkout] = []
    if let m = wts.first(where: { $0.main == true }) {
        out.append(Checkout(branch: m.branch ?? "(detached)", path: m.path, main: true))
    } else {
        out.append(Checkout(branch: "main checkout", path: p.path, main: true))
    }
    let rest = wts.filter { $0.main != true }.sorted { ($0.branch ?? "") < ($1.branch ?? "") }
    out += rest.map { Checkout(branch: $0.branch ?? "(detached)", path: $0.path, main: false) }
    let shown = Set(out.map(\.branch))
    out += (p.branches ?? []).map(\.name).filter { !shown.contains($0) }.sorted().map { Checkout(branch: $0, path: nil, main: false) }
    return out
}
