import AppKit
import ConchKit
import SwiftUI
import WebKit

// A browser beside the agents: what a dev server on a port serves, a page
// a file links to, docs. One browser pane holds tabs of its own, as any
// browser does — a strip of pages with +, ×, drag to reorder — each its
// own WebKit view. Which tab is in front, where a new one goes and what is
// saved for a relaunch is ConchKit's BrowserTabs; this is WebKit and the
// views around it.

/// One tab's page: its WKWebView and what the chrome shows of it.
@MainActor
final class BrowserPage: NSObject, ObservableObject, WKNavigationDelegate, WKUIDelegate {
    /// Its tab, and the pane it is in: both change when a tab moves to a
    /// pane of its own, page and history with it.
    var id: Int
    weak var owner: BrowserModel?
    let web: FocusingWebView
    @Published var loading = false
    @Published var progress = 0.0
    @Published var canBack = false
    @Published var canForward = false
    /// Said in the page's place: a dev server not up yet, a typo.
    @Published var failed = ""
    /// A certificate the system did not trust: the load stopped, and only
    /// Visit Anyway goes on to this address.
    @Published var certificate: (host: String, url: URL, why: String)?
    /// What went wrong on the page since it last started loading: the
    /// errors chip.
    @Published var issues = PageIssues()
    private var mixedSaid = false
    private let sink = BrowserIssueSink()
    private var watching: [NSKeyValueObservation] = []

    /// A tab's page; `configuration` is WebKit's for a window a page
    /// opened, else the tab's own is made.
    init(id: Int, owner: BrowserModel, configuration given: WKWebViewConfiguration? = nil, private p: Bool, develop: BrowserDevelop) {
        self.id = id
        self.owner = owner
        let configuration = given ?? Self.configuration(private: p)
        prepareContent(configuration, private: p, sink: sink, noImages: develop.disableImages)
        web = FocusingWebView(frame: .zero, configuration: configuration)
        web.customUserAgent = develop.responsive?.userAgent
        web.allowsBackForwardNavigationGestures = true
        web.allowsMagnification = true
        web.isInspectable = true // Develop ▸ Inspect, as in Safari
        super.init()
        sink.page = self
        if develop.disableImages && BrowserNoImages.rules == nil {
            BrowserNoImages.set(true, in: web) { [weak self] in self?.reload() }
        }
        // Clicked into in a split beside an agent: the split has the
        // keyboard now, not the terminal it was taken from.
        web.focused = { [weak self] in
            DispatchQueue.main.async { self?.owner?.tookKeyboard() }
        }
        web.navigationDelegate = self
        web.uiDelegate = self
        watching = [
            web.observe(\.title) { [weak self] w, _ in MainActor.assumeIsolated { self?.owner?.pageTitled(self?.id, w.title ?? "") } },
            web.observe(\.url) { [weak self] w, _ in MainActor.assumeIsolated { self?.moved(w.url) } },
            web.observe(\.isLoading) { [weak self] w, _ in MainActor.assumeIsolated { self?.loadingChanged(w.isLoading) } },
            web.observe(\.estimatedProgress) { [weak self] w, _ in MainActor.assumeIsolated { self?.progress = w.estimatedProgress } },
            web.observe(\.canGoBack) { [weak self] w, _ in MainActor.assumeIsolated { self?.canBack = w.canGoBack } },
            web.observe(\.canGoForward) { [weak self] w, _ in MainActor.assumeIsolated { self?.canForward = w.canGoForward } },
            web.observe(\.hasOnlySecureContent) { [weak self] w, _ in MainActor.assumeIsolated { self?.secureContentChanged(w) } },
        ]
    }

    /// The settings every tab's page starts with: one persistent store
    /// for normal tabs, one that keeps nothing for a private tab. Its
    /// content controller is BrowserContent.swift's: nothing of conch is
    /// in the page (see BrowserSecurity.swift in ConchKit).
    static func configuration(private p: Bool) -> WKWebViewConfiguration {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = BrowserShared.shared.dataStore(private: p)
        config.preferences.isElementFullscreenEnabled = true
        // A window opened by a page only from a click: never from a script
        // on its own (pop-unders, a page opening tabs as it loads).
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.mediaTypesRequiringUserActionForPlayback = []
        return config
    }

    private func moved(_ u: URL?) {
        guard let u, !u.absoluteString.hasPrefix("about:") else { return }
        owner?.pageMoved(id, u.absoluteString)
    }

    /// An https page that loaded something over http: said once a load.
    private func secureContentChanged(_ w: WKWebView) {
        guard !w.hasOnlySecureContent, !mixedSaid, w.url?.scheme?.lowercased() == "https" else { return }
        mixedSaid = true
        issues.add(PageIssue(.mixed, "This https page loaded something over plain http", source: w.url?.absoluteString ?? ""))
    }

    private var isSource: Bool { isViewSource(owner?.tabs.tab(id)?.address ?? "") }
    private var develop: BrowserDevelop { owner?.tabs.tab(id)?.develop ?? BrowserDevelop() }

    private func loadingChanged(_ l: Bool) {
        loading = l
        owner?.busyChanged(id, l)
    }

