import Foundation

// The browser's security rules.
//
// Threat model. A browser tab shows pages conch does not control — a dev
// server's, docs, any site typed in — inside the app that holds every
// agent's terminal and talks to conch's server. What it guards against:
//
// - A page reaching conch. There is no bridge: nothing a page can call,
//   nothing of conch's tokens or socket in the page's world. The scripts
//   conch runs to read a page (its icon's link, its user agent) run in
//   WebKit's own client world, which the page cannot see. A page is as
//   walled off from conch as from Safari.
// - The one channel back from a page, the errors chip's watcher, is
//   one-way and read-only: a script and a message handler in a content
//   world of their own ("conch-errors"), which the page cannot see or
//   post to; no reply; what arrives is checked as text, cut short and
//   capped (PageIssue.fromScript, PageIssues); none in a private tab
//   (BrowserContent.swift in the app, which alone may set one up).
// - A page's words reaching an agent. Send Errors to Agent pastes what
//   the page wrote into a prompt, so it is labelled as the page's text,
//   stripped of every control character (an ESC could end the paste and
//   press Enter: pasteSafe), and never sent: the person reads it and
//   presses Return. Copy URL as curl and Copy Details carry no cookie,
//   and Copy Details no query string, which can hold a token.
// - A page reading the disk. A file:// page may read that file and no
//   other — not the folder, or a page saved in Downloads could read its
//   neighbours (fileReadAccess).
// - Being fooled about where a page came from. The address bar says
//   https (a lock), plain http to another machine (Not Secure) and http
//   to this one (local, which is what a dev server speaks). A certificate
//   error stops the load and says so; only an explicit Visit Anyway goes
//   on, for that host alone and this session alone (CertExceptions, never
//   saved).
// - A page taking the camera, microphone, location or notifications
//   unasked. Each is denied until the person allows it for that site,
//   and listed in Settings → Browser to revoke. WebKit gives an app no way
//   to ask for location or notifications, so those are always denied.
// - Pop-ups, and downloads that run. window.open works only from a click;
//   a download that could run (an app, an installer, a script) is asked
//   about before it is saved, and every download goes to ~/Downloads under
//   a name that cannot climb out of it or overwrite a file.
// - conch keeping secrets. Logins live in WebKit's own data store (one
//   persistent store for normal tabs, a store that keeps nothing for a
//   private tab); conch never reads, copies or saves cookies or passwords
//   in files of its own, and password autofill is the system's.
//
// Not guarded against: a page's own bugs, a site you choose to trust, or
// WebKit itself — the same as Safari, whose engine this is.

/// What the address bar says of a page.
public enum AddressSecurity: Equatable, Sendable {
    /// https: a lock.
    case secure
    /// https whose certificate was not trusted, shown after Visit Anyway.
    case bypassed
    /// http to another machine.
    case notSecure
    /// http to this machine: a dev server.
    case local
    case file
    case none

    /// The words beside the address; nil where an icon says enough.
    public var label: String? {
        switch self {
        case .notSecure: return "Not Secure"
        case .bypassed: return "Certificate Not Trusted"
        default: return nil
        }
    }
}

/// Whether a host is this machine: localhost (and names under it),
/// 127.0.0.0/8, ::1, or a .local name, which is this network's own.
public func isLocalHost(_ host: String) -> Bool {
    let h = host.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
    if h == "localhost" || h.hasSuffix(".localhost") || h.hasSuffix(".local") || h == "::1" { return true }
    let parts = h.split(separator: ".", omittingEmptySubsequences: false)
    return parts.count == 4 && parts[0] == "127" && parts.allSatisfy { UInt8($0) != nil }
}

/// What the address bar says of an address; `bypassed` when its host's
/// certificate was accepted by Visit Anyway.
public func addressSecurity(_ address: String, bypassed: Bool = false) -> AddressSecurity {
    guard let u = URL(string: address), let scheme = u.scheme?.lowercased() else { return .none }
    switch scheme {
    case "https", "wss": return bypassed ? .bypassed : .secure
    case "http", "ws":
        guard let h = u.host, !h.isEmpty else { return .none }
        return isLocalHost(h) ? .local : .notSecure
    case "file": return .file
    default: return .none
    }
}

/// Whether a failed load was a certificate the system did not trust —
/// expired, not yet valid, signed by nobody it knows — which Visit Anyway
/// may accept. A TLS failure or a client certificate is not.
public func isCertificateError(domain: String, code: Int) -> Bool {
    domain == NSURLErrorDomain && (-1204 ... -1201).contains(code)
}

/// Hosts whose untrusted certificate was accepted with Visit Anyway: that
/// host exactly, for as long as the app runs. Deliberately not Codable.
public struct CertExceptions: Equatable, Sendable {
    public private(set) var hosts: Set<String> = []
    public init() {}
    public mutating func allow(_ host: String) {
        let h = host.lowercased()
        if !h.isEmpty { hosts.insert(h) }
    }
    public mutating func forget(_ host: String) { hosts.remove(host.lowercased()) }
    public func allows(_ host: String) -> Bool { !host.isEmpty && hosts.contains(host.lowercased()) }
}

// MARK: permissions

/// What a page may ask for.
public enum BrowserPermission: String, Codable, CaseIterable, Sendable {
    case camera, microphone, location, notifications

