import AppKit
import ConchKit
import SwiftUI
import WebKit

// The browser's Develop menu — what each item does to the tab in front —
// and what it draws: the errors chip, Responsive Design Mode's bar, and
// About Browser. The settings, the error list and the details are
// ConchKit's (BrowserDevelop.swift, BrowserAbout.swift).

extension BrowserModel {
    /// The Develop settings of the tab in front.
    var developSettings: BrowserDevelop { tabs.active?.develop ?? BrowserDevelop() }

    /// An item of the Develop menu, on the tab in front.
    func develop(_ i: BrowserDevelopItem) {
        guard let t = tabs.active else { return }
        switch i {
        case .inspector: inspect()
        case .viewSource: viewSource()
        case .hardReload: hardReload()
        case .disableCache, .disableJavaScript, .disableImages, .responsive: setDevelop(t.id, i.toggled(t.develop))
        case .clearSiteData: clearSiteData()
        case .sendErrors: sendErrors()
        case .sendScreenshot: sendScreenshot()
        case .copyCurl: copyCurl()
        case .screenshot: screenshot(full: false)
        case .screenshotFull: screenshot(full: true)
        }
    }

    func hardReload() { current?.hardReload() }
    func toggleResponsive() { develop(.responsive) }

    /// A tab's settings changed: what has to happen now for each — the
    /// user agent and JavaScript take a reload, images a rule and a reload.
    func setDevelop(_ id: Int, _ d: BrowserDevelop) {
        guard let old = tabs.tab(id)?.develop, old != d else { return }
        tabs.setDevelop(id, d)
        guard let p = pages[id] else { return }
        var reload = old.disableJavaScript != d.disableJavaScript
        if old.responsive?.userAgent != d.responsive?.userAgent {
            p.web.customUserAgent = d.responsive?.userAgent
            reload = true
        }
        if old.disableImages != d.disableImages {
            BrowserNoImages.set(d.disableImages, in: p.web) { p.reload() }
        } else if reload {
            p.reload()
        }
    }

    /// ⌥⌘U: the page's source as the server sent it, in a tab of its own
    /// beside it, as plain text nothing runs in.
    func viewSource() {
        guard let t = tabs.active, !isViewSource(t.address), let p = pages[t.id], let u = p.web.url, !u.absoluteString.hasPrefix("about:") else {
            say("No page to show the source of")
            return
        }
        p.web.createWebArchiveData { [weak self] r in
            guard let self else { return }
            guard case .success(let d) = r, let main = webArchiveMainResource(d) else {
                self.say("WebKit gave no source for this page")
                return
            }
            let id = self.tabs.open(viewSourceAddress(u.absoluteString), title: "Source: " + t.label, after: t.id, isPrivate: t.isPrivate)
            let src = BrowserPage(id: id, owner: self, private: t.isPrivate, develop: BrowserDevelop())
            self.pages[id] = src
            src.web.load(main.data, mimeType: "text/plain", characterEncodingName: main.encoding ?? "utf-8", baseURL: URL(string: "about:blank")!)
            self.showActive()
        }
    }

    /// Clear This Site's Data: its cookies, storage and cache, after
    /// asking — signed out of it in every tab, other sites untouched.
    func clearSiteData() {
        guard let p = current, let host = p.web.url?.host ?? URL(string: address)?.host, !host.isEmpty else {
            say("No site to clear")
            return
        }
        let a = NSAlert()
        a.messageText = "Clear \(host)’s data?"
        a.informativeText = "Its cookies, storage and cache go, in every tab, so you will be signed out of it. Other sites keep theirs."
        a.addButton(withTitle: "Clear")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        let store = p.web.configuration.websiteDataStore
        let all = WKWebsiteDataStore.allWebsiteDataTypes()
        store.fetchDataRecords(ofTypes: all) { records in
            let mine = records.filter { siteDataMatches(displayName: $0.displayName, host: host) }
            store.removeData(ofTypes: all, for: mine) { [weak self] in
                self?.say(mine.isEmpty ? "\(host) had no data to clear" : "Cleared \(host)’s data")
                p.reload()
            }
        }
    }

    func copyCurl() {
        guard let c = curlCommand(address) else {
            say("Only an http or https page has a curl")
            return
        }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(c, forType: .string)
        say("Copied — the address alone, without this tab’s cookies")
    }

    /// The page as a PNG: what is on screen, or the whole page (WebKit
    /// draws all of it as one PDF page, turned into pixels here).
    func snapshot(full: Bool, _ done: @escaping @MainActor (Data?) -> Void) {
        guard let p = current else { return done(nil) }
        if full {
            p.web.createPDF(configuration: WKPDFConfiguration()) { r in done((try? r.get()).flatMap(pngFromPDF)) }
        } else {
            p.web.takeSnapshot(with: nil) { img, _ in done(img.flatMap(pngData)) }
        }
    }

