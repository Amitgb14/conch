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
    /// What it says before a sandbox is made: a cost or a limit.
    public var note: String?
    /// A file of the person's own (conch sandbox adapt) rather than one
    /// installed from the registry — conch has no provider of its own —
    /// and where the file is.
    public var adapted: Bool?
    public var file: String?
    public var id: String { name }

    /// Whether its row offers Set key…: one that is not ready, which is a
    /// key it lacks. Its key goes in a card of the providers list below,
    /// which a provider has only once added there — so a row that said
    /// "put the key in Settings → Sandboxes" and had nothing to press left
    /// Tensorlake, new and with no card yet, with no way to set one.
    public var offersSetKey: Bool { !ready }

    /// Where its file came from, for its row.
    public var origin: String { adapted == true ? "your file" : "registry" }

    public init(name: String, label: String, ready: Bool, why: String? = nil, sizes: [String]? = nil, note: String? = nil, adapted: Bool? = nil, file: String? = nil) {
        self.name = name
        self.label = label
        self.ready = ready
        self.why = why
        self.sizes = sizes
        self.note = note
        self.adapted = adapted
        self.file = file
    }

    /// How its machines are chosen: an image and numbers, or a named size.
    public var takes: SandboxShape {
        if let s = sizes, !s.isEmpty { return .sizes(s) }
        return .snapshot
    }
}

public enum SandboxShape: Equatable, Sendable {
    case snapshot
    case sizes([String])
}

/// What a provider's file lets be chosen for its sandboxes, as `conch
/// sandbox -provider P settings -json` says it: Settings → Sandboxes and
/// the New Sandbox sheet draw it.
public struct SandboxSettingsInfo: Decodable, Equatable, Sendable {
    public struct Setting: Decodable, Equatable, Sendable {
        public var key: String
        public var label: String
        public var help: String?
        public var `default`: String?
        public var free: Bool?
        public var choices: [Choice]
        /// Why the choices could not be read; a value can still be typed.
        public var error: String?
        public var values: [String] { choices.map(\.value) }
    }
    public struct Choice: Decodable, Equatable, Sendable {
        public var value: String
        public var name: String?
    }
    public struct Size: Decodable, Equatable, Sendable {
        public var names: [String]?
        public var `default`: String?
        public var cpu: [Int]?
        public var memoryGib: [Int]?
        public var diskGib: [Int]?
        enum CodingKeys: String, CodingKey { case names, `default`, cpu; case memoryGib = "memory_gib", diskGib = "disk_gib" }
        /// Limits rather than names.
        public var limits: Bool { (cpu ?? []).count + (memoryGib ?? []).count + (diskGib ?? []).count > 0 }
    }
    public struct Chosen: Decodable, Equatable, Sendable {
        public var region: String?
        public var image: String?
        public var size: String?
        public var cpu: Int?
        public var memory: Int?
        public var disk: Int?
    }
    public var name: String
    public var label: String
    public var ready: Bool
    public var why: String?
    public var settings: [Setting]
    public var size: Size
    public var chosen: Chosen

    public func setting(_ key: String) -> Setting? { settings.first { $0.key == key } }

    /// Whether a provider's card in Settings shows a field of config.toml's
    /// [sandbox.NAME]: what its file declares, and conch's own (the key,
    /// idle stop, prices, what goes in). target and snapshot, which older
    /// conches wrote, show only while they still hold something.
    public func shows(_ key: String, value: String? = nil) -> Bool {
        switch key {
        case "region": return setting("region") != nil
        case "image": return setting("image") != nil
        case "size": return !(size.names ?? []).isEmpty
        case "cpu": return (size.cpu ?? []).count == 2
        case "memory": return (size.memoryGib ?? []).count == 2
        case "disk": return (size.diskGib ?? []).count == 2
        case "target", "snapshot": return !(value ?? "").isEmpty
        default: return true
        }
    }

    /// What a field offers to choose from: a setting's choices, or the
    /// named sizes.
    public func suggestions(_ key: String) -> [String]? {
        switch key {
        case "region", "image":
            guard let s = setting(key), !s.choices.isEmpty else { return nil }
            return s.values
        case "size": return size.names
        default: return nil
        }
    }
}

/// What New Sandbox… asks for.
public struct NewSandboxForm: Equatable, Sendable {
    public var provider: String
    public var label = ""
    /// The image or snapshot to start from.
    public var snapshot = ""
    /// Where it runs, for a provider whose file offers regions.
    public var region = ""
    /// A named size, for a provider whose machines come in them.
    public var size = ""
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
        let r = region.trimmingCharacters(in: .whitespaces)
        if !r.isEmpty { args += ["-region", r] }
        if case .sizes(let names) = p.takes, !size.isEmpty {
            guard names.contains(size) else { return .failure(.problem("size: one of \(names.joined(separator: ", "))")) }
            args += ["-size", size]
        }
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
