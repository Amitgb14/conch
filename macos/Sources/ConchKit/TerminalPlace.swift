import Foundation

// Where a new terminal starts: where the tab in front is. Not every tab is
// a pane's "machine|id" — read as one, a browser's "web|url" is on a
// machine "web", which nobody has.

/// A new terminal's machine, and what on it says its folder: a pane's, a
/// folder itself, or a project's.
public struct TerminalPlace: Equatable, Sendable {
    public var machine: String
    public var pane: String?
    public var project: String?
    public var dir: String?

    public init(machine: String, pane: String? = nil, project: String? = nil, dir: String? = nil) {
        self.machine = machine
        self.pane = pane
        self.project = project
        self.dir = dir
    }
}

/// Where a terminal opened from focused starts. tab is what shares its
/// tab, agent the workbench's and window the project whose window it is: a
/// page has no place of its own, so it takes theirs, else this Mac's home.
public func terminalPlace(focused: String?, tab: [String], agent: String? = nil, window: String? = nil) -> TerminalPlace {
    if let k = focused, parseBrowserKey(k) == nil {
        return placeOf(k)
    }
    if focused != nil, let beside = agent ?? tab.first(where: { parseBrowserKey($0) == nil }) {
        return placeOf(beside)
    }
    guard let window else { return TerminalPlace(machine: "local") }
    let (m, id) = unkey(window)
    return TerminalPlace(machine: m, project: id)
}

private func placeOf(_ k: String) -> TerminalPlace {
    if let agent = parseChatKey(k) ?? parseBenchShellKey(k) {
        return TerminalPlace(machine: unkey(agent).machine, pane: agent)
    }
    func on(_ machine: String, project: String? = nil, dir: String? = nil) -> TerminalPlace {
        TerminalPlace(machine: machine.isEmpty ? "local" : machine, project: project, dir: dir)
    }
    if let c = parseChangesKey(k) { return on(c.machine, project: c.project) }
    if let c = parseSetupKey(k) { return on(c.machine, project: c.project, dir: c.dir) }
    if let t = parseTaskKey(k) { return on(t.machine) }
    if isAgentsKey(k) { return on("local") }
    if let c = parseFilesKey(k) ?? parseCommitsKey(k) ?? parseSessionsKey(k) ?? parseMobileKey(k) {
        return on(c.machine, project: c.project)
    }
    return TerminalPlace(machine: unkey(k).machine, pane: k)
}