    /// Screenshot Page: into Downloads, never over a file there.
    func screenshot(full: Bool) {
        let host = URL(string: address)?.host
        snapshot(full: full) { [weak self] png in
            guard let self else { return }
            guard let png else { return self.say("WebKit could not take the screenshot") }
            let folder = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
                ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Downloads")
            let name = uniqueDownloadName(screenshotName(host: host, at: Date(), full: full)) {
                FileManager.default.fileExists(atPath: folder.appendingPathComponent($0).path)
            }
            do {
                try png.write(to: folder.appendingPathComponent(name), options: .withoutOverwriting)
                self.say("Saved to Downloads: \(name)")
            } catch {
                self.say("Could not save the screenshot: \(error.localizedDescription)")
            }
        }
    }

    /// The agent this tab's errors go to, and what it is called.
    var agent: (key: String, name: String)? {
        guard let k = store.browserAgentKey(for: key, address: address) else { return nil }
        return (k, store.pane(k)?.displayName ?? "the agent")
    }

    /// Send Errors to Agent: the list pasted into its prompt, not sent.
    func sendErrors() {
        guard let a = agent else { return say("No agent beside this browser or serving its page") }
        guard let p = current, !p.issues.isEmpty else { return say("No errors on this page") }
        store.pasteIntoAgent(p.issues.report(address: address), pane: a.key)
        say("In \(a.name)’s prompt — press Return there to send it")
    }

    /// Send Screenshot to Agent: what is on screen saved in conch's temp
    /// folder and its path pasted into the agent's prompt (copied to its
    /// machine first when it is on another), as a dropped file is.
    func sendScreenshot() {
        guard let a = agent else { return say("No agent beside this browser or serving its page") }
        let host = URL(string: address)?.host
        snapshot(full: false) { [weak self] png in
            guard let self else { return }
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("conch-browser", isDirectory: true)
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let f = dir.appendingPathComponent(screenshotName(host: host, at: Date(), full: false))
            guard let png, (try? png.write(to: f)) != nil else { return self.say("WebKit could not take the screenshot") }
            self.store.drop([f.path], on: a.key)
            self.say("Its path is in \(a.name)’s prompt — press Return there to send it")
        }
    }
}

/// A one-page PDF of a whole page as a PNG: at twice its size where that
/// stays under 60 million pixels, smaller for a very long page.
func pngFromPDF(_ d: Data) -> Data? {
    guard let pdf = NSPDFImageRep(data: d) else { return nil }
    let size = pdf.bounds.size
    guard size.width >= 1, size.height >= 1 else { return nil }
    let scale = min(2, (60_000_000 / (size.width * size.height)).squareRoot())
    let w = Int(size.width * scale), h = Int(size.height * scale)
    guard w > 0, h > 0, let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: w, pixelsHigh: h, bitsPerSample: 8, samplesPerPixel: 4,
                                                   hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
    else { return nil }
    rep.size = size
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    NSColor.white.setFill()
    NSRect(origin: .zero, size: size).fill()
    pdf.draw(in: NSRect(origin: .zero, size: size))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])
}

// MARK: views

/// ⋯ → Develop: the same items as the menu bar's Develop menu.
struct BrowserDevelopMenu: View {
    @ObservedObject var model: BrowserModel

    var body: some View {
        let d = model.developSettings
        let agent = model.agent
        Menu("Develop") {
            ForEach(Array(BrowserDevelopItem.menu.enumerated()), id: \.offset) { _, item in
                if let i = item {
                    let title = i.title + (i.key.map { "  \($0.shown)" } ?? "")
                    if i.isToggle {
                        Toggle(title, isOn: Binding(get: { i.isOn(d) }, set: { _ in model.develop(i) }))
                    } else {
                        Button(i.needsAgent && agent != nil ? "\(i.title) (\(agent!.name))" : title) { model.develop(i) }
                            .disabled(i.needsAgent && agent == nil)
                    }
                } else {
                    Divider()
                }
            }
        }
    }
}

