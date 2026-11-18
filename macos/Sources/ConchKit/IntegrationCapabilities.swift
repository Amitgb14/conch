import Foundation

/// What an integration can do, as the registry works it out from its file
/// (conch-integrations' "What each one can do") and conch says it in
/// `integration search|info -json`, `sandbox providers -json` and
/// `sandbox -provider P settings -json`. Nil — a built-in, an older index or
/// conch — can and takes everything, so what the app offered before stays.
public struct IntegrationCapabilities: Decodable, Equatable, Sendable {
    public struct SSH: Decodable, Equatable, Sendable {
        /// account, authorize or none.
        public var key: String
        public var access: Bool?
        /// fixed, reply or endpoint.
        public var port: String?
    }
    public var operations: [String]
    /// How ssh gets in; nil for a provider that is off (no [ssh] yet).
    public var ssh: SSH?
    /// The placeholders the create call fills: cpu, memory_mb, snapshot, …
    public var create: [String]?
    public var settings: [String]?
    /// names, limits, plan, or nil.
    public var size: String?
    public var lifetime: Bool?
    public var needs: [String]?
    public var detect: [String]?

    public init(operations: [String], ssh: SSH? = nil, create: [String]? = nil, settings: [String]? = nil, size: String? = nil) {
        self.operations = operations
        self.ssh = ssh
        self.create = create
        self.settings = settings
        self.size = size
    }

    public func can(_ op: String) -> Bool { operations.contains(op) }

    /// The page's "What it can do" rows that have something to say: an
    /// import has no operations and needs nothing, and a row reading "can"
    /// and nothing after it is a bug, not a fact. A sandbox always says
    /// how ssh gets in.
    public func facts(sandbox: Bool) -> [(key: String, value: String)] {
        var out: [(String, String)] = []
        if !operations.isEmpty { out.append(("can", operations.joined(separator: ", "))) }
        if sandbox { out.append(("ssh", sshWords)) }
        if let n = needs, !n.isEmpty {
            out.append(("you supply", n.map { ["key": "API key", "base_url": "server address", "user": "user name"][$0] ?? $0 }.joined(separator: ", ")))
        }
        if sandbox, let cr = create, !cr.isEmpty { out.append(("asks for", cr.joined(separator: ", "))) }
        return out
    }

    /// Whether create fills a placeholder. A provider's capabilities with
    /// no create list take nothing.
    public func takes(_ placeholder: String) -> Bool { (create ?? []).contains(placeholder) }

    /// Whether create sends a part of a size — cpu, memory or disk — in
    /// either unit.
    public func takesPart(_ part: String) -> Bool {
        switch part {
        case "memory", "disk": return takes(part + "_gib") || takes(part + "_mb")
        default: return takes(part)
        }
    }

    /// What it can do beyond the usual, for a card: "snapshots, preview links".
    public var extras: [String] {
        [("snapshot_save", "snapshots"), ("preview", "preview links"), ("usage", "usage")].filter { can($0.0) }.map(\.1)
    }

    /// How ssh gets in, in words.
    public var sshWords: String {
        guard let s = ssh else { return "no ssh yet" }
        var w: String
        switch s.key {
        case "authorize": w = "a key conch hands it"
        case "none": w = "a token the provider gives for each connection"
        default: w = "a key on your account there"
        }
        switch s.port {
        case "reply": w += ", on the port its API names"
        case "endpoint": w += ", through the provider's forwarder"
        default: break
        }
        return w
    }
}

extension Optional where Wrapped == IntegrationCapabilities {
    /// Nil can everything: the file decides.
    public func can(_ op: String) -> Bool { map { $0.can(op) } ?? true }
}