    /// An address, loaded. A file:// page may read that file and no other.
    func open(_ u: URL) {
        failed = ""
        certificate = nil
        if u.isFileURL { web.loadFileURL(u, allowingReadAccessTo: fileReadAccess(for: u)) } else { web.load(URLRequest(url: u)) }
    }

    /// Visit Anyway: this host's certificate accepted until the app quits,
    /// and the page it stopped at loaded again.
    func visitAnyway() {
        guard let c = certificate else { return }
        BrowserShared.shared.acceptCertificate(host: c.host)
        open(c.url)
    }

    func reload() {
        failed = ""
        if isSource { return } // its text is all there is
        if let c = certificate { open(c.url); return }
        if loading {
            web.stopLoading()
        } else if web.url == nil, let a = owner?.tabs.tab(id)?.address, let u = URL(string: a) {
            open(u)
        } else if develop.disableCache {
            _ = web.reloadFromOrigin()
        } else {
            _ = web.reload()
        }
    }

    /// ⇧⌘R: the page and everything on it from the server, not the cache.
    func hardReload() {
        failed = ""
        if isSource { return }
        if let c = certificate { open(c.url); return }
        _ = web.url == nil ? web.reload() : web.reloadFromOrigin()
    }

    /// The page goes quiet: a tab closed is not left playing.
    func stop() {
        web.stopLoading()
        web.loadHTMLString("", baseURL: nil)
    }

    // MARK: navigation

    nonisolated func webView(_ w: WKWebView, didFailProvisionalNavigation n: WKNavigation!, withError e: Error) {
        MainActor.assumeIsolated { fail(e) }
    }

    nonisolated func webView(_ w: WKWebView, didFail n: WKNavigation!, withError e: Error) {
        MainActor.assumeIsolated { fail(e) }
    }

    nonisolated func webView(_ w: WKWebView, didStartProvisionalNavigation n: WKNavigation!) {
        MainActor.assumeIsolated {
            failed = ""
            certificate = nil
            issues.clear()
            mixedSaid = false
        }
    }

    /// A server's certificate: the system's check, unless Visit Anyway
    /// accepted this host's this session.
    nonisolated func webView(_ w: WKWebView, didReceive challenge: URLAuthenticationChallenge,
                             completionHandler: @escaping @MainActor @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        MainActor.assumeIsolated {
            let space = challenge.protectionSpace
            if space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let trust = space.serverTrust,
               BrowserShared.shared.certs.allows(space.host) {
                completionHandler(.useCredential, URLCredential(trust: trust))
            } else {
                completionHandler(.performDefaultHandling, nil)
            }
        }
    }

    nonisolated func webView(_ w: WKWebView, didFinish n: WKNavigation!) {
        MainActor.assumeIsolated { owner?.fetchIcon(id) }
    }

    /// The page's web process died (out of memory, a crash): said, with
    /// Reload to start it again, rather than a blank page.
    nonisolated func webViewWebContentProcessDidTerminate(_ w: WKWebView) {
        MainActor.assumeIsolated { failed = "This page’s web process ended. Reload to open it again." }
    }

