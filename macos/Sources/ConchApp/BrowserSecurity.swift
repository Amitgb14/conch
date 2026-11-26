import AppKit
import ConchKit
import SwiftUI
import WebKit

// What every browser tab in the app shares: the logins' data store, each
// site's permissions, the certificates accepted this session, and the
// downloads. The rules and the threat model are ConchKit's
// (BrowserSecurity.swift); this is WebKit and AppKit around them.

@MainActor
final class BrowserShared: NSObject, ObservableObject, WKDownloadDelegate {
    static let shared = BrowserShared()
    static let permissionsKey = "conch.browserPermissions"
    /// Added after WebKit's user agent: "Version/26.1 Safari/605.1.15".
    static let applicationName = browserApplicationName(safariVersion: installedSafariVersion(), system: ProcessInfo.processInfo.operatingSystemVersion)

    /// Each site's answers, kept in the app's preferences: which sites may
    /// use the camera or microphone. Never cookies, never passwords.
    @Published private(set) var permissions = SitePermissions.load(UserDefaults.standard.data(forKey: BrowserShared.permissionsKey))
    /// Visit Anyway, for this run of the app only.
    @Published private(set) var certs = CertExceptions()
    @Published private(set) var downloads: [BrowserDownload] = []

    /// The store normal tabs share, so a site signed in stays signed in
    /// across relaunches: WebKit's own, in the app's container — conch
    /// never reads it. A private tab gets one that keeps nothing.
    func dataStore(private p: Bool) -> WKWebsiteDataStore { p ? .nonPersistent() : .default() }

    // MARK: permissions

    func setPermission(_ origin: String, _ k: BrowserPermission, allow: Bool) {
        permissions.set(origin, k, allow: allow)
        savePermissions()
    }

    func revoke(_ origin: String, _ k: BrowserPermission) {
        permissions.revoke(origin, k)
        savePermissions()
    }

    private func savePermissions() {
        UserDefaults.standard.set(try? JSONEncoder().encode(permissions), forKey: Self.permissionsKey)
    }

    /// A page asking for the camera or microphone: allowed only when the
    /// site was allowed it; denied when it was refused; otherwise asked,
    /// and anything but Allow is no.
    func decideMedia(origin: String?, kinds: [BrowserPermission]) -> Bool {
        guard let origin, !kinds.isEmpty else { return false }
        if kinds.allSatisfy({ permissions.allowed(origin, $0) }) { return true }
        if kinds.contains(where: { permissions.decision(origin, $0) == false }) { return false }
        let a = NSAlert()
        let what = kinds.map { $0.label.lowercased() }.joined(separator: " and ")
        a.messageText = "Allow \(origin) to use your \(what)?"
        a.informativeText = "You can take it back in Settings → Browser."
        a.addButton(withTitle: "Allow")
        a.addButton(withTitle: "Don’t Allow")
        let yes = a.runModal() == .alertFirstButtonReturn
        for k in kinds { setPermission(origin, k, allow: yes) }
        return yes
    }

    // MARK: certificates

    func acceptCertificate(host: String) { certs.allow(host) }
    func forgetCertificate(host: String) { certs.forget(host) }

    // MARK: downloads

    func track(_ d: WKDownload) {
        d.delegate = self
    }

    nonisolated func download(_ d: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String,
                              completionHandler: @escaping @MainActor @Sendable (URL?) -> Void) {
        MainActor.assumeIsolated {
            let name = safeDownloadName(suggestedFilename)
            if isExecutableDownload(name) {
                let a = NSAlert()
                a.messageText = "Download “\(name)”?"
                a.informativeText = "It is a kind of file that can run programs on this Mac. Save it only if you trust \(response.url?.host ?? "the site")."
                a.addButton(withTitle: "Download")
                a.addButton(withTitle: "Cancel")
                guard a.runModal() == .alertFirstButtonReturn else {
                    completionHandler(nil)
                    return
                }
            }
            let folder = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
                ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Downloads")
            let unique = uniqueDownloadName(name) { FileManager.default.fileExists(atPath: folder.appendingPathComponent($0).path) }
            let dest = folder.appendingPathComponent(unique)
            downloads.insert(BrowserDownload(download: d, file: dest), at: 0)
            completionHandler(dest)
        }
    }

