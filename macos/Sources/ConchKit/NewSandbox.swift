import Foundation

/// A sandbox provider as `conch sandbox providers -json` gives it: what the
/// New Sandbox sheet needs to offer it — whether it has its key here, and
/// what goes where a snapshot does.
public struct SandboxProviderInfo: Decodable, Equatable, Sendable, Identifiable {
    public var name: String
    public var label: String
    public var ready: Bool
    /// What is missing when it isn't ready: its API key.
    public var why: String?
    /// Named sizes, smallest first, for a provider that takes no numbers.
    public var sizes: [String]?
    /// What its snapshot field takes instead, for one that places machines.
    public var place: String?
    /// What it says before a sandbox is made: a cost or a limit.
    public var note: String?
    public var id: String { name }

    public init(name: String, label: String, ready: Bool, why: String? = nil, sizes: [String]? = nil, place: String? = nil, note: String? = nil) {
        self.name = name
        self.label = label
        self.ready = ready
        self.why = why
        self.sizes = sizes
        self.place = place
        self.note = note
    }

    /// How its machines are chosen: a snapshot and numbers, a named size,
    /// or a place.
    public var takes: SandboxShape {
        if let s = sizes, !s.isEmpty { return .sizes(s) }
        if let p = place, !p.isEmpty { return .place(p) }
        return .snapshot
    }
}

public enum SandboxShape: Equatable, Sendable {
    case snapshot
    case sizes([String])
    case place(String)
}

/// What New Sandbox… asks for.
public struct NewSandboxForm: Equatable, Sendable {
    public var provider: String
    public var label = ""
    /// The snapshot, the size's name or the place: whichever the provider takes.
    public var snapshot = ""
    public var cpu = ""
    public var memory = ""
    public var disk = ""

    public init(provider: String) { self.provider = provider }

    /// The arguments after `conch`, as the TUI's New sandbox dialog runs
    /// it, or why the form can't be sent. Numbers are only for a provider
    /// that takes them; it refuses them otherwise.
    public func arguments(for p: SandboxProviderInfo) -> Result<[String], NewSandboxError> {
        guard p.name == provider else { return .failure(.problem("pick a provider")) }
        guard p.ready else { return .failure(.problem(p.why ?? "\(p.label) isn't set up here")) }
        var args = ["sandbox", "-provider", provider, "create"]
        let l = label.trimmingCharacters(in: .whitespaces)
        if !l.isEmpty { args += ["-label", l] }
        let s = snapshot.trimmingCharacters(in: .whitespaces)
        if !s.isEmpty { args += ["-snapshot", s] }
        if case .snapshot = p.takes {
            for (flag, value, what) in [("-cpu", cpu, "vCPUs"), ("-memory", memory, "memory"), ("-disk", disk, "disk")] {
                let v = value.trimmingCharacters(in: .whitespaces)
                if v.isEmpty { continue }
                guard let n = Int(v), n > 0 else { return .failure(.problem("\(what): a whole number above 0, or empty for the snapshot's")) }
                args += [flag, String(n)]
            }
        }
        return .success(args)
    }
}

public enum NewSandboxError: Error, Equatable, Sendable {
    case problem(String)
    public var message: String { if case .problem(let m) = self { return m }; return "" }
}