    private func fail(_ e: Error) {
        let ns = e as NSError
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return } // the next load began
        if ns.domain == "WebKitErrorDomain" && ns.code == 102 { return } // a download took it, or a frame load was replaced
        if isCertificateError(domain: ns.domain, code: ns.code), let u = ns.userInfo[NSURLErrorFailingURLErrorKey] as? URL, let h = u.host {
            certificate = (h, u, ns.localizedDescription)
            return
        }
        failed = ns.localizedDescription
        issues.add(PageIssue(.network, ns.localizedDescription, source: (ns.userInfo[NSURLErrorFailingURLErrorKey] as? URL)?.absoluteString ?? ""))
    }

    /// ⌘-click or a middle click on a link: a new tab behind this one
    /// (⇧ too: in front), as browsers do, rather than this page leaving.
    /// The tab's Develop settings apply to each load: JavaScript off, and
    /// with the cache disabled this site's cache is cleared first.
    nonisolated func webView(_ w: WKWebView, decidePolicyFor action: WKNavigationAction, preferences: WKWebpagePreferences,
                             decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy, WKWebpagePreferences) -> Void) {
        MainActor.assumeIsolated {
            let flags = action.modifierFlags
            if action.navigationType == .linkActivated, flags.contains(.command) || action.buttonNumber == 2,
               let u = action.request.url, !u.absoluteString.hasPrefix("javascript:") {
                owner?.openTab(u.absoluteString, after: id, select: flags.contains(.shift))
                decisionHandler(.cancel, preferences)
                return
            }
            let d = develop
            preferences.allowsContentJavaScript = !d.disableJavaScript
            let policy: WKNavigationActionPolicy = action.shouldPerformDownload ? .download : .allow
            guard d.disableCache, action.targetFrame?.isMainFrame == true, let host = action.request.url?.host, !host.isEmpty else {
                decisionHandler(policy, preferences)
                return
            }
            let store = w.configuration.websiteDataStore
            let kinds: Set<String> = [WKWebsiteDataTypeDiskCache, WKWebsiteDataTypeMemoryCache]
            store.fetchDataRecords(ofTypes: kinds) { records in
                let mine = records.filter { siteDataMatches(displayName: $0.displayName, host: host) }
                store.removeData(ofTypes: kinds, for: mine) { decisionHandler(policy, preferences) }
            }
        }
    }

    /// What the page cannot show (an archive, a binary) or is sent as an
    /// attachment is downloaded rather than shown.
    nonisolated func webView(_ w: WKWebView, decidePolicyFor r: WKNavigationResponse, decisionHandler: @escaping @MainActor @Sendable (WKNavigationResponsePolicy) -> Void) {
        MainActor.assumeIsolated {
            if let h = r.response as? HTTPURLResponse,
               let i = PageIssue.http(status: h.statusCode, url: h.url?.absoluteString ?? "", mainFrame: r.isForMainFrame) {
                issues.add(i)
            }
            let attachment = ((r.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Disposition") ?? "").lowercased().hasPrefix("attachment")
            decisionHandler(r.isForMainFrame && (attachment || !r.canShowMIMEType) ? .download : .allow)
        }
    }

    nonisolated func webView(_ w: WKWebView, navigationAction: WKNavigationAction, didBecome d: WKDownload) {
        MainActor.assumeIsolated { BrowserShared.shared.track(d) }
    }

    nonisolated func webView(_ w: WKWebView, navigationResponse: WKNavigationResponse, didBecome d: WKDownload) {
        MainActor.assumeIsolated { BrowserShared.shared.track(d) }
    }

    /// The camera or microphone: asked per site, denied unless allowed.
    nonisolated func webView(_ w: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin, initiatedByFrame frame: WKFrameInfo,
                             type: WKMediaCaptureType, decisionHandler: @escaping @MainActor @Sendable (WKPermissionDecision) -> Void) {
        MainActor.assumeIsolated {
            let o = URL(string: "\(origin.protocol)://\(origin.host)" + (origin.port == 0 ? "" : ":\(origin.port)")).flatMap(permissionOrigin)
            let kinds: [BrowserPermission]
            switch type {
            case .camera: kinds = [.camera]
            case .microphone: kinds = [.microphone]
            case .cameraAndMicrophone: kinds = [.camera, .microphone]
            @unknown default: kinds = []
            }
            decisionHandler(BrowserShared.shared.decideMedia(origin: o, kinds: kinds) ? .grant : .deny)
        }
    }

    /// A link with target=_blank, or window.open from a click: a new tab
    /// here, with the configuration WebKit asks for so the page that
    /// opened it can still talk to it (a sign-in window does).
    nonisolated func webView(_ w: WKWebView, createWebViewWith c: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        MainActor.assumeIsolated {
            owner?.openWindow(configuration: c, from: id, request: action.request, select: !action.modifierFlags.contains(.command))?.web
        }
    }

    /// window.close() from the page: its tab closes.
    nonisolated func webViewDidClose(_ w: WKWebView) {
        MainActor.assumeIsolated { owner?.close(id) }
    }
}

/// A browser pane: its tabs and each one's page.
@MainActor
final class BrowserModel: ObservableObject {
    let key: String
    unowned let store: Store
    @Published var tabs: BrowserTabs
    /// Pages are made when a tab is first shown, so a pane of twenty saved
    /// tabs does not load twenty pages at launch.
    var pages: [Int: BrowserPage] = [:]
    @Published private(set) var busy: Set<Int> = []
    @Published private(set) var icons: [Int: NSImage] = [:]
    /// Bumped to put the cursor in the address bar (⌘L, ⌘T).
    @Published var addressAsked = 0
    @Published var finding = false
    @Published var findText = ""
    @Published var findMissed = false
    @Published var notice = ""
    /// About Browser, asked for from ⋯ or the menu bar.
    @Published var aboutShown = false
    /// The address bar or the find field has the keyboard, so ⌘-keys are
    /// the browser's there too.
    var chromeEditing = false

    init(key: String, store: Store, saved: SavedBrowser? = nil) {
        self.key = key
        self.store = store
        tabs = BrowserTabs(saved: saved, address: parseBrowserKey(key) ?? "")
    }

    /// A pane made around a tab moved out of another, keeping its page.
    init(key: String, store: Store, moving tab: BrowserTab, page: BrowserPage?) {
        self.key = key
        self.store = store
        var t = BrowserTabs(address: "")
        let id = t.tabs[0].id
        t.update(id, address: tab.address, title: tab.title)
        t.setZoom(id, tab.zoom)
        tabs = t
        if let page {
            page.id = id
            page.owner = self
            pages[id] = page
        }
    }

    // What the window's tab and the rest of the app read.
    var current: BrowserPage? { tabs.active.map { page($0.id) } }
    var web: WKWebView? { current?.web }
    var address: String { tabs.active?.address ?? "" }
    var title: String { tabs.active?.title ?? "" }

    func page(_ id: Int) -> BrowserPage {
        if let p = pages[id] { return p }
        let t = tabs.tab(id)
        let p = BrowserPage(id: id, owner: self, private: t?.isPrivate ?? false, develop: t?.develop ?? BrowserDevelop())
        pages[id] = p
        if let t {
            p.web.pageZoom = t.zoom
            if !t.address.isEmpty, !isViewSource(t.address), let u = URL(string: t.address) { p.open(u) }
        }
        return p
    }

    // MARK: tabs

    func newTab() {
        tabs.open("")
        showActive(address: true)
    }

    /// ⇧⌘N: a tab that keeps nothing — no cookies past its life, never
    /// saved, never reopened.
    func newPrivateTab() {
        tabs.open("", isPrivate: true)
        showActive(address: true)
    }

    /// A tab a page opened is private when that page was.
    func openTab(_ address: String, after: Int? = nil, select: Bool = true) {
        let id = tabs.open(address, after: after, select: select, isPrivate: after.flatMap { tabs.tab($0)?.isPrivate } ?? false)
        if select { showActive() } else { _ = page(id) } // a background tab loads now, as browsers do
    }

    /// A tab for a window the page asked for, with WebKit's configuration.
    func openWindow(configuration c: WKWebViewConfiguration, from: Int, request: URLRequest, select: Bool) -> BrowserPage? {
        let id = tabs.open(request.url?.absoluteString ?? "", after: from, select: select, isPrivate: tabs.tab(from)?.isPrivate ?? false)
        let p = BrowserPage(id: id, owner: self, configuration: c, private: tabs.tab(id)?.isPrivate ?? false, develop: BrowserDevelop())
        pages[id] = p
        if select { showActive() }
        return p
    }

    func select(_ id: Int) {
        tabs.select(id)
        showActive()
    }

    /// Closes a tab; the last one closes the browser.
    func close(_ id: Int) {
        pages.removeValue(forKey: id)?.stop()
        busy.remove(id)
        icons.removeValue(forKey: id)
        if tabs.close(id) {
            store.closeBrowser(key)
        } else {
            showActive()
        }
    }

    func closeOthers(_ id: Int) {
        for t in tabs.tabs where t.id != id { pages.removeValue(forKey: t.id)?.stop() }
        tabs.closeOthers(id)
        showActive()
    }

    func reopen() {
        if tabs.reopen() != nil { showActive() }
    }

    func duplicate(_ id: Int) {
        if tabs.duplicate(id) != nil { showActive() }
    }

    func move(_ id: Int, to place: Int) { tabs.move(id, to: place) }

    /// Clear Browsing Data: closed tabs are history too.
    func forgetClosed() { tabs.forgetClosed() }

    /// A tab out into a pane of its own, beside this one, its page and
    /// history going with it.
    func moveToOwnPane(_ id: Int) {
        let page = pages.removeValue(forKey: id)
        guard let t = tabs.take(id) else {
            if let page { pages[id] = page }
            return
        }
        busy.remove(id)
        icons.removeValue(forKey: id)
        store.browserTabToOwnPane(from: key, tab: t, page: page)
        showActive()
    }

    func reload(_ id: Int) { page(id).reload() }

    /// What was typed, loaded in the tab in front; said so when it is no
    /// address.
    func go(_ typed: String) {
        guard let t = tabs.active else { return }
        guard let u = browserURL(typed) else {
            page(t.id).failed = "“\(typed)” is not an address"
            return
        }
        tabs.update(t.id, address: u.absoluteString)
        page(t.id).open(u)
    }

    /// The page in the browser you use.
    func openOutside() {
        if let u = web?.url ?? URL(string: address), !u.absoluteString.hasPrefix("about:") { NSWorkspace.shared.open(u) }
    }

    func zoom(_ z: Double) {
        guard let t = tabs.active else { return }
        tabs.setZoom(t.id, z)
        page(t.id).web.pageZoom = tabs.tab(t.id)?.zoom ?? 1
    }

    func find(back: Bool = false) {
        guard let w = web, !findText.isEmpty else { return }
        let c = WKFindConfiguration()
        c.backwards = back
        c.wraps = true
        w.find(findText, configuration: c) { [weak self] r in self?.findMissed = !r.matchFound }
    }

    /// A key typed while the browser has the keyboard; false leaves it to
    /// whatever else takes it.
    func perform(_ k: BrowserKey) -> Bool {
        switch k {
        case .newTab: newTab()
        case .closeTab: if let t = tabs.active { close(t.id) }
        case .reopenTab: reopen()
        case .privateTab: newPrivateTab()
        case .address: addressAsked += 1
        case .reload: current?.reload()
        case .back: web?.goBack()
        case .forward: web?.goForward()
        case .nextTab: tabs.next(); showActive()
        case .previousTab: tabs.previous(); showActive()
        case .tab(let n): tabs.select(number: n); showActive()
        case .find: finding = true
        case .findNext: if finding { find() } else { finding = true }
        case .findPrevious: if finding { find(back: true) } else { finding = true }
        case .zoomIn: zoom(BrowserZoom.bigger(tabs.active?.zoom ?? 1))
        case .zoomOut: zoom(BrowserZoom.smaller(tabs.active?.zoom ?? 1))
        case .zoomReset: zoom(1)
        case .inspector: inspect()
        case .viewSource: viewSource()
        case .hardReload: hardReload()
        case .responsive: toggleResponsive()
        }
        return true
    }

    func inspect() {
        guard let w = web else { return }
        if !showWebInspector(w) { say("Right-click the page → Inspect Element") }
    }

    func say(_ s: String) {
        notice = s
        DispatchQueue.main.asyncAfter(deadline: .now() + 4) { [weak self] in if self?.notice == s { self?.notice = "" } }
    }

    /// Whether the keyboard is in this browser: one of its pages, or its
    /// address or find field.
    func hasKeyboard(_ r: NSResponder?) -> Bool {
        if chromeEditing { return true }
        var v = r as? NSView
        while let view = v {
            if let w = view as? WKWebView { return pages.values.contains { $0.web === w } }
            v = view.superview
        }
        return false
    }

    /// The tab in front shown, its page given the keyboard — or, a new
    /// tab, the address bar.
    func showActive(address: Bool = false) {
        guard let t = tabs.active else { return }
        let p = page(t.id)
        store.focus(key)
        store.terminalHasKeyboard = false
        if address || t.address.isEmpty {
            addressAsked += 1
        } else {
            DispatchQueue.main.async { if let win = p.web.window, win.firstResponder !== p.web { win.makeFirstResponder(p.web) } }
        }
        store.objectWillChange.send() // the window's tab is named by it
    }

    // MARK: from the pages

    func tookKeyboard() {
        store.terminalHasKeyboard = false
        store.focus(key)
    }

    func pageTitled(_ id: Int?, _ t: String) {
        guard let id else { return }
        tabs.update(id, title: t)
        if id == tabs.activeID { store.objectWillChange.send() }
    }

    func pageMoved(_ id: Int, _ address: String) {
        tabs.update(id, address: address)
        if id == tabs.activeID { store.objectWillChange.send() }
    }

    func busyChanged(_ id: Int, _ on: Bool) {
        if on { busy.insert(id) } else { busy.remove(id) }
    }

    /// The page's icon: what its <link rel=icon> names, else /favicon.ico.
    /// Read in WebKit's own client world, so nothing of conch is put in the
    /// page; fetched without cookies, at most 256 KB.
    func fetchIcon(_ id: Int) {
        guard let p = pages[id], let base = p.web.url, base.scheme == "http" || base.scheme == "https" else { return }
        let js = "(() => { const l = document.querySelector('link[rel~=\"icon\"]'); return l ? l.href : '' })()"
        p.web.evaluateJavaScript(js, in: nil, in: .defaultClient) { [weak self] r in
            var href = (try? r.get()) as? String ?? ""
            if href.isEmpty { href = URL(string: "/favicon.ico", relativeTo: base)?.absoluteString ?? "" }
            guard let u = URL(string: href), u.scheme == "http" || u.scheme == "https" else { return }
            Task { @MainActor in
                guard let (data, _) = try? await URLSession(configuration: .ephemeral).data(from: u), data.count < 256 << 10,
                      let img = NSImage(data: data) else { return }
                self?.icons[id] = img
            }
        }
    }

    /// Every page closed: the browser is going.
    func shutdown() {
        for p in pages.values { p.stop() }
        pages = [:]
    }
}

/// Opens Web Inspector on a page. WebKit has no public call for it —
/// `isInspectable` only lets Safari's Develop menu and the page's own
/// Inspect Element reach it — so this is the private `_inspector`'s
/// `show`, and false when a WebKit no longer has them: the caller says
/// how to open it by hand instead.
@MainActor
func showWebInspector(_ w: WKWebView) -> Bool {
    let get = NSSelectorFromString("_inspector")
    guard w.responds(to: get), let i = w.perform(get)?.takeUnretainedValue() as? NSObject else { return false }
    let show = NSSelectorFromString("show")
    guard i.responds(to: show) else { return false }
    i.perform(show)
    return true
}

/// A web view that says when it takes the keyboard.
final class FocusingWebView: WKWebView {
    var focused: (() -> Void)?
    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        if ok { focused?() }
        return ok
    }
}

