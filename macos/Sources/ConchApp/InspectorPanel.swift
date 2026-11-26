import AppKit
import ConchKit
import SwiftUI
import WebKit

// The inspector (⌃⌘I): a column at the window's right edge saying where
// the focused split's branch stands — its changes, the notes on it, its
// pull request's checks. A conch-ui view in a ConchUIHost (ConchUIHost.swift),
// sent InspectorView and acting only on what InspectorIntent lets it.
// What it shows is read by the Changes tab's own model for that branch,
// so the column and the tab never disagree and nothing is asked of the
// server twice.

/// The column and its rule, while it is open and a server is reached.
struct InspectorColumn: View {
    @ObservedObject var store: Store

    var body: some View {
        if store.inspectorOpen && store.connected {
            Rectangle().fill(Theme.line).frame(width: 1)
            InspectorPanel(store: store)
        }
    }
}

struct InspectorPanel: View {
    @ObservedObject var store: Store

    var body: some View {
        Group {
            if let t = target {
                InspectorBranch(store: store, target: t, model: store.changes(for: t.changes))
            } else {
                ConchUIView(host: store.inspectorHost,
                            script: webUpdateScript(inspectorEmpty("Focus an agent or a branch to see where it stands.", theme: Theme.current)),
                            onMessage: { _ in })
            }
        }
        .frame(width: 300)
        .background(Theme.side)
    }

    /// The focused split's branch.
    private var target: InspectorTarget? {
        guard let f = store.layout.focused else { return nil }
        let machine = unkey(f).machine
        let pane = store.pane(f)
        let project = pane?.projectID.flatMap { store.snapshots[machine]?.project($0) }
        return inspectorTarget(focused: f, pane: pane, project: project)
    }
}

/// The column for one branch, redrawn as its Changes model reads.
private struct InspectorBranch: View {
    @ObservedObject var store: Store
    let target: InspectorTarget
    @ObservedObject var model: ChangesModel

    var body: some View {
        let name = store.snapshots[target.machine]?.project(target.project)?.name ?? target.project
        let v = inspectorView(target, project: target.machine == "local" ? name : "\(name) · \(store.label(target.machine))",
                              branch: model.branchInfo, changes: model.changes, comments: model.comments,
                              error: model.error, theme: Theme.current)
        ConchUIView(host: store.inspectorHost, script: webUpdateScript(v)) { body in
            if let i = inspectorIntent(body) { act(i) }
        }
    }

    private func act(_ intent: InspectorIntent) {
        let project = key(target.machine, target.project)
        switch intent {
        case .ready:
            break
        case .openChanges:
            store.openChanges(project: project, branch: target.branch)
        case .openFile(let path):
            // Only a file the column listed: the page names a path, the
            // branch says whether it is one of its own.
            guard model.changes?.files.contains(where: { $0.path == path }) == true else { return }
            store.openChanges(project: project, branch: target.branch)
            model.chosen = .changes
            model.select(path, committed: false)
        case .resolve(let id):
            guard let c = model.comments.first(where: { $0.id == id }) else { return }
            model.resolve(c)
        case .openURL(let u):
            // Only the pull request's link or one of its checks'.
            let pr = model.branchInfo?.pr
            let listed = ([pr?.url] + (pr?.runs ?? []).map(\.url)).compactMap { $0 }
            guard listed.contains(u.absoluteString) else { return }
            NSWorkspace.shared.open(u)
        }
    }
}
