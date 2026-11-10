import Foundation

// The library: MCP servers and skills kept in conch, each given to the
// agents chosen for it — the TUI's Settings → Agents → Shared MCP servers &
// skills, and `conch agent library`. Per machine, since it is the agents'
// folders there it writes to. agent.library reads it (and with set saves a
// new one, with import takes an agent's in); neither writes to any agent.
// agent.library.apply without apply is the plan, with apply the writes,
// with undo the last apply put back.

public struct LibraryServer: Codable, Equatable, Sendable {
    public var name: String
    public var command: String?
    public var args: [String]?
    public var env: [String: String]?
    public var url: String?
    public var headers: [String: String]?
    /// "sse", or nil for streamable HTTP.
    public var transport: String?
    public var agents: [String]
    /// true when the person chose to keep its token in plain text rather
    /// than the Keychain: env and headers may hold it as it is, and it is
    /// written into each agent's file (agent.library.plain.v1).
    public var plain: Bool?

    public init(name: String, command: String? = nil, args: [String]? = nil, env: [String: String]? = nil,
                url: String? = nil, headers: [String: String]? = nil, transport: String? = nil, agents: [String],
                plain: Bool? = nil) {
        self.name = name
        self.command = command
        self.args = args
        self.env = env
        self.url = url
        self.headers = headers
        self.transport = transport
        self.agents = agents
        self.plain = plain
    }

    // An older server writes "agents": null for a server given to no agent
    // (#166), which failed the whole library: none is an empty list.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        command = try c.decodeIfPresent(String.self, forKey: .command)
        args = try c.decodeIfPresent([String].self, forKey: .args)
        env = try c.decodeIfPresent([String: String].self, forKey: .env)
        url = try c.decodeIfPresent(String.self, forKey: .url)
        headers = try c.decodeIfPresent([String: String].self, forKey: .headers)
        transport = try c.decodeIfPresent(String.self, forKey: .transport)
        agents = try c.decodeIfPresent([String].self, forKey: .agents) ?? []
        plain = try c.decodeIfPresent(Bool.self, forKey: .plain)
    }

    /// What it is in a word or two: http, sse, or the command it runs.
    public var what: String {
        if let u = url, !u.isEmpty { return transport == "sse" ? "sse" : "http" }
        return "stdio · " + (command ?? "")
    }

    /// The command line or URL, as the form shows it.
    public var commandLine: String {
        if let u = url, !u.isEmpty { return u }
        return joinArgs([command ?? ""] + (args ?? []))
    }
}

public struct LibrarySkill: Codable, Equatable, Sendable {
    public var name: String
    /// A folder holding a SKILL.md; ~ is the home directory there.
    public var path: String
    public var agents: [String]
    /// Where on GitHub a fetched skill came from (owner/repo/path@ref), to
    /// fetch it again; nil for a folder of the person's own. Carried back
    /// on every save, or saving would forget it.
    public var source: String?

    public init(name: String, path: String, agents: [String], source: String? = nil) {
        self.name = name
        self.path = path
        self.agents = agents
        self.source = source
    }

    // Null or missing agents, as for a server: none.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        path = try c.decode(String.self, forKey: .path)
        agents = try c.decodeIfPresent([String].self, forKey: .agents) ?? []
        source = try c.decodeIfPresent(String.self, forKey: .source)
    }
}

/// agent.library.fetch: a skill downloaded from GitHub into that machine's
/// library, or, for a repository holding several, which they are.
public struct LibraryFetchResult: Decodable, Equatable, Sendable {
    public var name: String?
    public var path: String?
    public var source: String?
    public var found: [String]?
}