// MARK: the views

struct BrowserView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    /// Closes it: out of its workbench, split or tab.
    var close: (() -> Void)? = nil
    /// Moves it out of a workbench or split into a tab of its own; nil when
    /// it is a tab of its own already.
    var ownTab: (() -> Void)? = nil

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            BrowserTabStrip(model: model)
            if let p = model.current {
                BrowserPageView(model: model, page: p, close: close, ownTab: ownTab).id(p.id)
            }
        }
    }
}

/// The tabs along the top: each a favicon, title, spinner and ×; + for a
/// new one. Drag to reorder, middle-click to close, right-click for more.
struct BrowserTabStrip: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @State private var dragging: Int?
    @State private var memoryTip: [Int: String] = [:]

    var body: some View {
        let _ = look
        return HStack(spacing: 2) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 2) {
                    ForEach(model.tabs.tabs) { t in chip(t) }
                }
                .padding(.leading, 6)
            }
            Button { model.newTab() } label: {
                Image(systemName: "plus").font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.text2)
                    .frame(width: 26, height: 26).contentShape(Rectangle())
            }
            .buttonStyle(.plain).help("New Tab (⌘T)").accessibilityLabel("New Tab")
            .padding(.trailing, 6)
        }
        .frame(height: 32)
        .background(Theme.well.opacity(0.6))
    }

    func chip(_ t: BrowserTab) -> some View {
        let on = t.id == model.tabs.activeID
        return HStack(spacing: 6) {
            Group {
                if model.busy.contains(t.id) {
                    ProgressView().controlSize(.mini).scaleEffect(0.7)
                } else if let img = model.icons[t.id] {
                    Image(nsImage: img).resizable().interpolation(.high)
                } else {
                    // The site's first letter, as the mock draws one.
                    let letter = (URL(string: t.address)?.host?.first).map { String($0).uppercased() } ?? "+"
                    Text(letter).font(.system(size: 8.5, weight: .bold)).foregroundStyle(.white)
                        .frame(width: 14, height: 14).background(RoundedRectangle(cornerRadius: 3).fill(Theme.muted))
                }
            }
            .frame(width: 14, height: 14)
            if t.isPrivate { Image(systemName: "eyeglasses").font(.system(size: 10)).foregroundStyle(Theme.muted) }
            Text(t.label).font(.ui(size: 12, weight: on ? .semibold : .regular)).foregroundStyle(Theme.text)
                .lineLimit(1).truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button { model.close(t.id) } label: {
                Image(systemName: "xmark").font(.system(size: 8.5, weight: .bold)).foregroundStyle(Theme.muted)
                    .frame(width: 16, height: 16).contentShape(Rectangle())
            }
            .buttonStyle(.plain).help("Close Tab (⌘W)").accessibilityLabel("Close \(t.label)")
        }
        .padding(.leading, 9).padding(.trailing, 4)
        .frame(minWidth: 90, idealWidth: 180, maxWidth: 190, minHeight: 26, maxHeight: 26)
        .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.window : Color.clear))
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(on ? Theme.line : Color.clear))
        .opacity(dragging == t.id ? 0.5 : 1)
        .contentShape(Rectangle())
        .onTapGesture { model.select(t.id) }
        .overlay(MiddleClick { model.close(t.id) })
        .help(tabTooltip(label: t.label, address: t.address, memory: memoryTip[t.id]))
        // Its memory read as the pointer comes onto it, for the tooltip:
        // nothing is read while nobody looks.
        .onHover { on in if on { memoryTip[t.id] = model.store.browserMemory(pane: model.key, tab: t.id) } }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(on ? [.isButton, .isSelected] : .isButton)
        .contextMenu {
            Button("Reload") { model.reload(t.id) }
            Button("Duplicate") { model.duplicate(t.id) }
            Button("Move to Its Own Pane") { model.moveToOwnPane(t.id) }.disabled(model.tabs.tabs.count < 2)
            Divider()
            Button("Close Tab") { model.close(t.id) }
            Button("Close Other Tabs") { model.closeOthers(t.id) }.disabled(model.tabs.tabs.count < 2)
        }
        .onDrag {
            dragging = t.id
            return NSItemProvider(object: browserTabDrag(model.key, t.id) as NSString)
        }
        .onDrop(of: [.text], delegate: TabReorder(model: model, target: t.id, dragging: $dragging))
    }

}

