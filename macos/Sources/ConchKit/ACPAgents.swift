import Foundation

// Agents that speak the Agent Client Protocol, as Settings › Agents shows
// them and the Add an ACP Agent sheet adds them: the server on the machine
// picked writes the manifest and offers the agent at once
// (agent.acp.add.v1). What can be decided without AppKit is here.

/// The capability a server adds, checks and removes ACP agents with.
public let capAgentACPAdd = "agent.acp.add.v1"

/// An ACP agent as its manifest's [acp] table says it.
public struct ACPSpec: Codable, Equatable, Sendable {
    public var name: String
    public var label: String?
    public var command: String
    public var dirs: [String]?
    public var env: [String]?

    public init(name: String, label: String? = nil, command: String, dirs: [String]? = nil, env: [String]? = nil) {
        self.name = name
        self.label = label
        self.command = command
        self.dirs = dirs
        self.env = env
    }
}

/// agent.acp.add's params: the spec, flat, and whether it changes one.
public struct ACPAddParams: Encodable, Equatable, Sendable {
    public var spec: ACPSpec
    public var replace: Bool

    public init(spec: ACPSpec, replace: Bool) {
        self.spec = spec
        self.replace = replace
    }

    enum CodingKeys: String, CodingKey { case name, label, command, dirs, env, replace }

    public func encode(to e: Encoder) throws {
        var c = e.container(keyedBy: CodingKeys.self)
        try c.encode(spec.name, forKey: .name)
        try c.encodeIfPresent(spec.label, forKey: .label)
        try c.encode(spec.command, forKey: .command)
        try c.encodeIfPresent(spec.dirs, forKey: .dirs)
        try c.encodeIfPresent(spec.env, forKey: .env)
        if replace { try c.encode(true, forKey: .replace) }
    }
}

/// What agent.acp.check found: problem is nil when it speaks ACP.
public struct ACPCheck: Decodable, Equatable, Sendable {
    public var problem: String?
    public var message: String?
    public var output: String?
    public var path: String?
    public var agent: String?
    public var version: String?
    public var protocolVersion: Int?
    public var load: Bool?
    public var list: Bool?
    public var auth: [String]?
    /// How to install a command conch knows that was not found; none
    /// from an older server.
    public var install: String?

    enum CodingKeys: String, CodingKey {
        case problem, message, output, path, agent, version, load, list, auth, install
        case protocolVersion = "protocol_version"
    }

    public init(problem: String? = nil, message: String? = nil, output: String? = nil, path: String? = nil, agent: String? = nil,
                version: String? = nil, protocolVersion: Int? = nil, load: Bool? = nil, list: Bool? = nil, auth: [String]? = nil,
                install: String? = nil) {
        self.install = install
        self.problem = problem
        self.message = message
        self.output = output
        self.path = path
        self.agent = agent
        self.version = version
        self.protocolVersion = protocolVersion
        self.load = load
        self.list = list
        self.auth = auth
    }

    public var speaksACP: Bool { (problem ?? "").isEmpty }
}

/// Commands people reach for, offered as picks in the sheet. Each one's
/// flag is the agent's to change: the sheet says to check its --help.
/// (Checked 2026-11-26: Gemini CLI 0.71.1 deprecated --experimental-acp
/// for --acp; the Claude Code and Codex adapters are now
/// @agentclientprotocol's, Claude's binary renamed claude-agent-acp.)
public let knownACPCommands = ["gemini --acp", "goose acp", "claude-agent-acp", "codex-acp"]

/// The name a command suggests: its program, and "-acp".
public func acpName(for command: String) -> String {
    var words = command.split(whereSeparator: \.isWhitespace).map(String.init)
    while words.count > 1, words[0].contains("=") { words.removeFirst() }
    guard let first = words.first else { return "" }
    var out = ""
    for ch in (first as NSString).lastPathComponent.lowercased() {
        if ch.isASCII && (ch.isLetter || ch.isNumber) {
            out.append(ch)
        } else if !out.isEmpty && !out.hasSuffix("-") {
            out.append("-")
        }
    }
    while out.hasSuffix("-") { out.removeLast() }
    if out.isEmpty { return "" }
    return out.hasSuffix("-acp") ? out : out + "-acp"
}

/// The sheet's fields, and what is wrong with them before anything is sent.
public struct ACPForm: Equatable, Sendable {
    public var command = ""
    public var name = ""
    public var label = ""
    /// One folder a line, and one NAME=value a line.
    public var dirs = ""
    public var env = ""
    /// The agent being changed, whose name stays: another name would be
    /// another agent.
    public var editing: String?

    public init() {}

    public init(_ a: AgentAvailability) {
        let s = a.acp ?? ACPSpec(name: a.name, command: "")
        command = s.command
        name = a.name
        label = s.label ?? a.label ?? ""
        dirs = (s.dirs ?? []).joined(separator: "\n")
        env = (s.env ?? []).joined(separator: "\n")
        editing = a.name
    }

