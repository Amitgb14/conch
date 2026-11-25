import AppKit
import ConchKit
import SwiftUI
import WebKit

// The inspector (⌃⌘I): a column at the window's right edge saying where
// the focused split's branch stands — its changes, the notes on it, its
// pull request's checks. The column is a web view showing ConchKit's page
// (Inspector.swift), the first of the hybrid design's web views: SwiftUI
// keeps the window, AppKit the terminals, and the page is sent the view
// as JSON and may only ask for what InspectorIntent lets it. What it
// shows is read by the Changes tab's own model for that branch, so the
// column and the tab never disagree and nothing is asked of the server
// twice.

/// The world the column's script and its handler share: the page's own
/// world runs nothing (its policy allows no script) and cannot see either.
@MainActor let inspectorWorld = WKContentWorld.world(name: inspectorWorldName)

/// The window's one web view for the column, kept for as long as the
/// window: focus moving from a branch to a shell and back would otherwise
/// make a new page each time, and the column would flash. The second of
/// the app's two channels from a page (pagesHaveNoBridgeIntoConch): the
/// page is conch's own, loads nothing, and runs no script of its own.
@MainActor
final class InspectorHost: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
    let web: WKWebView
    /// What to do with what the page asks, for the branch on screen now.
    var onIntent: (InspectorIntent) -> Void = { _ in }
    private var ready = false
    private var wanted: InspectorView?
    private var shown: InspectorView?

    override init() {
        let config = WKWebViewConfiguration()
        // Nothing the page does is kept: no cookies, no storage, no cache.
        config.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: .zero, configuration: config)
        super.init()
        let ucc = config.userContentController
        ucc.addUserScript(WKUserScript(source: inspectorScript, injectionTime: .atDocumentEnd, forMainFrameOnly: true, in: inspectorWorld))
        ucc.add(self, contentWorld: inspectorWorld, name: "conch")
        web.navigationDelegate = self
        // No white page before the first view arrives.
        web.setValue(false, forKey: "drawsBackground")
        web.allowsMagnification = false
        web.loadHTMLString(inspectorPage, baseURL: nil)
    }

    /// The view to show, sent once the page can take it, and only when it
    /// changed: the store publishes far more often than the branch does.
    func show(_ v: InspectorView) {
        wanted = v
        push()
    }

    private func push() {
        guard ready, let v = wanted, v != shown else { return }
        shown = v
        web.evaluateJavaScript(inspectorUpdateScript(v), in: nil, in: inspectorWorld)
    }

    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.world == inspectorWorld, message.frameInfo.isMainFrame, let intent = inspectorIntent(message.body) else { return }
        if intent == .ready {
            ready = true
            shown = nil
            push()
            return
        }
        onIntent(intent)
    }

    /// The page and nothing else: a link it drew opens through an intent,
    /// in the browser, never in the column.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let first = action.navigationType == .other && action.request.url?.absoluteString == "about:blank"
        decisionHandler(first ? .allow : .cancel)
    }

    /// WebKit ended the page's process (memory pressure, a crash): load it
    /// again, and send the view once it says it is ready.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        ready = false
        shown = nil
        webView.loadHTMLString(inspectorPage, baseURL: nil)
    }
}

/// The host's web view in the SwiftUI tree.
struct InspectorWeb: NSViewRepresentable {
    let host: InspectorHost
    let view: InspectorView
    let onIntent: (InspectorIntent) -> Void

    func makeNSView(context: Context) -> WKWebView { host.web }

    func updateNSView(_ web: WKWebView, context: Context) {
        host.onIntent = onIntent
        host.show(view)
    }
}

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
                InspectorWeb(host: store.inspectorHost,
                             view: inspectorEmpty("Focus an agent or a branch to see where it stands.", theme: Theme.current),
                             onIntent: { _ in })
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
        InspectorWeb(host: store.inspectorHost, view: v, onIntent: act)
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
