import Foundation

// The custom actions in config.toml ([[actions]], internal/config/
// actions.go): a named command of your own on the row menus, run in a
// terminal where the row is — a pane's folder, a branch's worktree, a
// project's checkout, a machine's home — with conch's names for the
// context (CONCH_MACHINE, CONCH_PROJECT, CONCH_BRANCH, CONCH_WORKTREE,
// CONCH_PANE) so one script serves every row.

public struct CustomAction: Equatable, Sendable {
    public var name: String
    public var run: String
    /// The rows it is offered on (pane, branch, project, machine); empty
    /// is all four.
    public var on: [String]
    /// Whether its terminal stays when the command exits; unset is true.
    public var keep: Bool

    public init(name: String, run: String, on: [String] = [], keep: Bool = true) {
        self.name = name
        self.run = run
        self.on = on
        self.keep = keep
    }

    public func offers(_ place: String) -> Bool {
        on.isEmpty || on.contains { $0.trimmingCharacters(in: .whitespaces).lowercased() == place }
    }
}

/// The [[actions]] of a config.toml, in the order written, half-written ones
/// (no name or no command) left out as the TUI leaves them out. It reads
/// what conch writes and people write by hand: basic and literal strings,
/// multi-line ones, a list of strings, booleans and comments.
public func parseActions(toml: String) -> [CustomAction] {
    var out: [CustomAction] = []
    var cur: [String: Any]? = nil
    func flush() {
        if let c = cur, let name = (c["name"] as? String)?.trimmingCharacters(in: .whitespaces), !name.isEmpty,
           let run = c["run"] as? String, !run.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            out.append(CustomAction(name: name, run: run, on: c["on"] as? [String] ?? [], keep: c["keep"] as? Bool ?? true))
        }
        cur = nil
    }
    let lines = toml.components(separatedBy: "\n")
    var i = 0
    while i < lines.count {
        let line = lines[i].trimmingCharacters(in: .whitespaces)
        i += 1
        if line.hasPrefix("[") {
            flush()
            if line.replacingOccurrences(of: " ", with: "").hasPrefix("[[actions]]") { cur = [:] }
            continue
        }
        guard cur != nil, !line.hasPrefix("#"), let eq = line.firstIndex(of: "=") else { continue }
        let k = line[..<eq].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
        var v = String(line[line.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
        // A multi-line string runs on until its closing quotes.
        for q in ["\"\"\"", "'''"] where v.hasPrefix(q) {
            var body = String(v.dropFirst(3))
            while !body.contains(q) && i < lines.count {
                body += "\n" + lines[i]
                i += 1
            }
            if let end = body.range(of: q) { body = String(body[..<end.lowerBound]) }
            if body.hasPrefix("\n") { body.removeFirst() } // TOML trims the first newline
            cur?[k] = q == "\"\"\"" ? unescapeTOML(body) : body
            v = ""
        }
        if v.isEmpty { continue }
        if let s = tomlString(v) { cur?[k] = s }
        else if v.hasPrefix("[") { cur?[k] = tomlStrings(v) }
        else if v.hasPrefix("true") { cur?[k] = true }
        else if v.hasPrefix("false") { cur?[k] = false }
    }
    flush()
    return out
}

/// A one-line TOML string value, its trailing comment dropped; nil when v
/// is not a string.
func tomlString(_ v: String) -> String? {
    guard let q = v.first, q == "\"" || q == "'" else { return nil }
    var out = "", escaped = false
    for ch in v.dropFirst() {
        if q == "\"" && escaped { out.append(ch); escaped = false; continue }
        if q == "\"" && ch == "\\" { out.append(ch); escaped = true; continue }
        if ch == q { return q == "\"" ? unescapeTOML(out) : out }
        out.append(ch)
    }
    return nil // never closed
}

/// ["a", 'b'] as its strings.
func tomlStrings(_ v: String) -> [String] {
    guard let end = v.lastIndex(of: "]") else { return [] }
    let body = v[v.index(after: v.startIndex)..<end]
    var out: [String] = [], cur = "", quote: Character? = nil
    for ch in body {
        if let q = quote {
            if ch == q { out.append(cur); cur = ""; quote = nil } else { cur.append(ch) }
        } else if ch == "\"" || ch == "'" {
            quote = ch
        }
    }
    return out
}

func unescapeTOML(_ s: String) -> String {
    var out = "", it = s.makeIterator()
    while let ch = it.next() {
        guard ch == "\\", let n = it.next() else { out.append(ch); continue }
        switch n {
        case "n": out.append("\n")
        case "t": out.append("\t")
        case "\"": out.append("\"")
        case "\\": out.append("\\")
        default: out.append("\\"); out.append(n)
        }
    }
    return out
}

/// Where an action runs for a row, and what it is told about it.
public struct ActionPlace: Equatable, Sendable {
    public var machine: String
    public var dir: String?
    public var project: String // its name, "" for none
    public var branch: String
    public var pane: String
    /// Outside every project: a machine's own terminal.
    public var loose: Bool

    /// conch's names for the context, empty where this row has none, so a
    /// `set -u` script does not trip over a missing one.
    public var env: [String: String] {
        ["CONCH_MACHINE": machine, "CONCH_PROJECT": project, "CONCH_BRANCH": branch,
         "CONCH_WORKTREE": dir ?? "", "CONCH_PANE": pane]
    }

    /// What its terminal is called after the action's name.
    public var label: String { !branch.isEmpty ? branch : !project.isEmpty ? project : machine }
}

extension Snapshot {
    /// Where an action runs for a row, as the TUI's placeOf: a pane's
    /// folder, a branch's worktree (none when it is not checked out), a
    /// project's checkout, a machine's home. home is the machine's.
    public func actionPlace(_ node: TreeNode, machine m: String, home: String?) -> (place: String, at: ActionPlace)? {
        switch node.kind {
        case .agent(let k), .terminal(let k):
            guard let p = pane(unkey(k).id) else { return nil }
            let pr = p.projectID.flatMap { project($0) }
            return ("pane", ActionPlace(machine: m, dir: p.cwd, project: pr?.name ?? "", branch: p.branch ?? "", pane: p.id, loose: p.projectID == nil))
        case .branch(let pk, let name, _):
            guard let pr = project(unkey(pk).id) else { return nil }
            let w = pr.worktrees?.first { $0.branch == name }?.path
            return ("branch", ActionPlace(machine: m, dir: w, project: pr.name, branch: name, pane: "", loose: false))
        case .project(let pk):
            guard let pr = project(unkey(pk).id) else { return nil }
            let b = pr.worktrees?.first { $0.path == pr.path }?.branch ?? ""
            return ("project", ActionPlace(machine: m, dir: pr.path, project: pr.name, branch: b, pane: "", loose: false))
        case .machine:
            return ("machine", ActionPlace(machine: m, dir: home, project: "", branch: "", pane: "", loose: true))
        default:
            return nil
        }
    }
}

// MARK: tasks from an issue, and later

/// One open GitHub issue of a project (project.issues).
public struct IssueInfo: Decodable, Equatable, Sendable, Identifiable {
    public var number: Int
    public var title: String
    public var url: String?
    public var id: Int { number }
    public init(number: Int, title: String) {
        self.number = number
        self.title = title
    }
}

/// A task sheet's When, as `conch task` takes it: nothing (now), the
/// queue, after other work, again and again, or a time the server keeps
/// (14:05, +30m, tomorrow 9:00 — conch's own words for one, read there).
public func taskWhenArgs(_ when: String) -> [String] {
    let w = when.trimmingCharacters(in: .whitespaces)
    let lower = w.lowercased()
    if w.isEmpty || lower == "now" { return [] }
    if lower == "queue" || lower == "queued" { return ["-queue"] }
    if lower.hasPrefix("after ") { return ["-after", String(w.dropFirst(6)).trimmingCharacters(in: .whitespaces)] }
    if lower.hasPrefix("every ") { return ["-every", String(w.dropFirst(6)).trimmingCharacters(in: .whitespaces)] }
    return ["-at", w]
}

/// `conch task`'s arguments for a sheet that needs the command rather than
/// task.create: an issue, or a time other than now.
public func taskArgs(dir: String, prompt: String, agent: String, branch: String, issue: Int?, when: String) -> [String] {
    var a = ["task", "-cwd", dir, "-agent", agent]
    let b = branch.trimmingCharacters(in: .whitespaces)
    if !b.isEmpty { a += ["-branch", b] }
    if let n = issue, n > 0 { a += ["-issue", String(n)] }
    a += taskWhenArgs(when)
    let p = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
    if !p.isEmpty { a.append(p) }
    return a
}
