import AppKit
import ConchKit

// Claude Code's artifacts beside its agent: each machine's list
// (artifact.list.v1), kept fresh by artifact.published, and the Artifacts
// tab — a browser pane whose first tab is the list (ArtifactsView.swift)
// and whose others are the artifacts on claude.ai, in the browser's own
// WebKit, signed in once like any site.

/// A publish said in the window's corner, with Open.
struct ArtifactToast: Identifiable, Equatable {
    let id = UUID()
    var machine: String
    var pane: String
    var who: String
    var title: String
    var url: String
    var version: Int
}

extension Store {
    /// Whether a machine's server lists artifacts.
    func listsArtifacts(_ machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(artifactsCapability) == true
    }

    /// A machine's artifacts asked for again. One whose conch is older says
    /// so, and what brings it.
    func loadArtifacts(_ machine: String) {
        guard let c = conns[machine] else { return }
        guard listsArtifacts(machine) else {
            artifactShelf.failed(machine, machine == "local"
                ? "This Mac's conch server is older and cannot list artifacts: reload it (right-click the machine → Reload Server)."
                : "\(machineLabel(machine))'s conch is older and cannot list artifacts yet: conch machine upgrade \(machine)")
            return
        }
        struct All: Encodable {}
        Task {
            do {
                let l = try await c.call("artifact.list", All(), as: ArtifactList.self)
                artifactShelf.load(machine, l.artifacts ?? [])
            } catch {
                artifactShelf.failed(machine, "Could not list the artifacts: \(error.localizedDescription)")
            }
        }
    }

    func machineLabel(_ m: String) -> String { machines.first { $0.id == m }?.label ?? m }

    /// artifact.published: into the list, and said unless its tab is on
    /// screen already showing it; an open tab of it loads the new version.
    func artifactPublished(_ machine: String, _ data: Data) {
        guard let ev = try? conchDecoder().decode(ArtifactPublished.self, from: data) else { return }
        artifactShelf.published(machine, ev)
        let a = ev.artifact
        let who = pane(key(machine, ev.pane))?.displayName ?? ev.agent ?? "An agent"
        var shown = false
        for m in browserViews.values {
            for t in m.tabs.tabs where artifactBase(t.address) == a.url {
                m.pages[t.id]?.reload()
                if t.id == m.tabs.activeID, layout.tab(showing: m.key) == layout.active {
                    shown = true
                    m.say("Reloaded: \(ev.agent ?? "the agent") published version \(a.versions)")
                }
            }
        }
        if shown {
            artifactShelf.opened(machine, a.url)
        } else {
            artifactToast = ArtifactToast(machine: machine, pane: ev.pane, who: who, title: a.title, url: a.url, version: a.versions)
            let id = artifactToast?.id
            DispatchQueue.main.asyncAfter(deadline: .now() + 10) { [weak self] in
                if self?.artifactToast?.id == id { self?.artifactToast = nil }
            }
        }
    }

    /// The pane an Artifacts tab is for when none is named: the focused
    /// agent, else the agent beside the focused browser, else the first
    /// agent in the tab.
    func artifactsPaneHere() -> String? {
        if let f = layout.focused {
            if pane(f)?.agent != nil { return f }
            if let a = parseBrowserKey(f).flatMap(parseArtifactsAddress) { return a }
        }
        if let t = layout.current {
            if let a = t.benchAgent { return a }
            return t.root.panes.first { pane($0)?.agent != nil }
        }
        return nil
    }

    /// An agent's Artifacts tab: shown if it is open, else opened beside
    /// the agent, on its list. nil pane: the one here (artifactsPaneHere).
    @discardableResult
    func showArtifacts(pane given: String? = nil, scope: ArtifactScope? = nil) -> BrowserModel? {
        guard let k = given ?? artifactsPaneHere() else {
            say("Pick an agent first: its artifacts open beside it", .info)
            return nil
        }
        let bk = browserKey(artifactsAddress(pane: k))
        if layout.tab(showing: bk) != nil {
            layout.focus(bk)
        } else {
            if layout.tab(showing: k) != nil { layout.focus(k) }
            openBrowser(artifactsAddress(pane: k), split: layout.tab(showing: k) != nil ? .horizontal : nil)
        }
        terminalHasKeyboard = false
        let m = browser(for: bk)
        m?.showArtifactList()
        if let scope { m?.artifactScope = scope }
        loadArtifacts(unkey(k).machine)
        return m
    }

    /// An artifact in its agent's Artifacts tab; with no agent to put it
    /// beside, in a browser tab of its own.
    func openArtifact(_ url: String, pane given: String? = nil) {
        let base = artifactBase(url)
        let found = artifactShelf.find(base)
        var k = given
        if k.map({ pane($0)?.agent == nil }) ?? true, let f = found, let p = f.artifact.panes?.first {
            k = key(f.machine, p)
        }
        if k == nil || pane(k!)?.agent == nil { k = artifactsPaneHere() }
        if let f = found { artifactShelf.opened(f.machine, base) }
        guard let k, let m = showArtifacts(pane: k) else {
            openBrowser(url, split: .horizontal)
            return
        }
        m.openArtifact(url)
    }

    /// A link ⌘-clicked in a pane: a claude.ai artifact opens in the
    /// Artifacts tab, anything else in the system's browser, as before.
    func openLink(_ url: URL) {
        if isArtifactURL(url.absoluteString) {
            openArtifact(url.absoluteString)
        } else {
            NSWorkspace.shared.open(url)
        }
    }

    /// The toast's Open.
    func openToast(_ t: ArtifactToast) {
        artifactToast = nil
        openArtifact(t.url, pane: key(t.machine, t.pane))
    }
}