    static func lines(_ s: String) -> [String] {
        s.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    /// The name the agent will have: the one typed, the edited one's, or
    /// the command's.
    public var resolvedName: String {
        if let editing { return editing }
        let n = name.trimmingCharacters(in: .whitespaces)
        return n.isEmpty ? acpName(for: command) : n
    }

    public var spec: ACPSpec {
        let l = label.trimmingCharacters(in: .whitespaces)
        let d = Self.lines(dirs), e = Self.lines(env)
        return ACPSpec(name: resolvedName, label: l.isEmpty ? nil : l, command: command.trimmingCharacters(in: .whitespaces),
                       dirs: d.isEmpty ? nil : d, env: e.isEmpty ? nil : e)
    }

    /// Why Add cannot be pressed yet, or nil. supported names the agents
    /// conch supports, whose names a manifest may not take.
    public func problem(supported: Set<String>) -> String? {
        let s = spec
        if s.command.isEmpty { return "A command is needed: the one that speaks ACP on its standard input and output." }
        if s.name.isEmpty || !s.name.allSatisfy({ $0.isASCII && ($0.isLowercase || $0.isNumber || $0 == "-") }) {
            return "The name takes lower-case letters, digits and dashes."
        }
        if editing == nil && supported.contains(s.name) {
            return "\(s.name) is a supported agent: pick another name — \(s.name)-acp is free."
        }
        for e in s.env ?? [] {
            let parts = e.split(separator: "=", maxSplits: 1)
            if parts.count < 2 && !e.hasSuffix("=") || e.hasPrefix("=") || (parts.first ?? "").contains(" ") {
                return "“\(e)” is not NAME=value."
            }
        }
        return nil
    }
}

/// One line of what a check found.
public struct ACPCheckLine: Equatable, Sendable {
    public enum Mark: Equatable, Sendable { case yes, no, warn }
    public var mark: Mark
    public var what: String
    public var why: String
}

/// The headline of a check: what speaks, or what went wrong.
public func acpCheckHeadline(_ c: ACPCheck) -> String {
    if !c.speaksACP {
        switch c.problem {
        // The message begins with the program's name, kept as typed.
        case "not_found": return c.message ?? "It was not found"
        case "exited": return "It exited while starting"
        case "not_acp": return "It does not speak ACP"
        default: return c.message ?? "It could not be checked"
        }
    }
    var who = [c.agent, c.version].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " ")
    if who.isEmpty { who = "it" }
    return "It speaks ACP · \(who) · protocol version \(c.protocolVersion ?? 1)"
}

/// What the check's box lists under its headline when it speaks ACP.
public func acpCheckLines(_ c: ACPCheck) -> [ACPCheckLine] {
    guard c.speaksACP else { return [] }
    var out = [
        ACPCheckLine(mark: .yes, what: "Takes a first message", why: "tasks and handoffs can start it"),
        c.load == true ? ACPCheckLine(mark: .yes, what: "Reopens a session", why: "loadSession: Resume works")
            : ACPCheckLine(mark: .no, what: "Does not reopen sessions", why: "no loadSession: Resume is refused"),
        c.list == true ? ACPCheckLine(mark: .yes, what: "Lists its sessions", why: "session/list: they show in Sessions")
            : ACPCheckLine(mark: .no, what: "Does not list its sessions", why: "Sessions says so for it"),
    ]
    if let auth = c.auth, !auth.isEmpty {
        out.append(ACPCheckLine(mark: .warn, what: "May ask to sign in", why: auth.joined(separator: ", ")))
    } else {
        out.append(ACPCheckLine(mark: .yes, what: "No sign-in asked", why: "it offers none now"))
    }
    return out
}

/// What Settings says under an ACP agent's name.
public func acpAgentNote(_ a: AgentAvailability) -> String {
    var note = a.sessions == true ? "Reopens and lists its sessions" : "Its sessions are not listed"
    if !a.installed { note += " · not installed" }
    return note
}

public extension AgentAvailability {
    /// Whether it runs over ACP.
    var isACP: Bool { proto == "acp" }
    /// What a menu calls it: an ACP agent says so.
    var menuTitle: String { isACP ? "\(title) — ACP" : title }
}

/// The agent an ACP command runs, for drawing its mark: "claude" for
/// Claude's adapter, "codex" for Codex's, the agent itself for one that
/// speaks ACP with a flag; nil for a program conch does not know, which
/// is drawn with a plug. The same table as the server's proto.ACPBaseAgent.
public func acpBaseAgent(command: String) -> String? {
    var words = command.split(whereSeparator: \.isWhitespace).map(String.init)
    while words.count > 1, words[0].contains("=") { words.removeFirst() }
    guard let first = words.first else { return nil }
    let prog = (first as NSString).lastPathComponent
    switch prog {
    case "claude-agent-acp", "claude-code-acp": return "claude"
    case "codex-acp": return "codex"
    case "gemini", "goose", "qwen", "opencode", "kimi", "cline", "copilot": return prog
    default: return nil
    }
}

/// The mark for agent, given the agents the machines report: the agent
/// itself when it is not run over ACP; the agent behind it when it is;
/// acpPlugMark for an ACP agent conch does not know.
public let acpPlugMark = "acp-plug"

public func markAgent(_ agent: String, acp: [AgentAvailability]) -> String {
    guard let a = acp.first(where: { $0.name == agent && $0.isACP }) else { return agent }
    return a.acp.flatMap { acpBaseAgent(command: $0.command) } ?? acpPlugMark
}

/// The agents a menu offers, in two parts: the ones with interfaces of
/// their own, then the ones over ACP, each part in the order given.
public func agentMenuGroups(_ agents: [AgentAvailability]) -> (native: [AgentAvailability], acp: [AgentAvailability]) {
    (agents.filter { !$0.isACP }, agents.filter(\.isACP))
}
