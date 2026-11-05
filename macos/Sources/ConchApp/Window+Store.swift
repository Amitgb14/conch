import AppKit
import ConchKit

// What the window's newer parts ask of the store: the gap between splits,
// a listening port opened. The scratch layer is Scratch.swift's.

extension Store {
    /// The gap between splits and round them: the style's, narrower when
    /// [app] density is compact.
    var gutter: CGFloat {
        let g = CGFloat(Theme.style.gutter)
        return appLook.compact ? min(g, 3) : g
    }

    /// A port a pane listens on: opened in the browser when it is this
    /// computer's; on another machine, the ssh tunnel that brings it here
    /// is copied, since a remote port is not reachable as it is.
    func openPort(_ port: Int, machine: String) {
        if machine == "local" {
            if let u = URL(string: "http://localhost:\(port)") { NSWorkspace.shared.open(u) }
            return
        }
        let home = ProcessInfo.processInfo.environment["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 }
            ?? (NSHomeDirectory() as NSString).appendingPathComponent(".config/conch")
        let entries = MachineEntry.load(machinesJSON: FileManager.default.contents(atPath: (home as NSString).appendingPathComponent("machines.json")))
        let host = entries.first { $0.id == machine }?.target ?? machine
        let cmd = forwardCommand(host: host, port: port)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(cmd, forType: .string)
        say("Copied \(cmd) — run it, then open http://localhost:\(port)")
    }
}

extension Store {
    /// A pane's screen as text, for a preview with no terminal of its own.
    func screenText(_ k: String) async -> [String] {
        let (machine, id) = unkey(k)
        guard let c = connection(machine) else { return [] }
        struct Read: Decodable { var lines: [String]? }
        struct Ref: Encodable { var id: String }
        return (try? await c.call("pane.read", Ref(id: id), as: Read.self))?.lines ?? []
    }
}

extension Store {
    /// What can be brought up to date, this computer first (Updates.swift).
    var updates: [UpdateCandidate] {
        updateCandidates(machines.map { (id: $0.id, label: $0.label, online: $0.online, behind: $0.behind) })
    }

    /// Does the ones picked in the updates sheet: this computer's server
    /// reloaded, other machines given this build. Each is a job of its own,
    /// said in the status bar as it runs and when it is done or why not.
    func applyUpdates(_ picked: Set<String>) {
        for u in updates where picked.contains(u.machine) {
            switch u.action {
            case .reload: reloadServer(u.machine)
            case .upgrade: upgrade(u.machine)
            }
        }
    }
}