/// Whether what was typed is a skill on GitHub — owner/repo[/path][@ref] or
/// a github.com link — rather than a folder on the machine, as the server's
/// ParseSkillSource reads it.
public func isGitHubSkill(_ s: String) -> Bool {
    var t = s.trimmingCharacters(in: .whitespaces)
    if t.isEmpty || t.hasPrefix("/") || t.hasPrefix("~") || t.hasPrefix(".") { return false }
    for p in ["https://github.com/", "http://github.com/", "github.com/"] where t.hasPrefix(p) {
        t = String(t.dropFirst(p.count))
        break
    }
    if t.contains("://") { return false }
    if let at = t.lastIndex(of: "@") { t = String(t[..<at]) }
    let parts = t.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
    guard parts.count >= 2 else { return false }
    let ok = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")
    func name(_ x: String) -> Bool { !x.isEmpty && x.unicodeScalars.allSatisfy(ok.contains) }
    return name(parts[0]) && name(parts[1]) && !parts.dropFirst(2).contains { $0 == ".." || $0 == "." }
}

public struct Library: Codable, Equatable, Sendable {
    public var servers: [LibraryServer]
    public var skills: [LibrarySkill]

    public init(servers: [LibraryServer] = [], skills: [LibrarySkill] = []) {
        self.servers = servers
        self.skills = skills
    }

    // The server writes null for an empty list.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        servers = try c.decodeIfPresent([LibraryServer].self, forKey: .servers) ?? []
        skills = try c.decodeIfPresent([LibrarySkill].self, forKey: .skills) ?? []
    }
}

/// Where one server or skill stands with one agent.
public struct LibraryCell: Decodable, Equatable, Sendable {
    /// "mcp server" or "skill".
    public var kind: String
    public var name: String
    public var agent: String
    /// on, pending, remove, off, theirs, differs or cannot.
    public var state: String
    public var detail: String?

    public init(kind: String, name: String, agent: String, state: String, detail: String? = nil) {
        self.kind = kind
        self.name = name
        self.agent = agent
        self.state = state
        self.detail = detail
    }
}

public enum LibraryKind {
    public static let server = "mcp server"
    public static let skill = "skill"
}

public struct AgentLibraryResult: Decodable, Equatable, Sendable {
    public var library: Library
    public var cells: [LibraryCell]?
    public var agents: [String]
    public var undos: [String]?
    public var imported: [String]?
    public var skipped: [String]?

    public init(library: Library, cells: [LibraryCell]? = nil, agents: [String], undos: [String]? = nil,
                imported: [String]? = nil, skipped: [String]? = nil) {
        self.library = library
        self.cells = cells
        self.agents = agents
        self.undos = undos
        self.imported = imported
        self.skipped = skipped
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        library = try c.decodeIfPresent(Library.self, forKey: .library) ?? Library()
        cells = try c.decodeIfPresent([LibraryCell].self, forKey: .cells)
        agents = try c.decodeIfPresent([String].self, forKey: .agents) ?? []
        undos = try c.decodeIfPresent([String].self, forKey: .undos)
        imported = try c.decodeIfPresent([String].self, forKey: .imported)
        skipped = try c.decodeIfPresent([String].self, forKey: .skipped)
    }

    private enum CodingKeys: String, CodingKey { case library, cells, agents, undos, imported, skipped }

    /// Where name stands with agent; off when the server said nothing.
    public func cell(_ kind: String, _ name: String, _ agent: String) -> LibraryCell {
        cells?.first { $0.kind == kind && $0.name == name && $0.agent == agent }
            ?? LibraryCell(kind: kind, name: name, agent: agent, state: "off")
    }

    /// How many cells applying would change.
    public var pending: Int { (cells ?? []).filter { $0.state == "pending" || $0.state == "remove" }.count }
}

/// One thing a sync or an apply would do, or did.
public struct SyncChange: Decodable, Equatable, Sendable {
    public var agent: String?
    public var kind: String
    public var name: String
    public var path: String?
    /// create, update, link, same, skip or remove.
    public var action: String
    public var detail: String?
    public var done: Bool?
    public var error: String?

    public init(agent: String? = nil, kind: String, name: String, path: String? = nil, action: String,
                detail: String? = nil, done: Bool? = nil, error: String? = nil) {
        self.agent = agent
        self.kind = kind
        self.name = name
        self.path = path
        self.action = action
        self.detail = detail
        self.done = done
        self.error = error
    }
}

public struct AgentSyncResult: Decodable, Equatable, Sendable {
    public var changes: [SyncChange]?
    public var notes: [String]?
    public var applied: Bool?
    public var undone: Bool?
    public var undo: String?