/// What a browser tab being dragged carries: its pane and its id, so a
/// drop elsewhere (a terminal, another browser) is told apart.
func browserTabDrag(_ pane: String, _ id: Int) -> String { "conch-browser-tab \(id) \(pane)" }

/// A tab dragged over another takes its place in the strip.
struct TabReorder: DropDelegate {
    let model: BrowserModel
    let target: Int
    @Binding var dragging: Int?

    func dropEntered(info: DropInfo) {
        guard let d = dragging, d != target, let to = model.tabs.tabs.firstIndex(where: { $0.id == target }) else { return }
        withAnimation(.easeOut(duration: 0.12)) { model.move(d, to: to) }
    }

    func dropUpdated(info: DropInfo) -> DropProposal? { DropProposal(operation: .move) }

    func performDrop(info: DropInfo) -> Bool {
        let was = dragging
        dragging = nil
        return was != nil
    }
}

/// A middle click on what it covers; every other click goes through.
struct MiddleClick: NSViewRepresentable {
    let action: () -> Void
    final class V: NSView {
        var action: () -> Void = {}
        override func hitTest(_ p: NSPoint) -> NSView? {
            switch NSApp.currentEvent?.type {
            case .otherMouseDown, .otherMouseUp: return super.hitTest(p)
            default: return nil
            }
        }
        override func otherMouseUp(with e: NSEvent) { if e.buttonNumber == 2 { action() } }
    }
    func makeNSView(context: Context) -> V { V() }
    func updateNSView(_ v: V, context: Context) { v.action = action }
}

