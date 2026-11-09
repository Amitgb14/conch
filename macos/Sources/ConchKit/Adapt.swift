import Foundation

// Adapting a sandbox provider conch does not know: `conch sandbox adapt
// -json` says what it is doing a line at a time, and the Add provider sheet
// draws what those lines add up to. The folding is here, where it can be
// tested; the sheet only draws an AdaptProgress.

/// One line of what conch will do with the provider, for checking.
public struct AdaptMapping: Decodable, Equatable, Sendable {
    public var does: String
    public var call: String
    public var reads: String?
    public init(does: String, call: String, reads: String? = nil) {
        self.does = does
        self.call = call
        self.reads = reads
    }
}

/// What checking the file with the key found.
public struct AdaptCheck: Decodable, Equatable, Sendable {
    public var signed: Bool
    public var all: Int?
    public var mine: Int?
    public var why: String?
    public var keyEnv: String?
    enum CodingKeys: String, CodingKey { case signed, all, mine, why, keyEnv = "key_env" }
}

/// A line of `conch sandbox adapt -json`.
public struct AdaptEvent: Decodable, Equatable, Sendable {
    public var event: String
    public var detail: String?
    public var name: String?
    public var pane: String?
    public var path: String?
    public var text: String?
    public var mapping: [AdaptMapping]?
    public var problems: [String]?
    public var check: AdaptCheck?
    public var ok: Bool?

    public init(event: String, detail: String? = nil) {
        self.event = event
        self.detail = detail
    }
}

/// Where adapting has got to.
public struct AdaptProgress: Equatable, Sendable {
    public enum Phase: Equatable, Sendable { case idle, docs, writing, review, failed }
    public var phase: Phase = .idle
    /// The docs conch fetched, and the places it looked that had none.
    public var docs: [String] = []
    public var misses: [String] = []
    public var agent = ""
    public var pane = ""
    /// The latest thing the agent is doing, or asking.
    public var status = ""
    public var blocked = false
    public var file = ""
    public var path = ""
    public var mapping: [AdaptMapping] = []
    public var problems: [String] = []
    public var check: AdaptCheck?
    public var error = ""
    public var next = ""

    public init() {}

    /// Folds a line in. A line that is not an event is ignored rather than
    /// failing the sheet: a newer conch may say more.
    public mutating func take(_ line: String) {
        guard let d = line.data(using: .utf8), let e = try? JSONDecoder().decode(AdaptEvent.self, from: d) else { return }
        take(e)
    }

    public mutating func take(_ e: AdaptEvent) {
        switch e.event {
        case "docs":
            if phase == .idle { phase = .docs }
            if e.ok == true { docs.append(e.detail ?? "") } else if let d = e.detail { misses.append(d) }
        case "agent":
            phase = .writing
            agent = e.name ?? agent
            pane = e.pane ?? pane
            status = e.detail ?? ""
            blocked = false
            problems = []
            error = ""
        case "waiting":
            status = e.detail ?? status
            blocked = false
        case "blocked":
            status = e.detail ?? status
            blocked = true
        case "file":
            file = e.text ?? ""
            path = e.path ?? ""
            mapping = e.mapping ?? []
            phase = .review
        case "problems":
            problems = e.problems ?? []
            phase = .review
        case "check":
            check = e.check
        case "done":
            next = e.detail ?? ""
            phase = .review
        case "error":
            error = e.detail ?? "conch could not adapt it"
            // A file with problems is still worth reviewing and revising.
            if phase != .review { phase = .failed }
        default:
            break
        }
    }

    /// Whether Save can be pressed: a file with nothing wrong with it. A
    /// key check that did not pass is said, but does not stop a save — the
    /// key may simply not be set yet.
    public var canSave: Bool { phase == .review && !file.isEmpty && problems.isEmpty && error.isEmpty }
}

/// What Add provider… asks for, as `conch sandbox adapt` takes it.
public struct AdaptForm: Equatable, Sendable {
    public var url = ""
    public var name = ""
    public var agent = "claude"
    public init() {}

    public func arguments() -> Result<[String], NewSandboxError> {
        let u = url.trimmingCharacters(in: .whitespaces)
        guard let parsed = URL(string: u), let scheme = parsed.scheme, ["http", "https"].contains(scheme), parsed.host != nil else {
            return .failure(.problem("the provider's API or docs address, starting https://"))
        }
        var args = ["sandbox", "adapt", "-json", "-agent", agent.isEmpty ? "claude" : agent]
        let n = name.trimmingCharacters(in: .whitespaces).lowercased()
        if !n.isEmpty {
            guard n.range(of: "^[a-z][a-z0-9-]{1,30}$", options: .regularExpression) != nil else {
                return .failure(.problem("name: lowercase letters, digits and -, starting with a letter"))
            }
            args += ["-name", n]
        }
        return .success(args + [u])
    }
}

/// The provider's name a draft is kept under, from the form or the URL as
/// conch would read it: api.tensorlake.ai is tensorlake.
public func adaptName(_ form: AdaptForm) -> String {
    let n = form.name.trimmingCharacters(in: .whitespaces).lowercased()
    if !n.isEmpty { return n }
    guard let host = URL(string: form.url.trimmingCharacters(in: .whitespaces))?.host?.lowercased() else { return "" }
    let parts = host.split(separator: ".").map(String.init)
    guard parts.count >= 2 else { return "" }
    var name = parts[parts.count - 2]
    if parts.count >= 3, name == "co" || name == "com" { name = parts[parts.count - 3] }
    return name
}