    public init(changes: [SyncChange]? = nil, notes: [String]? = nil, applied: Bool? = nil, undone: Bool? = nil, undo: String? = nil) {
        self.changes = changes
        self.notes = notes
        self.applied = applied
        self.undone = undone
        self.undo = undo
    }

    /// What it writes or wrote: never what is already right or left alone.
    public var writes: [SyncChange] { (changes ?? []).filter { ["create", "update", "link", "remove"].contains($0.action) } }
    public var failed: [SyncChange] { (changes ?? []).filter { !($0.error ?? "").isEmpty } }
}

/// One change, as a line of the question asked before an apply.
public func syncLine(_ c: SyncChange) -> String {
    var s = "\(c.action) \(c.kind) \(c.name)"
    if let a = c.agent, !a.isEmpty { s += " for \(a)" }
    if let p = c.path, !p.isEmpty { s += "\n    \(p)" }
    return s
}

// MARK: - Cells

/// A cell's mark, as the TUI draws it.
public func libraryMark(_ state: String) -> String {
    switch state {
    case "on": return "✓"
    case "pending": return "+"
    case "remove": return "−"
    case "differs": return "!"
    case "cannot": return "✗"
    case "theirs": return "○"
    default: return "·"
    }
}

/// What a cell's state means, for its tooltip.
public func libraryMeaning(_ c: LibraryCell) -> String {
    let base: String
    switch c.state {
    case "on": base = "has it"
    case "pending": base = "Apply gives it"
    case "remove": base = "Apply takes conch's copy out"
    case "differs": base = "declared by hand, differently: left alone"
    case "cannot": base = "conch cannot write it for this agent"
    case "theirs": base = "set by hand: left alone"
    default: base = "off"
    }
    if let d = c.detail, !d.isEmpty { return base + " — " + d }
    return base
}

/// Short column headings: five full names do not fit beside a name.
public func libraryAgentShort(_ a: String) -> String {
    let short = ["claude": "Cl", "codex": "Cx", "gemini": "Gm", "opencode": "Oc", "devin": "Dv"]
    if let s = short[a] { return s }
    return String(a.prefix(2))
}

/// agents, in the order the library lists them.
func ordered(_ agents: Set<String>, by order: [String]) -> [String] {
    order.filter { agents.contains($0) } + agents.subtracting(order).sorted()
}

extension Library {
    /// The library with agent turned on or off for one server or skill. A
    /// name it lacks leaves it as it is.
    public func toggling(_ kind: String, _ name: String, _ agent: String, order: [String]) -> Library {
        var lib = self
        func flip(_ have: [String]) -> [String] {
            var set = Set(have)
            if set.contains(agent) { set.remove(agent) } else { set.insert(agent) }
            return ordered(set, by: order)
        }
        if kind == LibraryKind.server, let i = lib.servers.firstIndex(where: { $0.name == name }) {
            lib.servers[i].agents = flip(lib.servers[i].agents)
        } else if kind == LibraryKind.skill, let i = lib.skills.firstIndex(where: { $0.name == name }) {
            lib.skills[i].agents = flip(lib.skills[i].agents)
        }
        return lib
    }

    /// The library without that server or skill; Apply takes it out of the
    /// agents that have conch's copy.
    public func removing(_ kind: String, _ name: String) -> Library {
        var lib = self
        if kind == LibraryKind.server { lib.servers.removeAll { $0.name == name } }
        if kind == LibraryKind.skill { lib.skills.removeAll { $0.name == name } }
        return lib
    }

    /// The library with a skill added, or replacing one of that name (or
    /// the one called replacing, when its name changed). A skill with no
    /// name is named after its folder.
    public func addingSkill(name: String, path: String, agents: [String], replacing old: String? = nil, source: String? = nil) throws -> Library {
        let path = path.trimmingCharacters(in: .whitespaces)
        var name = name.trimmingCharacters(in: .whitespaces)
        guard !path.isEmpty else { throw LibraryFormError("a skill needs the folder its SKILL.md is in") }
        if name.isEmpty { name = ((path as NSString).standardizingPath as NSString).lastPathComponent }
        var lib = self
        lib.skills.removeAll { $0.name == name || $0.name == old }
        lib.skills.append(LibrarySkill(name: name, path: path, agents: agents, source: source))
        return lib
    }