/// The tab in front: its address bar, the page, find.
struct BrowserPageView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @ObservedObject var page: BrowserPage
    var close: (() -> Void)?
    var ownTab: (() -> Void)?
    @State private var typed = ""
    @FocusState private var editing: Bool
    @FocusState private var findFocus: Bool
    @State private var memoryShown = false
    @State private var downloadsShown = false
    @ObservedObject private var shared = BrowserShared.shared

    var address: String { model.tabs.tab(page.id)?.address ?? "" }
    var zoom: Double { model.tabs.tab(page.id)?.zoom ?? 1 }

    var body: some View {
        let _ = look
        return VStack(spacing: 0) {
            bar
            ZStack(alignment: .top) {
                Rectangle().fill(Theme.line).frame(height: 1)
                if page.loading {
                    GeometryReader { g in
                        Rectangle().fill(Theme.accent).frame(width: g.size.width * max(0.05, page.progress), height: 2)
                    }.frame(height: 2)
                }
            }.frame(height: 2)
            if let t = model.tabs.tab(page.id), let r = t.develop.responsive {
                ResponsiveBar(model: model, tab: t.id, mode: r)
            }
            ZStack(alignment: .top) {
                pageHost
                if let c = page.certificate {
                    certificatePage(c)
                } else if !page.failed.isEmpty {
                    VStack(spacing: 10) {
                        Image(systemName: "wifi.exclamationmark").font(.system(size: 26)).foregroundStyle(Theme.muted)
                        Text(page.failed).foregroundStyle(Theme.text).multilineTextAlignment(.center)
                        Button("Try Again") { model.go(address) }.disabled(address.isEmpty)
                    }
                    .padding(30).frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.window)
                } else if address.isEmpty && !page.loading {
                    VStack(spacing: 8) {
                        Image(systemName: "globe").font(.system(size: 28)).foregroundStyle(Theme.muted)
                        Text("Type an address above — a port like localhost:3000 opens over http.").foregroundStyle(Theme.muted)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.window)
                }
                if !model.notice.isEmpty {
                    Text(model.notice).font(.ui(size: 12)).foregroundStyle(Theme.text)
                        .padding(.horizontal, 12).padding(.vertical, 6)
                        .background(Capsule().fill(Theme.window).shadow(radius: 4)).padding(.top, 10)
                }
            }
            if model.finding { findBar }
        }
        .onAppear {
            typed = address
            if address.isEmpty { askAddress() }
        }
        .onChange(of: address) { _, a in if !editing { typed = a } }
        .onChange(of: model.addressAsked) { _, _ in askAddress() }
        .onChange(of: editing) { _, e in model.chromeEditing = e || findFocus }
        .onChange(of: findFocus) { _, f in model.chromeEditing = f || editing }
        .onChange(of: model.finding) { _, f in if f { DispatchQueue.main.async { findFocus = true } } }
        .sheet(isPresented: $memoryShown) { BrowserMemoryView(store: model.store) { memoryShown = false } }
        .sheet(isPresented: $model.aboutShown) { BrowserAboutView(model: model, page: page) { model.aboutShown = false } }
    }

    /// The page: filling the pane, or at a device's size in Responsive
    /// Design Mode, scrolling when the pane is smaller.
    @ViewBuilder var pageHost: some View {
        if let r = model.tabs.tab(page.id)?.develop.responsive {
            ScrollView([.horizontal, .vertical]) {
                WebHost(web: page.web)
                    .frame(width: CGFloat(r.size.width), height: CGFloat(r.size.height))
                    .overlay(Rectangle().stroke(Theme.line))
                    .padding(16)
                    .frame(maxWidth: .infinity)
            }
            .background(Theme.well)
        } else {
            WebHost(web: page.web)
        }
    }

    /// A certificate error never goes on by itself: Go Back, or an explicit
    /// Visit Anyway for that host, for this session.
    func certificatePage(_ c: (host: String, url: URL, why: String)) -> some View {
        VStack(spacing: 12) {
            Image(systemName: "exclamationmark.lock.fill").font(.system(size: 30)).foregroundStyle(Theme.warn)
            Text("This connection is not private").font(.ui(size: 16, weight: .semibold)).foregroundStyle(Theme.text)
            Text("\(c.host)’s certificate is not trusted: \(c.why) Someone may be pretending to be it.")
                .font(.ui(size: 12.5)).foregroundStyle(Theme.text2).multilineTextAlignment(.center).frame(maxWidth: 440)
            HStack(spacing: 10) {
                Button("Go Back") { if page.canBack { page.web.goBack() } else { page.certificate = nil; page.failed = "Stopped: \(c.host)’s certificate is not trusted." } }
                    .keyboardShortcut(.defaultAction)
                Button("Visit Anyway") { page.visitAnyway() }
                    .help("Trust \(c.host)’s certificate until Conch quits")
            }
        }
        .padding(30).frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.window)
    }

    var security: AddressSecurity {
        let host = URL(string: address)?.host ?? ""
        return addressSecurity(address, bypassed: shared.certs.allows(host))
    }

    /// The lock, Not Secure, a laptop for this machine — before the address.
    @ViewBuilder var securityMark: some View {
        let s = security
        let isPrivate = model.tabs.tab(page.id)?.isPrivate == true
        HStack(spacing: 4) {
            if isPrivate { Image(systemName: "eyeglasses").help("Private tab: keeps nothing") }
            switch s {
            case .secure: Image(systemName: "lock.fill").help("Secure connection (https)")
            case .bypassed: Image(systemName: "lock.slash.fill").foregroundStyle(Theme.warn).help("Certificate not trusted: visited anyway this session")
            case .notSecure: Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Theme.warn).help("Not secure: plain http to another machine")
            case .local: Image(systemName: "laptopcomputer").help("This Mac (http to localhost)")
            case .file: Image(systemName: "doc").help("A file on this Mac: it can read itself only")
            case .none: EmptyView()
            }
            if let l = s.label { Text(l).font(.ui(size: 11)).foregroundStyle(Theme.warn) }
        }
        .font(.system(size: 10.5)).foregroundStyle(Theme.text2)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(s.label ?? (s == .secure ? "Secure" : ""))
    }

    func askAddress() {
        DispatchQueue.main.async {
            editing = true
            model.store.terminalHasKeyboard = false
        }
    }

    var bar: some View {
        HStack(spacing: 6) {
            button("chevron.left", "Back (⌘[)", enabled: page.canBack) { page.web.goBack() }
            button("chevron.right", "Forward (⌘])", enabled: page.canForward) { page.web.goForward() }
            button(page.loading ? "xmark" : "arrow.clockwise", page.loading ? "Stop" : "Reload (⌘R)") { page.reload() }
            HStack(spacing: 6) {
                if !editing { securityMark }
                TextField("Address — localhost:3000, a site, a file:// path", text: $typed)
                    .textFieldStyle(.plain)
                    .font(.ui(size: 12.5, design: .monospaced))
                    .foregroundStyle(Theme.text)
                    .focused($editing)
                    .onSubmit {
                        model.go(typed)
                        editing = false
                        model.showActive()
                    }
                    .onExitCommand { typed = address; editing = false }
                    .accessibilityLabel("Address")
            }
            .padding(.horizontal, 9).frame(height: 26)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(editing ? Theme.accent : Theme.line))
            BrowserErrorsChip(model: model, page: page)
            if abs(zoom - 1) > 0.001 {
                Button(BrowserZoom.label(zoom)) { model.zoom(1) }
                    .buttonStyle(.plain).font(.ui(size: 11)).foregroundStyle(Theme.text2)
                    .help("Zoom — click for actual size (⌘0)")
            }
            if !shared.downloads.isEmpty {
                button(shared.downloads.contains { !$0.done } ? "arrow.down.circle.fill" : "arrow.down.circle", "Downloads") { downloadsShown.toggle() }
            }
            menu.popover(isPresented: $downloadsShown, arrowEdge: .bottom) { DownloadsList() }
            if let close { button("xmark.circle", "Close the browser") { close() } }
        }
        .padding(.horizontal, 10).frame(height: 38)
        // The page takes its own right-clicks, so the bar holds the
        // browser's: a workbench's browser had none, and no way to close.
        .contentShape(Rectangle())
        .contextMenu { items }
        .simultaneousGesture(TapGesture().onEnded { model.store.focus(model.key) })
        .modifier(TabDrop(store: model.store, pane: model.key))
    }

    var menu: some View {
        Menu { items } label: {
            Image(systemName: "ellipsis").font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.text)
                .frame(width: 26, height: 26).contentShape(Rectangle())
        }
        .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
        .help("More").accessibilityLabel("More")
    }

    @ViewBuilder var items: some View {
        Button("New Tab") { model.newTab() }
        Button("New Private Tab") { model.newPrivateTab() }
        Button("Reopen Closed Tab") { model.reopen() }.disabled(model.tabs.closed.isEmpty)
        Divider()
        Button("Reload") { page.reload() }
        Button("Find in Page…") { model.finding = true }
        Menu("Zoom") {
            Button("Zoom In") { model.zoom(BrowserZoom.bigger(zoom)) }
            Button("Zoom Out") { model.zoom(BrowserZoom.smaller(zoom)) }
            Button("Actual Size") { model.zoom(1) }
        }
        BrowserDevelopMenu(model: model)
        Button("Browser Memory…") { memoryShown = true }
        Button("Downloads…") { downloadsShown = true }
        Divider()
        Button("Open in Your Browser") { model.openOutside() }.disabled(address.isEmpty)
        Button("Copy Address") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(address, forType: .string)
        }.disabled(address.isEmpty)
        Divider()
        Button("About Browser…") { model.aboutShown = true }
        if let ownTab { Divider(); Button("Move to Its Own Tab") { ownTab() } }
        if let close { Divider(); Button("Close Browser") { close() } }
    }

    var findBar: some View {
        HStack(spacing: 8) {
            Text("Find").font(.ui(size: 12)).foregroundStyle(Theme.text2)
            TextField("", text: $model.findText)
                .textFieldStyle(.plain).font(.ui(size: 12, design: .monospaced))
                .focused($findFocus)
                .onSubmit { model.find() }
                .onExitCommand { endFind() }
                .onChange(of: model.findText) { _, _ in model.find() }
                .padding(.horizontal, 8).frame(width: 240, height: 24)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(findFocus ? Theme.accent : Theme.line))
                .accessibilityLabel("Find in page")
            if model.findMissed && !model.findText.isEmpty { Text("Not found").font(.ui(size: 12)).foregroundStyle(Theme.muted) }
            button("chevron.left", "Previous Match (⇧⌘G)") { model.find(back: true) }
            button("chevron.right", "Next Match (⌘G)") { model.find() }
            Spacer()
            Button("Done") { endFind() }.buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.text2)
        }
        .padding(.horizontal, 12).frame(height: 36)
        .overlay(Rectangle().fill(Theme.line).frame(height: 1), alignment: .top)
    }

    func endFind() {
        model.finding = false
        model.findMissed = false
        model.showActive()
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

/// A tab's WKWebView, kept by its page so switching tabs keeps the page.
struct WebHost: NSViewRepresentable {
    let web: WKWebView
    func makeNSView(context: Context) -> WKWebView { web }
    func updateNSView(_ v: WKWebView, context: Context) {}
}