    nonisolated func downloadDidFinish(_ d: WKDownload) {
        MainActor.assumeIsolated { downloads.first { $0.download === d }?.finish(nil) }
    }

    nonisolated func download(_ d: WKDownload, didFailWithError e: Error, resumeData: Data?) {
        MainActor.assumeIsolated { downloads.first { $0.download === d }?.finish(e) }
    }

    func clearFinishedDownloads() { downloads.removeAll { $0.done } }
}

/// One download: its file, how far it is, and how it ended.
@MainActor
final class BrowserDownload: ObservableObject, Identifiable {
    let id = UUID()
    let download: WKDownload
    let file: URL
    @Published var fraction = 0.0
    @Published var bytes: Int64 = 0
    @Published var total: Int64 = 0
    @Published var done = false
    @Published var failed = ""
    private var watching: NSKeyValueObservation?

    init(download: WKDownload, file: URL) {
        self.download = download
        self.file = file
        watching = download.progress.observe(\.fractionCompleted) { [weak self] p, _ in
            let f = p.fractionCompleted, c = p.completedUnitCount, t = p.totalUnitCount
            DispatchQueue.main.async {
                self?.fraction = f
                self?.bytes = c
                self?.total = t
            }
        }
    }

    func finish(_ e: Error?) {
        done = true
        fraction = e == nil ? 1 : fraction
        if let e { failed = e.localizedDescription }
        watching = nil
    }
}

/// The downloads list: each with its progress, and Show in Finder.
struct DownloadsList: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var shared = BrowserShared.shared

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 10) {
            Text("Downloads").font(.ui(size: 13, weight: .semibold))
            if shared.downloads.isEmpty {
                Text("Nothing downloaded yet.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
            ForEach(shared.downloads) { d in DownloadRow(item: d) }
            HStack {
                Text("Saved to ~/Downloads").font(.ui(size: 11)).foregroundStyle(Theme.text2)
                Spacer()
                if shared.downloads.contains(where: \.done) { Button("Clear") { shared.clearFinishedDownloads() }.controlSize(.small) }
            }
        }
        .padding(14).frame(width: 340)
    }
}

struct DownloadRow: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var item: BrowserDownload

    var body: some View {
        let _ = look
        return HStack(spacing: 10) {
            Text(item.file.pathExtension.uppercased().prefix(4)).font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.text2)
                .frame(width: 30, height: 34).background(RoundedRectangle(cornerRadius: 4).fill(Theme.well))
            VStack(alignment: .leading, spacing: 3) {
                Text(item.file.lastPathComponent).font(.ui(size: 12)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.middle)
                if !item.failed.isEmpty {
                    Text(item.failed).font(.ui(size: 11)).foregroundStyle(Theme.warn).lineLimit(2)
                } else if item.done {
                    Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([item.file]) }
                        .buttonStyle(.link).font(.ui(size: 11))
                } else {
                    ProgressView(value: item.fraction).controlSize(.small)
                    Text(item.total > 0 ? "\(formatFootprint(UInt64(max(0, item.bytes)))) of \(formatFootprint(UInt64(item.total)))" : formatFootprint(UInt64(max(0, item.bytes))))
                        .font(.ui(size: 11)).foregroundStyle(Theme.muted)
                }
            }
            Spacer(minLength: 0)
            if !item.done {
                Button { item.download.cancel(nil) } label: { Image(systemName: "xmark.circle.fill") }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted).help("Stop").accessibilityLabel("Stop downloading \(item.file.lastPathComponent)")
            }
        }
    }
}