    /// The library with a server added, or replacing one: commandOrURL is a
    /// URL (http or https) or a command line, env and headers NAME=value
    /// pairs separated by ;.
    public func addingServer(name: String, commandOrURL: String, env: String, headers: String, sse: Bool,
                             agents: [String], replacing old: String? = nil) throws -> Library {
        let name = name.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { throw LibraryFormError("a server needs a name") }
        let words = splitArgs(commandOrURL)
        guard let first = words.first, !first.isEmpty else { throw LibraryFormError("a server needs a command to run, or a URL") }
        var s = LibraryServer(name: name, agents: agents)
        if words.count == 1, first.hasPrefix("http://") || first.hasPrefix("https://") {
            s.url = first
            s.transport = sse ? "sse" : nil
        } else {
            s.command = first
            s.args = words.count > 1 ? Array(words.dropFirst()) : nil
        }
        s.env = try parsePairs(env)
        s.headers = try parsePairs(headers)
        var lib = self
        lib.servers.removeAll { $0.name == name || $0.name == old }
        lib.servers.append(s)
        return lib
    }

    /// conch's own tools (`conch mcp`) as a server, for these agents.
    public func withConchTools(agents: [String]) -> Library {
        var lib = self
        lib.servers.removeAll { $0.name == "conch" }
        lib.servers.append(LibraryServer(name: "conch", command: "conch", args: ["mcp"], agents: agents))
        return lib
    }

    public var hasConchTools: Bool { servers.contains { $0.name == "conch" } }
}

public struct LibraryFormError: Error, Equatable, CustomStringConvertible {
    public var description: String
    public init(_ d: String) { description = d }
}

/// "K=V; K2=V2" as a map; nil when empty.
public func parsePairs(_ s: String) throws -> [String: String]? {
    var out: [String: String] = [:]
    for part in s.split(separator: ";") {
        let p = part.trimmingCharacters(in: .whitespaces)
        if p.isEmpty { continue }
        guard let eq = p.firstIndex(of: "=") else { throw LibraryFormError("\"\(p)\" is not NAME=value") }
        let k = p[..<eq].trimmingCharacters(in: .whitespaces)
        guard !k.isEmpty else { throw LibraryFormError("\"\(p)\" is not NAME=value") }
        out[k] = p[p.index(after: eq)...].trimmingCharacters(in: .whitespaces)
    }
    return out.isEmpty ? nil : out
}

public func joinPairs(_ m: [String: String]?) -> String {
    (m ?? [:]).keys.sorted().map { "\($0)=\(m![$0]!)" }.joined(separator: "; ")
}

/// A command line split on spaces, "quoted words" kept whole.
public func splitArgs(_ s: String) -> [String] {
    var out: [String] = []
    var cur = ""
    var inQuote = false, quoted = false
    for ch in s {
        if ch == "\"" {
            inQuote.toggle()
            quoted = true
        } else if ch == " " && !inQuote {
            if !cur.isEmpty || quoted { out.append(cur) }
            cur = ""
            quoted = false
        } else {
            cur.append(ch)
        }
    }
    if !cur.isEmpty || quoted { out.append(cur) }
    return out
}

/// splitArgs the other way: a word with a space is quoted.
public func joinArgs(_ words: [String]) -> String {
    words.map { $0.isEmpty || $0.contains(" ") ? "\"\($0)\"" : $0 }.joined(separator: " ")
}

// MARK: tokens kept in the Keychain (agent.library.keychain.v1)

/// The variable a server's token is kept under, in the Keychain and in the
/// environment of the agents conch starts: CONCH_MCP_<NAME>_TOKEN.
public func tokenVariable(for server: String) -> String {
    let name = server.uppercased().map { $0.isASCII && ($0.isLetter || $0.isNumber) ? String($0) : "_" }.joined()
    return "CONCH_MCP_\(name)_TOKEN"
}

