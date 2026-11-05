import Foundation

// The ssh hosts kept in the TUI's tree (internal/tui/ssh.go, sshhosts.go):
// ui.json's saved_ssh, the plain list older builds read, with a host's
// name and ssh options beside it in ssh_hosts. A saved host is listed under
// CLI while no session to it is running, and a click logs in to it again.

public struct SavedHost: Equatable, Sendable {
    public var host: String
    public var name: String?
    public var args: [String]

    public init(host: String, name: String? = nil, args: [String] = []) {
        self.host = host
        self.name = name
        self.args = args
    }

    public var title: String { (name ?? "").isEmpty ? host : name! }
}

/// The saved hosts in a ui.json, in its order, each once; a word ssh would
/// read as an option rather than a host is left out, as the TUI leaves it.
public func loadSavedHosts(uiJSON data: Data?) -> [SavedHost] {
    guard let data, let ui = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
    let info = ui["ssh_hosts"] as? [String: [String: Any]] ?? [:]
    var out: [SavedHost] = []
    for case let h as String in ui["saved_ssh"] as? [Any] ?? [] {
        let host = h.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty, !host.hasPrefix("-"), !host.contains(" "), !out.contains(where: { $0.host == host }) else { continue }
        out.append(SavedHost(host: host, name: info[host]?["name"] as? String, args: info[host]?["args"] as? [String] ?? []))
    }
    return out
}

/// ui.json with a host saved (its options with it) or, save false,
/// forgotten — the rest of the file as it was.
public func savingHost(_ h: SavedHost, save: Bool = true, in data: Data?) throws -> Data {
    var ui: [String: Any] = [:]
    if let data, !data.isEmpty {
        guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw ConchError(code: "ui.json", message: "ui.json is not what conch writes; not changing it")
        }
        ui = obj
    }
    var saved = (ui["saved_ssh"] as? [String]) ?? []
    var info = ui["ssh_hosts"] as? [String: Any] ?? [:]
    saved.removeAll { $0 == h.host }
    info.removeValue(forKey: h.host)
    if save {
        saved.append(h.host)
        var i: [String: Any] = [:]
        if let n = h.name, !n.isEmpty { i["name"] = n }
        if !h.args.isEmpty { i["args"] = h.args }
        if !i.isEmpty { info[h.host] = i }
    }
    ui["saved_ssh"] = saved
    if info.isEmpty { ui.removeValue(forKey: "ssh_hosts") } else { ui["ssh_hosts"] = info }
    return try JSONSerialization.data(withJSONObject: ui, options: [.prettyPrinted, .sortedKeys])
}

extension PaneInfo {
    /// The host an ssh session is logged in to: the word after `--`, last
    /// on its command, as conch ssh starts one (the TUI's sshTarget).
    public var sshTarget: String? {
        guard isSSH, let c = command, c.count >= 2, c[c.count - 2] == "--" else { return nil }
        return c.last
    }

    /// The ssh options a session was started with, between ssh and `--`.
    public var sshArgs: [String] {
        guard isSSH, let c = command, let dash = c.lastIndex(of: "--"), dash >= 1 else { return [] }
        return Array(c[1..<dash])
    }
}

/// The saved hosts no running session is logged in to.
public func idleSavedHosts(_ saved: [SavedHost], panes: [PaneInfo]) -> [SavedHost] {
    saved.filter { h in !panes.contains { $0.running && $0.sshTarget == h.host } }
}
