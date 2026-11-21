import AppKit
import ConchKit

// What the browser's Develop menu asks of the window: which agent a tab's
// errors go to, pasting into its prompt, and the menu bar's Develop menu.

extension Store {
    /// The agent a browser's errors and screenshots go to: the one whose
    /// dev server is on the page's port, else the workbench's, else one
    /// beside the browser (ConchKit's browserAgent).
    func browserAgentKey(for browser: String, address: String) -> String? {
        let tab = layout.tab(showing: browser).map { layout.tabs[$0] }
        let beside = Set(tab?.root.panes ?? [])
        var candidates: [BrowserAgentCandidate] = []
        for (m, snap) in snapshots.sorted(by: { $0.key < $1.key }) {
            for p in snap.panes where p.agent != nil {
                let k = key(m, p.id)
                let ports = (p.agent?.jobs ?? []).flatMap { $0.ports ?? [] }
                candidates.append(BrowserAgentCandidate(key: k, ports: ports, local: m == "local", beside: beside.contains(k)))
            }
        }
        return browserAgent(address: address, benchAgent: tab?.benchAgent, candidates: candidates)
    }

    /// Text put in an agent's prompt as a paste, as a file dropped on it
    /// is: never followed by Enter, so the person reads it and sends it.
    func pasteIntoAgent(_ text: String, pane k: String) {
        let (machine, id) = unkey(k)
        let t = pasteSafe(text)
        guard !t.isEmpty, let c = conns[machine] else { return }
        struct Paste: Encodable { var id: String; var text: String; var paste: Bool }
        Task { try? await c.call("pane.send_text", Paste(id: id, text: t, paste: true)) }
    }

    /// The browser the menu bar's Develop menu acts on: the focused one,
    /// while it has the keyboard.
    var browserWithKeyboard: BrowserModel? {
        guard !terminalHasKeyboard, let k = layout.focused, parseBrowserKey(k) != nil, let m = browserViews[k],
              let win = mainWindow, m.hasKeyboard(win.firstResponder) else { return nil }
        return m
    }

    /// The menu bar's Develop menu, and About Browser.
    func browserCommand(_ cmd: String) {
        guard let m = browserWithKeyboard else { return }
        if cmd == "about" {
            m.aboutShown = true
        } else if let i = BrowserDevelopItem(rawValue: cmd) {
            m.develop(i)
        }
    }
}
