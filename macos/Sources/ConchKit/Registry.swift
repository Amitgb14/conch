import Foundation

// The provider registry: provider files others adapted and published
// (github.com/Amitgb14/conch-sandbox-providers). conch reads it — `conch
// sandbox provider search -json` lists it, `install -y NAME` installs one —
// and the Add provider sheet shows what those say.

/// The last time somebody's key listed sandboxes with a provider's file.
public struct RegistryCheck: Decodable, Equatable, Sendable {
    public var date: String
    public var conch: String
    public var sandboxes: Int
}

/// One provider in the registry, as `conch sandbox provider search -json`
/// gives it.
public struct RegistryProvider: Decodable, Equatable, Identifiable, Sendable {
    public var name: String
    public var label: String
    public var apiHost: String
    public var sshHost: String
    public var keyEnv: String
    public var note: String?
    public var docs: String?
    public var adaptedBy: String?
    public var adapted: String?
    public var checked: RegistryCheck?
    /// What this computer has of it: "", "installed", "installed, edited
    /// here", or "a file of your own".
    public var here: String?
    /// False for a file newer than this conch reads.
    public var usable: Bool
    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, label, note, docs, checked, here, usable
        case apiHost = "api_host", sshHost = "ssh_host", keyEnv = "key_env", adaptedBy = "adapted_by", adapted
    }

    public init(name: String, label: String, apiHost: String, sshHost: String, keyEnv: String, usable: Bool = true) {
        self.name = name
        self.label = label
        self.apiHost = apiHost
        self.sshHost = sshHost
        self.keyEnv = keyEnv
        self.usable = usable
    }

    /// Where ssh goes, in words when the host is read from the API.
    public var sshWords: String {
        sshHost.contains("{reply.") ? "the sandbox's own address, as the provider's API gives it" : sshHost
    }

    public var liveCheck: String {
        guard let c = checked else { return "not checked live yet" }
        return "checked \(c.date)"
    }

    /// Whether Install can be pressed for it, and why not.
    public var installBlocker: String? {
        if !usable { return "needs a newer conch" }
        switch here ?? "" {
        case "": return nil
        case "a file of your own": return "you have a provider of this name already"
        default: return "installed"
        }
    }
}

/// Decodes the search's output; nothing when it is not that.
public func registryProviders(_ data: Data) -> [RegistryProvider] {
    (try? JSONDecoder().decode([RegistryProvider].self, from: data)) ?? []
}

/// What `conch sandbox provider publish -json` answers.
public struct PublishResult: Decodable, Equatable, Sendable {
    public var url: String
}