/// A server and the token to keep for it: the token typed in the form, or
/// one pasted into a header, which is taken out of the header and its place
/// given to the variable — conch never writes a token into an agent's file.
public struct ServerWithToken: Equatable, Sendable {
    public var server: LibraryServer
    /// What to keep in the Keychain under variable; nil when nothing is.
    public var token: String?
    public var variable: String
}

public func keepingToken(_ s: LibraryServer, token typed: String) throws -> ServerWithToken {
    var s = s
    let variable = tokenVariable(for: s.name)
    var token = typed.trimmingCharacters(in: .whitespacesAndNewlines)
    func isRef(_ v: String) -> Bool { v.range(of: #"^(?:[A-Za-z]+ )?\$\{[A-Za-z_][A-Za-z0-9_]*\}$"#, options: .regularExpression) != nil }
    // A token pasted where a reference goes: Authorization=Bearer abc…, or
    // X-API-Key=abc…. One is taken; two are more than one token.
    var headers = s.headers ?? [:]
    let literal = headers.filter { !$0.value.trimmingCharacters(in: .whitespaces).isEmpty && !isRef($0.value.trimmingCharacters(in: .whitespaces)) }
    if literal.count > 1 {
        throw LibraryFormError("\(literal.keys.sorted().joined(separator: " and ")) both hold a value: put the token in the Token field, and refer to it as ${\(variable)}")
    }
    if let (k, v) = literal.first {
        let parts = v.trimmingCharacters(in: .whitespaces).split(separator: " ", maxSplits: 1).map(String.init)
        let scheme = parts.count == 2 && ["bearer", "token", "basic"].contains(parts[0].lowercased()) ? parts[0] : nil
        let pasted = scheme == nil ? v.trimmingCharacters(in: .whitespaces) : parts[1]
        if !token.isEmpty && token != pasted {
            throw LibraryFormError("\(k) holds a token and the Token field another: keep one")
        }
        token = pasted
        headers[k] = (scheme.map { $0 + " " } ?? "") + "${\(variable)}"
    }
    guard !token.isEmpty else { return ServerWithToken(server: s, token: nil, variable: variable) }
    guard !token.contains(where: { $0 == "\"" || $0 == "\\" || $0.isNewline }) else {
        throw LibraryFormError("the token has a quote, a backslash or a line break in it, which the Keychain is not given here")
    }
    let named = "${\(variable)}"
    if s.url != nil {
        // A URL's token goes in its Authorization header unless a header
        // already names it.
        if !headers.values.contains(where: { $0.contains(named) }) { headers["Authorization"] = "Bearer " + named }
        s.headers = headers
    } else if !(s.env ?? [:]).values.contains(where: { $0.contains(named) }) && !(s.args ?? []).contains(where: { $0.contains(named) }) {
        throw LibraryFormError("say where the command reads the token: in Environment, e.g. API_KEY=\(named)")
    }
    return ServerWithToken(server: s, token: token, variable: variable)
}

/// The server with its token written in where the variable was, as the
/// person chose rather than the Keychain: what agents' files will hold.
public func writingTokenPlain(_ kept: ServerWithToken) -> LibraryServer {
    var s = kept.server
    guard let token = kept.token else { return s }
    let named = "${\(kept.variable)}"
    s.headers = s.headers?.mapValues { $0.replacingOccurrences(of: named, with: token) }
    s.env = s.env?.mapValues { $0.replacingOccurrences(of: named, with: token) }
    s.args = s.args?.map { $0.replacingOccurrences(of: named, with: token) }
    s.plain = true
    return s
}

/// A plain server as the form shows it: the token taken back out of its
/// header into the Token field, never shown in the clear.
public func formOfPlain(_ s: LibraryServer) -> ServerWithToken {
    (try? keepingToken(s, token: "")) ?? ServerWithToken(server: s, token: nil, variable: tokenVariable(for: s.name))
}

extension LibraryServer {
    /// Whether it refers to the variable conch keeps its token under.
    public var usesKeptToken: Bool {
        let named = "${\(tokenVariable(for: name))}"
        return (headers ?? [:]).values.contains { $0.contains(named) } || (env ?? [:]).values.contains { $0.contains(named) }
            || (args ?? []).contains { $0.contains(named) }
    }
}