    public var label: String {
        switch self {
        case .camera: return "Camera"
        case .microphone: return "Microphone"
        case .location: return "Location"
        case .notifications: return "Notifications"
        }
    }

    /// Whether WebKit lets an app ask: only for the camera and microphone.
    /// The others are always denied.
    public var askable: Bool { self == .camera || self == .microphone }
}

/// A site's origin for its permissions: scheme, host and port as the
/// address gives them; nil for what is no site (a file, about:).
public func permissionOrigin(_ u: URL) -> String? {
    guard let s = u.scheme?.lowercased(), s == "http" || s == "https", let h = u.host?.lowercased(), !h.isEmpty else { return nil }
    return "\(s)://\(h)" + (u.port.map { ":\($0)" } ?? "")
}

/// Each site's answers: allowed, or denied, by what it asked for. Nothing
/// is allowed that was not answered yes.
public struct SitePermissions: Codable, Equatable, Sendable {
    public struct Site: Equatable, Sendable {
        public var origin: String
        public var decisions: [BrowserPermission: Bool]
    }

    private var sites_: [String: [String: Bool]] = [:]
    private enum CodingKeys: String, CodingKey { case sites_ = "sites" }

    public init() {}

    /// The saved answers; nothing (or anything unreadable) is no answers.
    public static func load(_ data: Data?) -> SitePermissions {
        guard let data, let p = try? JSONDecoder().decode(SitePermissions.self, from: data) else { return SitePermissions() }
        return p
    }

    public func decision(_ origin: String, _ k: BrowserPermission) -> Bool? { sites_[origin]?[k.rawValue] }

    /// Allowed only when answered yes, and only what WebKit lets be asked.
    public func allowed(_ origin: String, _ k: BrowserPermission) -> Bool { k.askable && decision(origin, k) == true }

    public mutating func set(_ origin: String, _ k: BrowserPermission, allow: Bool) {
        sites_[origin, default: [:]][k.rawValue] = allow
    }

    public mutating func revoke(_ origin: String, _ k: BrowserPermission) {
        sites_[origin]?.removeValue(forKey: k.rawValue)
        if sites_[origin]?.isEmpty == true { sites_.removeValue(forKey: origin) }
    }

    /// Every site with an answer, by origin; kinds a later build added are
    /// left out.
    public var sites: [Site] {
        sites_.keys.sorted().compactMap { o in
            var d: [BrowserPermission: Bool] = [:]
            for (k, v) in sites_[o] ?? [:] { if let p = BrowserPermission(rawValue: k) { d[p] = v } }
            return d.isEmpty ? nil : Site(origin: o, decisions: d)
        }
    }
}

// MARK: files and downloads

/// What a file:// page may read: the file itself, never its folder.
public func fileReadAccess(for u: URL) -> URL { u }

/// A downloaded file that could run, or install something, if opened: it
/// is asked about before it is saved.
public func isExecutableDownload(_ name: String) -> Bool {
    let ext = (name as NSString).pathExtension.lowercased()
    let risky: Set<String> = ["app", "pkg", "mpkg", "dmg", "command", "sh", "bash", "zsh", "csh", "tool", "terminal", "workflow",
                              "scpt", "applescript", "jar", "exe", "msi", "bat", "cmd", "ps1", "vbs", "py", "pl", "rb", "js",
                              "webloc", "inetloc", "fileloc", "dylib", "kext", "plugin", "prefpane", "action", "osax", "scr"]
    if risky.contains(ext) { return true }
    // An app zipped up is still an app.
    return name.lowercased().hasSuffix(".app.zip")
}

/// A name a download may be saved under: its last path part only, never
/// hidden, nothing that is not a character of a name.
public func safeDownloadName(_ suggested: String) -> String {
    let last = suggested.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
    var n = String(last.unicodeScalars.filter { $0.value >= 0x20 && $0.value != 0x7f })
    while n.hasPrefix(".") { n.removeFirst() }
    n = n.trimmingCharacters(in: .whitespaces)
    return n.isEmpty ? "download" : n
}

/// The name, or "name (1).ext", "name (2).ext"… — the first not taken.
public func uniqueDownloadName(_ name: String, taken: (String) -> Bool) -> String {
    guard taken(name) else { return name }
    // archive.tar.gz keeps .tar.gz whole.
    var stem = name, ext = ""
    for double in [".tar.gz", ".tar.bz2", ".tar.xz"] where name.lowercased().hasSuffix(double) {
        stem = String(name.dropLast(double.count))
        ext = String(name.suffix(double.count))
    }
    if ext.isEmpty {
        let e = (name as NSString).pathExtension
        if !e.isEmpty {
            stem = (name as NSString).deletingPathExtension
            ext = "." + e
        }
    }
    var i = 1
    while taken("\(stem) (\(i))\(ext)") { i += 1 }
    return "\(stem) (\(i))\(ext)"
}

// MARK: settings

extension SettingsNav {
    /// Settings → Browser: the browser's stored data, site permissions and
    /// certificates accepted this session. The app's own, not config.toml's.
    public static let browser = "browser"

    /// A section's title for one of the app's own (no schema entry).
    public static func title(of id: String) -> String { id == browser ? "Browser" : skillsTitle }
    public static func icon(of id: String) -> String { id == browser ? "globe" : skillsIcon }

    static let browserSearchText = "browser cookies cache history clear browsing data website site sites logins sign in permissions camera microphone location notifications certificate downloads private"
}
