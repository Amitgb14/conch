import AppKit
import ConchKit
import SwiftUI
import WebKit

// The web views Conch.app draws conch-ui's views in (ConchKit's
// WebUI.swift): the inspector, a branch's Changes, the Agents overview.
// The second of the app's two channels from a page (the browser's error
// watcher is the first; pagesHaveNoBridgeIntoConch holds both to their
// rules): the page is conch's own, loads nothing and runs no script of its
// own, and conch-ui runs in a content world apart from it with the one
// message handler, so nothing the page draws can reach the app.

/// The world conch-ui's script and its handler share.
@MainActor let webWorld = WKContentWorld.world(name: webWorldName)

/// One view's web view, kept for as long as what shows it (a window, a
/// Changes tab), so drawing it again — focus moving, a tab switched to —
/// never makes a new page and the view never flashes.
@MainActor
final class ConchUIHost: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
    let kind: WebViewKind
    let web: WKWebView
    /// What to do with a message from the view, as drawn now.
    var onMessage: (Any) -> Void = { _ in }
    private var ready = false
    private var wanted: String?
    private var shown: String?

    init(kind: WebViewKind) {
        self.kind = kind
        let config = WKWebViewConfiguration()
        // Nothing the page does is kept: no cookies, no storage, no cache.
        config.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: .zero, configuration: config)
        super.init()
        let ucc = config.userContentController
        ucc.addUserScript(WKUserScript(source: webBootScript(kind), injectionTime: .atDocumentEnd, forMainFrameOnly: true, in: webWorld))
        ucc.add(self, contentWorld: webWorld, name: "conch")
        web.navigationDelegate = self
        // No white page before the first view arrives.
        web.setValue(false, forKey: "drawsBackground")
        web.allowsMagnification = false
        web.loadHTMLString(webPage, baseURL: nil)
    }

    /// The view's state (webUpdateScript), sent once the page can take it
    /// and only when it changed: the store publishes far more often than
    /// most views do.
    func show(_ script: String) {
        wanted = script
        push()
    }

    private func push() {
        guard ready, let s = wanted, s != shown else { return }
        shown = s
        web.evaluateJavaScript(s, in: nil, in: webWorld)
    }

    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.world == webWorld, message.frameInfo.isMainFrame,
              let m = webMessage(message.body), m.view == kind else { return }
        if m.type == "ready" {
            ready = true
            shown = nil
            push()
        }
        onMessage(message.body)
    }

    /// The page and nothing else: a link a view drew opens through a
    /// message, in the browser, never in the view.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let first = action.navigationType == .other && action.request.url?.absoluteString == "about:blank"
        decisionHandler(first ? .allow : .cancel)
    }

    /// WebKit ended the page's process (memory pressure, a crash): load it
    /// again, and send the state once the view says it is ready.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        ready = false
        shown = nil
        webView.loadHTMLString(webPage, baseURL: nil)
    }
}

/// A host's web view in the SwiftUI tree, given its state and what to do
/// with what it says.
struct ConchUIView: NSViewRepresentable {
    let host: ConchUIHost
    let script: String
    let onMessage: (Any) -> Void

    func makeNSView(context: Context) -> WKWebView { host.web }

    func updateNSView(_ web: WKWebView, context: Context) {
        host.onMessage = onMessage
        host.show(script)
    }
}
