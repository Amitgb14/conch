import Foundation

// Settings → Remote machines lists the machines conch reaches, as Phone
// lists the phones paired: this computer first, then machines.json's in
// its order, each with how it is reached and how it is now.

/// One machine's row in Settings → Remote machines.
public struct MachineRow: Equatable, Sendable {
    public var id: String
    public var label: String
    /// How it is reached: its ssh target, a sandbox's provider, or "this
    /// computer".
    public var detail: String
    /// How it is now, in a word or two: online, connecting, offline,
    /// turned off.
    public var status: String
    /// Why, when it is not online: the bridge's or the socket's own words.
    public var why: String
    public var online: Bool
    /// Whether conch reaches it at all: machines.json can list one turned off.
    public var enabled: Bool
    /// conch's version there, once it has answered.
    public var version: String
    /// Capabilities it lacks that this app's conch has: an older build,
    /// which a reload (here) or an upgrade (elsewhere) brings up to date.
    public var behind: Int
    public var isLocal: Bool { id == "local" }

    public init(id: String, label: String, detail: String, status: String, why: String = "", online: Bool,
                enabled: Bool = true, version: String = "", behind: Int = 0) {
        self.id = id
        self.label = label
        self.detail = detail
        self.status = status
        self.why = why
        self.online = online
        self.enabled = enabled
        self.version = version
        self.behind = behind
    }
}

/// How a machine's connection is now, as the window's sidebar holds it.
public struct MachineState: Equatable, Sendable {
    /// "" when online; otherwise what it is doing or why it is not.
    public var state: String
    public var why: String
    public var version: String
    public var behind: Int

    public init(state: String, why: String = "", version: String = "", behind: Int = 0) {
        self.state = state
        self.why = why
        self.version = version
        self.behind = behind
    }
}

/// The rows: this computer, then every machine in machines.json — turned
/// off ones too, which the sidebar leaves out — with the state of those
/// the window is connected to.
public func machineRows(catalog: [MachineEntry], states: [String: MachineState]) -> [MachineRow] {
    func row(_ id: String, label: String, detail: String, enabled: Bool) -> MachineRow {
        guard enabled else {
            return MachineRow(id: id, label: label, detail: detail, status: "turned off", online: false, enabled: false)
        }
        guard let s = states[id] else {
            return MachineRow(id: id, label: label, detail: detail, status: "connecting", online: false)
        }
        // Not online: what the sidebar says — connecting, offline, or a
        // sandbox's own state (stopped).
        let status = s.state.isEmpty ? (s.behind > 0 ? "older build" : "online") : s.state
        return MachineRow(id: id, label: label, detail: detail, status: status, why: s.state.isEmpty ? "" : s.why,
                          online: s.state.isEmpty, version: s.version, behind: s.behind)
    }
    var out = [row("local", label: "This Mac", detail: "this computer", enabled: true)]
    for e in catalog {
        let detail = e.sandboxProvider.map { "\($0) sandbox" } ?? (e.target ?? "")
        out.append(row(e.id, label: e.label, detail: detail, enabled: e.enabled != false))
    }
    return out
}