/// The errors chip in the address bar: how many things went wrong on the
/// page; a click lists them.
struct BrowserErrorsChip: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @ObservedObject var page: BrowserPage
    @State private var open = false

    var body: some View {
        let _ = look
        let n = page.issues.count
        if n > 0 {
            Button { open.toggle() } label: {
                HStack(spacing: 3) {
                    Image(systemName: "exclamationmark.octagon.fill").font(.system(size: 10))
                    Text(n > 999 ? "999+" : "\(n)").font(.ui(size: 11, weight: .semibold))
                }
                .foregroundStyle(Theme.err)
                .padding(.horizontal, 6).frame(height: 18)
                .background(Capsule().fill(Theme.err.opacity(0.14)))
            }
            .buttonStyle(.plain)
            .help("\(n) error\(n == 1 ? "" : "s") on this page")
            .accessibilityLabel("\(n) errors on this page")
            .popover(isPresented: $open, arrowEdge: .bottom) { list }
        }
    }

    var list: some View {
        let agent = model.agent
        return VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Errors on this page").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                Spacer()
                Button("Clear") { page.issues.clear(); open = false }
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(page.issues.items) { i in
                        VStack(alignment: .leading, spacing: 1) {
                            Text(i.kind.label + (i.count > 1 ? " ×\(i.count)" : "")).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.err)
                            Text(i.message).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text).textSelection(.enabled)
                            if !i.source.isEmpty {
                                Text(i.source + (i.line > 0 ? ":\(i.line)" : "")).font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                                    .lineLimit(1).truncationMode(.middle)
                            }
                        }
                    }
                    if page.issues.dropped > 0 {
                        Text("…and \(page.issues.dropped) more").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 320)
            Text("Uncaught errors, failed loads and mixed content. A page’s own console.error calls are not caught.")
                .font(.ui(size: 10.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            HStack {
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(page.issues.report(address: model.address), forType: .string)
                }
                Spacer()
                Button(agent.map { "Send to \($0.name)" } ?? "Send to Agent") {
                    model.sendErrors()
                    open = false
                }
                .disabled(agent == nil)
                .help(agent == nil ? "No agent beside this browser or serving its page" : "Pasted into its prompt; you press Return")
            }
        }
        .padding(12).frame(width: 420)
    }
}

/// Responsive Design Mode's bar: the device, its size, turned or not, the
/// user agent sent, and its pixel ratio.
struct ResponsiveBar: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    let tab: Int
    let mode: ResponsiveMode
    @State private var typed = ""

    func set(_ m: ResponsiveMode?) {
        guard var d = model.tabs.tab(tab)?.develop else { return }
        d.responsive = m
        model.setDevelop(tab, d)
    }

    var body: some View {
        let _ = look
        let screen = NSScreen.main?.backingScaleFactor ?? 2
        return HStack(spacing: 10) {
            Menu(mode.device == .custom ? "Custom" : mode.device.name) {
                ForEach(ResponsiveDevice.allCases.filter { $0 != .custom }, id: \.self) { dev in
                    Button("\(dev.name)  \(dev.size.width) × \(dev.size.height)") { set(ResponsiveMode(dev, rotated: mode.rotated)) }
                }
                Divider()
                Button("Custom…") { set(.custom(width: mode.size.width, height: mode.size.height)) }
            }
            .fixedSize()
            if mode.device == .custom {
                TextField("W × H", text: $typed)
                    .textFieldStyle(.roundedBorder).font(.ui(size: 11.5, design: .monospaced)).frame(width: 100)
                    .onSubmit {
                        if let s = ResponsiveMode.parse(typed) {
                            var m = ResponsiveMode.custom(width: s.width, height: s.height)
                            m.agent = mode.agent
                            set(m)
                        }
                        typed = mode.sizeLabel
                    }
                    .onAppear { typed = mode.sizeLabel }
            } else {
                Text(mode.sizeLabel).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
            }
            Button {
                var m = mode
                m.rotated.toggle()
                set(m)
            } label: { Image(systemName: "rotate.right") }
                .buttonStyle(.plain).help("Rotate").accessibilityLabel("Rotate")
            Picker("", selection: Binding(get: { mode.agent }, set: { a in var m = mode; m.agent = a; set(m) })) {
                ForEach(BrowserAgentKind.allCases, id: \.self) { Text($0.name).tag($0) }
            }
            .labelsHidden().fixedSize().help("The user agent the page is sent")
            Text(mode.pixelRatioLabel(screen: screen)).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                .help("WebKit draws at this screen’s pixel ratio: a page asking devicePixelRatio is told \(String(format: "%g", Double(screen)))")
            Spacer()
            Button("Done") { set(nil) }.buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.text2)
        }
        .font(.ui(size: 12))
        .padding(.horizontal, 12).frame(height: 32)
        .overlay(Rectangle().fill(Theme.line).frame(height: 1), alignment: .bottom)
    }
}

// MARK: about