/// Settings → Browser: sites with stored data (and clearing it), site
/// permissions to take back, certificates accepted this session.
struct BrowserSettingsPane: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    @ObservedObject var shared = BrowserShared.shared
    @State private var records: [WKWebsiteDataRecord] = []
    @State private var reading = true
    @State private var confirmClear = false

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 22) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Browser").font(.ui(size: 20, weight: .semibold)).foregroundStyle(Theme.text)
                Text("What the browser tabs keep. Logins stay in WebKit’s own store and passwords are the system’s (AutoFill and the Passwords app); conch keeps no cookies or passwords of its own. A private tab (⇧⌘N in a browser) keeps nothing.")
                    .font(.ui(size: 12.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            }
            card("Websites with stored data") {
                if reading {
                    Text("Reading…").foregroundStyle(Theme.muted)
                } else if records.isEmpty {
                    Text("None.").foregroundStyle(Theme.muted)
                }
                ForEach(records, id: \.displayName) { r in
                    HStack {
                        Text(r.displayName).font(.ui(size: 12.5, design: .monospaced)).foregroundStyle(Theme.text)
                        Text(kinds(r)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        Spacer()
                        Button("Remove") { remove([r]) }.controlSize(.small)
                    }
                }
                HStack {
                    Spacer()
                    Button("Clear All Browsing Data…") { confirmClear = true }
                        .help("Cookies, caches and site storage of every site, and the tabs closed recently")
                }
            }
            card("Site permissions") {
                let sites = shared.permissions.sites
                if sites.isEmpty { Text("No site has asked yet. A site is asked before it uses the camera or microphone, and denied unless you allow it.").foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true) }
                ForEach(sites, id: \.origin) { s in
                    ForEach(BrowserPermission.allCases.filter { s.decisions[$0] != nil }, id: \.self) { k in
                        HStack {
                            Text(s.origin).font(.ui(size: 12.5, design: .monospaced)).foregroundStyle(Theme.text)
                            Text("\(k.label): \(s.decisions[k] == true ? "allowed" : "denied")").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                            Spacer()
                            Button(s.decisions[k] == true ? "Revoke" : "Forget") { shared.revoke(s.origin, k) }.controlSize(.small)
                        }
                    }
                }
                Text("Location and notifications are always denied: WebKit gives an app no way to ask for them.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            }
            card("Certificates accepted this session") {
                if shared.certs.hosts.isEmpty { Text("None. Visit Anyway on a certificate error adds the host here until the app quits.").foregroundStyle(Theme.muted) }
                ForEach(shared.certs.hosts.sorted(), id: \.self) { h in
                    HStack {
                        Text(h).font(.ui(size: 12.5, design: .monospaced)).foregroundStyle(Theme.text)
                        Spacer()
                        Button("Forget") { shared.forgetCertificate(host: h) }.controlSize(.small)
                    }
                }
            }
        }
        .frame(maxWidth: 760, alignment: .leading)
        .onAppear(perform: read)
        .confirmationDialog("Clear all browsing data?", isPresented: $confirmClear) {
            Button("Clear All", role: .destructive) { clearAll() }
        } message: {
            Text("Every site’s cookies, caches and storage go, so you are signed out of them, and no closed tab can be reopened.")
        }
    }

    func card(_ title: String, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
            content().font(.ui(size: 12.5))
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.well))
    }

    func kinds(_ r: WKWebsiteDataRecord) -> String {
        var k: [String] = []
        if r.dataTypes.contains(WKWebsiteDataTypeCookies) { k.append("cookies") }
        if r.dataTypes.contains(where: { $0.contains("Cache") }) { k.append("cache") }
        if r.dataTypes.contains(where: { $0.contains("Storage") || $0.contains("IndexedDB") || $0.contains("Database") }) { k.append("storage") }
        return k.joined(separator: ", ")
    }

    func read() {
        reading = true
        WKWebsiteDataStore.default().fetchDataRecords(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes()) { rs in
            records = rs.sorted { $0.displayName < $1.displayName }
            reading = false
        }
    }

    func remove(_ rs: [WKWebsiteDataRecord]) {
        WKWebsiteDataStore.default().removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), for: rs) { read() }
    }

    /// Every site's data, and conch's own history of it — the tabs closed
    /// recently — in every window.
    func clearAll() {
        WKWebsiteDataStore.default().removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), modifiedSince: .distantPast) { read() }
        for s in AppWindows.shared.stores { for m in s.browserViews.values { m.forgetClosed() } }
    }
}
