import Foundation

// ⋯ → About Browser: what the browser is and what this tab is doing, for
// a look or a bug report. Copy Details gives it as text — never a cookie,
// a token or a page's query string.

/// A page's certificate, as the system read it.
public struct BrowserCertificate: Equatable, Sendable {
    public var subject: String
    public var issuer: String?
    public var expires: Date?

    public init(subject: String, issuer: String? = nil, expires: Date? = nil) {
        self.subject = subject
        self.issuer = issuer
        self.expires = expires
    }
}

/// Everything About Browser says.
public struct BrowserDetails: Equatable, Sendable {
    public var webKitVersion: String
    public var userAgent: String
    public var appVersion: String
    public var appBuild: String
    /// The server's conch, "" when unknown.
    public var conchVersion: String
    public var isPrivate: Bool
    /// What the persistent store takes on disk; nil when not known yet.
    public var dataBytes: UInt64?
    public var tabCount: Int
    /// What every tab's web processes use, counted once each.
    public var memory: String
    public var address: String
    public var security: AddressSecurity
    public var certificate: BrowserCertificate?
    public var exceptions: [String]
    public var permissions: [String]
    public var zoom: Double
    public var develop: BrowserDevelop

    public init(webKitVersion: String = "", userAgent: String = "", appVersion: String = "", appBuild: String = "", conchVersion: String = "",
                isPrivate: Bool = false, dataBytes: UInt64? = nil, tabCount: Int = 0, memory: String = "—", address: String = "",
                security: AddressSecurity = .none, certificate: BrowserCertificate? = nil, exceptions: [String] = [], permissions: [String] = [],
                zoom: Double = 1, develop: BrowserDevelop = BrowserDevelop()) {
        self.webKitVersion = webKitVersion
        self.userAgent = userAgent
        self.appVersion = appVersion
        self.appBuild = appBuild
        self.conchVersion = conchVersion
        self.isPrivate = isPrivate
        self.dataBytes = dataBytes
        self.tabCount = tabCount
        self.memory = memory
        self.address = address
        self.security = security
        self.certificate = certificate
        self.exceptions = exceptions
        self.permissions = permissions
        self.zoom = zoom
        self.develop = develop
    }

    /// "https · lock", "http · Not Secure": how the page arrived.
    public var securityText: String {
        let scheme = URL(string: address)?.scheme?.lowercased() ?? ""
        let how: String
        switch security {
        case .secure: how = "secure (lock)"
        case .bypassed: how = "certificate not trusted, visited anyway"
        case .notSecure: how = "Not Secure"
        case .local: how = "this Mac"
        case .file: how = "a file, reading itself only"
        case .none: how = "—"
        }
        return scheme.isEmpty ? how : "\(scheme) · \(how)"
    }

    public var dataStoreText: String {
        if isPrivate { return "Private: keeps nothing" }
        return "Persistent" + (dataBytes.map { " · \(formatFootprint($0)) on disk" } ?? "")
    }

    /// Each line as a label and a value, in the order the sheet shows them.
    public func rows(now: Date = Date()) -> [(String, String)] {
        var r: [(String, String)] = [
            ("WebKit", webKitVersion.isEmpty ? "—" : webKitVersion),
            ("User agent", userAgent.isEmpty ? "—" : userAgent),
            ("Conch app", appBuild.isEmpty || appBuild == appVersion ? appVersion : "\(appVersion) (\(appBuild))"),
            ("conch", conchVersion.isEmpty ? "—" : conchVersion),
            ("Data store", dataStoreText),
            ("Tabs", "\(tabCount) · \(memory) in all"),
            ("Page", address.isEmpty ? "—" : redactedAddress(address)),
            ("Security", securityText),
        ]
        if let c = certificate {
            r.append(("Certificate", c.subject))
            if let i = c.issuer { r.append(("Issued by", i)) }
            if let e = c.expires { r.append(("Expires", certificateExpiry(e, now: now))) }
        }
        if !exceptions.isEmpty { r.append(("Visit Anyway", exceptions.sorted().joined(separator: ", "))) }
        r.append(("Permissions", permissions.isEmpty ? "none granted" : permissions.joined(separator: ", ")))
        r.append(("Zoom", BrowserZoom.label(zoom)))
        r.append(("Develop", develop.inEffect.isEmpty ? "nothing on" : develop.inEffect.joined(separator: "; ")))
        return r
    }

    /// Copy Details: the rows as text for a bug report.
    public func text(now: Date = Date()) -> String {
        pasteSafe(rows(now: now).map { "\($0.0): \($0.1)" }.joined(separator: "\n"))
    }
}

/// "2027-03-04 (in 102 days)", "2026-01-02 (expired)".
public func certificateExpiry(_ d: Date, now: Date = Date()) -> String {
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = TimeZone(identifier: "UTC")
    f.dateFormat = "yyyy-MM-dd"
    let days = Int((d.timeIntervalSince(now) / 86400).rounded(.down))
    if d < now { return f.string(from: d) + " (expired)" }
    return f.string(from: d) + (days == 0 ? " (today)" : " (in \(days) day\(days == 1 ? "" : "s"))")
}

/// Where WebKit keeps an app's persistent store, for its size on disk:
/// its website data, its caches and its cookies' file. Only sizes are
/// read there, never what is in them.
public func browserDataFolders(home: String, bundleID: String) -> [String] {
    guard !bundleID.isEmpty else { return [] }
    let lib = (home as NSString).appendingPathComponent("Library")
    return [
        "\(lib)/WebKit/\(bundleID)",
        "\(lib)/Caches/\(bundleID)/WebKit",
        "\(lib)/HTTPStorages/\(bundleID)",
    ]
}
