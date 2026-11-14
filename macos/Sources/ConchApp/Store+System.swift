import AppKit
import ConchKit

/// The System Monitor's reading: system.stats from each machine's server,
/// every two seconds while the window is open — the machine shown in
/// full, the others in summary for their line in the sidebar — and from
/// this computer's alone for the resource monitor's strip.
extension Store {
    func openSystemMonitor() { onOpenSystemMonitor?() }

    /// Starts asking, unless it is already.
    func startSystemPolling() {
        guard systemPoll == nil else { return }
        systemPoll = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                if !self.systemPaused {
                    let shown = self.systemMachine
                    await self.readSystem(shown, summary: false)
                    for m in self.machines.map(\.id) where m != shown {
                        await self.readSystem(m, summary: true)
                    }
                }
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    func stopSystemPolling() {
        systemPoll?.cancel()
        systemPoll = nil
    }

    /// Asks machine m once. A machine whose conch is older than
    /// system.stats.v1, or that is offline, is said so rather than left
    /// blank; a summary keeps the processes of the last full reading, so
    /// a machine shown again has a table at once.
    func readSystem(_ m: String, summary: Bool) async {
        guard let link = machines.first(where: { $0.id == m }), link.online, let c = connection(m) else {
            systemWhy[m] = "not connected"
            return
        }
        guard c.hello?.capabilities?.contains(systemStatsCapability) == true else {
            systemWhy[m] = m == "local"
                ? "this server is from before the System Monitor · reload the server"
                : "conch on \(link.label) is from before the System Monitor · conch machine upgrade \(link.label)"
            return
        }
        let since = summary ? nil : systemHistory[m]?.last
        do {
            var got = try await c.call("system.stats", SystemStatsParams(since: since, summary: summary), as: SystemStats.self)
            if m == "local" { got = got.marking(app: Int(ProcessInfo.processInfo.processIdentifier)) }
            if summary, let was = systemStats[m] { got.processes = was.processes }
            if !got.history.isEmpty { systemHistory[m, default: SystemHistory()].merge(got.history) }
            systemStats[m] = got
            systemWhy[m] = nil
        } catch {
            systemWhy[m] = "\(error)"
        }
    }

    /// Show pane: the pane a process runs in, in the main window.
    func showPaneOf(machine m: String, pane: String) {
        // In the window that holds it, as a notification's click does.
        let k = key(m, pane)
        let s = AppWindows.shared.store(forPane: k) ?? self
        AppWindows.shared.bringForward(s)
        s.open(k)
        s.focusTerminal()
    }
}
