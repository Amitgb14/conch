import AppKit
import ConchKit
import SwiftUI
import WebKit

// A web page in a tab beside the agents: what a dev server on a port
// serves, a page a file links to, docs. WebKit's own view, with an
// address bar, back, forward and reload, and a way out to the browser
// you use — for what wants your extensions, sign-ins or devtools.

@MainActor
final class BrowserModel: NSObject, ObservableObject, WKNavigationDelegate, WKUIDelegate {
    let key: String
    unowned let store: Store
    let web: WKWebView
    /// What the address bar holds: the page's address, or what is typed.
    @Published var address = ""
    @Published var title = ""
    @Published var loading = false
    @Published var progress = 0.0
    @Published var canBack = false
    @Published var canForward = false
    @Published var failed = ""
    private var watching: [NSKeyValueObservation] = []

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        let config = WKWebViewConfiguration()
        config.preferences.isElementFullscreenEnabled = true
        config.mediaTypesRequiringUserActionForPlayback = []
        web = WKWebView(frame: .zero, configuration: config)
        web.allowsBackForwardNavigationGestures = true
        web.allowsMagnification = true
        web.isInspectable = true // Develop ▸ Inspect, as in Safari
        super.init()
        web.navigationDelegate = self
        web.uiDelegate = self
        watching = [
            web.observe(\.title) { [weak self] w, _ in MainActor.assumeIsolated { self?.titled(w.title ?? "") } },
            web.observe(\.url) { [weak self] w, _ in MainActor.assumeIsolated { self?.moved(w.url) } },
            web.observe(\.isLoading) { [weak self] w, _ in MainActor.assumeIsolated { self?.loading = w.isLoading } },
            web.observe(\.estimatedProgress) { [weak self] w, _ in MainActor.assumeIsolated { self?.progress = w.estimatedProgress } },
            web.observe(\.canGoBack) { [weak self] w, _ in MainActor.assumeIsolated { self?.canBack = w.canGoBack } },
            web.observe(\.canGoForward) { [weak self] w, _ in MainActor.assumeIsolated { self?.canForward = w.canGoForward } },
        ]
        if let first = parseBrowserKey(key), !first.hasPrefix("about:blank"), let u = URL(string: first) {
            address = first
            open(u)
        }
    }

    private func titled(_ t: String) {
        title = t
        store.objectWillChange.send() // the tab is named by it
    }

    private func moved(_ u: URL?) {
        guard let u, !u.absoluteString.hasPrefix("about:") else { return }
        address = u.absoluteString
    }

    /// What was typed, loaded; said so when it is no address.
    func go(_ typed: String) {
        guard let u = browserURL(typed) else {
            failed = "“\(typed)” is not an address"
            return
        }
        failed = ""
        address = u.absoluteString
        open(u)
    }

    /// A file's page may read the folder it is in (its CSS, its pictures).
    private func open(_ u: URL) {
        if u.isFileURL { web.loadFileURL(u, allowingReadAccessTo: u.deletingLastPathComponent()) } else { web.load(URLRequest(url: u)) }
    }

    func reload() {
        failed = ""
        if loading { web.stopLoading() } else { _ = web.reload() }
    }

    /// The page in the browser you use.
    func openOutside() {
        if let u = web.url ?? URL(string: address), !u.absoluteString.hasPrefix("about:") { NSWorkspace.shared.open(u) }
    }

    // A dev server not up yet, a typo: said in the page's place, and kept
    // until the next load.
    nonisolated func webView(_ w: WKWebView, didFailProvisionalNavigation n: WKNavigation!, withError e: Error) {
        MainActor.assumeIsolated { fail(e) }
    }

    nonisolated func webView(_ w: WKWebView, didFail n: WKNavigation!, withError e: Error) {
        MainActor.assumeIsolated { fail(e) }
    }

    nonisolated func webView(_ w: WKWebView, didStartProvisionalNavigation n: WKNavigation!) {
        MainActor.assumeIsolated { failed = "" }
    }

    private func fail(_ e: Error) {
        let ns = e as NSError
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return } // the next load began
        failed = ns.localizedDescription
    }

    // A link that opens a window opens here instead, in the same tab.
    nonisolated func webView(_ w: WKWebView, createWebViewWith c: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        MainActor.assumeIsolated {
            if action.targetFrame == nil, let u = action.request.url { w.load(URLRequest(url: u)) }
        }
        return nil
    }
}

struct BrowserView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @State private var typed = ""
    @FocusState private var editing: Bool

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            HStack(spacing: 6) {
                button("chevron.left", "Back", enabled: model.canBack) { model.web.goBack() }
                button("chevron.right", "Forward", enabled: model.canForward) { model.web.goForward() }
                button(model.loading ? "xmark" : "arrow.clockwise", model.loading ? "Stop" : "Reload") { model.reload() }
                TextField("Address — localhost:3000, a site, a file:// path", text: $typed)
                    .textFieldStyle(.plain)
                    .font(.ui(size: 12.5, design: .monospaced))
                    .foregroundStyle(Theme.text)
                    .focused($editing)
                    .onSubmit { model.go(typed); editing = false }
                    .padding(.horizontal, 9).frame(height: 26)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(editing ? Theme.accent : Theme.line))
                    .accessibilityLabel("Address")
                button("safari", "Open in your browser", enabled: !model.address.isEmpty) { model.openOutside() }
            }
            .padding(.horizontal, 10).frame(height: 38)
            ZStack(alignment: .top) {
                Rectangle().fill(Theme.line).frame(height: 1)
                if model.loading {
                    GeometryReader { g in
                        Rectangle().fill(Theme.accent).frame(width: g.size.width * max(0.05, model.progress), height: 2)
                    }.frame(height: 2)
                }
            }.frame(height: 2)
            ZStack {
                WebHost(web: model.web)
                if !model.failed.isEmpty {
                    VStack(spacing: 10) {
                        Image(systemName: "wifi.exclamationmark").font(.system(size: 26)).foregroundStyle(Theme.muted)
                        Text(model.failed).foregroundStyle(Theme.text).multilineTextAlignment(.center)
                        Button("Try Again") { model.go(model.address) }.disabled(model.address.isEmpty)
                    }
                    .padding(30).frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.window)
                } else if model.address.isEmpty && !model.loading {
                    VStack(spacing: 8) {
                        Image(systemName: "globe").font(.system(size: 28)).foregroundStyle(Theme.muted)
                        Text("Type an address above — a port like localhost:3000 opens over http.").foregroundStyle(Theme.muted)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.window)
                }
            }
        }
        .onAppear {
            typed = model.address
            if model.address.isEmpty { DispatchQueue.main.async { editing = true } }
        }
        .onChange(of: model.address) { _, a in if !editing { typed = a } }
    }

    func button(_ symbol: String, _ help: String, enabled: Bool = true, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: 12, weight: .medium))
                .foregroundStyle(enabled ? Theme.text : Theme.muted.opacity(0.5))
                .frame(width: 26, height: 26).contentShape(Rectangle())
        }
        .buttonStyle(.plain).disabled(!enabled).help(help).accessibilityLabel(help)
    }
}

/// The tab's WKWebView, kept by its model so switching tabs keeps the page.
struct WebHost: NSViewRepresentable {
    let web: WKWebView
    func makeNSView(context: Context) -> WKWebView { web }
    func updateNSView(_ v: WKWebView, context: Context) {}
}