/// ⋯ → About Browser: what WebKit and conch this is, the store, the tabs
/// and their memory, and this page's security, permissions and settings.
struct BrowserAboutView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    let page: BrowserPage
    let done: () -> Void
    @State private var details = BrowserDetails()

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 12) {
            Text("About Browser").font(.ui(size: 15, weight: .semibold))
            ScrollView {
                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 7) {
                    ForEach(Array(details.rows().enumerated()), id: \.offset) { _, row in
                        GridRow {
                            Text(row.0).font(.ui(size: 12)).foregroundStyle(Theme.text2).gridColumnAlignment(.trailing)
                            HStack(alignment: .firstTextBaseline, spacing: 6) {
                                Text(row.1).font(.ui(size: 12, design: row.0 == "User agent" || row.0 == "WebKit" ? .monospaced : .default))
                                    .foregroundStyle(Theme.text).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                                if row.0 == "User agent" || row.0 == "WebKit" {
                                    Button { copy(row.1) } label: { Image(systemName: "doc.on.doc") }
                                        .buttonStyle(.plain).foregroundStyle(Theme.muted).help("Copy").accessibilityLabel("Copy \(row.0)")
                                }
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 440)
            HStack {
                Button("Copy Details") { copy(details.text()) }
                    .help("As text for a bug report: no cookies, no tokens, no page query")
                Spacer()
                Button("Done") { done() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 560)
        .onAppear(perform: gather)
    }

    func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }

    /// Read once as the sheet opens: the user agent and the store's size
    /// come later, when WebKit and the disk answer.
    func gather() {
        let tab = model.tabs.tab(page.id)
        let shared = BrowserShared.shared
        let wk = Bundle(for: WKWebView.self)
        let app = Bundle.main
        let address = tab?.address ?? ""
        let u = URL(string: address)
        var d = BrowserDetails()
        d.webKitVersion = [wk.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
                           (wk.object(forInfoDictionaryKey: "CFBundleVersion") as? String).map { "(\($0))" }]
            .compactMap { $0 }.joined(separator: " ")
        d.appVersion = app.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
        d.appBuild = app.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        d.conchVersion = model.store.serverVersion
        d.isPrivate = tab?.isPrivate ?? false
        d.tabCount = model.store.browserViews.values.reduce(0) { $0 + $1.tabs.tabs.count }
        d.memory = formatFootprint(model.store.browserMemory().memory.total)
        d.address = address
        d.security = addressSecurity(address, bypassed: shared.certs.allows(u?.host ?? ""))
        d.certificate = page.web.serverTrust.flatMap(certificate)
        d.exceptions = Array(shared.certs.hosts)
        if let o = u.flatMap(permissionOrigin), let site = shared.permissions.sites.first(where: { $0.origin == o }) {
            d.permissions = site.decisions.sorted { $0.key.rawValue < $1.key.rawValue }.map { "\($0.key.label): \($0.value ? "allowed" : "denied")" }
        }
        d.zoom = tab?.zoom ?? 1
        d.develop = tab?.develop ?? BrowserDevelop()
        d.userAgent = page.web.customUserAgent ?? ""
        details = d
        if d.userAgent.isEmpty {
            page.web.evaluateJavaScript("navigator.userAgent", in: nil, in: .defaultClient) { r in
                if let ua = (try? r.get()) as? String { details.userAgent = ua }
            }
        }
        if !d.isPrivate {
            let folders = browserDataFolders(home: NSHomeDirectory(), bundleID: Bundle.main.bundleIdentifier ?? "")
            Task.detached {
                let bytes = folders.reduce(UInt64(0)) { $0 + folderSize($1) }
                await MainActor.run { details.dataBytes = bytes }
            }
        }
    }
}

/// The leaf certificate of a page's server trust: who it names, who
/// signed it (the next in the chain), and when it runs out.
func certificate(_ trust: SecTrust) -> BrowserCertificate? {
    guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else { return nil }
    let subject = SecCertificateCopySubjectSummary(leaf) as String? ?? "—"
    let issuer = chain.count > 1 ? SecCertificateCopySubjectSummary(chain[1]) as String? : nil
    var expires: Date?
    let keys = [kSecOIDX509V1ValidityNotAfter] as CFArray
    if let values = SecCertificateCopyValues(leaf, keys, nil) as? [String: Any],
       let entry = values[kSecOIDX509V1ValidityNotAfter as String] as? [String: Any],
       let n = entry[kSecPropertyKeyValue as String] as? NSNumber {
        expires = Date(timeIntervalSinceReferenceDate: n.doubleValue)
    }
    return BrowserCertificate(subject: subject, issuer: issuer, expires: expires)
}

/// What a folder's files take on disk; their sizes only, never their
/// contents.
func folderSize(_ path: String) -> UInt64 {
    guard let e = FileManager.default.enumerator(at: URL(fileURLWithPath: path), includingPropertiesForKeys: [.totalFileAllocatedSizeKey, .isRegularFileKey]) else { return 0 }
    var total: UInt64 = 0
    for case let u as URL in e {
        guard let v = try? u.resourceValues(forKeys: [.totalFileAllocatedSizeKey, .isRegularFileKey]), v.isRegularFile == true else { continue }
        total += UInt64(v.totalFileAllocatedSize ?? 0)
    }
    return total
}
